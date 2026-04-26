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

// topoSortByDeclarationOrder returns scenarios in dependency-respecting
// order. Each Scenario.DependsOn entry is treated as an edge
// (dep -> dependent). Ties are broken by declaration index (the order
// scenarios appear in the input slice). Returns *CycleError on cycle.
//
// Scope: the input slice is treated as one connected scope (typically
// one recipe, though the helper itself doesn't care). Names referenced
// by DependsOn that don't appear in the input slice are treated as
// missing edges — the scenario waits forever, which surfaces as
// CycleError when nothing further can be processed. validateHarness-
// Semantics catches unknown names earlier with a clearer message.
func topoSortByDeclarationOrder(scenarios []Scenario) ([]Scenario, error) {
	if len(scenarios) == 0 {
		return nil, nil
	}
	// nameToIdx records first-occurrence declaration index per name.
	// (Duplicate names across scenarios are a recipe-author bug;
	// validateHarnessSemantics rejects them. Defensive: last-wins
	// here is fine because such input never reaches us.)
	nameToIdx := make(map[string]int, len(scenarios))
	for i, sc := range scenarios {
		nameToIdx[sc.Name] = i
	}

	// In-degree per scenario name; forward edges dep -> dependents.
	indeg := make(map[string]int, len(scenarios))
	for _, sc := range scenarios {
		indeg[sc.Name] = 0
	}
	fwd := make(map[string][]string)
	for _, sc := range scenarios {
		for _, dep := range sc.DependsOn {
			// Edge only counts if the dep is in scope; otherwise it's a
			// dangling reference (validator catches; defensive: ignore).
			if _, ok := nameToIdx[dep]; !ok {
				continue
			}
			fwd[dep] = append(fwd[dep], sc.Name)
			indeg[sc.Name]++
		}
	}

	// Initial ready set: in-degree zero, ordered by declaration index.
	ready := make([]string, 0, len(scenarios))
	for name, n := range indeg {
		if n == 0 {
			ready = append(ready, name)
		}
	}
	sortByDecl := func(slice []string) {
		sort.Slice(slice, func(i, j int) bool {
			return nameToIdx[slice[i]] < nameToIdx[slice[j]]
		})
	}
	sortByDecl(ready)

	out := make([]Scenario, 0, len(scenarios))
	for len(ready) > 0 {
		head := ready[0]
		ready = ready[1:]
		out = append(out, scenarios[nameToIdx[head]])
		for _, succ := range fwd[head] {
			indeg[succ]--
			if indeg[succ] == 0 {
				ready = append(ready, succ)
				sortByDecl(ready) // keep ordered every insertion
			}
		}
	}
	if len(out) != len(scenarios) {
		// At least one cycle (or dangling dep). Compute a cycle path
		// for the error message: any node still with indeg>0 sits on
		// or downstream of a cycle. Walk the residual graph.
		remaining := make([]string, 0)
		for name, n := range indeg {
			if n > 0 {
				remaining = append(remaining, name)
			}
		}
		sort.Strings(remaining)
		return nil, &CycleError{Cycle: remaining}
	}
	return out, nil
}

// firstUnmetDep returns the name of the first dep in sc.DependsOn
// whose verdict in verdictByName is anything other than "pass". A dep
// not yet in verdictByName means the topo-sort guarantees it'll run
// before sc — but if scoring callers somehow process out of order, an
// unknown dep also blocks (treated as not-yet-passed). Returns "" if
// every dep has a "pass" verdict (or sc has no deps).
//
// Used by RunRecipeScenariosLive to decide whether to probe a
// scenario or mark it skipped + cascade. Extracted to a named helper
// so unit tests can verify the cascade rule independently of the live
// podman path.
func firstUnmetDep(sc Scenario, verdictByName map[string]string) string {
	for _, dep := range sc.DependsOn {
		v, ok := verdictByName[dep]
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
