package main

import (
	"strings"
	"testing"
)

// sharedNode is a SHARED-claim (refcounted, pod) claimant of the given tokens.
func sharedNode(tokens []string) BundleNode {
	return BundleNode{Target: "pod", RequiresShared: tokens}
}

// gpuResources is the token map a GPU-backed arbiter sees (drives mode flips).
func gpuResources() map[string]*ResourceDef {
	return map[string]*ResourceDef{"nvidia-gpu": {Gpu: &GpuSelector{Vendor: "0x10de"}}}
}

// S1. The FIRST shared claim flips the gpu-backed token to nvidia mode + CDI.
func TestArbiter_SharedAcquireFlipsNvidia(t *testing.T) {
	w := &fakeWorld{running: map[string]bool{"pod1": true}, resources: gpuResources()}
	a := newTestArbiter(t, map[string]BundleNode{}, w)

	lease, err := a.AcquireShared("pod1", sharedNode([]string{"nvidia-gpu"}), false)
	if err != nil {
		t.Fatalf("acquire-shared: %v", err)
	}
	if w.modes["0x10de"] != gpuModeNvidia {
		t.Fatalf("first shared claim must flip to nvidia; modes=%v ops=%v", w.modes, w.ops)
	}
	if w.cdiCalls == 0 {
		t.Fatal("first shared claim must (re)generate CDI")
	}
	led, _, _ := a.Status()
	if len(led.Leases) != 1 || !led.Leases[0].Shared {
		t.Fatalf("expected one SHARED lease, got %+v", led.Leases)
	}
	_ = lease
}

// S2. Two shared claims coexist (refcount); the last release flips back to vfio.
func TestArbiter_TwoSharedCoexist_LastReleaseRestoresVfio(t *testing.T) {
	w := &fakeWorld{running: map[string]bool{"pod1": true, "pod2": true}, resources: gpuResources()}
	a := newTestArbiter(t, map[string]BundleNode{}, w)

	l1, err := a.AcquireShared("pod1", sharedNode([]string{"nvidia-gpu"}), false)
	if err != nil {
		t.Fatalf("pod1 acquire: %v", err)
	}
	l2, err := a.AcquireShared("pod2", sharedNode([]string{"nvidia-gpu"}), false)
	if err != nil {
		t.Fatalf("pod2 acquire: %v", err)
	}
	led, _, _ := a.Status()
	if len(led.Leases) != 2 {
		t.Fatalf("two shared claims must coexist, got %+v", led.Leases)
	}

	// Releasing pod1 leaves the token shared by pod2 → stays nvidia.
	if err := l1.Release(); err != nil {
		t.Fatalf("pod1 release: %v", err)
	}
	if w.modes["0x10de"] != gpuModeNvidia {
		t.Fatalf("token still shared by pod2 → must stay nvidia; modes=%v", w.modes)
	}
	// Releasing the LAST shared claim returns the token to the vfio default.
	if err := l2.Release(); err != nil {
		t.Fatalf("pod2 release: %v", err)
	}
	if w.modes["0x10de"] != gpuModeVfio {
		t.Fatalf("last shared release must flip back to vfio; modes=%v ops=%v", w.modes, w.ops)
	}
	led, _, _ = a.Status()
	if len(led.Leases) != 0 {
		t.Fatalf("no leases should remain, got %+v", led.Leases)
	}
}

// S3. An EXCLUSIVE claim PREEMPTS running shared pods (stops them + drops their
// leases) and flips the token to vfio.
func TestArbiter_ExclusivePreemptsShared(t *testing.T) {
	w := &fakeWorld{running: map[string]bool{"pod1": true, "vm": true}, resources: gpuResources()}
	a := newTestArbiter(t, map[string]BundleNode{}, w)

	if _, err := a.AcquireShared("pod1", sharedNode([]string{"nvidia-gpu"}), false); err != nil {
		t.Fatalf("pod1 shared acquire: %v", err)
	}
	if w.modes["0x10de"] != gpuModeNvidia {
		t.Fatalf("pre-state: token should be nvidia; modes=%v", w.modes)
	}

	if _, err := a.AcquireExclusive("vm", claimantNode([]string{"nvidia-gpu"}), false); err != nil {
		t.Fatalf("vm exclusive acquire: %v", err)
	}
	if w.running["pod1"] {
		t.Fatalf("exclusive claim must stop the shared pod; ops=%v", w.ops)
	}
	if w.modes["0x10de"] != gpuModeVfio {
		t.Fatalf("exclusive claim must flip token to vfio; modes=%v", w.modes)
	}
	led, _, _ := a.Status()
	// Only the exclusive lease remains; the shared pod's lease was dropped.
	if len(led.Leases) != 1 || led.Leases[0].Shared || led.Leases[0].Claimant != "vm" {
		t.Fatalf("expected one EXCLUSIVE lease for vm, got %+v", led.Leases)
	}
}

// S4. A shared claim is REFUSED while an exclusive claim holds the token.
func TestArbiter_SharedRefusedUnderExclusive(t *testing.T) {
	w := &fakeWorld{running: map[string]bool{"vm": true, "pod1": true}, resources: gpuResources()}
	a := newTestArbiter(t, map[string]BundleNode{}, w)

	if _, err := a.AcquireExclusive("vm", claimantNode([]string{"nvidia-gpu"}), false); err != nil {
		t.Fatalf("vm exclusive acquire: %v", err)
	}
	_, err := a.AcquireShared("pod1", sharedNode([]string{"nvidia-gpu"}), false)
	if err == nil || !strings.Contains(err.Error(), "EXCLUSIVELY") {
		t.Fatalf("shared claim under an exclusive hold must be refused, got %v", err)
	}
}

// S5. A shared claim PREEMPTS a running preemptible HOLDER (operator VM) and the
// last shared release RESTORES it (+ flips back to vfio).
func TestArbiter_SharedPreemptsHolder_ReleaseRestores(t *testing.T) {
	w := &fakeWorld{running: map[string]bool{"h1": true, "pod1": true}, resources: gpuResources()}
	holders := map[string]BundleNode{"h1": holderNode([]string{"nvidia-gpu"}, "")}
	a := newTestArbiter(t, holders, w)

	lease, err := a.AcquireShared("pod1", sharedNode([]string{"nvidia-gpu"}), false)
	if err != nil {
		t.Fatalf("pod1 shared acquire: %v", err)
	}
	if w.running["h1"] {
		t.Fatalf("shared claim must preempt the vfio holder h1; ops=%v", w.ops)
	}
	if w.modes["0x10de"] != gpuModeNvidia {
		t.Fatalf("token should be nvidia while shared; modes=%v", w.modes)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if !w.running["h1"] {
		t.Fatalf("last shared release must restore the preempted holder h1; ops=%v", w.ops)
	}
	if w.modes["0x10de"] != gpuModeVfio {
		t.Fatalf("last shared release must flip token back to vfio; modes=%v", w.modes)
	}
}

// S7. A node may not claim a resource BOTH exclusively and shared (the arbiter
// dispatches on one or the other; the driver modes are mutually exclusive).
func TestValidate_BothExclusiveAndShared_Errors(t *testing.T) {
	node := BundleNode{
		RequiresExclusive: []string{"nvidia-gpu"},
		RequiresShared:    []string{"nvidia-gpu"},
	}
	errs := &ValidationError{}
	ValidatePreemptibleOnNode("bad", &node, errs)
	if !errs.HasErrors() || !strings.Contains(errs.Error(), "both") {
		t.Fatalf("expected a both-exclusive-and-shared validation error, got: %q", errs.Error())
	}
}

// S6. A selector-less (abstract) shared token refcounts WITHOUT any device flip.
func TestArbiter_SharedAbstractTokenNoFlip(t *testing.T) {
	w := &fakeWorld{running: map[string]bool{"pod1": true}, resources: map[string]*ResourceDef{"abstract": {}}}
	a := newTestArbiter(t, map[string]BundleNode{}, w)

	if _, err := a.AcquireShared("pod1", sharedNode([]string{"abstract"}), false); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if len(w.modes) != 0 {
		t.Fatalf("a selector-less token must not trigger a device flip; modes=%v", w.modes)
	}
	if w.cdiCalls != 0 {
		t.Fatalf("a selector-less token must not generate CDI; cdiCalls=%d", w.cdiCalls)
	}
}
