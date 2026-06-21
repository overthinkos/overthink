package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPersistBedDeployOverrides_SkipsLocalBed pins the bed-infra fix for the
// cross-project host-overlay pollution. persistBedDeployOverrides exists ONLY to
// seed a POD bed's `charly config` step (port/volume/env overrides). A LOCAL bed
// never runs `charly config` (kind:local applies in place during `charly bundle
// add`), and its only persistable cross-ref is a `local:` template defined in the
// bed's OWN project — writing that into the GLOBAL per-host overlay makes the
// overlay un-loadable from every OTHER project (validateCheckBeds rejects the
// unresolvable template), which broke concurrent check-pod / check-k3s-vm runs that
// loaded the overlay while a check-local bed run had seeded its entry. Local deploys
// persist via the install ledger, not this bundle-map path, so the skip is lossless.
//
// The test proves the skip is exact: a local bed leaves the overlay untouched (still
// loadable, no entry), while a pod bed is still persisted.
func TestPersistBedDeployOverrides_SkipsLocalBed(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "charly"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "charly", "charly.yml")
	if err := os.WriteFile(path, []byte("version: "+LatestSchemaVersion().String()+"\n"), 0o600); err != nil {
		t.Fatalf("write initial: %v", err)
	}

	// A LOCAL bed — persisting it would write an un-loadable `local:` cross-ref.
	disp := true
	persistBedDeployOverrides("check-local", BundleNode{
		Target:     "local",
		Local:      "check-local-app",
		Disposable: &disp,
		Lifecycle:  "dev",
	})

	dc, err := LoadBundleConfig()
	if err != nil {
		t.Fatalf("overlay unloadable after local-bed persist (it should have been SKIPPED): %v", err)
	}
	if dc != nil {
		if _, ok := dc.Bundle["check-local"]; ok {
			t.Error("local bed was persisted to the global overlay — must be skipped (cross-project pollution)")
		}
	}

	// A POD bed is STILL persisted (the skip is not too broad). Non-disposable so it
	// is a plain deploy entry, not a check bed subject to validateCheckBeds.
	persistBedDeployOverrides("pod-deploy-x", BundleNode{
		Target: "pod",
		Box:    "pod-deploy-x",
	})
	dc2, err := LoadBundleConfig()
	if err != nil {
		t.Fatalf("reload after pod-bed persist: %v", err)
	}
	if _, ok := dc2.Bundle["pod-deploy-x"]; !ok {
		t.Error("pod bed was NOT persisted — the local skip is too broad")
	}
}
