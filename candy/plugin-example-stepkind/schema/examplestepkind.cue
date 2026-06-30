// schema/examplestepkind.cue — the SELF-CONTAINED CUE def validating the example external
// step kind's opaque plugin_input (the Payload carried as "external:examplestepkind"). Ships
// over Describe (schema_cue) + drives the generated Go params; references no base def so it
// compiles standalone (BuildCapabilities compiles it alone, failing loudly if broken).
#ExamplestepkindInput: {
	// marker is the string the step writes to its venue marker file — proving the OPAQUE
	// payload round-tripped through the IR (InstallStepView.Payload) to the plugin's OpExecute.
	marker?: string
}
