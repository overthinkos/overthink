package main

import "context"

// cdpVerb is the BUILT-IN `cdp` LIVE-CONTAINER verb, extracted into its OWN dedicated
// file (Phase 1, the live-container-verb relocation). Unlike the goss/state-provision
// verbs (file/command/…), cdp stays a FIRST-CLASS #Op verb: it keeps its dedicated
// `cdp:` discriminator and its method-specific modifiers (Tab/URL/Expression/Selector/
// X/Y/Text/KeyName/…) on the closed base #Op — there is NO plugin_input and therefore
// NO served plugin schema. So it self-registers via registerDedicatedBuiltin (the
// schema-less dedicated-provider path, like the deploy-shape kinds / IR steps) rather
// than the schema-carrying RegisterBuiltinPluginUnit the plugin_input verbs use; it is
// INTENTIONALLY absent from BOTH builtinProviderInstances and the `providers:` manifest,
// yet resolves + dispatches through the SAME providerRegistry (the verb + method-allowlist
// bijection gates still see it). It embeds builtinVerbBase for Class()=ClassVerb + the
// in-proc-only Invoke stub (a live verb carries the *Runner and never serves itself over
// the wire).
//
// This file owns the verb's complete contract: the provider (Reserved/RunVerb), the
// LiveVerbProvider method contract (Methods/MethodField — the required-modifier/artifact
// rules the host's validateCharlyVerb reads through the registry), the cdpMethods method
// allowlist (each method → its CLI subcommand path + required modifiers + posArgs
// dispatch), and the runCdp dispatcher. The shared posArgs builder library + the
// methodSpec type + artifactValidatableMethods stay in checkrun_charly_verbs.go (reused
// across every live verb — R3).
type cdpVerb struct{ builtinVerbBase }

func (cdpVerb) Reserved() string { return "cdp" }

func (cdpVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runCdp(ctx, op)
}

func (cdpVerb) Methods() map[string]methodSpec { return cdpMethods }
func (cdpVerb) MethodField(c *Op) string       { return c.Cdp }

// cdpMethods is the cdp verb's method allowlist (the dispatch data runCharlyVerb reads).
// Hand-enumerated so authoring errors surface at `charly box validate` time.
var cdpMethods = map[string]methodSpec{
	// queries — produce assertable output
	"status":     {path: []string{"cdp", "status"}},
	"list":       {path: []string{"cdp", "list"}},
	"url":        {path: []string{"cdp", "url"}, required: []string{"Tab"}, posArgs: posTab},
	"text":       {path: []string{"cdp", "text"}, required: []string{"Tab"}, posArgs: posTab},
	"html":       {path: []string{"cdp", "html"}, required: []string{"Tab"}, posArgs: posTab},
	"eval":       {path: []string{"cdp", "eval"}, required: []string{"Tab", "Expression"}, posArgs: posTabExpression},
	"axtree":     {path: []string{"cdp", "axtree"}, required: []string{"Tab"}, posArgs: posTabQuery},
	"coords":     {path: []string{"cdp", "coords"}, required: []string{"Tab", "Selector"}, posArgs: posTabSelector},
	"raw":        {path: []string{"cdp", "raw"}, required: []string{"Tab", "Method"}, posArgs: posCdpRaw},
	"wait":       {path: []string{"cdp", "wait"}, required: []string{"Tab", "Selector"}, posArgs: posTabSelector},
	"screenshot": {path: []string{"cdp", "screenshot"}, required: []string{"Tab", "Artifact"}, posArgs: posTabArtifact, artifact: true},

	// side-effect actions — pass on exit 0
	"open":  {path: []string{"cdp", "open"}, required: []string{"URL"}, posArgs: posURL},
	"close": {path: []string{"cdp", "close"}, required: []string{"Tab"}, posArgs: posTab},
	"click": {path: []string{"cdp", "click"}, required: []string{"Tab", "Selector"}, posArgs: posTabSelector},
	"type":  {path: []string{"cdp", "type"}, required: []string{"Tab", "Selector", "Text"}, posArgs: posTabSelectorText},

	// SPA nested subcommands
	"spa-status":    {path: []string{"cdp", "spa", "status"}, required: []string{"Tab"}, posArgs: posTab},
	"spa-click":     {path: []string{"cdp", "spa", "click"}, required: []string{"Tab", "X", "Y"}, posArgs: posTabXY},
	"spa-type":      {path: []string{"cdp", "spa", "type"}, required: []string{"Tab", "Text"}, posArgs: posTabText},
	"spa-key":       {path: []string{"cdp", "spa", "key"}, required: []string{"Tab", "KeyName"}, posArgs: posTabKeyName},
	"spa-key-combo": {path: []string{"cdp", "spa", "key-combo"}, required: []string{"Tab", "Combo"}, posArgs: posTabCombo},
	"spa-mouse":     {path: []string{"cdp", "spa", "mouse"}, required: []string{"Tab", "X", "Y"}, posArgs: posTabXY},
}

func (r *Runner) runCdp(ctx context.Context, c *Op) CheckResult {
	return r.runCharlyVerb(ctx, c, "cdp", c.Cdp, cdpMethods)
}

var _ = registerDedicatedBuiltin(cdpVerb{})
