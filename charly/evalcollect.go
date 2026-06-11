package main

// CollectEval walks the base-image chain for boxName and gathers all
// declarative checks into a three-section LabelEvalSet. The structure of
// the walk mirrors CollectHooks (charly/hooks.go) — dedupe by candy name, step
// through internal bases until an external image is hit — so candy ordering
// is consistent across every collected label.
//
// Section assignment rules:
//
//   - Candy-defined checks with no scope (or scope:"build") land in Candy.
//   - Candy-defined checks with scope:"deploy" land in Deploy.
//   - Box-level Tests default to scope:"build" → Box section;
//     scope:"deploy" routes to Deploy.
//   - Box-level DeployEval always land in Deploy (scope forced to "deploy").
//
// Each check receives an Origin annotation for reporting:
// "candy:<name>", "box:<name>", or "deploy-default" (for box deploy entries).
//
// Returns nil if every section is empty — callers (generate.go) skip label
// emission in that case.
func CollectEval(cfg *Config, layers map[string]*Candy, boxName string) *LabelEvalSet {
	set := &LabelEvalSet{}

	// The base-chain candy walk (boxCandyChain) is the ONE shared traversal —
	// candy-order per level, internal bases stepped through, deduped, cycle-safe
	// (validateBoxDAG reports the cycle itself).
	allCandyNames, _ := cfg.boxCandyChain(layers, boxName)
	for _, candyName := range allCandyNames {
		layer, ok := layers[candyName]
		if !ok {
			continue
		}
		for _, c := range layer.tests {
			c.Origin = "candy:" + candyName
			switch c.Scope {
			case "deploy":
				set.Deploy = append(set.Deploy, c)
			default:
				c.Scope = "build"
				set.Candy = append(set.Candy, c)
			}
		}
	}

	// Box-level Tests (defaults to build scope) and DeployEval.
	if img, ok := cfg.Box[boxName]; ok {
		for _, c := range img.Eval {
			c.Origin = "box:" + boxName
			switch c.Scope {
			case "deploy":
				set.Deploy = append(set.Deploy, c)
			default:
				c.Scope = "build"
				set.Box = append(set.Box, c)
			}
		}
		for _, c := range img.DeployEval {
			c.Origin = "deploy-default"
			c.Scope = "deploy"
			set.Deploy = append(set.Deploy, c)
		}
	}

	if set.IsEmpty() {
		return nil
	}
	return set
}

// MergeDeployEval applies local per-host charly.yml test entries onto a label-baked
// deploy section. Merge rules (as specified in the plan):
//
//  1. Local entries with an id: that matches a baked entry's id: replace it.
//  2. Entries without a matching id: are appended.
//  3. A local entry with id:X and skip:true effectively disables the baked
//     entry (it replaces and is reported as skipped by the runner).
//
// Returns the merged deploy slice. Does not mutate the baked slice.
func MergeDeployEval(baked []Check, local []Check) []Check {
	byID := map[string]int{}
	merged := make([]Check, 0, len(baked)+len(local))
	for _, c := range baked {
		merged = append(merged, c)
		if c.ID != "" {
			byID[c.ID] = len(merged) - 1
		}
	}
	for _, c := range local {
		c.Origin = "deploy-local"
		if c.ID != "" {
			if idx, ok := byID[c.ID]; ok {
				merged[idx] = c
				continue
			}
		}
		merged = append(merged, c)
	}
	return merged
}
