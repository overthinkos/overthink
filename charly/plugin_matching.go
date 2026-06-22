package main

import (
	"context"
	"encoding/json"

	"github.com/overthinkos/overthink/charly/plugin/builtins/matching"
	"github.com/overthinkos/overthink/charly/plugin/builtins/matching/params"
	"github.com/overthinkos/overthink/charly/plugin/sdk"
)

// matchingProvider is the BUILT-IN `matching` plugin: it provides the `matching`
// check verb (pure in-process value matching — no target probe), registered
// in-process at init() as a PluginUnit (source: builtin). It is the in-proc analogue
// of an external plugin's main.go — a Provider plus a self-contained CUE schema
// (charly/plugin/builtins/matching). A `check:` step `plugin: matching` dispatches
// here via runPluginVerb, after the host has validated its plugin_input against the
// unit's served schema. The matcher evaluation reuses the SDK matcher helpers
// (sdk.MatchAll / sdk.MatchValueString — the SINGLE matcher implementation, R3).
type matchingProvider struct{}

func (matchingProvider) Reserved() string     { return "matching" }
func (matchingProvider) Class() ProviderClass { return ClassVerb }

// Invoke coerces plugin_input.matching to a string and asserts every plugin_input.contains
// matcher against it. It decodes into the CUE-GENERATED struct (params.MatchingInput,
// generated from the unit's schema/matching.cue) — never a hand-parsed map. gengotypes
// degrades the self-contained matcher disjunction to `any`, so the generated Contains
// is re-decoded through the SHARED matcher codec (MatcherList's UnmarshalJSON — R3)
// into the typed []Matcher sdk.MatchAll consumes.
func (matchingProvider) Invoke(_ context.Context, op *Operation) (*Result, error) {
	var in struct {
		PluginInput params.MatchingInput `json:"plugin_input"`
	}
	if len(op.Params) > 0 {
		_ = json.Unmarshal(op.Params, &in)
	}
	value := sdk.MatchValueString(in.PluginInput.Matching)
	var contains MatcherList
	if in.PluginInput.Contains != nil {
		raw, err := json.Marshal(in.PluginInput.Contains)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &contains); err != nil {
			return nil, err
		}
	}
	res := pluginCheckResult{Status: "pass", Message: "value=" + value}
	if err := sdk.MatchAll(value, contains); err != nil {
		res = pluginCheckResult{Status: "fail", Message: err.Error()}
	}
	j, err := json.Marshal(res)
	if err != nil {
		return nil, err
	}
	return &Result{JSON: j}, nil
}

func init() {
	RegisterBuiltinPluginUnit(PluginUnit{
		Providers: []Provider{matchingProvider{}},
		Schema:    PluginSchema{CueSource: matching.Schema(), InputDefs: matching.InputDefs},
	})
}
