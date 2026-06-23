// Package service is the BUILT-IN `service` plugin unit: its self-contained CUE schema
// (embedded here, served over the Describe channel exactly like an external plugin's)
// and the per-capability input-def map. The Provider implementation and the unit
// registration live in package main (plugin_verb_service.go) — the in-proc analogue of
// an external plugin's main.go. The `service` verb (supervisorctl/systemctl probe +
// enable the packaged unit) was formerly a base #Op verb; it now lives here as a
// dedicated plugin unit.
//
// `service` is the TYPED-STEP-OUTLIER state-provision verb: its provider is a
// CheckVerbProvider (RunVerb keeps the live *Runner the probe needs) AND a
// TypedStepProvider — its do:act half lowers into a TYPED ServicePackagedStep whose
// Reverse() records the load-bearing reversals (ReverseOpServiceDisable / RestoreEnabled
// / RemoveDropin), which a RenderProvisionScript shell string would drop. It also keeps a
// RenderProvisionScript for the runtime/opt-in live act path — see plugin_verb_service.go.
package service

import (
	"embed"

	"github.com/overthinkos/overthink/charly/internal/schemaconcat"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// Schema returns the unit's self-contained, package-less .cue source — produced by the
// SAME schemaconcat contract charly's base schema and an external plugin's SDK use (R3),
// so the host compiles base ++ this byte-identically to any other plugin.
func Schema() string {
	body, _, err := schemaconcat.ConcatSchema(schemaFS, "schema", nil)
	if err != nil {
		// The schema is embedded at build time; a read failure is a build defect.
		panic("service: read embedded schema: " + err.Error())
	}
	return body
}

// InputDefs maps the provided capability ("verb:service") to the CUE def that validates
// its plugin_input. The host carries this on the unit's PluginSchema, so
// validateAuthoredPluginInput finds #ServiceInput for the service verb.
var InputDefs = map[string]string{"verb:service": "#ServiceInput"}
