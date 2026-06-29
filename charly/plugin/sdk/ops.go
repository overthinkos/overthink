package sdk

// Operation selectors (the op.Op / InvokeRequest.Op wire value). Each provider class uses
// the subset it needs. This is the SINGLE SOURCE for the selectors (R3): charly's package
// main aliases these (provider.go), and an out-of-tree / compiled-in plugin's Invoke
// dispatch compares req.GetOp() against them — so a kind candy checks sdk.OpLoad, a
// step/deploy candy sdk.OpEmit/sdk.OpExecute, a builder candy sdk.OpResolve.
const (
	OpRun      = "run"      // verb: run a check / live-container probe → CheckResult
	OpLoad     = "load"     // kind: decode a node into its typed entity
	OpValidate = "validate" // kind: closed/concrete CUE validation → Diagnostics
	OpEmit     = "emit"     // deploy/step: emit an InstallPlan / Containerfile fragment
	OpExecute  = "execute"  // deploy/step: execute against a venue (streamed)
	OpResolve  = "resolve"  // builder: resolve a builder image + steps (build-time multi-stage)

	// OpCollectContext + OpReverse are the DEPLOY-TIME builder-IR legs of an externalized
	// detection-builder plugin (cargo/npm/pixi/aur). A builder's build-time multi-stage stays
	// the CORE vocabulary (emitBuilderStages); these two carry the per-builder deploy-time IR
	// shim — the stage-context the compiler records on a BuilderStep + that step's teardown
	// ops — out-of-process. BOTH are invoked HOST-SIDE in the build PRE-PASS (BEFORE the pure
	// BuildDeployPlan compile reads the result), never inside the pure compiler.
	OpCollectContext = "collect-context" // builder: per-candy stage-context keys → BuilderCollectReply
	OpReverse        = "reverse"         // builder: teardown ops for a resolved stage context → BuilderReverseReply
)
