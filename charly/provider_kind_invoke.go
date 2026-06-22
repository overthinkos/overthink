package main

import (
	"context"
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

// runPluginKind decodes an EXTERNAL kind node out-of-process via its Provider's
// Invoke envelope (Op: OpLoad) — the kind-class analogue of runPluginVerb
// (provider_checkenv.go). A BUILT-IN kind uses the typed DecodeNode fast path (no
// JSON, provider_kind.go); an external plugin kind, which the core has no Go type
// for, validates the authored value against its SERVED .cue and returns its
// canonical entity JSON, stored in uf.PluginKinds keyed by the kind word. The split
// (typed builtin / serializable envelope external) keeps the per-entity decode hot
// path zero-JSON for builtins — the E3 envelope is paid only out-of-process.
// Transport-invisible above the registry.
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
	// Validate the authored value against the plugin's served #Kind .cue BEFORE
	// dispatch — identical gate to the verb path (validateAuthoredPluginInput).
	if err := validateAuthoredPluginInput(ClassKind, gn.disc, paramsJSON); err != nil {
		return fmt.Errorf("node %q: %w", gn.name, err)
	}
	out, err := prov.Invoke(context.Background(), &Operation{Reserved: gn.disc, Op: OpLoad, Params: json.RawMessage(paramsJSON)})
	if err != nil {
		return fmt.Errorf("node %q: plugin kind %q: %w", gn.name, gn.disc, err)
	}
	if uf.PluginKinds == nil {
		uf.PluginKinds = map[string][]json.RawMessage{}
	}
	uf.PluginKinds[gn.disc] = append(uf.PluginKinds[gn.disc], out.JSON)
	return nil
}
