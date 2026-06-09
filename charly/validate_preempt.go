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

// ValidatePreemptibleAcrossDeploy validates every node in a deploy.yml config
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
	if errs.HasErrors() {
		return fmt.Errorf("preemptible / requires_exclusive validation:\n  %s", errs.Error())
	}
	return nil
}
