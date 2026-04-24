package main

// Disposable + lifecycle classification for deploy-shaped configs.
//
// Two independent fields with clearly separated roles:
//
//   disposable: <bool>    // LOAD-BEARING. Default false. Explicit opt-in.
//                         //   `true` authorizes `ov rebuild <name>` to
//                         //   destroy + rebuild + restart unattended.
//   lifecycle: <tier>     // INFORMATIONAL ONLY. Free-form human tag
//                         //   (dev | qa | prod | custom-whatever).
//                         //   Has ZERO effect on disposability.
//
// There is DELIBERATELY no derivation from `lifecycle` to `disposable`.
// `lifecycle: dev` does NOT imply `disposable: true`. A deploy is only
// disposable when it literally carries `disposable: true`. This makes
// the classification safe to use on shared hosts where unrelated
// production services may run alongside — `ov rebuild` cannot touch
// anything that isn't explicitly opted in.
//
// See /ov-dev:disposable for the schema + rationale, and CLAUDE.md
// R10 for the verification-loop implications.

// Classified is the small contract a config struct implements so the
// ov CLI can ask "are you disposable?" / "what lifecycle tag do you
// carry?" without caring whether the underlying struct is VmSpec
// (vms.yml), DeploymentNode (deploy.yml), or a per-instance
// override.
//
// Both fields are plain values — no pointers, no derivation. Default
// zero value of a plain `bool` is `false`, which is exactly the
// conservative "requires user confirmation" default we want.
type Classified interface {
	IsDisposable() bool
	LifecycleTag() string
}

// Canonical lifecycle tag names documented for operators. These are
// NOT enforced — the field is free-form string. They exist so skills
// + error messages can recommend a small vocabulary, and so `ov
// status --lifecycle <tier>` output is predictable when a project
// sticks to the vocabulary.
var CanonicalLifecycleTags = []string{
	"scratch", "dev", "test", "qa", "staging", "prod",
}

// Note: the former `IsDisposableFields(disposable, lifecycle)` helper
// was removed in the schema-v3 cutover. There was no derivation to
// encode — the result was always the `disposable` argument verbatim,
// and the helper's existence obscured that. Callers now read
// `node.Disposable` directly. See /ov-dev:disposable.
