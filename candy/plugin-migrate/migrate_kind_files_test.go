package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigrateKindFiles_NeverSplitsInline proves the kind-files step no longer
// SPLITS inline entities into per-kind sibling files — YAML files are generic
// kind-containers and per-kind sibling files are an optional convenience, never
// forced. Even an OLD config keeps its inline layout (which loads fine).
func TestMigrateKindFiles_NeverSplitsInline(t *testing.T) {
	dir := t.TempDir()
	// An OLD-version inline config: previously this would have been split.
	yml := "version: 2026.124.1200\n" +
		"box:\n  bazzite:\n    base: ghcr.io/x:latest\n" +
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
		t.Errorf("inline config with no kind: deployment must be a no-op, got transforms=%v written=%v", res.Transforms, res.WrittenFiles)
	}
	for _, f := range []string{"image.yml", "box.yml", "vm.yml", "pod.yml", "k8s.yml"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			t.Errorf("kind-files created %s — it must NEVER split inline entities into sibling files", f)
		}
	}
	out, _ := os.ReadFile(root)
	if !strings.Contains(string(out), "box:") || !strings.Contains(string(out), "vm:") {
		t.Errorf("inline box:/vm: must survive untouched:\n%s", out)
	}
}

// TestMigrateKindFiles_RenamesDeploymentKind proves the one remaining transform:
// the legacy `kind: deployment` / root-key `deployment:` → `deploy:` rename.
func TestMigrateKindFiles_RenamesDeploymentKind(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "overthink.yml")
	if err := os.WriteFile(root, []byte("version: 2026.124.1200\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	deployFile := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(deployFile, []byte("deployment:\n  web:\n    box: web\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := MigrateKindFiles(dir, false)
	if err != nil {
		t.Fatalf("MigrateKindFiles: %v", err)
	}
	if res.NoChanges {
		t.Fatal("expected the deployment: → deploy: rename, got NoChanges")
	}
	out, _ := os.ReadFile(deployFile)
	if strings.Contains(string(out), "deployment:") || !strings.Contains(string(out), "deploy:") {
		t.Errorf("root-key deployment: must be renamed to deploy::\n%s", out)
	}
}
