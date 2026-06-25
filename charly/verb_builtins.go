package main

import "context"

// The remaining IN-CHARLY-MODULE built-in check verbs as CheckVerbProviders (summarize,
// kill, plugin — defined below). These are internal/dispatch verbs, NOT user-authored
// plugin: blocks; each wraps its r.runX handler, and runOne resolves them via
// providerRegistry.ResolveVerb.
//
// Every OTHER verb has been relocated to a COMPILED-IN candy (candy/plugin-<name>):
//   - The goss-tier + state-provision verbs (process/port/dns/http/interface/addr/matching/
//     file/user/unix_group/kernel-param/mount/command/package/service) — RegisterBuiltinPluginUnit
//     candies. `package`/`service` are TypedStepProviders (their act lowers to a
//     SystemPackagesStep / ServicePackagedStep with load-bearing reversals via the host's
//     materializeStep — the one piece that stays in package main); file/user/unix_group/
//     kernel-param/mount are ProvisionActors; `command` is the install-task emitCmd branch.
//   - The live-container verbs (cdp/vnc/wl/dbus/mcp/record/libvirt) — kit.LiveVerbProvider
//     candies registered via registerCompiledDedicatedVerb (schema-less; their modifiers stay
//     on the closed base #Op), delegating dispatch to the host's runCharlyVerb via
//     cc.RunCharlyVerb.
// All relocated verbs are absent from builtinProviderInstances + the `providers:` manifest;
// they dispatch identically through providerRegistry. kube/adb/appium/spice are the four
// out-of-process external-charly-verbs (below).

// kube is NOT a built-in verb — it is an EXTERNAL-CHARLY-VERB served out-of-process by
// candy/plugin-kube (the third dep-shed: the client-go + apimachinery stack
// left charly's core go.mod). It keeps its `kube:` discriminator + modifiers on core #Op
// (authoring unchanged) but is NOT a CheckVerbProvider, so it dispatches via
// invokeVerbProvider (the else-branch in runOne) once the loader registers its grpcProvider
// — never through this in-proc set. The host pre-resolves any --cluster profile to a
// concrete kubeconfig context (preresolveKubeCluster) before marshaling; the same plugin's
// clientcmd-backed k3s kubeconfig-merge routes through it via k8s_plugin.go's invokeKubePlugin.

// adb is NOT a built-in verb — it is an EXTERNAL-CHARLY-VERB served out-of-process by
// candy/plugin-adb (the second dep-shed: the goadb ADB-wire dependency left charly's
// core go.mod). It keeps its `adb:` discriminator + modifiers on core #Op (authoring
// unchanged) but is NOT a CheckVerbProvider, so it dispatches via invokeVerbProvider (the
// else-branch in runOne) once the loader registers its grpcProvider — never through this
// in-proc set. Its goadb-backed deploy/status device ops also route through the SAME
// provider via android_plugin.go's invokeAdbPlugin (out-of-core goadb).

// appium is NOT a built-in verb — it is an EXTERNAL-CHARLY-VERB served out-of-process by
// candy/plugin-appium (the first dep-shed: tebeka/selenium left charly's core go.mod). It
// keeps its `appium:` discriminator + modifiers on core #Op (authoring unchanged) but is
// NOT a CheckVerbProvider, so it dispatches via invokeVerbProvider (the else-branch in
// runOne) once the loader registers its grpcProvider — never through this in-proc set.

// spice is NOT a built-in verb — it is an EXTERNAL-CHARLY-VERB served out-of-process by
// candy/plugin-spice (the fourth dep-shed: the upstream SPICE wire client library + its
// cgo opus/portaudio audio transitives left charly's core go.mod). It keeps its `spice:`
// discriminator + modifiers on core #Op (authoring unchanged) but is NOT a
// CheckVerbProvider, so it dispatches via invokeVerbProvider (the else-branch in runOne)
// once the loader registers its grpcProvider — never through this in-proc set. The host
// pre-resolves the VM's live SPICE endpoint to a dialable address (preresolveSpiceEndpoint)
// before marshaling, so the plugin needs no go-libvirt.

type summarizeVerb struct{ builtinVerbBase }

func (summarizeVerb) Reserved() string { return "summarize" }
func (summarizeVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runSummarize(ctx, op)
}

type killVerb struct{ builtinVerbBase }

func (killVerb) Reserved() string { return "kill" }
func (killVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runKill(ctx, op)
}

// pluginVerb — the generic `plugin:` discriminator. Its RunVerb resolves the
// authored plugin word (op.Plugin) to its registered Provider and Invokes it
// (the out-of-proc / built-in plugin verb). See runPluginVerb.
type pluginVerb struct{ builtinVerbBase }

func (pluginVerb) Reserved() string { return "plugin" }
func (pluginVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runPluginVerb(ctx, op)
}
