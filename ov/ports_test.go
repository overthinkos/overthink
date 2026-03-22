package main

import (
	"net"
	"strconv"
	"strings"
	"testing"
)

func TestParseHostPort(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"8000:8000", 8000},
		{"8000", 8000},
		{"5901:5900", 5901},
		{"443:8443", 443},
		{"47998:47998/udp", 47998},
		{"48000/udp", 48000},
	}
	for _, tt := range tests {
		got, err := ParseHostPort(tt.input)
		if err != nil {
			t.Errorf("ParseHostPort(%q) error: %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseHostPort(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestParseContainerPort(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"8000:9000", 9000},
		{"8000", 8000},
		{"5901:5900", 5900},
		{"47998:47998/udp", 47998},
		{"48000/udp", 48000},
	}
	for _, tt := range tests {
		got, err := ParseContainerPort(tt.input)
		if err != nil {
			t.Errorf("ParseContainerPort(%q) error: %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseContainerPort(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestApplyPortOverrides(t *testing.T) {
	ports := []string{"5900:5900", "18789:18789", "9222:9222", "11434:11434"}
	overrides := []string{"5901:5900", "11435:11434"}

	result, err := ApplyPortOverrides(ports, overrides)
	if err != nil {
		t.Fatalf("ApplyPortOverrides error: %v", err)
	}

	expected := []string{"5901:5900", "18789:18789", "9222:9222", "11435:11434"}
	if len(result) != len(expected) {
		t.Fatalf("got %d ports, want %d", len(result), len(expected))
	}
	for i, got := range result {
		if got != expected[i] {
			t.Errorf("port[%d] = %q, want %q", i, got, expected[i])
		}
	}
}

func TestApplyPortOverridesUDP(t *testing.T) {
	ports := []string{"47990:47990", "47998:47998/udp", "47999:47999/udp", "48000:48000/udp"}
	overrides := []string{"47991:47990", "48001:48000"}

	result, err := ApplyPortOverrides(ports, overrides)
	if err != nil {
		t.Fatalf("ApplyPortOverrides error: %v", err)
	}

	expected := []string{"47991:47990", "47998:47998/udp", "47999:47999/udp", "48001:48000/udp"}
	if len(result) != len(expected) {
		t.Fatalf("got %d ports, want %d", len(result), len(expected))
	}
	for i, got := range result {
		if got != expected[i] {
			t.Errorf("port[%d] = %q, want %q", i, got, expected[i])
		}
	}
}

func TestStripPortSuffix(t *testing.T) {
	tests := []struct {
		input    string
		wantPort string
		wantProto string
	}{
		{"47998/udp", "47998", "udp"},
		{"5900/tcp", "5900", "tcp"},
		{"8000", "8000", ""},
		{"47998:47998/udp", "47998:47998", "udp"},
	}
	for _, tt := range tests {
		port, proto := stripPortSuffix(tt.input)
		if port != tt.wantPort || proto != tt.wantProto {
			t.Errorf("stripPortSuffix(%q) = (%q, %q), want (%q, %q)", tt.input, port, proto, tt.wantPort, tt.wantProto)
		}
	}
}

func TestCheckPortAvailabilityUDP(t *testing.T) {
	// Bind a UDP port, then check it
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind test UDP port: %v", err)
	}
	defer conn.Close()

	port := conn.LocalAddr().(*net.UDPAddr).Port
	ports := []string{strconv.Itoa(port) + ":" + strconv.Itoa(port) + "/udp"}

	conflicts := CheckPortAvailability(ports, "127.0.0.1", "podman")
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 UDP conflict, got %d", len(conflicts))
	}
	if conflicts[0].HostPort != port {
		t.Errorf("conflict port = %d, want %d", conflicts[0].HostPort, port)
	}
}

func TestApplyPortOverridesNoMatch(t *testing.T) {
	ports := []string{"8000:8000", "9000:9000"}
	overrides := []string{"5901:5900"} // 5900 not in ports

	result, err := ApplyPortOverrides(ports, overrides)
	if err != nil {
		t.Fatalf("ApplyPortOverrides error: %v", err)
	}

	// No change — override doesn't match any container port
	for i, got := range result {
		if got != ports[i] {
			t.Errorf("port[%d] = %q, want %q (unchanged)", i, got, ports[i])
		}
	}
}

func TestApplyPortOverridesInvalid(t *testing.T) {
	_, err := ApplyPortOverrides([]string{"8000:8000"}, []string{"notaport"})
	if err == nil {
		t.Error("expected error for invalid override format")
	}
}

func TestCheckPortAvailabilityOpen(t *testing.T) {
	// All ports should be available on random high ports
	ports := []string{"0:0"} // port 0 is always available
	conflicts := CheckPortAvailability(ports, "127.0.0.1", "podman")
	if len(conflicts) != 0 {
		t.Errorf("expected no conflicts for port 0, got %d", len(conflicts))
	}
}

func TestCheckPortAvailabilityInUse(t *testing.T) {
	// Bind a port, then check it
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind test port: %v", err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port
	ports := []string{strconv.Itoa(port) + ":" + strconv.Itoa(port)}

	conflicts := CheckPortAvailability(ports, "127.0.0.1", "podman")
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(conflicts))
	}
	if conflicts[0].HostPort != port {
		t.Errorf("conflict port = %d, want %d", conflicts[0].HostPort, port)
	}
}

func TestFormatPortConflicts(t *testing.T) {
	conflicts := []PortConflict{
		{HostPort: 5900, ContPort: 5900, Owner: "ov-openclaw-sway-browser", OwnerType: "ov-container"},
		{HostPort: 11434, ContPort: 11434, Owner: "ollama-1", OwnerType: "container"},
	}

	output := FormatPortConflicts(conflicts, "my-image")

	if !containsAll(output, "Port 5900", "ov stop", "Port 11434", "podman stop", "--port") {
		t.Errorf("FormatPortConflicts output missing expected content:\n%s", output)
	}
}

func containsAll(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
