package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCdpCmdStructure(t *testing.T) {
	// Verify the command struct fields exist and have correct types.
	// This is a compile-time check more than a runtime one.
	cmd := CdpCmd{}
	_ = cmd.Open
	_ = cmd.List
	_ = cmd.Close
	_ = cmd.Text
	_ = cmd.Html
	_ = cmd.Url
	_ = cmd.Screenshot
	_ = cmd.Click
	_ = cmd.Type
	_ = cmd.Eval
	_ = cmd.Wait
	_ = cmd.Raw

	open := CdpOpenCmd{Box: "test", URL: "https://example.com", Instance: "inst"}
	if open.Box != "test" || open.URL != "https://example.com" || open.Instance != "inst" {
		t.Error("CdpOpenCmd fields not set correctly")
	}

	list := CdpListCmd{Box: "test", Instance: "inst"}
	if list.Box != "test" || list.Instance != "inst" {
		t.Error("CdpListCmd fields not set correctly")
	}

	close := CdpCloseCmd{Box: "test", TabID: "abc123", Instance: "inst"}
	if close.Box != "test" || close.TabID != "abc123" || close.Instance != "inst" {
		t.Error("CdpCloseCmd fields not set correctly")
	}
}

func TestCdpTextCmdStructure(t *testing.T) {
	cmd := CdpTextCmd{Box: "chrome", TabID: "tab1", Instance: "dev"}
	if cmd.Box != "chrome" || cmd.TabID != "tab1" || cmd.Instance != "dev" {
		t.Error("CdpTextCmd fields not set correctly")
	}
}

func TestCdpHtmlCmdStructure(t *testing.T) {
	cmd := CdpHtmlCmd{Box: "chrome", TabID: "tab1", Instance: "dev"}
	if cmd.Box != "chrome" || cmd.TabID != "tab1" || cmd.Instance != "dev" {
		t.Error("CdpHtmlCmd fields not set correctly")
	}
}

func TestCdpUrlCmdStructure(t *testing.T) {
	cmd := CdpUrlCmd{Box: "chrome", TabID: "tab1", Instance: "dev"}
	if cmd.Box != "chrome" || cmd.TabID != "tab1" || cmd.Instance != "dev" {
		t.Error("CdpUrlCmd fields not set correctly")
	}
}

func TestCdpScreenshotCmdStructure(t *testing.T) {
	cmd := CdpScreenshotCmd{Box: "chrome", TabID: "tab1", File: "out.png", Instance: "dev"}
	if cmd.Box != "chrome" || cmd.TabID != "tab1" || cmd.File != "out.png" || cmd.Instance != "dev" {
		t.Error("CdpScreenshotCmd fields not set correctly")
	}
}

func TestCdpClickCmdStructure(t *testing.T) {
	cmd := CdpClickCmd{Box: "chrome", TabID: "tab1", Selector: "#btn", Instance: "dev"}
	if cmd.Box != "chrome" || cmd.TabID != "tab1" || cmd.Selector != "#btn" || cmd.Instance != "dev" {
		t.Error("CdpClickCmd fields not set correctly")
	}
}

func TestCdpTypeCmdStructure(t *testing.T) {
	cmd := CdpTypeCmd{Box: "chrome", TabID: "tab1", Selector: "#input", Text: "hello", Instance: "dev"}
	if cmd.Box != "chrome" || cmd.TabID != "tab1" || cmd.Selector != "#input" || cmd.Text != "hello" || cmd.Instance != "dev" {
		t.Error("CdpTypeCmd fields not set correctly")
	}
}

func TestCdpEvalCmdStructure(t *testing.T) {
	cmd := CdpEvalCmd{Box: "chrome", TabID: "tab1", Expression: "1+1", Instance: "dev"}
	if cmd.Box != "chrome" || cmd.TabID != "tab1" || cmd.Expression != "1+1" || cmd.Instance != "dev" {
		t.Error("CdpEvalCmd fields not set correctly")
	}
}

func TestCdpWaitCmdStructure(t *testing.T) {
	cmd := CdpWaitCmd{Box: "chrome", TabID: "tab1", Selector: ".loaded", Instance: "dev", Timeout: 10 * time.Second}
	if cmd.Box != "chrome" || cmd.TabID != "tab1" || cmd.Selector != ".loaded" || cmd.Instance != "dev" || cmd.Timeout != 10*time.Second {
		t.Error("CdpWaitCmd fields not set correctly")
	}
}

func TestCdpRawCmdStructure(t *testing.T) {
	cmd := CdpRawCmd{Box: "chrome", TabID: "tab1", Method: "Page.navigate", Params: `{"url":"https://example.com"}`, Instance: "dev"}
	if cmd.Box != "chrome" || cmd.TabID != "tab1" || cmd.Method != "Page.navigate" || cmd.Params != `{"url":"https://example.com"}` || cmd.Instance != "dev" {
		t.Error("CdpRawCmd fields not set correctly")
	}
}

func TestCDPMessageSerialization(t *testing.T) {
	// Test marshaling a CDP request message.
	params, _ := json.Marshal(map[string]string{"expression": "1+1"})
	msg := cdpMessage{
		ID:     1,
		Method: "Runtime.evaluate",
		Params: params,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("failed to marshal cdpMessage: %v", err)
	}

	var decoded cdpMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal cdpMessage: %v", err)
	}
	if decoded.ID != 1 {
		t.Errorf("ID = %d, want 1", decoded.ID)
	}
	if decoded.Method != "Runtime.evaluate" {
		t.Errorf("Method = %q, want %q", decoded.Method, "Runtime.evaluate")
	}

	// Test marshaling a response with error.
	errMsg := cdpMessage{
		ID: 2,
		Error: &cdpError{
			Code:    -32601,
			Message: "method not found",
		},
	}
	data, err = json.Marshal(errMsg)
	if err != nil {
		t.Fatalf("failed to marshal error cdpMessage: %v", err)
	}

	var decodedErr cdpMessage
	if err := json.Unmarshal(data, &decodedErr); err != nil {
		t.Fatalf("failed to unmarshal error cdpMessage: %v", err)
	}
	if decodedErr.Error == nil {
		t.Fatal("expected error in decoded message")
	}
	if decodedErr.Error.Code != -32601 {
		t.Errorf("Error.Code = %d, want -32601", decodedErr.Error.Code)
	}
	if decodedErr.Error.Message != "method not found" {
		t.Errorf("Error.Message = %q, want %q", decodedErr.Error.Message, "method not found")
	}

	// Test cdpError.Error() method.
	if decodedErr.Error.Error() != "CDP error -32601: method not found" {
		t.Errorf("cdpError.Error() = %q, want %q", decodedErr.Error.Error(), "CDP error -32601: method not found")
	}

	// Test marshaling a response with result.
	resultMsg := cdpMessage{
		ID:     3,
		Result: json.RawMessage(`{"result":{"type":"number","value":2}}`),
	}
	data, err = json.Marshal(resultMsg)
	if err != nil {
		t.Fatalf("failed to marshal result cdpMessage: %v", err)
	}

	var decodedResult cdpMessage
	if err := json.Unmarshal(data, &decodedResult); err != nil {
		t.Fatalf("failed to unmarshal result cdpMessage: %v", err)
	}
	if decodedResult.ID != 3 {
		t.Errorf("ID = %d, want 3", decodedResult.ID)
	}
	if decodedResult.Result == nil {
		t.Fatal("expected result in decoded message")
	}

	// Test that Method is omitted when empty.
	noMethodMsg := cdpMessage{ID: 4}
	data, err = json.Marshal(noMethodMsg)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}
	s := string(data)
	if strContains(s, `"method"`) {
		t.Error("expected method to be omitted from JSON when empty")
	}
}

func TestJsQuote(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`hello`, `"hello"`},
		{`he said "hi"`, `"he said \"hi\""`},
		{`line1\nline2`, `"line1\\nline2"`},
		{`<script>alert('xss')</script>`, `"\u003cscript\u003ealert('xss')\u003c/script\u003e"`},
		{``, `""`},
	}
	for _, tt := range tests {
		got := jsQuote(tt.input)
		if got != tt.want {
			t.Errorf("jsQuote(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestDevToolsTabWebSocketField(t *testing.T) {
	// Verify WebSocketDebuggerURL is properly deserialized from JSON.
	jsonData := `{"id":"ABC123","title":"Test Page","url":"https://example.com","type":"page","webSocketDebuggerUrl":"ws://localhost:9222/devtools/page/ABC123"}`
	var tab devToolsTab
	if err := json.Unmarshal([]byte(jsonData), &tab); err != nil {
		t.Fatalf("failed to unmarshal devToolsTab: %v", err)
	}
	if tab.ID != "ABC123" {
		t.Errorf("ID = %q, want %q", tab.ID, "ABC123")
	}
	if tab.WebSocketDebuggerURL != "ws://localhost:9222/devtools/page/ABC123" {
		t.Errorf("WebSocketDebuggerURL = %q, want %q", tab.WebSocketDebuggerURL, "ws://localhost:9222/devtools/page/ABC123")
	}
}

// strContains checks if substr is in s. Helper to avoid importing strings in test.
func strContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestCdpClickWlFlag(t *testing.T) {
	// Verify the CdpClickCmd struct has the WL field.
	cmd := CdpClickCmd{WL: true}
	if !cmd.WL {
		t.Error("CdpClickCmd.WL should be true")
	}
}

func TestCdpAxtreeCmd(t *testing.T) {
	// Verify the CdpAxtreeCmd struct exists and has expected fields.
	cmd := CdpAxtreeCmd{
		Box:   "test",
		TabID: "tab1",
		Query: "button",
	}
	if cmd.Box != "test" || cmd.TabID != "tab1" || cmd.Query != "button" {
		t.Error("CdpAxtreeCmd fields not set correctly")
	}
}

func TestCdpCmdSubcommands(t *testing.T) {
	// Verify CdpCmd struct has all expected subcommands.
	var cmd CdpCmd
	_ = cmd.Open
	_ = cmd.List
	_ = cmd.Close
	_ = cmd.Text
	_ = cmd.Html
	_ = cmd.Url
	_ = cmd.Screenshot
	_ = cmd.Click
	_ = cmd.Type
	_ = cmd.Eval
	_ = cmd.Wait
	_ = cmd.Raw
	_ = cmd.Coords
	_ = cmd.Axtree
	_ = cmd.Status
}

// TestResolveTabWS_NumericIndex covers the resolveTabWS dual-path contract:
// numeric strings (e.g. "1") resolve as 1-based indices into type:page tabs;
// non-numeric strings fall through to the existing UUID-match path. The
// numeric path filters out non-page tabs (service-workers, dedicated-workers,
// iframes) so authors can rely on stable 1-based positions in `tab: "1"`
// plan authoring without knowing Chrome-assigned UUIDs.
func TestResolveTabWS_NumericIndex(t *testing.T) {
	type tabFixture struct {
		ID                   string
		Type                 string
		WebSocketDebuggerURL string
	}

	cases := []struct {
		name       string
		tabs       []tabFixture
		input      string
		wantSubstr string // "" expects an error containing wantErrSubstr
		wantErr    string
	}{
		{
			name: "numeric 1 with one page tab",
			tabs: []tabFixture{
				{ID: "abc-uuid-page", Type: "page", WebSocketDebuggerURL: "ws://x/abc"},
			},
			input:      "1",
			wantSubstr: "ws://x/abc",
		},
		{
			name: "numeric 2 picks second page tab",
			tabs: []tabFixture{
				{ID: "first", Type: "page", WebSocketDebuggerURL: "ws://x/first"},
				{ID: "second", Type: "page", WebSocketDebuggerURL: "ws://x/second"},
			},
			input:      "2",
			wantSubstr: "ws://x/second",
		},
		{
			name: "numeric 1 skips non-page tabs",
			tabs: []tabFixture{
				{ID: "sw", Type: "service_worker", WebSocketDebuggerURL: "ws://x/sw"},
				{ID: "iframe", Type: "iframe", WebSocketDebuggerURL: "ws://x/iframe"},
				{ID: "page", Type: "page", WebSocketDebuggerURL: "ws://x/page"},
			},
			input:      "1",
			wantSubstr: "ws://x/page",
		},
		{
			name: "numeric out of range falls through to UUID-match (not found)",
			tabs: []tabFixture{
				{ID: "first", Type: "page", WebSocketDebuggerURL: "ws://x/first"},
				{ID: "second", Type: "page", WebSocketDebuggerURL: "ws://x/second"},
			},
			input:   "5",
			wantErr: "tab 5 not found",
		},
		{
			name: "UUID match still works",
			tabs: []tabFixture{
				{ID: "abc", Type: "page", WebSocketDebuggerURL: "ws://x/abc"},
			},
			input:      "abc",
			wantSubstr: "ws://x/abc",
		},
		{
			name: "UUID no match returns not-found",
			tabs: []tabFixture{
				{ID: "first", Type: "page", WebSocketDebuggerURL: "ws://x/first"},
			},
			input:   "nope",
			wantErr: "tab nope not found",
		},
		{
			name: "zero is not a valid 1-based index",
			tabs: []tabFixture{
				{ID: "first", Type: "page", WebSocketDebuggerURL: "ws://x/first"},
			},
			input:   "0",
			wantErr: "tab 0 not found",
		},
		{
			name: "negative integer falls through to UUID-match (not found)",
			tabs: []tabFixture{
				{ID: "first", Type: "page", WebSocketDebuggerURL: "ws://x/first"},
			},
			input:   "-1",
			wantErr: "tab -1 not found",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/json" {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(tc.tabs)
			}))
			defer srv.Close()

			got, err := resolveTabWS(srv.URL, tc.input)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got %q", tc.wantErr, got)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %q, want containing %q", err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.wantSubstr {
				t.Errorf("got %q, want %q", got, tc.wantSubstr)
			}
		})
	}
}
