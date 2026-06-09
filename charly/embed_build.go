package main

import (
	_ "embed"
	"fmt"

	"gopkg.in/yaml.v3"
)

// embeddedBuildYAML is the DEFAULT build vocabulary (distro / builder / init /
// resource definitions) compiled into the charly binary. It mirrors the
// established embed pattern of charly/sidecar.go (`//go:embed sidecar.yml`): a
// project no longer needs to ship or import a build.yml at all — the binary
// carries the canonical vocabulary, and a project EXTENDS or OVERRIDES it by
// declaring its own distro:/builder:/init:/resource: entries (inline in
// charly.yml or in an imported vocabulary file).
//
//go:embed build.yml
var embeddedBuildYAML []byte

// LoadEmbeddedBuildConfig parses the embedded default build vocabulary into a
// UnifiedFile (only its Distro/Builder/Init/Resource maps are populated).
// Mirrors LoadEmbeddedSidecarConfig (sidecar.go): parsed fresh on each call so
// no mutable state is shared across loads.
func LoadEmbeddedBuildConfig() (*UnifiedFile, error) {
	var uf UnifiedFile
	if err := yaml.Unmarshal(embeddedBuildYAML, &uf); err != nil {
		return nil, fmt.Errorf("parsing embedded build.yml: %w", err)
	}
	return &uf, nil
}

// applyEmbeddedBuildDefaults merges the binary-embedded build vocabulary UNDER a
// project's own distro/builder/init/resource entries — the project always wins.
//
// This is the SAME base/overlay relationship as sidecar's
// MergeSidecar(base=embedded, overlay=project): the embedded vocabulary is the
// base, the project's vocabulary is the overlay that wins. It is implemented via
// the existing gap-filling per-key maps (mergeDistroMap / mergeBuilderMap /
// mergeInitMap / mergeResourceMap), which copy a key only when it is ABSENT. So
// calling this AFTER all project sources are merged fills only the vocabulary the
// project did not define — project-wins is structural, not order-dependent.
// Called at the depth-0 boundary of loadUnifiedInto for the root AND every
// namespace child, so each project/namespace inherits the default vocabulary.
func applyEmbeddedBuildDefaults(uf *UnifiedFile) error {
	def, err := LoadEmbeddedBuildConfig()
	if err != nil {
		return err
	}
	mergeDistroMap(&uf.Distro, def.Distro)
	mergeBuilderMap(&uf.Builder, def.Builder)
	mergeInitMap(&uf.Init, def.Init)
	mergeResourceMap(&uf.Resource, def.Resource)
	return nil
}
