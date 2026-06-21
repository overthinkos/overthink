package main

import (
	"context"
	"fmt"
)

// BuilderProvider is the typed in-process form of a builder Provider: it supplies
// the builder-specific reverse-ops (teardown) and the per-builder stage context
// the compiler records on a BuilderStep. Every BUILT-IN builder (aur/pixi/cargo/
// npm) implements it; the call sites resolve the builder through providerRegistry.
// A CUSTOM candy builder (a `builder:` BuilderDef with no special Go logic) has no
// provider — it correctly falls through (no reverse op, base stage context only),
// so builders need no bijection gate (there is no fixed CUE builder vocabulary).
type BuilderProvider interface {
	Provider
	Reverse(s *BuilderStep) []ReverseOp
	CollectContext(layer *Candy, img *ResolvedBox) map[string]any
}

// BuilderStager is the optional half for a builder that needs a host staging dir
// bind-mounted into the builder container (aur stages built package files at
// /tmp/aur-pkgs for the venue's package manager to install). execBuilder resolves
// it and creates/cleans the tmpdir; a builder without it needs no staging.
type BuilderStager interface {
	StagingMount() string // container path to bind a host tmpdir at; "" → none
}

// builtinBuilderBase supplies the in-proc-only Provider half (Class + a stub
// Invoke) for every built-in builder provider.
type builtinBuilderBase struct{}

func (builtinBuilderBase) Class() ProviderClass { return ClassBuilder }
func (builtinBuilderBase) Invoke(context.Context, *Operation) (*Result, error) {
	return nil, fmt.Errorf("built-in builder is in-process only (no out-of-proc Invoke)")
}

// builderProviderFor resolves a builder name to its BuilderProvider (nil, false
// for a custom candy builder with no special Go logic).
func builderProviderFor(name string) (BuilderProvider, bool) {
	prov, ok := providerRegistry.ResolveBuilder(name)
	if !ok {
		return nil, false
	}
	bp, ok := prov.(BuilderProvider)
	return bp, ok
}

// builderStagerFor resolves a builder name to its BuilderStager (nil, false for a
// builder that needs no host staging dir).
func builderStagerFor(name string) (BuilderStager, bool) {
	prov, ok := providerRegistry.ResolveBuilder(name)
	if !ok {
		return nil, false
	}
	st, ok := prov.(BuilderStager)
	return st, ok
}
