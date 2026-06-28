package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// TestPreemptStatusInternal_RendersArbiterLeases proves the hidden `charly __preempt-status`
// path (PreemptStatusInternalCmd.Run → renderPreemptStatus) actually invokes the in-core
// resource arbiter and surfaces its lease ledger — the seam the externalized
// candy/plugin-preempt shells back to. An empty ledger renders the no-leases message; a seeded
// ACTIVE transient lease renders its claimant, token, preempted holder, and live state.
func TestPreemptStatusInternal_RendersArbiterLeases(t *testing.T) {
	w := &fakeWorld{running: map[string]bool{}}
	a := newTestArbiter(t, map[string]BundleNode{}, w)

	// Empty ledger → the "no leases" message proves renderPreemptStatus reads the arbiter.
	var empty bytes.Buffer
	if err := renderPreemptStatus(a, &empty); err != nil {
		t.Fatalf("renderPreemptStatus (empty): %v", err)
	}
	if got := empty.String(); !strings.Contains(got, "No active preemption leases.") {
		t.Fatalf("empty-ledger render = %q, want the no-leases message", got)
	}

	// Seed one ACTIVE transient lease (owner = this live process, so leaseLive → not stranded)
	// and assert the table renders the claimant + token + preempted holder the arbiter's
	// Status() returns — proving the hidden __preempt-status path surfaces real arbiter state.
	if err := a.saveLedger(&preemptLedger{Leases: []preemptLease{{
		Claimant:   "check-gpu-bed",
		Tokens:     []string{"nvidia-gpu"},
		Transient:  true,
		Created:    "2026-01-01T00:00:00Z",
		OwnerPID:   os.Getpid(),
		OwnerStart: selfProcStart(),
		Preempted:  []preemptedHolder{{Addr: holderAddr{Name: "gpu-workstation"}}},
	}}}); err != nil {
		t.Fatalf("seed ledger: %v", err)
	}
	var buf bytes.Buffer
	if err := renderPreemptStatus(a, &buf); err != nil {
		t.Fatalf("renderPreemptStatus (seeded): %v", err)
	}
	out := buf.String()
	for _, want := range []string{"check-gpu-bed", "nvidia-gpu", "gpu-workstation", "active"} {
		if !strings.Contains(out, want) {
			t.Fatalf("seeded render missing %q:\n%s", want, out)
		}
	}
}
