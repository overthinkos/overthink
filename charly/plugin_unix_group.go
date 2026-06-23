package main

import (
	"context"
	"fmt"

	"github.com/overthinkos/overthink/charly/plugin/builtins/unix_group"
	"github.com/overthinkos/overthink/charly/plugin/builtins/unix_group/params"
)

// unixGroupVerb is the BUILT-IN `unix_group` plugin: it provides the `unix_group` verb,
// the FIRST extracted STATE-PROVISION verb. It is DUAL-NATURED:
//
//   - CheckVerbProvider (do:assert) — RunVerb dispatches the getent-group probe IN-PROCESS
//     via the live *Runner (r.Exec), which cannot cross the wire. Authored as
//     `check: … / plugin: unix_group / plugin_input: {unix_group, gid}`, dispatched via
//     runPluginVerb after the host validates plugin_input against the served #UnixGroupInput.
//   - ProvisionActor (do:act) — RenderProvisionScript renders the groupadd. It is reached
//     at install COMPILE+EMIT (a `run: {plugin: unix_group}` step → emitTasks' `case
//     "plugin"` for the box/OCI build, renderOpCommand for the local/vm deploy, both via
//     resolveProvisionScript — the act-emit enabler) AND at runtime act (runProvisionAct).
//     Authored as `run: … / plugin: unix_group / plugin_input: {…}`.
//
// The verb left the closed #Op/spec.OpVerbs; `gid` STAYS in #Op (the `user` verb still
// reads it) and is reproduced standalone in #UnixGroupInput. Both halves decode the typed
// plugin_input (params.UnixGroupInput, generated from the unit's schema/unix_group.cue) —
// never a hand-parsed map, never the removed Op.UnixGroup/Op.GID fields.
//
// It embeds builtinVerbBase, which supplies Class()=ClassVerb and the in-proc-only Invoke
// stub (an in-proc verb never serves itself over the wire — Invoke errors loudly rather
// than silently dropping the *Runner).
type unixGroupVerb struct{ builtinVerbBase }

func (unixGroupVerb) Reserved() string { return "unix_group" }

// RunVerb (the do:assert half) decodes plugin_input and runs the getent-group probe via
// the live *Runner; the impl stays in r.runUnixGroup (checkrun_verbs.go).
func (unixGroupVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	var in params.UnixGroupInput
	decodePluginInput(op.PluginInput, &in)
	return r.runUnixGroup(ctx, op, in.UnixGroup, in.GID)
}

// RenderProvisionScript (the do:act half) decodes plugin_input and renders the idempotent
// groupadd. ok is always true — a unix_group act always has a create form. distros are
// unused (the group name + gid are distro-agnostic).
func (unixGroupVerb) RenderProvisionScript(op *Op, _ []string) (string, bool) {
	var in params.UnixGroupInput
	decodePluginInput(op.PluginInput, &in)
	flags := ""
	if in.GID != nil {
		flags += fmt.Sprintf(" -g %d", *in.GID)
	}
	name := shellSingleQuote(in.UnixGroup)
	return fmt.Sprintf("getent group %[1]s >/dev/null 2>&1 || groupadd%[2]s %[1]s", name, flags), true
}

func init() {
	RegisterBuiltinPluginUnit(PluginUnit{
		Providers: []Provider{unixGroupVerb{}},
		Schema:    PluginSchema{CueSource: unix_group.Schema(), InputDefs: unix_group.InputDefs},
	})
}
