package main

import (
	"testing"
)

// TestUpdateCmd_BuildFlag_InvokesBuild verifies that defect E is fixed:
// UpdateCmd.Run calls the build hook when c.Build == true for a local
// image. Without the fix, c.Build was dead code for local images and
// `ov update ollama --build` silently restarted the service with the
// stale image.
//
// This test spies on updateCmdBuildFn instead of calling ResolveRuntime /
// EnsureImage / systemctl, which would require a live environment. The
// spy captures the image+tag arguments, returns an error to short-circuit
// the rest of UpdateCmd.Run (we only care about whether the hook fired).
func TestUpdateCmd_BuildFlag_InvokesBuild(t *testing.T) {
	origFn := updateCmdBuildFn
	defer func() { updateCmdBuildFn = origFn }()

	// Use a deliberately-nonexistent image name so the rest of UpdateCmd.Run
	// fails quickly on EnsureImage (image not in local store) without
	// touching any live systemd units.
	const fakeImage = "nonexistent-test-image-do-not-build"

	t.Run("Build=true invokes build hook for local image", func(t *testing.T) {
		calls := 0
		var gotImage, gotTag string
		updateCmdBuildFn = func(image, tag string) error {
			calls++
			gotImage = image
			gotTag = tag
			return errShortCircuit
		}
		cmd := &UpdateCmd{Image: fakeImage, Tag: "latest", Build: true}
		err := cmd.Run()
		if err == nil {
			t.Fatal("expected short-circuit error, got nil")
		}
		if calls != 1 {
			t.Errorf("build hook calls = %d, want 1", calls)
		}
		if gotImage != fakeImage {
			t.Errorf("image = %q, want %q", gotImage, fakeImage)
		}
		if gotTag != "latest" {
			t.Errorf("tag = %q, want %q", gotTag, "latest")
		}
	})

	t.Run("Build=false does NOT invoke build hook", func(t *testing.T) {
		calls := 0
		updateCmdBuildFn = func(image, tag string) error {
			calls++
			return errShortCircuit
		}
		cmd := &UpdateCmd{Image: fakeImage, Tag: "latest", Build: false}
		// With Build=false, the build hook should NOT fire. The rest of
		// UpdateCmd.Run will error on EnsureImage (fake image not in local
		// store), but we only care that the build hook abstained.
		_ = cmd.Run()
		if calls != 0 {
			t.Errorf("build hook calls = %d, want 0 (not requested)", calls)
		}
	})
}

// errShortCircuit is a sentinel error used by the build-hook spy to
// terminate UpdateCmd.Run early without touching runtime state.
var errShortCircuit = updateCmdTestError("short-circuit from test")

type updateCmdTestError string

func (e updateCmdTestError) Error() string { return string(e) }
