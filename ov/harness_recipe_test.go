package main

import (
	"strings"
	"testing"
)

func TestResolveRecipeTarget_ExactlyOnePod(t *testing.T) {
	r := &HarnessRecipe{Pod: "bench-pod"}
	k, n, err := ResolveRecipeTarget(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if k != TargetKindPod || n != "bench-pod" {
		t.Errorf("got (%s, %s), want (pod, bench-pod)", k, n)
	}
}

func TestResolveRecipeTarget_ExactlyOneVM(t *testing.T) {
	r := &HarnessRecipe{VM: "my-vm"}
	k, n, err := ResolveRecipeTarget(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if k != TargetKindVM || n != "my-vm" {
		t.Errorf("got (%s, %s), want (vm, my-vm)", k, n)
	}
}

func TestResolveRecipeTarget_HostRequiresDisposable(t *testing.T) {
	r := &HarnessRecipe{Host: true, Disposable: false}
	if _, _, err := ResolveRecipeTarget(r); err == nil {
		t.Error("expected error when host: true without disposable: true")
	} else if !strings.Contains(err.Error(), "disposable: true") {
		t.Errorf("error message should mention disposable; got: %v", err)
	}

	r2 := &HarnessRecipe{Host: true, Disposable: true}
	k, n, err := ResolveRecipeTarget(r2)
	if err != nil {
		t.Fatalf("host+disposable should succeed: %v", err)
	}
	if k != TargetKindHost || n != "" {
		t.Errorf("got (%s, %s), want (host, '')", k, n)
	}
}

func TestResolveRecipeTarget_NoneSet(t *testing.T) {
	r := &HarnessRecipe{}
	if _, _, err := ResolveRecipeTarget(r); err == nil {
		t.Error("expected error when none of pod/vm/host set")
	}
}

func TestResolveRecipeTarget_MultipleSet(t *testing.T) {
	r := &HarnessRecipe{Pod: "p", VM: "v"}
	_, _, err := ResolveRecipeTarget(r)
	if err == nil {
		t.Fatal("expected error when both pod and vm set")
	}
	msg := err.Error()
	if !strings.Contains(msg, "pod=") || !strings.Contains(msg, "vm=") {
		t.Errorf("error should list both fields; got: %s", msg)
	}
}

func TestNotesEnabled_DefaultTrue(t *testing.T) {
	r := &HarnessRecipe{}
	if !r.NotesEnabled() {
		t.Error("default NotesEnabled() should be true")
	}
	f := false
	r2 := &HarnessRecipe{Notes: &f}
	if r2.NotesEnabled() {
		t.Error("explicit notes: false should disable")
	}
	tr := true
	r3 := &HarnessRecipe{Notes: &tr}
	if !r3.NotesEnabled() {
		t.Error("explicit notes: true should enable")
	}
}

func TestEffectiveMCPEndpoint_DefaultAndDisable(t *testing.T) {
	r := &HarnessRecipe{}
	if got := r.EffectiveMCPEndpoint(); got != DefaultMCPEndpoint {
		t.Errorf("default mcp_endpoint should be %q, got %q", DefaultMCPEndpoint, got)
	}
	empty := ""
	r2 := &HarnessRecipe{MCPEndpoint: &empty}
	if got := r2.EffectiveMCPEndpoint(); got != "" {
		t.Errorf("explicit empty mcp_endpoint should disable (got %q)", got)
	}
	custom := "http://example.com/mcp"
	r3 := &HarnessRecipe{MCPEndpoint: &custom}
	if got := r3.EffectiveMCPEndpoint(); got != custom {
		t.Errorf("custom mcp_endpoint should pass through (got %q)", got)
	}
}

func TestResolveRecipe_NotFound(t *testing.T) {
	cat := map[string]*HarnessRecipe{
		"foo": {Pod: "pod-a"},
	}
	if _, err := ResolveRecipe(cat, "bar"); err == nil {
		t.Error("expected error for missing recipe")
	}
}
