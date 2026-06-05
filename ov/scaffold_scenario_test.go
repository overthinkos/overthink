package main

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestAppendLayerScenario_UnderCandyWrapper proves a scenario lands inside the
// `candy:` kind wrapper (never a stray top-level `description:`), and that a
// second append of the same scenario name is an idempotent no-op.
func TestAppendLayerScenario_UnderCandyWrapper(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "candy.yml")
	if err := os.WriteFile(f, []byte("candy:\n  name: foo\n  version: 2026.1.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	added, err := appendLayerScenario(f, "sc1", []string{"a baseline"}, nil, []string{"it responds"}, []string{"smoke"}, "pod1")
	if err != nil || !added {
		t.Fatalf("append: err=%v added=%v", err, added)
	}

	data, _ := os.ReadFile(f)
	var root map[string]any
	if err := yaml.Unmarshal(data, &root); err != nil {
		t.Fatalf("re-parse: %v\n%s", err, data)
	}
	if _, stray := root["description"]; stray {
		t.Fatalf("stray top-level description: introduced\n%s", data)
	}
	candy, ok := root["candy"].(map[string]any)
	if !ok {
		t.Fatalf("candy wrapper missing\n%s", data)
	}
	desc, ok := candy["description"].(map[string]any)
	if !ok {
		t.Fatalf("candy.description missing\n%s", data)
	}
	scs, ok := desc["scenario"].([]any)
	if !ok || len(scs) != 1 {
		t.Fatalf("want 1 scenario under candy.description, got %v\n%s", desc["scenario"], data)
	}
	sc0 := scs[0].(map[string]any)
	if sc0["name"] != "sc1" || sc0["pod"] != "pod1" {
		t.Fatalf("scenario fields wrong: %v", sc0)
	}
	steps := sc0["step"].([]any)
	if len(steps) != 2 { // one given + one then
		t.Fatalf("want 2 steps, got %d: %v", len(steps), steps)
	}

	// Idempotent: appending the same name again is a no-op.
	added2, err := appendLayerScenario(f, "sc1", nil, nil, []string{"dup"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if added2 {
		t.Fatal("expected idempotent no-op on duplicate scenario name")
	}
}

// TestAppendLayerPackages_UnderCandyWrapper is the regression guard for the
// box/candy-rename fix: package sections must be created INSIDE `candy:`, never
// as a stray top-level `rpm:` the loader would ignore.
func TestAppendLayerPackages_UnderCandyWrapper(t *testing.T) {
	dir := t.TempDir()
	layerDir := filepath.Join(dir, "candy", "foo")
	if err := os.MkdirAll(layerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layerDir, "candy.yml"),
		[]byte("candy:\n  name: foo\n  version: 2026.1.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	if err := appendLayerPackages("foo", "rpm", []string{"ripgrep", "ripgrep"}); err != nil {
		t.Fatalf("appendLayerPackages: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(layerDir, "candy.yml"))
	var root map[string]any
	if err := yaml.Unmarshal(data, &root); err != nil {
		t.Fatalf("re-parse: %v\n%s", err, data)
	}
	if _, stray := root["rpm"]; stray {
		t.Fatalf("stray top-level rpm: introduced (the rename bug)\n%s", data)
	}
	candy := root["candy"].(map[string]any)
	rpm, ok := candy["rpm"].(map[string]any)
	if !ok {
		t.Fatalf("candy.rpm missing\n%s", data)
	}
	pkgs := rpm["packages"].([]any)
	if len(pkgs) != 1 || pkgs[0] != "ripgrep" { // deduped
		t.Fatalf("want [ripgrep] (deduped), got %v", pkgs)
	}
}
