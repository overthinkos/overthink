package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	pb "github.com/overthinkos/overthink/charly/plugin/proto"
	"github.com/overthinkos/overthink/charly/spec"
)

// TestExternalStepKind_EndToEnd proves the FULL F3 external-step-kind path END-TO-END on real
// code: the reference class:step plugin (candy/plugin-example-stepkind) is host-built + served
// OUT-OF-PROCESS, its DECLARED StepContract is decoded from Describe (buildUnit), an external
// step ("external:examplestepkind") round-trips through the opaque step view, and the host's
// OPEN DEFAULT ARM in RunHostStep dispatches it (executeExternalStep → OpExecute over the E3b
// reverse channel) so the plugin writes a marker on the real shell venue and returns a dynamic
// teardown ReverseOp — NO compiled-in case for the word anywhere. Builds + execs a real binary,
// gated behind -short like the other reverse-channel e2es.
func TestExternalStepKind_EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("builds + execs the external plugin binary (slow)")
	}
	ctx := context.Background()

	srcDir, err := filepath.Abs("../candy/plugin-example-stepkind")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(srcDir, "go.mod")); err != nil {
		t.Fatalf("example step-kind plugin module not found at %s: %v", srcDir, err)
	}

	bin, err := buildPluginBinary(ctx, srcDir, "plugin-example-stepkind-test")
	if err != nil {
		t.Fatalf("buildPluginBinary: %v", err)
	}
	unit, closer, err := (&LocalTransport{BinPath: bin}).Connect(ctx)
	if err != nil {
		t.Fatalf("LocalTransport.Connect: %v", err)
	}
	defer func() { _ = closer.Close() }()

	if len(unit.Providers) != 1 || unit.Providers[0].Class() != ClassStep || unit.Providers[0].Reserved() != "examplestepkind" {
		t.Fatalf("providers = %+v, want exactly one step:examplestepkind", unit.Providers)
	}
	// The DECLARED StepContract round-trips from the plugin's Describe through buildUnit.
	carrier, ok := unit.Providers[0].(stepContractCarrier)
	if !ok {
		t.Fatalf("provider %T does not carry a step contract", unit.Providers[0])
	}
	sc, ok := carrier.declaredStepContract()
	if !ok || sc.Scope != ScopeUser || sc.Venue != VenueHostNative || sc.Gate != GateNone {
		t.Fatalf("declared contract = %+v ok=%v, want {ScopeUser, VenueHostNative, GateNone}", sc, ok)
	}
	if err := providerRegistry.RegisterPluginProviders(unit.Providers, "f3-stepkind-test", closer); err != nil {
		t.Fatalf("RegisterPluginProviders: %v", err)
	}

	// The compileActOp ROUTING seam: a `run: plugin: examplestepkind` op whose provider is a
	// class:step grpcProvider declaring a StepContract lowers to an externalStep carrying the
	// DECLARED contract + the opaque payload (NOT an OpStep / ExternalPluginStep). Using the
	// compiled step (not a hand-built one) proves the FULL authoring → compile → wire path.
	op := &Op{Plugin: "examplestepkind", PluginInput: map[string]any{"marker": "EXTERNAL-STEPKIND-E2E"}}
	routed := compileActOp(op, &Candy{Name: "plugin-example-stepkind"}, &ResolvedBox{Tags: []string{"fedora"}})
	step, ok := routed.(*externalStep)
	if !ok {
		t.Fatalf("compileActOp routed a class:step plugin to %T, want *externalStep", routed)
	}
	if step.Word != "examplestepkind" || step.ScopeV != ScopeUser || step.VenueV != VenueHostNative || step.GateV != GateNone {
		t.Fatalf("externalStep contract = {%q, %v, %v, %v}, want {examplestepkind, ScopeUser, VenueHostNative, GateNone}", step.Word, step.ScopeV, step.VenueV, step.GateV)
	}

	// Project it to the OPAQUE view (Kind "external:<word>" + Payload).
	view := stepToView(step)
	if view.Kind != "external:examplestepkind" {
		t.Fatalf("view.Kind = %q, want external:examplestepkind", view.Kind)
	}
	stepJSON, err := marshalJSON(view)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.RemoveAll("/tmp/charly-examplestepkind") })

	// Walk it via the host's RunHostStep OPEN DEFAULT ARM (stepFromView rebuilds the externalStep
	// from the carried contract + Payload; executeExternalStep dispatches OpExecute to the plugin).
	srv := &executorReverseServer{exec: ShellExecutor{}}
	reply, err := srv.RunHostStep(ctx, &pb.HostStepRequest{StepJson: stepJSON})
	if err != nil {
		t.Fatalf("RunHostStep: %v", err)
	}
	if reply.GetError() != "" {
		t.Fatalf("RunHostStep reply error: %s", reply.GetError())
	}

	// The external step executed on the real venue — the marker is present, carrying the
	// opaque payload's value (proving the Payload round-tripped to the plugin's OpExecute).
	got, err := os.ReadFile("/tmp/charly-examplestepkind/marker")
	if err != nil {
		t.Fatalf("external step did not write the venue marker: %v", err)
	}
	if string(got) != "EXTERNAL-STEPKIND-E2E\n" {
		t.Fatalf("marker = %q, want the opaque payload value round-tripped", got)
	}

	// The dynamic teardown op rode the reply (record-and-replay — externalStep.Reverse() is
	// populated from the plugin's OpExecute DeployReply, never compiled-in).
	var ops []spec.ReverseOp
	if err := json.Unmarshal(reply.GetReverseOpsJson(), &ops); err != nil {
		t.Fatalf("decode reverse ops: %v", err)
	}
	if len(ops) == 0 {
		t.Fatal("external step recorded no teardown op (record-and-replay broken)")
	}
}
