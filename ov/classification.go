package main

// Disposable + lifecycle + ephemeral classification for deploy-shaped configs.
//
// Three fields, two with clearly separated roles plus one operational
// counterpart:
//
//   disposable: <bool>    // LOAD-BEARING. Default false. Explicit opt-in.
//                         //   `true` authorizes `ov update <name>` to
//                         //   destroy + rebuild + restart unattended.
//   lifecycle: <tier>     // INFORMATIONAL ONLY. Free-form human tag
//                         //   (dev | qa | prod | custom-whatever).
//                         //   Has ZERO effect on disposability.
//   ephemeral: <block>    // OPERATIONAL MANDATE. Counterpart to
//                         //   disposable's authorization. Presence
//                         //   means "MUST be destroyed as soon as it
//                         //   isn't needed anymore". Implies
//                         //   `disposable: true` automatically (the
//                         //   one documented exception to anti-
//                         //   derivation, because ephemeral
//                         //   STRENGTHENS the contract).
//
// Anti-derivation invariant (with one named exception):
//   `lifecycle: dev` does NOT imply `disposable: true` — lifecycle is
//   informational only.
//   `ephemeral: ...` DOES imply `disposable: true` — ephemeral is a
//   stronger guarantee, and "must be destroyed when not needed" can
//   only be honored if "may be destroyed" is also true. Setting
//   `ephemeral: true` together with `disposable: false` is a
//   contradiction; the loader rejects it with a clear error.
//
// See /ov-internals:disposable for the schema + rationale, and CLAUDE.md
// R10 for the verification-loop implications.

// Classified is the small contract a config struct implements so the
// ov CLI can ask "are you disposable?" / "are you ephemeral?" /
// "what lifecycle tag do you carry?" without caring whether the
// underlying struct is VmSpec (vm.yml), DeploymentNode (deploy.yml),
// or a per-instance override.
//
// IsDisposable returns true when the config carries `disposable: true`
// OR is ephemeral (load-bearing implication). IsEphemeral is the
// stricter check.
type Classified interface {
	IsDisposable() bool
	IsEphemeral() bool
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

// Note: there is no `IsDisposableFields(disposable, lifecycle)` helper.
// There is no derivation to
// encode — the result would always be the `disposable` argument verbatim,
// and such a helper would obscure that. Callers read
// `node.Disposable` directly. See /ov-internals:disposable.
