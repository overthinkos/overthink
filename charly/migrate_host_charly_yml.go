package main

// migrate_host_charly_yml.go — `charly migrate` step renaming the per-host deploy
// overlay `~/.config/charly/deploy.yml` → `~/.config/charly/charly.yml` (Cutover E),
// so the per-host config loads through the SAME unified loader as every project
// charly.yml. TouchesHost: the rename mutates per-host state, and ctx.HostDeployPath
// is retargeted so the trailing calver-schema stamp lands on the renamed file
// (mirrors the charly-rebrand host relocation). Idempotent — a host already on
// charly.yml is a no-op. See CHANGELOG/.

import (
	"os"
	"path/filepath"
)

// MigrateHostCharlyYml renames the per-host deploy overlay file from the legacy
// `deploy.yml` name to `charly.yml` in the same dir, and retargets
// ctx.HostDeployPath so the trailing calver-schema stamp lands on it.
func MigrateHostCharlyYml(ctx *MigrateContext) (bool, error) {
	if ctx.HostDeployPath == "" {
		return false, nil // project-only mode (remote-cache auto-migration)
	}
	dir := filepath.Dir(ctx.HostDeployPath)
	oldPath := filepath.Join(dir, "deploy.yml")
	newPath := filepath.Join(dir, "charly.yml")

	// Retarget so downstream steps (calver-schema) stamp the unified filename,
	// even when there is nothing to rename (idempotent re-run). Gated on
	// !DryRun, like the charly-rebrand relocation.
	if !ctx.DryRun {
		ctx.HostDeployPath = newPath
	}

	changed := false
	// Rename the legacy file when present and the new one is not yet.
	if fileExists(oldPath) && !fileExists(newPath) {
		if ctx.DryRun {
			return true, nil
		}
		if err := os.Rename(oldPath, newPath); err != nil {
			return false, err
		}
		changed = true
	}

	if !fileExists(newPath) || ctx.DryRun {
		return changed, nil
	}

	// Per-host deploy configs predate per-file versioning (the old DeployConfig
	// carried no `version:` field), so a just-renamed file has no top-level
	// version line — and stampVersionField (calver-schema, the next step) only
	// REWRITES an existing line, never adds one. Without this the renamed
	// charly.yml would fail the unified loader's gate ("found \"\""). Prepend a
	// version line here; calver-schema then re-stamps it to HEAD.
	data, err := os.ReadFile(newPath)
	if err != nil {
		return changed, err
	}
	if firstYAMLVersionLine(data) == "" {
		stamped := append([]byte("version: "+latestSchemaVersion.String()+"\n"), data...)
		if err := os.WriteFile(newPath, stamped, 0600); err != nil {
			return changed, err
		}
		changed = true
	}
	return changed, nil
}
