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

func TestParseKeyCombo(t *testing.T) {
	tests := []struct {
		input   string
		wantMod []string
		wantKey string
		wantErr bool
	}{
		{"ctrl+c", []string{"ctrl"}, "c", false},
		{"alt+tab", []string{"alt"}, "tab", false},
		{"ctrl+shift+t", []string{"ctrl", "shift"}, "t", false},
		{"super+l", []string{"logo"}, "l", false},
		{"win+e", []string{"logo"}, "e", false},
		{"control+alt+delete", []string{"ctrl", "alt"}, "delete", false},
		{"meta+a", []string{"alt"}, "a", false},
		{"logo+return", []string{"logo"}, "return", false},
		// Single key (no modifiers).
		{"a", nil, "a", false},
		{"return", nil, "return", false},
		// Unknown modifier.
		{"bogus+c", nil, "", true},
	}
	for _, tt := range tests {
		mods, key, err := parseKeyCombo(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseKeyCombo(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			continue
		}
		if tt.wantErr {
			continue
		}
		if key != tt.wantKey {
			t.Errorf("parseKeyCombo(%q) key = %q, want %q", tt.input, key, tt.wantKey)
		}
		if len(mods) != len(tt.wantMod) {
			t.Errorf("parseKeyCombo(%q) mods = %v, want %v", tt.input, mods, tt.wantMod)
			continue
		}
		for i, m := range mods {
			if m != tt.wantMod[i] {
				t.Errorf("parseKeyCombo(%q) mod[%d] = %q, want %q", tt.input, i, m, tt.wantMod[i])
			}
		}
	}
}

func TestWlScrollButton(t *testing.T) {
	tests := []struct {
		dir     string
		want    int
		wantErr bool
	}{
		{"up", 4, false},
		{"down", 5, false},
		{"left", 6, false},
		{"right", 7, false},
		{"Up", 4, false},
		{"DOWN", 5, false},
		{"diagonal", 0, true},
		{"", 0, true},
	}
	for _, tt := range tests {
		got, err := wlScrollButton(tt.dir)
		if (err != nil) != tt.wantErr {
			t.Errorf("wlScrollButton(%q) error = %v, wantErr %v", tt.dir, err, tt.wantErr)
			continue
		}
		if got != tt.want {
			t.Errorf("wlScrollButton(%q) = %d, want %d", tt.dir, got, tt.want)
		}
	}
}

func TestWlModifierMap(t *testing.T) {
	// Verify all expected aliases are present.
	expected := map[string]string{
		"ctrl":    "ctrl",
		"control": "ctrl",
		"alt":     "alt",
		"shift":   "shift",
		"super":   "logo",
		"win":     "logo",
		"logo":    "logo",
		"meta":    "alt",
	}
	for alias, want := range expected {
		got, ok := wlModifierMap[alias]
		if !ok {
			t.Errorf("wlModifierMap missing alias %q", alias)
			continue
		}
		if got != want {
			t.Errorf("wlModifierMap[%q] = %q, want %q", alias, got, want)
		}
	}
}

func TestWlCmdSubcommands(t *testing.T) {
	// Verify WlCmd struct has all expected subcommands by checking Kong tags.
	// This is a structural test — it won't run commands, just confirms registration.
	var cmd WlCmd
	_ = cmd.Screenshot
	_ = cmd.Click
	_ = cmd.Type
	_ = cmd.Key
	_ = cmd.Mouse
	_ = cmd.Status
	_ = cmd.Windows
	_ = cmd.Focus
	_ = cmd.Toplevel
	_ = cmd.Close
	_ = cmd.Fullscreen
	_ = cmd.Minimize
	_ = cmd.Exec
	_ = cmd.Resolution
	_ = cmd.KeyCombo
	_ = cmd.DoubleClick
	_ = cmd.Scroll
	_ = cmd.Drag
	_ = cmd.Clipboard
	_ = cmd.Xprop
	_ = cmd.Geometry
	_ = cmd.Atspi
	_ = cmd.Overlay
}
