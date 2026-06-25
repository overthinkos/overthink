package main

import (
	"context"

	"github.com/overthinkos/overthink/charly/plugin/kit"
)

// recordVerb is the BUILT-IN `record` LIVE-CONTAINER verb, extracted into its OWN
// dedicated file (Phase 1, the live-container-verb relocation). Like cdp/vnc, record
// stays a FIRST-CLASS #Op verb: it keeps its dedicated `record:` discriminator and its
// method-specific modifiers (RecordName/RecordMode/RecordFps/RecordAudio/Text/Artifact)
// on the closed base #Op — there is NO plugin_input and therefore NO served plugin
// schema. So it self-registers via registerDedicatedBuiltin (the schema-less
// dedicated-provider path), INTENTIONALLY absent from BOTH builtinProviderInstances and
// the `providers:` manifest, yet resolving + dispatching through the SAME
// providerRegistry (the verb + method-allowlist bijection gates still see it). It embeds
// builtinVerbBase for Class()=ClassVerb + the in-proc-only Invoke stub (a live verb
// carries the *Runner and never serves itself over the wire).
//
// `charly check record <method>` drives in-container recording sessions (asciinema
// terminal / pixelflux-record / wf-recorder desktop). Container-only: resolveContainer
// does not know about VMs, so a `record:` check on a `vm:<name>` deploy will fail at
// subprocess dispatch.
//
// This file owns the verb's complete contract: the provider (Reserved/RunVerb), the
// LiveVerbProvider method contract (Methods/MethodField), the recordMethods method
// allowlist, and the runRecord dispatcher. The shared kit.PosArgs builder library
// (kit.PosRecordStart/kit.PosRecordStop/kit.PosRecordCmd), the kit.MethodSpec type, and the
// artifactValidatableMethods allowlist (record/stop) stay in checkrun_charly_verbs.go.
type recordVerb struct{ builtinVerbBase }

func (recordVerb) Reserved() string { return "record" }

func (recordVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runRecord(ctx, op)
}

func (recordVerb) Methods() map[string]kit.MethodSpec { return recordMethods }
func (recordVerb) MethodField(c *Op) string           { return c.Record }

// recordMethods is the record verb's method allowlist (the dispatch data runCharlyVerb reads).
var recordMethods = map[string]kit.MethodSpec{
	"list":  {Path: []string{"record", "list"}},
	"start": {Path: []string{"record", "start"}, PosArgs: kit.PosRecordStart},
	// stop's Artifact: true asserts the recording was copied out AND (when
	// ArtifactMinBytes is set) that the file is at least N bytes — a strong
	// "the recorder actually produced output" invariant.
	"stop": {Path: []string{"record", "stop"}, Required: []string{"Artifact"}, PosArgs: kit.PosRecordStop, Artifact: true},
	// `record: cmd` sends a text line into the recording's tmux session.
	// Text (not Command) is used because Command is itself a verb
	// discriminator — setting both would trip the Kind() uniqueness check.
	"cmd": {Path: []string{"record", "cmd"}, Required: []string{"Text"}, PosArgs: kit.PosRecordCmd},
}

func (r *Runner) runRecord(ctx context.Context, c *Op) CheckResult {
	return r.runCharlyVerb(ctx, c, "record", c.Record, recordMethods)
}

var _ = registerDedicatedBuiltin(recordVerb{})
