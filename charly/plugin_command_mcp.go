package main

// mcpCommand is the `charly mcp` command group as a dedicated COMMAND-class provider
// (the externalizable pattern landed for udev — see plugin_command_udev.go). It
// self-registers via registerDedicatedBuiltin and reaches the CLI root through
// collectCommandPlugins() → kong.Plugins; KongCommand() returns McpCmdGroup verbatim
// (the `serve` subcommand + its Run handler), so `charly mcp serve` parses and dispatches
// exactly as when it was a hardcoded CLI field. The reflected MCP tool path stays
// "mcp.serve" — its mcpDestructivePaths entry and the buildMcpServer leaf enumeration
// are keyed by that path string, not by the command's origin, so both are unaffected.
type mcpCommand struct{ builtinCommandBase }

func (mcpCommand) Reserved() string { return "mcp" }
func (mcpCommand) KongCommand() any {
	return &struct {
		Mcp McpCmdGroup `cmd:"" help:"Run an MCP server exposing the charly CLI as tools"`
	}{}
}

var _ = registerDedicatedBuiltin(mcpCommand{})
