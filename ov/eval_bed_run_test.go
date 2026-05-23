package main

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// TestExitCodeOf asserts exit-code extraction: nil→0, plain error→1,
// subprocess exit 2→2. Underpins the `ov eval run <bed>` exit-code
// propagation (eval-check failure = 2, infra failure = 1).
func TestExitCodeOf(t *testing.T) {
	if got := exitCodeOf(nil); got != 0 {
		t.Errorf("exitCodeOf(nil) = %d, want 0", got)
	}
	if got := exitCodeOf(errors.New("plain")); got != 1 {
		t.Errorf("exitCodeOf(plain) = %d, want 1", got)
	}
	if got := exitCodeOf(exec.Command("sh", "-c", "exit 2").Run()); got != 2 {
		t.Errorf("exitCodeOf(exit 2) = %d, want 2", got)
	}
}

// TestEvalFailedError asserts the distinct eval-fail exit code is neither 0
// nor 1, and that a wrapped EvalFailedError is detectable via errors.As (the
// path main() uses to map it to EvalCheckFailExitCode).
func TestEvalFailedError(t *testing.T) {
	if EvalCheckFailExitCode == 0 || EvalCheckFailExitCode == 1 {
		t.Fatalf("EvalCheckFailExitCode must differ from 0 and 1, got %d", EvalCheckFailExitCode)
	}
	var ef *EvalFailedError
	if !errors.As(fmt.Errorf("ctx: %w", &EvalFailedError{Failed: 3}), &ef) {
		t.Fatal("errors.As did not detect a wrapped EvalFailedError")
	}
	if ef.Failed != 3 {
		t.Errorf("Failed = %d, want 3", ef.Failed)
	}
	if got := (&EvalFailedError{Failed: 3}).Error(); got != "3 check(s) failed" {
		t.Errorf("Error() = %q, want \"3 check(s) failed\"", got)
	}
	if got := (&EvalFailedError{Msg: "bed x: eval checks failed"}).Error(); got != "bed x: eval checks failed" {
		t.Errorf("Error() msg override = %q", got)
	}
}

// TestFoldEvalBeds_FoldsIntoDeploy asserts kind:eval beds are folded into
// the Deploy map with EvalBed=true (so every deploy verb resolves them by
// name through the same path) while EvalBeds() returns them as the single
// enumeration source. This is the config-driven replacement for the old
// hardcoded bedTable coverage tests.
func TestFoldEvalBeds_FoldsIntoDeploy(t *testing.T) {
	uf := &UnifiedFile{
		Eval: map[string]DeploymentNode{
			"sample-pod-bed":   {Target: "pod", Image: "sample-image", Disposable: true},
			"sample-vm-bed":    {Target: "vm", Vm: "sample-vm", Disposable: true},
			"sample-local-bed": {Target: "local", Local: "sample-local", Disposable: true},
		},
	}
	if err := foldEvalBeds(uf); err != nil {
		t.Fatalf("foldEvalBeds: %v", err)
	}
	for _, name := range []string{"sample-pod-bed", "sample-vm-bed", "sample-local-bed"} {
		d, ok := uf.Deploy[name]
		if !ok {
			t.Errorf("bed %q not folded into Deploy", name)
			continue
		}
		if !d.EvalBed {
			t.Errorf("bed %q folded without EvalBed marker", name)
		}
	}
	if got := len(uf.EvalBeds()); got != 3 {
		t.Errorf("EvalBeds() = %d entries, want 3", got)
	}
}

// TestFoldEvalBeds_DisjointNameGuard asserts a name declared as BOTH a
// kind:eval bed and a kind:deploy entry is a hard error.
func TestFoldEvalBeds_DisjointNameGuard(t *testing.T) {
	uf := &UnifiedFile{
		Deploy: map[string]DeploymentNode{
			"clash": {Target: "pod", Image: "x"},
		},
		Eval: map[string]DeploymentNode{
			"clash": {Target: "pod", Image: "y", Disposable: true},
		},
	}
	err := foldEvalBeds(uf)
	if err == nil || !strings.Contains(err.Error(), "both a kind:eval bed and a kind:deploy entry") {
		t.Fatalf("expected disjoint-name error, got %v", err)
	}
}

// TestValidateEvalBeds_DisposableRequired asserts a bed without
// disposable:true is rejected (it can't be R10-rebuilt unattended).
func TestValidateEvalBeds_DisposableRequired(t *testing.T) {
	uf := &UnifiedFile{
		Eval: map[string]DeploymentNode{
			"sample-pod-bed": {Target: "pod", Image: "sample-image"}, // not disposable
		},
	}
	err := validateEvalBeds(uf)
	if err == nil || !strings.Contains(err.Error(), "disposable: true") {
		t.Fatalf("expected disposable-required error, got %v", err)
	}
}

// TestValidateEvalBeds_TargetEnum asserts an unsupported target is rejected.
func TestValidateEvalBeds_TargetEnum(t *testing.T) {
	uf := &UnifiedFile{
		Eval: map[string]DeploymentNode{
			"eval-weird": {Target: "k8s", Disposable: true},
		},
	}
	err := validateEvalBeds(uf)
	if err == nil || !strings.Contains(err.Error(), "unsupported target") {
		t.Fatalf("expected target-enum error, got %v", err)
	}
}

// TestValidateEvalBeds_VmRefMustResolve asserts a vm-target bed whose vm:
// entity is undefined is rejected, and that a defined entity passes.
func TestValidateEvalBeds_VmRefMustResolve(t *testing.T) {
	missing := &UnifiedFile{
		Eval: map[string]DeploymentNode{
			"eval-k3s-vm": {Target: "vm", Vm: "k3s-vm", Disposable: true},
		},
	}
	if err := validateEvalBeds(missing); err == nil || !strings.Contains(err.Error(), "not defined") {
		t.Fatalf("expected missing-vm-ref error, got %v", err)
	}
	ok := &UnifiedFile{
		VM: map[string]*VmSpec{"k3s-vm": {}},
		Eval: map[string]DeploymentNode{
			"eval-k3s-vm": {Target: "vm", Vm: "k3s-vm", Disposable: true},
		},
	}
	if err := validateEvalBeds(ok); err != nil {
		t.Fatalf("defined vm ref should pass, got %v", err)
	}
}

// TestValidateEvalBeds_LocalRefMustResolve asserts a local-target bed whose
// local: template is undefined is rejected, and that a defined one passes.
func TestValidateEvalBeds_LocalRefMustResolve(t *testing.T) {
	missing := &UnifiedFile{
		Eval: map[string]DeploymentNode{
			"eval-local": {Target: "local", Local: "eval-local", Disposable: true},
		},
	}
	if err := validateEvalBeds(missing); err == nil || !strings.Contains(err.Error(), "not defined") {
		t.Fatalf("expected missing-local-ref error, got %v", err)
	}
	ok := &UnifiedFile{
		Local: map[string]*LocalSpec{"eval-local": {}},
		Eval: map[string]DeploymentNode{
			"eval-local": {Target: "local", Local: "eval-local", Disposable: true},
		},
	}
	if err := validateEvalBeds(ok); err != nil {
		t.Fatalf("defined local ref should pass, got %v", err)
	}
}
