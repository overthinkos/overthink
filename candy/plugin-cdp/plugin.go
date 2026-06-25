// Package cdp is the importable, COMPILED-IN host-coupled `cdp` LIVE-CONTAINER verb:
// Chrome DevTools Protocol probing (open/list/click/eval/screenshot/…) against a live
// deployment. It implements kit.LiveVerbProvider — a SCHEMA-LESS live verb whose
// method-specific modifiers (Tab/URL/Expression/Selector/…) ride the closed base #Op, so
// there is NO plugin_input and NO served schema. RunVerb delegates the dispatch to the host
// via cc.RunCharlyVerb (build `charly check cdp <method>` argv from the allowlist, exec it
// against the live deployment, run the matcher + artifact pipeline); the candy owns only the
// verb's CONTRACT — the cdpMethods allowlist + the op.Cdp selector. Relocated out of charly's
// module (formerly charly/plugin_verb_cdp.go); COMPILED-IN-ONLY (the live CheckContext
// cannot cross a process boundary). The `charly check cdp` driver command stays host-side for
// now (cc.RunCharlyVerb self-invokes it); relocating the driver is a follow-on step.
package cdp

import (
	"context"

	"github.com/overthinkos/overthink/charly/plugin/kit"
	"github.com/overthinkos/overthink/charly/spec"
)

// NewLiveVerb returns the cdp verb as a kit.LiveVerbProvider for compiled-in registration
// (charly's registerCompiledDedicatedVerb wraps it via the schema-less dedicated path).
func NewLiveVerb() kit.LiveVerbProvider { return verb{} }

type verb struct{}

func (verb) Reserved() string { return "cdp" }

// RunVerb delegates to the host's shared live-verb dispatcher via cc.RunCharlyVerb, passing
// the verb word, the authored method (op.Cdp), and the cdpMethods allowlist.
func (verb) RunVerb(ctx context.Context, cc kit.CheckContext, op *spec.Op) kit.Result {
	return cc.RunCharlyVerb(ctx, op, "cdp", op.Cdp, cdpMethods)
}

func (verb) Methods() map[string]kit.MethodSpec { return cdpMethods }
func (verb) MethodField(op *spec.Op) string     { return op.Cdp }

// cdpMethods is the cdp verb's method allowlist (the dispatch data the host's runCharlyVerb
// reads). Hand-enumerated so authoring errors surface at `charly box validate` time. Each
// entry maps a method → its `charly check cdp <method>` subcommand path + required modifiers
// + the kit.PosX positional-arg builder.
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
