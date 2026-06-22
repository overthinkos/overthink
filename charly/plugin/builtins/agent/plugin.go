// Package agent is the BUILT-IN `agent` plugin unit: its self-contained CUE schema
// (embedded here, served over the Describe channel exactly like an external plugin's)
// and the per-capability input-def map. The Provider implementation and the unit
// registration live in package main (plugin_agent.go) — the in-proc analogue of an
// external plugin's main.go. The `agent` KIND (the AI-CLI grader catalog) was
// formerly a core builtin kind decoding into the typed core map uf.Agent; it is
// extracted into a dedicated plugin unit, mirroring the package-group kind→plugin
// extraction. The core #Agent def (schema/agent.cue) is KEPT — it still generates
// spec.Agent (which AgentConfig aliases and the iterate/check harness consumes); this
// plugin schema is the self-contained VALIDATION reproduction, exactly as the
// package-group plugin reproduces #Group while spec.Group stays core.
// Unlike a verb plugin (a CheckVerbProvider dispatched in-proc), a plugin kind
// dispatches through runPluginKind via the Provider.Invoke(OpLoad) envelope — the
// authored entity is validated against this served schema, then decoded out of the
// closed core, landing in uf.PluginKinds["agent"] keyed by the node name.
package agent

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
		panic("agent: read embedded schema: " + err.Error())
	}
	return body
}

// InputDefs maps the provided capability ("kind:agent") to the CUE def that validates
// its authored entity body. The host carries this on the unit's PluginSchema, so
// validateAuthoredPluginInput finds #AgentInput for the agent kind.
var InputDefs = map[string]string{"kind:agent": "#AgentInput"}
