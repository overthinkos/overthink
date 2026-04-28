package main

// harness_recipe.go — `kind: recipe` entity (pure BDD spec).
//
// Post the 2026-04 kind split, a recipe is JUST a named bundle of BDD
// scenarios with an optional narrative description. Targets, AI lists,
// plateau policy, prompts, deployments, and MCP endpoints all live on
// `kind: score` (see harness_score_kind.go) — a score is the runner
// config that references one or more recipes via `recipes:`.

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
)

// HarnessRecipe is one entry under the top-level `recipe:` map.
//
// Authoring shape (pure spec — no targets, no AI, no prompt):
//
//	recipe:
//	  tier1-easy:
//	    description:
//	      feature: "Tier 1 — easy: marker file."
//	    scenario:
//	      - name: tier1-marker-file-exists
//	        steps:
//	          - then: "/etc/ov-bench-marker exists"
//	            file: /etc/ov-bench-marker
//	            exists: true
type HarnessRecipe struct {
	Description *Description `yaml:"description,omitempty"`

	// From carries optional `kind: layer|image|pod|vm` import directives
	// that get expanded at load time into synthetic Scenario entries.
	// See harness_recipe_from.go (ExpandRecipeFrom). After expansion this
	// slice is cleared and recipe.Scenario contains the imports first
	// followed by hand-written scenarios.
	From []HarnessRecipeFrom `yaml:"from,omitempty"`

	// Scenario carries the BDD scenarios the score will evaluate this
	// recipe against. The harness scores these against the live running
	// deployment named in the active score's `deployment:` field.
	Scenario []Scenario `yaml:"scenario,omitempty"`
}

// ---------------------------------------------------------------------------
// Sentinel errors
// ---------------------------------------------------------------------------

var (
	// ErrNoRecipes fires when the project has no `recipe:` map.
	ErrNoRecipes = errors.New("eval: no recipes configured (add a 'recipe:' map to eval.yml)")

	// ErrRecipeNotFound fires when a referenced recipe name is absent.
	ErrRecipeNotFound = errors.New("harness: recipe not found")
)

// ResolveRecipe returns the named recipe. Returns a *copy* so callers
// can mutate without poisoning the catalog.
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
// Used by `ov eval list-recipe`. Recipes are pure spec, so the table
// shows scenario count + description summary.
func PrintRecipes(w io.Writer, catalog map[string]*HarnessRecipe) {
	if len(catalog) == 0 {
		fmt.Fprintln(w, "No recipes configured. Add a 'recipe:' map to eval.yml.")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSCENARIOS\tSUMMARY")
	for _, name := range SortedRecipeNames(catalog) {
		r := catalog[name]
		summary := ""
		if r.Description != nil {
			summary = r.Description.Feature
		}
		if len(summary) > 60 {
			summary = summary[:57] + "..."
		}
		fmt.Fprintf(tw, "%s\t%d\t%s\n", name, len(r.Scenario), summary)
	}
	_ = tw.Flush()
}
