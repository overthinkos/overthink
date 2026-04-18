package main

import (
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

// EnsureImage ensures the image is available in the run engine's local store,
// transferring from the build engine if needed. Returns ErrImageNotLocal
// (wrapped with the image ref) when the image is absent from both engines —
// callers unwrap this at the CLI boundary to render the "ov image pull"
// recommendation.
func EnsureImage(imageRef string, rt *ResolvedRuntime) error {
	if LocalImageExists(rt.RunEngine, imageRef) {
		return nil
	}

	if rt.BuildEngine == rt.RunEngine {
		return fmt.Errorf("%w: %s", ErrImageNotLocal, imageRef)
	}

	if !LocalImageExists(rt.BuildEngine, imageRef) {
		return fmt.Errorf("%w: %s", ErrImageNotLocal, imageRef)
	}

	return TransferImage(rt.BuildEngine, rt.RunEngine, imageRef)
}
