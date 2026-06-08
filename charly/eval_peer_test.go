package main

import (
	"context"
	"reflect"
	"sort"
	"testing"
)

func TestSplitPeerKey(t *testing.T) {
	cases := []struct {
		key, name, arg string
		ok             bool
	}{
		{"PEER_HOST:web", "PEER_HOST", "web", true},
		{"PEER_ENDPOINT:web:8080", "PEER_ENDPOINT", "web:8080", true},
		{"PEER_HOST", "PEER_HOST", "", false},
	}
	for _, c := range cases {
		name, arg, ok := splitPeerKey(c.key)
		if name != c.name || arg != c.arg || ok != c.ok {
			t.Errorf("splitPeerKey(%q) = (%q,%q,%v), want (%q,%q,%v)", c.key, name, arg, ok, c.name, c.arg, c.ok)
		}
	}
}

// TestCollectPeerRefs scans every check string field for ${PEER_*} refs and
// returns exactly those (not other parameterized vars like ${HOST_PORT}).
func TestCollectPeerRefs(t *testing.T) {
	checks := []Check{
		{Cdp: "open", URL: "http://${PEER_HOST:web}:8080"},
		{Command: "curl http://${PEER_ENDPOINT:web:8080}/health"},
		{Addr: "127.0.0.1:${HOST_PORT:8080}"}, // NOT a peer ref
		{HTTP: "http://${PEER_HOST:web}/"},    // duplicate PEER_HOST:web
	}
	got := collectPeerRefs(checks)
	sort.Strings(got)
	want := []string{"PEER_ENDPOINT:web:8080", "PEER_HOST:web"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("collectPeerRefs = %v, want %v", got, want)
	}
}

// TestEffectiveEnv_PeerVarsOverlay: ${PEER_*} addresses overlay onto the active
// resolver's env in effectiveEnv — the single injection point that makes
// cross-deployment addressing work for the primary AND on:-swapped resolvers.
func TestEffectiveEnv_PeerVarsOverlay(t *testing.T) {
	r := &Runner{
		Resolver: &EvalVarResolver{Env: map[string]string{"USER": "user"}},
		PeerVars: map[string]string{"PEER_HOST:web": "charly-web"},
	}
	env := r.effectiveEnv()
	if env["USER"] != "user" {
		t.Errorf("base resolver var lost: %v", env)
	}
	if env["PEER_HOST:web"] != "charly-web" {
		t.Errorf("peer var not overlaid: %v", env)
	}
	// The base resolver's own map must stay clean (copy-on-overlay).
	if _, leaked := r.Resolver.Env["PEER_HOST:web"]; leaked {
		t.Errorf("effectiveEnv mutated the shared resolver Env")
	}
}

// TestEffectiveEnv_NoPeerVarsReturnsBase: with no PeerVars and no Scenario,
// effectiveEnv returns the resolver's map directly (behaviour unchanged).
func TestEffectiveEnv_NoPeerVarsReturnsBase(t *testing.T) {
	base := map[string]string{"USER": "user"}
	r := &Runner{Resolver: &EvalVarResolver{Env: base}}
	if got := r.effectiveEnv(); !reflect.DeepEqual(got, base) {
		t.Errorf("effectiveEnv = %v, want the base map %v", got, base)
	}
}

// TestIsRuntimeOnlyVar_Peer: the peer address vars are runtime-only, so a
// build-scope check can't reference them.
func TestIsRuntimeOnlyVar_Peer(t *testing.T) {
	for _, key := range []string{"PEER_HOST:web", "PEER_ENDPOINT:web:8080"} {
		if !IsRuntimeOnlyVar(key) {
			t.Errorf("%q should be runtime-only", key)
		}
	}
}

// TestFilterPeerVars: only ${PEER_HOST}/${PEER_ENDPOINT} keys are selected — the
// ones whose unresolution must FAIL (not skip) a check.
func TestFilterPeerVars(t *testing.T) {
	got := filterPeerVars([]string{"PEER_ENDPOINT:web:8080", "HOST_PORT:8080", "PEER_HOST:web", "USER"})
	want := []string{"PEER_ENDPOINT:web:8080", "PEER_HOST:web"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("filterPeerVars = %v, want %v", got, want)
	}
	if got := filterPeerVars([]string{"HOST_PORT:8080", "USER"}); len(got) != 0 {
		t.Errorf("filterPeerVars with no peer vars = %v, want empty", got)
	}
}

// TestRunOne_UnresolvedPeerVarFails: an unresolvable ${PEER_*} (peer/subject
// unreachable) FAILS the check — a SKIP there would be a fake pass on an
// unreachable dependency. A non-peer unresolved var stays a legitimate SKIP.
func TestRunOne_UnresolvedPeerVarFails(t *testing.T) {
	r := &Runner{Resolver: &EvalVarResolver{Env: map[string]string{}}}
	// ${PEER_ENDPOINT} can't be resolved → the cross-deployment probe's premise
	// failed → FAIL (never reaches the curl; returns at the var-resolution gate).
	peerCheck := &Check{Command: "curl -fsS http://${PEER_ENDPOINT:absent:80}/"}
	if res := r.runOne(context.Background(), peerCheck); res.Status != TestFail {
		t.Errorf("unresolved ${PEER_ENDPOINT} → status %v (%q), want TestFail", res.Status, res.Message)
	}
	// A non-peer unresolved var is a legitimate SKIP (input genuinely N/A here).
	otherCheck := &Check{Command: "echo ${SOME_UNSET_VAR}"}
	if res := r.runOne(context.Background(), otherCheck); res.Status != TestSkip {
		t.Errorf("unresolved non-peer var → status %v (%q), want TestSkip", res.Status, res.Message)
	}
}
