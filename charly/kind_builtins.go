package main

// kind_builtins.go — Phase 2 COMPLETION MARKER (every KIND is now a dedicated
// provider; this file holds NO code, only the navigation tombstone map below: which
// kind went where and how). The former per-kind decode switch and the last typed
// builtin (`candy`) have all been extracted into their own dedicated files. A built-in
// KindProvider decodes via DecodeNode (no JSON) — normalizeNodeInto resolves the node's
// discriminator through providerRegistry.ResolveKind and calls it; CueDefPath carries the
// former reservedKindHandlers value (the CUE def the node value validates against).

// candy — the box⊻layer factory arm (the LAST kind extracted, completing Phase 2): a
// `candy:` node carrying the box base⊻from marker is a full IMAGE → uf.Box, otherwise a
// LAYER fragment → uf.Candy. It is now a dedicated-builtin KindProvider in plugin_candy.go
// (self-registering via registerDedicatedBuiltin, absent from builtinProviderInstances +
// the `providers:` manifest like the deploy-shape kinds), calling the CORE box⊻layer
// routing helpers (candyIsImage + buildCandy, node_candy.go) in-proc; checkKindProviderBijection
// still proves it is registered. The authored body is validated by the closed core
// #Candy/#Box (#NodeDoc) gate (registerCueKind("candy", "#Candy"), cue_kind_candy.go).

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
