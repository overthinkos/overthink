package main

import (
	"strings"
	"testing"
)

// opsCandy wraps a list of Ops as `check:` steps of a single candy plan so the
// per-Op validation rules (driven by validateOps walking plan steps) can be
// exercised in isolation.
func opsCandy(name string, ops ...Op) *Candy {
	steps := make([]Step, len(ops))
	for i := range ops {
		steps[i] = Step{Check: "check", Op: ops[i]}
	}
	return &Candy{Name: name, plan: steps}
}

// runValidateOps invokes validateOps against a synthetic fixture and returns the
// collected errors' text joined.
func runValidateOps(t *testing.T, cfg *Config, layers map[string]*Candy) string {
	t.Helper()
	errs := &ValidationError{}
	validateOps(cfg, layers, errs)
	return strings.Join(errs.Errors, "\n")
}

// A step bearing two verbs is rejected by Kind() at validation. A verbless step
// is a narrative-only (agent-graded) step and is intentionally NOT an error.
func TestValidateOps_MultiVerbRejected(t *testing.T) {
	layers := map[string]*Candy{
		"lyr": opsCandy("lyr", Op{File: "/x", Package: "redis"}),
	}
	cfg := &Config{Box: map[string]BoxConfig{}}
	got := runValidateOps(t, cfg, layers)
	if !strings.Contains(got, "multiple verbs") {
		t.Errorf("expected 'multiple verbs' error: %s", got)
	}
}

// Port out-of-range and timeout parse failure.
// A build-context op may not reference runtime-only variables. command defaults
// to build+deploy+runtime, so with no explicit context it is build-legal and the
// runtime-only ${HOST_PORT} reference is flagged.
func TestValidateOps_RuntimeVarInBuildContext(t *testing.T) {
	layers := map[string]*Candy{
		"lyr": opsCandy("lyr",
			Op{Plugin: "command", PluginInput: map[string]any{"command": "redis-cli -p ${HOST_PORT:6379}"}},
		),
	}
	cfg := &Config{Box: map[string]BoxConfig{}}
	got := runValidateOps(t, cfg, layers)
	if !strings.Contains(got, "runtime-only variable") || !strings.Contains(got, "HOST_PORT:6379") {
		t.Errorf("expected runtime-only variable error: %s", got)
	}
}

// Pinned to deploy context, the same op may use runtime variables — no error.
func TestValidateOps_RuntimeVarInDeployContext(t *testing.T) {
	layers := map[string]*Candy{
		"lyr": opsCandy("lyr",
			Op{Plugin: "command", PluginInput: map[string]any{"command": "redis-cli -p ${HOST_PORT:6379}"}, Context: []string{"deploy"}},
		),
	}
	cfg := &Config{Box: map[string]BoxConfig{}}
	got := runValidateOps(t, cfg, layers)
	if got != "" {
		t.Errorf("unexpected errors: %s", got)
	}
}

// A live-container verb (mcp/cdp/wl/vnc/record/spice/libvirt) pinned to build
// context is rejected — these need a running target (runtime context).
func TestValidateOps_McpRejectedInBuildContext(t *testing.T) {
	layers := map[string]*Candy{
		"jupyter": opsCandy("jupyter", Op{Mcp: "ping", Context: []string{"build"}}),
	}
	cfg := &Config{Box: map[string]BoxConfig{}}
	got := runValidateOps(t, cfg, layers)
	if !strings.Contains(got, "mcp:") || !strings.Contains(got, "runtime-context only") {
		t.Errorf("expected runtime-context-only error for mcp: %s", got)
	}
}

// mcp: call requires tool modifier.
func TestValidateOps_McpCallRequiresTool(t *testing.T) {
	layers := map[string]*Candy{
		"jupyter": opsCandy("jupyter", Op{Mcp: "call"}), // missing tool
	}
	cfg := &Config{Box: map[string]BoxConfig{}}
	got := runValidateOps(t, cfg, layers)
	if !strings.Contains(got, "mcp") || !strings.Contains(got, "tool") {
		t.Errorf("expected mcp call tool-required error: %s", got)
	}
}

// mcp: read requires uri modifier.
func TestValidateOps_McpReadRequiresURI(t *testing.T) {
	layers := map[string]*Candy{
		"jupyter": opsCandy("jupyter", Op{Mcp: "read"}), // missing uri
	}
	cfg := &Config{Box: map[string]BoxConfig{}}
	got := runValidateOps(t, cfg, layers)
	if !strings.Contains(got, "mcp") || !strings.Contains(got, "uri") {
		t.Errorf("expected mcp read uri-required error: %s", got)
	}
}

// Unknown mcp method rejected with a listing of allowed methods.

// Valid mcp checks (default runtime context) produce no errors.
func TestValidateOps_McpClean(t *testing.T) {
	layers := map[string]*Candy{
		"jupyter": opsCandy("jupyter",
			Op{Mcp: "ping"},
			Op{Mcp: "list-tools"},
			Op{Mcp: "call", Tool: "list_notebooks", Input: "{}"},
			Op{Mcp: "read", URI: "file:///x"},
		),
	}
	cfg := &Config{Box: map[string]BoxConfig{}}
	got := runValidateOps(t, cfg, layers)
	if got != "" {
		t.Errorf("clean mcp fixture produced errors: %s", got)
	}
}

// record/spice/libvirt verbs: runtime-context only, method allowlist, required
// modifiers mirror the cdp/wl/dbus/vnc/mcp rules.

func TestValidateOps_RecordRejectedInBuildContext(t *testing.T) {
	layers := map[string]*Candy{
		"asciinema": opsCandy("asciinema", Op{Record: "list", Context: []string{"build"}}),
	}
	got := runValidateOps(t, &Config{Box: map[string]BoxConfig{}}, layers)
	if !strings.Contains(got, "record:") || !strings.Contains(got, "runtime-context only") {
		t.Errorf("expected runtime-context-only error for record: %s", got)
	}
}

func TestValidateOps_RecordStopRequiresArtifact(t *testing.T) {
	layers := map[string]*Candy{
		"asciinema": opsCandy("asciinema", Op{Record: "stop"}), // missing artifact
	}
	got := runValidateOps(t, &Config{Box: map[string]BoxConfig{}}, layers)
	if !strings.Contains(got, "record") || !strings.Contains(got, "artifact") {
		t.Errorf("expected record: stop artifact-required error: %s", got)
	}
}

func TestValidateOps_RecordCmdRequiresText(t *testing.T) {
	layers := map[string]*Candy{
		"asciinema": opsCandy("asciinema", Op{Record: "cmd"}), // missing text
	}
	got := runValidateOps(t, &Config{Box: map[string]BoxConfig{}}, layers)
	if !strings.Contains(got, "record") || !strings.Contains(got, "text") {
		t.Errorf("expected record: cmd text-required error: %s", got)
	}
}

func TestValidateOps_RecordClean(t *testing.T) {
	layers := map[string]*Candy{
		"asciinema": opsCandy("asciinema",
			Op{Record: "list"},
			Op{Record: "start", RecordMode: "terminal"},
			Op{Record: "cmd", Text: "echo hi"},
			Op{Record: "stop", Artifact: "/tmp/demo.cast", ArtifactMinBytes: 100},
		),
	}
	got := runValidateOps(t, &Config{Box: map[string]BoxConfig{}}, layers)
	if got != "" {
		t.Errorf("clean record fixture produced errors: %s", got)
	}
}

func TestValidateOps_SpiceRejectedInBuildContext(t *testing.T) {
	layers := map[string]*Candy{
		"vm": opsCandy("vm", Op{Spice: "status", Context: []string{"build"}}),
	}
	got := runValidateOps(t, &Config{Box: map[string]BoxConfig{}}, layers)
	if !strings.Contains(got, "spice:") || !strings.Contains(got, "runtime-context only") {
		t.Errorf("expected runtime-context-only error for spice: %s", got)
	}
}

func TestValidateOps_SpiceTypeRequiresText(t *testing.T) {
	layers := map[string]*Candy{
		"vm": opsCandy("vm", Op{Spice: "type"}),
	}
	got := runValidateOps(t, &Config{Box: map[string]BoxConfig{}}, layers)
	if !strings.Contains(got, "spice") || !strings.Contains(got, "text") {
		t.Errorf("expected spice: type text-required error: %s", got)
	}
}

func TestValidateOps_SpiceClean(t *testing.T) {
	layers := map[string]*Candy{
		"vm": opsCandy("vm",
			Op{Spice: "status"},
			Op{Spice: "screenshot", Artifact: "/tmp/s.png"},
			Op{Spice: "type", Text: "hi"},
			Op{Spice: "key", KeyName: "Return"},
		),
	}
	got := runValidateOps(t, &Config{Box: map[string]BoxConfig{}}, layers)
	if got != "" {
		t.Errorf("clean spice fixture produced errors: %s", got)
	}
}

func TestValidateOps_LibvirtRejectedInBuildContext(t *testing.T) {
	layers := map[string]*Candy{
		"vm": opsCandy("vm", Op{Libvirt: "info", Context: []string{"build"}}),
	}
	got := runValidateOps(t, &Config{Box: map[string]BoxConfig{}}, layers)
	if !strings.Contains(got, "libvirt:") || !strings.Contains(got, "runtime-context only") {
		t.Errorf("expected runtime-context-only error for libvirt: %s", got)
	}
}

func TestValidateOps_LibvirtGuestExecRequiresCommand(t *testing.T) {
	layers := map[string]*Candy{
		"vm": opsCandy("vm", Op{Libvirt: "guest/exec"}),
	}
	got := runValidateOps(t, &Config{Box: map[string]BoxConfig{}}, layers)
	if !strings.Contains(got, "libvirt") || !strings.Contains(got, "command") {
		t.Errorf("expected libvirt: guest/exec command-required error: %s", got)
	}
}

func TestValidateOps_LibvirtSnapshotCreateRequiresTarget(t *testing.T) {
	layers := map[string]*Candy{
		"vm": opsCandy("vm", Op{Libvirt: "snapshot/create"}),
	}
	got := runValidateOps(t, &Config{Box: map[string]BoxConfig{}}, layers)
	if !strings.Contains(got, "libvirt") || !strings.Contains(got, "target") {
		t.Errorf("expected libvirt: snapshot/create target-required error: %s", got)
	}
}

func TestValidateOps_LibvirtClean(t *testing.T) {
	layers := map[string]*Candy{
		"vm": opsCandy("vm",
			Op{Libvirt: "list"},
			Op{Libvirt: "info"},
			Op{Libvirt: "screenshot", Artifact: "/tmp/v.png"},
			Op{Libvirt: "guest/ping"},
			Op{Libvirt: "guest/exec", Command: "uname -r"},
			Op{Libvirt: "snapshot/create", Target: "pre-upgrade"},
			Op{Libvirt: "qmp", Text: "query-status"},
			Op{Libvirt: "send-key", KeyName: "ctrl alt F2"},
		),
	}
	got := runValidateOps(t, &Config{Box: map[string]BoxConfig{}}, layers)
	if got != "" {
		t.Errorf("clean libvirt fixture produced errors: %s", got)
	}
}

// Full valid fixture — candy plan + box plan — should produce no errors.
func TestValidateOps_Clean(t *testing.T) {
	layers := map[string]*Candy{
		"redis": opsCandy("redis",
			Op{File: "/usr/bin/redis-server", Exists: new(true), Mode: "0755"},
			Op{Plugin: "port", PluginInput: map[string]any{"port": 6379, "listening": true}},
			Op{Plugin: "command", PluginInput: map[string]any{"command": "redis-cli -p ${HOST_PORT:6379} ping", "in_container": false}, Context: []string{"deploy"}},
		),
	}
	cfg := &Config{Box: map[string]BoxConfig{
		"redis-ml": {
			Enabled: new(true),
			Candy:   []string{"redis"},
			Plan: []Step{
				{Check: "version", Op: Op{ID: "version", Plugin: "command", PluginInput: map[string]any{"command": "redis-server --version"}}},
				{Check: "routed", Op: Op{ID: "routed", Plugin: "http", PluginInput: map[string]any{"http": "https://${DNS}/health", "status": 200}}},
			},
		},
	}}
	got := runValidateOps(t, cfg, layers)
	if got != "" {
		t.Errorf("clean fixture produced errors: %s", got)
	}
}

// Lowercase ${...} in a k8s identifier field is rejected — check variables are
// UPPERCASE, so a lowercase token never resolves and reaches the verb literally
// (the k3s-server "cluster: ${deploy_name}" class of bug). Uppercase is accepted,
// and a lowercase ${var} in a shell command body is NOT flagged (legit bash var).
func TestValidateOps_LowercaseCheckVarInClusterField(t *testing.T) {
	cfg := &Config{Box: map[string]BoxConfig{}}

	bad := map[string]*Candy{
		"lyr": opsCandy("lyr", Op{Kube: "addons", Cluster: "${deploy_name}", Context: []string{"deploy"}}),
	}
	if got := runValidateOps(t, cfg, bad); !strings.Contains(got, "UPPERCASE") || !strings.Contains(got, "${deploy_name}") {
		t.Errorf("expected lowercase-check-var rejection: %s", got)
	}

	ok := map[string]*Candy{
		"lyr": opsCandy("lyr", Op{Kube: "addons", Cluster: "${DEPLOY_NAME}", Context: []string{"deploy"}}),
	}
	if got := runValidateOps(t, cfg, ok); strings.Contains(got, "UPPERCASE") {
		t.Errorf("uppercase check var should pass: %s", got)
	}

	shell := map[string]*Candy{
		"lyr": opsCandy("lyr", Op{Plugin: "command", PluginInput: map[string]any{"command": `for v in ${name}; do echo "$v"; done`}, Context: []string{"deploy"}}),
	}
	if got := runValidateOps(t, cfg, shell); strings.Contains(got, "UPPERCASE") {
		t.Errorf("lowercase shell var in command must NOT be flagged: %s", got)
	}
}
