# candy/ — signpost (not the rule-set)

You are in the **candy** definitions (`candy/<name>/candy.yml` + supporting
config files: `pixi.toml`, `package.json`, `Cargo.toml`, service files, …).

**Load these skills FIRST (R0):**

- `/ov-image:layer` — the authoritative `candy.yml` schema: the `task:` verb
  catalog, `vars:` substitution, the unified `service:` schema, package
  sections, `eval:` checks, and the mandatory `version:` field.
- `/ov-image:image` — when composing candies into a box.
- `/ov-eval:eval` — authoring the `eval:` declarative checks a candy ships.

The `layer-validator` agent is a fast pre-edit sanity gate; `ov box validate`
is the authoritative checker. Use the `ov candy …` editor verbs (comment-
preserving) rather than hand-editing where possible.

**Authoritative rules live in the repo-root `CLAUDE.md`** (one level up). R0–R10,
the hard-cutover policy, and AI attribution are defined there — this file only
signposts and restates no rule. History lives in `CHANGELOG.md`.
