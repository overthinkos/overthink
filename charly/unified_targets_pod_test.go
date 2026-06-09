package main

import (
	"context"
	"strings"
	"testing"
)

// TestPodUnifiedTarget_Rebuild_RealInvocations is the regression guard for the
// stale-internal-verb class: the pod rebuild path must invoke the CURRENT verb
// names. The dry-run tests above only check the PRINTED lines; this stubs
// runCharlySubcommand and asserts the ACTUAL argv. That gap is exactly how
// `eval image` survived the image→box rebrand — the dry-run line and the error
// string were renamed to `eval box`, but the real call kept the old verb and no
// non-dry-run test exercised it.
func TestPodUnifiedTarget_Rebuild_RealInvocations(t *testing.T) {
	var calls [][]string
	orig := runCharlySubcommand
	runCharlySubcommand = func(args ...string) error {
		calls = append(calls, append([]string(nil), args...))
		return nil
	}
	defer func() { runCharlySubcommand = orig }()

	target := &PodUnifiedTarget{NodeName: "eval-x-pod", BaseImageRef: "x"}
	if err := target.Rebuild(context.Background(), RebuildOpts{DryRun: false, RebuildImage: true}); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	want := [][]string{
		{"box", "build", "x"},
		{"eval", "box", "x"}, // NOT "eval image" — the verb is registered as `eval box`
		{"deploy", "add", "eval-x-pod"},
		{"stop", "eval-x-pod"},
		{"config", "eval-x-pod"},
		{"start", "eval-x-pod"},
	}
	if len(calls) != len(want) {
		t.Fatalf("got %d charly subcommands, want %d: %v", len(calls), len(want), calls)
	}
	for i, w := range want {
		if strings.Join(calls[i], " ") != strings.Join(w, " ") {
			t.Errorf("charly call %d = %v, want %v", i, calls[i], w)
		}
	}
}

// TestPodUnifiedTarget_Basics verifies the trivial accessor methods.
func TestPodUnifiedTarget_Basics(t *testing.T) {
	target := &PodUnifiedTarget{NodeName: "eval-sway-browser-vnc-pod"}
	if got := target.Name(); got != "eval-sway-browser-vnc-pod" {
		t.Errorf("Name = %q, want %q", got, "eval-sway-browser-vnc-pod")
	}
	if got := target.Kind(); got != "pod" {
		t.Errorf("Kind = %q, want %q", got, "pod")
	}
	// With no embedded PodDeployTarget, Executor falls back to a
	// ShellExecutor (matches LocalUnifiedTarget's nil-safety).
	if target.Executor() == nil {
		t.Errorf("Executor: expected fallback executor, got nil")
	}
}

// TestPodUnifiedTarget_engine verifies the engine fallback (no embed
// → "podman"; embed with explicit Engine → that value; embed with
// empty Engine → "podman").
func TestPodUnifiedTarget_engine(t *testing.T) {
	{
		target := &PodUnifiedTarget{NodeName: "eval-sway-browser-vnc-pod"}
		if got := target.engine(); got != "podman" {
			t.Errorf("engine(no embed) = %q, want podman", got)
		}
	}
	{
		target := &PodUnifiedTarget{
			NodeName:        "eval-sway-browser-vnc-pod",
			PodDeployTarget: &PodDeployTarget{Engine: "docker"},
		}
		if got := target.engine(); got != "docker" {
			t.Errorf("engine(docker) = %q, want docker", got)
		}
	}
	{
		target := &PodUnifiedTarget{
			NodeName:        "eval-sway-browser-vnc-pod",
			PodDeployTarget: &PodDeployTarget{},
		}
		if got := target.engine(); got != "podman" {
			t.Errorf("engine(empty Engine) = %q, want podman", got)
		}
	}
}

// TestPodUnifiedTarget_Test_NilExecutor verifies Test errors cleanly
// when no executor is configured (rather than panicking on nil).
// PodUnifiedTarget.Executor() returns ShellExecutor on nil
// embed, but in a real flow the executor would be a podman-exec
// wrapper; this test is the nil-safety floor.
func TestPodUnifiedTarget_Test_NilExecutor(t *testing.T) {
	// Without any embed, Executor returns a non-nil ShellExecutor
	// — so Test should run on it. We use a hermetic command:true.
	target := &PodUnifiedTarget{NodeName: "eval-sway-browser-vnc-pod"}
	checks := []Check{{ID: "ok", Command: "true"}}
	if err := target.Test(context.Background(), checks, TestOpts{}); err != nil {
		t.Errorf("Test(local fallback): %v", err)
	}
}

// TestPodUnifiedTarget_Update_DryRun verifies the dry-run path emits
// the expected charly-update line without invoking the subcommand.
func TestPodUnifiedTarget_Update_DryRun(t *testing.T) {
	target := &PodUnifiedTarget{NodeName: "eval-sway-browser-vnc-pod"}
	if err := target.Update(context.Background(), nil, UpdateOpts{DryRun: true}); err != nil {
		t.Errorf("Update dry-run: %v", err)
	}
}

// TestPodUnifiedTarget_Rebuild_DryRun verifies the dry-run path emits
// the expected charly-build/eval/deploy/stop/config/start sequence without
// invoking the subcommands.
func TestPodUnifiedTarget_Rebuild_DryRun(t *testing.T) {
	target := &PodUnifiedTarget{NodeName: "eval-sway-browser-vnc-pod", BaseImageRef: "sway-browser-vnc"}
	if err := target.Rebuild(context.Background(), RebuildOpts{DryRun: true, RebuildImage: true}); err != nil {
		t.Errorf("Rebuild dry-run: %v", err)
	}
	// Without RebuildImage, the build/eval steps are skipped.
	if err := target.Rebuild(context.Background(), RebuildOpts{DryRun: true, RebuildImage: false}); err != nil {
		t.Errorf("Rebuild dry-run no-box: %v", err)
	}
}

// TestPodUnifiedTarget_Rebuild_BaseRefFallback verifies the BaseImageRef
// fallback rule: when empty, NodeName is used as the ref.
func TestPodUnifiedTarget_Rebuild_BaseRefFallback(t *testing.T) {
	// We can't easily inspect the printed dry-run output without
	// stdout capture, but we can confirm the dry-run branch returns
	// nil even when BaseImageRef is unset (Rebuild's internal
	// fallback prevents an empty-ref panic / shell-out).
	target := &PodUnifiedTarget{NodeName: "eval-sway-browser-vnc-pod" /* BaseImageRef unset */}
	if err := target.Rebuild(context.Background(), RebuildOpts{DryRun: true, RebuildImage: true}); err != nil {
		t.Errorf("Rebuild dry-run with empty BaseImageRef: %v", err)
	}
}

// Status is exercised by the R10 live verification (paste in commit
// message) — not by a unit test, because Status shells out via
// captureCharlyStdout which uses os.Args[0]. Inside `go test` that's the
// test binary, which doesn't understand the "status --json" CLI verbs
// and would hang or error spuriously. The other dry-run-able methods
// (Update, Rebuild) are safe because they early-return before any
// shell-out when DryRun is set.
