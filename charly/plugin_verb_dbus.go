package main

import (
	"context"

	"github.com/overthinkos/overthink/charly/plugin/kit"
)

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
// allowlist, and the runDbus dispatcher. The shared kit.MethodSpec type + the kit.PosX
// builder library (kit.PosDbusCall/kit.PosDbusIntrospect/kit.PosDbusNotify) live in
// charly/plugin/kit/liveverb.go; the artifact-validatable set is derived from spec.Artifact.
// The runCharlyVerb dispatcher stays in checkrun_charly_verbs.go.
type dbusVerb struct{ builtinVerbBase }

func (dbusVerb) Reserved() string { return "dbus" }

func (dbusVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runDbus(ctx, op)
}

func (dbusVerb) Methods() map[string]kit.MethodSpec { return dbusMethods }
func (dbusVerb) MethodField(c *Op) string           { return c.Dbus }

// dbusMethods is the dbus verb's method allowlist (the dispatch data runCharlyVerb reads).
var dbusMethods = map[string]kit.MethodSpec{
	"list":       {Path: []string{"dbus", "list"}},
	"call":       {Path: []string{"dbus", "call"}, Required: []string{"Dest", "Path", "Method"}, PosArgs: kit.PosDbusCall},
	"introspect": {Path: []string{"dbus", "introspect"}, Required: []string{"Dest", "Path"}, PosArgs: kit.PosDbusIntrospect},
	"notify":     {Path: []string{"dbus", "notify"}, Required: []string{"Text"}, PosArgs: kit.PosDbusNotify},
}

func (r *Runner) runDbus(ctx context.Context, c *Op) CheckResult {
	return r.runCharlyVerb(ctx, c, "dbus", c.Dbus, dbusMethods)
}

var _ = registerDedicatedBuiltin(dbusVerb{})
