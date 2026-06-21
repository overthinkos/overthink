package main

import (
	"context"
	"fmt"
)

// KindProvider is the typed in-process form of a kind Provider: it decodes a
// node-form value into its entity on the UnifiedFile. Every built-in kind
// implements it; normalizeNodeInto resolves the node's discriminator through
// providerRegistry and calls DecodeNode — the per-kind decode switch is gone (C2).
// CueDefPath names the CUE entity def the value validates against (the former
// reservedKindHandlers map, folded onto the provider — R3).
type KindProvider interface {
	Provider
	DecodeNode(gn *genericNode, uf *UnifiedFile) error
	CueDefPath() string
}

// builtinKindBase supplies the in-proc-only Provider half (Class + a stub Invoke)
// for every built-in kind provider. A compiled-in kind decodes via DecodeNode; it
// does not serve itself out-of-process.
type builtinKindBase struct{}

func (builtinKindBase) Class() ProviderClass { return ClassKind }
func (builtinKindBase) Invoke(context.Context, *Operation) (*Result, error) {
	return nil, fmt.Errorf("built-in kind is in-process only (no out-of-proc Invoke)")
}

// checkKindProviderBijection asserts every authoring kind keyword (spec.KindWords)
// has a registered in-proc KindProvider — the registry generalization of the
// reservedKindHandlers⇄spec.KindWords gate. Extra ClassKind providers (an
// out-of-tree plugin kind, registered later) are allowed; at init() only built-ins
// are present, so a built-in extra is a no-op (never resolved). Run in the same
// init() that registers, after registration (alphabetical init-order race).
func checkKindProviderBijection(kinds []string) error {
	var missing []string
	for _, k := range kinds {
		p, ok := providerRegistry.resolve(ClassKind, k)
		if !ok {
			missing = append(missing, k)
			continue
		}
		if _, ok := p.(KindProvider); !ok {
			missing = append(missing, k+" (registered but not a KindProvider)")
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("reserved-word registry: kinds in spec.KindWords with no in-proc KindProvider: %v", missing)
	}
	return nil
}
