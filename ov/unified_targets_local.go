package main

// unified_targets_host.go — C11 (Phase 3) implementations of
// LocalUnifiedTarget's lifecycle and management methods.
//
// Pattern: methods that map to existing CLI bodies (Del, Rebuild) are
// extracted from their cmd-file homes into target methods; the cmd-file
// entry points become thin wrappers that construct a LocalUnifiedTarget
// and call its method. Methods that don't apply to the host target
// (Start, Stop, Logs) return ErrNotSupportedOnHost — the host is always
// running, has no separate journal we own, and isn't ours to "stop".
// The pattern mirrors ErrNotSupportedOnK8s for k8s targets.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrNotSupportedOnHost is returned by lifecycle methods that have no
// meaning on a host target. The host is always running (you can't
// "start" or "stop" your own machine through ov); ov-managed log
// streams don't apply (logs of "the host" would be the system journal,
// outside ov's contract). Mirrors ErrNotSupportedOnK8s.
var ErrNotSupportedOnHost = errors.New("lifecycle operation not supported on host target")

// hostReverseExec is an inline ReverseExecutor adapter combining a
// LocalUnifiedTarget's gate flags with a per-call DryRun flag from
// DelOpts. Constructed inside Del so the target struct itself doesn't
// have to carry per-invocation state.
type hostReverseExec struct {
	DryRun          bool
	KeepRepoChanges bool
	KeepServices    bool
	Runner          ReverseRunner
}

func (e *hostReverseExec) reverseDryRun() bool          { return e.DryRun }
func (e *hostReverseExec) reverseKeepRepoChanges() bool { return e.KeepRepoChanges }
func (e *hostReverseExec) reverseKeepServices() bool    { return e.KeepServices }
func (e *hostReverseExec) reverseRunner() ReverseRunner { return e.Runner }

// Del tears down every host deploy in the ledger. Body extracted from
// DeployDelCmd.runHostDel — the cmd-file path is now a thin wrapper.
//
// Walks the deploys ledger, decrements layer refcounts, runs ReverseOps
// for layers that drop to refcount=0, and removes deploy + layer
// records. When all host deploys are torn down, also strips the
// shell-profile managed block.
func (t *LocalUnifiedTarget) Del(ctx context.Context, opts DelOpts) error {
	paths, err := DefaultLedgerPaths()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(paths.Deploys)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("No deployments recorded.")
			return nil
		}
		return err
	}

	hostHome := os.Getenv("HOME")
	anyRemoved := false

	re := &hostReverseExec{
		DryRun:          opts.DryRun,
		KeepRepoChanges: t.KeepRepoChanges,
		KeepServices:    t.KeepServices,
		Runner:          t.RevRunner,
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, ferr := os.ReadFile(filepath.Join(paths.Deploys, e.Name()))
		if ferr != nil {
			continue
		}
		var rec DeployRecord
		if jerr := json.Unmarshal(data, &rec); jerr != nil {
			continue
		}
		if rec.Target != "host" {
			continue
		}
		if opts.DryRun {
			fmt.Printf("[dry-run] would tear down host deploy %s (image=%s, %d layers)\n",
				rec.DeployID, rec.Image, len(rec.Layer))
			continue
		}
		if terr := teardownHostDeploy(paths, &rec, hostHome, re); terr != nil {
			return terr
		}
		anyRemoved = true
		fmt.Printf("Removed host deploy %s (%s)\n", rec.DeployID, rec.Image)
	}

	if anyRemoved && !opts.DryRun && !opts.KeepLedger {
		if remainingLayers, _ := os.ReadDir(paths.Layers); len(remainingLayers) == 0 {
			shell := DetectLoginShell()
			_ = RemoveManagedBlock(shell, hostHome)
		}
	}
	return nil
}

// teardownHostDeploy reverses a single host deploy record. Free
// function so LocalUnifiedTarget.Del can call it without a DeployDelCmd
// instance — the legacy DeployDelCmd.tearDownDeploy now delegates here.
func teardownHostDeploy(paths *LedgerPaths, rec *DeployRecord, hostHome string, re ReverseExecutor) error {
	for _, layer := range rec.Layer {
		layerRec, shouldRemove, err := RemoveLayerDeployment(paths, layer, rec.DeployID)
		if err != nil {
			return err
		}
		if !shouldRemove {
			continue
		}
		if err := runReverseOps(layerRec.ReverseOps, re); err != nil {
			return fmt.Errorf("reversing layer %s: %w", layer, err)
		}
		_ = RemoveEnvdFile(hostHome, layer)
		if err := DeleteLayerRecord(paths, layer); err != nil {
			return err
		}
	}
	return DeleteDeployRecord(paths, rec.DeployID)
}

// Test runs deploy-scope checks against the host target. Constructs a
// Runner around the target's executor (ShellExecutor for a
// non-nested host; NestedExecutor for a host-inside-vm child) and walks
// the supplied check list. OnlyIDs/StopOnFail mirror their TestOpts
// semantics; the runner-level matching for verb dispatch is shared with
// `ov eval live`.
func (t *LocalUnifiedTarget) Test(ctx context.Context, checks []Check, opts TestOpts) error {
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
	runner := NewRunner(t.Executor(), nil, RunModeLive)
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
		return fmt.Errorf("%d host check(s) failed", failed)
	}
	return nil
}

// Update re-applies plans against the host target. Idempotent re-apply
// over the existing ledger — equivalent in effect to a fresh Add. A
// future "diff and only apply changed steps" mode would live behind an
// UpdateOpts flag.
func (t *LocalUnifiedTarget) Update(ctx context.Context, plans []*InstallPlan, opts UpdateOpts) error {
	return t.LocalDeployTarget.Emit(plans, EmitOpts{
		DryRun:           opts.DryRun,
		AllowRepoChanges: opts.AllowRepoChanges,
		AllowRootTasks:   opts.AllowRootTasks,
		WithServices:     opts.WithServices,
		AssumeYes:        opts.AssumeYes,
	})
}

// Status reads the ledger and summarizes host-target deploys. "running"
// when at least one host deploy is recorded; "stopped" otherwise. The
// host machine itself is always running; ov-managed presence is the
// signal we report.
func (t *LocalUnifiedTarget) Status(ctx context.Context) (StatusInfo, error) {
	paths, err := DefaultLedgerPaths()
	if err != nil {
		return StatusInfo{}, err
	}
	entries, err := os.ReadDir(paths.Deploys)
	if err != nil {
		if os.IsNotExist(err) {
			return StatusInfo{State: "stopped", Healthy: false}, nil
		}
		return StatusInfo{}, err
	}
	deploys := 0
	totalLayers := 0
	var images []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, ferr := os.ReadFile(filepath.Join(paths.Deploys, e.Name()))
		if ferr != nil {
			continue
		}
		var rec DeployRecord
		if jerr := json.Unmarshal(data, &rec); jerr != nil {
			continue
		}
		if rec.Target != "host" {
			continue
		}
		deploys++
		totalLayers += len(rec.Layer)
		images = append(images, rec.Image)
	}
	state := "stopped"
	if deploys > 0 {
		state = "running"
	}
	return StatusInfo{
		State:   state,
		Healthy: deploys > 0,
		Details: map[string]string{
			"deploys": fmt.Sprintf("%d", deploys),
			"layers":  fmt.Sprintf("%d", totalLayers),
			"images":  strings.Join(images, ","),
		},
	}, nil
}

// Shell runs a command (or an interactive shell when cmd is empty) on
// the host target through its executor. For a non-nested host this is
// just bash on the local machine; for `target: host, inside: vm:foo`
// the shell lands inside the parent VM via NestedExecutor, which is the
// useful case (the local-bash case is already what the user has).
func (t *LocalUnifiedTarget) Shell(ctx context.Context, cmd []string) error {
	var script string
	if len(cmd) > 0 {
		parts := make([]string, len(cmd))
		for i, a := range cmd {
			parts[i] = fmt.Sprintf("%q", a)
		}
		script = strings.Join(parts, " ")
	} else {
		script = "exec ${SHELL:-/bin/bash}"
	}
	return t.Executor().RunUser(ctx, script, EmitOpts{})
}

// Rebuild re-applies the host target's deploys. For host targets,
// "rebuild" is refresh semantics (re-Add over the existing ledger) —
// destruction would reverse repo changes, disable services, and strip
// env.d files the operator explicitly opted into. The Disposable gate
// from RebuildOpts is checked by the caller's disposable-classification
// logic, so this method does not re-validate.
//
// This is the host-deploy rebuild path — the cmd-file
// caller is now a thin wrapper.
func (t *LocalUnifiedTarget) Rebuild(ctx context.Context, opts RebuildOpts) error {
	if opts.DryRun {
		fmt.Printf("dry-run: ov deploy add %s\n", t.NodeName)
		return nil
	}
	return runOvSubcommand("deploy", "add", t.NodeName)
}

// Start, Stop, Logs: not applicable to the host target. The host is
// always running; we don't own its journal. Mirror ErrNotSupportedOnK8s
// pattern for K8sUnifiedTarget.
func (t *LocalUnifiedTarget) Start(ctx context.Context) error {
	return fmt.Errorf("host %q: %w", t.NodeName, ErrNotSupportedOnHost)
}
func (t *LocalUnifiedTarget) Stop(ctx context.Context) error {
	return fmt.Errorf("host %q: %w", t.NodeName, ErrNotSupportedOnHost)
}
func (t *LocalUnifiedTarget) Logs(ctx context.Context, opts LogsOpts) error {
	return fmt.Errorf("host %q: %w", t.NodeName, ErrNotSupportedOnHost)
}
