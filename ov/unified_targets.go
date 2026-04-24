package main

// unified_targets.go — Phase 2 of the schema-v3 refactor.
//
// Adds UnifiedDeployTarget/LifecycleTarget adapters for each of the
// four legacy DeployTarget implementers (HostDeployTarget,
// VmDeployTarget, PodDeployTarget, K8sDeployTarget), plus the
// ResolveTarget dispatcher.
//
// Each adapter wraps an existing legacy target via struct embedding.
// Methods on the adapter take precedence over inherited legacy methods
// (Go's outer-struct shadowing), so Name()/Kind()/Executor()/Add() can
// be defined once here without touching the legacy files.
//
// Scope of this commit:
//   - Kind()/Executor()/Add() are live: Add() delegates to the legacy
//     Emit() body. Everything that already worked under the legacy
//     interface keeps working when dispatched through the adapter.
//   - Del()/Test()/Update() and the lifecycle methods (Start/Stop/
//     Status/Logs/Shell/Rebuild) are STUBS that return a typed
//     ErrNotYetImplemented sentinel. Phase 3 extracts the existing
//     runHostDel / runContainerDel / runVmDel / ov test / ov start /
//     ov stop / etc. bodies from their cmd files into these methods.
//   - ResolveTarget returns a concrete adapter given a DeploymentNode.
//     For now, only legacy-compatible targets are instantiable; full
//     construction (distro detection, builder resolver, SSH executor
//     setup, etc.) still happens in the cmd files. ResolveTarget here
//     is the shape; Phase 3 moves the construction logic inward.

import (
	"context"
	"errors"
	"fmt"
)

// ErrNotYetImplemented is returned by adapter methods that Phase 2
// hasn't filled in yet. Callers can `errors.Is(err, ErrNotYetImplemented)`
// to fall back to the legacy cmd-file path during the transition.
var ErrNotYetImplemented = errors.New("unified target method not yet implemented (Phase 3 extraction pending)")

// ErrNotSupportedOnK8s is returned by lifecycle methods on the K8s
// target. K8s cluster lifecycle is kubectl-managed outside ov; ov
// start/stop/status/logs/shell/rebuild have no meaning for a k8s
// "deployment" in our schema.
var ErrNotSupportedOnK8s = errors.New("lifecycle operation not supported on kubernetes target")

// ---------------------------------------------------------------------------
// HostUnifiedTarget — adapter over HostDeployTarget.
// ---------------------------------------------------------------------------

// HostUnifiedTarget wraps HostDeployTarget to satisfy
// UnifiedDeployTarget + LifecycleTarget.
type HostUnifiedTarget struct {
	*HostDeployTarget

	// NodeName is the deployment identifier from deploy.yml. Distinct
	// from the legacy HostDeployTarget.Name() which returns the kind
	// ("host"). UnifiedDeployTarget.Name() returns this.
	NodeName string
}

func (t *HostUnifiedTarget) Name() string { return t.NodeName }
func (t *HostUnifiedTarget) Kind() string { return "host" }
func (t *HostUnifiedTarget) Executor() DeployExecutor {
	if t.HostDeployTarget == nil {
		return LocalDeployExecutor{}
	}
	return t.HostDeployTarget.exec()
}

func (t *HostUnifiedTarget) Add(ctx context.Context, plans []*InstallPlan, opts EmitOpts) error {
	return t.HostDeployTarget.Emit(plans, opts)
}

func (t *HostUnifiedTarget) Del(ctx context.Context, opts DelOpts) error {
	return fmt.Errorf("host %q: %w", t.NodeName, ErrNotYetImplemented)
}

func (t *HostUnifiedTarget) Test(ctx context.Context, checks []Check, opts TestOpts) error {
	return fmt.Errorf("host %q: %w", t.NodeName, ErrNotYetImplemented)
}

func (t *HostUnifiedTarget) Update(ctx context.Context, plans []*InstallPlan, opts UpdateOpts) error {
	return fmt.Errorf("host %q: %w", t.NodeName, ErrNotYetImplemented)
}

func (t *HostUnifiedTarget) Start(ctx context.Context) error {
	return fmt.Errorf("host %q: %w", t.NodeName, ErrNotYetImplemented)
}
func (t *HostUnifiedTarget) Stop(ctx context.Context) error {
	return fmt.Errorf("host %q: %w", t.NodeName, ErrNotYetImplemented)
}
func (t *HostUnifiedTarget) Status(ctx context.Context) (StatusInfo, error) {
	return StatusInfo{}, fmt.Errorf("host %q: %w", t.NodeName, ErrNotYetImplemented)
}
func (t *HostUnifiedTarget) Logs(ctx context.Context, opts LogsOpts) error {
	return fmt.Errorf("host %q: %w", t.NodeName, ErrNotYetImplemented)
}
func (t *HostUnifiedTarget) Shell(ctx context.Context, cmd []string) error {
	return fmt.Errorf("host %q: %w", t.NodeName, ErrNotYetImplemented)
}
func (t *HostUnifiedTarget) Rebuild(ctx context.Context, opts RebuildOpts) error {
	return fmt.Errorf("host %q: %w", t.NodeName, ErrNotYetImplemented)
}

// ---------------------------------------------------------------------------
// VmUnifiedTarget — adapter over VmDeployTarget.
// ---------------------------------------------------------------------------

type VmUnifiedTarget struct {
	*VmDeployTarget

	// NodeName is the deploy.yml identifier (e.g. "arch-vm"). Distinct
	// from VmDeployTarget.Name ("vm:" + VMName legacy) and
	// VmDeployTarget.VMName (the underlying kind:vm entity name).
	NodeName string
}

func (t *VmUnifiedTarget) Name() string { return t.NodeName }
func (t *VmUnifiedTarget) Kind() string { return "vm" }
func (t *VmUnifiedTarget) Executor() DeployExecutor {
	if t.VmDeployTarget == nil {
		return nil
	}
	return t.VmDeployTarget.Exec
}

func (t *VmUnifiedTarget) Add(ctx context.Context, plans []*InstallPlan, opts EmitOpts) error {
	return t.VmDeployTarget.Emit(plans, opts)
}

func (t *VmUnifiedTarget) Del(ctx context.Context, opts DelOpts) error {
	return fmt.Errorf("vm %q: %w", t.NodeName, ErrNotYetImplemented)
}
func (t *VmUnifiedTarget) Test(ctx context.Context, checks []Check, opts TestOpts) error {
	return fmt.Errorf("vm %q: %w", t.NodeName, ErrNotYetImplemented)
}
func (t *VmUnifiedTarget) Update(ctx context.Context, plans []*InstallPlan, opts UpdateOpts) error {
	return fmt.Errorf("vm %q: %w", t.NodeName, ErrNotYetImplemented)
}
func (t *VmUnifiedTarget) Start(ctx context.Context) error {
	return fmt.Errorf("vm %q: %w", t.NodeName, ErrNotYetImplemented)
}
func (t *VmUnifiedTarget) Stop(ctx context.Context) error {
	return fmt.Errorf("vm %q: %w", t.NodeName, ErrNotYetImplemented)
}
func (t *VmUnifiedTarget) Status(ctx context.Context) (StatusInfo, error) {
	return StatusInfo{}, fmt.Errorf("vm %q: %w", t.NodeName, ErrNotYetImplemented)
}
func (t *VmUnifiedTarget) Logs(ctx context.Context, opts LogsOpts) error {
	return fmt.Errorf("vm %q: %w", t.NodeName, ErrNotYetImplemented)
}
func (t *VmUnifiedTarget) Shell(ctx context.Context, cmd []string) error {
	return fmt.Errorf("vm %q: %w", t.NodeName, ErrNotYetImplemented)
}
func (t *VmUnifiedTarget) Rebuild(ctx context.Context, opts RebuildOpts) error {
	return fmt.Errorf("vm %q: %w", t.NodeName, ErrNotYetImplemented)
}

// ---------------------------------------------------------------------------
// PodUnifiedTarget — adapter over PodDeployTarget.
//
// Named "Pod" in the new schema per the approved plan. The legacy
// struct remains PodDeployTarget until Phase 4's sweeping rename.
// ---------------------------------------------------------------------------

type PodUnifiedTarget struct {
	*PodDeployTarget

	// NodeName is the deploy.yml identifier (e.g. "sway-pod"). The
	// legacy PodDeployTarget.DeployName holds the same string;
	// we duplicate here for adapter-level symmetry with Host/Vm.
	NodeName string
}

func (t *PodUnifiedTarget) Name() string { return t.NodeName }
func (t *PodUnifiedTarget) Kind() string { return "pod" }
func (t *PodUnifiedTarget) Executor() DeployExecutor {
	if t.PodDeployTarget == nil {
		return LocalDeployExecutor{}
	}
	return t.PodDeployTarget.exec()
}

func (t *PodUnifiedTarget) Add(ctx context.Context, plans []*InstallPlan, opts EmitOpts) error {
	return t.PodDeployTarget.Emit(plans, opts)
}

func (t *PodUnifiedTarget) Del(ctx context.Context, opts DelOpts) error {
	return fmt.Errorf("pod %q: %w", t.NodeName, ErrNotYetImplemented)
}
func (t *PodUnifiedTarget) Test(ctx context.Context, checks []Check, opts TestOpts) error {
	return fmt.Errorf("pod %q: %w", t.NodeName, ErrNotYetImplemented)
}
func (t *PodUnifiedTarget) Update(ctx context.Context, plans []*InstallPlan, opts UpdateOpts) error {
	return fmt.Errorf("pod %q: %w", t.NodeName, ErrNotYetImplemented)
}
func (t *PodUnifiedTarget) Start(ctx context.Context) error {
	return fmt.Errorf("pod %q: %w", t.NodeName, ErrNotYetImplemented)
}
func (t *PodUnifiedTarget) Stop(ctx context.Context) error {
	return fmt.Errorf("pod %q: %w", t.NodeName, ErrNotYetImplemented)
}
func (t *PodUnifiedTarget) Status(ctx context.Context) (StatusInfo, error) {
	return StatusInfo{}, fmt.Errorf("pod %q: %w", t.NodeName, ErrNotYetImplemented)
}
func (t *PodUnifiedTarget) Logs(ctx context.Context, opts LogsOpts) error {
	return fmt.Errorf("pod %q: %w", t.NodeName, ErrNotYetImplemented)
}
func (t *PodUnifiedTarget) Shell(ctx context.Context, cmd []string) error {
	return fmt.Errorf("pod %q: %w", t.NodeName, ErrNotYetImplemented)
}
func (t *PodUnifiedTarget) Rebuild(ctx context.Context, opts RebuildOpts) error {
	return fmt.Errorf("pod %q: %w", t.NodeName, ErrNotYetImplemented)
}

// ---------------------------------------------------------------------------
// K8sUnifiedTarget — adapter over K8sDeployTarget.
//
// Only implements UnifiedDeployTarget (not LifecycleTarget). Cluster
// lifecycle is kubectl-managed; Start/Stop/etc. return
// ErrNotSupportedOnK8s wrapped with the deployment name.
// ---------------------------------------------------------------------------

type K8sUnifiedTarget struct {
	*K8sDeployTarget

	// NodeName is the deploy.yml identifier (e.g. "k8s-cluster").
	NodeName string
}

func (t *K8sUnifiedTarget) Name() string { return t.NodeName }
func (t *K8sUnifiedTarget) Kind() string { return "k8s" }

// Executor returns a nil DeployExecutor — k8s operations go through
// Kustomize + kubectl, not shell. Callers that need to run a shell
// primitive against a k8s target must special-case this (they don't
// today; no code path exists).
func (t *K8sUnifiedTarget) Executor() DeployExecutor { return nil }

func (t *K8sUnifiedTarget) Add(ctx context.Context, plans []*InstallPlan, opts EmitOpts) error {
	return t.K8sDeployTarget.Emit(plans, opts)
}

func (t *K8sUnifiedTarget) Del(ctx context.Context, opts DelOpts) error {
	return fmt.Errorf("k8s %q: %w", t.NodeName, ErrNotYetImplemented)
}
func (t *K8sUnifiedTarget) Test(ctx context.Context, checks []Check, opts TestOpts) error {
	return fmt.Errorf("k8s %q: %w", t.NodeName, ErrNotYetImplemented)
}
func (t *K8sUnifiedTarget) Update(ctx context.Context, plans []*InstallPlan, opts UpdateOpts) error {
	return fmt.Errorf("k8s %q: %w", t.NodeName, ErrNotYetImplemented)
}

// ---------------------------------------------------------------------------
// ResolveTarget — the unified dispatcher.
//
// Looks up a deploy.yml node by name, validates that `target:` is set,
// and returns the appropriate UnifiedDeployTarget adapter.
//
// Phase 2 scope: returns adapters with nil embedded legacy targets
// when full construction (distro detection, SSH setup, etc.) is only
// possible from the existing cmd-file entry points. Callers that need
// a fully-live target still go through the legacy runHost/runVM/
// runContainer paths during the Phase 2→3 transition. Once Phase 3
// moves the construction logic into these adapters, ResolveTarget
// becomes the canonical entry point.
// ---------------------------------------------------------------------------

// ResolveTarget returns the UnifiedDeployTarget for `name`. It loads
// the node from the given UnifiedFile (or similar — TODO: plumb the
// right loader signature in Phase 3) and dispatches on node.Target.
//
// Errors:
//   - "no deployment X" — node absent from deploy.yml
//   - "X: missing required `target:`" — schema-v3 violation
//   - "X: unknown target Y" — value not in host|vm|pod|k8s
func ResolveTarget(node *DeploymentNode, name string) (UnifiedDeployTarget, error) {
	if node == nil {
		return nil, fmt.Errorf("no deployment %q; run `ov deploy list`", name)
	}

	// Schema v3 invariant: every deployment MUST carry target:.
	// Phase 6's migrator sets it for legacy entries; after the
	// cutover commit, missing target: is a hard error at load.
	if node.Target == "" {
		return nil, fmt.Errorf("deployment %q missing required `target:` field "+
			"(host|vm|pod|k8s); run `ov migrate deploy-schema-v3`", name)
	}

	switch node.Target {
	case "host":
		// Phase 3 will plumb HostDeployTarget construction (distro,
		// shell detection, nested executor via node.Inside). Today's
		// cmd-file runHost() still does this setup; the returned
		// adapter has a nil embedded target so only Name/Kind work.
		return &HostUnifiedTarget{NodeName: name}, nil

	case "vm":
		// Legacy alias: "vm:<name>" naming is deprecated in schema v3
		// but may still appear pre-migration. The name-prefix parser
		// is removed in Phase 5; for now, we accept either.
		return &VmUnifiedTarget{NodeName: name}, nil

	case "pod", "container":
		// "container" is the legacy spelling. The Phase 4 sweep
		// renames it; migration in Phase 6 rewrites existing files.
		// Accepting both during the transition keeps old configs
		// loadable until the cutover commit.
		return &PodUnifiedTarget{NodeName: name}, nil

	case "k8s", "kubernetes":
		// "kubernetes" is the legacy spelling; same pattern as
		// container → pod.
		return &K8sUnifiedTarget{NodeName: name}, nil

	default:
		return nil, fmt.Errorf("deployment %q: unknown target %q "+
			"(want host|vm|pod|k8s)", name, node.Target)
	}
}

// compile-time assertion: every adapter satisfies the interfaces it
// claims. If any method signature drifts, `go build` fails here.
var (
	_ UnifiedDeployTarget = (*HostUnifiedTarget)(nil)
	_ UnifiedDeployTarget = (*VmUnifiedTarget)(nil)
	_ UnifiedDeployTarget = (*PodUnifiedTarget)(nil)
	_ UnifiedDeployTarget = (*K8sUnifiedTarget)(nil)

	_ LifecycleTarget = (*HostUnifiedTarget)(nil)
	_ LifecycleTarget = (*VmUnifiedTarget)(nil)
	_ LifecycleTarget = (*PodUnifiedTarget)(nil)
	// K8sUnifiedTarget intentionally NOT in the LifecycleTarget set.
)
