package main

// `group:` is a targetless DEPLOY group (validated as a #Deploy, matching
// groupKind.CueDefPath). The Calamares package group (`package-group:`) is no longer a
// core kind — it was extracted into a dedicated plugin unit (candy/plugin-package-group),
// so it carries no core CUE-kind registration; its served #PackageGroupInput schema
// validates authored input through the plugin path (runPluginKind).
func init() {
	registerCueKind("group", "#Deploy")
}
