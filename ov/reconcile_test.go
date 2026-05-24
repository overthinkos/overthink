package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestImageReconcile_NewestReferenced is the regression guard for the cross-repo
// pin-alignment tool: mixed pins of one repo collapse to the newest referenced
// version, comments survive, and a second run is a no-op (idempotent). Default
// mode is offline (newest-referenced), so the test is hermetic.
func TestImageReconcile_NewestReferenced(t *testing.T) {
	dir := t.TempDir()
	yml := "" +
		"# top comment\n" +
		"version: 2026.143.844\n" +
		"import:\n" +
		"  - '@github.com/overthinkos/overthink/build.yml:v2026.141.1600'\n" +
		"image:\n" +
		"  selkies-desktop:\n" +
		"    base: cachyos.cachyos\n" +
		"    layer:\n" +
		"      - '@github.com/overthinkos/overthink/layers/agent-forwarding:v2026.141.1600' # infra\n" +
		"      - '@github.com/overthinkos/overthink/layers/selkies-desktop:v2026.144.0531'\n" +
		"      - '@github.com/overthinkos/overthink/layers/dbus:v2026.141.1600'\n" +
		"      - '@github.com/overthinkos/other/layers/x:v1.0.0'\n"
	path := filepath.Join(dir, "overthink.yml")
	if err := os.WriteFile(path, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}

	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)

	cmd := &ImageReconcileCmd{}
	if err := cmd.Run(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got, _ := os.ReadFile(path)
	s := string(got)

	// Every overthink-repo pin aligns to the newest referenced (v2026.144.0531).
	if strings.Contains(s, "v2026.141.1600") {
		t.Errorf("overthink pins not aligned to newest; still has v2026.141.1600:\n%s", s)
	}
	for _, want := range []string{
		"build.yml:v2026.144.0531",
		"agent-forwarding:v2026.144.0531",
		"dbus:v2026.144.0531",
		"selkies-desktop:v2026.144.0531",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing aligned pin %q in:\n%s", want, s)
		}
	}
	// A DIFFERENT repo with a single version is left untouched.
	if !strings.Contains(s, "other/layers/x:v1.0.0") {
		t.Errorf("single-version other-repo pin should be untouched:\n%s", s)
	}
	// Comments preserved (node-API edit).
	if !strings.Contains(s, "# top comment") || !strings.Contains(s, "# infra") {
		t.Errorf("comments not preserved:\n%s", s)
	}

	// Idempotent: a second run rewrites nothing.
	if err := cmd.Run(); err != nil {
		t.Fatalf("reconcile (2nd): %v", err)
	}
	got2, _ := os.ReadFile(path)
	if string(got2) != s {
		t.Errorf("reconcile not idempotent:\nfirst:\n%s\nsecond:\n%s", s, string(got2))
	}
}

// TestImageReconcile_NoPins: a project with no @github pins is a clean no-op.
func TestImageReconcile_NoPins(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "overthink.yml"),
		[]byte("version: 2026.143.844\nimage:\n  foo:\n    base: fedora\n    layer: [agent-forwarding]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)
	if err := (&ImageReconcileCmd{}).Run(); err != nil {
		t.Fatalf("reconcile no-pins: %v", err)
	}
}
