package main

import (
	"encoding/json"
	"testing"
	"time"
)

func TestParseDevToolsPort(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		want    string
		wantErr bool
	}{
		{
			name:   "standard localhost binding",
			output: "127.0.0.1:9222\n",
			want:   "http://127.0.0.1:9222",
		},
		{
			name:   "all interfaces binding",
			output: "0.0.0.0:9222\n",
			want:   "http://127.0.0.1:9222",
		},
		{
			name:   "random high port",
			output: "0.0.0.0:49222\n",
			want:   "http://127.0.0.1:49222",
		},
		{
			name:   "ipv6 binding",
			output: "[::]:9222\n",
			want:   "http://127.0.0.1:9222",
		},
		{
			name:   "multiple lines ipv4 and ipv6",
			output: "0.0.0.0:9222\n[::]:9222\n",
			want:   "http://127.0.0.1:9222",
		},
		{
			name:   "no trailing newline",
			output: "127.0.0.1:9222",
			want:   "http://127.0.0.1:9222",
		},
		{
			name:    "empty output",
			output:  "",
			wantErr: true,
		},
		{
			name:    "only whitespace",
			output:  "  \n",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDevToolsPort(tt.output)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseDevToolsPort(%q) expected error, got %q", tt.output, got)
				}
				return
			}
			if err != nil {
				t.Errorf("parseDevToolsPort(%q) unexpected error: %v", tt.output, err)
				return
			}
			if got != tt.want {
				t.Errorf("parseDevToolsPort(%q) = %q, want %q", tt.output, got, tt.want)
			}
		})
	}
}

func TestBrowserCmdStructure(t *testing.T) {
	// Verify the command struct fields exist and have correct types.
	// This is a compile-time check more than a runtime one.
	cmd := BrowserCmd{}
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
	_ = cmd.Cdp

	open := BrowserOpenCmd{Image: "test", URL: "https://example.com", Instance: "inst"}
	if open.Image != "test" || open.URL != "https://example.com" || open.Instance != "inst" {
		t.Error("BrowserOpenCmd fields not set correctly")
	}

	list := BrowserListCmd{Image: "test", Instance: "inst"}
	if list.Image != "test" || list.Instance != "inst" {
		t.Error("BrowserListCmd fields not set correctly")
	}

	close := BrowserCloseCmd{Image: "test", TabID: "abc123", Instance: "inst"}
	if close.Image != "test" || close.TabID != "abc123" || close.Instance != "inst" {
		t.Error("BrowserCloseCmd fields not set correctly")
	}
}

func TestBrowserTextCmdStructure(t *testing.T) {
	cmd := BrowserTextCmd{Image: "chrome", TabID: "tab1", Instance: "dev"}
	if cmd.Image != "chrome" || cmd.TabID != "tab1" || cmd.Instance != "dev" {
		t.Error("BrowserTextCmd fields not set correctly")
	}
}

func TestBrowserHtmlCmdStructure(t *testing.T) {
	cmd := BrowserHtmlCmd{Image: "chrome", TabID: "tab1", Instance: "dev"}
	if cmd.Image != "chrome" || cmd.TabID != "tab1" || cmd.Instance != "dev" {
		t.Error("BrowserHtmlCmd fields not set correctly")
	}
}

func TestBrowserUrlCmdStructure(t *testing.T) {
	cmd := BrowserUrlCmd{Image: "chrome", TabID: "tab1", Instance: "dev"}
	if cmd.Image != "chrome" || cmd.TabID != "tab1" || cmd.Instance != "dev" {
		t.Error("BrowserUrlCmd fields not set correctly")
	}
}

func TestBrowserScreenshotCmdStructure(t *testing.T) {
	cmd := BrowserScreenshotCmd{Image: "chrome", TabID: "tab1", File: "out.png", Instance: "dev"}
	if cmd.Image != "chrome" || cmd.TabID != "tab1" || cmd.File != "out.png" || cmd.Instance != "dev" {
		t.Error("BrowserScreenshotCmd fields not set correctly")
	}
}

func TestBrowserClickCmdStructure(t *testing.T) {
	cmd := BrowserClickCmd{Image: "chrome", TabID: "tab1", Selector: "#btn", Instance: "dev"}
	if cmd.Image != "chrome" || cmd.TabID != "tab1" || cmd.Selector != "#btn" || cmd.Instance != "dev" {
		t.Error("BrowserClickCmd fields not set correctly")
	}
}

func TestBrowserTypeCmdStructure(t *testing.T) {
	cmd := BrowserTypeCmd{Image: "chrome", TabID: "tab1", Selector: "#input", Text: "hello", Instance: "dev"}
	if cmd.Image != "chrome" || cmd.TabID != "tab1" || cmd.Selector != "#input" || cmd.Text != "hello" || cmd.Instance != "dev" {
		t.Error("BrowserTypeCmd fields not set correctly")
	}
}

func TestBrowserEvalCmdStructure(t *testing.T) {
	cmd := BrowserEvalCmd{Image: "chrome", TabID: "tab1", Expression: "1+1", Instance: "dev"}
	if cmd.Image != "chrome" || cmd.TabID != "tab1" || cmd.Expression != "1+1" || cmd.Instance != "dev" {
		t.Error("BrowserEvalCmd fields not set correctly")
	}
}

func TestBrowserWaitCmdStructure(t *testing.T) {
	cmd := BrowserWaitCmd{Image: "chrome", TabID: "tab1", Selector: ".loaded", Instance: "dev", Timeout: 10 * time.Second}
	if cmd.Image != "chrome" || cmd.TabID != "tab1" || cmd.Selector != ".loaded" || cmd.Instance != "dev" || cmd.Timeout != 10*time.Second {
		t.Error("BrowserWaitCmd fields not set correctly")
	}
}

func TestBrowserCdpCmdStructure(t *testing.T) {
	cmd := BrowserCdpCmd{Image: "chrome", TabID: "tab1", Method: "Page.navigate", Params: `{"url":"https://example.com"}`, Instance: "dev"}
	if cmd.Image != "chrome" || cmd.TabID != "tab1" || cmd.Method != "Page.navigate" || cmd.Params != `{"url":"https://example.com"}` || cmd.Instance != "dev" {
		t.Error("BrowserCdpCmd fields not set correctly")
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
