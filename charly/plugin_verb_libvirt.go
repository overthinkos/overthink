package main

import (
	"context"

	"github.com/overthinkos/overthink/charly/plugin/kit"
)

// libvirtVerb is the BUILT-IN `libvirt` LIVE-CONTAINER verb, extracted into its OWN
// dedicated file (Phase 1, the live-container-verb relocation). Like cdp/vnc, libvirt
// stays a FIRST-CLASS #Op verb: it keeps its dedicated `libvirt:` discriminator and its
// method-specific modifiers (KeyName/Text/Command/Target/Input/Artifact) on the closed
// base #Op — there is NO plugin_input and therefore NO served plugin schema. So it
// self-registers via registerDedicatedBuiltin (the schema-less dedicated-provider path),
// INTENTIONALLY absent from BOTH builtinProviderInstances and the `providers:` manifest,
// yet resolving + dispatching through the SAME providerRegistry (the verb +
// method-allowlist bijection gates still see it). It embeds builtinVerbBase for
// Class()=ClassVerb + the in-proc-only Invoke stub (a live verb carries the *Runner and
// never serves itself over the wire).
//
// `charly check libvirt <method>` uses go-libvirt RPC against a running VM. Host-side;
// only applicable to `vm:<name>` deploys. Nested subgroups (guest/*, snapshot/*) are
// flattened via slash-separated method names so authors write `libvirt: guest/ping` or
// `libvirt: snapshot/list`.
//
// This file owns the verb's complete contract: the provider (Reserved/RunVerb), the
// LiveVerbProvider method contract (Methods/MethodField), the libvirtMethods method
// allowlist, and the runLibvirt dispatcher. The shared kit.MethodSpec type + the kit.PosX
// builder library (kit.PosArtifact/kit.PosKeyNameSplit/kit.PosText/kit.PosLibvirtQmp/
// kit.PosCommandFields/kit.PosTarget) live in charly/plugin/kit/liveverb.go; the
// artifact-validatable set (e.g. libvirt/screenshot) is derived from spec.Artifact. The
// runCharlyVerb dispatcher stays in checkrun_charly_verbs.go.
type libvirtVerb struct{ builtinVerbBase }

func (libvirtVerb) Reserved() string { return "libvirt" }

func (libvirtVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runLibvirt(ctx, op)
}

func (libvirtVerb) Methods() map[string]kit.MethodSpec { return libvirtMethods }
func (libvirtVerb) MethodField(c *Op) string           { return c.Libvirt }

// libvirtMethods is the libvirt verb's method allowlist (the dispatch data runCharlyVerb reads).
var libvirtMethods = map[string]kit.MethodSpec{
	// Top-level verbs
	"list":       {Path: []string{"libvirt", "list"}},
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

func (r *Runner) runLibvirt(ctx context.Context, c *Op) CheckResult {
	return r.runCharlyVerb(ctx, c, "libvirt", c.Libvirt, libvirtMethods)
}

var _ = registerDedicatedBuiltin(libvirtVerb{})
