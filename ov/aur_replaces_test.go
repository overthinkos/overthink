package main

import (
	"reflect"
	"testing"
)

// TestStringSliceFromYAML covers the three input shapes the helper
// must tolerate: pre-stringified slice, []interface{} (the YAML
// decoder's default), and unsupported types (return ok=false).
//
// Backs the AUR `replaces:` list extraction in collectBuilderContext —
// the decoder produces []interface{} for sequences, but pre-processed
// callers may pass []string directly.
func TestStringSliceFromYAML(t *testing.T) {
	cases := []struct {
		name string
		in   interface{}
		want []string
		ok   bool
	}{
		{"pre-stringified", []string{"code", "vscode"}, []string{"code", "vscode"}, true},
		{"yaml-decoded", []interface{}{"code", "vscode"}, []string{"code", "vscode"}, true},
		{"empty-decoded", []interface{}{}, []string{}, true},
		{"non-string-elements", []interface{}{"code", 42, "vscode"}, []string{"code", "vscode"}, true},
		{"nil", nil, nil, false},
		{"string", "code", nil, false},
		{"map", map[string]interface{}{"x": 1}, nil, false},
	}
	for _, c := range cases {
		got, ok := stringSliceFromYAML(c.in)
		if ok != c.ok {
			t.Errorf("%s: ok = %v, want %v", c.name, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// TestExtractStringSlice_AurReplacesShape exercises the helper used
// by execBuilder to read `replaces:` out of the BuilderStep's
// RawStageContext map. End-to-end shape: yaml-decoded list →
// stringSliceFromYAML → ctx["replaces"] → extractStringSlice.
func TestExtractStringSlice_AurReplacesShape(t *testing.T) {
	repls, ok := stringSliceFromYAML([]interface{}{"code", "code-features"})
	if !ok {
		t.Fatal("stringSliceFromYAML rejected expected shape")
	}
	ctx := map[string]interface{}{
		"layer":    "vscode",
		"builder":  "aur",
		"packages": []string{"visual-studio-code-bin"},
		"replaces": repls,
	}
	got := extractStringSlice(ctx, "replaces")
	want := []string{"code", "code-features"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("extractStringSlice replaces: got %v, want %v", got, want)
	}
	// Absent key returns empty.
	if got := extractStringSlice(ctx, "absent-key"); len(got) != 0 {
		t.Errorf("absent key: got %v, want []", got)
	}
}

// TestPacmanQqInstalledExactly covers the precheck used by
// removeInstalledPacmanPackages to decide whether a `replaces:` entry
// is actually installed under that exact name.
//
// Background: `pacman -Qq <pkg>` resolves virtual Provides= aliases
// and returns the REAL package name on stdout. `pacman -Rs <pkg>`,
// in contrast, only accepts real package names and exits with
// `target not found` for provides-only names. The bug we fixed:
// a re-run of `ov update ov-cachyos` after a successful vscode
// install hit `pacman -Qq code` returning `visual-studio-code-bin`,
// the precheck said "installed", and `pacman -Rs --noconfirm code`
// then failed and halted the entire deploy.
//
// The corrected precheck only treats the queried name as installed
// when `pacman -Qq` returns the queried name verbatim.
func TestPacmanQqInstalledExactly(t *testing.T) {
	cases := []struct {
		name    string
		queried string
		qqOut   string
		want    bool
	}{
		{
			name:    "exact match — pkg actually installed under queried name",
			queried: "code",
			qqOut:   "code\n",
			want:    true,
		},
		{
			name:    "provides alias — visual-studio-code-bin returned for query 'code' (the bug)",
			queried: "code",
			qqOut:   "visual-studio-code-bin\n",
			want:    false,
		},
		{
			name:    "exact match without trailing newline",
			queried: "neovim",
			qqOut:   "neovim",
			want:    true,
		},
		{
			name:    "exact match with surrounding whitespace",
			queried: "tmux",
			qqOut:   "  tmux  \n",
			want:    true,
		},
		{
			name:    "name-prefix collision is NOT a match",
			queried: "code",
			qqOut:   "code-features",
			want:    false,
		},
		{
			name:    "empty pacman stdout — pkg not installed",
			queried: "anything",
			qqOut:   "",
			want:    false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := pacmanQqInstalledExactly(c.queried, []byte(c.qqOut))
			if got != c.want {
				t.Errorf("pacmanQqInstalledExactly(%q, %q) = %v, want %v",
					c.queried, c.qqOut, got, c.want)
			}
		})
	}
}
