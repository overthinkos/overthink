package main

// CollectDescriptions walks the base-box chain for boxName and
// gathers every kind: entity's description: block into a three-section
// LabelDescriptionSet.
//
// The walk mirrors CollectHooks and CollectShell: candy-order per
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
		if !ok || (layer.Description == nil && len(layer.scenario) == 0) {
			continue
		}
		desc := Description{}
		if layer.Description != nil {
			desc = *layer.Description
		}
		set.Candy = append(set.Candy, LabeledDescription{
			Origin:      "candy:" + candyName,
			Description: desc,
			Scenario:    layer.scenario,
		})
	}

	// Box-level description + scenarios.
	if img, ok := cfg.Box[boxName]; ok && (img.Description != nil || len(img.Scenario) > 0) {
		desc := Description{}
		if img.Description != nil {
			desc = *img.Description
		}
		set.Box = append(set.Box, LabeledDescription{
			Origin:      "box:" + boxName,
			Description: desc,
			Scenario:    img.Scenario,
		})
	}

	if set.IsEmpty() {
		return nil
	}
	return set
}

// MergeDeployDescriptions overlays a deployment node's local `scenario:` list
// onto a label-baked LabelDescriptionSet's Deploy section. A baked deploy
// scenario with the same Name is replaced by the local one; otherwise the
// local scenario is appended. This is the per-host override surface for
// acceptance scenarios (charly.yml deploy entries).
//
// If localScenarios is empty, returns baked unchanged.
func MergeDeployDescriptions(baked *LabelDescriptionSet, localScenarios []Scenario, originName string) *LabelDescriptionSet {
	if len(localScenarios) == 0 {
		return baked
	}
	if baked == nil {
		baked = &LabelDescriptionSet{}
	}
	// Index baked deploy scenarios by name for replace-by-name.
	type loc struct{ ld, sc int }
	locByName := map[string]loc{}
	for li := range baked.Deploy {
		for si, sc := range baked.Deploy[li].Scenario {
			locByName[sc.Name] = loc{li, si}
		}
	}
	var fresh []Scenario
	for _, sc := range localScenarios {
		if l, ok := locByName[sc.Name]; ok {
			baked.Deploy[l.ld].Scenario[l.sc] = sc // replace by name
			continue
		}
		fresh = append(fresh, sc)
	}
	if len(fresh) > 0 {
		baked.Deploy = append(baked.Deploy, LabeledDescription{
			Origin:   "deploy-local:" + originName,
			Scenario: fresh,
		})
	}
	return baked
}
