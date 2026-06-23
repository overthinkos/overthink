package main

import "context"

// The built-in check verbs as CheckVerbProviders. Each wraps its existing
// r.runX handler unchanged — the migration is behavior-preserving; only the
// runOne dispatch switch is replaced by providerRegistry.ResolveVerb. The
// live-container verbs remaining here (kube/adb/appium) still funnel through
// runCharlyVerb + the method-allowlist maps (checkrun_charly_verbs.go) inside
// their handler.
//
// cdp/vnc/wl/dbus/mcp/record/spice/libvirt are NOT here — each is a live-container verb
// extracted into its OWN dedicated file (plugin_verb_<verb>.go) carrying its provider +
// LiveVerbProvider method contract + its <verb>Methods map + its runX dispatcher,
// self-registering via registerDedicatedBuiltin (the schema-less dedicated-provider
// path — no plugin_input, no served schema, since their modifiers stay on the closed
// base #Op), absent from both builtinProviderInstances and the `providers:` manifest.
// They dispatch identically through providerRegistry. (The dep-shedders adb/appium/kube
// stay here until their later, dep-shedding extraction.)
//
// The do-mode (act) half of the state-provision verbs is a ProvisionActor method
// per provider (checkrun_act.go) — runProvisionAct resolves + type-asserts it (C1b).

// file / package / command / service / user / unix_group / kernel-param / mount are NOT
// here — each is an extracted verb, a dedicated plugin UNIT (plugin_verb_file.go /
// plugin_verb_package.go / plugin_verb_command.go / plugin_verb_service.go / plugin_user.go /
// plugin_unix_group.go / plugin_kernel_param.go / plugin_mount.go) that self-registers via
// RegisterBuiltinPluginUnit (absent from both builtinProviderInstances and the providers:
// manifest). file/user/unix_group/kernel-param/mount are BOTH a CheckVerbProvider AND a
// ProvisionActor (touch+chmod / useradd / groupadd / sysctl-write / mount at install emit +
// runtime act). `package` and `service` are the TYPED-STEP verbs — each a CheckVerbProvider
// AND a TypedStepProvider (its act lowers to a SystemPackagesStep / ServicePackagedStep with
// load-bearing reversals, NOT a RenderProvisionScript) AND a ProvisionActor (the
// runtime/box-build live-act path); `package` is the simpler one (no PriorEnabled state).
// `command` is a CheckVerbProvider ONLY — its act is the dedicated install-task emitCmd
// branch (`plugin == "command"` in emitTasks/renderOpCommand), NOT a ProvisionActor.

type kubeVerb struct{ builtinVerbBase }

func (kubeVerb) Reserved() string { return "kube" }
func (kubeVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runKube(ctx, op)
}

type adbVerb struct{ builtinVerbBase }

func (adbVerb) Reserved() string { return "adb" }
func (adbVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runAdb(ctx, op)
}

type appiumVerb struct{ builtinVerbBase }

func (appiumVerb) Reserved() string { return "appium" }
func (appiumVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runAppium(ctx, op)
}

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
