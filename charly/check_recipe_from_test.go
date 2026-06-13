package main

import (
	"strings"
	"testing"
)

// fixture builders -----------------------------------------------------------

// fxScenario builds a minimal named scenario with a single prose+verb step.
func fxScenario(name, path string) Scenario {
	tr := true
	return Scenario{
		Name: name,
		Step: []Step{{Then: name + " exists", Op: Op{ID: name, File: path, Exists: &tr}}},
	}
}

// fxCandy builds a *Candy carrying the given acceptance scenarios. Recipe
// expansion pulls these directly (no per-check synthesis).
func fxCandy(name string, scenarios ...Scenario) *Candy {
	return &Candy{Name: name, scenario: scenarios}
}

// fxUnified builds a minimal UnifiedFile populated with the kind maps the
// recipe expander consults. Candies are passed separately because they go
// through the projected-candies map, not uf.Candy.
func fxUnified() *UnifiedFile {
	return &UnifiedFile{
		Version: LatestSchemaVersion().String(),
		Box:     map[string]BoxConfig{},
		Pod:     map[string]*PodSpec{},
		VM:      map[string]*VmSpec{},
		Recipe:  map[string]*HarnessRecipe{},
		Score:   map[string]*HarnessScore{},
	}
}

// per-kind expansion ---------------------------------------------------------

func TestExpandFromCandy(t *testing.T) {
	uf := fxUnified()
	layers := map[string]*Candy{
		"sshd": fxCandy("sshd",
			fxScenario("sshd-binary", "/usr/sbin/sshd"),
			fxScenario("sshd-wrapper", "/usr/local/bin/sshd-wrapper"),
		),
	}
	recipe := &HarnessRecipe{
		From: []HarnessRecipeFrom{
			{Kind: "candy", Name: "sshd", Pod: "selftest"},
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
	}
	if recipe.Scenario[0].Name != "sshd-binary" || recipe.Scenario[1].Name != "sshd-wrapper" {
		t.Errorf("unexpected scenario names: %q, %q", recipe.Scenario[0].Name, recipe.Scenario[1].Name)
	}
	if len(recipe.From) != 0 {
		t.Errorf("from: should be cleared after expansion, got %d entries", len(recipe.From))
	}
}

func TestExpandFromBox(t *testing.T) {
	uf := fxUnified()
	// A box with its own box-level scenarios — CollectDescriptions gathers
	// these into the Box section, which the expander flattens.
	uf.Box["arch-coder"] = BoxConfig{
		Scenario: []Scenario{
			fxScenario("arch-coder-charly", "/usr/local/bin/charly"),
		},
	}
	recipe := &HarnessRecipe{
		From: []HarnessRecipeFrom{
			{Kind: "box", Name: "arch-coder", Pod: "selftest-img"},
		},
	}
	if err := ExpandRecipeFrom(uf, map[string]*Candy{}, "test", recipe); err != nil {
		t.Fatalf("ExpandRecipeFrom returned error: %v", err)
	}
	if len(recipe.Scenario) != 1 || recipe.Scenario[0].Name != "arch-coder-charly" {
		t.Fatalf("want 1 box scenario, got %v", scenarioNames(recipe.Scenario))
	}
	if recipe.Scenario[0].Pod != "selftest-img" {
		t.Errorf("box scenario pod = %q, want selftest-img", recipe.Scenario[0].Pod)
	}
}

func TestExpandFromPod(t *testing.T) {
	uf := fxUnified()
	uf.Pod["redis"] = &PodSpec{
		Scenario: []Scenario{
			fxScenario("redis-binary", "/usr/bin/redis-server"),
			fxScenario("redis-config", "/etc/redis/redis.conf"),
		},
	}
	recipe := &HarnessRecipe{
		From: []HarnessRecipeFrom{
			{Kind: "pod", Name: "redis", Pod: "selftest-pod"},
		},
	}
	if err := ExpandRecipeFrom(uf, map[string]*Candy{}, "test", recipe); err != nil {
		t.Fatalf("ExpandRecipeFrom returned error: %v", err)
	}
	if len(recipe.Scenario) != 2 {
		t.Fatalf("want 2 scenarios from pod, got %d", len(recipe.Scenario))
	}
}

func TestExpandFromVM(t *testing.T) {
	uf := fxUnified()
	uf.VM["arch-vm"] = &VmSpec{
		Scenario: []Scenario{
			fxScenario("vm-charly", "/usr/local/bin/charly"),
		},
	}
	recipe := &HarnessRecipe{
		From: []HarnessRecipeFrom{
			{Kind: "vm", Name: "arch-vm", Pod: "selftest-vm"},
		},
	}
	if err := ExpandRecipeFrom(uf, map[string]*Candy{}, "test", recipe); err != nil {
		t.Fatalf("ExpandRecipeFrom returned error: %v", err)
	}
	if len(recipe.Scenario) != 1 {
		t.Fatalf("want 1 scenario from vm, got %d", len(recipe.Scenario))
	}
}

// composition / multi-kind ---------------------------------------------------

func TestMultiKindComposition(t *testing.T) {
	uf := fxUnified()
	uf.Box["img-a"] = BoxConfig{
		Scenario: []Scenario{fxScenario("img-a-test", "/etc/img-a")},
	}
	uf.Pod["pod-a"] = &PodSpec{
		Scenario: []Scenario{fxScenario("pod-a-test", "/etc/foo")},
	}
	layers := map[string]*Candy{
		"layer-a": fxCandy("layer-a", fxScenario("layer-a-test", "/etc/bar")),
	}
	recipe := &HarnessRecipe{
		From: []HarnessRecipeFrom{
			{Kind: "candy", Name: "layer-a", Pod: "container-x"},
			{Kind: "box", Name: "img-a", Pod: "container-x"},
			{Kind: "pod", Name: "pod-a", Pod: "container-y"},
		},
		Scenario: []Scenario{
			{
				Name: "handwritten",
				Pod:  "container-x",
				Step: []Step{
					{Then: "marker exists", Op: fxCheckFileOp("handwritten", "/etc/baz")},
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

// fxCheckFileOp returns a file-exists Op (used inline in hand-written steps).
func fxCheckFileOp(id, path string) Op {
	tr := true
	return Op{ID: id, File: path, Exists: &tr}
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
	layers := map[string]*Candy{
		"l": fxCandy("l",
			fxScenario("a", "/a"),
			fxScenario("b", "/b"),
			fxScenario("c", "/c"),
			fxScenario("d", "/d"),
		),
	}
	recipe := &HarnessRecipe{
		From: []HarnessRecipeFrom{
			{
				Kind: "candy", Name: "l", Pod: "p",
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

func TestEmptyAfterFilterIsError(t *testing.T) {
	uf := fxUnified()
	layers := map[string]*Candy{
		"l": fxCandy("l", fxScenario("a", "/a")),
	}
	recipe := &HarnessRecipe{
		From: []HarnessRecipeFrom{
			{Kind: "candy", Name: "l", Pod: "p", Select: []string{"nonexistent"}},
		},
	}
	err := ExpandRecipeFrom(uf, layers, "test", recipe)
	if err == nil {
		t.Fatalf("expected error for empty-after-filter; got nil")
	}
	if !strings.Contains(err.Error(), "no scenarios survived") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// validation -----------------------------------------------------------------

func TestUnknownEntityError(t *testing.T) {
	uf := fxUnified()
	recipe := &HarnessRecipe{
		From: []HarnessRecipeFrom{
			{Kind: "candy", Name: "nope", Pod: "p"},
		},
	}
	err := ExpandRecipeFrom(uf, map[string]*Candy{}, "test", recipe)
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
	layers := map[string]*Candy{
		"l": fxCandy("l", fxScenario("dup", "/a")),
	}
	recipe := &HarnessRecipe{
		From: []HarnessRecipeFrom{
			{Kind: "candy", Name: "l", Pod: "p"},
		},
		Scenario: []Scenario{
			{Name: "dup", Pod: "p", Step: []Step{{Then: "x", Op: fxCheckFileOp("x", "/x")}}},
		},
	}
	err := ExpandRecipeFrom(uf, layers, "test", recipe)
	if err == nil || !strings.Contains(err.Error(), "collides") {
		t.Fatalf("expected collision error, got %v", err)
	}
}

func TestPrefixDisambiguatesCollision(t *testing.T) {
	uf := fxUnified()
	layers := map[string]*Candy{
		"l": fxCandy("l", fxScenario("dup", "/a")),
	}
	recipe := &HarnessRecipe{
		From: []HarnessRecipeFrom{
			{Kind: "candy", Name: "l", Pod: "p", Prefix: "lp"},
		},
		Scenario: []Scenario{
			{Name: "dup", Pod: "p", Step: []Step{{Then: "x", Op: fxCheckFileOp("x", "/x")}}},
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

// imported scenarios preserve their step shape -------------------------------

func TestImportedScenarioStepShape(t *testing.T) {
	// Each imported scenario must carry its source steps inline so the
	// existing scoring path treats it identically to a hand-written one.
	uf := fxUnified()
	src := fxScenario("source", "/the/file")
	layers := map[string]*Candy{
		"l": fxCandy("l", src),
	}
	recipe := &HarnessRecipe{
		From: []HarnessRecipeFrom{
			{Kind: "candy", Name: "l", Pod: "p"},
		},
	}
	if err := ExpandRecipeFrom(uf, layers, "test", recipe); err != nil {
		t.Fatalf("expander error: %v", err)
	}
	if len(recipe.Scenario) != 1 || len(recipe.Scenario[0].Step) != 1 {
		t.Fatalf("imported scenario shape lost: %+v", recipe.Scenario)
	}
	step := recipe.Scenario[0].Step[0]
	if step.Op.File != "/the/file" {
		t.Errorf("source step File not preserved: got %q", step.Op.File)
	}
	if step.Then == "" {
		t.Errorf("imported step should retain its then: narrative")
	}
}

// idempotent --------------------------------------------------------------

func TestIdempotentExpansion(t *testing.T) {
	uf := fxUnified()
	layers := map[string]*Candy{"l": fxCandy("l", fxScenario("a", "/a"))}
	recipe := &HarnessRecipe{
		From: []HarnessRecipeFrom{{Kind: "candy", Name: "l", Pod: "p"}},
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
