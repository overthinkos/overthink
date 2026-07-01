package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/overthinkos/overthink/charly/spec"
)

// step_emit_hostbuild.go — the F-STEP-EMIT "step-emit" host-builder on the F10 HostBuild seam:
// the BUILD-context counterpart of "overlay"/"image"/"containerfiles". A HOST-COUPLED external
// step kind — one whose build-context Containerfile fragment needs the host build ENGINE (the
// DistroDef format templates, the Generator's task/builder rendering) that cannot cross the
// process boundary — has its serving class:step plugin call back Executor.HostBuild("step-emit",
// StepEmitRequest{word,payload,distros}) during its OpEmit. This host-builder dispatches by the
// step WORD to a registered per-word emitter that renders the fragment IN-CORE and returns it as
// an EmitReply (reusing EmitReply — R3). A PURE external step never reaches here: it returns its
// fragment directly from OpEmit (OCITarget.emitExternalStep splices that).
//
// FOUNDATION: the per-word emitter registry (stepEmitters) is EMPTY — no builtin step kind is
// relocated yet (C1). It becomes populated when C1 externalizes a host-coupled step kind (e.g.
// system-packages), whose plugin's OpEmit calls HostBuild("step-emit", …) and whose in-core
// rendering registers here via registerStepEmitter. The seam is GENERIC (dispatches by word,
// no per-word case here), exactly like hostBuilders dispatches by kind.

// stepEmitter renders one host-coupled external step kind's build-context Containerfile fragment
// IN-CORE from the opaque request + the host build-engine context. Registered per step word.
type stepEmitter func(req spec.StepEmitRequest, build buildEngineContext) (string, error)

// stepEmitters maps a step WORD → its in-core fragment renderer. Populated at package-var init
// (before any init(), like hostBuilders / the substrate registries), so lookup is race-free.
// Empty in the foundation; C1 registers a renderer per relocated host-coupled step kind.
var stepEmitters = map[string]stepEmitter{}

// registerStepEmitter records one host-coupled step kind's in-core fragment renderer. Panics on
// a duplicate (a startup invariant, like registerHostBuilder / registerSubstrateLifecycle).
func registerStepEmitter(word string, fn stepEmitter) {
	if word == "" || fn == nil {
		panic("registerStepEmitter: empty word or nil emitter")
	}
	if _, dup := stepEmitters[word]; dup {
		panic(fmt.Sprintf("registerStepEmitter: duplicate step emitter for %q", word))
	}
	stepEmitters[word] = fn
}

// stepEmitterFor returns the registered in-core emitter for a step word, if any.
func stepEmitterFor(word string) (stepEmitter, bool) {
	fn, ok := stepEmitters[word]
	return fn, ok
}

// hostBuildStepEmit is the "step-emit" host-builder (F10 HostBuild seam): decode the
// StepEmitRequest, dispatch by Word to the registered in-core emitter, and return the rendered
// fragment as an EmitReply JSON. An unregistered word is a LOUD error (a host-coupled step whose
// in-core renderer was never registered — never a silent empty bake, R4). The buildEngineContext
// carries the host engine the emitter renders against.
func hostBuildStepEmit(_ context.Context, specJSON []byte, build buildEngineContext) ([]byte, error) {
	var req spec.StepEmitRequest
	if err := json.Unmarshal(specJSON, &req); err != nil {
		return nil, fmt.Errorf("step-emit host-build: decode request: %w", err)
	}
	if req.Word == "" {
		return nil, fmt.Errorf("step-emit host-build: request carries no step word")
	}
	fn, ok := stepEmitterFor(req.Word)
	if !ok {
		return nil, fmt.Errorf("step-emit host-build: no in-core emitter registered for step %q", req.Word)
	}
	frag, err := fn(req, build)
	if err != nil {
		return nil, fmt.Errorf("step-emit host-build %q: %w", req.Word, err)
	}
	return marshalJSON(spec.EmitReply{Fragment: frag})
}

// Register the step-emit host-builder at package-var init (before any init(), like the
// image/overlay/plugin-binary builders).
var _ = func() bool { registerHostBuilder("step-emit", hostBuildStepEmit); return true }()
