package migrate

// migrate_driver.go — shared drivers for the file-rewriting `charly migrate`
// steps (R3). Two families of migration share a per-file rewrite loop that
// differs only in the per-document transform:
//
//   - runDocMigration drives the MULTI-document steps (MigrateDropBoxPort,
//     MigrateEvalCheck, MigrateOpUnify, MigratePlanUnify): a candidate-file
//     scan, a yaml.NewDecoder multi-doc decode, a per-doc transform, and a
//     re-encode (SetIndent 4, mode 0o644) of the whole stream when anything
//     changed. No backup file.
//
//   - rewriteDocFile drives the SINGLE-document, fixed-file-list steps
//     (MigrateInitCandyKeys, MigrateRecipeSectionValues): a single
//     yaml.Unmarshal, one transform, a re-encode (SetIndent 4, mode 0644) and
//     a per-file .bak.<unix-ts> backup before overwrite.
//
// The two families differ materially (multi-doc vs single-doc; no-backup vs
// backup), so they are deliberately TWO drivers, not one over-parametrized one.

import (
	"bytes"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// runDocMigration drives a multi-document migration step: it scans
// candidateFiles(dir), decodes each as a YAML multi-document stream, applies
// transform to every document, and — when any document changed — re-encodes the
// whole stream (4-space indent) and writes it back (0o644) unless dryRun.
// Returns the rewritten file paths. Unreadable files are skipped (never abort
// the chain). Shared by the box-port / eval→check / op-unify / plan-unify steps.
func runDocMigration(dir string, dryRun bool, candidateFiles func(string) []string, transform func(*yaml.Node) bool) ([]string, error) {
	var rewritten []string
	for _, path := range candidateFiles(dir) {
		data, err := os.ReadFile(path)
		if err != nil {
			continue // skip unreadable siblings; don't abort the chain
		}
		dec := yaml.NewDecoder(bytes.NewReader(data))
		var docs []*yaml.Node
		changed := false
		for {
			var doc yaml.Node
			if derr := dec.Decode(&doc); derr != nil {
				break
			}
			d := doc
			if transform(&d) {
				changed = true
			}
			docs = append(docs, &d)
		}
		if !changed {
			continue
		}
		var out bytes.Buffer
		enc := yaml.NewEncoder(&out)
		enc.SetIndent(4)
		for _, d := range docs {
			if eerr := enc.Encode(d); eerr != nil {
				return rewritten, fmt.Errorf("encoding %s: %w", path, eerr)
			}
		}
		_ = enc.Close()
		if !dryRun {
			if werr := os.WriteFile(path, out.Bytes(), 0o644); werr != nil {
				return rewritten, fmt.Errorf("writing %s: %w", path, werr)
			}
		}
		rewritten = append(rewritten, path)
	}
	return rewritten, nil
}

// migrateHostOverlayDoc applies a node-form document transform to the per-host
// deploy overlay at ctx.HostDeployPath, IN ADDITION to the project files a
// node-form migration step rewrites through runDocMigration. The per-host
// overlay (~/.config/charly/charly.yml) carries a legacy top-level `deploy:`
// map that the node-form loader gate (classifyDoc → #NodeDoc) HARD-REJECTS
// until it is converted to name-first node-form — but the project-dir drivers
// (runDocMigration over opUnifyCandidateFiles) never reach it. A node-form step
// that omits this call therefore leaves the overlay un-converted (the trailing
// calver-schema stamp bumps its `version:` to HEAD yet its body stays a legacy
// `deploy:` map), and every subsequent `charly` load of that overlay hard-fails
// with no migrate path to recover it. This shares the EXACT transform the
// project files receive (R3 — one node-form conversion for project and host)
// via the single-doc rewriteDocFile driver (which writes a .bak.<unix-ts>
// before overwrite — extra data safety for the user's live config).
//
// Gated on a non-empty ctx.HostDeployPath: the project-only runner
// (RunProjectMigrations, used by remote-cache auto-migration) leaves
// HostDeployPath empty, so a remote fetch NEVER mutates the user's per-host
// state — the SAME self-gate the calver-schema host stamp uses. A node-form
// step thus keeps TouchesHost=false (its project transform MUST run under the
// project-only runner so fetched remotes convert too); only this host portion
// self-gates on HostDeployPath. Returns whether the host overlay changed.
func migrateHostOverlayDoc(ctx *MigrateContext, transform func(*yaml.Node) bool) (bool, error) {
	if ctx.HostDeployPath == "" {
		return false, nil
	}
	return rewriteDocFile(ctx.HostDeployPath, ctx.DryRun, transform)
}

// rewriteDocFile drives a single-document file rewrite: it reads path, decodes
// ONE YAML document, applies transform, and — when it changed — re-encodes
// (4-space indent) and writes it back (0644) unless dryRun, after saving a
// .bak.<unix-ts> copy of the original. A missing/unparseable file is a no-op
// (false, nil). Shared by the init-candy-keys / recipe-section-values steps and
// the per-host-overlay node-form conversion (migrateHostOverlayDoc).
func rewriteDocFile(path string, dryRun bool, transform func(*yaml.Node) bool) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return false, nil
	}
	if !transform(&doc) {
		return false, nil
	}
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(4)
	if err := enc.Encode(&doc); err != nil {
		return false, err
	}
	_ = enc.Close()
	if dryRun {
		return true, nil
	}
	bak := fmt.Sprintf("%s.bak.%d", path, time.Now().Unix())
	_ = os.WriteFile(bak, data, 0644)
	if err := os.WriteFile(path, out.Bytes(), 0644); err != nil {
		return false, err
	}
	return true, nil
}
