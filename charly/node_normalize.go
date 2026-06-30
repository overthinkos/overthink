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
// spec map. Each kind's decode lives on its own dedicated provider file (every kind
// is now a dedicated provider — plugin_candy.go / plugin_group.go / plugin_substrate.go
// for the in-proc KindProviders, the per-kind plugin units for the tier-1 kinds).
func normalizeNodeInto(gn *genericNode, uf *UnifiedFile) error {
	prov, ok := providerRegistry.ResolveKind(gn.disc)
	if !ok {
		// An external DEPLOY substrate word (e.g. `exampledeploy`) at a deploy's edge
		// is not a KIND — it routes to the bundle builder, the same path the
		// deploy-shape kinds (pod/vm/k8s/local/android/group) take. Recognized via a
		// connected OR pre-scanned deploy provider (plugin_prescan.go), so the bed
		// parses before the out-of-process provider connects (loadProjectPlugins);
		// the actual Add still dispatches through the connected grpcProvider.
		if recognizedDeploySubstrate(gn.disc) {
			return buildBundleNodeInto(gn, uf)
		}
		// An external KIND (F4): declared by a project plugin candy whose out-of-process
		// provider has not registered. During the connect pre-pass's nested scan
		// (inKindConnectPass) the provider is not connected YET — DEFER (skip, no error) so the
		// nested ScanCandy/LoadUnified succeeds; the OUTER load decodes it after
		// connectDeclaredKindPlugins runs (depth-0, before this). OUTSIDE the pre-pass the
		// connect already ran, so a still-missing provider means the plugin FAILED to connect —
		// a hard error (a declared kind is never silently dropped).
		if recognizedKind(gn.disc) {
			if inKindConnectPass() {
				return nil
			}
			return fmt.Errorf("node %q: kind %q is declared by a plugin but its provider did not connect (build/connect failed)", gn.name, gn.disc)
		}
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
