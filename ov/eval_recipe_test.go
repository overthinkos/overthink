package main

// harness_recipe_test.go — covers BOTH the slim `kind: recipe` (spec)
// and the runner-config `kind: score` introduced in the 2026-04 kind
// split. Recipe target validation moved to ResolveScoreTarget; recipes
// no longer carry runner fields.

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// HarnessScore — target validation + helpers
// ---------------------------------------------------------------------------

func TestResolveScoreTarget_ExactlyOnePod(t *testing.T) {
	s := &HarnessScore{Pod: "sample-pod"}
	k, n, err := ResolveScoreTarget(s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if k != TargetKindPod || n != "sample-pod" {
		t.Errorf("got (%s, %s), want (pod, sample-pod)", k, n)
	}
}

func TestResolveScoreTarget_ExactlyOneVM(t *testing.T) {
	s := &HarnessScore{VM: "my-vm"}
	k, n, err := ResolveScoreTarget(s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if k != TargetKindVM || n != "my-vm" {
		t.Errorf("got (%s, %s), want (vm, my-vm)", k, n)
	}
}

func TestResolveScoreTarget_HostRequiresDisposable(t *testing.T) {
	s := &HarnessScore{Host: true, Disposable: false}
	if _, _, err := ResolveScoreTarget(s); err == nil {
		t.Error("expected error when host: true without disposable: true")
	} else if !strings.Contains(err.Error(), "disposable: true") {
		t.Errorf("error message should mention disposable; got: %v", err)
	}

	s2 := &HarnessScore{Host: true, Disposable: true}
	k, n, err := ResolveScoreTarget(s2)
	if err != nil {
		t.Fatalf("host+disposable should succeed: %v", err)
	}
	if k != TargetKindHost || n != "" {
		t.Errorf("got (%s, %s), want (host, '')", k, n)
	}
}

func TestResolveScoreTarget_NoneSet(t *testing.T) {
	s := &HarnessScore{}
	if _, _, err := ResolveScoreTarget(s); err == nil {
		t.Error("expected error when none of pod/vm/host set")
	}
}

func TestResolveScoreTarget_MultipleSet(t *testing.T) {
	s := &HarnessScore{Pod: "p", VM: "v"}
	_, _, err := ResolveScoreTarget(s)
	if err == nil {
		t.Fatal("expected error when both pod and vm set")
	}
	msg := err.Error()
	if !strings.Contains(msg, "pod=") || !strings.Contains(msg, "vm=") {
		t.Errorf("error should list both fields; got: %s", msg)
	}
}

func TestNotesEnabled_DefaultTrue(t *testing.T) {
	s := &HarnessScore{}
	if !s.NotesEnabled() {
		t.Error("default NotesEnabled() should be true")
	}
	f := false
	s2 := &HarnessScore{Notes: &f}
	if s2.NotesEnabled() {
		t.Error("explicit notes: false should disable")
	}
	tr := true
	s3 := &HarnessScore{Notes: &tr}
	if !s3.NotesEnabled() {
		t.Error("explicit notes: true should enable")
	}
}

func TestEffectiveMCPEndpoint_DefaultAndDisable(t *testing.T) {
	s := &HarnessScore{}
	if got := s.EffectiveMCPEndpoint(); got != DefaultMCPEndpoint {
		t.Errorf("default mcp_endpoint should be %q, got %q", DefaultMCPEndpoint, got)
	}
	empty := ""
	s2 := &HarnessScore{MCPEndpoint: &empty}
	if got := s2.EffectiveMCPEndpoint(); got != "" {
		t.Errorf("explicit empty mcp_endpoint should disable (got %q)", got)
	}
	custom := "http://example.com/mcp"
	s3 := &HarnessScore{MCPEndpoint: &custom}
	if got := s3.EffectiveMCPEndpoint(); got != custom {
		t.Errorf("custom mcp_endpoint should pass through (got %q)", got)
	}
}

func TestResolveRecipe_NotFound(t *testing.T) {
	cat := map[string]*HarnessRecipe{
		"foo": {Description: nil, Scenario: nil},
	}
	if _, err := ResolveRecipe(cat, "bar"); err == nil {
		t.Error("expected error for missing recipe")
	}
}

func TestResolveScore_NotFound(t *testing.T) {
	cat := map[string]*HarnessScore{
		"foo": {Pod: "p", PlateauIteration: 3, Recipe: []string{"x"}},
	}
	if _, err := ResolveScore(cat, "bar"); err == nil {
		t.Error("expected error for missing score")
	}
}

// ---------------------------------------------------------------------------
// ResolveScoreRecipe — merging + per-scenario SourceRecipe stamping
// ---------------------------------------------------------------------------

func TestResolveScoreRecipes_MergesInOrderAndStamps(t *testing.T) {
	recipes := map[string]*HarnessRecipe{
		"easy": {
			Scenario: []Scenario{{Name: "s1"}, {Name: "s2"}},
		},
		"hard": {
			Scenario: []Scenario{{Name: "s3"}},
		},
	}
	score := &HarnessScore{Recipe: []string{"easy", "hard"}}
	merged, resolved, err := ResolveScoreRecipe(score, recipes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := len(merged); got != 3 {
		t.Fatalf("merged: got %d scenarios, want 3", got)
	}
	wantOrder := []string{"s1", "s2", "s3"}
	wantSrc := []string{"easy", "easy", "hard"}
	for i, sc := range merged {
		if sc.Name != wantOrder[i] {
			t.Errorf("merged[%d].Name = %q, want %q", i, sc.Name, wantOrder[i])
		}
		if sc.SourceRecipe != wantSrc[i] {
			t.Errorf("merged[%d].SourceRecipe = %q, want %q", i, sc.SourceRecipe, wantSrc[i])
		}
	}
	if len(resolved) != 2 {
		t.Errorf("resolved: got %d, want 2", len(resolved))
	}
}

func TestResolveScoreRecipes_EmptyList(t *testing.T) {
	score := &HarnessScore{Recipe: []string{}}
	if _, _, err := ResolveScoreRecipe(score, map[string]*HarnessRecipe{}); err == nil {
		t.Error("expected error for empty recipes list")
	}
}

func TestResolveScoreRecipes_DuplicateRejected(t *testing.T) {
	recipes := map[string]*HarnessRecipe{"a": {Scenario: []Scenario{{Name: "x"}}}}
	score := &HarnessScore{Recipe: []string{"a", "a"}}
	_, _, err := ResolveScoreRecipe(score, recipes)
	if err == nil {
		t.Fatal("expected error on duplicate recipe name")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("error should mention 'duplicate'; got: %v", err)
	}
}

func TestResolveScoreRecipes_UnresolvedRejected(t *testing.T) {
	score := &HarnessScore{Recipe: []string{"missing"}}
	_, _, err := ResolveScoreRecipe(score, map[string]*HarnessRecipe{})
	if err == nil {
		t.Fatal("expected error on unresolved recipe name")
	}
}
