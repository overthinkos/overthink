package main

import (
	"errors"
	"fmt"
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
// /ov-internals:disposable). A preemptible HOLDER (a deploy carrying
// `preemptible: {holds: [...]}`) occupies named exclusive host-resource
// token(s) and MAY be gracefully stopped to free them for a CLAIMANT that
// declares `requires_exclusive: [...]`, then MUST be restarted afterward
// (disk + definition preserved). The arbiter coordinates this.
//
// Crash-safety is the load-bearing property: a crash must NEVER leave a
// holder permanently stopped. Two mechanisms guarantee it:
//
//  1. The lease ledger (~/.local/share/ov/preemption/leases.yml) is written
//     listing the holders to stop BEFORE any holder is stopped, and "restore"
//     means "start every listed holder that isn't currently running". So a
//     crash at any point — before, during, or after the stops — leaves a
//     recoverable record, and restoring is idempotent (already-running
//     holders are skipped).
//  2. reconcileStranded() (run at every AcquireExclusive, and via
//     `ov preempt restore`) restores any lease whose claimant is no longer
//     running. The normal path also restores via Lease.Release()/ReleaseFailed().

// holderAddr is the self-contained address of a deployment (holder or
// claimant) — enough to probe/stop/start it WITHOUT re-reading config, so a
// lease loaded after a crash can act on it.
type holderAddr struct {
	Name     string `yaml:"name"`               // full deploy key (for messages)
	Target   string `yaml:"target"`             // "vm" | "pod"
	Base     string `yaml:"base"`               // parseDeployKey base (pod container basis / vm fallback)
	Instance string `yaml:"instance,omitempty"` // parseDeployKey instance
	Vm       string `yaml:"vm,omitempty"`       // vm entity (target:vm)
}

// preemptedHolder records one holder a lease stopped, its declared exclusive
// tokens, and its restore policy — so ReleaseClaimant/reconcile restart
// exactly what was stopped.
type preemptedHolder struct {
	Addr    holderAddr `yaml:"addr"`
	Holds   []string   `yaml:"holds"`
	Restore string     `yaml:"restore"` // always | on-success
}

// preemptLease is one active exclusive claim.
type preemptLease struct {
	Claimant  string            `yaml:"claimant"`
	Claim     holderAddr        `yaml:"claim"` // probe whether the claimant is still alive (reconcile)
	Tokens    []string          `yaml:"tokens"`
	Transient bool              `yaml:"transient"` // eval-bed claims auto-release; persistent claims (vm create/start) don't
	Preempted []preemptedHolder `yaml:"preempted"`
	Created   string            `yaml:"created"` // RFC3339 UTC
}

type preemptLedger struct {
	Leases []preemptLease `yaml:"leases"`
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
	gather     func() map[string]DeploymentNode // candidate preemptible holders
	running    func(addr holderAddr) bool       // is this deployment running?
	stop       func(addr holderAddr) error      // graceful stop
	start      func(addr holderAddr) error      // start an already-configured deployment
	nowUTC     func() string
}

func newResourceArbiter() *ResourceArbiter {
	return &ResourceArbiter{
		ledgerPath: preemptLedgerPath(),
		gather:     gatherPreemptibleHolders,
		running:    holderRunning,
		stop:       holderStop,
		start:      holderStart,
		nowUTC:     func() string { return time.Now().UTC().Format(time.RFC3339) },
	}
}

// envPreemptLeaseHeld is set by the OUTERMOST claim-bringing `ov` invocation
// (runEvalBed, or a standalone `ov vm create`/`ov start`) so that the nested
// `ov` subprocesses it spawns (the bed's `ov vm create`/`ov deploy add`/
// `ov vm destroy`, etc.) do NOT independently acquire or release the lease —
// the owner manages it. An env channel, not config: it scopes to one process
// tree, mirroring the codebase's existing env-as-IPC idioms (OV_PROJECT_DIR,
// the nested-runtime keys).
const envPreemptLeaseHeld = "OV_PREEMPT_LEASE"

// acquireExclusiveForClaimant acquires (or reuses) an exclusive-resource lease
// for a claimant deploy that declares requires_exclusive — UNLESS an outer
// orchestrator already owns one (envPreemptLeaseHeld set), in which case the
// claim is already covered and this is a no-op. On a real acquire it marks the
// environment so nested `ov` subprocesses skip re-acquiring. Returns a lease
// whose Release()/ReleaseFailed() the caller must invoke (defer); a no-op
// lease is safe to Release. transient=true for eval-bed claims (auto-released
// at run end), false for persistent claims (ov vm create / ov start).
func acquireExclusiveForClaimant(claimant string, node DeploymentNode, transient bool) (*Lease, error) {
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

// releaseExclusiveForClaimant releases a persistent claimant's lease on
// teardown (ov vm stop/destroy, ov stop, ov remove). Best-effort, a no-op when
// the claimant holds no lease, and skipped when an outer orchestrator owns the
// lease (envPreemptLeaseHeld set — the owner will release it).
func releaseExclusiveForClaimant(claimant string) {
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
		return filepath.Join(os.TempDir(), "ov-preemption-leases.yml")
	}
	return filepath.Join(home, ".local", "share", "ov", "preemption", "leases.yml")
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

// AcquireExclusive frees the claimant's required exclusive tokens by stopping
// every running preemptible holder of them, persisting a crash-safe lease,
// and returning a handle whose Release()/ReleaseFailed() restarts them.
//
// transient=true marks an eval-bed-style claim (auto-released at run end);
// false marks a persistent claim (ov vm create / ov start) released only when
// the claimant itself is torn down.
//
// A claimant that requires nothing exclusive gets a no-op lease.
func (a *ResourceArbiter) AcquireExclusive(claimant string, claimantNode DeploymentNode, transient bool) (*Lease, error) {
	tokens := dedupeNonEmpty(claimantNode.RequiredExclusive())
	if len(tokens) == 0 {
		return &Lease{}, nil
	}

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

	// Mutual exclusion among claimants: refuse if another live claim already
	// holds an overlapping token (exclusive means one claimant at a time).
	for _, lz := range ledger.Leases {
		if shared := intersect(lz.Tokens, tokens); len(shared) > 0 {
			return nil, fmt.Errorf(
				"exclusive resource %s is already claimed by %q — release it (`ov preempt restore %s`) before claiming it for %q",
				strings.Join(shared, ", "), lz.Claimant, lz.Claimant, claimant)
		}
	}

	// Select the preemptible holders to preempt: those holding any requested
	// token that are CURRENTLY running (a holder the operator already left
	// stopped is not ours to start later).
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
			Restore: node.Preemptible.EffectiveRestore(),
		})
	}

	// Persist the lease FIRST (crash-safety): if we crash mid-stop, the
	// ledger already names the holders, and restore = "start any listed
	// holder that isn't running" recovers them.
	lease := preemptLease{
		Claimant:  claimant,
		Claim:     holderAddrFor(claimant, claimantNode),
		Tokens:    tokens,
		Transient: transient,
		Preempted: toStop,
		Created:   a.nowUTC(),
	}
	ledger.Leases = append(ledger.Leases, lease)
	if err := a.saveLedger(ledger); err != nil {
		return nil, err
	}

	// Now stop the holders. After each graceful stop, WAIT until the holder
	// actually reaches a stopped state before proceeding — a VM's `vm stop`
	// issues an ACPI shutdown and returns immediately, but the resource (e.g. a
	// VFIO GPU) isn't released until the domain powers off; the claim must not
	// race ahead and try to grab a still-held device. This is a readiness poll
	// (a real synchronization primitive), not a fixed sleep. On a stop failure
	// or timeout, roll back: restart what we stopped and drop the lease, so a
	// partial preemption never strands a holder or leaves a phantom lease.
	for i, ph := range toStop {
		fmt.Fprintf(os.Stderr, "preempt: stopping holder %q to free %s for %q\n",
			ph.Addr.Name, strings.Join(ph.Holds, ", "), claimant)
		stopErr := a.stop(ph.Addr)
		if stopErr == nil && !a.waitStopped(ph.Addr, holderStopTimeout) {
			stopErr = fmt.Errorf("holder did not reach a stopped state within %s (resource not freed)", holderStopTimeout)
		}
		if stopErr != nil {
			for _, done := range toStop[:i] {
				if !a.running(done.Addr) {
					_ = a.start(done.Addr)
				}
			}
			_ = a.removeLease(claimant)
			return nil, fmt.Errorf("preempting holder %q: %w", ph.Addr.Name, stopErr)
		}
		fmt.Fprintf(os.Stderr, "preempt: holder %q stopped — %s freed for %q\n",
			ph.Addr.Name, strings.Join(ph.Holds, ", "), claimant)
	}

	return &Lease{arbiter: a, claimant: claimant, active: true}, nil
}

// ReleaseClaimant restores the holders a claimant's lease stopped and removes
// the lease. success=false applies the per-holder restore policy: `always`
// holders are restarted, `on-success` holders are left stopped for inspection.
func (a *ResourceArbiter) ReleaseClaimant(claimant string, success bool) error {
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

	// Filter holders by restore policy for this outcome, then restart any
	// that aren't already running.
	var toRestore []preemptedHolder
	for _, ph := range lease.Preempted {
		if !success && ph.Restore == PreemptRestoreSuccess {
			fmt.Fprintf(os.Stderr,
				"preempt: leaving holder %q stopped (restore: on-success, claim failed) — `ov preempt restore %s` to bring it back\n",
				ph.Addr.Name, claimant)
			continue
		}
		toRestore = append(toRestore, ph)
	}
	allUp := a.restoreHolders(toRestore)

	// Remove the lease only when every policy-eligible holder is back up; a
	// partial restore keeps the lease so a later reconcile / `ov preempt
	// restore` retries. (on-success holders intentionally left stopped do not
	// block removal — they are a deliberate end state, recoverable manually.)
	if allUp {
		ledger.Leases = append(ledger.Leases[:idx], ledger.Leases[idx+1:]...)
	}
	if err := a.saveLedger(ledger); err != nil {
		return err
	}
	if !allUp {
		return fmt.Errorf("could not restore all holders for %q — lease retained; retry with `ov preempt restore %s`", claimant, claimant)
	}
	return nil
}

// reconcileStranded restores holders for any lease whose claimant is no longer
// running (a crashed run), then drops the fully-restored leases. Conservative:
// a lease whose claimant still appears to be running is left untouched so an
// active claim's holder is never restarted out from under it.
func (a *ResourceArbiter) reconcileStranded() error {
	ledger, err := a.loadLedger()
	if err != nil {
		return err
	}
	var kept []preemptLease
	for _, lz := range ledger.Leases {
		if a.running(lz.Claim) {
			kept = append(kept, lz)
			continue
		}
		if a.restoreHolders(lz.Preempted) {
			fmt.Fprintf(os.Stderr, "preempt: reconciled stranded lease (claimant %q gone) — holders restored\n", lz.Claimant)
			continue // fully restored → drop
		}
		kept = append(kept, lz) // partial restore → retry later
	}
	if len(kept) != len(ledger.Leases) {
		ledger.Leases = kept
		return a.saveLedger(ledger)
	}
	return nil
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

// holderStopTimeout / holderStopPoll bound the readiness poll after a graceful
// stop. A desktop VM ACPI shutdown is usually seconds; the generous ceiling
// covers a heavy guest while still failing a genuinely-stuck holder.
const (
	holderStopTimeout = 180 * time.Second
	holderStopPoll    = 2 * time.Second
)

// waitStopped polls until the holder is no longer running (its resource is
// released) or the timeout elapses. Returns true once stopped. A condition
// poll, not a fixed sleep — it returns immediately when the holder is already
// down (so the fake-backed unit tests never sleep).
func (a *ResourceArbiter) waitStopped(addr holderAddr, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if !a.running(addr) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(holderStopPoll)
	}
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
// stranded (claimant no longer running). Used by `ov preempt status`.
func (a *ResourceArbiter) Status() (*preemptLedger, []string, error) {
	ledger, err := a.loadLedger()
	if err != nil {
		return nil, nil, err
	}
	var stranded []string
	for _, lz := range ledger.Leases {
		if !a.running(lz.Claim) {
			stranded = append(stranded, lz.Claimant)
		}
	}
	return ledger, stranded, nil
}

// --- production dependency wiring -----------------------------------------

// gatherDeployNodes returns every deploy node visible to the current
// invocation: the current project's deploy map (committed overthink.yml, which
// includes folded kind:eval beds) as the BASE, with the operator's per-host
// ~/.config/ov/deploy.yml overlay merged ON TOP. Keyed by deploy name.
//
// The per-host overlay WINS on a name clash — it carries local-only deploy
// properties (above all `preemptible:`, a PER-HOST decision about whether THIS
// host's VM may be stopped to free an exclusive resource) that must override
// the committed profile, never be overwritten by it. The merge is per-field
// (MergeDeploymentNode), so a per-host `preemptible` AUGMENTS the committed node
// (keeping its target/vm/…) rather than the two clobbering each other. (The
// prior order let the project node wholesale-overwrite the per-host overlay, so
// a per-host preemptible silently never took effect for the arbiter.)
func gatherDeployNodes() map[string]DeploymentNode {
	out := map[string]DeploymentNode{}
	if uf, ok, err := LoadUnified("."); err == nil && ok && uf != nil {
		for name, node := range uf.Deploy {
			out[name] = node
		}
	}
	if dc := loadDeployConfigForRead("ov preempt"); dc != nil {
		for name, node := range dc.Deploy {
			out[name] = MergeDeploymentNode(out[name], node)
		}
	}
	return out
}

// gatherPreemptibleHolders is gatherDeployNodes filtered to the preemptible
// holders (the candidate set the arbiter may stop).
func gatherPreemptibleHolders() map[string]DeploymentNode {
	out := map[string]DeploymentNode{}
	for name, node := range gatherDeployNodes() {
		if node.IsPreemptible() {
			out[name] = node
		}
	}
	return out
}

// lookupVMClaimant finds a deploy/eval node that targets the given kind:vm
// entity and declares requires_exclusive — the claimant on whose behalf a
// standalone `ov vm create/stop/destroy <entity>` should acquire/release an
// exclusive lease. Returns the deploy key (claimant name) + node; ok=false
// when no such node exists (then VM lifecycle does not touch the arbiter).
func lookupVMClaimant(vmEntity string) (string, DeploymentNode, bool) {
	for name, node := range gatherDeployNodes() {
		if node.Target == "vm" && node.Vm == vmEntity && len(node.RequiredExclusive()) > 0 {
			return name, node, true
		}
	}
	return "", DeploymentNode{}, false
}

func holderAddrFor(name string, node DeploymentNode) holderAddr {
	base, instance := parseDeployKey(name)
	target := node.Target
	if target == "" {
		target = "pod"
	}
	addr := holderAddr{Name: name, Target: target, Base: base, Instance: instance}
	if target == "vm" {
		addr.Vm = node.Vm
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
// dual-backend probe `ov vm list` uses (libvirt domain state, then qemu
// pidfile liveness).
func vmIsRunning(name string) bool {
	if conn, err := connectLibvirt(""); err == nil {
		defer conn.Close()
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
		engine = EngineBinary(ResolveImageEngineForDeploy(base, instance, rt.RunEngine))
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

func sortedHolderKeys(m map[string]DeploymentNode) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
