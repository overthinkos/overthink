package main

import (
	"context"

	iface "github.com/overthinkos/overthink/charly/plugin/builtins/interface"
	"github.com/overthinkos/overthink/charly/plugin/builtins/interface/params"
)

// interfaceVerb is the BUILT-IN `interface` plugin: it provides the `interface` check
// verb (`ip -o addr show <name>` existence + MTU + address probe) as a CheckVerbProvider,
// so runPluginVerb dispatches it IN-PROCESS via RunVerb — keeping the live *Runner
// (r.Exec) the probe needs and that cannot cross the wire. The verb left the closed
// #Op/spec.OpVerbs and is now authored as `plugin: interface` + `plugin_input:
// {interface, mtu, addrs}`, dispatched through the provider registry, after the host has
// validated its plugin_input against the unit's served schema
// (charly/plugin/builtins/interface). Mirrors the process/port/dns extraction; the impl
// stays in r.runInterface.
//
// It embeds builtinVerbBase, which supplies Class()=ClassVerb and the in-proc-only
// Invoke stub (an in-proc verb never serves itself over the wire — Invoke errors
// loudly rather than silently dropping the *Runner).
type interfaceVerb struct{ builtinVerbBase }

func (interfaceVerb) Reserved() string { return "interface" }

// RunVerb decodes the typed plugin_input (params.InterfaceInput, generated from the
// unit's schema/interface.cue) and runs the probe via the live *Runner. The interface
// name + mtu/addrs modifiers come from plugin_input (the verb left the closed #Op, so
// they no longer ride the removed Op.Interface/MTU/Addrs fields); the impl stays in
// r.runInterface.
func (interfaceVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	var in params.InterfaceInput
	decodePluginInput(op.PluginInput, &in)
	return r.runInterface(ctx, op, in.Interface, in.MTU, in.Addrs)
}

func init() {
	RegisterBuiltinPluginUnit(PluginUnit{
		Providers: []Provider{interfaceVerb{}},
		Schema:    PluginSchema{CueSource: iface.Schema(), InputDefs: iface.InputDefs},
	})
}
