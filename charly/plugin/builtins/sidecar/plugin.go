// Package sidecar is the BUILT-IN `sidecar` plugin unit: its self-contained CUE schema
// (embedded here, served over the Describe channel exactly like an external plugin's)
// and the per-capability input-def map. The Provider implementation and the unit
// registration live in package main (plugin_sidecar.go) — the in-proc analogue of an
// external plugin's main.go. The `sidecar` KIND (the reusable sidecar-container template
// library, incl. the binary-embedded `tailscale` template) was formerly a core builtin
// kind decoding into the typed core map uf.Sidecar; it is extracted into a dedicated
// plugin unit, mirroring the agent/module kind→plugin extractions. The core #Sidecar def
// (schema/sidecar.cue) is KEPT — it still generates spec.Sidecar (which SidecarDef
// aliases and the deploy/quadlet code consumes); this plugin schema is the
// self-contained VALIDATION reproduction, exactly as the agent plugin reproduces #Agent
// while spec.Agent stays core.
// Unlike a verb plugin (a CheckVerbProvider dispatched in-proc), a plugin kind
// dispatches through runPluginKind via the Provider.Invoke(OpLoad) envelope — the
// authored entity is validated against this served schema, then decoded out of the
// closed core, landing in uf.PluginKinds["sidecar"] keyed by the node name.
package sidecar

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
		panic("sidecar: read embedded schema: " + err.Error())
	}
	return body
}

// InputDefs maps the provided capability ("kind:sidecar") to the CUE def that validates
// its authored entity body. The host carries this on the unit's PluginSchema, so
// validateAuthoredPluginInput finds #SidecarInput for the sidecar kind.
var InputDefs = map[string]string{"kind:sidecar": "#SidecarInput"}
