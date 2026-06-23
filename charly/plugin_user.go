package main

import (
	"context"
	"fmt"

	userplugin "github.com/overthinkos/overthink/charly/plugin/builtins/user"
	"github.com/overthinkos/overthink/charly/plugin/builtins/user/params"
)

// userVerb is the BUILT-IN `user` plugin: it provides the `user` verb, an extracted
// STATE-PROVISION verb. It is DUAL-NATURED:
//
//   - CheckVerbProvider (do:assert) — RunVerb dispatches the getent-passwd probe
//     IN-PROCESS via the live *Runner (r.Exec), which cannot cross the wire. Authored as
//     `check: … / plugin: user / plugin_input: {user, uid, gid, home, shell}`, dispatched
//     via runPluginVerb after the host validates plugin_input against the served #UserInput.
//   - ProvisionActor (do:act) — RenderProvisionScript renders the useradd. It is reached
//     at install COMPILE+EMIT (a `run: {plugin: user}` step → emitTasks' `case "plugin"`
//     for the box/OCI build, renderOpCommand for the local/vm deploy, both via
//     resolveProvisionScript — the act-emit enabler) AND at runtime act (runProvisionAct).
//
// The verb left the closed #Op/spec.OpVerbs; `uid`/`gid`/`home`/`shell` (read ONLY by the
// `user` verb) MOVED out of #Op into #UserInput. Both halves decode the typed plugin_input
// (params.UserInput, generated from the unit's schema/user.cue) — never a hand-parsed map,
// never the removed Op.User/Op.UID/Op.GID/Op.Home/Op.Shell fields.
//
// It embeds builtinVerbBase, which supplies Class()=ClassVerb and the in-proc-only Invoke
// stub (an in-proc verb never serves itself over the wire — Invoke errors loudly rather
// than silently dropping the *Runner).
type userVerb struct{ builtinVerbBase }

func (userVerb) Reserved() string { return "user" }

// RunVerb (the do:assert half) decodes plugin_input and runs the getent-passwd probe via
// the live *Runner; the impl stays in r.runUser (checkrun_verbs.go).
func (userVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	var in params.UserInput
	decodePluginInput(op.PluginInput, &in)
	return r.runUser(ctx, op, in.User, in.UID, in.GID, in.Home, in.Shell)
}

// RenderProvisionScript (the do:act half) decodes plugin_input and renders the idempotent
// useradd. ok is always true — a user act always has a create form. distros are unused
// (the account fields are distro-agnostic). gid is decoded for the assert; the act form
// does not set a primary group (matching the pre-extraction behaviour).
func (userVerb) RenderProvisionScript(op *Op, _ []string) (string, bool) {
	var in params.UserInput
	decodePluginInput(op.PluginInput, &in)
	flags := ""
	if in.UID != nil {
		flags += fmt.Sprintf(" -u %d", *in.UID)
	}
	if in.Home != "" {
		flags += " -m -d " + shellSingleQuote(in.Home)
	}
	if in.Shell != "" {
		flags += " -s " + shellSingleQuote(in.Shell)
	}
	name := shellSingleQuote(in.User)
	return fmt.Sprintf("id %[1]s >/dev/null 2>&1 || useradd%[2]s %[1]s", name, flags), true
}

func init() {
	RegisterBuiltinPluginUnit(PluginUnit{
		Providers: []Provider{userVerb{}},
		Schema:    PluginSchema{CueSource: userplugin.Schema(), InputDefs: userplugin.InputDefs},
	})
}
