package main

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeWorld is an in-memory model of deployment running-state + the lifecycle
// operations the arbiter performs, so the arbiter logic is testable without
// real VMs/pods. Keyed by deploy name (holderAddr.Name).
type fakeWorld struct {
	running   map[string]bool
	ops       []string
	stopErr   map[string]bool         // names whose stop() should fail
	resources map[string]*ResourceDef // token -> ResourceDef (gpu selector drives mode flips)
	modes     map[string]string       // vendor -> last mode flipped to (by switchMode)
	cdiCalls  int                     // EnsureCDI invocations (nvidia-direction flips)
}

func newTestArbiter(t *testing.T, holders map[string]DeploymentNode, w *fakeWorld) *ResourceArbiter {
	t.Helper()
	return &ResourceArbiter{
		ledgerPath: filepath.Join(t.TempDir(), "leases.yml"),
		gather:     func() map[string]DeploymentNode { return holders },
		running:    func(addr holderAddr) bool { return w.running[addr.Name] },
		stop: func(addr holderAddr) error {
			if w.stopErr[addr.Name] {
				w.ops = append(w.ops, "stop-FAIL:"+addr.Name)
				return errStopFailed
			}
			w.running[addr.Name] = false
			w.ops = append(w.ops, "stop:"+addr.Name)
			return nil
		},
		start: func(addr holderAddr) error {
			w.running[addr.Name] = true
			w.ops = append(w.ops, "start:"+addr.Name)
			return nil
		},
		resources: func() map[string]*ResourceDef { return w.resources },
		switchMode: func(vendor, mode string) error {
			if w.modes == nil {
				w.modes = map[string]string{}
			}
			w.modes[vendor] = mode
			w.ops = append(w.ops, "mode:"+mode+":"+vendor)
			return nil
		},
		ensureCDI: func() { w.cdiCalls++ },
		nowUTC:    func() string { return "2026-01-01T00:00:00Z" },
	}
}

var errStopFailed = &stopFailedError{}

type stopFailedError struct{}

func (*stopFailedError) Error() string { return "stop failed (test)" }

func holderNode(holds []string, restore string) DeploymentNode {
	return DeploymentNode{
		Target:      "pod",
		Preemptible: &PreemptibleConfig{Holds: holds, Restore: restore},
	}
}

func claimantNode(requires []string) DeploymentNode {
	return DeploymentNode{Target: "pod", RequiresExclusive: requires}
}

func opsContain(ops []string, want string) bool {
	return slices.Contains(ops, want)
}

// 1. Acquire stops a running holder of the token; Release restores it.
func TestArbiter_AcquireStopsHolder_ReleaseRestores(t *testing.T) {
	w := &fakeWorld{running: map[string]bool{"h1": true, "claimant": true}}
	holders := map[string]DeploymentNode{"h1": holderNode([]string{"gpu"}, "")}
	a := newTestArbiter(t, holders, w)

	lease, err := a.AcquireExclusive("claimant", claimantNode([]string{"gpu"}), true)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if w.running["h1"] {
		t.Fatalf("holder h1 should be stopped after acquire; ops=%v", w.ops)
	}
	led, _, _ := a.Status()
	if len(led.Leases) != 1 || led.Leases[0].Claimant != "claimant" {
		t.Fatalf("expected one lease for claimant, got %+v", led.Leases)
	}

	if err := lease.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if !w.running["h1"] {
		t.Fatalf("holder h1 should be restored after release; ops=%v", w.ops)
	}
	led, _, _ = a.Status()
	if len(led.Leases) != 0 {
		t.Fatalf("lease should be gone after release, got %+v", led.Leases)
	}
}

// 2. A holder already stopped before the claim is neither stopped nor restored.
func TestArbiter_AlreadyStoppedHolder_LeftAlone(t *testing.T) {
	w := &fakeWorld{running: map[string]bool{"h1": false, "claimant": true}}
	holders := map[string]DeploymentNode{"h1": holderNode([]string{"gpu"}, "")}
	a := newTestArbiter(t, holders, w)

	lease, err := a.AcquireExclusive("claimant", claimantNode([]string{"gpu"}), true)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if opsContain(w.ops, "stop:h1") {
		t.Fatalf("already-stopped holder must not be stopped; ops=%v", w.ops)
	}
	_ = lease.Release()
	if opsContain(w.ops, "start:h1") {
		t.Fatalf("must not start a holder we never stopped; ops=%v", w.ops)
	}
}

// 3. A second claimant of an overlapping token is refused while the first lease
// is live.
func TestArbiter_ConflictRefusesSecondClaimant(t *testing.T) {
	w := &fakeWorld{running: map[string]bool{"h1": true, "c1": true, "c2": true}}
	holders := map[string]DeploymentNode{"h1": holderNode([]string{"gpu"}, "")}
	a := newTestArbiter(t, holders, w)

	if _, err := a.AcquireExclusive("c1", claimantNode([]string{"gpu"}), false); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	_, err := a.AcquireExclusive("c2", claimantNode([]string{"gpu"}), false)
	if err == nil || !strings.Contains(err.Error(), "already claimed by") {
		t.Fatalf("expected conflict error for c2, got %v", err)
	}
}

// 4. A holder whose token does not overlap the claim is untouched.
func TestArbiter_NonOverlappingTokensIgnored(t *testing.T) {
	w := &fakeWorld{running: map[string]bool{"h1": true, "claimant": true}}
	holders := map[string]DeploymentNode{"h1": holderNode([]string{"other"}, "")}
	a := newTestArbiter(t, holders, w)

	if _, err := a.AcquireExclusive("claimant", claimantNode([]string{"gpu"}), true); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if !w.running["h1"] {
		t.Fatalf("non-overlapping holder h1 must not be stopped; ops=%v", w.ops)
	}
}

// 5. reconcileStranded restores holders for a lease whose claimant is gone.
func TestArbiter_ReconcileStrandedRestores(t *testing.T) {
	w := &fakeWorld{running: map[string]bool{"h1": false, "deadclaimant": false}}
	a := newTestArbiter(t, map[string]DeploymentNode{}, w)

	// Hand-write a stranded lease (claimant not running, holder stopped).
	led := &preemptLedger{Leases: []preemptLease{{
		Claimant: "deadclaimant",
		Claim:    holderAddr{Name: "deadclaimant", Target: "pod", Base: "deadclaimant"},
		Tokens:   []string{"gpu"},
		Preempted: []preemptedHolder{{
			Addr:    holderAddr{Name: "h1", Target: "pod", Base: "h1"},
			Holds:   []string{"gpu"},
			Restore: PreemptRestoreAlways,
		}},
	}}}
	if err := a.saveLedger(led); err != nil {
		t.Fatalf("save: %v", err)
	}

	if err := a.reconcileStranded(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !w.running["h1"] {
		t.Fatalf("stranded holder h1 should be restored; ops=%v", w.ops)
	}
	got, _, _ := a.Status()
	if len(got.Leases) != 0 {
		t.Fatalf("stranded lease should be pruned, got %+v", got.Leases)
	}
}

// 6a. restore: on-success — a FAILED claim leaves the holder stopped.
func TestArbiter_RestoreOnSuccess_FailedClaimLeavesStopped(t *testing.T) {
	w := &fakeWorld{running: map[string]bool{"h1": true, "claimant": true}}
	holders := map[string]DeploymentNode{"h1": holderNode([]string{"gpu"}, PreemptRestoreSuccess)}
	a := newTestArbiter(t, holders, w)

	lease, err := a.AcquireExclusive("claimant", claimantNode([]string{"gpu"}), true)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if w.running["h1"] {
		t.Fatal("holder should be stopped after acquire")
	}
	if err := lease.ReleaseFailed(); err != nil {
		t.Fatalf("release-failed: %v", err)
	}
	if w.running["h1"] {
		t.Fatalf("on-success holder must stay stopped on a failed claim; ops=%v", w.ops)
	}
}

// 6b. restore: always — even a FAILED claim restores the holder.
func TestArbiter_RestoreAlways_FailedClaimRestores(t *testing.T) {
	w := &fakeWorld{running: map[string]bool{"h1": true, "claimant": true}}
	holders := map[string]DeploymentNode{"h1": holderNode([]string{"gpu"}, PreemptRestoreAlways)}
	a := newTestArbiter(t, holders, w)

	lease, _ := a.AcquireExclusive("claimant", claimantNode([]string{"gpu"}), true)
	if err := lease.ReleaseFailed(); err != nil {
		t.Fatalf("release-failed: %v", err)
	}
	if !w.running["h1"] {
		t.Fatalf("always-restore holder must come back even on failure; ops=%v", w.ops)
	}
}

// 7. The ledger persists across arbiter instances (durable, crash-recoverable).
func TestArbiter_LedgerPersistsAcrossInstances(t *testing.T) {
	w := &fakeWorld{running: map[string]bool{"h1": true, "claimant": true}}
	holders := map[string]DeploymentNode{"h1": holderNode([]string{"gpu"}, "")}
	a1 := newTestArbiter(t, holders, w)
	if _, err := a1.AcquireExclusive("claimant", claimantNode([]string{"gpu"}), false); err != nil {
		t.Fatalf("acquire: %v", err)
	}

	// A fresh arbiter pointed at the same ledger sees the lease and can release.
	a2 := &ResourceArbiter{
		ledgerPath: a1.ledgerPath,
		gather:     a1.gather,
		running:    a1.running,
		stop:       a1.stop,
		start:      a1.start,
		resources:  a1.resources,
		switchMode: a1.switchMode,
		ensureCDI:  a1.ensureCDI,
		nowUTC:     a1.nowUTC,
	}
	led, _, _ := a2.Status()
	if len(led.Leases) != 1 {
		t.Fatalf("a2 should see the persisted lease, got %+v", led.Leases)
	}
	if err := a2.ReleaseClaimant("claimant", true); err != nil {
		t.Fatalf("a2 release: %v", err)
	}
	if !w.running["h1"] {
		t.Fatalf("a2 release should restore h1; ops=%v", w.ops)
	}
}

// 8. A claimant that requires nothing exclusive gets a no-op lease.
func TestArbiter_NoTokensIsNoop(t *testing.T) {
	w := &fakeWorld{running: map[string]bool{"h1": true}}
	holders := map[string]DeploymentNode{"h1": holderNode([]string{"gpu"}, "")}
	a := newTestArbiter(t, holders, w)
	lease, err := a.AcquireExclusive("claimant", claimantNode(nil), true)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if w.running["h1"] != true {
		t.Fatal("no-token claim must not stop any holder")
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("no-op release: %v", err)
	}
}

// 9. A stop failure rolls back: holders already stopped this call are
// restarted and the phantom lease is removed.
func TestArbiter_StopFailureRollsBack(t *testing.T) {
	w := &fakeWorld{
		running: map[string]bool{"h1": true, "h2": true, "claimant": true},
		stopErr: map[string]bool{"h2": true},
	}
	holders := map[string]DeploymentNode{
		"h1": holderNode([]string{"gpu"}, ""),
		"h2": holderNode([]string{"gpu"}, ""),
	}
	a := newTestArbiter(t, holders, w)

	_, err := a.AcquireExclusive("claimant", claimantNode([]string{"gpu"}), true)
	if err == nil {
		t.Fatal("expected acquire to fail when a holder stop fails")
	}
	// h1 was stopped first, then h2 failed → h1 must be rolled back (restarted).
	if !w.running["h1"] {
		t.Fatalf("h1 should be rolled back to running after the failed acquire; ops=%v", w.ops)
	}
	led, _, _ := a.Status()
	if len(led.Leases) != 0 {
		t.Fatalf("no lease should remain after a rolled-back acquire, got %+v", led.Leases)
	}
}
