package main

// EDGE-INHERIT cutover C: `group:` is a targetless DEPLOY group (validated as a
// #Deploy, matching groupKind.CueDefPath); the Calamares package group moved to its
// own `package-group:` kind (validated as #Group, matching packageGroupKind).
func init() {
	registerCueKind("group", "#Deploy")
	registerCueKind("package-group", "#Group")
}
