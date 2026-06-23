package main

// migrate_state_provision_verbs_to_plugin.go — the 2026-06 FIRST state-provision-verb
// extraction.
//
// A STATE-PROVISION verb carries BOTH a check (do:assert probe) AND an act (do:act
// provision). `unix_group` (getent-group probe + groupadd) was the first extracted;
// `user` (getent-passwd + useradd), `kernel-param` (sysctl read + write), `mount`
// (findmnt + mount), `command` (exec probe + install-task RUN) and finally `service`
// (supervisorctl/systemctl probe + enable the packaged unit) followed. Each left the
// closed `#Op`/`spec.OpVerbs` and became a BUILTIN plugin unit
// (plugin/builtins/{unix_group,user,kernel_param,mount,command,service}). user/unix_group/
// kernel-param/mount are BOTH a CheckVerbProvider AND a ProvisionActor; `command` is a
// CheckVerbProvider ONLY — its act IS the dedicated install-task emitCmd branch
// (`plugin == "command"` in emitTasks/renderOpCommand), NOT a RenderProvisionScript;
// `service` is the TYPED-STEP OUTLIER — a CheckVerbProvider AND a TypedStepProvider whose
// act lowers into a ServicePackagedStep (compileActOp) so the load-bearing reversals
// survive (a RenderProvisionScript shell string would drop them), plus a ProvisionActor
// for the runtime live-act path. A
// plan step that authored one inline now authors the generic plugin step `plugin: <verb>`
// + a typed `plugin_input:` validated against the unit's #*Input def.
//
// This is the SIBLING of the observe-only goss-verb migrator
// (migrate_goss_verbs_to_plugin.go), which CONVERTS only a `check:` step and STRIPS the
// verb keys on any other step — correct for an observe-only verb (a `run:` of it was
// vestigial). A state-provision verb's `run:` step is REAL (the act timeline), so this
// migrator CONVERTS a `check:` OR a `run:` step and STRIPS only on a verb-less step kind
// (agent-check/agent-run/include). The act-emit enabler renders the converted
// `run: {plugin: unix_group}` step at install emit.
//
// Companion fields move into plugin_input alongside the verb key. unix_group's companion
// is `gid`: it STAYS in #Op (the `user` verb's getent-passwd assertion still reads it) but
// MOVES into plugin_input on the unix_group step — exactly how the goss migrator moved the
// shared `running`/`reachable`/`addrs` for their extracted verbs.
//
// Gated on isStepNode so ONLY a real plan step is touched — the verb key inside the NEW
// `plugin_input:` block carries no step keyword, so a migrated config is a no-op
// (idempotent), and a non-step field of the same name (a Calamares `package-group:`'s
// fields, a box's published `port:`) is never rewritten. RAISES LatestSchemaVersion: a
// closed `#Op` no longer accepts the `unix_group:` key, so an un-migrated config is
// rejected with a `Run: charly migrate` hint. Comment-preserving (moves the original
// key/value nodes verbatim, builds only fresh scalar keys via scalarNode); TouchesHost
// false → remote-cache auto-migration applies it to fetched candy manifests. See CHANGELOG/.

import "gopkg.in/yaml.v3"

// stateProvisionVerbFields maps each EXTRACTED state-provision verb-key to the companion
// field keys that MOVE with it into plugin_input (in deterministic order). A step has at
// most one verb (Op.Kind enforces), so exactly one entry matches any step — `gid` appears
// in BOTH the unix_group and user field lists, but never on the same step (unix_group XOR
// user). Each verb's companions are the #Op fields read ONLY by that verb, now relocated
// into its builtin plugin unit's #*Input def:
//   - unix_group → gid          (#UnixGroupInput)
//   - user       → uid/gid/home/shell (#UserInput)
//   - kernel-param → value      (#KernelParamInput)
//   - mount      → mount_source/filesystem/opt (#MountInput)
//   - service    → running/enabled (#ServiceInput) — the TYPED-STEP-OUTLIER verb: its
//     companions running/enabled were #Op fields read ONLY by the service verb (process
//     reproduced `running` standalone in #ProcessInput and reads its own plugin_input),
//     so both move into plugin_input. Its do:act lowers into a TYPED ServicePackagedStep
//     (load-bearing reversals), not a RenderProvisionScript — but a check: OR a run:
//     service step migrates identically to the others.
//   - command    → background/from_host/in_container (#CommandInput) — the FIELD-SPLIT
//     case: ONLY the command-EXCLUSIVE fields move; the matchers exit_status/stdout/
//     stderr (shared via matchAll) and the general timeout/method/env STAY at step level
//     (#Op). `command` itself is ALSO a shared modifier (wl/libvirt argv), so it is the
//     command VERB only when no charly-verb is set — see migrateStateProvisionVerbStep.
var stateProvisionVerbFields = []struct {
	verb   string
	fields []string
}{
	{"unix_group", []string{"gid"}},
	{"user", []string{"uid", "gid", "home", "shell"}},
	{"kernel-param", []string{"value"}},
	{"mount", []string{"mount_source", "filesystem", "opt"}},
	{"service", []string{"running", "enabled"}},
	{"command", []string{"background", "from_host", "in_container"}},
}

// charlyVerbKeys are the live-container verb discriminators (cdp/wl/dbus/…). When any is
// present on a step, a sibling `command:` key is that verb's argv MODIFIER (wl: exec /
// libvirt: guest-exec), NOT the command verb — mirroring Op.VerbsSet's hasCharlyVerb
// guard. The command→plugin migration must then leave the `command:` key in place.
var charlyVerbKeys = []string{
	"cdp", "wl", "dbus", "vnc", "mcp", "record", "spice", "libvirt", "kube", "adb", "appium",
}

// stepHasCharlyVerb reports whether a plan step carries a live-container verb key, in
// which case a sibling `command:` is a modifier (argv), not the command verb.
func stepHasCharlyVerb(step *yaml.Node) bool {
	for _, v := range charlyVerbKeys {
		if k, _ := mappingEntry(step, v); k != nil {
			return true
		}
	}
	return false
}

// MigrateStateProvisionVerbsToPlugin rewrites every legacy state-provision-verb Op step
// (a check: OR a run: step) to the generic plugin step across a project's candy/ + box/
// dirs and root YAML siblings. Returns the rewritten file paths. Idempotent.
func MigrateStateProvisionVerbsToPlugin(dir string, dryRun bool) ([]string, error) {
	return runDocMigration(dir, dryRun, opUnifyCandidateFiles, stateProvisionVerbsToPluginDoc)
}

// stateProvisionVerbsToPluginDoc rewrites every plan STEP node carrying an extracted
// state-provision-verb Op in one document. Returns whether the document changed.
func stateProvisionVerbsToPluginDoc(doc *yaml.Node) bool {
	root := rootMappingNode(doc)
	if root == nil {
		return false
	}
	return stateProvisionVerbsToPluginWalk(root)
}

// stateProvisionVerbsToPluginWalk recurses every node, rewriting an extracted
// state-provision-verb Op only on a real plan step node (isStepNode). Mirrors
// gossVerbsToPluginWalk's recurse-everything shape.
func stateProvisionVerbsToPluginWalk(n *yaml.Node) bool {
	if n == nil {
		return false
	}
	changed := false
	if n.Kind == yaml.MappingNode && isStepNode(n) {
		if migrateStateProvisionVerbStep(n) {
			changed = true
		}
	}
	for _, c := range n.Content {
		if stateProvisionVerbsToPluginWalk(c) {
			changed = true
		}
	}
	return changed
}

// migrateStateProvisionVerbStep rewrites a step node carrying one extracted
// state-provision-verb key. A step has at most one verb (Op.Kind enforces), so the FIRST
// matching verb wins. A `check:` OR a `run:` step is CONVERTED to `plugin: <verb>` +
// `plugin_input:` (the verb + companion value nodes moved verbatim); a verb-less step kind
// (agent-*/include) has the vestigial keys STRIPPED. Returns whether the step changed.
func migrateStateProvisionVerbStep(step *yaml.Node) bool {
	for _, gv := range stateProvisionVerbFields {
		verbKey, verbVal := mappingEntry(step, gv.verb)
		if verbKey == nil {
			continue
		}
		// `command` is a SHARED #Op modifier: when a live-container verb is also present
		// (wl: exec / libvirt: guest-exec), the `command:` key is that verb's argv, NOT the
		// command verb — leave it in place (mirrors Op.VerbsSet's hasCharlyVerb guard).
		if gv.verb == "command" && stepHasCharlyVerb(step) {
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
		// A state-provision verb is dual-natured: a check: (assert) step OR a run: (act)
		// step is a real authoring of the verb and CONVERTS. Only a verb-less step kind
		// carried the keys vestigially.
		checkKey, _ := mappingEntry(step, "check")
		runKey, _ := mappingEntry(step, "run")

		// Both branches drop the step-level verb + companion Op keys.
		removeMappingKey(step, gv.verb)
		for _, f := range gv.fields {
			removeMappingKey(step, f)
		}

		if checkKey == nil && runKey == nil {
			// A verb-less step kind: the inline verb/companion keys were vestigial — strip only.
			return true
		}

		// A check: or run: step: rebuild as the generic plugin step. The original verb +
		// companion key/value nodes move verbatim into plugin_input: (comments and node
		// style preserved); only the plugin:/plugin_input: keys are fresh.
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
