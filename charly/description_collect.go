package main

// CollectDescriptions walks the base-image chain for imageName and
// gathers every kind: entity's description: block into a three-section
// LabelDescriptionSet.
//
// The walk mirrors CollectEval and CollectHooks: layer-order per
// level, then step into internal base, dedupe by layer name, stop at
// first external base OR on cycle. This keeps collection ordering
// consistent across every collected label.
//
// Section assignment rules:
//
//   - Layer-defined descriptions → Layer section
//   - Image-level description → Image section
//   - Deploy-node descriptions (from deploy.yml overlay) → Deploy section
//     (added later by MergeDeployDescriptions when applicable)
//
// Scenarios within a Description are already scope-tagged via the
// scenario's tags (@build vs @deploy). Section assignment here is
// purely about which entity contributed the description — the tag
// filter decides what actually runs per section.
//
// Returns nil if every section is empty.
func CollectDescriptions(cfg *Config, layers map[string]*Layer, imageName string) *LabelDescriptionSet {
	set := &LabelDescriptionSet{}

	var allLayerNames []string
	for _, node := range cfg.walkBaseChain(imageName) {
		resolved, err := ResolveLayerOrder(node.Img.Layer, layers, nil)
		if err != nil {
			break
		}
		allLayerNames = append(allLayerNames, resolved...)
	}

	seen := map[string]bool{}
	for _, layerName := range allLayerNames {
		if seen[layerName] {
			continue
		}
		seen[layerName] = true
		layer, ok := layers[layerName]
		if !ok || layer.Description == nil {
			continue
		}
		set.Layer = append(set.Layer, LabeledDescription{
			Origin:      "layer:" + layerName,
			Description: *layer.Description,
		})
	}

	// Image-level description.
	if img, ok := cfg.Image[imageName]; ok && img.Description != nil {
		set.Image = append(set.Image, LabeledDescription{
			Origin:      "image:" + imageName,
			Description: *img.Description,
		})
	}

	if set.IsEmpty() {
		return nil
	}
	return set
}

// MergeDeployDescriptions adds a local deploy.yml description onto a
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
