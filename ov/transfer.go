package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// LocalImageExists checks whether an image reference exists in the given engine's local store.
// Package-level var for testability (same pattern as DetectGPU in gpu.go).
var LocalImageExists = defaultLocalImageExists

func defaultLocalImageExists(engine, imageRef string) bool {
	binary := EngineBinary(engine)
	switch engine {
	case "podman":
		cmd := exec.Command(binary, "image", "exists", imageRef)
		return cmd.Run() == nil
	default:
		// Docker has no "image exists" subcommand; use "image inspect"
		cmd := exec.Command(binary, "image", "inspect", imageRef)
		cmd.Stdout = nil
		cmd.Stderr = nil
		return cmd.Run() == nil
	}
}

// TransferImage pipes an image from one engine to another via save | load.
func TransferImage(srcEngine, dstEngine, imageRef string) error {
	srcBinary := EngineBinary(srcEngine)
	dstBinary := EngineBinary(dstEngine)

	fmt.Fprintf(os.Stderr, "Transferring %s from %s to %s\n", imageRef, srcEngine, dstEngine)

	save := exec.Command(srcBinary, "save", imageRef)
	load := exec.Command(dstBinary, "load")

	pipe, err := save.StdoutPipe()
	if err != nil {
		return fmt.Errorf("creating pipe: %w", err)
	}
	load.Stdin = pipe
	load.Stderr = os.Stderr

	if err := load.Start(); err != nil {
		return fmt.Errorf("starting %s load: %w", dstBinary, err)
	}
	if err := save.Run(); err != nil {
		return fmt.Errorf("%s save failed: %w", srcBinary, err)
	}
	if err := load.Wait(); err != nil {
		return fmt.Errorf("%s load failed: %w", dstBinary, err)
	}

	fmt.Fprintf(os.Stderr, "Transferred %s to %s\n", imageRef, dstEngine)
	return nil
}

// EnsureImage ensures the image is available in the run engine's local store.
// Three-tier fallback (each step independent):
//
//  1. Already-present short-circuit (LocalImageExists in run engine).
//  2. Cross-engine transfer (`docker save | podman load`) when build
//     engine != run engine AND the image is present in the build
//     engine's storage.
//  3. Canonical `EnsureImagePresent` — pulls from the registry and
//     falls back to a local `ov image build <name>` when the ref maps
//     to a project image.yml entry. This is the same code path
//     BuilderRun, the eval preflight, and `ov image pull` all go
//     through (see ov/ensure_image.go).
//
// Returns ErrImageNotLocal (wrapped with the ref) only when ALL three
// tiers fail.
func EnsureImage(imageRef string, rt *ResolvedRuntime) error {
	if LocalImageExists(rt.RunEngine, imageRef) {
		return nil
	}

	// Cross-engine transfer first when applicable: it's faster than a
	// network pull and works offline.
	if rt.BuildEngine != rt.RunEngine && LocalImageExists(rt.BuildEngine, imageRef) {
		return TransferImage(rt.BuildEngine, rt.RunEngine, imageRef)
	}

	// Generic ensure: pull, fall back to local build for project
	// images. Loads the project cfg if cwd has one; gracefully
	// degrades to pull-only when no project is reachable.
	cfg, projectDir := loadProjectCfgFromCwd()
	if err := EnsureImagePresent(context.Background(), imageRef, cfg, projectDir); err == nil {
		return nil
	}

	return fmt.Errorf("%w: %s", ErrImageNotLocal, imageRef)
}

// loadProjectCfgFromCwd returns the project config + dir when the
// caller's cwd is inside an ov project; (nil, "") otherwise. EnsureImage
// (and any caller of EnsureImagePresent that doesn't carry project
// state) uses this to opportunistically opt into the build-fallback
// path.
func loadProjectCfgFromCwd() (*Config, string) {
	dir, err := os.Getwd()
	if err != nil || dir == "" {
		return nil, ""
	}
	cfg, err := LoadConfig(dir)
	if err != nil || cfg == nil {
		return nil, dir
	}
	return cfg, dir
}
