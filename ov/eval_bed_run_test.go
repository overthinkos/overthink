package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestPrintDebugRetentionNotice asserts that a FAILED bed prints the
// target-appropriate inspect + destroy hints — the keep-the-bed-up-on-failure
// behavior (fail() no longer tears the bed down). Each target kind gets the
// right destroy command.
func TestPrintDebugRetentionNotice(t *testing.T) {
	cases := []struct {
		name     string
		node     DeploymentNode
		wantSubs []string
	}{
		{"pod", DeploymentNode{Target: "pod"}, []string{"left running for debugging", "podman exec ov-bed1", "ov remove bed1"}},
		{"vm", DeploymentNode{Target: "vm", Vm: "k3s-vm"}, []string{"VM \"k3s-vm\" left running", "ov vm destroy k3s-vm"}},
		{"local", DeploymentNode{Target: "local"}, []string{"local apply left in place", "ov remove bed1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			printDebugRetentionNotice(&buf, "bed1", tc.node)
			got := buf.String()
			for _, sub := range tc.wantSubs {
				if !strings.Contains(got, sub) {
					t.Errorf("notice for %s missing %q; got:\n%s", tc.name, sub, got)
				}
			}
		})
	}
}

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
			"sample-pod-bed":   {Target: "pod", Image: "sample-image", Disposable: boolPtr(true)},
			"sample-vm-bed":    {Target: "vm", Vm: "sample-vm", Disposable: boolPtr(true)},
			"sample-local-bed": {Target: "local", Local: "sample-local", Disposable: boolPtr(true)},
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
			"clash": {Target: "pod", Image: "y", Disposable: boolPtr(true)},
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
			"eval-weird": {Target: "k8s", Disposable: boolPtr(true)},
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
			"eval-k3s-vm": {Target: "vm", Vm: "k3s-vm", Disposable: boolPtr(true)},
		},
	}
	if err := validateEvalBeds(missing); err == nil || !strings.Contains(err.Error(), "not defined") {
		t.Fatalf("expected missing-vm-ref error, got %v", err)
	}
	ok := &UnifiedFile{
		VM: map[string]*VmSpec{"k3s-vm": {}},
		Eval: map[string]DeploymentNode{
			"eval-k3s-vm": {Target: "vm", Vm: "k3s-vm", Disposable: boolPtr(true)},
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
			"eval-local": {Target: "local", Local: "eval-local", Disposable: boolPtr(true)},
		},
	}
	if err := validateEvalBeds(missing); err == nil || !strings.Contains(err.Error(), "not defined") {
		t.Fatalf("expected missing-local-ref error, got %v", err)
	}
	ok := &UnifiedFile{
		Local: map[string]*LocalSpec{"eval-local": {}},
		Eval: map[string]DeploymentNode{
			"eval-local": {Target: "local", Local: "eval-local", Disposable: boolPtr(true)},
		},
	}
	if err := validateEvalBeds(ok); err != nil {
		t.Fatalf("defined local ref should pass, got %v", err)
	}
}

// TestPersistBedDeployOverrides_SeedsPortBeforeConfig pins the fix for the
// bug class where a kind:eval pod bed's project-declared deploy-shaped fields
// (port:/volume:/env:/tunnel:) never reached the per-host deploy.yml: ov eval
// run shelled out `ov deploy add`/`ov config` with just the bed NAME, and both
// source port/security/network from the IMAGE LABELS (gating port writes behind
// an operator -p), so the bed's `port: 45434:11434` remap silently fell back to
// the image default and collided with a same-image production deploy at start.
// persistBedDeployOverrides seeds the bed node's overrides up front so the
// existing ov config -> MergeDeployOntoMetadata -> quadlet path honors them.
func TestPersistBedDeployOverrides_SeedsPortBeforeConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "ov"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A pre-existing unrelated deploy must survive the seed (merge, not clobber).
	initialYAML := `deploy:
    ollama:
        target: pod
        image: ollama
        port:
            - 11434:11434
`
	path := filepath.Join(dir, "ov", "deploy.yml")
	if err := os.WriteFile(path, []byte(initialYAML), 0600); err != nil {
		t.Fatalf("write initial: %v", err)
	}

	// A bed whose key differs from its image and whose port remaps off the
	// image default — exactly the eval-cachyos-ollama-pod shape.
	bed := DeploymentNode{
		Target:     "pod",
		Image:      "ollama",
		Port:       []string{"45434:11434"},
		Disposable: boolPtr(true),
		Lifecycle:  "dev",
	}
	persistBedDeployOverrides("eval-cachyos-ollama-pod", bed)

	dc, err := LoadDeployConfig()
	if err != nil {
		t.Fatalf("reload after seed: %v", err)
	}
	entry, ok := dc.Deploy["eval-cachyos-ollama-pod"]
	if !ok {
		t.Fatal("bed entry not seeded into deploy.yml")
	}
	if len(entry.Port) != 1 || entry.Port[0] != "45434:11434" {
		t.Errorf("bed port not seeded: got %v, want [45434:11434]", entry.Port)
	}
	if entry.Image != "ollama" || entry.Target != "pod" {
		t.Errorf("bed image/target not seeded: got image=%q target=%q", entry.Image, entry.Target)
	}
	if entry.Disposable == nil || !*entry.Disposable {
		t.Error("bed disposable not seeded (ov update would refuse the fresh-rebuild step)")
	}
	// The sibling production deploy must be untouched (distinct key).
	sib, ok := dc.Deploy["ollama"]
	if !ok || len(sib.Port) != 1 || sib.Port[0] != "11434:11434" {
		t.Errorf("sibling 'ollama' deploy clobbered: got %+v", sib)
	}
}
