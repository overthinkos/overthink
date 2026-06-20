package main

import (
	"strings"
	"testing"

	"github.com/overthinkos/overthink/charly/spec"
)

// TestReservedWordRegistry_KindBijection proves the live registry is a bijection
// (the init() gate already enforces this at process start; this re-runs it as a
// test) AND that the completeness check FAILS when a handler is missing or extra.
func TestReservedWordRegistry_KindBijection(t *testing.T) {
	// Positive: the real registry is in bijection with spec.KindWords.
	if err := checkKindBijection(reservedKindHandlers, spec.KindWords); err != nil {
		t.Fatalf("live kind registry is not a bijection: %v", err)
	}

	// Negative 1 — a CUE kind with NO handler: add a temp unregistered kind word
	// to the spec side; the completeness check must report it as missing.
	kindsPlusGhost := append(append([]string{}, spec.KindWords...), "ghostkind")
	err := checkKindBijection(reservedKindHandlers, kindsPlusGhost)
	if err == nil {
		t.Fatal("expected kind bijection to FAIL for a spec kind with no handler, got nil")
	}
	if !strings.Contains(err.Error(), "ghostkind") {
		t.Errorf("missing-handler error must name the unhandled kind; got: %v", err)
	}

	// Negative 2 — a handler with NO CUE kind: register a temp handler whose word
	// is not in spec.KindWords; the check must report it as extra.
	handlersPlusGhost := map[string]string{"ghosthandler": "#Nope"}
	for k, v := range reservedKindHandlers {
		handlersPlusGhost[k] = v
	}
	err = checkKindBijection(handlersPlusGhost, spec.KindWords)
	if err == nil {
		t.Fatal("expected kind bijection to FAIL for a handler with no spec kind, got nil")
	}
	if !strings.Contains(err.Error(), "ghosthandler") {
		t.Errorf("extra-handler error must name the orphan handler; got: %v", err)
	}

	// Negative 3 — a real kind with its handler REMOVED: drop one entry and prove
	// the check reports exactly that kind as unhandled.
	dropped := map[string]string{}
	for k, v := range reservedKindHandlers {
		if k == "candy" {
			continue // simulate a forgotten handler
		}
		dropped[k] = v
	}
	err = checkKindBijection(dropped, spec.KindWords)
	if err == nil || !strings.Contains(err.Error(), "candy") {
		t.Fatalf("expected bijection failure naming the removed handler 'candy', got: %v", err)
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

// TestReservedWordRegistry_MethodAllowlists proves the 11 live-verb dispatch maps'
// keys equal spec.LiveVerbMethods and that drift is detected.
func TestReservedWordRegistry_MethodAllowlists(t *testing.T) {
	if err := checkMethodAllowlists(liveVerbDispatch, spec.LiveVerbMethods); err != nil {
		t.Fatalf("live method allowlists drifted from spec.LiveVerbMethods: %v", err)
	}

	// Inject a dispatch method that the CUE enum doesn't carry → must be reported.
	doctored := map[string]map[string]methodSpec{
		"cdp": {"ghostmethod": {path: []string{"cdp", "ghost"}}},
	}
	if err := checkMethodAllowlists(doctored, spec.LiveVerbMethods); err == nil ||
		!strings.Contains(err.Error(), "ghostmethod") {
		t.Fatalf("expected method-allowlist check to FAIL on an extra dispatch method, got: %v", err)
	}

	// Drop a CUE method from the dispatch map → must be reported as missing.
	cdpMinusOne := map[string]methodSpec{}
	for k, v := range cdpMethods {
		if k == "status" {
			continue
		}
		cdpMinusOne[k] = v
	}
	if err := checkMethodAllowlists(map[string]map[string]methodSpec{"cdp": cdpMinusOne}, spec.LiveVerbMethods); err == nil ||
		!strings.Contains(err.Error(), "status") {
		t.Fatalf("expected method-allowlist check to FAIL on a missing dispatch method, got: %v", err)
	}
}

// TestReservedWordRegistry_KindsDispatchable proves every registered authoring
// kind is ACTUALLY handled by the loader's normalizeNodeInto dispatch — so the
// registry can never claim a handler that the dispatch switch lacks (the
// anti-drift link between the registry and the real code path).
func TestReservedWordRegistry_KindsDispatchable(t *testing.T) {
	for kind := range reservedKindHandlers {
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
