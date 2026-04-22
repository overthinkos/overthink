# Overthink — The Container Management Experience for You and Your AI

Compose, build, deploy, and manage container images from a library of fully configurable layers. Built on a generic init system framework (`build.yml` → `init:` section) and `ov` (Go CLI). Designed to work equally well from the command line and from AI agents like Claude Code. Supports both Docker and Podman.

See `README.md` for the user-facing feature overview and command reference, `plugins/README.md` for the full skill index. This file carries only **project-specific rules and mandates** — architectural descriptions belong in skills (the single source of truth).

---

## Five Cornerstones of AI Scut Testing

1. **Your Assumptions Are the Enemy** — The thing you didn't think to test is the thing that will break.
2. **Small Bugs Have Big Friends** — Every issue you dismissed as nonessential is tomorrow's catastrophe.
3. **It's Broken Until It Runs Live** — Localhost and mocks are deceptive liars.
4. **Check Every Damn Thing** — Methodically. Tediously. No shortcuts.
5. **Then Check It Again** — Because you missed something. You always do.

## Ground Truth Rules — NEVER claim success without these (HARD RULES)

These rules exist because an agent has claimed "tests pass" / "cutover complete" / "ready to merge" based on green unit tests while the actual image failed to start. Unit tests do NOT prove a feature works. Apply BEFORE declaring any task done:

- **R1. Unit tests never substitute for runtime verification.** A green `go test ./...` means the code compiles and fixture loaders work — nothing about whether the produced artifact behaves correctly. For any change that can affect Containerfile generation, OCI labels, init systems, service startup, or deploy code: **build a real image and run it**. A container that crash-loops on `supervisord: PermissionError: /var/log/supervisor/supervisord.log` exposes what no unit test would.

- **R2. Mandatory end-to-end gate before "done" on build/deploy/test code.** The minimum sequence:
  1. `ov image build <image>` — build a concrete image (not just generate Containerfile).
  2. `ov image test <image>` — baked layer + image sections pass (NB: passes on zero-content stages too — not a substitute for R3).
  3. `ov start <image>` (or `ov deploy add <image> <image>` / `ov update <image>` for an existing deploy) — container must reach `Active: active (running)`.
  4. `ov test <image>` — full three-section run including deploy probes must pass.
  5. If any step fails, the task is NOT done. Roll back to a known-good state before continuing.

- **R3. "Generated Containerfile contains X" is a testable invariant.** When a refactor touches generation, assert the presence of every critical section in the emitted Containerfile (e.g. `grep supervisord-conf .build/<image>/Containerfile`). A Containerfile that compiles but silently drops the init-system stage produces an image with the **stock RPM config**, not the overthink config — and the stock config almost always breaks at runtime. The emitted file is the source of truth; check it.

- **R4. OCI labels are part of the contract — verify each one post-build.** After `ov image build`, `podman inspect --format '{{index .Config.Labels "org.overthinkos.init"}}'` must return the expected value for every capability label the image claims. An empty or missing label usually means a detection path silently returned nil. Treat missing labels as a failure, not a warning.

- **R5. Never claim the cutover is clean until the old artifact is gone AND the new one runs.** Deleting `image.yml` while the new overthink.yml path silently skips a build stage is not a clean cutover — it's a regression masked by the old file's absence. The test of a cutover is: rebuild from the new config, run the resulting image, observe the service reach steady-state.

- **R6. "I'll fix it in Phase 2" is not in the approved plan unless the plan says so.** If the plan says "clean cutover, zero coexistence, one PR", honor that. Do not invent phases to defer work that the plan required in the current phase. When in doubt, ask the user — do not decide scope unilaterally.

- **R7. Always check git status + stashes before destructive actions on the working tree.** `git stash` discards in-progress work; `rm` on a tracked file is destructive. If the sandbox blocks an action, read the reason and find a non-destructive alternative — do not work around it with a cleverer command.

See `/ov:test` "DO NOT fake success" section for the mandatory sequence applied to test authoring specifically.

## Prioritize Clean Architecture Above All Else

Always pick the cleanest long-term approach and prioritize having a clean codebase with any deprecated code fully removed above everything.
You have all the time in the world and taking the time to get things properly done is ALWAYS worth the effort.

## Hard Cutover by Default

Every schema change, API rename, or deprecation MUST be delivered as a single
hard-cutover PR unless the user explicitly requests a phased migration. This
applies to BOTH code (Go types, exported functions, CLI flags, OCI labels) AND
config (overthink.yml / deploy.yml / layer.yml field names and shapes).

Forbidden by default:
- Backcompat unmarshalers that accept both old and new YAML forms.
- `deprecated.go` shims or type aliases that re-export removed identifiers.
- Silent upconverters that rewrite stale configs at load time.
- Dual-mode code paths where both the old and new surface work simultaneously.
- "Phase 2 cleanup" comments or TODOs for work that the cutover PR was supposed
  to complete.

Required for every breaking change:
- A one-shot `ov migrate <name>` command that transforms legacy configs in-place.
  Migration commands are idempotent — running twice is a no-op.
- Hard load-time errors for any residual legacy field, with a one-line remediation
  hint pointing at the migration command.
- Deletion — in the same PR — of every Go type, function, CLI flag, OCI label,
  YAML field, skill doc paragraph, and test fixture that references the removed
  surface.

Rationale: phased migrations accumulate mid-state complexity that in practice
rarely gets removed. "We'll clean up in Phase 2" is the anti-pattern that R6
already forbids on a per-plan basis. Making hard cutover the default across the
project closes the loophole where this behavior sneaks in via PRs whose plans
didn't explicitly call for a clean cutover.

Exception: explicit user instruction ("keep the old API for a grace period",
"phase the cutover across two releases"). The exception must be recorded in the
plan file; when the plan is silent, hard cutover is the default.

## Where things are documented

See `plugins/README.md` for the full skill index (250+ skills across `ov`, `ov-dev`, `ov-layers`, `ov-images`, `ov-jupyter`). README.md carries the user-facing intro. All architecture / mode split / subsystem detail lives in skills — do not duplicate here.

---

## Key Rules

- **Skills first** — invoke matching skills BEFORE reading source, launching Explore agents, or grepping. Order: skills → CLAUDE.md → memory → explore (last resort). See `/ov-dev:skills`.
- **Lowercase-hyphenated names** for layers and images.
- **Tests ship with the image** — every layer that installs a service has a `tests:` block. See `/ov:test`.
- **Unified YAML** — `overthink.yml` is the single project entry point with kind-keyed `build:` / `image:` / `layer:` / `vm:` entries, `includes:`, `discover:`, and `@host/org/repo:version` remote refs. Legacy `image.yml`/scattered `layer.yml` flat-form are rejected — convert with `ov migrate unified`. See `/ov:layer`, `/ov:image`, `/ov:migrate`.
- **VMs are `kind: vm` entities** — repo-declarable VM primitives with `source.kind: cloud_image | bootc`, structured libvirt + cloud-init, and `vm:<name>` deploy targets. Legacy `image.bootc: true` + `image.vm:` + `image.libvirt:` fields are removed; convert with `ov migrate vm-spec`. See `/ov:vm`, `/ov:migrate`.
- **Hard cutover by default** — schema/API changes ship as single-PR cutovers with a matching `ov migrate <name>` command; no backcompat shims or upconverters unless the user explicitly requests a phased migration. See "Hard Cutover by Default" section.
- **Mode purity** — `LoadUnified` reads `overthink.yml` only; never merges `deploy.yml`. See `/ov-dev:go` "Mode purity".
- **Project directory resolution** — `-C`/`--dir`/`OV_PROJECT_DIR` (local) or `--repo`/`OV_PROJECT_REPO` (remote). See `/ov:image` "Project directory resolution".
- **User policy: adopt over rename** — declarative via `build.yml distro.<name>.base_user:` + `user_policy:`. See `/ov:image` "user_policy" and `/ov:build` "base_user:".
- **Unified `service:` schema** — `layer.yml` uses a single structured `service:` list (22 fields including `kind: eventlistener` for supervisord circuit breakers). Legacy `service: |...|` raw INI and `system_services:` have been removed from the runtime — external repos migrate via `ov migrate unified`. See `/ov:layer` "Service Declaration" and `/ov:migrate`.
- **Capabilities as OCI-label contract** — every `Capabilities` (alias of `ImageMetadata`) field has a `CapabilityLabelMap` entry; `TestCapabilityLabelCompleteness` enforces it. `LabelServices` carries full structured per-entry service data so `ov deploy from-image` works source-less. See `/ov-dev:capabilities`.
- **Deploy targets** — `ov deploy add <name> <ref>` unified entry point: literal `host` → local filesystem; `kubernetes` → Kustomize tree; any other name → container deploy. See `/ov:deploy`, `/ov:host-deploy`, `/ov:kubernetes`. Shared IR: `/ov-dev:install-plan`.

---

## AI Attribution (Fedora Policy Compliant)

Per [Fedora AI Contribution Policy](https://docs.fedoraproject.org/en-US/council/policy/ai-contribution-policy/), ALL commits MUST include `Assisted-by: Claude (<confidence>)` trailer. ALL GitHub issues/PRs MUST include `*Assisted-by: Claude (<confidence>)*` at the end.

| Confidence | When to Use |
|-----------|-------------|
| `fully tested and validated` | Overlay testing + all 9 testing standards met |
| `analysed on a live system` | Observed live system behavior, logs checked |
| `syntax check only` | Pre-commit hooks passed, no functional testing |
| `theoretical suggestion` | No validation performed — AVOID |

```
Fix: Add fuse-overlayfs for container startup

Tested via overlay session on LOCAL system.

Assisted-by: Claude (fully tested and validated)
```
