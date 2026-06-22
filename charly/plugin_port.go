package main

import (
	"context"

	"github.com/overthinkos/overthink/charly/plugin/builtins/port"
	"github.com/overthinkos/overthink/charly/plugin/builtins/port/params"
)

// portVerb is the BUILT-IN `port` plugin: it provides the `port` check verb (the
// in-container `ss`/`netstat` listening probe, or a host-side TCP dial for outside-in
// reachability) as a CheckVerbProvider, so runPluginVerb dispatches it IN-PROCESS via
// RunVerb — keeping the live *Runner (r.Exec / r.DialTimeout) the probe + dial need and
// that cannot cross the wire. The verb left the closed #Op/spec.OpVerbs and is now
// authored as `plugin: port` + `plugin_input: {port, listening, ip, reachable}`,
// dispatched through the provider registry, after the host has validated its
// plugin_input against the unit's served schema (charly/plugin/builtins/port). Mirrors
// the process extraction; the underlying probe logic stays in r.runPort/r.dialPort.
//
// It embeds builtinVerbBase, which supplies Class()=ClassVerb and the in-proc-only
// Invoke stub (an in-proc verb never serves itself over the wire — Invoke errors
// loudly rather than silently dropping the *Runner).
type portVerb struct{ builtinVerbBase }

func (portVerb) Reserved() string { return "port" }

// RunVerb decodes the typed plugin_input (params.PortInput, generated from the unit's
// schema/port.cue) and runs the listening/reachability probe via the live *Runner. The
// port number + modifiers come from plugin_input (the verb left the closed #Op, so they
// no longer ride the removed Op.Port/Listening/IP fields); the impl stays in r.runPort.
func (portVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	var in params.PortInput
	decodePluginInput(op.PluginInput, &in)
	return r.runPort(ctx, op, in.Port, in.Listening, in.Reachable, in.IP)
}

func init() {
	RegisterBuiltinPluginUnit(PluginUnit{
		Providers: []Provider{portVerb{}},
		Schema:    PluginSchema{CueSource: port.Schema(), InputDefs: port.InputDefs},
	})
}
