package main

import (
	"context"
	"strings"
	"testing"

	"github.com/alecthomas/kong"

	"github.com/overthinkos/overthink/charly/plugin/sdk"
)

// TestExternalCommandExecPlan_PassthroughArgs proves the external-command FORK/EXEC path: a
// dynamic Kong subcommand built by externalCommandHolder parses a command line, and
// externalCommandExecPlan reads the parsed pass-through args (flags included, via passthrough),
// resolves the plugin binary by word (a baked binary here), and builds the exec argv + env —
// the plan dispatchExternalCommand then hands to syscall.Exec. The env must STRIP the go-plugin
// handshake cookie (so the plugin runs in CLI mode, not serve mode) and stamp CHARLY_BIN.
func TestExternalCommandExecPlan_PassthroughArgs(t *testing.T) {
	const word = "zzexeccmd"
	// Make the strip non-trivial: set the cookie, then assert it is absent from the exec env.
	t.Setenv(sdk.Handshake.MagicCookieKey, sdk.Handshake.MagicCookieValue)
	bakedPluginBinaries[provKey(ClassCommand, word)] = "/fake/plugins/" + word
	defer delete(bakedPluginBinaries, provKey(ClassCommand, word))

	field := exportedCommandField(word)
	holder := externalCommandHolder(word, field)
	var cli struct{ kong.Plugins }
	cli.Plugins = kong.Plugins{holder}
	parser, err := kong.New(&cli, kong.Name("charly"))
	if err != nil {
		t.Fatalf("kong.New with dynamic command holder: %v", err)
	}
	if _, err := parser.Parse([]string{word, "nodes", "--wide"}); err != nil {
		t.Fatalf("kong.Parse external command: %v", err)
	}

	d := externalCommandDispatch{word: word, holder: holder, field: field}
	bin, argv, env, err := externalCommandExecPlan(d)
	if err != nil {
		t.Fatalf("externalCommandExecPlan: %v", err)
	}
	if want := "/fake/plugins/" + word; bin != want {
		t.Fatalf("bin = %q, want the baked binary %q", bin, want)
	}
	want := []string{bin, "nodes", "--wide"}
	if len(argv) != len(want) {
		t.Fatalf("argv = %v, want %v", argv, want)
	}
	for i := range want {
		if argv[i] != want[i] {
			t.Fatalf("argv[%d] = %q, want %q (full %v)", i, argv[i], want[i], argv)
		}
	}
	assertCommandEnv(t, env)
}

// TestExternalCommandExecPlan_NestedCheckCommand proves a NestedCommandProvider's dynamic
// command nests UNDER `check` (kong.Plugins embedded in a CheckCmd-like parent), parses
// `check examplekube …`, keys the dispatch table by the full path "check examplekube"
// (commandPathKey), and builds the exec plan from the resolved (baked) binary + pass-through
// args.
func TestExternalCommandExecPlan_NestedCheckCommand(t *testing.T) {
	const word = "zzexecnested"
	bakedPluginBinaries[provKey(ClassCommand, word)] = "/fake/plugins/" + word
	defer delete(bakedPluginBinaries, provKey(ClassCommand, word))

	field := exportedCommandField(word)
	holder := externalCommandHolder(word, field)

	type checkLike struct {
		Box struct {
			X bool
		} `cmd:"" help:"static sibling"`
		kong.Plugins
	}
	var cli struct {
		Check checkLike `cmd:""`
	}
	cli.Check.Plugins = kong.Plugins{holder}

	parser, err := kong.New(&cli, kong.Name("charly"))
	if err != nil {
		t.Fatalf("kong.New nested: %v", err)
	}
	kctx, err := parser.Parse([]string{"check", word, "nodes", "--wide"})
	if err != nil {
		t.Fatalf("kong.Parse nested: %v", err)
	}
	if key := commandPathKey(kctx.Command()); key != "check "+word {
		t.Fatalf("commandPathKey(%q) = %q, want %q", kctx.Command(), key, "check "+word)
	}
	d := externalCommandDispatch{word: word, holder: holder, field: field}
	_, argv, _, err := externalCommandExecPlan(d)
	if err != nil {
		t.Fatalf("externalCommandExecPlan: %v", err)
	}
	if len(argv) != 3 || argv[0] != "/fake/plugins/"+word || argv[1] != "nodes" || argv[2] != "--wide" {
		t.Fatalf("argv = %v, want [/fake/plugins/%s nodes --wide]", argv, word)
	}
}

// assertCommandEnv checks commandExecEnv stripped the go-plugin handshake cookie (so the
// fork/exec'd plugin runs in CLI mode, not serve mode — sdk.IsServeMode) and stamped CHARLY_BIN.
func assertCommandEnv(t *testing.T, env []string) {
	t.Helper()
	cookie := sdk.Handshake.MagicCookieKey + "="
	hasBin := false
	for _, e := range env {
		if strings.HasPrefix(e, cookie) {
			t.Fatalf("env must NOT carry the go-plugin handshake cookie %q (the plugin would enter serve mode): %q", cookie, e)
		}
		if strings.HasPrefix(e, "CHARLY_BIN=") {
			hasBin = true
		}
	}
	if !hasBin {
		t.Fatal("env must stamp CHARLY_BIN so the plugin shells back to the dispatching charly")
	}
}

// fakeNestedCheckCmd is a NestedCommandProvider that nests its command under `check` — the
// shape the dep-shed extractions (charly check kube/adb/appium) take. Used by
// TestCommandProviders_CheckNestedPluginsInjected (provider_command_test.go) to prove the
// nested-under-check grammar seam survives the check-command extraction.
type fakeNestedCheckCmd struct{}

func (*fakeNestedCheckCmd) Reserved() string      { return "examplekube" }
func (*fakeNestedCheckCmd) Class() ProviderClass  { return ClassCommand }
func (*fakeNestedCheckCmd) CommandParent() string { return "check" }
func (*fakeNestedCheckCmd) Invoke(context.Context, *Operation) (*Result, error) {
	return &Result{}, nil
}
