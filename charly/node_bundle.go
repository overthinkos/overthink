package main

// node_bundle.go — the bundle / resource-member builder for the unified node-form
// model. A `bundle` node (or any resource node carrying resource members) becomes
// a BundleNode tree:
//   - the discriminator VALUE carries the deploy config (box/vm/… cross-ref +
//     port/env/volume/security/disposable/tunnel/…), decoded into the node;
//   - RESOURCE children become members — alongside siblings (Peer) under a
//     `bundle` group, inside-venue children (Nested) under a resource (pod-in-vm);
//   - STEP children become Plan steps whose venue is their position in the tree.
// Cross-member addressing is ${HOST:<member>} (resolved by position; see
// check_members.go). NO data-node children here (deploy config is value-carried),
// so #BundleArm narrows children to resources + steps.

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// buildBundleNodeInto builds gn into a BundleNode and registers it in the
// Deploy (bundle) map.
func buildBundleNodeInto(gn *genericNode, uf *UnifiedFile) error {
	dn, err := buildBundleNode(gn)
	if err != nil {
		return err
	}
	ensureMap(&uf.Bundle)
	uf.Bundle[gn.name] = *dn
	return nil
}

// buildBundleNode recursively builds a BundleNode from a bundle/resource node. The
// discriminator value carries the deploy config; inline STEP children (checks) fold
// into the bundle's plan via decodeNodeValue (the assembler); ENTITY children are
// RESOURCE members (deploy-into / alongside).
func buildBundleNode(gn *genericNode) (*BundleNode, error) {
	var dn BundleNode
	if err := decodeNodeValue(gn, &dn); err != nil {
		return nil, err
	}
	dn.Target = bundleTargetForDisc(gn.disc)
	// A scalar discriminator value (`vm: pg-vm`) is a same-kind entity cross-ref.
	if gn.discValue != nil && gn.discValue.Kind == yaml.ScalarNode {
		setBundleCrossRef(&dn, gn.disc, gn.discValue.Value)
	}
	if gn.disc == "bundle" {
		// A `bundle` deployment infers its workload target from the cross-ref it
		// carries (box→pod, vm→vm, …); none → a pure group (empty target).
		dn.Target = inferBundleTarget(&dn)
	}

	for _, rk := range gn.children {
		// Data + step children are folded into the bundle body by decodeNodeValue;
		// only sub-ENTITY children are resource members.
		if rk.discClass != "entity" {
			continue
		}
		if !isResourceDisc(rk.disc) {
			return nil, fmt.Errorf("node %q: a %q child %q is not a resource member (bundle/resource children must be pod/vm/k8s/local/android/host/bundle)", gn.name, rk.disc, rk.name)
		}
		member, err := buildBundleNode(rk)
		if err != nil {
			return nil, err
		}
		// A GROUP/venue (no own workload target) places members ALONGSIDE (shared
		// net → Peer); a WORKLOAD places its resource children INSIDE its venue
		// (deploy-into → Nested).
		if dn.Target == "" || gn.disc == "host" {
			if dn.Members == nil {
				dn.Members = map[string]*BundleNode{}
			}
			dn.Members[rk.name] = member
		} else {
			if dn.Children == nil {
				dn.Children = map[string]*BundleNode{}
			}
			dn.Children[rk.name] = member
		}
	}
	return &dn, nil
}

// isResourceDisc reports whether a discriminator names a deploy-substrate kind
// (the markers of a bundle member / bundle-shaped node) — the CUE-derived
// resourceKindSet (#ResourceKind).
func isResourceDisc(d string) bool {
	return resourceKindSet[d]
}

// bundleTargetForDisc maps a node discriminator to the BundleNode Target.
// `bundle`/`host` are groups/venues (no own workload target).
func bundleTargetForDisc(d string) string {
	switch d {
	case "bundle", "host":
		return ""
	default:
		return d // pod | vm | k8s | local | android
	}
}

// inferBundleTarget derives a bundle deployment's workload target from the
// cross-ref it carries; no cross-ref → a pure group (empty target). A per-host
// OVERLAY entry carries only runtime state (no authored cross-ref) — `vm_state`
// evidences a vm target so the overlay validates standalone (the project deploy
// supplies the `vm:` ref on merge).
func inferBundleTarget(dn *BundleNode) string {
	switch {
	case dn.Box != "":
		return "pod"
	case dn.Vm != "":
		return "vm"
	case dn.K8s != "":
		return "k8s"
	case dn.Local != "":
		return "local"
	case dn.Android != "":
		return "android"
	case dn.VmState != nil:
		return "vm" // overlay-only vm entry (runtime state, no authored cross-ref)
	default:
		return "" // pure group / venue
	}
}

// setBundleCrossRef sets the same-kind entity reference from a scalar value.
func setBundleCrossRef(dn *BundleNode, disc, ref string) {
	switch disc {
	case "pod":
		dn.Box = ref
	case "vm":
		dn.Vm = ref
	case "k8s":
		dn.K8s = ref
	case "local":
		dn.Local = ref
	case "android":
		dn.Android = ref
	}
}
