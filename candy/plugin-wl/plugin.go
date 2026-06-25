// Package wl is the importable, COMPILED-IN host-coupled `wl` LIVE-CONTAINER verb:
// Wayland/sway desktop automation (input, windows, screenshots, sway IPC, overlay) against
// a live deployment. A SCHEMA-LESS kit.LiveVerbProvider — its modifiers ride the closed base
// #Op; RunVerb delegates dispatch to the host via cc.RunCharlyVerb. Relocated out of charly's
// module (formerly charly/plugin_verb_wl.go); COMPILED-IN-ONLY. The `charly check wl` driver
// command stays host-side (cc.RunCharlyVerb self-invokes it).
package wl

import (
	"context"

	"github.com/overthinkos/overthink/charly/plugin/kit"
	"github.com/overthinkos/overthink/charly/spec"
)

// NewLiveVerb returns the wl verb as a kit.LiveVerbProvider for compiled-in registration.
func NewLiveVerb() kit.LiveVerbProvider { return verb{} }

type verb struct{}

func (verb) Reserved() string { return "wl" }

func (verb) RunVerb(ctx context.Context, cc kit.CheckContext, op *spec.Op) kit.Result {
	return cc.RunCharlyVerb(ctx, op, "wl", op.Wl, wlMethods)
}

func (verb) Methods() map[string]kit.MethodSpec { return wlMethods }
func (verb) MethodField(op *spec.Op) string     { return op.Wl }

// wlMethods is the wl verb's method allowlist (the dispatch data the host's runCharlyVerb reads).
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
