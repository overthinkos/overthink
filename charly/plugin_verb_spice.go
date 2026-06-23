package main

import "context"

// spiceVerb is the BUILT-IN `spice` LIVE-CONTAINER verb, extracted into its OWN dedicated
// file (Phase 1, the live-container-verb relocation). Like cdp/vnc, spice stays a
// FIRST-CLASS #Op verb: it keeps its dedicated `spice:` discriminator and its
// method-specific modifiers (X/Y/Text/KeyName/Artifact) on the closed base #Op — there is
// NO plugin_input and therefore NO served plugin schema. So it self-registers via
// registerDedicatedBuiltin (the schema-less dedicated-provider path), INTENTIONALLY
// absent from BOTH builtinProviderInstances and the `providers:` manifest, yet resolving
// + dispatching through the SAME providerRegistry (the verb + method-allowlist bijection
// gates still see it). It embeds builtinVerbBase for Class()=ClassVerb + the in-proc-only
// Invoke stub (a live verb carries the *Runner and never serves itself over the wire).
//
// `charly check spice <method>` speaks the SPICE wire protocol (github.com/Shells-com/spice)
// to a running VM's SPICE port. Host-side; only applicable to `vm:<name>` deploys that
// expose SPICE graphics.
//
// This file owns the verb's complete contract: the provider (Reserved/RunVerb), the
// LiveVerbProvider method contract (Methods/MethodField), the spiceMethods method
// allowlist, and the runSpice dispatcher. The shared posArgs builder library
// (posArtifact/posXY/posText/posKeyName), the methodSpec type, and the
// artifactValidatableMethods allowlist (spice/screenshot, spice/cursor) stay in
// checkrun_charly_verbs.go.
type spiceVerb struct{ builtinVerbBase }

func (spiceVerb) Reserved() string { return "spice" }

func (spiceVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runSpice(ctx, op)
}

func (spiceVerb) Methods() map[string]methodSpec { return spiceMethods }
func (spiceVerb) MethodField(c *Op) string       { return c.Spice }

// spiceMethods is the spice verb's method allowlist (the dispatch data runCharlyVerb reads).
var spiceMethods = map[string]methodSpec{
	"status":     {path: []string{"spice", "status"}},
	"screenshot": {path: []string{"spice", "screenshot"}, posArgs: posArtifact, artifact: true},
	"cursor":     {path: []string{"spice", "cursor"}, posArgs: posArtifact, artifact: true},
	"click":      {path: []string{"spice", "click"}, posArgs: posXY},
	"mouse":      {path: []string{"spice", "mouse"}, posArgs: posXY},
	"type":       {path: []string{"spice", "type"}, required: []string{"Text"}, posArgs: posText},
	"key":        {path: []string{"spice", "key"}, required: []string{"KeyName"}, posArgs: posKeyName},
}

func (r *Runner) runSpice(ctx context.Context, c *Op) CheckResult {
	return r.runCharlyVerb(ctx, c, "spice", c.Spice, spiceMethods)
}

var _ = registerDedicatedBuiltin(spiceVerb{})
