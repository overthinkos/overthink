package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigrateHostCharlyYml proves the per-host deploy.yml → charly.yml rename
// preserves the deploy content, ADDS a version: line (per-host configs predate
// per-file versioning, so they have none), retargets ctx.HostDeployPath (so
// calver-schema stamps the renamed file), and is idempotent / project-only-safe.
func TestMigrateHostCharlyYml(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "deploy.yml")
	newp := filepath.Join(dir, "charly.yml")
	// No version: line — the realistic legacy per-host shape (old BundleConfig
	// had no version field).
	content := "deploy:\n    web:\n        target: pod\n        box: web\n"
	if err := os.WriteFile(old, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := &MigrateContext{HostDeployPath: old}

	changed, err := MigrateHostCharlyYml(ctx)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if !changed {
		t.Fatalf("expected a change (rename), got none")
	}
	if fileExists(old) {
		t.Errorf("legacy deploy.yml should be gone")
	}
	got, err := os.ReadFile(newp)
	if err != nil {
		t.Fatalf("charly.yml not created: %v", err)
	}
	// A version line was prepended (else the unified loader's gate rejects it).
	if firstYAMLVersionLine(got) == "" {
		t.Errorf("migrated charly.yml has no version line:\n%s", got)
	}
	// The original deploy content is preserved.
	if !strings.Contains(string(got), "box: web") {
		t.Errorf("deploy content not preserved:\n%s", got)
	}
	if ctx.HostDeployPath != newp {
		t.Errorf("HostDeployPath = %q, want %q (calver-schema must stamp the renamed file)", ctx.HostDeployPath, newp)
	}

	// Idempotency: a second run (now targeting charly.yml) is a no-op.
	changed2, err := MigrateHostCharlyYml(ctx)
	if err != nil {
		t.Fatalf("migrate (2nd): %v", err)
	}
	if changed2 {
		t.Errorf("second run should be a no-op")
	}

	// Project-only mode (empty HostDeployPath, remote-cache auto-migration) is a no-op.
	changed3, err := MigrateHostCharlyYml(&MigrateContext{HostDeployPath: ""})
	if err != nil || changed3 {
		t.Errorf("project-only mode should be a no-op, got changed=%v err=%v", changed3, err)
	}
}
