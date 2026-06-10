package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigrateLedgerCandyKeys proves the install-ledger json keys
// layer→candy / add_layer→add_candy are rewritten + schema_version stamped, the
// migrated records read cleanly through the gated readers, and the step is
// idempotent.
func TestMigrateLedgerCandyKeys(t *testing.T) {
	root := t.TempDir()
	deploys := filepath.Join(root, "deploys")
	layers := filepath.Join(root, "layers")
	if err := os.MkdirAll(deploys, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(layers, 0755); err != nil {
		t.Fatal(err)
	}
	// Pre-cutover records: legacy json:"layer"/"add_layer", no schema_version.
	if err := os.WriteFile(filepath.Join(deploys, "abc.json"),
		[]byte(`{"deploy_id":"abc","image":"web","target":"host","layer":["ripgrep","uv"],"add_layer":["extra"],"deployed_at":"t"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layers, "ripgrep.json"),
		[]byte(`{"layer":"ripgrep","deployed_by":["abc"],"deployed_at":"t"}`), 0644); err != nil {
		t.Fatal(err)
	}

	ctx := &MigrateContext{LedgerRoot: root}
	changed, err := MigrateLedgerCandyKeys(ctx)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if !changed {
		t.Fatal("expected a change")
	}

	paths := &LedgerPaths{Root: root, Deploys: deploys, Candies: layers}
	dr, err := ReadDeployRecord(paths, "abc")
	if err != nil {
		t.Fatalf("ReadDeployRecord after migrate (gate should pass): %v", err)
	}
	if dr == nil || len(dr.Candy) != 2 || dr.Candy[0] != "ripgrep" {
		t.Errorf("DeployRecord.Candy not migrated from json:\"layer\": %+v", dr)
	}
	if len(dr.AddCandy) != 1 || dr.AddCandy[0] != "extra" {
		t.Errorf("DeployRecord.AddCandy not migrated from json:\"add_layer\": %+v", dr)
	}
	if dr.SchemaVersion == "" {
		t.Error("DeployRecord schema_version not stamped")
	}
	cr, err := ReadCandyRecord(paths, "ripgrep")
	if err != nil {
		t.Fatalf("ReadCandyRecord after migrate (gate should pass): %v", err)
	}
	if cr == nil || cr.Candy != "ripgrep" || cr.SchemaVersion == "" {
		t.Errorf("CandyRecord not migrated/stamped: %+v", cr)
	}

	// Idempotency: a second run is a no-op.
	changed2, err := MigrateLedgerCandyKeys(ctx)
	if err != nil {
		t.Fatalf("migrate (2nd): %v", err)
	}
	if changed2 {
		t.Error("second run should be a no-op")
	}
}

// TestReadCandyRecord_GatesPreCutover proves the ledger read path hard-rejects a
// pre-cutover record (no schema_version) with a `charly migrate` hint.
func TestReadCandyRecord_GatesPreCutover(t *testing.T) {
	root := t.TempDir()
	layers := filepath.Join(root, "layers")
	if err := os.MkdirAll(layers, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layers, "old.json"), []byte(`{"layer":"old","deployed_by":[]}`), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := ReadCandyRecord(&LedgerPaths{Root: root, Candies: layers}, "old")
	if err == nil {
		t.Fatal("expected gate error on a pre-cutover record")
	}
	if !strings.Contains(err.Error(), "charly migrate") {
		t.Errorf("gate error should hint `charly migrate`: %v", err)
	}
}
