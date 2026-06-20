package main

// migrate_step_venue.go — the 2026-06 venue-from-position cutover migration.
//
// The step-level venue OVERRIDES are retired: `pod:` (per-step container venue)
// and `on:` (cross-member driver dispatch) no longer exist on a step. A step's
// execution venue is now derived ENTIRELY from its POSITION in the bundle tree
// (flattenBundleVenues → Op.venue). This migration mechanically rewrites a
// node-form config to that shape:
//
//   - each distinct `pod:` venue becomes an `agent_provisioned: true`
//     resource-node chain — bare `os` → a sibling member node; dotted
//     `a.b.c` → nested children — and the step is REPARENTED under its leaf
//     venue node with `pod:` stripped;
//   - each `on: D` step is reparented under member `D` (created as an
//     agent-provisioned scaffold only if absent — an existing peer keeps its
//     box) with `on:` stripped;
//   - `${PEER_HOST:m}` → `${HOST:m}` and `${PEER_ENDPOINT:m:p}` → `${HOST:m:p}`
//     are rewritten across every step string field.
//
// It runs AFTER the unified-node migration, so it operates on NODE-FORM
// (`<name>: {bundle: …, <step-children>, <resource-children>}`). Idempotent — a
// step already nested under a resource node carries no pod:/on: and is skipped,
// so a second run is a byte-identical no-op. Comment-preserving — a step's own
// *yaml.Node is MOVED (not rebuilt), and synthesized resource scaffolds are
// minimal additions.
//
// The dotted VM phase's discriminator is emitted as a best-effort `pod`
// scaffold; the genuine vm/pod disc for a VM venue is hand-authored in the
// cutover (recorded in CHANGELOG/) — this migration never tries to infer it.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// reservedVenueKeywords are the entity-kind discriminators a venue name MUST NOT
// collide with: a tree member node named after a kind is parsed as that kind, not
// a venue (the CUE #BundleArm rejects it). The author must rename the venue.
var reservedVenueKeywords = map[string]bool{
	"pod": true, "vm": true, "k8s": true, "local": true, "android": true,
	"host": true, "box": true, "candy": true, "bundle": true,
}

// MigrateStepVenue rewrites every node-form bundle entity's step venue overrides
// to tree position across a project's candy/ + box/ dirs and root YAML siblings.
// Returns the rewritten file paths. Idempotent.
func MigrateStepVenue(dir string, dryRun bool) ([]string, error) {
	// Pre-check (R3-generic, before any transform): a step `pod:` venue whose
	// FIRST segment is a reserved kind keyword cannot become a tree member node
	// (the parser reads the name as a discriminator). HARD-ERROR with a rename
	// hint rather than silently emit an invalid member the loader later rejects
	// with a cryptic CUE "field not allowed" error.
	for _, path := range opUnifyCandidateFiles(dir) {
		if err := checkReservedStepVenues(path); err != nil {
			return nil, err
		}
	}
	return runDocMigration(dir, dryRun, opUnifyCandidateFiles, stepVenueDoc)
}

// checkReservedStepVenues hard-errors if any bundle entity in the file at path
// has a step whose `pod:` venue's first segment is a reserved kind keyword.
// Read-only; a missing/unparseable file is a no-op here (surfaced elsewhere).
func checkReservedStepVenues(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil
	}
	root := rootMappingNode(&doc)
	if root == nil {
		return nil
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		entityVal := root.Content[i+1]
		if entityVal == nil || entityVal.Kind != yaml.MappingNode || findMappingValue(entityVal, "bundle") == nil {
			continue
		}
		for j := 0; j+1 < len(entityVal.Content); j += 2 {
			sv := entityVal.Content[j+1]
			if sv == nil || sv.Kind != yaml.MappingNode || !isStepNode(sv) {
				continue
			}
			pod := scalarFieldValue(sv, "pod")
			if pod == "" {
				continue
			}
			seg0, _, _ := strings.Cut(pod, ".")
			if reservedVenueKeywords[seg0] {
				return fmt.Errorf("%s: step venue %q collides with the reserved kind keyword %q — a tree member node cannot be named after a kind; rename the venue (e.g. %q), update the referencing steps + bed prompt, then re-run charly migrate",
					filepath.Base(path), pod, seg0, suggestVenueRename(seg0))
			}
		}
	}
	return nil
}

// suggestVenueRename returns a descriptive non-reserved spelling for a reserved
// venue keyword (for the migration error hint).
func suggestVenueRename(reserved string) string {
	switch reserved {
	case "k8s":
		return "k8s-cluster"
	case "vm":
		return "vm-guest"
	case "pod":
		return "app-pod"
	default:
		return reserved + "-svc"
	}
}

// stepVenueDoc transforms every node-form bundle entity in one document. A
// bundle entity is a name-first node whose value carries a `bundle:`
// discriminator child; its DIRECT step children hold the retired pod:/on:
// venue overrides.
func stepVenueDoc(doc *yaml.Node) bool {
	root := rootMappingNode(doc)
	if root == nil {
		return false
	}
	changed := false
	for i := 0; i+1 < len(root.Content); i += 2 {
		entityVal := root.Content[i+1]
		if entityVal == nil || entityVal.Kind != yaml.MappingNode {
			continue
		}
		if findMappingValue(entityVal, "bundle") == nil {
			continue // only bundle entities (deploys / check beds) carry venue steps
		}
		if stepVenueEntity(entityVal) {
			changed = true
		}
	}
	return changed
}

// stepVenueEntity reparents the venue-bearing DIRECT step children of one bundle
// entity (em is the entity's value mapping) into the tree, and rewrites PEER_*
// vars across all step children. A venue-less `run:`/`include:` step (a
// provisioning step that carried no `pod:` in the flat plan) INHERITS the venue
// of the nearest pod:-bearing step in the same bundle — its phase — so it lands
// under the right member instead of being orphaned as a direct child of a group
// (which has no venue). Returns whether anything changed.
func stepVenueEntity(em *yaml.Node) bool {
	changed := false

	// Phase 1 — ordered scan of the direct step children. PEER_* rewrite applies
	// to every step regardless of reparenting (a step with no PEER_* var is left
	// byte-identical).
	type stepInfo struct {
		key      string // step child key
		venue    string // explicit pod: venue (possibly dotted); "" otherwise
		driver   string // on: driver; "" otherwise
		runOrInc bool   // run:/include: step — eligible for phase-venue inheritance
	}
	var steps []stepInfo
	for j := 0; j+1 < len(em.Content); j += 2 {
		keyNode, valNode := em.Content[j], em.Content[j+1]
		if valNode == nil || valNode.Kind != yaml.MappingNode || !isStepNode(valNode) {
			continue
		}
		if rewritePeerVarsInStep(valNode) {
			changed = true
		}
		steps = append(steps, stepInfo{
			key:      keyNode.Value,
			venue:    scalarFieldValue(valNode, "pod"),
			driver:   scalarFieldValue(valNode, "on"),
			runOrInc: findMappingValue(valNode, "run") != nil || findMappingValue(valNode, "include") != nil,
		})
	}

	// Phase 2 — a venue-less run:/include: step inherits the venue of the nearest
	// pod:-bearing step by index distance (preceding wins on a tie): its phase. A
	// venue-less check:/agent-check: step is NOT given a venue here — it must
	// declare one (the flatten/validate pass surfaces the authoring error rather
	// than masking it).
	for i := range steps {
		if steps[i].venue != "" || steps[i].driver != "" || !steps[i].runOrInc {
			continue
		}
		for d := 1; d < len(steps); d++ {
			if b := i - d; b >= 0 && steps[b].venue != "" {
				steps[i].venue = steps[b].venue
				break
			}
			if f := i + d; f < len(steps) && steps[f].venue != "" {
				steps[i].venue = steps[f].venue
				break
			}
		}
	}

	// Phase 3 — reparent every step that now has a venue/driver under its member.
	for _, s := range steps {
		if s.venue == "" && s.driver == "" {
			continue
		}
		kn, vn, ok := takeMappingEntry(em, s.key)
		if !ok {
			continue
		}
		// Locate (creating as needed) the leaf venue node's value mapping.
		leaf := em
		if s.driver != "" {
			leaf = ensureResourceChild(leaf, s.driver)
		} else {
			for _, seg := range strings.Split(s.venue, ".") {
				leaf = ensureResourceChild(leaf, seg)
			}
		}
		// Strip the retired venue overrides off the step, then move it.
		removeMappingKey(vn, "pod")
		removeMappingKey(vn, "on")
		leaf.Content = append(leaf.Content, kn, vn)
		changed = true
	}
	return changed
}

// isStepNode reports whether a mapping is a plan STEP node — it has a top-level
// step-verb discriminator (run / check / agent-run / agent-check / include). A
// resource node (pod:/vm:/…) or a data node is not a step.
func isStepNode(m *yaml.Node) bool {
	if m == nil || m.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if stepKeywordSet[m.Content[i].Value] {
			return true
		}
	}
	return false
}

// scalarFieldValue returns the scalar value of key in m, or "" when absent /
// non-scalar.
func scalarFieldValue(m *yaml.Node, key string) string {
	if v := findMappingValue(m, key); v != nil && v.Kind == yaml.ScalarNode {
		return v.Value
	}
	return ""
}

// takeMappingEntry removes the key→value pair from m and returns both nodes
// (preserving the value node's comments + structure for re-insertion elsewhere).
func takeMappingEntry(m *yaml.Node, key string) (keyNode, valNode *yaml.Node, ok bool) {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil, nil, false
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			kn, vn := m.Content[i], m.Content[i+1]
			m.Content = append(m.Content[:i:i], m.Content[i+2:]...)
			return kn, vn, true
		}
	}
	return nil, nil, false
}

// ensureResourceChild finds or creates the resource child node `name` inside
// parentMap, returning the child's VALUE mapping (the mapping holding the
// resource disc — where nested children / steps are appended). A freshly
// created node is an agent-provisioned `pod` scaffold (`name: {pod:
// {agent_provisioned: true}}`); an EXISTING node (e.g. a declared peer with its
// own box:) is returned untouched.
func ensureResourceChild(parentMap *yaml.Node, name string) *yaml.Node {
	if existing := findMappingValue(parentMap, name); existing != nil && existing.Kind == yaml.MappingNode {
		return existing
	}
	podInner := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
		scalarNode("agent_provisioned"),
		{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"},
	}}
	childVal := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
		scalarNode("pod"), podInner,
	}}
	parentMap.Content = append(parentMap.Content, scalarNode(name), childVal)
	return childVal
}

// rewritePeerVarsInStep rewrites the retired cross-member address variables to
// the unified ${HOST:…} form across every scalar value in the step node (both
// ${PEER_HOST:m} and ${PEER_ENDPOINT:m:p} fold to ${HOST:…}; the :port segment,
// if any, is preserved). Returns whether anything changed.
func rewritePeerVarsInStep(n *yaml.Node) bool {
	changed := false
	var walk func(*yaml.Node)
	walk = func(nd *yaml.Node) {
		if nd == nil {
			return
		}
		if nd.Kind == yaml.ScalarNode {
			v := strings.ReplaceAll(nd.Value, "${PEER_HOST:", "${HOST:")
			v = strings.ReplaceAll(v, "${PEER_ENDPOINT:", "${HOST:")
			if v != nd.Value {
				nd.Value = v
				changed = true
			}
			return
		}
		for _, c := range nd.Content {
			walk(c)
		}
	}
	walk(n)
	return changed
}
