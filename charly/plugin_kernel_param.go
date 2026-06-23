package main

import (
	"context"
	"fmt"

	kernelparamplugin "github.com/overthinkos/overthink/charly/plugin/builtins/kernel_param"
	"github.com/overthinkos/overthink/charly/plugin/builtins/kernel_param/params"
)

// kernelParamVerb is the BUILT-IN `kernel-param` plugin: it provides the `kernel-param`
// verb, an extracted STATE-PROVISION verb. It is DUAL-NATURED:
//
//   - CheckVerbProvider (do:assert) — RunVerb dispatches the /proc/sys read probe
//     IN-PROCESS via the live *Runner (r.Exec), which cannot cross the wire. The CHECK
//     reads /proc/sys/<key-as-slashes> directly (equivalent to `sysctl -n` but needing no
//     procps-ng, which minimal images omit). Authored as `check: … / plugin: kernel-param /
//     plugin_input: {kernel-param, value}`, dispatched via runPluginVerb after the host
//     validates plugin_input against #KernelParamInput.
//   - ProvisionActor (do:act) — RenderProvisionScript renders the `sysctl -w` (the act runs
//     in a deploy/runtime provisioning context where procps-ng is present). It is
//     reached at install COMPILE+EMIT (a `run: {plugin: kernel-param}` step → emitTasks'
//     `case "plugin"` for the box/OCI build, renderOpCommand for the local/vm deploy, both
//     via resolveProvisionScript — the act-emit enabler) AND at runtime act (runProvisionAct).
//
// The verb left the closed #Op/spec.OpVerbs; `value` (read ONLY by the `kernel-param`
// verb) MOVED out of #Op into #KernelParamInput. The verb word stays `kernel-param`
// (the existing wire key — a clean extraction, NOT a rename); the Go package is
// `kernel_param` only because a hyphen is not a legal Go package name. Both halves decode
// the typed plugin_input (params.KernelParamInput, generated from the unit's
// schema/kernel_param.cue); the `value` matcher disjunction gengotypes degrades to `any`,
// so it is re-decoded through the SHARED matcher codec (decodeMatcherList — R3).
//
// It embeds builtinVerbBase, which supplies Class()=ClassVerb and the in-proc-only Invoke
// stub (an in-proc verb never serves itself over the wire — Invoke errors loudly rather
// than silently dropping the *Runner).
type kernelParamVerb struct{ builtinVerbBase }

func (kernelParamVerb) Reserved() string { return "kernel-param" }

// RunVerb (the do:assert half) decodes plugin_input and runs the /proc/sys read probe via
// the live *Runner; the impl stays in r.runKernelParam (checkrun_verbs.go).
func (kernelParamVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	var in params.KernelParamInput
	decodePluginInput(op.PluginInput, &in)
	return r.runKernelParam(ctx, op, in.KernelParam, decodeMatcherList(in.Value))
}

// RenderProvisionScript (the do:act half) decodes plugin_input and renders the `sysctl -w`.
// ok=false when no desired value is given (a sysctl write with no value is meaningless).
func (kernelParamVerb) RenderProvisionScript(op *Op, _ []string) (string, bool) {
	var in params.KernelParamInput
	decodePluginInput(op.PluginInput, &in)
	if v, ok := firstMatcherScalar(decodeMatcherList(in.Value)); ok {
		return fmt.Sprintf("sysctl -w %s=%s", shellSingleQuote(in.KernelParam), shellSingleQuote(v)), true
	}
	return "", false // act with no desired value is meaningless
}

func init() {
	RegisterBuiltinPluginUnit(PluginUnit{
		Providers: []Provider{kernelParamVerb{}},
		Schema:    PluginSchema{CueSource: kernelparamplugin.Schema(), InputDefs: kernelparamplugin.InputDefs},
	})
}
