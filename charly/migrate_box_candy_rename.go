package main

// migrate_box_candy_rename.go — `charly migrate` step for the 2026-06 candy/box
// rebrand. The schema kinds were renamed: `layer:`→`candy:` and `image:`→`box:`
// across the whole authoring surface (YAML keys, kind discriminators, the
// per-kind filenames, and the layers/ directory).
//
// This step renames, in a project tree:
//   - mapping KEYS at EVERY depth — the top-level `image:`/`layer:` maps, the
//     kind-keyed `image:`/`layer:` wrappers, the `layer:` composition list
//     nested inside a candy body, the `image:` selector on pod/deploy/vm/k8s/
//     android nodes, and `add_layer:`→`add_candy:`. Exact-key match leaves
//     COMPOUND keys untouched (`image_default`, `imagelabel`, `layer_field`,
//     `layer_file`) — same boundary the Go struct tags draw.
//   - per-kind FILES: image.yml→box.yml, layer.yml→candy.yml (project root +
//     every layers/<name>/ directory).
//   - the DIRECTORY layers/→candy/.
//   - import:/discover: PATH scalars: image.yml→box.yml, layer.yml→candy.yml,
//     and the discover layers/ path → candy.
//
// Comment-preserving via the yaml.v3 node API; idempotent (a fully-migrated
// tree is a no-op); per-file .bak.<unix-ts> backups on key rewrites. File/dir
// renames use os.Rename so git rename-detection preserves history (the body is
// >99% similar after a key-only rewrite). TouchesHost is false, so remote-cache
// auto-migration renames fetched remote candy files too. See CHANGELOG.md.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// MigrateBoxCandyRename applies the candy/box rebrand to a project tree. When
// hostDeployPath is non-empty (the full `charly migrate` runner passes
// ~/.config/ov/deploy.yml; the project-only / remote-cache runner leaves it
// empty), the host deploy file's `image:` selector keys are renamed too, so the
// calver-schema stamp never lands a new version on a file still holding old
// keys.
func MigrateBoxCandyRename(dir, hostDeployPath string, dryRun bool) ([]string, error) {
	var changed []string

	if hostDeployPath != "" {
		if mod, err := rewriteBoxCandyFile(hostDeployPath, dryRun); err != nil {
			return changed, err
		} else if mod {
			changed = append(changed, hostDeployPath)
		}
	}

	// Phase A — rewrite KEYS + path scalars in every project YAML. Both the
	// pre-rename and post-rename filenames are processed so the step is
	// idempotent.
	rootFiles := []string{
		"overthink.yml", "image.yml", "box.yml", "base.yml", "build.yml",
		"vm.yml", "pod.yml", "k8s.yml", "local.yml", "android.yml",
		"deploy.yml", "eval.yml",
	}
	for _, f := range rootFiles {
		mod, err := rewriteBoxCandyFile(filepath.Join(dir, f), dryRun)
		if err != nil {
			return changed, err
		}
		if mod {
			changed = append(changed, f)
		}
	}
	for _, sub := range []string{"layers", "candy"} {
		subDir := filepath.Join(dir, sub)
		entries, err := os.ReadDir(subDir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			for _, fn := range []string{"layer.yml", "candy.yml"} {
				p := filepath.Join(subDir, e.Name(), fn)
				mod, err := rewriteBoxCandyFile(p, dryRun)
				if err != nil {
					return changed, err
				}
				if mod {
					changed = append(changed, filepath.Join(sub, e.Name(), fn))
				}
			}
		}
	}

	// Phase B — rename per-kind FILES.
	if mod, err := renameProjectPath(filepath.Join(dir, "image.yml"), filepath.Join(dir, "box.yml"), dryRun); err != nil {
		return changed, err
	} else if mod {
		changed = append(changed, "image.yml -> box.yml")
	}
	layersDir := filepath.Join(dir, "layers")
	if entries, err := os.ReadDir(layersDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			from := filepath.Join(layersDir, e.Name(), "layer.yml")
			to := filepath.Join(layersDir, e.Name(), "candy.yml")
			if mod, err := renameProjectPath(from, to, dryRun); err != nil {
				return changed, err
			} else if mod {
				changed = append(changed, filepath.Join("layers", e.Name(), "candy.yml"))
			}
		}
	}

	// Phase C — rename the DIRECTORY layers/ -> candy/ (after its files moved).
	if mod, err := renameProjectPath(layersDir, filepath.Join(dir, DefaultCandyDir), dryRun); err != nil {
		return changed, err
	} else if mod {
		changed = append(changed, "layers/ -> candy/")
	}

	return changed, nil
}

// rewriteBoxCandyFile rewrites one YAML file's mapping keys + discover/import
// path scalars. Returns false (no error) for a missing or unparseable file.
func rewriteBoxCandyFile(path string, dryRun bool) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return false, nil
	}
	keysChanged := renameBoxCandyKeys(&doc)
	pathsChanged := rewriteBoxCandyPathScalars(&doc)
	if !keysChanged && !pathsChanged {
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

// renameBoxCandyKeys recursively renames the exact mapping keys image->box,
// layer->candy, add_layer->add_candy at every depth. Compound keys that merely
// share a prefix (image_default, imagelabel, layer_field, layer_file) are left
// untouched because the match is exact.
func renameBoxCandyKeys(n *yaml.Node) bool {
	changed := false
	switch n.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, c := range n.Content {
			if renameBoxCandyKeys(c) {
				changed = true
			}
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := n.Content[i]
			switch key.Value {
			case "image":
				key.Value = "box"
				changed = true
			case "layer":
				key.Value = "candy"
				changed = true
			case "add_layer":
				key.Value = "add_candy"
				changed = true
			}
			if renameBoxCandyKeys(n.Content[i+1]) {
				changed = true
			}
		}
	}
	return changed
}

// rewriteBoxCandyPathScalars rewrites filename + directory path scalars so the
// import:/discover: statements point at the renamed files and directory. The
// filename rewrites (image.yml->box.yml, layer.yml->candy.yml) are safe
// anywhere; the bare "layers"->"candy" directory rewrite is scoped to the
// discover: subtree so an unrelated "layers" value elsewhere is never touched.
func rewriteBoxCandyPathScalars(n *yaml.Node) bool {
	changed := false
	var walk func(node *yaml.Node, inDiscover bool)
	walk = func(node *yaml.Node, inDiscover bool) {
		switch node.Kind {
		case yaml.DocumentNode:
			for _, c := range node.Content {
				walk(c, inDiscover)
			}
		case yaml.SequenceNode:
			for _, c := range node.Content {
				walk(c, inDiscover)
			}
		case yaml.MappingNode:
			for i := 0; i+1 < len(node.Content); i += 2 {
				key := node.Content[i]
				val := node.Content[i+1]
				childInDiscover := inDiscover || key.Value == "discover"
				if val.Kind == yaml.ScalarNode {
					if rewriteOnePathScalar(val, childInDiscover) {
						changed = true
					}
				} else {
					walk(val, childInDiscover)
				}
			}
		case yaml.ScalarNode:
			if rewriteOnePathScalar(node, inDiscover) {
				changed = true
			}
		}
	}
	walk(n, false)
	return changed
}

func rewriteOnePathScalar(node *yaml.Node, inDiscover bool) bool {
	switch node.Value {
	case "image.yml":
		node.Value = "box.yml"
		return true
	case "layer.yml":
		node.Value = "candy.yml"
		return true
	case "layers":
		if inDiscover {
			node.Value = "candy"
			return true
		}
	}
	// Remote layer refs embed the producer repo's layer directory, e.g.
	// "@github.com/org/repo/layers/<name>:vTAG" -> ".../candy/<name>:vTAG".
	// Only a value that looks like a remote ref (the segment before
	// "/layers/" carries a host dot, or it's an "@"-prefixed ref) is touched,
	// so an ordinary "/layers/" path elsewhere is left alone.
	if i := strings.Index(node.Value, "/layers/"); i >= 0 {
		if strings.HasPrefix(node.Value, "@") || strings.Contains(node.Value[:i], ".") {
			node.Value = node.Value[:i] + "/candy/" + node.Value[i+len("/layers/"):]
			return true
		}
	}
	return false
}

// renameProjectPath renames a file or directory if the source exists and the
// destination does not. Idempotent: a missing source or pre-existing
// destination is a no-op (a re-run after a completed rename).
func renameProjectPath(from, to string, dryRun bool) (bool, error) {
	if _, err := os.Stat(from); err != nil {
		return false, nil // already renamed or absent
	}
	if _, err := os.Stat(to); err == nil {
		return false, nil // destination already present
	}
	if dryRun {
		return true, nil
	}
	if err := os.Rename(from, to); err != nil {
		return false, fmt.Errorf("rename %s -> %s: %w", from, to, err)
	}
	return true, nil
}
