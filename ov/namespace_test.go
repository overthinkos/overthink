package main

import (
	"strings"
	"testing"
)

// TestImportList_Unmarshal covers the mixed-shape import list: bare strings
// (flat root imports) and single-key maps (namespaced child imports).
func TestImportList_Unmarshal(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "overthink.yml", `version: 2026.143.844
import:
  - build.yml
  - sub: ./sub.yml
`)
	writeFixture(t, root, "build.yml", `defaults:
  build: [rpm]
`)
	writeFixture(t, root, "sub.yml", `version: 2026.143.844
image:
  widget:
    base: quay.io/fedora/fedora:43
    distro: [fedora]
`)
	uf, _, err := LoadUnified(root)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	// Flat import merged build.yml into root.
	if len(uf.Defaults.Build) != 1 || uf.Defaults.Build[0] != "rpm" {
		t.Errorf("flat import not merged: Defaults.Build = %v", uf.Defaults.Build)
	}
	// Namespaced import mounted under "sub", NOT flat-merged into root.
	if _, leaked := uf.Image["widget"]; leaked {
		t.Error("namespaced entry leaked into root Image map")
	}
	if uf.Namespaces["sub"] == nil {
		t.Fatal("namespace 'sub' not mounted")
	}
	if _, ok := uf.Namespaces["sub"].Image["widget"]; !ok {
		t.Error("sub.widget missing from the 'sub' namespace")
	}
}

// TestResolveImageRef_Qualified checks namespace-relative resolution of a
// qualified image ref through the projected Config.
func TestResolveImageRef_Qualified(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "overthink.yml", `version: 2026.143.844
import:
  - sub: ./sub.yml
image:
  app:
    base: sub.widget
    distro: [fedora]
    build: [rpm]
    layer: []
`)
	writeFixture(t, root, "sub.yml", `version: 2026.143.844
image:
  widget:
    base: quay.io/fedora/fedora:43
    distro: [fedora]
    build: [rpm]
`)
	uf, _, err := LoadUnified(root)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	cfg := uf.ProjectConfig()
	// Bare local name resolves in root.
	if _, _, ok := cfg.resolveImageRef("app"); !ok {
		t.Error("bare ref 'app' did not resolve in root")
	}
	// Qualified ref descends into the namespace.
	wImg, wCfg, ok := cfg.resolveImageRef("sub.widget")
	if !ok {
		t.Fatal("qualified ref 'sub.widget' did not resolve")
	}
	if wImg.Base != "quay.io/fedora/fedora:43" {
		t.Errorf("sub.widget base = %q", wImg.Base)
	}
	if wCfg == cfg {
		t.Error("qualified ref should resolve in the sub-namespace Config, not root")
	}
	// app's base (sub.widget) must be classified INTERNAL (resolves via namespace),
	// not mistaken for an external OCI URL.
	ri, err := cfg.ResolveImage("app", "test", root, ResolveOpts{})
	if err != nil {
		t.Fatalf("ResolveImage(app): %v", err)
	}
	if ri.IsExternalBase {
		t.Error("app.base = sub.widget should be IsExternalBase=false (resolved through namespace)")
	}
}

// TestLoadUnified_RejectInclude confirms the deleted `include:` key is a hard
// load-time error pointing at ov migrate.
func TestLoadUnified_RejectInclude(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "overthink.yml", `version: 2026.143.844
include:
  - build.yml
`)
	writeFixture(t, root, "build.yml", `defaults: {build: [rpm]}
`)
	_, _, err := LoadUnified(root)
	if err == nil {
		t.Fatal("expected hard error for residual include: key")
	}
	if !strings.Contains(err.Error(), "import:") || !strings.Contains(err.Error(), "ov migrate") {
		t.Errorf("error %q should mention import: and 'ov migrate'", err)
	}
}

// TestMigrateImportNamespace renames include: -> import: idempotently.
func TestMigrateImportNamespace(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "overthink.yml", `version: 2026.141.1600
include:
  - build.yml
  - image.yml
`)
	changed, err := MigrateImportNamespace(dir, false)
	if err != nil {
		t.Fatalf("MigrateImportNamespace: %v", err)
	}
	if len(changed) != 1 || changed[0] != "overthink.yml" {
		t.Errorf("changed = %v, want [overthink.yml]", changed)
	}
	// Idempotent: a second run is a no-op.
	changed2, err := MigrateImportNamespace(dir, false)
	if err != nil {
		t.Fatalf("MigrateImportNamespace (2nd): %v", err)
	}
	if len(changed2) != 0 {
		t.Errorf("second run changed %v, want no-op", changed2)
	}
}

// TestImportNamespace_MutualCycle verifies the main<->sub mutual import is
// cycle-broken at load (the shared resolved-ref cache).
func TestImportNamespace_MutualCycle(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "overthink.yml", `version: 2026.143.844
import:
  - sub: ./sub
image:
  app:
    base: sub.widget
    distro: [fedora]
    build: [rpm]
`)
	writeFixture(t, root, "sub/overthink.yml", `version: 2026.143.844
import:
  - up: ../
image:
  widget:
    base: quay.io/fedora/fedora:43
    distro: [fedora]
    build: [rpm]
`)
	uf, _, err := LoadUnified(root)
	if err != nil {
		t.Fatalf("LoadUnified (mutual import must not loop): %v", err)
	}
	if uf.Namespaces["sub"] == nil || uf.Namespaces["sub"].Namespaces["up"] == nil {
		t.Fatal("mutual import namespaces not mounted")
	}
}
