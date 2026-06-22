package main

// unified_targets_pod.go — PodUnifiedTarget's Add + lifecycle and
// management methods.
//
//   - Add: builds the overlay image (Generator + PodDeployTarget) and
//     persists the disposable/lifecycle classification.
//   - Del: stops + removes the container deploy + overlay image + ledger.
//   - Rebuild: the pod rebuild path (image build + check + deploy + restart).
//   - Lifecycle methods (Start, Stop, Status, Logs, Shell) shell out via
//     runCharlySubcommand to the CLI surfaces (charly start / charly stop / charly status
//     / charly logs / charly shell). The spawned child uses the same binary on
//     $PATH, so a developer install picks up the local build.
//   - Test runs deploy-scope checks via a Runner over the target's
//     podman-exec executor.
//   - Update shells out to `charly update` (the image-update path).
//
// Like VM (and unlike Local), every lifecycle method makes sense on a
// pod target — no ErrNotSupportedOnPod sentinel needed.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Del tears the pod deploy down. Pods carry NO ledger DeployRecord — only VM/local
// deploys (which apply candies to a filesystem/guest) write one; a pod bakes its
// candies into an overlay image and persists charly.yml state. So teardown is
// RECORD-FREE: it delegates the container + quadlet + sidecar + charly.yml cleanup to
// the canonical `charly remove` path (consistent with the sibling lifecycle
// delegations Start/Stop/Update), then drops the deploy's synthesized overlay image
// (bundle del's one extra over `charly remove`) and cancels any ephemeral TTL lifecycle.
//
// KeepImage isn't on DelOpts (the unified type is uniform across kinds); the
// dispatcher passes it via the target's KeepImage field instead.
func (t *PodUnifiedTarget) Del(ctx context.Context, opts DelOpts) error {
	if opts.DryRun {
		fmt.Printf("[dry-run] charly remove %s + drop %s-overlay:* images (keep-image=%v)\n",
			t.NodeName, t.NodeName, t.KeepImage)
		return nil
	}
	// Canonical, record-free pod teardown (container + quadlet + sidecars + charly.yml).
	if err := runCharlySubcommand("remove", t.NodeName); err != nil {
		return err
	}
	// bundle del's one extra over `charly remove`: drop the synthesized, deploy-specific
	// <name>-overlay:* images. A shared base ref is never named that, so it is preserved.
	if !t.KeepImage {
		removeDeployOverlayImages(t.engine(), t.NodeName)
	}
	// Cancel an ephemeral TTL lifecycle if this deploy was marked ephemeral.
	if node, ok := loadDeployConfigForRead("pod bundle-del ephemeral-teardown").LookupKey(t.NodeName); ok && node.IsEphemeral() {
		if tdErr := TeardownEphemeralLifecycle(&node, t.NodeName); tdErr != nil {
			fmt.Fprintf(os.Stderr, "warning: ephemeral lifecycle teardown: %v\n", tdErr)
		}
	}
	return nil
}

// removeDeployOverlayImages best-effort removes the synthesized <deployName>-overlay
// images (all tags) for a pod deploy. Record-free: it queries the engine for images
// whose repository is "<deployName>-overlay" — the OverlayImageRef naming convention
// (deploy_target_pod.go) — so only the deploy-specific overlays are removed; a shared
// base ref is never named that.
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

// engine returns the configured engine. Defaults to "podman" when the
// embedded PodDeployTarget is nil or its Engine field is empty.
func (t *PodUnifiedTarget) engine() string {
	if t.PodDeployTarget != nil && t.Engine != "" {
		return t.Engine
	}
	return "podman"
}

// Test runs deploy-scope checks against the live container via its
// executor (podman-exec wrapper). Mirrors LocalUnifiedTarget.Test +
// VmUnifiedTarget.Test — only the executor differs.
func (t *PodUnifiedTarget) Test(ctx context.Context, checks []Op, opts TestOpts) error {
	return runUnifiedTargetChecks(ctx, t.Executor(), t.Kind(), t.NodeName, checks, opts)
}

// Update shells out to `charly update <name>`. charly update handles the
// image-pull + quadlet-regen + restart sequence already; the unified
// target wraps it so callers using the unified surface don't need to
// know the legacy command name.
func (t *PodUnifiedTarget) Update(ctx context.Context, plans []*InstallPlan, opts UpdateOpts) error {
	if opts.DryRun {
		fmt.Printf("dry-run: charly update %s\n", t.NodeName)
		return nil
	}
	return runCharlySubcommand("update", t.NodeName)
}

// Start brings the container deploy up via `charly start`.
func (t *PodUnifiedTarget) Start(ctx context.Context) error {
	return runCharlySubcommand("start", t.NodeName)
}

// Stop brings the container deploy down via `charly stop`.
func (t *PodUnifiedTarget) Stop(ctx context.Context) error {
	return runCharlySubcommand("stop", t.NodeName)
}

// Status parses `charly status --json` output for this deploy's row. We use
// the JSON form to avoid depending on the human table layout, which
// changes more often than the JSON keys.
func (t *PodUnifiedTarget) Status(ctx context.Context) (StatusInfo, error) {
	out, err := captureCharlyStdout("status", "--json")
	if err != nil {
		return StatusInfo{State: "unknown"}, err
	}
	// Best-effort parsing: scan for our deploy name and a state token.
	// Avoiding the full JSON unmarshal here keeps the unified surface
	// from coupling to the (still-evolving) status JSON schema.
	for line := range strings.SplitSeq(out, "\n") {
		if !strings.Contains(line, t.NodeName) {
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
			Details: map[string]string{"deploy": t.NodeName},
		}, nil
	}
	return StatusInfo{State: "stopped", Healthy: false}, nil
}

// Logs streams or tails the container's journal via `charly logs`.
// Follow=true wires the -f flag through; Tail sets -n.
func (t *PodUnifiedTarget) Logs(ctx context.Context, opts LogsOpts) error {
	args := []string{"logs", t.NodeName}
	if opts.Follow {
		args = append(args, "-f")
	}
	if opts.Tail > 0 {
		args = append(args, "-n", fmt.Sprintf("%d", opts.Tail))
	}
	return runCharlySubcommand(args...)
}

// Shell opens an interactive shell in the container via `charly shell`.
// With cmd, runs it non-interactively.
func (t *PodUnifiedTarget) Shell(ctx context.Context, cmd []string) error {
	args := make([]string, 0, 2+len(cmd))
	args = append(args, "shell", t.NodeName)
	args = append(args, cmd...)
	return runCharlySubcommand(args...)
}

// Rebuild follows the standard pod rebuild sequence: image rebuild
// (optional) → image check → deploy add → stop → config (regen
// quadlet) → start. This is the pod rebuild path
// — the cmd-file caller is now a thin wrapper.
//
// Per the existing rebuild semantics, `charly stop` is used (not `charly
// remove`) to preserve operator charly.yml configuration during the
// brief disruption window.
func (t *PodUnifiedTarget) Rebuild(ctx context.Context, opts RebuildOpts) error {
	baseRef := t.BaseImageRef
	if baseRef == "" {
		baseRef = t.NodeName
	}

	if opts.DryRun {
		if opts.RebuildImage {
			fmt.Printf("dry-run: charly box build %s\n", baseRef)
			fmt.Printf("dry-run: charly check box %s\n", baseRef)
		}
		fmt.Printf("dry-run: charly bundle add %s\n", t.NodeName)
		fmt.Printf("dry-run: charly stop %s\n", t.NodeName)
		fmt.Printf("dry-run: charly config %s\n", t.NodeName)
		fmt.Printf("dry-run: charly start %s\n", t.NodeName)
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

	if err := runCharlySubcommand("bundle", "add", t.NodeName); err != nil {
		return fmt.Errorf("charly bundle add %s: %w", t.NodeName, err)
	}

	_ = runCharlySubcommand("stop", t.NodeName)

	if err := runCharlySubcommand("config", t.NodeName); err != nil {
		return fmt.Errorf("charly config %s: %w", t.NodeName, err)
	}

	if err := runCharlySubcommand("start", t.NodeName); err != nil {
		return fmt.Errorf("charly start %s: %w", t.NodeName, err)
	}
	return nil
}

// Add builds the pod (container) overlay: synthesizes a Containerfile
// (FROM base + the add_candy: build steps) and a deterministic overlay
// image. Constructs the live PodDeployTarget (Generator + ResolvedBox +
// base-image DistroDef + baseRef CalVer), injects candy secrets, emits,
// persists --disposable/--lifecycle, and prints the `charly start` hint.
//
// node fields come from dctx.Node (the dispatch-merged node) — the
// ephemeral check consumes it directly rather than re-reading charly.yml.
func (t *PodUnifiedTarget) Add(ctx context.Context, dctx *DeployContext, plans []*InstallPlan, opts EmitOpts) error {
	node := dctx.Node
	dir := dctx.Dir
	base := dctx.Base

	// Ephemeral lifecycle hook (FIRST action — panic-safe TTL ordering).
	// Consumes the MERGED node (never a charly.yml re-read).
	registerEphemeralIfMarked(node, t.NodeName)

	// Build a Generator + ResolvedBox so the overlay's OCITarget renders
	// tasks as RUN directives (not comments).
	gen, _ := NewGenerator(dir, t.Tag, ResolveOpts{})
	var resolvedImg *ResolvedBox
	if gen != nil && gen.Boxes != nil {
		resolvedImg = gen.Boxes[base]
	}

	// Resolve DistroDef from the BASE IMAGE's distro, not the operator
	// host's — the overlay's SystemPackagesSteps render using the base
	// image's package format (fedora → rpm).
	var podDistroDef *DistroDef
	if resolvedImg != nil && len(resolvedImg.Distro) > 0 {
		podDistroDef = resolveDistroDef(dctx.DistroCfg, resolvedImg.Distro[0])
	} else {
		podDistroDef = resolveDistroDef(dctx.DistroCfg, detectHostContext().Distro)
	}

	// Build the BaseImage ref. With CalVer-only resolution, an empty Tag
	// resolves to the newest local CalVer so the overlay's FROM line gets
	// a real tag (never a trailing colon).
	var baseRef string
	if t.Tag != "" {
		baseRef = base + ":" + t.Tag
	} else if resolved, rerr := ResolveNewestLocalCalVer("podman", base); rerr == nil && resolved != "" {
		baseRef = resolved
	} else {
		baseRef = base
	}

	tgt := &PodDeployTarget{
		DeployName:    t.NodeName,
		BaseImage:     baseRef,
		DistroDef:     podDistroDef,
		BuilderConfig: dctx.BuilderCfg,
		Generator:     gen,
		Box:           resolvedImg,
	}

	// Resolve + inject candy secrets so the overlay Containerfile emits
	// `export VAR=VALUE` before each task body (R3 shared helper).
	if _, _, err := prepareCandySecrets(plans, dir); err != nil {
		return fmt.Errorf("loading candies for secret resolution: %w", err)
	}

	// Thread ParentExec: when this container is a child of another
	// deployment, the overlay build runs in the parent's venue.
	if opts.ParentExec != nil {
		tgt.Executor = opts.ParentExec
	}
	if err := tgt.Emit(plans, opts); err != nil {
		return err
	}

	// Persist classification flags into charly.yml when the user passed
	// --disposable / --lifecycle.
	if t.Disposable || t.Lifecycle != "" {
		saveDeployState(t.NodeName, "", SaveDeployStateInput{
			SetDisposable: t.Disposable,
			Disposable:    t.Disposable,
			SetLifecycle:  t.Lifecycle != "",
			Lifecycle:     t.Lifecycle,
			Box:           t.Ref,
			Target:        "pod",
		})
		if t.Disposable {
			fmt.Fprintln(os.Stderr, "Marked deploy disposable — `charly update` will act unattended on this deploy.")
		}
	}
	fmt.Printf("Overlay image ready: %s\n", tgt.OverlayImageRef())
	fmt.Println("To start the container, run: charly start " + t.NodeName)
	return nil
}
