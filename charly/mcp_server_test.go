package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alecthomas/kong"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Unit tests cover three contracts of the MCP server:
//
//  1. Every Kong leaf becomes an MCP tool; dotted naming matches path.
//  2. Destructive commands are annotated; --read-only skips them entirely.
//  3. Schema emission picks up positional args, flags, defaults, and enums.
//
// End-to-end tool invocation (capture + run) is exercised by a minimal call to
// the `version` tool, which has no side effects and returns a stable shape.

func buildTestKong(t *testing.T) *kong.Kong {
	t.Helper()
	var cli CLI
	k, err := kong.New(&cli, kong.Name("charly"), kong.UsageOnError())
	if err != nil {
		t.Fatalf("building kong: %v", err)
	}
	return k
}

// toolIndex returns a map from tool name → *mcp.Tool by walking the Kong leaf
// tree with the same logic buildMcpServer uses. Avoids a dependency on running
// an actual *mcp.Server.
func toolIndex(t *testing.T, readOnly bool) map[string]*mcp.Tool {
	t.Helper()
	k := buildTestKong(t)
	out := map[string]*mcp.Tool{}
	for _, leaf := range k.Model.Leaves(true) {
		path := leafPath(leaf)
		if mcpSkipToolPaths[path] {
			continue
		}
		destructive := mcpDestructivePaths[path]
		if readOnly && destructive {
			continue
		}
		out[path] = kongLeafToTool(leaf, path, destructive)
	}
	return out
}

func TestMcpServerSchema_CoreToolsPresent(t *testing.T) {
	tools := toolIndex(t, false)

	must := []string{
		"version",
		"status",
		"box.build",
		"box.inspect",
		"box.list.boxes",
		"box.list.candies",
		"settings.list",
		"eval.live",
		"eval.mcp.ping",
	}
	for _, name := range must {
		if _, ok := tools[name]; !ok {
			t.Errorf("missing expected tool %q", name)
		}
	}

	// Server's own serve command must not be exposed (avoids recursion).
	if _, ok := tools["mcp.serve"]; ok {
		t.Errorf("mcp.serve should be in the skip list, not exposed as a tool")
	}
}

func TestMcpServerSchema_DestructiveHint(t *testing.T) {
	tools := toolIndex(t, false)
	remove, ok := tools["remove"]
	if !ok {
		t.Fatalf("remove tool missing")
	}
	if remove.Annotations == nil || remove.Annotations.DestructiveHint == nil || !*remove.Annotations.DestructiveHint {
		t.Errorf("remove tool should have DestructiveHint=true, got %+v", remove.Annotations)
	}

	// A read-only command should not carry DestructiveHint=true.
	status, ok := tools["status"]
	if !ok {
		t.Fatalf("status tool missing")
	}
	if status.Annotations != nil && status.Annotations.DestructiveHint != nil && *status.Annotations.DestructiveHint {
		t.Errorf("status should not have DestructiveHint=true")
	}
	if status.Annotations == nil || !status.Annotations.ReadOnlyHint {
		t.Errorf("status should have ReadOnlyHint=true")
	}
}

func TestMcpServerSchema_ReadOnlyFilter(t *testing.T) {
	all := toolIndex(t, false)
	restricted := toolIndex(t, true)

	if len(restricted) >= len(all) {
		t.Errorf("read-only filter did not remove any tools (all=%d, restricted=%d)", len(all), len(restricted))
	}

	// Representative destructive tools must be gone.
	for _, name := range []string{"remove", "config.mount", "secrets.set", "box.build"} {
		if _, ok := restricted[name]; ok {
			t.Errorf("read-only server should not expose %q", name)
		}
	}

	// Representative read-only tools must still be present.
	for _, name := range []string{"version", "status", "box.list.boxes", "box.inspect"} {
		if _, ok := restricted[name]; !ok {
			t.Errorf("read-only server should still expose %q", name)
		}
	}
}

func TestMcpServerSchema_PositionalsAndFlags(t *testing.T) {
	tools := toolIndex(t, false)

	// box.inspect has one required positional ("box") plus --format and
	// --instance flags.
	inspect, ok := tools["box.inspect"]
	if !ok {
		t.Fatalf("box.inspect missing")
	}
	schema, ok := inspect.InputSchema.(map[string]any)
	if !ok {
		t.Fatalf("inspect schema wrong type: %T", inspect.InputSchema)
	}
	props, _ := schema["properties"].(map[string]any)
	for _, want := range []string{"box", "format", "instance"} {
		if _, ok := props[want]; !ok {
			t.Errorf("box.inspect schema missing property %q", want)
		}
	}
	reqList, _ := schema["required"].([]string)
	foundBox := false
	for _, r := range reqList {
		if r == "box" {
			foundBox = true
		}
	}
	if !foundBox {
		t.Errorf("box.inspect should require 'box' positional, required=%v", reqList)
	}
}

func TestMcpServerSchema_EnumAndDefault(t *testing.T) {
	tools := toolIndex(t, false)

	// secrets.export has --format with enum:"yaml,json" in secrets_cmd.go.
	export, ok := tools["secrets.export"]
	if !ok {
		t.Fatalf("secrets.export missing")
	}
	schema := export.InputSchema.(map[string]any)
	props := schema["properties"].(map[string]any)
	fmtProp, ok := props["format"].(map[string]any)
	if !ok {
		t.Fatalf("secrets.export missing 'format' property")
	}
	enum, ok := fmtProp["enum"].([]any)
	if !ok || len(enum) != 2 {
		t.Errorf("secrets.export --format should have 2 enum values, got %v", fmtProp)
	}
	if def, _ := fmtProp["default"].(string); def != "yaml" {
		t.Errorf("secrets.export --format default should be yaml, got %v", fmtProp["default"])
	}
}

func TestMcpServerSchema_AdditionalPropertiesFalse(t *testing.T) {
	tools := toolIndex(t, false)
	for name, tool := range tools {
		schema, ok := tool.InputSchema.(map[string]any)
		if !ok {
			t.Errorf("tool %q schema is %T, want map[string]any", name, tool.InputSchema)
			continue
		}
		if ap, ok := schema["additionalProperties"].(bool); !ok || ap {
			t.Errorf("tool %q should have additionalProperties: false", name)
		}
	}
}

// TestMcpServer_VersionRoundTrip exercises the full in-process capture +
// kong.Parse invocation path on a command that's safe (no side effects). It also
// proves `charly version` reports the STAMPED build identity (BuildCalVer), not a
// wall-clock readout — so the round-trip carries the binary's true CalVer.
func TestMcpServer_VersionRoundTrip(t *testing.T) {
	saved := BuildCalVer
	defer func() { BuildCalVer = saved }()
	BuildCalVer = "2026.154.0943"

	stdout, stderr, err := captureAndRun([]string{"version"})
	if err != nil {
		t.Fatalf("captureAndRun(version): %v", err)
	}
	trimmed := strings.TrimSpace(stdout)
	// Must echo the stamped identity verbatim — not the current time.
	if trimmed != "2026.154.0943" {
		t.Errorf("version output = %q, want the stamped BuildCalVer %q (stderr=%q)", trimmed, "2026.154.0943", stderr)
	}
}

// TestMcpServer_InvalidArgumentReturnsToolError ensures that a missing required
// positional surfaces as a tool error rather than a crash.
func TestMcpServer_InvalidArgumentReturnsToolError(t *testing.T) {
	// box.inspect requires a <box> positional. Omit it.
	stdout, stderr, err := captureAndRun([]string{"box", "inspect"})
	if err == nil {
		t.Errorf("expected error from inspect with no positional, got stdout=%q stderr=%q", stdout, stderr)
	}
}

// TestMcpServer_ArgvReconstruction verifies argvFromJSON emits the correct
// CLI args for a realistic tool call (box.inspect with --format and a
// positional).
func TestMcpServer_ArgvReconstruction(t *testing.T) {
	k := buildTestKong(t)
	var leaf *kong.Node
	for _, l := range k.Model.Leaves(true) {
		if leafPath(l) == "box.inspect" {
			leaf = l
			break
		}
	}
	if leaf == nil {
		t.Fatalf("box.inspect leaf not found")
	}

	posByProp := map[string]*kong.Positional{}
	var posOrder []string
	for _, p := range leaf.Positional {
		n := posPropName(p)
		posByProp[n] = p
		posOrder = append(posOrder, n)
	}
	flagByProp := map[string]*kong.Flag{}
	for _, group := range leaf.AllFlags(true) {
		for _, f := range group {
			if f.Hidden || isHelpFlag(f) {
				continue
			}
			flagByProp[flagPropName(f)] = f
		}
	}

	input := map[string]any{
		"box":    "charly-fedora",
		"format": "tag",
	}
	argv, err := argvFromJSON([]string{"box", "inspect"}, posOrder, posByProp, flagByProp, input)
	if err != nil {
		t.Fatalf("argvFromJSON: %v", err)
	}

	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "box inspect") {
		t.Errorf("argv missing command tokens: %q", joined)
	}
	if !strings.Contains(joined, "--format=tag") {
		t.Errorf("argv missing --format=tag: %q", joined)
	}
	if !strings.Contains(joined, "charly-fedora") {
		t.Errorf("argv missing positional charly-fedora: %q", joined)
	}
}

// TestMcpServer_SchemaMarshalsCleanly guards against anything in the schema
// tree that json.Marshal can't handle (e.g. a reflect.Value slipping in).
func TestMcpServer_SchemaMarshalsCleanly(t *testing.T) {
	tools := toolIndex(t, false)
	for name, tool := range tools {
		if _, err := json.Marshal(tool); err != nil {
			t.Errorf("tool %q failed to marshal: %v", name, err)
		}
	}
}
