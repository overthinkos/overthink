package main

// unified_targets_pod.go — PodUnifiedTarget's Add + lifecycle and
// management methods.
//
//   - Add: builds the overlay image (Generator + PodDeployTarget) and
//     persists the disposable/lifecycle classification.
//   - Del: stops + removes the container deploy + overlay image + ledger.
//   - Rebuild: the pod rebuild path (image build + eval + deploy + restart).
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
	"strings"
)

// Del stops + removes the container deploy, removes the overlay image
// (unless KeepImage is set), and cleans up the ledger entry.
//
// KeepImage isn't on DelOpts (the unified type is uniform across kinds);
// the dispatcher passes it via the target's KeepImage field instead.
func (t *PodUnifiedTarget) Del(ctx context.Context, opts DelOpts) error {
	paths, err := DefaultLedgerPaths()
	if err != nil {
		return err
	}
	rec, err := findContainerDeploy(paths, t.NodeName)
	if err != nil {
		return err
	}
	if rec == nil {
		return fmt.Errorf("no container deploy named %q in ledger", t.NodeName)
	}
	if opts.DryRun {
		fmt.Printf("[dry-run] would stop container %s, remove image %s (keep=%v)\n",
			t.NodeName, rec.Image, t.KeepImage)
		return nil
	}
	engine := t.engine()
	_ = runPodmanCommand(engine, "stop", t.NodeName)
	_ = runPodmanCommand(engine, "rm", "-f", t.NodeName)

	// Overlay image cleanup — only attempted for the synthesized
	// <name>-overlay images; non-overlay images (like a base ref) are
	// preserved so a re-add doesn't have to repull/rebuild.
	overlayRef := rec.Image
	if !t.KeepImage && strings.HasSuffix(overlayRef, "-overlay") {
		_ = runPodmanCommand(engine, "rmi", overlayRef)
	}

	for _, layer := range rec.Candy {
		_, shouldRemove, lerr := RemoveCandyDeployment(paths, layer, rec.DeployID)
		if lerr != nil {
			return lerr
		}
		if shouldRemove {
			_ = DeleteCandyRecord(paths, layer)
		}
	}

	if err := DeleteDeployRecord(paths, rec.DeployID); err != nil {
		return err
	}

	if node, ok := loadDeployConfigForRead("pod target ephemeral-teardown").LookupKey(t.NodeName); ok && node.IsEphemeral() {
		if tdErr := TeardownEphemeralLifecycle(&node, t.NodeName); tdErr != nil {
			fmt.Fprintf(os.Stderr, "warning: ephemeral lifecycle teardown: %v\n", tdErr)
		}
	}

	fmt.Printf("Removed container deploy %s\n", t.NodeName)
	return nil
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
func (t *PodUnifiedTarget) Test(ctx context.Context, checks []Check, opts TestOpts) error {
	onlyIDs := make(map[string]bool, len(opts.OnlyIDs))
	for _, id := range opts.OnlyIDs {
		onlyIDs[id] = true
	}
	filtered := checks
	if len(onlyIDs) > 0 {
		filtered = filtered[:0]
		for _, c := range checks {
			if onlyIDs[c.ID] {
				filtered = append(filtered, c)
			}
		}
	}
	exec := t.Executor()
	if exec == nil {
		return fmt.Errorf("pod %q: no executor configured", t.NodeName)
	}
	runner := NewRunner(exec, nil, RunModeLive)
	results := runner.Run(ctx, filtered)
	failed := 0
	for _, r := range results {
		if r.Status == TestFail {
			failed++
			id := ""
			if r.Check != nil {
				id = r.Check.ID
			}
			fmt.Fprintf(os.Stderr, "FAIL %s: %s\n", id, r.Message)
			if opts.StopOnFail {
				return fmt.Errorf("test stopped at first failure: %s", id)
			}
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d pod check(s) failed", failed)
	}
	return nil
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
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, t.NodeName) {
			continue
		}
		state := "stopped"
		if strings.Contains(line, "running") {
			state = "running"
		} else if strings.Contains(line, "paused") {
			state = "paused"
		} else if strings.Contains(line, "crashed") {
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
	args := []string{"shell", t.NodeName}
	args = append(args, cmd...)
	return runCharlySubcommand(args...)
}

// Rebuild follows the standard pod rebuild sequence: image rebuild
// (optional) → image eval → deploy add → stop → config (regen
// quadlet) → start. This is the pod rebuild path
// — the cmd-file caller is now a thin wrapper.
//
// Per the existing rebuild semantics, `charly stop` is used (not `charly
// remove`) to preserve operator deploy.yml configuration during the
// brief disruption window.
func (t *PodUnifiedTarget) Rebuild(ctx context.Context, opts RebuildOpts) error {
	baseRef := t.BaseImageRef
	if baseRef == "" {
		baseRef = t.NodeName
	}

	if opts.DryRun {
		if opts.RebuildImage {
			fmt.Printf("dry-run: charly box build %s\n", baseRef)
			fmt.Printf("dry-run: charly eval box %s\n", baseRef)
		}
		fmt.Printf("dry-run: charly deploy add %s\n", t.NodeName)
		fmt.Printf("dry-run: charly stop %s\n", t.NodeName)
		fmt.Printf("dry-run: charly config %s\n", t.NodeName)
		fmt.Printf("dry-run: charly start %s\n", t.NodeName)
		return nil
	}

	if opts.RebuildImage {
		if err := runCharlySubcommand("box", "build", baseRef); err != nil {
			return fmt.Errorf("charly box build %s: %w", baseRef, err)
		}
		if err := runCharlySubcommand("eval", "box", baseRef); err != nil {
			return fmt.Errorf("charly eval box %s: %w", baseRef, err)
		}
	}

	if err := runCharlySubcommand("deploy", "add", t.NodeName); err != nil {
		return fmt.Errorf("charly deploy add %s: %w", t.NodeName, err)
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
// (FROM base + the add_layer: build steps) and a deterministic overlay
// image. Constructs the live PodDeployTarget (Generator + ResolvedBox +
// base-image DistroDef + baseRef CalVer), injects layer secrets, emits,
// persists --disposable/--lifecycle, and prints the `charly start` hint.
//
// node fields come from dctx.Node (the dispatch-merged node) — the
// ephemeral check consumes it directly rather than re-reading deploy.yml.
func (t *PodUnifiedTarget) Add(ctx context.Context, dctx *DeployContext, plans []*InstallPlan, opts EmitOpts) error {
	node := dctx.Node
	dir := dctx.Dir
	base := dctx.Base

	// Ephemeral lifecycle hook (FIRST action — panic-safe TTL ordering).
	// Consumes the MERGED node (never a deploy.yml re-read).
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

	// Resolve + inject layer secrets so the overlay Containerfile emits
	// `export VAR=VALUE` before each task body (R3 shared helper).
	if _, _, err := prepareCandySecrets(plans, dir); err != nil {
		return fmt.Errorf("loading layers for secret resolution: %w", err)
	}

	// Thread ParentExec: when this container is a child of another
	// deployment, the overlay build runs in the parent's venue.
	if opts.ParentExec != nil {
		tgt.Executor = opts.ParentExec
	}
	if err := tgt.Emit(plans, opts); err != nil {
		return err
	}

	// Persist classification flags into deploy.yml when the user passed
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
