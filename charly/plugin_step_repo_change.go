package main

import (
	"context"
	"fmt"
)

// repoChangeStepProvider is the `RepoChange` InstallStep IR provider, extracted into its
// OWN file as the externalizable dedicated-provider pattern (Phase 3). An InstallStep is
// pure IR (never a user-authored input), so it is schema-less and does not fit the
// schema-carrying RegisterBuiltinPluginUnit path; it self-registers via
// registerDedicatedBuiltin below and is INTENTIONALLY absent from both the
// builtinProviderInstances slice and the `providers:` manifest, yet dispatches identically
// through providerRegistry.ResolveStep. Each Emit* preserves its venue's EXACT prior
// behaviour (behavior-preserving): the VM venue hard-errors when --allow-repo-changes is
// not set, then collects ReverseOps inline.
type repoChangeStepProvider struct{ builtinStepBase }

func (repoChangeStepProvider) Reserved() string { return string(StepKindRepoChange) }
func (repoChangeStepProvider) EmitOCI(t *OCITarget, step InstallStep, _ *InstallPlan) error {
	return t.emitRepoChange(step.(*RepoChangeStep))
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

// Self-register at package-var init (before any init(), so the per-class step bijection
// gate in registry_bootstrap.go observes it without a cross-init race).
var _ = registerDedicatedBuiltin(repoChangeStepProvider{})
