// Package exampleprobe is charly's reference BUILT-IN plugin unit: its
// self-contained CUE schema (embedded here, served over the Describe channel
// exactly like an external plugin's) and the per-capability input-def map. The
// Provider implementation and the unit registration live in package main
// (plugin_example.go) — the in-proc analogue of an external plugin's main.go. A
// built-in plugin's only difference from an external one is WHERE it runs (compiled
// into charly vs a separate process), never how its schema is handled.
package exampleprobe

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
		panic("exampleprobe: read embedded schema: " + err.Error())
	}
	return body
}

// InputDefs maps each provided capability ("verb:exampleprobe") to the CUE def that
// validates its plugin_input. The host carries this on the unit's PluginSchema, so
// validateAuthoredPluginInput finds #ExampleprobeInput for the exampleprobe verb.
var InputDefs = map[string]string{"verb:exampleprobe": "#ExampleprobeInput"}
