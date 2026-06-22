package main

import (
	"context"
	"encoding/json"
	"fmt"

	agentbuiltin "github.com/overthinkos/overthink/charly/plugin/builtins/agent"
	"github.com/overthinkos/overthink/charly/spec"
)

// agentKindPlugin is the BUILT-IN `agent` plugin KIND: the AI-CLI grader catalog,
// formerly a core builtin kind that decoded into the typed core map uf.Agent. It is
// extracted into a dedicated plugin unit, mirroring the package-group kind→plugin
// extraction (plugin_package_group.go).
//
// It is a Provider but NOT a KindProvider — it has no typed DecodeNode — so
// normalizeNodeInto routes an `agent:` node to runPluginKind, which validates the
// authored entity body against this unit's served #AgentInput schema
// (validateAuthoredPluginInput(ClassKind, "agent", …)) and then calls Invoke with
// OpLoad. The decoded entity lands in uf.PluginKinds["agent"][<name>] as canonical
// JSON; UnifiedFile.Agents() decodes it back into the name-keyed
// map[string]*AgentConfig the iterate/check harness consumes. Transport-invisible
// above the registry: an out-of-tree agent plugin would implement the SAME Provider
// and serve the SAME schema over gRPC instead.
type agentKindPlugin struct{}

func (agentKindPlugin) Reserved() string     { return "agent" }
func (agentKindPlugin) Class() ProviderClass { return ClassKind }

// Invoke handles OpLoad: it decodes the authored (NAMELESS) entity body (op.Params —
// the node-body JSON assembled by runPluginKind) into the core spec.Agent type, then
// returns it re-marshalled as canonical entity JSON in Result.JSON. The host has
// already validated op.Params against #AgentInput, so the decode is of a well-formed
// body; re-marshalling through spec.Agent canonicalises it (the same shape #Agent
// generates) so UnifiedFile.Agents() reads uf.PluginKinds["agent"] back into
// *AgentConfig (= *spec.Agent). Mirrors the package-group plugin's spec.Group
// round-trip.
func (agentKindPlugin) Invoke(_ context.Context, op *Operation) (*Result, error) {
	if op.Op != OpLoad {
		return nil, fmt.Errorf("agent kind: unsupported op %q (only %q)", op.Op, OpLoad)
	}
	var a spec.Agent
	if len(op.Params) > 0 {
		if err := json.Unmarshal(op.Params, &a); err != nil {
			return nil, fmt.Errorf("agent kind: decode entity: %w", err)
		}
	}
	out, err := json.Marshal(a)
	if err != nil {
		return nil, fmt.Errorf("agent kind: marshal entity: %w", err)
	}
	return &Result{JSON: out}, nil
}

func init() {
	RegisterBuiltinPluginUnit(PluginUnit{
		Providers: []Provider{agentKindPlugin{}},
		Schema:    PluginSchema{CueSource: agentbuiltin.Schema(), InputDefs: agentbuiltin.InputDefs},
	})
}
