// Command plugin-mcp is the OUT-OF-TREE charly plugin serving the `mcp`
// MCP-protocol check verb (a standalone Go module, its own go.mod). It probes MCP
// servers declared via mcp_provides on a live deployment — ping, servers,
// list-tools/resources/prompts, call, read — speaking the Model Context Protocol on
// the wire via github.com/modelcontextprotocol/go-sdk. The host go-builds this binary
// and serves it OUT-OF-PROCESS over go-plugin gRPC via the charly plugin SDK, so the
// `mcp:` verb dispatches through the provider registry exactly like a built-in — with
// the verb keeping its `mcp:` discriminator + every modifier (mcp_name/tool/uri/input)
// on charly's core #Op (authoring unchanged). The fifth external dep-shed (after
// candy/plugin-appium, candy/plugin-adb, candy/plugin-kube, candy/plugin-spice); the
// go-sdk MCP CLIENT lives HERE now, out of charly's core check surface (charly's core
// still imports go-sdk only for the `charly mcp serve` SERVER, mcp_server.go).
//
// The plugin owns NO podman / OCI-label / port-mapping machinery — the host
// pre-resolves the deployment's declared mcp_provides + the single picked,
// host-routable dial endpoint (preresolveMcpEndpoint, charly/mcp_preresolve.go) and
// hands them over via the check env, so this module needs no container inspection at all.
package main

import (
	"context"
	"embed"

	pb "github.com/overthinkos/overthink/charly/plugin/proto"
	"github.com/overthinkos/overthink/charly/plugin/sdk"
)

//go:embed schema/*.cue
var schemaFS embed.FS

func main() { sdk.Serve(&provider{}, &meta{}) }

type meta struct {
	pb.UnimplementedPluginMetaServer
}

// Describe ships the plugin's TWO capabilities — verb:mcp (the MCP check verb) AND
// command:mcp (`charly mcp …`, the externalized MCP-server CLI, C1 dep-shed of the
// go-sdk SERVER out of charly's core) — plus the self-contained CUE schema, over the
// wire via sdk.BuildCapabilities. The host builds one grpcProvider per capability over
// the SAME connection (plugin_grpc.go buildUnit), each stamped with its class, so a
// single provider struct routes by wire Class (provider.go).
//
//   - verb:mcp keeps its entire authoring contract (the #McpMethod enum + every
//     modifier) on charly's core #Op — like cdp/vnc/spice it has NO plugin_input — so
//     it advertises an EMPTY InputDef.
//   - command:mcp's pass-through args are the typed #McpInput (schema/mcp-command.cue).
//
// The SDK concatenates + compiles the embedded schema/*.cue standalone here, failing
// loudly before serving if it is broken or a declared InputDef is undefined.
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities("2026.178.1200",
		[]sdk.ProvidedCapability{
			{Class: "verb", Word: "mcp", InputDef: ""},
			{Class: "command", Word: "mcp", InputDef: "#McpInput"},
		},
		schemaFS, "schema")
}
