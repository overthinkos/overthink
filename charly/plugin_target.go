package main

import (
	"context"
	"encoding/json"
	"fmt"

	targetbuiltin "github.com/overthinkos/overthink/charly/plugin/builtins/target"
	"github.com/overthinkos/overthink/charly/spec"
)

// targetKindPlugin is the BUILT-IN `target` plugin KIND: the Calamares install target
// (settings.conf), formerly a core builtin kind that decoded into the typed core map
// uf.Target. It is extracted into a dedicated plugin unit, mirroring the sidecar/agent/
// module/distro/builder/init/resource kind→plugin extractions.
//
// It is a Provider but NOT a KindProvider — it has no typed DecodeNode — so
// normalizeNodeInto routes a `target:` node to runPluginKind, which validates the
// authored entity body against this unit's served #TargetInput schema
// (validateAuthoredPluginInput(ClassKind, "target", …)) and then calls Invoke with
// OpLoad. The decoded entity lands in uf.PluginKinds["target"][<name>] as canonical JSON.
// Calamares has zero on-disk corpus / core readers yet (importers/emitters deferred), so
// — like the zero-reader module/package-group kinds — there is no Targets() accessor; the
// canonical body sits in PluginKinds for a future importer. Transport-invisible above the
// registry.
type targetKindPlugin struct{}

func (targetKindPlugin) Reserved() string     { return "target" }
func (targetKindPlugin) Class() ProviderClass { return ClassKind }

// Invoke handles OpLoad: it decodes the authored (NAMELESS) entity body into the core
// spec.Target type and returns it re-marshalled as canonical entity JSON. The host has
// already validated op.Params against #TargetInput; re-marshalling through spec.Target
// canonicalises it (the same shape #Target generates, which TargetSpec aliases).
func (targetKindPlugin) Invoke(_ context.Context, op *Operation) (*Result, error) {
	if op.Op != OpLoad {
		return nil, fmt.Errorf("target kind: unsupported op %q (only %q)", op.Op, OpLoad)
	}
	var t spec.Target
	if len(op.Params) > 0 {
		if err := json.Unmarshal(op.Params, &t); err != nil {
			return nil, fmt.Errorf("target kind: decode entity: %w", err)
		}
	}
	out, err := json.Marshal(t)
	if err != nil {
		return nil, fmt.Errorf("target kind: marshal entity: %w", err)
	}
	return &Result{JSON: out}, nil
}

func init() {
	RegisterBuiltinPluginUnit(PluginUnit{
		Providers: []Provider{targetKindPlugin{}},
		Schema:    PluginSchema{CueSource: targetbuiltin.Schema(), InputDefs: targetbuiltin.InputDefs},
	})
}
