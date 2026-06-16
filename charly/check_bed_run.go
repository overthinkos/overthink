package main

// check_bed_run.go — the R10 acceptance-sequence engine for `kind: check`
// disposable test beds, driven by `charly check run <bed>` / `--all-beds`.
//
// A `kind: check` bed is a DeploymentNode (folded into the Deploy map with
// CheckBed=true by foldCheckBeds) describing a disposable target. runCheckBed
// drives the canonical sequence against it:
//
//	build → check box → deploy add → config → start → check live →
//	fresh update (R10 acceptance gate) → tear down
//
// Every parameter is read from the bed's DeploymentNode — there is NO
// hardcoded bed table. The target kind selects the bring-up/tear-down path:
//
//	target: pod   → charly box build + charly check box + charly deploy add +
//	                charly config + charly start + charly check live
//	target: vm    → charly vm build + charly vm create + charly deploy add + charly check
//	                live (image build / check box skipped — the substrate
//	                is a cloud_image, not an charly box)
//	target: local → charly deploy add only (kind:local applies candies in place;
//	                no container/VM to exec check live against)
//
// The dispatcher SHELLS OUT to the same `charly` binary the caller invoked, so
// each verb keeps its own validation, error reporting, and side effects —
// no probe/build/deploy logic is re-implemented here.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// bedRunOpts carries the per-run knobs (sourced from `charly check run` flags).
type bedRunOpts struct {
	Keep       bool   // don't tear the bed down after the run (--keep)
	NoRebuild  bool   // skip the fresh-update R10 re-verify step (--no-rebuild)
	CheckLevel string // the bed box's acceptance-depth rung (none|build|noagent|agent); gates how deep the run drives acceptance. Empty → DefaultCheckLevel.
}

// bedCheckLevel resolves the acceptance-depth rung for a bed from its box's
// authored check_level (none → DefaultCheckLevel). VM / local beds carry no box
// image, so they always run at the default rung.
func bedCheckLevel(uf *UnifiedFile, node DeploymentNode) string {
	if node.Box == "" {
		return DefaultCheckLevel
	}
	if bc, _, ok := uf.ProjectConfig().resolveBoxRef(node.Box); ok {
		return ResolveCheckLevel(bc.CheckLevel)
	}
	return DefaultCheckLevel
}

// stepResult captures one step's outcome for the summary.yml.
type stepResult struct {
	Name     string
	Duration time.Duration
	OK       bool
}

// bedRunResult captures one bed's full run outcome.
type bedRunResult struct {
	Bed    string
	CalVer string
	Step   []stepResult
	OK     bool
	// FailExitCode is the exit code of the FIRST failed step (0 = none).
	// CheckFailExitCode (2) means an check step (check-image/check-live)
	// reported failing checks; anything else is an infra failure (build /
	// deploy / vm-create). The caller maps it to the process exit code so
	// `charly check run <bed>` distinguishes "checks failed" from "couldn't run".
	FailExitCode int
}

// exitCodeOf extracts a subprocess exit code from an exec error. Non-ExitError
// failures (couldn't even spawn) map to 1; nil maps to 0.
func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		return ee.ExitCode()
	}
	return 1
}

// summaryStatus formats a bool as a human-readable status word.
func summaryStatus(ok bool) string {
	if ok {
		return "PASS"
	}
	return "FAIL"
}

// printDebugRetentionNotice tells the operator that a FAILED bed was left
// running for inspection, with the target-appropriate inspect + destroy
// commands. Pod/local beds tear down with `charly remove`; VM beds with
// `charly vm destroy`. The next `charly check run` best-effort clears the lingering
// target before rebuilding, so leaving it up never blocks a re-run.
func printDebugRetentionNotice(w io.Writer, name string, node DeploymentNode) {
	switch node.Target {
	case "vm":
		fmt.Fprintf(w, "\n[charly check run] bed %q FAILED — VM %q left running for debugging.\n"+
			"  inspect: charly check live %s | charly vm ssh %s\n"+
			"  destroy: charly vm destroy %s\n", name, node.Vm, name, node.Vm, node.Vm)
	case "local":
		fmt.Fprintf(w, "\n[charly check run] bed %q FAILED — local apply left in place for debugging.\n"+
			"  destroy: charly remove %s\n", name, name)
	default: // pod
		fmt.Fprintf(w, "\n[charly check run] bed %q FAILED — pod left running for debugging.\n"+
			"  inspect: charly check live %s | podman exec charly-%s sh\n"+
			"  destroy: charly remove %s\n", name, name, name, name)
	}
}

// Readiness-retry bounds for a pod/vm bed's check-live step. A fresh service may
// take time to become serviceable (Immich's first-run DB migration is the worst
// case observed) — stepReady polls check-live until it passes or the deadline.
const (
	bedCheckReadyDeadline = 6 * time.Minute
	bedCheckReadyInterval = 15 * time.Second
)

// persistBedDeployOverrides seeds the per-host charly.yml with a kind:check
// bed's project-declared deploy-shaped fields (port / volume / env / tunnel /
// security / network) plus its disposable/lifecycle classification, BEFORE the
// bed's `charly config` step runs. The folded bed node is the source of truth, but
// `charly deploy add` / `charly config` otherwise source those fields from the IMAGE
// LABELS and gate port writes behind an operator `-p` — so a bed's declared
// `port:` remap would never reach the quadlet (it would fall back to the image
// default and collide with any same-image deploy already bound to that port).
// Seeding the per-host entry up front lets the existing
// MergeDeployOntoMetadata → quadlet path honor the overrides with no new merge
// logic; `charly config`'s own SetPorts-gated save then leaves the seeded port
// untouched (it passes no `-p`). saveDeployState's per-field guards make
// unset bed fields no-ops, so this is safe for beds that declare only a subset.
func persistBedDeployOverrides(name string, node DeploymentNode) {
	saveDeployState(name, "", SaveDeployStateInput{
		Ports:         node.Port,
		SetPorts:      len(node.Port) > 0,
		Volume:        node.Volume,
		Env:           node.Env,
		CleanEnv:      true,
		Tunnel:        node.Tunnel,
		Security:      node.Security,
		Network:       node.Network,
		Box:           node.Box,
		Target:        node.Target,
		SetDisposable: true,
		Disposable:    node.IsDisposable(),
		SetLifecycle:  node.Lifecycle != "",
		Lifecycle:     node.Lifecycle,
	})
}

// runCheckBed executes the canonical R10 sequence for one `kind: check` bed
// and writes per-step logs + summary.yml to .check/<name>/<calver>/. Returns
// the result struct (always non-nil) and the first error encountered.
//
//nolint:gocyclo // canonical R10 bed sequence (build→check→deploy→check-live→update→teardown) woven from 6 interdependent inline closures (step/stepReady/fail/checkLiveTree/recoverVMIfDown/cleanup) over a shared mutable deployed flag + defer-bound preempt lease; contiguous-block extraction is not behavior-preserving
func runCheckBed(exe, name string, node DeploymentNode, opts bedRunOpts) (*bedRunResult, error) {
	isVM := node.Target == "vm"
	isLocal := node.Target == "local"
	image := node.Box
	vmTemplate := node.Vm
	localRef := node.Local

	// Acceptance-depth gating by the box's check_level rung (see check_level.go):
	//   none    → neither build- nor runtime-acceptance (build+deploy smoke only)
	//   build   → build-context acceptance (check box) only
	//   noagent → + deploy/runtime acceptance (check live + feature run --no-agent)
	//   agent   → + the prose-step agent grader (feature run WITHOUT --no-agent)
	// The default (noagent) preserves the prior full-sequence behavior.
	level := ResolveCheckLevel(opts.CheckLevel)
	runBuildCheck := CheckLevelReaches(level, CheckLevelBuild)
	runRuntimeCheck := CheckLevelReaches(level, CheckLevelNoAgent)
	runAgentGrader := CheckLevelReaches(level, CheckLevelAgent)
	featureRunArgs := func() []string {
		args := []string{"check", "feature", "run", name}
		if !runAgentGrader {
			args = append(args, "--no-agent")
		}
		return args
	}

	calver := ComputeCalVer()
	logDir := filepath.Join(".check", name, calver)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating %s: %w", logDir, err)
	}
	res := &bedRunResult{Bed: name, CalVer: calver, OK: true}

	// Local-candy resolution (the candy-ref analogue of --dev-local-pkg): a bed
	// in a box/<distro> submodule pulls its parent repo's shared candies via
	// `@github.com/<org>/<parent>/candy/...:<tag>` refs, which would otherwise
	// fetch the PINNED REMOTE candy — so the bed would test STALE code, not the
	// in-development tree (CLAUDE.md R10/RDD: a bed must verify code-under-
	// development). Auto-point those refs at the local superproject working tree
	// (the `:vTAG` is ignored) so EVERY bed tests the latest local candies. An
	// explicit operator CHARLY_REPO_OVERRIDE entry for the same repo still wins
	// (mergeRepoOverrides places it first). Scoped + restored so --all-beds and
	// the test suite don't leak the env.
	if pair := selfSuperprojectOverridePair("."); pair != "" {
		old, had := os.LookupEnv(RepoOverrideEnv)
		_ = os.Setenv(RepoOverrideEnv, mergeRepoOverrides(old, pair))
		defer func() {
			if had {
				_ = os.Setenv(RepoOverrideEnv, old)
			} else {
				_ = os.Unsetenv(RepoOverrideEnv)
			}
		}()
		fmt.Fprintf(os.Stderr, "charly check run %s: testing LOCAL candies (%s += %s)\n", name, RepoOverrideEnv, pair)
	}

	// Resource arbitration (the "preemptible" axis): if this bed claims a host
	// resource — EXCLUSIVE (requires_exclusive — sole use, e.g. a passthrough
	// GPU VM) or SHARED (requires_shared — refcounted, e.g. a GPU shared across
	// pods via CDI) — the arbiter frees/flips it BEFORE bring-up (stopping any
	// running preemptible holder; for shared, flipping the GPU to nvidia + CDI)
	// and restores it AFTER teardown. The lease is owned HERE (the outermost
	// orchestrator) and CHARLY_PREEMPT_LEASE suppresses the nested `charly vm create`/
	// `charly deploy add`/`charly vm destroy` subprocesses from touching it. The defer
	// guarantees restore on EVERY exit path (success, failure, early return);
	// crash-recovery beyond the defer is handled by the ledger + `charly preempt
	// restore`. See charly/preempt.go.
	lease, lerr := acquireResourceForClaimant(name, node, true)
	if lerr != nil {
		res.OK = false
		res.Step = append(res.Step, stepResult{Name: "preempt-acquire", OK: false})
		writeBedSummary(logDir, res)
		return res, fmt.Errorf("acquiring resources for %s: %w", name, lerr)
	}
	defer func() {
		if res.OK {
			_ = lease.Release()
		} else {
			_ = lease.ReleaseFailed()
		}
		if lease.active {
			_ = os.Unsetenv(envPreemptLeaseHeld)
		}
	}()

	// step records a step's outcome and writes its log file. Returns the
	// run error so the caller can short-circuit via fail().
	step := func(stepName string, args []string) error {
		t0 := time.Now()
		out, runErr := runCapture(exe, args)
		dur := time.Since(t0)
		ok := runErr == nil
		res.Step = append(res.Step, stepResult{Name: stepName, Duration: dur, OK: ok})
		if !ok {
			res.OK = false
			if res.FailExitCode == 0 {
				// First failure wins (fail() short-circuits the sequence);
				// capture the sub-charly exit code so the caller can tell an
				// check-check failure (2) from an infra failure (1).
				res.FailExitCode = exitCodeOf(runErr)
			}
		}
		// Write the step log even on success — useful for debugging
		// non-fatal warnings.
		logPath := filepath.Join(logDir, stepName+".log")
		if writeErr := os.WriteFile(logPath, out, 0o644); writeErr != nil {
			fmt.Fprintf(os.Stderr, "charly check run %s: writing %s: %v\n", name, logPath, writeErr)
		}
		return runErr
	}

	// stepReady runs a step with a bounded readiness retry: it re-runs the
	// command until it succeeds or the deadline elapses, recording only the
	// FINAL attempt. A service with slow first-run startup (e.g. a fresh Immich
	// running its one-shot DB migration before the API binds) is not ready when
	// the container is merely exec-able, so the deploy-scope check probes need a
	// readiness poll — the checks THEMSELVES are the readiness condition (a real
	// synchronization primitive, not a fixed sleep). Fast beds pass on the first
	// attempt (zero added latency); a genuinely-broken deploy still fails after
	// the deadline.
	stepReady := func(stepName string, args []string, deadline, interval time.Duration, beforeRetry func()) error {
		t0 := time.Now()
		end := t0.Add(deadline)
		var out []byte
		var runErr error
		for {
			out, runErr = runCapture(exe, args)
			if runErr == nil || time.Now().After(end) {
				break
			}
			if beforeRetry != nil {
				beforeRetry()
			}
			time.Sleep(interval)
		}
		dur := time.Since(t0)
		ok := runErr == nil
		res.Step = append(res.Step, stepResult{Name: stepName, Duration: dur, OK: ok})
		if !ok {
			res.OK = false
			if res.FailExitCode == 0 {
				res.FailExitCode = exitCodeOf(runErr)
			}
		}
		logPath := filepath.Join(logDir, stepName+".log")
		if writeErr := os.WriteFile(logPath, out, 0o644); writeErr != nil {
			fmt.Fprintf(os.Stderr, "charly check run %s: writing %s: %v\n", name, logPath, writeErr)
		}
		return runErr
	}

	// recoverVMIfDown is the check-live retry recovery hook for a disposable VM
	// bed: if the guest became unreachable mid-check (e.g. a rare host-side
	// QEMU/spice-server crash on a probe connect — see the 2026-05 RCA), restart
	// the domain and wait for sshd so the NEXT check-live retry runs against a
	// LIVE guest instead of pointlessly re-failing against a dead one. A
	// detect→restart→wait-ready recovery primitive, NOT a blind sleep-retry: it
	// no-ops when the guest still answers (the check-live failure is then a real
	// check failure to surface) and for non-VM / non-disposable beds.
	recoverVMIfDown := func() {
		if !isVM || !node.IsDisposable() {
			return
		}
		alias := "charly-" + vmTemplate
		probe := exec.Command("ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=3",
			"-o", "LogLevel=ERROR", alias, "true")
		if probe.Run() == nil {
			return // guest answers — not a dead VM
		}
		fmt.Fprintf(os.Stderr, "charly check run %s: VM bed %q unreachable mid-check — restarting disposable domain before retry\n", name, vmTemplate)
		_ = exec.Command(exe, "vm", "start", vmTemplate).Run()
		waitForVmSshReady(vmTemplate, 120*time.Second)
	}

	// cleanup tears the disposable bed down (suppressed by --keep). Used on
	// both the happy-path tear-down AND the failure path so the bed doesn't
	// linger after a partial run.
	cleanup := func() {
		if opts.Keep {
			return
		}
		if isVM {
			_ = step("cleanup", []string{"vm", "destroy", vmTemplate})
		} else {
			// --purge removes the bed's named volumes too. Safe because a
			// disposable bed's volumes are re-scoped to its own deploy key
			// (charly-<bed>-<vol>), never a production deploy's charly-<image>-<vol>.
			_ = step("cleanup", []string{"remove", name, "--purge"})
		}
		// Tear down any sibling peer deployments (companion driver pods) the
		// bed brought up alongside its root. Best-effort; never blocks teardown.
		tearDownPeers(&node)
	}

	// fail is the SINGLE failure tail shared by every step: record the
	// summary, wrap the error, and — crucially — LEAVE THE BED RUNNING so
	// the operator can debug the live target (the check-live failure is
	// already on record). Teardown on failure is deliberately suppressed:
	// the happy path (and --keep) still controls teardown via cleanup() at
	// the end, but a FAILED run preserves the target for inspection
	// (`charly check live <name>`, `podman exec charly-<name> …`, `charly check adb/appium
	// <name> …`). The next `charly check run` best-effort removes the lingering
	// bed before rebuilding (see the pre-run cleanup below), so kept-alive
	// state never blocks a re-run.
	// deployed flips true once the bed's target actually exists (after
	// deploy-add). The debug-retention notice is gated on it: a failure at
	// image-build / check-image (before any target is created) has nothing to
	// keep running, so it must NOT claim a pod was left up.
	deployed := false
	fail := func(format string, args ...any) (*bedRunResult, error) {
		writeBedSummary(logDir, res)
		if deployed {
			printDebugRetentionNotice(os.Stderr, name, node)
		}
		return res, fmt.Errorf(format, args...)
	}

	// Steps 1+2: image build + check box (pod beds only; VM substrate is a
	// cloud_image and kind:local has no image to build/disposable-check).
	if !isVM && !isLocal && image != "" {
		// Disposable check beds ALWAYS bake the IN-DEVELOPMENT charly toolchain
		// (any localpkg candy built from local source), never a stale published
		// release — so a bed tests the code under development. --dev-local-pkg is
		// the generic switch; production box builds omit it (download the release).
		if err := step("image-build", []string{"box", "build", image, "--dev-local-pkg"}); err != nil {
			return fail("image build %s: %w", image, err)
		}
		// Build-context acceptance — gated out at check_level: none.
		if runBuildCheck {
			if err := step("check-image", []string{"check", "box", image}); err != nil {
				return fail("check box %s: %w", image, err)
			}
		}
	}

	// Step 3: bring up the bed.
	if isVM {
		// VM beds need libvirt's user-session daemon for the check probes
		// (`charly check libvirt …`, `charly check spice …`) AND for the
		// `backend: libvirt` resolver. Best-effort start before any VM step;
		// the downstream gate surfaces a missing daemon as a clear error.
		startLibvirtUserSession()
		// Best-effort destroy first to clear any lingering libvirt domain
		// from a previous interrupted run, then build → create → deploy-add.
		_ = exec.Command(exe, "vm", "destroy", vmTemplate).Run()
		if err := step("vm-build", []string{"vm", "build", vmTemplate}); err != nil {
			return fail("vm build %s: %w", vmTemplate, err)
		}
		if err := step("vm-create", []string{"vm", "create", vmTemplate}); err != nil {
			return fail("vm create %s: %w", vmTemplate, err)
		}
		deployed = true // VM domain exists — keep it on any later failure
		// `charly vm create` auto-starts the domain, but in-guest sshd takes
		// 30-90s on cold boot; poll until ssh connects so deploy-add starts
		// at a known-ready state. Best-effort: silent on timeout.
		waitForVmSshReady(vmTemplate, 120*time.Second)
		// Deploy the VM node's own candies AND its nested target:pod children.
		// The VM target's Add applies the candies over SSH (incl. any kernel-driver
		// reboot), then deploys each nested pod as a PERSISTENT in-guest quadlet via
		// deployNestedPodsInGuest (build + cp-box into the guest + the guest's
		// own project-free `charly deploy from-box`). The dispatch routes a VM root
		// node-only (its pod children deploy in-guest, never via a host tree
		// walk), so no --node-only flag is needed and no separate image-transfer
		// step is required.
		if err := step("deploy-add", []string{"deploy", "add", name, vmTemplate}); err != nil {
			return fail("deploy add %s: %w", name, err)
		}
		// deployNestedPodsInGuest (inside the VM deploy-add above) brings up
		// nested target:pod children as in-guest quadlets, but it SKIPS
		// target:local children (they carry no image — they apply candies in
		// place). Deploy each nested local child via the dotted-path dispatch,
		// which applies the child's local-deploy candies into the guest over the
		// NestedExecutor (SSH). Without this, checkLiveTree below would check an
		// un-deployed child and fail. Mirrors the pod-bed nested-child loop.
		for _, childKey := range sortedNestedKeys(node.Nested) {
			child := node.Nested[childKey]
			if child == nil || (child.Target != "local" && child.Target != "host") {
				continue // pod children handled in-guest by deployNestedPodsInGuest
			}
			if err := step("deploy-"+childKey, []string{"deploy", "add", name + "." + childKey}); err != nil {
				return fail("deploy nested local child %s.%s: %w", name, childKey, err)
			}
		}
	} else {
		// Pod beds → image ref; kind:local beds → local template ref.
		ref := image
		if isLocal {
			ref = localRef
		}
		// Best-effort tear-down of any lingering bed from a previous
		// interrupted/failed run (symmetry with the VM path's pre-destroy
		// above). A failed run now LEAVES the bed up for debugging, so this
		// clears it before the fresh deploy — kept-alive state never blocks
		// a re-run. Silent on the common no-op case.
		// --purge clears any prior bed volumes so each deploy starts fresh
		// (a stale postgres volume would carry a stale password). Safe: a bed's
		// volumes are isolated under its own deploy key, never production's.
		_ = exec.Command(exe, "remove", name, "--purge").Run()
		// Clear any sibling peers left over from a previous interrupted run
		// (symmetry with the bed remove above) so kept-alive peer state never
		// blocks a fresh deploy.
		tearDownPeers(&node)
		// Seed the per-host charly.yml with the bed's project-declared
		// deploy-shaped overrides (port / volume / env / tunnel / security /
		// network) BEFORE charly config runs. The folded bed node is the source of
		// truth, but charly deploy add / charly config otherwise source those fields
		// from the IMAGE LABELS (and gate port writes behind an operator -p), so
		// a bed's declared port: remap would silently fall back to the image
		// default and collide with any same-image deploy already bound to it.
		persistBedDeployOverrides(name, node)
		// --node-only: deploy ONLY the bed's root node here. A pod bed's
		// container doesn't exist until `charly start` below, so any nested
		// children (e.g. a `target: android` device that installs apk:
		// packages onto the running emulator) can't deploy yet — they're
		// deployed after start (see the nested-child loop below). Harmless
		// for childless beds (the no-op is identical to a full walk).
		if err := step("deploy-add", []string{"deploy", "add", name, ref, "--node-only"}); err != nil {
			return fail("deploy add %s: %w", name, err)
		}
		deployed = true // target registered — keep it on any later failure
		// Pod beds: deploy add registers the entry but does not generate the
		// quadlet or start the service — `charly config` writes the unit,
		// `charly start` activates it. kind:local applies candies in place during
		// deploy add, so neither step is needed.
		if !isLocal {
			if err := step("config", []string{"config", name}); err != nil {
				return fail("config %s: %w", name, err)
			}
			if err := step("start", []string{"start", name}); err != nil {
				return fail("start %s: %w", name, err)
			}
			// `charly start` returns once systemd reports active, but the
			// container's services may not have bound ports yet. Poll until
			// `podman exec true` succeeds (cheap; usually <1s).
			waitForContainerReady(name)
			// Now the substrate is up: deploy any nested children onto it,
			// pre-order. The canonical case is a `target: android` device
			// child whose candies' apk: packages install onto the running
			// emulator (`charly deploy add <bed>.<child>` resolves the child
			// against the started pod's executor). Childless beds skip this.
			for _, childKey := range sortedNestedKeys(node.Nested) {
				if err := step("deploy-"+childKey, []string{"deploy", "add", name + "." + childKey}); err != nil {
					return fail("deploy nested child %s.%s: %w", name, childKey, err)
				}
			}
		}
	}

	// checkLiveTree runs `charly check live` against the bed's substrate AND every
	// nested child through the multi-hop NestedExecutor chain, so a nested
	// child's BAKED candy/box check (e.g. the selkies candy's frame-not-black
	// + encoder-active checks on a nested selkies-kde pod) is actually exercised
	// against its real venue. Without this, `charly check run` deploys nested
	// children (above) but never evaluates them — their coverage is silently
	// skipped, which is exactly why nested beds used to hand-roll guest-side
	// `podman exec <child>` probes. For a flat bed (no children) it is exactly
	// the prior parent-only check. stepLabel disambiguates initial vs rebuild.
	checkLiveTree := func(stepLabel string) error {
		for i, ref := range bedCheckLiveRefs(name, node.Nested) {
			label := stepLabel
			if i > 0 {
				label = stepLabel + "-" + ref[len(name)+1:] // childKey after "<name>."
			}
			if err := stepReady(label, []string{"check", "live", ref}, bedCheckReadyDeadline, bedCheckReadyInterval, recoverVMIfDown); err != nil {
				return err
			}
		}
		return nil
	}

	// Step 4: full-stack live check against the deployed target's venue —
	// container/VM via podman-exec/SSH, or the HOST filesystem (ShellExecutor)
	// for kind:local. A local bed's deploy-scope `check:` probes now run through
	// `charly check live <name>`'s local dispatch (runLocalCheck); the host is ready
	// the moment deploy-add returns, so stepReady passes on the first poll.
	//
	// Readiness retry: a fresh service may still be starting (e.g. Immich's
	// first-run DB migration runs minutes before the API binds). stepReady
	// polls check-live until it passes or the deadline, so we wait for real
	// readiness instead of racing a fixed sleep.
	// Bring up sibling peers (companion DRIVER deployments — e.g. a Chrome pod)
	// ALONGSIDE the substrate, ONCE, regardless of substrate kind (pod / vm /
	// local) — the subject's `on: <peer>` checks drive through them. Peers are
	// instruments, NEVER check-live'd (excluded from bedCheckLiveRefs). The SAME
	// bringUpPeers helper serves the operator deploy path (R3). One call, not
	// one per kind.
	// Deploy/runtime acceptance — gated out at check_level: none|build (the bed
	// then proves only that the image builds + deploys). Peers are instruments
	// for the runtime probes, so bring-up is gated with them.
	if runRuntimeCheck {
		if err := bringUpPeers(&node); err != nil {
			return fail("bring up peers for %s: %w", name, err)
		}
		if err := checkLiveTree("check-live"); err != nil {
			return fail("check live %s: %w", name, err)
		}

		// Step 4b: Agent Driven Evaluation acceptance — run the bed image's baked
		// `plan:` steps as acceptance tests. This is
		// the opt-in plan gate: a no-op PASS when the image bakes no plan steps,
		// real coverage when it does. At check_level: noagent it runs --no-agent
		// (deterministic, free); at check_level: agent the prose-step agent grader
		// runs too (featureRunArgs drops --no-agent). Pod beds only: VM/local
		// deployments carry no image-baked description label to run.
		if !isVM && !isLocal && image != "" {
			if err := step("feature-run", featureRunArgs()); err != nil {
				return fail("feature run %s: %w", name, err)
			}
		}
	}

	// Step 5: fresh-update re-verify (the R10 acceptance gate). Suppressed
	// by --no-rebuild for fast smoke that exercises the dispatcher only.
	if !opts.NoRebuild {
		if err := step("update", []string{"update", name}); err != nil {
			return fail("update %s: %w", name, err)
		}
		// For a nested bed, the fresh rebuild discards the substrate's
		// previously-deployed children (a rebuilt pod / VM guest is empty), so
		// the nested material MUST be re-applied and check-live re-run to
		// actually re-verify the new functionality on the rebuild — otherwise
		// the post-update state is unexercised. (A flat bed's `charly update`
		// succeeding is itself the rebuild proof; its baked deploy-scope check
		// needs no re-deploy.)
		if runRuntimeCheck && !isLocal && len(node.Nested) > 0 {
			if isVM {
				// `charly update` recreated the libvirt domain; the qcow2 disk (and
				// thus the applied guest candies, the nested pod's quadlet, and
				// its loaded image) persists across the recreate. The nested pod
				// is a PERSISTENT in-guest quadlet with lingering enabled, so it
				// auto-starts on the fresh boot — no re-assert needed. Just wait
				// for ssh; the rebuild check-live then PROVES the nested pod
				// survived the domain recreate (the Cutover 2 persistence gate).
				waitForVmSshReady(vmTemplate, 120*time.Second)
			} else {
				waitForContainerReady(name)
				for _, childKey := range sortedNestedKeys(node.Nested) {
					if err := step("redeploy-"+childKey, []string{"deploy", "add", name + "." + childKey}); err != nil {
						return fail("re-deploy nested child %s.%s (fresh rebuild): %w", name, childKey, err)
					}
				}
			}
			if err := checkLiveTree("check-live-rebuild"); err != nil {
				return fail("check live (fresh rebuild) %s: %w", name, err)
			}
		}
		// Re-run the bed image's baked plan steps on the fresh rebuild (pod
		// beds) — the ADE acceptance gate against the new image, at the box's
		// check_level depth. No-op pass when the image bakes no plan steps.
		if runRuntimeCheck && !isVM && !isLocal && image != "" {
			waitForContainerReady(name)
			if err := step("feature-run-rebuild", featureRunArgs()); err != nil {
				return fail("feature run (fresh rebuild) %s: %w", name, err)
			}
		}
	}

	// Step 6: tear down (suppressed by --keep). Errors are recorded but
	// don't fail the overall run — the live-check pass is already on record.
	cleanup()

	writeBedSummary(logDir, res)
	if !res.OK {
		return res, fmt.Errorf("bed %s: one or more steps failed", name)
	}
	return res, nil
}

// runCapture runs the given charly subcommand, capturing combined stdout+stderr
// and returning the bytes plus the exec error.
func runCapture(exe string, args []string) ([]byte, error) {
	cmd := exec.Command(exe, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	cmd.Stdin = os.Stdin
	err := cmd.Run()
	return buf.Bytes(), err
}

// waitForVmSshReady gates on the VM being SSH-reachable AND cloud-init having
// settled, using the SAME deterministic SSHExecutor preflight the VM check-live
// path (check_cmd.go) and VmDeployTarget.Emit run — NOT a fixed sleep. WaitForSSH
// polls until sshd answers; WaitForCloudInit retries until an ssh connection
// survives `cloud-init status --wait` (the deterministic cloud-init-settled
// signal — so deploy-add never races a still-running first-boot pacman). vmName
// is the kind:vm entity name. Best-effort: silent on timeout — the downstream
// deploy-add surfaces the real error.
func waitForVmSshReady(vmName string, timeout time.Duration) {
	gate := &SSHExecutor{Host: VmSshAlias(vmName), ConnectTimeout: 5}
	ctx := context.Background()
	if err := gate.WaitForSSH(ctx, int(timeout.Seconds())); err != nil {
		return
	}
	_ = gate.WaitForCloudInit(ctx)
}

// waitForContainerReady gates on the container being exec-able AND its
// supervisord-managed children having left their transitional states, so a
// one-shot check-live port/service probe never races a child that has not yet
// bound. `charly start` returns when systemd reports the service active, but
// supervisord's autostart children are still STARTING for a moment after. This
// polls `supervisorctl status` until no child is STARTING/BACKOFF (a child binds
// its port the instant it reaches RUNNING) instead of sleeping a fixed,
// host-tuned interval. Images without supervisord settle immediately. Best-effort:
// silent on timeout (the next check-live step surfaces the real failure).
func waitForContainerReady(bed string) {
	const readyTimeout = 30 * time.Second
	const readyPoll = 250 * time.Millisecond
	containerName := "charly-" + bed
	// supervisorStatus reports __NOSUP__ when the image has no supervisorctl, so
	// "no supervisord" is distinguishable from "socket not up yet".
	const supervisorStatus = `command -v supervisorctl >/dev/null 2>&1 || { echo __NOSUP__; exit 0; }; supervisorctl status 2>&1`
	deadline := time.Now().Add(readyTimeout)
	for time.Now().Before(deadline) {
		if exec.Command("podman", "exec", containerName, "true").Run() != nil {
			time.Sleep(readyPoll) // container not exec-able yet
			continue
		}
		out, _ := exec.Command("podman", "exec", containerName, "sh", "-c", supervisorStatus).CombinedOutput()
		if bytes.Contains(out, []byte("__NOSUP__")) {
			return // no supervisord — nothing to settle
		}
		if bytes.Contains(out, []byte("STARTING")) || bytes.Contains(out, []byte("BACKOFF")) {
			time.Sleep(readyPoll) // children still coming up
			continue
		}
		// Settle only once supervisord answered with real program-state lines
		// (guards the brief window before its control socket is up).
		if bytes.Contains(out, []byte("RUNNING")) || bytes.Contains(out, []byte("STOPPED")) ||
			bytes.Contains(out, []byte("EXITED")) || bytes.Contains(out, []byte("FATAL")) {
			return
		}
		time.Sleep(readyPoll) // supervisord socket not up yet
	}
}

// writeBedSummary emits a YAML summary alongside the per-step logs.
// Hand-rolled to keep the file dependency-free and diff-friendly.
func writeBedSummary(dir string, res *bedRunResult) {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "bed: %s\n", res.Bed)
	fmt.Fprintf(&buf, "calver: %s\n", res.CalVer)
	fmt.Fprintln(&buf, "steps:")
	var total time.Duration
	for _, s := range res.Step {
		fmt.Fprintf(&buf, "  - name: %s\n", s.Name)
		fmt.Fprintf(&buf, "    duration_seconds: %d\n", int(s.Duration.Round(time.Second)/time.Second))
		fmt.Fprintf(&buf, "    ok: %t\n", s.OK)
		total += s.Duration
	}
	fmt.Fprintf(&buf, "total_seconds: %d\n", int(total.Round(time.Second)/time.Second))
	fmt.Fprintf(&buf, "ok: %t\n", res.OK)

	path := filepath.Join(dir, "summary.yml")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "charly check run %s: writing %s: %v\n", res.Bed, path, err)
	}
}
