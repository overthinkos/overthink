package main

import "gopkg.in/yaml.v3"

// standaloneKind is the parameterized deploy-shape KIND for the 5 resource substrates
// (pod/vm/k8s/local/android), a dedicated-builtin KindProvider mirroring the step/builder
// dedicated-provider pattern (plugin_step_reboot.go etc.).
//
// Each substrate kind is BOTH a standalone TEMPLATE entity AND a deploy. A deploy-shape
// kind RECURSES over the genericNode tree (member nesting, vm→k8s, resource siblings) and
// lands its result in the TYPED core maps every deploy/check consumer reads — the bundle
// map (uf.Bundle) for a deploy-shaped node, or the per-substrate template map
// (uf.Pod/uf.VM/uf.K8s/uf.Local/uf.Android) for a bare template. For now it stays an in-proc
// KindProvider with a typed DecodeNode calling the core helpers (buildBundleNodeInto /
// buildStandaloneResource); the helpers stay in CORE and this provider calls them in-proc.
//
// EXTERNALIZATION STATUS (post-F5): the F5 authored-member INPUT-threading foundation LANDED
// (super fe52b96c) and `group` is now EXTERNALIZED through it (candy/plugin-group, C2-group) —
// so the "the JSON Invoke envelope cannot thread the genericNode member tree" claim is FALSE
// for the DEPLOY shape: the host pre-decodes the authored members (buildResourceMemberChildren)
// and threads them to the plugin via op.Env, and the plugin folds a spec.Deploy into uf.Bundle.
// The substrate substrates are the NEXT consumers, but they are NOT yet externalizable through
// the SAME channel: unlike group (uf.Bundle ONLY), a BARE substrate template lands in the
// per-substrate TEMPLATE map (uf.Pod/uf.VM/uf.K8s/uf.Local/uf.Android), for which the structural
// channel has no fold arm yet — that per-substrate template-fold is the F5 EXTENSION a future
// cutover adds (the deploy-shape half is already F5-ready). Until then these 5 stay in-proc.
//
// The 5 instances are absent from both builtinProviderInstances and the `providers:` manifest
// and self-register via registerDedicatedBuiltin; checkKindProviderBijection still proves each
// is registered. The authored body is validated by the closed core #Pod/#Vm/#K8s/#Local/#Android
// (#NodeDoc) gate (registerCueKind, cue_kind_*.go), not a served plugin schema. Parameterized by
// word + def.
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

// Self-register each substrate at package-var init (runs before any init(), so the
// kind-provider bijection gate in registry_bootstrap.go observes them without a
// cross-init race). The 5 substrates share this ONE parameterized impl.
var (
	_ = registerDedicatedBuiltin(standaloneKind{word: "pod", def: "#Pod"})
	_ = registerDedicatedBuiltin(standaloneKind{word: "vm", def: "#Vm"})
	_ = registerDedicatedBuiltin(standaloneKind{word: "k8s", def: "#K8s"})
	_ = registerDedicatedBuiltin(standaloneKind{word: "local", def: "#Local"})
	_ = registerDedicatedBuiltin(standaloneKind{word: "android", def: "#Android"})
)
