package main

import (
	"strings"
	"testing"
)

func TestWlShellCmd(t *testing.T) {
	got := wlShellCmd("grim -o HEADLESS-1 -")
	if !strings.Contains(got, "XDG_RUNTIME_DIR") {
		t.Errorf("wlShellCmd missing XDG_RUNTIME_DIR: %s", got)
	}
	if !strings.Contains(got, "WAYLAND_DISPLAY") {
		t.Errorf("wlShellCmd missing WAYLAND_DISPLAY: %s", got)
	}
	if !strings.HasSuffix(got, "&& grim -o HEADLESS-1 -") {
		t.Errorf("wlShellCmd wrong suffix: %s", got)
	}
}

func TestWlKeyNames(t *testing.T) {
	names := wlKeyNames()
	// Must contain common keys.
	for _, k := range []string{"Return", "Escape", "Tab", "F1", "space"} {
		if !strings.Contains(names, k) {
			t.Errorf("wlKeyNames missing %s: %s", k, names)
		}
	}
}

func TestWlValidKey(t *testing.T) {
	valid := []string{"Return", "Escape", "Tab", "BackSpace", "F12", "space",
		"Up", "Down", "Left", "Right", "Shift_L", "Control_L", "Super_L"}
	for _, k := range valid {
		if !wlValidKey(k) {
			t.Errorf("wlValidKey(%q) = false, want true", k)
		}
	}
	invalid := []string{"Enter", "Esc", "RETURN", "f1", "leftshift", ""}
	for _, k := range invalid {
		if wlValidKey(k) {
			t.Errorf("wlValidKey(%q) = true, want false", k)
		}
	}
}

func TestWlButton(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"left", "left"},
		{"right", "right"},
		{"middle", "middle"},
		{"unknown", ""},
		{"LEFT", ""},
	}
	for _, tt := range tests {
		got := wlButton(tt.input)
		if got != tt.want {
			t.Errorf("wlButton(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
