package main

import (
	"path/filepath"
	"testing"
)

// TestPickRenderNode_PrefersRealGPUOverVirtio guards the DRINODE/DRI_NODE
// auto-detect: on a GPU-passthrough VM the seat's virtio-gpu is renderD128 and
// the passed-through card is renderD129, so the first-wins default pointed the
// encoder/VAAPI probe at the wrong node. pickRenderNode must prefer the real
// GPU, while staying first-wins on single-GPU hosts.
func TestPickRenderNode_PrefersRealGPUOverVirtio(t *testing.T) {
	orig := renderNodeVendor
	defer func() { renderNodeVendor = orig }()

	// GPU-passthrough VM: virtio head (renderD128) + NVIDIA (renderD129).
	renderNodeVendor = func(node string) string {
		switch filepath.Base(node) {
		case "renderD128":
			return "0x1af4" // virtio-gpu
		case "renderD129":
			return "0x10de" // NVIDIA
		}
		return ""
	}
	if got := pickRenderNode([]string{"/dev/dri/renderD128", "/dev/dri/renderD129", "/dev/kfd"}); got != "/dev/dri/renderD129" {
		t.Fatalf("VM: want /dev/dri/renderD129 (NVIDIA), got %q", got)
	}

	// AMD-only host: the single render node is the AMD GPU — first-wins holds.
	renderNodeVendor = func(string) string { return "0x1002" }
	if got := pickRenderNode([]string{"/dev/dri/renderD128"}); got != "/dev/dri/renderD128" {
		t.Fatalf("AMD host: want /dev/dri/renderD128, got %q", got)
	}

	// No vendor info (unreadable /sys): fall back to the first render node.
	renderNodeVendor = func(string) string { return "" }
	if got := pickRenderNode([]string{"/dev/dri/renderD128", "/dev/dri/renderD129"}); got != "/dev/dri/renderD128" {
		t.Fatalf("fallback: want first /dev/dri/renderD128, got %q", got)
	}

	// No render nodes present → empty (kfd/kvm are not render nodes).
	if got := pickRenderNode([]string{"/dev/kfd", "/dev/kvm"}); got != "" {
		t.Fatalf("no render nodes: want empty, got %q", got)
	}
}
