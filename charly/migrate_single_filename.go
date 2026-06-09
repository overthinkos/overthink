package main

// migrate_single_filename.go — `charly migrate` step for the 2026-06
// single-filename cutover. charly.yml becomes the ONE filename that holds box and
// candy definitions, and the only file a project needs:
//
//   - BOXES are split out of box.yml / base.yml (and an inline `box:` map in
//     charly.yml, e.g. the bootc submodule) into per-box discovered directories
//     box/<name>/charly.yml (a kind-keyed `box:` doc).
//   - CANDY manifests rename candy/<name>/candy.yml -> candy/<name>/charly.yml.
//   - the per-kind files vm.yml / pod.yml / k8s.yml / eval.yml / local.yml /
//     android.yml fold their kind keys into charly.yml's root mapping; the files
//     are deleted.
//   - the build.yml import is dropped — the default distro/builder/init/resource
//     vocabulary is now EMBEDDED in the charly binary (charly/build.yml, see
//     embed_build.go). A local build.yml that byte-matches the embedded default is
//     deleted; a CUSTOMIZED local build.yml is left imported (it overrides the
//     embedded default). A remote `@github.../build.yml:vTAG` import is dropped
//     (the embed supplies the identical first-party vocabulary).
//   - discover: is rewritten to scan BOTH box/ and candy/ (manifest charly.yml,
//     the single default); the folded per-kind files + build.yml are removed from
//     import:.
//
// Comment-preserving via the yaml.v3 node API; idempotent (a fully-migrated tree
// is a no-op); per-file .bak.<unix-ts> backup on the charly.yml rewrite. File/dir
// moves use os.Rename so git rename-detection preserves history. TouchesHost is
// false, so remote-cache auto-migration renames fetched remote candy files too.
// See CHANGELOG.md.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// foldKindKeys are the per-kind file top-level keys folded into charly.yml's root
// mapping. version/import/discover/defaults are project-structure keys, never
// folded.
var foldKindKeys = map[string]bool{
	"vm": true, "pod": true, "k8s": true, "local": true, "android": true,
	"ai": true, "recipe": true, "score": true, "eval": true,
}

// foldKindFiles are the per-kind sibling files whose kind keys fold into
// charly.yml. (box.yml / base.yml are handled separately — split into box/ dirs.)
var foldKindFiles = []string{
	"vm.yml", "pod.yml", "k8s.yml", "eval.yml", "local.yml", "android.yml",
}

// MigrateSingleFilename applies the single-filename cutover to a project tree.
// hostDeployPath is accepted for signature parity with the other migrators; this
// step touches only project files.
func MigrateSingleFilename(dir, hostDeployPath string, dryRun bool) ([]string, error) {
	_ = hostDeployPath
	var changed []string

	rootPath := filepath.Join(dir, UnifiedFileName)
	rootData, err := os.ReadFile(rootPath)
	if err != nil {
		return nil, nil // no charly.yml — nothing to migrate
	}
	var rootDoc yaml.Node
	if err := yaml.Unmarshal(rootData, &rootDoc); err != nil {
		return nil, nil
	}
	rootMap := docRootMapping(&rootDoc)
	if rootMap == nil {
		return nil, nil
	}
	rootChanged := false

	// --- Phase 1: split boxes into box/<name>/charly.yml ---
	// 1a. From the per-kind box files.
	for _, bf := range []string{"box.yml", "base.yml"} {
		mod, err := splitBoxFile(dir, filepath.Join(dir, bf), dryRun)
		if err != nil {
			return changed, err
		}
		if mod {
			changed = append(changed, bf+" -> box/")
		}
	}
	// 1b. From an inline `box:` map in charly.yml (the bootc submodule).
	if boxVal := nodeMapValue(rootMap, "box"); boxVal != nil && boxVal.Kind == yaml.MappingNode && len(boxVal.Content) > 0 {
		if err := splitBoxMapping(dir, boxVal, dryRun); err != nil {
			return changed, err
		}
		removeMapKey(rootMap, "box")
		rootChanged = true
		changed = append(changed, "charly.yml inline box: -> box/")
	}

	// --- Phase 2: rename candy manifests candy.yml -> charly.yml ---
	candyDir := filepath.Join(dir, DefaultCandyDir)
	if entries, err := os.ReadDir(candyDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			from := filepath.Join(candyDir, e.Name(), "candy.yml")
			to := filepath.Join(candyDir, e.Name(), UnifiedFileName)
			if mod, err := renameProjectPath(from, to, dryRun); err != nil {
				return changed, err
			} else if mod {
				changed = append(changed, filepath.Join(DefaultCandyDir, e.Name(), UnifiedFileName))
			}
		}
	}

	// --- Phase 3: fold per-kind files into charly.yml's root mapping ---
	for _, pf := range foldKindFiles {
		p := filepath.Join(dir, pf)
		mod, err := foldKindFileInto(rootMap, p, dryRun)
		if err != nil {
			return changed, err
		}
		if mod {
			rootChanged = true
			if !dryRun {
				_ = os.Remove(p)
			}
			changed = append(changed, pf+" -> charly.yml")
		}
	}

	// --- Phase 4: drop build.yml import + delete a default-matching build.yml ---
	if rewriteBuildImport(rootMap, dir, dryRun) {
		rootChanged = true
		changed = append(changed, "build.yml import dropped")
	}

	// --- Phase 5: rewrite import: (drop folded files) + discover: (box+candy) ---
	if rewriteFoldedImports(rootMap) {
		rootChanged = true
	}
	if rewriteDiscover(rootMap) {
		rootChanged = true
		changed = append(changed, "discover: -> [box, candy]")
	}

	// --- write charly.yml back once ---
	if rootChanged && !dryRun {
		var buf bytes.Buffer
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(4)
		if err := enc.Encode(&rootDoc); err != nil {
			return changed, fmt.Errorf("encoding %s: %w", rootPath, err)
		}
		_ = enc.Close()
		bak := fmt.Sprintf("%s.bak.%d", rootPath, time.Now().Unix())
		_ = os.WriteFile(bak, rootData, 0o644)
		if err := os.WriteFile(rootPath, buf.Bytes(), 0o644); err != nil {
			return changed, fmt.Errorf("writing %s: %w", rootPath, err)
		}
	}

	return changed, nil
}

// splitBoxFile reads a root-shape box file (box.yml/base.yml), writes each box
// entry to box/<name>/charly.yml, and deletes the file. Missing/unparseable file
// or no box: map → no-op.
func splitBoxFile(dir, path string, dryRun bool) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, nil
	}
	var doc yaml.Node
	if yaml.Unmarshal(data, &doc) != nil {
		return false, nil
	}
	rootMap := docRootMapping(&doc)
	if rootMap == nil {
		return false, nil
	}
	boxVal := nodeMapValue(rootMap, "box")
	if boxVal == nil || boxVal.Kind != yaml.MappingNode || len(boxVal.Content) == 0 {
		return false, nil
	}
	if err := splitBoxMapping(dir, boxVal, dryRun); err != nil {
		return false, err
	}
	if !dryRun {
		if err := os.Remove(path); err != nil {
			return false, fmt.Errorf("removing %s: %w", path, err)
		}
	}
	return true, nil
}

// splitBoxMapping writes every <name>: <config> entry of a root-shape `box:`
// mapping to box/<name>/charly.yml as a kind-keyed `box:` doc. Idempotent: an
// existing box/<name>/charly.yml is left untouched.
func splitBoxMapping(dir string, boxMap *yaml.Node, dryRun bool) error {
	for i := 0; i+1 < len(boxMap.Content); i += 2 {
		name := boxMap.Content[i].Value
		cfg := boxMap.Content[i+1]
		if name == "" || cfg.Kind != yaml.MappingNode {
			continue
		}
		boxDir := filepath.Join(dir, DefaultBoxDir, name)
		dest := filepath.Join(boxDir, UnifiedFileName)
		if fileExists(dest) {
			continue // idempotent
		}
		if dryRun {
			continue
		}
		// Kind-keyed doc: box:\n  name: <name>\n  <cfg fields...>
		inner := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		inner.Content = append(inner.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "name"},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name})
		inner.Content = append(inner.Content, cfg.Content...)
		// Carry the entry's head comment (e.g. the box's leading "# …" doc).
		inner.HeadComment = boxMap.Content[i].HeadComment
		wrapper := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		wrapper.Content = append(wrapper.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "box"},
			inner)
		out := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{wrapper}}
		var buf bytes.Buffer
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(4)
		if err := enc.Encode(out); err != nil {
			return fmt.Errorf("encoding box %q: %w", name, err)
		}
		_ = enc.Close()
		if err := os.MkdirAll(boxDir, 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", boxDir, err)
		}
		if err := os.WriteFile(dest, buf.Bytes(), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", dest, err)
		}
	}
	return nil
}

// foldKindFileInto merges a per-kind file's kind keys (vm/pod/k8s/eval/...) into
// the charly.yml root mapping. version/import/discover/defaults are skipped.
// Returns whether the root mapping changed (caller deletes the file).
func foldKindFileInto(rootMap *yaml.Node, path string, dryRun bool) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, nil
	}
	var doc yaml.Node
	if yaml.Unmarshal(data, &doc) != nil {
		return false, nil
	}
	src := docRootMapping(&doc)
	if src == nil {
		return false, nil
	}
	changed := false
	for i := 0; i+1 < len(src.Content); i += 2 {
		key := src.Content[i].Value
		if !foldKindKeys[key] {
			continue // version/import/discover/defaults/etc.
		}
		if existing := nodeMapValue(rootMap, key); existing != nil {
			// Already present in the root — merge entries (root wins on conflict).
			if existing.Kind == yaml.MappingNode && src.Content[i+1].Kind == yaml.MappingNode {
				mergeMappingEntries(existing, src.Content[i+1])
				changed = true
			}
			continue
		}
		if dryRun {
			changed = true
			continue
		}
		rootMap.Content = append(rootMap.Content, src.Content[i], src.Content[i+1])
		changed = true
	}
	return changed, nil
}

// mergeMappingEntries appends src entries whose key is absent in dst (dst wins).
func mergeMappingEntries(dst, src *yaml.Node) {
	for i := 0; i+1 < len(src.Content); i += 2 {
		if nodeMapValue(dst, src.Content[i].Value) == nil {
			dst.Content = append(dst.Content, src.Content[i], src.Content[i+1])
		}
	}
}

// foldedImportNames are the per-kind import entries removed from import: (their
// content has been split into box/ dirs or folded into charly.yml's root).
var foldedImportNames = map[string]bool{
	"box.yml": true, "base.yml": true,
	"vm.yml": true, "pod.yml": true, "k8s.yml": true,
	"eval.yml": true, "local.yml": true, "android.yml": true,
}

// rewriteBuildImport drops the build.yml import. A flat local build.yml that
// byte-matches the embedded default is deleted (the embed supplies it); a
// customized local build.yml is LEFT imported (it overrides the embed). A remote
// `@github.../build.yml:vTAG` import is always dropped (the embed supplies the
// identical first-party vocabulary).
func rewriteBuildImport(rootMap *yaml.Node, dir string, dryRun bool) bool {
	imp := nodeMapValue(rootMap, "import")
	if imp == nil || imp.Kind != yaml.SequenceNode {
		return false
	}
	localBuild := filepath.Join(dir, "build.yml")
	localPresent := fileExists(localBuild)
	localMatchesEmbed := false
	if localPresent {
		if data, err := os.ReadFile(localBuild); err == nil {
			localMatchesEmbed = bytes.Equal(data, embeddedBuildYAML)
		}
	}
	changed := false
	var kept []*yaml.Node
	for _, item := range imp.Content {
		if item.Kind == yaml.ScalarNode {
			v := item.Value
			// Remote build.yml ref → always drop (embed supplies identical vocab).
			if strings.Contains(v, "/build.yml") && (strings.HasPrefix(v, "@") || strings.Contains(v, "github.com")) {
				changed = true
				continue
			}
			// Flat local build.yml → drop only if absent or default-matching;
			// keep a customized one (it overrides the embedded default).
			if v == "build.yml" {
				if !localPresent || localMatchesEmbed {
					changed = true
					if localMatchesEmbed && !dryRun {
						_ = os.Remove(localBuild)
					}
					continue
				}
			}
		}
		kept = append(kept, item)
	}
	imp.Content = kept
	return changed
}

// rewriteFoldedImports removes the per-kind file entries from import: (box.yml /
// base.yml / vm.yml / pod.yml / k8s.yml / eval.yml / local.yml / android.yml).
// Namespaced imports and anything else are kept verbatim.
func rewriteFoldedImports(rootMap *yaml.Node) bool {
	imp := nodeMapValue(rootMap, "import")
	if imp == nil || imp.Kind != yaml.SequenceNode {
		return false
	}
	changed := false
	var kept []*yaml.Node
	for _, item := range imp.Content {
		if item.Kind == yaml.ScalarNode && foldedImportNames[item.Value] {
			changed = true
			continue
		}
		kept = append(kept, item)
	}
	imp.Content = kept
	return changed
}

// rewriteDiscover sets discover: to scan BOTH box/ and candy/ with the single
// default manifest (charly.yml). Idempotent — already-correct discover is a no-op.
func rewriteDiscover(rootMap *yaml.Node) bool {
	want := buildDiscoverNode()
	disc := nodeMapValue(rootMap, "discover")
	if disc == nil {
		rootMap.Content = append(rootMap.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "discover"},
			want)
		return true
	}
	if discoverIsBoxCandy(disc) {
		return false
	}
	*disc = *want
	return true
}

func buildDiscoverNode() *yaml.Node {
	mk := func(path string) *yaml.Node {
		return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "path"},
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: path},
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "recursive"},
			{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"},
		}}
	}
	return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{mk("box"), mk("candy")}}
}

func discoverIsBoxCandy(disc *yaml.Node) bool {
	if disc.Kind != yaml.SequenceNode || len(disc.Content) != 2 {
		return false
	}
	paths := map[string]bool{}
	for _, spec := range disc.Content {
		if spec.Kind != yaml.MappingNode {
			return false
		}
		pv := nodeMapValue(spec, "path")
		if pv == nil {
			return false
		}
		if mv := nodeMapValue(spec, "manifest"); mv != nil && mv.Value != UnifiedFileName {
			return false
		}
		paths[pv.Value] = true
	}
	return paths["box"] && paths["candy"]
}

// docRootMapping returns the root MappingNode of a parsed document, or nil.
func docRootMapping(doc *yaml.Node) *yaml.Node {
	if doc == nil {
		return nil
	}
	n := doc
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return nil
		}
		n = n.Content[0]
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	return n
}

// removeMapKey deletes the key (and its value) from a MappingNode.
func removeMapKey(node *yaml.Node, key string) {
	if node == nil || node.Kind != yaml.MappingNode {
		return
	}
	var kept []*yaml.Node
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			continue
		}
		kept = append(kept, node.Content[i], node.Content[i+1])
	}
	node.Content = kept
}
