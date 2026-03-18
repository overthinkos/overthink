package main

import (
	"testing"
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
