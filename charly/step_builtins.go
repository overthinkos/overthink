package main

import (
	"context"
	"fmt"
	"os"
	"time"
)

// The built-in InstallStep kinds as StepProviders. Each Emit* method preserves
// its venue's EXACT prior switch-case behaviour (the per-target emitX/execX
// handlers are unchanged); only the three type-switches + a dead never-wired step-walker are
// gone (C4). The VM venue collects ReverseOps + applies gate checks inline, so each
// EmitVM mirrors its case (which steps append s.Reverse(), which gate, which skip).

// --- system packages ---
type systemPackagesStepProvider struct{ builtinStepBase }

func (systemPackagesStepProvider) Reserved() string { return string(StepKindSystemPackages) }
func (systemPackagesStepProvider) EmitOCI(t *OCITarget, step InstallStep, _ *InstallPlan) error {
	return t.emitSystemPackages(step.(*SystemPackagesStep))
}
func (systemPackagesStepProvider) EmitLocal(t *LocalDeployTarget, step InstallStep, plan *InstallPlan, opts EmitOpts, rec *CandyRecord, start time.Time) error {
	return t.execSystemPackages(step.(*SystemPackagesStep), plan, opts, rec, start)
}
func (systemPackagesStepProvider) EmitVM(t *VmDeployTarget, ctx context.Context, step InstallStep, plan *InstallPlan, opts EmitOpts, rec *CandyRecord) error {
	s := step.(*SystemPackagesStep)
	if err := t.execSystemPackages(ctx, s, plan, opts); err != nil {
		return err
	}
	rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)
	return nil
}

// --- builder ---
type builderStepProvider struct{ builtinStepBase }

func (builderStepProvider) Reserved() string { return string(StepKindBuilder) }
func (builderStepProvider) EmitOCI(t *OCITarget, step InstallStep, plan *InstallPlan) error {
	return t.emitBuilder(step.(*BuilderStep), plan)
}
func (builderStepProvider) EmitLocal(t *LocalDeployTarget, step InstallStep, plan *InstallPlan, opts EmitOpts, rec *CandyRecord, start time.Time) error {
	return t.execBuilder(step.(*BuilderStep), plan, opts, rec, start)
}
func (builderStepProvider) EmitVM(t *VmDeployTarget, ctx context.Context, step InstallStep, plan *InstallPlan, opts EmitOpts, _ *CandyRecord) error {
	return t.execBuilder(ctx, step.(*BuilderStep), plan, opts) // no ReverseOp (matches the VM switch)
}

// --- op (task) ---
type opStepProvider struct{ builtinStepBase }

func (opStepProvider) Reserved() string { return string(StepKindOp) }
func (opStepProvider) EmitOCI(t *OCITarget, step InstallStep, _ *InstallPlan) error {
	return t.emitOp(step.(*OpStep))
}
func (opStepProvider) EmitLocal(t *LocalDeployTarget, step InstallStep, plan *InstallPlan, opts EmitOpts, rec *CandyRecord, start time.Time) error {
	return t.execOp(step.(*OpStep), plan, opts, rec, start)
}
func (opStepProvider) EmitVM(t *VmDeployTarget, ctx context.Context, step InstallStep, plan *InstallPlan, opts EmitOpts, rec *CandyRecord) error {
	s := step.(*OpStep)
	if err := t.execOp(ctx, s, plan, opts); err != nil {
		return err
	}
	rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)
	return nil
}

// --- file ---
type fileStepProvider struct{ builtinStepBase }

func (fileStepProvider) Reserved() string { return string(StepKindFile) }
func (fileStepProvider) EmitOCI(t *OCITarget, step InstallStep, _ *InstallPlan) error {
	return t.emitFile(step.(*FileStep))
}
func (fileStepProvider) EmitLocal(t *LocalDeployTarget, step InstallStep, plan *InstallPlan, opts EmitOpts, rec *CandyRecord, start time.Time) error {
	return t.execFile(step.(*FileStep), plan, opts, rec, start)
}
func (fileStepProvider) EmitVM(t *VmDeployTarget, ctx context.Context, step InstallStep, plan *InstallPlan, opts EmitOpts, rec *CandyRecord) error {
	s := step.(*FileStep)
	if err := t.execFile(ctx, s, plan, opts); err != nil {
		return err
	}
	rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)
	return nil
}

// --- service (packaged) ---
type servicePackagedStepProvider struct{ builtinStepBase }

func (servicePackagedStepProvider) Reserved() string { return string(StepKindServicePackaged) }
func (servicePackagedStepProvider) EmitOCI(t *OCITarget, step InstallStep, _ *InstallPlan) error {
	return t.emitServicePackaged(step.(*ServicePackagedStep))
}
func (servicePackagedStepProvider) EmitLocal(t *LocalDeployTarget, step InstallStep, plan *InstallPlan, opts EmitOpts, rec *CandyRecord, start time.Time) error {
	return t.execServicePackaged(step.(*ServicePackagedStep), plan, opts, rec, start)
}
func (servicePackagedStepProvider) EmitVM(t *VmDeployTarget, ctx context.Context, step InstallStep, plan *InstallPlan, opts EmitOpts, rec *CandyRecord) error {
	if !opts.WithServices {
		return nil // gate silent when not enabled
	}
	s := step.(*ServicePackagedStep)
	if err := t.execServicePackaged(ctx, s, plan, opts); err != nil {
		return err
	}
	rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)
	return nil
}

// --- service (custom) ---
type serviceCustomStepProvider struct{ builtinStepBase }

func (serviceCustomStepProvider) Reserved() string { return string(StepKindServiceCustom) }
func (serviceCustomStepProvider) EmitOCI(t *OCITarget, step InstallStep, _ *InstallPlan) error {
	return t.emitServiceCustom(step.(*ServiceCustomStep))
}
func (serviceCustomStepProvider) EmitLocal(t *LocalDeployTarget, step InstallStep, plan *InstallPlan, opts EmitOpts, rec *CandyRecord, start time.Time) error {
	return t.execServiceCustom(step.(*ServiceCustomStep), plan, opts, rec, start)
}
func (serviceCustomStepProvider) EmitVM(t *VmDeployTarget, ctx context.Context, step InstallStep, plan *InstallPlan, opts EmitOpts, rec *CandyRecord) error {
	if !opts.WithServices {
		return nil
	}
	s := step.(*ServiceCustomStep)
	if err := t.execServiceCustom(ctx, s, plan, opts); err != nil {
		return err
	}
	rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)
	return nil
}

// --- shell hook ---
type shellHookStepProvider struct{ builtinStepBase }

func (shellHookStepProvider) Reserved() string { return string(StepKindShellHook) }
func (shellHookStepProvider) EmitOCI(t *OCITarget, step InstallStep, _ *InstallPlan) error {
	return t.emitShellHook(step.(*ShellHookStep))
}
func (shellHookStepProvider) EmitLocal(t *LocalDeployTarget, step InstallStep, plan *InstallPlan, opts EmitOpts, rec *CandyRecord, start time.Time) error {
	return t.execShellHook(step.(*ShellHookStep), plan, opts, rec, start)
}
func (shellHookStepProvider) EmitVM(t *VmDeployTarget, ctx context.Context, step InstallStep, plan *InstallPlan, opts EmitOpts, rec *CandyRecord) error {
	s := step.(*ShellHookStep)
	if err := t.execShellHook(ctx, s, plan, opts); err != nil {
		return err
	}
	rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)
	return nil
}

// --- shell snippet ---
type shellSnippetStepProvider struct{ builtinStepBase }

func (shellSnippetStepProvider) Reserved() string { return string(StepKindShellSnippet) }
func (shellSnippetStepProvider) EmitOCI(t *OCITarget, step InstallStep, _ *InstallPlan) error {
	return t.emitShellSnippet(step.(*ShellSnippetStep))
}
func (shellSnippetStepProvider) EmitLocal(t *LocalDeployTarget, step InstallStep, plan *InstallPlan, opts EmitOpts, rec *CandyRecord, start time.Time) error {
	return t.execShellSnippet(step.(*ShellSnippetStep), plan, opts, rec, start)
}
func (shellSnippetStepProvider) EmitVM(t *VmDeployTarget, ctx context.Context, step InstallStep, plan *InstallPlan, opts EmitOpts, rec *CandyRecord) error {
	s := step.(*ShellSnippetStep)
	if err := t.execShellSnippet(ctx, s, plan, opts); err != nil {
		return err
	}
	rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)
	return nil
}

// --- repo change (gated; VM hard-errors when not allowed) ---
type repoChangeStepProvider struct{ builtinStepBase }

func (repoChangeStepProvider) Reserved() string { return string(StepKindRepoChange) }
func (repoChangeStepProvider) EmitOCI(t *OCITarget, step InstallStep, _ *InstallPlan) error {
	return t.emitRepoChange(step.(*RepoChangeStep))
}
func (repoChangeStepProvider) EmitLocal(t *LocalDeployTarget, step InstallStep, plan *InstallPlan, opts EmitOpts, rec *CandyRecord, start time.Time) error {
	return t.execRepoChange(step.(*RepoChangeStep), plan, opts, rec, start)
}
func (repoChangeStepProvider) EmitVM(t *VmDeployTarget, ctx context.Context, step InstallStep, plan *InstallPlan, opts EmitOpts, rec *CandyRecord) error {
	if !opts.AllowRepoChanges {
		return fmt.Errorf("repo change in plan %s requires --allow-repo-changes", plan.Candy)
	}
	s := step.(*RepoChangeStep)
	if err := t.execRepoChange(ctx, s, plan, opts); err != nil {
		return err
	}
	rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)
	return nil
}

// --- apk install (only on a kind:android device; skipped on every venue here) ---
type apkInstallStepProvider struct{ builtinStepBase }

func (apkInstallStepProvider) Reserved() string { return string(StepKindApkInstall) }
func (apkInstallStepProvider) EmitOCI(_ *OCITarget, _ InstallStep, _ *InstallPlan) error {
	// No device at image-build time; the deploy-time AndroidDeployTarget runs it.
	return nil
}
func (apkInstallStepProvider) EmitLocal(t *LocalDeployTarget, step InstallStep, _ *InstallPlan, _ EmitOpts, rec *CandyRecord, start time.Time) error {
	s := step.(*ApkInstallStep)
	t.noteStep(rec, StepKindApkInstall, s.Scope(), VenueSkip,
		fmt.Sprintf("candy=%s skipped: apk installs only on a kind:android device", s.CandyName), start)
	return nil
}
func (apkInstallStepProvider) EmitVM(_ *VmDeployTarget, _ context.Context, step InstallStep, _ *InstallPlan, _ EmitOpts, _ *CandyRecord) error {
	s := step.(*ApkInstallStep)
	fmt.Fprintf(os.Stderr, "VmDeployTarget: skipping apk install (candy=%s) — apk installs only on a kind:android device\n", s.CandyName)
	return nil
}

// --- local-pkg install ---
type localPkgInstallStepProvider struct{ builtinStepBase }

func (localPkgInstallStepProvider) Reserved() string { return string(StepKindLocalPkgInstall) }
func (localPkgInstallStepProvider) EmitOCI(t *OCITarget, step InstallStep, _ *InstallPlan) error {
	return t.emitLocalPkgInstall(step.(*LocalPkgInstallStep))
}
func (localPkgInstallStepProvider) EmitLocal(t *LocalDeployTarget, step InstallStep, plan *InstallPlan, opts EmitOpts, rec *CandyRecord, start time.Time) error {
	return t.execLocalPkg(step.(*LocalPkgInstallStep), plan, opts, rec, start)
}
func (localPkgInstallStepProvider) EmitVM(t *VmDeployTarget, ctx context.Context, step InstallStep, _ *InstallPlan, opts EmitOpts, _ *CandyRecord) error {
	s := step.(*LocalPkgInstallStep)
	return execLocalPkgInstall(ctx, t.Exec, s, venueHasPkgManager(ctx, t.Exec, s.LocalPkg, opts), "vm:"+t.VMName, opts)
}

// --- reboot (skipped on OCI/local; executed on VM) ---
// rebootStepProvider lives in its own dedicated file (plugin_step_reboot.go) as the
// externalizable dedicated-provider pattern.
