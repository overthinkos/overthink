package main

import (
	"context"
	"encoding/json"

	"github.com/overthinkos/overthink/charly/plugin/builtins/exampleprobe"
	"github.com/overthinkos/overthink/charly/plugin/builtins/exampleprobe/params"
)

// exampleProbeProvider is the canonical BUILT-IN plugin: it provides the
// `exampleprobe` check verb, registered in-process at init() as a PluginUnit
// (source: builtin). It is the in-proc analogue of an external plugin's main.go —
// a Provider plus a self-contained CUE schema (charly/plugin/builtins/exampleprobe).
// A `check:` step `plugin: exampleprobe` dispatches here via runPluginVerb, after
// the host has validated its plugin_input against the unit's served schema; an
// out-of-tree plugin implements the same Provider and serves the same way over
// gRPC instead of compiled in.
type exampleProbeProvider struct{}

func (exampleProbeProvider) Reserved() string     { return "exampleprobe" }
func (exampleProbeProvider) Class() ProviderClass { return ClassVerb }

// Invoke returns a deterministic pass, echoing plugin_input.marker so a bed can
// assert a specific value travelled author → provider → result (the params
// round-trip). It decodes into the CUE-GENERATED struct (params.ExampleprobeInput,
// generated from the unit's schema/exampleprobe.cue) — never a hand-parsed map.
func (exampleProbeProvider) Invoke(_ context.Context, op *Operation) (*Result, error) {
	marker := "exampleprobe-ok"
	var in struct {
		PluginInput params.ExampleprobeInput `json:"plugin_input"`
	}
	if len(op.Params) > 0 {
		_ = json.Unmarshal(op.Params, &in)
	}
	if in.PluginInput.Marker != "" {
		marker = in.PluginInput.Marker
	}
	j, err := json.Marshal(pluginCheckResult{Status: "pass", Message: marker})
	if err != nil {
		return nil, err
	}
	return &Result{JSON: j}, nil
}

func init() {
	RegisterBuiltinPluginUnit(PluginUnit{
		Providers: []Provider{exampleProbeProvider{}},
		Schema:    PluginSchema{CueSource: exampleprobe.Schema(), InputDefs: exampleprobe.InputDefs},
	})
}
