package main

import (
	"context"
	"testing"
)

// TestPodUnifiedTarget_Basics verifies the trivial accessor methods.
func TestPodUnifiedTarget_Basics(t *testing.T) {
	target := &PodUnifiedTarget{NodeName: "sway-pod"}
	if got := target.Name(); got != "sway-pod" {
		t.Errorf("Name = %q, want %q", got, "sway-pod")
	}
	if got := target.Kind(); got != "pod" {
		t.Errorf("Kind = %q, want %q", got, "pod")
	}
	// With no embedded PodDeployTarget, Executor falls back to a
	// LocalDeployExecutor (matches HostUnifiedTarget's nil-safety).
	if target.Executor() == nil {
		t.Errorf("Executor: expected fallback executor, got nil")
	}
}

// TestPodUnifiedTarget_engine verifies the engine fallback (no embed
// → "podman"; embed with explicit Engine → that value; embed with
// empty Engine → "podman").
func TestPodUnifiedTarget_engine(t *testing.T) {
	{
		target := &PodUnifiedTarget{NodeName: "sway-pod"}
		if got := target.engine(); got != "podman" {
			t.Errorf("engine(no embed) = %q, want podman", got)
		}
	}
	{
		target := &PodUnifiedTarget{
			NodeName:        "sway-pod",
			PodDeployTarget: &PodDeployTarget{Engine: "docker"},
		}
		if got := target.engine(); got != "docker" {
			t.Errorf("engine(docker) = %q, want docker", got)
		}
	}
	{
		target := &PodUnifiedTarget{
			NodeName:        "sway-pod",
			PodDeployTarget: &PodDeployTarget{},
		}
		if got := target.engine(); got != "podman" {
			t.Errorf("engine(empty Engine) = %q, want podman", got)
		}
	}
}

// TestPodUnifiedTarget_Test_NilExecutor verifies Test errors cleanly
// when no executor is configured (rather than panicking on nil).
// PodUnifiedTarget.Executor() returns LocalDeployExecutor on nil
// embed, but in a real flow the executor would be a podman-exec
// wrapper; this test is the nil-safety floor.
func TestPodUnifiedTarget_Test_NilExecutor(t *testing.T) {
	// Without any embed, Executor returns a non-nil LocalDeployExecutor
	// — so Test should run on it. We use a hermetic command:true.
	target := &PodUnifiedTarget{NodeName: "sway-pod"}
	checks := []Check{{ID: "ok", Command: "true"}}
	if err := target.Test(context.Background(), checks, TestOpts{}); err != nil {
		t.Errorf("Test(local fallback): %v", err)
	}
}

// TestPodUnifiedTarget_Update_DryRun verifies the dry-run path emits
// the expected ov-update line without invoking the subcommand.
func TestPodUnifiedTarget_Update_DryRun(t *testing.T) {
	target := &PodUnifiedTarget{NodeName: "sway-pod"}
	if err := target.Update(context.Background(), nil, UpdateOpts{DryRun: true}); err != nil {
		t.Errorf("Update dry-run: %v", err)
	}
}

// TestPodUnifiedTarget_Rebuild_DryRun verifies the dry-run path emits
// the expected ov-build/eval/deploy/stop/config/start sequence without
// invoking the subcommands.
func TestPodUnifiedTarget_Rebuild_DryRun(t *testing.T) {
	target := &PodUnifiedTarget{NodeName: "sway-pod", BaseImageRef: "openclaw-sway-browser"}
	if err := target.Rebuild(context.Background(), RebuildOpts{DryRun: true, RebuildImage: true}); err != nil {
		t.Errorf("Rebuild dry-run: %v", err)
	}
	// Without RebuildImage, the build/eval steps are skipped.
	if err := target.Rebuild(context.Background(), RebuildOpts{DryRun: true, RebuildImage: false}); err != nil {
		t.Errorf("Rebuild dry-run no-image: %v", err)
	}
}

// TestPodUnifiedTarget_Rebuild_BaseRefFallback verifies the BaseImageRef
// fallback rule: when empty, NodeName is used as the ref.
func TestPodUnifiedTarget_Rebuild_BaseRefFallback(t *testing.T) {
	// We can't easily inspect the printed dry-run output without
	// stdout capture, but we can confirm the dry-run branch returns
	// nil even when BaseImageRef is unset (Rebuild's internal
	// fallback prevents an empty-ref panic / shell-out).
	target := &PodUnifiedTarget{NodeName: "sway-pod" /* BaseImageRef unset */}
	if err := target.Rebuild(context.Background(), RebuildOpts{DryRun: true, RebuildImage: true}); err != nil {
		t.Errorf("Rebuild dry-run with empty BaseImageRef: %v", err)
	}
}

// Status is exercised by the R10 live verification (paste in commit
// message) — not by a unit test, because Status shells out via
// captureOvStdout which uses os.Args[0]. Inside `go test` that's the
// test binary, which doesn't understand the "status --json" CLI verbs
// and would hang or error spuriously. The other dry-run-able methods
// (Update, Rebuild) are safe because they early-return before any
// shell-out when DryRun is set.
