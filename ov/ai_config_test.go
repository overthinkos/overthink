package main

import (
	"strings"
	"testing"
)

func TestResolveAI_NoAIs(t *testing.T) {
	if _, _, err := ResolveAI(nil, ""); err != ErrNoAIs {
		t.Errorf("expected ErrNoAIs, got %v", err)
	}
	if _, _, err := ResolveAI(map[string]*AIConfig{}, ""); err != ErrNoAIs {
		t.Errorf("expected ErrNoAIs for empty map, got %v", err)
	}
}

func TestResolveAI_SoleAIImplicit(t *testing.T) {
	cat := map[string]*AIConfig{
		"claude": {Command: []string{"claude"}},
	}
	ai, name, err := ResolveAI(cat, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "claude" {
		t.Errorf("name=%q, want claude", name)
	}
	if ai.Timeout != DefaultAITimeout {
		t.Errorf("default timeout not applied: got %q", ai.Timeout)
	}
	if ai.PromptVia != "argv" {
		t.Errorf("default prompt_via not applied: got %q", ai.PromptVia)
	}
}

func TestResolveAI_MultipleAmbiguous(t *testing.T) {
	cat := map[string]*AIConfig{
		"a": {Command: []string{"a"}},
		"b": {Command: []string{"b"}},
	}
	_, _, err := ResolveAI(cat, "")
	if err == nil {
		t.Fatal("expected ambiguity error")
	}
	if !strings.Contains(err.Error(), "--ai NAME") {
		t.Errorf("error message should suggest --ai; got: %s", err)
	}
}

func TestResolveAI_NotFound(t *testing.T) {
	cat := map[string]*AIConfig{"claude": {Command: []string{"claude"}}}
	_, _, err := ResolveAI(cat, "missing")
	if err == nil {
		t.Fatal("expected not-found error")
	}
}

func TestSortedAINames(t *testing.T) {
	cat := map[string]*AIConfig{
		"zebra": {},
		"alpha": {},
		"mike":  {},
	}
	got := SortedAINames(cat)
	want := []string{"alpha", "mike", "zebra"}
	if len(got) != len(want) {
		t.Fatalf("len mismatch: got %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("[%d] got %q, want %q", i, got[i], w)
		}
	}
}

func TestVersionResultString(t *testing.T) {
	v := VersionResult{Stdout: "claude 0.1.2"}
	if v.String() != "claude 0.1.2" {
		t.Errorf("got %q", v.String())
	}
	v2 := VersionResult{Error: "command not found"}
	if v2.String() != "error: command not found" {
		t.Errorf("got %q", v2.String())
	}
}

func TestParseAITimeout(t *testing.T) {
	d, err := ParseAITimeout("")
	if err != nil {
		t.Fatalf("default timeout failed: %v", err)
	}
	if d.Minutes() != 30 {
		t.Errorf("default 30m parse: got %v", d)
	}
	d2, err := ParseAITimeout("5m")
	if err != nil || d2.Minutes() != 5 {
		t.Errorf("explicit timeout: got %v, err=%v", d2, err)
	}
	if _, err := ParseAITimeout("nope"); err == nil {
		t.Error("invalid duration should error")
	}
}
