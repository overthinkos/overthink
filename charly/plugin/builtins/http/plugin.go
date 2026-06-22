// Package http is the BUILT-IN `http` plugin unit: its self-contained CUE schema
// (embedded here, served over the Describe channel exactly like an external plugin's)
// and the per-capability input-def map. The Provider implementation and the unit
// registration live in package main (plugin_http.go) — the in-proc analogue of an
// external plugin's main.go. The `http` verb (host-side request / in-container curl
// probe) was formerly a base #Op verb; it now lives here as a dedicated plugin unit
// (mirrors process/port/dns — its provider is a CheckVerbProvider, keeping the live
// *Runner the request needs).
package http

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
		panic("http: read embedded schema: " + err.Error())
	}
	return body
}

// InputDefs maps the provided capability ("verb:http") to the CUE def that validates
// its plugin_input. The host carries this on the unit's PluginSchema, so
// validateAuthoredPluginInput finds #HttpInput for the http verb.
var InputDefs = map[string]string{"verb:http": "#HttpInput"}
