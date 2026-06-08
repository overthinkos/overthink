package main

// walk_plan.go — shared InstallPlan walker used by every DeployTarget.
//
// Before this file, LocalDeployTarget.emitPlan (deploy_target_host.go:156)
// and VmDeployTarget.emitPlan (deploy_target_vm.go:201) each maintained
// their own switch-over-step-kind. The two paths had diverged subtly:
// host batched by (Scope, Venue) via plan.StepsByVenue(), vm iterated
// plan.Steps directly; host dispatched through execStep(), vm dispatched
// inline; vm collected ReverseOps into the LayerRecord, host did not.
//
// WalkPlan here consolidates the dispatch table. Targets implement
// StepExecutor (one method per InstallStep kind); WalkPlan handles the
// iteration order, gate checks, and ReverseOp collection uniformly.
//
// Phase 1 (this commit): WalkPlan defined; nothing calls it yet.
// Phase 2: Host/Vm/Pod targets implement StepExecutor; their Add()
//   methods call WalkPlan. Host's batch-by-venue grouping is preserved
//   by having the StepExecutor buffer shell bodies internally — the
//   IR-level ordering WalkPlan walks is compatible with venue batching.

import (
	"context"
	"fmt"
	"os"
	"time"
)

// StepExecutor is the per-target handler for each InstallStep kind.
// Every target implements all eight methods; WalkPlan dispatches each
// step in a plan to the matching method.
//
// Returning an error aborts the plan walk and propagates to the caller.
// Returning nil records ReverseOps from step.Reverse() into the
// LayerRecord so `charly deploy del` can replay them.
type StepExecutor interface {
	ExecSystemPackages(ctx context.Context, s *SystemPackagesStep, plan *InstallPlan, opts EmitOpts) error
	ExecTask(ctx context.Context, s *TaskStep, plan *InstallPlan, opts EmitOpts) error
	ExecFile(ctx context.Context, s *FileStep, plan *InstallPlan, opts EmitOpts) error
	ExecShellHook(ctx context.Context, s *ShellHookStep, plan *InstallPlan, opts EmitOpts) error
	ExecRepoChange(ctx context.Context, s *RepoChangeStep, plan *InstallPlan, opts EmitOpts) error
	ExecServicePackaged(ctx context.Context, s *ServicePackagedStep, plan *InstallPlan, opts EmitOpts) error
	ExecServiceCustom(ctx context.Context, s *ServiceCustomStep, plan *InstallPlan, opts EmitOpts) error
	ExecBuilder(ctx context.Context, s *BuilderStep, plan *InstallPlan, opts EmitOpts) error
}

// WalkPlan iterates plan.Steps in IR order, applies gate checks, and
// dispatches each step to the matching StepExecutor method. Returns a
// partially-populated LayerRecord on error so the caller can still
// persist whatever was applied before the failure.
//
// Gate handling:
//   - Each step declares its required Gate via RequiresGate().
//     WalkPlan checks GateEnabled(gate, opts); gated-off steps are
//     skipped silently (no error) — same as the legacy paths.
//   - RepoChangeStep has a separate, stricter check: opts.AllowRepoChanges
//     must be true, or WalkPlan returns an error. This matches vm's
//     existing behavior; host treated it the same via its GateEnabled
//     guard. Both paths converge on the same outcome.
//
// ReverseOp handling:
//   - Every successfully-executed step has its Reverse() ops appended
//     to the returned LayerRecord. BuilderStep is the one exception —
//     builder outputs are consumed by later Task/File steps and don't
//     themselves carry teardown state.
//
// Unknown step kinds log a warning and continue. This matches the
// pre-refactor behavior; in the long term, a type-check against the
// set of known kinds at plan-build time should render this branch
// dead.
func WalkPlan(ctx context.Context, ex StepExecutor, plan *InstallPlan, opts EmitOpts) (*CandyRecord, error) {
	if plan == nil {
		return nil, fmt.Errorf("WalkPlan: nil plan")
	}

	rec := &CandyRecord{
		Layer:      plan.Layer,
		Version:    plan.Version,
		DeployedAt: time.Now().UTC().Format(time.RFC3339),
	}

	for _, step := range plan.Steps {
		gate := step.RequiresGate()
		if !GateEnabled(gate, opts) {
			// Silent skip: matches legacy host+vm behavior for
			// non-RepoChange gated-off steps. Logged at the caller
			// level via logGated when desired.
			continue
		}

		switch s := step.(type) {
		case *SystemPackagesStep:
			if err := ex.ExecSystemPackages(ctx, s, plan, opts); err != nil {
				return rec, err
			}
			rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)

		case *TaskStep:
			if err := ex.ExecTask(ctx, s, plan, opts); err != nil {
				return rec, err
			}
			rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)

		case *FileStep:
			if err := ex.ExecFile(ctx, s, plan, opts); err != nil {
				return rec, err
			}
			rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)

		case *ShellHookStep:
			if err := ex.ExecShellHook(ctx, s, plan, opts); err != nil {
				return rec, err
			}
			rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)

		case *RepoChangeStep:
			// Stricter than GateEnabled: repo changes require an
			// explicit opt-in, never silently skipped.
			if !opts.AllowRepoChanges {
				return rec, fmt.Errorf("repo change in plan %s requires --allow-repo-changes", plan.Layer)
			}
			if err := ex.ExecRepoChange(ctx, s, plan, opts); err != nil {
				return rec, err
			}
			rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)

		case *ServicePackagedStep:
			// Gated-off when services are disabled, matching legacy.
			if !opts.WithServices && !opts.AssumeYes {
				continue
			}
			if err := ex.ExecServicePackaged(ctx, s, plan, opts); err != nil {
				return rec, err
			}
			rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)

		case *ServiceCustomStep:
			if !opts.WithServices && !opts.AssumeYes {
				continue
			}
			if err := ex.ExecServiceCustom(ctx, s, plan, opts); err != nil {
				return rec, err
			}
			rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)

		case *BuilderStep:
			if err := ex.ExecBuilder(ctx, s, plan, opts); err != nil {
				return rec, err
			}
			// Builder outputs feed later steps; no ReverseOps
			// of their own.

		default:
			fmt.Fprintf(os.Stderr, "WalkPlan: skipping unsupported step kind %T\n", step)
		}
	}

	return rec, nil
}
