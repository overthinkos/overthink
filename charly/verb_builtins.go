package main

import "context"

// The built-in check verbs as CheckVerbProviders. Each wraps its existing
// r.runX handler unchanged — the migration is behavior-preserving; only the
// runOne dispatch switch is replaced by providerRegistry.ResolveVerb. The
// live-container verbs (cdp/wl/…) still funnel through runCharlyVerb + the
// method-allowlist maps (checkrun_charly_verbs.go) inside their handler.
//
// The do-mode (act) half of the state-provision verbs is a ProvisionActor method
// per provider (checkrun_act.go) — runProvisionAct resolves + type-asserts it (C1b).

type fileVerb struct{ builtinVerbBase }

func (fileVerb) Reserved() string { return "file" }
func (fileVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runFile(ctx, op)
}

type commandVerb struct{ builtinVerbBase }

func (commandVerb) Reserved() string { return "command" }
func (commandVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runCommand(ctx, op)
}

type packageVerb struct{ builtinVerbBase }

func (packageVerb) Reserved() string { return "package" }
func (packageVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runPackage(ctx, op)
}

type serviceVerb struct{ builtinVerbBase }

func (serviceVerb) Reserved() string { return "service" }
func (serviceVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runService(ctx, op)
}

type userVerb struct{ builtinVerbBase }

func (userVerb) Reserved() string { return "user" }
func (userVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runUser(ctx, op)
}

// unix_group is NOT here — it is the FIRST extracted STATE-PROVISION verb, a dedicated
// plugin UNIT (plugin_unix_group.go) that self-registers via RegisterBuiltinPluginUnit
// (absent from both builtinProviderInstances and the providers: manifest). Its provider
// is BOTH a CheckVerbProvider (getent-group probe) AND a ProvisionActor (groupadd at
// install emit + runtime act).

type kernelParamVerb struct{ builtinVerbBase }

func (kernelParamVerb) Reserved() string { return "kernel-param" }
func (kernelParamVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runKernelParam(ctx, op)
}

type mountVerb struct{ builtinVerbBase }

func (mountVerb) Reserved() string { return "mount" }
func (mountVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runMount(ctx, op)
}

type cdpVerb struct{ builtinVerbBase }

func (cdpVerb) Reserved() string { return "cdp" }
func (cdpVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runCdp(ctx, op)
}

type wlVerb struct{ builtinVerbBase }

func (wlVerb) Reserved() string { return "wl" }
func (wlVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runWl(ctx, op)
}

type dbusVerb struct{ builtinVerbBase }

func (dbusVerb) Reserved() string { return "dbus" }
func (dbusVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runDbus(ctx, op)
}

type vncVerb struct{ builtinVerbBase }

func (vncVerb) Reserved() string { return "vnc" }
func (vncVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runVnc(ctx, op)
}

type mcpVerb struct{ builtinVerbBase }

func (mcpVerb) Reserved() string { return "mcp" }
func (mcpVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runMcp(ctx, op)
}

type recordVerb struct{ builtinVerbBase }

func (recordVerb) Reserved() string { return "record" }
func (recordVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runRecord(ctx, op)
}

type spiceVerb struct{ builtinVerbBase }

func (spiceVerb) Reserved() string { return "spice" }
func (spiceVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runSpice(ctx, op)
}

type libvirtVerb struct{ builtinVerbBase }

func (libvirtVerb) Reserved() string { return "libvirt" }
func (libvirtVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runLibvirt(ctx, op)
}

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
