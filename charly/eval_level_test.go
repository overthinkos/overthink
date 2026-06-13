package main

import (
	"strings"
	"testing"
)

// The ladder ordering + default are load-bearing for the bed-runner depth
// dispatch — a regression here would silently change how deep every bed runs.
func TestEvalLevelReaches(t *testing.T) {
	cases := []struct {
		have, want string
		reaches    bool
	}{
		{EvalLevelNone, EvalLevelBuild, false},
		{EvalLevelBuild, EvalLevelBuild, true},
		{EvalLevelBuild, EvalLevelNoAgent, false},
		{EvalLevelNoAgent, EvalLevelNoAgent, true},
		{EvalLevelNoAgent, EvalLevelAgent, false},
		{EvalLevelAgent, EvalLevelAgent, true},
		{EvalLevelAgent, EvalLevelBuild, true},
		{"", EvalLevelNoAgent, true}, // empty resolves to the noagent default
		{"", EvalLevelAgent, false},  // ...which does NOT reach agent
		{"", EvalLevelBuild, true},   // ...but does reach build
	}
	for _, c := range cases {
		if got := EvalLevelReaches(c.have, c.want); got != c.reaches {
			t.Errorf("EvalLevelReaches(%q, %q) = %v, want %v", c.have, c.want, got, c.reaches)
		}
	}
}

func TestResolveEvalLevel_DefaultsToNoAgent(t *testing.T) {
	if got := ResolveEvalLevel(""); got != EvalLevelNoAgent {
		t.Errorf("ResolveEvalLevel(\"\") = %q, want %q", got, EvalLevelNoAgent)
	}
	if got := ResolveEvalLevel("agent"); got != EvalLevelAgent {
		t.Errorf("ResolveEvalLevel(\"agent\") = %q, want %q", got, EvalLevelAgent)
	}
}

func TestIsValidEvalLevel(t *testing.T) {
	for _, ok := range []string{"none", "build", "noagent", "agent"} {
		if !IsValidEvalLevel(ok) {
			t.Errorf("IsValidEvalLevel(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", "full", "deploy", "AGENT"} {
		if IsValidEvalLevel(bad) {
			t.Errorf("IsValidEvalLevel(%q) = true, want false", bad)
		}
	}
}

// The eval_level capability label must round-trip: emitted from BoxConfig at
// build (normalized via ResolveEvalLevel), parsed back into BoxMetadata at
// deploy. Mirrors the deleted LabelEval round-trip coverage.
func TestExtractMetadata_EvalLevel(t *testing.T) {
	orig := InspectLabels
	defer func() { InspectLabels = orig }()

	InspectLabels = func(engine, imageRef string) (map[string]string, error) {
		return map[string]string{
			LabelVersion:   "1",
			LabelBox:       "x",
			LabelEvalLevel: "agent",
		}, nil
	}
	meta, err := ExtractMetadata("podman", "x")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if meta.EvalLevel != "agent" {
		t.Errorf("meta.EvalLevel = %q, want agent", meta.EvalLevel)
	}
}

// validateBuild rejects an out-of-ladder eval_level value.
func TestValidate_RejectsBadEvalLevel(t *testing.T) {
	cfg := &Config{Box: map[string]BoxConfig{
		"img": {Enabled: boolPtr(true), EvalLevel: "verbose"},
	}}
	errs := &ValidationError{}
	validateBuildAndDistro(cfg, &DistroConfig{}, errs)
	if !errs.HasErrors() || !strings.Contains(errs.Error(), "eval_level") {
		t.Errorf("expected eval_level rejection, got: %v", errs.Error())
	}
}
