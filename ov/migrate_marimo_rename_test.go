package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestApplyMarimoRenameRewrites_FullExample exercises every shape the
// rename has to handle in one go: top-level deploy key, top-level image
// key, image cross-ref, free-form description text, comment line. Asserts
// the longest-first ordering collapses cleanly.
func TestApplyMarimoRenameRewrites_FullExample(t *testing.T) {
	legacy := `version: 4
image:
    marimo-ml:
        base: cachyos
        layer:
            - marimo
        port:
            - 2718:2718
deploy:
    marimo-ml-pod:
        target: pod
        image: marimo-ml
        disposable: true
        description:
            feature: "R10 bed for marimo-ml — exercises the full stack"
        port:
            - 22718:2718
# The marimo-ml-pod deploy is the canonical dev bed for marimo-ml.
`
	got := applyMarimoRenameRewrites(legacy)
	for _, needle := range []string{"marimo-ml-pod", "marimo-ml"} {
		if strings.Contains(got, needle) {
			t.Errorf("rewrite left residual %q in output:\n%s", needle, got)
		}
	}
	for _, needle := range []string{
		"    versa:\n        base: cachyos",
		"    versa:\n        target: pod",
		"        image: versa\n",
		"R10 bed for versa — exercises the full stack",
		"# The versa deploy is the canonical dev bed for versa.",
	} {
		if !strings.Contains(got, needle) {
			t.Errorf("rewrite missing expected %q in output:\n%s", needle, got)
		}
	}
}

// TestApplyMarimoRenameRewrites_Idempotent confirms that running the
// rewrite on an already-migrated file is a no-op. This is the load-
// bearing safety property: ov migrate can be run
// repeatedly without corrupting the canonical form.
func TestApplyMarimoRenameRewrites_Idempotent(t *testing.T) {
	post := `version: 4
image:
    versa:
        base: cachyos
deploy:
    versa:
        target: pod
        image: versa
`
	first := applyMarimoRenameRewrites(post)
	if first != post {
		t.Errorf("rewrite changed already-canonical input:\n--- before:\n%s\n--- after:\n%s", post, first)
	}
	second := applyMarimoRenameRewrites(first)
	if second != first {
		t.Errorf("rewrite is not idempotent across two runs")
	}
}

// TestMigrateMarimoRename_FileWriting exercises the full file walk +
// write path. Sets up a temp dir with a legacy deploy.yml + image.yml,
// runs the migration, asserts the on-disk content is canonical. HOME
// is sandboxed to a separate temp dir so the migration's per-machine
// ~/.config/ov/deploy.yml walk doesn't leak into the user's real file.
func TestMigrateMarimoRename_FileWriting(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	deployPath := filepath.Join(dir, "deploy.yml")
	imagePath := filepath.Join(dir, "image.yml")

	if err := os.WriteFile(deployPath, []byte(`deploy:
    marimo-ml-pod:
        target: pod
        image: marimo-ml
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(imagePath, []byte(`image:
    marimo-ml:
        base: cachyos
`), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := MigrateMarimoRename(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 2 {
		t.Errorf("expected 2 files modified, got %d: %v", len(changed), changed)
	}

	// Run twice — second invocation is the idempotency check at the
	// filesystem level.
	changed2, err := MigrateMarimoRename(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed2) != 0 {
		t.Errorf("second run should be a no-op, modified %d files: %v", len(changed2), changed2)
	}

	deployOut, _ := os.ReadFile(deployPath)
	imageOut, _ := os.ReadFile(imagePath)
	if strings.Contains(string(deployOut), "marimo-ml") {
		t.Errorf("deploy.yml still contains marimo-ml after migration:\n%s", deployOut)
	}
	if strings.Contains(string(imageOut), "marimo-ml") {
		t.Errorf("image.yml still contains marimo-ml after migration:\n%s", imageOut)
	}
}

// TestMigrateMarimoRename_DryRun confirms that --dry-run reports the
// would-be modifications but never touches the filesystem. HOME is
// sandboxed so the user's real ~/.config/ov/deploy.yml is not read or
// written even by accident.
func TestMigrateMarimoRename_DryRun(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	deployPath := filepath.Join(dir, "deploy.yml")
	legacy := `deploy:
    marimo-ml-pod:
        target: pod
        image: marimo-ml
`
	if err := os.WriteFile(deployPath, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := MigrateMarimoRename(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 1 || changed[0] != deployPath {
		t.Errorf("dry-run should report deploy.yml as would-modify, got %v", changed)
	}
	got, _ := os.ReadFile(deployPath)
	if string(got) != legacy {
		t.Errorf("dry-run mutated the file on disk:\n%s", got)
	}
}
