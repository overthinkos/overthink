package main

import (
	"strings"
	"testing"
)

// fixture builders -----------------------------------------------------------

func fxLayer(name string, checks []Check) *Layer {
	return &Layer{Name: name, tests: checks}
}

func fxCheckFile(id, path string) Check {
	t := true
	return Check{ID: id, File: path, Exists: &t}
}

func fxCheckCommand(id, cmd string) Check {
	z := 0
	return Check{ID: id, Command: cmd, ExitStatus: &z}
}

// fxCheckLiveOnly returns a check whose verb requires live-container
// infrastructure (cdp). filterDropLiveOnly should remove it.
func fxCheckLiveOnly(id string) Check {
	return Check{ID: id, Cdp: "status"}
}

// fxUnified builds a minimal UnifiedFile populated with the given
// images / pods / vms. Layers are passed separately because they go
// through the projected-layers map, not uf.Layers.
func fxUnified() *UnifiedFile {
	return &UnifiedFile{
		Version: 4,
		Images:  map[string]ImageConfig{},
		Pod:     map[string]*PodSpec{},
		VM:      map[string]*VmSpec{},
		Recipe:  map[string]*HarnessRecipe{},
		Score:   map[string]*HarnessScore{},
	}
}

// per-kind expansion ---------------------------------------------------------

func TestExpandFromLayer(t *testing.T) {
	uf := fxUnified()
	layers := map[string]*Layer{
		"sshd": fxLayer("sshd", []Check{
			fxCheckFile("sshd-binary", "/usr/sbin/sshd"),
			fxCheckFile("sshd-wrapper", "/usr/local/bin/sshd-wrapper"),
		}),
	}
	recipe := &HarnessRecipe{
		From: []HarnessRecipeFrom{
			{Kind: "layer", Name: "sshd", Pod: "selftest"},
		},
	}
	if err := ExpandRecipeFrom(uf, layers, "test", recipe); err != nil {
		t.Fatalf("ExpandRecipeFrom returned error: %v", err)
	}
	if len(recipe.Scenario) != 2 {
		t.Fatalf("want 2 scenarios, got %d", len(recipe.Scenario))
	}
	for _, sc := range recipe.Scenario {
		if sc.Pod != "selftest" {
			t.Errorf("scenario %q: pod = %q, want %q", sc.Name, sc.Pod, "selftest")
		}
		if len(sc.Steps) != 1 {
			t.Errorf("scenario %q: expected 1 step (Section-5 invariant), got %d", sc.Name, len(sc.Steps))
		}
	}
	if recipe.Scenario[0].Name != "sshd-binary" || recipe.Scenario[1].Name != "sshd-wrapper" {
		t.Errorf("unexpected scenario names: %q, %q", recipe.Scenario[0].Name, recipe.Scenario[1].Name)
	}
	if len(recipe.From) != 0 {
		t.Errorf("from: should be cleared after expansion, got %d entries", len(recipe.From))
	}
}

func TestExpandFromImage(t *testing.T) {
	uf := fxUnified()
	uf.Images["arch-coder"] = ImageConfig{
		Eval:  []Check{
			fxCheckCommand("arch-coder-ov", "test -x /usr/local/bin/ov"),
		},
		DeployEval:  []Check{
			fxCheckCommand("arch-coder-ov-version", "ov version"),
		},
	}
	layers := map[string]*Layer{}
	recipe := &HarnessRecipe{
		From: []HarnessRecipeFrom{
			{Kind: "image", Name: "arch-coder", Pod: "selftest-img"},
		},
	}
	if err := ExpandRecipeFrom(uf, layers, "test", recipe); err != nil {
		t.Fatalf("ExpandRecipeFrom returned error: %v", err)
	}
	if len(recipe.Scenario) != 2 {
		t.Fatalf("want 2 scenarios (one image, one deploy), got %d", len(recipe.Scenario))
	}
}

func TestExpandFromPod(t *testing.T) {
	uf := fxUnified()
	uf.Pod["redis"] = &PodSpec{
		Eval:  []Check{fxCheckFile("redis-binary", "/usr/bin/redis-server")},
		DeployEval:  []Check{fxCheckCommand("redis-ping", "redis-cli ping")},
	}
	layers := map[string]*Layer{}
	recipe := &HarnessRecipe{
		From: []HarnessRecipeFrom{
			{Kind: "pod", Name: "redis", Pod: "selftest-pod"},
		},
	}
	if err := ExpandRecipeFrom(uf, layers, "test", recipe); err != nil {
		t.Fatalf("ExpandRecipeFrom returned error: %v", err)
	}
	if len(recipe.Scenario) != 2 {
		t.Fatalf("want 2 scenarios from pod tests + deploy_tests, got %d", len(recipe.Scenario))
	}
}

func TestExpandFromVM(t *testing.T) {
	uf := fxUnified()
	uf.VM["arch-vm"] = &VmSpec{
		Eval:        []Check{fxCheckCommand("vm-id", "id")},
		DeployEval:  []Check{fxCheckCommand("vm-uptime", "uptime")},
	}
	layers := map[string]*Layer{}
	recipe := &HarnessRecipe{
		From: []HarnessRecipeFrom{
			{Kind: "vm", Name: "arch-vm", Pod: "selftest-vm"},
		},
	}
	if err := ExpandRecipeFrom(uf, layers, "test", recipe); err != nil {
		t.Fatalf("ExpandRecipeFrom returned error: %v", err)
	}
	if len(recipe.Scenario) != 2 {
		t.Fatalf("want 2 scenarios from vm tests + deploy_tests, got %d", len(recipe.Scenario))
	}
}

// composition / multi-kind ---------------------------------------------------

func TestMultiKindComposition(t *testing.T) {
	uf := fxUnified()
	uf.Images["img-a"] = ImageConfig{
		Eval:  []Check{fxCheckCommand("img-a-test", "true")},
	}
	uf.Pod["pod-a"] = &PodSpec{
		Eval:  []Check{fxCheckFile("pod-a-test", "/etc/foo")},
	}
	layers := map[string]*Layer{
		"layer-a": fxLayer("layer-a", []Check{fxCheckFile("layer-a-test", "/etc/bar")}),
	}
	recipe := &HarnessRecipe{
		From: []HarnessRecipeFrom{
			{Kind: "layer", Name: "layer-a", Pod: "container-x"},
			{Kind: "image", Name: "img-a", Pod: "container-x"},
			{Kind: "pod", Name: "pod-a", Pod: "container-y"},
		},
		Scenario: []Scenario{
			{
				Name: "handwritten",
				Pod:  "container-x",
				Steps: []Step{
					{Then: "marker exists", Check: fxCheckFile("handwritten", "/etc/baz")},
				},
				DependsOn: []string{"layer-a-test"}, // cross-namespace dep
			},
		},
	}
	if err := ExpandRecipeFrom(uf, layers, "test", recipe); err != nil {
		t.Fatalf("ExpandRecipeFrom returned error: %v", err)
	}
	if len(recipe.Scenario) != 4 {
		t.Fatalf("want 4 scenarios (3 imported + 1 hand-written), got %d", len(recipe.Scenario))
	}
	// Imports first, hand-written last.
	if recipe.Scenario[len(recipe.Scenario)-1].Name != "handwritten" {
		t.Errorf("hand-written scenario should be last; got order: %v", scenarioNames(recipe.Scenario))
	}
	// Confirm pods are routed correctly.
	for _, sc := range recipe.Scenario {
		switch sc.Name {
		case "layer-a-test", "img-a-test", "handwritten":
			if sc.Pod != "container-x" {
				t.Errorf("scenario %q: pod = %q, want container-x", sc.Name, sc.Pod)
			}
		case "pod-a-test":
			if sc.Pod != "container-y" {
				t.Errorf("scenario %q: pod = %q, want container-y", sc.Name, sc.Pod)
			}
		}
	}
}

func scenarioNames(scs []Scenario) []string {
	out := make([]string, len(scs))
	for i, sc := range scs {
		out[i] = sc.Name
	}
	return out
}

// filter pipeline ------------------------------------------------------------

func TestSelectExcludeFilterPipeline(t *testing.T) {
	uf := fxUnified()
	layers := map[string]*Layer{
		"l": fxLayer("l", []Check{
			fxCheckFile("a", "/a"),
			fxCheckFile("b", "/b"),
			fxCheckFile("c", "/c"),
			fxCheckFile("d", "/d"),
		}),
	}
	recipe := &HarnessRecipe{
		From: []HarnessRecipeFrom{
			{
				Kind: "layer", Name: "l", Pod: "p",
				Select:  []string{"a", "b", "c"}, // first allow
				Exclude: []string{"b"},           // then deny
			},
		},
	}
	if err := ExpandRecipeFrom(uf, layers, "test", recipe); err != nil {
		t.Fatalf("expander error: %v", err)
	}
	if len(recipe.Scenario) != 2 {
		t.Fatalf("want 2 scenarios after select+exclude (a, c), got %d: %v",
			len(recipe.Scenario), scenarioNames(recipe.Scenario))
	}
	got := scenarioNames(recipe.Scenario)
	if got[0] != "a" || got[1] != "c" {
		t.Errorf("unexpected scenario names: %v", got)
	}
}

func TestLiveOnlyVerbsDroppedByDefault(t *testing.T) {
	uf := fxUnified()
	layers := map[string]*Layer{
		"l": fxLayer("l", []Check{
			fxCheckFile("real", "/real"),
			fxCheckLiveOnly("cdp-thing"),
		}),
	}
	recipe := &HarnessRecipe{
		From: []HarnessRecipeFrom{
			{Kind: "layer", Name: "l", Pod: "p"},
		},
	}
	if err := ExpandRecipeFrom(uf, layers, "test", recipe); err != nil {
		t.Fatalf("expander error: %v", err)
	}
	if len(recipe.Scenario) != 1 || recipe.Scenario[0].Name != "real" {
		t.Errorf("live-only check should be dropped; got %v", scenarioNames(recipe.Scenario))
	}
}

func TestEmptyAfterFilterIsError(t *testing.T) {
	uf := fxUnified()
	layers := map[string]*Layer{
		"l": fxLayer("l", []Check{fxCheckFile("a", "/a")}),
	}
	recipe := &HarnessRecipe{
		From: []HarnessRecipeFrom{
			{Kind: "layer", Name: "l", Pod: "p", Select: []string{"nonexistent"}},
		},
	}
	err := ExpandRecipeFrom(uf, layers, "test", recipe)
	if err == nil {
		t.Fatalf("expected error for empty-after-filter; got nil")
	}
	if !strings.Contains(err.Error(), "no tests survived") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// validation -----------------------------------------------------------------

func TestUnknownEntityError(t *testing.T) {
	uf := fxUnified()
	recipe := &HarnessRecipe{
		From: []HarnessRecipeFrom{
			{Kind: "layer", Name: "nope", Pod: "p"},
		},
	}
	err := ExpandRecipeFrom(uf, map[string]*Layer{}, "test", recipe)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected 'not found' error, got %v", err)
	}
}

func TestInvalidKindError(t *testing.T) {
	recipe := &HarnessRecipe{
		From: []HarnessRecipeFrom{
			{Kind: "deployment", Name: "x", Pod: "p"},
		},
	}
	err := ExpandRecipeFrom(fxUnified(), nil, "test", recipe)
	if err == nil || !strings.Contains(err.Error(), "invalid kind") {
		t.Fatalf("expected 'invalid kind' error, got %v", err)
	}
}

func TestNamingCollisionError(t *testing.T) {
	uf := fxUnified()
	layers := map[string]*Layer{
		"l": fxLayer("l", []Check{fxCheckFile("dup", "/a")}),
	}
	recipe := &HarnessRecipe{
		From: []HarnessRecipeFrom{
			{Kind: "layer", Name: "l", Pod: "p"},
		},
		Scenario: []Scenario{
			{Name: "dup", Pod: "p", Steps: []Step{{Then: "x", Check: fxCheckFile("x", "/x")}}},
		},
	}
	err := ExpandRecipeFrom(uf, layers, "test", recipe)
	if err == nil || !strings.Contains(err.Error(), "collides") {
		t.Fatalf("expected collision error, got %v", err)
	}
}

func TestPrefixDisambiguatesCollision(t *testing.T) {
	uf := fxUnified()
	layers := map[string]*Layer{
		"l": fxLayer("l", []Check{fxCheckFile("dup", "/a")}),
	}
	recipe := &HarnessRecipe{
		From: []HarnessRecipeFrom{
			{Kind: "layer", Name: "l", Pod: "p", Prefix: "lp"},
		},
		Scenario: []Scenario{
			{Name: "dup", Pod: "p", Steps: []Step{{Then: "x", Check: fxCheckFile("x", "/x")}}},
		},
	}
	if err := ExpandRecipeFrom(uf, layers, "test", recipe); err != nil {
		t.Fatalf("prefix should disambiguate: %v", err)
	}
	want := []string{"lp-dup", "dup"}
	got := scenarioNames(recipe.Scenario)
	if got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %v, want %v", got, want)
	}
}

// scoring invariant — Section 5 of the plan ----------------------------------

func TestImportedScenarioCountEqualsCheckCount(t *testing.T) {
	// Section 5 invariant: 1 Check = 1 ScenarioID = 1 point.
	// N checks (post-filter) MUST produce exactly N synthetic scenarios,
	// each with exactly one Step.
	uf := fxUnified()
	layers := map[string]*Layer{
		"l": fxLayer("l", []Check{
			fxCheckFile("a", "/a"),
			fxCheckFile("b", "/b"),
			fxCheckFile("c", "/c"),
		}),
	}
	recipe := &HarnessRecipe{
		From: []HarnessRecipeFrom{
			{Kind: "layer", Name: "l", Pod: "p"},
		},
		Scenario: []Scenario{
			{Name: "extra1", Pod: "p", Steps: []Step{{Then: "x", Check: fxCheckFile("x", "/x")}}},
			{Name: "extra2", Pod: "p", Steps: []Step{{Then: "y", Check: fxCheckFile("y", "/y")}}},
		},
	}
	if err := ExpandRecipeFrom(uf, layers, "test", recipe); err != nil {
		t.Fatalf("expander error: %v", err)
	}
	const wantImported = 3
	const wantHandwritten = 2
	if len(recipe.Scenario) != wantImported+wantHandwritten {
		t.Fatalf("Section-5 invariant broken: %d Checks + %d hand-written should produce %d scenarios; got %d",
			wantImported, wantHandwritten, wantImported+wantHandwritten, len(recipe.Scenario))
	}
	for i, sc := range recipe.Scenario {
		if len(sc.Steps) != 1 {
			t.Errorf("Section-5 invariant broken: scenario[%d] %q has %d steps; want exactly 1", i, sc.Name, len(sc.Steps))
		}
	}
}

func TestImportedScenarioStepShape(t *testing.T) {
	// Each synthetic scenario must carry the source Check inline in its
	// single Step (so the existing scoring path treats it identically
	// to a hand-written scenario).
	uf := fxUnified()
	src := fxCheckFile("source", "/the/file")
	layers := map[string]*Layer{
		"l": fxLayer("l", []Check{src}),
	}
	recipe := &HarnessRecipe{
		From: []HarnessRecipeFrom{
			{Kind: "layer", Name: "l", Pod: "p"},
		},
	}
	if err := ExpandRecipeFrom(uf, layers, "test", recipe); err != nil {
		t.Fatalf("expander error: %v", err)
	}
	step := recipe.Scenario[0].Steps[0]
	if step.Check.File != src.File {
		t.Errorf("source Check.File not preserved in synthetic step: got %q want %q", step.Check.File, src.File)
	}
	if step.Check.ID != src.ID {
		t.Errorf("source Check.ID not preserved: got %q want %q", step.Check.ID, src.ID)
	}
	if step.Then == "" {
		t.Errorf("synthetic step should have a then: narrative")
	}
}

// idempotent --------------------------------------------------------------

func TestIdempotentExpansion(t *testing.T) {
	uf := fxUnified()
	layers := map[string]*Layer{"l": fxLayer("l", []Check{fxCheckFile("a", "/a")})}
	recipe := &HarnessRecipe{
		From: []HarnessRecipeFrom{{Kind: "layer", Name: "l", Pod: "p"}},
	}
	if err := ExpandRecipeFrom(uf, layers, "test", recipe); err != nil {
		t.Fatalf("first call: %v", err)
	}
	got1 := len(recipe.Scenario)
	if err := ExpandRecipeFrom(uf, layers, "test", recipe); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if len(recipe.Scenario) != got1 {
		t.Errorf("expander not idempotent: scenario count changed from %d to %d", got1, len(recipe.Scenario))
	}
}

// scope filter ------------------------------------------------------------

func TestScopeFilterDeployOnly(t *testing.T) {
	uf := fxUnified()
	uf.Images["i"] = ImageConfig{
		Eval:        []Check{fxCheckCommand("img-build", "echo build")},
		DeployEval:  []Check{fxCheckCommand("img-deploy", "echo deploy")},
	}
	recipe := &HarnessRecipe{
		From: []HarnessRecipeFrom{
			{Kind: "image", Name: "i", Pod: "p", Scope: []string{"deploy"}},
		},
	}
	if err := ExpandRecipeFrom(uf, map[string]*Layer{}, "test", recipe); err != nil {
		t.Fatalf("expander error: %v", err)
	}
	if len(recipe.Scenario) != 1 || recipe.Scenario[0].Name != "img-deploy" {
		t.Errorf("scope filter [deploy] should leave only img-deploy; got %v", scenarioNames(recipe.Scenario))
	}
}
