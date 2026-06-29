package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	pb "github.com/overthinkos/overthink/charly/plugin/proto"
	"github.com/overthinkos/overthink/charly/spec"
)

// TestRunBuildStep_Dispatch proves the F3 build-channel host handler routes each step
// view to the RIGHT existing core fn — the only NEW seam F3 adds (the build machinery
// itself is already proven by the deploy beds). The reverse server holds a fake executor,
// so no real build runs; we assert the DISPATCH decision per kind:
//
//   - Builder            → runVenueBuilderStep (here: a no-host-cell builder under
//     --skip-incompatible, so it cleanly returns the step's ReverseOps without a build).
//   - LocalPkgInstall    → execLocalPkgInstall (here: nil LocalPkg, a clean skip).
//   - any other kind     → a loud error (F3 is the BUILD channel; every other kind the
//     plugin executes itself via RunSystem/PutFile).
func TestRunBuildStep_Dispatch(t *testing.T) {
	srv := &executorReverseServer{exec: &recordingExec{homeReturn: "/home/test"}}
	ctx := context.Background()

	call := func(t *testing.T, step InstallStep, opts EmitOpts) *pb.BuildStepReply {
		t.Helper()
		stepJSON, err := json.Marshal(stepToView(step))
		if err != nil {
			t.Fatalf("marshal step view: %v", err)
		}
		optsJSON, _ := json.Marshal(opts)
		rep, err := srv.RunBuildStep(ctx, &pb.BuildStepRequest{StepJson: stepJSON, OptsJson: optsJSON})
		if err != nil {
			t.Fatalf("RunBuildStep returned a transport error (should ride reply.Error): %v", err)
		}
		return rep
	}

	t.Run("builder arm", func(t *testing.T) {
		// A builder with no LocalPkg + nil BuilderDef has no host build cell; under
		// --skip-incompatible runVenueBuilderStep returns nil — proving the Builder arm
		// was taken (the default arm would have errored "not a build-engine step").
		rep := call(t, &BuilderStep{Builder: "npm", CandyName: "x"}, EmitOpts{SkipIncompatible: true})
		if rep.GetError() != "" {
			t.Fatalf("builder arm: unexpected error: %s", rep.GetError())
		}
		var ops []spec.ReverseOp
		if err := json.Unmarshal(rep.GetReverseOpsJson(), &ops); err != nil {
			t.Fatalf("builder arm: reverse ops not decodable: %v", err)
		}
	})

	t.Run("localpkg arm", func(t *testing.T) {
		// A LocalPkgInstallStep with nil LocalPkg is a clean skip in execLocalPkgInstall —
		// proving the LocalPkgInstall arm was taken (no error, vs the default arm's error).
		rep := call(t, &LocalPkgInstallStep{CandyName: "charly", PkgbuildRef: "pkg/arch", Format: "pac"}, EmitOpts{})
		if rep.GetError() != "" {
			t.Fatalf("localpkg arm: unexpected error: %s", rep.GetError())
		}
	})

	t.Run("non-build kind rejected", func(t *testing.T) {
		// An Op step is NOT a build-engine step — the plugin must execute it itself via
		// RunSystem/PutFile. RunBuildStep rejects it loudly.
		rep := call(t, &OpStep{CandyName: "x", Op: &Op{Command: "true"}}, EmitOpts{})
		if !strings.Contains(rep.GetError(), "not a build-engine step") {
			t.Fatalf("non-build kind: want a 'not a build-engine step' error, got %q", rep.GetError())
		}
	})

	t.Run("malformed step view", func(t *testing.T) {
		rep, err := srv.RunBuildStep(ctx, &pb.BuildStepRequest{StepJson: []byte("{not json")})
		if err != nil {
			t.Fatalf("transport error: %v", err)
		}
		if rep.GetError() == "" {
			t.Fatal("malformed step view: want a decode error in reply.Error")
		}
	})
}
