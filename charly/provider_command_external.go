package main

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/alecthomas/kong"
)

// externalCommandDispatch pairs an OUT-OF-PROCESS command word with the dynamic Kong holder
// struct kong.Parse fills, so the parsed pass-through args can be read and forwarded to the
// plugin AFTER parsing. Built by collectExternalCommandPlugins; consulted in main once
// kong.Parse has run. prov is nil for a PRESCANNED command (the common path) — the provider
// is built+connected LAZILY in dispatchExternalCommand, only when the user actually invokes
// the command — and set when a command provider was already connected at collect time.
type externalCommandDispatch struct {
	prov   Provider // nil ⇒ lazy-connect by word on dispatch
	word   string   // the command word: keys the registry resolve + the lazy-connect
	holder any      // *struct{ <field> *struct{ Args []string } }
	field  string   // the exported holder field name (Kong needs exported fields)
}

// collectExternalCommandPlugins builds a dynamic Kong subcommand for every OUT-OF-PROCESS
// command — a ClassCommand that is NOT a builtin CommandProvider (the builtin ones contribute
// a static KongCommand()). reflect.StructOf cannot attach methods, so a dynamic command has no
// Run() handler and Kong's ctx.Run() cannot dispatch it; these are dispatched MANUALLY
// post-parse via dispatchExternalCommand. Returns the holder structs for kong.Plugins embedding
// — TOP-LEVEL on the CLI root, or NESTED under a parent command (e.g. `check`) for a provider
// implementing NestedCommandProvider — plus the dispatch table keyed by the full command PATH
// ("vm" top-level, "check kube" nested). TWO sources, unioned:
//   - (a) an already-CONNECTED external command provider in the registry (the eager path —
//     uncommon, since command plugins connect lazily; carries the live provider + supports a
//     NestedCommandProvider parent);
//   - (b) a PRESCANNED command word (prescanProjectCommandWords, run in main before kong.Parse)
//     — the common path: a TOP-LEVEL grammar holder with prov nil, lazy-connected on dispatch.
//
// Empty (no external commands) when the project declares no command plugins — the grammar is
// then byte-for-byte the builtin set.
func collectExternalCommandPlugins() (topLevel kong.Plugins, nestedByParent map[string]kong.Plugins, table map[string]externalCommandDispatch) {
	nestedByParent = map[string]kong.Plugins{}
	table = map[string]externalCommandDispatch{}
	seen := map[string]bool{} // command words already given a holder
	// (a) already-connected external command providers.
	for _, p := range providerRegistry.allProviders() {
		if p.Class() != ClassCommand {
			continue
		}
		if _, builtin := p.(CommandProvider); builtin {
			continue // builtin commands use their static, compiled-in KongCommand()
		}
		word := p.Reserved()
		field := exportedCommandField(word)
		holder := externalCommandHolder(word, field)
		d := externalCommandDispatch{prov: p, word: word, holder: holder, field: field}
		seen[word] = true
		if ncp, ok := p.(NestedCommandProvider); ok {
			if parent := ncp.CommandParent(); parent != "" {
				nestedByParent[parent] = append(nestedByParent[parent], holder)
				table[parent+" "+word] = d
				continue
			}
		}
		topLevel = append(topLevel, holder)
		table[word] = d
	}
	// (b) prescanned-but-not-yet-connected command words — TOP-LEVEL holders, lazy-connected
	// on dispatch (prov nil). Nested external commands stay the connected path (a): the prescan
	// learns only the word, not its parent, and no real nested external command exists today.
	for _, word := range declaredExternalCommandWords() {
		if seen[word] {
			continue
		}
		field := exportedCommandField(word)
		holder := externalCommandHolder(word, field)
		topLevel = append(topLevel, holder)
		table[word] = externalCommandDispatch{prov: nil, word: word, holder: holder, field: field}
	}
	return topLevel, nestedByParent, table
}

// NestedCommandProvider is an optional refinement of a ClassCommand Provider: it nests its
// command UNDER an existing parent command (e.g. `check`) rather than at the CLI root. The
// parent command must embed kong.Plugins for the dynamic subcommand to attach (CheckCmd
// does). Used by the dep-shed command extractions — `charly check kube`/`adb`/`appium`.
type NestedCommandProvider interface {
	Provider
	CommandParent() string
}

// commandPathKey strips the trailing " <args>" placeholder Kong renders for an external
// command's pass-through Args, yielding the command PATH that keys the dispatch table:
// "examplecmd <args>" → "examplecmd"; "check kube <args>" → "check kube".
func commandPathKey(kongCommand string) string {
	return strings.TrimSuffix(kongCommand, " <args>")
}

// externalCommandHolder builds a Kong command holder for one out-of-process command:
//
//	*struct{ <Field> *struct{ Args []string `arg:"" passthrough:""` } `cmd:"" name:"<word>"` }
//
// The pass-through Args carry every token after the command word; the plugin parses its own
// flags (its .cue/params own that contract), so the core needs no per-flag knowledge here.
func externalCommandHolder(word, field string) any {
	argsType := reflect.StructOf([]reflect.StructField{
		{
			Name: "Args",
			Type: reflect.TypeOf([]string{}),
			Tag:  `arg:"" optional:"" passthrough:"" help:"arguments forwarded to the command plugin"`,
		},
	})
	holderType := reflect.StructOf([]reflect.StructField{
		{
			Name: field,
			Type: reflect.PtrTo(argsType),
			Tag:  reflect.StructTag(fmt.Sprintf(`cmd:"" name:%q help:%q`, word, word+" (out-of-process command plugin)")),
		},
	})
	return reflect.New(holderType).Interface()
}

// dispatchExternalCommand forwards the parsed pass-through args to the out-of-process command
// provider via the standard Invoke envelope (the same path verbs/kinds use), reading the args
// out of the kong-populated holder by reflection. When the dispatch entry was a PRESCANNED word
// (prov nil), it LAZY-CONNECTS the plugin first (connectCommandPlugin) — so the host build +
// gRPC connect is paid only on an actual `charly <word>` invocation, never on every charly call.
func dispatchExternalCommand(d externalCommandDispatch) error {
	var args []string
	cmdField := reflect.ValueOf(d.holder).Elem().FieldByName(d.field)
	if cmdField.IsValid() && !cmdField.IsNil() {
		if a, ok := cmdField.Elem().FieldByName("Args").Interface().([]string); ok {
			args = a
		}
	}
	prov := d.prov
	if prov == nil {
		p, ok := connectCommandPlugin(d.word)
		if !ok {
			return fmt.Errorf("command %q: command plugin did not connect (no ClassCommand provider)", d.word)
		}
		prov = p
	}
	params, err := marshalJSON(map[string]any{"args": args})
	if err != nil {
		return fmt.Errorf("command %q: marshal args: %w", d.word, err)
	}
	if _, err := prov.Invoke(context.Background(), &Operation{Reserved: d.word, Op: OpRun, Params: params}); err != nil {
		return fmt.Errorf("command %q: %w", d.word, err)
	}
	return nil
}

// exportedCommandField makes an exported (capitalized, alnum-only) Go field name from a
// command word so reflect.StructOf accepts it (Kong requires exported fields); the `name:`
// tag carries the real CLI word, so the field name itself is never user-visible.
func exportedCommandField(word string) string {
	clean := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return '_'
	}, word)
	if clean == "" {
		return "Cmd"
	}
	return strings.ToUpper(clean[:1]) + clean[1:]
}
