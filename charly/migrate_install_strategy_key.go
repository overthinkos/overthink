package main

// migrate_install_strategy_key.go — `charly migrate` step renaming the legacy VM
// deploy-STATE key `ov_install_strategy:` → `charly_install_strategy:`.
//
// The 2026-06 ov→charly rebrand (the charly-rebrand / charly-cutover4 steps)
// renamed every AUTHORED brand surface, but the PER-HOST VM deploy STATE carries
// an INTERNAL field those project-file walkers never reached:
// VmDeployState.CharlyInstallStrategy (yaml tag `charly_install_strategy`, the
// CURRENT name — see spec/hand_state_types.go). A recovered / pre-rebrand
// per-host overlay (~/.config/charly/charly.yml) still spells that key the old
// way, `ov_install_strategy:`, in its `vm_state:` blocks. The current loader
// silently drops the unknown key, so the install strategy is lost on the next
// VM destroy→create. This step renames that exact mapping key wherever it
// appears — the per-host overlay (the real carrier) AND any project YAML, for
// R3 symmetry — so `charly migrate` recovers it.
//
// This is a COMPLETION of the already-shipped ov→charly rename (the
// `charly_install_strategy` format landed at schema < 2026.169), NOT a new
// format change, so it does NOT raise LatestSchemaVersion: it slots in as an
// intra-HEAD step (after unified-node converts the overlay to node-form, before
// the calver-schema stamp). `charly migrate` runs the WHOLE chain regardless of
// a config's stamp, so the rename reaches a stale overlay whether it is stamped
// below HEAD or was mis-stamped AT HEAD by a prior (buggy) migrate run.
//
// Comment-preserving via the yaml.v3 node API; idempotent (a renamed overlay is
// a no-op). TouchesHost false: the project-file rewrite runs under remote-cache
// auto-migration too, while the per-host overlay portion self-gates on a
// non-empty ctx.HostDeployPath (migrateHostOverlayDoc) so a remote fetch never
// mutates per-host state — the SAME split unified-node / step-venue use.

import "gopkg.in/yaml.v3"

// MigrateInstallStrategyKey renames the legacy vm_state key ov_install_strategy →
// charly_install_strategy across the project candidate files and the per-host
// overlay. Returns whether anything changed.
func MigrateInstallStrategyKey(ctx *MigrateContext) (bool, error) {
	w, err := runDocMigration(ctx.Dir, ctx.DryRun, opUnifyCandidateFiles, renameInstallStrategyKeyDoc)
	if err != nil {
		return len(w) > 0, err
	}
	hostChanged, herr := migrateHostOverlayDoc(ctx, renameInstallStrategyKeyDoc)
	return len(w) > 0 || hostChanged, herr
}

// renameInstallStrategyKeyDoc renames the EXACT mapping key
// ov_install_strategy → charly_install_strategy at every depth. The exact match
// leaves any compound sibling untouched. Returns whether the document changed.
func renameInstallStrategyKeyDoc(n *yaml.Node) bool {
	changed := false
	switch n.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, c := range n.Content {
			if renameInstallStrategyKeyDoc(c) {
				changed = true
			}
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			key, val := n.Content[i], n.Content[i+1]
			if key.Kind == yaml.ScalarNode && key.Value == "ov_install_strategy" {
				key.Value = "charly_install_strategy"
				changed = true
			}
			if renameInstallStrategyKeyDoc(val) {
				changed = true
			}
		}
	}
	return changed
}
