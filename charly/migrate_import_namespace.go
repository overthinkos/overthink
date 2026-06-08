package main

// migrate_import_namespace.go — `charly migrate` step for the 2026-05
// import-namespace cutover.
//
// The legacy `include:` composition key was deleted in favor of the single
// `import:` statement (flat string items + namespaced `alias: ref` items — see
// unified.go). This step renames the top-level `include:` key to `import:` in
// every project YAML, preserving the existing list verbatim (a flat include
// becomes a flat import — same root-namespace-merge semantics). Comment-
// preserving via the yaml.v3 node API; idempotent (a config already on `import:`
// is a no-op); per-file backups follow the <file>.bak.<unix-ts> convention.
//
// Repo-specific reshaping (combining arch-base.yml + fedora-base.yml into
// base.yml, mounting the cachyos namespace, the deploy→eval bed move) is NOT
// done here — it is hand-authored in the cutover and recorded in CHANGELOG.md.
// A third-party config that only flat-includes its own files migrates cleanly
// to flat imports with no behavior change.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// MigrateImportNamespace renames `include:` → `import:` in overthink.yml and its
// per-kind sibling files at the project root.
func MigrateImportNamespace(dir string, dryRun bool) ([]string, error) {
	var changed []string
	for _, f := range []string{
		"overthink.yml", "image.yml", "vm.yml", "pod.yml", "k8s.yml",
		"local.yml", "deploy.yml", "eval.yml", "build.yml", "base.yml",
		"arch-base.yml", "fedora-base.yml", "cachyos-base.yml",
	} {
		mod, err := renameIncludeToImportFile(filepath.Join(dir, f), dryRun)
		if err != nil {
			return changed, err
		}
		if mod {
			changed = append(changed, f)
		}
	}
	return changed, nil
}

// renameIncludeToImportFile rewrites a single file's top-level `include:` key to
// `import:`. Returns false (no error) for a missing file or a file with no
// `include:` key. The value sequence is preserved verbatim.
func renameIncludeToImportFile(path string, dryRun bool) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, nil // missing sibling — skip silently
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return false, nil // not parseable as a single doc — leave untouched
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return false, nil
	}
	root := doc.Content[0]
	renamed := false
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "include" {
			root.Content[i].Value = "import"
			renamed = true
		}
	}
	if !renamed {
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
