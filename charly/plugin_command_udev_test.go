package main

import (
	"testing"

	"github.com/alecthomas/kong"
)

// TestCommandProvider_Udev proves the `udev` leaf command is served by a builtin
// CommandProvider (the 6th, COMMAND-class, provider) instead of a hardcoded field on
// the CLI struct: it self-registers in providerRegistry as (command, udev), is gathered
// by collectCommandPlugins(), and its static KongCommand() embeds into the REAL charly
// CLI grammar via kong.Plugins, so `charly udev <sub>` parses and selects that command
// exactly as before the extraction. The test FAILS if the dedicated registration
// regresses or the seam stops wiring the command into the root.
func TestCommandProvider_Udev(t *testing.T) {
	// 1. Registered as a COMMAND-class provider, resolvable through the registry.
	p, ok := providerRegistry.resolve(ClassCommand, "udev")
	if !ok {
		t.Fatal("command:udev not registered — dedicated self-registration regressed")
	}
	if p.Class() != ClassCommand {
		t.Fatalf("udev provider Class() = %q, want %q", p.Class(), ClassCommand)
	}
	cp, ok := p.(CommandProvider)
	if !ok {
		t.Fatalf("udev provider is not a CommandProvider (got %T)", p)
	}
	if cp.Reserved() != "udev" {
		t.Fatalf("udev provider Reserved() = %q, want %q", cp.Reserved(), "udev")
	}

	// 2. Collected by the command seam and injected into the real CLI grammar.
	var cli CLI
	cli.Plugins = collectCommandPlugins()
	parser, err := kong.New(&cli, kong.Name("charly"), kong.Exit(func(int) {}))
	if err != nil {
		t.Fatalf("kong.New with the command-plugin seam failed: %v", err)
	}
	ctx, err := parser.Parse([]string{"udev", "status"})
	if err != nil {
		t.Fatalf("udev command not injected into the CLI grammar: %v", err)
	}
	if got := ctx.Command(); got != "udev status" {
		t.Fatalf("expected `udev status` selected, got %q", got)
	}

	// 3. The MCP tool surface still exposes the udev leaves — the extraction must
	//    not drop them from the reflected grammar (buildTestKong mirrors buildMcpServer).
	tools := toolIndex(t, false)
	if _, ok := tools["udev.status"]; !ok {
		t.Error("udev.status missing from the MCP tool surface after extracting udev into a CommandProvider")
	}
}
