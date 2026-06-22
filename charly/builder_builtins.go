package main

// The built-in builders as BuilderProviders. Each carries its UNCHANGED reverse
// (teardown) + stage-context logic — the migration is behavior-preserving; only
// the BuilderStep.Reverse + collectBuilderContext switches are replaced by
// providerRegistry.ResolveBuilder. renderBuilderScript stays data-driven.

// aur — builds package files installed into the host package DB; needs a host
// staging dir (BuilderStager) for the built .pkg files.
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

// pixi — envs land at $HOME/.pixi/envs/<env-name>/; reverse removes the env.
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

// cargo — binaries tracked by name; the host target reads Cargo.toml [[bin]]
// entries at install time and updates the ledger (empty list → best-effort).
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

// npm — globals tracked by package name from package.json (read host-side).
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
