// Package vnc is the importable, COMPILED-IN host-coupled `vnc` LIVE-CONTAINER verb:
// control a VNC desktop in a running deployment (status, screenshot, input, RFB). A
// SCHEMA-LESS kit.LiveVerbProvider — its modifiers ride the closed base #Op; RunVerb
// delegates dispatch to the host via cc.RunCharlyVerb. Relocated out of charly's module
// (formerly charly/plugin_verb_vnc.go); COMPILED-IN-ONLY. The `charly check vnc` driver
// command stays host-side (cc.RunCharlyVerb self-invokes it).
package vnc

import (
	"context"

	"github.com/overthinkos/overthink/charly/plugin/kit"
	"github.com/overthinkos/overthink/charly/spec"
)

// NewLiveVerb returns the vnc verb as a kit.LiveVerbProvider for compiled-in registration.
func NewLiveVerb() kit.LiveVerbProvider { return verb{} }

type verb struct{}

func (verb) Reserved() string { return "vnc" }

func (verb) RunVerb(ctx context.Context, cc kit.CheckContext, op *spec.Op) kit.Result {
	return cc.RunCharlyVerb(ctx, op, "vnc", op.Vnc, vncMethods)
}

func (verb) Methods() map[string]kit.MethodSpec { return vncMethods }
func (verb) MethodField(op *spec.Op) string     { return op.Vnc }

// vncMethods is the vnc verb's method allowlist (the dispatch data the host's runCharlyVerb reads).
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
