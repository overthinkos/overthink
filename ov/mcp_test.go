package main

import (
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ---------------------------------------------------------------------------
// pickMCPEntry — discriminator semantics
// ---------------------------------------------------------------------------

func TestPickMCPEntry_Empty(t *testing.T) {
	_, err := pickMCPEntry(nil, "")
	if err == nil || !strings.Contains(err.Error(), "no mcp_provides") {
		t.Fatalf("expected no-entries error, got %v", err)
	}
}

func TestPickMCPEntry_SingleAutoPicks(t *testing.T) {
	entries := []MCPProvidesEntry{{Name: "jupyter", URL: "http://x:8888/mcp"}}
	got, err := pickMCPEntry(entries, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "jupyter" {
		t.Fatalf("expected jupyter, got %q", got.Name)
	}
}

func TestPickMCPEntry_MultipleRequireName(t *testing.T) {
	entries := []MCPProvidesEntry{
		{Name: "jupyter", URL: "http://x:8888/mcp"},
		{Name: "chrome-devtools", URL: "http://x:9224/mcp"},
	}
	_, err := pickMCPEntry(entries, "")
	if err == nil || !strings.Contains(err.Error(), "multiple mcp servers") {
		t.Fatalf("expected disambiguation error, got %v", err)
	}
	if !strings.Contains(err.Error(), "jupyter") || !strings.Contains(err.Error(), "chrome-devtools") {
		t.Fatalf("expected error to list available names, got %v", err)
	}
}

func TestPickMCPEntry_NamedMatch(t *testing.T) {
	entries := []MCPProvidesEntry{
		{Name: "jupyter", URL: "http://x:8888/mcp"},
		{Name: "chrome-devtools", URL: "http://x:9224/mcp"},
	}
	got, err := pickMCPEntry(entries, "chrome-devtools")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "chrome-devtools" {
		t.Fatalf("expected chrome-devtools, got %q", got.Name)
	}
}

func TestPickMCPEntry_UnknownName(t *testing.T) {
	entries := []MCPProvidesEntry{{Name: "jupyter", URL: "http://x:8888/mcp"}}
	_, err := pickMCPEntry(entries, "bogus")
	if err == nil || !strings.Contains(err.Error(), `named "bogus"`) {
		t.Fatalf("expected unknown-name error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// resolveContainerNameTemplate — trivial substitution
// ---------------------------------------------------------------------------

func TestResolveContainerNameTemplate(t *testing.T) {
	cases := []struct {
		raw, ctrName, want string
	}{
		{"http://{{.ContainerName}}:8888/mcp", "ov-jupyter", "http://ov-jupyter:8888/mcp"},
		{"http://x/y", "ignored", "http://x/y"},
		{"", "ov-jupyter", ""},
		{"http://{{.ContainerName}}", "", "http://{{.ContainerName}}"}, // empty ctrName: no-op
	}
	for _, tc := range cases {
		got := resolveContainerNameTemplate(tc.raw, tc.ctrName)
		if got != tc.want {
			t.Errorf("resolve(%q, %q) = %q, want %q", tc.raw, tc.ctrName, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// rewriteMCPURLForHost — the load-bearing translator
// ---------------------------------------------------------------------------

func makeInspect(containerPort, hostPort string) *ContainerInspection {
	return &ContainerInspection{
		NetworkSettings: InspectNetwork{
			Ports: map[string][]InspectPortBind{
				containerPort + "/tcp": {{HostPort: hostPort}},
			},
		},
	}
}

func TestRewriteMCPURL_ContainerName(t *testing.T) {
	got, err := rewriteMCPURLForHost("http://ov-jupyter:8888/mcp", "ov-jupyter", makeInspect("8888", "8888"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "http://127.0.0.1:8888/mcp"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRewriteMCPURL_RemappedHostPort(t *testing.T) {
	// Container port 8888 is published to host port 18888 (instance remap).
	got, err := rewriteMCPURLForHost("http://ov-jupyter:8888/mcp", "ov-jupyter", makeInspect("8888", "18888"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "http://127.0.0.1:18888/mcp"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRewriteMCPURL_ExternalHostPassthrough(t *testing.T) {
	// URL points at an external host — do not rewrite.
	got, err := rewriteMCPURLForHost("https://mcp.example.com/api", "ov-jupyter", makeInspect("8888", "8888"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://mcp.example.com/api" {
		t.Errorf("expected passthrough, got %q", got)
	}
}

func TestRewriteMCPURL_NoHostPort(t *testing.T) {
	// Container declares port 8888 but nothing is published.
	inspect := &ContainerInspection{
		NetworkSettings: InspectNetwork{Ports: map[string][]InspectPortBind{"8888/tcp": nil}},
	}
	_, err := rewriteMCPURLForHost("http://ov-jupyter:8888/mcp", "ov-jupyter", inspect)
	if err == nil || !strings.Contains(err.Error(), "not published to a host port") {
		t.Fatalf("expected unpublished-port error, got %v", err)
	}
}

func TestRewriteMCPURL_NoInspection(t *testing.T) {
	_, err := rewriteMCPURLForHost("http://ov-jupyter:8888/mcp", "ov-jupyter", nil)
	if err == nil || !strings.Contains(err.Error(), "no container inspection") {
		t.Fatalf("expected no-inspection error, got %v", err)
	}
}

func TestRewriteMCPURL_MissingPort(t *testing.T) {
	// URL has no explicit port. Can't map.
	_, err := rewriteMCPURLForHost("http://ov-jupyter/mcp", "ov-jupyter", makeInspect("8888", "8888"))
	if err == nil || !strings.Contains(err.Error(), "no port") {
		t.Fatalf("expected missing-port error, got %v", err)
	}
}

func TestRewriteMCPURL_LocalhostIsAccepted(t *testing.T) {
	// Entry may already be pod-rewritten to localhost from podAwareMCPProvides;
	// we still need to map the container port to the published host port.
	got, err := rewriteMCPURLForHost("http://localhost:8888/mcp", "ov-jupyter", makeInspect("8888", "18888"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "http://127.0.0.1:18888/mcp"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// buildMCPTransport — transport dispatch
// ---------------------------------------------------------------------------

func TestBuildMCPTransport(t *testing.T) {
	cases := []struct {
		transport string
		wantType  string // %T substring
		wantErr   bool
	}{
		{"", "StreamableClientTransport", false},
		{"http", "StreamableClientTransport", false},
		{"HTTP", "StreamableClientTransport", false},
		{"streamable-http", "StreamableClientTransport", false},
		{"sse", "SSEClientTransport", false},
		{"SSE", "SSEClientTransport", false},
		{"stdio", "", true},
		{"websocket", "", true},
	}
	for _, tc := range cases {
		tr, err := buildMCPTransport(MCPProvidesEntry{URL: "http://x", Transport: tc.transport})
		if tc.wantErr {
			if err == nil {
				t.Errorf("transport=%q: expected error, got %T", tc.transport, tr)
			}
			continue
		}
		if err != nil {
			t.Errorf("transport=%q: unexpected error: %v", tc.transport, err)
			continue
		}
		typeName := typeOf(tr)
		if !strings.Contains(typeName, tc.wantType) {
			t.Errorf("transport=%q: got %s, want contains %s", tc.transport, typeName, tc.wantType)
		}
	}
}

// typeOf gets a short type name string via fmt %T.
func typeOf(v any) string {
	return strings.TrimPrefix(stringOfType(v), "*")
}

func stringOfType(v any) string {
	if v == nil {
		return "<nil>"
	}
	type stringer interface{ String() string }
	if s, ok := v.(stringer); ok {
		return s.String()
	}
	// Fall back to simple reflect-free formatting.
	// Go's fmt.Sprintf("%T", v) would do, but avoid the fmt import here.
	// Since transports are concrete types, a hand-rolled switch works:
	switch v.(type) {
	case *mcp.StreamableClientTransport:
		return "*mcp.StreamableClientTransport"
	case *mcp.SSEClientTransport:
		return "*mcp.SSEClientTransport"
	}
	return "unknown"
}

// ---------------------------------------------------------------------------
// Formatters — stable one-line-per-record output
// ---------------------------------------------------------------------------

func TestFormatTool(t *testing.T) {
	got := formatTool(&mcp.Tool{Name: "insert_cell", Description: "Insert a cell.\nSecond line."})
	want := "insert_cell\tInsert a cell."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatTool_Nil(t *testing.T) {
	if got := formatTool(nil); got != "" {
		t.Errorf("expected empty for nil, got %q", got)
	}
}

func TestFormatResource(t *testing.T) {
	got := formatResource(&mcp.Resource{URI: "file:///foo", Name: "foo", MIMEType: "text/plain"})
	want := "file:///foo\tfoo\ttext/plain"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatPrompt(t *testing.T) {
	got := formatPrompt(&mcp.Prompt{Name: "greet", Description: "Greet someone"})
	want := "greet\tGreet someone"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFirstLine(t *testing.T) {
	cases := []struct{ in, want string }{
		{"single", "single"},
		{"first\nsecond", "first"},
		{"\n  \n  useful  \nmore", "useful"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := firstLine(tc.in); got != tc.want {
			t.Errorf("firstLine(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// extractToolText
// ---------------------------------------------------------------------------

func TestExtractToolText(t *testing.T) {
	res := &mcp.CallToolResult{Content: []mcp.Content{
		&mcp.TextContent{Text: "line one"},
		&mcp.TextContent{Text: "line two"},
	}}
	want := "line one\nline two"
	if got := extractToolText(res); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExtractToolText_Nil(t *testing.T) {
	if got := extractToolText(nil); got != "" {
		t.Errorf("expected empty for nil, got %q", got)
	}
}

func TestExtractToolText_NoContent(t *testing.T) {
	if got := extractToolText(&mcp.CallToolResult{}); got != "" {
		t.Errorf("expected empty for no content, got %q", got)
	}
}
