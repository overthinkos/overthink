package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/overthinkos/overthink/charly/spec"
	"gopkg.in/yaml.v3"
)

// runPluginKind decodes an EXTERNAL kind node out-of-process via its Provider's
// Invoke envelope (Op: OpLoad) — the kind-class analogue of runPluginVerb
// (provider_checkenv.go). A BUILT-IN kind uses the typed DecodeNode fast path (no
// JSON, provider_kind.go); an external plugin kind, which the core has no Go type
// for, validates the NAMELESS authored body against its SERVED .cue and returns its
// canonical entity JSON, stored in uf.PluginKinds[kind][name]. The entity NAME is
// the node KEY (gn.name) — never part of the validated body, so #<Kind>Input is
// untouched — threaded here from the node key into the storage key, so a consumer
// can look the entity up by name and the merge is root-wins override
// (mergePluginKindsMap). The split (typed builtin / serializable envelope external)
// keeps the per-entity decode hot path zero-JSON for builtins — the E3 envelope is
// paid only out-of-process. Transport-invisible above the registry.
func runPluginKind(prov Provider, gn *genericNode, uf *UnifiedFile) error {
	body, err := assembleEntityBody(gn)
	if err != nil {
		return fmt.Errorf("node %q: assemble: %w", gn.name, err)
	}
	yamlBytes, err := yaml.Marshal(body)
	if err != nil {
		return fmt.Errorf("node %q: marshal: %w", gn.name, err)
	}
	// YAML → JSON for the wire envelope (yaml.v3 decodes mappings as
	// map[string]any, which marshals to JSON cleanly).
	var asMap any
	if err := yaml.Unmarshal(yamlBytes, &asMap); err != nil {
		return fmt.Errorf("node %q: reparse: %w", gn.name, err)
	}
	paramsJSON, err := json.Marshal(asMap)
	if err != nil {
		return fmt.Errorf("node %q: to json: %w", gn.name, err)
	}
	// A plugin KIND validates at LOAD time (inside the loader), BEFORE the
	// check/deploy paths gate plugin schemas (loadProjectPlugins). Ensure every
	// builtin plugin unit's served schema is loaded so validateAuthoredPluginInput
	// can find this kind's def; idempotent (sync.Once), local (no fetch).
	if err := loadBuiltinPluginUnits(); err != nil {
		return fmt.Errorf("node %q: builtin plugin schema gate: %w", gn.name, err)
	}
	// Validate the authored value against the plugin's served #Kind .cue BEFORE
	// dispatch — identical gate to the verb path (validateAuthoredPluginInput).
	if err := validateAuthoredPluginInput(ClassKind, gn.disc, paramsJSON); err != nil {
		return fmt.Errorf("node %q: %w", gn.name, err)
	}
	// F7/C8: a kind declaring Validates serves a DEEP OpValidate check BEYOND the static CUE
	// input-def gate above — the host dispatches it and surfaces error-severity Diagnostics as a
	// load failure. A kind that does not declare it pays nothing (no extra round-trip).
	if vc, ok := prov.(validatingKindCarrier); ok && vc.isValidatingKind() {
		vres, verr := prov.Invoke(context.Background(), &Operation{Reserved: gn.disc, Op: OpValidate, Params: json.RawMessage(paramsJSON)})
		if verr != nil {
			return fmt.Errorf("node %q: plugin kind %q validate: %w", gn.name, gn.disc, verr)
		}
		var diags spec.Diagnostics
		if vres != nil && len(vres.JSON) > 0 {
			if err := json.Unmarshal(vres.JSON, &diags); err != nil {
				return fmt.Errorf("node %q: plugin kind %q validate: decode diagnostics: %w", gn.name, gn.disc, err)
			}
		}
		if diags.HasErrors() {
			return fmt.Errorf("node %q: kind %q validation failed: %s", gn.name, gn.disc, formatKindDiagnostics(diags))
		}
	}
	out, err := prov.Invoke(context.Background(), &Operation{Reserved: gn.disc, Op: OpLoad, Params: json.RawMessage(paramsJSON)})
	if err != nil {
		return fmt.Errorf("node %q: plugin kind %q: %w", gn.name, gn.disc, err)
	}
	// F5: a STRUCTURAL kind's OpLoad returns a spec.Deploy (BundleNode) member tree the host
	// folds into uf.Bundle — the SAME map a builtin structural kind's DecodeNode populates
	// (buildBundleNodeInto), so the entity participates in deploy/check exactly like a builtin
	// pod/group/candy. A FLAT kind (F4) lands its opaque body in uf.PluginKinds, unchanged.
	if sc, ok := prov.(structuralKindCarrier); ok && sc.isStructuralKind() {
		var dn BundleNode
		if err := json.Unmarshal(out.JSON, &dn); err != nil {
			return fmt.Errorf("node %q: structural kind %q reply decode: %w", gn.name, gn.disc, err)
		}
		if uf.Bundle == nil {
			uf.Bundle = map[string]BundleNode{}
		}
		uf.Bundle[gn.name] = dn
		return nil
	}
	if uf.PluginKinds == nil {
		uf.PluginKinds = map[string]map[string]json.RawMessage{}
	}
	if uf.PluginKinds[gn.disc] == nil {
		uf.PluginKinds[gn.disc] = map[string]json.RawMessage{}
	}
	uf.PluginKinds[gn.disc][gn.name] = out.JSON
	return nil
}

// formatKindDiagnostics renders the error-severity items of an OpValidate reply into one
// semicolon-joined string (path-prefixed when a path is set) for the load error message.
func formatKindDiagnostics(d spec.Diagnostics) string {
	msgs := make([]string, 0, len(d.Items))
	for _, it := range d.Items {
		if it.Severity == "warning" {
			continue
		}
		if it.Path != "" {
			msgs = append(msgs, it.Path+": "+it.Message)
		} else {
			msgs = append(msgs, it.Message)
		}
	}
	return strings.Join(msgs, "; ")
}
