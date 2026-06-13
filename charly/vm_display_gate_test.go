package main

import "testing"

// TestVmDisplayDeviceAbsent pins the SPICE/VNC precondition-not-met SKIP gate:
// a VM-display verb against a deployment with no such display device is N/A
// (the SPICE-less cachyos-gpu operator), never a check failure — while the
// SPICE-having check bed still asserts. One shared check candy, no bed/operator
// split (R3).
func TestVmDisplayDeviceAbsent(t *testing.T) {
	noSpice := "charly: error: VM cachyos-gpu has no SPICE graphics device declared in vm.yml"
	cases := []struct {
		name, verb, stderr string
		want               bool
	}{
		{"spice no-device → skip", "spice", noSpice, true},
		{"vnc no-device → skip", "vnc", "VM x has no VNC graphics device declared in vm.yml", true},
		{"spice connected → no skip", "spice", "connected: 127.0.0.1:5901\ndisplay: 1280x800", false},
		{"non-display verb never gated (wl)", "wl", noSpice, false},
		{"non-display verb never gated (cdp)", "cdp", noSpice, false},
		{"empty stderr → no skip", "spice", "", false},
	}
	for _, tc := range cases {
		if got := vmDisplayDeviceAbsent(tc.verb, tc.stderr); got != tc.want {
			t.Errorf("%s: vmDisplayDeviceAbsent(%q, ...) = %v, want %v", tc.name, tc.verb, got, tc.want)
		}
	}
}
