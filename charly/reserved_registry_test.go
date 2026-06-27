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
	if err := checkMethodAllowlists(spec.LiveVerbMethods); err != nil {
		t.Fatalf("live method allowlists drifted from spec.LiveVerbMethods: %v", err)
	}

	// A CUE method the provider's allowlist lacks → reported as a CUE method with no
	// dispatch entry.
	withGhost := map[string][]string{}
	for k, v := range spec.LiveVerbMethods {
		withGhost[k] = v
	}
	withGhost["wl"] = append(append([]string{}, spec.LiveVerbMethods["wl"]...), "ghostmethod")
	if err := checkMethodAllowlists(withGhost); err == nil ||
		!strings.Contains(err.Error(), "ghostmethod") {
		t.Fatalf("expected method-allowlist check to FAIL on a CUE method with no dispatch entry, got: %v", err)
	}

	// A provider method dropped from the CUE side → reported as not-in-spec.
	minusStatus := map[string][]string{}
	for k, v := range spec.LiveVerbMethods {
		minusStatus[k] = v
	}
	wlNoStatus := []string{}
	for _, m := range spec.LiveVerbMethods["wl"] {
		if m == "status" {
			continue
		}
		wlNoStatus = append(wlNoStatus, m)
	}
	minusStatus["wl"] = wlNoStatus
	if err := checkMethodAllowlists(minusStatus); err == nil ||
		!strings.Contains(err.Error(), "status") {
		t.Fatalf("expected method-allowlist check to FAIL on a provider method dropped from the CUE side, got: %v", err)
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
