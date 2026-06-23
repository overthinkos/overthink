package main

import (
	"context"
	"encoding/json"
	"fmt"

	initbuiltin "github.com/overthinkos/overthink/charly/plugin/builtins/init"
	"github.com/overthinkos/overthink/charly/spec"
)

// initKindPlugin is the BUILT-IN `init` plugin KIND: the init-system vocabulary
// (supervisord/systemd fragment-assembly + entrypoint + service-management templates),
// formerly a core builtin kind that decoded into the typed core map uf.Init. It is
// extracted into a dedicated plugin unit, mirroring the sidecar/agent/module/distro/
// builder kind→plugin extractions.
//
// It is a Provider but NOT a KindProvider — it has no typed DecodeNode — so
// normalizeNodeInto routes an `init:` node to runPluginKind, which validates the
// authored entity body against this unit's served #InitInput schema
// (validateAuthoredPluginInput(ClassKind, "init", …)) and then calls Invoke with
// OpLoad. The decoded entity lands in uf.PluginKinds["init"][<name>] as canonical JSON;
// UnifiedFile.Inits() reads it back into the name-keyed map[string]*InitDef the
// generator consumes (via ProjectInitConfig). The binary-embedded build vocabulary flows
// through the SAME path, merged root-wins via mergePluginKindsMap. Transport-invisible
// above the registry.
type initKindPlugin struct{}

func (initKindPlugin) Reserved() string     { return "init" }
func (initKindPlugin) Class() ProviderClass { return ClassKind }

// Invoke handles OpLoad: it decodes the authored (NAMELESS) entity body into the core
// spec.Init type and returns it re-marshalled as canonical entity JSON. The host has
// already validated op.Params against #InitInput; re-marshalling through spec.Init
// canonicalises it so UnifiedFile.Inits() reads uf.PluginKinds["init"] back into
// InitDef (= spec.Init).
func (initKindPlugin) Invoke(_ context.Context, op *Operation) (*Result, error) {
	if op.Op != OpLoad {
		return nil, fmt.Errorf("init kind: unsupported op %q (only %q)", op.Op, OpLoad)
	}
	var in spec.Init
	if len(op.Params) > 0 {
		if err := json.Unmarshal(op.Params, &in); err != nil {
			return nil, fmt.Errorf("init kind: decode entity: %w", err)
		}
	}
	out, err := json.Marshal(in)
	if err != nil {
		return nil, fmt.Errorf("init kind: marshal entity: %w", err)
	}
	return &Result{JSON: out}, nil
}

func init() {
	RegisterBuiltinPluginUnit(PluginUnit{
		Providers: []Provider{initKindPlugin{}},
		Schema:    PluginSchema{CueSource: initbuiltin.Schema(), InputDefs: initbuiltin.InputDefs},
	})
}
