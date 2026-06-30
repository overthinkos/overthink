package main

// migrate_field_singular_core_test.go — the field-singular round-trip test that
// spans BOTH modules: it runs the candy's MigrateFieldSingular migrator, then
// asserts the result passes the CORE load-time RejectLegacyPluralKeys gate
// (package-main). Relocated from the migrate chain in C13a.

import (
	"os"
	"path/filepath"
	"testing"

	migrate "github.com/overthinkos/overthink/candy/plugin-migrate"
)

func writeFieldSingularTempFile(t *testing.T, dir, name, content string) string {
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

func TestFieldSingularRoundTripParseable(t *testing.T) {
	dir := t.TempDir()
	p := writeFieldSingularTempFile(t, dir, "overthink.yml", `version: 4
images:
  foo:
    layers: [base]
    ports: [80]
`)
	if _, err := migrate.MigrateFieldSingular(migrate.MigrateFieldSingularOpts{Dir: dir, BackupSuffix: ".bak.test"}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := RejectLegacyPluralKeys(p, data); err != nil {
		t.Errorf("post-migration tree should pass rejection check: %v", err)
	}
	pre := []byte(`version: 4
images:
  foo: {}
`)
	if err := RejectLegacyPluralKeys("pre", pre); err == nil {
		t.Errorf("pre-migration tree should be rejected by helper")
	}
}
