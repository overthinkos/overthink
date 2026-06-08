package main

// migrate_require_image.go — `charly migrate`.
//
// One-shot migration for the 2026-05-12 schema cutover that hard-
// requires the `image:` field on every `target: pod` deploy entry.
//
// Pre-cutover the eval runner silently fell back to inspecting the
// running container's image ref via `containerImageRef`, which read
// stale OCI labels off volume-pinned containers and dropped any
// probes added after the seed image. The new validator
// (validateDeployRequiresImage) hard-errors at load time when the
// field is missing — this command is the one-shot remediation.
//
// Inference rules (in order; the first that matches wins):
//   1. `<base>/<instance>` deploy key → `image: <base>` (Pattern A
//      from /charly-core:deploy "Two supported deploy patterns").
//   2. `<base>-pod` deploy-key suffix → `image: <base>` (the
//      established multi-pod convention; see project deploy.yml's
//      jupyter-pod → image: jupyter, jupyter-ml-pod → image:
//      jupyter-ml, sway-browser-vnc-pod → image:
//      sway-browser-vnc).
//   3. Deploy key matches a kind:image entry name (same document) →
//      `image: <key>` (the exact-name match convention for ollama,
//      openwebui, immich-ml, …).
//   4. Sibling deploy entry already declares image:<value> for the
//      same key → reuse that value (cluster of related entries).
//   5. Otherwise the entry needs operator attention; we print a
//      WARNING line naming the entry but DON'T inject anything.
//      The operator either picks Pattern A (rename to <base>/<inst>)
//      or Pattern B (declare the explicit `image:` ref).
//
// Walks BOTH the project root's deploy.yml + every reachable
// included file AND the per-host ~/.config/ov/deploy.yml. Emits a
// `<file>.bak.<unix-ts>` rollback before writing each modified file.
// Idempotent: running again on a fully-migrated tree is a no-op.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// RequireImageResult is the per-file summary of injections applied.
type RequireImageResult struct {
	Path    string
	Changes []string
}

// MigrateRequireImage walks every deploy.yml reachable from cwd and
// (when includeHostFile is true) also the per-host
// ~/.config/ov/deploy.yml. Injects `image:` on every target:pod
// deploy entry that lacks it. Returns the list of touched files +
// any per-entry warnings the operator must resolve manually.
//
// The includeHostFile gate exists so tests can drive the migration
// against a tempdir without ever touching the operator's real state.
// The CLI command always passes true.
func MigrateRequireImage(cwd string, dryRun bool, includeHostFile bool) ([]RequireImageResult, []string, error) {
	var results []RequireImageResult
	var warnings []string

	// Compute the project-wide set of kind:image names ONCE so it
	// can drive inference rule 3 for the per-host file (which has
	// no project context of its own). LoadUnified is best-effort —
	// a project without overthink.yml just gets an empty set.
	projectImages := map[string]bool{}
	if uf, ok, _ := LoadUnified(cwd); ok && uf != nil {
		for name := range uf.Image {
			projectImages[name] = true
		}
	}

	// 1. Walk the project tree for any deploy.yml file. The project
	// may inline its deploy entries in overthink.yml, in deploy.yml,
	// or in a custom-named file under includes:; we err on the side
	// of inspecting every *.yml / *.yaml that COULD carry deploy
	// entries by checking each file for the structural pattern.
	walkErr := filepath.Walk(cwd, func(path string, info os.FileInfo, err error) error {
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
		res, w, perr := migrateRequireImageOneFile(path, dryRun, projectImages)
		if perr != nil {
			return perr
		}
		if res != nil {
			results = append(results, *res)
		}
		warnings = append(warnings, w...)
		return nil
	})
	if walkErr != nil {
		return nil, nil, walkErr
	}

	// 2. Per-host file. Skip if it's already inside cwd (would be a
	// duplicate walk; ~/.config/ov/deploy.yml is normally outside the
	// project tree anyway). Tests pass includeHostFile=false to avoid
	// touching operator state.
	if includeHostFile {
		hostPath, err := DeployConfigPath()
		// A host that has never run `charly config` has no ~/.config/ov/deploy.yml;
		// that is normal, not a migration failure — skip it silently.
		if _, statErr := os.Stat(hostPath); err == nil && hostPath != "" && statErr == nil {
			abs, _ := filepath.Abs(hostPath)
			cwdAbs, _ := filepath.Abs(cwd)
			if !strings.HasPrefix(abs, cwdAbs+string(os.PathSeparator)) {
				res, w, perr := migrateRequireImageOneFile(hostPath, dryRun, projectImages)
				if perr != nil {
					return results, warnings, perr
				}
				if res != nil {
					results = append(results, *res)
				}
				warnings = append(warnings, w...)
			}
		}
	}

	// Stable order for predictable test output.
	sort.Slice(results, func(i, j int) bool { return results[i].Path < results[j].Path })
	sort.Strings(warnings)
	return results, warnings, nil
}

// deployNodeImageRef returns a pod deploy entry's image reference, honoring
// BOTH the current `box:` key (the candy/box rebrand) and the legacy `image:`
// key (which the later box-rename migration step converts to `box:`). Either
// one already satisfies the image-reference requirement, so require-image must
// NOT warn/inject on a box-format pod deploy. Empty when neither is present.
func deployNodeImageRef(valNode *yaml.Node) string {
	if v := mapStringField(valNode, "box"); v != "" {
		return v
	}
	return mapStringField(valNode, "image")
}

// migrateRequireImageOneFile reads path, walks its YAML node tree to
// find every `deploy:` (or legacy `deployment:`) map, and injects
// `image:` on each pod-target entry that lacks it. Returns the
// per-file result, list of warnings, and any I/O error.
//
// Returns (nil, nil, nil) when the file isn't a deploy-carrying
// document or when no changes were needed.
func migrateRequireImageOneFile(path string, dryRun bool, extraImageNames map[string]bool) (*RequireImageResult, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		// Not a YAML file we can parse — skip silently rather than
		// blow up the walk on unrelated files (e.g. a fixture with
		// intentionally-malformed content).
		return nil, nil, nil
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, nil, nil
	}
	root := doc.Content[0]

	// Collect every deploy map referenced from this document. Deploy
	// entries can appear under root.deploy, root.deployment (legacy,
	// rejected at load time but still possible in stale files), or
	// nested under kind:deploy entities (rare).
	deployMaps := collectDeployMaps(root)
	if len(deployMaps) == 0 {
		return nil, nil, nil
	}

	// Build the kind:image name set from this same document AND the
	// caller-supplied extra set (project-wide images known to the
	// orchestrator) so the per-host file can apply rule 3 even when
	// its own document lists no kind:image entries.
	imageNames := collectImageNames(root)
	for name := range extraImageNames {
		imageNames[name] = true
	}

	// Build the deploy-name → already-declared-image mapping so we
	// can apply inference rule 2 (cluster-of-related-entries).
	declaredImages := map[string]string{}
	for _, m := range deployMaps {
		for i := 0; i+1 < len(m.Content); i += 2 {
			keyNode := m.Content[i]
			valNode := m.Content[i+1]
			if valNode.Kind != yaml.MappingNode {
				continue
			}
			if img := deployNodeImageRef(valNode); img != "" {
				declaredImages[keyNode.Value] = img
			}
		}
	}

	var changes []string
	var warnings []string
	mutated := false
	for _, m := range deployMaps {
		for i := 0; i+1 < len(m.Content); i += 2 {
			keyNode := m.Content[i]
			valNode := m.Content[i+1]
			if valNode.Kind != yaml.MappingNode {
				continue
			}
			target := mapStringField(valNode, "target")
			if target != "" && target != "pod" {
				continue
			}
			if deployNodeImageRef(valNode) != "" {
				continue
			}
			key := keyNode.Value

			// Inference rule 1: <base>/<instance> deploy key.
			if idx := strings.Index(key, "/"); idx > 0 {
				base := key[:idx]
				if injectImageField(valNode, base) {
					mutated = true
					changes = append(changes, fmt.Sprintf("injected image: %q on %q (Pattern A: <base>/<instance> key)", base, key))
				}
				continue
			}

			// Inference rule 2: <base>-pod deploy-key suffix.
			// Established multi-pod convention — see project
			// deploy.yml's jupyter-pod → image: jupyter,
			// jupyter-ml-pod → image: jupyter-ml.
			if strings.HasSuffix(key, "-pod") {
				base := strings.TrimSuffix(key, "-pod")
				if injectImageField(valNode, base) {
					mutated = true
					changes = append(changes, fmt.Sprintf("injected image: %q on %q (<base>-pod suffix convention)", base, key))
				}
				continue
			}

			// Inference rule 3: deploy key matches a kind:image entity.
			if imageNames[key] {
				if injectImageField(valNode, key) {
					mutated = true
					changes = append(changes, fmt.Sprintf("injected image: %q on %q (key matches kind:image entry)", key, key))
				}
				continue
			}

			// Inference rule 4: a sibling deploy entry already declares image:<key>.
			if img, ok := declaredImages[key]; ok && img != "" {
				if injectImageField(valNode, img) {
					mutated = true
					changes = append(changes, fmt.Sprintf("injected image: %q on %q (reused from sibling)", img, key))
				}
				continue
			}

			// Couldn't infer safely. Tell the operator.
			warnings = append(warnings, fmt.Sprintf("%s: deploy entry %q has no `image:` field and migration cannot infer one — pick Pattern A (rename to <base>/<instance>) or Pattern B (declare explicit `image:` ref). See /charly-core:deploy 'Two supported deploy patterns'.", path, key))
		}
	}

	if !mutated {
		return nil, warnings, nil
	}

	// Re-encode preserving comments + ordering via yaml.v3 Node.
	var buf strings.Builder
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(4)
	if err := enc.Encode(&doc); err != nil {
		return nil, warnings, fmt.Errorf("encoding %s: %w", path, err)
	}
	if err := enc.Close(); err != nil {
		return nil, warnings, fmt.Errorf("closing encoder for %s: %w", path, err)
	}

	if !dryRun {
		// Backup first; same pattern as MigrateLocalDeploy.
		bak := fmt.Sprintf("%s.bak.%d", path, time.Now().Unix())
		if err := os.WriteFile(bak, data, 0o644); err != nil {
			return nil, warnings, fmt.Errorf("writing backup %s: %w", bak, err)
		}
		if err := os.WriteFile(path, []byte(buf.String()), 0o644); err != nil {
			return nil, warnings, fmt.Errorf("writing %s: %w", path, err)
		}
	}
	return &RequireImageResult{Path: path, Changes: changes}, warnings, nil
}

// collectDeployMaps returns every yaml.MappingNode that holds deploy
// entries. We look for two forms: `deploy:` (current schema) and
// `deployment:` (legacy; load-time-rejected but still touched here so
// the migration can prep stale files for a follow-up rename).
func collectDeployMaps(root *yaml.Node) []*yaml.Node {
	if root == nil || root.Kind != yaml.MappingNode {
		return nil
	}
	var result []*yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		key := root.Content[i].Value
		val := root.Content[i+1]
		if (key == "deploy" || key == "deployment") && val.Kind == yaml.MappingNode {
			result = append(result, val)
		}
	}
	return result
}

// collectImageNames returns the set of kind:image entry names declared
// at root.image (the modern shape) or root.images (the legacy plural).
func collectImageNames(root *yaml.Node) map[string]bool {
	out := map[string]bool{}
	if root == nil || root.Kind != yaml.MappingNode {
		return out
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		key := root.Content[i].Value
		val := root.Content[i+1]
		if (key != "image" && key != "images") || val.Kind != yaml.MappingNode {
			continue
		}
		for j := 0; j+1 < len(val.Content); j += 2 {
			out[val.Content[j].Value] = true
		}
	}
	return out
}

// mapStringField returns the scalar value of node[name] when node is a
// MappingNode and the value is a scalar; "" otherwise.
func mapStringField(node *yaml.Node, name string) string {
	if node == nil || node.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == name {
			v := node.Content[i+1]
			if v.Kind == yaml.ScalarNode {
				return v.Value
			}
			return ""
		}
	}
	return ""
}

// injectImageField prepends image:<value> as the FIRST entry of the
// node's content, preserving the rest of the entry verbatim. Returns
// false if image: is already present (defensive — callers check this
// upstream too, so this is belt+suspenders for idempotence).
func injectImageField(node *yaml.Node, value string) bool {
	if node == nil || node.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == "image" {
			return false
		}
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "image"}
	valNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
	// Prepend so `image:` appears at the top of the entry — matches the
	// canonical authoring order in the docs (image:, target:, …).
	node.Content = append([]*yaml.Node{keyNode, valNode}, node.Content...)
	return true
}
