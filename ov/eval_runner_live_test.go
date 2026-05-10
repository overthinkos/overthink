package main

import (
	"context"
	"strings"
	"testing"
)

// TestFilterOutCyclic_RemovesMatchingKeysPreservesOrder verifies the
// helper drops scenarios whose scenarioKey is in cyclicKeys, while
// preserving the declaration order of the kept scenarios.
func TestFilterOutCyclic_RemovesMatchingKeysPreservesOrder(t *testing.T) {
	scs := []Scenario{
		{Name: "a", SourceRecipe: "r1", Pod: "p"},
		{Name: "b", SourceRecipe: "r1", Pod: "p"},
		{Name: "c", SourceRecipe: "r1", Pod: "p"},
		{Name: "d", SourceRecipe: "r2", Pod: "q"},
	}
	cyclicKeys := map[scenarioKey]bool{
		{recipe: "r1", name: "b"}: true,
		{recipe: "r2", name: "d"}: true,
	}
	got := filterOutCyclic(scs, cyclicKeys)
	if len(got) != 2 {
		t.Fatalf("want 2 scenarios kept, got %d", len(got))
	}
	if got[0].Name != "a" || got[1].Name != "c" {
		t.Errorf("order not preserved: got %v", []string{got[0].Name, got[1].Name})
	}
}

func TestFilterOutCyclic_EmptyCyclicReturnsInput(t *testing.T) {
	scs := []Scenario{{Name: "a"}, {Name: "b"}}
	got := filterOutCyclic(scs, nil)
	if len(got) != 2 {
		t.Errorf("empty cyclicKeys should pass-through; got %d scenarios", len(got))
	}
}

func TestFilterOutCyclic_RecipeScopeMatters(t *testing.T) {
	// Two scenarios share Name="x" but different SourceRecipe. Marking
	// only r1's "x" as cyclic must not also drop r2's "x".
	scs := []Scenario{
		{Name: "x", SourceRecipe: "r1", Pod: "p"},
		{Name: "x", SourceRecipe: "r2", Pod: "q"},
	}
	cyclic := map[scenarioKey]bool{{recipe: "r1", name: "x"}: true}
	got := filterOutCyclic(scs, cyclic)
	if len(got) != 1 || got[0].SourceRecipe != "r2" {
		t.Errorf("recipe-scoped filter failed: kept %v", scenarioKeysOf(got))
	}
}

func scenarioKeysOf(scs []Scenario) []scenarioKey {
	out := make([]scenarioKey, len(scs))
	for i, sc := range scs {
		out[i] = keyOf(sc)
	}
	return out
}

// TestRunRecipeScenariosLive_PureCycleEmitsFailVerdictsNoPropagation
// exercises the post-Fix-D behavior where a *CycleError no longer
// wipes the entire phase. With every scenario in a cycle, the
// non-cyclic subset is empty (no podman exec needed), so the
// function returns a EvalRunResults whose Scenario slice contains
// one fail verdict per cyclic scenario with SkippedReason starting
// with "cycle:".
func TestRunRecipeScenariosLive_PureCycleEmitsFailVerdictsNoPropagation(t *testing.T) {
	// A → B, B → A. Pure cycle.
	scenarios := []Scenario{
		{Name: "a", Pod: "test-pod", SourceRecipe: "r1", DependsOn: []string{"b"},
			Step: []Step{{Then: "x", Check: Check{File: "/a"}}}},
		{Name: "b", Pod: "test-pod", SourceRecipe: "r1", DependsOn: []string{"a"},
			Step: []Step{{Then: "y", Check: Check{File: "/b"}}}},
	}
	res, err := RunEvalLive(context.Background(), "", "test-score", scenarios, RunScoringOpts{})
	if err != nil {
		t.Fatalf("CycleError must NOT propagate per Fix D — got error: %v", err)
	}
	if res == nil {
		t.Fatalf("expected non-nil EvalRunResults even on pure cycle")
	}
	if len(res.Scenario) != 2 {
		t.Fatalf("Section-5 invariant: 2 cyclic scenarios → 2 fail verdicts, got %d", len(res.Scenario))
	}
	for _, sc := range res.Scenario {
		if sc.Status != "fail" {
			t.Errorf("scenario %q: status = %q, want fail", sc.Name, sc.Status)
		}
		if !strings.HasPrefix(sc.SkippedReason, "cycle:") {
			t.Errorf("scenario %q: SkippedReason = %q, want prefix 'cycle:'", sc.Name, sc.SkippedReason)
		}
	}
	if res.Summary.Fail != 2 || res.Summary.Total != 2 {
		t.Errorf("summary mismatch: total=%d fail=%d, want total=2 fail=2", res.Summary.Total, res.Summary.Fail)
	}
}

// TestRunRecipeScenariosLive_NonCycleEmptyInputReturnsEarly is a
// regression on the empty-input fast path.
func TestRunRecipeScenariosLive_NonCycleEmptyInputReturnsEarly(t *testing.T) {
	res, err := RunEvalLive(context.Background(), "", "test-score", nil, RunScoringOpts{})
	if err != nil {
		t.Fatalf("nil scenarios should not error: %v", err)
	}
	if res == nil || len(res.Scenario) != 0 {
		t.Errorf("nil scenarios should yield empty result; got %v", res)
	}
}
