// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 The PharosVPN Authors

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// sharedStateFile is where the connect worker records the running tunnel so
// `caravel-owrt status` (and, later, a LuCI view via rpcd) can read it. It lives
// on tmpfs (/var/run) and holds no secrets.
const sharedStateFile = "/var/run/pharosvpn/state.json"

// State is the running-tunnel record written while connected.
type State struct {
	Profile  string    `json:"profile"`
	Iface    string    `json:"iface"`
	Endpoint string    `json:"endpoint"`
	PID      int       `json:"pid"`
	Since    time.Time `json:"since"`
	RX       int64     `json:"rx"` // cumulative received bytes
	TX       int64     `json:"tx"` // cumulative transmitted bytes
}

// writeState records the running tunnel at the shared path.
func writeState(s State) error {
	if err := os.MkdirAll(filepath.Dir(sharedStateFile), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(sharedStateFile, data, 0o644)
}

// clearState removes the running-tunnel record.
func clearState() {
	_ = os.Remove(sharedStateFile)
}

// readState returns the recorded tunnel state, or (zero, false) if none is
// recorded or the recorded worker is no longer alive (a stale record).
func readState() (State, bool) {
	data, err := os.ReadFile(sharedStateFile)
	if err != nil {
		return State{}, false
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, false
	}
	if s.PID > 0 && !processAlive(s.PID) {
		return State{}, false
	}
	return s, true
}

// processAlive reports whether a PID names a live process (signal 0 probe).
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

func cmdStatus(args []string) error {
	jsonOut := false
	for _, a := range args {
		if a == "--json" {
			jsonOut = true
		}
	}
	s, ok := readState()
	if jsonOut {
		// A stable shape the LuCI rpcd backend consumes without screen-scraping.
		out := struct {
			Up       bool   `json:"up"`
			Profile  string `json:"profile,omitempty"`
			Iface    string `json:"iface,omitempty"`
			Endpoint string `json:"endpoint,omitempty"`
			RX       int64  `json:"rx"`
			TX       int64  `json:"tx"`
			Since    string `json:"since,omitempty"`
			PID      int    `json:"pid,omitempty"`
		}{Up: ok}
		if ok {
			out.Profile, out.Iface, out.Endpoint = s.Profile, s.Iface, s.Endpoint
			out.RX, out.TX, out.PID = s.RX, s.TX, s.PID
			out.Since = s.Since.Format(time.RFC3339)
		}
		return emitJSON(out)
	}
	if !ok {
		fmt.Println("disconnected")
		return nil
	}
	fmt.Printf("connected — profile %q on %s → %s (since %s)  ↓%s ↑%s\n",
		s.Profile, s.Iface, s.Endpoint, s.Since.Format(time.Kitchen),
		humanBytes(s.RX), humanBytes(s.TX))
	return nil
}

// humanBytes formats a byte count compactly (e.g. 1.2 MB).
func humanBytes(n int64) string {
	const u = 1024
	if n < u {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(u), 0
	for x := n / u; x >= u; x /= u {
		div *= u
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
