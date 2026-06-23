// Package kernel_param is the BUILT-IN `kernel-param` plugin unit: its self-contained CUE
// schema (embedded here, served over the Describe channel exactly like an external
// plugin's) and the per-capability input-def map. The Provider implementation and the
// unit registration live in package main (plugin_kernel_param.go) — the in-proc analogue
// of an external plugin's main.go. The `kernel-param` verb (`sysctl -n` probe + `sysctl
// -w`) was formerly a base #Op verb; it now lives here as a dedicated plugin unit.
//
// The verb WORD stays `kernel-param` (the existing hyphenated wire key); the Go package
// is `kernel_param` only because a hyphen is not a legal Go package name.
//
// It is a STATE-PROVISION verb — its provider is BOTH a CheckVerbProvider (RunVerb keeps
// the live *Runner the `sysctl -n` probe needs) AND a ProvisionActor
// (RenderProvisionScript renders the `sysctl -w` at install emit + runtime act).
package kernel_param

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
		panic("kernel_param: read embedded schema: " + err.Error())
	}
	return body
}

// InputDefs maps the provided capability ("verb:kernel-param") to the CUE def that
// validates its plugin_input. The host carries this on the unit's PluginSchema, so
// validateAuthoredPluginInput finds #KernelParamInput for the kernel-param verb.
var InputDefs = map[string]string{"verb:kernel-param": "#KernelParamInput"}
