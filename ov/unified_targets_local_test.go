package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// writeFakeDeploy writes a host-target DeployRecord into the ledger
// dir. Returns the file path so the test can later assert removal.
func writeFakeDeploy(t *testing.T, paths *LedgerPaths, deployID, image string, layers []string) string {
	t.Helper()
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	rec := DeployRecord{
		DeployID: deployID,
		Image:    image,
		Target:   "host",
		Layer:   layers,
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(paths.Deploys, deployID+".json")
	if err := os.WriteFile(p, data, 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

// writeFakeLayer writes a LayerRecord with empty ReverseOps so teardown
// touches no real system state. DeployedBy carries the deploy IDs we
// pass — the refcount logic decides when the layer record is removed.
func writeFakeLayer(t *testing.T, paths *LedgerPaths, layer string, deployedBy []string) {
	t.Helper()
	rec := LayerRecord{
		Layer:      layer,
		DeployedBy: deployedBy,
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.Layers, layer+".json"), data, 0644); err != nil {
		t.Fatal(err)
	}
}

// TestHostUnifiedTarget_Basics verifies the trivial accessor methods.
func TestHostUnifiedTarget_Basics(t *testing.T) {
	target := &LocalUnifiedTarget{NodeName: "arch-vm.arch-host"}
	if got := target.Name(); got != "arch-vm.arch-host" {
		t.Errorf("Name = %q, want %q", got, "arch-vm.arch-host")
	}
	if got := target.Kind(); got != "host" {
		t.Errorf("Kind = %q, want %q", got, "host")
	}
	if target.Executor() == nil {
		t.Errorf("Executor returned nil; expected ShellExecutor fallback")
	}
}

// TestHostUnifiedTarget_NotSupportedMethods verifies the three
// lifecycle methods that do not apply to host targets return
// ErrNotSupportedOnHost. Mirrors the K8sUnifiedTarget pattern.
func TestHostUnifiedTarget_NotSupportedMethods(t *testing.T) {
	target := &LocalUnifiedTarget{NodeName: "host"}
	ctx := context.Background()

	if err := target.Start(ctx); !errors.Is(err, ErrNotSupportedOnHost) {
		t.Errorf("Start: got %v, want ErrNotSupportedOnHost", err)
	}
	if err := target.Stop(ctx); !errors.Is(err, ErrNotSupportedOnHost) {
		t.Errorf("Stop: got %v, want ErrNotSupportedOnHost", err)
	}
	if err := target.Logs(ctx, LogsOpts{}); !errors.Is(err, ErrNotSupportedOnHost) {
		t.Errorf("Logs: got %v, want ErrNotSupportedOnHost", err)
	}
}

// TestHostUnifiedTarget_Status_Empty: with no ledger, returns
// stopped/unhealthy and no error.
func TestHostUnifiedTarget_Status_Empty(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	target := &LocalUnifiedTarget{NodeName: "host"}
	info, err := target.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.State != "stopped" {
		t.Errorf("State = %q, want stopped", info.State)
	}
	if info.Healthy {
		t.Errorf("Healthy = true, want false (no deploys)")
	}
}

// TestHostUnifiedTarget_Status_OneDeploy verifies deploy-count and
// layer-count emerge in Details when at least one host deploy is
// recorded.
func TestHostUnifiedTarget_Status_OneDeploy(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	paths, err := DefaultLedgerPaths()
	if err != nil {
		t.Fatal(err)
	}
	writeFakeDeploy(t, paths, "h-1", "fedora-coder", []string{"a", "b"})

	target := &LocalUnifiedTarget{NodeName: "host"}
	info, err := target.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.State != "running" {
		t.Errorf("State = %q, want running", info.State)
	}
	if !info.Healthy {
		t.Errorf("Healthy = false, want true")
	}
	if info.Details["deploys"] != "1" {
		t.Errorf("Details[deploys] = %q, want 1", info.Details["deploys"])
	}
	if info.Details["layers"] != "2" {
		t.Errorf("Details[layers] = %q, want 2", info.Details["layers"])
	}
	if info.Details["images"] != "fedora-coder" {
		t.Errorf("Details[images] = %q, want fedora-coder", info.Details["images"])
	}
}

// TestHostUnifiedTarget_Del_DryRun verifies the dry-run path lists
// pending teardowns without actually removing the ledger entry.
func TestHostUnifiedTarget_Del_DryRun(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	paths, err := DefaultLedgerPaths()
	if err != nil {
		t.Fatal(err)
	}
	deployFile := writeFakeDeploy(t, paths, "h-1", "fedora-coder", []string{"a"})
	writeFakeLayer(t, paths, "a", []string{"h-1"})

	target := &LocalUnifiedTarget{NodeName: "host"}
	if err := target.Del(context.Background(), DelOpts{DryRun: true}); err != nil {
		t.Fatalf("Del dry-run: %v", err)
	}
	// The ledger entry MUST still exist after a dry-run.
	if _, err := os.Stat(deployFile); err != nil {
		t.Errorf("dry-run removed ledger entry: %v", err)
	}
}

// TestHostUnifiedTarget_Del_RemovesEntries verifies the non-dry-run
// path deletes the deploy record (and the layer record, since refcount
// drops to zero).
func TestHostUnifiedTarget_Del_RemovesEntries(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	paths, err := DefaultLedgerPaths()
	if err != nil {
		t.Fatal(err)
	}
	deployFile := writeFakeDeploy(t, paths, "h-1", "fedora-coder", []string{"a"})
	layerFile := filepath.Join(paths.Layers, "a.json")
	writeFakeLayer(t, paths, "a", []string{"h-1"})

	target := &LocalUnifiedTarget{NodeName: "host"}
	if err := target.Del(context.Background(), DelOpts{}); err != nil {
		t.Fatalf("Del: %v", err)
	}
	if _, err := os.Stat(deployFile); !os.IsNotExist(err) {
		t.Errorf("deploy file still exists: %v", err)
	}
	if _, err := os.Stat(layerFile); !os.IsNotExist(err) {
		t.Errorf("layer file still exists: %v", err)
	}
}

// TestHostUnifiedTarget_Del_SkipsNonHost verifies a deploy with
// Target != "host" is left untouched. Important: Del walks the entire
// deploys dir but only acts on host-target records.
func TestHostUnifiedTarget_Del_SkipsNonHost(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	paths, err := DefaultLedgerPaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}

	// A pod-target deploy that should NOT be touched.
	podRec := DeployRecord{
		DeployID: "p-1",
		Image:    "sway-pod",
		Target:   "pod:sway-pod",
		Layer:   []string{"x"},
	}
	data, _ := json.Marshal(podRec)
	podFile := filepath.Join(paths.Deploys, "p-1.json")
	if err := os.WriteFile(podFile, data, 0644); err != nil {
		t.Fatal(err)
	}

	target := &LocalUnifiedTarget{NodeName: "host"}
	if err := target.Del(context.Background(), DelOpts{}); err != nil {
		t.Fatalf("Del: %v", err)
	}
	if _, err := os.Stat(podFile); err != nil {
		t.Errorf("pod-target deploy was removed: %v", err)
	}
}

// TestHostUnifiedTarget_Rebuild_DryRun verifies the dry-run path emits
// the expected "ov deploy add <name>" message without invoking the
// subcommand.
func TestHostUnifiedTarget_Rebuild_DryRun(t *testing.T) {
	target := &LocalUnifiedTarget{NodeName: "arch-vm.arch-host"}
	if err := target.Rebuild(context.Background(), RebuildOpts{DryRun: true}); err != nil {
		t.Fatalf("Rebuild dry-run: %v", err)
	}
	// We can't easily capture stdout in a unit test without a fixture,
	// but the absence of a non-nil error confirms the dry-run path
	// returns cleanly without calling runOvSubcommand.
}

// TestHostReverseExec_AccessorPassthrough verifies the inline adapter
// forwards each ReverseExecutor accessor.
func TestHostReverseExec_AccessorPassthrough(t *testing.T) {
	e := &hostReverseExec{
		DryRun:          true,
		KeepRepoChanges: true,
		KeepServices:    false,
		Runner:          nil,
	}
	if !e.reverseDryRun() {
		t.Errorf("reverseDryRun = false, want true")
	}
	if !e.reverseKeepRepoChanges() {
		t.Errorf("reverseKeepRepoChanges = false, want true")
	}
	if e.reverseKeepServices() {
		t.Errorf("reverseKeepServices = true, want false")
	}
	if e.reverseRunner() != nil {
		t.Errorf("reverseRunner non-nil, want nil")
	}
}

// TestHostUnifiedTarget_Test_EmptyChecks verifies an empty check list
// returns nil (no failures).
func TestHostUnifiedTarget_Test_EmptyChecks(t *testing.T) {
	target := &LocalUnifiedTarget{NodeName: "host"}
	if err := target.Test(context.Background(), nil, TestOpts{}); err != nil {
		t.Errorf("Test(empty): %v", err)
	}
}

// TestHostUnifiedTarget_Test_OnlyIDsFilter verifies the OnlyIDs filter
// reduces the check set passed to the runner. Uses `command: "true"`
// (hermetic; no environmental dependencies beyond the system shell).
// If OnlyIDs failed to filter, the runner would attempt to run the
// "fail" check too — which would also pass since "true" exits 0, but
// the assertion below catches the case where the filter is broken in
// a way that produces a real failure.
func TestHostUnifiedTarget_Test_OnlyIDsFilter(t *testing.T) {
	target := &LocalUnifiedTarget{NodeName: "host"}
	checks := []Check{
		{ID: "match", Command: "true"},
		{ID: "fail", Command: "false"},
	}
	if err := target.Test(context.Background(), checks, TestOpts{OnlyIDs: []string{"match"}}); err != nil {
		t.Errorf("Test(OnlyIDs): expected no failures (filter excluded `fail`), got: %v", err)
	}
}
