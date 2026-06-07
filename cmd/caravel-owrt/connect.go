// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 The PharosVPN Authors

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/PharosVPN/caravel/core/profile"
	"github.com/PharosVPN/caravel/core/vp"
	"github.com/amnezia-vpn/amneziawg-go/device"
	"github.com/amnezia-vpn/amneziawg-go/tun"
	"golang.org/x/term"
)

// ifaceName is the userspace AmneziaWG tun device caravel-owrt creates over
// kmod-tun (the OpenWrt analog of macOS's utun).
const ifaceName = "awg0"

// fileConfig is an inline JSON tunnel config (--config), for testing without a
// full profile.
type fileConfig struct {
	Endpoint        string         `json:"endpoint"`
	ServerPublicKey string         `json:"server_public_key"`
	PrivateKey      string         `json:"private_key"`
	PresharedKey    string         `json:"preshared_key"`
	Address         string         `json:"address"`
	AllowedIPs      []string       `json:"allowed_ips"`
	Keepalive       int            `json:"keepalive"`
	MTU             int            `json:"mtu"`
	Obfuscation     vp.Obfuscation `json:"obfuscation"`
}

// dialSpec is the unified tunnel input both --config and --profile resolve to.
type dialSpec struct {
	cfg     vp.Config
	address string // bare tunnel IP
	mtu     int
	label   string // for logs/state
}

func cmdConnect(args []string) error {
	fs := flag.NewFlagSet("connect", flag.ContinueOnError)
	profileRef := fs.String("profile", "", "a stored profile name, or a path to a .pharos file")
	cfgPath := fs.String("config", "", "a JSON tunnel config (testing)")
	password := fs.String("password", "", "password for a password-mode profile (prompted if omitted)")
	connRef := fs.String("connection", "", "which named connection profile in the bundle (default: the first)")
	nodeID := fs.String("node", "", "which node in the profile to use (default: the entry/first)")
	fullTunnel := fs.Bool("full-tunnel", true, "route all traffic through the tunnel")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if (*profileRef == "") == (*cfgPath == "") {
		return errors.New("give exactly one of --profile or --config")
	}

	var spec dialSpec
	var err error
	if *cfgPath != "" {
		spec, err = specFromConfig(*cfgPath)
	} else {
		spec, err = specFromProfile(*profileRef, *connRef, *nodeID, password)
	}
	if err != nil {
		return err
	}

	if os.Geteuid() != 0 {
		return errors.New("must run as root (tun + routes)")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	tn, err := connect(spec, *fullTunnel)
	if err != nil {
		return err
	}
	defer tn.Close()

	// Record the running tunnel so `caravel-owrt status` (and a LuCI view) can
	// see it; clear it on the way out.
	since := time.Now()
	writeTunnelState := func() {
		rx, tx := tn.stats()
		_ = writeState(State{Profile: spec.label, Iface: tn.iface, Endpoint: spec.cfg.Endpoint,
			PID: os.Getpid(), Since: since, RX: rx, TX: tx})
	}
	writeTunnelState()
	defer clearState()

	// Refresh RX/TX while connected.
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				writeTunnelState()
			}
		}
	}()

	fmt.Printf("caravel-owrt: tunnel up on %s → %s (%s, full-tunnel=%v). Ctrl-C to disconnect.\n",
		tn.iface, spec.cfg.Endpoint, spec.label, *fullTunnel)
	<-ctx.Done()
	fmt.Println("\ncaravel-owrt: disconnecting")
	return nil
}

// specFromConfig builds a dialSpec from an inline JSON config file.
func specFromConfig(path string) (dialSpec, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return dialSpec{}, err
	}
	var fc fileConfig
	if err := json.Unmarshal(raw, &fc); err != nil {
		return dialSpec{}, fmt.Errorf("parse config: %w", err)
	}
	if fc.Address == "" {
		return dialSpec{}, errors.New("config needs an address")
	}
	mtu := fc.MTU
	if mtu == 0 {
		mtu = 1420
	}
	return dialSpec{
		cfg: vp.Config{
			PrivateKey:      fc.PrivateKey,
			ServerPublicKey: fc.ServerPublicKey,
			PresharedKey:    fc.PresharedKey,
			Endpoint:        fc.Endpoint,
			AllowedIPs:      fc.AllowedIPs,
			Keepalive:       fc.Keepalive,
			Obfuscation:     fc.Obfuscation,
		},
		address: fc.Address,
		mtu:     mtu,
		label:   "config",
	}, nil
}

// specFromProfile loads a .pharos profile (from the store by name, or a file
// path) and resolves the chosen node to a dialSpec, prompting for a password if
// one is needed (the interactive CLI path).
func specFromProfile(ref, connRef, nodeID string, password *string) (dialSpec, error) {
	data, err := loadProfileBytes(ref)
	if err != nil {
		return dialSpec{}, err
	}
	spec, err := resolveProfileSpec(data, connRef, nodeID, *password)
	if errors.Is(err, profile.ErrPasswordNeeded) && *password == "" {
		pw, perr := promptPassword(fmt.Sprintf("password for profile %q: ", ref))
		if perr != nil {
			return dialSpec{}, perr
		}
		*password = pw
		spec, err = resolveProfileSpec(data, connRef, nodeID, pw)
	}
	return spec, err
}

// resolveProfileSpec decrypts a .pharos and resolves the chosen node to a
// dialSpec without prompting (the password, if any, is supplied by the caller).
// connRef selects a named connection profile within the bundle (the profiles[]
// model: one entry per controller-issued config); empty picks the first.
func resolveProfileSpec(data []byte, connRef, nodeID, password string) (dialSpec, error) {
	p, err := profile.Parse(data, profile.Options{Password: password})
	if err != nil {
		return dialSpec{}, err
	}
	cp, err := p.Select(connRef)
	if err != nil {
		return dialSpec{}, err
	}
	node, err := cp.Node(nodeID)
	if err != nil {
		return dialSpec{}, err
	}
	tn, err := node.Tunnel()
	if err != nil {
		return dialSpec{}, err
	}
	return dialSpec{
		cfg: vp.Config{
			PrivateKey:      tn.PrivateKey,
			ServerPublicKey: tn.ServerPublicKey,
			PresharedKey:    tn.PresharedKey,
			Endpoint:        tn.Endpoint,
			AllowedIPs:      tn.AllowedIPs,
			Keepalive:       tn.Keepalive,
			Obfuscation:     toVPObfuscation(tn.Obfuscation),
		},
		address: tn.Address,
		mtu:     tn.MTU,
		label:   fmt.Sprintf("%s/%s", p.FleetID, tn.NodeName),
	}, nil
}

// loadProfileBytes resolves a --profile reference: a readable file path, else a
// stored profile name.
func loadProfileBytes(ref string) ([]byte, error) {
	if data, err := os.ReadFile(ref); err == nil {
		return data, nil
	}
	st, err := openStore()
	if err != nil {
		return nil, err
	}
	data, err := st.Raw(ref)
	if errors.Is(err, profile.ErrProfileNotFound) {
		return nil, fmt.Errorf("no profile %q (not a file path, not in %s)", ref, st.Dir())
	}
	return data, err
}

// toVPObfuscation maps a profile obfuscation set to the engine's.
func toVPObfuscation(o profile.Obfuscation) vp.Obfuscation {
	return vp.Obfuscation{
		Jc: o.Jc, Jmin: o.Jmin, Jmax: o.Jmax,
		S1: o.S1, S2: o.S2, S3: o.S3, S4: o.S4,
		H1: o.H1, H2: o.H2, H3: o.H3, H4: o.H4,
		I1: o.I1, I2: o.I2, I3: o.I3, I4: o.I4, I5: o.I5,
	}
}

// promptPassword reads a password from the terminal without echo.
func promptPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	pw, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	return strings.TrimSpace(string(pw)), nil
}

// ───────── tunnel (kmod-tun + ip routing) ─────────

// tunnel is a running tunnel plus the host routes to undo on close.
type tunnel struct {
	vt    *vp.Tunnel
	iface string
	undo  []string // route destinations to `ip route del` on close (LIFO)
}

func connect(spec dialSpec, full bool) (*tunnel, error) {
	dev, err := tun.CreateTUN(ifaceName, spec.mtu)
	if err != nil {
		return nil, fmt.Errorf("create %s (is kmod-tun loaded?): %w", ifaceName, err)
	}
	name, err := dev.Name()
	if err != nil {
		dev.Close()
		return nil, err
	}

	vt, err := vp.Up(spec.cfg, dev, device.LogLevelError)
	if err != nil {
		dev.Close() // vp.Up closes the device on failure, but be safe
		return nil, err
	}

	tn := &tunnel{vt: vt, iface: name}
	if err := tn.configureNetwork(spec, full); err != nil {
		tn.Close()
		return nil, fmt.Errorf("configure network: %w", err)
	}
	return tn, nil
}

// configureNetwork addresses the tun device and, for a full tunnel, pins the
// server endpoint to its current real route and overrides the default with the
// 0.0.0.0/1 + 128.0.0.0/1 split (so reaching the endpoint is preserved while
// everything else flows through the tunnel — the same trick caravel-mac uses,
// expressed in iproute2).
func (t *tunnel) configureNetwork(spec dialSpec, full bool) error {
	if spec.address == "" {
		return errors.New("profile/config has no tunnel address")
	}
	if err := sh("ip", "addr", "add", spec.address+"/32", "dev", t.iface); err != nil {
		return err
	}
	if err := sh("ip", "link", "set", "dev", t.iface, "mtu", strconv.Itoa(spec.mtu), "up"); err != nil {
		return err
	}

	// Decide whether we're stealing the default route. A full tunnel always does;
	// a "split" tunnel whose AllowedIPs include a default (0.0.0.0/0 — what
	// node.Tunnel() fills in when a profile doesn't restrict them) is really a
	// full tunnel too and MUST use the same endpoint-pin + 0.0.0.0/1+128.0.0.0/1
	// split. We never `ip route replace` the host's real default: that would
	// silently clobber the WAN gateway and leave the router with no default route
	// after teardown.
	captureDefault := full
	if !full {
		for _, cidr := range spec.cfg.AllowedIPs {
			if isDefaultRoute(cidr) {
				captureDefault = true
				continue
			}
			if strings.Contains(cidr, ":") {
				continue // IPv6 routing is not handled in this slice (IPv4 only)
			}
			// Non-default subnet: `add` (not `replace`) so a collision with an
			// existing route fails loudly instead of silently displacing it.
			if err := sh("ip", "route", "add", cidr, "dev", t.iface); err != nil {
				return err
			}
			t.undo = append(t.undo, cidr)
		}
	}

	if !captureDefault {
		return nil
	}

	// Pin the server endpoint to whatever route currently reaches it, *before*
	// we steal the default. routeTo handles both an endpoint across the WAN (via
	// a gateway) and one on a directly connected subnet (no gateway — the LAN
	// test rig). Without this pin the encrypted UDP to the node would recurse
	// into the tunnel and the handshake would never complete.
	host, _, err := net.SplitHostPort(spec.cfg.Endpoint)
	if err != nil {
		host = spec.cfg.Endpoint
	}
	ip, err := net.ResolveIPAddr("ip4", host)
	if err != nil {
		return fmt.Errorf("resolve endpoint %q: %w", host, err)
	}
	via, dev, err := routeTo(ip.String())
	if err != nil {
		return err
	}
	pin := []string{"ip", "route", "replace", ip.String() + "/32", "dev", dev}
	if via != "" {
		pin = []string{"ip", "route", "replace", ip.String() + "/32", "via", via, "dev", dev}
	}
	if err := sh(pin...); err != nil {
		return err
	}
	t.undo = append(t.undo, ip.String()+"/32")

	for _, half := range []string{"0.0.0.0/1", "128.0.0.0/1"} {
		if err := sh("ip", "route", "replace", half, "dev", t.iface); err != nil {
			return err
		}
		t.undo = append(t.undo, half)
	}
	return nil
}

// Close tears down routes (in reverse order) and the tunnel device.
func (t *tunnel) Close() error {
	for i := len(t.undo) - 1; i >= 0; i-- {
		_ = sh("ip", "route", "del", t.undo[i])
	}
	t.undo = nil
	if t.vt != nil {
		t.vt.Close()
		t.vt = nil
	}
	return nil
}

// stats returns the tunnel's cumulative RX/TX bytes (0 if unavailable).
func (t *tunnel) stats() (rx, tx int64) {
	if t.vt == nil {
		return 0, 0
	}
	rx, tx, _ = t.vt.Stats()
	return rx, tx
}

// routeTo returns the gateway ("" if directly connected) and the egress device
// the kernel would currently use to reach ip — read before we steal the default.
func routeTo(ip string) (via, dev string, err error) {
	out, err := exec.Command("ip", "route", "get", ip).Output()
	if err != nil {
		return "", "", fmt.Errorf("ip route get %s: %w", ip, err)
	}
	f := strings.Fields(string(out))
	for i := 0; i+1 < len(f); i++ {
		switch f[i] {
		case "via":
			via = f[i+1]
		case "dev":
			dev = f[i+1]
		}
	}
	if dev == "" {
		return "", "", fmt.Errorf("no route to endpoint %s", ip)
	}
	return via, dev, nil
}

// isDefaultRoute reports whether a CIDR is a default route (which must be
// captured via the split trick, never `ip route replace`d onto the real one).
func isDefaultRoute(cidr string) bool {
	return cidr == "0.0.0.0/0" || cidr == "::/0"
}

func sh(args ...string) error {
	cmd := exec.Command(args[0], args[1:]...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
