package main

// pixiBuilder is the `pixi` builder IR provider — envs land at $HOME/.pixi/envs/<env-name>/;
// reverse removes the env. Extracted into its OWN file as the externalizable
// dedicated-provider pattern (Phase 3). A builder is candy-internal (no user-authored
// plugin_input, no CUE schema), so it does not fit the schema-carrying
// RegisterBuiltinPluginUnit path; it self-registers via registerDedicatedBuiltin below
// and is INTENTIONALLY absent from both the builtinProviderInstances slice and the
// `providers:` manifest, yet dispatches identically through
// providerRegistry.ResolveBuilder. Its reverse (teardown) + stage-context logic are
// unchanged (behavior-preserving). Builders have no bijection gate (there is no fixed
// CUE builder vocabulary), so the registry resolve IS the proof it is wired.
type pixiBuilder struct{ builtinBuilderBase }

func (pixiBuilder) Reserved() string { return "pixi" }
func (pixiBuilder) Reverse(s *BuilderStep) []ReverseOp {
	if env := extractString(s.RawStageContext, "env_name"); env != "" {
		return []ReverseOp{{
			Kind:    ReverseOpPixiEnvRemove,
			Targets: []string{env},
			Scope:   ScopeUser,
			Extra:   map[string]string{"layer": s.CandyName},
		}}
	}
	return nil
}
func (pixiBuilder) CollectContext(layer *Candy, _ *ResolvedBox) map[string]any {
	// Default pixi env name; a candy's pixi.toml [workspace]/[project] name
	// overrides this (the host target reads pixi.toml at install time).
	return map[string]any{"env_name": pixiDefaultEnvName(layer)}
}

// Self-register at package-var init (before any init()), mirroring cargoBuilder.
var _ = registerDedicatedBuiltin(pixiBuilder{})
