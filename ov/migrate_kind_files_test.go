package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigrateKindFiles_SkipsIntentionalInline proves the version-gate: a config
// already AT/PAST the kind-files schema keeps its inline image:/vm: layout (a
// supported terminal layout, e.g. image/bootc) — kind-files must NOT re-split it.
// Without the gate this fails (runMigrations runs every step, so the step would
// split the inline blocks into sibling files on every `ov migrate`).
func TestMigrateKindFiles_SkipsIntentionalInline(t *testing.T) {
	dir := t.TempDir()
	// version >= kindFilesSchemaVersion → intentional inline, leave it.
	yml := "version: " + LatestSchemaVersion().String() + "\n" +
		"image:\n  bazzite:\n    base: ghcr.io/x:latest\n" +
		"vm:\n  bazzite-bootc:\n    source:\n      kind: bootc\n"
	root := filepath.Join(dir, "overthink.yml")
	if err := os.WriteFile(root, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := MigrateKindFiles(dir, false)
	if err != nil {
		t.Fatalf("MigrateKindFiles: %v", err)
	}
	if !res.NoChanges {
		t.Errorf("post-cutover inline config must be a no-op, got transforms=%v written=%v", res.Transforms, res.WrittenFiles)
	}
	for _, f := range []string{"image.yml", "vm.yml", "pod.yml", "k8s.yml"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			t.Errorf("kind-files created %s on a post-cutover inline config — must leave the inline layout untouched", f)
		}
	}
	out, _ := os.ReadFile(root)
	if !strings.Contains(string(out), "image:") || !strings.Contains(string(out), "vm:") {
		t.Errorf("inline image:/vm: must survive untouched:\n%s", out)
	}
}

// TestMigrateKindFiles_SplitsLegacyInline proves the gate does NOT break the
// legacy migration: a config OLDER than the kind-files schema still gets its
// inline image: split into image.yml.
func TestMigrateKindFiles_SplitsLegacyInline(t *testing.T) {
	dir := t.TempDir()
	// version < kindFilesSchemaVersion (2026.125.2355) → legacy, split it.
	yml := "version: 2026.124.1200\n" +
		"image:\n  foo:\n    base: ghcr.io/x:latest\n"
	root := filepath.Join(dir, "overthink.yml")
	if err := os.WriteFile(root, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := MigrateKindFiles(dir, false)
	if err != nil {
		t.Fatalf("MigrateKindFiles: %v", err)
	}
	if res.NoChanges {
		t.Fatal("legacy inline config must be split, got NoChanges")
	}
	if _, err := os.Stat(filepath.Join(dir, "image.yml")); err != nil {
		t.Errorf("legacy inline image: must split into image.yml: %v", err)
	}
	out, _ := os.ReadFile(root)
	if strings.HasPrefix(string(out), "image:") || strings.Contains(string(out), "\nimage:\n") {
		t.Errorf("inline image: must be removed from overthink.yml after the split:\n%s", out)
	}
}
