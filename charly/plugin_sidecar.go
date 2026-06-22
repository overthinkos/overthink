package main

import (
	"context"
	"encoding/json"
	"fmt"

	sidecarbuiltin "github.com/overthinkos/overthink/charly/plugin/builtins/sidecar"
	"github.com/overthinkos/overthink/charly/spec"
)

// sidecarKindPlugin is the BUILT-IN `sidecar` plugin KIND: the reusable
// sidecar-container template library (incl. the binary-embedded `tailscale` template),
// formerly a core builtin kind that decoded into the typed core map uf.Sidecar. It is
// extracted into a dedicated plugin unit, mirroring the agent/module kind→plugin
// extractions (plugin_agent.go / plugin_module.go).
//
// It is a Provider but NOT a KindProvider — it has no typed DecodeNode — so
// normalizeNodeInto routes a `sidecar:` node to runPluginKind, which validates the
// authored entity body against this unit's served #SidecarInput schema
// (validateAuthoredPluginInput(ClassKind, "sidecar", …)) and then calls Invoke with
// OpLoad. The decoded entity lands in uf.PluginKinds["sidecar"][<name>] as canonical
// JSON; UnifiedFile.Sidecars() decodes it back into the name-keyed
// map[string]SidecarDef the deploy/quadlet code consumes (via Config.Sidecar /
// BundleConfig.Sidecar). The binary-embedded `tailscale` template (an authored
// `sidecar:` node in charly/charly.yml) flows through the SAME path and is merged
// root-wins via the generic mergePluginKindsMap, so a project's own `sidecar: tailscale`
// overrides it. Transport-invisible above the registry: an out-of-tree sidecar plugin
// would implement the SAME Provider and serve the SAME schema over gRPC instead.
type sidecarKindPlugin struct{}

func (sidecarKindPlugin) Reserved() string     { return "sidecar" }
func (sidecarKindPlugin) Class() ProviderClass { return ClassKind }

// Invoke handles OpLoad: it decodes the authored (NAMELESS) entity body (op.Params —
// the node-body JSON assembled by runPluginKind) into the core spec.Sidecar type, then
// returns it re-marshalled as canonical entity JSON in Result.JSON. The host has
// already validated op.Params against #SidecarInput, so the decode is of a well-formed
// body; re-marshalling through spec.Sidecar canonicalises it (the same shape #Sidecar
// generates) so UnifiedFile.Sidecars() reads uf.PluginKinds["sidecar"] back into
// SidecarDef (= spec.Sidecar). Mirrors the agent plugin's spec.Agent round-trip.
func (sidecarKindPlugin) Invoke(_ context.Context, op *Operation) (*Result, error) {
	if op.Op != OpLoad {
		return nil, fmt.Errorf("sidecar kind: unsupported op %q (only %q)", op.Op, OpLoad)
	}
	var s spec.Sidecar
	if len(op.Params) > 0 {
		if err := json.Unmarshal(op.Params, &s); err != nil {
			return nil, fmt.Errorf("sidecar kind: decode entity: %w", err)
		}
	}
	out, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("sidecar kind: marshal entity: %w", err)
	}
	return &Result{JSON: out}, nil
}

func init() {
	RegisterBuiltinPluginUnit(PluginUnit{
		Providers: []Provider{sidecarKindPlugin{}},
		Schema:    PluginSchema{CueSource: sidecarbuiltin.Schema(), InputDefs: sidecarbuiltin.InputDefs},
	})
}
