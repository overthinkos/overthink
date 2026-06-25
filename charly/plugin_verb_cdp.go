package main

import (
	"context"

	"github.com/overthinkos/overthink/charly/plugin/kit"
)

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
// allowlist (each method → its CLI subcommand path + required modifiers + kit.PosArgs
// dispatch), and the runCdp dispatcher. The shared kit.PosArgs builder library + the
// kit.MethodSpec type + artifactValidatableMethods stay in checkrun_charly_verbs.go (reused
// across every live verb — R3).
type cdpVerb struct{ builtinVerbBase }

func (cdpVerb) Reserved() string { return "cdp" }

func (cdpVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runCdp(ctx, op)
}

func (cdpVerb) Methods() map[string]kit.MethodSpec { return cdpMethods }
func (cdpVerb) MethodField(c *Op) string           { return c.Cdp }

// cdpMethods is the cdp verb's method allowlist (the dispatch data runCharlyVerb reads).
// Hand-enumerated so authoring errors surface at `charly box validate` time.
var cdpMethods = map[string]kit.MethodSpec{
	// queries — produce assertable output
	"status":     {Path: []string{"cdp", "status"}},
	"list":       {Path: []string{"cdp", "list"}},
	"url":        {Path: []string{"cdp", "url"}, Required: []string{"Tab"}, PosArgs: kit.PosTab},
	"text":       {Path: []string{"cdp", "text"}, Required: []string{"Tab"}, PosArgs: kit.PosTab},
	"html":       {Path: []string{"cdp", "html"}, Required: []string{"Tab"}, PosArgs: kit.PosTab},
	"eval":       {Path: []string{"cdp", "eval"}, Required: []string{"Tab", "Expression"}, PosArgs: kit.PosTabExpression},
	"axtree":     {Path: []string{"cdp", "axtree"}, Required: []string{"Tab"}, PosArgs: kit.PosTabQuery},
	"coords":     {Path: []string{"cdp", "coords"}, Required: []string{"Tab", "Selector"}, PosArgs: kit.PosTabSelector},
	"raw":        {Path: []string{"cdp", "raw"}, Required: []string{"Tab", "Method"}, PosArgs: kit.PosCdpRaw},
	"wait":       {Path: []string{"cdp", "wait"}, Required: []string{"Tab", "Selector"}, PosArgs: kit.PosTabSelector},
	"screenshot": {Path: []string{"cdp", "screenshot"}, Required: []string{"Tab", "Artifact"}, PosArgs: kit.PosTabArtifact, Artifact: true},

	// side-effect actions — pass on exit 0
	"open":  {Path: []string{"cdp", "open"}, Required: []string{"URL"}, PosArgs: kit.PosURL},
	"close": {Path: []string{"cdp", "close"}, Required: []string{"Tab"}, PosArgs: kit.PosTab},
	"click": {Path: []string{"cdp", "click"}, Required: []string{"Tab", "Selector"}, PosArgs: kit.PosTabSelector},
	"type":  {Path: []string{"cdp", "type"}, Required: []string{"Tab", "Selector", "Text"}, PosArgs: kit.PosTabSelectorText},

	// SPA nested subcommands
	"spa-status":    {Path: []string{"cdp", "spa", "status"}, Required: []string{"Tab"}, PosArgs: kit.PosTab},
	"spa-click":     {Path: []string{"cdp", "spa", "click"}, Required: []string{"Tab", "X", "Y"}, PosArgs: kit.PosTabXY},
	"spa-type":      {Path: []string{"cdp", "spa", "type"}, Required: []string{"Tab", "Text"}, PosArgs: kit.PosTabText},
	"spa-key":       {Path: []string{"cdp", "spa", "key"}, Required: []string{"Tab", "KeyName"}, PosArgs: kit.PosTabKeyName},
	"spa-key-combo": {Path: []string{"cdp", "spa", "key-combo"}, Required: []string{"Tab", "Combo"}, PosArgs: kit.PosTabCombo},
	"spa-mouse":     {Path: []string{"cdp", "spa", "mouse"}, Required: []string{"Tab", "X", "Y"}, PosArgs: kit.PosTabXY},
}

func (r *Runner) runCdp(ctx context.Context, c *Op) CheckResult {
	return r.runCharlyVerb(ctx, c, "cdp", c.Cdp, cdpMethods)
}

var _ = registerDedicatedBuiltin(cdpVerb{})
