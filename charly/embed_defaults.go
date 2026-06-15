package main

import (
	_ "embed"
	"fmt"
)

// embeddedCharlyCUE is the binary's DEFAULT config, compiled into the charly
// CLI. It is a complete charly.cue carrying the default build vocabulary
// (resource / builder / distro / init) AND the default sidecar-template library
// (sidecar:), authored in CUE (definitions / references / hidden fields factor
// out the per-distro / per-format repetition). A project needs to ship NONE of
// it: the binary fills any vocabulary or sidecar the project did not declare
// (project-wins), and a project EXTENDS or OVERRIDES it by declaring its own
// entries in its charly.yml (project config stays YAML).
//
//go:embed charly.cue
var embeddedCharlyCUE []byte

// embeddedDefaults compiles the binary-embedded charly.cue to its concrete data
// (compileCUEToYAML — the CUE-source front-end) and parses it into a UnifiedFile
// through the SAME document-routing core (mergeUnifiedDocs → classifyDoc →
// mergeUnified) that every on-disk charly.yml flows through. The embedded
// default is just another config that happens to live in the binary, authored
// in CUE. Parsed fresh on each call so no mutable state is shared across loads.
func embeddedDefaults() (*UnifiedFile, error) {
	yamlBytes, err := compileCUEToYAML(embeddedCharlyCUE, "charly.cue (embedded)")
	if err != nil {
		return nil, fmt.Errorf("compiling embedded charly.cue: %w", err)
	}
	var uf UnifiedFile
	if _, err := mergeUnifiedDocs(&uf, yamlBytes, "charly.cue (embedded)", ""); err != nil {
		return nil, fmt.Errorf("parsing embedded charly.cue: %w", err)
	}
	return &uf, nil
}

// applyEmbeddedDefaults merges the binary-embedded build vocabulary AND sidecar
// templates UNDER a project's own entries — the project always wins.
//
// The embedded set is the BASE; the project's entries are the overlay that
// wins. Implemented via the gap-filling per-key maps (mergeDistroMap /
// mergeBuilderMap / mergeInitMap / mergeResourceMap / mergeSidecarMap), which
// copy a key only when it is ABSENT. So calling this AFTER all project sources
// are merged fills only what the project did not define — project-wins is
// structural, not order-dependent. Called at the depth-0 boundary of
// loadUnifiedInto for the root AND every namespace child, so each
// project/namespace inherits the default vocabulary + sidecar templates.
func applyEmbeddedDefaults(uf *UnifiedFile) error {
	def, err := embeddedDefaults()
	if err != nil {
		return err
	}
	mergeDistroMap(&uf.Distro, def.Distro)
	mergeBuilderMap(&uf.Builder, def.Builder)
	mergeInitMap(&uf.Init, def.Init)
	mergeResourceMap(&uf.Resource, def.Resource)
	mergeSidecarMap(&uf.Sidecar, def.Sidecar)
	return nil
}
