package main

import (
	"context"
	"encoding/json"
	"fmt"

	builderbuiltin "github.com/overthinkos/overthink/charly/plugin/builtins/builder"
	"github.com/overthinkos/overthink/charly/spec"
)

// builderKindPlugin is the BUILT-IN `builder` plugin KIND: the multi-stage builder
// vocabulary (pixi/npm/cargo/aur/bootstrap stage templates + cache mounts), formerly a
// core builtin kind that decoded into the typed core map uf.Builder. It is extracted
// into a dedicated plugin unit, mirroring the sidecar/agent/module/distro kind→plugin
// extractions.
//
// FILE NAME: plugin_builder_KIND.go — distinct from the plugin_builder_<name>.go files
// (pixi/npm/cargo/aur), which register ClassBuilder build-STRATEGY providers. This is
// the build-VOCABULARY KIND (`builder:` map entries), a ClassKind provider.
//
// It is a Provider but NOT a KindProvider — it has no typed DecodeNode — so
// normalizeNodeInto routes a `builder:` node to runPluginKind, which validates the
// authored entity body against this unit's served #BuilderInput schema
// (validateAuthoredPluginInput(ClassKind, "builder", …)) and then calls Invoke with
// OpLoad. The decoded entity lands in uf.PluginKinds["builder"][<name>] as canonical
// JSON; UnifiedFile.Builders() reads it back into the name-keyed map[string]*BuilderDef
// the generator consumes (via ProjectBuilderConfig). The binary-embedded build
// vocabulary flows through the SAME path, merged root-wins via mergePluginKindsMap.
// Transport-invisible above the registry.
type builderKindPlugin struct{}

func (builderKindPlugin) Reserved() string     { return "builder" }
func (builderKindPlugin) Class() ProviderClass { return ClassKind }

// Invoke handles OpLoad: it decodes the authored (NAMELESS) entity body into the core
// spec.Builder type and returns it re-marshalled as canonical entity JSON. The host has
// already validated op.Params against #BuilderInput; re-marshalling through spec.Builder
// canonicalises it so UnifiedFile.Builders() reads uf.PluginKinds["builder"] back into
// BuilderDef (= spec.Builder).
func (builderKindPlugin) Invoke(_ context.Context, op *Operation) (*Result, error) {
	if op.Op != OpLoad {
		return nil, fmt.Errorf("builder kind: unsupported op %q (only %q)", op.Op, OpLoad)
	}
	var b spec.Builder
	if len(op.Params) > 0 {
		if err := json.Unmarshal(op.Params, &b); err != nil {
			return nil, fmt.Errorf("builder kind: decode entity: %w", err)
		}
	}
	out, err := json.Marshal(b)
	if err != nil {
		return nil, fmt.Errorf("builder kind: marshal entity: %w", err)
	}
	return &Result{JSON: out}, nil
}

func init() {
	RegisterBuiltinPluginUnit(PluginUnit{
		Providers: []Provider{builderKindPlugin{}},
		Schema:    PluginSchema{CueSource: builderbuiltin.Schema(), InputDefs: builderbuiltin.InputDefs},
	})
}
