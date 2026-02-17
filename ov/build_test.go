package main

import (
	"reflect"
	"testing"
)

func TestBuildLocalArgs(t *testing.T) {
	cmd := &BuildCmd{}
	args := cmd.buildLocalArgs("docker", ".build/fedora/Containerfile",
		[]string{"ghcr.io/atrawog/fedora:2026.46.1415", "ghcr.io/atrawog/fedora:latest"},
		"linux/amd64")
	want := []string{
		"docker", "build", "-f", ".build/fedora/Containerfile",
		"-t", "ghcr.io/atrawog/fedora:2026.46.1415",
		"-t", "ghcr.io/atrawog/fedora:latest",
		"--platform", "linux/amd64",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildLocalArgs() =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildLocalArgsPodman(t *testing.T) {
	cmd := &BuildCmd{}
	args := cmd.buildLocalArgs("podman", ".build/fedora/Containerfile",
		[]string{"ghcr.io/atrawog/fedora:2026.46.1415"},
		"linux/arm64")
	want := []string{
		"podman", "build", "-f", ".build/fedora/Containerfile",
		"-t", "ghcr.io/atrawog/fedora:2026.46.1415",
		"--platform", "linux/arm64",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildLocalArgs(podman) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildDockerPushArgs(t *testing.T) {
	cmd := &BuildCmd{}
	args := cmd.buildDockerPushArgs(".build/fedora/Containerfile",
		[]string{"ghcr.io/atrawog/fedora:2026.46.1415", "ghcr.io/atrawog/fedora:latest"},
		[]string{"linux/amd64", "linux/arm64"})
	want := []string{
		"docker", "buildx", "build", "--push", "-f", ".build/fedora/Containerfile",
		"-t", "ghcr.io/atrawog/fedora:2026.46.1415",
		"-t", "ghcr.io/atrawog/fedora:latest",
		"--platform", "linux/amd64,linux/arm64",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildDockerPushArgs() =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildPodmanPushArgs(t *testing.T) {
	cmd := &BuildCmd{}
	args := cmd.buildPodmanPushArgs(".build/fedora/Containerfile",
		[]string{"ghcr.io/atrawog/fedora:2026.46.1415"},
		[]string{"linux/amd64", "linux/arm64"})
	want := []string{
		"podman", "build", "-f", ".build/fedora/Containerfile",
		"--manifest", "ghcr.io/atrawog/fedora:2026.46.1415",
		"--platform", "linux/amd64,linux/arm64",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildPodmanPushArgs() =\n  %v\nwant\n  %v", args, want)
	}
}

func TestFilterImages(t *testing.T) {
	images := map[string]*ResolvedImage{
		"fedora": {
			Name:           "fedora",
			IsExternalBase: true,
		},
		"fedora-test": {
			Name:           "fedora-test",
			Base:           "fedora",
			IsExternalBase: false,
		},
		"ubuntu": {
			Name:           "ubuntu",
			IsExternalBase: true,
		},
	}

	order := []string{"fedora", "ubuntu", "fedora-test"}

	// Request only fedora-test — should pull in fedora as dependency
	filtered, err := filterImages(order, []string{"fedora-test"}, images)
	if err != nil {
		t.Fatalf("filterImages() error: %v", err)
	}
	want := []string{"fedora", "fedora-test"}
	if !reflect.DeepEqual(filtered, want) {
		t.Errorf("filterImages() = %v, want %v", filtered, want)
	}
}

func TestFilterImagesUnknown(t *testing.T) {
	images := map[string]*ResolvedImage{
		"fedora": {Name: "fedora", IsExternalBase: true},
	}
	_, err := filterImages([]string{"fedora"}, []string{"nonexistent"}, images)
	if err == nil {
		t.Error("expected error for unknown image")
	}
}

func TestHostPlatform(t *testing.T) {
	p := hostPlatform()
	// Should start with linux/
	if p != "linux/amd64" && p != "linux/arm64" {
		t.Logf("hostPlatform() = %q (non-standard arch, that's OK)", p)
	}
}
