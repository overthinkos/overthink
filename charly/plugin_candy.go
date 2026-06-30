package main

// candyKind is the `candy` KIND — the box⊻layer factory arm — extracted into its OWN
// file as a dedicated-builtin KindProvider, COMPLETING Phase 2 (every kind is now a
// dedicated provider). It mirrors the deploy-shape extraction (plugin_group.go /
// plugin_substrate.go).
//
// Unlike the tier-1 kinds (distro/builder/init/resource/target/agent/module/sidecar/
// package-group), which became schema-carrying RegisterBuiltinPluginUnit plugins routed
// out-of-process through runPluginKind, candy DECODES the authored node body into TWO
// different TYPED core maps — uf.Box for a full IMAGE (the former box:, marked by base:/
// from:), uf.Candy for a LAYER fragment — via the core box⊻layer routing (candyIsImage +
// buildCandy, node_candy.go). Those core helpers STAY in CORE and this provider calls them
// in-proc (the same rule that kept buildBundleNodeInto core for the deploy-shape kinds). An
// out-of-tree candy plugin is deferred to the ExecutorService enabler (the JSON Invoke
// envelope cannot reach the typed maps). candy is therefore INTENTIONALLY absent from both
// builtinProviderInstances and the `providers:` manifest, yet dispatches identically through
// providerRegistry.ResolveKind; checkKindProviderBijection still proves it is registered. The
// authored body is validated by the closed core #Candy/#Box (#NodeDoc) gate
// (registerCueKind("candy", "#Candy"), cue_kind_candy.go), not a served plugin schema.
// candy is BOOTSTRAP-CRITICAL — decoded on every load, including the discovered-candy
// pre-check in unified.go (candyIsImage distinguishes a lazy LAYER ref from an eager IMAGE
// decode); the corpus / node-loader tests (uf.Candy["redis"] / uf.Box["coder"]) gate it.
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

// Self-register at package-var init (runs before any init(), so the kind-provider
// bijection gate in registry_bootstrap.go observes it without a cross-init race).
var _ = registerDedicatedBuiltin(candyKind{})
