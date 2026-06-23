package main

import (
	"strings"
	"testing"

	libvirtxml "libvirt.org/go/libvirtxml"
)

// TestVmDisplayDeviceAbsent pins the in-proc VNC precondition-not-met SKIP gate:
// the `vnc` VM-display verb against a deployment with no VNC display device is N/A
// (the display-less cachyos-gpu operator), never a check failure — while the
// VNC-having check bed still asserts. One shared check candy, no bed/operator split
// (R3). `spice` is an EXTERNAL-CHARLY-VERB now (candy/plugin-spice): it does NOT flow
// through this subprocess gate, so vmDisplayDeviceAbsent("spice", …) is always false —
// the SPICE no-device skip moved HOST-side to preresolveSpiceEndpoint
// (spice_preresolve.go), which keys off the SAME noVmDisplayDeviceErr sentinel.
func TestVmDisplayDeviceAbsent(t *testing.T) {
	noSpice := "charly: error: VM cachyos-gpu has no SPICE graphics device declared in vm.yml"
	cases := []struct {
		name, verb, stderr string
		want               bool
	}{
		{"vnc no-device → skip", "vnc", "VM x has no VNC graphics device declared in vm.yml", true},
		{"spice no longer gated here (moved host-side)", "spice", noSpice, false},
		{"vnc connected → no skip", "vnc", "connected: 127.0.0.1:5901\ndisplay: 1280x800", false},
		{"non-display verb never gated (wl)", "wl", noSpice, false},
		{"non-display verb never gated (cdp)", "cdp", noSpice, false},
		{"empty stderr → no skip", "vnc", "", false},
	}
	for _, tc := range cases {
		if got := vmDisplayDeviceAbsent(tc.verb, tc.stderr); got != tc.want {
			t.Errorf("%s: vmDisplayDeviceAbsent(%q, ...) = %v, want %v", tc.name, tc.verb, got, tc.want)
		}
	}
}

// TestSpiceEndpoint_NoDevice_MatchesSkipSentinel proves the host-side no-SPICE-device
// SKIP is wired correctly: when a resolved VM declares no <graphics type='spice'>,
// SpiceEndpoint must return an error whose text contains noVmDisplayDeviceErr — the
// SAME substring preresolveSpiceEndpoint (spice_preresolve.go) keys off to return a
// SKIP (the SPICE-less cachyos-gpu operator) rather than a FAIL. This pins the
// resolver-error ⇄ skip-sentinel contract so a change to either side that breaks the
// no-device skip is caught at `go test` time, with no live VM needed.
func TestSpiceEndpoint_NoDevice_MatchesSkipSentinel(t *testing.T) {
	// A domain with a VNC head but NO spice device.
	parsed := &libvirtxml.Domain{}
	if err := parsed.Unmarshal(`<domain><devices><graphics type='vnc' port='5900'/></devices></domain>`); err != nil {
		t.Fatalf("unmarshal domain XML: %v", err)
	}
	tgt := &VmTarget{XML: parsed, VmName: "cachyos-gpu", DomName: "charly-cachyos-gpu"}
	_, err := tgt.SpiceEndpoint()
	if err == nil {
		t.Fatal("SpiceEndpoint on a spice-less domain: want error, got nil")
	}
	if !strings.Contains(err.Error(), noVmDisplayDeviceErr) {
		t.Errorf("SpiceEndpoint no-device error %q does not contain the skip sentinel %q — "+
			"preresolveSpiceEndpoint would FAIL instead of SKIP", err, noVmDisplayDeviceErr)
	}
}
