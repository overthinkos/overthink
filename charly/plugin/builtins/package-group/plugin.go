// Package packagegroup is the BUILT-IN `package-group` plugin unit: its
// self-contained CUE schema (embedded here, served over the Describe channel exactly
// like an external plugin's) and the per-capability input-def map. The Provider
// implementation and the unit registration live in package main
// (plugin_package_group.go) — the in-proc analogue of an external plugin's main.go.
// The `package-group` KIND (the Calamares-style netinstall package group) was
// formerly a core builtin kind decoding into a typed core map; it is the
// FIRST kind extracted into a dedicated plugin unit, proving the kind→plugin pattern.
// Unlike a verb plugin (a CheckVerbProvider dispatched in-proc), a plugin kind
// dispatches through runPluginKind via the Provider.Invoke(OpLoad) envelope — the
// authored entity is validated against this served schema, then decoded out of the
// closed core, landing in uf.PluginKinds keyed by the kind word.
package packagegroup

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
		panic("packagegroup: read embedded schema: " + err.Error())
	}
	return body
}

// InputDefs maps the provided capability ("kind:package-group") to the CUE def that
// validates its authored entity body. The host carries this on the unit's
// PluginSchema, so validateAuthoredPluginInput finds #PackageGroupInput for the
// package-group kind.
var InputDefs = map[string]string{"kind:package-group": "#PackageGroupInput"}
