package main

// eval_bed_run.go — the R10 acceptance-sequence engine for `kind: eval`
// disposable test beds, driven by `ov eval run <bed>` / `--all-beds`.
//
// A `kind: eval` bed is a DeploymentNode (folded into the Deploy map with
// EvalBed=true by foldEvalBeds) describing a disposable target. runEvalBed
// drives the canonical sequence against it:
//
//	build → eval image → deploy add → config → start → eval live →
//	fresh update (R10 acceptance gate) → tear down
//
// Every parameter is read from the bed's DeploymentNode — there is NO
// hardcoded bed table. The target kind selects the bring-up/tear-down path:
//
//	target: pod   → ov image build + ov eval image + ov deploy add +
//	                ov config + ov start + ov eval live
//	target: vm    → ov vm build + ov vm create + ov deploy add + ov eval
//	                live (image build / eval image skipped — the substrate
//	                is a cloud_image, not an ov image)
//	target: local → ov deploy add only (kind:local applies layers in place;
//	                no container/VM to exec eval live against)
//
// The dispatcher SHELLS OUT to the same `ov` binary the caller invoked, so
// each verb keeps its own validation, error reporting, and side effects —
// no probe/build/deploy logic is re-implemented here.

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// bedRunOpts carries the per-run knobs (sourced from `ov eval run` flags).
type bedRunOpts struct {
	Keep      bool // don't tear the bed down after the run (--keep)
	NoRebuild bool // skip the fresh-update R10 re-verify step (--no-rebuild)
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
	// EvalCheckFailExitCode (2) means an eval step (eval-image/eval-live)
	// reported failing checks; anything else is an infra failure (build /
	// deploy / vm-create). The caller maps it to the process exit code so
	// `ov eval run <bed>` distinguishes "checks failed" from "couldn't run".
	FailExitCode int
}

// exitCodeOf extracts a subprocess exit code from an exec error. Non-ExitError
// failures (couldn't even spawn) map to 1; nil maps to 0.
func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
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
// commands. Pod/local beds tear down with `ov remove`; VM beds with
// `ov vm destroy`. The next `ov eval run` best-effort clears the lingering
// target before rebuilding, so leaving it up never blocks a re-run.
func printDebugRetentionNotice(w io.Writer, name string, node DeploymentNode) {
	switch node.Target {
	case "vm":
		fmt.Fprintf(w, "\n[ov eval run] bed %q FAILED — VM %q left running for debugging.\n"+
			"  inspect: ov eval live %s | ov vm ssh %s\n"+
			"  destroy: ov vm destroy %s\n", name, node.Vm, name, node.Vm, node.Vm)
	case "local":
		fmt.Fprintf(w, "\n[ov eval run] bed %q FAILED — local apply left in place for debugging.\n"+
			"  destroy: ov remove %s\n", name, name)
	default: // pod
		fmt.Fprintf(w, "\n[ov eval run] bed %q FAILED — pod left running for debugging.\n"+
			"  inspect: ov eval live %s | podman exec ov-%s sh\n"+
			"  destroy: ov remove %s\n", name, name, name, name)
	}
}

// Readiness-retry bounds for a pod/vm bed's eval-live step. A fresh service may
// take time to become serviceable (Immich's first-run DB migration is the worst
// case observed) — stepReady polls eval-live until it passes or the deadline.
const (
	bedEvalReadyDeadline = 6 * time.Minute
	bedEvalReadyInterval = 15 * time.Second
)

// persistBedDeployOverrides seeds the per-host deploy.yml with a kind:eval
// bed's project-declared deploy-shaped fields (port / volume / env / tunnel /
// security / network) plus its disposable/lifecycle classification, BEFORE the
// bed's `ov config` step runs. The folded bed node is the source of truth, but
// `ov deploy add` / `ov config` otherwise source those fields from the IMAGE
// LABELS and gate port writes behind an operator `-p` — so a bed's declared
// `port:` remap would never reach the quadlet (it would fall back to the image
// default and collide with any same-image deploy already bound to that port).
// Seeding the per-host entry up front lets the existing
// MergeDeployOntoMetadata → quadlet path honor the overrides with no new merge
// logic; `ov config`'s own SetPorts-gated save then leaves the seeded port
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
		Image:         node.Image,
		Target:        node.Target,
		SetDisposable: true,
		Disposable:    node.Disposable,
		SetLifecycle:  node.Lifecycle != "",
		Lifecycle:     node.Lifecycle,
	})
}

// runEvalBed executes the canonical R10 sequence for one `kind: eval` bed
// and writes per-step logs + summary.yml to .eval/<name>/<calver>/. Returns
// the result struct (always non-nil) and the first error encountered.
func runEvalBed(exe, name string, node DeploymentNode, opts bedRunOpts) (*bedRunResult, error) {
	isVM := node.Target == "vm"
	isLocal := node.Target == "local"
	image := node.Image
	vmTemplate := node.Vm
	localRef := node.Local

	calver := ComputeCalVer()
	logDir := filepath.Join(".eval", name, calver)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating %s: %w", logDir, err)
	}
	res := &bedRunResult{Bed: name, CalVer: calver, OK: true}

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
				// capture the sub-ov exit code so the caller can tell an
				// eval-check failure (2) from an infra failure (1).
				res.FailExitCode = exitCodeOf(runErr)
			}
		}
		// Write the step log even on success — useful for debugging
		// non-fatal warnings.
		logPath := filepath.Join(logDir, stepName+".log")
		if writeErr := os.WriteFile(logPath, out, 0o644); writeErr != nil {
			fmt.Fprintf(os.Stderr, "ov eval run %s: writing %s: %v\n", name, logPath, writeErr)
		}
		return runErr
	}

	// stepReady runs a step with a bounded readiness retry: it re-runs the
	// command until it succeeds or the deadline elapses, recording only the
	// FINAL attempt. A service with slow first-run startup (e.g. a fresh Immich
	// running its one-shot DB migration before the API binds) is not ready when
	// the container is merely exec-able, so the deploy-scope eval probes need a
	// readiness poll — the checks THEMSELVES are the readiness condition (a real
	// synchronization primitive, not a fixed sleep). Fast beds pass on the first
	// attempt (zero added latency); a genuinely-broken deploy still fails after
	// the deadline.
	stepReady := func(stepName string, args []string, deadline, interval time.Duration) error {
		t0 := time.Now()
		end := t0.Add(deadline)
		var out []byte
		var runErr error
		for {
			out, runErr = runCapture(exe, args)
			if runErr == nil || time.Now().After(end) {
				break
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
			fmt.Fprintf(os.Stderr, "ov eval run %s: writing %s: %v\n", name, logPath, writeErr)
		}
		return runErr
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
			// (ov-<bed>-<vol>), never a production deploy's ov-<image>-<vol>.
			_ = step("cleanup", []string{"remove", name, "--purge"})
		}
	}

	// fail is the SINGLE failure tail shared by every step: record the
	// summary, wrap the error, and — crucially — LEAVE THE BED RUNNING so
	// the operator can debug the live target (the eval-live failure is
	// already on record). Teardown on failure is deliberately suppressed:
	// the happy path (and --keep) still controls teardown via cleanup() at
	// the end, but a FAILED run preserves the target for inspection
	// (`ov eval live <name>`, `podman exec ov-<name> …`, `ov eval adb/appium
	// <name> …`). The next `ov eval run` best-effort removes the lingering
	// bed before rebuilding (see the pre-run cleanup below), so kept-alive
	// state never blocks a re-run.
	// deployed flips true once the bed's target actually exists (after
	// deploy-add). The debug-retention notice is gated on it: a failure at
	// image-build / eval-image (before any target is created) has nothing to
	// keep running, so it must NOT claim a pod was left up.
	deployed := false
	fail := func(format string, args ...any) (*bedRunResult, error) {
		writeBedSummary(logDir, res)
		if deployed {
			printDebugRetentionNotice(os.Stderr, name, node)
		}
		return res, fmt.Errorf(format, args...)
	}

	// Steps 1+2: image build + eval image (pod beds only; VM substrate is a
	// cloud_image and kind:local has no image to build/disposable-eval).
	if !isVM && !isLocal && image != "" {
		if err := step("image-build", []string{"image", "build", image}); err != nil {
			return fail("image build %s: %w", image, err)
		}
		if err := step("eval-image", []string{"eval", "image", image}); err != nil {
			return fail("eval image %s: %w", image, err)
		}
	}

	// Step 3: bring up the bed.
	if isVM {
		// VM beds need libvirt's user-session daemon for the eval probes
		// (`ov eval libvirt …`, `ov eval spice …`) AND for the
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
		// `ov vm create` auto-starts the domain, but in-guest sshd takes
		// 30-90s on cold boot; poll until ssh connects so deploy-add starts
		// at a known-ready state. Best-effort: silent on timeout.
		waitForVmSshReady(vmTemplate, 120*time.Second)
		if err := step("deploy-add", []string{"deploy", "add", name, vmTemplate}); err != nil {
			return fail("deploy add %s: %w", name, err)
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
		// Seed the per-host deploy.yml with the bed's project-declared
		// deploy-shaped overrides (port / volume / env / tunnel / security /
		// network) BEFORE ov config runs. The folded bed node is the source of
		// truth, but ov deploy add / ov config otherwise source those fields
		// from the IMAGE LABELS (and gate port writes behind an operator -p), so
		// a bed's declared port: remap would silently fall back to the image
		// default and collide with any same-image deploy already bound to it.
		persistBedDeployOverrides(name, node)
		if err := step("deploy-add", []string{"deploy", "add", name, ref}); err != nil {
			return fail("deploy add %s: %w", name, err)
		}
		deployed = true // target registered — keep it on any later failure
		// Pod beds: deploy add registers the entry but does not generate the
		// quadlet or start the service — `ov config` writes the unit,
		// `ov start` activates it. kind:local applies layers in place during
		// deploy add, so neither step is needed.
		if !isLocal {
			if err := step("config", []string{"config", name}); err != nil {
				return fail("config %s: %w", name, err)
			}
			if err := step("start", []string{"start", name}); err != nil {
				return fail("start %s: %w", name, err)
			}
			// `ov start` returns once systemd reports active, but the
			// container's services may not have bound ports yet. Poll until
			// `podman exec true` succeeds (cheap; usually <1s).
			waitForContainerReady(name, 30*time.Second)
		}
	}

	// Step 4: full-stack live eval. kind:local has no container/VM to exec
	// against — its layers apply to the host filesystem during deploy-add,
	// and the update step exercises tear-down + re-apply.
	if !isLocal {
		// Readiness retry: a fresh service may still be starting (e.g. Immich's
		// first-run DB migration runs minutes before the API binds). stepReady
		// polls eval-live until it passes or the deadline, so we wait for real
		// readiness instead of racing a fixed sleep.
		if err := stepReady("eval-live", []string{"eval", "live", name}, bedEvalReadyDeadline, bedEvalReadyInterval); err != nil {
			return fail("eval live %s: %w", name, err)
		}
	}

	// Step 5: fresh-update re-verify (the R10 acceptance gate). Suppressed
	// by --no-rebuild for fast smoke that exercises the dispatcher only.
	if !opts.NoRebuild {
		if err := step("update", []string{"update", name}); err != nil {
			return fail("update %s: %w", name, err)
		}
	}

	// Step 6: tear down (suppressed by --keep). Errors are recorded but
	// don't fail the overall run — the live-eval pass is already on record.
	cleanup()

	writeBedSummary(logDir, res)
	if !res.OK {
		return res, fmt.Errorf("bed %s: one or more steps failed", name)
	}
	return res, nil
}

// runCapture runs the given ov subcommand, capturing combined stdout+stderr
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

// waitForVmSshReady polls the VM's managed ssh-config alias until ssh accepts
// a connection (or timeout). `ov vm create` returns when the domain is first
// started, but a snippet-injection post-step can stop+restart it; the second
// start can take 5-30s on slow hosts. vmName is the kind:vm entity name; the
// SSH alias is "ov-" + vmName (matching publishVmSshAlias). Best-effort:
// silent on timeout — the downstream deploy-add surfaces the real error.
func waitForVmSshReady(vmName string, timeout time.Duration) {
	alias := "ov-" + vmName
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cmd := exec.Command("ssh",
			"-o", "BatchMode=yes",
			"-o", "ConnectTimeout=2",
			"-o", "LogLevel=ERROR",
			alias, "true")
		if err := cmd.Run(); err == nil {
			// Brief settle so cloud-init can finish any first-boot package
			// install before deploy-add fires another pacman invocation.
			time.Sleep(2 * time.Second)
			return
		}
		time.Sleep(1 * time.Second)
	}
}

// waitForContainerReady polls until the container is exec-able, then waits a
// beat for supervisord-managed services to bind. `ov start` returns when
// systemd reports the service active, but supervisord + child programs may
// not have bound listening ports yet. Best-effort: silent on timeout (the
// next eval-live step surfaces the real failure with full context).
func waitForContainerReady(bed string, timeout time.Duration) {
	containerName := "ov-" + bed
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cmd := exec.Command("podman", "exec", containerName, "true")
		if err := cmd.Run(); err == nil {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	// Supervisord-settle: programs with autostart=true bind a moment after
	// supervisord itself comes up. 1.5s is empirically enough on dev
	// hardware for nc/sleep services on fedora-minimal.
	time.Sleep(1500 * time.Millisecond)
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
		fmt.Fprintf(os.Stderr, "ov eval run %s: writing %s: %v\n", res.Bed, path, err)
	}
}
