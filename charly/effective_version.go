package main

import "fmt"

// computeEffectiveVersions assigns ResolvedBox.EffectiveVersion for every
// image in the build graph. EffectiveVersion is the content-derived identity
// emitted as the ai.opencharly.version OCI label:
//
//  1. the image's dedicated `version:` (img.Version) if set; else
//  2. the highest layer `version:` across its full layer set
//     (collectAllBoxLayers spans the entire base chain, including
//     namespaced bases since img.Base is a fully-qualified key in g.Images);
//     else
//  3. the internal base image's EffectiveVersion (recurse); else
//  4. a HARD ERROR pointing at `charly migrate` — there is NO build-timestamp
//     fallback (see CHANGELOG: per-kind versioning hard cutover).
//
// The label is stable across builds when no layer changed; that stability is
// what keeps a child's `FROM <base>` SHA from shifting and cascading
// cache-misses. Run by NewGenerator AFTER ComputeIntermediates +
// GlobalLayerOrder, so the base chain and the auto-intermediate images are
// fully materialized in g.Images (auto-intermediates carry no own version: and
// resolve via step 2 over their hoisted layers).
func (g *Generator) computeEffectiveVersions() error {
	memo := make(map[string]string)
	visiting := make(map[string]bool)

	var compute func(name string) (string, error)
	compute = func(name string) (string, error) {
		if v, ok := memo[name]; ok {
			return v, nil
		}
		img, ok := g.Boxes[name]
		if !ok {
			return "", fmt.Errorf("effective version: unknown image %q", name)
		}
		if visiting[name] {
			return "", fmt.Errorf("effective version: cyclic base chain at image %q", name)
		}
		visiting[name] = true
		defer delete(visiting, name)

		// 1. A dedicated version: wins (the only versioned-by-author images
		//    today are bare distro bases, which carry no layers).
		if img.Version != "" {
			memo[name] = img.Version
			return img.Version, nil
		}

		// 2. Highest layer version across the full layer set (own + base chain).
		//    Layers are mandatory-versioned, so a layered image always resolves
		//    here. compareCalVer orders YYYY.DDD.HHMM numerically.
		best := ""
		for _, ln := range collectAllBoxLayers(name, g.Boxes, g.Layers) {
			l, ok := g.Layers[ln]
			if !ok || l.Version == "" {
				continue
			}
			if best == "" || compareCalVer(l.Version, best) > 0 {
				best = l.Version
			}
		}
		if best != "" {
			memo[name] = best
			return best, nil
		}

		// 3. Layerless internal-base image (e.g. a passthrough) inherits the
		//    base's effective version.
		if !img.IsExternalBase && img.Base != "" {
			bv, err := compute(img.Base)
			if err != nil {
				return "", err
			}
			memo[name] = bv
			return bv, nil
		}

		// 4. Nothing derivable — a layerless image on an external base with no
		//    dedicated version. Hard cutover: no build-timestamp fallback.
		return "", fmt.Errorf("image %q resolves no version: a layerless image on an external base needs a dedicated `version:`. Run: charly migrate", name)
	}

	for name, img := range g.Boxes {
		v, err := compute(name)
		if err != nil {
			return err
		}
		img.EffectiveVersion = v
	}
	return nil
}
