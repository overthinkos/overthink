package main

import "fmt"

// CollectShell walks the base-image chain for boxName and gathers
// per-(origin, shell) shell-init contributions into a three-section
// LabelShellSet. Mirrors CollectEval / CollectHooks shape — dedupe by
// layer name, walk internal bases until an external image, terminate
// on visited-image cycle.
//
// Section assignment:
//   - Each layer's `shell:` (intrinsic + per-shell sub-blocks) → Layer.
//   - Image-level `shell:` → Image.
//   - Deploy-scope defaults from charly.yml are not yet expressed —
//     reserved for future use; today the Deploy section is filled at
//     deploy time by MergeDeployShell from deploy.yml entries.
//
// Returns nil if every section is empty.
func CollectShell(cfg *Config, layers map[string]*Layer, boxName string) *LabelShellSet {
	set := &LabelShellSet{}

	var allLayerNames []string
	for _, node := range cfg.walkBaseChain(boxName) {
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
		if !ok {
			continue
		}
		entry := shellConfigToEntry(layer.Shell(), layerName)
		if entry == nil {
			continue
		}
		entry.ID = layerName
		set.Layer = append(set.Layer, *entry)
	}

	if img, ok := cfg.Box[boxName]; ok {
		if img.Shell != nil {
			entry := shellConfigToEntry(img.Shell, "image:"+boxName)
			if entry != nil {
				entry.ID = "image:" + boxName
				set.Box = append(set.Box, *entry)
			}
		}
	}

	if len(set.Layer) == 0 && len(set.Box) == 0 && len(set.Deploy) == 0 {
		return nil
	}
	return set
}

// shellConfigToEntry projects an in-memory ShellConfig into the
// label-emission ShellEntry shape. Returns nil when the config is
// effectively empty (no Init, no PathAppend, no per-shell overrides).
func shellConfigToEntry(cfg *ShellConfig, origin string) *ShellEntry {
	if cfg == nil {
		return nil
	}
	hasGeneric := cfg.Init != "" || len(cfg.PathAppend) > 0 || cfg.Path != ""
	if !hasGeneric && len(cfg.ByShell) == 0 {
		return nil
	}
	entry := &ShellEntry{
		Origin:   origin,
		Priority: cfg.Priority,
	}
	if hasGeneric {
		entry.Generic = &ShellSpec{
			Init:       cfg.Init,
			PathAppend: append([]string(nil), cfg.PathAppend...),
			Path:       cfg.Path,
		}
	}
	if len(cfg.ByShell) > 0 {
		entry.ByShell = make(map[string]*ShellSpec, len(cfg.ByShell))
		for k, v := range cfg.ByShell {
			if v == nil {
				continue
			}
			entry.ByShell[k] = &ShellSpec{
				Init:       v.Init,
				PathAppend: append([]string(nil), v.PathAppend...),
				Path:       v.Path,
			}
		}
	}
	return entry
}

// MergeDeployShell applies a deploy.yml `shell:` overlay onto a label-
// baked LabelShellSet, returning a new merged set. Mirrors
// MergeDeployEval semantics:
//   - Entry with matching ID and skip:true → drop the matched entry.
//   - Entry with matching ID and skip:false → replace the matched
//     entry wholesale.
//   - Entry with no matching ID (or no ID) → append into the deploy
//     section with Origin "deploy" if not set.
func MergeDeployShell(baked *LabelShellSet, overlay []ShellEntry) *LabelShellSet {
	if baked == nil {
		baked = &LabelShellSet{}
	}
	out := &LabelShellSet{
		Layer:  append([]ShellEntry(nil), baked.Layer...),
		Box:    append([]ShellEntry(nil), baked.Box...),
		Deploy: append([]ShellEntry(nil), baked.Deploy...),
	}
	if len(overlay) == 0 {
		return out
	}
	for _, e := range overlay {
		if e.ID != "" {
			if replaced := replaceShellEntryByID(out, e); replaced {
				continue
			}
		}
		// Unmatched ID or no ID — append to Deploy.
		if e.Origin == "" {
			e.Origin = "deploy"
		}
		out.Deploy = append(out.Deploy, e)
	}
	return out
}

// replaceShellEntryByID looks up entry.ID across the three sections of
// `set` and either replaces (skip=false) or removes (skip=true). The
// `skip` field on ShellEntry is encoded as zero priority + nil
// Generic + nil ByShell when stored on disk; deploy.yml-side parsing
// consumes a separate ShellOverlayEntry struct that carries Skip
// explicitly. Here we treat any incoming entry whose Generic/ByShell
// are both nil AND whose Origin is "deploy" or "" as a skip-by-id
// signal — see DeployImageConfig.Shell parsing in deploy.go.
func replaceShellEntryByID(set *LabelShellSet, e ShellEntry) bool {
	skip := e.Generic == nil && len(e.ByShell) == 0
	for _, bucket := range [...]*[]ShellEntry{&set.Layer, &set.Box, &set.Deploy} {
		for i, b := range *bucket {
			if b.ID != e.ID {
				continue
			}
			if skip {
				*bucket = append((*bucket)[:i], (*bucket)[i+1:]...)
			} else {
				(*bucket)[i] = e
			}
			return true
		}
	}
	return false
}

// resolveDeploymentShellOverride applies the selection rule (per-shell
// wins over generic) to an aggregate LabelShellSet at deploy time.
// Returns a flat (origin, shell) → ShellSpec map for the renderer to
// consume. Origin order is Layer first, then Image, then Deploy — so
// later contributors win on (origin, shell) collision.
func resolveDeploymentShellOverride(set *LabelShellSet) map[string]map[string]*ShellSpec {
	out := map[string]map[string]*ShellSpec{}
	if set == nil {
		return out
	}
	for _, bucket := range [][]ShellEntry{set.Layer, set.Box, set.Deploy} {
		for _, e := range bucket {
			if _, ok := out[e.Origin]; !ok {
				out[e.Origin] = map[string]*ShellSpec{}
			}
			for _, shell := range []string{"bash", "zsh", "fish", "sh"} {
				if spec, has := e.ByShell[shell]; has && spec != nil {
					out[e.Origin][shell] = spec
					continue
				}
				if e.Generic != nil && e.Generic.Init != "" {
					out[e.Origin][shell] = e.Generic
				}
			}
		}
	}
	return out
}

// shellEntryDescribe returns a one-line summary for log output.
func shellEntryDescribe(e ShellEntry) string {
	shells := make([]string, 0, len(e.ByShell))
	for k := range e.ByShell {
		shells = append(shells, k)
	}
	if e.Generic != nil && e.Generic.Init != "" {
		shells = append(shells, "generic")
	}
	return fmt.Sprintf("origin=%s id=%s shells=%v", e.Origin, e.ID, shells)
}
