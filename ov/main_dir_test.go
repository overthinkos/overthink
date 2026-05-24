package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestOvDir_FlagChdir verifies that -C / --dir / OV_PROJECT_DIR causes
// main() to chdir before dispatching, so downstream os.Getwd() calls
// (used by every build-mode command to locate image.yml) see the target
// directory. Covers three modes:
//
//  1. -C <path>  — short flag
//  2. --dir <path> — long flag
//  3. OV_PROJECT_DIR=<path> env var
//
// Uses `ov image list images` as the probe: it reads image.yml from the
// resolved project dir. If the binary fails to chdir, the command errors
// with "reading image.yml: ... no such file or directory". A pass means
// chdir worked — the command listed the images from the scratch project.
func TestOvDir_FlagChdir(t *testing.T) {
	bin := buildOvBinary(t)

	scratch := t.TempDir()
	writeMinProject(t, scratch)

	// Spawn each variant from /tmp (a guaranteed-different cwd so a missed
	// chdir would fail loudly).
	startCwd := os.TempDir()

	cases := []struct {
		name string
		args []string
		env  []string
	}{
		{name: "short flag -C", args: []string{"-C", scratch, "image", "list", "images"}},
		{name: "long flag --dir", args: []string{"--dir", scratch, "image", "list", "images"}},
		{name: "env var OV_PROJECT_DIR", args: []string{"image", "list", "images"}, env: []string{"OV_PROJECT_DIR=" + scratch}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bin, tc.args...)
			cmd.Dir = startCwd
			cmd.Env = append(append([]string{}, os.Environ()...), tc.env...)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("ov %s failed: %v\noutput: %s", strings.Join(tc.args, " "), err, out)
			}
			if !strings.Contains(string(out), "testimage") {
				t.Errorf("ov image list images did not see the scratch project's image; output:\n%s", out)
			}
		})
	}
}

// TestOvDir_Errors covers the error paths: non-existent directory, and a
// file (not a directory) passed to --dir.
func TestOvDir_Errors(t *testing.T) {
	bin := buildOvBinary(t)

	scratch := t.TempDir()
	notADir := filepath.Join(scratch, "not-a-dir")
	if err := os.WriteFile(notADir, []byte("x"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	missing := filepath.Join(scratch, "missing")

	cases := []struct {
		name string
		dir  string
		want string
	}{
		{name: "missing dir", dir: missing, want: "cannot chdir"},
		{name: "file not dir", dir: notADir, want: "cannot chdir"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bin, "-C", tc.dir, "version")
			cmd.Dir = os.TempDir()
			out, _ := cmd.CombinedOutput()
			if !strings.Contains(string(out), tc.want) {
				t.Errorf("expected error containing %q, got: %s", tc.want, out)
			}
		})
	}
}

// buildOvBinary compiles ov into the test temp dir. Cached per-test via
// t.TempDir. Go build is <1s on a warm module cache, well within test
// budget.
func buildOvBinary(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "ov")
	cmd := exec.Command("go", "build", "-o", out, ".")
	if buildOut, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, buildOut)
	}
	return out
}

// writeMinProject materialises a minimal image.yml in dir so `ov image list
// images` has something to parse. Mirrors the scaffold of a real project.
func writeMinProject(t *testing.T, dir string) {
	t.Helper()
	// Post-unified-cutover: write overthink.yml (the unified format) instead
	// of a legacy image.yml. LoadConfig reads overthink.yml exclusively.
	overthinkYAML := `version: 2026.144.1443
defaults:
  registry: ghcr.io/test
  tag: latest
  platform:
    - linux/amd64
  build: [rpm]

image:
  testimage:
    base: "quay.io/fedora/fedora:43"
    distro: ["fedora:43", fedora]
`
	if err := os.WriteFile(filepath.Join(dir, "overthink.yml"), []byte(overthinkYAML), 0644); err != nil {
		t.Fatalf("writing overthink.yml: %v", err)
	}
}
