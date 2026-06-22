package main

import (
	"context"

	"github.com/overthinkos/overthink/charly/plugin/builtins/addr"
	"github.com/overthinkos/overthink/charly/plugin/builtins/addr/params"
)

// addrVerb is the BUILT-IN `addr` plugin: it provides the `addr` check verb (host-side
// TCP dial under live mode, in-container `nc -z` under box mode) as a CheckVerbProvider,
// so runPluginVerb dispatches it IN-PROCESS via RunVerb — keeping the live *Runner
// (r.Mode / r.Exec / r.DialTimeout) the probe + host dial need and that cannot cross the
// wire. The verb left the closed #Op/spec.OpVerbs and is now authored as `plugin: addr` +
// `plugin_input: {addr, reachable}`, dispatched through the provider registry, after the
// host has validated its plugin_input against the unit's served schema
// (charly/plugin/builtins/addr). Mirrors the process/port/dns extraction; the impl stays
// in r.runAddr.
//
// It embeds builtinVerbBase, which supplies Class()=ClassVerb and the in-proc-only
// Invoke stub (an in-proc verb never serves itself over the wire — Invoke errors
// loudly rather than silently dropping the *Runner).
type addrVerb struct{ builtinVerbBase }

func (addrVerb) Reserved() string { return "addr" }

// RunVerb decodes the typed plugin_input (params.AddrInput, generated from the unit's
// schema/addr.cue) and runs the reachability probe via the live *Runner. The host:port +
// reachable expectation come from plugin_input (the verb left the closed #Op, so they no
// longer ride the removed Op.Addr/Reachable fields); the impl stays in r.runAddr.
func (addrVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	var in params.AddrInput
	decodePluginInput(op.PluginInput, &in)
	return r.runAddr(ctx, op, in.Addr, in.Reachable)
}

func init() {
	RegisterBuiltinPluginUnit(PluginUnit{
		Providers: []Provider{addrVerb{}},
		Schema:    PluginSchema{CueSource: addr.Schema(), InputDefs: addr.InputDefs},
	})
}
