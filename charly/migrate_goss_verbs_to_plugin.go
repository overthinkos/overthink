package main

// migrate_goss_verbs_to_plugin.go — the 2026-06 observe-only goss-verb extraction.
//
// The observe-only goss check verbs — `process` (pgrep), `port` (listening/reachable),
// and `dns` (resolve) — left the closed `#Op`/`spec.OpVerbs` and became BUILTIN plugin
// units (plugin/builtins/{process,port,dns}). A plan step that authored such a verb
// inline (e.g. `process: redis-server` + `running: true`) now authors the generic
// plugin step `plugin: <verb>` + a typed `plugin_input:` validated against the plugin's
// own #<Verb>Input schema.
//
// The STATE-PROVISION goss verbs `package`/`service` are NOT extracted: they carry act
// (provision) forms (RenderProvisionScript) AND lower to install-plan steps
// (SystemPackagesStep / ServicePackagedStep) — the package-install / service-enable
// install timeline the check-only plugin model does not cover. They remain base #Op
// verbs, so this migrator never touches a `package:`/`service:` step.
//
// Per step node, for the matched verb key:
//   - a DETERMINISTIC step (carries `check:`) → CONVERT: move the verb key + its
//     companion field nodes verbatim into a new `plugin_input:` mapping and add
//     `plugin: <verb>`.
//   - any OTHER step (`agent-check:`/`agent-run:`/`run:`/`include:`) → STRIP the verb
//     key + companion keys (vestigial Op fields the verb ignored).
//
// Companion fields include SHARED #Op fields (port's `reachable` → also `addr`; dns's
// `addrs` → also `interface`; process's `running` → also `service`): on the EXTRACTED
// verb's step they MOVE into plugin_input, while the field STAYS in #Op for the
// non-extracted verb. `exclude_distro` is a GENERIC step modifier (skip-on-distro) and
// is never listed, so it never moves.
//
// Gated on isStepNode so ONLY a real plan step is touched — the verb key inside the NEW
// `plugin_input:` block carries no step keyword, so a migrated config is a no-op
// (idempotent), and a non-step field of the same name (a box's published `port:` or a
// candy's `package:` install list) is never rewritten. RAISES LatestSchemaVersion: a
// closed `#Op` no longer accepts these verb keys, so an un-migrated config is rejected
// with a `Run: charly migrate` hint. Comment-preserving (moves the original key/value
// nodes verbatim, builds only fresh scalar keys via scalarNode); TouchesHost false →
// remote-cache auto-migration applies it to fetched candy manifests. See CHANGELOG/.

import "gopkg.in/yaml.v3"

// gossVerbFields maps each EXTRACTED observe-only goss verb-key to the companion field
// keys that MOVE with it into plugin_input on a check step (in deterministic order). A
// shared companion (process→running [service], port→reachable [addr], dns→addrs
// [interface]) STAYS in #Op for the non-extracted verb but MOVES here for the extracted
// verb's step — exactly how the matching extraction moved the shared `contains`.
var gossVerbFields = []struct {
	verb   string
	fields []string
}{
	{"process", []string{"running"}},
	{"port", []string{"listening", "ip", "reachable"}},
	{"dns", []string{"resolvable", "addrs", "server"}},
}

// MigrateGossVerbsToPlugin rewrites every legacy observe-only goss-verb Op step to the
// generic plugin step (or strips the vestigial keys on a non-check step) across a
// project's candy/ + box/ dirs and root YAML siblings. Returns the rewritten file
// paths. Idempotent.
func MigrateGossVerbsToPlugin(dir string, dryRun bool) ([]string, error) {
	return runDocMigration(dir, dryRun, opUnifyCandidateFiles, gossVerbsToPluginDoc)
}

// gossVerbsToPluginDoc rewrites every plan STEP node carrying an extracted goss-verb Op
// in one document. Returns whether the document changed.
func gossVerbsToPluginDoc(doc *yaml.Node) bool {
	root := rootMappingNode(doc)
	if root == nil {
		return false
	}
	return gossVerbsToPluginWalk(root)
}

// gossVerbsToPluginWalk recurses every node, rewriting an extracted goss-verb Op only on
// a real plan step node (isStepNode). Mirrors matchingToPluginWalk's recurse-everything
// shape.
func gossVerbsToPluginWalk(n *yaml.Node) bool {
	if n == nil {
		return false
	}
	changed := false
	if n.Kind == yaml.MappingNode && isStepNode(n) {
		if migrateGossVerbStep(n) {
			changed = true
		}
	}
	for _, c := range n.Content {
		if gossVerbsToPluginWalk(c) {
			changed = true
		}
	}
	return changed
}

// migrateGossVerbStep rewrites a step node that carries one extracted goss-verb key. A
// step has at most one verb (Op.Kind enforces), so the FIRST matching verb wins. A
// `check:` step is CONVERTED to `plugin: <verb>` + `plugin_input:` (the verb + companion
// value nodes moved verbatim); any other step has the vestigial keys STRIPPED. Returns
// whether the step changed.
func migrateGossVerbStep(step *yaml.Node) bool {
	for _, gv := range gossVerbFields {
		verbKey, verbVal := mappingEntry(step, gv.verb)
		if verbKey == nil {
			continue
		}
		// Capture the companion field pairs (if present) before removal — removeMappingKey
		// re-slices step.Content but never mutates the captured node objects.
		type companion struct{ k, v *yaml.Node }
		var companions []companion
		for _, f := range gv.fields {
			if k, v := mappingEntry(step, f); k != nil {
				companions = append(companions, companion{k, v})
			}
		}
		checkKey, _ := mappingEntry(step, "check")

		// Both branches drop the step-level verb + companion Op keys.
		removeMappingKey(step, gv.verb)
		for _, f := range gv.fields {
			removeMappingKey(step, f)
		}

		if checkKey == nil {
			// A non-check step: the inline verb/companion keys were vestigial — strip only.
			return true
		}

		// A deterministic check step: rebuild as the generic plugin step. The original
		// verb + companion key/value nodes move verbatim into plugin_input: (comments and
		// node style preserved); only the plugin:/plugin_input: keys are fresh.
		pluginInput := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		pluginInput.Content = append(pluginInput.Content, verbKey, verbVal)
		for _, c := range companions {
			pluginInput.Content = append(pluginInput.Content, c.k, c.v)
		}
		step.Content = append(step.Content,
			scalarNode("plugin"), scalarNode(gv.verb),
			scalarNode("plugin_input"), pluginInput,
		)
		return true
	}
	return false
}
