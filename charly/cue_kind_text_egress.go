package main

// Registers the (package-less, shared-scope) egress schema for rendered non-data
// text artifacts (Containerfile + service units) — the template-render-failure gate.
func init() { registerCueKind("rendered_text", "#RenderedText") }
