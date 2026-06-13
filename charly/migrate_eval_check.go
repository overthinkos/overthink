package main

// 2026-06 eval→check rename cutover. The evaluation harness's schema vocabulary
// renames from "eval" to "check" so the YAML verb matches the renamed CLI verb
// (charly eval → charly check). This step renames the SCHEMA TOKENS only:
//
//   - the root `eval:` bed-registry key → `check:` (a MAPPING of kind:check bed
//     name → deploy node);
//   - the `eval_level:` field key → `check_level:` (per-box acceptance-depth rung);
//   - the `keep_eval_runs:` field key → `keep_check_runs:` (defaults retention);
//   - the `kind: eval` scalar value → `kind: check`.
//
// Entity NAMES (eval-pod, eval-base-layer, …) are author identifiers and are NOT
// renamed here — a third-party config keeps its own names, and the opencharly
// repo renames ITS own eval-* boxes/candies/beds via the cutover's filesystem
// sweep (git mv + reference rewrite), with this step layering the token renames
// on top. Comment-preserving (yaml.v3 node API); idempotent (a migrated config
// has no eval: / eval_level / keep_eval_runs / kind: eval left). TouchesHost
// false → remote-cache auto-migration applies it to fetched candy manifests.

import (
	"bytes"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// MigrateEvalCheck renames the eval-harness schema vocabulary to check across a
// project's candy/ + box/ dirs and root YAML siblings. Returns the rewritten
// file paths. Idempotent.
func MigrateEvalCheck(dir string, dryRun bool) ([]string, error) {
	var rewritten []string
	for _, path := range opUnifyCandidateFiles(dir) {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		dec := yaml.NewDecoder(bytes.NewReader(data))
		var docs []*yaml.Node
		changed := false
		for {
			var doc yaml.Node
			if derr := dec.Decode(&doc); derr != nil {
				break
			}
			d := doc
			if evalCheckDoc(&d) {
				changed = true
			}
			docs = append(docs, &d)
		}
		if !changed {
			continue
		}
		var out bytes.Buffer
		enc := yaml.NewEncoder(&out)
		enc.SetIndent(4)
		for _, d := range docs {
			if eerr := enc.Encode(d); eerr != nil {
				return rewritten, fmt.Errorf("encoding %s: %w", path, eerr)
			}
		}
		enc.Close()
		if !dryRun {
			if werr := os.WriteFile(path, out.Bytes(), 0o644); werr != nil {
				return rewritten, fmt.Errorf("writing %s: %w", path, werr)
			}
		}
		rewritten = append(rewritten, path)
	}
	return rewritten, nil
}

// evalCheckDoc renames the eval→check schema tokens in one document: the root
// `eval:` registry key → `check:`, plus the recursive field/value renames.
func evalCheckDoc(doc *yaml.Node) bool {
	root := rootMappingNode(doc)
	if root == nil {
		return false
	}
	changed := false
	// Root bed-registry key: eval: → check: (the only top-level eval: key — the
	// former check-list eval: was folded into scenario: by the op-unify step).
	if renameMappingKey(root, "eval", "check") {
		changed = true
	}
	if evalCheckWalk(root) {
		changed = true
	}
	return changed
}

// evalCheckWalk recurses every node renaming the field keys
// `eval_level`→`check_level` and `keep_eval_runs`→`keep_check_runs`, and the
// `kind: eval` scalar value → `check`.
func evalCheckWalk(n *yaml.Node) bool {
	if n == nil {
		return false
	}
	changed := false
	if n.Kind == yaml.MappingNode {
		if renameMappingKey(n, "eval_level", "check_level") {
			changed = true
		}
		if renameMappingKey(n, "keep_eval_runs", "keep_check_runs") {
			changed = true
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if k.Value == "kind" && v.Kind == yaml.ScalarNode && v.Value == "eval" {
				v.Value = "check"
				changed = true
			}
		}
	}
	for _, c := range n.Content {
		if evalCheckWalk(c) {
			changed = true
		}
	}
	return changed
}
