package main

import (
	"context"
	"fmt"

	"github.com/overthinkos/overthink/charly/plugin/builtins/examplerunverb"
	"github.com/overthinkos/overthink/charly/plugin/builtins/examplerunverb/params"
)

// exampleRunVerbProvider is the canonical EXECUTION-NEEDING built-in plugin: it
// provides the `examplerunverb` check verb as a CheckVerbProvider, so runPluginVerb
// dispatches it IN-PROCESS via RunVerb — keeping the live *Runner that cannot cross
// the wire. This is the in-proc analogue of exampleProbeProvider (which is reached
// over the out-of-process Invoke envelope instead). It is the proof the dispatch fix
// works: an execution-needing verb (one that reaches r.Exec / the *Runner) CAN be a
// builtin plugin unit, the enabler that unblocks extracting file/port/command/… into
// dedicated plugin units.
//
// It embeds builtinVerbBase, which supplies Class()=ClassVerb and the in-proc-only
// Invoke stub (an in-proc verb never serves itself over the wire — Invoke errors
// loudly rather than silently dropping the *Runner).
type exampleRunVerbProvider struct{ builtinVerbBase }

func (exampleRunVerbProvider) Reserved() string { return "examplerunverb" }

// RunVerb returns a deterministic pass, echoing plugin_input.marker (proving the
// value round-trips author → provider → result) AND a fact read off the live *Runner
// (the run mode) — which an out-of-proc Invoke could never reach, proving the
// CheckVerbProvider dispatch keeps the executor. It decodes op.PluginInput (already a
// map on the *Op handed to RunVerb) through the shared decodePluginInput (R3) into the
// CUE-GENERATED struct (params.ExamplerunverbInput, generated from the unit's
// schema/examplerunverb.cue) — never a hand-parsed map.
func (exampleRunVerbProvider) RunVerb(_ context.Context, r *Runner, op *Op) CheckResult {
	marker := "examplerunverb-ok"
	var in params.ExamplerunverbInput
	decodePluginInput(op.PluginInput, &in)
	if in.Marker != "" {
		marker = in.Marker
	}
	return CheckResult{
		Status:  TestPass,
		Message: fmt.Sprintf("%s (mode=%s)", marker, runModeName(r.Mode)),
	}
}

func init() {
	RegisterBuiltinPluginUnit(PluginUnit{
		Providers: []Provider{exampleRunVerbProvider{}},
		Schema:    PluginSchema{CueSource: examplerunverb.Schema(), InputDefs: examplerunverb.InputDefs},
	})
}
