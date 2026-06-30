package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestApplyTargetLocalRewrites_PhaseInstallHostUntouched: a build-vocabulary
// `phase.install.host: |` install-TEMPLATE block scalar is NOT a deploy host and
// must be left completely untouched — never flagged AMBIGUOUS. The over-broad
// `host:` match once stacked AMBIGUOUS comments onto build.yml's install
// templates (a deploy host is always a plain scalar, never a block/flow scalar).
func TestApplyTargetLocalRewrites_PhaseInstallHostUntouched(t *testing.T) {
	src := "phase:\n" +
		"    install:\n" +
		"        host: |\n" +
		"            set -e\n" +
		"            echo install\n"
	got := applyTargetLocalRewrites(src, map[string]bool{})
	if strings.Contains(got, "AMBIGUOUS") {
		t.Errorf("phase.install.host: | (a build template) was wrongly flagged AMBIGUOUS:\n%s", got)
	}
	if got != src {
		t.Errorf("phase.install.host: | block scalar should be left untouched\n--- got ---\n%s--- want ---\n%s", got, src)
	}
}

// TestApplyTargetLocalRewrites_AmbiguousHostIsIdempotent: a genuinely ambiguous
// deploy `host: <bareword>` (not a known template, not hostname-like) is flagged
// AMBIGUOUS exactly ONCE; re-running migrate must NEVER re-stack the marker.
func TestApplyTargetLocalRewrites_AmbiguousHostIsIdempotent(t *testing.T) {
	src := "some-deploy:\n" +
		"    target: local\n" +
		"    host: mystery\n"
	templates := map[string]bool{} // "mystery" is not a known kind:local template
	once := applyTargetLocalRewrites(src, templates)
	if n := strings.Count(once, "AMBIGUOUS"); n != 1 {
		t.Fatalf("first pass: expected exactly 1 AMBIGUOUS marker, got %d:\n%s", n, once)
	}
	twice := applyTargetLocalRewrites(once, templates)
	if twice != once {
		t.Errorf("applyTargetLocalRewrites is not idempotent — second pass changed output:\n--- once ---\n%s--- twice ---\n%s", once, twice)
	}
	if n := strings.Count(twice, "AMBIGUOUS"); n != 1 {
		t.Errorf("AMBIGUOUS marker re-stacked on second pass (count=%d):\n%s", n, twice)
	}
}

// TestApplyTargetLocalRewrites_KnownTemplateHostRewrites: a deploy `host: <name>`
// matching a known kind:local template still becomes `local: <name>` (regression
// guard — the fix must not break the legitimate disambiguation path).
func TestApplyTargetLocalRewrites_KnownTemplateHostRewrites(t *testing.T) {
	src := "some-deploy:\n" +
		"    host: my-laptop\n"
	got := applyTargetLocalRewrites(src, map[string]bool{"my-laptop": true})
	if !strings.Contains(got, "local: my-laptop") {
		t.Errorf("host: <known-template> should rewrite to local: <name>, got:\n%s", got)
	}
	if strings.Contains(got, "AMBIGUOUS") {
		t.Errorf("a known template must not be flagged AMBIGUOUS:\n%s", got)
	}
}

// TestMigrateTargetLocal_EndToEndIdempotent exercises the FULL command-invoked
// path (walk → read → applyTargetLocalRewrites → write) on real files: the first
// pass flags a genuinely-ambiguous deploy host once and leaves a build
// `phase.install.host:` template alone; the second pass reports ZERO changed
// files and mutates nothing — the end-to-end idempotency the stacked-comment bug
// violated.
func TestMigrateTargetLocal_EndToEndIdempotent(t *testing.T) {
	dir := t.TempDir()
	yml := filepath.Join(dir, "build.yml")
	content := "builder:\n" +
		"    pixi:\n" +
		"        phase:\n" +
		"            install:\n" +
		"                host: |\n" +
		"                    set -e\n" +
		"                    pixi install\n" +
		"deploy:\n" +
		"    some-deploy:\n" +
		"        target: local\n" +
		"        host: mystery\n"
	if err := os.WriteFile(yml, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateTargetLocal(dir, false); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	first, _ := os.ReadFile(yml)
	if n := strings.Count(string(first), "AMBIGUOUS"); n != 1 {
		t.Fatalf("first pass: want exactly 1 AMBIGUOUS (deploy host flagged, install template untouched), got %d:\n%s", n, first)
	}
	if strings.Contains(string(first), "host: |  # AMBIGUOUS") {
		t.Errorf("phase.install.host: | (build template) was wrongly flagged:\n%s", first)
	}
	changed, err := MigrateTargetLocal(dir, false)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if len(changed) != 0 {
		t.Errorf("not idempotent — second pass reported changed files: %v", changed)
	}
	if second, _ := os.ReadFile(yml); string(second) != string(first) {
		t.Errorf("second pass mutated the file:\n--- first ---\n%s--- second ---\n%s", first, second)
	}
}
