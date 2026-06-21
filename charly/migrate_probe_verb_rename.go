package main

// migrate_probe_verb_rename.go — the EDGE-INHERIT cutover-A migration: rename the
// step PROBE VERBS that collided with reserved KIND words.
//
// `k8s:` (the cluster probe) and `group:` (getent unix group) reused the `k8s`
// deploy-substrate kind and the Calamares `group` kind as STEP verbs — a kind
// word in the MIDDLE of a step, violating the EDGE-INHERIT invariant "kinds live
// only at config edges, never as a step verb". They become `kube:` / `unix_group:`,
// and the kube verb's `k8s_*` modifiers follow to `kube_*`.
//
// The rename is gated on isStepNode: it touches a key ONLY inside a plan STEP node
// (one carrying run/check/agent-run/agent-check/include). A top-level `k8s:` deploy
// entity, a `bundle:{k8s: <ref>}` cross-ref, or the Calamares `group:` kind has no
// step keyword, so it is never a step node and is left untouched — the deploy KIND
// `k8s` and the Calamares `group` kind survive verbatim.
//
// This RAISES LatestSchemaVersion: an `#Op` is CLOSED, so a `k8s:`/`group:` key in
// a step no longer validates — an un-migrated config must be rejected with a
// `Run: charly migrate` hint, not a cryptic closed-schema error. Comment-preserving
// (renames the key node's Value in place; value + comments untouched), idempotent
// (a step already on kube:/unix_group: has no legacy key). TouchesHost false: the
// project rewrite runs under remote-cache auto-migration so a fetched remote's beds
// rename too, while the per-host overlay portion self-gates on ctx.HostDeployPath
// (the SAME split unified-node / step-venue / install-strategy-key use).

import "gopkg.in/yaml.v3"

// probeVerbRenames maps each retired step verb / kube modifier key to its
// EDGE-INHERIT replacement. Applied ONLY inside step nodes (see file header).
var probeVerbRenames = map[string]string{
	"k8s":          "kube",
	"group":        "unix_group",
	"k8s_kind":     "kube_kind",
	"k8s_context":  "kube_context",
	"k8s_count":    "kube_count",
	"k8s_resource": "kube_resource",
	"k8s_group":    "kube_group",
	"k8s_version":  "kube_version",
}

// MigrateProbeVerbRename renames the retired step verbs + kube modifiers across a
// project's candy/ + box/ dirs and root YAML siblings, plus the per-host overlay
// (R3 symmetry). Returns whether anything changed.
func MigrateProbeVerbRename(ctx *MigrateContext) (bool, error) {
	w, err := runDocMigration(ctx.Dir, ctx.DryRun, opUnifyCandidateFiles, probeVerbRenameDoc)
	if err != nil {
		return len(w) > 0, err
	}
	hostChanged, herr := migrateHostOverlayDoc(ctx, probeVerbRenameDoc)
	return len(w) > 0 || hostChanged, herr
}

// probeVerbRenameDoc renames legacy verb/modifier keys in every STEP node at any
// depth of one document. Returns whether the document changed.
func probeVerbRenameDoc(n *yaml.Node) bool {
	changed := false
	switch n.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, c := range n.Content {
			if probeVerbRenameDoc(c) {
				changed = true
			}
		}
	case yaml.MappingNode:
		if isStepNode(n) {
			for i := 0; i+1 < len(n.Content); i += 2 {
				key := n.Content[i]
				if key.Kind != yaml.ScalarNode {
					continue
				}
				if to, ok := probeVerbRenames[key.Value]; ok {
					key.Value = to
					changed = true
				}
			}
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			if probeVerbRenameDoc(n.Content[i+1]) {
				changed = true
			}
		}
	}
	return changed
}
