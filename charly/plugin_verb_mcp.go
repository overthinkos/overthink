package main

import "context"

// mcpVerb is the BUILT-IN `mcp` LIVE-CONTAINER verb, extracted into its OWN dedicated
// file (Phase 1, the live-container-verb relocation). Like cdp/vnc, mcp stays a
// FIRST-CLASS #Op verb: it keeps its dedicated `mcp:` discriminator and its
// method-specific modifiers (Tool/URI/Input/McpName) on the closed base #Op — there is
// NO plugin_input and therefore NO served plugin schema. So it self-registers via
// registerDedicatedBuiltin (the schema-less dedicated-provider path), INTENTIONALLY
// absent from BOTH builtinProviderInstances and the `providers:` manifest, yet resolving
// + dispatching through the SAME providerRegistry (the verb + method-allowlist bijection
// gates still see it). It embeds builtinVerbBase for Class()=ClassVerb + the in-proc-only
// Invoke stub (a live verb carries the *Runner and never serves itself over the wire).
//
// The mcp verb dispatches to `charly check mcp <method> <image> …`, which uses
// github.com/modelcontextprotocol/go-sdk to connect to the declared MCP server. Methods
// mirror the SDK's ClientSession surface. See mcp.go / mcp_client.go for the host-side
// implementation.
//
// This file owns the verb's complete contract: the provider (Reserved/RunVerb), the
// LiveVerbProvider method contract (Methods/MethodField), the mcpMethods method
// allowlist, and the runMcp dispatcher. The shared posArgs builder library
// (posMcpCommon/posMcpCall/posMcpRead), the methodSpec type, and
// artifactValidatableMethods stay in checkrun_charly_verbs.go.
type mcpVerb struct{ builtinVerbBase }

func (mcpVerb) Reserved() string { return "mcp" }

func (mcpVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	return r.runMcp(ctx, op)
}

func (mcpVerb) Methods() map[string]methodSpec { return mcpMethods }
func (mcpVerb) MethodField(c *Op) string       { return c.Mcp }

// mcpMethods is the mcp verb's method allowlist (the dispatch data runCharlyVerb reads).
var mcpMethods = map[string]methodSpec{
	"ping":           {path: []string{"mcp", "ping"}, posArgs: posMcpCommon},
	"servers":        {path: []string{"mcp", "servers"}, posArgs: posMcpCommon},
	"list-tools":     {path: []string{"mcp", "list-tools"}, posArgs: posMcpCommon},
	"list-resources": {path: []string{"mcp", "list-resources"}, posArgs: posMcpCommon},
	"list-prompts":   {path: []string{"mcp", "list-prompts"}, posArgs: posMcpCommon},
	"call":           {path: []string{"mcp", "call"}, required: []string{"Tool"}, posArgs: posMcpCall},
	"read":           {path: []string{"mcp", "read"}, required: []string{"URI"}, posArgs: posMcpRead},
}

func (r *Runner) runMcp(ctx context.Context, c *Op) CheckResult {
	return r.runCharlyVerb(ctx, c, "mcp", c.Mcp, mcpMethods)
}

var _ = registerDedicatedBuiltin(mcpVerb{})
