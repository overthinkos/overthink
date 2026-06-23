package main

import (
	"context"
	"encoding/json"
	"fmt"

	distrobuiltin "github.com/overthinkos/overthink/charly/plugin/builtins/distro"
	"github.com/overthinkos/overthink/charly/spec"
)

// distroKindPlugin is the BUILT-IN `distro` plugin KIND: the per-distro build
// vocabulary (bootstrap commands, package-format templates, pacstrap/debootstrap),
// formerly a core builtin kind that decoded into the typed core map uf.Distro. It is
// extracted into a dedicated plugin unit, mirroring the sidecar/agent/module kind→plugin
// extractions (plugin_sidecar.go / plugin_agent.go / plugin_module.go).
//
// It is a Provider but NOT a KindProvider — it has no typed DecodeNode — so
// normalizeNodeInto routes a `distro:` node to runPluginKind, which validates the
// authored entity body against this unit's served #DistroInput schema
// (validateAuthoredPluginInput(ClassKind, "distro", …)) and then calls Invoke with
// OpLoad. The decoded entity lands in uf.PluginKinds["distro"][<name>] as canonical
// JSON; UnifiedFile.Distros() reads it back into the name-keyed map[string]*DistroDef
// the generator/format code consumes (via ProjectDistroConfig). The binary-embedded
// build vocabulary (authored `distro:` nodes in charly/charly.yml) flows through the
// SAME path and is merged root-wins via the generic mergePluginKindsMap, so a project's
// own `distro: <name>` overrides it. Transport-invisible above the registry: an
// out-of-tree distro plugin would implement the SAME Provider and serve the SAME schema
// over gRPC instead.
type distroKindPlugin struct{}

func (distroKindPlugin) Reserved() string     { return "distro" }
func (distroKindPlugin) Class() ProviderClass { return ClassKind }

// Invoke handles OpLoad: it decodes the authored (NAMELESS) entity body (op.Params —
// the node-body JSON assembled by runPluginKind) into the core spec.Distro type, then
// returns it re-marshalled as canonical entity JSON in Result.JSON. The host has
// already validated op.Params against #DistroInput, so the decode is of a well-formed
// body; re-marshalling through spec.Distro canonicalises it (the same shape #Distro
// generates) so UnifiedFile.Distros() reads uf.PluginKinds["distro"] back into
// DistroDef (= spec.Distro). Mirrors the sidecar plugin's spec.Sidecar round-trip.
func (distroKindPlugin) Invoke(_ context.Context, op *Operation) (*Result, error) {
	if op.Op != OpLoad {
		return nil, fmt.Errorf("distro kind: unsupported op %q (only %q)", op.Op, OpLoad)
	}
	var d spec.Distro
	if len(op.Params) > 0 {
		if err := json.Unmarshal(op.Params, &d); err != nil {
			return nil, fmt.Errorf("distro kind: decode entity: %w", err)
		}
	}
	out, err := json.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("distro kind: marshal entity: %w", err)
	}
	return &Result{JSON: out}, nil
}

func init() {
	RegisterBuiltinPluginUnit(PluginUnit{
		Providers: []Provider{distroKindPlugin{}},
		Schema:    PluginSchema{CueSource: distrobuiltin.Schema(), InputDefs: distrobuiltin.InputDefs},
	})
}
