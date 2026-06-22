package main

// aurBuilder is the `aur` builder IR provider — builds package files installed into the
// host package DB; needs a host staging dir for the built .pkg files. Extracted into its
// OWN file as the externalizable dedicated-provider pattern (Phase 3). A builder is
// candy-internal (no user-authored plugin_input, no CUE schema), so it does not fit the
// schema-carrying RegisterBuiltinPluginUnit path; it self-registers via
// registerDedicatedBuiltin below and is INTENTIONALLY absent from both the
// builtinProviderInstances slice and the `providers:` manifest, yet dispatches
// identically through providerRegistry.ResolveBuilder. It additionally implements the
// optional BuilderStager half (StagingMount → /tmp/aur-pkgs, bind-mounted into the
// builder container). Its reverse (teardown) + stage-context logic are unchanged
// (behavior-preserving). Builders have no bijection gate (there is no fixed CUE builder
// vocabulary), so the registry resolve IS the proof it is wired.
type aurBuilder struct{ builtinBuilderBase }

func (aurBuilder) Reserved() string     { return "aur" }
func (aurBuilder) StagingMount() string { return "/tmp/aur-pkgs" }
func (aurBuilder) Reverse(s *BuilderStep) []ReverseOp {
	// aur packages get installed into the host package DB; reverse is the same
	// as SystemPackagesStep. The compiler records the names in RawStageContext.
	if pkgs := extractStringSlice(s.RawStageContext, "packages"); len(pkgs) > 0 {
		return []ReverseOp{{
			Kind:    ReverseOpPackageRemove,
			Format:  "pac",
			Targets: pkgs,
			Scope:   ScopeSystem,
		}}
	}
	return nil
}
func (aurBuilder) CollectContext(layer *Candy, _ *ResolvedBox) map[string]any {
	ctx := map[string]any{}
	if section := layer.FormatSection("aur"); section != nil {
		ctx["packages"] = append([]string(nil), section.Packages...)
		// `replaces:` lists distro-repo packages that conflict with the AUR build
		// artifact and must be removed before `pacman -U` (idempotent host-side).
		if raw, ok := section.Raw["replaces"]; ok {
			if list, ok := stringSliceFromYAML(raw); ok {
				ctx["replaces"] = list
			}
		}
	}
	return ctx
}

// Self-register at package-var init (before any init()), mirroring cargoBuilder.
var _ = registerDedicatedBuiltin(aurBuilder{})
