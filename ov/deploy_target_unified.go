package main

// deploy_target_unified.go — forward-looking DeployTarget interface for
// schema v3.
//
// The legacy DeployTarget interface at install_plan.go:859 is the 2-method
// contract (Name + Emit) that the four target implementers (host, vm,
// container/pod, k8s) currently satisfy. This file defines the unified
// replacement: UnifiedDeployTarget with real methods, plus LifecycleTarget
// for the three live-runtime targets.
//
// Rollout plan (from /home/atrawog/.claude/plans/can-you-have-a-recursive-axolotl.md):
//   Phase 1 (this commit): interfaces + opts types defined alongside the
//       legacy DeployTarget. Nothing uses them yet.
//   Phase 2: each target implements UnifiedDeployTarget (Host/Vm/Pod/K8s)
//       and LifecycleTarget (Host/Vm/Pod) as adapters to existing Emit
//       bodies. ResolveTarget(name) returns UnifiedDeployTarget.
//   Phase 3: every ov cmd dispatcher switches to ResolveTarget +
//       UnifiedDeployTarget method calls.
//   Phase 4-5: rename container → pod, kubernetes → k8s; delete legacy
//       DeployTarget interface + per-cmd dispatch switches.
//
// Keeping both interfaces live during Phases 1-3 means existing call
// sites keep compiling while new call sites adopt UnifiedDeployTarget
// incrementally. Phase 5 deletes the legacy surface.

import (
	"context"
)

// UnifiedDeployTarget is the unified contract all four deploy methods
// (host, vm, pod, k8s) implement uniformly. Each method corresponds to
// an `ov deploy …` subcommand, so the dispatcher in resolve_target.go
// can route purely on target.Kind() without per-cmd switches.
type UnifiedDeployTarget interface {
	// Name is the deployment's identifier from deploy.yml (e.g.
	// "arch-vm", "sway-pod"). Unique within a deploy.yml.
	Name() string

	// Kind returns one of "host" | "vm" | "pod" | "k8s".
	// Drives ledger keying ("<kind>:<name>") and command dispatch.
	Kind() string

	// Executor returns the DeployExecutor this target will use for
	// shell operations. For host → ShellExecutor; for vm →
	// SSHExecutor; for pod → a podman-exec wrapper; for k8s → a nop
	// executor that errors on invocation (k8s operates via
	// kubectl/Kustomize, not shell ops).
	//
	// Exposing the executor on the interface lets parent targets in
	// a nested tree compose a NestedExecutor over the child. This
	// is also the sole plumbing point for the `inside:` cross-ref
	// (a target=host deployment with inside: arch-vm resolves its
	// executor as NestedExecutor(ResolveTarget(arch-vm).Executor())).
	Executor() DeployExecutor

	// Add applies the given plans to the target. Equivalent to
	// `ov deploy add <name>`. Idempotent: re-applying the same plan
	// is safe.
	Add(ctx context.Context, plans []*InstallPlan, opts EmitOpts) error

	// Del reverses every layer currently recorded for this target
	// and removes the deploy record. Equivalent to `ov deploy del
	// <name>`. Only recorded ReverseOps are replayed — never an
	// ad-hoc computation from layer.yml.
	Del(ctx context.Context, opts DelOpts) error

	// Test runs the given deploy-scope checks against the live
	// target. Equivalent to `ov eval live <name>`. Returns nil only if
	// every non-skipped check passes.
	Test(ctx context.Context, checks []Check, opts TestOpts) error

	// Update re-applies the plan diff between the currently-recorded
	// layer set and the plan set derived from fresh overthink.yml.
	// Equivalent to `ov deploy update <name>` (new command; today's
	// `ov update` is image-focused and will be separate).
	Update(ctx context.Context, plans []*InstallPlan, opts UpdateOpts) error
}

// LifecycleTarget extends UnifiedDeployTarget for live-runtime targets
// (host, vm, pod). K8s does NOT implement this: its cluster lifecycle
// is kubectl-managed outside ov. Commands that require a live runtime
// (ov start/stop/status/logs/shell/rebuild) assert the interface and
// error uniformly on k8s targets.
type LifecycleTarget interface {
	UnifiedDeployTarget

	// Start brings the target up (ov start / podman start / virsh
	// start / systemctl start as appropriate). Idempotent: no-op if
	// already running.
	Start(ctx context.Context) error

	// Stop brings the target down. Idempotent.
	Stop(ctx context.Context) error

	// Status reports the target's live runtime state.
	Status(ctx context.Context) (StatusInfo, error)

	// Logs streams or tails the target's logs. See LogsOpts for
	// follow/tail semantics.
	Logs(ctx context.Context, opts LogsOpts) error

	// Shell opens an interactive shell in the target. cmd, if
	// non-empty, is run instead of starting a login shell.
	Shell(ctx context.Context, cmd []string) error

	// Rebuild is destroy + create + start. Gated on the target's
	// Disposable flag — each implementation must verify it before
	// any destructive action. See /ov-internals:disposable for the
	// authorization contract.
	Rebuild(ctx context.Context, opts RebuildOpts) error
}

// ---------------------------------------------------------------------------
// Opts types — one per method.
// ---------------------------------------------------------------------------

// DelOpts parameterizes `ov deploy del`.
type DelOpts struct {
	// DryRun prints what would happen without executing.
	DryRun bool

	// AssumeYes skips confirmation prompts.
	AssumeYes bool

	// KeepLedger retains the ledger records after reversal. Useful
	// for debugging a failed teardown.
	KeepLedger bool

	// RemoveVolumes deletes bind-mount / named-volume data. Off by
	// default to avoid accidental data loss.
	RemoveVolumes bool
}

// TestOpts parameterizes `ov eval live` against a live deployment.
type TestOpts struct {
	// OnlyIDs restricts the run to the listed check IDs. Empty =
	// run every check defined on the deployment.
	OnlyIDs []string

	// FormatJSON emits machine-readable output instead of the human
	// summary table.
	FormatJSON bool

	// StopOnFail aborts on the first failing check.
	StopOnFail bool
}

// UpdateOpts parameterizes `ov deploy update`.
type UpdateOpts struct {
	DryRun           bool
	AssumeYes        bool
	RebuildImage     bool
	AllowRepoChanges bool
	AllowRootTasks   bool
	WithServices     bool
}

// StatusInfo summarizes a live deployment's runtime state.
type StatusInfo struct {
	// State is one of "running" | "stopped" | "paused" | "crashed"
	// | "unknown". Implementations normalize the underlying
	// runtime's lingo into this set.
	State string

	// Healthy combines runtime state + health-probe result, if any.
	Healthy bool

	// Details is a free-form map for target-specific extras (PID,
	// VM instance-id, cgroup, container image ref, etc.). Keys and
	// values are strings for ergonomic printing; values that are
	// semantically structured should marshal through JSON.
	Details map[string]string
}

// LogsOpts parameterizes `ov logs`.
type LogsOpts struct {
	// Follow streams new log lines as they arrive (tail -f style).
	Follow bool

	// Tail is the number of trailing lines to emit first. 0 = all.
	Tail int
}

// RebuildOpts parameterizes the rebuild path of `ov update`. Per /ov-internals:disposable, the
// target MUST be marked `disposable: true` in deploy.yml — every
// implementation asserts this before any destructive action.
type RebuildOpts struct {
	// RebuildImage forces an `ov image build` before redeploy. Off by
	// default: rebuilds only the deployment, reusing the existing
	// image ref.
	RebuildImage bool

	// AssumeYes skips confirmation prompts (already-gated by the
	// disposable check; this covers any additional "really rebuild?"
	// prompts from the underlying runtime).
	AssumeYes bool

	// DryRun prints the destroy + create steps without executing.
	DryRun bool
}
