package main

// node_normalize.go — the normalizer dispatcher: turn a parsed genericNode into
// its domain struct and register it in the UnifiedFile. This is the node-form
// authoring surface's decode path, and the ONLY one: the legacy kind-keyed
// routing (the kind-first decode + per-kind document wrappers) was DELETED in the
// #NodeDoc-sole-gate cutover — a legacy kind-keyed / root-shape document is now
// hard-rejected at classifyDoc with a `charly migrate` hint. Every kind flows
// through the ONE generic value-decoder (node_build.go),
// so node-form yields the exact same domain structs the kind-first decode
// produced (proven by the *_RoundTrip tests).

import "fmt"

// normalizeNodeInto decodes one top-level entity node into uf's matching map by
// resolving the node's discriminator to its KindProvider and calling DecodeNode —
// the per-kind decode switch is gone (C2). Bundle/resource kinds carrying nested
// members route to the bundle builder (node_bundle.go); a bare standalone resource
// (pod/vm/k8s/local/android with no member children) decodes directly into its own
// spec map. Each kind's decode lives on its provider (kind_builtins.go).
func normalizeNodeInto(gn *genericNode, uf *UnifiedFile) error {
	prov, ok := providerRegistry.ResolveKind(gn.disc)
	if !ok {
		return fmt.Errorf("node %q: unsupported discriminator %q", gn.name, gn.disc)
	}
	// Built-in kind: the typed DecodeNode fast path (no JSON). External plugin kind:
	// the serializable Invoke envelope (runPluginKind) — the E3 generalization of the
	// verb dual-path to the kind class.
	if kp, ok := prov.(KindProvider); ok {
		return kp.DecodeNode(gn, uf)
	}
	return runPluginKind(prov, gn, uf)
}

// buildStandaloneResource decodes a bare pod/vm/k8s/local/android entity (steps
// allowed, no resource members) into its own spec map.
func buildStandaloneResource(gn *genericNode, uf *UnifiedFile) error {
	switch gn.disc {
	case "vm":
		if err := decodePtrInto(gn, &uf.VM); err != nil {
			return err
		}
	case "pod":
		if err := decodePtrInto(gn, &uf.Pod); err != nil {
			return err
		}
	case "k8s":
		if err := decodePtrInto(gn, &uf.K8s); err != nil {
			return err
		}
	case "local":
		if err := decodePtrInto(gn, &uf.Local); err != nil {
			return err
		}
	case "android":
		if err := decodePtrInto(gn, &uf.Android); err != nil {
			return err
		}
	default:
		return fmt.Errorf("node %q: %q is not a standalone resource kind", gn.name, gn.disc)
	}
	return nil
}

// resourceChildren returns gn's children whose discriminator is itself a
// resource/bundle kind (the markers of a bundle-shaped node). The deployable set
// is the CUE-derived resourceKindSet (#ResourceKind).
func resourceChildren(gn *genericNode) []*genericNode {
	var out []*genericNode
	for _, ch := range gn.children {
		if resourceKindSet[ch.disc] {
			out = append(out, ch)
		}
	}
	return out
}

// decodePtrInto decodes gn's body into a fresh T and stores &T at gn.name in *m
// (allocating the map on first use). For the map[string]*T spec maps.
func decodePtrInto[T any](gn *genericNode, m *map[string]*T) error {
	var v T
	if err := decodeNodeValue(gn, &v); err != nil {
		return err
	}
	if *m == nil {
		*m = map[string]*T{}
	}
	(*m)[gn.name] = &v
	return nil
}

// ensureMap allocates a nil map[string]V in place.
func ensureMap[V any](m *map[string]V) {
	if *m == nil {
		*m = map[string]V{}
	}
}
