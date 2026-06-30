package main

// legacy_local_images.go — shared YAML scanner for the 2026-05
// deploy-fetch-narrowing cutover. Detects (and, in the migration
// command, rewrites) any `images:` key nested under `local.<name>` in
// project YAML.
//
// The kind:local `images:` field was removed; see local_spec.go and
// the check preflight in check_image_preflight.go. Operators with
// legacy YAML run `charly migrate` to convert each block to
// a dated comment fence; the validator (validateLegacyLocalImagesField)
// hard-errors on any surviving key so legacy configs cannot silently
// load and behave differently than fresh ones.

import (
	"os"
	"path/filepath"
	"strings"
)

// scanLegacyLocalImagesBlocks walks every YAML file under dir and
// returns every detected legacy `images:` block. Skips vendor/build
// dirs to mirror the loader's discovery scope.
func scanLegacyLocalImagesBlocks(dir string) []LegacyImagesBlock {
	var out []LegacyImagesBlock
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if migrateSkipDir(path, dir) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".yml") && !strings.HasSuffix(path, ".yaml") {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		out = append(out, scanLegacyLocalImagesInFile(path, string(data))...)
		return nil
	})
	return out
}

// walkYAMLForLegacyLocalImages emits one validation error per detected
// legacy block. The error names the file, the template, and the
// migration command — exactly the breadcrumb pattern R5 requires when
// a stale schema field is loaded.
func walkYAMLForLegacyLocalImages(dir string, errs *ValidationError) {
	for _, block := range scanLegacyLocalImagesBlocks(dir) {
		errs.Add("kind:local %q in %s:%d: legacy `images:` field detected — run `charly migrate` to convert; the field was removed in the 2026-05 deploy-fetch-narrowing cutover (test-bed image preflight moved to `charly check run`)",
			block.TemplateName, relPathForError(dir, block.Path), block.StartLine)
	}
}

// relPathForError returns a path relative to dir when possible, or the
// absolute path otherwise. Pure cosmetic helper for clearer errors.
func relPathForError(dir, path string) string {
	if rel, err := filepath.Rel(dir, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}
