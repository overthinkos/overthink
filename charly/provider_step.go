package main

import (
	"context"
	"fmt"
)

// StepProvider is the typed in-process form of an InstallStep Provider: it emits one step
// to the ONE remaining in-proc venue — the OCI image build (the pod-overlay add_candy:
// Containerfile synthesis). Every InstallStep kind implements it; the dispatch resolves the
// step through providerRegistry.ResolveStep(step.Kind()) and calls EmitOCI, which preserves
// the build venue's exact behaviour.
//
// There is NO EmitLocal and NO EmitVM: BOTH target:local AND target:vm externalized (into
// candy/plugin-deploy-local / candy/plugin-deploy-vm), whose out-of-process kit.WalkPlans
// executes every step on the venue (the plugin-renderable kinds via the F2 reverse legs,
// the host-engine kinds via RunHostStep) — so the in-proc per-deploy-step dispatch is gone.
// OCITarget (the pod-overlay synthesizer) is the sole remaining in-proc StepProvider consumer.
type StepProvider interface {
	Provider
	EmitOCI(t *OCITarget, step InstallStep, plan *InstallPlan) error
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
// user-authored CUE vocab). EVERY kind here round-trips through stepToView/stepFromView (the
// deploy view, exercised by step_view_test); the bijection below asserts each kind is SERVED —
// either by a compiled-in in-proc StepProvider, or (for the pluginEmitStepWords set) by a
// compiled-in class:step plugin's build-emit.
var allStepKinds = []StepKind{
	StepKindSystemPackages, StepKindBuilder, StepKindOp, StepKindFile,
	StepKindServicePackaged, StepKindServiceCustom, StepKindShellHook,
	StepKindShellSnippet, StepKindRepoChange, StepKindApkInstall,
	StepKindLocalPkgInstall, StepKindReboot, StepKindExternalPlugin,
}

// pluginEmitStepWords maps the seven PURE builtin InstallStep kinds whose BUILD-emit externalized
// (C1.1) to the lowercase-hyphenated class:step plugin word that serves their pod-overlay OpEmit
// (candy/plugin-installstep). These kinds have NO in-proc StepProvider — OCITarget.emitStep routes
// them here, serializing the step VIEW as the OpEmit payload. Their DEPLOY leg is unchanged
// (charly/plugin/kit.WalkPlans renders them from the same view). apk-install's plugin declares
// Emits=false (no build fragment); every other word Emits=true.
var pluginEmitStepWords = map[StepKind]string{
	StepKindFile:            "file",
	StepKindShellHook:       "shell-hook",
	StepKindShellSnippet:    "shell-snippet",
	StepKindServicePackaged: "service-packaged",
	StepKindServiceCustom:   "service-custom",
	StepKindRepoChange:      "repo-change",
	StepKindApkInstall:      "apk-install",
}

// checkStepProviderBijection asserts every InstallStep kind is SERVED. A kind in
// pluginEmitStepWords must resolve to a compiled-in class:step plugin declaring a StepContract
// (its build-emit); every other kind must resolve to an in-proc StepProvider (its EmitOCI). Run in
// the same init() that registers, after registration (the compiled-in plugins register first —
// plugins_generated.go's init precedes registry_bootstrap.go's alphabetically, the SAME ordering
// checkVerbProviderBijection relies on).
func checkStepProviderBijection() error {
	var missing []string
	for _, k := range allStepKinds {
		if word, isPlugin := pluginEmitStepWords[k]; isPlugin {
			p, ok := providerRegistry.resolve(ClassStep, word)
			if !ok {
				missing = append(missing, fmt.Sprintf("%s (externalized build-emit; class:step plugin %q not registered)", k, word))
				continue
			}
			if _, ok := p.(stepContractCarrier); !ok {
				missing = append(missing, fmt.Sprintf("%s (class:step provider %q declares no StepContract)", k, word))
			}
			continue
		}
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
		return fmt.Errorf("reserved-word registry: unserved InstallStep kinds: %v", missing)
	}
	return nil
}
