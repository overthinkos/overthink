package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
)

// TestVmUnifiedTarget_Basics verifies the trivial accessor methods.
func TestVmUnifiedTarget_Basics(t *testing.T) {
	target := &VmUnifiedTarget{NodeName: "arch-vm"}
	if got := target.Name(); got != "arch-vm" {
		t.Errorf("Name = %q, want %q", got, "arch-vm")
	}
	if got := target.Kind(); got != "vm" {
		t.Errorf("Kind = %q, want %q", got, "vm")
	}
	// With no embedded VmDeployTarget, Executor returns nil — caller
	// must check before use.
	if target.Executor() != nil {
		t.Errorf("Executor: expected nil for empty target, got %T", target.Executor())
	}
}

// TestVmUnifiedTarget_vmEntityName covers the NodeName ↔ VMName
// fallback rules.
func TestVmUnifiedTarget_vmEntityName(t *testing.T) {
	// No embedded target → falls back to NodeName.
	{
		target := &VmUnifiedTarget{NodeName: "arch-vm"}
		if got := target.vmEntityName(); got != "arch-vm" {
			t.Errorf("vmEntityName(no embed) = %q, want arch-vm", got)
		}
	}
	// Embedded with VMName populated → prefers VMName.
	{
		target := &VmUnifiedTarget{
			NodeName:       "arch-vm",
			VmDeployTarget: &VmDeployTarget{VMName: "arch"},
		}
		if got := target.vmEntityName(); got != "arch" {
			t.Errorf("vmEntityName(with embed) = %q, want arch", got)
		}
	}
	// Embedded with VMName empty → falls back to NodeName.
	{
		target := &VmUnifiedTarget{
			NodeName:       "arch-vm",
			VmDeployTarget: &VmDeployTarget{},
		}
		if got := target.vmEntityName(); got != "arch-vm" {
			t.Errorf("vmEntityName(empty VMName) = %q, want arch-vm", got)
		}
	}
}

// TestVmUnifiedTarget_vmDomainName verifies the "ov-<entity>" prefix.
func TestVmUnifiedTarget_vmDomainName(t *testing.T) {
	target := &VmUnifiedTarget{NodeName: "arch-vm"}
	if got := target.vmDomainName(); got != "ov-arch-vm" {
		t.Errorf("vmDomainName = %q, want ov-arch-vm", got)
	}
	target2 := &VmUnifiedTarget{
		NodeName:       "arch-vm",
		VmDeployTarget: &VmDeployTarget{VMName: "arch"},
	}
	if got := target2.vmDomainName(); got != "ov-arch" {
		t.Errorf("vmDomainName(with VMName) = %q, want ov-arch", got)
	}
}

// TestVmUnifiedTarget_Update_NilEmbedded verifies Update returns a
// clean error when VmDeployTarget isn't embedded (cannot Emit without
// the executor + spec).
func TestVmUnifiedTarget_Update_NilEmbedded(t *testing.T) {
	target := &VmUnifiedTarget{NodeName: "arch-vm"}
	err := target.Update(context.Background(), nil, UpdateOpts{})
	if err == nil {
		t.Error("Update(nil embed): expected error, got nil")
	}
}

// TestVmUnifiedTarget_Test_NilExecutor verifies Test returns a clean
// error rather than panicking when no SSHExecutor is configured.
func TestVmUnifiedTarget_Test_NilExecutor(t *testing.T) {
	target := &VmUnifiedTarget{NodeName: "arch-vm"}
	err := target.Test(context.Background(), nil, TestOpts{})
	if err == nil {
		t.Error("Test(nil exec): expected error, got nil")
	}
}

// TestVmReverseExec_AccessorPassthrough verifies the inline adapter
// forwards each ReverseExecutor accessor — same shape as the host
// adapter, kept separate so a future divergence doesn't ripple.
func TestVmReverseExec_AccessorPassthrough(t *testing.T) {
	e := &vmReverseExec{
		DryRun:          true,
		KeepRepoChanges: false,
		KeepServices:    true,
		Runner:          nil,
	}
	if !e.reverseDryRun() {
		t.Errorf("reverseDryRun = false, want true")
	}
	if e.reverseKeepRepoChanges() {
		t.Errorf("reverseKeepRepoChanges = true, want false")
	}
	if !e.reverseKeepServices() {
		t.Errorf("reverseKeepServices = false, want true")
	}
	if e.reverseRunner() != nil {
		t.Errorf("reverseRunner non-nil, want nil")
	}
}

// captureVmStdout runs fn with os.Stdout redirected to a pipe and
// returns everything written. Mirrors captureStderr in tunnel_test.go.
func captureVmStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	done := make(chan struct{})
	var buf bytes.Buffer
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()

	fn()
	w.Close()
	<-done
	return buf.String()
}

// TestVmUnifiedTarget_Rebuild_DryRun verifies the dry-run path emits the
// expected ordered sequence — and, critically, that it ENDS in
// `ov deploy add <node>` so the deploy node's layers are re-applied to the
// fresh guest, exactly like LocalUnifiedTarget/PodUnifiedTarget.Rebuild. A
// VM Rebuild that recreates the domain but skips the layer re-apply (the #42
// bug) would not emit this line, and this test would fail.
func TestVmUnifiedTarget_Rebuild_DryRun(t *testing.T) {
	// NodeName != entity name (the eval-k3s-vm → vm: k3s-vm shape): the
	// vm-* steps key on the entity name, but `deploy add` MUST key on the
	// deploy NodeName — that is the deploy key dispatchNode resolves.
	target := &VmUnifiedTarget{
		NodeName:       "k3s-vm",
		VmDeployTarget: &VmDeployTarget{VMName: "k3s"},
	}

	out := captureVmStdout(t, func() {
		if err := target.Rebuild(context.Background(), RebuildOpts{DryRun: true, RebuildImage: true}); err != nil {
			t.Errorf("Rebuild dry-run: %v", err)
		}
	})

	// The layer re-apply step (the fix) must be present, keyed on NodeName.
	deployAdd := "dry-run: ov deploy add k3s-vm"
	if !strings.Contains(out, deployAdd) {
		t.Errorf("Rebuild dry-run missing layer re-apply step %q in:\n%s", deployAdd, out)
	}
	// And it must come AFTER `ov vm start` (re-apply on the booted guest).
	vmStart := "dry-run: ov vm start k3s"
	if i, j := strings.Index(out, vmStart), strings.Index(out, deployAdd); i < 0 || j < 0 || j < i {
		t.Errorf("Rebuild dry-run: expected %q before %q in:\n%s", vmStart, deployAdd, out)
	}

	// Without RebuildImage the disk build step is skipped, but the layer
	// re-apply still runs (a config-only rebuild must still re-deploy).
	out = captureVmStdout(t, func() {
		if err := target.Rebuild(context.Background(), RebuildOpts{DryRun: true, RebuildImage: false}); err != nil {
			t.Errorf("Rebuild dry-run no-image: %v", err)
		}
	})
	if strings.Contains(out, "dry-run: ov vm build") {
		t.Errorf("Rebuild dry-run no-image: should not emit `ov vm build` in:\n%s", out)
	}
	if !strings.Contains(out, deployAdd) {
		t.Errorf("Rebuild dry-run no-image: missing layer re-apply step %q in:\n%s", deployAdd, out)
	}
}

// TestVmUnifiedTarget_Rebuild_ReappliesLayers exercises the real (non-dry-run)
// Rebuild body through the stubbable runOvSubcommand seam and asserts the
// recorded subcommand sequence ends in `deploy add <node>` — the shared
// layer-apply primitive LocalUnifiedTarget.Rebuild and PodUnifiedTarget.Rebuild
// also call (R3). The #42 bug (domain-recreate only) would record no such call.
func TestVmUnifiedTarget_Rebuild_ReappliesLayers(t *testing.T) {
	var calls [][]string
	origRun := runOvSubcommand
	runOvSubcommand = func(args ...string) error {
		calls = append(calls, append([]string(nil), args...))
		return nil
	}
	defer func() { runOvSubcommand = origRun }()

	// `ov vm start` goes through runOvSubcommandCapture (real exec). Stub it
	// to return a benign "already running" so Rebuild falls through to the
	// layer re-apply without spawning a real ov binary.
	origCap := runOvSubcommandCapture
	runOvSubcommandCapture = func(args ...string) (string, error) {
		calls = append(calls, append([]string{"<capture>"}, args...))
		return "domain is already running", nil
	}
	defer func() { runOvSubcommandCapture = origCap }()

	target := &VmUnifiedTarget{
		NodeName:       "k3s-vm",
		VmDeployTarget: &VmDeployTarget{VMName: "k3s"},
	}
	if err := target.Rebuild(context.Background(), RebuildOpts{RebuildImage: false}); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	if len(calls) == 0 {
		t.Fatal("Rebuild recorded no subcommand calls")
	}
	last := calls[len(calls)-1]
	want := []string{"deploy", "add", "k3s-vm"}
	if len(last) != len(want) || last[0] != want[0] || last[1] != want[1] || last[2] != want[2] {
		t.Errorf("Rebuild last call = %v, want %v (full sequence: %v)", last, want, calls)
	}
}
