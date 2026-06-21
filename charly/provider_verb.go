package main

import (
	"context"
	"fmt"
)

// CheckVerbProvider is the typed in-process form of a check-verb Provider: it
// runs the assert-mode probe and returns a CheckResult directly (carrying the
// live *Runner, which cannot cross the wire). Every built-in verb implements it;
// `runOne` resolves the verb through providerRegistry and calls RunVerb — the
// switch is gone.
//
// An OUT-OF-PROCESS plugin verb does NOT implement this — it is reached via the
// generic `plugin:` envelope (runPluginVerb → ResolveVerb → the Invoke wire
// form), so runOne only ever deals with in-proc CheckVerbProviders.
type CheckVerbProvider interface {
	Provider
	RunVerb(ctx context.Context, rt *Runner, op *Op) CheckResult
}

// builtinVerbBase supplies the in-proc-only Provider half (Class + a stub Invoke)
// for every built-in verb provider. A compiled-in verb runs via RunVerb; it does
// not serve itself out-of-process, so Invoke is an explicit error rather than a
// silent path. Each verb embeds this and adds Reserved() + RunVerb().
type builtinVerbBase struct{}

func (builtinVerbBase) Class() ProviderClass { return ClassVerb }
func (builtinVerbBase) Invoke(context.Context, *Operation) (*Result, error) {
	return nil, fmt.Errorf("built-in verb is in-process only (no out-of-proc Invoke)")
}

// checkVerbProviderBijection asserts every CUE-declared verb (spec.OpVerbs) has a
// registered in-proc CheckVerbProvider — the registry generalization of the
// VerbCatalog⇄spec.OpVerbs gate. Extra ClassVerb providers (out-of-tree plugin
// verbs, not in spec.OpVerbs) are ALLOWED — a plugin contributes new verbs. Runs
// at init() alongside the other bijection gates.
func checkVerbProviderBijection(verbs []string) error {
	var missing []string
	for _, v := range verbs {
		// Pure-install verbs (mkdir/copy/write/link/download/setcap/build) render
		// ONLY to install steps — runOne never check-dispatches them (it skips an
		// install verb authored as a check). `command` is in installVerbs too but
		// IS check-dispatched (runCommand), so it is NOT skipped here.
		if installVerbs[v] && v != "command" {
			continue
		}
		p, ok := providerRegistry.resolve(ClassVerb, v)
		if !ok {
			missing = append(missing, v)
			continue
		}
		if _, ok := p.(CheckVerbProvider); !ok {
			missing = append(missing, v+" (registered but not a CheckVerbProvider)")
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("reserved-word registry: check-dispatch verbs in spec.OpVerbs with no in-proc CheckVerbProvider: %v", missing)
	}
	return nil
}
