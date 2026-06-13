package main

// harness_score_kind.go — `kind: score` entity (the harness runner config).
//
// A score is the unit you actually invoke: `charly check run <score>`.
// It carries the target, AI list, plateau policy, prompt, deployment,
// MCP endpoint, notes toggle, env, AND `recipes:` — the ordered list
// of recipes whose scenarios this run evaluates against in EVERY
// iteration. The harness sums solved scenarios across the whole set
// each iteration — score increases monotonically as the AI solves more.
//
// Filename note: `harness_score.go` already houses the SCORING
// PRIMITIVES (Verdict, Classify, FingerprintScenario). This file
// houses the runner-config kind. Two distinct concerns; two distinct
// files.

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// HarnessScore — one entry under top-level `score:`
// ---------------------------------------------------------------------------

// HarnessScore is the runner config for `charly check run <name>`.
//
// Exactly one of `pod`/`vm`/`host` MUST be set (ResolveScoreTarget
// enforces). `recipes:` MUST be a non-empty list of names that resolve
// to entries in the project's `recipe:` map; the harness validator
// rejects empty lists, duplicates, and unresolved names at load time.
type HarnessScore struct {
	Description *Description `yaml:"description,omitempty"`

	// Target discriminator — exactly one must be set.
	Pod  string `yaml:"pod,omitempty"`
	VM   string `yaml:"vm,omitempty"`
	Host bool   `yaml:"host,omitempty"`

	// Disposable opt-in — REQUIRED when Host==true. Mirrors the
	// explicit-only rule from /charly-internals:disposable.
	Disposable bool `yaml:"disposable,omitempty"`

	// Eligible agent names (must reference entries in the `agent:` map).
	Agent []string `yaml:"agent,omitempty"`

	// PlateauIteration is the only loop bound. The loop exits after
	// this many consecutive non-improving iterations. 0 disables
	// plateau detection (loop runs until ctx cancelled or solved-all).
	PlateauIteration int `yaml:"plateau_iteration,omitempty"`

	// Progressive enables curriculum-style phase scoping. When true,
	// the harness runs phases in score.recipes order: phase 1 shows
	// only recipes[0]; phase N shows recipes[0..N-1]. Each phase has
	// its own iteration loop bounded by plateau_iteration; phases
	// advance on solved-all OR plateau. State (deployed pods,
	// fingerprints, NOTES.md) carries across phase boundaries.
	// Default: false (single-pass behavior — all recipes visible
	// from iter1, one iteration loop, single result.yml shape).
	Progressive bool `yaml:"progressive,omitempty"`

	// Gherkin tag filter expression.
	Tag string `yaml:"tag,omitempty"`

	// Image whose scenarios are scored. "" means "the target's own image".
	TargetImage string `yaml:"target_image,omitempty"`

	// MCPEndpoint drives the canonical ${MCP_ENDPOINT} substitution.
	// Pointer so unset (use default) and set-to-"" (disable) differ on
	// the YAML wire.
	MCPEndpoint *string `yaml:"mcp_endpoint,omitempty"`

	// Free-form substitution env. Each KEY becomes a ${KEY} token.
	Env map[string]string `yaml:"env,omitempty"`

	// Recipes is the ordered list of recipe names this score evaluates
	// against in every iteration. Order is significant: scenarios
	// concatenate in this order, and `${RECIPES}` renders blocks in
	// this order. MUST be non-empty; duplicates are rejected.
	Recipe []string `yaml:"recipe,omitempty"`

	// Deployment names the running deployment the harness scores
	// against. Required: the AI must `charly deploy add <Deployment> <ref>`
	// before exiting.
	Deploy string `yaml:"deploy,omitempty"`

	// Prompt template. Standard ${TOKEN} substitution applied per iter.
	Prompt string `yaml:"prompt,omitempty"`

	// Notes controls the persistent NOTES.md memory subsystem. Pointer
	// so the default (true) and an explicit `false` are distinguishable.
	Notes *bool `yaml:"note,omitempty"`

	// ValidateAiArtifacts narrows artifact-producing state-dependent
	// probes (the screenshot + record-stop methods listed in
	// artifactValidatableMethods) to validate the AI's iteration
	// artifact instead of re-running the capture. Re-running these
	// probes a few seconds later against the same logically-correct
	// state can yield different pixel bytes (animation frames, cursor
	// movement) and the AI's iteration moment is the canonical capture.
	//
	// All NON-state-dependent probes (status queries, checkuations,
	// listings, info dumps, file/process/package/port checks, libvirt
	// RPC queries, k8s queries, mcp queries) ALWAYS re-run independently
	// regardless of this flag. The harness remains authoritative for
	// everything the AI cannot prove by holding up a frozen artifact.
	//
	// A freshness mtime gate enforces that the artifact was written
	// during the current iteration (mtime ≥ iter start) — pre-staged
	// or stale files are rejected.
	//
	// Default false (all probes re-run, including artifact-producing).
	ValidateAiArtifacts bool `yaml:"validate_ai_artifacts,omitempty"`
}

// NotesEnabled returns true unless the score explicitly opts out.
func (s *HarnessScore) NotesEnabled() bool {
	if s.Notes == nil {
		return true
	}
	return *s.Notes
}

// EffectiveMCPEndpoint resolves the canonical ${MCP_ENDPOINT} value.
//
//   - nil pointer (unset)        → DefaultMCPEndpoint
//   - non-nil, value=""          → "" (disabled by author)
//   - non-nil, value=<something> → that value verbatim
func (s *HarnessScore) EffectiveMCPEndpoint() string {
	if s.MCPEndpoint == nil {
		return DefaultMCPEndpoint
	}
	return *s.MCPEndpoint
}

// DefaultMCPEndpoint is the canonical charly-mcp bind URL.
const DefaultMCPEndpoint = "http://localhost:18765/mcp"

// ---------------------------------------------------------------------------
// Target discriminator
// ---------------------------------------------------------------------------

// TargetKind identifies which executor backend the harness uses.
type TargetKind string

const (
	TargetKindPod  TargetKind = "pod"
	TargetKindVM   TargetKind = "vm"
	TargetKindHost TargetKind = "host"
)

// ResolveScoreTarget validates the score's exactly-one-of {pod, vm, host}
// invariant and returns the discriminator + the named deployment (empty
// for host).
func ResolveScoreTarget(s *HarnessScore) (TargetKind, string, error) {
	set := []string{}
	if s.Pod != "" {
		set = append(set, fmt.Sprintf(`pod=%q`, s.Pod))
	}
	if s.VM != "" {
		set = append(set, fmt.Sprintf(`vm=%q`, s.VM))
	}
	if s.Host {
		set = append(set, "host=true")
	}
	if len(set) == 0 {
		return "", "", errors.New("score: must declare exactly one of: pod, vm, host (none set)")
	}
	if len(set) > 1 {
		return "", "", fmt.Errorf("score: must declare exactly one of: pod, vm, host. Found: %s", strings.Join(set, ", "))
	}
	switch {
	case s.Pod != "":
		return TargetKindPod, s.Pod, nil
	case s.VM != "":
		return TargetKindVM, s.VM, nil
	case s.Host:
		if !s.Disposable {
			return "", "", errors.New("score: host: true requires disposable: true (host runs edit the project tree on a per-run branch — opt in explicitly)")
		}
		return TargetKindHost, "", nil
	}
	return "", "", errors.New("score: ResolveScoreTarget unreachable")
}

// ---------------------------------------------------------------------------
// Sentinel errors
// ---------------------------------------------------------------------------

var (
	// ErrNoScores fires when the project has no `score:` map.
	ErrNoScores = errors.New("check: no scores configured (add a 'score:' map to check.yml)")

	// ErrScoreNotFound fires when the requested score name is absent.
	ErrScoreNotFound = errors.New("harness: score not found")
)

// ---------------------------------------------------------------------------
// ResolveScore — load + target-validate one named score
// ---------------------------------------------------------------------------

// ResolveScore returns the named score with target validation applied.
// Returns a *copy* so callers can apply CLI overrides without poisoning
// the catalog.
func ResolveScore(catalog map[string]*HarnessScore, name string) (*HarnessScore, error) {
	if len(catalog) == 0 {
		return nil, ErrNoScores
	}
	if name == "" {
		return nil, fmt.Errorf("harness: score name required (available: %s)",
			strings.Join(SortedScoreNames(catalog), ", "))
	}
	s, ok := catalog[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q (available: %s)",
			ErrScoreNotFound, name, strings.Join(SortedScoreNames(catalog), ", "))
	}
	out := *s
	if _, _, err := ResolveScoreTarget(&out); err != nil {
		return nil, fmt.Errorf("score %q: %w", name, err)
	}
	return &out, nil
}

// SortedScoreNames returns score names in alphabetical order.
func SortedScoreNames(catalog map[string]*HarnessScore) []string {
	out := make([]string, 0, len(catalog))
	for k := range catalog {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// ResolveScoreRecipe — concatenate scenarios across the score's recipes
// ---------------------------------------------------------------------------

// ResolveScoreRecipe returns the merged scenario list across every
// recipe named in score.Recipe (in order), with each appended Scenario
// stamped with its source recipe name (for ${RECIPES} rendering).
//
// Errors:
//   - empty Recipes list
//   - duplicate names in Recipes
//   - any name not present in the recipe catalog
func ResolveScoreRecipe(score *HarnessScore, recipeCatalog map[string]*HarnessRecipe) ([]Scenario, []*HarnessRecipe, error) {
	if score == nil {
		return nil, nil, errors.New("ResolveScoreRecipe: nil score")
	}
	if len(score.Recipe) == 0 {
		return nil, nil, errors.New("score.recipes: must reference at least one recipe (got empty list)")
	}
	seen := make(map[string]bool, len(score.Recipe))
	var merged []Scenario
	var resolved []*HarnessRecipe
	for _, name := range score.Recipe {
		if seen[name] {
			return nil, nil, fmt.Errorf("score.recipes: duplicate recipe name %q (each recipe may appear at most once)", name)
		}
		seen[name] = true
		recipe, ok := recipeCatalog[name]
		if !ok {
			return nil, nil, fmt.Errorf("%w: %q referenced from score.recipes (available: %s)",
				ErrRecipeNotFound, name, strings.Join(SortedRecipeNames(recipeCatalog), ", "))
		}
		resolved = append(resolved, recipe)
		for _, sc := range recipe.Scenario {
			cp := sc
			cp.SourceRecipe = name
			merged = append(merged, cp)
		}
	}
	return merged, resolved, nil
}

// ---------------------------------------------------------------------------
// ${RECIPES} renderer
// ---------------------------------------------------------------------------

// RenderScoreRecipesYAML renders a per-recipe-grouped YAML block: one
// entry per included recipe, each with name + description + scenarios.
// Drives the ${RECIPES} substitution token. Order matches the input
// score.Recipe list.
func RenderScoreRecipesYAML(recipeNames []string, recipeCatalog map[string]*HarnessRecipe) string {
	type recipeBlock struct {
		Recipe      string     `yaml:"recipe"`
		Description string     `yaml:"description,omitempty"`
		Scenarios   []Scenario `yaml:"scenario,omitempty"`
	}
	if len(recipeNames) == 0 {
		return ""
	}
	blocks := make([]recipeBlock, 0, len(recipeNames))
	for _, name := range recipeNames {
		r, ok := recipeCatalog[name]
		if !ok {
			continue
		}
		desc := ""
		if r.Description != nil {
			desc = r.Description.Feature
		}
		blocks = append(blocks, recipeBlock{
			Recipe:      name,
			Description: desc,
			Scenarios:   r.Scenario,
		})
	}
	data, err := yaml.Marshal(blocks)
	if err != nil {
		return fmt.Sprintf("# error rendering recipes: %v", err)
	}
	return string(data)
}

// ---------------------------------------------------------------------------
// Listing
// ---------------------------------------------------------------------------

// PrintScores writes a human-readable table of configured scores to w.
// Used by `charly check list-score`.
func PrintScores(w io.Writer, catalog map[string]*HarnessScore) {
	if len(catalog) == 0 {
		fmt.Fprintln(w, "No scores configured. Add a 'score:' map to check.yml.")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tWHERE\tAGENT\tPLATEAU\tRECIPES\tNOTES\tSUMMARY")
	for _, name := range SortedScoreNames(catalog) {
		s := catalog[name]
		where := "(invalid)"
		if k, n, err := ResolveScoreTarget(s); err == nil {
			if n != "" {
				where = string(k) + ":" + n
			} else {
				where = string(k)
			}
		}
		ai := strings.Join(s.Agent, ",")
		if ai == "" {
			ai = "(none)"
		}
		recipes := strings.Join(s.Recipe, ",")
		if recipes == "" {
			recipes = "(none)"
		}
		notes := "yes"
		if !s.NotesEnabled() {
			notes = "no"
		}
		summary := ""
		if s.Description != nil {
			summary = s.Description.Feature
		}
		if len(summary) > 50 {
			summary = summary[:47] + "..."
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\t%s\n",
			name, where, ai, s.PlateauIteration, recipes, notes, summary)
	}
	_ = tw.Flush()
}
