package main

// npmBuilder is the `npm` builder IR provider — globals tracked by package name from
// package.json (read host-side). Extracted into its OWN file as the externalizable
// dedicated-provider pattern (Phase 3). A builder is candy-internal (no user-authored
// plugin_input, no CUE schema), so it does not fit the schema-carrying
// RegisterBuiltinPluginUnit path; it self-registers via registerDedicatedBuiltin below
// and is INTENTIONALLY absent from both the builtinProviderInstances slice and the
// `providers:` manifest, yet dispatches identically through
// providerRegistry.ResolveBuilder. Its reverse (teardown) + stage-context logic are
// unchanged (behavior-preserving). Builders have no bijection gate (there is no fixed
// CUE builder vocabulary), so the registry resolve IS the proof it is wired.
type npmBuilder struct{ builtinBuilderBase }

func (npmBuilder) Reserved() string { return "npm" }
func (npmBuilder) Reverse(s *BuilderStep) []ReverseOp {
	if pkgs := extractStringSlice(s.RawStageContext, "packages"); len(pkgs) > 0 {
		return []ReverseOp{{
			Kind:    ReverseOpNpmUninstallG,
			Targets: pkgs,
			Scope:   ScopeUser,
		}}
	}
	return nil
}
func (npmBuilder) CollectContext(_ *Candy, _ *ResolvedBox) map[string]any { return nil }

// Self-register at package-var init (before any init()), mirroring cargoBuilder.
var _ = registerDedicatedBuiltin(npmBuilder{})
