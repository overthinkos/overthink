// This out-of-tree COMMAND plugin's OWN CUE schema — the typed plugin_input for the
// `examplecommand` command it provides. It is the SINGLE SOURCE for this plugin's
// params, used two ways (the same contract the verb candy/plugin-example-external,
// the built-in exampleprobe, and core `spec` use):
//
//  1. GENERATE the Go param struct — `cue exp gengotypes` (driven by task cue:gen,
//     which wraps this with `package params` + `@go(params)`) emits
//     params/cue_types_gen.go, so the plugin decodes its input into a TYPED struct
//     (params.ExamplecommandInput), never a hand-parsed map[string]any.
//  2. VALIDATE / DESCRIBE AT RUNTIME — the plugin SERVES this source over the
//     Describe channel (the gRPC schema_cue field via sdk.BuildCapabilities); the
//     host splices it onto its base (base ++ plugin) for the `command:examplecommand`
//     capability. The host NEVER reads this file from disk — the schema travels with
//     the plugin.
//
// SELF-CONTAINED (carries no package clause and references NO base def): it compiles
// STANDALONE (gengotypes + the SDK serve-side check) AND splices onto the base — the
// base ++ plugin splice exists to detect a def-name collision with the base, not to
// resolve base references. CUE stays the single source: no hand-written struct.
//
// Command class shape: charly's CLI dispatch (charly/provider_command_external.go
// dispatchExternalCommand) forwards the user's pass-through CLI tokens as
// op.Params = {"args": [...]} on an OpRun Invoke — so the typed input is just that
// args list. (NOTE: unlike a verb CHECK step, command dispatch does NOT wrap the
// payload in a `plugin_input` envelope; the args ride op.Params directly.)

// #ExamplecommandInput — the input shape for the `examplecommand` command. The
// optional `args` list carries every CLI token after the command word.
#ExamplecommandInput: {
	// args is the pass-through CLI token list charly forwards from
	// `charly examplecommand <args…>`. Optional — an empty/absent list means the
	// command was invoked with no positional arguments.
	args?: [...string] @go(Args)
}
