package main

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/alecthomas/kong"
)

// externalCommandDispatch pairs an OUT-OF-PROCESS command Provider with the dynamic Kong
// holder struct kong.Parse fills, so the parsed pass-through args can be read and forwarded
// to the plugin AFTER parsing. Built by collectExternalCommandPlugins; consulted in main
// once kong.Parse has run.
type externalCommandDispatch struct {
	prov   Provider
	holder any    // *struct{ <field> *struct{ Args []string } }
	field  string // the exported holder field name (Kong needs exported fields)
}

// collectExternalCommandPlugins builds a dynamic Kong subcommand for every OUT-OF-PROCESS
// command provider — a Provider of ClassCommand that is NOT a builtin CommandProvider (the
// builtin ones contribute a static KongCommand()). reflect.StructOf cannot attach methods,
// so a dynamic command has no Run() handler and Kong's ctx.Run() cannot dispatch it; these
// are dispatched MANUALLY post-parse via dispatchExternalCommand. Returns the holder structs
// for kong.Plugins embedding — TOP-LEVEL on the CLI root, or NESTED under a parent command
// (e.g. `check`) for a provider implementing NestedCommandProvider — plus the dispatch table
// keyed by the full command PATH ("vm" top-level, "check kube" nested). Empty until a real
// external command plugin registers (the Phase-1+ command extractions, e.g. check kube).
func collectExternalCommandPlugins() (topLevel kong.Plugins, nestedByParent map[string]kong.Plugins, table map[string]externalCommandDispatch) {
	nestedByParent = map[string]kong.Plugins{}
	table = map[string]externalCommandDispatch{}
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
		d := externalCommandDispatch{prov: p, holder: holder, field: field}
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
// provider via the standard Invoke envelope (the same path verbs/kinds use). It reads the
// args out of the kong-populated holder by reflection.
func dispatchExternalCommand(d externalCommandDispatch) error {
	var args []string
	cmdField := reflect.ValueOf(d.holder).Elem().FieldByName(d.field)
	if cmdField.IsValid() && !cmdField.IsNil() {
		if a, ok := cmdField.Elem().FieldByName("Args").Interface().([]string); ok {
			args = a
		}
	}
	params, err := marshalJSON(map[string]any{"args": args})
	if err != nil {
		return fmt.Errorf("command %q: marshal args: %w", d.prov.Reserved(), err)
	}
	if _, err := d.prov.Invoke(context.Background(), &Operation{Reserved: d.prov.Reserved(), Op: OpRun, Params: params}); err != nil {
		return fmt.Errorf("command %q: %w", d.prov.Reserved(), err)
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
