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
  supervisord: {}
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

	// Run migration.
	written, err := MigrateUnified(MigrateUnifiedOpts{
		Dir: root,
	})
	if err != nil {
		t.Fatalf("MigrateUnified: %v", err)
	}
	// The full chain also runs candy-box-rename (a later step): layers/ → candy/,
	// layer.yml → candy.yml, and the discover path — so the migrated tree loads
	// and discovers under the current schema.
	if _, err := MigrateBoxCandyRename(root, "", false); err != nil {
		t.Fatalf("candy-box rename: %v", err)
	}
	// ... and charly-rebrand (the final pre-stamp step) renames overthink.yml →
	// charly.yml so the tree loads under the current UnifiedFileName.
	if _, err := MigrateCharlyRebrand(&MigrateContext{Dir: root}); err != nil {
		t.Fatalf("charly-rebrand: %v", err)
	}
	// Expect root + build.yml + images.yml written.
	if len(written) < 3 {
		t.Errorf("written = %v, want ≥3 files", written)
	}

	// Root file contains includes + discover.
	rootData, err := os.ReadFile(filepath.Join(root, UnifiedFileName))
	if err != nil {
		t.Fatalf("read root: %v", err)
	}
	s := string(rootData)
	if !strings.Contains(s, "import:") {
		t.Error("root overthink.yml missing import:")
	}
	if !strings.Contains(s, "build.yml") {
		t.Error("includes missing build.yml")
	}
	if !strings.Contains(s, "images.yml") {
		t.Error("includes missing images.yml")
	}
	if !strings.Contains(s, "discover:") {
		t.Error("root missing discover:")
	}

	// Round-trip: LoadUnified + ApplyDiscover should see the migrated fedora
	// image, the fedora distro, and the discovered chrome layer.
	uf, _, err := LoadUnified(root)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	if _, ok := uf.Image["fedora"]; !ok {
		t.Error("LoadUnified lost images.fedora after migration")
	}
	if _, ok := uf.Distro["fedora"]; !ok {
		t.Error("LoadUnified lost distros.fedora after migration")
	}
	if err := uf.ApplyDiscover(root); err != nil {
		t.Fatalf("ApplyDiscover: %v", err)
	}
	if _, ok := uf.Layer["chrome"]; !ok {
		t.Error("ApplyDiscover didn't pick up layers/chrome")
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

func TestMigrateUnified_LayerRewriteIdempotent(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "layers/chrome/layer.yml", `rpm:
  package: [chromium]
`)
	// First pass: rewrites flat → kind-keyed.
	if _, err := MigrateUnified(MigrateUnifiedOpts{Dir: root, RewriteLayers: true}); err != nil {
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
	if _, err := MigrateUnified(MigrateUnifiedOpts{Dir: root, RewriteLayers: true}); err != nil {
		t.Fatalf("migrate 2: %v", err)
	}
	data2, _ := os.ReadFile(filepath.Join(root, "layers", "chrome", "layer.yml"))
	if string(data1) != string(data2) {
		t.Error("second rewrite pass changed the file — not idempotent")
	}
}
