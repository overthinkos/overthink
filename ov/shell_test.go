package main

import (
	"reflect"
	"testing"
)

func TestBuildShellArgs(t *testing.T) {
	args := buildShellArgs("ghcr.io/atrawog/fedora:latest", "/home/user/project", 1000, 1000)
	want := []string{
		"docker", "run", "--rm", "-it",
		"-v", "/home/user/project:/workspace",
		"-w", "/workspace",
		"--user", "1000:1000",
		"--entrypoint", "bash",
		"ghcr.io/atrawog/fedora:latest",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildShellArgs() =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildShellArgsCustomUIDGID(t *testing.T) {
	args := buildShellArgs("fedora:latest", "/tmp", 1001, 1002)
	want := []string{
		"docker", "run", "--rm", "-it",
		"-v", "/tmp:/workspace",
		"-w", "/workspace",
		"--user", "1001:1002",
		"--entrypoint", "bash",
		"fedora:latest",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildShellArgs() =\n  %v\nwant\n  %v", args, want)
	}
}

func TestResolveShellImageRef(t *testing.T) {
	tests := []struct {
		name     string
		registry string
		image    string
		tag      string
		want     string
	}{
		{
			name:     "with registry",
			registry: "ghcr.io/atrawog",
			image:    "fedora",
			tag:      "latest",
			want:     "ghcr.io/atrawog/fedora:latest",
		},
		{
			name:     "without registry",
			registry: "",
			image:    "fedora",
			tag:      "latest",
			want:     "fedora:latest",
		},
		{
			name:     "custom tag",
			registry: "ghcr.io/atrawog",
			image:    "ubuntu",
			tag:      "2026.46.1415",
			want:     "ghcr.io/atrawog/ubuntu:2026.46.1415",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveShellImageRef(tt.registry, tt.image, tt.tag)
			if got != tt.want {
				t.Errorf("resolveShellImageRef() = %q, want %q", got, tt.want)
			}
		})
	}
}
