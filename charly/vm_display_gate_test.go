package main

import "testing"

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

// TestSpiceEndpoint_NoDevice_MatchesSkipSentinel moved to candy/plugin-vm/spice_endpoint_test.go
// with the VmTarget/SpiceEndpoint resolver (the go-libvirt + libvirtxml shed).
