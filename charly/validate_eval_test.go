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
	cfg := &Config{Image: map[string]BoxConfig{}}
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
	cfg := &Config{Image: map[string]BoxConfig{}}
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
	cfg := &Config{Image: map[string]BoxConfig{}}
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
	cfg := &Config{Image: map[string]BoxConfig{}}
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
	cfg := &Config{Image: map[string]BoxConfig{}}
	got := runValidateTests(t, cfg, layers)
	if !strings.Contains(got, "scope") {
		t.Errorf("expected scope error: %s", got)
	}
}

// ID collisions within image.Eval and across image.Eval ↔ DeployTests.
func TestValidateTests_IDUniqueness_SameImage(t *testing.T) {
	cfg := &Config{Image: map[string]BoxConfig{
		"img": {
			Enabled: boolPtr(true),
			Eval: []Check{
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
	cfg := &Config{Image: map[string]BoxConfig{
		"img": {Enabled: boolPtr(true), Layer: []string{"a", "b"}},
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
	cfg := &Config{Image: map[string]BoxConfig{}}
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
	cfg := &Config{Image: map[string]BoxConfig{}}
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
	cfg := &Config{Image: map[string]BoxConfig{}}
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
	cfg := &Config{Image: map[string]BoxConfig{}}
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
	cfg := &Config{Image: map[string]BoxConfig{}}
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
	cfg := &Config{Image: map[string]BoxConfig{}}
	got := runValidateTests(t, cfg, layers)
	if got != "" {
		t.Errorf("clean mcp fixture produced errors: %s", got)
	}
}

// record/spice/libvirt verbs: deploy-scope-only, method allowlist, required
// modifiers mirror the cdp/wl/dbus/vnc/mcp rules.

func TestValidateTests_RecordRejectedInBuildScope(t *testing.T) {
	layers := map[string]*Layer{
		"asciinema": {Name: "asciinema", tests: []Check{
			{Record: "list"}, // default build scope
		}},
	}
	got := runValidateTests(t, &Config{Image: map[string]BoxConfig{}}, layers)
	if !strings.Contains(got, "record:") || !strings.Contains(got, `scope:"deploy"`) {
		t.Errorf("expected deploy-scope error for record: %s", got)
	}
}

func TestValidateTests_RecordStopRequiresArtifact(t *testing.T) {
	layers := map[string]*Layer{
		"asciinema": {Name: "asciinema", tests: []Check{
			{Record: "stop", Scope: "deploy"}, // missing artifact
		}},
	}
	got := runValidateTests(t, &Config{Image: map[string]BoxConfig{}}, layers)
	if !strings.Contains(got, "record") || !strings.Contains(got, "artifact") {
		t.Errorf("expected record: stop artifact-required error: %s", got)
	}
}

func TestValidateTests_RecordCmdRequiresText(t *testing.T) {
	layers := map[string]*Layer{
		"asciinema": {Name: "asciinema", tests: []Check{
			{Record: "cmd", Scope: "deploy"}, // missing text
		}},
	}
	got := runValidateTests(t, &Config{Image: map[string]BoxConfig{}}, layers)
	if !strings.Contains(got, "record") || !strings.Contains(got, "text") {
		t.Errorf("expected record: cmd text-required error: %s", got)
	}
}

func TestValidateTests_RecordClean(t *testing.T) {
	layers := map[string]*Layer{
		"asciinema": {Name: "asciinema", tests: []Check{
			{Record: "list", Scope: "deploy"},
			{Record: "start", RecordMode: "terminal", Scope: "deploy"},
			{Record: "cmd", Text: "echo hi", Scope: "deploy"},
			{Record: "stop", Artifact: "/tmp/demo.cast", ArtifactMinBytes: 100, Scope: "deploy"},
		}},
	}
	got := runValidateTests(t, &Config{Image: map[string]BoxConfig{}}, layers)
	if got != "" {
		t.Errorf("clean record fixture produced errors: %s", got)
	}
}

func TestValidateTests_SpiceRejectedInBuildScope(t *testing.T) {
	layers := map[string]*Layer{
		"vm": {Name: "vm", tests: []Check{{Spice: "status"}}},
	}
	got := runValidateTests(t, &Config{Image: map[string]BoxConfig{}}, layers)
	if !strings.Contains(got, "spice:") || !strings.Contains(got, `scope:"deploy"`) {
		t.Errorf("expected deploy-scope error for spice: %s", got)
	}
}

func TestValidateTests_SpiceTypeRequiresText(t *testing.T) {
	layers := map[string]*Layer{
		"vm": {Name: "vm", tests: []Check{{Spice: "type", Scope: "deploy"}}},
	}
	got := runValidateTests(t, &Config{Image: map[string]BoxConfig{}}, layers)
	if !strings.Contains(got, "spice") || !strings.Contains(got, "text") {
		t.Errorf("expected spice: type text-required error: %s", got)
	}
}

func TestValidateTests_SpiceUnknownMethod(t *testing.T) {
	layers := map[string]*Layer{
		"vm": {Name: "vm", tests: []Check{{Spice: "bogus", Scope: "deploy"}}},
	}
	got := runValidateTests(t, &Config{Image: map[string]BoxConfig{}}, layers)
	if !strings.Contains(got, "spice: unknown method") {
		t.Errorf("expected spice unknown-method error: %s", got)
	}
}

func TestValidateTests_SpiceClean(t *testing.T) {
	layers := map[string]*Layer{
		"vm": {Name: "vm", tests: []Check{
			{Spice: "status", Scope: "deploy"},
			{Spice: "screenshot", Artifact: "/tmp/s.png", Scope: "deploy"},
			{Spice: "type", Text: "hi", Scope: "deploy"},
			{Spice: "key", KeyName: "Return", Scope: "deploy"},
		}},
	}
	got := runValidateTests(t, &Config{Image: map[string]BoxConfig{}}, layers)
	if got != "" {
		t.Errorf("clean spice fixture produced errors: %s", got)
	}
}

func TestValidateTests_LibvirtRejectedInBuildScope(t *testing.T) {
	layers := map[string]*Layer{
		"vm": {Name: "vm", tests: []Check{{Libvirt: "info"}}},
	}
	got := runValidateTests(t, &Config{Image: map[string]BoxConfig{}}, layers)
	if !strings.Contains(got, "libvirt:") || !strings.Contains(got, `scope:"deploy"`) {
		t.Errorf("expected deploy-scope error for libvirt: %s", got)
	}
}

func TestValidateTests_LibvirtGuestExecRequiresCommand(t *testing.T) {
	layers := map[string]*Layer{
		"vm": {Name: "vm", tests: []Check{{Libvirt: "guest/exec", Scope: "deploy"}}},
	}
	got := runValidateTests(t, &Config{Image: map[string]BoxConfig{}}, layers)
	if !strings.Contains(got, "libvirt") || !strings.Contains(got, "command") {
		t.Errorf("expected libvirt: guest/exec command-required error: %s", got)
	}
}

func TestValidateTests_LibvirtSnapshotCreateRequiresTarget(t *testing.T) {
	layers := map[string]*Layer{
		"vm": {Name: "vm", tests: []Check{{Libvirt: "snapshot/create", Scope: "deploy"}}},
	}
	got := runValidateTests(t, &Config{Image: map[string]BoxConfig{}}, layers)
	if !strings.Contains(got, "libvirt") || !strings.Contains(got, "target") {
		t.Errorf("expected libvirt: snapshot/create target-required error: %s", got)
	}
}

func TestValidateTests_LibvirtUnknownMethod(t *testing.T) {
	layers := map[string]*Layer{
		"vm": {Name: "vm", tests: []Check{{Libvirt: "bogus", Scope: "deploy"}}},
	}
	got := runValidateTests(t, &Config{Image: map[string]BoxConfig{}}, layers)
	if !strings.Contains(got, "libvirt: unknown method") {
		t.Errorf("expected libvirt unknown-method error: %s", got)
	}
}

func TestValidateTests_LibvirtClean(t *testing.T) {
	layers := map[string]*Layer{
		"vm": {Name: "vm", tests: []Check{
			{Libvirt: "list", Scope: "deploy"},
			{Libvirt: "info", Scope: "deploy"},
			{Libvirt: "screenshot", Artifact: "/tmp/v.png", Scope: "deploy"},
			{Libvirt: "guest/ping", Scope: "deploy"},
			{Libvirt: "guest/exec", Command: "uname -r", Scope: "deploy"},
			{Libvirt: "snapshot/create", Target: "pre-upgrade", Scope: "deploy"},
			{Libvirt: "qmp", Text: "query-status", Scope: "deploy"},
			{Libvirt: "send-key", KeyName: "ctrl alt F2", Scope: "deploy"},
		}},
	}
	got := runValidateTests(t, &Config{Image: map[string]BoxConfig{}}, layers)
	if got != "" {
		t.Errorf("clean libvirt fixture produced errors: %s", got)
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
	cfg := &Config{Image: map[string]BoxConfig{
		"redis-ml": {
			Enabled: boolPtr(true),
			Layer:   []string{"redis"},
			Eval:    []Check{{ID: "version", Command: "redis-server --version"}},
			DeployEval: []Check{
				{ID: "routed", HTTP: "https://${DNS}/health", Status: 200},
			},
		},
	}}
	got := runValidateTests(t, cfg, layers)
	if got != "" {
		t.Errorf("clean fixture produced errors: %s", got)
	}
}

// Lowercase ${...} in a k8s identifier field is rejected — eval variables are
// UPPERCASE, so a lowercase token never resolves and reaches the verb literally
// (the k3s-server "cluster: ${deploy_name}" class of bug). Uppercase is accepted,
// and a lowercase ${var} in a shell command body is NOT flagged (legit bash var).
func TestValidateTests_LowercaseEvalVarInClusterField(t *testing.T) {
	cfg := &Config{Image: map[string]BoxConfig{}}

	bad := map[string]*Layer{
		"lyr": {Name: "lyr", tests: []Check{
			{K8s: "addons", Cluster: "${deploy_name}", Scope: "deploy"},
		}},
	}
	if got := runValidateTests(t, cfg, bad); !strings.Contains(got, "UPPERCASE") || !strings.Contains(got, "${deploy_name}") {
		t.Errorf("expected lowercase-eval-var rejection: %s", got)
	}

	ok := map[string]*Layer{
		"lyr": {Name: "lyr", tests: []Check{
			{K8s: "addons", Cluster: "${DEPLOY_NAME}", Scope: "deploy"},
		}},
	}
	if got := runValidateTests(t, cfg, ok); strings.Contains(got, "UPPERCASE") {
		t.Errorf("uppercase eval var should pass: %s", got)
	}

	shell := map[string]*Layer{
		"lyr": {Name: "lyr", tests: []Check{
			{Command: `for v in ${name}; do echo "$v"; done`, Scope: "deploy"},
		}},
	}
	if got := runValidateTests(t, cfg, shell); strings.Contains(got, "UPPERCASE") {
		t.Errorf("lowercase shell var in command must NOT be flagged: %s", got)
	}
}
