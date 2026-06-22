package main

import (
	"testing"

	"github.com/alecthomas/kong"
)

// zzCmdSeamProbeCmd is a fake subcommand used only to exercise the command seam.
type zzCmdSeamProbeCmd struct{}

func (zzCmdSeamProbeCmd) Run() error { return nil }

// zzCmdSeamProv is a fake COMMAND-class provider contributing zzCmdSeamProbeCmd.
type zzCmdSeamProv struct{ builtinCommandBase }

func (zzCmdSeamProv) Reserved() string { return "zz-cmd-seam-probe" }
func (zzCmdSeamProv) KongCommand() any {
	return &struct {
		ZzCmdSeamProbe zzCmdSeamProbeCmd `cmd:"" help:"command-seam test probe"`
	}{}
}

// TestCommandSeam_PluginCommandInjected proves the 6th (COMMAND-class) provider seam:
// a registered CommandProvider's subcommand is collected (collectCommandPlugins) and
// embedded into the REAL charly CLI grammar via the kong.Plugins embed, so
// `charly zz-cmd-seam-probe` parses and selects that command — exactly how a
// non-machinery command reaches the CLI once migrated into a provider (Phase 1-4).
// The test FAILS if the seam does not wire the provider's command into the root.
func TestCommandSeam_PluginCommandInjected(t *testing.T) {
	RegisterBuiltinProvider(zzCmdSeamProv{})

	var cli CLI
	cli.Plugins = collectCommandPlugins()
	parser, err := kong.New(&cli, kong.Name("charly"), kong.Exit(func(int) {}))
	if err != nil {
		t.Fatalf("kong.New with the command-plugin seam failed: %v", err)
	}
	ctx, err := parser.Parse([]string{"zz-cmd-seam-probe"})
	if err != nil {
		t.Fatalf("plugin command not injected into the CLI grammar: %v", err)
	}
	if got := ctx.Command(); got != "zz-cmd-seam-probe" {
		t.Fatalf("expected the plugin command selected, got %q", got)
	}
}

// TestCommandProviders_ExtractedLeafCommands proves every leaf-domain command extracted
// into a dedicated COMMAND-class provider (alias/tmux/ssh/secrets/preempt/mcp — the udev
// batch siblings) is (1) registered in providerRegistry as a CommandProvider with the
// matching Reserved() word, and (2) collected by collectCommandPlugins() and injected
// into the REAL charly CLI grammar via kong.Plugins, so its subcommand path parses and
// selects exactly as before the extraction. The test FAILS if any dedicated registration
// regresses or the command seam stops wiring one of them into the root.
func TestCommandProviders_ExtractedLeafCommands(t *testing.T) {
	cases := []struct {
		word     string   // Reserved() + top-level command name
		parse    []string // argv selecting a leaf subcommand
		selected string   // expected ctx.Command() after parse
	}{
		{"alias", []string{"alias", "list"}, "alias list"},
		{"tmux", []string{"tmux", "list", "mybox"}, "tmux list <box>"},
		{"ssh", []string{"ssh", "tunnel", "spice", "myvm"}, "ssh tunnel spice <vm>"},
		{"secrets", []string{"secrets", "list"}, "secrets list"},
		{"preempt", []string{"preempt", "status"}, "preempt status"},
		{"mcp", []string{"mcp", "serve"}, "mcp serve"},
	}
	for _, tc := range cases {
		t.Run(tc.word, func(t *testing.T) {
			// 1. Registered as a COMMAND-class provider, resolvable through the registry.
			p, ok := providerRegistry.resolve(ClassCommand, tc.word)
			if !ok {
				t.Fatalf("command:%s not registered — dedicated self-registration regressed", tc.word)
			}
			cp, ok := p.(CommandProvider)
			if !ok {
				t.Fatalf("%s provider is not a CommandProvider (got %T)", tc.word, p)
			}
			if cp.Reserved() != tc.word {
				t.Fatalf("%s provider Reserved() = %q, want %q", tc.word, cp.Reserved(), tc.word)
			}

			// 2. Collected by the command seam and injected into the real CLI grammar.
			var cli CLI
			cli.Plugins = collectCommandPlugins()
			parser, err := kong.New(&cli, kong.Name("charly"), kong.Exit(func(int) {}))
			if err != nil {
				t.Fatalf("kong.New with the command-plugin seam failed: %v", err)
			}
			ctx, err := parser.Parse(tc.parse)
			if err != nil {
				t.Fatalf("%s command not injected into the CLI grammar: %v", tc.word, err)
			}
			if got := ctx.Command(); got != tc.selected {
				t.Fatalf("expected %q selected, got %q", tc.selected, got)
			}
		})
	}
}

// TestCommandProviders_DeployLifecycleCommands proves every deploy-lifecycle + remaining
// leaf command extracted into a dedicated COMMAND-class provider (the udev/alias-template
// batch: start/stop/status/restart/update/remove/logs/shell/cmd/cp/volume/service/config/
// bundle/reap-orphans) is (1) registered in providerRegistry as a CommandProvider with the
// matching Reserved() word, and (2) collected by collectCommandPlugins() and injected into
// the REAL charly CLI grammar via kong.Plugins, so its subcommand path parses and selects
// exactly as before the extraction (the Run handler — which calls the unchanged core
// deploy/bundle machinery — is preserved verbatim). The test FAILS if any dedicated
// registration regresses or the command seam stops wiring one of them into the root.
func TestCommandProviders_DeployLifecycleCommands(t *testing.T) {
	cases := []struct {
		word     string   // Reserved() + top-level command name
		parse    []string // argv selecting the command (or a leaf subcommand)
		selected string   // expected ctx.Command() after parse
	}{
		{"start", []string{"start", "mybox"}, "start <box>"},
		{"stop", []string{"stop", "mybox"}, "stop <box>"},
		{"status", []string{"status", "mybox"}, "status <box>"},
		{"restart", []string{"restart", "mybox"}, "restart <box>"},
		{"update", []string{"update", "mybox"}, "update <box>"},
		{"remove", []string{"remove", "mybox"}, "remove <box>"},
		{"logs", []string{"logs", "mybox"}, "logs <box>"},
		{"shell", []string{"shell", "mybox"}, "shell <box>"},
		{"cmd", []string{"cmd", "mybox", "echo hi"}, "cmd <box> <command>"},
		{"cp", []string{"cp", "mybox", ":/a", "/b"}, "cp <box> <src> <dst>"},
		{"volume", []string{"volume", "list", "mybox"}, "volume list <box>"},
		{"service", []string{"service", "status", "mybox"}, "service status <box>"},
		{"config", []string{"config", "status", "mybox"}, "config status <box>"},
		{"bundle", []string{"bundle", "path"}, "bundle path"},
		{"reap-orphans", []string{"reap-orphans"}, "reap-orphans"},
	}
	for _, tc := range cases {
		t.Run(tc.word, func(t *testing.T) {
			// 1. Registered as a COMMAND-class provider, resolvable through the registry.
			p, ok := providerRegistry.resolve(ClassCommand, tc.word)
			if !ok {
				t.Fatalf("command:%s not registered — dedicated self-registration regressed", tc.word)
			}
			cp, ok := p.(CommandProvider)
			if !ok {
				t.Fatalf("%s provider is not a CommandProvider (got %T)", tc.word, p)
			}
			if cp.Reserved() != tc.word {
				t.Fatalf("%s provider Reserved() = %q, want %q", tc.word, cp.Reserved(), tc.word)
			}

			// 2. Collected by the command seam and injected into the real CLI grammar.
			var cli CLI
			cli.Plugins = collectCommandPlugins()
			parser, err := kong.New(&cli, kong.Name("charly"), kong.Exit(func(int) {}))
			if err != nil {
				t.Fatalf("kong.New with the command-plugin seam failed: %v", err)
			}
			ctx, err := parser.Parse(tc.parse)
			if err != nil {
				t.Fatalf("%s command not injected into the CLI grammar: %v", tc.word, err)
			}
			if got := ctx.Command(); got != tc.selected {
				t.Fatalf("expected %q selected, got %q", tc.selected, got)
			}
		})
	}
}

// TestCommandProviders_ExtractedReachMCP proves the extraction did not change the
// reflected MCP tool surface for the extracted commands — collectCommandPlugins() feeds
// buildMcpServer's modelCLI exactly as it feeds the real CLI, so each command's leaves
// stay auto-generated tools (toolIndex mirrors buildMcpServer via buildTestKong). `mcp`
// is the deliberate exception: `mcp.serve` is path-skipped (mcpSkipToolPaths) — you do
// not expose "start an MCP server" as a tool inside the MCP server — and the extraction
// must preserve that skip (the skip is path-keyed, not origin-keyed), so it stays ABSENT.
func TestCommandProviders_ExtractedReachMCP(t *testing.T) {
	tools := toolIndex(t, false)
	for _, name := range []string{"alias.list", "tmux.list", "secrets.list", "preempt.status"} {
		if _, ok := tools[name]; !ok {
			t.Errorf("%s missing from the MCP tool surface after extracting its command into a CommandProvider", name)
		}
	}
	if _, ok := tools["mcp.serve"]; ok {
		t.Error("mcp.serve unexpectedly present in the MCP tool surface — its mcpSkipToolPaths skip must survive the command extraction")
	}
}
