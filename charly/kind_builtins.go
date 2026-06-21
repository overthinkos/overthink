package main

import (
	"github.com/overthinkos/overthink/charly/spec"

	"gopkg.in/yaml.v3"
)

// The built-in kinds as KindProviders. Each wraps its existing decode logic
// unchanged — the migration is behavior-preserving; only the normalizeNodeInto
// dispatch switch is replaced by providerRegistry.ResolveKind. CueDefPath carries
// the former reservedKindHandlers value (the CUE def the node value validates
// against; the documented alias host→#HostValue is kept).

// candy — the special factory arm (buildCandy returns name + InlineCandy).
type candyKind struct{ builtinKindBase }

func (candyKind) Reserved() string   { return "candy" }
func (candyKind) CueDefPath() string { return "#Candy" }
func (candyKind) DecodeNode(gn *genericNode, uf *UnifiedFile) error {
	name, ic, err := buildCandy(gn)
	if err != nil {
		return err
	}
	ensureMap(&uf.Candy)
	uf.Candy[name] = ic
	return nil
}

// box / sidecar — decode a struct value into a name-keyed map.
type boxKind struct{ builtinKindBase }

func (boxKind) Reserved() string   { return "box" }
func (boxKind) CueDefPath() string { return "#Box" }
func (boxKind) DecodeNode(gn *genericNode, uf *UnifiedFile) error {
	var b BoxConfig
	if err := decodeNodeValue(gn, &b); err != nil {
		return err
	}
	ensureMap(&uf.Box)
	uf.Box[gn.name] = b
	return nil
}

type sidecarKind struct{ builtinKindBase }

func (sidecarKind) Reserved() string   { return "sidecar" }
func (sidecarKind) CueDefPath() string { return "#Sidecar" }
func (sidecarKind) DecodeNode(gn *genericNode, uf *UnifiedFile) error {
	var s SidecarDef
	if err := decodeNodeValue(gn, &s); err != nil {
		return err
	}
	ensureMap(&uf.Sidecar)
	uf.Sidecar[gn.name] = s
	return nil
}

// The pointer-into-map kinds (decodePtrInto into a typed uf.<X> map).
type distroKind struct{ builtinKindBase }

func (distroKind) Reserved() string   { return "distro" }
func (distroKind) CueDefPath() string { return "#Distro" }
func (distroKind) DecodeNode(gn *genericNode, uf *UnifiedFile) error {
	return decodePtrInto(gn, &uf.Distro)
}

type builderKind struct{ builtinKindBase }

func (builderKind) Reserved() string   { return "builder" }
func (builderKind) CueDefPath() string { return "#Builder" }
func (builderKind) DecodeNode(gn *genericNode, uf *UnifiedFile) error {
	return decodePtrInto(gn, &uf.Builder)
}

type initKind struct{ builtinKindBase }

func (initKind) Reserved() string   { return "init" }
func (initKind) CueDefPath() string { return "#Init" }
func (initKind) DecodeNode(gn *genericNode, uf *UnifiedFile) error {
	return decodePtrInto(gn, &uf.Init)
}

type resourceKind struct{ builtinKindBase }

func (resourceKind) Reserved() string   { return "resource" }
func (resourceKind) CueDefPath() string { return "#Resource" }
func (resourceKind) DecodeNode(gn *genericNode, uf *UnifiedFile) error {
	return decodePtrInto(gn, &uf.Resource)
}

type agentKind struct{ builtinKindBase }

func (agentKind) Reserved() string   { return "agent" }
func (agentKind) CueDefPath() string { return "#Agent" }
func (agentKind) DecodeNode(gn *genericNode, uf *UnifiedFile) error {
	return decodePtrInto(gn, &uf.Agent)
}

type groupKind struct{ builtinKindBase }

func (groupKind) Reserved() string   { return "group" }
func (groupKind) CueDefPath() string { return "#Group" }

// DecodeNode — EDGE-INHERIT cutover B: group: is BOTH the Calamares package-group
// (#Group, package/subgroup children) AND a TARGETLESS deploy group (resource
// members, no own workload — the former targetless `bundle:`). Routed by shape: a
// node carrying pod/vm/k8s/local/android resource MEMBERS is a deploy group; otherwise
// it is the Calamares package group. (The Calamares group has zero on-disk corpus.)
func (groupKind) DecodeNode(gn *genericNode, uf *UnifiedFile) error {
	if len(resourceChildren(gn)) > 0 {
		return buildBundleNodeInto(gn, uf)
	}
	return decodePtrInto(gn, &uf.Group)
}

type targetKind struct{ builtinKindBase }

func (targetKind) Reserved() string   { return "target" }
func (targetKind) CueDefPath() string { return "#Target" }
func (targetKind) DecodeNode(gn *genericNode, uf *UnifiedFile) error {
	return decodePtrInto(gn, &uf.Target)
}

type moduleKind struct{ builtinKindBase }

func (moduleKind) Reserved() string   { return "module" }
func (moduleKind) CueDefPath() string { return "#Module" }
func (moduleKind) DecodeNode(gn *genericNode, uf *UnifiedFile) error {
	return decodePtrInto(gn, &uf.Module)
}

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

// bundleKind — bundle / host, the explicit bundle-shaped nodes.
type bundleKind struct {
	builtinKindBase
	word string
	def  string
}

func (k bundleKind) Reserved() string   { return k.word }
func (k bundleKind) CueDefPath() string { return k.def }
func (k bundleKind) DecodeNode(gn *genericNode, uf *UnifiedFile) error {
	return buildBundleNodeInto(gn, uf)
}

func init() {
	for _, p := range []KindProvider{
		candyKind{}, boxKind{}, sidecarKind{},
		distroKind{}, builderKind{}, initKind{}, resourceKind{}, agentKind{}, groupKind{}, targetKind{}, moduleKind{},
		standaloneKind{word: "pod", def: "#Pod"},
		standaloneKind{word: "vm", def: "#Vm"},
		standaloneKind{word: "k8s", def: "#K8s"},
		standaloneKind{word: "local", def: "#Local"},
		standaloneKind{word: "android", def: "#Android"},
		bundleKind{word: "host", def: "#HostValue"},
	} {
		RegisterBuiltinProvider(p)
	}
	// Same-init() gate (after registration) so it can't race the alphabetical
	// init order: every CUE-declared kind has an in-proc KindProvider.
	if err := checkKindProviderBijection(spec.KindWords); err != nil {
		panic(err)
	}
}
