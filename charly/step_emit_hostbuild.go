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
// The per-word emitter registry (stepEmitters) holds one renderer per relocated host-coupled step
// kind. C1.2 registered the FIRST — system-packages (stepEmitSystemPackages, below), whose plugin's
// OpEmit calls HostBuild("step-emit", {Word:"system-packages", …}) and whose in-core rendering
// registers here via registerStepEmitter. C1.3 registered the SECOND — builder (stepEmitBuilder,
// below), whose build-emit needs the multi-stage builder render engine (buildStageContext +
// RenderTemplate) that cannot cross the process boundary. C1.4 registered the THIRD —
// local-pkg-install (stepEmitLocalPkgInstall, below), whose build-emit needs the host localpkg
// build engine (renderLocalPkgImageInstall → buildLocalPkgOnHost + host-dir staging). The seam is
// GENERIC (dispatches by word, no per-word case here), exactly like hostBuilders dispatches by kind.

// stepEmitter renders one host-coupled external step kind's build-context Containerfile fragment
// IN-CORE from the opaque request + the host build-engine context. Registered per step word.
type stepEmitter func(req spec.StepEmitRequest, build buildEngineContext) (string, error)

// stepEmitters maps a step WORD → its in-core fragment renderer. Populated at package-var init
// (before any init(), like hostBuilders / the substrate registries), so lookup is race-free.
// Holds one renderer per relocated host-coupled step kind (C1.2 registered system-packages).
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

// stepEmitSystemPackages renders the SystemPackages InstallStep's BUILD-context (container-venue)
// Containerfile fragment IN-CORE — the C1.2 relocation of the SystemPackages build-emit off
// OCITarget onto the step-emit seam. SystemPackages' build-emit is HOST-COUPLED: it needs the host
// build ENGINE (the DistroDef format templates + RenderTemplate) that cannot cross the process
// boundary, so its serving class:step plugin (candy/plugin-installstep) calls back
// HostBuild("step-emit", …) during OpEmit and this renders the fragment host-side. The render is
// UNCHANGED from the former in-proc build-emit (R3): reconstruct the concrete step from the wire
// view (stepFromView), resolve the box-specific FormatDef via the SAME DistroConfig.FindFormat path
// the host deploy render uses (build.DistroCfg wraps the box-resolved DistroDef — wrapDistroDef),
// and render the format's phase.install.container template. A nil FormatDef is a LOUD error (as the
// former in-proc render was); an empty template for the phase/venue is legitimately nothing to emit.
func stepEmitSystemPackages(req spec.StepEmitRequest, build buildEngineContext) (string, error) {
	var view spec.InstallStepView
	if len(req.Payload) > 0 {
		if err := json.Unmarshal(req.Payload, &view); err != nil {
			return "", fmt.Errorf("decode SystemPackages step view: %w", err)
		}
	}
	step, err := stepFromView(view)
	if err != nil {
		return "", err
	}
	s, ok := step.(*SystemPackagesStep)
	if !ok {
		return "", fmt.Errorf("step-emit system-packages: view kind %q is not a SystemPackagesStep", view.Kind)
	}
	formatDef := build.DistroCfg.FindFormat(s.Format)
	if formatDef == nil {
		return "", fmt.Errorf("no distro definition for format %q", s.Format)
	}
	template := formatPhaseTemplate(formatDef, s.Phase, VenueContainerBuilder)
	if template == "" {
		// No template for this phase/venue is not an error — some phases simply have
		// nothing to emit in the container (e.g. cleanup phases whose host: blocks only
		// record state for teardown).
		return "", nil
	}
	ctx := NewInstallContext(s.RawInstallContext, formatDefCacheMountDefs(formatDef))
	rendered, err := RenderTemplate(s.Format+"-install", template, ctx)
	if err != nil {
		return "", fmt.Errorf("rendering %s install template: %w", s.Format, err)
	}
	return rendered, nil
}

// Register the system-packages step-emitter at package-var init — the FIRST host-coupled step
// kind relocated onto the step-emit seam (C1.2). Its plugin (candy/plugin-installstep) serves the
// OpEmit that calls back HostBuild("step-emit", {Word:"system-packages", …}).
var _ = func() bool { registerStepEmitter("system-packages", stepEmitSystemPackages); return true }()

// stepEmitBuilder renders the Builder InstallStep's BUILD-context (container-venue) Containerfile
// fragment IN-CORE — the C1.3 relocation of the Builder build-emit off OCITarget onto the step-emit
// seam. The Builder build-emit is HOST-COUPLED: it needs the host build ENGINE — the embedded
// builder: vocabulary (BuilderConfig), the multi-stage stage_template render (Generator.buildStageContext),
// and the box UID/GID + builder-ref (ResolvedBox) — none of which can cross the process boundary. So
// its serving class:step plugin (candy/plugin-installstep) calls back HostBuild("step-emit", …)
// during OpEmit and this renders the fragment host-side. The render is UNCHANGED from the former
// in-proc OCITarget builder build-emit (R3): reconstruct the concrete step from the wire view
// (stepFromView), then reuse the SAME buildStageContext + RenderTemplate pipeline the box build uses.
// The build engine
// (Generator/BuilderConfig/Box) is threaded on the reverse channel via buildEngineContext (populated
// by OCITarget.stepEmitBuildContext); a nil BuilderConfig / Box / layer yields the SAME informative
// skip comment the former in-proc render produced (synthetic test paths), and an undefined builder or
// a template error is a LOUD failure (never a silent empty bake, R4).
func stepEmitBuilder(req spec.StepEmitRequest, build buildEngineContext) (string, error) {
	var view spec.InstallStepView
	if len(req.Payload) > 0 {
		if err := json.Unmarshal(req.Payload, &view); err != nil {
			return "", fmt.Errorf("decode Builder step view: %w", err)
		}
	}
	step, err := stepFromView(view)
	if err != nil {
		return "", err
	}
	s, ok := step.(*BuilderStep)
	if !ok {
		return "", fmt.Errorf("step-emit builder: view kind %q is not a BuilderStep", view.Kind)
	}

	if build.BuilderConfig == nil {
		return fmt.Sprintf("# Builder: %s (layer=%s) — skipped, no BuilderConfig\n",
			s.Builder, s.CandyName), nil
	}
	bDef, ok := build.BuilderConfig.Builder[s.Builder]
	if !ok || bDef == nil {
		return "", fmt.Errorf("builder %q: not defined in BuilderConfig", s.Builder)
	}
	if build.Box == nil {
		return fmt.Sprintf("# Builder: %s (layer=%s) — skipped, no Image context\n",
			s.Builder, s.CandyName), nil
	}

	// candyByName is nil-safe (returns nil for a nil Generator), matching the former
	// OCITarget.lookupCandy guard.
	layer := build.Generator.candyByName(s.CandyName)
	if layer == nil {
		return fmt.Sprintf("# Builder: %s (layer=%s) — layer not found in scan\n",
			s.Builder, s.CandyName), nil
	}

	// Inline builders (cargo): render InstallTemplate with the builder's inline context; no
	// separate FROM stage. Switch USER to the image user for the inline builder steps.
	if bDef.Inline {
		ctx := &BuildStageContext{
			LayerStage:  layer.Name,
			UID:         build.Box.UID,
			GID:         build.Box.GID,
			CacheMounts: bDef.CacheMount,
		}
		rendered, err := RenderTemplate(s.Builder+"-inline", bDef.InstallTemplate, ctx)
		if err != nil {
			return "", fmt.Errorf("inline builder %s: %w", s.Builder, err)
		}
		return fmt.Sprintf("USER %d\n", build.Box.UID) + rendered, nil
	}

	// Multi-stage builders (pixi/npm/aur): emit the stage via the Generator's buildStageContext
	// helper. A synthetic path without a Generator falls back to an informative comment (the layer
	// lookup above already returned nil for a nil Generator, so this is defensive parity with the
	// former in-proc render).
	if build.Generator == nil {
		return fmt.Sprintf("# Builder: %s (layer=%s) — multi-stage requires Generator; emit skipped\n",
			s.Builder, s.CandyName), nil
	}
	builderRef := ""
	if build.Box.Builder != nil {
		builderRef = build.Box.Builder[s.Builder]
	}
	ctx := build.Generator.buildStageContext(layer, s.Builder, bDef, build.Box, builderRef)
	if ctx == nil {
		return "", fmt.Errorf("buildStageContext returned nil for %s", s.Builder)
	}
	rendered, err := RenderTemplate(s.Builder+"-stage", bDef.StageTemplate, ctx)
	if err != nil {
		return "", fmt.Errorf("multi-stage builder %s: %w", s.Builder, err)
	}
	return rendered, nil
}

// Register the builder step-emitter at package-var init — the SECOND host-coupled step kind
// relocated onto the step-emit seam (C1.3). Its plugin (candy/plugin-installstep) serves the OpEmit
// that calls back HostBuild("step-emit", {Word:"builder", …}).
var _ = func() bool { registerStepEmitter("builder", stepEmitBuilder); return true }()

// stepEmitLocalPkgInstall renders the LocalPkgInstall InstallStep's BUILD-context Containerfile
// fragment IN-CORE — the C1.4 relocation of the LocalPkgInstall build-emit off OCITarget onto the
// step-emit seam. The LocalPkgInstall build-emit is HOST-COUPLED: renderLocalPkgImageInstall reads
// the box-type switch off the Generator (DevLocalPkg) and, for a disposable check bed, BUILDS the
// candy's package from LOCAL in-development source on the HOST (buildLocalPkgOnHost — makepkg /
// podman) and STAGES the built file into the per-image build dir (ImageBuildDir) — none of which can
// cross the process boundary. So its serving class:step plugin (candy/plugin-installstep) calls back
// HostBuild("step-emit", …) during OpEmit and this renders the fragment host-side. The render is
// UNCHANGED from the former in-proc OCITarget localpkg build-emit (R3): reconstruct the concrete step
// from the wire view (stepFromView), then call the SAME renderLocalPkgImageInstall generate.go's
// image build also uses — a PRODUCTION box DOWNLOADS the published release, a DISPOSABLE bed BUILDS
// the in-development package and COPYs it in; a distro with no localpkg-capable format (LocalPkg==nil)
// renders nothing. The build engine (Generator.DevLocalPkg + Box.Name + ImageBuildDir) is threaded on
// the reverse channel via buildEngineContext (populated by OCITarget.stepEmitBuildContext); the
// overlay/deploy path never sets DevLocalPkg, so the pod-overlay build-emit takes the production leg.
func stepEmitLocalPkgInstall(req spec.StepEmitRequest, build buildEngineContext) (string, error) {
	var view spec.InstallStepView
	if len(req.Payload) > 0 {
		if err := json.Unmarshal(req.Payload, &view); err != nil {
			return "", fmt.Errorf("decode LocalPkgInstall step view: %w", err)
		}
	}
	step, err := stepFromView(view)
	if err != nil {
		return "", err
	}
	s, ok := step.(*LocalPkgInstallStep)
	if !ok {
		return "", fmt.Errorf("step-emit local-pkg-install: view kind %q is not a LocalPkgInstallStep", view.Kind)
	}
	dev := build.Generator != nil && build.Generator.DevLocalPkg
	boxName := ""
	if build.Box != nil {
		boxName = build.Box.Name
	}
	return renderLocalPkgImageInstall(s, dev, build.ImageBuildDir, boxName)
}

// Register the local-pkg-install step-emitter at package-var init — the THIRD host-coupled step kind
// relocated onto the step-emit seam (C1.4). Its plugin (candy/plugin-installstep) serves the OpEmit
// that calls back HostBuild("step-emit", {Word:"local-pkg-install", …}).
var _ = func() bool { registerStepEmitter("local-pkg-install", stepEmitLocalPkgInstall); return true }()
