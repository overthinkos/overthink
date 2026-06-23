package main

import (
	"context"
	"fmt"
	"strings"

	mountplugin "github.com/overthinkos/overthink/charly/plugin/builtins/mount"
	"github.com/overthinkos/overthink/charly/plugin/builtins/mount/params"
)

// mountVerb is the BUILT-IN `mount` plugin: it provides the `mount` verb, an extracted
// STATE-PROVISION verb. It is DUAL-NATURED:
//
//   - CheckVerbProvider (do:assert) — RunVerb dispatches the findmnt probe IN-PROCESS via
//     the live *Runner (r.Exec), which cannot cross the wire. Authored as
//     `check: … / plugin: mount / plugin_input: {mount, mount_source, filesystem, opt}`,
//     dispatched via runPluginVerb after the host validates plugin_input against #MountInput.
//   - ProvisionActor (do:act) — RenderProvisionScript renders the mount. It is reached at
//     install COMPILE+EMIT (a `run: {plugin: mount}` step → emitTasks' `case "plugin"` for
//     the box/OCI build, renderOpCommand for the local/vm deploy, both via
//     resolveProvisionScript — the act-emit enabler) AND at runtime act (runProvisionAct).
//
// The verb left the closed #Op/spec.OpVerbs; `mount_source`/`filesystem`/`opt` (read ONLY
// by the `mount` verb) MOVED out of #Op into #MountInput. Both halves decode the typed
// plugin_input (params.MountInput, generated from the unit's schema/mount.cue); the `opt`
// matcher disjunction gengotypes degrades to `any`, so it is re-decoded through the SHARED
// matcher codec (decodeMatcherList — R3, mirrors plugin_http.go).
//
// It embeds builtinVerbBase, which supplies Class()=ClassVerb and the in-proc-only Invoke
// stub (an in-proc verb never serves itself over the wire — Invoke errors loudly rather
// than silently dropping the *Runner).
type mountVerb struct{ builtinVerbBase }

func (mountVerb) Reserved() string { return "mount" }

// RunVerb (the do:assert half) decodes plugin_input and runs the findmnt probe via the
// live *Runner; the impl stays in r.runMount (checkrun_verbs.go).
func (mountVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	var in params.MountInput
	decodePluginInput(op.PluginInput, &in)
	return r.runMount(ctx, op, in.Mount, in.MountSource, in.Filesystem, decodeMatcherList(in.Opts))
}

// RenderProvisionScript (the do:act half) decodes plugin_input and renders the idempotent
// mount. ok=false when no mount_source is given (nothing to mount).
func (mountVerb) RenderProvisionScript(op *Op, _ []string) (string, bool) {
	var in params.MountInput
	decodePluginInput(op.PluginInput, &in)
	var args []string
	if in.Filesystem != "" {
		args = append(args, "-t "+shellSingleQuote(in.Filesystem))
	}
	if v, ok := firstMatcherScalar(decodeMatcherList(in.Opts)); ok && v != "" {
		args = append(args, "-o "+shellSingleQuote(v))
	}
	if in.MountSource == "" {
		return "", false // need a source to mount
	}
	return fmt.Sprintf("findmnt %[1]s >/dev/null 2>&1 || mount %[2]s %[3]s %[1]s",
		shellSingleQuote(in.Mount), strings.Join(args, " "), shellSingleQuote(in.MountSource)), true
}

func init() {
	RegisterBuiltinPluginUnit(PluginUnit{
		Providers: []Provider{mountVerb{}},
		Schema:    PluginSchema{CueSource: mountplugin.Schema(), InputDefs: mountplugin.InputDefs},
	})
}
