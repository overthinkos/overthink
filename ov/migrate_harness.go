package main

// migrate_harness.go — `ov migrate harness`.
//
// One-shot, idempotent migrator from the legacy `benchmark:` block in
// overthink.yml to the new harness.yml file with kind:ai + kind:recipe
// entities. Also rewrites every layer.yml's `description: scenarios:`
// (plural) → `description: scenario:` (singular) and inner `tags:` →
// `tag:` to match the project-wide singular convention.
//
// Output is byte-stable on second run: re-running produces no diff.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// MigrateHarnessCmd is `ov migrate harness`.
type MigrateHarnessCmd struct {
	DryRun bool `long:"dry-run" help:"Print files that would be written; touch nothing"`
}

func (c *MigrateHarnessCmd) Run() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	written, err := MigrateHarness(MigrateHarnessOpts{Dir: cwd, DryRun: c.DryRun})
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

// MigrateHarnessOpts carries the migrator's inputs.
type MigrateHarnessOpts struct {
	Dir    string
	DryRun bool
}

// MigrateHarness performs the migration and returns the list of files
// written (or that would be written under --dry-run).
func MigrateHarness(opts MigrateHarnessOpts) ([]string, error) {
	overthinkYml := filepath.Join(opts.Dir, UnifiedFileName)
	if !fileExists(overthinkYml) {
		return nil, fmt.Errorf("no %s in %s", UnifiedFileName, opts.Dir)
	}

	// Read overthink.yml raw — we can't use LoadUnified because the
	// post-cutover loader hard-rejects the legacy `benchmark:` key.
	data, err := os.ReadFile(overthinkYml)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", overthinkYml, err)
	}

	var rootNode yaml.Node
	if err := yaml.Unmarshal(data, &rootNode); err != nil {
		return nil, fmt.Errorf("parse %s: %w", overthinkYml, err)
	}

	bench, benchKeyIdx, err := extractBenchmarkBlock(&rootNode)
	if err != nil {
		return nil, err
	}

	written := []string{}

	// If overthink.yml already has no benchmark: block, the migrator's
	// only remaining work is the layer description rename. Both halves
	// are idempotent — re-runs are no-ops.
	if bench != nil {
		harnessYmlPath := filepath.Join(opts.Dir, "harness.yml")
		harnessBody, err := buildHarnessYml(bench)
		if err != nil {
			return nil, err
		}
		if !opts.DryRun {
			if err := writeIfChanged(harnessYmlPath, harnessBody); err != nil {
				return nil, err
			}
		}
		written = append(written, harnessYmlPath)

		// Edit overthink.yml: drop benchmark: block, add harness.yml to includes:.
		if err := dropBenchmarkBlock(&rootNode, benchKeyIdx); err != nil {
			return nil, err
		}
		if err := ensureIncludeListed(&rootNode, "harness.yml"); err != nil {
			return nil, err
		}
		newOverthink, err := marshalNode(&rootNode)
		if err != nil {
			return nil, err
		}
		if !opts.DryRun {
			if err := writeIfChanged(overthinkYml, newOverthink); err != nil {
				return nil, err
			}
		}
		written = append(written, overthinkYml)
	}

	// Rewrite description.scenarios:/tags: → singular in every layer.yml
	// reachable under layers/. Idempotent — files already in singular form
	// are not touched.
	if changed, err := migrateLayerDescriptions(opts.Dir, opts.DryRun); err != nil {
		return nil, err
	} else {
		written = append(written, changed...)
	}

	// Rename .benchmark/ → .harness/ if the cache directory exists. Best-
	// effort; failure is logged but not fatal (user can rename manually).
	benchDir := filepath.Join(opts.Dir, ".benchmark")
	harnessDir := filepath.Join(opts.Dir, ".harness")
	if fileExists(benchDir) && !fileExists(harnessDir) {
		if !opts.DryRun {
			if err := os.Rename(benchDir, harnessDir); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to rename .benchmark → .harness: %v\n", err)
			} else {
				written = append(written, harnessDir+" (renamed from .benchmark)")
			}
		} else {
			written = append(written, harnessDir+" (would rename from .benchmark)")
		}
	}

	// .gitignore: rewrite .benchmark/ → .harness/ if found.
	if changed, err := migrateGitignore(filepath.Join(opts.Dir, ".gitignore"), opts.DryRun); err != nil {
		fmt.Fprintf(os.Stderr, "warning: gitignore rewrite: %v\n", err)
	} else if changed {
		written = append(written, filepath.Join(opts.Dir, ".gitignore"))
	}

	sort.Strings(written)
	return written, nil
}

// ---------------------------------------------------------------------------
// Legacy benchmark: block extraction
// ---------------------------------------------------------------------------

// legacyBenchmark mirrors the pre-cutover BenchmarkConfig schema for the
// migrator's eyes only. Defined locally so the loader code can keep the
// hard-error rejection without a parallel "transitional accept" path.
type legacyBenchmark struct {
	Runners []legacyRunner `yaml:"runners,omitempty"`
	Prompt  string         `yaml:"prompt,omitempty"`
}

type legacyRunner struct {
	Name        string            `yaml:"name"`
	Command     []string          `yaml:"command"`
	PromptVia   string            `yaml:"prompt_via,omitempty"`
	Env         map[string]string `yaml:"env,omitempty"`
	Timeout     string            `yaml:"timeout,omitempty"`
	WorkingDir  string            `yaml:"working_dir,omitempty"`
	Credentials []CredentialMount `yaml:"credentials,omitempty"`
}

// extractBenchmarkBlock walks the document's root mapping for the
// `benchmark:` key. Returns the decoded legacyBenchmark + the index of
// the key node in root.Content, or (nil, -1, nil) when absent.
func extractBenchmarkBlock(rootNode *yaml.Node) (*legacyBenchmark, int, error) {
	mapping := rootMapping(rootNode)
	if mapping == nil {
		return nil, -1, errors.New("overthink.yml: top-level is not a mapping")
	}
	for i := 0; i < len(mapping.Content); i += 2 {
		k := mapping.Content[i]
		if k.Kind != yaml.ScalarNode || k.Value != "benchmark" {
			continue
		}
		v := mapping.Content[i+1]
		var bench legacyBenchmark
		if err := v.Decode(&bench); err != nil {
			return nil, -1, fmt.Errorf("decoding legacy benchmark: block: %w", err)
		}
		return &bench, i, nil
	}
	return nil, -1, nil
}

// rootMapping returns the mapping node at the top of a parsed YAML doc,
// peeling the DocumentNode wrapper.
func rootMapping(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return nil
		}
		return rootMapping(n.Content[0])
	}
	if n.Kind == yaml.MappingNode {
		return n
	}
	return nil
}

// dropBenchmarkBlock removes Content[keyIdx:keyIdx+2] (the key node and
// its value node) from the root mapping in place.
func dropBenchmarkBlock(rootNode *yaml.Node, keyIdx int) error {
	mapping := rootMapping(rootNode)
	if mapping == nil {
		return errors.New("dropBenchmarkBlock: root is not a mapping")
	}
	if keyIdx < 0 || keyIdx+1 >= len(mapping.Content) {
		return errors.New("dropBenchmarkBlock: key index out of range")
	}
	mapping.Content = append(mapping.Content[:keyIdx], mapping.Content[keyIdx+2:]...)
	return nil
}

// ensureIncludeListed adds filename to the root's `includes:` sequence
// when missing; no-op when already present.
func ensureIncludeListed(rootNode *yaml.Node, filename string) error {
	mapping := rootMapping(rootNode)
	if mapping == nil {
		return errors.New("ensureIncludeListed: root is not a mapping")
	}
	for i := 0; i < len(mapping.Content); i += 2 {
		k := mapping.Content[i]
		if k.Value != "includes" {
			continue
		}
		v := mapping.Content[i+1]
		if v.Kind != yaml.SequenceNode {
			return fmt.Errorf("includes: expected sequence, got kind=%v", v.Kind)
		}
		for _, item := range v.Content {
			if item.Kind == yaml.ScalarNode && item.Value == filename {
				return nil // already present
			}
		}
		v.Content = append(v.Content, &yaml.Node{
			Kind:  yaml.ScalarNode,
			Tag:   "!!str",
			Value: filename,
		})
		return nil
	}
	// No includes: key — append one with our single entry.
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "includes"},
		&yaml.Node{
			Kind:    yaml.SequenceNode,
			Tag:     "!!seq",
			Content: []*yaml.Node{{Kind: yaml.ScalarNode, Tag: "!!str", Value: filename}},
		},
	)
	return nil
}

// ---------------------------------------------------------------------------
// harness.yml synthesis
// ---------------------------------------------------------------------------

// buildHarnessYml produces the new harness.yml body from the legacy
// benchmark block: ai: catalog from runners[], recipe.default: from the
// prompt + plateau defaults.
func buildHarnessYml(bench *legacyBenchmark) ([]byte, error) {
	var doc strings.Builder
	doc.WriteString(`# harness.yml — AI catalog (kind:ai) + harness recipes (kind:recipe).
# Produced by ` + "`ov migrate harness`" + `.
#
# Edit recipe.default.pod / .vm / .host below to point the harness at
# the deployment that should run iterations.

ai:
`)
	// Sort runners alphabetically for deterministic output.
	runners := make([]legacyRunner, len(bench.Runners))
	copy(runners, bench.Runners)
	sort.Slice(runners, func(i, j int) bool { return runners[i].Name < runners[j].Name })

	for _, r := range runners {
		doc.WriteString("  " + r.Name + ":\n")
		// description: stub
		summary := canonicalAISummary(r.Name)
		doc.WriteString("    description:\n")
		doc.WriteString("      feature: " + yamlScalarString(summary) + "\n")
		// command:
		doc.WriteString("    command: " + yamlInlineList(r.Command) + "\n")
		if r.PromptVia != "" {
			doc.WriteString("    prompt_via: " + r.PromptVia + "\n")
		}
		// version_command: synthesized per known runner.
		ver := canonicalVersionCommand(r.Name)
		doc.WriteString("    version_command: " + yamlInlineList(ver) + "\n")
		if r.Timeout != "" {
			doc.WriteString("    timeout: " + r.Timeout + "\n")
		}
		if len(r.Env) > 0 {
			doc.WriteString("    env:\n")
			for _, k := range sortedStringKeys(r.Env) {
				doc.WriteString("      " + k + ": " + yamlScalarString(r.Env[k]) + "\n")
			}
		}
		if len(r.Credentials) > 0 {
			doc.WriteString("    credential:\n")
			for _, cm := range r.Credentials {
				doc.WriteString("      - {src: " + cm.Src + ", dst: " + cm.Dst)
				if cm.Optional {
					doc.WriteString(", optional: true")
				}
				if cm.Mode != "" {
					doc.WriteString(", mode: " + cm.Mode)
				}
				doc.WriteString("}\n")
			}
		}
		doc.WriteString("\n")
	}

	// Build the eligible AI list for recipe.default.
	aiList := make([]string, len(runners))
	for i, r := range runners {
		aiList[i] = r.Name
	}

	// Apply token renames in the prompt (singular convention).
	prompt := bench.Prompt
	prompt = strings.ReplaceAll(prompt, "${MAX_ITERATIONS}", "${MAX_ITERATION}")
	prompt = strings.ReplaceAll(prompt, "${PLATEAU_ITERATIONS}", "${PLATEAU_ITERATION}")
	prompt = strings.ReplaceAll(prompt, "ov benchmark ", "ov harness ")
	// Prepend the canonical Memory block.
	prompt = memoryBlockPreamble + prompt

	doc.WriteString(`recipe:
  default:
    description:
      feature: "Default recipe migrated from legacy benchmark: block."
    # Where to run this recipe — exactly ONE of pod / vm / host:
    #   pod:  <name>   # name of a running pod deployment (ov start <name>)
    #   vm:   <name>   # name of a running VM (ov vm start <name>)
    #   host: true     # run on this host directly (requires disposable: true)
    pod: ""             # ← fill in the pod deployment name
    ai: ` + yamlInlineList(aiList) + `
    plateau_iteration: 3
    max_iteration: 50
    tag: ""
    target_image: ""
    notes: true
    mcp_endpoint: "` + DefaultMCPEndpoint + `"
    env: {}
    prompt: |
`)
	for _, line := range strings.Split(strings.TrimRight(prompt, "\n"), "\n") {
		doc.WriteString("      " + line + "\n")
	}

	return []byte(doc.String()), nil
}

const memoryBlockPreamble = `== Memory ==
Notes from previous runs (read-only context — do not edit by hand):

${NOTES}

If you discover something worth remembering for future runs of this recipe — a subtle
convention, a fragile test, a mis-tagged scenario, a workaround that took an hour to find —
append a brief note via:
    ov harness note append "<one-paragraph note>"

Notes are persistent across runs of recipe ${RECIPE_NAME}. Be terse. Future-you reads them.

`

// canonicalAISummary returns a one-line description for the named AI.
// Unknown names get a generic placeholder so the migrator never fabricates
// something misleading.
func canonicalAISummary(name string) string {
	switch name {
	case "claude":
		return "Anthropic Claude Code CLI — autonomous coding agent."
	case "codex":
		return "OpenAI Codex CLI — coding agent."
	case "gemini":
		return "Google Gemini CLI."
	default:
		return "AI CLI " + name + " (description placeholder; please fill in)."
	}
}

// canonicalVersionCommand returns the per-AI conventional --version
// invocation. Unknown names get a placeholder the user must verify.
func canonicalVersionCommand(name string) []string {
	switch name {
	case "claude":
		return []string{"claude", "--version"}
	case "codex":
		return []string{"codex", "--version"}
	case "gemini":
		return []string{"gemini", "--version"}
	default:
		return []string{name, "--version"}
	}
}

// yamlInlineList renders a string slice as a YAML flow sequence: [a, b, c].
// Quotes elements that contain whitespace, special chars, or look numeric.
func yamlInlineList(items []string) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, it := range items {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(yamlScalarString(it))
	}
	b.WriteByte(']')
	return b.String()
}

// yamlScalarString returns a YAML-safe scalar representation. Strings
// without special chars pass through unquoted; everything else is
// double-quoted with backslash escaping.
func yamlScalarString(s string) string {
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, " :,'\"#{}[]&*?|<>=!%@`\t\n") || isYamlSpecialKeyword(s) {
		// Use double quotes, escape backslash + double-quote.
		esc := strings.ReplaceAll(s, `\`, `\\`)
		esc = strings.ReplaceAll(esc, `"`, `\"`)
		return `"` + esc + `"`
	}
	return s
}

// isYamlSpecialKeyword returns true for tokens YAML 1.2 parses as
// non-string scalars (booleans + null + numerics) so we know to quote.
func isYamlSpecialKeyword(s string) bool {
	switch strings.ToLower(s) {
	case "true", "false", "yes", "no", "on", "off", "null", "~":
		return true
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true // all-digits → numeric, must quote
}

// sortedStringKeys returns the keys of m in alphabetical order.
func sortedStringKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// Output helpers
// ---------------------------------------------------------------------------

// marshalNode re-serializes a yaml.Node to bytes via yaml.v3, preserving
// comments and key order as much as possible.
func marshalNode(n *yaml.Node) ([]byte, error) {
	var b strings.Builder
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(4)
	if err := enc.Encode(n); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

// writeIfChanged writes data to path only when the on-disk content
// differs. Idempotent — re-runs produce no mtime churn when nothing
// changed.
func writeIfChanged(path string, data []byte) error {
	existing, err := os.ReadFile(path)
	if err == nil && string(existing) == string(data) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	return os.Rename(tmp, path)
}

// ---------------------------------------------------------------------------
// Layer description rewrite
// ---------------------------------------------------------------------------

// migrateLayerDescriptions walks layers/*/layer.yml and rewrites
// `description: scenarios:` → `description: scenario:` and inner
// `tags:` → `tag:`. Returns the list of files that changed (or would
// change under DryRun). Idempotent: files already singular are skipped.
func migrateLayerDescriptions(rootDir string, dryRun bool) ([]string, error) {
	var changed []string
	layersDir := filepath.Join(rootDir, "layers")
	if !fileExists(layersDir) {
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
		original, err := os.ReadFile(path)
		if err != nil {
			return changed, fmt.Errorf("read %s: %w", path, err)
		}
		updated := rewriteDescriptionPlurals(string(original))
		if updated == string(original) {
			continue
		}
		changed = append(changed, path)
		if !dryRun {
			if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
				return changed, fmt.Errorf("write %s: %w", path, err)
			}
		}
	}
	return changed, nil
}

// rewriteDescriptionPlurals applies the singular sweep to a layer.yml's
// content. Operates on text with line-anchored regexes so non-description
// `tags:` keys (rare; would only appear inside non-description blocks)
// are not touched.
//
// In overthink layer.yml files, `tags:` and `scenarios:` appear ONLY
// inside description: blocks (verified in the cutover plan), so a
// global key rewrite is safe.
func rewriteDescriptionPlurals(s string) string {
	// scenarios: → scenario: (only when key form, not a value containing
	// "scenarios:")
	out := strings.Builder{}
	for _, line := range splitLinesPreservingNewline(s) {
		out.WriteString(rewriteLayerLine(line))
	}
	return out.String()
}

func rewriteLayerLine(line string) string {
	// Preserve a final newline if present.
	end := ""
	if strings.HasSuffix(line, "\n") {
		end = "\n"
		line = strings.TrimSuffix(line, "\n")
	}
	trimmed := strings.TrimLeft(line, " \t")
	indentLen := len(line) - len(trimmed)
	indent := line[:indentLen]
	switch {
	case strings.HasPrefix(trimmed, "scenarios:"):
		return indent + "scenario:" + trimmed[len("scenarios:"):] + end
	case strings.HasPrefix(trimmed, "- scenarios:"):
		return indent + "- scenario:" + trimmed[len("- scenarios:"):] + end
	case strings.HasPrefix(trimmed, "tags:"):
		return indent + "tag:" + trimmed[len("tags:"):] + end
	case strings.HasPrefix(trimmed, "- tags:"):
		return indent + "- tag:" + trimmed[len("- tags:"):] + end
	default:
		return line + end
	}
}

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

// ---------------------------------------------------------------------------
// .gitignore rewrite
// ---------------------------------------------------------------------------

// migrateGitignore rewrites .benchmark/ → .harness/ in the project's
// .gitignore (if any). Idempotent: returns false (no change) when
// already migrated. Returns true when the file was rewritten (or
// would be rewritten under dryRun).
func migrateGitignore(path string, dryRun bool) (bool, error) {
	if !fileExists(path) {
		return false, nil
	}
	original, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	updated := strings.ReplaceAll(string(original), ".benchmark/", ".harness/")
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

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// avoid an unused-imports failure during incremental development.
var _ io.Reader = (*strings.Reader)(nil)
