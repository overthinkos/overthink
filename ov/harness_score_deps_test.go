package main

import (
	"reflect"
	"testing"
)

func TestTopoSortByDeclarationOrder_LinearChain(t *testing.T) {
	scenarios := []Scenario{
		{Name: "a", Pod: "p"},
		{Name: "b", Pod: "p", DependsOn: []string{"a"}},
		{Name: "c", Pod: "p", DependsOn: []string{"b"}},
	}
	got, err := topoSortByDeclarationOrder(scenarios)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotNames := names(got)
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(gotNames, want) {
		t.Errorf("linear chain: got %v, want %v", gotNames, want)
	}
}

func TestTopoSortByDeclarationOrder_NoEdgesPreservesOrder(t *testing.T) {
	scenarios := []Scenario{
		{Name: "first", Pod: "p"},
		{Name: "second", Pod: "p"},
		{Name: "third", Pod: "p"},
	}
	got, err := topoSortByDeclarationOrder(scenarios)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"first", "second", "third"}
	if !reflect.DeepEqual(names(got), want) {
		t.Errorf("no-edge tie-break by declaration order: got %v, want %v", names(got), want)
	}
}

func TestTopoSortByDeclarationOrder_CrossPodDepReorders(t *testing.T) {
	// Recipe author wants set-from-client (decl order #2) BEFORE
	// readback-on-server (decl order #4) regardless of pod ordering.
	scenarios := []Scenario{
		{Name: "server-running", Pod: "redis"},
		{Name: "set-from-client", Pod: "redis-client"},
		{Name: "client-has-cli", Pod: "redis-client"},
		{Name: "readback-on-server", Pod: "redis", DependsOn: []string{"set-from-client"}},
	}
	got, err := topoSortByDeclarationOrder(scenarios)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Topo order respects: set-from-client BEFORE readback-on-server.
	// Within that, declaration order ties: server-running first
	// (no deps, decl idx 0), set-from-client (decl idx 1), client-has-cli (decl idx 2),
	// readback-on-server (decl idx 3, depends on set-from-client).
	want := []string{"server-running", "set-from-client", "client-has-cli", "readback-on-server"}
	if !reflect.DeepEqual(names(got), want) {
		t.Errorf("cross-pod dep reorder: got %v, want %v", names(got), want)
	}
}

func TestTopoSortByDeclarationOrder_DiamondDeps(t *testing.T) {
	// a -> b, a -> c, b+c -> d. Declaration order [a, b, c, d].
	// Expected output: a (no deps), b (only a passed), c (only a),
	// d (last). Tie-break b before c by declaration index.
	scenarios := []Scenario{
		{Name: "a", Pod: "p"},
		{Name: "b", Pod: "p", DependsOn: []string{"a"}},
		{Name: "c", Pod: "p", DependsOn: []string{"a"}},
		{Name: "d", Pod: "p", DependsOn: []string{"b", "c"}},
	}
	got, err := topoSortByDeclarationOrder(scenarios)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"a", "b", "c", "d"}
	if !reflect.DeepEqual(names(got), want) {
		t.Errorf("diamond: got %v, want %v", names(got), want)
	}
}

func TestTopoSortByDeclarationOrder_CycleReturnsCycleError(t *testing.T) {
	scenarios := []Scenario{
		{Name: "a", Pod: "p", DependsOn: []string{"b"}},
		{Name: "b", Pod: "p", DependsOn: []string{"a"}},
	}
	_, err := topoSortByDeclarationOrder(scenarios)
	if err == nil {
		t.Fatalf("expected CycleError, got nil")
	}
	if _, ok := err.(*CycleError); !ok {
		t.Fatalf("expected *CycleError, got %T: %v", err, err)
	}
}

func TestTopoSortByDeclarationOrder_DanglingDepIgnored(t *testing.T) {
	// A reference to a non-existent scenario is treated as missing
	// (validation surfaces the typo earlier; defensive: ignore here).
	scenarios := []Scenario{
		{Name: "a", Pod: "p", DependsOn: []string{"does-not-exist"}},
	}
	got, err := topoSortByDeclarationOrder(scenarios)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Name != "a" {
		t.Errorf("dangling dep should be ignored: got %v", names(got))
	}
}

func TestTopoSortByDeclarationOrder_Empty(t *testing.T) {
	got, err := topoSortByDeclarationOrder(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty input → empty output, got %v", got)
	}
}

func TestGroupConsecutiveByPod_TwoPodAlternation(t *testing.T) {
	scenarios := []Scenario{
		{Name: "1", Pod: "redis"},
		{Name: "2", Pod: "redis"},
		{Name: "3", Pod: "redis-client"},
		{Name: "4", Pod: "redis-client"},
		{Name: "5", Pod: "redis"},
	}
	buckets := groupConsecutiveByPod(scenarios)
	if len(buckets) != 3 {
		t.Fatalf("expected 3 buckets (redis, redis-client, redis), got %d", len(buckets))
	}
	if names(buckets[0]); !reflect.DeepEqual(names(buckets[0]), []string{"1", "2"}) {
		t.Errorf("bucket 0: got %v, want [1 2]", names(buckets[0]))
	}
	if !reflect.DeepEqual(names(buckets[1]), []string{"3", "4"}) {
		t.Errorf("bucket 1: got %v, want [3 4]", names(buckets[1]))
	}
	if !reflect.DeepEqual(names(buckets[2]), []string{"5"}) {
		t.Errorf("bucket 2: got %v, want [5]", names(buckets[2]))
	}
}

func TestGroupConsecutiveByPod_SinglePod(t *testing.T) {
	scenarios := []Scenario{
		{Name: "1", Pod: "p"},
		{Name: "2", Pod: "p"},
		{Name: "3", Pod: "p"},
	}
	buckets := groupConsecutiveByPod(scenarios)
	if len(buckets) != 1 {
		t.Fatalf("expected 1 bucket, got %d", len(buckets))
	}
	if !reflect.DeepEqual(names(buckets[0]), []string{"1", "2", "3"}) {
		t.Errorf("single pod: got %v, want [1 2 3]", names(buckets[0]))
	}
}

func TestGroupConsecutiveByPod_Empty(t *testing.T) {
	if got := groupConsecutiveByPod(nil); got != nil {
		t.Errorf("empty input → nil output, got %v", got)
	}
}

// names extracts scenario names from a slice for comparison.
func names(scs []Scenario) []string {
	out := make([]string, len(scs))
	for i, sc := range scs {
		out[i] = sc.Name
	}
	return out
}

// ----------------------------------------------------------------------
// firstUnmetDep — dep-skip-cascade logic
// ----------------------------------------------------------------------

func TestFirstUnmetDep_NoDepsReturnsEmpty(t *testing.T) {
	sc := Scenario{Name: "x"}
	if blocked := firstUnmetDep(sc, map[string]string{}); blocked != "" {
		t.Errorf("scenario with no deps should never be blocked, got %q", blocked)
	}
}

func TestFirstUnmetDep_AllDepsPassReturnsEmpty(t *testing.T) {
	sc := Scenario{Name: "x", DependsOn: []string{"a", "b"}}
	verdicts := map[string]string{"a": "pass", "b": "pass"}
	if blocked := firstUnmetDep(sc, verdicts); blocked != "" {
		t.Errorf("all deps pass → not blocked, got %q", blocked)
	}
}

func TestFirstUnmetDep_FirstFailedDepIsBlocking(t *testing.T) {
	sc := Scenario{Name: "x", DependsOn: []string{"a", "b", "c"}}
	verdicts := map[string]string{"a": "pass", "b": "fail", "c": "pass"}
	if blocked := firstUnmetDep(sc, verdicts); blocked != "b" {
		t.Errorf("expected blocked='b' (first non-pass dep), got %q", blocked)
	}
}

func TestFirstUnmetDep_SkippedDepCascades(t *testing.T) {
	// Transitive cascade: A failed → B was skipped → C depends on B,
	// gets skipped too. firstUnmetDep should report "b" because
	// verdictByName[b] == "skipped".
	sc := Scenario{Name: "c", DependsOn: []string{"b"}}
	verdicts := map[string]string{"a": "fail", "b": "skipped"}
	if blocked := firstUnmetDep(sc, verdicts); blocked != "b" {
		t.Errorf("skipped dep should block dependent, got %q", blocked)
	}
}

func TestFirstUnmetDep_UnknownDepBlocks(t *testing.T) {
	// Defensive: a dep not yet in verdictByName means topo-sort
	// processed out of order (or scoring is racing). Block — safer
	// than running unblocked. validateHarnessSemantics catches
	// dangling deps at load time, so this branch shouldn't fire in
	// well-formed configs.
	sc := Scenario{Name: "x", DependsOn: []string{"missing"}}
	verdicts := map[string]string{}
	if blocked := firstUnmetDep(sc, verdicts); blocked != "missing" {
		t.Errorf("unknown dep should block, got %q", blocked)
	}
}
