package main

import (
	"path/filepath"
	"testing"
)

// TestVmDiskDir_PerVM asserts disk/seed output is namespaced per VM, so building
// or creating one VM never reuses a sibling VM's disk or (critically) its stale
// seed.iso — the regression that made `ov vm create cachyos-coder` adopt the
// bed VM's seed (whose embedded SSH key mismatched cachyos-coder's id_ed25519).
func TestVmDiskDir_PerVM(t *testing.T) {
	coder := vmDiskDir("cachyos-coder")
	bed := vmDiskDir("cachyos-gpu-vm")
	if coder == bed {
		t.Fatalf("vmDiskDir must be per-VM; got identical paths for two VMs: %s", coder)
	}
	want := filepath.Join("output", "qcow2", "cachyos-coder")
	if coder != want {
		t.Errorf("vmDiskDir(cachyos-coder) = %q, want %q", coder, want)
	}
}
