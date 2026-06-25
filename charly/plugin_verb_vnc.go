package main

import (
	"context"

	"github.com/overthinkos/overthink/charly/plugin/kit"
)

// vncVerb is the BUILT-IN `vnc` LIVE-CONTAINER verb, extracted into its OWN dedicated
// file (Phase 1, the live-container-verb relocation). Like cdp, vnc stays a FIRST-CLASS
// #Op verb: it keeps its dedicated `vnc:` discriminator and its method-specific modifiers
// (X/Y/Text/KeyName/Method/Artifact) on the closed base #Op — there is NO plugin_input and
// therefore NO served plugin schema. So it self-registers via registerDedicatedBuiltin
// (the schema-less dedicated-provider path), INTENTIONALLY absent from BOTH
// builtinProviderInstances and the `providers:` manifest, yet resolving + dispatching
// through the SAME providerRegistry (the verb + method-allowlist bijection gates still see
// it). It embeds builtinVerbBase for Class()=ClassVerb + the in-proc-only Invoke stub (a
// live verb carries the *Runner and never serves itself over the wire).
//
// This file owns the verb's complete contract: the provider (Reserved/RunVerb), the
// LiveVerbProvider method contract (Methods/MethodField), the vncMethods method allowlist,
// and the runVnc dispatcher. The shared kit.PosArgs builder library (kit.PosArtifact/kit.PosXY/kit.PosText/
// kit.PosKeyName/kit.PosCommand are all reused by wl/spice/… — R3), the kit.MethodSpec type, and the
// artifactValidatableMethods allowlist stay in checkrun_charly_verbs.go.
type vncVerb struct{ builtinVerbBase }

func (vncVerb) Reserved() string { return "vnc" }

func (vncVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runVnc(ctx, op)
}

func (vncVerb) Methods() map[string]kit.MethodSpec { return vncMethods }
func (vncVerb) MethodField(c *Op) string           { return c.Vnc }

// vncMethods is the vnc verb's method allowlist (the dispatch data runCharlyVerb reads).
var vncMethods = map[string]kit.MethodSpec{
	"status":     {Path: []string{"vnc", "status"}},
	"screenshot": {Path: []string{"vnc", "screenshot"}, Required: []string{"Artifact"}, PosArgs: kit.PosArtifact, Artifact: true},
	"click":      {Path: []string{"vnc", "click"}, Required: []string{"X", "Y"}, PosArgs: kit.PosXY},
	"mouse":      {Path: []string{"vnc", "mouse"}, Required: []string{"X", "Y"}, PosArgs: kit.PosXY},
	"type":       {Path: []string{"vnc", "type"}, Required: []string{"Text"}, PosArgs: kit.PosText},
	"key":        {Path: []string{"vnc", "key"}, Required: []string{"KeyName"}, PosArgs: kit.PosKeyName},
	"rfb":        {Path: []string{"vnc", "rfb"}, Required: []string{"Method"}, PosArgs: kit.PosCommand}, // Method field reused as rfb method
	"passwd":     {Path: []string{"vnc", "passwd"}},
}

func (r *Runner) runVnc(ctx context.Context, c *Op) CheckResult {
	return r.runCharlyVerb(ctx, c, "vnc", c.Vnc, vncMethods)
}

var _ = registerDedicatedBuiltin(vncVerb{})
