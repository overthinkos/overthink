package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alecthomas/kong"

	"github.com/overthinkos/overthink/candy/plugin-mcp/params"
	pb "github.com/overthinkos/overthink/charly/plugin/proto"
)

// command.go is the command:mcp leg of this plugin — the externalized `charly mcp …`
// CLI, ported OUT of charly's core (the deleted charly/mcp_server.go +
// plugin_command_mcp.go) so the github.com/modelcontextprotocol/go-sdk SERVER no longer
// links into the core binary (the operator-approved C1 dep-shed). It is the COMMAND-class
// companion of this same module's verb:mcp check verb (provider.go / methods.go).
//
// Dispatch contract (charly/provider_command_external.go dispatchExternalCommand): on
// `charly mcp <args…>`, charly forwards the pass-through CLI tokens AFTER the `mcp` word by
// Invoking this provider with op == OpRun and params == {"args": [...]} (marshalled
// DIRECTLY — NOT wrapped in the `plugin_input` envelope a verb CHECK step uses). The args
// are decoded into the CUE-generated params.McpInput, then parsed with kong against the
// SAME command grammar the core command formerly carried, and dispatched to `serve`.

// opRun mirrors charly's command-run op selector (package main's OpRun = sdk.OpRun =
// "run"). An external plugin can't import that constant, so it is named here; the host's
// dispatchExternalCommand sends opRun on the command Invoke.
const opRun = "run"

// McpCmdGroup mirrors the original core `charly mcp` command group (charly/mcp_server.go).
// One subcommand: serve.
type McpCmdGroup struct {
	Serve McpServeCmd `cmd:"" help:"Run an MCP server exposing every charly CLI command as a tool"`
}

// McpServeCmd: `charly mcp serve` — the same flag surface the core command carried. The
// flag names derive from the recognized kong `name:` tag (the original's `long:` tag is
// NOT a kong tag — kong ignored it and kebab-cased the field name, which happens to match;
// the explicit `name:` is the correct, self-documenting form).
//
// Unlike the core original, this command does NOT redefine --dir / --repo: the top-level
// `charly --repo OWNER/REPO mcp serve` still pins the project, and the project context is
// threaded to every charly fork/exec as a managed prefix (serve.go computeProjectPrefix).
// --no-default-repo stays, opting out of the auto-fallback to overthinkos/overthink.
type McpServeCmd struct {
	Listen        string `name:"listen" default:":18765" help:"TCP listen address for Streamable HTTP transport"`
	Path          string `name:"path" default:"/mcp" help:"HTTP path prefix for the MCP endpoint"`
	Stdio         bool   `name:"stdio" help:"Use stdio transport instead of HTTP (for editor/LLM integration)"`
	ReadOnly      bool   `name:"read-only" help:"Skip registration of tools that mutate state"`
	NoDefaultRepo bool   `name:"no-default-repo" help:"Disable auto-fallback to overthinkos/overthink; require --dir / --repo / local charly.yml"`
}

// invokeCommand handles the command:mcp OpRun: decode the pass-through CLI tokens, parse
// them with kong against McpCmdGroup, and dispatch the selected command. `serve` BLOCKS
// for the lifetime of the MCP server, so this Invoke (and the host's
// dispatchExternalCommand call that drives it) stays open until the server stops — exactly
// the foreground-blocking shape of the original `charly mcp serve`.
func (p provider) invokeCommand(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != opRun {
		return &pb.InvokeReply{ResultJson: []byte("{}")}, nil
	}
	// Decode the args list into the CUE-GENERATED typed struct (params.McpInput) — never a
	// hand-parsed map. Command dispatch marshals op.Params as {"args": [...]} directly, so
	// the struct decodes the top-level payload (no plugin_input envelope).
	var in params.McpInput
	if raw := req.GetParamsJson(); len(raw) > 0 {
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, fmt.Errorf("plugin-mcp: decode command args: %w", err)
		}
	}

	var grp McpCmdGroup
	parser, err := kong.New(&grp,
		kong.Name("mcp"),
		kong.Description("charly mcp — the externalized MCP server bridge"),
		// Never os.Exit on a parse error; surface it as the Invoke error so the host's
		// dispatchExternalCommand reports it.
		kong.Exit(func(int) {}),
	)
	if err != nil {
		return nil, fmt.Errorf("plugin-mcp: build kong parser: %w", err)
	}
	kctx, err := parser.Parse(in.Args)
	if err != nil {
		return nil, fmt.Errorf("plugin-mcp: parse `charly mcp %s`: %w", strings.Join(in.Args, " "), err)
	}

	switch kctx.Command() {
	case "serve":
		if err := p.serve(ctx, &grp.Serve); err != nil {
			return nil, fmt.Errorf("plugin-mcp serve: %w", err)
		}
		return &pb.InvokeReply{ResultJson: []byte("{}")}, nil
	default:
		return nil, fmt.Errorf("plugin-mcp: unsupported command %q", kctx.Command())
	}
}
