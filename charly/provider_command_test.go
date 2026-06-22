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
