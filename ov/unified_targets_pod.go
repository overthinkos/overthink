package main

// unified_targets_pod.go — C13 (Phase 3) implementations of
// PodUnifiedTarget's lifecycle and management methods.
//
// Pattern mirrors C11 (Host) and C12 (VM) extractions:
//   - Del: extracted from DeployDelCmd.runContainerDel; cmd-file becomes
//     a thin wrapper.
//   - Rebuild: extracted from RebuildCmd.rebuildContainerDeploy; cmd-
//     file becomes a thin wrapper.
//   - Lifecycle methods (Start, Stop, Status, Logs, Shell) shell out via
//     runOvSubcommand to the existing CLI surfaces (ov start / ov stop /
//     ov status / ov logs / ov shell). The spawned child uses the same
//     binary on $PATH, so a developer install picks up the local build.
//   - Test reuses the host pattern: a Runner over the target's executor
//     walks deploy-scope checks. For pod targets the executor is a
//     podman-exec wrapper.
//   - Update shells out to `ov update` (today's image-update path).
//
// Like VM (and unlike Host), every lifecycle method makes sense on a
// pod target — no ErrNotSupportedOnPod sentinel needed.

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// Del stops + removes the container deploy, removes the overlay image
// (unless KeepImage is set), and cleans up the ledger entry. Body
// extracted from DeployDelCmd.runContainerDel — the cmd-file caller is
// now a thin wrapper.
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

	for _, layer := range rec.Layer {
		_, shouldRemove, lerr := RemoveLayerDeployment(paths, layer, rec.DeployID)
		if lerr != nil {
			return lerr
		}
		if shouldRemove {
			_ = DeleteLayerRecord(paths, layer)
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

// Update shells out to `ov update <name>`. ov update handles the
// image-pull + quadlet-regen + restart sequence already; the unified
// target wraps it so callers using the unified surface don't need to
// know the legacy command name.
func (t *PodUnifiedTarget) Update(ctx context.Context, plans []*InstallPlan, opts UpdateOpts) error {
	if opts.DryRun {
		fmt.Printf("dry-run: ov update %s\n", t.NodeName)
		return nil
	}
	return runOvSubcommand("update", t.NodeName)
}

// Start brings the container deploy up via `ov start`.
func (t *PodUnifiedTarget) Start(ctx context.Context) error {
	return runOvSubcommand("start", t.NodeName)
}

// Stop brings the container deploy down via `ov stop`.
func (t *PodUnifiedTarget) Stop(ctx context.Context) error {
	return runOvSubcommand("stop", t.NodeName)
}

// Status parses `ov status --json` output for this deploy's row. We use
// the JSON form to avoid depending on the human table layout, which
// changes more often than the JSON keys.
func (t *PodUnifiedTarget) Status(ctx context.Context) (StatusInfo, error) {
	out, err := captureOvStdout("status", "--json")
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

// Logs streams or tails the container's journal via `ov logs`.
// Follow=true wires the -f flag through; Tail sets -n.
func (t *PodUnifiedTarget) Logs(ctx context.Context, opts LogsOpts) error {
	args := []string{"logs", t.NodeName}
	if opts.Follow {
		args = append(args, "-f")
	}
	if opts.Tail > 0 {
		args = append(args, "-n", fmt.Sprintf("%d", opts.Tail))
	}
	return runOvSubcommand(args...)
}

// Shell opens an interactive shell in the container via `ov shell`.
// With cmd, runs it non-interactively.
func (t *PodUnifiedTarget) Shell(ctx context.Context, cmd []string) error {
	args := []string{"shell", t.NodeName}
	args = append(args, cmd...)
	return runOvSubcommand(args...)
}

// Rebuild follows the standard pod rebuild sequence: image rebuild
// (optional) → image eval → deploy add → stop → config (regen
// quadlet) → start. Body extracted from RebuildCmd.rebuildContainerDeploy
// — the cmd-file caller is now a thin wrapper.
//
// Per the existing rebuild semantics, `ov stop` is used (not `ov
// remove`) to preserve operator deploy.yml configuration during the
// brief disruption window.
func (t *PodUnifiedTarget) Rebuild(ctx context.Context, opts RebuildOpts) error {
	baseRef := t.BaseImageRef
	if baseRef == "" {
		baseRef = t.NodeName
	}

	if opts.DryRun {
		if opts.RebuildImage {
			fmt.Printf("dry-run: ov image build %s\n", baseRef)
			fmt.Printf("dry-run: ov eval image %s\n", baseRef)
		}
		fmt.Printf("dry-run: ov deploy add %s\n", t.NodeName)
		fmt.Printf("dry-run: ov stop %s\n", t.NodeName)
		fmt.Printf("dry-run: ov config %s\n", t.NodeName)
		fmt.Printf("dry-run: ov start %s\n", t.NodeName)
		return nil
	}

	if opts.RebuildImage {
		if err := runOvSubcommand("image", "build", baseRef); err != nil {
			return fmt.Errorf("ov image build %s: %w", baseRef, err)
		}
		if err := runOvSubcommand("eval", "image", baseRef); err != nil {
			return fmt.Errorf("ov eval image %s: %w", baseRef, err)
		}
	}

	if err := runOvSubcommand("deploy", "add", t.NodeName); err != nil {
		return fmt.Errorf("ov deploy add %s: %w", t.NodeName, err)
	}

	_ = runOvSubcommand("stop", t.NodeName)

	if err := runOvSubcommand("config", t.NodeName); err != nil {
		return fmt.Errorf("ov config %s: %w", t.NodeName, err)
	}

	if err := runOvSubcommand("start", t.NodeName); err != nil {
		return fmt.Errorf("ov start %s: %w", t.NodeName, err)
	}
	return nil
}
