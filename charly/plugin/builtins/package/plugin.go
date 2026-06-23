// Package pkg is the BUILT-IN `package` plugin unit: its self-contained CUE schema
// (embedded here, served over the Describe channel exactly like an external plugin's)
// and the per-capability input-def map. The Provider implementation and the unit
// registration live in package main (plugin_verb_package.go) — the in-proc analogue of
// an external plugin's main.go. The `package` verb (rpm -q / dpkg -s / pacman -Q probe +
// install the package) was formerly a base #Op verb; it now lives here as a dedicated
// plugin unit.
//
// `package` is a STATE-PROVISION verb like service but the TYPED-STEP form WITHOUT
// service's PriorEnabled teardown-restore state: its provider is a CheckVerbProvider
// (RunVerb keeps the live *Runner the rpm/dpkg/pacman probe needs) AND a
// TypedStepProvider — its do:act half lowers into a TYPED SystemPackagesStep whose
// Reverse() records the load-bearing reversals (ReverseOpPackageRemove +
// ReverseOpCoprDisable), which a RenderProvisionScript shell string would drop. It also
// keeps a RenderProvisionScript for the runtime/opt-in live act path — see
// plugin_verb_package.go.
//
// The Go package is named `pkg` (not `package`, a reserved keyword); its import path
// stays .../plugin/builtins/package, so plugin_verb_package.go aliases it
// `packageplugin "…/builtins/package"`.
package pkg

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
		panic("package: read embedded schema: " + err.Error())
	}
	return body
}

// InputDefs maps the provided capability ("verb:package") to the CUE def that validates
// its plugin_input. The host carries this on the unit's PluginSchema, so
// validateAuthoredPluginInput finds #PackageInput for the package verb.
var InputDefs = map[string]string{"verb:package": "#PackageInput"}
