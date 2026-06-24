package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/alecthomas/kong"
)

// fakeLazyCmd is a registered ClassCommand provider used to prove the LAZY-connect dispatch
// path: dispatchExternalCommand with a nil prov resolves it from the registry by word (the
// in-process analogue of connectCommandPlugin's out-of-process build+connect).
type fakeLazyCmd struct{ gotArgs []string }

func (*fakeLazyCmd) Reserved() string     { return "zzlazycmd" }
func (*fakeLazyCmd) Class() ProviderClass { return ClassCommand }
func (f *fakeLazyCmd) Invoke(_ context.Context, op *Operation) (*Result, error) {
	var p struct {
		Args []string `json:"args"`
	}
	_ = json.Unmarshal(op.Params, &p)
	f.gotArgs = p.Args
	return &Result{}, nil
}

// TestCommandPrescan_RegisterAndCollect proves the prescan→grammar path: a declared external
// command word (registerDeclaredExternalCommand, as the byte-gated prescanPluginManifest does)
// surfaces in declaredExternalCommandWords AND collectExternalCommandPlugins builds a TOP-LEVEL
// grammar holder + a LAZY dispatch entry (prov nil, word set) for it — so `charly <word>` parses
// before the provider connects.
func TestCommandPrescan_RegisterAndCollect(t *testing.T) {
	registerDeclaredExternalCommand("zzprescancmd")
	found := false
	for _, w := range declaredExternalCommandWords() {
		if w == "zzprescancmd" {
			found = true
		}
	}
	if !found {
		t.Fatal("declaredExternalCommandWords missing the prescanned word")
	}
	_, _, table := collectExternalCommandPlugins()
	d, ok := table["zzprescancmd"]
	if !ok {
		t.Fatal("collectExternalCommandPlugins built no dispatch entry for the prescanned word")
	}
	if d.prov != nil {
		t.Fatalf("prescanned dispatch entry should be lazy (prov nil), got %T", d.prov)
	}
	if d.word != "zzprescancmd" {
		t.Fatalf("dispatch entry word = %q, want zzprescancmd", d.word)
	}
}

// TestDispatchExternalCommand_LazyConnect proves the lazy dispatch resolves a not-eagerly-passed
// provider from the registry by word and forwards the pass-through args — the path a prescanned
// `charly <word> …` takes (connectCommandPlugin's first line returns the already-registered
// provider instead of doing the out-of-process build+connect).
func TestDispatchExternalCommand_LazyConnect(t *testing.T) {
	fake := &fakeLazyCmd{}
	RegisterBuiltinProvider(fake)
	field := exportedCommandField("zzlazycmd")
	holder := externalCommandHolder("zzlazycmd", field)
	var cli struct{ kong.Plugins }
	cli.Plugins = kong.Plugins{holder}
	parser, err := kong.New(&cli, kong.Name("charly"))
	if err != nil {
		t.Fatalf("kong.New: %v", err)
	}
	if _, err := parser.Parse([]string{"zzlazycmd", "alpha", "--beta"}); err != nil {
		t.Fatalf("kong.Parse: %v", err)
	}
	// prov nil ⇒ lazy: dispatchExternalCommand must resolve fake from the registry by word.
	d := externalCommandDispatch{prov: nil, word: "zzlazycmd", holder: holder, field: field}
	if err := dispatchExternalCommand(d); err != nil {
		t.Fatalf("dispatchExternalCommand (lazy): %v", err)
	}
	if len(fake.gotArgs) != 2 || fake.gotArgs[0] != "alpha" || fake.gotArgs[1] != "--beta" {
		t.Fatalf("lazy plugin got args %v, want [alpha --beta]", fake.gotArgs)
	}
}

// TestScanDirFlag covers the pre-parse -C/--dir project-dir scan (both spaced and = forms).
func TestScanDirFlag(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"charly", "examplecommand"}, ""},
		{[]string{"charly", "-C", "/p", "examplecommand"}, "/p"},
		{[]string{"charly", "--dir", "/q", "x"}, "/q"},
		{[]string{"charly", "-C=/r", "x"}, "/r"},
		{[]string{"charly", "--dir=/s", "x"}, "/s"},
		{[]string{"charly", "-C"}, ""}, // dangling flag, no value
	}
	for _, tc := range cases {
		if got := scanDirFlag(tc.args); got != tc.want {
			t.Errorf("scanDirFlag(%v) = %q, want %q", tc.args, got, tc.want)
		}
	}
}
