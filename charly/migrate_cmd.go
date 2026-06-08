package main

import (
	"fmt"
	"os"
)

// MigrateCmd is `charly migrate` — the single, idempotent command that brings any
// overthink config up to the latest schema CalVer. It runs the full ordered
// migration chain (see migrate_registry.go) across the project tree, the
// per-host ~/.config/charly files, the encrypted-volume quadlets, and the .secrets
// file, then stamps every versioned file to LatestSchemaVersion.
//
// It replaces the former ~16 `charly migrate <name>` sub-verbs: there is nothing to
// choose — `charly migrate` always migrates, and only ever to the latest CalVer.
// Future cutovers add one MigrationStep to the registry; the operator command
// never changes.
//
// The project directory is taken from the current working directory; use the
// top-level `-C` / `--dir` / CH_PROJECT_DIR global to point at a different
// project (main() chdir's before dispatch, so os.Getwd already reflects it).
type MigrateCmd struct {
	DryRun bool `long:"dry-run" help:"Print every change the chain would make without touching the filesystem"`
}

func (c *MigrateCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	ctx, err := NewMigrateContext(dir, c.DryRun)
	if err != nil {
		return err
	}
	if _, err := RunMigrations(ctx); err != nil {
		return err
	}
	if c.DryRun {
		fmt.Fprintln(os.Stderr, "(dry-run — no files were modified)")
	}
	return nil
}
