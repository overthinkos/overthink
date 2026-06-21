package main

// migrate_unified_node.go — the forward migration to the UNIFIED node-form model
// ("everything is a node"). Every legacy kind-keyed entity (a `candy:`/`box:`/`vm:`/…
// single entity, or a root-shape `<kind>: {name → entity}` map) is rewritten
// name-first as `<name>: {<kind>: <SCALARS>, <child-nodes>…}`: the kind value keeps
// only SCALAR fields, every NON-scalar field (composition / collection / object)
// becomes a child node `<name>-<key>: {<key>: <value>}`, each plan step becomes a
// child step node, and deploy/check nested/peer members become sub-entity children
// (`deploy:`/`check:` → `bundle`). Comment-preserving (yaml.v3 node API); idempotent
// (a node-form doc has no legacy kind-map key → no-op).

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// legacyKindMapKeys are the top-level kind-map keys the migration rewrites into
// name-first node-form. (Directives version/import/discover/defaults/repo/provides
// are preserved verbatim.)
var legacyKindMapKeys = map[string]bool{
	"candy": true, "box": true, "vm": true, "pod": true, "k8s": true,
	"local": true, "android": true, "distro": true, "builder": true, "init": true,
	"resource": true, "sidecar": true, "agent": true, "group": true, "module": true,
	"target": true,
	// deploy/check carry target + nested/peer members → migrateDeployEntity.
	"deploy": true, "check": true,
}

// MigrateUnifiedNode rewrites every legacy kind-keyed config in a project
// (candy/ + box/ + root YAML) into the unified node-form. Comment-preserving,
// idempotent. Returns the rewritten file paths.
func MigrateUnifiedNode(dir string, dryRun bool) ([]string, error) {
	return runDocMigration(dir, dryRun, opUnifyCandidateFiles, migrateUnifiedNodeDoc)
}

// nodeMigrationJob is one legacy kind-map entity queued for node-form emission.
// keyNode is the top-level key node (reused for comment preservation, or
// synthesized for a single-entity doc); its .Value is set to final at emit time.
type nodeMigrationJob struct {
	keyNode  *yaml.Node
	body     *yaml.Node
	kind     string
	origName string
	isDeploy bool
	final    string // assigned globally-unique top-level name ("" until assigned)
}

// nodeEmitSlot preserves document order: a slot is either a verbatim directive
// (verbatimK non-nil) or a run of entity jobs produced from one kind-map.
type nodeEmitSlot struct {
	verbatimK, verbatimV *yaml.Node
	jobs                 []*nodeMigrationJob
}

// migrateUnifiedNodeDoc rewrites one document's legacy kind-keyed entities into
// node-form. Returns true if it changed anything. Idempotent.
//
// Cross-kind name reuse was legal in the kind-keyed format (a name could appear
// under SEPARATE `deploy:` / `local:` / `vm:` maps), but a node-form document's
// top-level names are GLOBALLY UNIQUE — each entity flattens to one top-level
// `<name>:` key, so two same-named entities would collide on one YAML key (and
// CUE would unify their child nodes, e.g. two `<name>-env` children with
// incompatible env lists). This migration resolves such a collision the same way
// the convention dictates: the user-facing bundle (`deploy:`/`check:`) KEEPS the
// bare name; a colliding template (`box`/`local`/`vm`/`k8s`/`android`) is renamed
// `<name>-<kind>` (then -2…), and the bundle's cross-reference to it is rewritten.
func migrateUnifiedNodeDoc(doc *yaml.Node) bool {
	root := rootMappingNode(doc)
	if root == nil {
		return false
	}
	var slots []nodeEmitSlot
	var allJobs []*nodeMigrationJob
	used := map[string]bool{} // top-level names already taken (verbatim directives + assigned entities)
	for i := 0; i+1 < len(root.Content); i += 2 {
		k, v := root.Content[i], root.Content[i+1]
		// Keep verbatim when the key is a non-kind directive OR the entry is
		// ALREADY a node-form entity that merely happens to be NAMED after a kind
		// word (`vm: {vm: …}` — a node named `vm`). Without this guard such an
		// entity matches legacyKindMapKeys["vm"], enters the legacy-kind-map path,
		// and is rebuilt byte-identically but with changed=true — so
		// `charly migrate --dry-run` perpetually (wrongly) reports `would apply
		// unified-node`. nodeShapedValue(v) detects the kind-discriminator child;
		// the !mapHasKey(v,"name") guard keeps the skip UNAMBIGUOUS — a genuine
		// legacy single-entity body (`vm: {name: …, …}`, which may even carry a
		// kind-word CROSS-REF child like vm:/box:/local:) still has its `name:` key
		// and migrates. A legacy COLLECTION (`vm: {arch: {…}, cachyos: {…}}`) has
		// entity-name children (not kind words) so nodeShapedValue is false → it
		// still converts.
		if !legacyKindMapKeys[k.Value] || (nodeShapedValue(v) && !mapHasKey(v, "name")) {
			slots = append(slots, nodeEmitSlot{verbatimK: k, verbatimV: v}) // directive or already a node → keep
			used[k.Value] = true                                            // reserve so no entity is renamed onto it
			continue
		}
		kind := k.Value
		isDeploy := kind == "deploy" || kind == "check"
		var jobs []*nodeMigrationJob
		// A single-entity kind-keyed doc has a `name:` child; a root-shape map is
		// {entityName → entityBody}.
		if mapHasKey(v, "name") {
			name := ""
			if nv := mapValue(v, "name"); nv != nil {
				name = nv.Value
			}
			// The new name key carries the kind-map key's comments (a banner above
			// `candy:`/`vm:`/… would otherwise be lost — scalarNode starts bare).
			nameKey := scalarNode(name)
			carryKeyComments(nameKey, k)
			jobs = append(jobs, &nodeMigrationJob{keyNode: nameKey, body: v, kind: kind, origName: name, isDeploy: isDeploy})
		} else if v.Kind == yaml.MappingNode {
			for j := 0; j+1 < len(v.Content); j += 2 {
				en, eb := v.Content[j], v.Content[j+1]
				// REUSE the original entity-name key node so its own comments survive;
				// the kind-map key's banner (its HeadComment) moves onto the FIRST
				// entity so the whole-section documentation is not dropped.
				if j == 0 {
					prependHeadComment(en, k.HeadComment)
				}
				jobs = append(jobs, &nodeMigrationJob{keyNode: en, body: eb, kind: kind, origName: en.Value, isDeploy: isDeploy})
			}
		}
		slots = append(slots, nodeEmitSlot{jobs: jobs})
		allJobs = append(allJobs, jobs...)
	}
	if len(allJobs) == 0 {
		return false // node-form already (no legacy kind map) → idempotent no-op
	}

	// Assign globally-unique final top-level names. A uniquely-named entity keeps
	// its bare name; within a collision group the bundle wins the bare name and the
	// template is suffixed. uniqueChildName disambiguates against `used` (numeric
	// `-2` fallback) so a generated suffix can never clobber a distinct entity.
	count := map[string]int{}
	for _, j := range allJobs {
		count[j.origName]++
	}
	renamed := map[string]string{} // crossRefKey(kind, origName) → final, only when changed
	assign := func(pred func(*nodeMigrationJob) bool, desired func(*nodeMigrationJob) string) {
		for _, j := range allJobs {
			if j.final != "" || !pred(j) {
				continue
			}
			j.final = uniqueChildName(used, desired(j))
			if j.final != j.origName {
				renamed[crossRefKey(j.kind, j.origName)] = j.final
			}
		}
	}
	bare := func(j *nodeMigrationJob) string { return j.origName }
	suffixed := func(j *nodeMigrationJob) string { return j.origName + "-" + j.kind }
	assign(func(j *nodeMigrationJob) bool { return count[j.origName] == 1 }, bare) // unique → bare
	assign(func(j *nodeMigrationJob) bool { return j.isDeploy }, bare)             // collision: bundle keeps bare
	assign(func(j *nodeMigrationJob) bool { return true }, suffixed)               // collision: template renamed

	rebuilt := make([]*yaml.Node, 0, len(root.Content))
	for _, s := range slots {
		if s.verbatimK != nil {
			rebuilt = append(rebuilt, s.verbatimK, s.verbatimV)
			continue
		}
		for _, j := range s.jobs {
			j.keyNode.Value = j.final
			rebuilt = append(rebuilt, j.keyNode, entityToNodeContent(j.kind, j.final, j.body, j.isDeploy))
		}
	}
	root.Content = rebuilt
	if len(renamed) > 0 {
		rewriteBundleCrossRefs(root, renamed) // point every bundle cross-ref at its renamed template
	}
	return true
}

// bundleCrossRefKeys are the bundle-value scalar keys that NAME another top-level
// entity (the key equals the referenced entity's kind). When a referenced template
// is renamed for uniqueness, the cross-ref must follow.
var bundleCrossRefKeys = map[string]bool{
	"box": true, "vm": true, "k8s": true, "local": true, "android": true,
}

// crossRefKey keys the rename map by (kind, name) so a `box: x` and a `vm: x`
// cross-ref never alias the same rename entry.
func crossRefKey(kind, name string) string { return kind + "\x00" + name }

// rewriteBundleCrossRefs walks the rebuilt tree and, inside every `bundle:` value
// mapping (top-level and nested), rewrites each cross-ref scalar whose target was
// renamed. Non-bundle structures carry no `bundle:` key, so they are untouched.
func rewriteBundleCrossRefs(node *yaml.Node, renamed map[string]string) {
	if node == nil {
		return
	}
	switch node.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			k, v := node.Content[i], node.Content[i+1]
			if k.Value == "bundle" && v.Kind == yaml.MappingNode {
				for j := 0; j+1 < len(v.Content); j += 2 {
					rk, rv := v.Content[j], v.Content[j+1]
					if rv.Kind != yaml.ScalarNode || !bundleCrossRefKeys[rk.Value] {
						continue
					}
					if fn, ok := renamed[crossRefKey(rk.Value, rv.Value)]; ok {
						rv.Value = fn
					}
				}
			}
			rewriteBundleCrossRefs(v, renamed)
		}
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, c := range node.Content {
			rewriteBundleCrossRefs(c, renamed)
		}
	}
}

// entityToNodeContent builds the node CONTENT mapping from a legacy entity body
// (name excluded — it's the node key). The kind value holds only SCALARS; every
// non-scalar field (composition / collection / single object) becomes one child
// node `<name>-<key>: {<key>: <value>}`, and each plan step becomes a child step
// node — the unified "everything is a node" shape.
func entityToNodeContent(kind, name string, body *yaml.Node, isDeploy bool) *yaml.Node {
	if isDeploy {
		return migrateDeployEntity(name, body)
	}
	content := &yaml.Node{Kind: yaml.MappingNode}
	value := &yaml.Node{Kind: yaml.MappingNode}
	content.Content = append(content.Content, scalarNode(kind), value)
	explodeFields(name, body, value, content, map[string]bool{kind: true}, nil)
	return content
}

// migrateDeployEntity rewrites a legacy deploy/check entity into a node-form node:
// scalars (disposable/host/box/vm/…) stay in the value; nested/peer members become
// resource child nodes (recursive; target inferred by buildBundleNode);
// other non-scalars (env/port/add_candy/iterate/…) + plan steps become child nodes.
// The legacy `target:` key is dropped — its classification is preserved by
// bundleDiscForEntity (a `bundle` disc when a cross-ref infers the target, else the
// kind disc directly for a template-less deploy).
func migrateDeployEntity(name string, body *yaml.Node) *yaml.Node {
	content := &yaml.Node{Kind: yaml.MappingNode}
	value := &yaml.Node{Kind: yaml.MappingNode}
	disc := bundleDiscForEntity(body)
	content.Content = append(content.Content, scalarNode(disc), value)
	used := map[string]bool{disc: true}
	for i := 0; i+1 < len(body.Content); i += 2 {
		k, v := body.Content[i], body.Content[i+1]
		if k.Value == "nested" || k.Value == "peer" {
			for j := 0; j+1 < len(v.Content); j += 2 {
				mn, mb := v.Content[j], v.Content[j+1]
				content.Content = append(content.Content, scalarNode(mn.Value), migrateDeployEntity(mn.Value, mb))
				used[mn.Value] = true // a member name reserves a child key
			}
		}
	}
	explodeFields(name, body, value, content, used, map[string]bool{"target": true, "nested": true, "peer": true})
	return content
}

// bundleDiscForEntity picks the node-form discriminator for a legacy deploy/check
// entity whose `target:` key is about to be dropped. A bundle carrying a same-kind
// cross-ref scalar (box/vm/local/k8s/android) uses `bundle:` and lets
// buildBundleNode infer the workload target from that cross-ref — the common case
// (e.g. `bundle: {vm: arch}`). But a TEMPLATE-LESS deploy — an add_candy-only
// `target: local` overlay with NO cross-ref — has nothing to infer from: dropping
// its `target:` under a bare `bundle:` leaves a group node (empty target) that
// classifyTarget defaults to a pod, which then mis-routes a guest-shell check to
// `podman exec` (the check-arch-vm nested-local regression). Such an entity must
// carry its kind discriminator (`local:`) directly so the classification survives.
func bundleDiscForEntity(body *yaml.Node) string {
	// Legacy box/vm/k8s/local/android cross-ref (the unified-node migration of a
	// VERY old config): emit `bundle:` and let the later edge-inherit migration step
	// convert it to the substrate kind. Current saves never carry these keys.
	if body != nil {
		for i := 0; i+1 < len(body.Content); i += 2 {
			if bundleCrossRefKeys[body.Content[i].Value] {
				return "bundle"
			}
		}
	}
	// EDGE-INHERIT cutover B: the substrate kind IS the discriminator at the edge.
	// The SAVE path marshals BundleNode.Target (loader-derived), so the disc is that
	// target; an empty target is a targetless deploy GROUP. (`host` is the pre-rename
	// spelling of `local`.)
	switch t := scalarFieldValue(body, "target"); t {
	case "host":
		return "local"
	case "":
		return "group"
	default:
		return t // pod | vm | k8s | local | android
	}
}

// explodeFields splits a legacy entity body: SCALAR fields append to value; every
// nodeDataKeys field becomes a child `<name>-<key>: {<key>: <value>}`; each plan
// step becomes a child step node; `name` and any skip key are dropped. used tracks
// the child-node names already taken under this parent so a generated data-child
// name and a step's id can't collide into one YAML key.
func explodeFields(name string, body, value, content *yaml.Node, used, skip map[string]bool) {
	for i := 0; i+1 < len(body.Content); i += 2 {
		k, v := body.Content[i], body.Content[i+1]
		switch {
		case k.Value == "name" || (skip != nil && skip[k.Value]):
			// dropped (name → node key; target/nested/peer handled elsewhere)
		case k.Value == "plan":
			appendStepChildren(name, v, content, used)
		case dataKeySet[k.Value]:
			child := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{k, v}}
			content.Content = append(content.Content, scalarNode(uniqueChildName(used, name+"-"+k.Value)), child)
		default:
			value.Content = append(value.Content, k, v) // scalar
		}
	}
}

// appendStepChildren turns each legacy plan step into a child step node, keyed by
// the step's id when present, else `<entity>-step-<index>`, disambiguated against
// used so a step id matching a data-child name doesn't collide.
func appendStepChildren(entity string, plan, content *yaml.Node, used map[string]bool) {
	if plan == nil || plan.Kind != yaml.SequenceNode {
		return
	}
	for idx, step := range plan.Content {
		content.Content = append(content.Content, scalarNode(uniqueChildName(used, stepNodeName(entity, step, idx))), step)
	}
}

// stepNodeName names a step child: its `id` if set, else `<entity>-step-<index>`.
func stepNodeName(entity string, step *yaml.Node, idx int) string {
	if id := mapValue(step, "id"); id != nil && id.Value != "" {
		return id.Value
	}
	return fmt.Sprintf("%s-step-%d", entity, idx)
}

// uniqueChildName returns desired if it is not yet taken under the parent, else the
// first free `desired-2` / `desired-3` / … variant, recording the choice in used.
// Child node names must be unique within a node — a generated data-child name
// (`<name>-<key>`) and a plan step whose id equals it would otherwise collide into
// ONE YAML key and silently merge their fields (a list `package` + a scalar probe
// `package`, the bug this fixes).
func uniqueChildName(used map[string]bool, desired string) string {
	if !used[desired] {
		used[desired] = true
		return desired
	}
	for i := 2; ; i++ {
		c := fmt.Sprintf("%s-%d", desired, i)
		if !used[c] {
			used[c] = true
			return c
		}
	}
}

// carryKeyComments copies a source key node's comments onto dst (used when a
// kind-map key is replaced by a name key — its banner/inline/foot comments move).
func carryKeyComments(dst, src *yaml.Node) {
	dst.HeadComment = src.HeadComment
	dst.LineComment = src.LineComment
	dst.FootComment = src.FootComment
}

// prependHeadComment prepends head to n's HeadComment (no-op when head is empty),
// preserving n's own head comment below the relocated banner.
func prependHeadComment(n *yaml.Node, head string) {
	if head == "" {
		return
	}
	if n.HeadComment == "" {
		n.HeadComment = head
		return
	}
	n.HeadComment = head + "\n" + n.HeadComment
}
