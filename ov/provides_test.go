package main

import (
	"reflect"
	"testing"
)

func TestFilterOwnProvidesEnv(t *testing.T) {
	entries := []EnvProvidesEntry{
		{Name: "OLLAMA_HOST", Value: "http://ov-ollama:11434", Source: "ollama"},
		{Name: "PGHOST", Value: "ov-postgresql", Source: "postgresql"},
		{Name: "CUSTOM", Value: "val", Source: "myimage"},
	}

	got := filterOwnProvides(entries, "ollama")
	want := []EnvProvidesEntry{
		{Name: "PGHOST", Value: "ov-postgresql", Source: "postgresql"},
		{Name: "CUSTOM", Value: "val", Source: "myimage"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("filterOwnProvides(env, ollama) = %v, want %v", got, want)
	}
}

func TestFilterOwnProvidesMCP(t *testing.T) {
	entries := []MCPProvidesEntry{
		{Name: "jupyter", URL: "http://ov-jupyter:8888/mcp", Transport: "http", Source: "jupyter"},
		{Name: "code-search", URL: "http://ov-search:3100/mcp", Transport: "http", Source: "search"},
	}

	got := filterOwnProvides(entries, "jupyter")
	if len(got) != 1 || got[0].Name != "code-search" {
		t.Errorf("filterOwnProvides(mcp, jupyter) = %v, want only code-search", got)
	}
}

func TestFilterOwnProvidesEmpty(t *testing.T) {
	entries := []MCPProvidesEntry{
		{Name: "test", URL: "http://localhost", Source: "img"},
	}
	got := filterOwnProvides(entries, "")
	if len(got) != 1 {
		t.Errorf("filterOwnProvides with empty imageName should return all entries")
	}
}

func TestRemoveBySource(t *testing.T) {
	entries := []MCPProvidesEntry{
		{Name: "jupyter", URL: "http://ov-jupyter:8888/mcp", Source: "jupyter"},
		{Name: "code-search", URL: "http://ov-search:3100/mcp", Source: "search"},
	}

	got, removed := removeBySource(entries, "jupyter")
	if !removed {
		t.Error("removeBySource should report removal")
	}
	if len(got) != 1 || got[0].Name != "code-search" {
		t.Errorf("removeBySource = %v, want only code-search", got)
	}

	got2, removed2 := removeBySource(entries, "nonexistent")
	if removed2 {
		t.Error("removeBySource should not report removal for nonexistent source")
	}
	if len(got2) != 2 {
		t.Errorf("removeBySource(nonexistent) should keep all entries, got %d", len(got2))
	}
}

func TestResolveTemplate(t *testing.T) {
	tests := []struct {
		tmpl, ctr, want string
	}{
		{"http://{{.ContainerName}}:8888/mcp", "ov-jupyter", "http://ov-jupyter:8888/mcp"},
		{"no-template", "ov-test", "no-template"},
		{"{{.ContainerName}}:{{.ContainerName}}", "ov-x", "ov-x:ov-x"},
	}
	for _, tt := range tests {
		got := resolveTemplate(tt.tmpl, tt.ctr)
		if got != tt.want {
			t.Errorf("resolveTemplate(%q, %q) = %q, want %q", tt.tmpl, tt.ctr, got, tt.want)
		}
	}
}

func TestValidateProvidesTemplate(t *testing.T) {
	tests := []struct {
		tmpl string
		want bool
	}{
		{"http://{{.ContainerName}}:8888/mcp", true},
		{"no-template", true},
		{"{{.ContainerName}}", true},
		{"{{.BadVar}}", false},
		{"{{.ContainerName}}{{.Other}}", false},
		{"{{broken", false},
	}
	for _, tt := range tests {
		got := validateProvidesTemplate(tt.tmpl)
		if got != tt.want {
			t.Errorf("validateProvidesTemplate(%q) = %v, want %v", tt.tmpl, got, tt.want)
		}
	}
}

func TestPodAwareEnvProvides(t *testing.T) {
	entries := []EnvProvidesEntry{
		{Name: "OLLAMA_HOST", Value: "http://ov-combined:11434", Source: "combined-image"},
		{Name: "PGHOST", Value: "ov-postgresql", Source: "postgresql-image"},
	}

	// Pod case: consumer IS the combined-image — own entries resolve to localhost
	got := podAwareEnvProvides(entries, "combined-image", "ov-combined")
	if len(got) != 2 {
		t.Fatalf("podAwareEnvProvides should return 2 entries, got %d", len(got))
	}
	// Local entry should use localhost
	if got[0].Name != "OLLAMA_HOST" || got[0].Value != "http://localhost:11434" {
		t.Errorf("pod-local entry: got %+v, want localhost URL", got[0])
	}
	// Remote entry should keep hostname
	if got[1].Name != "PGHOST" || got[1].Value != "ov-postgresql" {
		t.Errorf("cross-container entry: got %+v, want original value", got[1])
	}
}

func TestPodAwareEnvProvidesLocalPrecedence(t *testing.T) {
	// Both local and remote provide the same env var name
	entries := []EnvProvidesEntry{
		{Name: "OLLAMA_HOST", Value: "http://ov-combined:11434", Source: "combined-image"},
		{Name: "OLLAMA_HOST", Value: "http://ov-standalone:11434", Source: "standalone"},
	}

	got := podAwareEnvProvides(entries, "combined-image", "ov-combined")
	if len(got) != 1 {
		t.Fatalf("podAwareEnvProvides with name conflict: got %d entries, want 1 (local wins)", len(got))
	}
	if got[0].Value != "http://localhost:11434" {
		t.Errorf("local should win: got Value %q, want localhost", got[0].Value)
	}
}

func TestPodAwareEnvProvidesCrossContainer(t *testing.T) {
	// Consumer is a different image — all entries are remote
	entries := []EnvProvidesEntry{
		{Name: "OLLAMA_HOST", Value: "http://ov-ollama:11434", Source: "ollama-image"},
	}

	got := podAwareEnvProvides(entries, "hermes-image", "ov-hermes")
	if len(got) != 1 {
		t.Fatalf("cross-container: got %d entries, want 1", len(got))
	}
	if got[0].Value != "http://ov-ollama:11434" {
		t.Errorf("cross-container should keep original value: got %q", got[0].Value)
	}
}

func TestPodAwareMCPProvides(t *testing.T) {
	entries := []MCPProvidesEntry{
		{Name: "jupyter", URL: "http://ov-combined:8888/mcp", Transport: "http", Source: "combined-image"},
		{Name: "code-search", URL: "http://ov-search:3100/mcp", Transport: "http", Source: "search-image"},
	}

	// Pod case: consumer IS the combined-image — own entries resolve to localhost
	got := podAwareMCPProvides(entries, "combined-image", "ov-combined")
	if len(got) != 2 {
		t.Fatalf("podAwareMCPProvides should return 2 entries, got %d", len(got))
	}
	// Local entry should use localhost
	if got[0].Name != "jupyter" || got[0].URL != "http://localhost:8888/mcp" {
		t.Errorf("pod-local entry: got %+v, want localhost URL", got[0])
	}
	// Remote entry should keep hostname
	if got[1].Name != "code-search" || got[1].URL != "http://ov-search:3100/mcp" {
		t.Errorf("cross-container entry: got %+v, want original URL", got[1])
	}
}

func TestPodAwareMCPProvidesLocalPrecedence(t *testing.T) {
	// Both local and remote provide the same MCP server name
	entries := []MCPProvidesEntry{
		{Name: "jupyter", URL: "http://ov-combined:8888/mcp", Transport: "http", Source: "combined-image"},
		{Name: "jupyter", URL: "http://ov-standalone:8888/mcp", Transport: "http", Source: "standalone"},
	}

	got := podAwareMCPProvides(entries, "combined-image", "ov-combined")
	if len(got) != 1 {
		t.Fatalf("podAwareMCPProvides with name conflict: got %d entries, want 1 (local wins)", len(got))
	}
	if got[0].URL != "http://localhost:8888/mcp" {
		t.Errorf("local should win: got URL %q, want localhost", got[0].URL)
	}
}

func TestPodAwareMCPProvidesCrossContainer(t *testing.T) {
	// Consumer is a different image — all entries are remote
	entries := []MCPProvidesEntry{
		{Name: "jupyter", URL: "http://ov-jupyter:8888/mcp", Transport: "http", Source: "jupyter-image"},
	}

	got := podAwareMCPProvides(entries, "hermes-image", "ov-hermes")
	if len(got) != 1 {
		t.Fatalf("cross-container: got %d entries, want 1", len(got))
	}
	if got[0].URL != "http://ov-jupyter:8888/mcp" {
		t.Errorf("cross-container should keep original URL: got %q", got[0].URL)
	}
}
