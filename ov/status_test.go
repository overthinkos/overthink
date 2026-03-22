package main

import (
	"testing"
)

func TestSummarizePorts(t *testing.T) {
	tests := []struct {
		name  string
		ports []string
		want  string
	}{
		{"empty", nil, "-"},
		{"single", []string{"5900:5900"}, "5900"},
		{"multiple sorted", []string{"8080:8080", "5900:5900", "18789:18789"}, "5900,8080,18789"},
		{"with proto", []string{"47998:47998/udp"}, "47998"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := summarizePorts(tt.ports)
			if got != tt.want {
				t.Errorf("summarizePorts(%v) = %q, want %q", tt.ports, got, tt.want)
			}
		})
	}
}

func TestSummarizeDevices(t *testing.T) {
	tests := []struct {
		name    string
		devices []string
		want    string
	}{
		{"empty", nil, "-"},
		{"gpu only", []string{"nvidia.com/gpu=all"}, "gpu"},
		{"dri only", []string{"/dev/dri/renderD128"}, "dri"},
		{"gpu+dri", []string{"nvidia.com/gpu=all", "/dev/dri/renderD128"}, "dri,gpu"},
		{"gpu+dri+kvm", []string{"nvidia.com/gpu=all", "/dev/dri/renderD128", "/dev/kvm"}, "dri,gpu,kvm"},
		{"dedup dri", []string{"/dev/dri/renderD128", "/dev/dri/card0"}, "dri"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := summarizeDevices(tt.devices)
			if got != tt.want {
				t.Errorf("summarizeDevices(%v) = %q, want %q", tt.devices, got, tt.want)
			}
		})
	}
}

func TestSummarizeToolsTable(t *testing.T) {
	tests := []struct {
		name  string
		tools []ToolStatus
		want  string
	}{
		{"empty", nil, "-"},
		{"all unconfigured", []ToolStatus{{Name: "cdp", Status: "-"}}, "-"},
		{"one ok with port", []ToolStatus{{Name: "cdp", Status: "ok", Port: 9222}}, "cdp:9222"},
		{"socket tool", []ToolStatus{{Name: "sway", Status: "ok"}}, "sway"},
		{"mixed sorted", []ToolStatus{
			{Name: "cdp", Status: "ok", Port: 9222},
			{Name: "vnc", Status: "ok", Port: 5900},
			{Name: "sway", Status: "ok"},
			{Name: "wl", Status: "ok"},
			{Name: "sun", Status: "-"},
		}, "cdp:9222,sway,vnc:5900,wl"},
		{"remapped port", []ToolStatus{
			{Name: "cdp", Status: "ok", Port: 9223},
			{Name: "vnc", Status: "unreachable", Port: 5901},
		}, "cdp:9223,vnc:5901"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := summarizeToolsTable(tt.tools)
			if got != tt.want {
				t.Errorf("summarizeToolsTable() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSummarizeToolsDetail(t *testing.T) {
	tools := []ToolStatus{
		{Name: "cdp", Status: "ok", Port: 9222},
		{Name: "vnc", Status: "ok", Port: 5900},
		{Name: "sway", Status: "ok"},
		{Name: "wl", Status: "ok"},
		{Name: "sun", Status: "-"},
	}
	got := summarizeToolsDetail(tools)
	want := "cdp:9222 (ok), sway (ok), vnc:5900 (ok), wl (ok)"
	if got != want {
		t.Errorf("summarizeToolsDetail() = %q, want %q", got, want)
	}
}

func TestExtractPortFromAddress(t *testing.T) {
	tests := []struct {
		addr string
		want int
	}{
		{"127.0.0.1:5900", 5900},
		{"127.0.0.1:9222", 9222},
		{"0.0.0.0:47990", 47990},
		{"noport", 0},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			got := extractPortFromAddress(tt.addr)
			if got != tt.want {
				t.Errorf("extractPortFromAddress(%q) = %d, want %d", tt.addr, got, tt.want)
			}
		})
	}
}

func TestExtractPortFromURL(t *testing.T) {
	tests := []struct {
		url  string
		want int
	}{
		{"http://127.0.0.1:9222", 9222},
		{"https://127.0.0.1:47990", 47990},
		{"http://localhost", 0},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got := extractPortFromURL(tt.url)
			if got != tt.want {
				t.Errorf("extractPortFromURL(%q) = %d, want %d", tt.url, got, tt.want)
			}
		})
	}
}

func TestParsePSJSON(t *testing.T) {
	// Podman format: JSON array
	podmanJSON := `[{"Names":"ov-ollama","State":"running","Status":"Up 3 hours","Ports":"0.0.0.0:11434->11434/tcp"}]`
	entries, err := parsePSJSON(podmanJSON)
	if err != nil {
		t.Fatalf("parsePSJSON (podman) error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("parsePSJSON (podman) got %d entries, want 1", len(entries))
	}
	if entries[0].Names != "ov-ollama" {
		t.Errorf("Names = %q, want %q", entries[0].Names, "ov-ollama")
	}
	if entries[0].State != "running" {
		t.Errorf("State = %q, want %q", entries[0].State, "running")
	}

	// Docker format: newline-delimited JSON
	dockerJSON := `{"Names":"ov-ollama","State":"running","Status":"Up 3 hours","Ports":"0.0.0.0:11434->11434/tcp"}
{"Names":"ov-jupyter","State":"running","Status":"Up 1 hour","Ports":"0.0.0.0:8888->8888/tcp"}`
	entries, err = parsePSJSON(dockerJSON)
	if err != nil {
		t.Fatalf("parsePSJSON (docker) error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("parsePSJSON (docker) got %d entries, want 2", len(entries))
	}
}

func TestFormatTunnelSummary(t *testing.T) {
	tests := []struct {
		name   string
		tunnel *TunnelYAML
		want   string
	}{
		{"nil", nil, ""},
		{"tailscale all", &TunnelYAML{Provider: "tailscale", Private: PortScope{All: true}}, "tailscale (all ports)"},
		{"cloudflare all", &TunnelYAML{Provider: "cloudflare", Public: PortScope{All: true}}, "cloudflare (all ports)"},
		{"provider only", &TunnelYAML{Provider: "tailscale"}, "tailscale"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatTunnelSummary(tt.tunnel)
			if got != tt.want {
				t.Errorf("formatTunnelSummary() = %q, want %q", got, tt.want)
			}
		})
	}
}
