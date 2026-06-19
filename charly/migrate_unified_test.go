package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateUnified_IncludesSplit(t *testing.T) {
	root := t.TempDir()

	// Seed legacy layout.
	writeFixture(t, root, "build.yml", `distro:
  fedora:
    bootstrap:
      install_cmd: dnf install
      package: [dnf5]
builder:
  fedora-builder: {}
init:
  supervisord: {model: fragment_assembly}
`)
	writeFixture(t, root, "image.yml", `defaults:
  registry: quay.io/example
  build: [rpm]
image:
  fedora:
    base: quay.io/fedora/fedora:43
    layer: [base]
`)
	writeFixture(t, root, "layers/chrome/layer.yml", `layer:
  rpm:
    package: [chromium]
`)

	// Run the FULL project migration chain to HEAD (unified → candy-box-rename →
	// charly-rebrand → single-filename → calver-schema): the legacy image.yml +
	// layers/<n>/layer.yml become box/<n>/charly.yml + candy/<n>/charly.yml, and the
	// tree is stamped to the current schema so it loads + discovers.
	ctx, err := NewMigrateContext(root, false)
	if err != nil {
		t.Fatalf("NewMigrateContext: %v", err)
	}
	if _, err := RunProjectMigrations(ctx); err != nil {
		t.Fatalf("RunProjectMigrations: %v", err)
	}

	// The single entry point is charly.yml; the custom build.yml stays imported (it
	// overrides the embedded default vocabulary) and discover: scans box + candy.
	rootData, err := os.ReadFile(filepath.Join(root, UnifiedFileName))
	if err != nil {
		t.Fatalf("read root: %v", err)
	}
	s := string(rootData)
	if !strings.Contains(s, "import:") {
		t.Error("root charly.yml missing import:")
	}
	if !strings.Contains(s, "build.yml") {
		t.Error("custom build.yml import dropped (should be kept — it overrides the embed)")
	}
	if !strings.Contains(s, "discover:") {
		t.Error("root charly.yml missing discover:")
	}

	// LoadUnified runs discovery internally, so it sees the migrated fedora box,
	// the fedora distro, and the discovered chrome candy.
	uf, present, err := LoadUnified(root)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	if !present {
		t.Fatal("charly.yml not present after migration")
	}
	if _, ok := uf.Box["fedora"]; !ok {
		t.Error("LoadUnified lost the fedora box after migration")
	}
	if _, ok := uf.Distro["fedora"]; !ok {
		t.Error("LoadUnified lost the fedora distro after migration")
	}
	if _, ok := uf.Candy["chrome"]; !ok {
		t.Error("discovery didn't pick up candy/chrome after migration")
	}
}

func TestMigrateUnified_Monolithic(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "build.yml", `distro:
  fedora: {}
`)
	writeFixture(t, root, "image.yml", `image:
  x:
    base: alpine
`)
	written, err := MigrateUnified(MigrateUnifiedOpts{Dir: root, Monolithic: true})
	if err != nil {
		t.Fatalf("MigrateUnified: %v", err)
	}
	if len(written) != 1 {
		t.Errorf("written = %v, want 1 file in monolithic mode", written)
	}
	// MigrateUnified (a historical step) writes the legacy overthink.yml in
	// isolation; charly-rebrand renames it to charly.yml later in the chain.
	data, _ := os.ReadFile(filepath.Join(root, "overthink.yml"))
	s := string(data)
	if strings.Contains(s, "includes:") {
		t.Error("monolithic emission should not include `includes:`")
	}
	if !strings.Contains(s, "distro:") {
		t.Error("monolithic output missing distro:")
	}
	if !strings.Contains(s, "box:") {
		t.Error("monolithic output missing box:")
	}
}

func TestMigrateUnified_CandyRewriteIdempotent(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "layers/chrome/layer.yml", `rpm:
  package: [chromium]
`)
	// First pass: rewrites flat → kind-keyed.
	if _, err := MigrateUnified(MigrateUnifiedOpts{Dir: root, RewriteCandies: true}); err != nil {
		t.Fatalf("migrate 1: %v", err)
	}
	data1, _ := os.ReadFile(filepath.Join(root, "layers", "chrome", "layer.yml"))
	if !strings.HasPrefix(string(data1), "layer:") {
		t.Fatalf("after rewrite: file does not start with `layer:` (got %q)", string(data1)[:30])
	}
	if !strings.Contains(string(data1), "name: chrome") {
		t.Error("rewritten file missing name: chrome")
	}
	// Second pass: should be a no-op (idempotent).
	if _, err := MigrateUnified(MigrateUnifiedOpts{Dir: root, RewriteCandies: true}); err != nil {
		t.Fatalf("migrate 2: %v", err)
	}
	data2, _ := os.ReadFile(filepath.Join(root, "layers", "chrome", "layer.yml"))
	if string(data1) != string(data2) {
		t.Error("second rewrite pass changed the file — not idempotent")
	}
}
