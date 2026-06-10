package main

// migrate_kind_files.go — `charly migrate`.
//
// Schema kind rename: `kind: deployment` → `kind: deploy` everywhere (kind-keyed
// standalone docs and root-shape collection maps). It no longer SPLITS inline
// `box:`/`vm:` into sibling files — YAML files are generic kind-containers and
// per-kind sibling files are an optional convenience, never forced.
//
// Preserves YAML comments via yaml.Node round-trip (same approach as
// migrate_schema_v4.go). Idempotent on already-migrated trees (files with no
// kind: deployment are no-ops).
//
// Hard load-time errors in unified.go point at this command for residual
// `kind: deployment` and `deployment:` root collection keys.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// MigrateKindFilesResult reports the outcome.
type MigrateKindFilesResult struct {
	Transforms   []string
	WrittenFiles []string
	NoChanges    bool
}

// kindFilesSchemaVersion is this step's schema CalVer (also its registry entry's
// Version).
var kindFilesSchemaVersion = mustCalVer("2026.125.2355")

// MigrateKindFiles applies the cutover transforms to the project rooted at dir.
// In dry-run mode no files are written.
func MigrateKindFiles(dir string, dryRun bool) (MigrateKindFilesResult, error) {
	var res MigrateKindFilesResult
	// kind-files no longer SPLITS inline box:/vm: into sibling files — YAML
	// files are generic kind-containers and per-kind sibling files are an
	// optional convenience, never forced. The only remaining transform is the
	// legacy `kind: deployment` → `kind: deploy` rename.

	// Rename root-key `deployment:` → `deploy:` in deploy.yml.
	deployPath := filepath.Join(dir, "deploy.yml")
	if _, err := os.Stat(deployPath); err == nil {
		renamed, err := renameDeployRootKey(deployPath, dryRun)
		if err != nil {
			return res, err
		}
		if renamed {
			res.Transforms = append(res.Transforms, "deploy.yml: root-key deployment: → deploy:")
			res.WrittenFiles = append(res.WrittenFiles, deployPath)
		}
	}

	// Rename `kind: deployment` → `kind: deploy` in every reachable YAML file
	// (overthink.yml + imports + every *.yml at the project root). Idempotent —
	// files with no kind: deployment are no-ops.
	yamlFiles, err := collectProjectYAMLs(dir)
	if err != nil {
		return res, err
	}
	for _, p := range yamlFiles {
		renamed, err := renameKindDocKey(p, "deployment", "deploy", dryRun)
		if err != nil {
			return res, err
		}
		if renamed > 0 {
			res.Transforms = append(res.Transforms,
				fmt.Sprintf("%s: %d kind: deployment → kind: deploy", filepath.Base(p), renamed))
			res.WrittenFiles = append(res.WrittenFiles, p)
		}
	}

	res.NoChanges = len(res.WrittenFiles) == 0
	return res, nil
}

// writeYAMLDoc serializes a yaml.Node tree with 4-space indent (matches the
// existing migrate_schema_v4.go style).
func writeYAMLDoc(path string, doc *yaml.Node) error {
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(4)
	if err := enc.Encode(doc); err != nil {
		return fmt.Errorf("encoding %s: %w", path, err)
	}
	enc.Close()
	if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// renameDeployRootKey renames the root-shape `deployment:` key to `deploy:`
// in a YAML file. Returns (renamed, error). Idempotent.
func renameDeployRootKey(path string, dryRun bool) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("reading %s: %w", path, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return false, fmt.Errorf("parsing %s: %w", path, err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return false, nil
	}
	root := doc.Content[0]

	// Disambiguation: only rename if the value is a root-shape collection map
	// (NOT kind-keyed with a `name:` child).
	val := findMappingValue(root, "deployment")
	if val == nil {
		return false, nil
	}
	if val.Kind == yaml.MappingNode && findMappingValue(val, "name") != nil {
		// Kind-keyed form — handled by renameKindDocKey, not here.
		return false, nil
	}

	if !renameRootKey(root, "deployment", "deploy") {
		return false, nil
	}

	if !dryRun {
		if err := writeYAMLDoc(path, &doc); err != nil {
			return false, err
		}
	}
	return true, nil
}

// renameKindDocKey rewrites kind-keyed standalone docs and any flat
// occurrence of `kind: <oldKind>` → `kind: <newKind>`. Walks every YAML
// document in the file (multi-document streams supported via `---`).
// Returns the count of renames applied.
func renameKindDocKey(path, oldKind, newKind string, dryRun bool) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("reading %s: %w", path, err)
	}
	// Decode as a multi-doc stream.
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var docs []*yaml.Node
	for {
		var doc yaml.Node
		if err := dec.Decode(&doc); err != nil {
			if err.Error() == "EOF" {
				break
			}
			// Plain io.EOF check via string match — yaml.v3 doesn't expose io.EOF directly here.
			break
		}
		docs = append(docs, &doc)
	}
	if len(docs) == 0 {
		return 0, nil
	}
	count := 0
	for _, doc := range docs {
		if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
			continue
		}
		root := doc.Content[0]
		// Case A: `kind: <old>` scalar at root.
		if v := findMappingValue(root, "kind"); v != nil && v.Kind == yaml.ScalarNode && v.Value == oldKind {
			v.Value = newKind
			count++
		}
		// Case B: kind-keyed wrapper key `<old>:` with a `name:` child.
		// In that shape, the kind is encoded by the wrapper key itself.
		if val := findMappingValue(root, oldKind); val != nil && val.Kind == yaml.MappingNode {
			if findMappingValue(val, "name") != nil {
				if renameRootKey(root, oldKind, newKind) {
					count++
				}
			}
		}
	}
	if count == 0 || dryRun {
		return count, nil
	}
	// Re-encode all docs.
	var out bytes.Buffer
	for i, doc := range docs {
		if i > 0 {
			out.WriteString("---\n")
		}
		enc := yaml.NewEncoder(&out)
		enc.SetIndent(4)
		if err := enc.Encode(doc); err != nil {
			return count, fmt.Errorf("encoding %s: %w", path, err)
		}
		enc.Close()
	}
	if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
		return count, fmt.Errorf("writing %s: %w", path, err)
	}
	return count, nil
}

// collectProjectYAMLs returns every *.yml file at dir's top level. The
// migration only touches files at the project root; nested charly.yml files
// are left alone (they don't carry kind: deployment).
func collectProjectYAMLs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yml") {
			continue
		}
		paths = append(paths, filepath.Join(dir, e.Name()))
	}
	return paths, nil
}
