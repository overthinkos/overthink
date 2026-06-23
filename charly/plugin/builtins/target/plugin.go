// Package target is the BUILT-IN `target` plugin unit: its self-contained CUE schema
// (embedded here, served over the Describe channel exactly like an external plugin's)
// and the per-capability input-def map. The Provider implementation and the unit
// registration live in package main (plugin_target.go) — the in-proc analogue of an
// external plugin's main.go. The `target` KIND (the Calamares install target /
// settings.conf) was formerly a core builtin kind decoding into the typed core map
// uf.Target; it is extracted into a dedicated plugin unit, mirroring the
// sidecar/agent/module/distro/builder/init/resource kind→plugin extractions. The core
// #Target def (schema/target.cue) is KEPT — it still generates spec.Target (which
// TargetSpec aliases); this plugin schema is the self-contained VALIDATION reproduction.
// A plugin kind dispatches through runPluginKind via the Provider.Invoke(OpLoad)
// envelope — the authored entity is validated against this served schema, then decoded
// out of the closed core, landing in uf.PluginKinds["target"] keyed by the node name.
// Calamares has zero on-disk corpus / readers yet, so (like module/package-group) there
// is no Targets() accessor; the canonical body sits in PluginKinds for a future importer.
package target

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
		panic("target: read embedded schema: " + err.Error())
	}
	return body
}

// InputDefs maps the provided capability ("kind:target") to the CUE def that validates
// its authored entity body. The host carries this on the unit's PluginSchema, so
// validateAuthoredPluginInput finds #TargetInput for the target kind.
var InputDefs = map[string]string{"kind:target": "#TargetInput"}
