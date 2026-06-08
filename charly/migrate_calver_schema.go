package main

// migrate_calver_schema.go — the HEAD step of the migration chain.
//
// This is the integer→CalVer transition AND the universal version stamper. It
// rewrites the top-level `version:` field of every versioned file (the
// project-root per-kind files + the per-host deploy.yml) to the HEAD CalVer.
// A file reading `version: 4` (the legacy integer schema version) becomes
// `version: <HEAD>` (a CalVer string); a file already at an older CalVer is
// bumped; a file already at HEAD is left untouched (idempotent).
//
// The rewrite is line-oriented — only the single top-level `version:` line
// changes, so the rest of each file (comments, key order, list indentation)
// is preserved byte-for-byte and the resulting diff is minimal.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// calverSchemaProjectFiles are the project-root files that carry a top-level
// version: schema stamp (matching the per-file-stamp layout).
var calverSchemaProjectFiles = []string{
	"charly.yml", "box.yml", "deploy.yml", "vm.yml", "vms.yml",
	"pod.yml", "k8s.yml", "local.yml", "eval.yml",
}

// MigrateCalverSchema stamps the version: field of every versioned file to head.
// Returns the list of files changed (or, under dryRun, that would change).
func MigrateCalverSchema(dir, hostDeployPath string, head CalVer, dryRun bool) ([]string, error) {
	var changed []string
	paths := make([]string, 0, len(calverSchemaProjectFiles)+1)
	for _, name := range calverSchemaProjectFiles {
		paths = append(paths, filepath.Join(dir, name))
	}
	if hostDeployPath != "" {
		paths = append(paths, hostDeployPath)
	}
	for _, path := range paths {
		did, err := stampVersionField(path, head.String(), dryRun)
		if err != nil {
			return changed, err
		}
		if did {
			changed = append(changed, path)
		}
	}
	return changed, nil
}

// stampVersionField rewrites the first top-level `version:` line of one file to
// `version: <want>`, preserving any trailing comment. Returns (changed, err);
// changed is false when the file is absent, has no top-level version: key, or
// is already at want. A <path>.bak.<unix-ts> rollback is written before any
// rewrite. A top-level key is one with no leading whitespace.
func stampVersionField(path, want string, dryRun bool) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("reading %s: %w", path, err)
	}
	lines := strings.Split(string(data), "\n")
	idx := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "version:") {
			idx = i
			break
		}
	}
	if idx == -1 {
		return false, nil // no top-level version: key — keep layout, add nothing
	}
	newLine := "version: " + want
	if h := strings.Index(lines[idx], "#"); h >= 0 {
		newLine += "  " + strings.TrimSpace(lines[idx][h:])
	}
	if lines[idx] == newLine {
		return false, nil // already stamped
	}
	if dryRun {
		return true, nil
	}
	backup := fmt.Sprintf("%s.bak.%d", path, time.Now().Unix())
	if err := os.WriteFile(backup, data, 0o644); err != nil {
		return false, fmt.Errorf("writing backup %s: %w", backup, err)
	}
	lines[idx] = newLine
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		return false, fmt.Errorf("writing %s: %w", path, err)
	}
	return true, nil
}
