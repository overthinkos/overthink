package main

// ensure_image_test.go — exercises the kind:local `images:` cutover.

import (
	"strings"
	"testing"
)

// TestEnsureImageStep_Reverse — Reverse() emits a single
// ReverseOpRemoveImage with the image and engine in Extra.
func TestEnsureImageStep_Reverse(t *testing.T) {
	step := &EnsureImageStep{
		LayerName: "_local-images_",
		Origin:    "local:ov-cachyos",
		Image:     "eval-target",
		Engine:    "podman",
		PullFirst: true,
		DeployID:  "abc123",
	}
	ops := step.Reverse()
	if len(ops) != 1 {
		t.Fatalf("expected 1 reverse op, got %d", len(ops))
	}
	if ops[0].Kind != ReverseOpRemoveImage {
		t.Errorf("expected ReverseOpRemoveImage, got %q", ops[0].Kind)
	}
	if len(ops[0].Targets) != 1 || ops[0].Targets[0] != "eval-target" {
		t.Errorf("targets: %v", ops[0].Targets)
	}
	if ops[0].Extra["engine"] != "podman" {
		t.Errorf("engine extra: %v", ops[0].Extra)
	}
}

// TestEnsureImageStep_KindScopeVenue — basic interface impls.
func TestEnsureImageStep_KindScopeVenue(t *testing.T) {
	step := &EnsureImageStep{Image: "x"}
	if step.Kind() != StepKindEnsureImage {
		t.Errorf("Kind() = %q, want %q", step.Kind(), StepKindEnsureImage)
	}
	if step.Scope() != ScopeSystem {
		t.Errorf("Scope() = %q, want ScopeSystem", step.Scope())
	}
	if step.Venue() != VenueHostNative {
		t.Errorf("Venue() = %q, want VenueHostNative", step.Venue())
	}
	if step.RequiresGate() != GateNone {
		t.Errorf("RequiresGate() = %q, want GateNone", step.RequiresGate())
	}
}

// TestCompileImagesSteps_EmitsOnePerEntry — compiler produces N steps
// for N entries, with deterministic field population.
func TestCompileImagesSteps_EmitsOnePerEntry(t *testing.T) {
	spec := &LocalSpec{
		Images: []string{"eval-target", "openclaw-sway-browser", "fedora-coder"},
	}
	steps := compileImagesSteps(spec, "ov-cachyos", "deploy123", "podman")
	if len(steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(steps))
	}
	for i, s := range steps {
		es, ok := s.(*EnsureImageStep)
		if !ok {
			t.Errorf("step %d not *EnsureImageStep: %T", i, s)
			continue
		}
		if es.Image != spec.Images[i] {
			t.Errorf("step %d Image = %q, want %q", i, es.Image, spec.Images[i])
		}
		if es.DeployID != "deploy123" {
			t.Errorf("step %d DeployID = %q", i, es.DeployID)
		}
		if es.Origin != "local:ov-cachyos" {
			t.Errorf("step %d Origin = %q", i, es.Origin)
		}
		if !es.PullFirst || !es.BuildOnFail {
			t.Errorf("step %d defaults: PullFirst=%v BuildOnFail=%v", i, es.PullFirst, es.BuildOnFail)
		}
	}
}

// TestCompileImagesSteps_NilOrEmpty — nil spec or empty list returns nil.
func TestCompileImagesSteps_NilOrEmpty(t *testing.T) {
	if got := compileImagesSteps(nil, "x", "y", "podman"); got != nil {
		t.Errorf("nil spec: got %d steps, want nil", len(got))
	}
	spec := &LocalSpec{Images: nil}
	if got := compileImagesSteps(spec, "x", "y", "podman"); got != nil {
		t.Errorf("empty Images: got %d steps, want nil", len(got))
	}
	spec.Images = []string{"", "  ", "real-image", ""}
	got := compileImagesSteps(spec, "x", "y", "podman")
	// We accept all entries (including blank — caller's responsibility).
	// Just verify the real entry made it through.
	if len(got) < 1 {
		t.Fatalf("expected at least one step for real-image")
	}
}

// TestResolveImageRefForEnsure_RemoteRef — @github.com/... refs pass
// through unchanged so runImagePull can route them via
// ResolveRemoteImage.
func TestResolveImageRefForEnsure_RemoteRef(t *testing.T) {
	ref, err := resolveImageRefForEnsure("@github.com/overthinkos/overthink/eval-target:latest", nil, "")
	if err != nil {
		t.Fatalf("remote ref err: %v", err)
	}
	if !strings.HasPrefix(ref, "@github.com/") {
		t.Errorf("remote ref returned %q, expected @github.com/ prefix", ref)
	}
}

// TestResolveImageRefForEnsure_FullRef — fully-qualified registry refs
// pass through unchanged.
func TestResolveImageRefForEnsure_FullRef(t *testing.T) {
	ref, err := resolveImageRefForEnsure("ghcr.io/overthinkos/eval-target:latest", nil, "")
	if err != nil {
		t.Fatalf("full ref err: %v", err)
	}
	if ref != "ghcr.io/overthinkos/eval-target:latest" {
		t.Errorf("full ref returned %q", ref)
	}
}

// TestResolveImageRefForEnsure_ShortNameRequiresCfg — short names
// without a *Config error with a friendly message.
func TestResolveImageRefForEnsure_ShortNameRequiresCfg(t *testing.T) {
	_, err := resolveImageRefForEnsure("eval-target", nil, "")
	if err == nil {
		t.Fatal("expected error for short name with nil cfg")
	}
	if !strings.Contains(err.Error(), "image.yml") {
		t.Errorf("error should mention image.yml: %v", err)
	}
}

// reclaimMockReverseExec extends the unit-test reverse-exec mock with a
// configurable reverseReclaimImages return value, so the rmi path can
// be exercised in both branches (reclaim=false → no-op, reclaim=true →
// RunUser called with the rmi command).
type reclaimMockReverseExec struct {
	reclaim   bool
	runScript string // captures the script the runner would have executed
}

func (m *reclaimMockReverseExec) reverseDryRun() bool          { return false }
func (m *reclaimMockReverseExec) reverseKeepRepoChanges() bool { return false }
func (m *reclaimMockReverseExec) reverseKeepServices() bool    { return false }
func (m *reclaimMockReverseExec) reverseReclaimImages() bool   { return m.reclaim }
func (m *reclaimMockReverseExec) reverseRunner() ReverseRunner { return m }
func (m *reclaimMockReverseExec) RunSystem(s string) error     { m.runScript = s; return nil }
func (m *reclaimMockReverseExec) RunUser(s string) error       { m.runScript = s; return nil }

// TestReverseRemoveImage_DefaultKeeps — without --reclaim-images, the
// op is a no-op (the runner is never invoked).
func TestReverseRemoveImage_DefaultKeeps(t *testing.T) {
	m := &reclaimMockReverseExec{reclaim: false}
	op := ReverseOp{
		Kind:    ReverseOpRemoveImage,
		Targets: []string{"ghcr.io/overthinkos/eval-target:latest"},
		Extra:   map[string]string{"engine": "podman"},
	}
	if err := reverseRemoveImage(op, m); err != nil {
		t.Fatalf("reverseRemoveImage(reclaim=false): %v", err)
	}
	if m.runScript != "" {
		t.Errorf("expected no script run, got %q", m.runScript)
	}
}

// TestReverseRemoveImage_ReclaimRunsRmi — with --reclaim-images, the
// runner gets `podman rmi -f <ref>` for each Target.
func TestReverseRemoveImage_ReclaimRunsRmi(t *testing.T) {
	m := &reclaimMockReverseExec{reclaim: true}
	op := ReverseOp{
		Kind:    ReverseOpRemoveImage,
		Targets: []string{"ghcr.io/overthinkos/eval-target:latest"},
		Extra:   map[string]string{"engine": "podman"},
	}
	if err := reverseRemoveImage(op, m); err != nil {
		t.Fatalf("reverseRemoveImage(reclaim=true): %v", err)
	}
	if !strings.Contains(m.runScript, "podman rmi -f ghcr.io/overthinkos/eval-target:latest") {
		t.Errorf("expected rmi command, got %q", m.runScript)
	}
}
