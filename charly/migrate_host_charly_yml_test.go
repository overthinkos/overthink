package main

import (
	"bytes"
	migrate "github.com/overthinkos/overthink/candy/plugin-migrate"
	"os"
	"path/filepath"
	"sort"
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

	changed, err := migrate.MigrateHostCharlyYml(ctx)
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
	changed2, err := migrate.MigrateHostCharlyYml(ctx)
	if err != nil {
		t.Fatalf("migrate (2nd): %v", err)
	}
	if changed2 {
		t.Errorf("second run should be a no-op")
	}

	// Project-only mode (empty HostDeployPath, remote-cache auto-migration) is a no-op.
	changed3, err := migrate.MigrateHostCharlyYml(&MigrateContext{HostDeployPath: ""})
	if err != nil || changed3 {
		t.Errorf("project-only mode should be a no-op, got changed=%v err=%v", changed3, err)
	}
}

// migrateStepByName returns the registry step with the given Name (test helper
// so a test can exercise a SPECIFIC slice of the chain — host-affecting steps
// only — without running steps like charly-rebrand that touch the REAL
// ~/.config dirs).
func migrateStepByName(t *testing.T, name string) migrate.MigrationStep {
	t.Helper()
	for _, s := range migrate.MigrationSteps() {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no migration step named %q", name)
	return migrate.MigrationStep{}
}

// TestMigrate_HostOverlayConvertsToNodeForm is the regression guard for the
// per-host migrate-path bug: a legacy per-host deploy overlay (a `version`-less
// `deploy:` map at ~/.config/charly/deploy.yml) MUST be brought to node-form at
// the RUNTIME path the loader reads (~/.config/charly/charly.yml) by the migrate
// chain — rename (host-charly-yml) → node-form conversion (unified-node +
// step-venue) → HEAD stamp (calver-schema) — and the result MUST load through
// the SAME unified loader, idempotently.
//
// Pre-fix: unified-node + step-venue ran runDocMigration over PROJECT files only
// and never touched ctx.HostDeployPath, so the overlay ended up `version: <HEAD>`
// (calver-schema stamped it) + a legacy `deploy:` map — which the node-form
// loader gate HARD-REJECTS, and migrate could not fix.
func TestMigrate_HostOverlayConvertsToNodeForm(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	cfgDir := filepath.Join(dir, "charly")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// The realistic legacy per-host shape: no version: line, a top-level
	// `deploy:` map (one entry carrying a non-scalar `volume:` to prove the
	// node-form child-explosion reaches the host overlay too), still on the
	// legacy deploy.yml filename.
	legacy := "deploy:\n" +
		"    web:\n" +
		"        target: pod\n" +
		"        box: web\n" +
		"        volume:\n" +
		"            - name: data\n" +
		"              type: bind\n" +
		"              host: ~/web-data\n" +
		"    api:\n" +
		"        target: pod\n" +
		"        box: api\n"
	deployPath := filepath.Join(cfgDir, "deploy.yml")
	if err := os.WriteFile(deployPath, []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy overlay: %v", err)
	}

	// ctx mirrors the post-rebrand state where the chain has already relocated
	// ~/.config/ov → ~/.config/charly: HostDeployPath points at the charly-dir
	// deploy.yml (host-charly-yml does the deploy.yml→charly.yml rename). Dir is
	// an EMPTY project so the project-dir half of each step is a no-op.
	ctx := &MigrateContext{Dir: t.TempDir(), HostDeployPath: deployPath}

	// Apply exactly the host-affecting node-form slice of the chain, in order.
	hostChainNames := []string{"host-charly-yml", "unified-node", "step-venue", "edge-inherit", "calver-schema"}
	for _, name := range hostChainNames {
		step := migrateStepByName(t, name)
		if _, err := step.Apply(ctx); err != nil {
			t.Fatalf("step %s: %v", name, err)
		}
	}

	// The overlay now lives at the runtime path the loader reads.
	runtimePath := filepath.Join(cfgDir, "charly.yml")
	if ctx.HostDeployPath != runtimePath {
		t.Errorf("HostDeployPath = %q, want runtime path %q", ctx.HostDeployPath, runtimePath)
	}
	if fileExists(deployPath) {
		t.Errorf("legacy deploy.yml should be gone after rename")
	}
	raw, err := os.ReadFile(runtimePath)
	if err != nil {
		t.Fatalf("read migrated overlay: %v", err)
	}
	// No residual legacy top-level `deploy:` map; version stamped to HEAD.
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "deploy:" {
			t.Errorf("residual legacy top-level deploy: map in migrated overlay:\n%s", raw)
		}
	}
	if got := firstYAMLVersionLine(raw); got != LatestSchemaVersion().String() {
		t.Errorf("version = %q, want HEAD %q\n%s", got, LatestSchemaVersion().String(), raw)
	}

	// THE key assertion: the migrated overlay loads cleanly through the unified
	// loader (DeployConfigPath → ~/.config/charly/charly.yml under XDG override),
	// and the deploy content survived the node-form conversion.
	dc, err := LoadBundleConfig()
	if err != nil {
		t.Fatalf("migrated overlay still rejected by the loader: %v\n%s", err, raw)
	}
	if dc == nil {
		t.Fatal("LoadBundleConfig returned nil after migration")
	}
	web, ok := dc.Bundle["web"]
	if !ok {
		t.Fatalf("web deploy entry lost in node-form conversion; got keys %v", bundleKeys(dc))
	}
	if web.Image != "web" {
		t.Errorf("web.Image = %q, want %q", web.Image, "web")
	}
	if len(web.Volume) == 0 || web.Volume[0].Name != "data" {
		t.Errorf("web volume child not preserved: %+v", web.Volume)
	}
	if _, ok := dc.Bundle["api"]; !ok {
		t.Errorf("api deploy entry lost; got keys %v", bundleKeys(dc))
	}

	// Idempotency: re-running the node-form + stamp steps is a byte-identical
	// no-op (the loader already accepts the file; nothing left to convert).
	before, _ := os.ReadFile(runtimePath)
	for _, name := range []string{"unified-node", "step-venue", "calver-schema"} {
		step := migrateStepByName(t, name)
		changed, err := step.Apply(ctx)
		if err != nil {
			t.Fatalf("idempotent re-run step %s: %v", name, err)
		}
		if changed {
			t.Errorf("step %s reported a change on the already-migrated overlay (not idempotent)", name)
		}
	}
	after, _ := os.ReadFile(runtimePath)
	if !bytes.Equal(before, after) {
		t.Errorf("second migrate run mutated the overlay\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
}

// bundleKeys returns the sorted deploy-entry keys of dc (test diagnostics).
func bundleKeys(dc *BundleConfig) []string {
	if dc == nil {
		return nil
	}
	out := make([]string, 0, len(dc.Bundle))
	for k := range dc.Bundle {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
