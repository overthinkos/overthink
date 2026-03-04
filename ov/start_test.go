package main

import (
	"reflect"
	"testing"
)

func TestBuildStartArgs(t *testing.T) {
	args := buildStartArgs("docker", "ghcr.io/overthinkos/fedora-test:latest", "/home/user/project", 1000, 1000, nil, "ov-fedora-test", nil, nil, false, "127.0.0.1", nil, SecurityConfig{})
	want := []string{
		"docker", "run", "-d", "--rm",
		"--name", "ov-fedora-test",
		"-v", "/home/user/project:/workspace",
		"-w", "/workspace",
		"ghcr.io/overthinkos/fedora-test:latest",
		"supervisord", "-n", "-c", "/etc/supervisord.conf",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildStartArgs() =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildStartArgsPodman(t *testing.T) {
	args := buildStartArgs("podman", "ghcr.io/overthinkos/fedora-test:latest", "/home/user/project", 1000, 1000, nil, "ov-fedora-test", nil, nil, false, "127.0.0.1", nil, SecurityConfig{})
	want := []string{
		"podman", "run", "-d", "--rm",
		"--name", "ov-fedora-test",
		"-v", "/home/user/project:/workspace",
		"-w", "/workspace",
		"ghcr.io/overthinkos/fedora-test:latest",
		"supervisord", "-n", "-c", "/etc/supervisord.conf",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildStartArgs(podman) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildStartArgsWithPorts(t *testing.T) {
	args := buildStartArgs("docker", "ghcr.io/overthinkos/fedora-test:latest", "/home/user/project", 1000, 1000, []string{"9090:9090", "8080:8080"}, "ov-fedora-test", nil, nil, false, "127.0.0.1", nil, SecurityConfig{})
	want := []string{
		"docker", "run", "-d", "--rm",
		"--name", "ov-fedora-test",
		"-v", "/home/user/project:/workspace",
		"-w", "/workspace",
		"-p", "127.0.0.1:9090:9090",
		"-p", "127.0.0.1:8080:8080",
		"ghcr.io/overthinkos/fedora-test:latest",
		"supervisord", "-n", "-c", "/etc/supervisord.conf",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildStartArgs() =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildStartArgsWithVolumes(t *testing.T) {
	volumes := []VolumeMount{
		{VolumeName: "ov-ollama-models", ContainerPath: "/home/user/.ollama/models"},
	}
	args := buildStartArgs("docker", "ghcr.io/overthinkos/ollama:latest", "/home/user/project", 1000, 1000, nil, "ov-ollama", volumes, nil, false, "127.0.0.1", nil, SecurityConfig{})
	want := []string{
		"docker", "run", "-d", "--rm",
		"--name", "ov-ollama",
		"-v", "/home/user/project:/workspace",
		"-w", "/workspace",
		"-v", "ov-ollama-models:/home/user/.ollama/models",
		"ghcr.io/overthinkos/ollama:latest",
		"supervisord", "-n", "-c", "/etc/supervisord.conf",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildStartArgs() =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildStartArgsWithGPU(t *testing.T) {
	args := buildStartArgs("docker", "ghcr.io/overthinkos/ollama:latest", "/home/user/project", 1000, 1000, nil, "ov-ollama", nil, nil, true, "127.0.0.1", nil, SecurityConfig{})
	want := []string{
		"docker", "run", "-d", "--rm",
		"--name", "ov-ollama",
		"-v", "/home/user/project:/workspace",
		"-w", "/workspace",
		"--gpus", "all",
		"ghcr.io/overthinkos/ollama:latest",
		"supervisord", "-n", "-c", "/etc/supervisord.conf",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildStartArgs(gpu=true) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildStartArgsWithGPUPodman(t *testing.T) {
	args := buildStartArgs("podman", "ghcr.io/overthinkos/ollama:latest", "/home/user/project", 1000, 1000, nil, "ov-ollama", nil, nil, true, "127.0.0.1", nil, SecurityConfig{})
	want := []string{
		"podman", "run", "-d", "--rm",
		"--name", "ov-ollama",
		"-v", "/home/user/project:/workspace",
		"-w", "/workspace",
		"--device", "nvidia.com/gpu=all",
		"ghcr.io/overthinkos/ollama:latest",
		"supervisord", "-n", "-c", "/etc/supervisord.conf",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildStartArgs(podman+gpu) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestContainerName(t *testing.T) {
	tests := []struct {
		image string
		want  string
	}{
		{"fedora-test", "ov-fedora-test"},
		{"fedora", "ov-fedora"},
		{"ubuntu", "ov-ubuntu"},
	}
	for _, tt := range tests {
		got := containerName(tt.image)
		if got != tt.want {
			t.Errorf("containerName(%q) = %q, want %q", tt.image, got, tt.want)
		}
	}
}

func TestContainerNameInstance(t *testing.T) {
	tests := []struct {
		image    string
		instance string
		want     string
	}{
		{"githubrunner", "", "ov-githubrunner"},
		{"githubrunner", "runner-1", "ov-githubrunner-runner-1"},
		{"ollama", "gpu2", "ov-ollama-gpu2"},
	}
	for _, tt := range tests {
		got := containerNameInstance(tt.image, tt.instance)
		if got != tt.want {
			t.Errorf("containerNameInstance(%q, %q) = %q, want %q", tt.image, tt.instance, got, tt.want)
		}
	}
}

func TestBuildStartArgsWithEnvVars(t *testing.T) {
	envVars := []string{"FOO=bar", "TOKEN=secret"}
	args := buildStartArgs("docker", "ghcr.io/overthinkos/fedora:latest", "/home/user", 1000, 1000, nil, "ov-fedora", nil, nil, false, "127.0.0.1", envVars, SecurityConfig{})
	want := []string{
		"docker", "run", "-d", "--rm",
		"--name", "ov-fedora",
		"-v", "/home/user:/workspace",
		"-w", "/workspace",
		"-e", "FOO=bar",
		"-e", "TOKEN=secret",
		"ghcr.io/overthinkos/fedora:latest",
		"supervisord", "-n", "-c", "/etc/supervisord.conf",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildStartArgs(envVars) =\n  %v\nwant\n  %v", args, want)
	}
}
