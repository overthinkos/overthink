// The BUILT-IN `process` plugin's OWN CUE schema — the typed plugin_input for the
// `process` verb (pgrep -x exact-name match of a running process). It is the SINGLE
// SOURCE for this plugin's params, used two ways (the same contract the reference
// examplerunverb and core `spec` use):
//
//  1. GENERATE the Go param struct — `cue exp gengotypes` (driven by task cue:gen,
//     which wraps this with `package params` + `@go(params)`) emits
//     ../params/cue_types_gen.go, so the provider decodes plugin_input into a TYPED
//     struct, never a hand-parsed map.
//  2. VALIDATE authored input AT RUNTIME — the builtin serves this source over the
//     Describe channel (InProcTransport) exactly like an external serves it over
//     gRPC; the host splices it onto the base (base ++ plugin) and validates every
//     authored `process` step's plugin_input against #ProcessInput.
//
// SELF-CONTAINED: it references NO base def, so it compiles standalone (gengotypes +
// the load-gate compile) AND splices onto the base — the base ++ plugin splice
// exists to detect a def-name collision with the base, not to resolve base refs.
//
// `running` is a SHARED base #Op field (the `service` verb also reads it), so it
// STAYS in #Op; this plugin reproduces its `bool` shape standalone here (a bare
// primitive, NO def reference, so there is no collision when base ++ plugin
// compiles). Its provider is a CheckVerbProvider — it dispatches IN-PROCESS via
// RunVerb and so keeps the live *Runner the pgrep probe needs (mirrors
// examplerunverb), the property this extraction preserves.
#ProcessInput: {
	// process — the exact process name pgrep -x matches against.
	process: string @go(Process)
	// running — whether the process is expected to be running (default true). A
	// tri-state pointer so an absent key means "expected running", matching the base
	// #Op.running semantics the verb had before extraction.
	running?: bool @go(Running,type=*bool)
}
