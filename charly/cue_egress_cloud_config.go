package main

// Registers the vendored cloud-init cloud-config schema as the egress kind
// "cloud_config". The schema is Canonical's authoritative JSON Schema run
// through `cue import jsonschema:` (see schema/vendor/README.md); it carries a
// `package` clause + CUE-stdlib imports, so it compiles as its own instance via
// registerVendoredEgressKind rather than joining sharedCueSchema.
func init() { registerVendoredEgressKind("cloud_config", "cloud_config.cue", "#CloudConfig") }
