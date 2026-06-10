package main

// migrate_ledger_candy_keys.go — `charly migrate` step renaming the install-ledger
// json keys `layer`→`candy` / `add_layer`→`add_candy` and stamping each record
// with the ledger schema_version (Cutover F). The install ledger
// (~/.config/opencharly/installed/{deploys,layers}/*.json) is per-host deploy
// STATE the unified loader never sees, so it gets its OWN version gate: the new
// read path (ReadDeployRecord/ReadCandyRecord) hard-rejects a record without
// schema_version (a pre-cutover record whose json:"layer" key would silently
// unmarshal to an empty Candy). This step converts existing ledgers so they pass.
// TouchesHost; idempotent (a record already carrying schema_version is a no-op);
// raw-JSON rewrite (no record schema needed — key order is irrelevant for the
// machine-managed ledger).

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// MigrateLedgerCandyKeys rewrites every install-ledger record's legacy
// `layer`/`add_layer` keys to `candy`/`add_candy` and stamps schema_version.
func MigrateLedgerCandyKeys(ctx *MigrateContext) (bool, error) {
	if ctx.LedgerRoot == "" {
		return false, nil // project-only mode (remote-cache auto-migration)
	}
	changed := false
	for _, sub := range []string{"deploys", "layers"} {
		dir := filepath.Join(ctx.LedgerRoot, sub)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // ledger dir absent — nothing to migrate
		}
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
				continue
			}
			mod, err := rewriteLedgerRecord(filepath.Join(dir, e.Name()), ctx.DryRun)
			if err != nil {
				return changed, err
			}
			if mod {
				changed = true
			}
		}
	}
	return changed, nil
}

func rewriteLedgerRecord(path string, dryRun bool) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, nil
	}
	var rec map[string]json.RawMessage
	if err := json.Unmarshal(data, &rec); err != nil {
		return false, nil // not a JSON object — leave alone
	}
	if _, ok := rec["schema_version"]; ok {
		return false, nil // already migrated
	}
	if v, ok := rec["layer"]; ok {
		rec["candy"] = v
		delete(rec, "layer")
	}
	if v, ok := rec["add_layer"]; ok {
		rec["add_candy"] = v
		delete(rec, "add_layer")
	}
	rec["schema_version"], _ = json.Marshal(ledgerSchemaVersion)
	if dryRun {
		return true, nil
	}
	out, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return false, err
	}
	return true, os.WriteFile(path, out, 0644)
}
