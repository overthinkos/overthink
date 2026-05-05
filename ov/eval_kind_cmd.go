package main

// `ov eval kind <kind>` — run the canonical R10 acceptance sequence
// (build → eval image → deploy add → eval live → fresh rebuild → tear
// down) against a per-kind disposable eval bed. Implements the seven
// disposable beds defined by the eval-bed cutover:
//
//	image   → eval-image-pod   (target: pod)    image: eval-image
//	layer   → eval-layer-pod   (target: pod)    image: eval-layer
//	pod     → eval-pod-pod     (target: pod)    image: eval-pod
//	vm      → arch-pacstrap-vm (target: vm)     [cloud_image; no image build]
//	k8s     → k3s-vm           (target: vm)     [cloud_image + layer; no image build]
//	local   → eval-local-deploy(target: local)  [kind:local; no image build]
//	deploy  → eval-deploy-pod  (target: pod)    image: eval-deploy
//
// Plus `all`, which runs all seven serially in declaration order.
//
// Each kind's run lands a per-step log in
//   .eval/kind/<kind>/<calver>/<step>.log
// and a summary YAML in the same directory.
//
// The dispatcher is a SHELL-style sequencer using exec.Command calls
// into the same `ov` binary the caller invoked. Each verb (build,
// eval image, deploy add, eval live, rebuild, remove) already
// performs its own validation, error reporting, and side effects;
// re-invoking via subprocess preserves their full behaviour without
// re-implementing it.

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// EvalKindCmd implements `ov eval kind <kind>`.
type EvalKindCmd struct {
	Kind      string `arg:"" enum:"image,layer,pod,vm,k8s,local,deploy,all" help:"Kind to eval (one of: image, layer, pod, vm, k8s, local, deploy, all)"`
	NoRebuild bool   `long:"no-rebuild" help:"Skip the fresh-rebuild re-verify step (R10 acceptance gate)"`
	Keep      bool   `long:"keep" help:"Don't tear down the bed after the run (leaves the disposable target running)"`
	Timeout   string `long:"timeout" default:"1200s" help:"Per-kind hard wall-clock cap"`
}

// bedSpec describes the disposable eval bed for a single kind.
//
//   - Bed     — the deployment/VM name fed to `ov deploy add` / `ov vm
//     create` (also the name `ov rebuild` / `ov remove` / `ov vm
//     destroy` operate on).
//   - Image   — the image to build via `ov image build`. Empty for
//     VM-source-from-cloud_image and kind:local beds (no image to
//     build; substrate comes from cloud_image fetch or local-only
//     templating).
//   - IsVM    — when true, step 3 uses `ov vm create` + `ov vm start`
//     and step 6 uses `ov vm destroy`; otherwise `ov deploy add` +
//     `ov remove` are used. Image build (step 1) and `ov eval image`
//     (step 2) are skipped when IsVM is true.
//   - IsLocal — when true, image build (step 1) and `ov eval image`
//     (step 2) are skipped (no image present). The deploy add path
//     drives the kind:local template via `ov deploy add`.
type bedSpec struct {
	Bed        string
	Image      string // image ref for pod beds; empty for VM/local
	LocalRef   string // kind:local template name; only set when IsLocal
	VmTemplate string // kind:vm entity name; only set when IsVM
	IsVM       bool
	IsLocal    bool
}

// bedTable maps the eight valid `kind` values to their disposable
// bed. Order is the canonical execution order for `kind: all`.
var bedTable = []struct {
	Kind string
	Spec bedSpec
}{
	{"image", bedSpec{Bed: "eval-image-pod", Image: "eval-image"}},
	{"layer", bedSpec{Bed: "eval-layer-pod", Image: "eval-layer"}},
	{"pod", bedSpec{Bed: "eval-pod-pod", Image: "eval-pod"}},
	{"vm", bedSpec{Bed: "arch-vm", VmTemplate: "arch", IsVM: true}},
	{"k8s", bedSpec{Bed: "k3s-vm", VmTemplate: "k3s-vm", IsVM: true}},
	{"local", bedSpec{Bed: "eval-local-deploy", LocalRef: "eval-local", IsLocal: true}},
	{"deploy", bedSpec{Bed: "eval-deploy-pod", Image: "eval-deploy"}},
}

// bedSpecFor returns the spec for a single non-"all" kind, or false
// if the kind is unknown.
func bedSpecFor(kind string) (bedSpec, bool) {
	for _, e := range bedTable {
		if e.Kind == kind {
			return e.Spec, true
		}
	}
	return bedSpec{}, false
}

// kindList returns the ordered kind list for `all`, or a single-entry
// slice for one kind.
func kindList(kind string) []string {
	if kind == "all" {
		out := make([]string, 0, len(bedTable))
		for _, e := range bedTable {
			out = append(out, e.Kind)
		}
		return out
	}
	return []string{kind}
}

// stepResult captures one step's outcome for the summary.yml.
type stepResult struct {
	Name     string
	Duration time.Duration
	OK       bool
}

// kindResult captures one kind's full run outcome.
type kindResult struct {
	Kind   string
	Bed    string
	CalVer string
	Steps  []stepResult
	OK     bool
}

// Run executes the per-kind R10 sequence (or all kinds for `all`).
func (c *EvalKindCmd) Run() error {
	exe, err := os.Executable()
	if err != nil {
		// Fall back to argv[0]; this still works for normal `ov`
		// invocations even when /proc isn't readable.
		exe = os.Args[0]
	}

	kinds := kindList(c.Kind)
	var failures []string
	for _, k := range kinds {
		spec, ok := bedSpecFor(k)
		if !ok {
			return fmt.Errorf("ov eval kind: unknown kind %q", k)
		}
		res, err := c.runOne(exe, k, spec)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", k, err))
		}
		if res != nil {
			fmt.Fprintf(os.Stderr, "ov eval kind %s: %s (steps=%d)\n",
				k, summaryStatus(res.OK), len(res.Steps))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("ov eval kind: %d failure(s):\n  - %s",
			len(failures), strings.Join(failures, "\n  - "))
	}
	return nil
}

// summaryStatus formats a bool as a human-readable status word.
func summaryStatus(ok bool) string {
	if ok {
		return "PASS"
	}
	return "FAIL"
}

// runOne executes the canonical R10 sequence for one kind and writes
// per-step logs + summary.yml to .eval/kind/<kind>/<calver>/. Returns
// the result struct (always non-nil) and the first error encountered
// (so the caller can decide whether to continue with other kinds).
func (c *EvalKindCmd) runOne(exe, kind string, spec bedSpec) (*kindResult, error) {
	calver := ComputeCalVer()
	logDir := filepath.Join(".eval", "kind", kind, calver)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating %s: %w", logDir, err)
	}
	res := &kindResult{Kind: kind, Bed: spec.Bed, CalVer: calver, OK: true}

	// step records a step's outcome and writes its log file. Returns
	// the run error so the caller can short-circuit on failure.
	step := func(name string, args []string) error {
		t0 := time.Now()
		out, runErr := runCapture(exe, args)
		dur := time.Since(t0)
		ok := runErr == nil
		res.Steps = append(res.Steps, stepResult{Name: name, Duration: dur, OK: ok})
		if !ok {
			res.OK = false
		}
		// Write the step log even on success — useful for debugging
		// non-fatal warnings.
		logPath := filepath.Join(logDir, name+".log")
		if writeErr := os.WriteFile(logPath, out, 0o644); writeErr != nil {
			fmt.Fprintf(os.Stderr, "ov eval kind %s: writing %s: %v\n", kind, logPath, writeErr)
		}
		return runErr
	}

	// Best-effort cleanup helper. Used both on the happy-path tear
	// down (step 6, suppressed by --keep) AND on the failure path so
	// the disposable bed doesn't linger after a partial run.
	cleanup := func() {
		if c.Keep {
			return
		}
		var args []string
		if spec.IsVM {
			args = []string{"vm", "destroy", spec.VmTemplate}
		} else {
			args = []string{"remove", spec.Bed}
		}
		_ = step("cleanup", args)
	}

	// Step 1: image build (skip for VM beds and kind:local beds).
	if !spec.IsVM && !spec.IsLocal && spec.Image != "" {
		if err := step("image-build", []string{"image", "build", spec.Image}); err != nil {
			c.writeSummary(logDir, res)
			cleanup()
			return res, fmt.Errorf("image build %s: %w", spec.Image, err)
		}
	}

	// Step 2: ov eval image <image> (skip for VM beds and kind:local
	// beds — no image to disposable-eval).
	if !spec.IsVM && !spec.IsLocal && spec.Image != "" {
		if err := step("eval-image", []string{"eval", "image", spec.Image}); err != nil {
			c.writeSummary(logDir, res)
			cleanup()
			return res, fmt.Errorf("eval image %s: %w", spec.Image, err)
		}
	}

	// Step 3: bring up the bed. VM beds use `ov vm create` + `ov vm
	// start` against the VM template name (which differs from the
	// deploy bed name — bed `arch-pacstrap-vm` references VM template
	// `arch-pacstrap`). Pod / local beds use `ov deploy add <bed>
	// <ref>`.
	if spec.IsVM {
		// VM beds need libvirt's user-session daemon for the eval
		// probes (`ov eval libvirt …`, `ov eval spice …`) to work AND
		// for the deploy.yml `backend: libvirt` resolver in
		// resolveVmBackend(). Best-effort start before any VM step;
		// downstream gate surfaces missing-daemon as a clear error.
		startLibvirtUserSession()
		// Best-effort destroy first to clear any lingering libvirt
		// domain or qemu process from a previous interrupted run
		// (the cleanup hook can fail to fire on hard exits). Then
		// build (idempotent) → create (auto-boots qemu) → deploy-add
		// (applies bed's add_layers: in-guest via SSH).
		_ = exec.Command(exe, "vm", "destroy", spec.VmTemplate).Run()
		if err := step("vm-build", []string{"vm", "build", spec.VmTemplate}); err != nil {
			c.writeSummary(logDir, res)
			cleanup()
			return res, fmt.Errorf("vm build %s: %w", spec.VmTemplate, err)
		}
		if err := step("vm-create", []string{"vm", "create", spec.VmTemplate}); err != nil {
			c.writeSummary(logDir, res)
			cleanup()
			return res, fmt.Errorf("vm create %s: %w", spec.VmTemplate, err)
		}
		// `ov vm create` auto-starts the libvirt domain after applying
		// snippet injections (no separate `ov vm start` needed — that
		// would error with "domain is already running"). However, the
		// in-guest sshd takes 30-90s on cold boot (cloud-init has to
		// install qemu-guest-agent + portaudio + run pacman-key
		// --populate). The subsequent deploy-add invokes WaitForSSH
		// against the managed `ov-<vm>` alias — poll until ssh
		// connects successfully so deploy-add starts at a known-ready
		// state. Best-effort: silent on timeout (deploy-add surfaces
		// the real error if VM is genuinely broken).
		waitForVmSshReady(spec.VmTemplate, 120*time.Second)
		if err := step("deploy-add", []string{"deploy", "add", spec.Bed, spec.VmTemplate}); err != nil {
			c.writeSummary(logDir, res)
			cleanup()
			return res, fmt.Errorf("deploy add %s: %w", spec.Bed, err)
		}
	} else {
		// Resolve the second `ov deploy add <bed> <ref>` argument:
		// pod beds → image name; kind:local beds → local template name.
		ref := spec.Image
		if spec.IsLocal {
			ref = spec.LocalRef
		}
		if err := step("deploy-add", []string{"deploy", "add", spec.Bed, ref}); err != nil {
			c.writeSummary(logDir, res)
			cleanup()
			return res, fmt.Errorf("deploy add %s: %w", spec.Bed, err)
		}
		// Pod beds: deploy add registers the entry but does not generate
		// the quadlet or start the service — `ov config <bed>` writes
		// the systemd unit, `ov start <bed>` activates it. kind:local
		// applies layers in place during deploy add, so neither step is
		// needed.
		if !spec.IsLocal {
			if err := step("config", []string{"config", spec.Bed}); err != nil {
				c.writeSummary(logDir, res)
				cleanup()
				return res, fmt.Errorf("config %s: %w", spec.Bed, err)
			}
			if err := step("start", []string{"start", spec.Bed}); err != nil {
				c.writeSummary(logDir, res)
				cleanup()
				return res, fmt.Errorf("start %s: %w", spec.Bed, err)
			}
			// `ov start` returns once systemd reports the service active,
			// but the container's services (supervisord → nc, etc.) may
			// not have finished binding ports yet. Poll until `podman
			// exec true` succeeds (cheap; usually <1s on cold start).
			waitForContainerReady(spec.Bed, 30*time.Second)
		}
	}

	// Step 4: full-stack live eval. The bed must be running before
	// this fires; preceding steps are responsible for ensuring that.
	// kind:local targets have no container/VM to exec against — their
	// install plan applies layers directly to the host filesystem
	// during deploy-add, and the rebuild step exercises tear-down +
	// re-apply. Skip eval-live for local; deploy-add + rebuild are
	// the canonical smoke for kind:local.
	if !spec.IsLocal {
		if err := step("eval-live", []string{"eval", "live", spec.Bed}); err != nil {
			c.writeSummary(logDir, res)
			cleanup()
			return res, fmt.Errorf("eval live %s: %w", spec.Bed, err)
		}
	}

	// Step 5: fresh-rebuild re-verify (the R10 acceptance gate).
	// Suppressed by --no-rebuild for fast smoke runs that exercise
	// the dispatcher itself without paying the full rebuild cost.
	if !c.NoRebuild {
		if err := step("rebuild", []string{"rebuild", spec.Bed}); err != nil {
			c.writeSummary(logDir, res)
			cleanup()
			return res, fmt.Errorf("rebuild %s: %w", spec.Bed, err)
		}
	}

	// Step 6: tear down the bed (suppressed by --keep). Errors here
	// are recorded but don't fail the overall run — the operator
	// already has the live-eval pass on record.
	cleanup()

	c.writeSummary(logDir, res)
	if !res.OK {
		return res, fmt.Errorf("kind %s: one or more steps failed", kind)
	}
	return res, nil
}

// runCapture runs the given ov subcommand, capturing combined
// stdout+stderr and returning the bytes plus the exec error.
func runCapture(exe string, args []string) ([]byte, error) {
	cmd := exec.Command(exe, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	cmd.Stdin = os.Stdin
	err := cmd.Run()
	return buf.Bytes(), err
}

// waitForVmSshReady polls the VM's managed ssh-config alias until ssh
// accepts a connection (or timeout). `ov vm create` returns when the
// libvirt domain is defined + first started, but a snippet-injection
// post-step stops + restarts the domain; the second start can take
// 5-30s on slow hosts. Without this poll, the dispatcher's deploy-add
// runs WaitForSSH which fast-fails when ssh can't even resolve the
// alias (race against the SSH config Include's re-prepend).
//
// vmName is the kind:vm entity name (e.g., "arch"); the SSH alias is
// "ov-" + vmName matching what publishVmSshAlias writes.
//
// Best-effort: silent on timeout. The downstream deploy-add surfaces
// the real error if the VM genuinely isn't accepting SSH.
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
			// One brief settle to give cloud-init a beat to finish
			// any first-boot package install before deploy-add fires
			// another pacman invocation.
			time.Sleep(2 * time.Second)
			return
		}
		time.Sleep(1 * time.Second)
	}
}

// waitForContainerReady polls until the container is exec-able AND its
// supervisord-managed services have had a beat to bind. `ov start` returns
// when systemd reports the service active, but supervisord + child
// programs may not have bound listening ports yet. Two-phase wait:
// (1) podman exec true succeeds, (2) brief supervisord-settle delay.
// Best-effort: silent on timeout (the next eval-live step surfaces the
// real failure with full context).
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
	// Supervisord-settle: programs with autostart=true bind a moment
	// after supervisord itself comes up. 1.5s is empirically enough on
	// dev hardware for nc/sleep services on fedora-minimal.
	time.Sleep(1500 * time.Millisecond)
}

// writeSummary emits a YAML summary alongside the per-step logs.
// Hand-rolls YAML to keep the file dependency-free (no yaml import
// for this single shape) and keep the output stable + diff-friendly.
func (c *EvalKindCmd) writeSummary(dir string, res *kindResult) {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "kind: %s\n", res.Kind)
	fmt.Fprintf(&buf, "bed: %s\n", res.Bed)
	fmt.Fprintf(&buf, "calver: %s\n", res.CalVer)
	fmt.Fprintln(&buf, "steps:")
	var total time.Duration
	for _, s := range res.Steps {
		fmt.Fprintf(&buf, "  - name: %s\n", s.Name)
		fmt.Fprintf(&buf, "    duration_seconds: %d\n", int(s.Duration.Round(time.Second)/time.Second))
		fmt.Fprintf(&buf, "    ok: %t\n", s.OK)
		total += s.Duration
	}
	fmt.Fprintf(&buf, "total_seconds: %d\n", int(total.Round(time.Second)/time.Second))
	fmt.Fprintf(&buf, "ok: %t\n", res.OK)

	path := filepath.Join(dir, "summary.yml")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "ov eval kind %s: writing %s: %v\n", res.Kind, path, err)
	}
}

// validKinds returns the sorted list of valid `kind` values
// (excluding "all"). Used by the test helper to validate the table
// covers every advertised kind.
func validKinds() []string {
	out := make([]string, 0, len(bedTable))
	for _, e := range bedTable {
		out = append(out, e.Kind)
	}
	sort.Strings(out)
	return out
}
