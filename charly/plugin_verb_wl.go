package main

import (
	"context"

	"github.com/overthinkos/overthink/charly/plugin/kit"
)

// wlVerb is the BUILT-IN `wl` LIVE-CONTAINER verb, extracted into its OWN dedicated
// file (Phase 1, the live-container-verb relocation). Like cdp/vnc, wl stays a
// FIRST-CLASS #Op verb: it keeps its dedicated `wl:` discriminator and its
// method-specific modifiers (X/Y/Text/KeyName/Combo/Target/Action/Direction/…) on the
// closed base #Op — there is NO plugin_input and therefore NO served plugin schema. So
// it self-registers via registerDedicatedBuiltin (the schema-less dedicated-provider
// path), INTENTIONALLY absent from BOTH builtinProviderInstances and the `providers:`
// manifest, yet resolving + dispatching through the SAME providerRegistry (the verb +
// method-allowlist bijection gates still see it). It embeds builtinVerbBase for
// Class()=ClassVerb + the in-proc-only Invoke stub (a live verb carries the *Runner and
// never serves itself over the wire).
//
// This file owns the verb's complete contract: the provider (Reserved/RunVerb), the
// LiveVerbProvider method contract (Methods/MethodField), the wlMethods method allowlist,
// and the runWl dispatcher. The shared kit.PosArgs builder library (kit.PosXY/kit.PosText/kit.PosKeyName/
// kit.PosTarget/kit.PosCommand/… are all reused by other verbs — R3), the kit.MethodSpec type, and
// the artifactValidatableMethods allowlist stay in checkrun_charly_verbs.go.
type wlVerb struct{ builtinVerbBase }

func (wlVerb) Reserved() string { return "wl" }

func (wlVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runWl(ctx, op)
}

func (wlVerb) Methods() map[string]kit.MethodSpec { return wlMethods }
func (wlVerb) MethodField(c *Op) string           { return c.Wl }

// wlMethods is the wl verb's method allowlist (the dispatch data runCharlyVerb reads).
var wlMethods = map[string]kit.MethodSpec{
	// queries
	"status":     {Path: []string{"wl", "status"}},
	"toplevel":   {Path: []string{"wl", "toplevel"}},
	"windows":    {Path: []string{"wl", "windows"}},
	"geometry":   {Path: []string{"wl", "geometry"}, Required: []string{"Target"}, PosArgs: kit.PosTarget},
	"xprop":      {Path: []string{"wl", "xprop"}, PosArgs: kit.PosTargetOptional},
	"atspi":      {Path: []string{"wl", "atspi"}, Required: []string{"Action"}, PosArgs: kit.PosAtspi},
	"screenshot": {Path: []string{"wl", "screenshot"}, Required: []string{"Artifact"}, PosArgs: kit.PosArtifact, Artifact: true},
	"clipboard":  {Path: []string{"wl", "clipboard"}, Required: []string{"Action"}, PosArgs: kit.PosClipboard},

	// side-effect actions
	"click":        {Path: []string{"wl", "click"}, Required: []string{"X", "Y"}, PosArgs: kit.PosXY},
	"double-click": {Path: []string{"wl", "double-click"}, Required: []string{"X", "Y"}, PosArgs: kit.PosXY},
	"mouse":        {Path: []string{"wl", "mouse"}, Required: []string{"X", "Y"}, PosArgs: kit.PosXY},
	"scroll":       {Path: []string{"wl", "scroll"}, Required: []string{"X", "Y", "Direction"}, PosArgs: kit.PosScroll},
	"drag":         {Path: []string{"wl", "drag"}, Required: []string{"X", "Y", "X2", "Y2"}, PosArgs: kit.PosXYXY}, // start (X,Y) → end (X2,Y2)
	"type":         {Path: []string{"wl", "type"}, Required: []string{"Text"}, PosArgs: kit.PosText},
	"key":          {Path: []string{"wl", "key"}, Required: []string{"KeyName"}, PosArgs: kit.PosKeyName},
	"key-combo":    {Path: []string{"wl", "key-combo"}, Required: []string{"Combo"}, PosArgs: kit.PosCombo},
	"focus":        {Path: []string{"wl", "focus"}, Required: []string{"Target"}, PosArgs: kit.PosTarget},
	"close":        {Path: []string{"wl", "close"}, Required: []string{"Target"}, PosArgs: kit.PosTarget},
	"fullscreen":   {Path: []string{"wl", "fullscreen"}, Required: []string{"Target"}, PosArgs: kit.PosTarget},
	"minimize":     {Path: []string{"wl", "minimize"}, Required: []string{"Target"}, PosArgs: kit.PosTarget},
	"exec":         {Path: []string{"wl", "exec"}, Required: []string{"Command"}, PosArgs: kit.PosCommand},
	"resolution":   {Path: []string{"wl", "resolution"}, Required: []string{"Target"}, PosArgs: kit.PosTarget}, // target here = "WxH"

	// overlay nested
	"overlay-list":   {Path: []string{"wl", "overlay", "list"}},
	"overlay-status": {Path: []string{"wl", "overlay", "status"}},
	"overlay-show":   {Path: []string{"wl", "overlay", "show"}, Required: []string{"Text"}, PosArgs: kit.PosOverlayShow},
	"overlay-hide":   {Path: []string{"wl", "overlay", "hide"}, Required: []string{"Target"}, PosArgs: kit.PosTarget},

	// sway nested
	"sway-tree":       {Path: []string{"wl", "sway", "tree"}},
	"sway-workspaces": {Path: []string{"wl", "sway", "workspaces"}},
	"sway-outputs":    {Path: []string{"wl", "sway", "outputs"}},
	"sway-msg":        {Path: []string{"wl", "sway", "msg"}, Required: []string{"Command"}, PosArgs: kit.PosCommand},
	"sway-focus":      {Path: []string{"wl", "sway", "focus"}, Required: []string{"Target"}, PosArgs: kit.PosTarget},
	"sway-move":       {Path: []string{"wl", "sway", "move"}, Required: []string{"Target"}, PosArgs: kit.PosTarget},
	"sway-resize":     {Path: []string{"wl", "sway", "resize"}, Required: []string{"Target"}, PosArgs: kit.PosTarget},
	"sway-layout":     {Path: []string{"wl", "sway", "layout"}, Required: []string{"Target"}, PosArgs: kit.PosTarget},
	"sway-workspace":  {Path: []string{"wl", "sway", "workspace"}, Required: []string{"Target"}, PosArgs: kit.PosTarget},
	"sway-kill":       {Path: []string{"wl", "sway", "kill"}},
	"sway-floating":   {Path: []string{"wl", "sway", "floating"}},
	"sway-reload":     {Path: []string{"wl", "sway", "reload"}},
}

func (r *Runner) runWl(ctx context.Context, c *Op) CheckResult {
	return r.runCharlyVerb(ctx, c, "wl", c.Wl, wlMethods)
}

var _ = registerDedicatedBuiltin(wlVerb{})
