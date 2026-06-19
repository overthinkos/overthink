package main

// node_normalize.go — the normalizer dispatcher: turn a parsed genericNode into
// its domain struct and register it in the UnifiedFile. This is the node-form
// authoring surface's decode path. The legacy kind-keyed routing
// (classifyDoc-for-kinds / mergeKindDoc / kindKeyedDoc / the per-kind Doc
// wrappers) STILL COEXISTS in the loader during this transition (the reader is
// bilingual — node-form + legacy); its deletion is the next cutover. Every kind
// flows through the ONE generic value-decoder (node_build.go), so node-form
// yields the exact same domain structs the kind-first decode produced (proven by
// the *_RoundTrip tests).

import "fmt"

// normalizeNodeInto decodes one top-level entity node into uf's matching map.
// Bundle/resource kinds carrying nested members route to the bundle builder
// (node_bundle.go); a bare standalone resource (pod/vm/k8s/local/android with no
// member children) decodes directly into its own spec map.
func normalizeNodeInto(gn *genericNode, uf *UnifiedFile) error {
	switch gn.disc {
	case "candy":
		name, ic, err := buildCandy(gn)
		if err != nil {
			return err
		}
		ensureMap(&uf.Candy)
		uf.Candy[name] = ic
	case "box":
		var b BoxConfig
		if err := decodeNodeValue(gn, &b); err != nil {
			return err
		}
		ensureMap(&uf.Box)
		uf.Box[gn.name] = b
	case "distro":
		if err := decodePtrInto(gn, &uf.Distro); err != nil {
			return err
		}
	case "builder":
		if err := decodePtrInto(gn, &uf.Builder); err != nil {
			return err
		}
	case "init":
		if err := decodePtrInto(gn, &uf.Init); err != nil {
			return err
		}
	case "resource":
		if err := decodePtrInto(gn, &uf.Resource); err != nil {
			return err
		}
	case "agent":
		if err := decodePtrInto(gn, &uf.Agent); err != nil {
			return err
		}
	case "group":
		if err := decodePtrInto(gn, &uf.Group); err != nil {
			return err
		}
	case "target":
		if err := decodePtrInto(gn, &uf.Target); err != nil {
			return err
		}
	case "module":
		if err := decodePtrInto(gn, &uf.Module); err != nil {
			return err
		}
	case "sidecar":
		var s SidecarDef
		if err := decodeNodeValue(gn, &s); err != nil {
			return err
		}
		ensureMap(&uf.Sidecar)
		uf.Sidecar[gn.name] = s
	case "pod", "vm", "k8s", "local", "android":
		// A standalone resource entity (no bundle members). When it carries
		// resource children it is a bundle-shaped node → the bundle builder.
		if len(resourceChildren(gn)) > 0 {
			return buildBundleNodeInto(gn, uf)
		}
		return buildStandaloneResource(gn, uf)
	case "bundle", "host":
		return buildBundleNodeInto(gn, uf)
	default:
		return fmt.Errorf("node %q: unsupported discriminator %q", gn.name, gn.disc)
	}
	return nil
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
// resource/bundle kind (the markers of a bundle-shaped node).
func resourceChildren(gn *genericNode) []*genericNode {
	var out []*genericNode
	for _, ch := range gn.children {
		switch ch.disc {
		case "pod", "vm", "k8s", "local", "android", "host", "bundle":
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
