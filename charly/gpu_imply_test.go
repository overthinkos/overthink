package main

import (
	"slices"
	"testing"
)

// withDetectGPU swaps the package-level DetectGPU probe for the duration of a
// test (restored on cleanup), so the implied-shared logic can be exercised
// without a real nvidia-smi on the host.
func withDetectGPU(t *testing.T, present bool) {
	t.Helper()
	prev := DetectGPU
	DetectGPU = func() bool { return present }
	t.Cleanup(func() { DetectGPU = prev })
}

// gpuPodNode is a plain pod deploy that declares NO resource claim — the
// untracked GPU-consumer case from the RCA (auto-detected `--device
// nvidia.com/gpu=all`, no requires_shared).
func gpuPodNode() BundleNode { return BundleNode{Target: "pod"} }

// I1. A GPU-device node (host presents nvidia) implies requires_shared:[nvidia-gpu];
// a non-GPU node implies nothing — the core of the auto-claim fix.
func TestImpliedGPUShared_TokenFromDeviceUsage(t *testing.T) {
	res := gpuResources() // {nvidia-gpu: {gpu: {vendor: 0x10de}}}

	withDetectGPU(t, true)
	if tok := impliedGPUSharedToken(gpuPodNode(), res); tok != "nvidia-gpu" {
		t.Fatalf("a GPU-consuming pod must imply the nvidia-gpu token, got %q", tok)
	}

	withDetectGPU(t, false)
	if tok := impliedGPUSharedToken(gpuPodNode(), res); tok != "" {
		t.Fatalf("a non-GPU pod must imply no token, got %q", tok)
	}
}

// I2. The implied token is derived from the resource: config — a host with no
// gpu-backed token implies nothing even when the GPU is present.
func TestImpliedGPUShared_NoTokenWithoutResourceConfig(t *testing.T) {
	withDetectGPU(t, true)
	if tok := impliedGPUSharedToken(gpuPodNode(), nil); tok != "" {
		t.Fatalf("no resource config → no implied token, got %q", tok)
	}
	// A selector-less (abstract) token is not gpu-backed → not implied.
	abstract := map[string]*ResourceDef{"abstract": {}}
	if tok := impliedGPUSharedToken(gpuPodNode(), abstract); tok != "" {
		t.Fatalf("a selector-less token must not be implied, got %q", tok)
	}
}

// I3. A node-intrinsic /dev/nvidia* device declaration implies the token even
// when host auto-detection is momentarily false (card consumer regardless).
func TestImpliedGPUShared_SecurityDevicesSignal(t *testing.T) {
	withDetectGPU(t, false)
	node := BundleNode{Target: "pod", Security: &SecurityConfig{Devices: []string{"/dev/nvidia0"}}}
	if tok := impliedGPUSharedToken(node, gpuResources()); tok != "nvidia-gpu" {
		t.Fatalf("a node listing /dev/nvidia0 must imply the token, got %q", tok)
	}
	// The CDI device name is the other accepted form.
	node2 := BundleNode{Target: "pod", Security: &SecurityConfig{Devices: []string{"nvidia.com/gpu=all"}}}
	if tok := impliedGPUSharedToken(node2, gpuResources()); tok != "nvidia-gpu" {
		t.Fatalf("a node listing nvidia.com/gpu must imply the token, got %q", tok)
	}
}

// I4. An EXCLUSIVE claimant (a VM via vfio) is never treated as a pod GPU
// consumer — it gets a PCI hostdev, not the pod --device.
func TestImpliedGPUShared_ExclusiveClaimantNotImplied(t *testing.T) {
	withDetectGPU(t, true)
	if tok := impliedGPUSharedToken(claimantNode([]string{"nvidia-gpu"}), gpuResources()); tok != "" {
		t.Fatalf("an exclusive claimant must not imply a shared token, got %q", tok)
	}
}

// I5. applyImpliedGPUShared unions the token onto a bare node, and NEVER
// double-claims a token the node already lists.
func TestApplyImpliedGPUShared_UnionAndNoDoubleClaim(t *testing.T) {
	withDetectGPU(t, true)
	res := gpuResources()

	got := applyImpliedGPUShared(gpuPodNode(), res)
	if !slices.Equal(got.RequiresShared, []string{"nvidia-gpu"}) {
		t.Fatalf("bare GPU pod must gain requires_shared:[nvidia-gpu], got %v", got.RequiresShared)
	}

	// Already an explicit shared claimant → unchanged (no duplicate).
	explicit := sharedNode([]string{"nvidia-gpu"})
	got = applyImpliedGPUShared(explicit, res)
	if !slices.Equal(got.RequiresShared, []string{"nvidia-gpu"}) {
		t.Fatalf("must not double-claim an already-declared token, got %v", got.RequiresShared)
	}
}

// I6. End-to-end: a GPU-consuming pod that declared NO claim becomes a tracked,
// preemptable shared claimant — an exclusive (vfio VM) claim stops it and frees
// the card. This is the RCA fix: an untracked auto-detected GPU pod no longer
// silently blocks a requires_exclusive claimant.
func TestArbiter_PreemptsImpliedSharedGPUPod(t *testing.T) {
	withDetectGPU(t, true)
	w := &fakeWorld{running: map[string]bool{"untracked-gpu-pod": true, "vm": true}, resources: gpuResources()}
	a := newTestArbiter(t, map[string]BundleNode{}, w)

	// The pod authored NOTHING; the implied-shared promotion makes it claim the
	// gpu token (exactly what acquireResourceForClaimant does at start).
	node := applyImpliedGPUShared(gpuPodNode(), w.resources)
	if len(node.RequiredShared()) == 0 {
		t.Fatalf("precondition: implied promotion must give the pod a shared claim, got %+v", node)
	}
	if _, err := a.AcquireShared("untracked-gpu-pod", node, false); err != nil {
		t.Fatalf("shared acquire for the auto-tracked GPU pod: %v", err)
	}

	// The operator's exclusive claimant now arrives — it must stop the pod.
	if _, err := a.AcquireExclusive("vm", claimantNode([]string{"nvidia-gpu"}), false); err != nil {
		t.Fatalf("vm exclusive acquire: %v", err)
	}
	if w.running["untracked-gpu-pod"] {
		t.Fatalf("exclusive claim must stop the auto-tracked GPU pod; ops=%v", w.ops)
	}
	if w.modes["0x10de"] != gpuModeVfio {
		t.Fatalf("exclusive claim must flip the card to vfio; modes=%v", w.modes)
	}
	led, _, _ := a.Status()
	if len(led.Leases) != 1 || led.Leases[0].Shared || led.Leases[0].Claimant != "vm" {
		t.Fatalf("only the exclusive vm lease should remain, got %+v", led.Leases)
	}
}
