// Package dbus is the importable, COMPILED-IN host-coupled `dbus` LIVE-CONTAINER verb:
// interact with D-Bus services inside a running deployment (list, call, introspect, notify).
// A SCHEMA-LESS kit.LiveVerbProvider — its modifiers ride the closed base #Op; RunVerb
// delegates dispatch to the host via cc.RunCharlyVerb. Relocated out of charly's module
// (formerly charly/plugin_verb_dbus.go); COMPILED-IN-ONLY. The `charly check dbus` driver
// command stays host-side (cc.RunCharlyVerb self-invokes it).
package dbus

import (
	"context"

	"github.com/overthinkos/overthink/charly/plugin/kit"
	"github.com/overthinkos/overthink/charly/spec"
)

// NewLiveVerb returns the dbus verb as a kit.LiveVerbProvider for compiled-in registration.
func NewLiveVerb() kit.LiveVerbProvider { return verb{} }

type verb struct{}

func (verb) Reserved() string { return "dbus" }

func (verb) RunVerb(ctx context.Context, cc kit.CheckContext, op *spec.Op) kit.Result {
	return cc.RunCharlyVerb(ctx, op, "dbus", op.Dbus, dbusMethods)
}

func (verb) Methods() map[string]kit.MethodSpec { return dbusMethods }
func (verb) MethodField(op *spec.Op) string     { return op.Dbus }

// dbusMethods is the dbus verb's method allowlist (the dispatch data the host's runCharlyVerb reads).
var dbusMethods = map[string]kit.MethodSpec{
	"list":       {Path: []string{"dbus", "list"}},
	"call":       {Path: []string{"dbus", "call"}, Required: []string{"Dest", "Path", "Method"}, PosArgs: kit.PosDbusCall},
	"introspect": {Path: []string{"dbus", "introspect"}, Required: []string{"Dest", "Path"}, PosArgs: kit.PosDbusIntrospect},
	"notify":     {Path: []string{"dbus", "notify"}, Required: []string{"Text"}, PosArgs: kit.PosDbusNotify},
}
