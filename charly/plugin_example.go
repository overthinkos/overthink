package main

import (
	"context"
	"encoding/json"
)

// exampleProbeProvider is the canonical BUILT-IN plugin: it provides the
// `exampleprobe` check verb, registered in-process at init() (source: builtin).
// It is the reference implementation of a Provider and the first consumer of the
// registry — a `check:` step `plugin: exampleprobe` dispatches here via
// runPluginVerb, transport-invisibly. Its candy (candy/plugin-example) declares
// it like any candy. An out-of-tree plugin implements the same Provider interface
// and is served over gRPC instead of compiled in.
type exampleProbeProvider struct{}

func (exampleProbeProvider) Reserved() string     { return "exampleprobe" }
func (exampleProbeProvider) Class() ProviderClass { return ClassVerb }

// Invoke returns a deterministic pass. If the authored step carries
// plugin_input.marker, it echoes that marker (so a bed can assert a specific
// value travelled author → provider → result, proving the params round-trip).
func (exampleProbeProvider) Invoke(_ context.Context, op *Operation) (*Result, error) {
	marker := "exampleprobe-ok"
	var params struct {
		PluginInput map[string]any `json:"plugin_input"`
	}
	if len(op.Params) > 0 {
		_ = json.Unmarshal(op.Params, &params)
	}
	if v, ok := params.PluginInput["marker"].(string); ok && v != "" {
		marker = v
	}
	j, err := json.Marshal(pluginCheckResult{Status: "pass", Message: marker})
	if err != nil {
		return nil, err
	}
	return &Result{JSON: j}, nil
}

func init() { RegisterBuiltinProvider(exampleProbeProvider{}) }
