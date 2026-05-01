package main

import (
	"context"
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

// TestVmUnifiedTarget_Rebuild_DryRun verifies the dry-run path emits
// the expected ov-vm-* sequence without invoking the subcommand. We
// can't easily capture stdout here, but the absence of a non-nil
// error confirms the dry-run branch returns cleanly.
func TestVmUnifiedTarget_Rebuild_DryRun(t *testing.T) {
	target := &VmUnifiedTarget{NodeName: "arch-vm"}
	if err := target.Rebuild(context.Background(), RebuildOpts{DryRun: true, RebuildImage: true}); err != nil {
		t.Errorf("Rebuild dry-run: %v", err)
	}
	// Without RebuildImage flag, the build step is skipped — same path
	// works.
	if err := target.Rebuild(context.Background(), RebuildOpts{DryRun: true, RebuildImage: false}); err != nil {
		t.Errorf("Rebuild dry-run no-image: %v", err)
	}
}
