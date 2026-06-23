package main

import (
	"context"
	"encoding/json"
	"fmt"

	resourcebuiltin "github.com/overthinkos/overthink/charly/plugin/builtins/resource"
	"github.com/overthinkos/overthink/charly/spec"
)

// resourceKindPlugin is the BUILT-IN `resource` plugin KIND: exclusive host-resource
// tokens (the GPU selector that drives the vfio<->nvidia mode flip + auto-allocation),
// formerly a core builtin kind that decoded into the typed core map uf.Resource. It is
// extracted into a dedicated plugin unit, mirroring the sidecar/agent/module/distro/
// builder/init kind→plugin extractions.
//
// It is a Provider but NOT a KindProvider — it has no typed DecodeNode — so
// normalizeNodeInto routes a `resource:` node to runPluginKind, which validates the
// authored entity body against this unit's served #ResourceInput schema
// (validateAuthoredPluginInput(ClassKind, "resource", …)) and then calls Invoke with
// OpLoad. The decoded entity lands in uf.PluginKinds["resource"][<name>] as canonical
// JSON; UnifiedFile.Resources() reads it back into the name-keyed map[string]*ResourceDef
// the GPU-arbitration code consumes (gatherResources / validateResourceDefs / vm create).
// The binary-embedded resource vocabulary flows through the SAME path, merged root-wins
// via mergePluginKindsMap. Transport-invisible above the registry.
type resourceKindPlugin struct{}

func (resourceKindPlugin) Reserved() string     { return "resource" }
func (resourceKindPlugin) Class() ProviderClass { return ClassKind }

// Invoke handles OpLoad: it decodes the authored (NAMELESS) entity body into the core
// spec.Resource type and returns it re-marshalled as canonical entity JSON. The host has
// already validated op.Params against #ResourceInput; re-marshalling through spec.Resource
// canonicalises it so UnifiedFile.Resources() reads uf.PluginKinds["resource"] back into
// ResourceDef (= spec.Resource).
func (resourceKindPlugin) Invoke(_ context.Context, op *Operation) (*Result, error) {
	if op.Op != OpLoad {
		return nil, fmt.Errorf("resource kind: unsupported op %q (only %q)", op.Op, OpLoad)
	}
	var r spec.Resource
	if len(op.Params) > 0 {
		if err := json.Unmarshal(op.Params, &r); err != nil {
			return nil, fmt.Errorf("resource kind: decode entity: %w", err)
		}
	}
	out, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("resource kind: marshal entity: %w", err)
	}
	return &Result{JSON: out}, nil
}

func init() {
	RegisterBuiltinPluginUnit(PluginUnit{
		Providers: []Provider{resourceKindPlugin{}},
		Schema:    PluginSchema{CueSource: resourcebuiltin.Schema(), InputDefs: resourcebuiltin.InputDefs},
	})
}
