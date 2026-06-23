// Package distro is the BUILT-IN `distro` plugin unit: its self-contained CUE schema
// (embedded here, served over the Describe channel exactly like an external plugin's)
// and the per-capability input-def map. The Provider implementation and the unit
// registration live in package main (plugin_distro.go) — the in-proc analogue of an
// external plugin's main.go. The `distro` KIND (the per-distro build vocabulary) was
// formerly a core builtin kind decoding into the typed core map uf.Distro; it is
// extracted into a dedicated plugin unit, mirroring the sidecar/agent/module kind→plugin
// extractions. The core #Distro def (schema/distro.cue) is KEPT — it still generates
// spec.Distro (which DistroDef aliases and the generator/format code consumes); this
// plugin schema is the self-contained VALIDATION reproduction, exactly as the sidecar
// plugin reproduces #Sidecar while spec.Sidecar stays core.
// A plugin kind dispatches through runPluginKind via the Provider.Invoke(OpLoad)
// envelope — the authored entity is validated against this served schema, then decoded
// out of the closed core, landing in uf.PluginKinds["distro"] keyed by the node name.
package distro

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
		panic("distro: read embedded schema: " + err.Error())
	}
	return body
}

// InputDefs maps the provided capability ("kind:distro") to the CUE def that validates
// its authored entity body. The host carries this on the unit's PluginSchema, so
// validateAuthoredPluginInput finds #DistroInput for the distro kind.
var InputDefs = map[string]string{"kind:distro": "#DistroInput"}
