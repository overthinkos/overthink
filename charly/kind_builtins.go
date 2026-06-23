package main

import "gopkg.in/yaml.v3"

// The built-in kinds as KindProviders. Each wraps its existing decode logic
// unchanged — the migration is behavior-preserving; only the normalizeNodeInto
// dispatch switch is replaced by providerRegistry.ResolveKind. CueDefPath carries
// the former reservedKindHandlers value (the CUE def the node value validates
// against).

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

type groupKind struct{ builtinKindBase }

func (groupKind) Reserved() string   { return "group" }
func (groupKind) CueDefPath() string { return "#Deploy" }

// DecodeNode — EDGE-INHERIT cutover C: group: is UNAMBIGUOUSLY a TARGETLESS deploy
// group (resource members, no own workload — the former targetless `bundle:`). The
// Calamares package group moved to its own `package-group:` kind, so the former
// shape-routing is gone.
func (groupKind) DecodeNode(gn *genericNode, uf *UnifiedFile) error {
	return buildBundleNodeInto(gn, uf)
}

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

// standaloneKind — the 5 resource-deploy kinds (pod/vm/k8s/local/android). A
// standalone entity unless it carries resource children, in which case it is a
// bundle-shaped node → the bundle builder. Parameterized by word + def.
type standaloneKind struct {
	builtinKindBase
	word string
	def  string
}

func (k standaloneKind) Reserved() string   { return k.word }
func (k standaloneKind) CueDefPath() string { return k.def }
func (k standaloneKind) DecodeNode(gn *genericNode, uf *UnifiedFile) error {
	// EDGE-INHERIT cutover B: a substrate kind is BOTH the template entity AND the
	// deploy. A node is a DEPLOY when it carries a cross-ref (`from:`/`image:`, or a
	// scalar disc value like `vm: pg-vm`) or resource members; otherwise it is a
	// standalone TEMPLATE (e.g. `vm: {source: …}`). The per-substrate from⊻image /
	// source⊻from validity is Go-enforced downstream.
	if isDeployShape(gn) || len(resourceChildren(gn)) > 0 {
		return buildBundleNodeInto(gn, uf)
	}
	return buildStandaloneResource(gn, uf)
}

// isDeployShape reports whether a substrate node is a DEPLOY (vs a standalone
// template): a scalar discriminator value (`vm: pg-vm` / `pod: img`) is a cross-ref
// deploy, and a mapping value carrying `from:` or `image:` is a deploy.
func isDeployShape(gn *genericNode) bool {
	dv := gn.discValue
	if dv == nil {
		return false
	}
	if dv.Kind == yaml.ScalarNode {
		return dv.Value != ""
	}
	if dv.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(dv.Content); i += 2 {
			if k := dv.Content[i].Value; k == "from" || k == "image" {
				return true
			}
		}
	}
	return false
}
