package main

// migrate_eval.go — `ov migrate eval`.
//
// One-shot, idempotent forward-only migrator from the
// (April-2026) `eval.yml` shape to the (April-2026 cutover-2)
// `eval.yml` shape. Strict forward-only: there is NO chain-aware
// handling of the pre-April-2026 `benchmark:` block. Anyone two
// cutovers behind must run `ov migrate harness` from a pre-April-2026
// `ov` release first, then upgrade and run `ov migrate eval`.
//
// What this migrator does:
//
//   Stage 1 — schema renames:
//     1. eval.yml → eval.yml (file rename in project root).
//     2. overthink.yml: rewrite `includes: eval.yml` → `eval.yml`.
//     3. layers/*/layer.yml: top-level `tests:` key → `eval:`.
//     4. image.yml: `tests:` → `eval:`, `deploy_tests:` → `deploy_eval:`.
//     5. deploy.yml: `tests:` → `eval:` per image entry.
//     6. Substitution tokens in score[*].prompt:
//        `${HARNESS_NONCE_<NAME>}` → `${EVAL_NONCE_<NAME>}`.
//     7. .harness/ → .eval/ directory rename if present.
//     8. .gitignore: `.harness/` → `.eval/`.
//
//   Stage 2 — bench/benchmark + fixture project-data renames:
//     9. deploy.yml: rename well-known bench aliases (eval-pod →
//        eval-pod, bench-vm → eval-vm, nested-eval-vm → nested-eval-vm).
//        Only exact matches against the well-known set are touched.
//    10. eval.yml: rewrite scenario `pod:` references; drop both
//        `bench-` and `fixture-` prefixes from recipe map keys and pod
//        references; rename score.target_image: bench-target →
//        eval-target; rewrite literal ov-fixture-<purpose> →
//        ov-<purpose> in scenario commands.
//
// What this migrator does NOT do:
//
//   - `kind: ai`, `kind: recipe`, `kind: score` and their YAML root
//     keys (`ai:`, `recipe:`, `score:`) stay generic — these names may
//     be reused for non-eval features later. NO eval-prefix.
//   - Generic substitution tokens (${RECIPES}, ${SCORE_NAME},
//     ${SCORE_DELTA}, ${SCENARIOS}, ${PLATEAU_*}) stay.
//   - Pre-April-2026 `benchmark:` blocks are NOT auto-migrated.
//   - Arbitrary user-chosen aliases containing the substring "bench"
//     are NOT touched (the migrator avoids guessing user intent;
//     only the well-known aliases above are renamed).
//
// Output is byte-stable on second run: re-running produces no diff.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MigrateEvalCmd is `ov migrate eval`.
type MigrateEvalCmd struct {
	DryRun bool `long:"dry-run" help:"Print files that would be written/renamed; touch nothing"`
}

func (c *MigrateEvalCmd) Run() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	written, err := MigrateEval(MigrateEvalOpts{Dir: cwd, DryRun: c.DryRun})
	if err != nil {
		return err
	}
	prefix := "wrote "
	if c.DryRun {
		prefix = "[dry-run] would write "
	}
	for _, p := range written {
		fmt.Println(prefix + p)
	}
	if len(written) == 0 {
		fmt.Println("Already migrated — nothing to do.")
	}
	return nil
}

// MigrateEvalOpts carries the migrator's inputs.
type MigrateEvalOpts struct {
	Dir    string
	DryRun bool
}

// well-known legacy aliases the migrator renames in deploy.yml + eval.yml.
// Only exact substring matches against this set are renamed; arbitrary
// user-chosen aliases that happen to contain "bench" are left alone.
// Order matters: longer keys MUST come first so prefix collisions resolve
// correctly (`nested-bench-vm` before `bench-vm`).
//
// Note: these strings deliberately reference the LEGACY names (with the
// `bench-` prefix). They must not be touched by automated sed sweeps —
// the migrator MUST know the old names to translate them.
var benchAliasOrder = []string{
	"nested-" + "bench-vm",
	"bench-" + "pod",
	"bench-" + "vm",
}
var benchAliasRenames = map[string]string{
	"nested-" + "bench-vm": "nested-eval-vm",
	"bench-" + "pod":       "eval-pod",
	"bench-" + "vm":        "eval-vm",
}

// MigrateEval performs the migration and returns the list of files
// written / renamed (or that would be under --dry-run).
func MigrateEval(opts MigrateEvalOpts) ([]string, error) {
	written := []string{}

	// Stage 1.1: harness.yml → eval.yml file rename.
	// Legacy filename (split to defeat sed sweeps): "harness" + ".yml"
	harnessYml := filepath.Join(opts.Dir, "harness"+".yml")
	evalYml := filepath.Join(opts.Dir, "eval.yml")
	if fileExists(harnessYml) && !fileExists(evalYml) {
		if !opts.DryRun {
			if err := os.Rename(harnessYml, evalYml); err != nil {
				return nil, fmt.Errorf("rename harness.yml → eval.yml: %w", err)
			}
		}
		written = append(written, evalYml+" (renamed from harness.yml)")
	}

	// Stage 1.2: overthink.yml: rewrite includes: harness.yml → eval.yml.
	if changed, err := rewriteOverthinkIncludes(opts.Dir, opts.DryRun); err != nil {
		return nil, err
	} else if changed {
		written = append(written, filepath.Join(opts.Dir, UnifiedFileName))
	}

	// Stage 1.3: layers/*/layer.yml: tests: → eval:.
	if changed, err := rewriteLayersTestsKey(opts.Dir, opts.DryRun); err != nil {
		return nil, err
	} else {
		written = append(written, changed...)
	}

	// Stage 1.4–1.5: overthink.yml + image.yml + deploy.yml + pod.yml +
	// vm.yml + k8s.yml + host.yml: tests: → eval:, deploy_tests: →
	// deploy_eval:. The unified overthink.yml file carries inline
	// image/pod/vm/k8s content with the same `tests:` / `deploy_tests:`
	// shape, so it gets the same line-anchored rewrite. Idempotent.
	for _, base := range []string{
		UnifiedFileName,
		"image.yml", "deploy.yml",
		"pod.yml", "vm.yml", "k8s.yml", "host.yml",
	} {
		path := filepath.Join(opts.Dir, base)
		if !pathExists(path) {
			continue
		}
		if changed, err := rewriteEvalKeysFile(path, opts.DryRun); err != nil {
			return nil, err
		} else if changed {
			written = append(written, path)
		}
	}

	// Stage 1.6 + Stage 2: rewrite eval.yml content (substitution tokens,
	// bench/fixture sweeps).
	if fileExists(evalYml) {
		if changed, err := rewriteEvalYml(evalYml, opts.DryRun); err != nil {
			return nil, err
		} else if changed {
			written = append(written, evalYml)
		}
	}

	// Stage 1.7: .harness/ → .eval/ dir rename.
	harnessDir := filepath.Join(opts.Dir, ".harness")
	evalDir := filepath.Join(opts.Dir, ".eval")
	if pathExists(harnessDir) && !pathExists(evalDir) {
		if !opts.DryRun {
			if err := os.Rename(harnessDir, evalDir); err != nil {
				fmt.Fprintf(os.Stderr,
					"warning: failed to rename .harness → .eval: %v\n", err)
			} else {
				written = append(written, evalDir+" (renamed from .harness)")
			}
		} else {
			written = append(written, evalDir+" (would rename from .harness)")
		}
	}

	// Stage 1.8: .gitignore: .harness/ → .eval/.
	if changed, err := rewriteGitignoreHarnessDir(filepath.Join(opts.Dir, ".gitignore"), opts.DryRun); err != nil {
		fmt.Fprintf(os.Stderr, "warning: gitignore rewrite: %v\n", err)
	} else if changed {
		written = append(written, filepath.Join(opts.Dir, ".gitignore"))
	}

	// Stage 2: deploy.yml well-known bench-alias renames.
	deployYml := filepath.Join(opts.Dir, "deploy.yml")
	if fileExists(deployYml) {
		if changed, err := rewriteBenchAliasesFile(deployYml, opts.DryRun); err != nil {
			return nil, err
		} else if changed {
			written = append(written, deployYml+" (bench-alias rename)")
		}
	}

	sort.Strings(written)
	// Deduplicate (the same file may have been touched by multiple stages).
	out := written[:0]
	seen := map[string]bool{}
	for _, p := range written {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out, nil
}

// rewriteOverthinkIncludes scans overthink.yml's includes: list for a
// `harness.yml` entry and replaces it with `eval.yml`. Idempotent.
func rewriteOverthinkIncludes(dir string, dryRun bool) (bool, error) {
	path := filepath.Join(dir, UnifiedFileName)
	if !fileExists(path) {
		return false, nil
	}
	original, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	updated := strings.ReplaceAll(string(original), "harness"+".yml", "eval.yml")
	if updated == string(original) {
		return false, nil
	}
	if !dryRun {
		if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
			return false, err
		}
	}
	return true, nil
}

// rewriteLayersTestsKey walks layers/*/layer.yml and rewrites top-level
// `tests:` key → `eval:`. Returns the list of files that changed (or
// would change under DryRun).
func rewriteLayersTestsKey(dir string, dryRun bool) ([]string, error) {
	var changed []string
	layersDir := filepath.Join(dir, "layers")
	if !pathExists(layersDir) {
		return changed, nil
	}
	entries, err := os.ReadDir(layersDir)
	if err != nil {
		return changed, fmt.Errorf("read %s: %w", layersDir, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(layersDir, entry.Name(), "layer.yml")
		if !fileExists(path) {
			continue
		}
		did, err := rewriteEvalKeysFile(path, dryRun)
		if err != nil {
			return changed, err
		}
		if did {
			changed = append(changed, path)
		}
	}
	return changed, nil
}

// rewriteEvalKeysFile rewrites top-level YAML keys within a file:
//
//	tests:        → eval:
//	deploy_tests: → deploy_eval:
//
// The rewrite is line-anchored, so the keys are matched only when they
// appear as YAML keys at the start of a line (any indent). Idempotent —
// a tree already migrated produces no changes.
func rewriteEvalKeysFile(path string, dryRun bool) (bool, error) {
	original, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	out := strings.Builder{}
	for _, line := range splitLinesPreservingNewline(string(original)) {
		out.WriteString(rewriteEvalKeyLine(line))
	}
	updated := out.String()
	if updated == string(original) {
		return false, nil
	}
	if !dryRun {
		if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
			return false, err
		}
	}
	return true, nil
}

// rewriteEvalKeyLine rewrites a single YAML line's top-level key when
// it matches `tests:` or `deploy_tests:`. Indentation preserved.
func rewriteEvalKeyLine(line string) string {
	end := ""
	if strings.HasSuffix(line, "\n") {
		end = "\n"
		line = strings.TrimSuffix(line, "\n")
	}
	trimmed := strings.TrimLeft(line, " \t")
	indentLen := len(line) - len(trimmed)
	indent := line[:indentLen]
	switch {
	case strings.HasPrefix(trimmed, "deploy_tests:"):
		return indent + "deploy_eval:" + trimmed[len("deploy_tests:"):] + end
	case strings.HasPrefix(trimmed, "tests:"):
		return indent + "eval:" + trimmed[len("tests:"):] + end
	default:
		return line + end
	}
}

// rewriteEvalYml applies the eval-yml-specific transformations:
//
//   - ${HARNESS_NONCE_<NAME>} → ${EVAL_NONCE_<NAME>}
//   - eval-pod / bench-vm / nested-eval-vm pod references → eval-equivalents
//   - bench-target value → eval-target
//   - bench: prefix in scenario commands → eval:
//   - bench-fixture-* / fixture-* recipe keys → bare <purpose>
//   - fixture-* pod-name references → bare <purpose>
//   - ov-fixture-<purpose> → ov-<purpose> in commands
func rewriteEvalYml(path string, dryRun bool) (bool, error) {
	original, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	updated := string(original)

	// Substitution tokens.
	updated = strings.ReplaceAll(updated, "${HARNESS_NONCE_", "${EVAL_NONCE_")

	// Bench-target informational value.
	updated = strings.ReplaceAll(updated, "bench-target", "eval-target")

	// Bench-pod / bench-vm / nested-eval-vm aliases (used in scenario
	// pod: fields and score.pod: fields). Replaced as substrings because
	// dotted-path children like nested-eval-vm.inner-app-pod must update
	// too. Order: longer keys first.
	for _, old := range benchAliasOrder {
		updated = strings.ReplaceAll(updated, old, benchAliasRenames[old])
	}

	// fixture- pod-name and recipe-key references.
	// Replace ov-bench-fixture-<purpose> and ov-fixture-<purpose> first
	// (consumer-side hostnames in scenario commands like
	// `curl http://ov-fixture-web:8080/`). Doing this BEFORE the bare
	// prefix drops avoids "ov-" + remaining-prefix collisions.
	updated = strings.ReplaceAll(updated, "ov-bench-fixture-", "ov-")
	updated = strings.ReplaceAll(updated, "ov-fixture-", "ov-")
	// Drop bench-fixture- prefix from recipe map keys and pod refs
	// (e.g. `bench-fixture-mcp` recipe → `mcp`). Order matters:
	// bench-fixture- BEFORE fixture- so the longer prefix wins.
	updated = strings.ReplaceAll(updated, "bench-fixture-", "")
	updated = strings.ReplaceAll(updated, "fixture-", "")

	if updated == string(original) {
		return false, nil
	}
	if !dryRun {
		if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
			return false, err
		}
	}
	return true, nil
}

// rewriteGitignoreHarnessDir replaces .harness/ with .eval/ in .gitignore.
func rewriteGitignoreHarnessDir(path string, dryRun bool) (bool, error) {
	if !fileExists(path) {
		return false, nil
	}
	original, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	updated := strings.ReplaceAll(string(original), ".eval/", ".eval/")
	if updated == string(original) {
		return false, nil
	}
	if !dryRun {
		if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
			return false, err
		}
	}
	return true, nil
}

// pathExists returns true for files OR directories. fileExists in
// layers.go returns false for directories, which broke our layers/
// walk; this helper covers both.
func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// splitLinesPreservingNewline splits s by '\n' while keeping each line's
// trailing newline. Used by line-anchored rewriters above.
func splitLinesPreservingNewline(s string) []string {
	var out []string
	for s != "" {
		idx := strings.IndexByte(s, '\n')
		if idx < 0 {
			out = append(out, s)
			return out
		}
		out = append(out, s[:idx+1])
		s = s[idx+1:]
	}
	return out
}

// rewriteBenchAliasesFile rewrites well-known legacy bench-* deploy
// aliases in deploy.yml. Substring-replace for cross-reference values
// (pod: eval-pod → pod: eval-pod) AND for top-level alias keys
// (eval-pod: → eval-pod:). Only the exact aliases in
// `benchAliasRenames` are renamed; arbitrary user-chosen aliases are
// untouched.
func rewriteBenchAliasesFile(path string, dryRun bool) (bool, error) {
	original, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	updated := string(original)
	for _, old := range benchAliasOrder {
		updated = strings.ReplaceAll(updated, old, benchAliasRenames[old])
	}
	if updated == string(original) {
		return false, nil
	}
	if !dryRun {
		if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
			return false, err
		}
	}
	return true, nil
}
