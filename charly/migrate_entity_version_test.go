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

	candyDir := filepath.Join(dir, "layers", "ripgrep")
	if err := os.MkdirAll(candyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	candyYML := "layer:\n  name: ripgrep\n  package:\n    - ripgrep\n"
	if err := os.WriteFile(filepath.Join(candyDir, "layer.yml"), []byte(candyYML), 0o644); err != nil {
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

	gotCandy, _ := os.ReadFile(filepath.Join(candyDir, "layer.yml"))
	if !strings.Contains(string(gotCandy), "version: "+seed) {
		t.Errorf("layer.yml missing version after backfill:\n%s", gotCandy)
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

// TestMigrateEntityVersion_SkipsNestedGitRepos proves the walker descends into a
// normal project's OWN box/<name> dirs but SKIPS a nested git repo (a submodule
// checkout, identified generically by a `.git` entry) — so a superproject
// migration never rewrites a submodule's files, independent of WHERE the
// submodule is mounted. The cwd guard keeps the
// walk root's own `.git` in scope (a worktree root carries a `.git` gitfile too).
func TestMigrateEntityVersion_SkipsNestedGitRepos(t *testing.T) {
	dir := t.TempDir()
	seed := "2026.144.1443"
	candyYML := "layer:\n  name: demo\n  package:\n    - demo\n"

	// The walk ROOT itself carries a `.git` (mimics a linked-worktree root, whose
	// `.git` is a gitfile) + an own layer file — both MUST be migrated (the cwd
	// guard keeps the root in scope despite its `.git`).
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /elsewhere/root\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rootFile := filepath.Join(dir, "root.yml")
	if err := os.WriteFile(rootFile, []byte(candyYML), 0o644); err != nil {
		t.Fatal(err)
	}

	// A normal own-box layer dir (NOT a git repo) — MUST be walked + migrated.
	ownFile := filepath.Join(dir, "box", "mybox", "charly.yml")
	if err := os.MkdirAll(filepath.Dir(ownFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ownFile, []byte(candyYML), 0o644); err != nil {
		t.Fatal(err)
	}

	// A nested submodule checkout under box/ (a `.git` gitfile makes it a separate
	// repo) — MUST be skipped even though it lives under box/.
	subFile := filepath.Join(dir, "box", "distro-sub", "charly.yml")
	if err := os.MkdirAll(filepath.Dir(subFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "box", "distro-sub", ".git"), []byte("gitdir: /elsewhere/sub\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(subFile, []byte(candyYML), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := MigrateEntityVersion(dir, seed, false); err != nil {
		t.Fatalf("MigrateEntityVersion: %v", err)
	}

	// Root layer migrated (cwd guard keeps the root's own `.git` in scope).
	if got, _ := os.ReadFile(rootFile); !strings.Contains(string(got), "version: "+seed) {
		t.Errorf("walk root layer was NOT migrated (cwd guard should keep it in scope):\n%s", got)
	}
	// Own box layer migrated (box/ is descended into when it is not a git repo).
	if got, _ := os.ReadFile(ownFile); !strings.Contains(string(got), "version: "+seed) {
		t.Errorf("own box/<name> layer was NOT migrated (should be walked):\n%s", got)
	}
	// Submodule file untouched (nested git repo is skipped).
	if got, _ := os.ReadFile(subFile); strings.Contains(string(got), "version:") {
		t.Errorf("nested git repo file WAS modified (should be skipped):\n%s", got)
	}
}
