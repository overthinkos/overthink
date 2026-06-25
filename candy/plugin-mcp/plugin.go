// Package mcp is the importable, COMPILED-IN host-coupled `mcp` LIVE-CONTAINER verb:
// probe MCP servers declared via mcp_provides on a live deployment (ping, servers,
// list-tools/resources/prompts, call, read). A SCHEMA-LESS kit.LiveVerbProvider — its
// modifiers ride the closed base #Op; RunVerb delegates dispatch to the host via
// cc.RunCharlyVerb. Relocated out of charly's module (formerly charly/plugin_verb_mcp.go);
// COMPILED-IN-ONLY. The `charly check mcp` driver command stays host-side (self-invoked).
package mcp

import (
	"context"

	"github.com/overthinkos/overthink/charly/plugin/kit"
	"github.com/overthinkos/overthink/charly/spec"
)

// NewLiveVerb returns the mcp verb as a kit.LiveVerbProvider for compiled-in registration.
func NewLiveVerb() kit.LiveVerbProvider { return verb{} }

type verb struct{}

func (verb) Reserved() string { return "mcp" }

func (verb) RunVerb(ctx context.Context, cc kit.CheckContext, op *spec.Op) kit.Result {
	return cc.RunCharlyVerb(ctx, op, "mcp", op.Mcp, mcpMethods)
}

func (verb) Methods() map[string]kit.MethodSpec { return mcpMethods }
func (verb) MethodField(op *spec.Op) string     { return op.Mcp }

// mcpMethods is the mcp verb's method allowlist (the dispatch data the host's runCharlyVerb reads).
var mcpMethods = map[string]kit.MethodSpec{
	"ping":           {Path: []string{"mcp", "ping"}, PosArgs: kit.PosMcpCommon},
	"servers":        {Path: []string{"mcp", "servers"}, PosArgs: kit.PosMcpCommon},
	"list-tools":     {Path: []string{"mcp", "list-tools"}, PosArgs: kit.PosMcpCommon},
	"list-resources": {Path: []string{"mcp", "list-resources"}, PosArgs: kit.PosMcpCommon},
	"list-prompts":   {Path: []string{"mcp", "list-prompts"}, PosArgs: kit.PosMcpCommon},
	"call":           {Path: []string{"mcp", "call"}, Required: []string{"Tool"}, PosArgs: kit.PosMcpCall},
	"read":           {Path: []string{"mcp", "read"}, Required: []string{"URI"}, PosArgs: kit.PosMcpRead},
}
