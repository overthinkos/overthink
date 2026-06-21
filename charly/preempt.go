package main

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

// Resource arbitration — the "preemptible" axis (see classification.go +
// /charly-internals:disposable). A preemptible HOLDER (a deploy carrying
// `preemptible: {holds: [...]}`) occupies named exclusive host-resource
// token(s) and MAY be gracefully stopped to free them for a CLAIMANT that
// declares `requires_exclusive: [...]`, then MUST be restarted afterward
// (disk + definition preserved). The arbiter coordinates this.
//
// Crash-safety is the load-bearing property: a crash must NEVER leave a
// holder permanently stopped. Two mechanisms guarantee it:
//
//  1. The lease ledger (~/.local/share/charly/preemption/leases.yml) is written
//     listing the holders to stop BEFORE any holder is stopped, and "restore"
//     means "start every listed holder that isn't currently running". So a
//     crash at any point — before, during, or after the stops — leaves a
//     recoverable record, and restoring is idempotent (already-running
//     holders are skipped).
//  2. reconcileStranded() (run at every AcquireExclusive, and via
//     `charly preempt restore`) restores any lease whose claimant is no longer
//     running. The normal path also restores via Lease.Release()/ReleaseFailed().

// holderAddr is the self-contained address of a deployment (holder or
// claimant) — enough to probe/stop/start it WITHOUT re-reading config, so a
// lease loaded after a crash can act on it.
type holderAddr struct {
	Name     string `yaml:"name" json:"name"`                             // full deploy key (for messages)
	Target   string `yaml:"target" json:"target"`                         // "vm" | "pod"
	Base     string `yaml:"base" json:"base"`                             // parseDeployKey base (pod container basis / vm fallback)
	Instance string `yaml:"instance,omitempty" json:"instance,omitempty"` // parseDeployKey instance
	Vm       string `yaml:"vm,omitempty" json:"vm,omitempty"`             // vm entity (target:vm)
}

// preemptedHolder records one holder a lease stopped, its declared exclusive
// tokens, and its restore policy — so ReleaseClaimant/reconcile restart
// exactly what was stopped.
type preemptedHolder struct {
	Addr    holderAddr `yaml:"addr" json:"addr"`
	Holds   []string   `yaml:"holds" json:"holds"`
	Restore string     `yaml:"restore" json:"restore"` // always | on-success
}

// preemptLease is one active resource claim — exclusive (a VM with sole use) OR
// shared (a refcounted pod claim; many coexist on one token).
type preemptLease struct {
	Claimant  string            `yaml:"claimant" json:"claimant"`
	Claim     holderAddr        `yaml:"claim" json:"claim"` // the claimant DEPLOYMENT addr (persistent-lease liveness; see leaseLive)
	Tokens    []string          `yaml:"tokens" json:"tokens"`
	Shared    bool              `yaml:"shared,omitempty" json:"shared,omitempty"` // true = refcounted SHARED claim (pods); false = EXCLUSIVE (VM)
	Mode      string            `yaml:"mode,omitempty" json:"mode,omitempty"`     // driver MODE this claim needs: "nvidia" (shared) | "vfio" (exclusive); "" = legacy/none
	Transient bool              `yaml:"transient" json:"transient"`               // check-bed claims auto-release; persistent claims (vm create/start) don't
	Preempted []preemptedHolder `yaml:"preempted" json:"preempted"`               // holders/pods THIS claim stopped + must restore on release
	Created   string            `yaml:"created" json:"created"`                   // RFC3339 UTC
	// OwnerPID/OwnerStart identify the OUTERMOST process that created this lease —
	// the long-lived `charly check run` orchestrator for a transient bed claim,
	// the (short-lived) `charly start`/`charly vm create` invocation for a
	// persistent claim. They are the liveness signal a CONCURRENT charly process's
	// reconcile uses to tell a still-working owner from a crashed one (leaseLive);
	// OwnerStart is the /proc start-time, a PID-REUSE guard. Set only in the
	// outermost process (nested subprocesses skip the arbiter — envPreemptLeaseHeld).
	OwnerPID   int    `yaml:"owner_pid,omitempty" json:"owner_pid,omitempty"`
	OwnerStart string `yaml:"owner_start,omitempty" json:"owner_start,omitempty"`
}

type preemptLedger struct {
	Leases []preemptLease `yaml:"leases" json:"leases"`
}

// Lease is the handle returned by AcquireExclusive. Release() restores
// holders on the success path; ReleaseFailed() applies the restore policy for
// a failed claim (on-success holders stay stopped). A zero/no-op Lease (the
// claimant required nothing exclusive) is safe to Release.
type Lease struct {
	arbiter  *ResourceArbiter
	claimant string
	active   bool
}

// Release restores preempted holders assuming the claim succeeded.
func (l *Lease) Release() error {
	if l == nil || !l.active || l.arbiter == nil {
		return nil
	}
	return l.arbiter.ReleaseClaimant(l.claimant, true)
}

// ReleaseFailed restores preempted holders per policy for a FAILED claim:
// `restore: always` holders come back; `restore: on-success` holders stay
// stopped for operator inspection.
func (l *Lease) ReleaseFailed() error {
	if l == nil || !l.active || l.arbiter == nil {
		return nil
	}
	return l.arbiter.ReleaseClaimant(l.claimant, false)
}

// ResourceArbiter coordinates exclusive host-resource access. Its four
// behaviors are injected so unit tests can fake holder discovery + lifecycle
// without real deployments; newResourceArbiter wires the production ones.
type ResourceArbiter struct {
	ledgerPath string
	gather     func() map[string]BundleNode    // candidate preemptible holders
	running    func(addr holderAddr) bool      // is this deployment running?
	stop       func(addr holderAddr) error     // graceful stop
	start      func(addr holderAddr) error     // start an already-configured deployment
	resources  func() map[string]*ResourceDef  // token -> ResourceDef (gpu selector for the mode flip)
	switchMode func(vendor, mode string) error // flip a gpu-backed token's host driver (vfio<->nvidia)
	ensureCDI  func()                          // (re)generate the nvidia CDI spec after a flip to nvidia
	nowUTC     func() string
}

func newResourceArbiter() *ResourceArbiter {
	return &ResourceArbiter{
		ledgerPath: preemptLedgerPath(),
		gather:     gatherPreemptibleHolders,
		running:    holderRunning,
		stop:       holderStop,
		start:      holderStart,
		resources:  gatherResources,
		switchMode: gpuSwitchModeTolerant,
		ensureCDI:  ensureCDIRoot,
		nowUTC:     func() string { return time.Now().UTC().Format(time.RFC3339) },
	}
}

// applyMode flips every gpu-backed token in `tokens` to the target driver MODE
// (+ regenerate CDI after a flip to nvidia). Selector-less tokens are pure
// refcounted arbitration labels — there is no device to flip, so they're
// skipped. Idempotent at the primitive level (switchMode no-ops when already in
// mode). This is the ONE place a claim's desired mode reaches the hardware.
func (a *ResourceArbiter) applyMode(tokens []string, mode string) error {
	resources := a.resources()
	for _, tok := range tokens {
		rdef := resources[tok]
		if rdef == nil || rdef.Gpu == nil {
			continue // arbitration-only token — no physical device to rebind
		}
		// Refuse to touch a card whose driver switch previously WEDGED its
		// device_lock — re-issuing a bind/unbind would D-state behind the stuck
		// nvidia `.remove` (a SECOND permanent wedge). The poison clears on
		// reboot (boot-id keyed). Guards BOTH the acquire flip and the reconcile
		// flip from re-touching a wedged card.
		if a.resourcePoisoned(tok) {
			return fmt.Errorf("resource %q is poisoned: a previous GPU driver switch wedged the device_lock — a host reboot is required (%w)", tok, errGPUSwitchWedged)
		}
		if err := a.switchMode(rdef.Gpu.Vendor, mode); err != nil {
			if errors.Is(err, errGPUSwitchWedged) {
				a.poisonResource(tok) // contain the cascade: no later claimant may re-wedge
			}
			return fmt.Errorf("setting resource %q to %s mode: %w", tok, mode, err)
		}
		if mode == gpuModeNvidia {
			a.ensureCDI()
		}
	}
	return nil
}

// holdersToStop selects the RUNNING preemptible holders whose holds intersect
// `tokens` (excluding the claimant itself) — the VMs to gracefully stop to free
// the resource. Shared between exclusive and shared acquisition (R3).
func (a *ResourceArbiter) holdersToStop(tokens []string, claimant string) []preemptedHolder {
	holders := a.gather()
	var toStop []preemptedHolder
	for _, name := range sortedHolderKeys(holders) {
		if name == claimant {
			continue
		}
		node := holders[name]
		shared := intersect(node.PreemptionHolds(), tokens)
		if len(shared) == 0 {
			continue
		}
		addr := holderAddrFor(name, node)
		if !a.running(addr) {
			continue
		}
		toStop = append(toStop, preemptedHolder{
			Addr:    addr,
			Holds:   shared,
			Restore: preemptEffectiveRestore(node.Preemptible),
		})
	}
	return toStop
}

// stopHolders gracefully stops each holder and WAITS until it powers off (the
// resource is truly released) before returning. On any stop failure it rolls
// back — restarts what it already stopped and drops the claimant's lease — so a
// partial preemption never strands a holder or leaves a phantom lease. Shared
// between exclusive and shared acquisition (R3).
func (a *ResourceArbiter) stopHolders(toStop []preemptedHolder, claimant string) error {
	for i, ph := range toStop {
		fmt.Fprintf(os.Stderr, "preempt: stopping holder %q to free %s for %q\n",
			ph.Addr.Name, strings.Join(ph.Holds, ", "), claimant)
		stopErr := a.stop(ph.Addr)
		if stopErr == nil && !a.waitStopped(ph.Addr) {
			stopErr = fmt.Errorf("holder %q did not reach a stopped state within the stop grace (resource not freed)", ph.Addr.Name)
		}
		if stopErr != nil {
			for _, done := range toStop[:i] {
				if !a.running(done.Addr) {
					_ = a.start(done.Addr)
				}
			}
			_ = a.removeLease(claimant)
			return fmt.Errorf("preempting holder %q: %w", ph.Addr.Name, stopErr)
		}
		fmt.Fprintf(os.Stderr, "preempt: holder %q stopped — %s freed for %q\n",
			ph.Addr.Name, strings.Join(ph.Holds, ", "), claimant)
	}
	return nil
}

// envPreemptLeaseHeld is set by the OUTERMOST claim-bringing `charly` invocation
// (runCheckBed, or a standalone `charly vm create`/`charly start`) so that the nested
// `charly` subprocesses it spawns (the bed's `charly vm create`/`charly bundle add`/
// `charly vm destroy`, etc.) do NOT independently acquire or release the lease —
// the owner manages it. An env channel, not config: it scopes to one process
// tree, mirroring the codebase's existing env-as-IPC idioms (CHARLY_PROJECT_DIR,
// the nested-runtime keys).
const envPreemptLeaseHeld = "CHARLY_PREEMPT_LEASE"

// acquireExclusiveForClaimant acquires (or reuses) an exclusive-resource lease
// for a claimant deploy that declares requires_exclusive — UNLESS an outer
// orchestrator already owns one (envPreemptLeaseHeld set), in which case the
// claim is already covered and this is a no-op. On a real acquire it marks the
// environment so nested `charly` subprocesses skip re-acquiring. Returns a lease
// whose Release()/ReleaseFailed() the caller must invoke (defer); a no-op
// lease is safe to Release. transient=true for check-bed claims (auto-released
// at run end), false for persistent claims (charly vm create / charly start).
func acquireExclusiveForClaimant(claimant string, node BundleNode, transient bool) (*Lease, error) {
	if len(node.RequiredExclusive()) == 0 {
		return &Lease{}, nil
	}
	if os.Getenv(envPreemptLeaseHeld) != "" {
		return &Lease{}, nil // an outer orchestrator owns the lease
	}
	lease, err := newResourceArbiter().AcquireExclusive(claimant, node, transient)
	if err != nil {
		return nil, err
	}
	if lease != nil && lease.active {
		_ = os.Setenv(envPreemptLeaseHeld, claimant)
	}
	return lease, nil
}

// acquireSharedForClaimant acquires (or reuses) a SHARED refcounted lease for a
// pod/bed that declares requires_shared (e.g. a GPU shared across pods via CDI)
// — UNLESS an outer orchestrator already owns the lease (envPreemptLeaseHeld
// set). Mirrors acquireExclusiveForClaimant; the release path is shared
// (releaseResourceClaim). A no-op lease when nothing shared is claimed.
func acquireSharedForClaimant(claimant string, node BundleNode, transient bool) (*Lease, error) {
	if len(node.RequiredShared()) == 0 {
		return &Lease{}, nil
	}
	if os.Getenv(envPreemptLeaseHeld) != "" {
		return &Lease{}, nil // an outer orchestrator owns the lease
	}
	lease, err := newResourceArbiter().AcquireShared(claimant, node, transient)
	if err != nil {
		return nil, err
	}
	if lease != nil && lease.active {
		_ = os.Setenv(envPreemptLeaseHeld, claimant)
	}
	return lease, nil
}

// acquireResourceForClaimant acquires the appropriate lease for a claimant:
// EXCLUSIVE when it declares requires_exclusive, SHARED when it declares
// requires_shared (a node declares at most one — enforced by validation), a
// no-op lease when it claims nothing. The single entry point for the start +
// check-bed paths (R3), so both honor the same exclusive/shared semantics.
//
// A node that USES the nvidia GPU device but declared NO explicit claim is
// auto-promoted to a SHARED claimant of the gpu token here (withImpliedGPUShared):
// EVERY GPU-consuming deployment becomes a tracked, preemptable shared claimant
// with no per-deploy config, so an exclusive claimant (a vfio VM) can stop it to
// free the card. The promotion never touches an exclusive claimant (a VM gets a
// PCI hostdev, not the pod device) and never double-claims a token the node
// already lists.
func acquireResourceForClaimant(claimant string, node BundleNode, transient bool) (*Lease, error) {
	node = withImpliedGPUShared(node)
	if len(node.RequiredExclusive()) > 0 {
		return acquireExclusiveForClaimant(claimant, node, transient)
	}
	if len(node.RequiredShared()) > 0 {
		return acquireSharedForClaimant(claimant, node, transient)
	}
	return &Lease{}, nil
}

// releaseResourceClaim releases a persistent claimant's resource-arbitration
// lease on teardown (charly vm stop/destroy, charly stop, charly remove) —
// kind-agnostic: it releases whatever lease (SHARED or EXCLUSIVE) the claimant
// holds, by name. Best-effort, a no-op when the claimant holds no lease, and
// skipped when an outer orchestrator owns the lease (envPreemptLeaseHeld set —
// the owner will release it).
func releaseResourceClaim(claimant string) {
	if os.Getenv(envPreemptLeaseHeld) != "" {
		return
	}
	if err := newResourceArbiter().ReleaseClaimant(claimant, true); err != nil {
		fmt.Fprintf(os.Stderr, "preempt: %v\n", err)
	}
}

func preemptLedgerPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "charly-preemption-leases.yml")
	}
	return filepath.Join(home, ".local", "share", "charly", "preemption", "leases.yml")
}

func (a *ResourceArbiter) loadLedger() (*preemptLedger, error) {
	data, err := os.ReadFile(a.ledgerPath)
	if errors.Is(err, os.ErrNotExist) {
		return &preemptLedger{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading preemption ledger %s: %w", a.ledgerPath, err)
	}
	var l preemptLedger
	if err := yaml.Unmarshal(data, &l); err != nil {
		return nil, fmt.Errorf("parsing preemption ledger %s: %w", a.ledgerPath, err)
	}
	return &l, nil
}

// saveLedger writes the ledger atomically (temp + rename) so a crash never
// leaves a half-written ledger.
func (a *ResourceArbiter) saveLedger(l *preemptLedger) error {
	if err := os.MkdirAll(filepath.Dir(a.ledgerPath), 0o755); err != nil {
		return fmt.Errorf("creating preemption dir: %w", err)
	}
	data, err := yaml.Marshal(l)
	if err != nil {
		return err
	}
	tmp := a.ledgerPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("writing preemption ledger: %w", err)
	}
	if err := os.Rename(tmp, a.ledgerPath); err != nil {
		return fmt.Errorf("committing preemption ledger: %w", err)
	}
	return nil
}

// acquireArbiterLock serializes the whole load→decide→GPU-flip→save sequence of
// AcquireExclusive / AcquireShared / ReleaseClaimant across concurrent charly
// processes (the unified flock primitive — filelock.go). Without it, two
// concurrent GPU claimants both load a ledger with no lease, both flip the
// driver at once — the observed "switch-to-nvidia FAILED: 0000:01:00.0
// driver=unbound" race when comfyui + another GPU bed start together — and both
// race the lease-ledger write. Blocking: a flip is brief, and the idempotent
// currentGPUMode check makes the second claimant a no-op once the first flipped.
func (a *ResourceArbiter) acquireArbiterLock() (func() error, error) {
	return acquireFileLock(filepath.Join(filepath.Dir(a.ledgerPath), ".lock"), true)
}

// AcquireExclusive gives a VM SOLE use of its required tokens: it stops every
// running preemptible HOLDER of them AND every running SHARED-claim pod of them,
// flips each gpu-backed token to vfio mode, and persists a crash-safe lease. The
// returned handle's Release()/ReleaseFailed() restarts the preempted HOLDERS
// (the operator's VM) — a preempted shared POD is NOT auto-restarted by the
// arbiter, because a low-level restart would bring it back while the card is
// vfio (GPU-less); instead the operator re-runs `charly start`, which re-acquires
// shared and re-flips to nvidia. The driver MODE (vfio XOR nvidia) is the real
// mutual exclusion, so a SHARED claim does not block an exclusive claim — it is
// preempted by it.
//
// transient=true marks an check-bed-style claim (auto-released at run end);
// false marks a persistent claim (charly vm create / charly start) released only when
// the claimant itself is torn down. A claimant that requires nothing exclusive
// gets a no-op lease.
func (a *ResourceArbiter) AcquireExclusive(claimant string, claimantNode BundleNode, transient bool) (*Lease, error) {
	tokens := dedupeNonEmpty(claimantNode.RequiredExclusive())
	if len(tokens) == 0 {
		return &Lease{}, nil
	}

	// Serialize the whole acquire (flip + ledger write) against concurrent GPU
	// claimants — see acquireArbiterLock.
	unlock, lerr := a.acquireArbiterLock()
	if lerr != nil {
		return nil, fmt.Errorf("acquiring resource-arbiter lock: %w", lerr)
	}
	defer func() { _ = unlock() }()

	// Recover any holders stranded by a previously-crashed claim BEFORE
	// reasoning about current occupancy.
	if err := a.reconcileStranded(); err != nil {
		return nil, err
	}

	ledger, err := a.loadLedger()
	if err != nil {
		return nil, err
	}

	// Idempotent re-acquire: this claimant already holds a lease → reuse it.
	for _, lz := range ledger.Leases {
		if lz.Claimant == claimant {
			return &Lease{arbiter: a, claimant: claimant, active: true}, nil
		}
	}

	// Mutual exclusion: refuse only against another EXCLUSIVE claim (one VM at a
	// time). A SHARED claim (refcounted pods) does NOT block — it is PREEMPTED.
	for _, lz := range ledger.Leases {
		if lz.Shared {
			continue
		}
		if shared := intersect(lz.Tokens, tokens); len(shared) > 0 {
			return nil, fmt.Errorf(
				"exclusive resource %s is already claimed by %q — release it (`charly preempt restore %s`) before claiming it for %q",
				strings.Join(shared, ", "), lz.Claimant, lz.Claimant, claimant)
		}
	}

	// Refuse a card wedged by a prior switch (boot-id poison) BEFORE stopping any
	// holder — a flip on a wedged device_lock would only add a second D-state.
	if tok := a.firstPoisonedToken(tokens); tok != "" {
		return nil, fmt.Errorf("GPU resource %q is unavailable until a host reboot — a previous driver switch wedged the card's device_lock (see `charly vm gpu status` / `charly vm gpu recover`); reboot clears it", tok)
	}

	// Preempt the running preemptible HOLDERS (the operator's vfio VM) — these
	// are restored when this exclusive claim releases.
	toStop := a.holdersToStop(tokens, claimant)
	// PLUS every running SHARED-claim pod of these tokens: stop it to free the
	// device for vfio, and DROP its shared lease. A preempted shared pod is NOT
	// recorded for restore (see the doc comment — the operator re-runs `charly
	// start`); carry forward only the operator-VM restore obligation the shared
	// leases held, so it is not lost.
	var kept []preemptLease
	var sharedPodStops []preemptedHolder
	for _, lz := range ledger.Leases {
		if !lz.Shared || len(intersect(lz.Tokens, tokens)) == 0 {
			kept = append(kept, lz)
			continue
		}
		if a.running(lz.Claim) {
			sharedPodStops = append(sharedPodStops, preemptedHolder{
				Addr:    lz.Claim,
				Holds:   intersect(lz.Tokens, tokens),
				Restore: PreemptRestoreAlways,
			})
		}
		toStop = append(toStop, lz.Preempted...) // carry the operator-VM obligation forward
	}
	toStop = dedupePreempted(toStop)
	ledger.Leases = kept

	// Persist the lease FIRST (crash-safety): if we crash mid-stop, the ledger
	// already names what to restore, and restore = "start any listed holder that
	// isn't running" recovers them. lease.Preempted holds ONLY the holders to
	// restore on release (operator VMs) — never the preempted shared pods.
	lease := preemptLease{
		Claimant:   claimant,
		Claim:      holderAddrFor(claimant, claimantNode),
		Tokens:     tokens,
		Shared:     false,
		Mode:       gpuModeVfio,
		Transient:  transient,
		Preempted:  toStop,
		Created:    a.nowUTC(),
		OwnerPID:   os.Getpid(),
		OwnerStart: selfProcStart(),
	}
	ledger.Leases = append(ledger.Leases, lease)
	if err := a.saveLedger(ledger); err != nil {
		return nil, err
	}

	// Stop everything (holders to restore + shared pods to free), waiting for
	// each to actually power off / stop (rollback on failure — see stopHolders),
	// then flip the gpu-backed tokens to vfio. The stops freed the device (no pod
	// holds /dev/nvidia*), so the rebind succeeds; libvirt managed='yes' then
	// re-binds vfio-pci on domain start (a no-op safety net since the card is
	// already vfio here).
	allStops := append(append([]preemptedHolder{}, lease.Preempted...), sharedPodStops...)
	if err := a.stopHolders(allStops, claimant); err != nil {
		return nil, err
	}
	if err := a.applyMode(tokens, gpuModeVfio); err != nil {
		a.restoreHolders(lease.Preempted)
		_ = a.removeLease(claimant)
		return nil, fmt.Errorf("freeing %s for vfio passthrough: %w", strings.Join(tokens, ", "), err)
	}

	return &Lease{arbiter: a, claimant: claimant, active: true}, nil
}

// AcquireShared brings up a SHARED (refcounted) claim — the pod side. Many
// shared claimants of one token run CONCURRENTLY. The FIRST shared claim flips
// the token's gpu-backed resource to nvidia mode (+ regenerate CDI) and preempts
// any running preemptible holder; subsequent claims just refcount (the device is
// already nvidia, the holder already stopped). A shared claim is refused only
// when an EXCLUSIVE claim already holds the token.
func (a *ResourceArbiter) AcquireShared(claimant string, claimantNode BundleNode, transient bool) (*Lease, error) {
	tokens := dedupeNonEmpty(claimantNode.RequiredShared())
	if len(tokens) == 0 {
		return &Lease{}, nil
	}

	// Serialize the whole acquire (flip + ledger write) against concurrent GPU
	// claimants — see acquireArbiterLock.
	unlock, lerr := a.acquireArbiterLock()
	if lerr != nil {
		return nil, fmt.Errorf("acquiring resource-arbiter lock: %w", lerr)
	}
	defer func() { _ = unlock() }()

	if err := a.reconcileStranded(); err != nil {
		return nil, err
	}
	ledger, err := a.loadLedger()
	if err != nil {
		return nil, err
	}

	// Idempotent re-acquire.
	for _, lz := range ledger.Leases {
		if lz.Claimant == claimant {
			return &Lease{arbiter: a, claimant: claimant, active: true}, nil
		}
	}

	// Refuse against an EXCLUSIVE holder (sole use). Other SHARED claims coexist.
	for _, lz := range ledger.Leases {
		if lz.Shared {
			continue
		}
		if s := intersect(lz.Tokens, tokens); len(s) > 0 {
			return nil, fmt.Errorf(
				"resource %s is held EXCLUSIVELY by %q — cannot share it for %q (release the exclusive claim first)",
				strings.Join(s, ", "), lz.Claimant, claimant)
		}
	}

	// Refuse a card wedged by a prior switch (boot-id poison) before doing work.
	if tok := a.firstPoisonedToken(tokens); tok != "" {
		return nil, fmt.Errorf("GPU resource %q is unavailable until a host reboot — a previous driver switch wedged the card's device_lock (see `charly vm gpu status` / `charly vm gpu recover`); reboot clears it", tok)
	}

	// First shared claim for these tokens → flip to nvidia + preempt holders.
	// A subsequent claim finds the device already nvidia + the holder stopped, so
	// it records no preemption and just refcounts.
	var toStop []preemptedHolder
	if !tokenHeldByShared(ledger, tokens) {
		toStop = a.holdersToStop(tokens, claimant)
	}

	lease := preemptLease{
		Claimant:   claimant,
		Claim:      holderAddrFor(claimant, claimantNode),
		Tokens:     tokens,
		Shared:     true,
		Mode:       gpuModeNvidia,
		Transient:  transient,
		Preempted:  toStop,
		Created:    a.nowUTC(),
		OwnerPID:   os.Getpid(),
		OwnerStart: selfProcStart(),
	}
	ledger.Leases = append(ledger.Leases, lease)
	if err := a.saveLedger(ledger); err != nil {
		return nil, err
	}

	if err := a.stopHolders(toStop, claimant); err != nil {
		return nil, err
	}
	if err := a.applyMode(tokens, gpuModeNvidia); err != nil {
		a.restoreHolders(toStop)
		_ = a.removeLease(claimant)
		return nil, fmt.Errorf("setting %s to nvidia/CDI mode for %q: %w", strings.Join(tokens, ", "), claimant, err)
	}

	return &Lease{arbiter: a, claimant: claimant, active: true}, nil
}

// ReleaseClaimant restores the holders a claimant's lease stopped and removes
// the lease. success=false applies the per-holder restore policy: `always`
// holders are restarted, `on-success` holders are left stopped for inspection.
func (a *ResourceArbiter) ReleaseClaimant(claimant string, success bool) error {
	// Serialize against concurrent acquires/releases (flip-back + ledger write) —
	// see acquireArbiterLock.
	unlock, lerr := a.acquireArbiterLock()
	if lerr != nil {
		return fmt.Errorf("acquiring resource-arbiter lock: %w", lerr)
	}
	defer func() { _ = unlock() }()

	ledger, err := a.loadLedger()
	if err != nil {
		return err
	}
	idx := -1
	for i, lz := range ledger.Leases {
		if lz.Claimant == claimant {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil // nothing to release
	}
	lease := ledger.Leases[idx]
	// State AFTER removing this lease — drives which preempted tokens are now
	// free (restore) vs still claimed by a survivor (carry forward), and the
	// post-release driver mode of each token.
	remaining := make([]preemptLease, 0, len(ledger.Leases)-1)
	remaining = append(remaining, ledger.Leases[:idx]...)
	remaining = append(remaining, ledger.Leases[idx+1:]...)

	// Apply the teardown side-effects (restore freed holders, carry the rest onto
	// a survivor, recompute each touched token's driver mode) — shared with
	// reconcileStranded so an owner release and a crashed-owner GC behave identically (R3).
	ok := a.releaseLeaseEffects(lease, remaining, success)
	if !ok {
		// Partial restore → keep the lease untouched so a later reconcile /
		// `charly preempt restore` retries; mode/ledger left unmutated. (on-success
		// holders intentionally left stopped are a deliberate end state, not a partial.)
		return fmt.Errorf("could not restore all holders for %q — lease retained; retry with `charly preempt restore %s`", claimant, claimant)
	}

	ledger.Leases = remaining
	return a.saveLedger(ledger)
}

// releaseLeaseEffects applies the side-effects of removing `lease` from a ledger
// whose post-removal state is `remaining`: restore the holders whose token is now
// FREE, carry forward (onto a survivor) the ones whose token is still claimed by a
// remaining lease — never restart a holder under an active claim — and recompute +
// apply each touched token's driver mode (nvidia while a shared claim remains,
// vfio under a surviving exclusive claim, vfio when fully free). Returns false
// (caller must RETAIN the lease) on a PARTIAL holder restore. Shared by
// ReleaseClaimant (owner/explicit release) and reconcileStranded (crashed-owner
// GC) so both honor identical restore + mode semantics (R3) — in particular a
// crashed shared-GPU bed flips the card back instead of leaving it nvidia.
func (a *ResourceArbiter) releaseLeaseEffects(lease preemptLease, remaining []preemptLease, success bool) bool {
	var toRestore, carry []preemptedHolder
	for _, ph := range lease.Preempted {
		if !success && ph.Restore == PreemptRestoreSuccess {
			fmt.Fprintf(os.Stderr,
				"preempt: leaving holder %q stopped (restore: on-success, claim failed) — `charly preempt restore %s` to bring it back\n",
				ph.Addr.Name, lease.Claimant)
			continue
		}
		if tokenClaimed(remaining, ph.Holds) {
			carry = append(carry, ph)
		} else {
			toRestore = append(toRestore, ph)
		}
	}
	if !a.restoreHolders(toRestore) {
		return false
	}
	if len(carry) > 0 {
		attachPreemptedToSurvivor(remaining, carry)
	}
	for _, tok := range lease.Tokens {
		mode := desiredModeForToken(remaining, tok)
		if err := a.applyMode([]string{tok}, mode); err != nil {
			fmt.Fprintf(os.Stderr, "preempt: could not set %q to %s mode after releasing %q: %v\n", tok, mode, lease.Claimant, err)
		}
	}
	return true
}

// reconcileStranded restores holders for any lease whose OWNER is gone (a crashed
// run — leaseLive false), recomputes the freed tokens' driver mode, then drops the
// fully-restored lease. Conservative: a lease whose owner is still alive is left
// untouched, so a still-building bed's lease is never garbage-collected out from
// under it by a CONCURRENT claimant's acquire (the concurrent-shared-GPU clobber
// bug — leaseLive keys liveness on the owner PROCESS for transient beds, not on a
// pod that does not exist until well after the lease is taken).
func (a *ResourceArbiter) reconcileStranded() error {
	ledger, err := a.loadLedger()
	if err != nil {
		return err
	}
	changed := false
	for i := 0; i < len(ledger.Leases); {
		lz := ledger.Leases[i]
		if a.leaseLive(lz) {
			i++
			continue
		}
		remaining := make([]preemptLease, 0, len(ledger.Leases)-1)
		remaining = append(remaining, ledger.Leases[:i]...)
		remaining = append(remaining, ledger.Leases[i+1:]...)
		ok := a.releaseLeaseEffects(lz, remaining, true)
		if !ok {
			i++ // partial restore → retain, retry on a later reconcile
			continue
		}
		fmt.Fprintf(os.Stderr, "preempt: reconciled stranded lease (claimant %q gone) — holders restored\n", lz.Claimant)
		ledger.Leases = remaining
		changed = true
	}
	if changed {
		return a.saveLedger(ledger)
	}
	return nil
}

// leaseLive reports whether a lease's owner is still alive — the inverse of
// "stranded". The owner differs by lease kind, so pod-running is NOT a universal
// signal. A TRANSIENT lease (an check bed) is owned by the long-lived `charly
// check run` orchestrator PROCESS, which exists from the acquire (the run's FIRST
// step) through teardown — i.e. for the whole multi-minute build phase BEFORE its
// pod is ever deployed; judging it by pod-running lets a concurrent claimant's
// reconcile garbage-collect a still-building bed's lease (the observed
// concurrent-shared-GPU clobber). A PERSISTENT lease (charly start / charly vm
// create) is owned by the DEPLOYMENT — its creator process exits right after
// bring-up — so deployment-running is the signal, OR-ed with owner-alive to cover
// the creator's own bring-up window before the deployment reports running.
func (a *ResourceArbiter) leaseLive(lz preemptLease) bool {
	if lz.Transient {
		return ownerAlive(lz.OwnerPID, lz.OwnerStart)
	}
	return a.running(lz.Claim) || ownerAlive(lz.OwnerPID, lz.OwnerStart)
}

// ownerAlive reports whether the process that created a lease (its OUTERMOST
// orchestrator/creator) is still running, guarding against PID REUSE by matching
// the recorded /proc start-time: a recycled PID belongs to a different process
// with a different start-time, so a crashed owner reads as gone and its lease is
// reconciled (crash-safety must not hinge on a PID happening not to be recycled).
// pid<=0 (a pre-upgrade lease with no owner recorded) reads as not-alive, so such
// a lease falls back to deployment-running / normal reconcile.
func ownerAlive(pid int, start string) bool {
	if pid <= 0 {
		return false
	}
	st, err := procStartTime(pid)
	if err != nil {
		return false // /proc/<pid> gone → process dead
	}
	return start == "" || st == start
}

// procStartTime returns a process's kernel start-time (field 22 of
// /proc/<pid>/stat, clock ticks since boot) — a per-process identity stable for
// the process's life and distinct across PID reuse. Linux-specific; mirrors the
// /proc-based liveness vmIsRunning already relies on.
func procStartTime(pid int) (string, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", err
	}
	// comm (field 2) is wrapped in parens and may itself contain spaces or ')',
	// so parse the fixed fields AFTER the last ')': field 3 (state) onward.
	s := string(data)
	rp := strings.LastIndexByte(s, ')')
	if rp < 0 || rp+2 > len(s) {
		return "", fmt.Errorf("malformed /proc/%d/stat", pid)
	}
	f := strings.Fields(s[rp+2:])
	const startIdx = 19 // field 22 overall = index 19 counting from field 3 (state)
	if len(f) <= startIdx {
		return "", fmt.Errorf("short /proc/%d/stat", pid)
	}
	return f[startIdx], nil
}

// selfProcStart is the current process's start-time, stamped onto a lease so a
// later reconcile running in ANOTHER charly process can tell a live owner from a
// reused PID. Best-effort: "" disables only the reuse cross-check, never liveness.
func selfProcStart() string {
	st, _ := procStartTime(os.Getpid())
	return st
}

// restoreHolders starts every holder that isn't currently running. Returns
// true iff all are running afterward. Idempotent — already-running holders are
// skipped (so a crash before the stop, or a double restore, is harmless).
func (a *ResourceArbiter) restoreHolders(holders []preemptedHolder) bool {
	allUp := true
	for _, ph := range holders {
		if a.running(ph.Addr) {
			continue
		}
		if err := a.start(ph.Addr); err != nil {
			fmt.Fprintf(os.Stderr, "preempt: could not restore holder %q: %v\n", ph.Addr.Name, err)
			allUp = false
			continue
		}
		fmt.Fprintf(os.Stderr, "preempt: restored holder %q\n", ph.Addr.Name)
	}
	return allUp
}

// waitStopped polls until the holder is no longer running (its resource is
// released), via the unified pollUntil primitive's StopGate mode (cap-only at
// the config StopGrace — replaces the old fixed 180s magic deadline). Returns
// true once stopped; false if it did not stop within StopGrace (the caller then
// force-stops). Returns immediately when the holder is already down (so the
// fake-backed unit tests never sleep).
func (a *ResourceArbiter) waitStopped(addr holderAddr) bool {
	cfg := loadedReadiness().StopGate("stop " + addr.Name)
	return pollUntil(context.Background(), cfg, func(context.Context) (bool, float64, error) {
		return !a.running(addr), 0, nil
	}) == nil
}

// removeLease drops a claimant's lease without restoring (rollback helper).
func (a *ResourceArbiter) removeLease(claimant string) error {
	ledger, err := a.loadLedger()
	if err != nil {
		return err
	}
	out := ledger.Leases[:0]
	for _, lz := range ledger.Leases {
		if lz.Claimant != claimant {
			out = append(out, lz)
		}
	}
	ledger.Leases = out
	return a.saveLedger(ledger)
}

// Status returns the current ledger plus the claimant names whose lease is
// stranded (its OWNER is gone — leaseLive false). Used by `charly preempt status`.
// Uses the SAME predicate as reconcileStranded so the display never disagrees with
// the garbage collector (a still-building bed shows active, not STRANDED).
func (a *ResourceArbiter) Status() (*preemptLedger, []string, error) {
	ledger, err := a.loadLedger()
	if err != nil {
		return nil, nil, err
	}
	var stranded []string
	for _, lz := range ledger.Leases {
		if !a.leaseLive(lz) {
			stranded = append(stranded, lz.Claimant)
		}
	}
	return ledger, stranded, nil
}

// --- production dependency wiring -----------------------------------------

// gatherDeployNodes returns every deploy node visible to the current
// invocation: the current project's deploy map (committed charly.yml, which
// includes folded kind:check beds) as the BASE, with the operator's per-host
// ~/.config/charly/charly.yml overlay merged ON TOP. Keyed by deploy name.
//
// The per-host overlay WINS on a name clash — it carries local-only deploy
// properties (above all `preemptible:`, a PER-HOST decision about whether THIS
// host's VM may be stopped to free an exclusive resource) that must override
// the committed profile, never be overwritten by it. The merge is per-field
// (MergeBundleNode), so a per-host `preemptible` AUGMENTS the committed node
// (keeping its target/vm/…) rather than the two clobbering each other. (The
// prior order let the project node wholesale-overwrite the per-host overlay, so
// a per-host preemptible silently never took effect for the arbiter.)
func gatherDeployNodes() map[string]BundleNode {
	out := map[string]BundleNode{}
	if uf, ok, err := LoadUnified("."); err == nil && ok && uf != nil {
		maps.Copy(out, uf.Bundle)
	}
	if dc := loadDeployConfigForRead("charly preempt"); dc != nil {
		for name, node := range dc.Bundle {
			out[name] = MergeBundleNode(out[name], node)
		}
	}
	return out
}

// gatherPreemptibleHolders is gatherDeployNodes filtered to the preemptible
// holders (the candidate set the arbiter may stop).
func gatherPreemptibleHolders() map[string]BundleNode {
	out := map[string]BundleNode{}
	for name, node := range gatherDeployNodes() {
		if node.IsPreemptible() {
			out[name] = node
		}
	}
	return out
}

// lookupVMClaimant finds a deploy/check node that targets the given kind:vm
// entity and declares requires_exclusive — the claimant on whose behalf a
// standalone `charly vm create/stop/destroy <entity>` should acquire/release an
// exclusive lease. Returns the deploy key (claimant name) + node; ok=false
// when no such node exists (then VM lifecycle does not touch the arbiter).
func lookupVMClaimant(vmEntity string) (string, BundleNode, bool) {
	for name, node := range gatherDeployNodes() {
		if node.Target == "vm" && node.From == vmEntity && len(node.RequiredExclusive()) > 0 {
			return name, node, true
		}
	}
	return "", BundleNode{}, false
}

func holderAddrFor(name string, node BundleNode) holderAddr {
	base, instance := parseDeployKey(name)
	target := node.Target
	if target == "" {
		target = "pod"
	}
	addr := holderAddr{Name: name, Target: target, Base: base, Instance: instance}
	if target == "vm" {
		addr.Vm = node.From
		if addr.Vm == "" {
			addr.Vm = base
		}
	}
	return addr
}

func holderRunning(addr holderAddr) bool {
	if addr.Target == "vm" {
		return vmIsRunning(vmName(addr.Vm, addr.Instance))
	}
	return podIsRunning(addr.Base, addr.Instance)
}

func holderStop(addr holderAddr) error {
	if addr.Target == "vm" {
		return stopVM(addr.Vm, addr.Instance, false)
	}
	return stopPodService(addr.Base, addr.Instance)
}

func holderStart(addr holderAddr) error {
	if addr.Target == "vm" {
		return startVM(addr.Vm, addr.Instance)
	}
	return startPodService(addr.Base, addr.Instance)
}

// vmIsRunning reports whether the named domain is running, mirroring the
// dual-backend probe `charly vm list` uses (libvirt domain state, then qemu
// pidfile liveness).
func vmIsRunning(name string) bool {
	if conn, err := connectLibvirt(""); err == nil {
		defer conn.Close() //nolint:errcheck
		if dom, lerr := conn.lookupDomain(name); lerr == nil {
			if st, serr := conn.domainState(dom); serr == nil {
				return domainStateString(st) == "running"
			}
		}
	}
	dir, err := vmDir()
	if err != nil {
		return false
	}
	data, err := os.ReadFile(filepath.Join(dir, name, "qemu.pid"))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// podIsRunning reports whether a pod deployment is up, via the quadlet service
// when one exists, else the container's runtime state.
func podIsRunning(base, instance string) bool {
	if active, _ := quadletExistsInstance(base, instance); active {
		svc := serviceNameInstance(base, instance)
		out, _ := exec.Command("systemctl", "--user", "is-active", svc).Output()
		return strings.TrimSpace(string(out)) == "active"
	}
	engine := "podman"
	if rt, err := ResolveRuntime(); err == nil {
		engine = EngineBinary(ResolveBoxEngineForDeploy(base, instance, rt.RunEngine))
	}
	name := containerNameInstance(base, instance)
	out, err := exec.Command(engine, "inspect", "--format", "{{.State.Running}}", name).CombinedOutput()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

// --- small set helpers -----------------------------------------------------

func dedupeNonEmpty(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// intersect returns the sorted set intersection of a and b.
func intersect(a, b []string) []string {
	set := map[string]bool{}
	for _, s := range a {
		set[s] = true
	}
	var out []string
	seen := map[string]bool{}
	for _, s := range b {
		if set[s] && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func sortedHolderKeys(m map[string]BundleNode) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// --- shared-claim + mode-arbitration helpers -------------------------------

// gatherResources loads the token -> ResourceDef map (the gpu selector that
// drives the vfio<->nvidia mode flip) the same way gatherDeployNodes loads
// deploy nodes — from the project charly.yml. nil when none / unreadable.
func gatherResources() map[string]*ResourceDef {
	if uf, ok, err := LoadUnified("."); err == nil && ok && uf != nil {
		return uf.Resource
	}
	return nil
}

// --- GPU-resource poisoning (device_lock wedge containment) ----------------
//
// When a GPU driver switch WEDGES the card's device_lock (errGPUSwitchWedged —
// nvidia `.remove` stuck), recovery is reboot-only and ANY further bind/unbind on
// the device would add a SECOND permanent D-state. poisonResource records the
// wedge keyed to the current boot, so every later acquire + reconcile REFUSES the
// token until the host reboots (a new boot_id makes the marker stale → cleared).
// This is the cascade-containment half of the device_lock fix (the prevention half
// is the modprobe -r gate in gpu_driver_switch.go).

func bootID() string {
	data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// poisonTokenFileName sanitizes a token into a safe poison-marker filename.
func poisonTokenFileName(token string) string {
	var b strings.Builder
	for _, r := range token {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return "poison-" + b.String() + ".id"
}

// poisonPath is the marker file for a token, in the arbiter's ledger dir — so it
// shares the production preemption dir (newResourceArbiter) AND stays hermetic in
// tests (the injected t.TempDir() ledgerPath).
func (a *ResourceArbiter) poisonPath(token string) string {
	return filepath.Join(filepath.Dir(a.ledgerPath), poisonTokenFileName(token))
}

// poisonResource marks a GPU token unusable until the next host reboot.
func (a *ResourceArbiter) poisonResource(token string) {
	bid := bootID()
	if bid == "" {
		return // no boot_id → cannot key the marker; the live wedge still errors per-call
	}
	p := a.poisonPath(token)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(p, []byte(bid+"\n"), 0o644)
	fmt.Fprintf(os.Stderr, "preempt: POISONED GPU resource %q — driver switch wedged the device_lock; a host reboot is required before it can be claimed again\n", token)
}

// resourcePoisoned reports whether a token is poisoned FOR THE CURRENT BOOT. A
// marker from a prior boot is stale (the reboot cleared the wedge) — it is removed
// and reads as not-poisoned.
func (a *ResourceArbiter) resourcePoisoned(token string) bool {
	p := a.poisonPath(token)
	data, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	if marked := strings.TrimSpace(string(data)); marked != "" && marked == bootID() {
		return true
	}
	_ = os.Remove(p) // stale (prior boot) → clear
	return false
}

// firstPoisonedToken returns the first gpu-backed token in `tokens` poisoned for
// the current boot, or "" when none is. Only gpu-backed tokens can wedge.
func (a *ResourceArbiter) firstPoisonedToken(tokens []string) string {
	resources := a.resources()
	for _, tok := range tokens {
		if rdef := resources[tok]; rdef != nil && rdef.Gpu != nil && a.resourcePoisoned(tok) {
			return tok
		}
	}
	return ""
}

// clearPoison removes a token's poison marker (used by `charly vm gpu recover`
// after verifying the card is healthy; a boot-id mismatch clears it implicitly).
func (a *ResourceArbiter) clearPoison(token string) {
	_ = os.Remove(a.poisonPath(token))
}

// tokenHeldByShared reports whether any existing SHARED lease holds a token in
// `tokens` (so a new shared claim is a refcount bump, not a first-claim flip).
func tokenHeldByShared(ledger *preemptLedger, tokens []string) bool {
	for _, lz := range ledger.Leases {
		if lz.Shared && len(intersect(lz.Tokens, tokens)) > 0 {
			return true
		}
	}
	return false
}

// tokenClaimed reports whether any lease still claims a token overlapping
// `toks` — used on release to choose restore-now vs carry-forward.
func tokenClaimed(leases []preemptLease, toks []string) bool {
	for _, lz := range leases {
		if len(intersect(lz.Tokens, toks)) > 0 {
			return true
		}
	}
	return false
}

// desiredModeForToken computes the driver MODE a token should be in given the
// active leases: vfio under any exclusive claim, nvidia while a shared claim
// remains, vfio (the boot default) when fully free.
func desiredModeForToken(leases []preemptLease, token string) string {
	hasShared := false
	for _, lz := range leases {
		if len(intersect(lz.Tokens, []string{token})) == 0 {
			continue
		}
		if lz.Shared {
			hasShared = true
		} else {
			return gpuModeVfio // an exclusive claim pins vfio
		}
	}
	if hasShared {
		return gpuModeNvidia
	}
	return gpuModeVfio
}

// attachPreemptedToSurvivor moves carried restore-obligations onto the first
// surviving lease overlapping each holder's token, so the LAST release of that
// token restores them (the operator-VM-restore obligation outlives any single
// shared claim). leases entries are structs, so the mutation persists when the
// caller saves the slice.
func attachPreemptedToSurvivor(leases []preemptLease, carry []preemptedHolder) {
	for _, ph := range carry {
		for i := range leases {
			if len(intersect(leases[i].Tokens, ph.Holds)) > 0 {
				leases[i].Preempted = dedupePreempted(append(leases[i].Preempted, ph))
				break
			}
		}
	}
}

// dedupePreempted removes duplicate preempted holders by deploy name (a holder
// stopped by two paths — e.g. the operator VM carried forward AND re-listed — is
// restored exactly once).
func dedupePreempted(in []preemptedHolder) []preemptedHolder {
	seen := map[string]bool{}
	var out []preemptedHolder
	for _, ph := range in {
		if ph.Addr.Name == "" || seen[ph.Addr.Name] {
			continue
		}
		seen[ph.Addr.Name] = true
		out = append(out, ph)
	}
	return out
}
