package main

import "context"

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
// and the runWl dispatcher. The shared posArgs builder library (posXY/posText/posKeyName/
// posTarget/posCommand/… are all reused by other verbs — R3), the methodSpec type, and
// the artifactValidatableMethods allowlist stay in checkrun_charly_verbs.go.
type wlVerb struct{ builtinVerbBase }

func (wlVerb) Reserved() string { return "wl" }

func (wlVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runWl(ctx, op)
}

func (wlVerb) Methods() map[string]methodSpec { return wlMethods }
func (wlVerb) MethodField(c *Op) string       { return c.Wl }

// wlMethods is the wl verb's method allowlist (the dispatch data runCharlyVerb reads).
var wlMethods = map[string]methodSpec{
	// queries
	"status":     {path: []string{"wl", "status"}},
	"toplevel":   {path: []string{"wl", "toplevel"}},
	"windows":    {path: []string{"wl", "windows"}},
	"geometry":   {path: []string{"wl", "geometry"}, required: []string{"Target"}, posArgs: posTarget},
	"xprop":      {path: []string{"wl", "xprop"}, posArgs: posTargetOptional},
	"atspi":      {path: []string{"wl", "atspi"}, required: []string{"Action"}, posArgs: posAtspi},
	"screenshot": {path: []string{"wl", "screenshot"}, required: []string{"Artifact"}, posArgs: posArtifact, artifact: true},
	"clipboard":  {path: []string{"wl", "clipboard"}, required: []string{"Action"}, posArgs: posClipboard},

	// side-effect actions
	"click":        {path: []string{"wl", "click"}, required: []string{"X", "Y"}, posArgs: posXY},
	"double-click": {path: []string{"wl", "double-click"}, required: []string{"X", "Y"}, posArgs: posXY},
	"mouse":        {path: []string{"wl", "mouse"}, required: []string{"X", "Y"}, posArgs: posXY},
	"scroll":       {path: []string{"wl", "scroll"}, required: []string{"X", "Y", "Direction"}, posArgs: posScroll},
	"drag":         {path: []string{"wl", "drag"}, required: []string{"X", "Y", "X2", "Y2"}, posArgs: posXYXY}, // start (X,Y) → end (X2,Y2)
	"type":         {path: []string{"wl", "type"}, required: []string{"Text"}, posArgs: posText},
	"key":          {path: []string{"wl", "key"}, required: []string{"KeyName"}, posArgs: posKeyName},
	"key-combo":    {path: []string{"wl", "key-combo"}, required: []string{"Combo"}, posArgs: posCombo},
	"focus":        {path: []string{"wl", "focus"}, required: []string{"Target"}, posArgs: posTarget},
	"close":        {path: []string{"wl", "close"}, required: []string{"Target"}, posArgs: posTarget},
	"fullscreen":   {path: []string{"wl", "fullscreen"}, required: []string{"Target"}, posArgs: posTarget},
	"minimize":     {path: []string{"wl", "minimize"}, required: []string{"Target"}, posArgs: posTarget},
	"exec":         {path: []string{"wl", "exec"}, required: []string{"Command"}, posArgs: posCommand},
	"resolution":   {path: []string{"wl", "resolution"}, required: []string{"Target"}, posArgs: posTarget}, // target here = "WxH"

	// overlay nested
	"overlay-list":   {path: []string{"wl", "overlay", "list"}},
	"overlay-status": {path: []string{"wl", "overlay", "status"}},
	"overlay-show":   {path: []string{"wl", "overlay", "show"}, required: []string{"Text"}, posArgs: posOverlayShow},
	"overlay-hide":   {path: []string{"wl", "overlay", "hide"}, required: []string{"Target"}, posArgs: posTarget},

	// sway nested
	"sway-tree":       {path: []string{"wl", "sway", "tree"}},
	"sway-workspaces": {path: []string{"wl", "sway", "workspaces"}},
	"sway-outputs":    {path: []string{"wl", "sway", "outputs"}},
	"sway-msg":        {path: []string{"wl", "sway", "msg"}, required: []string{"Command"}, posArgs: posCommand},
	"sway-focus":      {path: []string{"wl", "sway", "focus"}, required: []string{"Target"}, posArgs: posTarget},
	"sway-move":       {path: []string{"wl", "sway", "move"}, required: []string{"Target"}, posArgs: posTarget},
	"sway-resize":     {path: []string{"wl", "sway", "resize"}, required: []string{"Target"}, posArgs: posTarget},
	"sway-layout":     {path: []string{"wl", "sway", "layout"}, required: []string{"Target"}, posArgs: posTarget},
	"sway-workspace":  {path: []string{"wl", "sway", "workspace"}, required: []string{"Target"}, posArgs: posTarget},
	"sway-kill":       {path: []string{"wl", "sway", "kill"}},
	"sway-floating":   {path: []string{"wl", "sway", "floating"}},
	"sway-reload":     {path: []string{"wl", "sway", "reload"}},
}

func (r *Runner) runWl(ctx context.Context, c *Op) CheckResult {
	return r.runCharlyVerb(ctx, c, "wl", c.Wl, wlMethods)
}

var _ = registerDedicatedBuiltin(wlVerb{})
