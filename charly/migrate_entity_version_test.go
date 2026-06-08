package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigrateEntityVersion covers the per-entity-version backfill: every
// layer.yml gets a version inside its `layer:` map; a bare-base image entry
// (no layer:, external base:) gets a dedicated version; a layered image and an
// internal-base image are left UNVERSIONED (they derive); the document-root
// schema stamp is NEVER touched; and the migration is idempotent.
func TestMigrateEntityVersion(t *testing.T) {
	dir := t.TempDir()
	seed := "2026.144.1443"

	layerDir := filepath.Join(dir, "layers", "ripgrep")
	if err := os.MkdirAll(layerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	layerYML := "layer:\n  name: ripgrep\n  package:\n    - ripgrep\n"
	if err := os.WriteFile(filepath.Join(layerDir, "layer.yml"), []byte(layerYML), 0o644); err != nil {
		t.Fatal(err)
	}

	// base.yml: a root schema stamp + a bare base (external) + a layered image +
	// an internal-base image. Only `arch` (bare external base) should be backfilled.
	baseYML := "version: 2026.143.844\n" +
		"image:\n" +
		"    arch:\n" +
		"        base: quay.io/archlinux/archlinux:latest\n" +
		"        distro:\n" +
		"            - arch\n" +
		"    arch-builder:\n" +
		"        base: arch\n" +
		"        layer:\n" +
		"            - pixi\n" +
		"    versa:\n" +
		"        base: cachyos.cachyos\n" +
		"        layer:\n" +
		"            - marimo\n"
	if err := os.WriteFile(filepath.Join(dir, "base.yml"), []byte(baseYML), 0o644); err != nil {
		t.Fatal(err)
	}

	results, err := MigrateEntityVersion(dir, seed, false)
	if err != nil {
		t.Fatalf("MigrateEntityVersion: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 modified files (layer.yml + base.yml), got %d: %+v", len(results), results)
	}

	gotLayer, _ := os.ReadFile(filepath.Join(layerDir, "layer.yml"))
	if !strings.Contains(string(gotLayer), "version: "+seed) {
		t.Errorf("layer.yml missing version after backfill:\n%s", gotLayer)
	}

	gotBase, _ := os.ReadFile(filepath.Join(dir, "base.yml"))
	gb := string(gotBase)
	// Root schema stamp untouched.
	if !strings.HasPrefix(gb, "version: 2026.143.844\n") {
		t.Errorf("root schema stamp was modified:\n%s", gb)
	}
	// arch (bare external base) backfilled exactly once.
	if n := strings.Count(gb, "version: "+seed); n != 1 {
		t.Errorf("expected exactly 1 per-entity version (arch) in base.yml, got %d:\n%s", n, gb)
	}
	// The per-entity version must sit under the arch entry, not on arch-builder/versa.
	archIdx := strings.Index(gb, "arch:")
	builderIdx := strings.Index(gb, "arch-builder:")
	verIdx := strings.Index(gb, "version: "+seed)
	if !(verIdx > archIdx && verIdx < builderIdx) {
		t.Errorf("per-entity version not placed under the arch entry (verIdx=%d archIdx=%d builderIdx=%d):\n%s", verIdx, archIdx, builderIdx, gb)
	}

	// Idempotent: a second run changes nothing.
	results2, err := MigrateEntityVersion(dir, seed, false)
	if err != nil {
		t.Fatalf("second MigrateEntityVersion: %v", err)
	}
	if len(results2) != 0 {
		t.Errorf("second run should be a no-op, got %d changes: %+v", len(results2), results2)
	}
}
