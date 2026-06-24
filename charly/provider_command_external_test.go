package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/alecthomas/kong"
)

// fakeExternalCmd is an OUT-OF-PROCESS-style command Provider: ClassCommand but NOT a
// builtin CommandProvider (no static KongCommand), so it takes the dynamic external path.
type fakeExternalCmd struct{ gotArgs []string }

func (*fakeExternalCmd) Reserved() string     { return "examplecmd" }
func (*fakeExternalCmd) Class() ProviderClass { return ClassCommand }
func (f *fakeExternalCmd) Invoke(_ context.Context, op *Operation) (*Result, error) {
	var p struct {
		Args []string `json:"args"`
	}
	_ = json.Unmarshal(op.Params, &p)
	f.gotArgs = p.Args
	return &Result{}, nil
}

// TestExternalCommandSeam_DynamicCommandDispatch proves the external-command-plugin path: a
// dynamic Kong subcommand built by externalCommandHolder parses a command line, and
// dispatchExternalCommand forwards the parsed pass-through args (flags included, via
// passthrough) to the out-of-process command provider's Invoke — the path a
// `charly <plugin-cmd> …` invocation takes once a real external command plugin registers.
func TestExternalCommandSeam_DynamicCommandDispatch(t *testing.T) {
	fake := &fakeExternalCmd{}
	field := exportedCommandField("examplecmd")
	holder := externalCommandHolder("examplecmd", field)

	var cli struct{ kong.Plugins }
	cli.Plugins = kong.Plugins{holder}
	parser, err := kong.New(&cli, kong.Name("charly"))
	if err != nil {
		t.Fatalf("kong.New with dynamic command holder: %v", err)
	}
	kctx, err := parser.Parse([]string{"examplecmd", "nodes", "--wide"})
	if err != nil {
		t.Fatalf("kong.Parse external command: %v", err)
	}
	t.Logf("kctx.Command() = %q", kctx.Command())

	d := externalCommandDispatch{prov: fake, word: "examplecmd", holder: holder, field: field}
	if err := dispatchExternalCommand(d); err != nil {
		t.Fatalf("dispatchExternalCommand: %v", err)
	}
	if len(fake.gotArgs) != 2 || fake.gotArgs[0] != "nodes" || fake.gotArgs[1] != "--wide" {
		t.Fatalf("plugin received args %v, want [nodes --wide] (passthrough incl. the flag)", fake.gotArgs)
	}
}

// fakeNestedCheckCmd is a NestedCommandProvider that nests its command under `check` —
// the shape the dep-shed extractions (charly check kube/adb/appium) take.
type fakeNestedCheckCmd struct{ gotArgs []string }

func (*fakeNestedCheckCmd) Reserved() string      { return "examplekube" }
func (*fakeNestedCheckCmd) Class() ProviderClass  { return ClassCommand }
func (*fakeNestedCheckCmd) CommandParent() string { return "check" }
func (f *fakeNestedCheckCmd) Invoke(_ context.Context, op *Operation) (*Result, error) {
	var p struct {
		Args []string `json:"args"`
	}
	_ = json.Unmarshal(op.Params, &p)
	f.gotArgs = p.Args
	return &Result{}, nil
}

// TestExternalCommandSeam_NestedCheckCommand proves a NestedCommandProvider's dynamic
// command nests UNDER `check` (kong.Plugins embedded in a CheckCmd-like parent, exactly
// like the real CheckCmd embed), parses `check examplekube …`, keys the dispatch table by
// the full path "check examplekube" (commandPathKey), and forwards the pass-through args.
func TestExternalCommandSeam_NestedCheckCommand(t *testing.T) {
	fake := &fakeNestedCheckCmd{}
	field := exportedCommandField("examplekube")
	holder := externalCommandHolder("examplekube", field)

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
	kctx, err := parser.Parse([]string{"check", "examplekube", "nodes", "--wide"})
	if err != nil {
		t.Fatalf("kong.Parse nested: %v", err)
	}
	if key := commandPathKey(kctx.Command()); key != "check examplekube" {
		t.Fatalf("commandPathKey(%q) = %q, want %q", kctx.Command(), key, "check examplekube")
	}
	d := externalCommandDispatch{prov: fake, word: "examplekube", holder: holder, field: field}
	if err := dispatchExternalCommand(d); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(fake.gotArgs) != 2 || fake.gotArgs[0] != "nodes" || fake.gotArgs[1] != "--wide" {
		t.Fatalf("nested plugin got args %v, want [nodes --wide]", fake.gotArgs)
	}
}
