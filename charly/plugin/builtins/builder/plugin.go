// Package builder is the BUILT-IN `builder` plugin unit: its self-contained CUE schema
// (embedded here, served over the Describe channel exactly like an external plugin's)
// and the per-capability input-def map. The Provider implementation and the unit
// registration live in package main (plugin_builder_kind.go) — the in-proc analogue of
// an external plugin's main.go. The `builder` KIND (the multi-stage builder vocabulary)
// was formerly a core builtin kind decoding into the typed core map uf.Builder; it is
// extracted into a dedicated plugin unit, mirroring the sidecar/agent/module/distro
// kind→plugin extractions. The core #Builder def (schema/builder.cue) is KEPT — it still
// generates spec.Builder (which BuilderDef aliases and the generator consumes); this
// plugin schema is the self-contained VALIDATION reproduction.
// A plugin kind dispatches through runPluginKind via the Provider.Invoke(OpLoad)
// envelope — the authored entity is validated against this served schema, then decoded
// out of the closed core, landing in uf.PluginKinds["builder"] keyed by the node name.
//
// NOTE — distinct from the deploy-target/step/builder PROVIDER files
// (plugin_builder_pixi.go etc.): those register ClassBuilder build-strategy providers.
// This is the build-VOCABULARY KIND (`builder:` map entries), a ClassKind provider.
package builder

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
		panic("builder: read embedded schema: " + err.Error())
	}
	return body
}

// InputDefs maps the provided capability ("kind:builder") to the CUE def that validates
// its authored entity body. The host carries this on the unit's PluginSchema, so
// validateAuthoredPluginInput finds #BuilderInput for the builder kind.
var InputDefs = map[string]string{"kind:builder": "#BuilderInput"}
