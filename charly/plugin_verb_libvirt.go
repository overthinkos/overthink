package main

import "context"

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
// allowlist, and the runLibvirt dispatcher. The shared posArgs builder library
// (posArtifact/posKeyNameSplit/posText/posLibvirtQmp/posCommandFields/posTarget), the
// methodSpec type, and the artifactValidatableMethods allowlist (libvirt/screenshot)
// stay in checkrun_charly_verbs.go.
type libvirtVerb struct{ builtinVerbBase }

func (libvirtVerb) Reserved() string { return "libvirt" }

func (libvirtVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runLibvirt(ctx, op)
}

func (libvirtVerb) Methods() map[string]methodSpec { return libvirtMethods }
func (libvirtVerb) MethodField(c *Op) string       { return c.Libvirt }

// libvirtMethods is the libvirt verb's method allowlist (the dispatch data runCharlyVerb reads).
var libvirtMethods = map[string]methodSpec{
	// Top-level verbs
	"list":       {path: []string{"libvirt", "list"}},
	"info":       {path: []string{"libvirt", "info"}},
	"screenshot": {path: []string{"libvirt", "screenshot"}, posArgs: posArtifact, artifact: true},
	"send-key":   {path: []string{"libvirt", "send-key"}, required: []string{"KeyName"}, posArgs: posKeyNameSplit},
	"passwd":     {path: []string{"libvirt", "passwd"}, required: []string{"Text"}, posArgs: posText},
	// qmp takes a QMP method name + optional JSON args. Text holds the
	// method name (Command would collide with the command: verb).
	"qmp":        {path: []string{"libvirt", "qmp"}, required: []string{"Text"}, posArgs: posLibvirtQmp},
	"domain-xml": {path: []string{"libvirt", "domain-xml"}},
	"console":    {path: []string{"libvirt", "console"}},
	"events":     {path: []string{"libvirt", "events"}},

	// qemu-guest-agent subgroup
	"guest/ping":       {path: []string{"libvirt", "guest", "ping"}},
	"guest/info":       {path: []string{"libvirt", "guest", "info"}},
	"guest/os-info":    {path: []string{"libvirt", "guest", "os-info"}},
	"guest/time":       {path: []string{"libvirt", "guest", "time"}},
	"guest/hostname":   {path: []string{"libvirt", "guest", "hostname"}},
	"guest/users":      {path: []string{"libvirt", "guest", "users"}},
	"guest/interfaces": {path: []string{"libvirt", "guest", "interfaces"}},
	"guest/disks":      {path: []string{"libvirt", "guest", "disks"}},
	"guest/fsinfo":     {path: []string{"libvirt", "guest", "fsinfo"}},
	"guest/vcpus":      {path: []string{"libvirt", "guest", "vcpus"}},
	// guest/exec runs a command via qemu-guest-agent inside the VM. Reuses the
	// `command:` field as a sub-modifier (verbsSet treats it as a modifier when
	// libvirt: is set). The string is split on whitespace into argv (no shell
	// metacharacter handling — guest-exec wants a real argv list).
	"guest/exec":   {path: []string{"libvirt", "guest", "exec"}, required: []string{"Command"}, posArgs: posCommandFields},
	"guest/fstrim": {path: []string{"libvirt", "guest", "fstrim"}},

	// Snapshot subgroup — Target holds the snapshot name.
	"snapshot/list":   {path: []string{"libvirt", "snapshot", "list"}},
	"snapshot/create": {path: []string{"libvirt", "snapshot", "create"}, required: []string{"Target"}, posArgs: posTarget},
	"snapshot/info":   {path: []string{"libvirt", "snapshot", "info"}, required: []string{"Target"}, posArgs: posTarget},
	"snapshot/revert": {path: []string{"libvirt", "snapshot", "revert"}, required: []string{"Target"}, posArgs: posTarget},
	"snapshot/delete": {path: []string{"libvirt", "snapshot", "delete"}, required: []string{"Target"}, posArgs: posTarget},
}

func (r *Runner) runLibvirt(ctx context.Context, c *Op) CheckResult {
	return r.runCharlyVerb(ctx, c, "libvirt", c.Libvirt, libvirtMethods)
}

var _ = registerDedicatedBuiltin(libvirtVerb{})
