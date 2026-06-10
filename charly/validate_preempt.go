package main

import (
	"fmt"
	"strings"
)

// Validation for the resource-arbitration ("preemptible") classification axis
// — the holder-side `preemptible:` block and the claimant-side
// `requires_exclusive:` list on a DeploymentNode. See classification.go +
// charly/preempt.go. Mirrors validate_ephemeral.go.

// ValidatePreemptibleOnNode checks one deploy node's preemptible +
// requires_exclusive fields and accumulates problems into errs:
//
//   - preemptible.holds must be non-empty (a holder that holds nothing is
//     meaningless — nothing for a claimant to contend over).
//   - preemptible.stop must be "shutdown" (the ONLY mechanism that frees a
//     VFIO passthrough device; pause/managedsave/destroy are rejected with a
//     reason).
//   - preemptible.restore must be "always" or "on-success".
//   - requires_exclusive entries must be non-empty strings.
//   - a node may not both hold and require the SAME token (self-contention).
func ValidatePreemptibleOnNode(name string, node *DeploymentNode, errs *ValidationError) {
	if node == nil {
		return
	}
	if p := node.Preemptible; p != nil {
		if len(dedupeNonEmpty(p.Holds)) == 0 {
			errs.Add("deploy %q: `preemptible.holds` must list at least one exclusive-resource token — a preemptible holder that holds nothing is meaningless", name)
		}
		if p.Stop != "" && p.Stop != PreemptStopShutdown {
			errs.Add("deploy %q: `preemptible.stop: %s` is not supported — only %q (graceful shutdown, disk preserved) frees a passthrough device; pause/managedsave keep the device assigned to the holder", name, p.Stop, PreemptStopShutdown)
		}
		if p.Restore != "" && p.Restore != PreemptRestoreAlways && p.Restore != PreemptRestoreSuccess {
			errs.Add("deploy %q: `preemptible.restore: %s` is invalid — must be %q or %q", name, p.Restore, PreemptRestoreAlways, PreemptRestoreSuccess)
		}
	}
	for _, tok := range node.RequiresExclusive {
		if strings.TrimSpace(tok) == "" {
			errs.Add("deploy %q: `requires_exclusive` contains an empty token", name)
		}
	}
	if node.Preemptible != nil {
		if shared := intersect(node.Preemptible.Holds, node.RequiresExclusive); len(shared) > 0 {
			errs.Add("deploy %q: cannot both hold and require the same exclusive token(s): %s — a holder cannot contend with itself", name, strings.Join(shared, ", "))
		}
	}
}

// ValidatePreemptibleAcrossDeploy validates every node in a charly.yml config
// (the operator-deploy load path). Accumulates into errs.
func ValidatePreemptibleAcrossDeploy(dc *DeployConfig, errs *ValidationError) {
	if dc == nil {
		return
	}
	for name, node := range dc.Deploy {
		ValidatePreemptibleOnNode(name, &node, errs)
	}
}

// validatePreemptibleUnified validates preemptible/requires_exclusive across a
// unified project's deploy map (which includes folded kind:eval beds),
// returning the first batch of errors for the LoadUnified hard-fail path.
func validatePreemptibleUnified(uf *UnifiedFile) error {
	if uf == nil {
		return nil
	}
	errs := &ValidationError{}
	for name, node := range uf.Deploy {
		ValidatePreemptibleOnNode(name, &node, errs)
	}
	validateResourceDefs(uf, errs)
	if errs.HasErrors() {
		return fmt.Errorf("preemptible / requires_exclusive validation:\n  %s", errs.Error())
	}
	return nil
}

// validateResourceDefs checks the build.yml `resource:` vocabulary and its
// interaction with VM-targeted claimants:
//
//   - a `gpu:` selector MUST carry a non-empty vendor (auto-allocation matches
//     DetectVFIO's reported PCI vendor against it);
//   - a `target: vm` claimant requiring a GPU resource needs `backend: libvirt`
//     on its VM entity — a PCI <hostdev> does not render under the qemu backend,
//     so auto-allocation would silently fail at create time.
func validateResourceDefs(uf *UnifiedFile, errs *ValidationError) {
	for name, rdef := range uf.Resource {
		if rdef == nil {
			continue
		}
		if rdef.Gpu != nil && strings.TrimSpace(rdef.Gpu.Vendor) == "" {
			errs.Add("resource %q: `gpu.vendor` is required (e.g. \"0x10de\" for NVIDIA) — it is the PCI vendor auto-allocation matches", name)
		}
	}
	if len(uf.Resource) == 0 {
		return
	}
	for name, node := range uf.Deploy {
		if node.Target != "vm" {
			continue
		}
		if _, _, ok := requiredGPUResource(&node, uf.Resource); !ok {
			continue
		}
		vmName := node.Vm
		if vmName == "" {
			base, _ := parseDeployKey(name)
			vmName = base
		}
		if spec := uf.VM[vmName]; spec != nil && spec.Backend == "qemu" {
			errs.Add("deploy %q requires an auto-allocated GPU but its VM %q pins `backend: qemu` — GPU passthrough needs `backend: libvirt` (PCI <hostdev> does not render under qemu)", name, vmName)
		}
	}
}
