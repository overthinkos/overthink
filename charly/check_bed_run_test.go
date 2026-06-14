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
		{"pod", DeploymentNode{Target: "pod"}, []string{"left running for debugging", "podman exec charly-bed1", "charly remove bed1"}},
		{"vm", DeploymentNode{Target: "vm", Vm: "k3s-vm"}, []string{"VM \"k3s-vm\" left running", "charly vm destroy k3s-vm"}},
		{"local", DeploymentNode{Target: "local"}, []string{"local apply left in place", "charly remove bed1"}},
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
// subprocess exit 2→2. Underpins the `charly check run <bed>` exit-code
// propagation (check-check failure = 2, infra failure = 1).
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

// TestCheckFailedError asserts the distinct check-fail exit code is neither 0
// nor 1, and that a wrapped CheckFailedError is detectable via errors.As (the
// path main() uses to map it to CheckFailExitCode).
func TestCheckFailedError(t *testing.T) {
	if CheckFailExitCode == 0 || CheckFailExitCode == 1 {
		t.Fatalf("CheckFailExitCode must differ from 0 and 1, got %d", CheckFailExitCode)
	}
	var ef *CheckFailedError
	if !errors.As(fmt.Errorf("ctx: %w", &CheckFailedError{Failed: 3}), &ef) {
		t.Fatal("errors.As did not detect a wrapped CheckFailedError")
	}
	if ef.Failed != 3 {
		t.Errorf("Failed = %d, want 3", ef.Failed)
	}
	if got := (&CheckFailedError{Failed: 3}).Error(); got != "3 check(s) failed" {
		t.Errorf("Error() = %q, want \"3 check(s) failed\"", got)
	}
	if got := (&CheckFailedError{Msg: "bed x: check checks failed"}).Error(); got != "bed x: check checks failed" {
		t.Errorf("Error() msg override = %q", got)
	}
}

// TestFoldCheckBeds_FoldsIntoDeploy asserts kind:check beds are folded into
// the Deploy map with CheckBed=true (so every deploy verb resolves them by
// name through the same path) while CheckBeds() returns them as the single
// enumeration source. This is the config-driven replacement for the old
// hardcoded bedTable coverage tests.
func TestFoldCheckBeds_FoldsIntoDeploy(t *testing.T) {
	uf := &UnifiedFile{
		Check: map[string]DeploymentNode{
			"sample-pod-bed":   {Target: "pod", Box: "sample-image", Disposable: new(true)},
			"sample-vm-bed":    {Target: "vm", Vm: "sample-vm", Disposable: new(true)},
			"sample-local-bed": {Target: "local", Local: "sample-local", Disposable: new(true)},
		},
	}
	if err := foldCheckBeds(uf); err != nil {
		t.Fatalf("foldCheckBeds: %v", err)
	}
	for _, name := range []string{"sample-pod-bed", "sample-vm-bed", "sample-local-bed"} {
		d, ok := uf.Deploy[name]
		if !ok {
			t.Errorf("bed %q not folded into Deploy", name)
			continue
		}
		if !d.CheckBed {
			t.Errorf("bed %q folded without CheckBed marker", name)
		}
	}
	if got := len(uf.CheckBeds()); got != 3 {
		t.Errorf("CheckBeds() = %d entries, want 3", got)
	}
}

// TestFoldCheckBeds_DisjointNameGuard asserts a name declared as BOTH a
// kind:check bed and a kind:deploy entry is a hard error.
func TestFoldCheckBeds_DisjointNameGuard(t *testing.T) {
	uf := &UnifiedFile{
		Deploy: map[string]DeploymentNode{
			"clash": {Target: "pod", Box: "x"},
		},
		Check: map[string]DeploymentNode{
			"clash": {Target: "pod", Box: "y", Disposable: new(true)},
		},
	}
	err := foldCheckBeds(uf)
	if err == nil || !strings.Contains(err.Error(), "both a kind:check bed and a kind:deploy entry") {
		t.Fatalf("expected disjoint-name error, got %v", err)
	}
}

// TestValidateCheckBeds_DisposableRequired asserts a bed without
// disposable:true is rejected (it can't be R10-rebuilt unattended).
func TestValidateCheckBeds_DisposableRequired(t *testing.T) {
	uf := &UnifiedFile{
		Check: map[string]DeploymentNode{
			"sample-pod-bed": {Target: "pod", Box: "sample-image"}, // not disposable
		},
	}
	err := validateCheckBeds(uf)
	if err == nil || !strings.Contains(err.Error(), "disposable: true") {
		t.Fatalf("expected disposable-required error, got %v", err)
	}
}

// TestValidateCheckBeds_TargetEnum asserts an unsupported target is rejected.
func TestValidateCheckBeds_TargetEnum(t *testing.T) {
	uf := &UnifiedFile{
		Check: map[string]DeploymentNode{
			"check-weird": {Target: "k8s", Disposable: new(true)},
		},
	}
	err := validateCheckBeds(uf)
	if err == nil || !strings.Contains(err.Error(), "unsupported target") {
		t.Fatalf("expected target-enum error, got %v", err)
	}
}

// TestValidateCheckBeds_VmRefMustResolve asserts a vm-target bed whose vm:
// entity is undefined is rejected, and that a defined entity passes.
func TestValidateCheckBeds_VmRefMustResolve(t *testing.T) {
	missing := &UnifiedFile{
		Check: map[string]DeploymentNode{
			"check-k3s-vm": {Target: "vm", Vm: "k3s-vm", Disposable: new(true)},
		},
	}
	if err := validateCheckBeds(missing); err == nil || !strings.Contains(err.Error(), "not defined") {
		t.Fatalf("expected missing-vm-ref error, got %v", err)
	}
	ok := &UnifiedFile{
		VM: map[string]*VmSpec{"k3s-vm": {}},
		Check: map[string]DeploymentNode{
			"check-k3s-vm": {Target: "vm", Vm: "k3s-vm", Disposable: new(true)},
		},
	}
	if err := validateCheckBeds(ok); err != nil {
		t.Fatalf("defined vm ref should pass, got %v", err)
	}
}

// TestValidateCheckBeds_LocalRefMustResolve asserts a local-target bed whose
// local: template is undefined is rejected, and that a defined one passes.
func TestValidateCheckBeds_LocalRefMustResolve(t *testing.T) {
	missing := &UnifiedFile{
		Check: map[string]DeploymentNode{
			"check-local": {Target: "local", Local: "check-local", Disposable: new(true)},
		},
	}
	if err := validateCheckBeds(missing); err == nil || !strings.Contains(err.Error(), "not defined") {
		t.Fatalf("expected missing-local-ref error, got %v", err)
	}
	ok := &UnifiedFile{
		Local: map[string]*LocalSpec{"check-local": {}},
		Check: map[string]DeploymentNode{
			"check-local": {Target: "local", Local: "check-local", Disposable: new(true)},
		},
	}
	if err := validateCheckBeds(ok); err != nil {
		t.Fatalf("defined local ref should pass, got %v", err)
	}
}

// TestPersistBedDeployOverrides_SeedsPortBeforeConfig pins the fix for the
// bug class where a kind:check pod bed's project-declared deploy-shaped fields
// (port:/volume:/env:/tunnel:) never reached the per-host deploy.yml: charly check
// run shelled out `charly deploy add`/`charly config` with just the bed NAME, and both
// source port/security/network from the IMAGE LABELS (gating port writes behind
// an operator -p), so the bed's `port: 45434:11434` remap silently fell back to
// the image default and collided with a same-image production deploy at start.
// persistBedDeployOverrides seeds the bed node's overrides up front so the
// existing charly config -> MergeDeployOntoMetadata -> quadlet path honors them.
func TestPersistBedDeployOverrides_SeedsPortBeforeConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "charly"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A pre-existing unrelated deploy must survive the seed (merge, not clobber).
	initialYAML := `version: 2026.165.1048
deploy:
    ollama:
        target: pod
        box: ollama
        port:
            - 11434:11434
`
	path := filepath.Join(dir, "charly", "charly.yml")
	if err := os.WriteFile(path, []byte(initialYAML), 0600); err != nil {
		t.Fatalf("write initial: %v", err)
	}

	// A bed whose key differs from its image and whose port remaps off the
	// image default — exactly the check-cachyos-ollama-pod shape.
	bed := DeploymentNode{
		Target:     "pod",
		Box:        "ollama",
		Port:       []string{"45434:11434"},
		Disposable: new(true),
		Lifecycle:  "dev",
	}
	persistBedDeployOverrides("check-cachyos-ollama-pod", bed)

	dc, err := LoadDeployConfig()
	if err != nil {
		t.Fatalf("reload after seed: %v", err)
	}
	entry, ok := dc.Deploy["check-cachyos-ollama-pod"]
	if !ok {
		t.Fatal("bed entry not seeded into deploy.yml")
	}
	if len(entry.Port) != 1 || entry.Port[0] != "45434:11434" {
		t.Errorf("bed port not seeded: got %v, want [45434:11434]", entry.Port)
	}
	if entry.Box != "ollama" || entry.Target != "pod" {
		t.Errorf("bed image/target not seeded: got image=%q target=%q", entry.Box, entry.Target)
	}
	if entry.Disposable == nil || !*entry.Disposable {
		t.Error("bed disposable not seeded (the check-runner requires it to authorize the unattended fresh-rebuild)")
	}
	// The sibling production deploy must be untouched (distinct key).
	sib, ok := dc.Deploy["ollama"]
	if !ok || len(sib.Port) != 1 || sib.Port[0] != "11434:11434" {
		t.Errorf("sibling 'ollama' deploy clobbered: got %+v", sib)
	}
}

// TestBedCheckLiveRefs proves `charly check run <bed>` check-lives the substrate AND
// every nested child (sorted, dotted) — so a nested pod's baked candy/box
// check runs against its real venue. Before the nested-check fix this produced
// only [name], so a nested selkies-kde pod was deployed but never evaluated.
func TestBedCheckLiveRefs(t *testing.T) {
	// Flat bed: just the substrate (identical to the prior behavior).
	if got := bedCheckLiveRefs("check-pod", nil); len(got) != 1 || got[0] != "check-pod" {
		t.Fatalf("flat bed: got %v, want [check-pod]", got)
	}
	// Nested bed: substrate first, then each child as a sorted dotted path.
	nested := map[string]*DeploymentNode{
		"selkies-kde": {Target: "pod"},
		"cuda-pod":    {Target: "pod"},
	}
	got := bedCheckLiveRefs("check-cachyos-gpu-vm", nested)
	want := []string{
		"check-cachyos-gpu-vm",
		"check-cachyos-gpu-vm.cuda-pod", // sorted before selkies-kde
		"check-cachyos-gpu-vm.selkies-kde",
	}
	if len(got) != len(want) {
		t.Fatalf("nested bed: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("nested bed ref[%d]: got %q, want %q", i, got[i], want[i])
		}
	}

	// Android child: a target:android nested child shares the parent pod's
	// venue (its app-presence checks are baked into the parent's
	// android-emulator-layer and run in the parent ref) and has NO own venue
	// `charly check live` can resolve — so it gets NO dotted hop, while a pod sibling
	// still does. This is the check-coverage gate for the e740430 defect: a hop
	// for an android child wrongly resolved to a non-existent
	// `charly-<parent>.device` container, failing every nested pod→android bed's R10.
	androidNested := map[string]*DeploymentNode{
		"web":    {Target: "pod"},
		"device": {Target: "android"},
	}
	gotA := bedCheckLiveRefs("check-android-emulator-pod", androidNested)
	wantA := []string{
		"check-android-emulator-pod",
		"check-android-emulator-pod.web", // pod child kept; android "device" omitted
	}
	if len(gotA) != len(wantA) {
		t.Fatalf("android bed: got %v, want %v (android child must be omitted)", gotA, wantA)
	}
	for i := range wantA {
		if gotA[i] != wantA[i] {
			t.Errorf("android bed ref[%d]: got %q, want %q", i, gotA[i], wantA[i])
		}
	}
}
