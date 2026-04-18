package main

// CollectTests walks the base-image chain for imageName and gathers all
// declarative checks into a three-section LabelTestSet. The structure of
// the walk mirrors CollectHooks (ov/hooks.go) — dedupe by layer name, step
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
func CollectTests(cfg *Config, layers map[string]*Layer, imageName string) *LabelTestSet {
	set := &LabelTestSet{}

	// Walk base-image chain the same way CollectHooks does: layer-order per
	// level, then step into the internal base. Tracks visited images so we
	// terminate cleanly on pathological cycles (validateImageDAG reports the
	// cycle itself; we just refuse to infinite-loop on bad input here).
	var allLayerNames []string
	current := imageName
	visited := map[string]bool{}
	for {
		if visited[current] {
			break
		}
		visited[current] = true
		img, ok := cfg.Images[current]
		if !ok {
			break
		}
		resolved, err := ResolveLayerOrder(img.Layers, layers, nil)
		if err != nil {
			break
		}
		allLayerNames = append(allLayerNames, resolved...)
		if baseImg, isInternal := cfg.Images[img.Base]; isInternal && baseImg.IsEnabled() {
			current = img.Base
		} else {
			break
		}
	}

	seen := map[string]bool{}
	for _, layerName := range allLayerNames {
		if seen[layerName] {
			continue
		}
		seen[layerName] = true
		layer, ok := layers[layerName]
		if !ok {
			continue
		}
		for _, c := range layer.tests {
			c.Origin = "layer:" + layerName
			switch c.Scope {
			case "deploy":
				set.Deploy = append(set.Deploy, c)
			default:
				c.Scope = "build"
				set.Layer = append(set.Layer, c)
			}
		}
	}

	// Image-level Tests (defaults to build scope) and DeployTests.
	if img, ok := cfg.Images[imageName]; ok {
		for _, c := range img.Tests {
			c.Origin = "image:" + imageName
			switch c.Scope {
			case "deploy":
				set.Deploy = append(set.Deploy, c)
			default:
				c.Scope = "build"
				set.Image = append(set.Image, c)
			}
		}
		for _, c := range img.DeployTests {
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

// MergeDeployOverlay applies local deploy.yml test entries onto a label-baked
// deploy section. Merge rules (as specified in the plan):
//
//  1. Local entries with an id: that matches a baked entry's id: replace it.
//  2. Entries without a matching id: are appended.
//  3. A local entry with id:X and skip:true effectively disables the baked
//     entry (it replaces and is reported as skipped by the runner).
//
// Returns the merged deploy slice. Does not mutate the baked slice.
func MergeDeployTests(baked []Check, local []Check) []Check {
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
