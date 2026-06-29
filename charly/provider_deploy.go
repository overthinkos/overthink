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
// drift from spec.KindWords — AND asserts each is served EITHER by an in-proc
// DeployTargetProvider OR by an external out-of-process plugin (externalizedDeploySubstrates).
var deployTargetWords = []string{"local", "vm", "pod", "k8s", "android"}

// externalizedDeploySubstrates is THE single source of truth for which canonical
// deploy-substrate kinds are served by an EXTERNAL out-of-process plugin instead
// of a compiled-in DeployTargetProvider (F1 — the substrate-kind-plugin dispatch
// seam). A word listed here has NO in-proc builtin: its grpcProvider registers at
// plugin-load time and ResolveTarget routes target:<word> to externalDeployTarget
// over the E3b reverse channel. Both checkDeployProviderBijection (in-proc XOR
// externalized) and isExternalDeploySubstrate (a substrate kind is external iff
// listed here) consult it — so the two gates can never disagree. GENERAL for all
// 5: pod/vm/k8s/local join this set as they migrate; the ONLY android-specific
// piece is the registered preresolver body (android_deploy_preresolve.go), never a
// branch in the generic dispatch.
var externalizedDeploySubstrates = map[string]bool{
	"android": true,
}

// checkDeployProviderBijection: every canonical deploy-target word is a valid kind
// (⊆ spec.KindWords — the "word is known" invariant) AND is served by EXACTLY ONE
// of {an in-proc DeployTargetProvider, an external plugin (externalizedDeploySubstrates)}
// — never both (XOR), never neither. Run in the same init() that registers (after
// registration), avoiding the alphabetical race. An externalized word legitimately
// has NO provider at process start (its grpcProvider connects later at load time).
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
		p, hasBuiltin := providerRegistry.resolve(ClassDeployTarget, w)
		ext := externalizedDeploySubstrates[w]
		switch {
		case ext && hasBuiltin:
			problems = append(problems, w+" (externalized substrate must NOT also have an in-proc DeployTargetProvider)")
		case ext && !hasBuiltin:
			// OK — served out-of-process by an external plugin connected at load time.
		case !ext && !hasBuiltin:
			problems = append(problems, w+" (no DeployTargetProvider and not an externalized substrate)")
		default: // !ext && hasBuiltin
			if _, ok := p.(DeployTargetProvider); !ok {
				problems = append(problems, w+" (registered but not a DeployTargetProvider)")
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("reserved-word registry: deploy-target provider bijection broken: %v", problems)
	}
	return nil
}
