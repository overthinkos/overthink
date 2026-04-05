package main

import (
	"reflect"
	"testing"
)

func withTerminal(t *testing.T, tty bool) {
	orig := isTerminal
	isTerminal = func() bool { return tty }
	t.Cleanup(func() { isTerminal = orig })
}

func withForceTTY(t *testing.T, force bool) {
	orig := forceTTY
	forceTTY = force
	t.Cleanup(func() { forceTTY = orig })
}

func TestBuildShellArgs(t *testing.T) {
	withTerminal(t, true)
	args := buildShellArgs("docker", "ghcr.io/overthinkos/fedora:latest", 1000, 1000, nil, nil, nil, false, "", "127.0.0.1", nil, SecurityConfig{}, "/home/user/workspace")
	want := []string{
		"docker", "run", "--rm", "-it",
		"-w", "/home/user/workspace",
		"--user", "1000:1000",
		"--entrypoint", "bash",
		"ghcr.io/overthinkos/fedora:latest",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildShellArgs() =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildShellArgsCustomUIDGID(t *testing.T) {
	withTerminal(t, true)
	args := buildShellArgs("docker", "fedora:latest", 1001, 1002, nil, nil, nil, false, "", "127.0.0.1", nil, SecurityConfig{}, "/home/user/workspace")
	want := []string{
		"docker", "run", "--rm", "-it",
		"-w", "/home/user/workspace",
		"--user", "1001:1002",
		"--entrypoint", "bash",
		"fedora:latest",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildShellArgs() =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildShellArgsWithPorts(t *testing.T) {
	withTerminal(t, true)
	args := buildShellArgs("docker", "ghcr.io/overthinkos/fedora:latest", 1000, 1000, []string{"9090:9090", "8080:8080"}, nil, nil, false, "", "127.0.0.1", nil, SecurityConfig{}, "/home/user/workspace")
	want := []string{
		"docker", "run", "--rm", "-it",
		"-w", "/home/user/workspace",
		"--user", "1000:1000",
		"-p", "127.0.0.1:9090:9090",
		"-p", "127.0.0.1:8080:8080",
		"--entrypoint", "bash",
		"ghcr.io/overthinkos/fedora:latest",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildShellArgs() =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildShellArgsWithSinglePort(t *testing.T) {
	withTerminal(t, true)
	args := buildShellArgs("docker", "ghcr.io/overthinkos/fedora:latest", 1000, 1000, []string{"8080"}, nil, nil, false, "", "127.0.0.1", nil, SecurityConfig{}, "/home/user/workspace")
	want := []string{
		"docker", "run", "--rm", "-it",
		"-w", "/home/user/workspace",
		"--user", "1000:1000",
		"-p", "127.0.0.1:8080:8080",
		"--entrypoint", "bash",
		"ghcr.io/overthinkos/fedora:latest",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildShellArgs() =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildShellArgsWithVolumes(t *testing.T) {
	withTerminal(t, true)
	volumes := []VolumeMount{
		{VolumeName: "ov-openclaw-data", ContainerPath: "/home/user/.openclaw"},
	}
	args := buildShellArgs("docker", "ghcr.io/overthinkos/openclaw:latest", 1000, 1000, nil, volumes, nil, false, "", "127.0.0.1", nil, SecurityConfig{}, "/home/user/workspace")
	want := []string{
		"docker", "run", "--rm", "-it",
		"-w", "/home/user/workspace",
		"--user", "1000:1000",
		"-v", "ov-openclaw-data:/home/user/.openclaw",
		"--entrypoint", "bash",
		"ghcr.io/overthinkos/openclaw:latest",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildShellArgs() =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildShellArgsWithGPU(t *testing.T) {
	withTerminal(t, true)
	args := buildShellArgs("docker", "ghcr.io/overthinkos/ollama:latest", 1000, 1000, nil, nil, nil, true, "", "127.0.0.1", nil, SecurityConfig{}, "/home/user/workspace")
	want := []string{
		"docker", "run", "--rm", "-it",
		"-w", "/home/user/workspace",
		"--user", "1000:1000",
		"--gpus", "all",
		"--entrypoint", "bash",
		"ghcr.io/overthinkos/ollama:latest",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildShellArgs(gpu=true) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildShellArgsWithGPUPodman(t *testing.T) {
	withTerminal(t, true)
	args := buildShellArgs("podman", "ghcr.io/overthinkos/ollama:latest", 1000, 1000, nil, nil, nil, true, "", "127.0.0.1", nil, SecurityConfig{}, "/home/user/workspace")
	want := []string{
		"podman", "run", "--rm", "-it",
		"-w", "/home/user/workspace",
		"--user", "1000:1000",
		"--device", "nvidia.com/gpu=all",
		"--entrypoint", "bash",
		"ghcr.io/overthinkos/ollama:latest",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildShellArgs(podman+gpu) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildShellArgsWithoutGPU(t *testing.T) {
	args := buildShellArgs("docker", "ghcr.io/overthinkos/ollama:latest", 1000, 1000, nil, nil, nil, false, "", "127.0.0.1", nil, SecurityConfig{}, "/home/user/workspace")
	for _, arg := range args {
		if arg == "--gpus" {
			t.Error("buildShellArgs(gpu=false) should not contain --gpus")
		}
	}
}

func TestLocalizePort(t *testing.T) {
	tests := []struct {
		input    string
		bindAddr string
		want     string
	}{
		{"80:8000", "127.0.0.1", "127.0.0.1:80:8000"},
		{"8080:8080", "127.0.0.1", "127.0.0.1:8080:8080"},
		{"8080", "127.0.0.1", "127.0.0.1:8080:8080"},
		{"9090", "127.0.0.1", "127.0.0.1:9090:9090"},
		{"80:8000", "0.0.0.0", "0.0.0.0:80:8000"},
		{"8080", "0.0.0.0", "0.0.0.0:8080:8080"},
		{"47998:47998/udp", "127.0.0.1", "127.0.0.1:47998:47998/udp"},
		{"48000/udp", "127.0.0.1", "127.0.0.1:48000:48000/udp"},
		{"47990:47990/tcp", "127.0.0.1", "127.0.0.1:47990:47990/tcp"},
	}
	for _, tt := range tests {
		t.Run(tt.bindAddr+"/"+tt.input, func(t *testing.T) {
			got := localizePort(tt.input, tt.bindAddr)
			if got != tt.want {
				t.Errorf("localizePort(%q, %q) = %q, want %q", tt.input, tt.bindAddr, got, tt.want)
			}
		})
	}
}

func TestBuildShellArgsWithCommand(t *testing.T) {
	withTerminal(t, false)
	args := buildShellArgs("docker", "ghcr.io/overthinkos/fedora:latest", 1000, 1000, nil, nil, nil, false, "echo hello", "127.0.0.1", nil, SecurityConfig{}, "/home/user/workspace")
	want := []string{
		"docker", "run", "--rm", "-i",
		"-w", "/home/user/workspace",
		"--user", "1000:1000",
		"--entrypoint", "bash",
		"ghcr.io/overthinkos/fedora:latest",
		"-c", "echo hello",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildShellArgs(command) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildShellArgsWithCommandAndGPU(t *testing.T) {
	withTerminal(t, false)
	args := buildShellArgs("docker", "ghcr.io/overthinkos/ollama:latest", 1000, 1000, nil, nil, nil, true, "nvidia-smi", "127.0.0.1", nil, SecurityConfig{}, "/home/user/workspace")
	want := []string{
		"docker", "run", "--rm", "-i",
		"-w", "/home/user/workspace",
		"--user", "1000:1000",
		"--gpus", "all",
		"--entrypoint", "bash",
		"ghcr.io/overthinkos/ollama:latest",
		"-c", "nvidia-smi",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildShellArgs(command+gpu) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildExecArgs(t *testing.T) {
	withTerminal(t, true)
	args := buildExecArgs("docker", "ov-fedora", 1000, 1000, "", nil, "/home/user/workspace")
	want := []string{
		"docker", "exec", "-it",
		"--user", "1000:1000",
		"-w", "/home/user/workspace",
		"ov-fedora",
		"bash",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildExecArgs() =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildExecArgsWithCommand(t *testing.T) {
	withTerminal(t, false)
	args := buildExecArgs("docker", "ov-openclaw", 1000, 1000, "echo hello", nil, "/home/user/workspace")
	want := []string{
		"docker", "exec", "-i",
		"--user", "1000:1000",
		"-w", "/home/user/workspace",
		"ov-openclaw",
		"bash",
		"-c", "echo hello",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildExecArgs(command) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildShellArgsWithCommandTTY(t *testing.T) {
	withTerminal(t, true)
	args := buildShellArgs("docker", "ghcr.io/overthinkos/fedora:latest", 1000, 1000, nil, nil, nil, false, "openclaw tui", "127.0.0.1", nil, SecurityConfig{}, "/home/user/workspace")
	want := []string{
		"docker", "run", "--rm", "-it",
		"-w", "/home/user/workspace",
		"--user", "1000:1000",
		"--entrypoint", "bash",
		"ghcr.io/overthinkos/fedora:latest",
		"-c", "openclaw tui",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildShellArgs(command+tty) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildExecArgsWithCommandTTY(t *testing.T) {
	withTerminal(t, true)
	args := buildExecArgs("docker", "ov-openclaw", 1000, 1000, "openclaw tui", nil, "/home/user/workspace")
	want := []string{
		"docker", "exec", "-it",
		"--user", "1000:1000",
		"-w", "/home/user/workspace",
		"ov-openclaw",
		"bash",
		"-c", "openclaw tui",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildExecArgs(command+tty) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildShellArgsForceTTY(t *testing.T) {
	withTerminal(t, false) // no real terminal
	withForceTTY(t, true)  // but --tty flag set
	args := buildShellArgs("docker", "ghcr.io/overthinkos/fedora:latest", 1000, 1000, nil, nil, nil, false, "openclaw models auth login", "127.0.0.1", nil, SecurityConfig{}, "/home/user/workspace")
	want := []string{
		"docker", "run", "--rm", "-it",
		"-w", "/home/user/workspace",
		"--user", "1000:1000",
		"--entrypoint", "bash", "ghcr.io/overthinkos/fedora:latest",
		"-c", "openclaw models auth login",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildShellArgs(forceTTY) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildExecArgsForceTTY(t *testing.T) {
	withTerminal(t, false) // no real terminal
	withForceTTY(t, true)  // but --tty flag set
	args := buildExecArgs("docker", "ov-openclaw", 1000, 1000, "openclaw models auth login", nil, "/home/user/workspace")
	want := []string{
		"docker", "exec", "-it",
		"--user", "1000:1000",
		"-w", "/home/user/workspace",
		"ov-openclaw",
		"bash",
		"-c", "openclaw models auth login",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildExecArgs(forceTTY) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildExecArgsCustomUIDGID(t *testing.T) {
	withTerminal(t, true)
	args := buildExecArgs("podman", "ov-ubuntu", 1001, 1002, "", nil, "/home/user/workspace")
	want := []string{
		"podman", "exec", "-it",
		"--user", "1001:1002",
		"-w", "/home/user/workspace",
		"ov-ubuntu",
		"bash",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildExecArgs(custom uid/gid) =\n  %v\nwant\n  %v", args, want)
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
			registry: "ghcr.io/overthinkos",
			image:    "fedora",
			tag:      "latest",
			want:     "ghcr.io/overthinkos/fedora:latest",
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
			registry: "ghcr.io/overthinkos",
			image:    "ubuntu",
			tag:      "2026.46.1415",
			want:     "ghcr.io/overthinkos/ubuntu:2026.46.1415",
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

func TestBuildShellArgsWithEnvVars(t *testing.T) {
	withTerminal(t, true)
	envVars := []string{"DB_HOST=localhost", "SECRET=abc"}
	args := buildShellArgs("docker", "myapp:latest", 1000, 1000, nil, nil, nil, false, "", "127.0.0.1", envVars, SecurityConfig{}, "/home/user/workspace")

	// Check env vars appear before --entrypoint
	entryIdx := -1
	envIdx := -1
	for i, arg := range args {
		if arg == "-e" && envIdx == -1 {
			envIdx = i
		}
		if arg == "--entrypoint" {
			entryIdx = i
		}
	}
	if envIdx < 0 {
		t.Fatal("expected -e flags in args")
	}
	if entryIdx < envIdx {
		t.Error("expected -e flags before --entrypoint")
	}

	// Verify values
	found := 0
	for i, arg := range args {
		if arg == "-e" && i+1 < len(args) {
			if args[i+1] == "DB_HOST=localhost" || args[i+1] == "SECRET=abc" {
				found++
			}
		}
	}
	if found != 2 {
		t.Errorf("expected 2 env vars, found %d in args: %v", found, args)
	}
}

func TestBuildExecArgsWithEnvVars(t *testing.T) {
	withTerminal(t, true)
	envVars := []string{"FOO=bar"}
	args := buildExecArgs("docker", "ov-myapp", 1000, 1000, "", envVars, "/home/user/workspace")
	want := []string{
		"docker", "exec", "-it",
		"--user", "1000:1000",
		"-w", "/home/user/workspace",
		"-e", "FOO=bar",
		"ov-myapp",
		"bash",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildExecArgs(envVars) =\n  %v\nwant\n  %v", args, want)
	}
}
