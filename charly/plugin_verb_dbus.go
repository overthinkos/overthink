package main

import "context"

// dbusVerb is the BUILT-IN `dbus` LIVE-CONTAINER verb, extracted into its OWN dedicated
// file (Phase 1, the live-container-verb relocation). Like cdp/vnc, dbus stays a
// FIRST-CLASS #Op verb: it keeps its dedicated `dbus:` discriminator and its
// method-specific modifiers (Dest/Path/Method/Args/Text) on the closed base #Op — there
// is NO plugin_input and therefore NO served plugin schema. So it self-registers via
// registerDedicatedBuiltin (the schema-less dedicated-provider path), INTENTIONALLY
// absent from BOTH builtinProviderInstances and the `providers:` manifest, yet resolving
// + dispatching through the SAME providerRegistry (the verb + method-allowlist bijection
// gates still see it). It embeds builtinVerbBase for Class()=ClassVerb + the in-proc-only
// Invoke stub (a live verb carries the *Runner and never serves itself over the wire).
//
// This file owns the verb's complete contract: the provider (Reserved/RunVerb), the
// LiveVerbProvider method contract (Methods/MethodField), the dbusMethods method
// allowlist, and the runDbus dispatcher. The shared posArgs builder library
// (posDbusCall/posDbusIntrospect/posDbusNotify), the methodSpec type, and
// artifactValidatableMethods stay in checkrun_charly_verbs.go.
type dbusVerb struct{ builtinVerbBase }

func (dbusVerb) Reserved() string { return "dbus" }

func (dbusVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runDbus(ctx, op)
}

func (dbusVerb) Methods() map[string]methodSpec { return dbusMethods }
func (dbusVerb) MethodField(c *Op) string       { return c.Dbus }

// dbusMethods is the dbus verb's method allowlist (the dispatch data runCharlyVerb reads).
var dbusMethods = map[string]methodSpec{
	"list":       {path: []string{"dbus", "list"}},
	"call":       {path: []string{"dbus", "call"}, required: []string{"Dest", "Path", "Method"}, posArgs: posDbusCall},
	"introspect": {path: []string{"dbus", "introspect"}, required: []string{"Dest", "Path"}, posArgs: posDbusIntrospect},
	"notify":     {path: []string{"dbus", "notify"}, required: []string{"Text"}, posArgs: posDbusNotify},
}

func (r *Runner) runDbus(ctx context.Context, c *Op) CheckResult {
	return r.runCharlyVerb(ctx, c, "dbus", c.Dbus, dbusMethods)
}

var _ = registerDedicatedBuiltin(dbusVerb{})
