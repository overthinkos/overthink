package main

import (
	"reflect"
	"testing"
)

func TestSecurityArgsPrivileged(t *testing.T) {
	args := SecurityArgs(SecurityConfig{Privileged: true})
	want := []string{"--privileged"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("SecurityArgs(privileged) = %v, want %v", args, want)
	}
}

func TestSecurityArgsCapabilities(t *testing.T) {
	args := SecurityArgs(SecurityConfig{
		CapAdd:      []string{"SYS_ADMIN", "MKNOD"},
		Devices:     []string{"/dev/fuse"},
		SecurityOpt: []string{"label=disable"},
	})
	want := []string{
		"--cap-add", "SYS_ADMIN",
		"--cap-add", "MKNOD",
		"--device", "/dev/fuse",
		"--security-opt", "label=disable",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("SecurityArgs(caps) = %v, want %v", args, want)
	}
}

func TestSecurityArgsEmpty(t *testing.T) {
	args := SecurityArgs(SecurityConfig{})
	if len(args) != 0 {
		t.Errorf("SecurityArgs(empty) = %v, want empty", args)
	}
}

func TestSecurityArgsPrivilegedOverridesCaps(t *testing.T) {
	// When privileged is true, only --privileged is emitted (caps are redundant)
	args := SecurityArgs(SecurityConfig{
		Privileged: true,
		CapAdd:     []string{"SYS_ADMIN"},
	})
	want := []string{"--privileged"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("SecurityArgs(privileged+caps) = %v, want %v", args, want)
	}
}

func TestAppendUnique(t *testing.T) {
	result := appendUnique([]string{"a", "b"}, "b", "c", "a", "d")
	want := []string{"a", "b", "c", "d"}
	if !reflect.DeepEqual(result, want) {
		t.Errorf("appendUnique = %v, want %v", result, want)
	}
}

func TestSecurityArgsShmSize(t *testing.T) {
	args := SecurityArgs(SecurityConfig{ShmSize: "1g"})
	want := []string{"--shm-size", "1g"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("SecurityArgs(shm_size) = %v, want %v", args, want)
	}
}

func TestSecurityArgsShmSizeWithPrivileged(t *testing.T) {
	args := SecurityArgs(SecurityConfig{Privileged: true, ShmSize: "512m"})
	want := []string{"--privileged", "--shm-size", "512m"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("SecurityArgs(privileged+shm) = %v, want %v", args, want)
	}
}

func TestMaxShmSize(t *testing.T) {
	tests := []struct {
		a, b, want string
	}{
		{"", "1g", "1g"},
		{"1g", "", "1g"},
		{"256m", "1g", "1g"},
		{"2g", "1g", "2g"},
		{"512m", "512m", "512m"},
	}
	for _, tt := range tests {
		got := maxShmSize(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("maxShmSize(%q, %q) = %q, want %q", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestParseShmBytes(t *testing.T) {
	tests := []struct {
		s    string
		want int64
	}{
		{"1g", 1024 * 1024 * 1024},
		{"256m", 256 * 1024 * 1024},
		{"64k", 64 * 1024},
		{"1024", 1024},
		{"", 0},
	}
	for _, tt := range tests {
		got := parseShmBytes(tt.s)
		if got != tt.want {
			t.Errorf("parseShmBytes(%q) = %d, want %d", tt.s, got, tt.want)
		}
	}
}

func TestBuildStartArgsWithPrivileged(t *testing.T) {
	sec := SecurityConfig{Privileged: true}
	args := buildStartArgs("docker", "myimage:latest", "/workspace", 0, 0, nil, "ov-myimage", nil, nil, false, "127.0.0.1", nil, sec)
	found := false
	for _, arg := range args {
		if arg == "--privileged" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected --privileged in args: %v", args)
	}
}

func TestBuildShellArgsWithCapAdd(t *testing.T) {
	withTerminal(t, true)
	sec := SecurityConfig{
		CapAdd:  []string{"SYS_ADMIN"},
		Devices: []string{"/dev/fuse"},
	}
	args := buildShellArgs("docker", "myimage:latest", "/workspace", 0, 0, nil, nil, nil, false, "", "127.0.0.1", nil, sec)
	foundCap := false
	foundDev := false
	for i, arg := range args {
		if arg == "--cap-add" && i+1 < len(args) && args[i+1] == "SYS_ADMIN" {
			foundCap = true
		}
		if arg == "--device" && i+1 < len(args) && args[i+1] == "/dev/fuse" {
			foundDev = true
		}
	}
	if !foundCap {
		t.Errorf("expected --cap-add SYS_ADMIN in args: %v", args)
	}
	if !foundDev {
		t.Errorf("expected --device /dev/fuse in args: %v", args)
	}
}

func TestGenerateQuadletWithPrivileged(t *testing.T) {
	cfg := QuadletConfig{
		ImageName: "runner",
		ImageRef:  "ghcr.io/test/runner:latest",
		Workspace: "/workspace",
		Security:  SecurityConfig{Privileged: true},
	}
	content := generateQuadlet(cfg)
	if !containsLine(content, "PodmanArgs=--privileged") {
		t.Error("expected PodmanArgs=--privileged in quadlet")
	}
	if !containsLine(content, "SecurityLabelDisable=true") {
		t.Error("expected SecurityLabelDisable=true in quadlet")
	}
}

func TestGenerateQuadletWithCapAdd(t *testing.T) {
	cfg := QuadletConfig{
		ImageName: "builder",
		ImageRef:  "ghcr.io/test/builder:latest",
		Workspace: "/workspace",
		Security: SecurityConfig{
			CapAdd:      []string{"SYS_ADMIN"},
			Devices:     []string{"/dev/fuse"},
			SecurityOpt: []string{"label=disable"},
		},
	}
	content := generateQuadlet(cfg)
	if !containsLine(content, "AddCapability=SYS_ADMIN") {
		t.Errorf("expected AddCapability=SYS_ADMIN in quadlet:\n%s", content)
	}
	if !containsLine(content, "AddDevice=/dev/fuse") {
		t.Errorf("expected AddDevice=/dev/fuse in quadlet:\n%s", content)
	}
	if !containsLine(content, "SecurityLabelDisable=true") {
		t.Errorf("expected SecurityLabelDisable=true in quadlet:\n%s", content)
	}
}

func containsLine(content, line string) bool {
	for _, l := range splitLines(content) {
		if l == line {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
