package main

import "gopkg.in/yaml.v3"

// kind_builtins.go now hosts only `candy` — the lone remaining manifest-listed builtin
// KindProvider, the box⊻layer factory arm whose DecodeNode routes to two different core
// maps (uf.Box vs uf.Candy). Every OTHER kind has been extracted into its own dedicated
// file (the tombstone comments below are the navigation map: which kind went where and
// how). A built-in KindProvider decodes via DecodeNode (no JSON) — normalizeNodeInto
// resolves the node's discriminator through providerRegistry.ResolveKind and calls it;
// CueDefPath carries the former reservedKindHandlers value (the CUE def the node value
// validates against).

// candy — the special factory arm (buildCandy returns name + InlineCandy).
type candyKind struct{ builtinKindBase }

func (candyKind) Reserved() string   { return "candy" }
func (candyKind) CueDefPath() string { return "#Candy" }

// DecodeNode — EDGE-INHERIT cutover D: `box:` merged INTO `candy:`. A `candy:` node
// that carries the box base⊻from MARKER (base: or from:) is a full IMAGE (the former
// box:) → decode as BoxConfig into uf.Box; otherwise it is a LAYER fragment → uf.Candy.
func (candyKind) DecodeNode(gn *genericNode, uf *UnifiedFile) error {
	if candyIsImage(gn) {
		var b BoxConfig
		if err := decodeNodeValue(gn, &b); err != nil {
			return err
		}
		ensureMap(&uf.Box)
		uf.Box[gn.name] = b
		return nil
	}
	name, ic, err := buildCandy(gn)
	if err != nil {
		return err
	}
	ensureMap(&uf.Candy)
	uf.Candy[name] = ic
	return nil
}

// candyIsImage reports whether a candy: node is a full IMAGE (the former box:): it
// carries the box base⊻from marker — `base:` (an external base) or `from:` (a builder
// ref). A LAYER fragment has neither (no layer-candy uses `from:` in the corpus).
func candyIsImage(gn *genericNode) bool {
	dv := gn.discValue
	if dv == nil || dv.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(dv.Content); i += 2 {
		switch dv.Content[i].Value {
		case "base", "from":
			return true
		}
	}
	return false
}

// The `sidecar` KIND (the sidecar-container template library) is no longer a core
// builtin kind — it was extracted into a dedicated plugin UNIT (plugin_sidecar.go +
// plugin/builtins/sidecar), mirroring the agent/module kind→plugin extractions. A
// `sidecar:` node now routes through runPluginKind (Invoke/OpLoad) into
// uf.PluginKinds["sidecar"], validated against the plugin's served #SidecarInput schema;
// UnifiedFile.Sidecars() reads it back into the name-keyed map[string]SidecarDef the
// deploy/quadlet code consumes.

// The build-vocabulary kinds (distro / builder / init / resource) and the Calamares
// install `target` kind are no longer core builtin kinds — each was extracted into a
// dedicated plugin UNIT (plugin_distro.go + plugin/builtins/distro, plugin_builder_kind.go
// + plugin/builtins/builder, plugin_init.go + plugin/builtins/init, plugin_resource.go +
// plugin/builtins/resource, plugin_target.go + plugin/builtins/target), mirroring the
// sidecar/agent/module kind→plugin extractions. Such a node now routes through
// runPluginKind (Invoke/OpLoad) into uf.PluginKinds["<kind>"], validated against the
// plugin's served #<Kind>Input schema; the build-vocab accessors UnifiedFile.Distros() /
// .Builders() / .Inits() / .Resources() read those bodies back into the typed name-keyed
// maps the generator/format/GPU-arbitration code consumes (target has zero core readers,
// so — like module/package-group — no accessor). The binary-embedded build vocabulary
// (authored `distro:`/`builder:`/`init:`/`resource:` nodes in charly/charly.yml) flows
// through the SAME plugin path and merges root-wins via the generic mergePluginKindsMap.

// The `agent` KIND (the AI-CLI grader catalog) is no longer a core builtin kind — it
// was extracted into a dedicated plugin UNIT (plugin_agent.go + plugin/builtins/agent),
// mirroring the package-group kind→plugin extraction. An `agent:` node now routes
// through runPluginKind (Invoke/OpLoad) into uf.PluginKinds["agent"], validated against
// the plugin's served #AgentInput schema; UnifiedFile.Agents() reads it back into the
// name-keyed map[string]*AgentConfig the harness consumes.

// The `group` deploy-shape KIND (the targetless deploy group) is no longer a builtin
// kind decoded HERE — it was extracted into its OWN dedicated-builtin KindProvider file
// (plugin_group.go), mirroring the deploy-target/step/builder dedicated-provider pattern.
// It stays an in-proc KindProvider (typed DecodeNode → the core buildBundleNodeInto
// recursion helper, node_bundle.go) because it recurses over the genericNode member tree
// and lands in the typed core uf.Bundle map; it is absent from builtinProviderInstances +
// the `providers:` manifest and self-registers via registerDedicatedBuiltin.

// The Calamares package group (`package-group:`) is no longer a core builtin kind —
// it was extracted into a dedicated plugin UNIT (plugin_package_group.go +
// plugin/builtins/package-group), the first kind→plugin extraction. A
// `package-group:` node now routes through runPluginKind (Invoke/OpLoad) into
// uf.PluginKinds, validated against the plugin's served #PackageGroupInput schema.

// The `module` KIND (the Calamares installer module) is no longer a core builtin kind
// — it was extracted into a dedicated plugin UNIT (plugin_module.go +
// plugin/builtins/module), mirroring the package-group kind→plugin extraction. A
// `module:` node now routes through runPluginKind (Invoke/OpLoad) into
// uf.PluginKinds["module"], validated against the plugin's served #ModuleInput schema.

// The 5 resource-substrate deploy-shape KINDS (pod/vm/k8s/local/android) are no longer
// builtin kinds decoded HERE — they were extracted into the parameterized dedicated-builtin
// KindProvider standaloneKind (plugin_substrate.go), mirroring the group extraction
// (plugin_group.go) and the deploy-target/step/builder dedicated-provider pattern. Each stays
// an in-proc KindProvider (typed DecodeNode → the core buildBundleNodeInto /
// buildStandaloneResource helpers) because it recurses over the genericNode member tree and
// lands in the typed core maps (uf.Bundle for a deploy, uf.Pod/uf.VM/uf.K8s/uf.Local/
// uf.Android for a bare template); the 5 instances are absent from builtinProviderInstances +
// the `providers:` manifest and self-register via registerDedicatedBuiltin.
