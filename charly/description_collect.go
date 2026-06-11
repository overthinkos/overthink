package main

// CollectDescriptions walks the base-box chain for boxName and
// gathers every kind: entity's description: block into a three-section
// LabelDescriptionSet.
//
// The walk mirrors CollectEval and CollectHooks: candy-order per
// level, then step into internal base, dedupe by candy name, stop at
// first external base OR on cycle. This keeps collection ordering
// consistent across every collected label.
//
// Section assignment rules:
//
//   - Candy-defined descriptions → Candy section
//   - Box-level description → Box section
//   - Deploy-node descriptions (from charly.yml overlay) → Deploy section
//     (added later by MergeDeployDescriptions when applicable)
//
// Scenarios within a Description are already scope-tagged via the
// scenario's tags (@build vs @deploy). Section assignment here is
// purely about which entity contributed the description — the tag
// filter decides what actually runs per section.
//
// Returns nil if every section is empty.
func CollectDescriptions(cfg *Config, layers map[string]*Candy, boxName string) *LabelDescriptionSet {
	set := &LabelDescriptionSet{}

	allCandyNames, _ := cfg.boxCandyChain(layers, boxName)
	for _, candyName := range allCandyNames {
		layer, ok := layers[candyName]
		if !ok || layer.Description == nil {
			continue
		}
		set.Candy = append(set.Candy, LabeledDescription{
			Origin:      "candy:" + candyName,
			Description: *layer.Description,
		})
	}

	// Box-level description.
	if img, ok := cfg.Box[boxName]; ok && img.Description != nil {
		set.Box = append(set.Box, LabeledDescription{
			Origin:      "box:" + boxName,
			Description: *img.Description,
		})
	}

	if set.IsEmpty() {
		return nil
	}
	return set
}

// MergeDeployDescriptions adds a local charly.yml description onto a
// label-baked LabelDescriptionSet's Deploy section. Mirrors
// MergeDeployEval semantics but at Description-level (the finest
// granularity for description overlays is per-deploy-entity — if the
// user wants to override a specific step or scenario, they author a
// replacement description on the DeploymentNode).
//
// If local is nil, returns baked unchanged.
func MergeDeployDescriptions(baked *LabelDescriptionSet, local *Description, originName string) *LabelDescriptionSet {
	if local == nil {
		return baked
	}
	if baked == nil {
		baked = &LabelDescriptionSet{}
	}
	baked.Deploy = append(baked.Deploy, LabeledDescription{
		Origin:      "deploy-local:" + originName,
		Description: *local,
	})
	return baked
}
