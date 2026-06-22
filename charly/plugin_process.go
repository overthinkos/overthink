package main

import (
	"context"

	"github.com/overthinkos/overthink/charly/plugin/builtins/process"
	"github.com/overthinkos/overthink/charly/plugin/builtins/process/params"
)

// processVerb is the BUILT-IN `process` plugin: it provides the `process` check verb
// (pgrep -x exact-name match) as a CheckVerbProvider, so runPluginVerb dispatches it
// IN-PROCESS via RunVerb — keeping the live *Runner (r.Exec) the pgrep probe needs and
// that cannot cross the wire. The verb left the closed #Op/spec.OpVerbs and is now
// authored as `plugin: process` + `plugin_input: {process: <name>, running: <bool>}`,
// dispatched through the provider registry, after the host has validated its
// plugin_input against the unit's served schema (charly/plugin/builtins/process).
// It is the FIRST goss (execution-needing) verb extracted, using the runPluginVerb
// CheckVerbProvider dispatch the examplerunverb reference proved.
//
// It embeds builtinVerbBase, which supplies Class()=ClassVerb and the in-proc-only
// Invoke stub (an in-proc verb never serves itself over the wire — Invoke errors
// loudly rather than silently dropping the *Runner).
type processVerb struct{ builtinVerbBase }

func (processVerb) Reserved() string { return "process" }

// RunVerb decodes the typed plugin_input (params.ProcessInput, generated from the
// unit's schema/process.cue) — never a hand-parsed map — and runs the pgrep probe via
// the live *Runner. The process name + optional running expectation come from
// plugin_input (the verb left the closed #Op, so they no longer ride the removed
// Op.Process field); the underlying pgrep impl stays in r.runProcess (checkrun_verbs.go).
func (processVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	var in params.ProcessInput
	decodePluginInput(op.PluginInput, &in)
	return r.runProcess(ctx, op, in.Process, in.Running)
}

func init() {
	RegisterBuiltinPluginUnit(PluginUnit{
		Providers: []Provider{processVerb{}},
		Schema:    PluginSchema{CueSource: process.Schema(), InputDefs: process.InputDefs},
	})
}
