// Package initbuiltin is the BUILT-IN `init` plugin unit: its self-contained CUE schema
// (embedded here, served over the Describe channel exactly like an external plugin's)
// and the per-capability input-def map. The Provider implementation and the unit
// registration live in package main (plugin_init.go) — the in-proc analogue of an
// external plugin's main.go. The `init` KIND (the init-system vocabulary:
// supervisord/systemd) was formerly a core builtin kind decoding into the typed core
// map uf.Init; it is extracted into a dedicated plugin unit, mirroring the
// sidecar/agent/module/distro/builder kind→plugin extractions. The core #Init def
// (schema/init.cue) is KEPT — it still generates spec.Init (which InitDef aliases and the
// generator consumes); this plugin schema is the self-contained VALIDATION reproduction.
// A plugin kind dispatches through runPluginKind via the Provider.Invoke(OpLoad)
// envelope — the authored entity is validated against this served schema, then decoded
// out of the closed core, landing in uf.PluginKinds["init"] keyed by the node name.
//
// PACKAGE NAME: `initbuiltin`, not `init` — `init` is a reserved Go identifier (the
// package-level init function) and cannot name a package. The directory + the authoring
// kind keyword stay `init`; only the Go package identifier differs.
package initbuiltin

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
		panic("init: read embedded schema: " + err.Error())
	}
	return body
}

// InputDefs maps the provided capability ("kind:init") to the CUE def that validates
// its authored entity body. The host carries this on the unit's PluginSchema, so
// validateAuthoredPluginInput finds #InitInput for the init kind.
var InputDefs = map[string]string{"kind:init": "#InitInput"}
