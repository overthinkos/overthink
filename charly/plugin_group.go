package main

// groupKind is the `group` deploy-shape KIND — a TARGETLESS deploy group (resource
// members, no own workload; the former targetless `bundle:`) — extracted into its OWN
// file as a dedicated-builtin KindProvider (Phase 2 deploy-shape batch), mirroring the
// step dedicated-provider pattern (plugin_step_reboot.go etc.).
//
// Unlike the tier-1 kinds (distro/builder/init/resource/target/agent/module/sidecar/
// package-group), which became schema-carrying RegisterBuiltinPluginUnit plugins routed
// out-of-process through runPluginKind, a deploy-shape kind RECURSES over the genericNode
// tree (member nesting) and lands its result in the TYPED core deploy maps (uf.Bundle)
// that every deploy/check consumer reads — so it stays an in-proc KindProvider with a
// typed DecodeNode that calls the core recursion helper (buildBundleNodeInto, node_bundle.go).
// The helper stays in CORE; this provider calls it in-proc. An out-of-tree group plugin is
// deferred to the ExecutorService enabler (the JSON Invoke envelope cannot thread the
// genericNode member tree or reach the typed maps). It is therefore INTENTIONALLY absent
// from both builtinProviderInstances and the `providers:` manifest, yet dispatches
// identically through providerRegistry.ResolveKind; checkKindProviderBijection still proves
// it is registered. The authored body is validated by the closed core #Deploy/#NodeDoc gate
// (registerCueKind("group", "#Deploy"), cue_kind_group.go), not a served plugin schema.
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

// Self-register at package-var init (runs before any init(), so the kind-provider
// bijection gate in registry_bootstrap.go observes it without a cross-init race).
var _ = registerDedicatedBuiltin(groupKind{})
