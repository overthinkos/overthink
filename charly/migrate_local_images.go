package main

// migrate_local_images.go — `charly migrate`.
//
// One-shot migration of the (deleted) kind:local `images:` field to a
// dated comment fence. The field was removed in the 2026-05
// deploy-fetch-narrowing cutover; legacy YAML carrying it hard-errors
// at validate time pointing at this command.
//
// The transform is line-oriented (preserves comments + formatting),
// idempotent (running twice is a no-op), and operates only on
// `images:` blocks nested under `local.<template>`. Top-level
// `images:` maps (image.yml shapes) and `deployments.images:` legacy
// keys are NOT touched.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MigrateLocalImage walks every *.yml / *.yaml file under dir and
// rewrites legacy kind:local `images:` blocks to a dated comment
// fence. Returns the list of touched paths.
func MigrateLocalImage(dir string, dryRun bool) ([]string, error) {
	var changed []string
	walkErr := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			base := filepath.Base(path)
			if base == ".git" || base == "node_modules" || base == ".build" || base == ".cache" || base == ".eval" || base == "plugins" {
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
		updated, n := rewriteLegacyLocalImagesInFile(path, string(data))
		if n == 0 {
			return nil
		}
		changed = append(changed, path)
		if dryRun {
			return nil
		}
		if werr := os.WriteFile(path, []byte(updated), 0o644); werr != nil {
			return fmt.Errorf("writing %s: %w", path, werr)
		}
		return nil
	})
	if walkErr != nil {
		return changed, walkErr
	}
	return changed, nil
}
