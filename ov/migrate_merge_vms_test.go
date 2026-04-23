package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// migrate_merge_vms_test.go — tests for `ov migrate merge-vms`.

const legacyV1Overthink = `version: 1
includes:
    - build.yml
    - images.yml
    - vms.yml
    - deploy.yml
`

const legacyVmsYml = `vms:
    arch-cloud-base:
        disposable: true
        lifecycle: dev
        source:
            kind: cloud_image
            url: https://example.invalid/arch.qcow2
        disk_size: 40G
        ssh:
            port: 2224
`

const legacyDeployYml = `deployments:
    images:
        "vm:arch-cloud-base":
            target: vm
            vm_source: arch-cloud-base
            add_layers:
                - ripgrep
`

func writeV1Fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "overthink.yml"), legacyV1Overthink)
	mustWrite(t, filepath.Join(dir, "vms.yml"), legacyVmsYml)
	mustWrite(t, filepath.Join(dir, "deploy.yml"), legacyDeployYml)
	return dir
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

func TestMigrateMergeVms_HappyPath(t *testing.T) {
	dir := writeV1Fixture(t)
	changed, err := MigrateMergeVms(MigrateMergeVmsOpts{Dir: dir})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(changed) == 0 {
		t.Fatal("expected at least one file changed, got none")
	}

	// overthink.yml: version 2, vms.yml removed from includes.
	overthinkBody := mustRead(t, filepath.Join(dir, "overthink.yml"))
	if !strings.Contains(overthinkBody, "version: 2") {
		t.Errorf("overthink.yml: missing version: 2\n--\n%s", overthinkBody)
	}
	if strings.Contains(overthinkBody, "- vms.yml") {
		t.Errorf("overthink.yml: vms.yml still listed in includes\n--\n%s", overthinkBody)
	}

	// vms.yml file is gone.
	if _, err := os.Stat(filepath.Join(dir, "vms.yml")); !os.IsNotExist(err) {
		t.Errorf("vms.yml should be deleted, got err=%v", err)
	}

	// deploy.yml: contains new `vm:` root key, references `arch`, NO `arch-cloud-base`.
	deployBody := mustRead(t, filepath.Join(dir, "deploy.yml"))
	if !strings.Contains(deployBody, "\nvm:\n") && !strings.HasPrefix(deployBody, "vm:") {
		t.Errorf("deploy.yml: missing new `vm:` root key\n--\n%s", deployBody)
	}
	if strings.Contains(deployBody, "arch-cloud-base") {
		t.Errorf("deploy.yml: legacy arch-cloud-base string still present\n--\n%s", deployBody)
	}
	if !strings.Contains(deployBody, "vm_source: arch") {
		t.Errorf("deploy.yml: missing `vm_source: arch` rewrite\n--\n%s", deployBody)
	}
	if !strings.Contains(deployBody, "\"vm:arch\"") {
		t.Errorf("deploy.yml: missing `vm:arch` deploy-key rewrite\n--\n%s", deployBody)
	}
}

func TestMigrateMergeVms_Idempotent(t *testing.T) {
	dir := writeV1Fixture(t)
	// First run: does the migration.
	if _, err := MigrateMergeVms(MigrateMergeVmsOpts{Dir: dir}); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	// Second run: must be a no-op.
	changed, err := MigrateMergeVms(MigrateMergeVmsOpts{Dir: dir})
	if err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if len(changed) != 0 {
		t.Errorf("second run should be no-op, got changed=%v", changed)
	}
}

func TestMigrateMergeVms_DryRun(t *testing.T) {
	dir := writeV1Fixture(t)
	// Capture overthink.yml before the dry-run.
	before := mustRead(t, filepath.Join(dir, "overthink.yml"))

	_, err := MigrateMergeVms(MigrateMergeVmsOpts{Dir: dir, DryRun: true})
	if err != nil {
		t.Fatalf("dry-run migrate: %v", err)
	}

	// Verify no file was actually modified.
	after := mustRead(t, filepath.Join(dir, "overthink.yml"))
	if before != after {
		t.Errorf("dry-run mutated overthink.yml")
	}
	if _, err := os.Stat(filepath.Join(dir, "vms.yml")); os.IsNotExist(err) {
		t.Errorf("dry-run deleted vms.yml")
	}
}

func TestMigrateMergeVms_NoMarkers(t *testing.T) {
	// Fresh v2 fixture — no legacy markers anywhere. Migration should
	// return nil, nil immediately.
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "overthink.yml"), "version: 2\nincludes: [deploy.yml]\n")
	mustWrite(t, filepath.Join(dir, "deploy.yml"), "deployments:\n  images: {}\nvm: {}\n")
	changed, err := MigrateMergeVms(MigrateMergeVmsOpts{Dir: dir})
	if err != nil {
		t.Fatalf("migrate on clean v2: %v", err)
	}
	if len(changed) != 0 {
		t.Errorf("clean v2 should report no changes, got %v", changed)
	}
}

func TestMigrateMergeVms_LegacyVersionBumpOnly(t *testing.T) {
	// Edge case: overthink.yml v1 but no vms.yml on disk, no deploy.yml.
	// The migration should still fire (version marker) and produce a
	// v2 overthink.yml — without accidentally creating an empty
	// deploy.yml as a side effect.
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "overthink.yml"), "version: 1\nincludes:\n  - build.yml\n")
	changed, err := MigrateMergeVms(MigrateMergeVmsOpts{Dir: dir})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(changed) == 0 {
		t.Fatal("version-only bump should report overthink.yml as changed")
	}
	body := mustRead(t, filepath.Join(dir, "overthink.yml"))
	if !strings.Contains(body, "version: 2") {
		t.Errorf("version not bumped: %s", body)
	}
	if _, err := os.Stat(filepath.Join(dir, "deploy.yml")); err == nil {
		t.Errorf("migration should not create a spurious deploy.yml when none existed")
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(b)
}
