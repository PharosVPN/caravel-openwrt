// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 The PharosVPN Authors

// Command caravel-owrt is the OpenWrt PharosVPN client. It is a thin platform
// shell around the caravel core: it imports `.pharos` profiles (the format the
// controller exports), brings up a userspace AmneziaWG tunnel over a kmod-tun
// device (amnezia-vpn/amneziawg-go — the *same* engine the macOS/iOS/Android
// clients use), and routes the router's traffic (and, with the firewall config,
// the whole LAN) through it.
//
// It reuses caravel/go's `profile`, `vp`, and `sync` packages unchanged; the
// only OpenWrt-specific part is the tun + `ip` routing in connect.go. See
// docs/integrations/openwrt.md (§3) for the design.
//
// Subcommands:
//
//	caravel-owrt import <file.pharos> [--name NAME]   store a profile
//	caravel-owrt sync   <file.pharosid> [--email E] [--password PW] [--name NAME]
//	caravel-owrt list                                 list stored profiles
//	caravel-owrt rm <name>                            forget a profile
//	caravel-owrt status                               show whether a tunnel is up
//	caravel-owrt connect --profile NAME [--password PW] [--node ID]
//
// Everything runs as root on OpenWrt (the default), which connect needs for the
// tun device and routing changes.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/PharosVPN/caravel/core/deviceid"
	"github.com/PharosVPN/caravel/core/profile"
	csync "github.com/PharosVPN/caravel/core/sync"
	"golang.org/x/term"
)

// version is stamped at build time (see scripts/build.sh).
var version = "dev"

// storeDir is the on-router profile store. uci holds only the *name* of the
// selected profile; the (possibly ciphertext) blobs live here at 0600 — never in
// world-readable uci (design §6).
const storeDir = "/etc/pharos/profiles"

func main() {
	if err := dispatch(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "caravel-owrt:", err)
		os.Exit(1)
	}
}

func dispatch(args []string) error {
	if len(args) == 0 {
		usage()
		return errors.New("a subcommand is required")
	}
	switch args[0] {
	case "connect":
		return cmdConnect(args[1:])
	case "import":
		return cmdImport(args[1:])
	case "sync":
		return cmdSync(args[1:])
	case "list", "ls":
		return cmdList(args[1:])
	case "rm", "remove":
		return cmdRemove(args[1:])
	case "inspect":
		return cmdInspect(args[1:])
	case "status":
		return cmdStatus(args[1:])
	case "version", "-v", "--version":
		fmt.Println("caravel-owrt", version)
		return nil
	case "-h", "--help", "help":
		usage()
		return nil
	default:
		if strings.HasPrefix(args[0], "-") {
			return cmdConnect(args)
		}
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `caravel-owrt — PharosVPN OpenWrt client

  caravel-owrt import <file.pharos> [--name NAME]   store a profile
  caravel-owrt sync <file.pharosid> [--email E] [--password PW] [--name NAME]
                                                    fetch your profile from the controller
  caravel-owrt list [--json]                        list stored profiles
  caravel-owrt rm <name>                            forget a profile
  caravel-owrt inspect --profile NAME [--password PW] [--json]
                                                    show a profile's nodes/path/enc/expiry
  caravel-owrt status [--json]                      show whether a tunnel is up
  caravel-owrt connect --profile NAME [--password PW] [--node ID]
  caravel-owrt connect --config FILE.json           inline JSON config (testing)

connect flags:
  --profile NAME|PATH   a stored profile name, or a path to a .pharos file
  --config PATH         a JSON tunnel config (testing)
  --password PW         password for a password-mode profile (prompted if omitted)
  --connection NAME     which named connection profile in the bundle (default: the first)
  --node ID             which node in the profile to use (default: the entry/first)
  --full-tunnel         route all traffic through the tunnel (default true)
`)
}

// emitJSON writes v to stdout as indented JSON — the shared shape every `--json`
// subcommand (status/list/inspect) emits for the LuCI rpcd backend to consume.
func emitJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// ───────── store ─────────

// openStore opens the on-disk profile store (/etc/pharos/profiles).
func openStore() (*profile.Store, error) {
	return profile.NewStore(storeDir)
}

func cmdImport(args []string) error {
	// Parse <file> + optional --name in any order (the stdlib flag package stops
	// at the first positional, so we scan manually).
	var src, name string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--name" || a == "-name":
			if i+1 >= len(args) {
				return errors.New("--name needs a value")
			}
			name = args[i+1]
			i++
		case strings.HasPrefix(a, "--name="):
			name = strings.TrimPrefix(a, "--name=")
		case !strings.HasPrefix(a, "-") && src == "":
			src = a
		default:
			return fmt.Errorf("unexpected argument %q (usage: caravel-owrt import <file.pharos> [--name NAME])", a)
		}
	}
	if src == "" {
		return errors.New("usage: caravel-owrt import <file.pharos> [--name NAME]")
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	n := name
	if n == "" {
		n = strings.TrimSuffix(filepath.Base(src), profile.Extension)
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	path, err := st.Import(n, data)
	if err != nil {
		return err
	}
	fmt.Printf("imported profile %q → %s\n", n, path)
	return nil
}

// cmdSync fetches the account's end-to-end-encrypted profile from the controller
// (through the relay named in the `.pharosid` bundle), decrypts it on-device, and
// stores it as a connectable enc:none profile. The controller only ever served
// ciphertext.
func cmdSync(args []string) error {
	var src, name, email, password string
	havePW := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		val := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", a)
			}
			i++
			return args[i], nil
		}
		var err error
		switch {
		case a == "--name":
			name, err = val()
		case a == "--email":
			email, err = val()
		case a == "--password":
			password, err = val()
			havePW = true
		case a == "--password-stdin":
			// Read the passphrase from stdin so it never appears in the process
			// table.
			pw, rerr := io.ReadAll(os.Stdin)
			if rerr != nil {
				return fmt.Errorf("read passphrase from stdin: %w", rerr)
			}
			password, havePW = strings.TrimRight(string(pw), "\r\n"), true
		case strings.HasPrefix(a, "--name="):
			name = strings.TrimPrefix(a, "--name=")
		case strings.HasPrefix(a, "--email="):
			email = strings.TrimPrefix(a, "--email=")
		case strings.HasPrefix(a, "--password="):
			password, havePW = strings.TrimPrefix(a, "--password="), true
		case !strings.HasPrefix(a, "-") && src == "":
			src = a
		default:
			return fmt.Errorf("unexpected argument %q (usage: caravel-owrt sync <file.pharosid> [--email E] [--password PW] [--name NAME])", a)
		}
		if err != nil {
			return err
		}
	}
	if src == "" {
		return errors.New("usage: caravel-owrt sync <file.pharosid> [--email E] [--password PW] [--name NAME]")
	}

	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	bundle, err := deviceid.Parse(data)
	if err != nil {
		return err
	}
	// Email is optional: with none, sync authenticates by the device's leaf
	// (cert-auth) and the passphrase never leaves this box.
	who := bundle.User
	if email != "" {
		who = email
	}
	if who == "" {
		who = "your account"
	}
	if !havePW {
		fmt.Fprintf(os.Stderr, "account passphrase for %s: ", who)
		pw, err := term.ReadPassword(int(syscall.Stdin))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return fmt.Errorf("read passphrase: %w", err)
		}
		password = string(pw)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	res, err := csync.Fetch(ctx, bundle, email, password)
	if errors.Is(err, csync.ErrNoProfile) {
		return fmt.Errorf("signed in as %s, but no profile has been issued for this account yet", who)
	}
	if err != nil {
		return err
	}

	env, err := profile.WrapPlaintext(res.Plaintext)
	if err != nil {
		return err
	}
	if name == "" {
		if bundle.Alias != "" {
			name = syncProfileName(bundle.Alias)
		} else {
			name = syncProfileName(email)
		}
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	path, err := st.Import(name, env)
	if err != nil {
		return err
	}
	// Stash the bundle next to the profile so a later refresh needs no re-import.
	marker, _ := json.Marshal(map[string]any{"user": email, "revision": res.Revision, "relay": bundle.RelayAddr})
	_ = os.WriteFile(filepath.Join(st.Dir(), name+".synced"), marker, 0o600)
	_ = os.WriteFile(filepath.Join(st.Dir(), name+deviceid.Extension), data, 0o600)

	var summary struct {
		Nodes []struct {
			Name   string `json:"name"`
			Region string `json:"region"`
		} `json:"nodes"`
	}
	_ = json.Unmarshal(res.Plaintext, &summary)
	fmt.Printf("synced profile %q (rev %d, %d node(s)) → %s\n", name, res.Revision, len(summary.Nodes), path)
	for _, n := range summary.Nodes {
		fmt.Printf("  · %s (%s)\n", n.Name, n.Region)
	}
	fmt.Printf("connect with:  caravel-owrt connect --profile %s\n", name)
	return nil
}

// syncProfileName derives a stable store name from an account email/alias.
func syncProfileName(email string) string {
	n := email
	if at := strings.IndexByte(n, '@'); at > 0 {
		n = n[:at]
	}
	n = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, n)
	if n == "" {
		n = "account"
	}
	return n
}

func cmdList(args []string) error {
	jsonOut := false
	for _, a := range args {
		if a == "--json" {
			jsonOut = true
		}
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	entries, err := st.List()
	if err != nil {
		return err
	}
	if jsonOut {
		// Stable JSON shape for the LuCI rpcd backend: {"profiles":[{name,enc,synced}]}.
		type listEntry struct {
			Name   string `json:"name"`
			Enc    string `json:"enc"`
			Synced bool   `json:"synced"`
		}
		out := struct {
			Dir      string      `json:"dir"`
			Profiles []listEntry `json:"profiles"`
		}{Dir: st.Dir(), Profiles: make([]listEntry, 0, len(entries))}
		for _, e := range entries {
			// A profile fetched by `sync` leaves a <name>.synced marker so the UI
			// can offer "Sync now" instead of "Upload" for account-mode entries.
			_, serr := os.Stat(filepath.Join(st.Dir(), e.Name+".synced"))
			out.Profiles = append(out.Profiles, listEntry{Name: e.Name, Enc: e.Enc, Synced: serr == nil})
		}
		return emitJSON(out)
	}
	if len(entries) == 0 {
		fmt.Printf("no profiles in %s — import one with `caravel-owrt import <file.pharos>`\n", st.Dir())
		return nil
	}
	fmt.Printf("profiles in %s:\n", st.Dir())
	for _, e := range entries {
		fmt.Printf("  %-24s  (%s)\n", e.Name, e.Enc)
	}
	return nil
}

// cmdInspect decrypts a stored profile (or a .pharos file path) and emits its
// human-meaningful shape — the named connection profiles, each profile's nodes
// and (for a cascade) its egress path, plus enc mode / expiry — so the LuCI
// Connection view can populate node/exit pickers without re-implementing the
// crypto in JS/ucode. All decryption stays here in Go.
func cmdInspect(args []string) error {
	var ref, password string
	jsonOut := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		val := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", a)
			}
			i++
			return args[i], nil
		}
		var err error
		switch {
		case a == "--profile":
			ref, err = val()
		case a == "--password":
			password, err = val()
		case a == "--password-stdin":
			pw, rerr := io.ReadAll(os.Stdin)
			if rerr != nil {
				return fmt.Errorf("read password from stdin: %w", rerr)
			}
			password = strings.TrimRight(string(pw), "\r\n")
		case a == "--json":
			jsonOut = true
		case strings.HasPrefix(a, "--profile="):
			ref = strings.TrimPrefix(a, "--profile=")
		case strings.HasPrefix(a, "--password="):
			password = strings.TrimPrefix(a, "--password=")
		case !strings.HasPrefix(a, "-") && ref == "":
			ref = a
		default:
			return fmt.Errorf("unexpected argument %q (usage: caravel-owrt inspect --profile NAME [--password PW] [--json])", a)
		}
		if err != nil {
			return err
		}
	}
	if ref == "" {
		return errors.New("usage: caravel-owrt inspect --profile NAME [--password PW] [--json]")
	}

	data, err := loadProfileBytes(ref)
	if err != nil {
		return err
	}
	enc := profileEnc(data)
	p, err := profile.Parse(data, profile.Options{Password: password})
	if err != nil {
		// Surface the "needs a password" case as a typed JSON result so the UI can
		// prompt, rather than a generic error.
		if jsonOut && errors.Is(err, profile.ErrPasswordNeeded) {
			return emitJSON(inspectOut{Name: ref, Enc: enc, NeedsPassword: true})
		}
		return err
	}

	out := inspectOut{
		Name:     ref,
		Enc:      enc,
		FleetID:  p.FleetID,
		User:     p.User,
		Revision: p.Revision,
	}
	if !p.ExpiresAt.IsZero() {
		out.ExpiresAt = p.ExpiresAt.Format(time.RFC3339)
	}
	for i := range p.Profiles {
		cp := &p.Profiles[i]
		ip := inspectProfile{ID: cp.ID, Name: cp.Name, Protocol: cp.Protocol, EntryNodeID: cp.EntryNodeID()}
		for _, n := range cp.Nodes {
			ip.Nodes = append(ip.Nodes, inspectNode{ID: n.ID, Name: n.Name, Region: n.Region})
		}
		if cp.Path != nil {
			ip.Path = &inspectPath{Name: cp.Path.Name}
			for _, h := range cp.Path.Hops {
				ip.Path.Hops = append(ip.Path.Hops, inspectHop{ID: h.ID, Name: h.Name, Region: h.Region, Role: h.Role})
			}
		}
		out.Profiles = append(out.Profiles, ip)
	}

	if jsonOut {
		return emitJSON(out)
	}
	fmt.Printf("profile %q (enc:%s", ref, enc)
	if out.ExpiresAt != "" {
		fmt.Printf(", expires %s", out.ExpiresAt)
	}
	fmt.Printf(")\n")
	for _, ip := range out.Profiles {
		fmt.Printf("  · %s [%s]\n", ip.Name, ip.Protocol)
		for _, n := range ip.Nodes {
			fmt.Printf("      - %s (%s) %s\n", n.Name, n.Region, n.ID)
		}
		if ip.Path != nil {
			hops := make([]string, len(ip.Path.Hops))
			for i, h := range ip.Path.Hops {
				hops[i] = h.Name
			}
			fmt.Printf("      path: %s\n", strings.Join(hops, " → "))
		}
	}
	return nil
}

// inspectOut and friends are the stable JSON shape `inspect --json` emits.
type inspectOut struct {
	Name          string           `json:"name"`
	Enc           string           `json:"enc"`
	NeedsPassword bool             `json:"needs_password,omitempty"`
	FleetID       string           `json:"fleet_id,omitempty"`
	User          string           `json:"user,omitempty"`
	Revision      int64            `json:"revision,omitempty"`
	ExpiresAt     string           `json:"expires_at,omitempty"`
	Profiles      []inspectProfile `json:"profiles,omitempty"`
}

type inspectProfile struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Protocol    string        `json:"protocol"`
	EntryNodeID string        `json:"entry_node_id,omitempty"`
	Nodes       []inspectNode `json:"nodes,omitempty"`
	Path        *inspectPath  `json:"path,omitempty"`
}

type inspectNode struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Region string `json:"region,omitempty"`
}

type inspectPath struct {
	Name string       `json:"name,omitempty"`
	Hops []inspectHop `json:"hops,omitempty"`
}

type inspectHop struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Region string `json:"region,omitempty"`
	Role   string `json:"role,omitempty"`
}

// profileEnc reads just the always-readable `enc` header from a .pharos blob.
func profileEnc(data []byte) string {
	var h struct {
		Fmt string `json:"fmt"`
		Enc string `json:"enc"`
	}
	if err := json.Unmarshal(data, &h); err != nil {
		return "?"
	}
	if h.Enc == "" {
		return "?"
	}
	return h.Enc
}

func cmdRemove(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: caravel-owrt rm <name>")
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	if err := st.Remove(args[0]); err != nil {
		return err
	}
	fmt.Printf("removed profile %q\n", args[0])
	return nil
}
