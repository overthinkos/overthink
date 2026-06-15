package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// CheckStatus is the outcome of a single Check.
type CheckStatus int

const (
	TestPass CheckStatus = iota
	TestFail
	TestSkip
)

func (s CheckStatus) String() string {
	switch s {
	case TestPass:
		return "pass"
	case TestFail:
		return "fail"
	case TestSkip:
		return "skip"
	}
	return "unknown"
}

// CheckResult captures the outcome of running a single Check.
//
// Attempts and TotalElapsed are populated only when the check had an
// `eventually:` modifier (retry loop). Attempts=1 + TotalElapsed==Elapsed
// for checks that ran exactly once. Reporters surface these when Attempts>1
// so slow startup paths are visible ("PASS in 5 attempts over 12.3s").
type CheckResult struct {
	Op           *Op
	Verb         string
	Status       CheckStatus
	Message      string
	Elapsed      time.Duration
	Attempts     int           `json:"attempts,omitempty"`
	TotalElapsed time.Duration `json:"total_elapsed,omitempty"`

	// CapturedValue is the value stashed under `capture:` for consumption
	// by downstream steps in the same plan run. Empty when Capture was
	// unset or the check did not pass (captures are recorded only on
	// final PASS — failing `eventually:` attempts don't pollute).
	CapturedValue string `json:"captured_value,omitempty"`
}

// RunMode selects routing rules for a Run() invocation.
//
//   - RunModeLive: charly check live — against a running container. In-container
//     probes via Exec; host-side verbs (http/dns/addr) from the charly process.
//   - RunModeBox: charly check box — against a disposable container
//     (podman run --rm). All probes via Exec; host-side reachability is
//     not meaningful and those checks are skipped.
type RunMode int

const (
	RunModeLive RunMode = iota
	RunModeBox
)

// Executor + ContainerExecutor + ImageExecutor + VmTestExecutor were
// deleted in the 2026-04 executor-hierarchy cutover. The runner now
// uses DeployExecutor (deploy_executor.go) directly — chains for
// nested topologies (host → ssh-vm → podman-exec-pod → podman-exec-
// nested-pod) come from ResolveDeployChain (deploy_chain.go). Every
// former call site of `r.Exec.RunCapture(ctx, cmd)` became
// `r.Exec.RunCapture(ctx, cmd)` with identical (stdout, stderr, exit,
// err) return semantics.
//
// The `runCapture(cmd *exec.Cmd)` helper that used to live here moved
// to deploy_executor.go as `runCaptureCmd` so every DeployExecutor
// implementation can share it. asExitError moved alongside as
// asExitErrorDeploy. Both are package-private and used by
// ShellExecutor.RunCapture / SSHExecutor.RunCapture.

// Runner wires together the execution context for one pass of checks.
//
// Image and Instance are the user-supplied names under RunModeLive, used to
// build CLI invocations for the cdp/wl/dbus/vnc verbs (testrun_ov_verbs.go).
// They are empty under RunModeBox, which causes those verbs to skip
// with a clear message — they need a running container with port mappings.
type Runner struct {
	Exec        DeployExecutor
	Resolver    *CheckVarResolver
	Mode        RunMode
	HTTPClient  *http.Client
	DialTimeout time.Duration
	Box         string
	Instance    string
	// Distros is the image's distro tag list (e.g. ["fedora:43", "fedora"]
	// or ["arch"]). Used by the `package:` verb's PackageMap resolution
	// to pick a distro-specific package name when names diverge across
	// distros (e.g. openssh-server on Fedora vs openssh on Arch).
	Distros []string

	// CandyDirs maps a candy name → its resolved source directory. Used to
	// anchor a relative committed-APK path in an `adb: install` / `appium:
	// install-app` check (apk: ./tests/data/...) against the authoring candy's
	// source tree, so a check resolves the same way whether the candy is local
	// or fetched via @github (mirrors the deploy resolveApkPath, R3). Best-effort:
	// empty when the project context isn't available; the apk path then stays
	// cwd-relative (existing behaviour, no regression).
	CandyDirs map[string]string

	// VerifyOnly, when true, restricts a RunPlan walk to the idempotent
	// verification steps (check:/agent-check:) and SKIPS mutating steps
	// (run:/agent-run:). This is the `charly check live` / `charly check box`
	// mode — verify a running/disposable target without re-provisioning.
	// False (the default) runs every step in order (provision-and-verify).
	VerifyOnly bool

	// Scenario carries the per-run capture/var context when the runner is
	// driving a plan: (from description_run.go). Nil under classical bare-Op
	// runs — captures/${STEP_ID}/etc. stay absent and behaviour is unchanged.
	Scenario *ScenarioContext

	// Grader, when set, judges an agent step (agent-run:/agent-check:) instead
	// of the default skip/--strict-fail. The agent grader
	// (check_feature_grader.go) spawns the configured kind:agent CLI to probe
	// the live target and return a pass/fail verdict with evidence. Set only by
	// `charly check feature run <deployment>` against a running deployment the
	// agent can reach; nil elsewhere (agent steps then advisory-skip).
	Grader StepGrader

	// TargetResolver, when set, is called to obtain a (resolver, exec)
	// pair for a given `on:` target name. Enables multi-target plan runs
	// (the `on:` step modifier). Classical `tests:` runs leave this nil
	// and use the Runner's static Resolver+Exec pair throughout.
	//
	// The caller (usually description_run.go) decides the lookup policy
	// — typically a map of deployment/image names to pre-initialized
	// executors. Returning (nil, nil, nil) means "unknown target"; the
	// runner then reports the step as FAIL with a clear message.
	TargetResolver func(target string) (*CheckVarResolver, DeployExecutor, error)

	// ValidateAiArtifacts, when true, narrows artifact-producing
	// state-dependent probes (the screenshot + record-stop methods in
	// artifactValidatableMethods) to validate the AI's iteration
	// artifact instead of re-running the capture. See HarnessScore's
	// field of the same name for the design rationale. Always false in
	// `charly check self-evaluate` invocations, regardless of score
	// config — self-evaluate's job is to actually produce the
	// artifacts that the harness scorer then validates.
	ValidateAiArtifacts bool

	// IterStartTime is the freshness floor for AI-artifact mtime
	// checks. The harness scorer populates this with the BENCHMARK
	// start time (not per-iter start) so artifacts produced
	// legitimately in earlier phases survive scoring across phase
	// boundaries — a record/stop cast file written in phase 6
	// remains valid in phase 7 + 8 scoring. Zero value = no
	// freshness check (validators only see file existence +
	// content). Non-zero = artifact mtime MUST be ≥ this time or
	// the probe fails with the stale-artifact anti-deception error.
	// The field name is historical; semantically the floor is the
	// run/benchmark start.
	IterStartTime time.Time

	// PeerVars carries pre-resolved cross-deployment address variables —
	// ${PEER_HOST:name} and ${PEER_ENDPOINT:name:port} (check_peer.go) — that
	// let a driven probe (a check with `on: <driver>`) TARGET a SEPARATE
	// SUBJECT deployment over the shared `charly` network or the host. Overlaid by
	// effectiveEnv onto WHATEVER resolver is active (primary, on:-swapped, or a
	// harness bucket), so cross-deployment addressing is identical across
	// `charly check live`, kind:check beds, and AI-iteration runs. Nil under classical runs
	// with no ${PEER_*} refs (no overlay, behaviour unchanged).
	PeerVars map[string]string
	// peerCleanups tears down anything opened while resolving PeerVars (an
	// ssh -L forward for a ${PEER_ENDPOINT} VM/host subject). Run via
	// ClosePeers() — the check command defers it at run end.
	peerCleanups []func()
}

// ClosePeers tears down any resources opened while resolving ${PEER_*} address
// variables (ssh -L forwards). Safe to call when none were opened.
func (r *Runner) ClosePeers() {
	for _, c := range r.peerCleanups {
		if c != nil {
			c()
		}
	}
	r.peerCleanups = nil
}

// RunLive runs `checks` as a LIVE cross-deployment check. It is the SINGLE entry
// point every host-context live-check path (a pod / VM / local SUBJECT) shares,
// so cross-deployment support is wired generically in ONE place, never per kind
// (R3). It wires the `on:` driver TargetResolver (liveTargetResolver resolves a
// driver of ANY kind via resolveCheckVenue), pre-resolves the ${PEER_*} subject
// addresses (applyPeerVars), runs, and tears down any peer endpoints opened.
// The harness scorer (check_runner_live.go) keeps its OWN resolver — it runs
// against sandbox-NESTED pods, a genuinely different venue context, not a
// duplicate of this host-context path.
func (r *Runner) RunLive(ctx context.Context, checks []Op, instance string) []CheckResult {
	r.TargetResolver = liveTargetResolver(instance)
	applyPeerVars(r, checks, instance)
	defer r.ClosePeers()
	return r.Run(ctx, checks)
}

// NewRunner constructs a Runner with sensible defaults. Caller passes a
// DeployExecutor appropriate for the mode — typically the chain returned
// by ResolveDeployChain (deploy_chain.go). For RunModeLive probes against
// a single running container, that's NestedExecutor{Parent: Local, Jump:
// PodmanExec{charly-name}}; for RunModeBox, ImageChain(engine, ref).
func NewRunner(exec DeployExecutor, resolver *CheckVarResolver, mode RunMode) *Runner {
	return &Runner{
		Exec:        exec,
		Resolver:    resolver,
		Mode:        mode,
		HTTPClient:  &http.Client{Timeout: 10 * time.Second},
		DialTimeout: 3 * time.Second,
	}
}

// Run executes the supplied checks sequentially and returns per-check
// results. Does not short-circuit on failure — the report should show
// every check's outcome for CI ergonomics.
func (r *Runner) Run(ctx context.Context, checks []Op) []CheckResult {
	results := make([]CheckResult, 0, len(checks))
	for i := range checks {
		results = append(results, r.runOne(ctx, &checks[i]))
	}
	return results
}

// runOne handles all the per-check housekeeping (verb resolution, skip
// handling, variable expansion, routing) and dispatches to a verb handler.
//
// Two BDD-era behaviours layer on top of the classical path:
//
//  1. `on:` target dispatch — if the check specifies a non-default
//     target AND r.TargetResolver is set, runOne temporarily swaps
//     r.Exec / r.Resolver for the duration of the dispatch. Classical
//     tests: runs pass nil TargetResolver and never hit this path.
//  2. `eventually:` retry wrapper — when set, the verb dispatch is
//     called repeatedly until pass or deadline.
//
//nolint:gocyclo // verb dispatch router with on: target-swap and eventually: retry wrapper; branching is essential to the execution model
func (r *Runner) runOne(ctx context.Context, c *Op) CheckResult {
	start := time.Now()
	kind, err := c.Kind()
	result := CheckResult{Op: c, Verb: kind}
	if err != nil {
		result.Status = TestFail
		result.Message = err.Error()
		result.Elapsed = time.Since(start)
		result.Attempts = 1
		result.TotalElapsed = result.Elapsed
		return result
	}
	if c.Skip {
		result.Status = TestSkip
		result.Message = "skip: true"
		result.Elapsed = time.Since(start)
		result.Attempts = 1
		result.TotalElapsed = result.Elapsed
		return result
	}
	// exclude_distros: skip when any of the image's distro tags intersects
	// with the exclusion list. Used for probes that are only meaningful on
	// some distros (e.g. a binary that a given distro renames or drops).
	if len(c.ExcludeDistros) > 0 && len(r.Distros) > 0 {
		for _, imgTag := range r.Distros {
			if slices.Contains(c.ExcludeDistros, imgTag) {
				result.Status = TestSkip
				result.Message = fmt.Sprintf("excluded on distro %q", imgTag)
				result.Elapsed = time.Since(start)
				result.Attempts = 1
				result.TotalElapsed = result.Elapsed
				return result
			}
		}
	}

	// Context-vs-mode skip — the unified-Op replacement for the old
	// scope:build↔check-box / scope:deploy↔check-live split. `charly check box`
	// (RunModeBox) runs against a disposable BUILD container, so it runs only
	// build-context steps; `charly check live` (RunModeLive) runs against a
	// RUNNING target, so it runs runtime-context steps. A step whose effective
	// context excludes the run's context is SKIPPED with a reason (e.g. a
	// `context: [runtime]` port/service probe in check box — no service runs in
	// a disposable build container).
	wantCtx := CtxRuntime
	modeName := "live"
	if r.Mode == RunModeBox {
		wantCtx, modeName = CtxBuild, "box"
	}
	if !c.InContext(wantCtx) {
		result.Status = TestSkip
		result.Message = fmt.Sprintf("context %v not active in %s mode", c.EffectiveContexts(), modeName)
		result.Elapsed = time.Since(start)
		result.Attempts = 1
		result.TotalElapsed = result.Elapsed
		return result
	}
	// `on:` multi-target dispatch. Swap executor + resolver + image for
	// the duration of this check only; restore on return. When
	// r.TargetResolver is nil (classical tests: path), Resolver+Exec
	// stay as-is.
	//
	// 2026-05: also swap r.Image so cdp/wl/vnc/mcp/etc dispatch
	// (runCharlyVerb at checkrun_ov_verbs.go:709 reads r.Image to build
	// `charly check <verb> <method> <image> ...` argv) routes against the
	// on-named pod, not the plan run's default pod. Without this swap,
	// `cdp: open` with `on: sway-browser-vnc-concurrency-test` was
	// silently dispatched against the plan run's jupyter pod and
	// failed at unknown-image.
	origExec, origResolver, origImage := r.Exec, r.Resolver, r.Box
	if c.On != "" && r.TargetResolver != nil {
		newResolver, newExec, terr := r.TargetResolver(c.On)
		if terr != nil {
			result.Status = TestFail
			result.Message = fmt.Sprintf("on: %q — %v", c.On, terr)
			result.Elapsed = time.Since(start)
			result.Attempts = 1
			result.TotalElapsed = result.Elapsed
			return result
		}
		if newExec != nil {
			r.Exec = newExec
		}
		if newResolver != nil {
			r.Resolver = newResolver
		}
		r.Box = c.On
		defer func() {
			r.Exec = origExec
			r.Resolver = origResolver
			r.Box = origImage
		}()
	}

	// Expand variables in-place on a copy so repeated runs over the same
	// check list don't accumulate substitutions. The env is derived by
	// overlaying the ScenarioContext (captures + ids) onto the resolver
	// base — so classical tests: with Scenario==nil see exactly today's
	// behavior.
	expanded := *c
	env := r.effectiveEnv()
	missing := expanded.ExpandVars(env)
	if len(missing) > 0 {
		// An unresolved cross-deployment var (${PEER_HOST}/${PEER_ENDPOINT})
		// means the peer/subject this probe targets is UNREACHABLE — the
		// probe's whole premise failed, so the check FAILS. A SKIP there would
		// be a fake pass (the bed must NOT go green on an unreachable peer).
		// Other unresolved vars stay a legitimate SKIP: a deploy-only var under
		// build scope, an unmounted volume — inputs that genuinely don't apply
		// to this run, not a failed dependency.
		if peerMissing := filterPeerVars(missing); len(peerMissing) > 0 {
			result.Status = TestFail
			result.Message = fmt.Sprintf("peer unreachable — unresolved cross-deployment variable(s): %s", strings.Join(peerMissing, ", "))
		} else {
			result.Status = TestSkip
			result.Message = fmt.Sprintf("unresolved variables: %s", strings.Join(missing, ", "))
		}
		result.Elapsed = time.Since(start)
		result.Attempts = 1
		result.TotalElapsed = result.Elapsed
		return result
	}

	// Verb dispatch, wrapped in the `eventually:` retry when requested.
	dispatch := func() CheckResult {
		var dr CheckResult
		// do-mode branch: act on a state-provision verb → execute the
		// create/configure. Action verbs (command/http/dbus/cdp/…) act in their
		// own handler, so do:act there falls through to the assert dispatch
		// below (the handler IS the act). Agent steps never reach runOne —
		// they route to the grader in runUnit (description_run.go).
		if expanded.EffectiveDo() == DoAct {
			if act, ok := r.runProvisionAct(ctx, &expanded, kind); ok {
				return act
			}
		}
		switch kind {
		case "file":
			dr = r.runFile(ctx, &expanded)
		case "port":
			dr = r.runPort(ctx, &expanded)
		case "command":
			dr = r.runCommand(ctx, &expanded)
		case "http":
			dr = r.runHTTP(ctx, &expanded)
		case "package":
			dr = r.runPackage(ctx, &expanded)
		case "service":
			dr = r.runService(ctx, &expanded)
		case "process":
			dr = r.runProcess(ctx, &expanded)
		case "dns":
			dr = r.runDNS(ctx, &expanded)
		case "user":
			dr = r.runUser(ctx, &expanded)
		case "group":
			dr = r.runGroup(ctx, &expanded)
		case "interface":
			dr = r.runInterface(ctx, &expanded)
		case "kernel-param":
			dr = r.runKernelParam(ctx, &expanded)
		case "mount":
			dr = r.runMount(ctx, &expanded)
		case "addr":
			dr = r.runAddr(ctx, &expanded)
		case "matching":
			dr = r.runMatching(ctx, &expanded)
		case "cdp":
			dr = r.runCdp(ctx, &expanded)
		case "wl":
			dr = r.runWl(ctx, &expanded)
		case "dbus":
			dr = r.runDbus(ctx, &expanded)
		case "vnc":
			dr = r.runVnc(ctx, &expanded)
		case "mcp":
			dr = r.runMcp(ctx, &expanded)
		case "record":
			dr = r.runRecord(ctx, &expanded)
		case "spice":
			dr = r.runSpice(ctx, &expanded)
		case "libvirt":
			dr = r.runLibvirt(ctx, &expanded)
		case "k8s":
			dr = r.runK8s(ctx, &expanded)
		case "adb":
			dr = r.runAdb(ctx, &expanded)
		case "appium":
			dr = r.runAppium(ctx, &expanded)
		case "summarize":
			dr = r.runSummarize(ctx, &expanded)
		case "kill":
			dr = r.runKill(ctx, &expanded)
		default:
			dr.Status = TestSkip
			dr.Message = fmt.Sprintf("unknown verb %q", kind)
		}
		return dr
	}

	result = runWithEventually(ctx, &expanded, dispatch)
	result.Op = c
	result.Verb = kind
	result.Elapsed = time.Since(start)
	// runWithEventually sets TotalElapsed relative to its own start time;
	// prefer that for multi-attempt cases. For single-attempt, Elapsed ≈
	// TotalElapsed so the caller-facing fields stay consistent.
	if result.TotalElapsed == 0 {
		result.TotalElapsed = result.Elapsed
	}

	// Record capture on PASS only — retry loops handled by
	// runWithEventually already enforce "final pass wins", so a single
	// check with capture: + eventually: captures the right value.
	if result.Status == TestPass && c.Capture != "" && r.Scenario != nil {
		// Prefer Check-type-specific capture extraction where we have it;
		// fall back to result.Message which verb handlers populate with
		// their primary output on PASS.
		raw := CaptureFromResult("", result.Message)
		if c.CaptureExtract != "" {
			extracted, err := ApplyCaptureExtract(raw, c.CaptureExtract)
			if err != nil {
				// A capture_extract miss is a real failure — better to
				// surface it loudly than store an empty/noisy value
				// that downstream ${CAPTURED:<name>} refs would then
				// misuse.
				result.Status = TestFail
				result.Message = fmt.Sprintf("%s (capture_extract on Message=%q)", err, raw)
				return result
			}
			raw = extracted
		}
		r.Scenario.Capture(c.Capture, raw)
		result.CapturedValue = r.Scenario.Captures[c.Capture]
	}
	return result
}

// effectiveEnv builds the variable-expansion env map for the current
// check. When a ScenarioContext is attached, captures + STEP_ID are
// overlaid on top of the resolver's base env — keeping
// classical tests: behaviour unchanged (nil Scenario → no overlay).
func (r *Runner) effectiveEnv() map[string]string {
	var base map[string]string
	if r.Resolver != nil {
		base = r.Resolver.Env
	}
	if r.Scenario == nil && len(r.PeerVars) == 0 {
		return base
	}
	// Copy-on-overlay so the resolver's shared Env map stays clean across
	// plan runs. Cross-deployment ${PEER_*} addresses overlay first (they are
	// per-run, target-independent), then plan-run captures (which win on the
	// rare key collision).
	capN := 0
	if r.Scenario != nil {
		capN = len(r.Scenario.Captures)
	}
	env := make(map[string]string, len(base)+len(r.PeerVars)+capN+2)
	maps.Copy(env, base)
	maps.Copy(env, r.PeerVars)
	if r.Scenario != nil {
		r.Scenario.ApplyToEnv(env)
	}
	return env
}

// ---------------------------------------------------------------------------
// file verb
// ---------------------------------------------------------------------------

// runFile checks existence, mode, owner, group, filetype, sha256, and
// optional content matchers on a path inside the target.
func (r *Runner) runFile(ctx context.Context, c *Op) CheckResult {
	path := c.File
	// Probe script emits a single line: exists|type|mode|owner|group|sha256
	// then (optionally) the file's contents on stdout following a marker.
	// `stat -c` portable fields: %F (type), %a (mode), %U (user), %G (group).
	probe := fmt.Sprintf(
		`if [ -e %[1]s ] || [ -L %[1]s ]; then
  printf "exists=1|"
  stat -c "%%F|%%a|%%U|%%G" %[1]s
else
  printf "exists=0|||||\n"
fi`, shellSingleQuote(path))
	stdout, stderr, exit, err := r.Exec.RunCapture(ctx, probe)
	if err != nil {
		return failf(c, "probe failed: %v (stderr: %s)", err, stderr)
	}
	if exit != 0 {
		return failf(c, "probe exit %d (stderr: %s)", exit, stderr)
	}
	line := strings.TrimSpace(stdout)
	// Expected: "exists=1|<type>|<mode>|<user>|<group>" OR "exists=0||||"
	parts := strings.SplitN(line, "|", 5)
	if len(parts) < 5 {
		return failf(c, "unexpected probe output: %q", line)
	}
	exists := strings.TrimPrefix(parts[0], "exists=") == "1"
	typeStr, mode, owner, group := parts[1], parts[2], parts[3], parts[4]

	// exists attribute (nil = default true)
	wantExists := true
	if c.Exists != nil {
		wantExists = *c.Exists
	}
	if wantExists != exists {
		return failf(c, "exists=%v, want %v", exists, wantExists)
	}
	if !exists {
		return passf(c, "file absent (as expected)")
	}
	if c.Mode != "" && strings.TrimLeft(mode, "0") != strings.TrimLeft(c.Mode, "0") {
		return failf(c, "mode=%s, want %s", mode, c.Mode)
	}
	if c.Owner != "" && owner != c.Owner {
		return failf(c, "owner=%s, want %s", owner, c.Owner)
	}
	if c.GroupOf != "" && group != c.GroupOf {
		return failf(c, "group=%s, want %s", group, c.GroupOf)
	}
	if c.Filetype != "" {
		ft := normalizeFiletype(typeStr)
		if ft != c.Filetype {
			return failf(c, "filetype=%s, want %s", ft, c.Filetype)
		}
	}

	// Content matchers: pull file contents and evaluate.
	if len(c.Contains) > 0 {
		contents, err := r.readFile(ctx, path)
		if err != nil {
			return failf(c, "read for contains: %v", err)
		}
		if err := matchAll(contents, c.Contains); err != nil {
			return failf(c, "contains: %v", err)
		}
	}
	if c.Sha256 != "" {
		out, _, exit, err := r.Exec.RunCapture(ctx, fmt.Sprintf("sha256sum %s", shellSingleQuote(path)))
		if err != nil || exit != 0 {
			return failf(c, "sha256 probe exit %d err %v", exit, err)
		}
		sum := strings.Fields(strings.TrimSpace(out))
		if len(sum) == 0 || sum[0] != c.Sha256 {
			return failf(c, "sha256=%s, want %s", sum, c.Sha256)
		}
	}

	return passf(c, "ok")
}

// readFile returns a file's contents from the target via Exec.
func (r *Runner) readFile(ctx context.Context, path string) (string, error) {
	out, stderr, exit, err := r.Exec.RunCapture(ctx, "cat "+shellSingleQuote(path))
	if err != nil {
		return "", err
	}
	if exit != 0 {
		return "", fmt.Errorf("cat exit %d: %s", exit, stderr)
	}
	return out, nil
}

// normalizeFiletype converts stat's %F verbose string into goss-parity short
// forms ("regular file" → "file", "directory" → "directory", "symbolic link"
// → "symlink").
func normalizeFiletype(s string) string {
	switch {
	case strings.Contains(s, "regular"):
		return "file"
	case strings.Contains(s, "directory"):
		return "directory"
	case strings.Contains(s, "symbolic link"), strings.Contains(s, "symlink"):
		return "symlink"
	case strings.Contains(s, "character"):
		return "character"
	case strings.Contains(s, "block"):
		return "block"
	case strings.Contains(s, "fifo"):
		return "fifo"
	case strings.Contains(s, "socket"):
		return "socket"
	}
	return s
}

// ---------------------------------------------------------------------------
// port verb
// ---------------------------------------------------------------------------

// runPort dispatches between in-container "listening" check and host-side
// reachability check based on attributes + run mode.
//
// Routing rules:
//   - listening: true (default when unset) → probe via Exec (container-internal)
//   - reachable/from-host semantics → dial 127.0.0.1:<HOST_PORT:N> from host
//     (only meaningful in RunModeLive; RunModeBox skips with reason)
func (r *Runner) runPort(ctx context.Context, c *Op) CheckResult {
	wantListening := true
	if c.Listening != nil {
		wantListening = *c.Listening
	}

	// If the user asked for outside-in reachability (listening:false with a
	// HOST_PORT substitution already performed, or Reachable explicitly set),
	// dial from host.
	if c.Reachable != nil || (c.Listening != nil && !*c.Listening) {
		if r.Mode == RunModeBox {
			return skipf(c, "host-side port check not meaningful under charly check box")
		}
		return r.dialPort(c)
	}

	// In-container listening probe: ss first, netstat fallback.
	probe := fmt.Sprintf(
		`(ss -tlnH 2>/dev/null || netstat -tln 2>/dev/null) | awk '{print $4}' | grep -E ':%d$' >/dev/null`,
		c.Port)
	_, stderr, exit, err := r.Exec.RunCapture(ctx, probe)
	if err != nil {
		return failf(c, "probe failed: %v (%s)", err, stderr)
	}
	listening := exit == 0
	if listening != wantListening {
		return failf(c, "listening=%v, want %v (on port %d)", listening, wantListening, c.Port)
	}
	return passf(c, fmt.Sprintf("port %d listening=%v", c.Port, listening))
}

// dialPort attempts a TCP dial on 127.0.0.1:<port> from the host. Used for
// deploy-scope reachability checks where ${HOST_PORT:N} has been substituted
// into the Port field. If the Port was remapped by charly.yml, the substituted
// value is what we'll dial.
func (r *Runner) dialPort(c *Op) CheckResult {
	addr := fmt.Sprintf("127.0.0.1:%d", c.Port)
	if c.IP != "" {
		addr = fmt.Sprintf("%s:%d", c.IP, c.Port)
	}
	conn, err := net.DialTimeout("tcp", addr, r.DialTimeout)
	wantReachable := true
	if c.Reachable != nil {
		wantReachable = *c.Reachable
	}
	if err != nil {
		if !wantReachable {
			return passf(c, fmt.Sprintf("%s unreachable (as expected)", addr))
		}
		return failf(c, "dial %s: %v", addr, err)
	}
	_ = conn.Close()
	if !wantReachable {
		return failf(c, "%s reachable but wanted unreachable", addr)
	}
	return passf(c, fmt.Sprintf("%s reachable", addr))
}

// ---------------------------------------------------------------------------
// command verb
// ---------------------------------------------------------------------------

// runCommand runs the command (in-container by default, from-host if
// InContainer=false or FromHost=true) and matches against Exit/Stdout/Stderr.
//
// runKill resolves the PID in c.Kill (typically expanded from
// ${CAPTURED:<name>} carrying a prior `command:` step's
// "backgrounded pid=N" message via capture_extract), and sends a
// signal — SIGTERM by default, SIGKILL when c.Signal == "KILL". The
// counterpart to `command: ... background: true`: a plan can
// spawn a writer, capture its PID, kill it mid-stream, and assert
// post-state consistency.
//
// Host-side only. Like Background, in-container PID kill is the
// user's responsibility (drop into command: with kill -<sig>).
func (r *Runner) runKill(_ context.Context, c *Op) CheckResult {
	if r.Mode == RunModeBox {
		return skipf(c, "kill: not meaningful under charly check box")
	}
	pidStr := strings.TrimSpace(c.Kill)
	if pidStr == "" {
		return failf(c, "kill: empty PID (resolved value is blank — check capture/${CAPTURED:...} chain)")
	}
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return failf(c, "kill: cannot parse PID %q: %v", pidStr, err)
	}
	if pid <= 0 {
		return failf(c, "kill: invalid PID %d", pid)
	}

	sig := strings.ToUpper(strings.TrimSpace(c.Signal))
	switch sig {
	case "", "TERM", "SIGTERM":
		if err := sendSIGTERM(pid); err != nil {
			return failf(c, "kill -TERM %d: %v", pid, err)
		}
		return passf(c, fmt.Sprintf("sent SIGTERM to pid=%d", pid))
	case "KILL", "SIGKILL":
		if err := sendSIGKILL(pid); err != nil {
			return failf(c, "kill -KILL %d: %v", pid, err)
		}
		return passf(c, fmt.Sprintf("sent SIGKILL to pid=%d", pid))
	default:
		return failf(c, "kill: unsupported signal %q (valid: TERM, KILL)", c.Signal)
	}
}

// Background mode: when c.Background is true, the host-side command is
// spawned via cmd.Start() (no Wait); the PID is registered with the
// plan-run context for SIGTERM-reap at plan teardown. Background
// mode is host-side only — in-container backgrounding is the user's
// responsibility (use `setsid nohup ... &` inside the bash given to
// the container shell).

// wrapContainerCommand guards an in-container command-check script against
// stdin-consuming subcommands. The runner delivers in-container scripts to the
// pod shell over a stdin heredoc (NestedExecutor.wrapWithJump, "stdin-attached
// exec"); without this guard the FIRST subcommand that reads stdin — adb shell,
// ssh, read, cat — consumes the REST of the heredoc (the not-yet-executed
// script lines), silently truncating the check to its first command. Wrapping
// the whole script in a brace group with stdin redirected from /dev/null fixes
// it generically: the shell reads the entire group before executing it (so the
// heredoc is fully drained by parse time), then runs it with every subcommand's
// stdin tied to /dev/null. The host path (`sh -c` argv, below) is unaffected.
func wrapContainerCommand(script string) string {
	return "{ " + script + "\n} </dev/null"
}

func (r *Runner) runCommand(ctx context.Context, c *Op) CheckResult {
	inContainer := true
	if c.InContainer != nil {
		inContainer = *c.InContainer
	}
	if c.FromHost {
		inContainer = false
	}

	// Background path — host-side only, fire-and-forget. Plan teardown
	// reaps via SIGTERM. Returns immediately with PASS.
	if c.Background {
		if inContainer {
			return failf(c, "background: true is host-side only (set in_container: false or from_host: true)")
		}
		if r.Mode == RunModeBox {
			return skipf(c, "background command not meaningful under charly check box")
		}
		cmd := exec.Command("sh", "-c", c.Command) // not CommandContext — survives ctx cancel
		if err := cmd.Start(); err != nil {
			return failf(c, "background start: %v", err)
		}
		if r.Scenario != nil {
			r.Scenario.AddBackground(cmd.Process.Pid)
		}
		// Reap asynchronously so `kill:` SIGKILL doesn't leave the
		// process as a zombie (which `kill -0 PID` would still report
		// as alive). Wait blocks until the process actually exits;
		// either teardown's SIGTERM or an in-plan kill: SIGKILL
		// will trigger that exit, after which Wait clears the zombie.
		go func() { _ = cmd.Wait() }()
		return passf(c, fmt.Sprintf("backgrounded pid=%d", cmd.Process.Pid))
	}

	var stdout, stderr string
	var exit int
	var err error
	if inContainer {
		stdout, stderr, exit, err = r.Exec.RunCapture(ctx, wrapContainerCommand(c.Command))
	} else {
		if r.Mode == RunModeBox {
			return skipf(c, "host-side command not meaningful under charly check box")
		}
		cmd := exec.CommandContext(ctx, "sh", "-c", c.Command)
		stdout, stderr, exit, err = runCaptureCmd(cmd)
	}
	if err != nil {
		return failf(c, "execution error: %v", err)
	}

	wantExit := 0
	if c.ExitStatus != nil {
		wantExit = *c.ExitStatus
	}
	if exit != wantExit {
		return failf(c, "exit=%d, want %d (stderr: %s)", exit, wantExit, trimPreview(stderr))
	}
	if err := matchAll(stdout, c.Stdout); err != nil {
		return failf(c, "stdout: %v (got: %s)", err, trimPreview(stdout))
	}
	if err := matchAll(stderr, c.Stderr); err != nil {
		return failf(c, "stderr: %v (got: %s)", err, trimPreview(stderr))
	}
	return passf(c, fmt.Sprintf("exit=%d", exit))
}

// ---------------------------------------------------------------------------
// http verb
// ---------------------------------------------------------------------------

// runHTTP performs an HTTP request against the URL and matches the response
// against Status/Body/Headers.
//
// Under RunModeLive the request goes from the charly process (outside-in
// reachability). Under RunModeBox the request is issued from inside
// the disposable container via curl (the container may have no network
// reachability from the host, so host-side is wrong there).
func (r *Runner) runHTTP(ctx context.Context, c *Op) CheckResult {
	if r.Mode == RunModeBox {
		return r.runHTTPInContainer(ctx, c)
	}
	return r.runHTTPFromHost(ctx, c)
}

func (r *Runner) runHTTPFromHost(ctx context.Context, c *Op) CheckResult {
	client, err := httpClientFor(c, r.HTTPClient)
	if err != nil {
		return failf(c, "http client: %v", err)
	}
	method := c.Method
	if method == "" {
		method = "GET"
	}
	var body io.Reader
	if c.RequestBody != "" {
		body = strings.NewReader(c.RequestBody)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.HTTP, body)
	if err != nil {
		return failf(c, "building request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return failf(c, "request: %v", err)
	}
	defer resp.Body.Close()

	if c.Status != 0 && resp.StatusCode != c.Status {
		return failf(c, "status=%d, want %d", resp.StatusCode, c.Status)
	}
	if len(c.Headers) > 0 {
		headerBlob := formatHeaders(resp.Header)
		if err := matchAll(headerBlob, c.Headers); err != nil {
			return failf(c, "headers: %v", err)
		}
	}
	if len(c.Body) > 0 {
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return failf(c, "reading body: %v", err)
		}
		if err := matchAll(string(bodyBytes), c.Body); err != nil {
			return failf(c, "body: %v", err)
		}
	}
	return passf(c, fmt.Sprintf("status=%d", resp.StatusCode))
}

func (r *Runner) runHTTPInContainer(ctx context.Context, c *Op) CheckResult {
	// In-container HTTP via curl. We only check status/body here; full
	// header-matching is implemented host-side. For Phase 3 this is
	// sufficient for validating that the service inside the disposable
	// container answers.
	cmd := fmt.Sprintf("curl -sS -o /tmp/.charly-test-body -w '%%{http_code}' %s", shellSingleQuote(c.HTTP))
	if c.AllowInsecure {
		cmd = "curl -sSk -o /tmp/.charly-test-body -w '%{http_code}' " + shellSingleQuote(c.HTTP)
	}
	stdout, stderr, exit, err := r.Exec.RunCapture(ctx, cmd)
	if err != nil || exit != 0 {
		return failf(c, "curl exit %d err %v (%s)", exit, err, trimPreview(stderr))
	}
	code, convErr := strconv.Atoi(strings.TrimSpace(stdout))
	if convErr != nil {
		return failf(c, "unexpected curl output: %q", stdout)
	}
	if c.Status != 0 && code != c.Status {
		return failf(c, "status=%d, want %d", code, c.Status)
	}
	if len(c.Body) > 0 {
		body, _, exit, err := r.Exec.RunCapture(ctx, "cat /tmp/.charly-test-body")
		if err != nil || exit != 0 {
			return failf(c, "reading body: exit=%d err=%v", exit, err)
		}
		if err := matchAll(body, c.Body); err != nil {
			return failf(c, "body: %v", err)
		}
	}
	return passf(c, fmt.Sprintf("status=%d", code))
}

// httpClientFor builds a per-check http.Client honoring AllowInsecure,
// NoFollowRedir, CAFile, and Timeout. Derives from the runner's base client
// so concurrent checks don't share TLS state surprises.
func httpClientFor(c *Op, base *http.Client) (*http.Client, error) {
	client := &http.Client{Timeout: base.Timeout}
	if c.Timeout != "" {
		if d, err := time.ParseDuration(c.Timeout); err == nil {
			client.Timeout = d
		}
	}
	tr := &http.Transport{}
	if c.AllowInsecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	if c.CAFile != "" {
		pem, err := os.ReadFile(c.CAFile)
		if err != nil {
			return nil, fmt.Errorf("reading CA file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no certs parsed from %s", c.CAFile)
		}
		if tr.TLSClientConfig == nil {
			tr.TLSClientConfig = &tls.Config{}
		}
		tr.TLSClientConfig.RootCAs = pool
	}
	client.Transport = tr
	if c.NoFollowRedir {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	return client, nil
}

func formatHeaders(h http.Header) string {
	var b strings.Builder
	for k, vs := range h {
		for _, v := range vs {
			b.WriteString(k)
			b.WriteString(": ")
			b.WriteString(v)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Matcher checkuation
// ---------------------------------------------------------------------------

// matchAll returns nil if every matcher succeeds against the value. The first
// failure wins (reports the specific unmet expectation).
//
// Takes []Matcher rather than MatcherList so callers can pass any named slice
// type whose underlying element is Matcher (e.g. ContainsList) without an
// explicit conversion at every call site.
func matchAll(value string, matchers []Matcher) error {
	for _, m := range matchers {
		if err := matchOne(value, m); err != nil {
			return err
		}
	}
	return nil
}

// matchOne evaluates a single matcher. The operator set here must stay in
// lockstep with #MatchOpMap (the CUE matcher-operator authority in _common.cue)
// — if the schema accepts an op, the runner must handle it.
func matchOne(value string, m Matcher) error {
	switch m.Op {
	case "equals":
		want := matchValueString(m.Value)
		if strings.TrimRight(value, "\r\n") != want {
			return fmt.Errorf("expected exactly %q", want)
		}
	case "not_equals":
		want := matchValueString(m.Value)
		if strings.TrimRight(value, "\r\n") == want {
			return fmt.Errorf("expected NOT to equal %q", want)
		}
	case "contains":
		for _, want := range matchValueStrings(m.Value) {
			if !strings.Contains(value, want) {
				return fmt.Errorf("expected to contain %q", want)
			}
		}
	case "not_contains":
		for _, want := range matchValueStrings(m.Value) {
			if strings.Contains(value, want) {
				return fmt.Errorf("expected NOT to contain %q", want)
			}
		}
	case "matches":
		re, err := regexp.Compile(matchValueString(m.Value))
		if err != nil {
			return fmt.Errorf("bad regex %v: %w", m.Value, err)
		}
		if !re.MatchString(value) {
			return fmt.Errorf("expected to match /%s/", re.String())
		}
	case "not_matches":
		re, err := regexp.Compile(matchValueString(m.Value))
		if err != nil {
			return fmt.Errorf("bad regex %v: %w", m.Value, err)
		}
		if re.MatchString(value) {
			return fmt.Errorf("expected NOT to match /%s/", re.String())
		}
	case "lt", "le", "gt", "ge":
		return matchNumeric(value, m)
	default:
		return fmt.Errorf("unsupported matcher op %q", m.Op)
	}
	return nil
}

// matchNumeric compares both sides as float64. Used for HTTP status codes,
// kernel-param integers, port counts — anywhere an ordering-aware matcher
// makes sense. String values with leading/trailing whitespace (like
// `sysctl -n` output) are trimmed before parsing.
func matchNumeric(value string, m Matcher) error {
	wantStr := matchValueString(m.Value)
	want, err := strconv.ParseFloat(strings.TrimSpace(wantStr), 64)
	if err != nil {
		return fmt.Errorf("%s: operand %q not numeric: %w", m.Op, wantStr, err)
	}
	got, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return fmt.Errorf("%s: observed %q not numeric: %w", m.Op, value, err)
	}
	var ok bool
	switch m.Op {
	case "lt":
		ok = got < want
	case "le":
		ok = got <= want
	case "gt":
		ok = got > want
	case "ge":
		ok = got >= want
	}
	if !ok {
		return fmt.Errorf("expected %s %v (got %v)", m.Op, want, got)
	}
	return nil
}

// matchValueString coerces a matcher's stored Value (any) to a string. For
// numeric types it renders canonically; for everything else it falls back
// to fmt.Sprint.
func matchValueString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	case nil:
		return ""
	}
	return fmt.Sprint(v)
}

// matchValueStrings handles list-valued matchers like {contains: [a, b]}.
// A scalar value becomes a singleton list.
func matchValueStrings(v any) []string {
	if list, ok := v.([]any); ok {
		out := make([]string, 0, len(list))
		for _, e := range list {
			out = append(out, matchValueString(e))
		}
		return out
	}
	return []string{matchValueString(v)}
}

// ---------------------------------------------------------------------------
// Result helpers
// ---------------------------------------------------------------------------

func passf(c *Op, msg string) CheckResult {
	return CheckResult{Op: c, Status: TestPass, Message: msg}
}

func failf(c *Op, format string, args ...any) CheckResult {
	return CheckResult{Op: c, Status: TestFail, Message: fmt.Sprintf(format, args...)}
}

func skipf(c *Op, msg string) CheckResult {
	return CheckResult{Op: c, Status: TestSkip, Message: msg}
}

func trimPreview(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

// ---------------------------------------------------------------------------
// Report rendering — text / JSON / TAP v13.
// ---------------------------------------------------------------------------

// FormatResultsText writes a human-readable summary of results to w and
// returns the number of failures.
func FormatResultsText(w io.Writer, results []CheckResult) int {
	passes, fails, skips := 0, 0, 0
	for _, r := range results {
		glyph := "?"
		switch r.Status {
		case TestPass:
			glyph = "✓"
			passes++
		case TestFail:
			glyph = "✗"
			fails++
		case TestSkip:
			glyph = "⚠"
			skips++
		}
		verb := r.Verb
		subject := firstNonEmpty(r.Op.File, r.Op.HTTP, r.Op.Command, r.Op.DNS, r.Op.Addr)
		if r.Op.Port != 0 {
			subject = fmt.Sprintf("%d", r.Op.Port)
		}
		fmt.Fprintf(w, "%s %s %s — %s\n", glyph, verb, subject, r.Message)
		if r.Op.Origin != "" && r.Status == TestFail {
			fmt.Fprintf(w, "  from %s\n", r.Op.Origin)
		}
	}
	fmt.Fprintf(w, "%d passed · %d failed · %d skipped\n", passes, fails, skips)
	return fails
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// FormatResultsJSON emits a structured report suitable for CI consumption.
// Returns the number of failures.
func FormatResultsJSON(w io.Writer, results []CheckResult) int {
	type entry struct {
		Verb    string `json:"verb"`
		Status  string `json:"status"`
		Origin  string `json:"origin,omitempty"`
		Subject string `json:"subject,omitempty"`
		Message string `json:"message,omitempty"`
	}
	out := make([]entry, 0, len(results))
	fails := 0
	for _, r := range results {
		subject := firstNonEmpty(r.Op.File, r.Op.HTTP, r.Op.Command, r.Op.DNS, r.Op.Addr)
		if r.Op.Port != 0 {
			subject = fmt.Sprintf("%d", r.Op.Port)
		}
		if r.Status == TestFail {
			fails++
		}
		out = append(out, entry{
			Verb:    r.Verb,
			Status:  r.Status.String(),
			Origin:  r.Op.Origin,
			Subject: subject,
			Message: r.Message,
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
	return fails
}

// FormatResultsTAP emits TAP v13. Returns the number of failures.
func FormatResultsTAP(w io.Writer, results []CheckResult) int {
	fails := 0
	fmt.Fprintf(w, "TAP version 13\n1..%d\n", len(results))
	for i, r := range results {
		subject := firstNonEmpty(r.Op.File, r.Op.HTTP, r.Op.Command, r.Op.DNS, r.Op.Addr)
		if r.Op.Port != 0 {
			subject = fmt.Sprintf("%d", r.Op.Port)
		}
		label := fmt.Sprintf("%s %s - %s", r.Verb, subject, r.Message)
		switch r.Status {
		case TestPass:
			fmt.Fprintf(w, "ok %d - %s\n", i+1, label)
		case TestSkip:
			fmt.Fprintf(w, "ok %d - %s # SKIP %s\n", i+1, label, r.Message)
		case TestFail:
			fails++
			fmt.Fprintf(w, "not ok %d - %s\n", i+1, label)
		}
	}
	return fails
}
