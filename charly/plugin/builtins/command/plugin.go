// Package command is the BUILT-IN `command` plugin unit: its self-contained CUE schema
// (embedded here, served over the Describe channel exactly like an external plugin's)
// and the per-capability input-def map. The Provider implementation and the unit
// registration live in package main (plugin_verb_command.go) — the in-proc analogue of
// an external plugin's main.go. The `command` verb (run a shell command + assert exit/
// stdout/stderr) was formerly a base #Op verb; it now lives here as a dedicated plugin
// unit. It is the HARDEST extracted verb: its provider is a CheckVerbProvider (keeping
// the live *Runner the exec probe needs), and its ACT is the dedicated install-task
// emitCmd branch (NOT a ProvisionActor) — see plugin_verb_command.go.
package command

import (
	"embed"

	"github.com/overthinkos/overthink/charly/internal/schemaconcat"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// Schema returns the unit's self-contained, package-less .cue source — produced by
// the SAME schemaconcat contract charly's base schema and an external plugin's SDK
// use (R3), so the host compiles base ++ this byte-identically to any other plugin.
func Schema() string {
	body, _, err := schemaconcat.ConcatSchema(schemaFS, "schema", nil)
	if err != nil {
		// The schema is embedded at build time; a read failure is a build defect.
		panic("command: read embedded schema: " + err.Error())
	}
	return body
}

// InputDefs maps the provided capability ("verb:command") to the CUE def that
// validates its plugin_input. The host carries this on the unit's PluginSchema, so
// validateAuthoredPluginInput finds #CommandInput for the command verb.
var InputDefs = map[string]string{"verb:command": "#CommandInput"}
