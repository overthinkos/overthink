package main

// cargoBuilder is the `cargo` builder IR provider — binaries tracked by name; the host
// target reads Cargo.toml [[bin]] entries at install time and updates the ledger (empty
// list → best-effort). Extracted into its OWN file as the externalizable
// dedicated-provider pattern (Phase 3). A builder is candy-internal (no user-authored
// plugin_input, no CUE schema), so it does not fit the schema-carrying
// RegisterBuiltinPluginUnit path; it self-registers via registerDedicatedBuiltin below
// and is INTENTIONALLY absent from both the builtinProviderInstances slice and the
// `providers:` manifest, yet dispatches identically through
// providerRegistry.ResolveBuilder. Its reverse (teardown) + stage-context logic are
// unchanged (behavior-preserving). Builders have no bijection gate (there is no fixed
// CUE builder vocabulary), so the registry resolve IS the proof it is wired.
type cargoBuilder struct{ builtinBuilderBase }

func (cargoBuilder) Reserved() string { return "cargo" }
func (cargoBuilder) Reverse(s *BuilderStep) []ReverseOp {
	if bins := extractStringSlice(s.RawStageContext, "binaries"); len(bins) > 0 {
		return []ReverseOp{{
			Kind:    ReverseOpCargoUninstall,
			Targets: bins,
			Scope:   ScopeUser,
		}}
	}
	return nil
}
func (cargoBuilder) CollectContext(_ *Candy, _ *ResolvedBox) map[string]any { return nil }

// Self-register at package-var init (before any init()), mirroring localTarget.
var _ = registerDedicatedBuiltin(cargoBuilder{})
