package migrate

// migrate_drop_box_port.go — `charly migrate`.
//
// 2026-06 candy-port-inheritance cutover. Boxes no longer declare ports: the
// box-level `port:` field is RETIRED and the published ports are inherited from
// the box's candy chain (CollectBoxPorts). Host mappings are auto-allocated on
// 127.0.0.1 at deploy time, so the `port: [auto]` sentinel is also retired
// (absence of pins IS auto now). This step:
//
//   - removes the box-level `port:` field from every box doc + `defaults:` (the
//     loader hard-rejects a residual box `port:` via rejectLegacyBoxPort);
//   - removes a `port: [auto]` sentinel from deploy/eval/pod/k8s entries (incl.
//     their nested:/peer: children) — auto-allocation is the deploy default.
//
// EXPLICIT deploy port PINS (host:container, e.g. "8888:8888") are PRESERVED —
// they remain a valid way to fix a published host port. Candy `port:` is never
// touched (candies are the source of truth now). Comment-preserving via the
// yaml.v3 node API, idempotent.

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// MigrateDropBoxPort strips the retired box-level `port:` field and the
// redundant deploy `port: [auto]` sentinel across a project's box dir + root
// YAML siblings. Returns the rewritten file paths. Does NOT touch candy/
// (candies keep their ports) and does NOT recurse into box/<distro> submodules
// (separate repos, migrated on their own). Idempotent.
func MigrateDropBoxPort(dir string, dryRun bool) ([]string, error) {
	return runDocMigration(dir, dryRun, dropBoxPortCandidateFiles, dropBoxPortFromDoc)
}

// dropBoxPortFromDoc removes box.port + defaults.port (unconditional) and the
// `port: [auto]` sentinel from every deploy-shaped entry in one doc. Returns
// whether anything changed.
func dropBoxPortFromDoc(doc *yaml.Node) bool {
	root := rootMappingNode(doc)
	if root == nil {
		return false
	}
	changed := false
	// The box-level port field is retired entirely.
	for _, k := range []string{"box", "defaults"} {
		if m := findMappingValue(root, k); m != nil && m.Kind == yaml.MappingNode {
			if removeMappingKey(m, "port") {
				changed = true
			}
		}
	}
	// Deploy-shaped maps: drop a `port: [auto]` sentinel (auto is the default).
	for _, k := range []string{"deploy", "eval", "pod", "k8s"} {
		if m := findMappingValue(root, k); m != nil && m.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(m.Content); i += 2 {
				if dropAutoPortFromNode(m.Content[i+1]) {
					changed = true
				}
			}
		}
	}
	return changed
}

// dropAutoPortFromNode removes a `port: [auto]` (auto-only) from a deployment
// node and recurses into its `nested:` + `peer:` child maps. An explicit pin
// list is left untouched.
func dropAutoPortFromNode(node *yaml.Node) bool {
	if node == nil || node.Kind != yaml.MappingNode {
		return false
	}
	changed := false
	if pv := findMappingValue(node, "port"); isAutoOnlyPortSeq(pv) {
		if removeMappingKey(node, "port") {
			changed = true
		}
	}
	for _, k := range []string{"nested", "peer"} {
		if m := findMappingValue(node, k); m != nil && m.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(m.Content); i += 2 {
				if dropAutoPortFromNode(m.Content[i+1]) {
					changed = true
				}
			}
		}
	}
	return changed
}

// isAutoOnlyPortSeq reports whether a `port:` value is a sequence whose entries
// are ALL the literal `auto` sentinel — the only case it is safe to drop (auto
// is now the default). An explicit pin (any host:container entry) makes it false.
func isAutoOnlyPortSeq(n *yaml.Node) bool {
	if n == nil || n.Kind != yaml.SequenceNode || len(n.Content) == 0 {
		return false
	}
	for _, c := range n.Content {
		if c.Kind != yaml.ScalarNode || strings.TrimSpace(c.Value) != "auto" {
			return false
		}
	}
	return true
}

// dropBoxPortCandidateFiles returns the project YAML files that can carry a box
// `port:` or a deploy `port: [auto]`: box/<name>/charly.yml + root-level YAML
// siblings (inline boxes / deploys / eval beds). NOT candy/ (candies keep ports).
// Delegates to the shared scanner (migrateCandidateYAMLFiles), which skips the
// box/<distro> submodules (separate repos) AND every testdata fixture dir.
// Sorted, deduplicated.
func dropBoxPortCandidateFiles(dir string) []string {
	return migrateCandidateYAMLFiles(dir, []string{"box"})
}
