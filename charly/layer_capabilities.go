package main

import (
	"fmt"
	"sort"
	"strings"
)

// CandyCapabilities is the per-layer YAML shape parsed from the candy manifest
// `capabilities:`. Layers contribute image-level facts that previously
// hid behind magic image-level booleans (image.bootc, image.data_image).
//
// Aggregation rules at image resolve time:
//   - bools: OR (any contributing layer wins)
//   - strings: last-layer-wins (deterministic via topological order)
//   - oci_labels map: union; key collision with conflicting values is a hard error
//
// The aggregated values populate the existing BoxMetadata surface
// (labels.go), which already round-trips through OCI labels. We do NOT
// introduce a parallel runtime contract type; we just change the source
// of truth from BoxConfig flags to layer aggregation.
type CandyCapabilities struct {
	PreserveUser       bool              `yaml:"preserve_user,omitempty"`
	NeedsRootAfterInit bool              `yaml:"needs_root_after_init,omitempty"`
	InitSystemHint     string            `yaml:"init_system_hint,omitempty"`
	DataOnly           bool              `yaml:"data_only,omitempty"`
	OCILabels          map[string]string `yaml:"oci_label,omitempty"`
}

// AggregatedCandyCaps is the output of walking all layers in resolution
// order. It is populated onto ResolvedBox and consumed wherever code
// previously read BoxConfig.Bootc, BoxConfig.DataImage, or the
// init-system bootc parameter.
type AggregatedCandyCaps struct {
	PreserveUser       bool
	NeedsRootAfterInit bool
	InitSystemHint     string
	DataOnly           bool
	OCILabels          map[string]string
	// Provided is the set of capability names declared by some layer in
	// the composition. Used by CheckRequiredCapabilities to validate
	// `requires_capabilities:` cross-layer requirements.
	Provided map[string]bool
}

// AggregateCandyCapabilities walks `order` (layer names in topological
// resolution order) and merges each layer's `capabilities:` contribution.
// Returns an error if two layers declare conflicting values for the same
// OCI label key — the conflict surfaces the bug rather than silently
// picking a winner.
func AggregateCandyCapabilities(layers map[string]*Candy, order []string) (*AggregatedCandyCaps, error) {
	out := &AggregatedCandyCaps{
		OCILabels: make(map[string]string),
		Provided:  make(map[string]bool),
	}
	type ociSource struct {
		layer string
		value string
	}
	seen := make(map[string]ociSource)

	for _, name := range order {
		layer, ok := layers[name]
		if !ok || layer == nil {
			continue
		}
		c := layer.capabilities
		if c == nil {
			continue
		}
		if c.PreserveUser {
			out.PreserveUser = true
			out.Provided["preserve_user"] = true
		}
		if c.NeedsRootAfterInit {
			out.NeedsRootAfterInit = true
			out.Provided["needs_root_after_init"] = true
		}
		if c.DataOnly {
			out.DataOnly = true
			out.Provided["data_only"] = true
		}
		if c.InitSystemHint != "" {
			out.InitSystemHint = c.InitSystemHint
			out.Provided["init_system:"+c.InitSystemHint] = true
		}
		for k, v := range c.OCILabels {
			if existing, ok := seen[k]; ok && existing.value != v {
				return nil, fmt.Errorf(
					"capability oci_labels conflict for key %q: layer %q contributes %q, prior layer %q contributes %q",
					k, name, v, existing.layer, existing.value,
				)
			}
			seen[k] = ociSource{layer: name, value: v}
			out.OCILabels[k] = v
		}
	}
	return out, nil
}

// CheckRequiredCapabilities returns a sorted list of capability names
// requested via `requires_capabilities:` on any layer in `order` but not
// provided by the aggregated capabilities. Empty slice on success.
func CheckRequiredCapabilities(layers map[string]*Candy, order []string, agg *AggregatedCandyCaps) []string {
	if agg == nil {
		agg = &AggregatedCandyCaps{Provided: map[string]bool{}}
	}
	missing := make(map[string]bool)
	for _, name := range order {
		layer, ok := layers[name]
		if !ok || layer == nil {
			continue
		}
		for _, req := range layer.requiresCapabilities {
			if !agg.Provided[req] {
				missing[req] = true
			}
		}
	}
	out := make([]string, 0, len(missing))
	for k := range missing {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// CandyCapabilitiesError formats a missing-capabilities error with a
// remediation hint pointing at which layers requested what.
func CandyCapabilitiesError(layers map[string]*Candy, order []string, missing []string) error {
	if len(missing) == 0 {
		return nil
	}
	var requesters []string
	for _, name := range order {
		layer, ok := layers[name]
		if !ok || layer == nil {
			continue
		}
		for _, req := range layer.requiresCapabilities {
			for _, m := range missing {
				if req == m {
					requesters = append(requesters, fmt.Sprintf("    %s (requires %s)", name, req))
				}
			}
		}
	}
	return fmt.Errorf(
		"image composition is missing required capabilities: %s\n  layers requesting them:\n%s\n  fix: include a layer that contributes the missing capability via `capabilities:` block",
		strings.Join(missing, ", "),
		strings.Join(requesters, "\n"),
	)
}
