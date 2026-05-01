package main

// unified_targets_vm.go — C12 (Phase 3) implementations of
// VmUnifiedTarget's lifecycle and management methods.
//
// Pattern mirrors C11's HostUnifiedTarget extraction:
//   - Methods that map to existing CLI bodies (Del, Rebuild) are
//     extracted from their cmd-file homes; cmd-file entry points
//     become thin wrappers.
//   - Lifecycle methods that have a clean ov subcommand surface
//     (Start, Stop, Shell, Logs) shell out via runOvSubcommand —
//     same pattern rebuildVm + rebuildHostDeploy already used. The
//     spawned child uses the same binary on $PATH, so a developer
//     install picks up the local build automatically.
//   - Test mirrors HostUnifiedTarget.Test: a Runner over the target's
//     SSHExecutor walks the supplied checks.
//   - Status reads ov vm list output for the specific VM.
//
// Unlike Host, every lifecycle method makes sense on a VM target:
// there's a real VM to start/stop/console-into. So no ErrNotSupportedOnVM
// sentinel — every method has a meaningful body.

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// Del tears down a VM deploy: walks the host ledger, runs guest-side
// ReverseOps over SSH (via sshReverseRunner), removes the deploy.yml
// vm: entry. Body extracted from DeployDelCmd.runVmDel — the cmd-file
// runVmDel is now a thin wrapper.
//
// The SSHExecutor used for ReverseOps comes from buildVmReverseRunner
// against the VM's persisted deploy state (deploy.yml's vm_state block).
// The unified target doesn't carry that state directly; the caller (the
// thin runVmDel wrapper) supplies a pre-built ReverseRunner via
// VmUnifiedTarget.RevRunner so this method stays pure.
func (t *VmUnifiedTarget) Del(ctx context.Context, opts DelOpts) error {
	paths, err := DefaultLedgerPaths()
	if err != nil {
		return err
	}
	rec, err := findVmDeployRecord(paths, t.NodeName)
	if err != nil {
		return err
	}
	if rec == nil {
		// No ledger record → nothing to reverse on the guest. Still
		// clean up the deploy.yml entry if present.
		if entryErr := removeVmDeployEntry(t.NodeName); entryErr != nil {
			fmt.Fprintf(os.Stderr, "note: deploy.yml cleanup: %v\n", entryErr)
		}
		fmt.Fprintf(os.Stderr, "No VM deploy ledger entry for %s (already torn down?)\n", t.NodeName)
		return nil
	}
	if opts.DryRun {
		fmt.Printf("[dry-run] would tear down VM deploy %s (deploy_id=%s, %d layers)\n",
			t.NodeName, rec.DeployID, len(rec.Layers))
		for _, layer := range rec.Layers {
			layerRec, lerr := ReadLayerRecord(paths, layer)
			if lerr != nil || layerRec == nil {
				continue
			}
			for _, op := range layerRec.ReverseOps {
				fmt.Printf("  - %s %v\n", op.Kind, op.Targets)
			}
		}
		return nil
	}

	if t.RevRunner == nil {
		// Caller didn't pre-build the runner — build it ourselves from
		// the persisted deploy state. Path mirrors runVmDel's setup.
		runner, rerr := buildVmReverseRunner(t.NodeName)
		if rerr != nil {
			return fmt.Errorf("building VM reverse runner: %w", rerr)
		}
		t.RevRunner = runner
	}
	re := &vmReverseExec{
		DryRun:          opts.DryRun,
		KeepRepoChanges: t.KeepRepoChanges,
		KeepServices:    t.KeepServices,
		Runner:          t.RevRunner,
	}

	for _, layer := range rec.Layers {
		layerRec, shouldRemove, lerr := RemoveLayerDeployment(paths, layer, rec.DeployID)
		if lerr != nil {
			return fmt.Errorf("removing layer deployment %s: %w", layer, lerr)
		}
		if !shouldRemove {
			continue
		}
		if rerr := runReverseOps(layerRec.ReverseOps, re); rerr != nil {
			return fmt.Errorf("reversing layer %s: %w", layer, rerr)
		}
		_ = t.RevRunner.RunUser(fmt.Sprintf(`rm -f "$HOME/.config/overthink/env.d/%s.env"`, layer))
		if derr := DeleteLayerRecord(paths, layer); derr != nil {
			return fmt.Errorf("deleting layer record %s: %w", layer, derr)
		}
	}

	if derr := DeleteDeployRecord(paths, rec.DeployID); derr != nil {
		return fmt.Errorf("deleting deploy record: %w", derr)
	}

	// Ephemeral lifecycle teardown — same path runVmDel takes.
	if dc, _ := LoadDeployConfig(); dc != nil {
		if node, ok := dc.Deployment[t.NodeName]; ok && node.IsEphemeral() {
			if tdErr := TeardownEphemeralLifecycle(&node, t.NodeName); tdErr != nil {
				fmt.Fprintf(os.Stderr, "warning: ephemeral lifecycle teardown: %v\n", tdErr)
			}
		}
	}

	if rerr := removeVmDeployEntry(t.NodeName); rerr != nil {
		fmt.Fprintf(os.Stderr, "note: deploy.yml cleanup: %v\n", rerr)
	}
	return nil
}

// vmReverseExec is the VM equivalent of hostReverseExec — combines the
// target's gate flags with per-call DryRun. Identical shape; kept
// separate so a future divergence (VM-specific gates) doesn't ripple
// into Host code.
type vmReverseExec struct {
	DryRun          bool
	KeepRepoChanges bool
	KeepServices    bool
	Runner          ReverseRunner
}

func (e *vmReverseExec) reverseDryRun() bool          { return e.DryRun }
func (e *vmReverseExec) reverseKeepRepoChanges() bool { return e.KeepRepoChanges }
func (e *vmReverseExec) reverseKeepServices() bool    { return e.KeepServices }
func (e *vmReverseExec) reverseRunner() ReverseRunner { return e.Runner }

// Test runs deploy-scope checks against the live VM via its SSHExecutor.
// Mirrors HostUnifiedTarget.Test — only the executor differs.
func (t *VmUnifiedTarget) Test(ctx context.Context, checks []Check, opts TestOpts) error {
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
		return fmt.Errorf("vm %q: no SSHExecutor configured", t.NodeName)
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
		return fmt.Errorf("%d vm check(s) failed", failed)
	}
	return nil
}

// Update re-applies plans against the VM via SSH. Idempotent re-apply
// over the existing guest ledger — equivalent to a fresh Add. Future
// "diff and only apply changed steps" mode would live behind an
// UpdateOpts flag.
func (t *VmUnifiedTarget) Update(ctx context.Context, plans []*InstallPlan, opts UpdateOpts) error {
	if t.VmDeployTarget == nil {
		return fmt.Errorf("vm %q: VmDeployTarget is nil", t.NodeName)
	}
	return t.VmDeployTarget.Emit(plans, EmitOpts{
		DryRun:           opts.DryRun,
		AllowRepoChanges: opts.AllowRepoChanges,
		AllowRootTasks:   opts.AllowRootTasks,
		WithServices:     opts.WithServices,
		AssumeYes:        opts.AssumeYes,
	})
}

// Start boots the VM via the existing `ov vm start` subcommand. Sub-
// process spawn matches the rebuildVm pattern; the spawned child uses
// the same binary the parent was invoked from.
func (t *VmUnifiedTarget) Start(ctx context.Context) error {
	return runOvSubcommand("vm", "start", t.vmEntityName())
}

// Stop graceful-shutdowns the VM via `ov vm stop`.
func (t *VmUnifiedTarget) Stop(ctx context.Context) error {
	return runOvSubcommand("vm", "stop", t.vmEntityName())
}

// Status reads `ov vm list` output and walks for this target's domain
// name. Returns a typed StatusInfo regardless of which backend
// (libvirt/qemu) is in use — the list output normalizes them. Falls
// back to "stopped" when the VM isn't in the list (no domain created
// yet). Returns "unknown" + the captured error on a real CLI failure.
func (t *VmUnifiedTarget) Status(ctx context.Context) (StatusInfo, error) {
	want := t.vmDomainName()
	out, err := captureOvStdout("vm", "list")
	if err != nil {
		return StatusInfo{State: "unknown"}, err
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		if fields[0] != want {
			continue
		}
		state := fields[len(fields)-1]
		return StatusInfo{
			State:   state,
			Healthy: state == "running",
			Details: map[string]string{
				"backend": fields[1],
				"domain":  fields[0],
			},
		}, nil
	}
	return StatusInfo{State: "stopped", Healthy: false}, nil
}

// Logs streams the VM's serial console via `ov vm console`. Follow=true
// keeps streaming; follow=false captures whatever is in the console
// buffer and returns. Tail is currently ignored — console buffers
// don't expose a per-line tail.
func (t *VmUnifiedTarget) Logs(ctx context.Context, opts LogsOpts) error {
	return runOvSubcommand("vm", "console", t.vmEntityName())
}

// Shell sshes into the VM via `ov vm ssh`. With cmd, runs it non-
// interactively and returns. Without cmd, opens an interactive session.
func (t *VmUnifiedTarget) Shell(ctx context.Context, cmd []string) error {
	args := []string{"vm", "ssh", t.vmEntityName()}
	args = append(args, cmd...)
	return runOvSubcommand(args...)
}

// Rebuild destroys + (optionally) rebuilds the disk image + recreates +
// starts the VM. Body extracted from RebuildCmd.rebuildVm. The
// disposable check is the caller's responsibility (rebuild.go's
// classification); this method does not re-validate.
func (t *VmUnifiedTarget) Rebuild(ctx context.Context, opts RebuildOpts) error {
	name := t.vmEntityName()
	if opts.DryRun {
		fmt.Printf("dry-run: ov vm destroy %s\n", name)
		if opts.RebuildImage {
			fmt.Printf("dry-run: ov vm build %s\n", name)
		}
		fmt.Printf("dry-run: ov vm create %s\n", name)
		fmt.Printf("dry-run: ov vm start %s\n", name)
		return nil
	}
	// Destroy is best-effort — the VM may not exist yet on a first build.
	_ = runOvSubcommand("vm", "destroy", name)
	if opts.RebuildImage {
		if err := runOvSubcommand("vm", "build", name); err != nil {
			return fmt.Errorf("ov vm build %s: %w", name, err)
		}
	}
	if err := runOvSubcommand("vm", "create", name); err != nil {
		return fmt.Errorf("ov vm create %s: %w", name, err)
	}
	stderr, startErr := runOvSubcommandCapture("vm", "start", name)
	if startErr != nil {
		if isBenignAlreadyRunning(stderr) {
			return nil
		}
		fmt.Fprint(os.Stderr, stderr)
		return fmt.Errorf("ov vm start %s: %w", name, startErr)
	}
	if stderr != "" {
		fmt.Fprint(os.Stderr, stderr)
	}
	return nil
}

// vmEntityName returns the name to pass to `ov vm <verb>` — the
// kind:vm entity name. Defaults to NodeName; when VmDeployTarget is
// embedded with a populated VMName, prefers that (the deploy.yml
// node's `vm:` cross-ref is the canonical mapping).
func (t *VmUnifiedTarget) vmEntityName() string {
	if t.VmDeployTarget != nil && t.VMName != "" {
		return t.VMName
	}
	return t.NodeName
}

// vmDomainName returns the libvirt/qemu domain name. Convention:
// "ov-<entity>" with optional "-<instance>" suffix. Mirrors the
// vmName() helper used by VmStartCmd et al.
func (t *VmUnifiedTarget) vmDomainName() string {
	return "ov-" + t.vmEntityName()
}
