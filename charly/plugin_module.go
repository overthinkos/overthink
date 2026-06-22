package main

import (
	"context"
	"encoding/json"
	"fmt"

	modulebuiltin "github.com/overthinkos/overthink/charly/plugin/builtins/module"
	"github.com/overthinkos/overthink/charly/spec"
)

// moduleKindPlugin is the BUILT-IN `module` plugin KIND: the Calamares installer
// module (module.desc), formerly a core builtin kind that decoded into a typed core
// map (uf.Module). It is extracted into a dedicated plugin unit, mirroring the
// package-group kind→plugin extraction (plugin_package_group.go).
//
// It is a Provider but NOT a KindProvider — it has no typed DecodeNode — so
// normalizeNodeInto routes a `module:` node to runPluginKind, which validates the
// authored entity body against this unit's served #ModuleInput schema
// (validateAuthoredPluginInput(ClassKind, "module", …)) and then calls Invoke with
// OpLoad. The decoded entity lands in uf.PluginKinds["module"][<name>] as canonical
// JSON — the core no longer owns the kind. Transport-invisible above the registry: an
// out-of-tree module plugin would implement the SAME Provider and serve the SAME
// schema over gRPC instead.
type moduleKindPlugin struct{}

func (moduleKindPlugin) Reserved() string     { return "module" }
func (moduleKindPlugin) Class() ProviderClass { return ClassKind }

// Invoke handles OpLoad: it decodes the authored (NAMELESS) entity body (op.Params —
// the node-body JSON assembled by runPluginKind) into the core spec.ModuleSpec type,
// then returns it re-marshalled as canonical entity JSON in Result.JSON. The host has
// already validated op.Params against #ModuleInput, so the decode is of a well-formed
// body; re-marshalling through spec.ModuleSpec canonicalises it (the same shape
// #Module generates), mirroring the package-group plugin's spec.Group round-trip.
func (moduleKindPlugin) Invoke(_ context.Context, op *Operation) (*Result, error) {
	if op.Op != OpLoad {
		return nil, fmt.Errorf("module kind: unsupported op %q (only %q)", op.Op, OpLoad)
	}
	var m spec.ModuleSpec
	if len(op.Params) > 0 {
		if err := json.Unmarshal(op.Params, &m); err != nil {
			return nil, fmt.Errorf("module kind: decode entity: %w", err)
		}
	}
	out, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("module kind: marshal entity: %w", err)
	}
	return &Result{JSON: out}, nil
}

func init() {
	RegisterBuiltinPluginUnit(PluginUnit{
		Providers: []Provider{moduleKindPlugin{}},
		Schema:    PluginSchema{CueSource: modulebuiltin.Schema(), InputDefs: modulebuiltin.InputDefs},
	})
}
