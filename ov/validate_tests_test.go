package main

import (
	"strings"
	"testing"
)

// runValidateTests invokes validateTests against a small synthetic fixture
// and returns the collected errors' text joined.
func runValidateTests(t *testing.T, cfg *Config, layers map[string]*Layer) string {
	t.Helper()
	errs := &ValidationError{}
	validateTests(cfg, layers, errs)
	return strings.Join(errs.Errors, "\n")
}

// Empty-verb and multi-verb checks must be rejected by Kind() at validation.
func TestValidateTests_VerbDiscriminator(t *testing.T) {
	layers := map[string]*Layer{
		"lyr": {Name: "lyr", tests: []Check{
			{},                     // no verb
			{File: "/x", Port: 80}, // two verbs
		}},
	}
	cfg := &Config{Images: map[string]ImageConfig{}}
	got := runValidateTests(t, cfg, layers)
	if !strings.Contains(got, "no verb") {
		t.Errorf("expected 'no verb' error: %s", got)
	}
	if !strings.Contains(got, "multiple verbs") {
		t.Errorf("expected 'multiple verbs' error: %s", got)
	}
}

// Port out-of-range and timeout parse failure.
func TestValidateTests_NumericAndTimeout(t *testing.T) {
	layers := map[string]*Layer{
		"lyr": {Name: "lyr", tests: []Check{
			{Port: 70000},                // out of range
			{Port: 6379, Timeout: "xxx"}, // bad duration
		}},
	}
	cfg := &Config{Images: map[string]ImageConfig{}}
	got := runValidateTests(t, cfg, layers)
	if !strings.Contains(got, "out of range") {
		t.Errorf("expected range error: %s", got)
	}
	if !strings.Contains(got, "timeout") {
		t.Errorf("expected timeout error: %s", got)
	}
}

// Build-scope checks may not reference runtime-only variables.
func TestValidateTests_RuntimeVarInBuildScope(t *testing.T) {
	layers := map[string]*Layer{
		"lyr": {Name: "lyr", tests: []Check{
			// scope defaults to build at layer level
			{Command: "redis-cli -p ${HOST_PORT:6379}"},
		}},
	}
	cfg := &Config{Images: map[string]ImageConfig{}}
	got := runValidateTests(t, cfg, layers)
	if !strings.Contains(got, "runtime-only variable") || !strings.Contains(got, "HOST_PORT:6379") {
		t.Errorf("expected runtime-only variable error: %s", got)
	}
}

// Deploy-scope checks can use runtime variables — must NOT error.
func TestValidateTests_RuntimeVarInDeployScope(t *testing.T) {
	layers := map[string]*Layer{
		"lyr": {Name: "lyr", tests: []Check{
			{Command: "redis-cli -p ${HOST_PORT:6379}", Scope: "deploy"},
		}},
	}
	cfg := &Config{Images: map[string]ImageConfig{}}
	got := runValidateTests(t, cfg, layers)
	if got != "" {
		t.Errorf("unexpected errors: %s", got)
	}
}

// Invalid scope value.
func TestValidateTests_UnknownScope(t *testing.T) {
	layers := map[string]*Layer{
		"lyr": {Name: "lyr", tests: []Check{{File: "/x", Scope: "weird"}}},
	}
	cfg := &Config{Images: map[string]ImageConfig{}}
	got := runValidateTests(t, cfg, layers)
	if !strings.Contains(got, "scope") {
		t.Errorf("expected scope error: %s", got)
	}
}

// ID collisions within image.Tests and across image.Tests ↔ DeployTests.
func TestValidateTests_IDUniqueness_SameImage(t *testing.T) {
	cfg := &Config{Images: map[string]ImageConfig{
		"img": {
			Enabled: boolPtr(true),
			Tests: []Check{
				{ID: "same", File: "/a"},
				{ID: "same", File: "/b"},
			},
		},
	}}
	got := runValidateTests(t, cfg, map[string]*Layer{})
	if !strings.Contains(got, "duplicate id") {
		t.Errorf("expected duplicate-id error: %s", got)
	}
}

// ID collision across layers that land in the same section of a collected image.
func TestValidateTests_IDUniqueness_CrossLayer(t *testing.T) {
	layers := map[string]*Layer{
		"a": {Name: "a", tests: []Check{{ID: "same", File: "/a"}}},
		"b": {Name: "b", tests: []Check{{ID: "same", File: "/b"}}},
	}
	cfg := &Config{Images: map[string]ImageConfig{
		"img": {Enabled: boolPtr(true), Layers: []string{"a", "b"}},
	}}
	got := runValidateTests(t, cfg, layers)
	if !strings.Contains(got, "duplicate id") || !strings.Contains(got, "layer") {
		t.Errorf("expected cross-layer duplicate-id error: %s", got)
	}
}

// Unknown matcher operator rejected.
func TestValidateTests_UnknownMatcherOp(t *testing.T) {
	layers := map[string]*Layer{
		"lyr": {Name: "lyr", tests: []Check{
			{Command: "x", Stdout: MatcherList{{Op: "mystery", Value: "?"}}},
		}},
	}
	cfg := &Config{Images: map[string]ImageConfig{}}
	got := runValidateTests(t, cfg, layers)
	if !strings.Contains(got, "unsupported matcher op") {
		t.Errorf("expected matcher op error: %s", got)
	}
}

// mcp verb is deploy-scope-only like cdp/wl/dbus/vnc.
func TestValidateTests_McpRejectedInBuildScope(t *testing.T) {
	layers := map[string]*Layer{
		"jupyter": {Name: "jupyter", tests: []Check{
			{Mcp: "ping"}, // default scope at layer level is build
		}},
	}
	cfg := &Config{Images: map[string]ImageConfig{}}
	got := runValidateTests(t, cfg, layers)
	if !strings.Contains(got, "mcp:") || !strings.Contains(got, `scope:"deploy"`) {
		t.Errorf("expected deploy-scope error for mcp: %s", got)
	}
}

// mcp: call requires tool modifier.
func TestValidateTests_McpCallRequiresTool(t *testing.T) {
	layers := map[string]*Layer{
		"jupyter": {Name: "jupyter", tests: []Check{
			{Mcp: "call", Scope: "deploy"}, // missing tool
		}},
	}
	cfg := &Config{Images: map[string]ImageConfig{}}
	got := runValidateTests(t, cfg, layers)
	if !strings.Contains(got, "mcp") || !strings.Contains(got, "tool") {
		t.Errorf("expected mcp call tool-required error: %s", got)
	}
}

// mcp: read requires uri modifier.
func TestValidateTests_McpReadRequiresURI(t *testing.T) {
	layers := map[string]*Layer{
		"jupyter": {Name: "jupyter", tests: []Check{
			{Mcp: "read", Scope: "deploy"}, // missing uri
		}},
	}
	cfg := &Config{Images: map[string]ImageConfig{}}
	got := runValidateTests(t, cfg, layers)
	if !strings.Contains(got, "mcp") || !strings.Contains(got, "uri") {
		t.Errorf("expected mcp read uri-required error: %s", got)
	}
}

// Unknown mcp method rejected with a listing of allowed methods.
func TestValidateTests_McpUnknownMethod(t *testing.T) {
	layers := map[string]*Layer{
		"jupyter": {Name: "jupyter", tests: []Check{
			{Mcp: "bogus", Scope: "deploy"},
		}},
	}
	cfg := &Config{Images: map[string]ImageConfig{}}
	got := runValidateTests(t, cfg, layers)
	if !strings.Contains(got, "mcp: unknown method") {
		t.Errorf("expected unknown method error: %s", got)
	}
	if !strings.Contains(got, "ping") || !strings.Contains(got, "list-tools") {
		t.Errorf("expected error to list allowed methods: %s", got)
	}
}

// Valid mcp checks produce no errors.
func TestValidateTests_McpClean(t *testing.T) {
	layers := map[string]*Layer{
		"jupyter": {Name: "jupyter", tests: []Check{
			{Mcp: "ping", Scope: "deploy"},
			{Mcp: "list-tools", Scope: "deploy"},
			{Mcp: "call", Tool: "list_notebooks", Input: "{}", Scope: "deploy"},
			{Mcp: "read", URI: "file:///x", Scope: "deploy"},
		}},
	}
	cfg := &Config{Images: map[string]ImageConfig{}}
	got := runValidateTests(t, cfg, layers)
	if got != "" {
		t.Errorf("clean mcp fixture produced errors: %s", got)
	}
}

// Full valid fixture — should produce no errors.
func TestValidateTests_Clean(t *testing.T) {
	layers := map[string]*Layer{
		"redis": {Name: "redis", tests: []Check{
			{File: "/usr/bin/redis-server", Exists: ptrBool(true), Mode: "0755"},
			{Port: 6379, Listening: ptrBool(true)},
			{Command: "redis-cli -p ${HOST_PORT:6379} ping", Scope: "deploy", InContainer: ptrBool(false)},
		}},
	}
	cfg := &Config{Images: map[string]ImageConfig{
		"redis-ml": {
			Enabled: boolPtr(true),
			Layers:  []string{"redis"},
			Tests:   []Check{{ID: "version", Command: "redis-server --version"}},
			DeployTests: []Check{
				{ID: "routed", HTTP: "https://${DNS}/health", Status: 200},
			},
		},
	}}
	got := runValidateTests(t, cfg, layers)
	if got != "" {
		t.Errorf("clean fixture produced errors: %s", got)
	}
}
