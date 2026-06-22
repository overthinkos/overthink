package main

import (
	"context"

	"github.com/overthinkos/overthink/charly/plugin/builtins/dns"
	"github.com/overthinkos/overthink/charly/plugin/builtins/dns/params"
)

// dnsVerb is the BUILT-IN `dns` plugin: it provides the `dns` check verb (host-side
// net.LookupIP under live mode, in-container `getent hosts` under box mode) as a
// CheckVerbProvider, so runPluginVerb dispatches it IN-PROCESS via RunVerb — keeping
// the live *Runner (r.Exec / r.Mode) the probe needs and that cannot cross the wire.
// The verb left the closed #Op/spec.OpVerbs and is now authored as `plugin: dns` +
// `plugin_input: {dns, resolvable, addrs, server}`, dispatched through the provider
// registry, after the host has validated its plugin_input against the unit's served
// schema (charly/plugin/builtins/dns). Mirrors the process/port extraction; the impl
// stays in r.runDNS.
//
// It embeds builtinVerbBase, which supplies Class()=ClassVerb and the in-proc-only
// Invoke stub (an in-proc verb never serves itself over the wire — Invoke errors
// loudly rather than silently dropping the *Runner).
type dnsVerb struct{ builtinVerbBase }

func (dnsVerb) Reserved() string { return "dns" }

// RunVerb decodes the typed plugin_input (params.DnsInput, generated from the unit's
// schema/dns.cue) and runs the resolve probe via the live *Runner. The hostname +
// modifiers come from plugin_input (the verb left the closed #Op, so they no longer
// ride the removed Op.DNS/Resolvable fields); the impl stays in r.runDNS. `server` is
// decoded for authoring compatibility but, like the pre-extraction Op.Server, is not
// consumed by the resolve.
func (dnsVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	var in params.DnsInput
	decodePluginInput(op.PluginInput, &in)
	return r.runDNS(ctx, op, in.DNS, in.Resolvable, in.Addrs)
}

func init() {
	RegisterBuiltinPluginUnit(PluginUnit{
		Providers: []Provider{dnsVerb{}},
		Schema:    PluginSchema{CueSource: dns.Schema(), InputDefs: dns.InputDefs},
	})
}
