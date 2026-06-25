package main

import "context"

// The built-in check verbs as CheckVerbProviders. Each wraps its existing
// r.runX handler unchanged — the migration is behavior-preserving; only the
// runOne dispatch switch is replaced by providerRegistry.ResolveVerb. NO
// live-container verb remains here — the dep-shedders kube/adb/appium have all
// been extracted as external-charly-verbs.
//
// cdp/vnc/wl/dbus/mcp/record/libvirt are NOT here — each is a live-container verb
// extracted into its OWN dedicated file (plugin_verb_<verb>.go) carrying its provider +
// LiveVerbProvider method contract + its <verb>Methods map + its runX dispatcher,
// self-registering via registerDedicatedBuiltin (the schema-less dedicated-provider
// path — no plugin_input, no served schema, since their modifiers stay on the closed
// base #Op), absent from both builtinProviderInstances and the `providers:` manifest.
// They dispatch identically through providerRegistry. (kube/adb/appium/spice are
// extracted as external-charly-verbs.)
//
// The do-mode (act) half of the state-provision verbs is a ProvisionActor method
// per provider (checkrun_act.go) — runProvisionAct resolves + type-asserts it (C1b).

// file / package / command / service / user / unix_group / kernel-param / mount are NOT
// here — each is an extracted verb, a dedicated plugin UNIT that self-registers via
// RegisterBuiltinPluginUnit (absent from both builtinProviderInstances and the providers:
// manifest). command/package/service remain in-charly-module (plugin_verb_command.go /
// plugin_verb_package.go / plugin_verb_service.go); file/user/unix_group/kernel-param/mount
// relocated to compiled-in candies (candy/plugin-file, candy/plugin-user,
// candy/plugin-unix-group, candy/plugin-kernel-param, candy/plugin-mount).
// file/user/unix_group/kernel-param/mount are BOTH a CheckVerbProvider AND a
// ProvisionActor (touch+chmod / useradd / groupadd / sysctl-write / mount at install emit +
// runtime act). `package` and `service` are the TYPED-STEP verbs — each a CheckVerbProvider
// AND a TypedStepProvider (its act lowers to a SystemPackagesStep / ServicePackagedStep with
// load-bearing reversals, NOT a RenderProvisionScript) AND a ProvisionActor (the
// runtime/box-build live-act path); `package` is the simpler one (no PriorEnabled state).
// `command` is a CheckVerbProvider ONLY — its act is the dedicated install-task emitCmd
// branch (`plugin == "command"` in emitTasks/renderOpCommand), NOT a ProvisionActor.

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
