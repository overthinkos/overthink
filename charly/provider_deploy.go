package main

import (
	"context"
	"fmt"

	"github.com/overthinkos/overthink/charly/spec"
)

// DeployTargetProvider is the typed in-process form of a deploy-target Provider:
// it resolves a BundleNode to the UnifiedDeployTarget that adds/dels/updates it.
// Every built-in target (local/vm/pod/k8s/android) implements it; ResolveTarget
// resolves the node's derived target word through providerRegistry and calls
// ResolveTarget — the legacy-alias normalization + the dispatch switch are gone (C3).
type DeployTargetProvider interface {
	Provider
	ResolveTarget(node *BundleNode, name string) (UnifiedDeployTarget, error)
}

// builtinDeployBase supplies the in-proc-only Provider half (Class + a stub Invoke)
// for every built-in deploy-target provider. A compiled-in target resolves via
// ResolveTarget; it does not serve itself out-of-process.
type builtinDeployBase struct{}

func (builtinDeployBase) Class() ProviderClass { return ClassDeployTarget }
func (builtinDeployBase) Invoke(context.Context, *Operation) (*Result, error) {
	return nil, fmt.Errorf("built-in deploy target is in-process only (no out-of-proc Invoke)")
}

// deployTargetWords is the canonical deploy-target set (the cross-ref-inferred
// node.Target values). Every word is also a kind (a deploy target is a deployable
// kind), so the bijection ties this list to the CUE kind vocabulary — it cannot
// drift from spec.KindWords — AND asserts each has a DeployTargetProvider.
var deployTargetWords = []string{"local", "vm", "pod", "k8s", "android"}

// checkDeployProviderBijection: every canonical deploy-target word is a valid kind
// (⊆ spec.KindWords) AND has a registered DeployTargetProvider. Run in the same
// init() that registers (after registration), avoiding the alphabetical race.
func checkDeployProviderBijection() error {
	kinds := map[string]bool{}
	for _, k := range spec.KindWords {
		kinds[k] = true
	}
	var problems []string
	for _, w := range deployTargetWords {
		if !kinds[w] {
			problems = append(problems, w+" (not a spec.KindWords kind)")
		}
		p, ok := providerRegistry.resolve(ClassDeployTarget, w)
		if !ok {
			problems = append(problems, w+" (no DeployTargetProvider)")
			continue
		}
		if _, ok := p.(DeployTargetProvider); !ok {
			problems = append(problems, w+" (registered but not a DeployTargetProvider)")
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("reserved-word registry: deploy-target provider bijection broken: %v", problems)
	}
	return nil
}
