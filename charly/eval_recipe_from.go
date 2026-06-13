package main

// eval_recipe_from.go — composition primitive on `kind: recipe`.
//
// A recipe author can pull existing acceptance scenarios out of a candy, box,
// pod, or vm entity into a recipe by adding a `from:` block. Each entry expands
// AT LOAD TIME into the source entity's `scenario:` list (filter pipeline
// below), with the harness container (`pod:`) and an optional name prefix
// stamped onto each imported scenario.
//
// Scoring invariant: 1 imported Scenario = 1 ScenarioID = 1 point. Imported
// scenarios are scored identically to hand-written ones (the harness scorer
// cannot tell them apart).
//
// Filter pipeline per `from:` entry, in order:
//   1. `select:` allow-list by scenario name (if empty, keep all).
//   2. `exclude:` deny-list by scenario name (applied after select).

import (
	"fmt"
	"sort"
	"strings"
)

// HarnessRecipeFrom is one entry under a recipe's `from:` block. It pulls
// acceptance scenarios out of an existing entity (candy / box / pod / vm) and
// lets the expander stamp them onto the recipe as synthetic scenarios.
type HarnessRecipeFrom struct {
	Kind    string   `yaml:"kind"`              // candy | box | pod | vm
	Name    string   `yaml:"name"`              // entity name (matches uf.Candy/Box/Pod/VM)
	Pod     string   `yaml:"pod"`               // harness container name (becomes scenario.pod)
	Select  []string `yaml:"select,omitempty"`  // optional allow-list (Scenario.Name)
	Exclude []string `yaml:"exclude,omitempty"` // optional deny-list (Scenario.Name)
	Prefix  string   `yaml:"prefix,omitempty"`  // optional scenario-name prefix
}

// recipeFromKinds enumerates the valid `kind:` discriminator values.
var recipeFromKinds = []string{"candy", "box", "pod", "vm"}

// ExpandRecipeFrom resolves every `from:` directive on the recipe into
// synthetic scenarios appended to recipe.Scenario. The recipe's existing
// hand-written scenarios are left intact and ordered AFTER the imports.
//
// Returns an error if any `from:` entry references an unknown entity, has an
// invalid kind, ends up with zero scenarios after the filter pipeline (likely
// a typo), or produces a scenario name collision with another imported or
// hand-written scenario in the same recipe.
//
// The expander is idempotent: it consumes recipe.From and clears it, so
// re-invocation is a no-op.
func ExpandRecipeFrom(uf *UnifiedFile, layers map[string]*Candy, recipeName string, recipe *HarnessRecipe) error {
	if recipe == nil || len(recipe.From) == 0 {
		return nil
	}

	// Track the set of scenario names already in use so we can detect
	// collisions (against both hand-written and prior-import names).
	used := make(map[string]bool, len(recipe.Scenario))
	for _, sc := range recipe.Scenario {
		used[sc.Name] = true
	}

	var imported []Scenario
	for fromIdx, from := range recipe.From {
		if err := validateFromEntryShape(recipeName, fromIdx, from); err != nil {
			return err
		}

		scenarios, err := collectScenariosForFrom(uf, layers, from)
		if err != nil {
			return fmt.Errorf("recipe %q: from[%d] (kind=%s name=%q): %w", recipeName, fromIdx, from.Kind, from.Name, err)
		}
		scenarios = filterScenariosBySelect(scenarios, from.Select, from.Prefix)
		scenarios = filterScenariosByExclude(scenarios, from.Exclude, from.Prefix)
		if len(scenarios) == 0 {
			return fmt.Errorf("recipe %q: from[%d] (kind=%s name=%q): no scenarios survived the filter pipeline (select/exclude) — check filter names against the source entity's scenario: list",
				recipeName, fromIdx, from.Kind, from.Name)
		}

		for _, sc := range scenarios {
			name := scenarioImportName(from.Prefix, sc.Name)
			if used[name] {
				return fmt.Errorf("recipe %q: from[%d] (kind=%s name=%q): imported scenario name %q collides with an existing scenario in the same recipe — set a distinct `prefix:` or rename the conflicting hand-written scenario",
					recipeName, fromIdx, from.Kind, from.Name, name)
			}
			used[name] = true
			// Clone the scenario, overriding pod (the recipe's chosen
			// harness container) and stamping the scoped name.
			cloned := sc
			cloned.Name = name
			cloned.Pod = from.Pod
			imported = append(imported, cloned)
		}
	}

	// Imports first, hand-written scenarios after — preserves authoring
	// order intuition (the from: block reads BEFORE scenario:, so its
	// expansions appear first in the flat list).
	merged := make([]Scenario, 0, len(imported)+len(recipe.Scenario))
	merged = append(merged, imported...)
	merged = append(merged, recipe.Scenario...)
	recipe.Scenario = merged
	recipe.From = nil // idempotent: subsequent calls are no-ops
	return nil
}

// validateFromEntryShape enforces the structural invariants on a single
// `from:` entry that don't require entity-graph lookup. Used by
// ExpandRecipeFrom and (independently) by the harness validator.
func validateFromEntryShape(recipeName string, idx int, from HarnessRecipeFrom) error {
	if from.Kind == "" {
		return fmt.Errorf("recipe %q: from[%d]: missing required `kind:` field (one of: %s)",
			recipeName, idx, strings.Join(recipeFromKinds, ", "))
	}
	validKind := false
	for _, k := range recipeFromKinds {
		if from.Kind == k {
			validKind = true
			break
		}
	}
	if !validKind {
		return fmt.Errorf("recipe %q: from[%d]: invalid kind %q (one of: %s)",
			recipeName, idx, from.Kind, strings.Join(recipeFromKinds, ", "))
	}
	if from.Name == "" {
		return fmt.Errorf("recipe %q: from[%d] (kind=%s): missing required `name:` field — names the entity to import scenarios from",
			recipeName, idx, from.Kind)
	}
	if from.Pod == "" {
		return fmt.Errorf("recipe %q: from[%d] (kind=%s name=%q): missing required `pod:` field — names the harness container the imported scenarios will probe",
			recipeName, idx, from.Kind, from.Name)
	}
	return nil
}

// collectScenariosForFrom returns the unfiltered Scenario list for a `from:`
// entry. Candy/pod/vm read the entity's own top-level scenario: list; box
// walks the candy chain via CollectDescriptions.
func collectScenariosForFrom(uf *UnifiedFile, layers map[string]*Candy, from HarnessRecipeFrom) ([]Scenario, error) {
	switch from.Kind {
	case "candy":
		layer, ok := layers[from.Name]
		if !ok {
			return nil, fmt.Errorf("candy %q not found (available: %s)", from.Name, sortedMapKeys(layers))
		}
		if len(layer.scenario) == 0 {
			return nil, fmt.Errorf("candy %q has no scenario: list to import from", from.Name)
		}
		return append([]Scenario(nil), layer.scenario...), nil

	case "box":
		// Namespace-aware: a recipe may import from a box that lives in an
		// imported submodule. resolveBoxRef descends into the namespace and
		// returns that namespace's Config; CollectDescriptions then walks the
		// chain in the right Config keyed by the leaf name. Bare refs resolve
		// locally.
		cfg := uf.ProjectConfig()
		_, nsCfg, ok := cfg.resolveBoxRef(from.Name)
		if !ok {
			return nil, fmt.Errorf("box %q not found (available: %s)", from.Name, sortedBoxNames(uf))
		}
		set := CollectDescriptions(nsCfg, layers, leafName(from.Name))
		if set == nil {
			return nil, fmt.Errorf("box %q produced no scenarios (no candy in the chain has a scenario: list)", from.Name)
		}
		// Flatten the three sections into one slice. Authors who want
		// section-specific behaviour can use kind: candy instead.
		var out []Scenario
		for _, sec := range [][]LabeledDescription{set.Candy, set.Box, set.Deploy} {
			for _, ld := range sec {
				out = append(out, ld.Scenario...)
			}
		}
		return out, nil

	case "pod":
		pod, ok := uf.Pod[from.Name]
		if !ok {
			return nil, fmt.Errorf("pod %q not found (available: %s)", from.Name, sortedPodNames(uf))
		}
		return append([]Scenario(nil), pod.Scenario...), nil

	case "vm":
		vm, ok := uf.VM[from.Name]
		if !ok {
			return nil, fmt.Errorf("vm %q not found (available: %s)", from.Name, sortedVMNames(uf))
		}
		return append([]Scenario(nil), vm.Scenario...), nil
	}
	return nil, fmt.Errorf("unhandled kind %q (this is a bug — validateFromEntryShape should have caught this)", from.Kind)
}

// filterScenariosBySelect, when `select` is non-empty, keeps only scenarios
// whose Name (or prefixed name) appears in the select list.
func filterScenariosBySelect(scenarios []Scenario, sel []string, prefix string) []Scenario {
	if len(sel) == 0 {
		return scenarios
	}
	want := map[string]bool{}
	for _, s := range sel {
		want[s] = true
	}
	out := make([]Scenario, 0, len(scenarios))
	for _, sc := range scenarios {
		if want[sc.Name] || want[scenarioImportName(prefix, sc.Name)] {
			out = append(out, sc)
		}
	}
	return out
}

// filterScenariosByExclude drops scenarios whose Name (or prefixed name)
// appears in the exclude list. Applied AFTER select.
func filterScenariosByExclude(scenarios []Scenario, excl []string, prefix string) []Scenario {
	if len(excl) == 0 {
		return scenarios
	}
	deny := map[string]bool{}
	for _, e := range excl {
		deny[e] = true
	}
	out := make([]Scenario, 0, len(scenarios))
	for _, sc := range scenarios {
		if deny[sc.Name] || deny[scenarioImportName(prefix, sc.Name)] {
			continue
		}
		out = append(out, sc)
	}
	return out
}

// scenarioImportName returns the recipe-scoped name for an imported scenario:
// prefix-name when prefix is set, name otherwise.
func scenarioImportName(prefix, name string) string {
	if prefix != "" {
		return prefix + "-" + name
	}
	return name
}

// sortedMapKeys returns the keys of a map[string]*Candy sorted, for
// "available:" hint strings on errors.
func sortedMapKeys(m map[string]*Candy) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

func sortedBoxNames(uf *UnifiedFile) string {
	keys := make([]string, 0, len(uf.Box))
	for k := range uf.Box {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

func sortedPodNames(uf *UnifiedFile) string {
	keys := make([]string, 0, len(uf.Pod))
	for k := range uf.Pod {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

func sortedVMNames(uf *UnifiedFile) string {
	keys := make([]string, 0, len(uf.VM))
	for k := range uf.VM {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}
