// Package record is the importable, COMPILED-IN host-coupled `record` LIVE-CONTAINER verb:
// record terminal sessions or desktop video inside a running deployment (list/start/stop/cmd).
// A SCHEMA-LESS kit.LiveVerbProvider — its modifiers ride the closed base #Op; RunVerb
// delegates dispatch to the host via cc.RunCharlyVerb. Relocated out of charly's module
// (formerly charly/plugin_verb_record.go); COMPILED-IN-ONLY. The `charly check record` driver
// command stays host-side (cc.RunCharlyVerb self-invokes it).
package record

import (
	"context"

	"github.com/overthinkos/overthink/charly/plugin/kit"
	"github.com/overthinkos/overthink/charly/spec"
)

// NewLiveVerb returns the record verb as a kit.LiveVerbProvider for compiled-in registration.
func NewLiveVerb() kit.LiveVerbProvider { return verb{} }

type verb struct{}

func (verb) Reserved() string { return "record" }

func (verb) RunVerb(ctx context.Context, cc kit.CheckContext, op *spec.Op) kit.Result {
	return cc.RunCharlyVerb(ctx, op, "record", op.Record, recordMethods)
}

func (verb) Methods() map[string]kit.MethodSpec { return recordMethods }
func (verb) MethodField(op *spec.Op) string     { return op.Record }

// recordMethods is the record verb's method allowlist (the dispatch data the host's runCharlyVerb reads).
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
