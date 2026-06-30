package migrate

// migrate_entity_version.go — `charly migrate`.
//
// Backfill for the per-kind versioning hard cutover that made the per-entity
// `version:` field load-bearing (it drives the image's content-stable
// org.overthinkos.version label AND cross-repo candy resolution).
//
// Two backfills, both seeded with the cutover's HEAD CalVer:
//
//  1. EVERY layer.yml (kind-keyed `layer:` map) gets `version: <seed>` — the
//     layer kind now REQUIRES it (validateCandyContents hard-errors otherwise).
//  2. Bare-base image entries — an `image:` entry with NO `layer:` field AND an
//     EXTERNAL `base:` (a registry ref, detected by a "/" in the value, e.g.
//     `quay.io/archlinux/archlinux:base-20260525.0.535911`) — get a dedicated
//     `version: <seed>`
//     so their label is stable (a layerless image cannot derive a
//     highest-layer-version). Layered images and internal-base images (builders
//     `FROM arch`, namespaced `base: cachyos.cachyos`) are left UNVERSIONED so
//     they derive (computeEffectiveVersions step 2/3).
//
// NEVER touches a document-root `version:` (the schema stamp — that's the
// calver-schema step's job). Comment-preserving via the yaml.v3 node API;
// idempotent; writes a <file>.bak.<unix-ts> before each modified file.
//
// Any NESTED git submodule is skipped: each is its own charly-project repo,
// migrated by its OWN `charly migrate` (or by remote-cache auto-migration when
// fetched). That auto-migration (RunProjectMigrations on a freshly-cloned cache)
// is what backfills fetched remote candies' versions, so the runtime can
// hard-error on an unversioned fetched candy instead of carrying a fallback.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// EntityVersionResult is the per-file summary of version injections.
type EntityVersionResult struct {
	Path    string
	Changes []string
}

// MigrateEntityVersion backfills per-entity `version:` across cwd's project YAML.
// seed is the CalVer stamped onto every backfilled entity (the cutover HEAD).
func MigrateEntityVersion(cwd, seed string, dryRun bool) ([]EntityVersionResult, error) {
	var results []EntityVersionResult
	walkErr := filepath.Walk(cwd, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if migrateSkipDir(path, cwd) {
				return filepath.SkipDir
			}
			// Backfilling a version: into a hand-authored Go test fixture (or a
			// build-output dir) would corrupt it — entity-version skips
			// output/testdata ON TOP OF the shared build-artifact + submodule set.
			switch filepath.Base(path) {
			case "output", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".yml") && !strings.HasSuffix(path, ".yaml") {
			return nil
		}
		res, perr := migrateEntityVersionOneFile(path, seed, dryRun)
		if perr != nil {
			return perr
		}
		if res != nil {
			results = append(results, *res)
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Path < results[j].Path })
	return results, nil
}

func migrateEntityVersionOneFile(path, seed string, dryRun bool) (*EntityVersionResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, nil // not parseable YAML — skip silently (unrelated file)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, nil
	}

	var changes []string
	mutated := false

	// 1. Single layer entry: a kind-keyed `layer:` mapping with a `name:` child
	//    (layer.yml, or an inline single-layer doc).
	if lv := nodeMapValue(root, "layer"); lv != nil && lv.Kind == yaml.MappingNode && mapStringField(lv, "name") != "" {
		if injectScalarFirst(lv, "version", seed) {
			mutated = true
			changes = append(changes, fmt.Sprintf("layer %q: injected version: %s", mapStringField(lv, "name"), seed))
		}
	}

	// 2. image:/images: map of name→entry; bare-base entries get a dedicated
	//    version. Layered + internal-base entries are left to derive.
	for _, mapKey := range []string{"image", "images"} {
		mv := nodeMapValue(root, mapKey)
		if mv == nil || mv.Kind != yaml.MappingNode {
			continue
		}
		for i := 0; i+1 < len(mv.Content); i += 2 {
			nameNode := mv.Content[i]
			entry := mv.Content[i+1]
			if entry.Kind != yaml.MappingNode {
				continue
			}
			if nodeMapValue(entry, "layer") != nil || nodeMapValue(entry, "layers") != nil {
				continue // layered image — derives the highest layer version
			}
			if !strings.Contains(mapStringField(entry, "base"), "/") {
				continue // internal / namespaced base (no "/") — derives via the base
			}
			if injectScalarFirst(entry, "version", seed) {
				mutated = true
				changes = append(changes, fmt.Sprintf("image %q: injected version: %s (bare external base)", nameNode.Value, seed))
			}
		}
	}

	if !mutated {
		return nil, nil
	}

	var buf strings.Builder
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(4)
	if err := enc.Encode(&doc); err != nil {
		return nil, fmt.Errorf("encoding %s: %w", path, err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("closing encoder for %s: %w", path, err)
	}

	if !dryRun {
		bak := fmt.Sprintf("%s.bak.%d", path, time.Now().Unix())
		if err := os.WriteFile(bak, data, 0o644); err != nil {
			return nil, fmt.Errorf("writing backup %s: %w", bak, err)
		}
		if err := os.WriteFile(path, []byte(buf.String()), 0o644); err != nil {
			return nil, fmt.Errorf("writing %s: %w", path, err)
		}
	}
	return &EntityVersionResult{Path: path, Changes: changes}, nil
}

// nodeMapValue returns the value node for key in a MappingNode, or nil.
func nodeMapValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

// injectScalarFirst prepends key:value as the FIRST entry of a MappingNode,
// preserving the rest verbatim. Returns false (no-op) if key already exists —
// the idempotence guard.
func injectScalarFirst(node *yaml.Node, key, value string) bool {
	if node == nil || node.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return false
		}
	}
	k := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	v := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
	node.Content = append([]*yaml.Node{k, v}, node.Content...)
	return true
}
