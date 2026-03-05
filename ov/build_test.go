package main

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestBuildLocalArgs(t *testing.T) {
	cmd := &BuildCmd{}
	args := cmd.buildLocalArgs("docker",
		[]string{"ghcr.io/overthinkos/fedora:2026.46.1415", "ghcr.io/overthinkos/fedora:latest"},
		"linux/amd64", "fedora", "ghcr.io/overthinkos")
	want := []string{
		"docker", "build", "-f", "-",
		"-t", "ghcr.io/overthinkos/fedora:2026.46.1415",
		"-t", "ghcr.io/overthinkos/fedora:latest",
		"--platform", "linux/amd64",
		"--cache-from", "ghcr.io/overthinkos/fedora",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildLocalArgs() =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildLocalArgsPodman(t *testing.T) {
	cmd := &BuildCmd{}
	args := cmd.buildLocalArgs("podman",
		[]string{"ghcr.io/overthinkos/fedora:2026.46.1415"},
		"linux/arm64", "fedora", "ghcr.io/overthinkos")
	want := []string{
		"podman", "build", "-f", "-",
		"-t", "ghcr.io/overthinkos/fedora:2026.46.1415",
		"--platform", "linux/arm64",
		"--cache-from", "ghcr.io/overthinkos/fedora",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildLocalArgs(podman) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildDockerPushArgs(t *testing.T) {
	cmd := &BuildCmd{Push: true}
	args := cmd.buildDockerPushArgs(
		[]string{"ghcr.io/overthinkos/fedora:2026.46.1415", "ghcr.io/overthinkos/fedora:latest"},
		[]string{"linux/amd64", "linux/arm64"},
		"fedora", "ghcr.io/overthinkos")
	want := []string{
		"docker", "buildx", "build", "--push", "-f", "-",
		"-t", "ghcr.io/overthinkos/fedora:2026.46.1415",
		"-t", "ghcr.io/overthinkos/fedora:latest",
		"--platform", "linux/amd64,linux/arm64",
		"--cache-from", "type=registry,ref=ghcr.io/overthinkos/cache:fedora",
		"--cache-to", "type=registry,ref=ghcr.io/overthinkos/cache:fedora,mode=max",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildDockerPushArgs() =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildLocalArgsWithGHACache(t *testing.T) {
	cmd := &BuildCmd{Cache: "gha"}
	args := cmd.buildLocalArgs("docker",
		[]string{"ghcr.io/overthinkos/fedora:latest"},
		"linux/amd64", "fedora", "ghcr.io/overthinkos")
	want := []string{
		"docker", "build", "-f", "-",
		"-t", "ghcr.io/overthinkos/fedora:latest",
		"--platform", "linux/amd64",
		"--cache-from", "type=gha,scope=fedora",
		"--cache-to", "type=gha,mode=max,scope=fedora",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildLocalArgs(gha) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildDockerPushArgsWithGHACache(t *testing.T) {
	cmd := &BuildCmd{Cache: "gha"}
	args := cmd.buildDockerPushArgs(
		[]string{"ghcr.io/overthinkos/fedora:latest"},
		[]string{"linux/amd64", "linux/arm64"},
		"fedora", "ghcr.io/overthinkos")
	want := []string{
		"docker", "buildx", "build", "--push", "-f", "-",
		"-t", "ghcr.io/overthinkos/fedora:latest",
		"--platform", "linux/amd64,linux/arm64",
		"--cache-from", "type=gha,scope=fedora",
		"--cache-to", "type=gha,mode=max,scope=fedora",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildDockerPushArgs(gha) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildLocalArgsWithRegistryCache(t *testing.T) {
	cmd := &BuildCmd{Cache: "registry"}
	args := cmd.buildLocalArgs("docker",
		[]string{"ghcr.io/overthinkos/fedora:latest"},
		"linux/amd64", "fedora", "ghcr.io/overthinkos")
	want := []string{
		"docker", "build", "-f", "-",
		"-t", "ghcr.io/overthinkos/fedora:latest",
		"--platform", "linux/amd64",
		"--cache-from", "type=registry,ref=ghcr.io/overthinkos/cache:fedora",
		"--cache-to", "type=registry,ref=ghcr.io/overthinkos/cache:fedora,mode=max",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildLocalArgs(registry) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildDockerPushArgsWithRegistryCache(t *testing.T) {
	cmd := &BuildCmd{Cache: "registry"}
	args := cmd.buildDockerPushArgs(
		[]string{"ghcr.io/overthinkos/fedora:latest"},
		[]string{"linux/amd64", "linux/arm64"},
		"fedora", "ghcr.io/overthinkos")
	want := []string{
		"docker", "buildx", "build", "--push", "-f", "-",
		"-t", "ghcr.io/overthinkos/fedora:latest",
		"--platform", "linux/amd64,linux/arm64",
		"--cache-from", "type=registry,ref=ghcr.io/overthinkos/cache:fedora",
		"--cache-to", "type=registry,ref=ghcr.io/overthinkos/cache:fedora,mode=max",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildDockerPushArgs(registry) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildRegistryCacheNoRegistry(t *testing.T) {
	cmd := &BuildCmd{Cache: "registry"}
	args := cmd.buildLocalArgs("docker",
		[]string{"fedora:latest"},
		"linux/amd64", "fedora", "")
	want := []string{
		"docker", "build", "-f", "-",
		"-t", "fedora:latest",
		"--platform", "linux/amd64",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildLocalArgs(registry, no registry) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildPodmanPushArgs(t *testing.T) {
	cmd := &BuildCmd{Push: true}
	args := cmd.buildPodmanPushArgs(
		[]string{"ghcr.io/overthinkos/fedora:2026.46.1415"},
		[]string{"linux/amd64", "linux/arm64"},
		"fedora", "ghcr.io/overthinkos")
	want := []string{
		"podman", "build", "-f", "-",
		"--manifest", "ghcr.io/overthinkos/fedora:2026.46.1415",
		"--platform", "linux/amd64,linux/arm64",
		"--cache-from", "type=registry,ref=ghcr.io/overthinkos/cache:fedora",
		"--cache-to", "type=registry,ref=ghcr.io/overthinkos/cache:fedora,mode=max",
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

func TestFilterImagesIncludesBuilder(t *testing.T) {
	images := map[string]*ResolvedImage{
		"builder": {
			Name:           "builder",
			IsExternalBase: true,
		},
		"fedora": {
			Name:           "fedora",
			IsExternalBase: true,
			Builder:        "builder",
		},
		"app": {
			Name:           "app",
			Base:           "fedora",
			IsExternalBase: false,
			Builder:        "builder",
		},
	}

	order := []string{"builder", "fedora", "app"}

	// Request only app — should pull in fedora (base) and builder
	filtered, err := filterImages(order, []string{"app"}, images)
	if err != nil {
		t.Fatalf("filterImages() error: %v", err)
	}
	want := []string{"builder", "fedora", "app"}
	if !reflect.DeepEqual(filtered, want) {
		t.Errorf("filterImages() = %v, want %v", filtered, want)
	}
}

func TestBuildLocalArgsWithImageCache(t *testing.T) {
	cmd := &BuildCmd{Cache: "image"}
	args := cmd.buildLocalArgs("podman",
		[]string{"ghcr.io/overthinkos/fedora:latest"},
		"linux/amd64", "fedora", "ghcr.io/overthinkos")
	want := []string{
		"podman", "build", "-f", "-",
		"-t", "ghcr.io/overthinkos/fedora:latest",
		"--platform", "linux/amd64",
		"--cache-from", "ghcr.io/overthinkos/fedora",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildLocalArgs(image) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildDockerPushArgsWithImageCache(t *testing.T) {
	cmd := &BuildCmd{Cache: "image"}
	args := cmd.buildDockerPushArgs(
		[]string{"ghcr.io/overthinkos/fedora:latest"},
		[]string{"linux/amd64"},
		"fedora", "ghcr.io/overthinkos")
	want := []string{
		"docker", "buildx", "build", "--push", "-f", "-",
		"-t", "ghcr.io/overthinkos/fedora:latest",
		"--platform", "linux/amd64",
		"--cache-from", "ghcr.io/overthinkos/fedora",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildDockerPushArgs(image) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildPodmanPushArgsWithImageCache(t *testing.T) {
	cmd := &BuildCmd{Cache: "image"}
	args := cmd.buildPodmanPushArgs(
		[]string{"ghcr.io/overthinkos/fedora:2026.46.1415"},
		[]string{"linux/amd64"},
		"fedora", "ghcr.io/overthinkos")
	want := []string{
		"podman", "build", "-f", "-",
		"--manifest", "ghcr.io/overthinkos/fedora:2026.46.1415",
		"--platform", "linux/amd64",
		"--cache-from", "ghcr.io/overthinkos/fedora",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildPodmanPushArgs(image) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildImageCacheNoRegistry(t *testing.T) {
	cmd := &BuildCmd{Cache: "image"}
	args := cmd.buildLocalArgs("podman",
		[]string{"fedora:latest"},
		"linux/amd64", "fedora", "")
	want := []string{
		"podman", "build", "-f", "-",
		"-t", "fedora:latest",
		"--platform", "linux/amd64",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildLocalArgs(image, no registry) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestRetryCmdSucceedsFirstAttempt(t *testing.T) {
	calls := 0
	err := retryCmd(3, time.Millisecond, func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}

func TestRetryCmdSucceedsAfterRetries(t *testing.T) {
	calls := 0
	err := retryCmd(3, time.Millisecond, func() error {
		calls++
		if calls < 3 {
			return fmt.Errorf("transient error")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if calls != 3 {
		t.Errorf("expected 3 calls, got %d", calls)
	}
}

func TestRetryCmdExhaustsAttempts(t *testing.T) {
	calls := 0
	err := retryCmd(3, time.Millisecond, func() error {
		calls++
		return fmt.Errorf("persistent error")
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if calls != 3 {
		t.Errorf("expected 3 calls, got %d", calls)
	}
}

func TestBuildDefaultCacheNoRegistry(t *testing.T) {
	cmd := &BuildCmd{}
	args := cmd.buildLocalArgs("docker",
		[]string{"fedora:latest"},
		"linux/amd64", "fedora", "")
	want := []string{
		"docker", "build", "-f", "-",
		"-t", "fedora:latest",
		"--platform", "linux/amd64",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildLocalArgs(no registry) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildNoCache(t *testing.T) {
	cmd := &BuildCmd{NoCache: true}
	args := cmd.buildLocalArgs("docker",
		[]string{"ghcr.io/overthinkos/fedora:latest"},
		"linux/amd64", "fedora", "ghcr.io/overthinkos")
	want := []string{
		"docker", "build", "-f", "-",
		"-t", "ghcr.io/overthinkos/fedora:latest",
		"--platform", "linux/amd64",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildLocalArgs(no-cache) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildCacheNone(t *testing.T) {
	cmd := &BuildCmd{Cache: "none"}
	args := cmd.buildLocalArgs("docker",
		[]string{"ghcr.io/overthinkos/fedora:latest"},
		"linux/amd64", "fedora", "ghcr.io/overthinkos")
	want := []string{
		"docker", "build", "-f", "-",
		"-t", "ghcr.io/overthinkos/fedora:latest",
		"--platform", "linux/amd64",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildLocalArgs(cache=none) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestHostPlatform(t *testing.T) {
	p := hostPlatform()
	// Should start with linux/
	if p != "linux/amd64" && p != "linux/arm64" {
		t.Logf("hostPlatform() = %q (non-standard arch, that's OK)", p)
	}
}
