package main

import (
	"context"

	commandplugin "github.com/overthinkos/overthink/charly/plugin/builtins/command"
	"github.com/overthinkos/overthink/charly/plugin/builtins/command/params"
)

// commandVerb is the BUILT-IN `command` plugin: it provides the `command` check verb
// (run a shell command in-container/host and assert exit/stdout/stderr) as a
// CheckVerbProvider, so runPluginVerb dispatches it IN-PROCESS via RunVerb — keeping
// the live *Runner (r.Exec / r.Mode / r.Scenario) the exec probe needs and that cannot
// cross the wire. The verb left the closed #OpVerb/spec.OpVerbs and is now authored as
// `plugin: command` + `plugin_input: {command, in_container, background, from_host}`,
// dispatched through the provider registry, after the host has validated its
// plugin_input against the unit's served schema (charly/plugin/builtins/command).
//
// `command` is the HARDEST extracted verb: unlike the pure-check process/dns plugins,
// its `run:` step is a REAL install-task (a Containerfile RUN at build, a shell body at
// deploy), so it ALSO carries an act path — but that act path is the dedicated
// `plugin == "command"` branch in emitTasks/renderOpCommand/opActsInBuildDeploy that
// PRESERVES emitCmd, NOT a ProvisionActor. commandVerb is therefore a CheckVerbProvider
// ONLY (it does NOT implement ProvisionActor; resolveProvisionScript never resolves it).
//
// The command-EXCLUSIVE fields (command/in_container/background/from_host) ride
// plugin_input (decoded into params.CommandInput); the SHARED matchers
// exit_status/stdout/stderr (also asserted by the live cdp/wl/… verbs via matchAll) and
// the general timeout stay base #Op fields, so r.runCommand reads them off the step Op
// directly (see commandCheck). Mirrors the http extraction (matchers from the Op).
//
// It embeds builtinVerbBase, which supplies Class()=ClassVerb and the in-proc-only
// Invoke stub (an in-proc verb never serves itself over the wire — Invoke errors
// loudly rather than silently dropping the *Runner).
type commandVerb struct{ builtinVerbBase }

func (commandVerb) Reserved() string { return "command" }

// RunVerb decodes the typed plugin_input (params.CommandInput, generated from the
// unit's schema/command.cue) — never a hand-parsed map — and runs the command via the
// live *Runner. The exec string + execution-location flags come from plugin_input (they
// left #Op when the verb became a plugin unit); exit_status/stdout/stderr stay on the
// step Op and are read by r.runCommand from `op`.
func (commandVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	var in params.CommandInput
	decodePluginInput(op.PluginInput, &in)
	return r.runCommand(ctx, op, commandCheck{
		Command:     in.Command,
		InContainer: in.InContainer,
		Background:  in.Background,
		FromHost:    in.FromHost,
	})
}

func init() {
	RegisterBuiltinPluginUnit(PluginUnit{
		Providers: []Provider{commandVerb{}},
		Schema:    PluginSchema{CueSource: commandplugin.Schema(), InputDefs: commandplugin.InputDefs},
	})
}
