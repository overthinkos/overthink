package main

// Registers charly's own (package-less, shared-scope) cloud-init egress schemas:
// the meta-data and network-config seed files. user-data is the separately
// compiled vendored #CloudConfig (cue_egress_cloud_config.go).
func init() {
	registerCueKind("cloud_init_meta", "#CloudInitMeta")
	registerCueKind("cloud_init_net", "#NetworkConfigV2")
}
