package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// These tests cover the CDP helpers retained HOST-side after the `cdp` declarative verb
// moved out-of-process (candy/plugin-cdp): resolveTabWS (kept for connectTab, used by
// `charly check wl|vnc … --from-cdp`), the cdpMessage wire form (browser_cdp.go), and
// the devToolsTab JSON shape. The declarative cdp methods' tests live in the plugin
// module (candy/plugin-cdp/methods_test.go).

func TestCDPMessageSerialization(t *testing.T) {
	params, _ := json.Marshal(map[string]string{"expression": "1+1"})
	msg := cdpMessage{ID: 1, Method: "Runtime.evaluate", Params: params}
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

	errMsg := cdpMessage{ID: 2, Error: &cdpError{Code: -32601, Message: "method not found"}}
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
	if decodedErr.Error.Error() != "CDP error -32601: method not found" {
		t.Errorf("cdpError.Error() = %q, want %q", decodedErr.Error.Error(), "CDP error -32601: method not found")
	}

	noMethodMsg := cdpMessage{ID: 4}
	data, err = json.Marshal(noMethodMsg)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}
	if strings.Contains(string(data), `"method"`) {
		t.Error("expected method to be omitted from JSON when empty")
	}
}

func TestDevToolsTabWebSocketField(t *testing.T) {
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

// TestResolveTabWS_NumericIndex covers the resolveTabWS dual-path contract: numeric
// strings (e.g. "1") resolve as 1-based indices into type:page tabs; non-numeric
// strings fall through to the existing UUID-match path. The numeric path filters out
// non-page tabs so authors can rely on stable 1-based positions in `tab: "1"`.
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
		wantSubstr string
		wantErr    string
	}{
		{
			name:       "numeric 1 with one page tab",
			tabs:       []tabFixture{{ID: "abc-uuid-page", Type: "page", WebSocketDebuggerURL: "ws://x/abc"}},
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
			name:       "UUID match still works",
			tabs:       []tabFixture{{ID: "abc", Type: "page", WebSocketDebuggerURL: "ws://x/abc"}},
			input:      "abc",
			wantSubstr: "ws://x/abc",
		},
		{
			name:    "zero is not a valid 1-based index",
			tabs:    []tabFixture{{ID: "first", Type: "page", WebSocketDebuggerURL: "ws://x/first"}},
			input:   "0",
			wantErr: "tab 0 not found",
		},
		{
			name:    "negative integer falls through to UUID-match (not found)",
			tabs:    []tabFixture{{ID: "first", Type: "page", WebSocketDebuggerURL: "ws://x/first"}},
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
