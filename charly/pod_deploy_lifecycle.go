package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// pod_deploy_lifecycle.go — the HOST-SIDE lifecycle hook for the EXTERNAL `pod` deploy
// substrate (Design A — the SAME seam vm uses). pod is the DEFAULT substrate: a deployment
// run as a container IMAGE via quadlet/podman. Unlike vm (whose plugin WALKS the plan inside
// the guest), pod bakes its install steps INTO the image at BUILD time — so there is NO
// per-step venue walk. This hook owns the host-only container lifecycle the plugin cannot:
//
//   - PrepareVenue BUILDS the overlay container image HOST-SIDE (the SAME core OCITarget /
//     Generator build engine, in-process — nothing crosses gRPC, exactly as vm's
//     PrepareVenue builds the VM disk host-side). It lifts the prior PodUnifiedTarget.Add
//     body verbatim: NewGenerator + resolve the ResolvedBox from node.Image + the base
//     image's DistroDef + the baseRef CalVer + secret injection + PodDeployTarget.Emit
//     (overlay synthesis, or the deploy-alias tag when there is no add_candy). It returns a
//     host-local ShellExecutor so the generic externalDeployTarget.apply's plugin walk is a
//     clean no-op (plugin-deploy-pod.Invoke walks nothing) and recordVenueLedger no-ops (a
//     pod carries no venue-side ledger — its candies live in the image).
//   - Start/Stop/Status/Logs/Shell shell to the `charly start/stop/status/logs/shell` CLI.
//   - Rebuild = `box build`+`check box`+`bundle add`+`stop`+`config`+`start` (the pod
//     rebuild sequence the `charly update <pod-bed>` R10 fresh-rebuild gate routes through).
//   - PostTeardown = `charly remove` + drop the synthesized <name>-overlay images
//     (keep-image-gated) + ephemeral lifecycle teardown.
//
// It carries NO per-deploy mutable state (a stateless singleton, like vmSubstrateLifecycle)
// — every method re-resolves what it needs from (name, node) and re-loads config itself.
// The bed runner drives a pod bed through the SAME build→config→start→check-live→remove
// path as the in-proc pod (only the `bundle add` overlay-build internally routes through
// this hook now); a pod is NOT an "in-place" external substrate (see check_bed_run.go).

// podSubstrateLifecycle implements substrateLifecycle for the `pod` word.
type podSubstrateLifecycle struct{}

// register at package-var init (before any init(), race-free with the rest of the wiring).
var _ = func() bool {
	registerSubstrateLifecycle("pod", podSubstrateLifecycle{})
	return true
}()

// PrepareVenue builds the overlay container image HOST-SIDE and returns a host-local
// ShellExecutor (the plugin walks nothing; the overlay is baked here). It LIFTS the prior
// PodUnifiedTarget.Add body: a Generator + ResolvedBox so the overlay's OCITarget renders
// tasks as RUN directives, the base-image DistroDef so SystemPackagesSteps render with the
// base image's package format, the baseRef CalVer (newest-local when unset), candy-secret
// injection, then PodDeployTarget.Emit — which synthesizes the add_candy overlay when
// present, or tags the deploy-name alias when there is none. plans is the add_candy overlay
// plan set (empty for a pod with no add_candy; the external-substrate compileNodePlans skips
// the primary box plan — the candies are already baked into the base image).
func (podSubstrateLifecycle) PrepareVenue(_ context.Context, name, dir string, node *BundleNode, plans []*InstallPlan, opts EmitOpts) (DeployExecutor, error) {
	if dir == "" {
		if cwd, err := os.Getwd(); err == nil {
			dir = cwd
		}
	}
	if node == nil {
		tree, err := resolveTreeRoot(dir)
		if err != nil {
			return nil, fmt.Errorf("pod deploy %q: resolve deploy node: %w", name, err)
		}
		n, ok := tree[name]
		if !ok {
			return nil, fmt.Errorf("pod deploy %q: no deploy entry", name)
		}
		node = &n
	}

	// Ephemeral lifecycle hook (FIRST action — panic-safe TTL ordering). Consumes the
	// MERGED node (never a charly.yml re-read).
	registerEphemeralIfMarked(node, name)

	// Re-load the build vocabulary (distro/builder) for the base-image DistroDef + the
	// overlay BuilderConfig. dispatchNode already registered it via loadConfigForDeploy; the
	// hook re-reads the configs (self-contained, like vmSubstrateLifecycle re-loads VmSpec).
	distroCfg, builderCfg, _, err := LoadDefaultBuildConfig(dir)
	if err != nil {
		return nil, fmt.Errorf("pod deploy %q: load build config: %w", name, err)
	}

	// The box ref the overlay inherits FROM (the `pod: image:` field). Falls back to the
	// deploy name for a bare deploy (legacy parity with PodUnifiedTarget's NodeName fallback).
	base := node.Image
	if base == "" {
		base = name
	}
	tag := node.Version

	// Build a Generator + ResolvedBox so the overlay's OCITarget renders task steps as
	// actual RUN directives (not "no Generator context" comments).
	gen, _ := NewGenerator(dir, tag, ResolveOpts{})
	var resolvedImg *ResolvedBox
	if gen != nil && gen.Boxes != nil {
		resolvedImg = gen.Boxes[base]
	}

	// Resolve DistroDef from the BASE IMAGE's distro, not the operator host's — the
	// overlay's SystemPackagesSteps render using the base image's package format.
	var podDistroDef *DistroDef
	if resolvedImg != nil && len(resolvedImg.Distro) > 0 {
		podDistroDef = resolveDistroDef(distroCfg, resolvedImg.Distro[0])
	} else {
		podDistroDef = resolveDistroDef(distroCfg, detectHostContext().Distro)
	}

	// Build the BaseImage ref. With CalVer-only resolution, an empty tag resolves to the
	// newest local CalVer so the overlay FROM line gets a real tag (never a trailing colon).
	var baseRef string
	switch {
	case tag != "":
		baseRef = base + ":" + tag
	default:
		if resolved, rerr := ResolveNewestLocalCalVer("podman", base); rerr == nil && resolved != "" {
			baseRef = resolved
		} else {
			baseRef = base
		}
	}

	// A nested pod (a dotted deploy path, e.g. "parent.child") flattens to a dot-free
	// container/overlay name — the SAME NestedContainerName mapping the prior dispatcher's
	// PodUnifiedTarget case applied. A top-level pod (no dot) is unchanged.
	deployName := name
	if strings.Contains(name, ".") {
		deployName = NestedContainerName(name)
	}

	tgt := &PodDeployTarget{
		DeployName:    deployName,
		BaseImage:     baseRef,
		DistroDef:     podDistroDef,
		BuilderConfig: builderCfg,
		Generator:     gen,
		Box:           resolvedImg,
	}

	// Resolve + inject candy secrets so the overlay Containerfile emits `export VAR=VALUE`
	// before each add_candy task body (R3 shared helper). No-op when plans is empty.
	if _, _, serr := prepareCandySecrets(plans, dir); serr != nil {
		return nil, fmt.Errorf("pod deploy %q: loading candies for secret resolution: %w", name, serr)
	}

	// Thread ParentExec: when this container is a child of another deployment, the overlay
	// build runs in the parent's venue.
	if opts.ParentExec != nil {
		tgt.Executor = opts.ParentExec
	}
	if err := tgt.Emit(plans, opts); err != nil {
		return nil, fmt.Errorf("pod deploy %q: overlay build: %w", name, err)
	}
	if !opts.DryRun {
		fmt.Printf("Overlay image ready: %s\n", tgt.OverlayImageRef())
		fmt.Println("To start the container, run: charly start " + deployName)
	}

	// Host-local venue: the overlay is baked host-side; the plugin walks nothing, and
	// recordVenueLedger no-ops on a ShellExecutor (a pod carries no venue-side ledger).
	return ShellExecutor{}, nil
}

// ArtifactKey keys candy artifacts under the deploy name (the generic default) — pod has no
// shared-cluster artifact naming like vm's k3s ClusterProfile, so it returns "".
func (podSubstrateLifecycle) ArtifactKey(string, *BundleNode) string { return "" }

// PostApply is a no-op for pod: a pod bakes its candies into the overlay image, and its
// nested children deploy via the bed runner's tree walk AFTER `charly start` (not as an
// in-substrate orchestration like vm's nested pod-in-guest quadlets).
func (podSubstrateLifecycle) PostApply(context.Context, string, string, *BundleNode, DeployExecutor, EmitOpts) error {
	return nil
}

// TeardownExecutor returns nil so the generic Del keeps the ResolveTarget-selected executor
// (the host ShellExecutor). pod records no reverse ops, so the replay is a host-side no-op;
// the real teardown is PostTeardown (`charly remove` + drop overlay).
func (podSubstrateLifecycle) TeardownExecutor(string, *BundleNode) (DeployExecutor, error) {
	return nil, nil
}

// PostTeardown runs the canonical, record-free pod teardown (the SAME body as the prior
// PodUnifiedTarget.Del): delegate container + quadlet + sidecar + charly.yml cleanup to
// `charly remove`, then drop the deploy's synthesized <name>-overlay images (bundle del's
// one extra over `charly remove`, keep-image-gated), and cancel any ephemeral TTL lifecycle.
func (podSubstrateLifecycle) PostTeardown(name string, node *BundleNode, keepImage bool) error {
	if err := runCharlySubcommand("remove", name); err != nil {
		return err
	}
	if !keepImage {
		removeDeployOverlayImages(podDeployEngine(node), name)
	}
	if dcNode, ok := loadDeployConfigForRead("pod bundle-del ephemeral-teardown").LookupKey(name); ok && dcNode.IsEphemeral() {
		if tdErr := TeardownEphemeralLifecycle(&dcNode, name); tdErr != nil {
			fmt.Fprintf(os.Stderr, "warning: ephemeral lifecycle teardown: %v\n", tdErr)
		}
	}
	return nil
}

// Start brings the container deploy up via `charly start`.
func (podSubstrateLifecycle) Start(_ context.Context, name string, _ *BundleNode) error {
	return runCharlySubcommand("start", name)
}

// Stop brings the container deploy down via `charly stop`.
func (podSubstrateLifecycle) Stop(_ context.Context, name string, _ *BundleNode) error {
	return runCharlySubcommand("stop", name)
}

// Status parses `charly status --json` for this deploy's row (the SAME best-effort scan the
// prior PodUnifiedTarget.Status used — avoids coupling to the still-evolving status JSON).
func (podSubstrateLifecycle) Status(_ context.Context, name string, _ *BundleNode) (StatusInfo, error) {
	out, err := captureCharlyStdout("status", "--json")
	if err != nil {
		return StatusInfo{State: "unknown"}, err
	}
	for line := range strings.SplitSeq(out, "\n") {
		if !strings.Contains(line, name) {
			continue
		}
		state := "stopped"
		switch {
		case strings.Contains(line, "running"):
			state = "running"
		case strings.Contains(line, "paused"):
			state = "paused"
		case strings.Contains(line, "crashed"):
			state = "crashed"
		}
		return StatusInfo{
			State:   state,
			Healthy: state == "running",
			Details: map[string]string{"deploy": name},
		}, nil
	}
	return StatusInfo{State: "stopped", Healthy: false}, nil
}

// Logs streams or tails the container's journal via `charly logs`.
func (podSubstrateLifecycle) Logs(_ context.Context, name string, _ *BundleNode, opts LogsOpts) error {
	args := []string{"logs", name}
	if opts.Follow {
		args = append(args, "-f")
	}
	if opts.Tail > 0 {
		args = append(args, "-n", fmt.Sprintf("%d", opts.Tail))
	}
	return runCharlySubcommand(args...)
}

// Shell opens an interactive shell in the container via `charly shell` (or runs cmd).
func (podSubstrateLifecycle) Shell(_ context.Context, name string, _ *BundleNode, cmd []string) error {
	args := make([]string, 0, 2+len(cmd))
	args = append(args, "shell", name)
	args = append(args, cmd...)
	return runCharlySubcommand(args...)
}

// Rebuild follows the pod rebuild sequence (the SAME body as the prior PodUnifiedTarget
// .Rebuild): image rebuild (optional) → image check → deploy add → stop → config (regen
// quadlet) → start. This is the path `charly update <pod-bed>` routes through (the
// disposable bed's fresh-rebuild R10 gate). `charly stop` (not `remove`) preserves operator
// charly.yml config during the brief disruption window.
func (podSubstrateLifecycle) Rebuild(_ context.Context, name string, node *BundleNode, opts RebuildOpts) error {
	baseRef := ""
	if node != nil {
		baseRef = node.Image
	}
	if baseRef == "" {
		baseRef = name
	}

	if opts.DryRun {
		if opts.RebuildImage {
			fmt.Printf("dry-run: charly box build %s\n", baseRef)
			fmt.Printf("dry-run: charly check box %s\n", baseRef)
		}
		fmt.Printf("dry-run: charly bundle add %s\n", name)
		fmt.Printf("dry-run: charly stop %s\n", name)
		fmt.Printf("dry-run: charly config %s\n", name)
		fmt.Printf("dry-run: charly start %s\n", name)
		return nil
	}

	if opts.RebuildImage {
		if err := runCharlySubcommand("box", "build", baseRef); err != nil {
			return fmt.Errorf("charly box build %s: %w", baseRef, err)
		}
		if err := runCharlySubcommand("check", "box", baseRef); err != nil {
			return fmt.Errorf("charly check box %s: %w", baseRef, err)
		}
	}

	if err := runCharlySubcommand("bundle", "add", name); err != nil {
		return fmt.Errorf("charly bundle add %s: %w", name, err)
	}

	_ = runCharlySubcommand("stop", name)

	if err := runCharlySubcommand("config", name); err != nil {
		return fmt.Errorf("charly config %s: %w", name, err)
	}

	if err := runCharlySubcommand("start", name); err != nil {
		return fmt.Errorf("charly start %s: %w", name, err)
	}
	return nil
}

// podDeployEngine returns the container engine for a pod deploy node — node.Engine when
// set, else "podman" (the default). Relocated from the deleted PodUnifiedTarget.engine().
func podDeployEngine(node *BundleNode) string {
	if node != nil && node.Engine != "" {
		return node.Engine
	}
	return "podman"
}

// removeDeployOverlayImages best-effort removes the synthesized <deployName>-overlay images
// (all tags) for a pod deploy. Record-free: it queries the engine for images whose
// repository is "<deployName>-overlay" — the OverlayImageRef naming convention
// (deploy_target_pod.go) — so only the deploy-specific overlays are removed; a shared base
// ref is never named that. Relocated from the deleted unified_targets_pod.go.
func removeDeployOverlayImages(engine, deployName string) {
	out, err := exec.Command(EngineBinary(engine), "images",
		"--filter", "reference="+deployName+"-overlay", "--format", "{{.Repository}}:{{.Tag}}").Output()
	if err != nil {
		return
	}
	for _, ref := range strings.Fields(string(out)) {
		_ = exec.Command(EngineBinary(engine), "rmi", ref).Run()
	}
}
