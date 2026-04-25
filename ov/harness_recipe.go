package main

// harness_recipe.go — `kind: recipe` entity (named harness recipes).
//
// A recipe is the minimum configuration the harness needs to drive an
// iteration loop: where to run (pod / vm / host), which AIs are eligible,
// the prompt template, the plateau policy, and any per-recipe substitution
// tokens.
//
// Recipes are reusable: one recipe can be invoked against multiple AIs
// (the recipe lists eligible AIs; `--ai NAME` picks at run time). Two
// recipes can share the same pod/vm/host without the harness caring;
// concurrent invocations against the same target are serialized via the
// per-target flock at /workspace/.harness/.lock.

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// HarnessRecipe is one entry under the top-level `recipe:` map.
//
// Authoring shape (one of pod/vm/host MUST be set; ResolveRecipeTarget validates):
//
//	recipe:
//	  bench-claude:
//	    description:
//	      feature: "Benchmark recipe — score claude against pending scenarios"
//	    pod: bench-pod                  # name of a running pod deployment
//	    ai: [claude]                    # eligible AI names
//	    plateau_iteration: 3
//	    max_iteration: 50
//	    tag: ""                         # Gherkin tag filter expression
//	    target_image: ""                # "" = pod's own image
//	    notes: true                     # persistent NOTES.md across runs
//	    mcp_endpoint: "http://localhost:18765/mcp"
//	    env:                            # arbitrary tokens; KEY → ${KEY} in prompt
//	      JUPYTER_MCP: "http://localhost:8888/mcp"
//	    prompt: |
//	      You are benchmarking ...
type HarnessRecipe struct {
	Description *Description `yaml:"description,omitempty"`

	// Target discriminator — exactly one must be set. ResolveRecipeTarget enforces.
	Pod  string `yaml:"pod,omitempty"`
	VM   string `yaml:"vm,omitempty"`
	Host bool   `yaml:"host,omitempty"`

	// Disposable opt-in — REQUIRED when Host==true. Mirrors the explicit-
	// only rule from /ov-dev:disposable enforced at harness boundary.
	Disposable bool `yaml:"disposable,omitempty"`

	// Eligible AI names (must reference entries in the project's `ai:` map).
	AI []string `yaml:"ai,omitempty"`

	// Iteration policy. `plateau_iteration` of 0 disables plateau detection;
	// `max_iteration` of 0 means unbounded.
	PlateauIteration int `yaml:"plateau_iteration,omitempty"`
	MaxIteration     int `yaml:"max_iteration,omitempty"`

	// Gherkin tag filter expression (passed to ov image test --tag).
	Tag string `yaml:"tag,omitempty"`

	// Image whose scenarios are scored. "" means "the target's own image"
	// (the pod's image for pod targets; the VM's image for vm targets;
	// for host targets the user must set this explicitly).
	TargetImage string `yaml:"target_image,omitempty"`

	// MCPEndpoint drives the canonical ${MCP_ENDPOINT} substitution token.
	// Pointer so unset (use default) and set-to-empty-string (disable) are
	// distinguishable on the YAML wire.
	MCPEndpoint *string `yaml:"mcp_endpoint,omitempty"`

	// Free-form substitution env. Each KEY becomes a ${KEY} token in the
	// prompt + AI argv with this recipe's value (overrides ai.env, falls
	// through to os.Getenv).
	Env map[string]string `yaml:"env,omitempty"`

	// Scenario carries the BDD scenarios the AI must make pass. The
	// harness scores these AGAINST the live running deployment named
	// in Deployment — the AI is expected to build, deploy, and test
	// the image themselves; the harness scores what they actually
	// deployed. AI sees the scenarios via the ${SCENARIOS} prompt
	// token (rendered as YAML).
	Scenario []Scenario `yaml:"scenario,omitempty"`

	// Deployment names the running deployment the harness scores
	// against. The AI must `ov deploy add <Deployment> <ref>` (or
	// equivalent) before exiting their iteration so the harness can
	// reach `ov-<Deployment>` for scoring. Required when Scenario is
	// non-empty.
	Deployment string `yaml:"deployment,omitempty"`

	// Prompt template. Standard ${TOKEN} substitution is applied per
	// iteration; see harness_substitute.go for the precedence chain.
	// Authors typically include ${SCENARIOS} so the AI sees what it
	// must make pass.
	Prompt string `yaml:"prompt,omitempty"`

	// Notes controls the persistent NOTES.md memory subsystem.
	// Pointer so the default (true) and an explicit `false` are
	// distinguishable; nil is treated as true.
	Notes *bool `yaml:"notes,omitempty"`
}

// NotesEnabled returns true unless the recipe explicitly opts out via
// `notes: false`. The default for unset (nil pointer) is true.
func (r *HarnessRecipe) NotesEnabled() bool {
	if r.Notes == nil {
		return true
	}
	return *r.Notes
}

// EffectiveMCPEndpoint resolves the canonical ${MCP_ENDPOINT} token value
// for this recipe:
//   - nil pointer (unset)        → DefaultMCPEndpoint
//   - non-nil, value=""          → "" (disabled by author)
//   - non-nil, value=<something> → that value verbatim
func (r *HarnessRecipe) EffectiveMCPEndpoint() string {
	if r.MCPEndpoint == nil {
		return DefaultMCPEndpoint
	}
	return *r.MCPEndpoint
}

// DefaultMCPEndpoint is the canonical ov-mcp bind URL. Recipes that omit
// `mcp_endpoint:` get this value substituted into their prompt; recipes
// that set it to "" disable the substitution explicitly.
const DefaultMCPEndpoint = "http://localhost:18765/mcp"

// ---------------------------------------------------------------------------
// Target discriminator
// ---------------------------------------------------------------------------

// TargetKind identifies which executor backend the harness uses for a recipe.
type TargetKind string

const (
	TargetKindPod  TargetKind = "pod"
	TargetKindVM   TargetKind = "vm"
	TargetKindHost TargetKind = "host"
)

// ResolveRecipeTarget validates the recipe's exactly-one-of {pod, vm, host}
// invariant and returns the discriminator + the named deployment (empty
// for host).
//
// Errors:
//   - zero of the three set → load-time error naming all three options
//   - more than one set → load-time error listing the conflicting fields
//   - host==true without disposable==true → safety error
func ResolveRecipeTarget(r *HarnessRecipe) (TargetKind, string, error) {
	set := []string{}
	if r.Pod != "" {
		set = append(set, fmt.Sprintf(`pod=%q`, r.Pod))
	}
	if r.VM != "" {
		set = append(set, fmt.Sprintf(`vm=%q`, r.VM))
	}
	if r.Host {
		set = append(set, "host=true")
	}
	if len(set) == 0 {
		return "", "", errors.New("recipe: must declare exactly one of: pod, vm, host (none set)")
	}
	if len(set) > 1 {
		return "", "", fmt.Errorf("recipe: must declare exactly one of: pod, vm, host. Found: %s", strings.Join(set, ", "))
	}
	switch {
	case r.Pod != "":
		return TargetKindPod, r.Pod, nil
	case r.VM != "":
		return TargetKindVM, r.VM, nil
	case r.Host:
		if !r.Disposable {
			return "", "", errors.New("recipe: host: true requires disposable: true (host runs edit the project tree on a per-run branch — opt in explicitly)")
		}
		return TargetKindHost, "", nil
	}
	return "", "", errors.New("recipe: ResolveRecipeTarget unreachable") // belt-and-suspenders
}

// ---------------------------------------------------------------------------
// Sentinel errors
// ---------------------------------------------------------------------------

var (
	// ErrNoRecipes fires when the project has no `recipe:` map.
	ErrNoRecipes = errors.New("harness: no recipes configured (add a 'recipe:' map to harness.yml)")

	// ErrRecipeNotFound fires when the requested recipe name is absent.
	ErrRecipeNotFound = errors.New("harness: recipe not found")
)

// ResolveRecipe returns the named recipe with target validation applied.
// Returns a *copy* so callers can mutate without poisoning the catalog.
func ResolveRecipe(catalog map[string]*HarnessRecipe, name string) (*HarnessRecipe, error) {
	if len(catalog) == 0 {
		return nil, ErrNoRecipes
	}
	if name == "" {
		return nil, fmt.Errorf("harness: recipe name required (available: %s)",
			strings.Join(SortedRecipeNames(catalog), ", "))
	}
	r, ok := catalog[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q (available: %s)",
			ErrRecipeNotFound, name, strings.Join(SortedRecipeNames(catalog), ", "))
	}
	out := *r
	if _, _, err := ResolveRecipeTarget(&out); err != nil {
		return nil, fmt.Errorf("recipe %q: %w", name, err)
	}
	return &out, nil
}

// SortedRecipeNames returns recipe names in alphabetical order.
func SortedRecipeNames(catalog map[string]*HarnessRecipe) []string {
	out := make([]string, 0, len(catalog))
	for k := range catalog {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// Listing
// ---------------------------------------------------------------------------

// PrintRecipes writes a human-readable table of configured recipes to w.
// Used by `ov harness list-recipe`.
func PrintRecipes(w io.Writer, catalog map[string]*HarnessRecipe) {
	if len(catalog) == 0 {
		fmt.Fprintln(w, "No recipes configured. Add a 'recipe:' map to harness.yml.")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tWHERE\tAI\tPLATEAU\tMAX\tNOTES\tSUMMARY")
	for _, name := range SortedRecipeNames(catalog) {
		r := catalog[name]
		where := "(invalid)"
		if k, n, err := ResolveRecipeTarget(r); err == nil {
			if n != "" {
				where = string(k) + ":" + n
			} else {
				where = string(k)
			}
		}
		ai := strings.Join(r.AI, ",")
		if ai == "" {
			ai = "(none)"
		}
		notes := "yes"
		if !r.NotesEnabled() {
			notes = "no"
		}
		summary := ""
		if r.Description != nil {
			summary = r.Description.Feature
		}
		if len(summary) > 50 {
			summary = summary[:47] + "..."
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%s\t%s\n",
			name, where, ai, r.PlateauIteration, r.MaxIteration, notes, summary)
	}
	_ = tw.Flush()
}
