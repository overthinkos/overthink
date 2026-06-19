package main

// migrate_ov_cachyos.go — `charly migrate`.
//
// One-shot migration that renames the operator-specific CachyOS
// deployment to its 2026-05 canonical form `ov-cachyos`. Collapses
// the qc → cachyos-dx → ov-cachyos rename chain into a single hop:
// any project on EITHER pre-cutover state lands on `ov-cachyos`
// after one run.
//
// Renamed:
//
//   - `deployment.qc` → `deployment.ov-cachyos`
//   - `deployment.cachyos-dx` → `deployment.ov-cachyos`
//   - `local.cachyos-dx` → `local.ov-cachyos` (kind:local template
//     name moves with the deployment so they keep matching, per
//     the cross-kind name reuse policy in CLAUDE.md)
//   - `local: cachyos-dx` cross-references inside deployment
//     entries → `local: ov-cachyos`
//   - documented comment idioms touched up so the surrounding
//     prose stays in sync.
//
// Idempotent. Running twice is a no-op (modulo printing "nothing to
// migrate"). Walks both:
//
//   - the in-repo overthink.yml + deploy.yml (project deployment tree)
//   - the per-machine ~/.config/ov/deploy.yml (user overlay)
//
// Both files are line-oriented edited so comments and surrounding
// content are preserved. The rename is a precise top-level key
// substitution — never a global string replace.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// MigrateCharlyCachyos walks both the in-repo project files and the
// per-machine ~/.config/ov/deploy.yml, applying the qc → ov-cachyos
// AND cachyos-dx → ov-cachyos renames. Returns the list of touched
// files.
func MigrateCharlyCachyos(dir string, dryRun bool) ([]string, error) {
	var changed []string

	// In-repo files: overthink.yml + any included deploy.yml that
	// the project author chose to ship.
	projectFiles := []string{
		filepath.Join(dir, "overthink.yml"),
		filepath.Join(dir, "charly.yml"),
		filepath.Join(dir, "deploy.yml"),
		filepath.Join(dir, "local.yml"),
	}
	for _, p := range projectFiles {
		modified, err := rewriteCharlyCachyosFile(p, dryRun)
		if err != nil {
			return changed, err
		}
		if modified {
			changed = append(changed, p)
		}
	}

	// Per-machine deploy.yml. Errors silently fall through — most
	// users won't have one, and the per-repo edit alone is
	// sufficient.
	if home, err := os.UserHomeDir(); err == nil {
		for _, sub := range []string{"ov", "charly"} {
			userFile := filepath.Join(home, ".config", sub, "deploy.yml")
			if modified, err := rewriteCharlyCachyosFile(userFile, dryRun); err == nil && modified {
				changed = append(changed, userFile)
			}
		}
	}

	return changed, nil
}

// rewriteCharlyCachyosFile applies the consolidated rename to a single
// YAML file. Returns (modified, error). Missing files are NOT errors
// — the migration is opportunistic per file.
func rewriteCharlyCachyosFile(path string, dryRun bool) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("reading %s: %w", path, err)
	}

	updated := applyCharlyCachyosRewrites(string(data))
	if updated == string(data) {
		return false, nil
	}
	if dryRun {
		return true, nil
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return false, fmt.Errorf("writing %s: %w", path, err)
	}
	return true, nil
}

// Top-level (indented) keys to rewrite. Each pattern matches a
// `<key>:` line under a parent mapping (`deployment:` or `local:`),
// captured by leading whitespace so the parent indent is preserved.
//
// We deliberately scope each pattern to the indented-mapping-key
// shape — bare `qc:` / `cachyos-dx:` strings elsewhere in the file
// (a comment, a fixture in test data, a candy name reference) are
// NOT touched.
var (
	qcDeployKeyPattern        = regexp.MustCompile(`(?m)^(\s+)qc:\s*$`)
	cachyosDxDeployKeyPattern = regexp.MustCompile(`(?m)^(\s+)cachyos-dx:\s*$`)
	ovCachyosDeployKeyPattern = regexp.MustCompile(`(?m)^(\s+)ov-cachyos:\s*$`)
	cachyosDxCrossRefPattern  = regexp.MustCompile(`(?m)^(\s+)local:\s+cachyos-dx\s*$`)
	ovCachyosCrossRefPattern  = regexp.MustCompile(`(?m)^(\s+)local:\s+ov-cachyos\s*$`)
)

// applyCharlyCachyosRewrites returns src with all rename rewrites
// applied. The legacy deployment-name chain qc → cachyos-dx →
// ov-cachyos is normalized to the current canonical `charly-cachyos`
// (any pre-cutover state lands on charly-cachyos in a single hop).
// Comment text naming the legacy keys is touched up so surrounding
// prose stays in sync. The rewrite is narrow: only references in the
// deployment-key idiom + a small set of documented comment idioms are
// touched.
func applyCharlyCachyosRewrites(src string) string {
	out := qcDeployKeyPattern.ReplaceAllString(src, "${1}charly-cachyos:")
	out = cachyosDxDeployKeyPattern.ReplaceAllString(out, "${1}charly-cachyos:")
	out = ovCachyosDeployKeyPattern.ReplaceAllString(out, "${1}charly-cachyos:")
	out = cachyosDxCrossRefPattern.ReplaceAllString(out, "${1}local: charly-cachyos")
	out = ovCachyosCrossRefPattern.ReplaceAllString(out, "${1}local: charly-cachyos")

	// Comment idioms that name the legacy keys directly. Order
	// matters: longer / more-specific patterns first to avoid
	// double-substitution.
	commentRewrites := []struct {
		old string
		new string
	}{
		// qc → charly-cachyos
		{"# qc — this CachyOS workstation", "# charly-cachyos — this CachyOS workstation"},
		{"`charly rebuild qc`", "`charly rebuild charly-cachyos`"},
		{"'charly rebuild qc'", "'charly rebuild charly-cachyos'"},
		{"charly rebuild qc", "charly rebuild charly-cachyos"},
		{"charly bundle add qc", "charly bundle add charly-cachyos"},
		{"qc CachyOS host", "charly-cachyos CachyOS host"},
		{"# qc host", "# charly-cachyos host"},
		{"`qc` deployment", "`charly-cachyos` deployment"},
		{"the `qc` deployment", "the `charly-cachyos` deployment"},
		{"qc host", "charly-cachyos host"},
		// cachyos-dx → charly-cachyos
		{"# cachyos-dx — this CachyOS workstation", "# charly-cachyos — this CachyOS workstation"},
		{"`charly rebuild cachyos-dx`", "`charly rebuild charly-cachyos`"},
		{"'charly rebuild cachyos-dx'", "'charly rebuild charly-cachyos'"},
		{"charly rebuild cachyos-dx", "charly rebuild charly-cachyos"},
		{"charly bundle add cachyos-dx", "charly bundle add charly-cachyos"},
		{"charly test cachyos-dx", "charly eval live charly-cachyos"},
		{"cachyos-dx CachyOS host", "charly-cachyos CachyOS host"},
		{"`cachyos-dx` (kind:local template, applied via the `cachyos-dx` deployment", "`charly-cachyos` (kind:local template, applied via the `charly-cachyos` deployment"},
		{"`cachyos-dx` deployment", "`charly-cachyos` deployment"},
		{"the `cachyos-dx` deployment", "the `charly-cachyos` deployment"},
		{"cachyos-dx host", "charly-cachyos host"},
		{"Apply cachyos-dx to this workstation", "Apply charly-cachyos to this workstation"},
		// ov-cachyos → charly-cachyos
		{"# ov-cachyos — this CachyOS workstation", "# charly-cachyos — this CachyOS workstation"},
		{"`charly rebuild ov-cachyos`", "`charly rebuild charly-cachyos`"},
		{"charly rebuild ov-cachyos", "charly rebuild charly-cachyos"},
		{"charly bundle add ov-cachyos", "charly bundle add charly-cachyos"},
		{"charly eval live ov-cachyos", "charly eval live charly-cachyos"},
		{"ov-cachyos CachyOS host", "charly-cachyos CachyOS host"},
		{"`ov-cachyos` deployment", "`charly-cachyos` deployment"},
		{"the `ov-cachyos` deployment", "the `charly-cachyos` deployment"},
		{"ov-cachyos host", "charly-cachyos host"},
		{"Apply ov-cachyos to this workstation", "Apply charly-cachyos to this workstation"},
	}
	for _, r := range commentRewrites {
		out = strings.ReplaceAll(out, r.old, r.new)
	}
	return out
}
