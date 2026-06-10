package main

// scenario_validate.go — shared validator for any Scenario list,
// regardless of whether it came from a harness recipe or a candy/box
// description.
//
// Promoted in the 2026-04 BDD/test/harness surface-cleanup cutover from
// the harness-only path (validateHarnessSemantics + validateRecipeScenario
// Dependencies in unified.go) so candy/box `description:` blocks get
// the SAME `depends_on:` + name-uniqueness enforcement that recipes have
// always had. Pre-cutover, depends_on in a description was silently
// accepted and silently ignored.
//
// Callers parametrize the rule set via ScenarioValidationContext:
//
//   - RequirePod=true     — every scenario MUST set `pod:`. Recipes only.
//                           BDD descriptions don't set pod (their target
//                           is the entity hosting the description), so
//                           they pass false.
//   - OwnerLabel          — error-message prefix identifying the
//                           container of the scenarios ("recipe foo",
//                           "candy redis", "box fedora-coder"). Lets
//                           one validator emit precise errors regardless
//                           of who called it.

import (
	"fmt"
	"strings"
)

// ScenarioValidationContext parametrizes ValidateScenarios for the
// caller's rule set + error framing.
type ScenarioValidationContext struct {
	// OwnerLabel is the prefix used in error messages, e.g.
	//   "recipe foo"            (from validateHarnessSemantics)
	//   "candy redis"           (from description loading)
	//   "box fedora-coder"    (from description loading)
	OwnerLabel string

	// RequirePod toggles enforcement of non-empty Scenario.Pod. True
	// for harness recipes (where pod IS the scoring target); false
	// for candy/box descriptions (which run against the entity
	// itself, no per-scenario pod selection).
	RequirePod bool
}

// ValidateScenarios runs every rule that applies to ANY Scenario list:
//
//   - name uniqueness within the list (depends_on resolution requires it)
//   - pod field present (when ctx.RequirePod is true)
//   - depends_on entries resolve intra-list (no cross-list refs)
//   - depends_on graph is acyclic (uses topoSortByDeclarationOrder)
//
// All error messages are prefixed with ctx.OwnerLabel so the caller's
// container is named precisely.
func ValidateScenarios(scenarios []Scenario, ctx ScenarioValidationContext) error {
	if len(scenarios) == 0 {
		return nil
	}

	// Pass 1: name uniqueness + pod requirement.
	known := make(map[string]bool, len(scenarios))
	names := make([]string, 0, len(scenarios))
	for i, sc := range scenarios {
		if sc.Name == "" {
			return fmt.Errorf("%s: scenario[%d]: missing required `name:` field (depends_on resolution requires unique scenario names)",
				ctx.OwnerLabel, i)
		}
		if known[sc.Name] {
			return fmt.Errorf("%s: duplicate scenario name %q (each scenario name must be unique for depends_on resolution)",
				ctx.OwnerLabel, sc.Name)
		}
		known[sc.Name] = true
		names = append(names, sc.Name)

		if ctx.RequirePod && sc.Pod == "" {
			return fmt.Errorf("%s: scenario %q: missing required `pod:` field — every scenario in a recipe must declare the container name its steps probe (the harness has no default scoring target)",
				ctx.OwnerLabel, sc.Name)
		}
	}

	// Pass 2: depends_on resolves intra-list.
	for _, sc := range scenarios {
		for _, dep := range sc.DependsOn {
			if dep == sc.Name {
				return fmt.Errorf("%s: scenario %q: depends_on cannot reference itself (%q)",
					ctx.OwnerLabel, sc.Name, dep)
			}
			if !known[dep] {
				suggestion := findSimilarName(dep, names)
				if suggestion != "" {
					return fmt.Errorf("%s: scenario %q: depends_on: unknown scenario %q (did you mean %q?). depends_on resolution is intra-list — the referenced scenario must live in the same recipe / description as the dependent",
						ctx.OwnerLabel, sc.Name, dep, suggestion)
				}
				return fmt.Errorf("%s: scenario %q: depends_on: unknown scenario %q. depends_on resolution is intra-list — the referenced scenario must live in the same recipe / description as the dependent (available scenarios: %s)",
					ctx.OwnerLabel, sc.Name, dep, strings.Join(names, ", "))
			}
		}
	}

	// Pass 3: cycle detection via the shared topo-sort.
	if _, err := topoSortByDeclarationOrder(scenarios); err != nil {
		if cycleErr, ok := err.(*CycleError); ok {
			return fmt.Errorf("%s: scenario depends_on cycle: %s",
				ctx.OwnerLabel, strings.Join(cycleErr.Cycle, " -> "))
		}
		return fmt.Errorf("%s: depends_on resolution failed: %w",
			ctx.OwnerLabel, err)
	}
	return nil
}
