package main

// CollectEval walks the base-image chain for boxName and gathers all
// declarative checks into a three-section LabelEvalSet. The structure of
// the walk mirrors CollectHooks (charly/hooks.go) — dedupe by layer name, step
// through internal bases until an external image is hit — so layer ordering
// is consistent across every collected label.
//
// Section assignment rules:
//
//   - Layer-defined checks with no scope (or scope:"build") land in Layer.
//   - Layer-defined checks with scope:"deploy" land in Deploy.
//   - Image-level Tests default to scope:"build" → Image section;
//     scope:"deploy" routes to Deploy.
//   - Image-level DeployTests always land in Deploy (scope forced to "deploy").
//
// Each check receives an Origin annotation for reporting:
// "layer:<name>", "image:<name>", or "deploy-default" (for image deploy entries).
//
// Returns nil if every section is empty — callers (generate.go) skip label
// emission in that case.
func CollectEval(cfg *Config, layers map[string]*Candy, boxName string) *LabelEvalSet {
	set := &LabelEvalSet{}

	// Walk base-image chain the same way CollectHooks does: layer-order per
	// level, then step into the internal base. Tracks visited images so we
	// terminate cleanly on pathological cycles (validateBoxDAG reports the
	// cycle itself; we just refuse to infinite-loop on bad input here).
	var allCandyNames []string
	for _, node := range cfg.walkBaseChain(boxName) {
		resolved, err := ResolveCandyOrder(node.Img.Candy, layers, nil)
		if err != nil {
			break
		}
		allCandyNames = append(allCandyNames, resolved...)
	}

	seen := map[string]bool{}
	for _, candyName := range allCandyNames {
		if seen[candyName] {
			continue
		}
		seen[candyName] = true
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

	// Image-level Tests (defaults to build scope) and DeployTests.
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
