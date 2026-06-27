package main

import (
	"strings"
	"testing"

	"github.com/overthinkos/overthink/charly/spec"
)

// TestReservedWordRegistry_KindBijection proves every CUE kind (spec.KindWords)
// has a registered in-proc KindProvider (the init() gate enforces this at process
// start; this re-runs it as a test) AND that the completeness check FAILS for a
// spec kind with no provider. The decode behaviour itself is covered by the
// *_RoundTrip tests (which now route through normalizeNodeInto → the registry).
func TestReservedWordRegistry_KindBijection(t *testing.T) {
	// Positive: every spec.KindWord resolves to a registered KindProvider.
	if err := checkKindProviderBijection(spec.KindWords); err != nil {
		t.Fatalf("live kind registry is not complete: %v", err)
	}

	// Negative — a CUE kind with NO provider: add a ghost kind word; the
	// completeness check must report it as missing. (Extra ClassKind providers —
	// out-of-tree plugin kinds — are intentionally allowed, so there is no
	// extra-provider failure case.)
	kindsPlusGhost := append(append([]string{}, spec.KindWords...), "ghostkind")
	err := checkKindProviderBijection(kindsPlusGhost)
	if err == nil {
		t.Fatal("expected kind bijection to FAIL for a spec kind with no provider, got nil")
	}
	if !strings.Contains(err.Error(), "ghostkind") {
		t.Errorf("missing-provider error must name the unhandled kind; got: %v", err)
	}
}

// TestReservedWordRegistry_VerbBijection proves VerbCatalog ⇄ spec.OpVerbs is a
// bijection and that every verb is an authorable Op field, plus the failure paths.
func TestReservedWordRegistry_VerbBijection(t *testing.T) {
	if err := checkVerbBijection(VerbCatalog, spec.OpVerbs, spec.AuthoringVerbs); err != nil {
		t.Fatalf("live verb registry is not a bijection: %v", err)
	}

	// A CUE verb with no VerbCatalog handler must be reported.
	verbsPlusGhost := append(append([]string{}, spec.OpVerbs...), "ghostverb")
	// ghostverb is also not an authorable field, so it surfaces in two buckets;
	// the missing-handler bucket is what we assert on.
	if err := checkVerbBijection(VerbCatalog, verbsPlusGhost, spec.AuthoringVerbs); err == nil ||
		!strings.Contains(err.Error(), "ghostverb") {
		t.Fatalf("expected verb bijection to FAIL for a spec verb with no handler, got: %v", err)
	}

	// A verb absent from spec.AuthoringVerbs (i.e. CUE doesn't know it as an Op
	// field) must be reported as not-authorable.
	if err := checkVerbBijection(VerbCatalog, spec.OpVerbs, []string{"file"}); err == nil ||
		!strings.Contains(err.Error(), "not in spec.AuthoringVerbs") {
		t.Fatalf("expected verb bijection to FAIL when verbs are not authorable, got: %v", err)
	}
}

// TestReservedWordRegistry_MethodAllowlists proves every live-verb provider's method
// allowlist (LiveVerbProvider.Methods) equals spec.LiveVerbMethods and that drift is
// detected. The check reads each allowlist from the registered provider (E4 — no
// central liveVerbDispatch); drift is simulated by doctoring the passed CUE side.
func TestReservedWordRegistry_MethodAllowlists(t *testing.T) {
	// spec.LiveVerbMethods is EMPTY now: `wl` (the LAST in-proc live verb) externalized into
	// candy/plugin-wl, so there is no compiled-in LiveVerbProvider left to gate. The real
	// (empty) set must pass — the gate compares two empty sets.
	if err := checkMethodAllowlists(spec.LiveVerbMethods); err != nil {
		t.Fatalf("live method allowlists drifted from spec.LiveVerbMethods: %v", err)
	}

	// The gate still detects a phantom spec.LiveVerbMethods entry naming a verb that is NOT a
	// registered in-proc LiveVerbProvider: `file` is a compiled-in goss verb (it resolves but
	// has no in-proc live-verb method contract, or is not registered in this unit-test binary)
	// — either way the check reports it rather than silently passing. (An external grpcProvider
	// is not a LiveVerbProvider either, so this would also catch one wrongly placed in the set.)
	phantom := map[string][]string{"file": {"ghostmethod"}}
	if err := checkMethodAllowlists(phantom); err == nil ||
		!strings.Contains(err.Error(), "file") {
		t.Fatalf("expected method-allowlist check to FAIL on a spec.LiveVerbMethods verb with no in-proc LiveVerbProvider, got: %v", err)
	}
}

// TestReservedWordRegistry_KindsDispatchable proves every registered authoring
// kind is ACTUALLY handled by the loader's normalizeNodeInto dispatch — so the
// registry can never claim a handler that the dispatch switch lacks (the
// anti-drift link between the registry and the real code path).
func TestReservedWordRegistry_KindsDispatchable(t *testing.T) {
	for _, kind := range spec.KindWords {
		gn := &genericNode{name: "probe-" + kind, disc: kind, discClass: "entity"}
		uf := &UnifiedFile{}
		err := normalizeNodeInto(gn, uf)
		// A real handler arm may return a decode error on the empty probe node,
		// but it must NEVER return the "unsupported discriminator" sentinel — that
		// is the no-handler signal.
		if err != nil && strings.Contains(err.Error(), "unsupported discriminator") {
			t.Errorf("kind %q is registered but normalizeNodeInto has no handler: %v", kind, err)
		}
	}
}
