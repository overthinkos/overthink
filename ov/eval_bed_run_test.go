package main

import (
	"strings"
	"testing"
)

// TestFoldEvalBeds_FoldsIntoDeploy asserts kind:eval beds are folded into
// the Deploy map with EvalBed=true (so every deploy verb resolves them by
// name through the same path) while EvalBeds() returns them as the single
// enumeration source. This is the config-driven replacement for the old
// hardcoded bedTable coverage tests.
func TestFoldEvalBeds_FoldsIntoDeploy(t *testing.T) {
	uf := &UnifiedFile{
		Eval: map[string]DeploymentNode{
			"eval-image-pod": {Target: "pod", Image: "eval-image", Disposable: true},
			"eval-k3s-vm":    {Target: "vm", Vm: "k3s-vm", Disposable: true},
			"eval-local":     {Target: "local", Local: "eval-local", Disposable: true},
		},
	}
	if err := foldEvalBeds(uf); err != nil {
		t.Fatalf("foldEvalBeds: %v", err)
	}
	for _, name := range []string{"eval-image-pod", "eval-k3s-vm", "eval-local"} {
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
			"eval-image-pod": {Target: "pod", Image: "eval-image"}, // not disposable
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
