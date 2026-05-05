package main

// migrate_qc_rename.go — `ov migrate qc-rename`.
//
// One-shot migration that renames the operator-specific `qc` deployment
// key to `cachyos-dx`, demonstrating the cross-kind name reuse policy
// (2026-05): the same name `cachyos-dx` is used for the kind:local
// template AND the kind:deployment entry that applies it. Operator
// disambiguation is by verb context.
//
// Idempotent. Running twice is a no-op (modulo printing "already
// renamed" / "nothing to migrate"). Walks both:
//
//   - the in-repo overthink.yml (project deployment tree)
//   - the per-machine ~/.config/ov/deploy.yml (user overlay)
//
// Both files are line-oriented edited so comments and surrounding
// content are preserved. The rename is a precise top-level key
// substitution in the deployment: section — never a global string
// replace.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// MigrateQcRenameCmd is `ov migrate qc-rename`.
type MigrateQcRenameCmd struct {
	DryRun bool `long:"dry-run" help:"Print files that would be modified, don't touch the filesystem"`
}

func (c *MigrateQcRenameCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	changed, err := MigrateQcRename(dir, c.DryRun)
	if err != nil {
		return err
	}
	prefix := "modified "
	if c.DryRun {
		prefix = "[dry-run] would modify "
	}
	if len(changed) == 0 {
		fmt.Println("ov migrate qc-rename: nothing to migrate (qc already renamed to cachyos-dx)")
		return nil
	}
	for _, p := range changed {
		fmt.Println(prefix + p)
	}
	return nil
}

// MigrateQcRename walks both the in-repo overthink.yml and the
// per-machine ~/.config/ov/deploy.yml, renaming any `qc:` deployment
// key to `cachyos-dx:` (and any commentary references). Returns the
// list of touched files.
func MigrateQcRename(dir string, dryRun bool) ([]string, error) {
	var changed []string

	// In-repo files: overthink.yml + any included deploy.yml /
	// pod.yml / etc. that the project author chose to ship.
	projectFiles := []string{
		filepath.Join(dir, "overthink.yml"),
		filepath.Join(dir, "deploy.yml"),
	}
	for _, p := range projectFiles {
		modified, err := rewriteQcRenameFile(p, dryRun)
		if err != nil {
			return changed, err
		}
		if modified {
			changed = append(changed, p)
		}
	}

	// Per-machine deploy.yml. Errors silently fall through — most
	// users won't have one, and the per-repo edit alone is sufficient.
	if home, err := os.UserHomeDir(); err == nil {
		userFile := filepath.Join(home, ".config", "ov", "deploy.yml")
		if modified, err := rewriteQcRenameFile(userFile, dryRun); err == nil && modified {
			changed = append(changed, userFile)
		}
	}

	return changed, nil
}

// rewriteQcRenameFile applies the qc → cachyos-dx rewrite to a single
// YAML file. Returns (modified, error). Missing files are NOT errors
// — the migration is opportunistic per file.
func rewriteQcRenameFile(path string, dryRun bool) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("reading %s: %w", path, err)
	}

	updated := applyQcRenameRewrites(string(data))
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

// qcDeployKeyPattern matches the `qc:` deployment key as a top-level
// child of a deployment: section. The leading whitespace is captured
// to preserve the parent indent. We deliberately scope the rename to
// the deployment-tree shape — bare `qc:` lines elsewhere (a hypothetical
// secret name, a layer.yml field, etc.) are out of scope.
var qcDeployKeyPattern = regexp.MustCompile(`(?m)^(\s+)qc:\s*$`)

// applyQcRenameRewrites returns src with the deployment-key rename
// applied. Comment text mentioning `qc` is also rewritten to keep the
// surrounding documentation in sync. The rewrite is intentionally
// narrow: only references in the qc-deployment idiom are touched.
func applyQcRenameRewrites(src string) string {
	out := qcDeployKeyPattern.ReplaceAllString(src, "${1}cachyos-dx:")

	// Common comment idioms in this repo that mention the qc deploy
	// directly. Each is a single substring; matching is exact.
	commentRewrites := map[string]string{
		"# qc — this CachyOS workstation":                                 "# cachyos-dx — this CachyOS workstation",
		"`ov rebuild qc`":                                                 "`ov rebuild cachyos-dx`",
		"'ov rebuild qc'":                                                 "'ov rebuild cachyos-dx'",
		"ov rebuild qc":                                                   "ov rebuild cachyos-dx",
		"ov deploy add qc":                                                "ov deploy add cachyos-dx",
		"qc CachyOS host":                                                 "cachyos-dx CachyOS host",
		"qc host":                                                         "cachyos-dx host",
		"# qc host":                                                       "# cachyos-dx host",
		"`qc` deployment":                                                 "`cachyos-dx` deployment",
		"the `qc` deployment":                                             "the `cachyos-dx` deployment",
	}
	for old, new := range commentRewrites {
		out = strings.ReplaceAll(out, old, new)
	}
	return out
}
