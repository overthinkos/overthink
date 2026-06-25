// Package libvirt is the importable, COMPILED-IN host-coupled `libvirt` LIVE-CONTAINER
// verb: VM management via the libvirt API on a live deployment (info, screenshot, send-key,
// QMP, qemu-guest-agent, snapshots, events). A SCHEMA-LESS kit.LiveVerbProvider — its
// modifiers ride the closed base #Op; RunVerb delegates dispatch to the host via
// cc.RunCharlyVerb. Relocated out of charly's module (formerly charly/plugin_verb_libvirt.go);
// COMPILED-IN-ONLY. The `charly check libvirt` driver command (the go-libvirt-backed impl)
// stays host-side for now (cc.RunCharlyVerb self-invokes it); relocating the driver — which
// sheds the go-libvirt dep from charly's binary — is a follow-on step.
package libvirt

import (
	"context"

	"github.com/overthinkos/overthink/charly/plugin/kit"
	"github.com/overthinkos/overthink/charly/spec"
)

// NewLiveVerb returns the libvirt verb as a kit.LiveVerbProvider for compiled-in registration.
func NewLiveVerb() kit.LiveVerbProvider { return verb{} }

type verb struct{}

func (verb) Reserved() string { return "libvirt" }

func (verb) RunVerb(ctx context.Context, cc kit.CheckContext, op *spec.Op) kit.Result {
	return cc.RunCharlyVerb(ctx, op, "libvirt", op.Libvirt, libvirtMethods)
}

func (verb) Methods() map[string]kit.MethodSpec { return libvirtMethods }
func (verb) MethodField(op *spec.Op) string     { return op.Libvirt }

// libvirtMethods is the libvirt verb's method allowlist (the dispatch data the host's runCharlyVerb reads).
var libvirtMethods = map[string]kit.MethodSpec{
	// Top-level verbs
	"list":       {Path: []string{"libvirt", "list"}, SkipBox: true},
	"info":       {Path: []string{"libvirt", "info"}},
	"screenshot": {Path: []string{"libvirt", "screenshot"}, PosArgs: kit.PosArtifact, Artifact: true},
	"send-key":   {Path: []string{"libvirt", "send-key"}, Required: []string{"KeyName"}, PosArgs: kit.PosKeyNameSplit},
	"passwd":     {Path: []string{"libvirt", "passwd"}, Required: []string{"Text"}, PosArgs: kit.PosText},
	// qmp takes a QMP method name + optional JSON args. Text holds the
	// method name (Command would collide with the command: verb).
	"qmp":        {Path: []string{"libvirt", "qmp"}, Required: []string{"Text"}, PosArgs: kit.PosLibvirtQmp},
	"domain-xml": {Path: []string{"libvirt", "domain-xml"}},
	"console":    {Path: []string{"libvirt", "console"}},
	"events":     {Path: []string{"libvirt", "events"}},

	// qemu-guest-agent subgroup
	"guest/ping":       {Path: []string{"libvirt", "guest", "ping"}},
	"guest/info":       {Path: []string{"libvirt", "guest", "info"}},
	"guest/os-info":    {Path: []string{"libvirt", "guest", "os-info"}},
	"guest/time":       {Path: []string{"libvirt", "guest", "time"}},
	"guest/hostname":   {Path: []string{"libvirt", "guest", "hostname"}},
	"guest/users":      {Path: []string{"libvirt", "guest", "users"}},
	"guest/interfaces": {Path: []string{"libvirt", "guest", "interfaces"}},
	"guest/disks":      {Path: []string{"libvirt", "guest", "disks"}},
	"guest/fsinfo":     {Path: []string{"libvirt", "guest", "fsinfo"}},
	"guest/vcpus":      {Path: []string{"libvirt", "guest", "vcpus"}},
	// guest/exec runs a command via qemu-guest-agent inside the VM. Reuses the
	// `command:` field as a sub-modifier (verbsSet treats it as a modifier when
	// libvirt: is set). The string is split on whitespace into argv (no shell
	// metacharacter handling — guest-exec wants a real argv list).
	"guest/exec":   {Path: []string{"libvirt", "guest", "exec"}, Required: []string{"Command"}, PosArgs: kit.PosCommandFields},
	"guest/fstrim": {Path: []string{"libvirt", "guest", "fstrim"}},

	// Snapshot subgroup — Target holds the snapshot name.
	"snapshot/list":   {Path: []string{"libvirt", "snapshot", "list"}},
	"snapshot/create": {Path: []string{"libvirt", "snapshot", "create"}, Required: []string{"Target"}, PosArgs: kit.PosTarget},
	"snapshot/info":   {Path: []string{"libvirt", "snapshot", "info"}, Required: []string{"Target"}, PosArgs: kit.PosTarget},
	"snapshot/revert": {Path: []string{"libvirt", "snapshot", "revert"}, Required: []string{"Target"}, PosArgs: kit.PosTarget},
	"snapshot/delete": {Path: []string{"libvirt", "snapshot", "delete"}, Required: []string{"Target"}, PosArgs: kit.PosTarget},
}
