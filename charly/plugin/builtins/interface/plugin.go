// Package iface is the BUILT-IN `interface` plugin unit: its self-contained CUE schema
// (embedded here, served over the Describe channel exactly like an external plugin's)
// and the per-capability input-def map. The Provider implementation and the unit
// registration live in package main (plugin_interface.go) — the in-proc analogue of an
// external plugin's main.go. The `interface` verb (`ip -o addr show` existence + MTU +
// address probe) was formerly a base #Op verb; it now lives here as a dedicated plugin
// unit (mirrors process/port/dns — its provider is a CheckVerbProvider, keeping the
// live *Runner the probe needs). The Go package is `iface` because `interface` is a Go
// keyword; the reserved word the plugin serves is still "interface".
package iface

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
		panic("interface: read embedded schema: " + err.Error())
	}
	return body
}

// InputDefs maps the provided capability ("verb:interface") to the CUE def that
// validates its plugin_input. The host carries this on the unit's PluginSchema, so
// validateAuthoredPluginInput finds #InterfaceInput for the interface verb.
var InputDefs = map[string]string{"verb:interface": "#InterfaceInput"}
