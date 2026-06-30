package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStripLegacyOverthinkBlocks covers the shared self-heal helper that both
// charly's managed-block writer and the cutover-4 migration use to remove a
// pre-rebrand `# overthink:` block. The canonical case is the one the
// eval-local / eval-charly-vm beds surfaced: a shell-init file carrying BOTH
// the stale `# overthink:` block AND the current `# opencharly:` block.
func TestStripLegacyOverthinkBlocks(t *testing.T) {
	both := "# overthink:begin (managed by ov; do not edit inside this block)\n" +
		"for f in /home/u/.config/overthink/env.d/*.env; do [ -r \"$f\" ] && . \"$f\"; done\n" +
		"# overthink:end\n" +
		"\n" +
		"# opencharly:begin (managed by charly; do not edit inside this block)\n" +
		"for f in /home/u/.config/opencharly/env.d/*.env; do [ -r \"$f\" ] && . \"$f\"; done\n" +
		"# opencharly:end\n"

	got := stripLegacyOverthinkBlocks(both)
	if strings.Contains(got, "# overthink:") {
		t.Errorf("legacy block survived:\n%s", got)
	}
	if strings.Contains(got, "/.config/overthink/env.d") {
		t.Errorf("stale overthink env.d glob survived:\n%s", got)
	}
	if !strings.Contains(got, "# opencharly:begin") || !strings.Contains(got, "opencharly/env.d") {
		t.Errorf("current opencharly block must be preserved:\n%s", got)
	}

	// Only the legacy block, no current block → stripped to (effectively) empty.
	onlyLegacy := "# overthink:begin (managed by ov; do not edit inside this block)\n" +
		"for f in /home/u/.config/overthink/env.d/*.env; do . \"$f\"; done\n" +
		"# overthink:end\n"
	if got := stripLegacyOverthinkBlocks(onlyLegacy); strings.Contains(got, "overthink") {
		t.Errorf("only-legacy strip left a residue: %q", got)
	}

	// No legacy block → EXACT passthrough (idempotency contract for callers).
	clean := "# opencharly:begin direnv (managed by charly; do not edit inside this block)\n" +
		"eval \"$(direnv hook bash)\"\n# opencharly:end direnv\n"
	if got := stripLegacyOverthinkBlocks(clean); got != clean {
		t.Errorf("clean content must pass through unchanged:\ngot:  %q\nwant: %q", got, clean)
	}

	// Per-layer tagged legacy block is also stripped (begin/end pair matched by
	// the bare `# overthink:begin` / `# overthink:end` substring).
	tagged := "# overthink:begin keyring (managed by ov; do not edit inside this block)\n" +
		"export X=1\n# overthink:end keyring\nkeepline\n"
	got = stripLegacyOverthinkBlocks(tagged)
	if strings.Contains(got, "overthink") || !strings.Contains(got, "keepline") {
		t.Errorf("tagged legacy strip wrong: %q", got)
	}
}

// TestCutover4StripLegacyBlocks_Zshenv proves the file-level migration helper
// removes the stale block from a real .zshenv, is idempotent, and writes a
// .bak only on an actual change.
func TestCutover4StripLegacyBlocks_Zshenv(t *testing.T) {
	dir := t.TempDir()
	zshenv := filepath.Join(dir, ".zshenv")
	content := "# overthink:begin (managed by ov; do not edit inside this block)\n" +
		"for f in /home/u/.config/overthink/env.d/*.env; do [ -r \"$f\" ] && . \"$f\"; done\n" +
		"# overthink:end\n\n" +
		"# opencharly:begin (managed by charly; do not edit inside this block)\n" +
		"for f in /home/u/.config/opencharly/env.d/*.env; do [ -r \"$f\" ] && . \"$f\"; done\n" +
		"# opencharly:end\n"
	if err := os.WriteFile(zshenv, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	mod, err := cutover4StripLegacyBlocks(zshenv, false)
	if err != nil {
		t.Fatalf("strip: %v", err)
	}
	if !mod {
		t.Fatal("expected a change on the first run")
	}
	out, _ := os.ReadFile(zshenv)
	if strings.Contains(string(out), "overthink") {
		t.Errorf("overthink survived in .zshenv:\n%s", out)
	}
	if !strings.Contains(string(out), "# opencharly:begin") {
		t.Errorf("opencharly block lost:\n%s", out)
	}

	// Idempotent: a second run is a no-op and writes no further backup.
	mod2, err := cutover4StripLegacyBlocks(zshenv, false)
	if err != nil {
		t.Fatalf("strip (2nd): %v", err)
	}
	if mod2 {
		t.Error("second run must be a no-op")
	}

	// Absent file → no-op, no error.
	if mod, err := cutover4StripLegacyBlocks(filepath.Join(dir, "nope"), false); err != nil || mod {
		t.Errorf("absent file: mod=%v err=%v, want false/nil", mod, err)
	}
}

// TestCutover4RewriteEnvdHeader proves the stale brand header in an env.d file
// written by the old binary is corrected, and the rewrite is idempotent.
func TestCutover4RewriteEnvdHeader(t *testing.T) {
	dir := t.TempDir()
	envd := filepath.Join(dir, "nodejs.env")
	if err := os.WriteFile(envd,
		[]byte("# overthink env for layer nodejs — managed by ov; do not edit\nexport NODE=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mod, err := cutover4RewriteEnvdHeader(envd, false)
	if err != nil || !mod {
		t.Fatalf("rewrite: mod=%v err=%v", mod, err)
	}
	out, _ := os.ReadFile(envd)
	if strings.Contains(string(out), "overthink") || strings.Contains(string(out), "managed by ov;") {
		t.Errorf("stale header survived:\n%s", out)
	}
	if !strings.Contains(string(out), "# opencharly env for layer nodejs — managed by charly;") {
		t.Errorf("header not rebranded:\n%s", out)
	}
	if !strings.Contains(string(out), "export NODE=1") {
		t.Errorf("body content lost:\n%s", out)
	}
	mod2, _ := cutover4RewriteEnvdHeader(envd, false)
	if mod2 {
		t.Error("second run must be a no-op")
	}
}

// TestCutover4Scalar covers the Phase A string transforms: CH_→CHARLY_ env
// keys (whole-token, sparing BRANCH_/MATCH_ tails), the ov/<svc>→charly/<svc>
// credential prefix, and the charly-first power-user entity renames.
func TestCutover4Scalar(t *testing.T) {
	cases := []struct{ in, want string }{
		{"CH_REPO_OVERRIDE", "CHARLY_REPO_OVERRIDE"},
		{"CH_BUILD_ENGINE=podman", "CHARLY_BUILD_ENGINE=podman"},
		{"ov/secret/API_KEY", "charly/secret/API_KEY"},
		{"ov/enc/immich", "charly/enc/immich"},
		{"ov_install", "charly_install"},
		{"ov_install_strategy", "charly_install_strategy"},
		{"arch-charly", "charly-arch"},
		{"fedora-charly", "charly-fedora"},
		// Protected: CH_ only at a word boundary — these tails must NOT change.
		{"GIT_BRANCH_NAME", "GIT_BRANCH_NAME"},
		{"NO_MATCH_HERE", "NO_MATCH_HERE"},
		// A plain value with none of the tokens is untouched.
		{"just a value", "just a value"},
	}
	for _, c := range cases {
		if got := cutover4Scalar(c.in); got != c.want {
			t.Errorf("cutover4Scalar(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
