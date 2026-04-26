package main

// harness_score_deps.go — scenario-level dependency resolution.
//
// Two helpers used by RunRecipeScenariosLive:
//
//   - topoSortByDeclarationOrder: walks scenarios in dependency-respecting
//     order. Tie-breaks (no edge between two scenarios) by declaration
//     index, so recipes read top-to-bottom unless a depends_on: forces
//     reordering. Returns *CycleError on cycle (reusing graph.go's type).
//
//   - groupConsecutiveByPod: splits a topo-sorted slice into maximal
//     runs of same-pod scenarios. The scorer runs one `podman exec`
//     per bucket, so this preserves bucketed-execution efficiency
//     while honoring cross-pod dependency edges (a single pod may
//     legitimately span multiple buckets when a cross-pod dep splits
//     it).
//
// Validation (cycles, unknown depends_on, cross-recipe references) lives
// in validateHarnessSemantics — these helpers assume input has already
// passed validation; cycle detection here is defensive (fail-loud).

import "sort"

// scenarioKey is the canonical identity for a Scenario in the harness
// scoring pipeline. With the `from:` composition primitive, two recipes
// can legitimately import a scenario with the same name (e.g. both
// from-single-kind-selftest and from-composition-selftest import
// "sshd-binary"). Identity-by-Name is no longer unique across the
// merged recipe slice, so every scoring data structure that needs to
// associate state with a scenario keys by (SourceRecipe, Name).
//
// Validator-path callers may pass scenarios with empty SourceRecipe
// (pre-merge); they all share one recipe-scope, matching pre-cutover
// semantics where Name was the only identity.
type scenarioKey struct {
	recipe, name string
}

// keyOf returns the canonical (SourceRecipe, Name) identity for a
// scenario. Used by every cross-bucket lookup in the live scorer.
func keyOf(sc Scenario) scenarioKey {
	return scenarioKey{sc.SourceRecipe, sc.Name}
}

// topoSortByDeclarationOrder returns scenarios in dependency-respecting
// order. Each Scenario.DependsOn entry is treated as an edge
// (dep -> dependent). Ties are broken by declaration index (the order
// scenarios appear in the input slice). Returns *CycleError on cycle.
//
// Scope: DependsOn is intra-recipe (validator-enforced). When the input
// slice is the merged output of ResolveScoreRecipes, two recipes may
// legitimately import a scenario with the same name from different
// layers/images (e.g. both from-single-kind-selftest and
// from-composition-selftest import "sshd-binary"). To handle this
// without false-cycle errors, internal bookkeeping uses scenario INDEX
// (unique by construction) and DependsOn names are resolved within the
// SAME SourceRecipe. Validator-path callers pass scenarios with empty
// SourceRecipe → all share one recipe-scope, matching pre-merge
// semantics.
func topoSortByDeclarationOrder(scenarios []Scenario) ([]Scenario, error) {
	if len(scenarios) == 0 {
		return nil, nil
	}
	// (sourceRecipe, name) → index. Validator guarantees names are
	// unique within a recipe; cross-recipe collisions resolve via the
	// SourceRecipe scope.
	nameToIdx := make(map[scenarioKey]int, len(scenarios))
	for i, sc := range scenarios {
		nameToIdx[keyOf(sc)] = i
	}

	indeg := make([]int, len(scenarios))
	fwd := make([][]int, len(scenarios))
	for i, sc := range scenarios {
		for _, dep := range sc.DependsOn {
			depIdx, ok := nameToIdx[scenarioKey{sc.SourceRecipe, dep}]
			if !ok {
				// Dangling reference (validator catches earlier; defensive: ignore).
				continue
			}
			fwd[depIdx] = append(fwd[depIdx], i)
			indeg[i]++
		}
	}

	// Initial ready set: in-degree zero, ordered by declaration index.
	ready := make([]int, 0, len(scenarios))
	for i, n := range indeg {
		if n == 0 {
			ready = append(ready, i)
		}
	}
	sort.Ints(ready)

	out := make([]Scenario, 0, len(scenarios))
	for len(ready) > 0 {
		head := ready[0]
		ready = ready[1:]
		out = append(out, scenarios[head])
		for _, succ := range fwd[head] {
			indeg[succ]--
			if indeg[succ] == 0 {
				ready = append(ready, succ)
				sort.Ints(ready) // keep ordered every insertion
			}
		}
	}
	if len(out) != len(scenarios) {
		// At least one cycle. Surface names of the still-blocked
		// scenarios for the error message.
		remaining := make([]string, 0)
		for i, n := range indeg {
			if n > 0 {
				remaining = append(remaining, scenarios[i].Name)
			}
		}
		sort.Strings(remaining)
		return nil, &CycleError{Cycle: remaining}
	}
	return out, nil
}

// firstUnmetDep returns the name of the first dep in sc.DependsOn
// whose verdict (resolved within sc.SourceRecipe scope) is anything
// other than "pass". A dep not yet in verdictByKey means the topo-sort
// guarantees it'll run before sc — but if scoring callers somehow
// process out of order, an unknown dep also blocks (treated as
// not-yet-passed). Returns "" if every dep has a "pass" verdict (or
// sc has no deps).
//
// DependsOn entries are recipe-scoped names (intra-recipe per the
// validator), so the lookup combines `sc.SourceRecipe` with the dep
// name to produce a scenarioKey. This prevents cross-recipe bleed
// when two recipes both import a scenario with the same name (e.g.
// both `from-*-selftest` recipes importing `sshd-binary` from the
// same layer).
//
// Used by RunRecipeScenariosLive to decide whether to probe a
// scenario or mark it skipped + cascade. Extracted to a named helper
// so unit tests can verify the cascade rule independently of the live
// podman path.
func firstUnmetDep(sc Scenario, verdictByKey map[scenarioKey]string) string {
	for _, dep := range sc.DependsOn {
		v, ok := verdictByKey[scenarioKey{sc.SourceRecipe, dep}]
		if !ok || v != "pass" {
			return dep
		}
	}
	return ""
}

// groupConsecutiveByPod splits sorted into maximal runs of same-pod
// scenarios. Order within each bucket matches the input order.
// An empty input returns nil. A single scenario produces a single
// bucket. The function does not validate Pod (empty pod values
// would form their own bucket; validateHarnessSemantics rejects
// them upstream).
func groupConsecutiveByPod(sorted []Scenario) [][]Scenario {
	if len(sorted) == 0 {
		return nil
	}
	var buckets [][]Scenario
	cur := []Scenario{sorted[0]}
	curPod := sorted[0].Pod
	for _, sc := range sorted[1:] {
		if sc.Pod == curPod {
			cur = append(cur, sc)
			continue
		}
		buckets = append(buckets, cur)
		cur = []Scenario{sc}
		curPod = sc.Pod
	}
	buckets = append(buckets, cur)
	return buckets
}
