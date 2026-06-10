package main

import (
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/alecthomas/kong"
)

// TestFoldPeers_FoldsTopLevelAndInheritsDisposability verifies a peer is
// registered as a top-level addressable Deploy entry, PeerOf points at the
// owner, and a disposable owner's disposability is inherited (so a kind:eval
// bed's destroy+rebuild is authorized to tear the peer down too).
func TestFoldPeers_FoldsTopLevelAndInheritsDisposability(t *testing.T) {
	uf := &UnifiedFile{Deploy: map[string]DeploymentNode{
		"eval-cross-pod-cdp": {
			Target:     "pod",
			Box:        "web",
			Disposable: ptrBool(true),
			Peer: map[string]*DeploymentNode{
				"chrome": {Target: "pod", Box: "chrome-headless"},
			},
		},
	}}
	if err := foldPeers(uf); err != nil {
		t.Fatalf("foldPeers: %v", err)
	}
	peer, ok := uf.Deploy["chrome"]
	if !ok {
		t.Fatalf("peer 'chrome' was not folded into the Deploy map: %v", deployKeysList(uf.Deploy))
	}
	if peer.PeerOf != "eval-cross-pod-cdp" {
		t.Errorf("peer.PeerOf = %q, want eval-cross-pod-cdp", peer.PeerOf)
	}
	if peer.Box != "chrome-headless" {
		t.Errorf("peer.Image = %q, want chrome-headless", peer.Box)
	}
	if !peer.IsDisposable() {
		t.Errorf("folded peer should inherit the disposable owner's disposability")
	}
}

// TestFoldPeers_NonDisposableOwnerDoesNotForceDisposable: a peer of a
// non-disposable owner is NOT auto-promoted to disposable (no autonomy granted).
func TestFoldPeers_NonDisposableOwnerDoesNotForceDisposable(t *testing.T) {
	uf := &UnifiedFile{Deploy: map[string]DeploymentNode{
		"prod": {
			Target: "pod",
			Box:    "web",
			Peer:   map[string]*DeploymentNode{"sidecar": {Target: "pod", Box: "chrome-headless"}},
		},
	}}
	if err := foldPeers(uf); err != nil {
		t.Fatalf("foldPeers: %v", err)
	}
	if uf.Deploy["sidecar"].IsDisposable() {
		t.Errorf("peer of a non-disposable owner must not be disposable")
	}
}

// TestFoldPeers_CollisionIsError: a peer name colliding with an existing
// deploy/bed/peer entry is a hard error (globally-unique peer names).
func TestFoldPeers_CollisionIsError(t *testing.T) {
	uf := &UnifiedFile{Deploy: map[string]DeploymentNode{
		"web": {Target: "pod", Box: "web"},
		"bed": {Target: "pod", Box: "web", Peer: map[string]*DeploymentNode{"web": {Target: "pod", Box: "chrome-headless"}}},
	}}
	err := foldPeers(uf)
	if err == nil || !strings.Contains(err.Error(), "collides") {
		t.Fatalf("expected a collision error, got %v", err)
	}
}

// TestFoldPeers_EmptyPeerIsError: a nil peer node is rejected.
func TestFoldPeers_EmptyPeerIsError(t *testing.T) {
	uf := &UnifiedFile{Deploy: map[string]DeploymentNode{
		"bed": {Target: "pod", Box: "web", Peer: map[string]*DeploymentNode{"chrome": nil}},
	}}
	if err := foldPeers(uf); err == nil {
		t.Fatalf("expected an error for a nil peer node")
	}
}

// TestValidatePeers_BadTarget rejects an unsupported peer target kind.
func TestValidatePeers_BadTarget(t *testing.T) {
	uf := &UnifiedFile{Deploy: map[string]DeploymentNode{
		"bed": {Target: "pod", Box: "web", Peer: map[string]*DeploymentNode{
			"chrome": {Target: "bogus", Box: "chrome-headless"},
		}},
	}}
	if err := validatePeers(uf); err == nil || !strings.Contains(err.Error(), "unsupported target") {
		t.Fatalf("expected unsupported-target error, got %v", err)
	}
}

// TestValidatePeers_DottedKeyRejected: a peer key with a dot collides with the
// nested dotted-path addressing grammar.
func TestValidatePeers_DottedKeyRejected(t *testing.T) {
	uf := &UnifiedFile{Deploy: map[string]DeploymentNode{
		"bed": {Target: "pod", Box: "web", Peer: map[string]*DeploymentNode{
			"a.b": {Target: "pod", Box: "chrome-headless"},
		}},
	}}
	if err := validatePeers(uf); err == nil {
		t.Fatalf("expected a dotted-key rejection")
	}
}

// TestIsPodPeer covers the pod-vs-other routing used by bringUp/tearDownPeers.
func TestIsPodPeer(t *testing.T) {
	if !isPodPeer(&DeploymentNode{Target: ""}) || !isPodPeer(&DeploymentNode{Target: "pod"}) {
		t.Errorf("empty/pod target should be a pod peer")
	}
	if isPodPeer(&DeploymentNode{Target: "vm"}) || isPodPeer(&DeploymentNode{Target: "local"}) {
		t.Errorf("vm/local target should NOT be a pod peer")
	}
}

// TestSortedPeerKeys is deterministic ascending order.
func TestSortedPeerKeys(t *testing.T) {
	got := sortedPeerKeys(map[string]*DeploymentNode{"c": {}, "a": {}, "b": {}})
	if want := []string{"a", "b", "c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("sortedPeerKeys = %v, want %v", got, want)
	}
}

// TestTearDownPeers_RoutingAndOrder: tearDownPeers iterates peers in sorted
// order and routes a pod peer to `charly remove --purge`, a non-pod peer to
// `charly deploy del --assume-yes` — the same iteration/routing logic bringUpPeers
// uses, verified here with the stubbable runCharlySubcommand package var (no side
// effects). The flag itself is proven valid against real Kong parsing by
// TestDeployDelArgv_KongAccepts (this stub-based test cannot — it never invokes
// flag parsing, which is exactly how a `--yes`/`--force` drift once slipped through).
func TestTearDownPeers_RoutingAndOrder(t *testing.T) {
	orig := runCharlySubcommand
	defer func() { runCharlySubcommand = orig }()
	var calls [][]string
	runCharlySubcommand = func(args ...string) error {
		calls = append(calls, args)
		return nil
	}
	node := &DeploymentNode{Peer: map[string]*DeploymentNode{
		"zeta-pod":   {Target: "pod"},
		"alpha-host": {Target: "local"},
	}}
	tearDownPeers(node)
	want := [][]string{
		deployDelArgv("alpha-host"),       // sorted first; non-pod → deploy del --assume-yes (unattended)
		{"remove", "zeta-pod", "--purge"}, // pod → remove --purge
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("tearDownPeers calls = %v, want %v", calls, want)
	}
}

// TestTearDownPeers_NoPeersNoop: nothing happens when there are no peers.
func TestTearDownPeers_NoPeersNoop(t *testing.T) {
	orig := runCharlySubcommand
	defer func() { runCharlySubcommand = orig }()
	called := false
	runCharlySubcommand = func(args ...string) error { called = true; return nil }
	tearDownPeers(&DeploymentNode{})
	if called {
		t.Errorf("tearDownPeers ran a subcommand for a node with no peers")
	}
}

func deployKeysList(m map[string]DeploymentNode) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestDeployDelArgv_KongAccepts proves deployDelArgv emits a flag the REAL
// `charly deploy del` Kong grammar accepts, and that the two historically-wrong
// flags are rejected. The stub-based TestTearDownPeers_RoutingAndOrder asserts
// arg strings without ever invoking Kong, so it CANNOT catch a flag the binary
// rejects — which is exactly how `--yes` (and `--force` at the ephemeral/reap
// call sites) shipped while silently aborting teardown at arg-parse and leaking
// the resource. This test exercises real flag parsing so the drift can never
// silently re-land.
func TestDeployDelArgv_KongAccepts(t *testing.T) {
	type deployGrammar struct {
		Deploy struct {
			Del DeployDelCmd `cmd:""`
		} `cmd:""`
	}
	parse := func(args ...string) error {
		var g deployGrammar
		k, err := kong.New(&g, kong.Name("charly"), kong.Exit(func(int) {}), kong.Writers(io.Discard, io.Discard))
		if err != nil {
			t.Fatalf("kong.New: %v", err)
		}
		_, err = k.Parse(args)
		return err
	}
	// The helper every programmatic teardown builds its command through must
	// parse cleanly against the real grammar.
	if err := parse(deployDelArgv("x")...); err != nil {
		t.Errorf("deployDelArgv produced args `charly deploy del` rejects: %v (args=%v)", err, deployDelArgv("x"))
	}
	// -y is the valid short form.
	if err := parse("deploy", "del", "x", "-y"); err != nil {
		t.Errorf("`charly deploy del -y` should be accepted, got: %v", err)
	}
	// The two flags wrongly used at call sites MUST be rejected (regression guard).
	for _, bad := range []string{"--yes", "--force"} {
		if err := parse("deploy", "del", "x", bad); err == nil {
			t.Errorf("`charly deploy del %s` must be REJECTED by Kong (it silently aborted teardown)", bad)
		}
	}
}
