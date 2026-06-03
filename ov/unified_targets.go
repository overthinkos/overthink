package main

// unified_targets.go — The unified deploy-target abstraction.
//
// UnifiedDeployTarget/LifecycleTarget adapters for each of the five
// legacy DeployTarget implementers (LocalDeployTarget, VmDeployTarget,
// PodDeployTarget, K8sDeployTarget, AndroidDeployTarget), plus the
// ResolveTarget dispatcher.
//
// Each adapter wraps an existing legacy target via struct embedding.
// Methods on the adapter take precedence over inherited legacy methods
// (Go's outer-struct shadowing), so Name()/Kind()/Executor()/Add() are
// defined once here without touching the legacy files.
//
// Add()/Del()/Test()/Update() and the lifecycle methods (Start/Stop/
// Status/Logs/Shell/Rebuild) are the canonical implementations: Add()
// CONSTRUCTS its live embedded target from the DeployContext and runs
// the kind-specific deploy; Del() walks the ledger / runs kubectl /
// uninstalls apks per kind. ResolveTarget(node, name) returns the right
// adapter; the cmd files (deploy_add_cmd.go) carry no per-kind dispatch
// switch — they build the DeployContext and call target.Add / target.Del.

import (
	"errors"
	"fmt"
)

// ErrNotSupportedOnK8s is returned by lifecycle methods on the K8s
// target. K8s cluster lifecycle is kubectl-managed outside ov; ov
// start/stop/status/logs/shell/rebuild have no meaning for a k8s
// "deployment" in our schema.
var ErrNotSupportedOnK8s = errors.New("lifecycle operation not supported on kubernetes target")

// ---------------------------------------------------------------------------
// LocalUnifiedTarget — adapter over LocalDeployTarget.
//
// Stubbed Add/Name/Kind/Executor live here. The lifecycle and management
// methods (Del, Test, Update, Start, Stop, Status, Logs, Shell, Rebuild)
// live in unified_targets_host.go (C11 / Phase 3 implementation).
// ---------------------------------------------------------------------------

// LocalUnifiedTarget wraps LocalDeployTarget to satisfy
// UnifiedDeployTarget + LifecycleTarget.
type LocalUnifiedTarget struct {
	*LocalDeployTarget

	// NodeName is the deployment identifier from deploy.yml. Distinct
	// from the legacy LocalDeployTarget.Name() which returns the kind
	// ("host"). UnifiedDeployTarget.Name() returns this.
	NodeName string

	// KeepRepoChanges and KeepServices are deploy-del gate flags
	// populated by the dispatcher from `ov deploy del --keep-…`.
	// Forwarded to runReverseOps when Del runs. Default false → repo
	// changes and packaged services ARE reversed (the destructive
	// teardown path).
	KeepRepoChanges bool
	KeepServices    bool

	// RevRunner is the ReverseRunner used by ReverseOp handlers.
	// Defaults to nil → reverse_ops.go falls back to local exec.Command,
	// which matches the long-standing on-host teardown path. Tests
	// substitute a mock here.
	RevRunner ReverseRunner
}

func (t *LocalUnifiedTarget) Name() string { return t.NodeName }
func (t *LocalUnifiedTarget) Kind() string { return "host" }
func (t *LocalUnifiedTarget) Executor() DeployExecutor {
	if t.LocalDeployTarget == nil {
		return ShellExecutor{}
	}
	return t.LocalDeployTarget.exec()
}

// Add for the local target lives in unified_targets_local.go alongside
// Del/Test/Update/Rebuild — it constructs the live LocalDeployTarget.

// ---------------------------------------------------------------------------
// VmUnifiedTarget — adapter over VmDeployTarget.
// ---------------------------------------------------------------------------

type VmUnifiedTarget struct {
	*VmDeployTarget

	// NodeName is the deploy.yml identifier (e.g. "arch-vm"). Distinct
	// from VmDeployTarget.Name ("vm:" + VMName legacy) and
	// VmDeployTarget.VMName (the underlying kind:vm entity name).
	NodeName string

	// Instance is the optional per-instance suffix for multi-instance
	// VMs. Combined with the entity name to form the libvirt/qemu
	// domain via vmName(entity, instance).
	Instance string

	// KeepRepoChanges and KeepServices are deploy-del gate flags
	// populated by the dispatcher from `ov deploy del --keep-…`.
	// Forwarded to runReverseOps when Del runs.
	KeepRepoChanges bool
	KeepServices    bool

	// RevRunner is the ReverseRunner used by guest-side ReverseOp
	// teardown. Typically an *sshReverseRunner constructed by the
	// dispatcher from the persisted vm_state in deploy.yml. Nil →
	// Del builds it itself from buildVmReverseRunner(NodeName).
	RevRunner ReverseRunner

	// NodeOnly mirrors `ov deploy add --node-only`: when true, Add does
	// NOT descend into nested target:pod children (the caller deploys
	// them explicitly afterwards via the dotted path). Set by the
	// dispatcher from DeployAddCmd.NodeOnly.
	NodeOnly bool
}

func (t *VmUnifiedTarget) Name() string { return t.NodeName }
func (t *VmUnifiedTarget) Kind() string { return "vm" }
func (t *VmUnifiedTarget) Executor() DeployExecutor {
	if t.VmDeployTarget == nil {
		return nil
	}
	return t.VmDeployTarget.Exec
}

// Add for the vm target lives in unified_targets_vm.go alongside
// Del/Test/Update/Rebuild — it constructs the live VmDeployTarget
// (ssh-config stanza + auto-boot + SSHExecutor) and deploys nested
// pods from the merged dctx.Node.

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

	// KeepImage suppresses overlay-image removal during Del. Populated
	// by the dispatcher from `ov deploy del --keep-image`. The unified
	// DelOpts is uniform across kinds; pod-specific gates live here.
	KeepImage bool

	// BaseImageRef is the image ref the rebuild's image-build/eval
	// steps target. Set by the dispatcher from the deploy.yml node's
	// `image:` field (or NodeName when absent). Empty → falls back to
	// NodeName at Rebuild time.
	BaseImageRef string

	// Add-time inputs, set by the dispatcher from DeployAddCmd flags.
	// Tag overrides the resolved CalVer; Ref is the user-supplied image
	// ref (persisted into deploy.yml when --disposable/--lifecycle are
	// set); Disposable / Lifecycle carry the classification opt-ins.
	Tag        string
	Ref        string
	Disposable bool
	Lifecycle  string
}

func (t *PodUnifiedTarget) Name() string { return t.NodeName }
func (t *PodUnifiedTarget) Kind() string { return "pod" }
func (t *PodUnifiedTarget) Executor() DeployExecutor {
	if t.PodDeployTarget == nil {
		return ShellExecutor{}
	}
	return t.PodDeployTarget.exec()
}

// Add for the pod target lives in unified_targets_pod.go alongside
// Del/Test/Update/Rebuild — it constructs the overlay PodDeployTarget
// (Generator + ResolvedImage + base-image DistroDef + baseRef CalVer).

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

// Add for the k8s target emits a Kustomize tree from (caps, node,
// cluster) — it does NOT consume the InstallPlan IR (plans is ignored,
// per K8sDeployTarget.Emit being a no-op). Body in unified_targets_k8s.go.
//
// Del / Test / Update for k8s also live in unified_targets_k8s.go.

// ---------------------------------------------------------------------------
// AndroidUnifiedTarget — adapter over AndroidDeployTarget.
//
// A target: android deploy installs its add_layer: layers' apk: packages
// onto a kind:android DEVICE (an in-pod emulator or a remote adb endpoint).
// Like K8s it only implements UnifiedDeployTarget (not LifecycleTarget) —
// the device's lifecycle belongs to its pod deploy / the remote host, not
// to the android deploy. Start/Stop/etc. are not meaningful here.
// ---------------------------------------------------------------------------

type AndroidUnifiedTarget struct {
	*AndroidDeployTarget

	// NodeName is the deploy.yml identifier.
	NodeName string
}

func (t *AndroidUnifiedTarget) Name() string             { return t.NodeName }
func (t *AndroidUnifiedTarget) Kind() string             { return "android" }
func (t *AndroidUnifiedTarget) Executor() DeployExecutor { return nil }

// Add / Del for the android target live in unified_targets_android.go —
// Add resolves + readiness-gates the device then installs apks; Del
// uninstalls them best-effort.

// ---------------------------------------------------------------------------
// ResolveTarget — the unified dispatcher.
//
// Looks up a deploy.yml node by name, validates that `target:` is set,
// and returns the appropriate UnifiedDeployTarget adapter. This is the
// canonical entry point for every deploy verb (`ov deploy add` / `del`
// and `ov update`). The returned adapter carries identity only; its Add
// method CONSTRUCTS the live embedded target from the DeployContext.
// ---------------------------------------------------------------------------

// canonicalTarget normalizes the legacy target spellings to the
// canonical vocabulary (local|vm|pod|k8s|android). "container" → "pod",
// "kubernetes" → "k8s", "host" → "local". An empty value is left empty
// so ResolveTarget can raise the missing-target error. The `ov migrate`
// deploy step rewrites these on-disk; this normalization keeps the
// resolver tolerant of in-flight configs.
func canonicalTarget(target string) string {
	switch target {
	case "container":
		return "pod"
	case "kubernetes":
		return "k8s"
	case "host":
		return "local"
	}
	return target
}

// ResolveTarget returns the UnifiedDeployTarget for `name`, dispatching
// on the node's canonical target. The node MUST be the dispatch-merged
// DeploymentNode (project+operator field merge from resolveTreeRoot) —
// the adapter consumes node fields (Nested/Env/ephemeral/disposable)
// directly and NEVER re-reads them from disk.
//
// Errors:
//   - "no deployment X" — node absent / nil
//   - "X: missing required `target:`" — schema violation
//   - "X: unknown target Y" — value not in local|vm|pod|k8s|android
func ResolveTarget(node *DeploymentNode, name string) (UnifiedDeployTarget, error) {
	if node == nil {
		return nil, fmt.Errorf("no deployment %q; run `ov deploy list`", name)
	}

	// Every deployment MUST carry target:. The migrator sets it for
	// legacy entries; missing target: is a hard error at load.
	if node.Target == "" {
		return nil, fmt.Errorf("deployment %q missing required `target:` field "+
			"(local|vm|pod|k8s|android); run `ov migrate`", name)
	}

	switch canonicalTarget(node.Target) {
	case "local":
		return &LocalUnifiedTarget{NodeName: name}, nil

	case "vm":
		return &VmUnifiedTarget{NodeName: name}, nil

	case "pod":
		// BaseImageRef is the image the rebuild's build/eval steps target;
		// node.Image is the deploy.yml `image:` field (Rebuild falls back to
		// NodeName when empty).
		return &PodUnifiedTarget{NodeName: name, BaseImageRef: node.Image}, nil

	case "k8s":
		return &K8sUnifiedTarget{NodeName: name}, nil

	case "android":
		return &AndroidUnifiedTarget{NodeName: name}, nil

	default:
		return nil, fmt.Errorf("deployment %q: unknown target %q "+
			"(want local|vm|pod|k8s|android)", name, node.Target)
	}
}

// compile-time assertion: every adapter satisfies the interfaces it
// claims. If any method signature drifts, `go build` fails here.
var (
	_ UnifiedDeployTarget = (*LocalUnifiedTarget)(nil)
	_ UnifiedDeployTarget = (*VmUnifiedTarget)(nil)
	_ UnifiedDeployTarget = (*PodUnifiedTarget)(nil)
	_ UnifiedDeployTarget = (*K8sUnifiedTarget)(nil)
	_ UnifiedDeployTarget = (*AndroidUnifiedTarget)(nil)

	_ LifecycleTarget = (*LocalUnifiedTarget)(nil)
	_ LifecycleTarget = (*VmUnifiedTarget)(nil)
	_ LifecycleTarget = (*PodUnifiedTarget)(nil)
	// K8sUnifiedTarget + AndroidUnifiedTarget intentionally NOT in the
	// LifecycleTarget set (cluster / device lifecycle is external).
)
