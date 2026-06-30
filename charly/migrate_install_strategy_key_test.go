package main

import (
	migrate "github.com/overthinkos/overthink/candy/plugin-migrate"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The rename target MUST equal the live struct's yaml tag — otherwise the
// migrator would rewrite the key to a name the loader still drops. This pins the
// two together so a future tag rename can't silently desync the migrator.
func TestInstallStrategyKey_MatchesStructTag(t *testing.T) {
	f, ok := reflect.TypeOf(VmDeployState{}).FieldByName("CharlyInstallStrategy")
	if !ok {
		t.Fatal("VmDeployState has no CharlyInstallStrategy field")
	}
	tag := f.Tag.Get("yaml")
	name, _, _ := strings.Cut(tag, ",")
	if name != "charly_install_strategy" {
		t.Fatalf("VmDeployState.CharlyInstallStrategy yaml tag = %q, want charly_install_strategy", name)
	}
}

// migrate.MigrateInstallStrategyKey renames the legacy per-host overlay vm_state key
// ov_install_strategy → charly_install_strategy and is idempotent. The overlay
// (node-form by this point in the chain) carries the key under <name>.vm_state.
func TestMigrateInstallStrategyKey_HostOverlay(t *testing.T) {
	dir := t.TempDir()
	overlay := filepath.Join(t.TempDir(), "charly.yml")
	src := `version: "2026.172.0006"
vm:arch:
    vm:
        from: arch
    vm_state:
        ssh_port: 2224
        ov_install_strategy: auto
`
	if err := os.WriteFile(overlay, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := &MigrateContext{Dir: dir, HostDeployPath: overlay}

	changed, err := migrate.MigrateInstallStrategyKey(ctx)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if !changed {
		t.Fatal("expected the overlay to change (legacy key present)")
	}
	out, _ := os.ReadFile(overlay)
	if strings.Contains(string(out), "ov_install_strategy") {
		t.Errorf("legacy key survived:\n%s", out)
	}
	if !strings.Contains(string(out), "charly_install_strategy: auto") {
		t.Errorf("renamed key (with its value) missing:\n%s", out)
	}

	// Idempotent: a second run is a no-op.
	changed, err = migrate.MigrateInstallStrategyKey(ctx)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if changed {
		t.Errorf("migration not idempotent: re-ran and reported a change")
	}
}

// The host-overlay portion self-gates on a non-empty HostDeployPath, mirroring
// unified-node / step-venue: the project-only / remote-cache runner (empty
// HostDeployPath) must never touch per-host state.
func TestMigrateInstallStrategyKey_HostSelfGate(t *testing.T) {
	dir := t.TempDir()
	// A project file carrying the key IS rewritten (project portion always runs).
	if err := os.WriteFile(filepath.Join(dir, "charly.yml"),
		[]byte("version: \"2026.172.0006\"\nx:\n    vm_state:\n        ov_install_strategy: scp\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := &MigrateContext{Dir: dir, HostDeployPath: ""} // project-only mode
	changed, err := migrate.MigrateInstallStrategyKey(ctx)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if !changed {
		t.Fatal("expected the project file to change")
	}
	out, _ := os.ReadFile(filepath.Join(dir, "charly.yml"))
	if strings.Contains(string(out), "ov_install_strategy") || !strings.Contains(string(out), "charly_install_strategy: scp") {
		t.Errorf("project key not renamed:\n%s", out)
	}
}
