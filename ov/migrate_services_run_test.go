package main

import (
	"os"
	"testing"
)

// TestRunMigrateServices performs the actual layer.yml migration. It's
// gated by the OV_RUN_MIGRATION env var so normal `go test` runs don't
// mutate the repo. Invoke with:
//
//	OV_RUN_MIGRATION=1 go test -run '^TestRunMigrateServices$' ./...
//
// After a successful run, 38 layer.yml files get their legacy
// service:/system_services: blocks replaced with the unified
// services: schema.
func TestRunMigrateServices(t *testing.T) {
	if os.Getenv("OV_RUN_MIGRATION") != "1" {
		t.Skip("skipping migration (set OV_RUN_MIGRATION=1 to run)")
	}
	dir := os.Getenv("OV_MIGRATION_DIR")
	if dir == "" {
		dir = "../layers"
	}
	n, err := MigrateServicesDir(dir)
	if err != nil {
		t.Fatalf("MigrateServicesDir: %v", err)
	}
	t.Logf("migrated %d files", n)
}
