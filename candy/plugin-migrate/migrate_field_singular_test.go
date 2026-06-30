package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTempFile writes content to <dir>/<name> creating any needed
// parent directories under dir; fatal on error.
func writeTempFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(p), err)
	}
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(data)
}

// 1. TestFieldSingularBasicRewrite — single layer.yml; assert plural
// keys rewritten and a .bak file is created.
func TestFieldSingularBasicRewrite(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "overthink.yml", "version: 4\n")
	p := writeTempFile(t, dir, "layers/redis/layer.yml", `layer:
  name: redis
  layers: [base]
  ports: [6379]
  requires: [base]
`)
	res, err := MigrateFieldSingular(MigrateFieldSingularOpts{Dir: dir, BackupSuffix: ".bak.test"})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(res.Rewritten) != 1 {
		t.Fatalf("expected 1 rewrite, got %d (%+v)", len(res.Rewritten), res.Rewritten)
	}
	got := readFile(t, p)
	for _, want := range []string{"layer: [base]", "port: [6379]", "require: [base]"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\ngot:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "name: redis") {
		t.Errorf("non-plural fields should pass through\ngot:\n%s", got)
	}
	if _, err := os.Stat(p + ".bak.test"); err != nil {
		t.Errorf("backup file not written: %v", err)
	}
}

// 2. TestFieldSingularIdempotent — second run reports zero changes.
func TestFieldSingularIdempotent(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "overthink.yml", "version: 4\n")
	writeTempFile(t, dir, "layers/redis/layer.yml", `layer:
  layers: [base]
`)
	first, err := MigrateFieldSingular(MigrateFieldSingularOpts{Dir: dir, BackupSuffix: ".bak.test"})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if len(first.Rewritten) != 1 {
		t.Fatalf("first run: expected 1 rewrite, got %d", len(first.Rewritten))
	}
	second, err := MigrateFieldSingular(MigrateFieldSingularOpts{Dir: dir, BackupSuffix: ".bak.test2"})
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if len(second.Rewritten) != 0 {
		t.Fatalf("second run should be a no-op, got %d rewrites: %+v", len(second.Rewritten), second.Rewritten)
	}
	for _, p := range []string{
		filepath.Join(dir, "layers/redis/layer.yml.bak.test2"),
	} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("second run wrote a spurious backup at %s", p)
		}
	}
}

// 3. TestFieldSingularPreservesComments — comments above and on the same
// line as plural keys are retained verbatim.
func TestFieldSingularPreservesComments(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "overthink.yml", "version: 4\n")
	p := writeTempFile(t, dir, "layers/redis/layer.yml", `# Top-of-file comment.
layer:
  # comment above the plural key
  layers: [base]   # trailing comment
`)
	if _, err := MigrateFieldSingular(MigrateFieldSingularOpts{Dir: dir, BackupSuffix: ".bak.test"}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	got := readFile(t, p)
	for _, want := range []string{
		"# Top-of-file comment.",
		"# comment above the plural key",
		"layer: [base]   # trailing comment",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("comment lost: %q\ngot:\n%s", want, got)
		}
	}
}

// 4. TestFieldSingularPreservesIndentation — keys at indent 0/2/4/8 all
// rewritten with original indentation preserved.
func TestFieldSingularPreservesIndentation(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "overthink.yml", "version: 4\n")
	p := writeTempFile(t, dir, "layers/redis/layer.yml", "layers: []\n  ports: [80]\n    requires: [a]\n        tasks: []\n")
	if _, err := MigrateFieldSingular(MigrateFieldSingularOpts{Dir: dir, BackupSuffix: ".bak.test"}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	got := readFile(t, p)
	wants := []string{
		"layer: []\n",
		"  port: [80]\n",
		"    require: [a]\n",
		"        task: []\n",
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("indent not preserved for %q\ngot:\n%s", w, got)
		}
	}
}

// 5. TestFieldSingularMultiFileWalk — fixture project with multiple
// .yml files; assert all of them are rewritten.
func TestFieldSingularMultiFileWalk(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "overthink.yml", `version: 4
includes:
  - image.yml
  - deploy.yml
`)
	writeTempFile(t, dir, "image.yml", "kind: image\nimage:\n  foo:\n    layers: [base]\n")
	writeTempFile(t, dir, "deploy.yml", "kind: deploy\ndeploy:\n  bar:\n    ports: [80]\n")
	writeTempFile(t, dir, "layers/redis/layer.yml", "layer:\n  requires: [a]\n")
	writeTempFile(t, dir, "layers/postgres/layer.yml", "layer:\n  requires: [a]\n")
	res, err := MigrateFieldSingular(MigrateFieldSingularOpts{Dir: dir, BackupSuffix: ".bak.test"})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	rewrittenSet := map[string]struct{}{}
	for _, r := range res.Rewritten {
		rel, _ := filepath.Rel(dir, r.Path)
		rewrittenSet[rel] = struct{}{}
	}
	for _, want := range []string{
		"overthink.yml",
		"image.yml",
		"deploy.yml",
		"layers/redis/layer.yml",
		"layers/postgres/layer.yml",
	} {
		if _, ok := rewrittenSet[want]; !ok {
			t.Errorf("expected %s rewritten, got %+v", want, rewrittenSet)
		}
	}
}

// 6. TestFieldSingularCompoundKeys — `requires_capabilities:` rewritten
// to `requires_capability:` BEFORE `requires:` rule fires (longest-first).
func TestFieldSingularCompoundKeys(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "overthink.yml", "version: 4\n")
	p := writeTempFile(t, dir, "layers/foo/layer.yml", `layer:
  requires_capabilities: [bind_mount]
  requires: [base]
  env_requires: [HOME]
`)
	if _, err := MigrateFieldSingular(MigrateFieldSingularOpts{Dir: dir, BackupSuffix: ".bak.test"}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	got := readFile(t, p)
	for _, want := range []string{
		"requires_capability: [bind_mount]",
		"require: [base]",
		"env_require: [HOME]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("compound rewrite missing %q\ngot:\n%s", want, got)
		}
	}
	if strings.Contains(got, "require_capability") {
		t.Errorf("compound rewrite produced bad form 'require_capability':\n%s", got)
	}
}

// 7. TestFieldSingularListVsMapPlurals — `images:` as both top-level
// mapping AND as nested mapping; both rewritten.
func TestFieldSingularListVsMapPlurals(t *testing.T) {
	dir := t.TempDir()
	p := writeTempFile(t, dir, "overthink.yml", `version: 4
images:
  foo:
    base: archlinux
distros:
  archlinux:
    base_user: user
deployments:
  images:
    bar:
      target: pod
`)
	if _, err := MigrateFieldSingular(MigrateFieldSingularOpts{Dir: dir, BackupSuffix: ".bak.test"}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	got := readFile(t, p)
	for _, want := range []string{
		"image:\n  foo:",
		"distro:\n  archlinux:",
		"deploy:",
		"  image:\n    bar:",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing rewrite %q\ngot:\n%s", want, got)
		}
	}
}

// 8. TestFieldSingularSkipsValueLines — quoted string value containing
// the word `layers:` mid-content is NOT rewritten.
func TestFieldSingularSkipsValueLines(t *testing.T) {
	dir := t.TempDir()
	p := writeTempFile(t, dir, "overthink.yml", `version: 4
defaults:
  description: "see layers: section in README for details"
  comment: 'the layers: directive is a list'
`)
	if _, err := MigrateFieldSingular(MigrateFieldSingularOpts{Dir: dir, BackupSuffix: ".bak.test"}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	got := readFile(t, p)
	if !strings.Contains(got, `"see layers: section in README for details"`) {
		t.Errorf("quoted value rewritten in error\ngot:\n%s", got)
	}
	if !strings.Contains(got, `'the layers: directive is a list'`) {
		t.Errorf("single-quoted value rewritten in error\ngot:\n%s", got)
	}
}

// 9. TestFieldSingularDryRun — DryRun reports planned writes; touches no
// files on disk.
func TestFieldSingularDryRun(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "overthink.yml", "version: 4\n")
	p := writeTempFile(t, dir, "image.yml", "kind: image\nimage:\n  foo:\n    layers: [base]\n")
	original := readFile(t, p)
	res, err := MigrateFieldSingular(MigrateFieldSingularOpts{Dir: dir, DryRun: true, BackupSuffix: ".bak.test"})
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if len(res.Rewritten) == 0 {
		t.Fatalf("dry-run reported zero planned writes; expected ≥1")
	}
	if got := readFile(t, p); got != original {
		t.Errorf("dry-run wrote to disk\nwant:\n%s\ngot:\n%s", original, got)
	}
	if _, err := os.Stat(p + ".bak.test"); err == nil {
		t.Errorf("dry-run wrote a backup file")
	}
}

// 10. TestFieldSingularNoOpOnEmptyFile — empty / comment-only / null
// files silently skipped.
func TestFieldSingularNoOpOnEmptyFile(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "overthink.yml", "version: 4\n")
	writeTempFile(t, dir, "image.yml", "")
	writeTempFile(t, dir, "deploy.yml", "# only a comment\n")
	writeTempFile(t, dir, "pod.yml", "null\n")
	res, err := MigrateFieldSingular(MigrateFieldSingularOpts{Dir: dir, BackupSuffix: ".bak.test"})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(res.Rewritten) != 0 {
		t.Errorf("empty/comment/null files should not produce rewrites; got %+v", res.Rewritten)
	}
}

// 11. TestFieldSingularRoundTripParseable — after migration, the
// rejection helper accepts the rewritten output.
// TestFieldSingularRoundTripParseable moved to charly core
// (charly/migrate_field_singular_core_test.go) — it asserts the migrator output
// passes the CORE load-time RejectLegacyPluralKeys gate (package-main), so it spans
// both modules and lives in core (C13a).

// 12. TestFieldSingularRejectsBackupCollision — pre-existing
// .bak.<suffix> file errors instead of overwriting.
func TestFieldSingularRejectsBackupCollision(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, dir, "overthink.yml", "version: 4\n")
	p := writeTempFile(t, dir, "image.yml", "kind: image\nimage:\n  foo:\n    layers: [base]\n")
	preBackup := p + ".bak.test"
	if err := os.WriteFile(preBackup, []byte("stale-pre-existing-backup"), 0600); err != nil {
		t.Fatalf("pre-create backup: %v", err)
	}
	_, err := MigrateFieldSingular(MigrateFieldSingularOpts{Dir: dir, BackupSuffix: ".bak.test"})
	if err == nil {
		t.Fatalf("expected error on backup collision; got nil")
	}
	if !strings.Contains(err.Error(), "backup file already exists") {
		t.Errorf("error message should mention backup collision; got: %v", err)
	}
	if got := readFile(t, preBackup); got != "stale-pre-existing-backup" {
		t.Errorf("stale backup was overwritten: %s", got)
	}
}
