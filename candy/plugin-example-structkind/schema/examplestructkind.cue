// schema/examplestructkind.cue — the SELF-CONTAINED CUE def validating the example STRUCTURAL
// external KIND's authored body. Ships over Describe (schema_cue); references no base def so it
// compiles standalone (BuildCapabilities compiles it alone, failing loudly if broken).
#ExamplestructkindInput: {
	// marker names the single member the plugin emits in its spec.Deploy reply — proving the
	// authored body round-trips through OpLoad into the member tree the host folds into uf.Bundle.
	marker?: string
}
