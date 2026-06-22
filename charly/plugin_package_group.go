package main

import (
	"context"
	"encoding/json"
	"fmt"

	packagegroup "github.com/overthinkos/overthink/charly/plugin/builtins/package-group"
	"github.com/overthinkos/overthink/charly/spec"
)

// packageGroupKindPlugin is the BUILT-IN `package-group` plugin KIND: the Calamares
// netinstall package group, formerly a core builtin kind that decoded into a typed
// core map. It is the FIRST kind extracted into a dedicated plugin unit, proving the
// kind→plugin pattern (the kind-class analogue of the verb extractions
// exampleprobe/process proved).
//
// It is a Provider but NOT a KindProvider — it has no typed DecodeNode — so
// normalizeNodeInto routes a `package-group:` node to runPluginKind, which validates
// the authored entity body against this unit's served #PackageGroupInput schema
// (validateAuthoredPluginInput(ClassKind, "package-group", …)) and then calls Invoke
// with OpLoad. The decoded entity lands in uf.PluginKinds["package-group"] as
// canonical JSON, NOT in a typed core map — the core no longer owns the kind.
// Transport-invisible above the registry: an out-of-tree package-group plugin would
// implement the SAME Provider and serve the SAME schema over gRPC instead.
type packageGroupKindPlugin struct{}

func (packageGroupKindPlugin) Reserved() string     { return "package-group" }
func (packageGroupKindPlugin) Class() ProviderClass { return ClassKind }

// Invoke handles OpLoad: it decodes the authored entity body (op.Params — the
// node-body JSON assembled by runPluginKind) into the spec.Group entity, then returns
// it re-marshalled as canonical entity JSON in Result.JSON. The host has already
// validated op.Params against #PackageGroupInput, so the decode is of a well-formed
// body; re-marshalling through spec.Group canonicalises it (the same shape #Group
// generates) so a consumer reads uf.PluginKinds["package-group"] back into spec.Group.
func (packageGroupKindPlugin) Invoke(_ context.Context, op *Operation) (*Result, error) {
	if op.Op != OpLoad {
		return nil, fmt.Errorf("package-group kind: unsupported op %q (only %q)", op.Op, OpLoad)
	}
	var g spec.Group
	if len(op.Params) > 0 {
		if err := json.Unmarshal(op.Params, &g); err != nil {
			return nil, fmt.Errorf("package-group kind: decode entity: %w", err)
		}
	}
	out, err := json.Marshal(g)
	if err != nil {
		return nil, fmt.Errorf("package-group kind: marshal entity: %w", err)
	}
	return &Result{JSON: out}, nil
}

func init() {
	RegisterBuiltinPluginUnit(PluginUnit{
		Providers: []Provider{packageGroupKindPlugin{}},
		Schema:    PluginSchema{CueSource: packagegroup.Schema(), InputDefs: packagegroup.InputDefs},
	})
}
