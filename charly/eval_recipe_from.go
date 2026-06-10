package main

// harness_recipe_from.go — composition primitive on `kind: recipe`.
//
// A recipe author can pull existing tests out of a layer, image, pod, or vm
// entity into a recipe by adding a `from:` block. Each entry expands AT
// LOAD TIME into one synthetic Scenario per surviving Check (filter
// pipeline below), each carrying one Step that embeds the source Check.
//
// Scoring invariant: 1 Check = 1 ScenarioID = 1 point. Imported scenarios
// are scored identically to hand-written ones (the harness scorer cannot
// tell them apart). Enforced by TestImportedScenarioCountEqualsCheckCount
// in harness_recipe_from_test.go.
//
// Filter pipeline per `from:` entry, in order:
//   1. Section filter via `scope:` (default: layer + image + deploy).
//   2. Live-only verb filter (drops cdp/wl/dbus/vnc/mcp/record/spice/
//      libvirt/k8s when skip_live_only is true; default true).
//   3. `select:` allow-list by id (matches Check.ID OR the synthesized
//      scenario name; if empty, keep all).
//   4. `exclude:` deny-list by id (same matching rules).

import (
	"fmt"
	"sort"
	"strings"
)

// HarnessRecipeFrom is one entry under a recipe's `from:` block. It pulls
// tests out of an existing entity (layer / image / pod / vm) and lets the
// expander turn them into synthetic scenarios on the recipe.
//
// `source:` (added 2026-04 BDD/test/harness cleanup cutover) selects
// what gets imported:
//   - "tests" (default) — flat Check list from `tests:` / `deploy_tests:`
//     blocks. Each surviving Check expands to ONE synthetic Scenario
//     with ONE Step. Invariant: 1 Check = 1 ScenarioID = 1 point.
//   - "description" — rich Scenarios from the entity's `description:`
//     block, preserving Steps, DependsOn, OnFail. Invariant: 1
//     imported Scenario = 1 ScenarioID = 1 point.
//
// Only `kind: layer` and `kind: image` carry descriptions today;
// `source: description` with kind=pod or kind=vm fails validation.
type HarnessRecipeFrom struct {
	Kind         string   `yaml:"kind"`                     // layer | image | pod | vm
	Name         string   `yaml:"name"`                     // entity name (matches uf.Layer/Images/Pod/VM)
	Pod          string   `yaml:"pod"`                      // harness container name (becomes scenario.pod)
	Source       string   `yaml:"source,omitempty"`         // tests (default) | description
	Select       []string `yaml:"select,omitempty"`         // optional allow-list (Check.ID for tests, Scenario.Name for description)
	Exclude      []string `yaml:"exclude,omitempty"`        // optional deny-list (same matching as Select)
	Scope        []string `yaml:"scope,omitempty"`          // optional section filter (default: layer + image + deploy)
	Prefix       string   `yaml:"prefix,omitempty"`         // optional scenario-name prefix
	SkipLiveOnly *bool    `yaml:"skip_live_only,omitempty"` // optional; default true (drops live-only verbs)
}

// recipeFromKinds enumerates the valid `kind:` discriminator values.
var recipeFromKinds = []string{"candy", "box", "pod", "vm"}

// ExpandRecipeFrom resolves every `from:` directive on the recipe into
// synthetic scenarios appended to recipe.Scenario. The recipe's existing
// hand-written scenarios are left intact and ordered AFTER the imports.
//
// Returns an error if any `from:` entry references an unknown entity, has
// an invalid kind, ends up with zero checks after the filter pipeline
// (likely a typo), or produces a scenario name collision with another
// imported or hand-written scenario in the same recipe.
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

		switch from.sourceEffective() {
		case "description":
			scenarios, err := collectScenariosForFromDescription(uf, layers, from)
			if err != nil {
				return fmt.Errorf("recipe %q: from[%d] (kind=%s name=%q source=description): %w", recipeName, fromIdx, from.Kind, from.Name, err)
			}
			scenarios = filterScenariosBySelect(scenarios, from.Select, from.Prefix)
			scenarios = filterScenariosByExclude(scenarios, from.Exclude, from.Prefix)
			if len(scenarios) == 0 {
				return fmt.Errorf("recipe %q: from[%d] (kind=%s name=%q source=description): no scenarios survived the filter pipeline (select/exclude) — check filter names against the source entity's description: scenarios",
					recipeName, fromIdx, from.Kind, from.Name)
			}
			for _, sc := range scenarios {
				name := scenarioImportName(from.Prefix, sc.Name)
				if used[name] {
					return fmt.Errorf("recipe %q: from[%d] (kind=%s name=%q source=description): imported scenario name %q collides with an existing scenario in the same recipe — set a distinct `prefix:` or rename the conflicting hand-written scenario",
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

		default: // "tests" or empty
			checks, err := collectChecksForFrom(uf, layers, from)
			if err != nil {
				return fmt.Errorf("recipe %q: from[%d] (kind=%s name=%q): %w", recipeName, fromIdx, from.Kind, from.Name, err)
			}

			// Filter pipeline: scope → live-only → select → exclude.
			checks = filterByScope(checks, from.Scope)
			if from.skipLiveOnlyEffective() {
				checks = filterDropLiveOnly(checks)
			}
			checks = filterBySelect(checks, from.Select, from.Kind, from.Prefix)
			checks = filterByExclude(checks, from.Exclude, from.Kind, from.Prefix)

			if len(checks) == 0 {
				return fmt.Errorf("recipe %q: from[%d] (kind=%s name=%q): no tests survived the filter pipeline (scope/select/exclude/live-only) — check filter ids and `scope:` against the source entity",
					recipeName, fromIdx, from.Kind, from.Name)
			}

			for idx, c := range checks {
				name := synthScenarioName(from.Prefix, from.Kind, c, idx)
				if used[name] {
					return fmt.Errorf("recipe %q: from[%d] (kind=%s name=%q): synthesized scenario name %q collides with an existing scenario in the same recipe — set a distinct `prefix:` on this from-entry or rename the conflicting hand-written scenario",
						recipeName, fromIdx, from.Kind, from.Name, name)
				}
				used[name] = true

				imported = append(imported, Scenario{
					Name: name,
					Pod:  from.Pod,
					Step: []Step{
						{
							Then:  stepNarrative(c),
							Check: c,
						},
					},
				})
			}
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
		return fmt.Errorf("recipe %q: from[%d] (kind=%s): missing required `name:` field — names the entity to import tests from",
			recipeName, idx, from.Kind)
	}
	if from.Pod == "" {
		return fmt.Errorf("recipe %q: from[%d] (kind=%s name=%q): missing required `pod:` field — names the harness container the imported scenarios will probe",
			recipeName, idx, from.Kind, from.Name)
	}
	for _, s := range from.Scope {
		switch s {
		case "candy", "box", "deploy":
		default:
			return fmt.Errorf("recipe %q: from[%d] (kind=%s name=%q): invalid scope value %q (one of: layer, image, deploy)",
				recipeName, idx, from.Kind, from.Name, s)
		}
	}
	switch from.Source {
	case "", "tests":
		// default — flat-Check import path
	case "description":
		// Only layer/image carry descriptions today. pod/vm
		// descriptions could be added later but are absent.
		if from.Kind != "candy" && from.Kind != "box" {
			return fmt.Errorf("recipe %q: from[%d] (kind=%s name=%q): source=description is only valid for kind=layer or kind=image (pod and vm don't carry description: blocks)",
				recipeName, idx, from.Kind, from.Name)
		}
	default:
		return fmt.Errorf("recipe %q: from[%d] (kind=%s name=%q): invalid source %q (one of: tests, description)",
			recipeName, idx, from.Kind, from.Name, from.Source)
	}
	return nil
}

// sourceEffective returns the effective `source:` value with default "tests".
func (f HarnessRecipeFrom) sourceEffective() string {
	if f.Source == "" {
		return "tests"
	}
	return f.Source
}

// collectScenariosForFromDescription returns the unfiltered Scenario list
// for a `source: description` entry. Layer kinds read the layer's own
// description; image kinds walk the layer chain via CollectDescriptions.
func collectScenariosForFromDescription(uf *UnifiedFile, layers map[string]*Candy, from HarnessRecipeFrom) ([]Scenario, error) {
	switch from.Kind {
	case "candy":
		layer, ok := layers[from.Name]
		if !ok {
			return nil, fmt.Errorf("layer %q not found (available: %s)", from.Name, sortedMapKeys(layers))
		}
		if layer.Description == nil {
			return nil, fmt.Errorf("layer %q has no description: block to import scenarios from", from.Name)
		}
		return append([]Scenario(nil), layer.Description.Scenario...), nil

	case "box":
		// Namespace-aware: a recipe may import from an image that lives in an
		// imported submodule (e.g. `fedora.composition-source` after the box
		// inversion). resolveBoxRef descends into the namespace and returns
		// that namespace's Config; CollectDescriptions then walks the chain in
		// the right Config keyed by the leaf name. Bare refs resolve locally.
		cfg := uf.ProjectConfig()
		_, nsCfg, ok := cfg.resolveBoxRef(from.Name)
		if !ok {
			return nil, fmt.Errorf("box %q not found (available: %s)", from.Name, sortedBoxNames(uf))
		}
		set := CollectDescriptions(nsCfg, layers, leafName(from.Name))
		if set == nil {
			return nil, fmt.Errorf("image %q produced no descriptions (no layer in the chain has a description: block)", from.Name)
		}
		// Flatten the three sections into one slice. Authors who want
		// section-specific behaviour can use kind: layer instead.
		var out []Scenario
		for _, sec := range [][]LabeledDescription{set.Candy, set.Box, set.Deploy} {
			for _, ld := range sec {
				out = append(out, ld.Description.Scenario...)
			}
		}
		return out, nil
	}
	return nil, fmt.Errorf("unhandled kind %q for source=description", from.Kind)
}

// filterScenariosBySelect, when `select` is non-empty, keeps only
// scenarios whose Name (or prefixed name) appears in the select list.
// Mirrors filterBySelect but operates on Scenario.Name (not Check.ID).
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

// scenarioImportName returns the recipe-scoped name for an imported
// scenario (source: description). Mirrors synthScenarioName for the
// description path: prefix-name when prefix is set, name otherwise.
func scenarioImportName(prefix, name string) string {
	if prefix != "" {
		return prefix + "-" + name
	}
	return name
}

// collectChecksForFrom returns the unfiltered `[]Check` for a from entry,
// dispatching by kind. For layer/image, it walks the existing collection
// machinery; for pod/vm, it concats the entity-direct fields with whatever
// the underlying image baked in.
func collectChecksForFrom(uf *UnifiedFile, layers map[string]*Candy, from HarnessRecipeFrom) ([]Check, error) {
	switch from.Kind {
	case "candy":
		layer, ok := layers[from.Name]
		if !ok {
			return nil, fmt.Errorf("layer %q not found (available: %s)", from.Name, sortedMapKeys(layers))
		}
		out := make([]Check, 0, len(layer.tests))
		for _, c := range layer.tests {
			c.Origin = "candy:" + from.Name
			if c.Scope == "" {
				c.Scope = "build"
			}
			out = append(out, c)
		}
		return out, nil

	case "box":
		// Namespace-aware (see the kind:image case in collectScenariosForFromDescription).
		cfg := uf.ProjectConfig()
		_, nsCfg, ok := cfg.resolveBoxRef(from.Name)
		if !ok {
			return nil, fmt.Errorf("box %q not found (available: %s)", from.Name, sortedBoxNames(uf))
		}
		set := CollectEval(nsCfg, layers, leafName(from.Name))
		if set == nil {
			return nil, nil
		}
		// Flatten the three sections into one slice; the scope filter
		// step downstream picks which sections to keep.
		out := make([]Check, 0, len(set.Candy)+len(set.Box)+len(set.Deploy))
		out = append(out, set.Candy...)
		out = append(out, set.Box...)
		out = append(out, set.Deploy...)
		return out, nil

	case "pod":
		pod, ok := uf.Pod[from.Name]
		if !ok {
			return nil, fmt.Errorf("pod %q not found (available: %s)", from.Name, sortedPodNames(uf))
		}
		// If the pod wraps an image, walk the image's layer chain too.
		var out []Check
		if pod.Box != "" {
			if _, hasImage := uf.Box[pod.Box]; hasImage {
				cfg := uf.ProjectConfig()
				if set := CollectEval(cfg, layers, pod.Box); set != nil {
					out = append(out, set.Candy...)
					out = append(out, set.Box...)
					out = append(out, set.Deploy...)
				}
			}
		}
		// Append pod-direct tests.
		for _, c := range pod.Eval {
			c.Origin = "pod:" + from.Name
			if c.Scope == "" {
				c.Scope = "build"
			}
			out = append(out, c)
		}
		for _, c := range pod.DeployEval {
			c.Origin = "pod:" + from.Name
			c.Scope = "deploy"
			out = append(out, c)
		}
		return out, nil

	case "vm":
		vm, ok := uf.VM[from.Name]
		if !ok {
			return nil, fmt.Errorf("vm %q not found (available: %s)", from.Name, sortedVMNames(uf))
		}
		var out []Check
		for _, c := range vm.Eval {
			c.Origin = "vm:" + from.Name
			if c.Scope == "" {
				c.Scope = "build"
			}
			out = append(out, c)
		}
		for _, c := range vm.DeployEval {
			c.Origin = "vm:" + from.Name
			c.Scope = "deploy"
			out = append(out, c)
		}
		return out, nil
	}
	return nil, fmt.Errorf("unhandled kind %q (this is a bug — validateFromEntryShape should have caught this)", from.Kind)
}

// filterByScope keeps only checks whose effective scope is in the allowed
// set. An empty allowed slice means "keep all sections" (the default).
func filterByScope(checks []Check, allowed []string) []Check {
	if len(allowed) == 0 {
		return checks
	}
	allow := map[string]bool{}
	for _, s := range allowed {
		allow[s] = true
	}
	out := make([]Check, 0, len(checks))
	for _, c := range checks {
		// Scope on a check is "build" or "deploy". The author-facing
		// scope filter values are "candy" / "box" / "deploy".
		// The Origin annotation tells us which section the check came
		// from (layer:* for layer, image:* / pod:* / vm:* / deploy-default
		// for the entity itself). Map back to the author's vocabulary:
		section := scopeSection(c)
		if allow[section] {
			out = append(out, c)
		}
	}
	return out
}

// scopeSection maps a Check's (Scope, Origin) onto the author-facing
// scope vocabulary used in `from.scope:` ([layer | image | deploy]).
func scopeSection(c Check) string {
	if c.Scope == "deploy" {
		return "deploy"
	}
	if strings.HasPrefix(c.Origin, "candy:") {
		return "candy"
	}
	// box/pod/vm-direct build-scope checks all bucket as "box" for
	// filter purposes — they ship in the box's "Box" section of the
	// LabelEvalSet.
	return "box"
}

// filterDropLiveOnly removes checks that use a verb requiring live-
// container infrastructure (cdp / wl / dbus / vnc / mcp / record /
// spice / libvirt / k8s). These verbs don't compose cleanly into a
// generic harness sandbox and are dropped by default. Authors can
// re-enable per from-entry via `skip_live_only: false`.
func filterDropLiveOnly(checks []Check) []Check {
	out := make([]Check, 0, len(checks))
	for _, c := range checks {
		if c.Cdp != "" || c.Wl != "" || c.Dbus != "" || c.Vnc != "" ||
			c.Mcp != "" || c.Record != "" || c.Spice != "" ||
			c.Libvirt != "" || c.K8s != "" {
			continue
		}
		out = append(out, c)
	}
	return out
}

// filterBySelect, when `select` is non-empty, keeps only checks whose ID
// (or synthesized name) appears in the select list.
func filterBySelect(checks []Check, sel []string, kind, prefix string) []Check {
	if len(sel) == 0 {
		return checks
	}
	want := map[string]bool{}
	for _, s := range sel {
		want[s] = true
	}
	out := make([]Check, 0, len(checks))
	for idx, c := range checks {
		if want[c.ID] || want[synthScenarioName(prefix, kind, c, idx)] {
			out = append(out, c)
		}
	}
	return out
}

// filterByExclude drops checks whose ID (or synthesized name) appears in
// the exclude list. Applied AFTER select.
func filterByExclude(checks []Check, excl []string, kind, prefix string) []Check {
	if len(excl) == 0 {
		return checks
	}
	deny := map[string]bool{}
	for _, e := range excl {
		deny[e] = true
	}
	out := make([]Check, 0, len(checks))
	for idx, c := range checks {
		if deny[c.ID] || deny[synthScenarioName(prefix, kind, c, idx)] {
			continue
		}
		out = append(out, c)
	}
	return out
}

// synthScenarioName produces the scenario name for one imported Check.
// Uses Check.ID when set (with optional prefix); otherwise synthesizes
// a stable name from (prefix, kind, Origin annotation, index).
func synthScenarioName(prefix, kind string, c Check, idx int) string {
	if c.ID != "" {
		if prefix != "" {
			return prefix + "-" + c.ID
		}
		return c.ID
	}
	originSlug := strings.ReplaceAll(c.Origin, ":", "-")
	if originSlug == "" {
		originSlug = kind
	}
	base := fmt.Sprintf("%s-%d", originSlug, idx)
	if prefix != "" {
		return prefix + "-" + base
	}
	return base
}

// stepNarrative produces a Gherkin-style `then:` string describing what
// the imported Check does. Falls back to a synthesized line when the
// source Check carries no narrative.
func stepNarrative(c Check) string {
	switch {
	case c.File != "":
		return fmt.Sprintf("file %s exists", c.File)
	case c.Package != "":
		return fmt.Sprintf("package %s is installed", c.Package)
	case c.Service != "":
		return fmt.Sprintf("service %s is running", c.Service)
	case c.Port != 0:
		return fmt.Sprintf("port %d is listening", c.Port)
	case c.Process != "":
		return fmt.Sprintf("process %s is running", c.Process)
	case c.Command != "":
		return fmt.Sprintf("command exits successfully: %s", trimNarrative(c.Command))
	case c.HTTP != "":
		return fmt.Sprintf("HTTP %s responds", c.HTTP)
	case c.Addr != "":
		return fmt.Sprintf("addr %s is reachable", c.Addr)
	case c.User != "":
		return fmt.Sprintf("user %s exists", c.User)
	case c.Group != "":
		return fmt.Sprintf("group %s exists", c.Group)
	case c.Interface != "":
		return fmt.Sprintf("interface %s is configured", c.Interface)
	case c.KernelParam != "":
		return fmt.Sprintf("kernel param %s is set", c.KernelParam)
	case c.Mount != "":
		return fmt.Sprintf("mount %s is active", c.Mount)
	case c.DNS != "":
		return fmt.Sprintf("dns %s resolves", c.DNS)
	}
	if c.ID != "" {
		return fmt.Sprintf("imported check %q passes", c.ID)
	}
	return "imported check passes"
}

// trimNarrative truncates very long command strings for narrative use.
func trimNarrative(s string) string {
	const max = 60
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

// skipLiveOnlyEffective returns the bool value for SkipLiveOnly with the
// default (true) when the field is unset.
func (f HarnessRecipeFrom) skipLiveOnlyEffective() bool {
	if f.SkipLiveOnly == nil {
		return true
	}
	return *f.SkipLiveOnly
}

// sortedMapKeys returns the keys of a map[string]*Layer sorted, for
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
