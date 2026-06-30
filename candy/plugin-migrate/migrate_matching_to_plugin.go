package migrate

// migrate_matching_to_plugin.go — the 2026-06 `matching` verb-extraction migration.
//
// The `matching` check verb (pure in-process value matching, no target probe) left
// the closed `#Op`/`spec.OpVerbs` and became a plugin unit (now the compiled-in
// candy candy/plugin-matching). A plan step that authored the verb inline as
// `matching: <value>` (+ optional goss-style `contains:` matchers) now authors it
// through the generic plugin step: `plugin: matching` + a typed `plugin_input:`
// ({matching, contains}) validated against the plugin's own #MatchingInput schema.
//
// This step rewrites every legacy `matching:` Op carried by a plan STEP node:
//
//   - a DETERMINISTIC step (carries `check:`) → CONVERT: move the `matching:` and
//     optional `contains:` value nodes verbatim into a new `plugin_input:` mapping
//     and add `plugin: matching`.
//   - any OTHER step (`agent-check:`/`agent-run:`/`run:`/`include:` — where the
//     inline `matching:`/`contains:` were vestigial Op fields the verb ignored)
//     → STRIP the `matching:`/`contains:` keys (no plugin added).
//
// Gated on isStepNode so ONLY a real plan step is touched — the `matching:` key
// inside the NEW `plugin_input:` block carries no step keyword, so a migrated
// config is a no-op (idempotent), and an unrelated data field named `matching` is
// never rewritten. RAISES LatestSchemaVersion: a closed `#Op` no longer accepts a
// `matching:` key, so an un-migrated config must be rejected with a `Run: charly
// migrate` hint rather than a cryptic closed-schema error. Comment-preserving
// (moves the original key/value nodes verbatim, builds only fresh scalar keys via
// scalarNode — no marshal/remarshal of scalars); TouchesHost false → remote-cache
// auto-migration applies it to fetched candy manifests. See CHANGELOG/.

import "gopkg.in/yaml.v3"

// MigrateMatchingToPlugin rewrites every legacy `matching:` Op step to the generic
// plugin step (or strips the vestigial keys on a non-check step) across a project's
// candy/ + box/ dirs and root YAML siblings. Returns the rewritten file paths.
// Idempotent.
func MigrateMatchingToPlugin(dir string, dryRun bool) ([]string, error) {
	return runDocMigration(dir, dryRun, opUnifyCandidateFiles, matchingToPluginDoc)
}

// matchingToPluginDoc rewrites every plan STEP node carrying a `matching:` Op in one
// document. Returns whether the document changed.
func matchingToPluginDoc(doc *yaml.Node) bool {
	root := rootMappingNode(doc)
	if root == nil {
		return false
	}
	return matchingToPluginWalk(root)
}

// matchingToPluginWalk recurses every node, rewriting a `matching:` Op only on a
// real plan step node (isStepNode). Mirrors evalCheckWalk's recurse-everything shape.
func matchingToPluginWalk(n *yaml.Node) bool {
	if n == nil {
		return false
	}
	changed := false
	if n.Kind == yaml.MappingNode && isStepNode(n) {
		if k, _ := mappingEntry(n, "matching"); k != nil {
			if migrateMatchingStep(n) {
				changed = true
			}
		}
	}
	for _, c := range n.Content {
		if matchingToPluginWalk(c) {
			changed = true
		}
	}
	return changed
}

// migrateMatchingStep rewrites a step node that carries a `matching:` Op. A step
// with `check:` is CONVERTED to `plugin: matching` + `plugin_input:` (the matching:
// and optional contains: value nodes moved verbatim, preserving comments/style);
// any other step has the vestigial `matching:`/`contains:` keys STRIPPED. Returns
// whether the step changed.
func migrateMatchingStep(step *yaml.Node) bool {
	matchKey, matchVal := mappingEntry(step, "matching")
	if matchKey == nil {
		return false
	}
	// Capture the contains: pair (if any) before removal — removeMappingKey re-slices
	// step.Content but never mutates the captured node objects, so these stay valid.
	containsKey, containsVal := mappingEntry(step, "contains")
	checkKey, _ := mappingEntry(step, "check")

	// Both branches drop the step-level matching:/contains: Op keys.
	removeMappingKey(step, "matching")
	removeMappingKey(step, "contains")

	if checkKey == nil {
		// A non-check step (agent-check/agent-run/run/include): the inline
		// matching:/contains: were vestigial Op fields the verb ignored — strip only.
		return true
	}

	// A deterministic check step: rebuild as the generic plugin step. The original
	// matching:/contains: key+value nodes move verbatim into plugin_input: (comments
	// and node style preserved); only the plugin:/plugin_input: keys are fresh.
	pluginInput := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	pluginInput.Content = append(pluginInput.Content, matchKey, matchVal)
	if containsKey != nil {
		pluginInput.Content = append(pluginInput.Content, containsKey, containsVal)
	}
	step.Content = append(step.Content,
		scalarNode("plugin"), scalarNode("matching"),
		scalarNode("plugin_input"), pluginInput,
	)
	return true
}
