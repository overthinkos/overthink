package main

import (
	"context"
	"fmt"
)

// StepProvider is the typed in-process form of an InstallStep Provider: it emits one step
// to each in-proc venue — OCI image build and VM deploy. Every InstallStep kind implements
// it; the per-venue dispatch resolves the step through
// providerRegistry.ResolveStep(step.Kind()) and calls the matching Emit* method. Each Emit*
// method preserves its venue's EXACT behaviour (gate checks, ReverseOp collection, skips).
//
// There is NO EmitLocal: target:local externalized into candy/plugin-deploy-local, whose
// out-of-process kit.WalkPlans executes every step on the venue (the plugin-renderable kinds
// via the F2 reverse legs, the host-engine kinds via RunHostStep) — so the in-proc local
// per-step dispatch is gone. The VM target keeps its in-proc EmitVM walk until vm itself
// externalizes.
type StepProvider interface {
	Provider
	EmitOCI(t *OCITarget, step InstallStep, plan *InstallPlan) error
	EmitVM(t *VmDeployTarget, ctx context.Context, step InstallStep, plan *InstallPlan, opts EmitOpts, rec *CandyRecord) error
}

// builtinStepBase supplies the in-proc-only Provider half (Class + a stub Invoke)
// for every built-in step provider.
type builtinStepBase struct{}

func (builtinStepBase) Class() ProviderClass { return ClassStep }
func (builtinStepBase) Invoke(context.Context, *Operation) (*Result, error) {
	return nil, fmt.Errorf("built-in install step is in-process only (no out-of-proc Invoke)")
}

// stepProviderFor resolves an InstallStep kind to its StepProvider.
func stepProviderFor(kind StepKind) (StepProvider, bool) {
	prov, ok := providerRegistry.ResolveStep(string(kind))
	if !ok {
		return nil, false
	}
	sp, ok := prov.(StepProvider)
	return sp, ok
}

// allStepKinds is the fixed InstallStep IR vocabulary (Go-internal; steps are not a
// user-authored CUE vocab). The bijection asserts each has a StepProvider.
var allStepKinds = []StepKind{
	StepKindSystemPackages, StepKindBuilder, StepKindOp, StepKindFile,
	StepKindServicePackaged, StepKindServiceCustom, StepKindShellHook,
	StepKindShellSnippet, StepKindRepoChange, StepKindApkInstall,
	StepKindLocalPkgInstall, StepKindReboot, StepKindExternalPlugin,
}

// checkStepProviderBijection asserts every InstallStep kind has a registered
// StepProvider. Run in the same init() that registers, after registration.
func checkStepProviderBijection() error {
	var missing []string
	for _, k := range allStepKinds {
		p, ok := providerRegistry.resolve(ClassStep, string(k))
		if !ok {
			missing = append(missing, string(k))
			continue
		}
		if _, ok := p.(StepProvider); !ok {
			missing = append(missing, string(k)+" (registered but not a StepProvider)")
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("reserved-word registry: InstallStep kinds with no StepProvider: %v", missing)
	}
	return nil
}
