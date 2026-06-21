package main

import (
	"context"

	"github.com/overthinkos/overthink/charly/spec"
)

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

type portVerb struct{ builtinVerbBase }

func (portVerb) Reserved() string { return "port" }
func (portVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runPort(ctx, op)
}

type commandVerb struct{ builtinVerbBase }

func (commandVerb) Reserved() string { return "command" }
func (commandVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runCommand(ctx, op)
}

type httpVerb struct{ builtinVerbBase }

func (httpVerb) Reserved() string { return "http" }
func (httpVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runHTTP(ctx, op)
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

type processVerb struct{ builtinVerbBase }

func (processVerb) Reserved() string { return "process" }
func (processVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runProcess(ctx, op)
}

type dnsVerb struct{ builtinVerbBase }

func (dnsVerb) Reserved() string { return "dns" }
func (dnsVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runDNS(ctx, op)
}

type userVerb struct{ builtinVerbBase }

func (userVerb) Reserved() string { return "user" }
func (userVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runUser(ctx, op)
}

type unixGroupVerb struct{ builtinVerbBase }

func (unixGroupVerb) Reserved() string { return "unix_group" }
func (unixGroupVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runUnixGroup(ctx, op)
}

type interfaceVerb struct{ builtinVerbBase }

func (interfaceVerb) Reserved() string { return "interface" }
func (interfaceVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runInterface(ctx, op)
}

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

type addrVerb struct{ builtinVerbBase }

func (addrVerb) Reserved() string { return "addr" }
func (addrVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runAddr(ctx, op)
}

type matchingVerb struct{ builtinVerbBase }

func (matchingVerb) Reserved() string { return "matching" }
func (matchingVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runMatching(ctx, op)
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

func init() {
	for _, p := range []CheckVerbProvider{
		fileVerb{}, portVerb{}, commandVerb{}, httpVerb{}, packageVerb{}, serviceVerb{},
		processVerb{}, dnsVerb{}, userVerb{}, unixGroupVerb{}, interfaceVerb{}, kernelParamVerb{},
		mountVerb{}, addrVerb{}, matchingVerb{}, cdpVerb{}, wlVerb{}, dbusVerb{}, vncVerb{},
		mcpVerb{}, recordVerb{}, spiceVerb{}, libvirtVerb{}, kubeVerb{}, adbVerb{}, appiumVerb{},
		summarizeVerb{}, killVerb{}, pluginVerb{},
	} {
		RegisterBuiltinProvider(p)
	}
	// Same-init() gate (after registration) so it can't race the alphabetical
	// init order: every CUE-declared verb has an in-proc CheckVerbProvider.
	if err := checkVerbProviderBijection(spec.OpVerbs); err != nil {
		panic(err)
	}
}
