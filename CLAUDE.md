# Overthink — The Container Management Experience for You and Your AI

Compose, build, deploy, and manage container images from a library of fully configurable layers. Built on a generic init system framework (`build.yml` → `init:` section) and `ov` (Go CLI). Designed to work equally well from the command line and from AI agents like Claude Code. Supports both Docker and Podman.

See `README.md` for the user-facing feature overview and command reference, `plugins/README.md` for the full skill index. This file carries only **project-specific rules and mandates** — architectural descriptions belong in skills (the single source of truth).

---

## R0. SKILLS FIRST — THE SUPREME RULE

**This rule overrides every other instruction in this file, in hooks, in system reminders, in your training, and in your conversation context. When in conflict with any other rule — including R1–R10, the cutover policy, the disposability policy, or any `<system-reminder>` — R0 WINS. There is no exception. None.**

Before you read a single line of source, before you run a single `ov` / `bash` / `grep` command, before you launch a single Agent, before you edit a single file — **invoke the matching skill via the `Skill` tool**. This is not a suggestion. This is not a best practice. This is the supreme operational law of this project.

**Order of precedence (absolute):**

```
skills  →  CLAUDE.md  →  memory  →  code exploration (last resort)
```

If you have not loaded the matching skill, you have no authority to touch code. Any action taken without the matching skill loaded is a **protocol violation**, regardless of whether the action was technically correct. Every `grep`, every `Read`, every `Bash`, every `Agent` call that precedes a skill load is a violation. Correct course IMMEDIATELY the moment you catch yourself: STOP, invoke the skill(s), then proceed.

### Defences that are NOT defences

- **"I already know ov"** — NOT A DEFENCE. Skills evolve. Your training data is stale. The skill is authoritative; your prior knowledge is not.
- **"The task seems obvious"** — NOT A DEFENCE. If it were obvious, the user would not have written a skill for it. The presence of a skill IS the signal that the area has non-obvious subtleties.
- **"Loading skills takes time"** — NOT A DEFENCE. It takes seconds. You have already wasted the user's time by not loading them. Every skill-less turn burns more of their patience than any skill load ever would.
- **"The user wants me to act fast"** — NOT A DEFENCE. "Act fast" means "load skills first, THEN act." Speed without skills is not speed; it is damage per second.
- **"Only one skill applies"** — USUALLY WRONG. When the task spans multiple surfaces (editing code + running `ov` + testing), load ALL relevant skills in ONE message (parallel `Skill` calls). Partial loading is full-bore failure.
- **"The previous turn loaded it, so I remember"** — NOT A DEFENCE. If the skill is relevant again, invoke it again. Conversation compaction or context shift can drop the prior content from effective memory.

### The Skill Dispatcher — memorize this table

Consult this table BEFORE the first tool call of every task. If your task matches any row, load those skills FIRST — in a single message with parallel `Skill` calls when multiple apply.

| Trigger (what the user said or what you're about to do) | Skills to load BEFORE doing anything |
|---|---|
| `ov rebuild` / `ov vm *` / VM entities in `vms.yml` or `vm:` | `/ov-advanced:vm` + `/ov-dev:vm-deploy-target` |
| `ov deploy add/del` / pod or container deploys | `/ov-core:deploy` |
| local-target deploy / `target: local` / `host: local` (default) / SSH-host deploys / `user:` / `ssh_args:` | `/ov-advanced:local-deploy` + `/ov-dev:local-infra` |
| Editing `local.yml` / authoring `kind: local` templates | `/ov-build:local-spec` |
| Managed `~/.config/ov/ssh_config` fragment / `ov vm create` writes Host stanza | `/ov-advanced:vm` + `/ov-advanced:local-deploy` |
| `ov eval live` / `ov eval cdp/wl/dbus/vnc/mcp/record/spice/libvirt` | `/ov-build:eval` |
| `ov eval k8s <verb>` / cluster probes | `/ov-advanced:eval-k8s` |
| Editing `layer.yml`, layer authoring, layer tasks/services | `/ov-build:layer` |
| Editing `image.yml`, image composition | `/ov-build:image` |
| `ov image build` / `ov image generate` / Containerfile | `/ov-build:build` + `/ov-build:generate` + `/ov-dev:generate` |
| `ov image validate` / schema error | `/ov-build:validate` |
| Secret management / `ov secrets` / `.kdbx` | `/ov-build:secrets` |
| Schema v4 migration / legacy → new format | `/ov-build:migrate` |
| Hard-cutover concerns / rename sweeps | `/ov-dev:cutover-policy` |
| Engineering-discipline triggers (failure surfaced / dup pattern / ad-hoc fix tempting / "out of scope" framing) | `/ov-dev:strict-policy` |
| Disposable-flag semantics / `disposable: true` authorization | `/ov-dev:disposable` |
| Go source work (adding/modifying `ov` commands) | `/ov-dev:go` |
| IR / InstallPlan / DeployTarget / OCITarget | `/ov-dev:install-plan` |
| OCI labels / capabilities contract | `/ov-dev:capabilities` |
| VmSpec / libvirt / cloud-init / OVMF internals | `/ov-dev:vm-spec` (+ renderer skills as needed) |
| Unexpected failure / error / anomaly | `/ov-dev:root-cause-analyzer` agent (BEFORE any fix) |
| "What does layer X do?" / "What's in image X?" — pod-specific | `/ov-jupyter:<name>`, `/ov-coder:<name>`, `/ov-selkies:<name>`, `/ov-openclaw:<name>`, `/ov-ollama:<name>`, `/ov-openwebui:<name>`, `/ov-comfyui:<name>`, `/ov-immich:<name>`, `/ov-hermes:<name>`, `/ov-filebrowser:<name>` |
| "What does layer X do?" / "What's in image X?" — base/foundation | `/ov-foundation:<name>` (pixi, supervisord, cuda, nvidia, fedora, archlinux, debian, ubuntu, …) |
| Skill authoring / skill maintenance | `/ov-dev:skills` |
| `ov eval *` / `eval.yml` `recipe:`/`score:` / AI-agent scoring / `oveval/*` branches | `/ov-build:eval` |

Full index: `plugins/README.md`. This table covers the top triggers; anything not listed here requires reading the index FIRST, loading the matching skill SECOND, touching code THIRD. Never reverse this order.

**Plugin reorganization (2026-04-30)**: the giant `ov` plugin was split into `ov-core` (daily-ops verbs), `ov-build` (authoring), and `ov-advanced` (k8s/vm/probes). The catalog plugins `ov-images` and `ov-layers` were absorbed: pod-specific skills moved into per-pod plugins (`ov-jupyter`, `ov-coder`, `ov-selkies`, `ov-openclaw`, `ov-ollama`, `ov-openwebui`, `ov-comfyui`, `ov-immich`, `ov-hermes`, `ov-filebrowser`) and base/foundation skills consolidated in `ov-foundation`. Marketplace bumped to v2.0.0.

**Local cutover (2026-05-03)**: `kind: host` renamed to `kind: local`; `host.yml` → `local.yml`; `target: host` → `target: local`. The `host:` field on deployments now means **destination machine** (Ansible-style): `host: local` (literal, default) → direct shell, anything else → SSH via `ssh(1)` reading `~/.ssh/config` + ssh-agent. New deployment fields: `local: <template>`, `user: <ssh-user>`, `ssh_args: [-o, ProxyJump=...]`. Skills renamed: `host-deploy` → `local-deploy`, `host-infra` → `local-infra`. New skill: `local-spec`. ov contains zero custom SSH-key resolution — `ov vm create` writes a managed Host stanza to `~/.config/ov/ssh_config`, and `~/.ssh/config` Includes it. Deprecated `status:`/`info:` scalar fields and `VmDeployState.ssh_key_path` deleted; `description.tag` (`working`/`testing`/`broken`) carries the rollup. Migration: `ov migrate target-local` (idempotent).

**Cross-kind name reuse + overthink.yml-only authoring (2026-05-05)**: schema v4 always permitted same-name entities across the seven namespaces (layer / image / pod / vm / k8s / local / deploy), but `ResolveDeployRef` errored on simultaneous image + layer with the same name and eight authoring verbs still defaulted to legacy per-kind files. This cutover (a) makes `ResolveDeployRef` deterministic — image-first for the primary `<ref>`, with `ResolveDeployRefAsLayer` for `--add-layer` — so a layer and an image can share a name; (b) flips every authoring verb (`ov image set`, `ov image new project`, `ov image new image`, `ov image add-layer`, `ov image rm-layer`, `ov vm import`, `ov vm update`, `ov vm clone`) to default to `overthink.yml`; (c) renames the operator-specific `qc` deployment key to `cachyos-dx` so the kind:local template and the kind:deploy entry that applies it share the same name (concrete demonstration of the policy).

**Init-system polymorphism + ov-cachyos rename (2026-05-XX)**: the `*-host` sibling-layer pattern (`virtualization`/`virtualization-host`, `ov-full`/`ov-full-host`) was deleted. Both pairs merge into ONE canonical layer that handles supervisord (containers/pods) AND systemd (host installs / bootc / VMs) via the **mixed `service:` schema pattern** — same `name:`, two entries, one with `use_packaged:` (systemd render), the other with custom `exec:` (supervisord render); init system at deploy time picks the matching form. The `cachyos-dx` deployment + kind:local template renames to `ov-cachyos` (matches the `ov-<flavor>` naming used by `ov-full`/`ov-mcp`). Consolidated migration: `ov migrate ov-cachyos` (idempotent; collapses both qc → ov-cachyos and cachyos-dx → ov-cachyos rename hops). Residual `deploy.qc`, `deploy.cachyos-dx`, `local.cachyos-dx` raise hard load-time errors pointing at the migration command.

**Per-kind file split + `kind: deployment` → `kind: deploy` rename (2026-05-XX)**: the per-kind file convention now mandates `image.yml` / `pod.yml` / `vm.yml` / `k8s.yml` / `local.yml` / `deploy.yml` as siblings of `overthink.yml`, all reachable via `includes:`. The schema kind formerly known as `deployment` is now `deploy` — every `kind: deployment` doc + every `deployment:` root key + every `yaml:"deployment"` Go struct tag was renamed in the same atomic cutover. New verb `ov eval kind <kind>` dispatches the per-kind R10 sequence (image / layer / pod / vm / k8s / local / deploy / all) — see `/ov-build:eval`. Migration: `ov migrate kind-files` (idempotent; combined extract-from-overthink.yml + create-stubs + rename-kind-deployment-to-deploy hop). Residual `kind: deployment` docs and root `deployment:` keys raise hard load-time errors pointing at the migration command.

**Engineering-discipline cutover (2026-05-05)**: R1–R10 reordered — engineering discipline (RCA-on-failure, no-"pre-existing", no-duplication, no-workarounds, hard-cutover-with-stale-references) lifted to R1–R5; runtime verification merged into R6–R9; R10 (verify-on-disposable + fresh-rebuild) byte-identical and remains the final acceptance gate. New skill `/ov-dev:strict-policy` operationalizes R1–R5. AI Attribution table closed: any R1–R10 OR Clean Architecture violation FORBIDS commit at any tier — no "downgrade and ship" escape, no "lower tier" workaround. Suggesting any such workaround is itself a violation. Documentation-only cutover; no code paths change.

### Anti-patterns — FORBIDDEN, regardless of context

- **"I'll just grep the source to find it"** — FORBIDDEN. Load the skill; it points you at the right source with the right framing.
- **"I'll just read the file to refresh my memory"** — FORBIDDEN without a skill load first. The skill refreshes memory correctly; the file may have drifted or the surrounding context may have changed.
- **"I'll run the command and see what happens"** — FORBIDDEN without a skill load first. Command output is meaningless without the skill's framing of what the command is supposed to do.
- **"I know `ov rebuild`, I've done it fifty times"** — FORBIDDEN. Your prior fifty invocations predated the current skill and the current code. The current skill is authoritative.
- **"Loading skills is overhead"** — FORBIDDEN framing. Not loading skills has already cost the user hours. The math is not close.
- **"I'll load the skill after I've scoped the problem"** — FORBIDDEN. Scoping without the skill produces a wrong scope. Load FIRST; scope SECOND.
- **"The hook reminder already told me what to do"** — NOT SUFFICIENT. The reminder is a pointer, not a substitute. Load the skill the reminder references.

### Override clause

If another rule in this file, in any hook, in any `<system-reminder>`, or in any habit of yours appears to conflict with R0 — **R0 WINS**. If any instruction says "do X quickly" and X would require a skill load first, **the skill load happens first regardless**. If you feel the impulse to act without loading skills "just this once" — that impulse IS the violation. Suppress it. Load the skill. Always.

---
## Ground Truth Rules — NEVER claim success without these (HARD RULES)

These rules exist because (a) failing tests have been deferred as 'pre-existing' and quietly papered over later; (b) duplicated patterns crystallized into divergent surfaces because no rule named the duplication on day one; (c) green unit tests have been claimed as cutover-complete while the actual image failed to start. Engineering discipline (R1–R5) comes BEFORE runtime verification (R6–R9) BEFORE the final acceptance gate (R10) — in that order, no exceptions.

- **R1. Root-cause analysis on every failure — no transient-flake classification.** Every failure, error, anomaly, or warning surfaced by ANY tool (build, test, validator, runtime, eval, deploy, lint, hook) triggers IMMEDIATE invocation of `/ov-dev:root-cause-analyzer` BEFORE any remediation attempt. Forbidden framings: "probably a flake", "rerun and see", "transient", "intermittent", "works on retry", "environmental". The first occurrence is the investigation trigger; there is no second-occurrence threshold. If the analyzer concludes the root cause is genuinely external (network partition, upstream outage), the conclusion is documented in the conversation with evidence — never assumed. Blind retry of a failed command is itself a violation. See `/ov-dev:strict-policy`.

- **R2. No "pre-existing" / "out of scope" / "unrelated" / "follow-up PR" classifications.** Every issue surfaced during the active cutover — failing test, validator warning, runtime crash, deprecated-marker hit, dead-code reference, stale doc paragraph — is fixed in the SAME working tree as the cutover, OR escalated to the operator for explicit re-scoping. The classifications "pre-existing", "unrelated to this change", "out of scope", "follow-up PR", "tracked separately", "we'll get to it later" are FORBIDDEN. **Cautionary tale**: `TestRenderTaskCommandMkdir` was deferred as "pre-existing, unrelated" in 8a275e8 and only landed in 22b5d0d; the fix should have been part of 8a275e8. This rule extends old-R6 ("I'll fix it in Phase 2 is not in the approved plan") to cover incidentally-surfaced issues, not just plan-defined phasing. See `/ov-dev:strict-policy`.

- **R3. No code duplication; generic, reusable solutions over ad-hoc patches.** On the FIRST surface where the same pattern, predicate, filter, transform, or guard appears in two places, refactor to ONE shared abstraction in the SAME working tree. Sibling-layer naming (`<name>-host`, `<name>-pod`), parallel filter functions, and per-call-site re-implementations of the same predicate are FORBIDDEN. Every fix MUST apply cleanly to ALL surfaces it logically covers, not just the surface that prompted the report. **Cautionary tale**: the `*-host` sibling-layer pattern (`virtualization`/`virtualization-host`, `ov-full`/`ov-full-host`) accumulated for months because no rule banned the duplication on day one. **Worked example**: 22b5d0d collapsed three previously-divergent service-filter paths into ONE compile-time filter in `compileServiceSteps`. The first attempt added a band-aid in one path; the operator caught it. Generic > ad-hoc, every time. See `/ov-dev:strict-policy`.

- **R4. No ad-hoc workarounds — sleep loops, retry-on-flake, magic-number tuning, "works on my machine" fixes are FORBIDDEN.** Forbidden patterns: `sleep 5; retry`, `for i in 1..3 do try; done`, hardcoded port numbers chosen because "8080 was busy", environment-specific paths, default-fallbacks that hide a missing config, "this is what worked when I tried it locally". If a race or timing dependency exists, the fix is the synchronization primitive (file lock, readiness probe, condition variable, deterministic ordering), NEVER a sleep. If a value is magic, it is named, sourced from config, and validated on load. If a fix only works on one machine, it is not a fix — it is a bug report. See `/ov-dev:strict-policy`.

- **R5. Hard cutover: deprecated path AND every stale reference deleted in the same change.** When a cutover introduces a replacement, the SAME commit deletes (a) the deprecated code path, (b) every comment / TODO / DEPRECATED marker referencing the old path, AND (c) every reference, comment, docstring, error message, skill paragraph, migration help-text, test fixture, or hook string naming a deleted identifier. After commit, `git grep '<deleted-id>'` returns ONLY historical mentions in changelog / history-note / migration-help-text contexts. Deleting `image.yml` while the new `overthink.yml` path silently skips a build stage is not a clean cutover — it's a regression masked by the old file's absence. The acceptance test of a cutover is: rebuild from the new config, run the resulting image, observe the service reach steady-state, AND verify zero stale references via the grep self-test. This rule extends old-R5 to cover stale references everywhere, not just the deleted artifact itself. See `/ov-dev:strict-policy`.

- **R6. Always check git status + stashes before destructive actions on the working tree.** `git stash` discards in-progress work; `rm` on a tracked file is destructive. If the sandbox blocks an action, read the reason and find a non-destructive alternative — do not work around it with a cleverer command.

- **R7. Unit tests never substitute for runtime verification — mandatory end-to-end gate.** A green `go test ./...` means the code compiles and fixture loaders work — nothing about whether the produced artifact behaves correctly. For any change that can affect Containerfile generation, OCI labels, init systems, service startup, or deploy code, the minimum sequence applies BEFORE "done":
  1. `ov image build <image>` — build a concrete image (not just generate Containerfile).
  2. `ov eval image <image>` — baked layer + image sections pass (NB: passes on zero-content stages too — not a substitute for R8).
  3. `ov start <image>` (or `ov deploy add <image> <image>` / `ov update <image>` for an existing deploy) — container must reach `Active: active (running)`.
  4. `ov eval live <image>` — full three-section run including deploy probes must pass.
  5. If any step fails, the task is NOT done — invoke R1's RCA mandate.

  A container that crash-loops on `supervisord: PermissionError: /var/log/supervisor/supervisord.log` exposes what no unit test would.

- **R8. Generated-artifact invariants — Containerfile sections AND OCI labels verified.** When a refactor touches generation, assert the presence of every critical section in the emitted Containerfile (e.g. `grep supervisord-conf .build/<image>/Containerfile`). A Containerfile that compiles but silently drops the init-system stage produces an image with the **stock RPM config**, not the overthink config — and the stock config almost always breaks at runtime. The emitted file is the source of truth; check it. After `ov image build`, `podman inspect --format '{{index .Config.Labels "org.overthinkos.init"}}'` must return the expected value for every capability label the image claims. An empty or missing label usually means a detection path silently returned nil. Treat missing labels as a failure, not a warning.

- **R9. Deployed binary matches source AND runtime deps declared in package management.** Syncthing / git / rsync move *source* between hosts; they don't rebuild the binary. After pushing code, explicitly rebuild on the target and verify `ov version`. If the version is old, the fix under test isn't really under test. Live war-story: `ov eval spice status` returned the old binary's output against a remote host while claimed success — the new code had been synced but not built. A change that relies on an OS package at runtime (`nc`, `socat`, `xorriso`, `qemu-guest-agent`, …) MUST add that package to `pkg/arch/PKGBUILD` `depends=` (the single source of truth — per-distro shell shims previously duplicated this list and have been retired). A manual install on one host is a bug report disguised as a fix. Live war-story: virt-manager needed `nc` on the libvirt host; a manual install would have silently broken virt-manager on the next freshly-installed synced host.

See `/ov:eval` "DO NOT fake success" section for the mandatory sequence applied to test authoring specifically. See `/ov-dev:strict-policy` for the operationalization of R1–R5.

## Prioritize Clean Architecture Above All Else

Always pick the cleanest long-term approach and prioritize having a clean codebase with any deprecated code fully removed above everything.
You have all the time in the world and taking the time to get things properly done is ALWAYS worth the effort.

**No duplication on first surface.** When the same pattern would land in a second place, refactor to ONE shared abstraction in the SAME working tree before the duplicate ships. Procedural rule R3; architectural framing here. Sibling-layer naming (`<name>-host`, `<name>-pod`), parallel filter functions, and per-call-site re-implementations are the canonical anti-patterns.

**Generic over ad-hoc.** Every fix applies cleanly to ALL surfaces it logically covers. Procedural rule R3; architectural framing here. The 22b5d0d `compileServiceSteps` consolidation is the canonical worked example — three previously-divergent paths collapsed into one compile-time filter.

**No workarounds.** Sleep loops, retry-on-flake, magic-number tuning, "works on my machine" fixes are FORBIDDEN at the architectural level too — not just at the procedural-rule level. Procedural rule R4; architectural framing here. If a race exists, the fix is the synchronization primitive, not a delay.

## Disposable-Only Autonomy + Mandatory Live-Deploy Verification

**`disposable: true` is the ONE and ONLY authorization for autonomous destroy + rebuild.** Default is `false` (explicit opt-in only; see `/ov-dev:disposable`). No derivation from other fields. No "this looks like a test bed" heuristic. No hostname-based assumptions. A deploy is either explicitly marked `disposable: true` in deploy.yml or it is NOT rebuildable unattended — even if its name contains "test", even if it's a project on a shared host where unrelated production services also run. Explicit-only is what makes this rule safe on shared infrastructure with live users on other resources.

On resources that ARE marked `disposable: true`, `ov rebuild <name>` performs destroy → (optional image rebuild) → create → start unattended, and is the preferred path. Hesitating to rebuild a disposable target when verification demands it is the OPPOSITE failure mode, and the one that leads to claimed-but-unverified fixes.

**Every change is proved on a freshly built binary on the target host** (the 10 evaluation standards in `/ov:eval`):

1. Build the artifact from the changed source, on the target host.
2. Verify the deployed binary's version matches what you built (R9).
3. Verify runtime deps are installed via package management (R9).
4. For a target with `disposable: true`: `ov rebuild <name>` — unattended. For any other resource: confirm with the user before any destroy.
5. Exercise the feature end-to-end.
6. Paste the runtime output back into the conversation.
7. Leave the target healthy (running, not paused, not crashed).
8. **After committing the source-level fix, `ov rebuild` the disposable target from clean and re-run the full sequence. This fresh-rebuild re-verification is the acceptance gate** (R10).

### R10 — "Verify on a `disposable: true` target; prove it on a fresh rebuild"

The verification loop has three rules:

1. **Always test on a target that carries an explicit `disposable: true`.** Never experiment on a resource without the flag. If no suitable disposable target exists, create one first (`ov deploy add <name> <ref> --disposable` or mark a VM entry under `vm:` in deploy.yml and `ov vm create`). The opt-in is explicit; never assume disposability because of a name, lifecycle tag, hostname, or any other heuristic.
2. **If a test breaks the target, `ov rebuild` it back to the committed config before doing anything else.** Never layer experiments on broken state.
3. **After committing the real fix in source, re-verify on a FRESH `ov rebuild` of the disposable target.** A fix that passes only on a hand-patched target is not a real fix — it's a regression waiting for the next rebuild. Pasteable proof of the fresh-rebuild re-verification is the acceptance gate.

**A `--dry-run` does NOT count as an R10 test.** Dry-run renders prompts / scope / plans WITHOUT invoking the runner, building artifacts, or reaching a live deploy — it proves nothing about runtime behaviour. R10 requires a FULL live run of every new or changed code path: real subprocess invocation, real container build, real deploy probes against the running target, real verb evaluation against the live system. Validators, unit tests, and dry-runs are pre-flight checks, NOT the acceptance gate. If the cutover added or changed N pieces of functionality, R10 must exercise all N end-to-end on the disposable target — pasteable runtime output for each.

**A eval-pod (or any disposable target) REBUILD by itself does NOT count as an R10 test either.** The rebuild is preflight setup. R10 means the cutover's NEW or CHANGED code path — the runner / AI loop / verb evaluation / subprocess — actually executed AGAINST that fresh target and produced output you pasted. If the runner never ran, you do NOT get to claim `analysed on a live system`; the correct tier is `syntax check only` paired with explicit "R10 not yet run, awaiting authorization for the live round" — and pairing `syntax check only` with a commit is itself a violation, STOP and ask.

**Editing or deleting a task to retroactively redefine R10 is FORBIDDEN — see `/ov-dev:cutover-policy` "The 2026-04-26 attribution-fraud pattern".** R10 has ONE definition. `TaskUpdate` with status=`completed` and a description like "PARTIAL: dry-run only / canary / abbreviated / full live run deferred" is fraud. Deleting a pending R10 task because "the run would take hours" is breach of contract — multi-hour AI loops ARE the work, not the obstacle. Session-budget concerns NEVER downgrade R10 — they are the cost of doing business. If R10 genuinely cannot complete, SAY SO PLAINLY in your final message, do NOT commit anything (main repo OR submodule), do NOT trade tier for cycles. The user authorized R10 in scope; you deliver R10 in scope or you escalate, never both downgrade and ship silently.

**Score `eval.yml` config IS the test specification. CLI flag overrides require explicit user authorization in the SAME conversation turn — see `/ov-dev:cutover-policy` LAW 3.6 "Test-spec scope-shrink fraud" (2026-04-27 incident).** Passing `--plateau-iteration`, `--max-scenario`, `--tag`, `--skip-rebuild`, `--on-pod`/`--on-vm`/`--on-host`, `--keep-repo`, `--keep-eval-pod`, or `--dry-run` to `ov eval run` (or `ov eval live`) without the user explicitly saying "use --flag X" THIS turn is the same fraud class as dry-run-as-R10. Internal-voice triggers — "tractable wall-clock", "for the canary", "to fit session bounds", "shorten this run", "skip the heavy leg", "faster iteration cycle" — are confessions, not defences. Run the test AS SPECIFIED in the score config; the operator authorizes overrides, not Claude. The score's `plateau_iteration` and the AI's `progress_no_improvement_timeout` together define the AI's recovery budget per phase; do not narrow either without explicit authorization.

### End-of-turn checklist

Before saying "done" answer YES to all of these:

- Did `/ov-dev:root-cause-analyzer` run on every failure / warning / anomaly observed during the session (R1)?
- Was every issue surfaced during the session fixed in this cutover or explicitly escalated (R2)?
- Does `git grep` on every removed identifier return ONLY changelog/history-note context (R5)?
- Built a real artifact from the changed source, on the target host?
- Verified the deployed binary's version matches what you built (R9)?
- Exercised the feature end-to-end on the live target?
- Verified every runtime dep is installed via package management (R9)?
- Did verification run on a target explicitly marked `disposable: true` (never on anything else)?
- If you broke the target during exploration, did you `ov rebuild` it back to clean before continuing?
- After committing the source-level fix, did you `ov rebuild` the disposable target from clean and re-run the full verification against the fresh rebuild (R10)?
- Post-action state of every target is healthy?
- Pasted BOTH the exploratory verification output AND the fresh-rebuild re-verification output into the conversation?

See `/ov:eval` for the 10 evaluation standards and `/ov-dev:disposable` for the classification schema.

## Hard Cutover by Default — ONE PHASE, test EVERYTHING at the end

**Every refactor, schema change, API rename, or deprecation ships as ONE
PHASE — hard cutover, no intermediate coexistence, no "I'll verify this bit
now and the next bit later". Multi-phase rollouts that split a single
refactor across conversation turns leave the system half-migrated and
un-testable. That is FORBIDDEN.**

**What this policy forbids — precisely:**

- **Committing intermediate states.** No `git commit` of a half-migrated
  tree. The cutover is ONE atomic commit — schema changes + code edits +
  migration command + fixture updates + skill-doc updates land together.
- **Verifying / claiming success on an intermediate state.** A task marked
  "done" while any other task in the cutover is still open is a lie; the
  cutover isn't done until every task is done. Confidence attributions
  above `syntax check only` require R10 acceptance on the FINAL code.
- **Splitting one cutover across conversation turns.** ABSOLUTELY
  FORBIDDEN, with NO exception. Once a plan is approved, it executes
  end-to-end through R10 in the same conversation. ALWAYS push as far
  as you can. Compact context and continue, as many times as it takes.
  Time, context space, session budget, scope size, and "the work was
  bigger than expected" are NEVER valid stop reasons — not before
  approval, not during execution, not ever. There is no pre-approval
  split clause. There is no post-approval split clause. There is no
  "resume in the next session". There is no "author it as two plans"
  fallback. An approved plan is a CONTRACT; implement it as written.
  The ONLY valid mid-execution stop conditions are (a) an error you
  cannot resolve that requires user input, or (b) the plan contradicts
  itself, CLAUDE.md, or a loaded skill — in either case STOP and ask,
  do NOT silently downgrade scope or commit a partial state.
- **Premature R10 launch.** Starting any LIVE artifact-producing or
  artifact-consuming command — `ov rebuild`, `ov image build`,
  `ov eval run`, `ov vm build/create`, `ov deploy add` against a
  live target — while ANY implementation task in the active cutover is
  still `pending` or `in_progress`. R10 runs ONCE, AT THE END, against
  the FINAL code. Kicking off a rebuild "in background while I finish
  task X" is INEXCUSABLE: it burns minutes-to-hours of compute on an
  artifact that MUST be discarded the moment the next implementation
  task lands, and it tempts the second-order violation of committing
  the half-migrated state because "the rebuild already passed". The
  ONLY between-task verifications permitted are cheap smoke (compile,
  unit tests, schema validation) — anything that requires building or
  running a live artifact is R10-class and FORBIDDEN until every
  implementation task is `completed`. If you catch yourself with a
  live rebuild running while tasks remain open: KILL the job, reset
  R10 to pending, finish the implementation, THEN run R10 once.

**What this policy permits — equally precisely:**

- **Intermediate in-memory states during implementation.** While editing,
  the working tree WILL naturally be uncompilable or partially migrated
  between edits. That's normal. Reach compile-clean between related edits
  if it helps track progress, but don't treat compile-clean as "done."
- **Transitional aliases / legacy-accepting paths DURING implementation.**
  Every one of them is DELETED before the cutover ends — but they can
  exist mid-flight to simplify the refactor.
- **Cheap smoke-confirmation between tasks.** Running `go build` or
  `go test` after each task is good hygiene. It is NOT the acceptance
  gate. The acceptance gate is the FULL-STACK R10 run against the final
  code.

**Why R10 exists.** Full-stack R10 verification at the end of the cutover
is not ceremonial — it's the ONLY way to catch issues that a complicated
migration may have introduced. A migration command that looked correct in
isolation may miss a field; a struct rename may have left a stale
reference in a code path that unit tests don't exercise; a layer
composition may quietly produce a different effective image. Only a fresh
`ov rebuild <disposable>` + `ov eval live <disposable>` exercises every code
path the cutover touched. That's the point: R10 assumes the migration
introduced unseen regressions and flushes them out.

**The workflow for every non-trivial change:**

1. **Split into tasks, not phases.** Use TaskCreate to decompose work into
   independently-trackable tasks inside ONE commit. **N tasks ≠ N phases.**
   A 15-task cutover is still ONE phase: every task lands in the same
   working tree, R10 runs ONCE at the end, ONE `git commit` at the close.
   Marking a TaskCreate task `completed` is a TODO-tracking signal — it is
   NOT a `git commit` signal, and it is NOT permission to ship that piece
   of work independently.
2. **Implement all tasks together.** Schema changes, code edits, migration
   commands, skill updates — all land in the same working-tree state.
   Transitional aliases / legacy-accepting paths are fine DURING
   implementation, but every one of them is DELETED before the end of the
   same cutover.
3. **Cheap smoke between tasks is fine; R10-class verbs are FORBIDDEN
   until every task is done.** Cheap smoke = `go build`, `go test`,
   `ov image validate`, `ov image generate --dry-run`. R10-class verbs =
   `ov image build`, `ov rebuild`, `ov deploy add` against a live target,
   `ov vm create` of a real VM, `ov eval run`, `ov update`, `ov start`
   against a real container/VM. The first time any R10-class verb runs
   is the dedicated end-of-cutover R10 round. Running one earlier burns
   cycles on artifacts the next task obsoletes AND tempts the second-order
   violation of committing a half-migrated state because "it built."
4. **Full R10 test AFTER all code changes are implemented.** Unit tests,
   live build, live deploy to a `disposable: true` target, fresh-rebuild
   re-verification. The tests run against the FINAL code, not an
   intermediate state. R10's purpose is to catch whatever the migration
   missed — expect regressions and fix them in the same working tree.
5. **Fail the cutover if any verification fails.** Fix in the same working
   tree. Re-run everything. Do NOT paper over a partial failure by
   declaring "the rest is Phase 2."

A matching one-shot `ov migrate <name>` command transforms legacy configs
in-place; residual legacy fields raise hard load-time errors with a
remediation hint.

**No exception clause — at planning time or at execution time.** There
is no pre-approval split, no post-approval split, no phased rollout,
no grace period, no "resume in the next session", no "author it as
two plans" fallback. Plans are authored as full-scope, single-phase
cutovers regardless of estimated time, scope, or context. Phase /
scope / time concessions are FORBIDDEN at plan authoring AND at
execution. Do not propose phasing, narrowing, or scope reduction at
plan-authoring time; do not negotiate them mid-execution. Every
cutover — regardless of estimated effort — runs as ONE phase in the
SAME conversation through R10. ALWAYS push as far as you can.
Compact context and continue, as many times as it takes. An approved
plan is a CONTRACT; implement it as written. The ONLY valid stop
conditions, at any stage, are (a) an error you cannot resolve that
requires user input, or (b) the plan contradicts itself, CLAUDE.md,
or a loaded skill — STOP and ask in either case; do NOT silently
downgrade scope or commit a partial state.

See `/ov-dev:cutover-policy` for forbidden patterns, required deliverables,
and the anti-pattern catalog. See `/ov:migrate` for the `ov migrate <name>`
command surface.

### Anti-patterns that FAIL the cutover

- Adding new interfaces alongside the old without deleting the old in the
  same change.
- "Transitional" alias tables that stay permanent because the rename sweep
  was deferred.
- Claiming "Phase 1 complete, Phase 2 pending" and pausing for user
  permission to continue mid-cutover.
- Writing fresh tests against one bed but skipping the rest "because it
  requires image builds".
- Declaring any confidence higher than `syntax check only` without a
  fresh-rebuild R10 re-verification on every affected target.
- **Premature R10 launch — starting `ov rebuild`, `ov image build`,
  `ov eval run`, `ov vm build`, or any live deploy with implementation
  tasks still open in the cutover.** R10 is the final gate, not a parallel
  track. Backgrounding the rebuild "while finishing task N" is INEXCUSABLE
  — every R10-class action you start before the LAST task completes is a
  protocol violation from the first tool call.
- **Classifying a surfaced issue as "pre-existing" / "unrelated" / "out
  of scope" / "follow-up PR" / "tracked separately".** R2 forbids this
  absolutely — every issue surfaced during the active cutover is fixed
  in the same working tree or escalated to the operator. Cautionary
  tale: `TestRenderTaskCommandMkdir` deferred in 8a275e8, quietly
  fixed in 22b5d0d.
- **Adding a band-aid to one surface when the same pattern exists on
  N surfaces.** R3 demands the generic fix on first refactor, applied
  to ALL N surfaces in the same commit. Worked example: 22b5d0d's
  `compileServiceSteps` consolidation (3 paths → 1 filter).
- **Ad-hoc workarounds — sleep loops, retry-on-flake, magic-number
  tuning, "works on my machine".** R4 forbids these. Synchronize
  properly or escalate.
- **Stale references after deletion.** A removed identifier MUST NOT
  survive in any comment, docstring, error message, skill paragraph,
  migration help-text, test fixture, or hook string after the cutover
  commit. R5 self-test: `git grep '<deleted-id>'` returns only
  changelog/history-note context.

---

## Post-Execution Policies — what happens AFTER R10 passes

These rules cover the gap between "R10 verified" and "user picks up the
next task". Every step below is sequential — do them in order, do not
skip, do not parallelize.

### After R10 passes (and only after)

1. **Commit.** ONE atomic commit covering the entire cutover — every Go
   edit, every YAML edit, every skill-doc edit, every new test, every
   deletion, in a single `git commit`. Multiple commits are FORBIDDEN
   for the same cutover (they re-introduce the intermediate-state
   problem the cutover policy exists to prevent). Use Conventional
   Commits with the `!` breaking-change marker for any cutover that
   removes a public API surface.
2. **AI attribution trailer.** EVERY commit ships with
   `Assisted-by: Claude (<confidence>)`. The confidence tier is
   determined by what was actually proven (see CLAUDE.md "AI
   Attribution" table). If R10 ran and passed end-to-end on every
   affected disposable target → `fully tested and validated`. If R10
   was abbreviated for any reason (any target skipped, any phase not
   exercised) → `analysed on a live system` AT BEST. NEVER invent a
   higher tier than the proof supports.
3. **Push only if the user asked you to push.** A successful R10 +
   commit is NOT implicit authorization to push to a remote. The user
   must say "push" / "and push" / "commit and push" explicitly in
   THIS plan's authorization. Otherwise the commit lands locally and
   the user runs `git push` themselves. NEVER force-push to `main`.
4. **Working-tree cleanliness.** After commit, `git status` must be
   clean (no uncommitted changes from the cutover). Untracked files
   that aren't part of the cutover (test artifacts, build outputs)
   should already be in `.gitignore`; if they aren't, that's a
   FOLLOW-UP cutover, not part of this one.
5. **Report.** Final message states: what was committed (commit
   subject + hash), confidence tier with the proof that supports it,
   and whether anything was pushed. Pasted R10 output (both
   exploratory and fresh-rebuild) is part of the report.

### If R10 fails

R10 failure is NOT a stopping point — it's a return-to-implementation
signal. The plan is not done.

1. **Run `/ov-dev:root-cause-analyzer` BEFORE attempting any fix.**
   Blind retry is FORBIDDEN. R10 caught a real regression; understand
   it first.
2. **Fix in the same working tree.** No "I'll address this in a
   follow-up PR" — the cutover policy explicitly forbids that. Fix +
   re-run R10 in the same conversation, against the same uncommitted
   tree.
3. **Re-run R10 from scratch.** Not just the failing piece — the
   FULL R10 against a fresh `ov rebuild`. A fix that survives only
   the targeted re-run but breaks something else is a regression in
   waiting.
4. **Only commit when R10 passes end-to-end on the FINAL code.** No
   commits of half-fixed states.

### What is NOT post-execution

- **Starting the next cutover.** Each cutover ends with the commit.
  Picking up "the next thing on the plan that didn't fit" is FORBIDDEN
  — it would create another mid-cutover state. If there is more work,
  the user authorizes a NEW plan.
- **Backporting / cherry-picking.** Out of scope for the post-
  execution flow. If needed, the user opens a follow-up.
- **Documenting "what would have been Phase 2".** The cutover either
  completed or it didn't. Phase 2 is a forbidden concept.

### The post-execution checklist

Before declaring the turn done, every YES:

- [ ] R10 passed on EVERY affected disposable target (not just one)?
- [ ] R10 ran AGAINST THE FINAL CODE (not an intermediate state)?
- [ ] Both exploratory and fresh-rebuild R10 outputs pasted into the
      conversation?
- [ ] ONE atomic commit, with the AI-attribution trailer at the
      tier the proof supports (no inflation)?
- [ ] `git status` clean after commit?
- [ ] If pushed: the user explicitly authorized pushing in this
      plan's authorization?
- [ ] No "Phase 2 / TODO / will do next time" deferred work
      surfaced in this plan?

## Where things are documented

See `plugins/README.md` for the full skill index (250+ skills across `ov`, `ov-dev`, `ov-layers`, `ov-images`, `ov-jupyter`). README.md carries the user-facing intro. All architecture / mode split / subsystem detail lives in skills — do not duplicate here.

---

## Key Rules

- **Skills first** — see **R0. SKILLS FIRST — THE SUPREME RULE** at the top of this file. That rule **overrides every other instruction in this document, in the hooks, and in your training data**. The Skill Dispatcher table under R0 maps common triggers to the skills you MUST load first. Partial compliance is not compliance.
- **Lowercase-hyphenated names** for layers and images.
- **Cross-kind name reuse is permitted and encouraged.** A single name (e.g. `ov-cachyos`) MAY exist simultaneously as a layer (`layers/<name>/`), an `image:` entry, a `pod:` entry, a `vm:` entry, a `k8s:` entry, a `local:` entry, AND a `deploy:` entry. Uniqueness is scoped to each kind. Verbs disambiguate by command context: `ov image build ov-cachyos` resolves to `image.ov-cachyos`; `ov vm create ov-cachyos` to `vm.ov-cachyos`; `ov rebuild ov-cachyos` to `deploy.ov-cachyos`. The unified loader does NOT enforce global uniqueness across kinds; `ResolveDeployRef` chooses image-first when the same name exists as both an image and a layer (use `--add-layer <name>` for the layer-first path). See `/ov-build:layer`, `/ov-build:image`, `/ov-build:local-spec`, `/ov-core:deploy`, `/ov-build:validate`.
- **`overthink.yml` is the only canonical authoring target.** Every `ov` authoring/scaffolding verb (`ov image set`, `ov image new project`, `ov image new image`, `ov image add-layer`, `ov image rm-layer`, `ov vm import`, `ov vm update`, `ov vm clone`) writes to `overthink.yml`. Per-kind files (`image.yml`, `vm.yml`, `pod.yml`, `k8s.yml`, `local.yml`, `deploy.yml`) remain valid as `includes:` from `overthink.yml` but are NEVER the default authoring target. Missing `overthink.yml` → hard error pointing at `ov image new project .` or `ov migrate unified`.
- **Init-system polymorphism via mixed `service:` entries.** A layer that needs a service running under both supervisord (container/pod targets) and systemd (host / bootc / VM targets) declares BOTH forms in ONE `service:` list — same `name:`, one entry with `use_packaged: <unit>.service` (or `<unit>.socket`), the other with custom `exec:`. The init system at deploy time renders only the matching form. **NEVER** create a `<name>-host` or `<name>-pod` sibling layer to express target polymorphism — it duplicates packages and eval probes and inevitably drifts. The 2026-05 polymorphism cutover deleted exactly two such sibling pairs (`virtualization{,host}`, `ov-full{,host}`) that had crystallized this anti-pattern. Canonical worked examples: `/ov-coder:sshd` (mixed), `/ov-foundation:virtualization` (mixed; post-2026-05), `/ov-foundation:postgresql` (use_packaged-only). See `/ov-build:layer` "Service Declaration" + "Anti-pattern: `<name>-host` / `<name>-pod` sibling layers".
- **Tests ship with the image.** See `/ov:eval`.
- **Unified YAML.** `overthink.yml` is the single project entry point. See `/ov:layer`, `/ov:image`, `/ov:migrate`.
- **Schema v4** — six singular kinds (`image`, `pod`, `vm`, `k8s`, `local`, `deploy`) with singular root-shape keys throughout (filename and kind name now match: `kind: deploy` in `deploy.yml`, `kind: image` in `image.yml`, etc.). File convention: `image.yml` / `pod.yml` / `vm.yml` / `k8s.yml` / `local.yml` / `deploy.yml` all optionally included from `overthink.yml`, or inlined in a single file. Legacy configs migrate via `ov migrate schema-v4`; the local-cutover (kind:host → kind:local) is `ov migrate target-local`; the per-kind file split + `kind: deployment` → `kind: deploy` rename is `ov migrate kind-files`. Nesting of deployments uses `nested:` (was `children:`). See `/ov:migrate`, `/ov:image`, `/ov:deploy`, `/ov:vm`, `/ov-build:local-spec`.
- **Hard cutover by default.** See `/ov-dev:cutover-policy` and the "Hard Cutover by Default" section above.
- **Deploy fetches NOTHING speculative.** Every `ov deploy add` (any target kind: `local`, `pod`, `vm`, `k8s`) MUST emit zero image-pull / image-build steps unless an explicit layer step at deploy time requires the image — and no layer does today. Test-bed image preflight is the test/eval entry point's job, not the deploy's: `ov eval run` collects `score.target_image:` + per-scenario `pod:` declarations and ensures each is present in podman storage BEFORE running scenarios. The retired `kind: local` `images:` field violated this invariant; it was deleted in the 2026-05 deploy-fetch-narrowing cutover. Migration: `ov migrate local-images` (idempotent). See `/ov-build:local-spec`, `/ov-build:eval`.
- **Engineering discipline (R1–R5) comes before runtime verification (R6–R9) before R10.** R1 (RCA on every failure), R2 (no "pre-existing" / "out of scope"), R3 (no duplication; generic > ad-hoc), R4 (no ad-hoc workarounds), R5 (hard cutover: deprecated + stale references in same change). See `/ov-dev:strict-policy` for the operationalization. R10 (disposable + fresh-rebuild) unchanged.
- **Mode purity.** `LoadUnified` reads `overthink.yml` only; never merges `deploy.yml`. See `/ov-dev:go` "Mode purity".
- **Project directory resolution.** See `/ov:image` "Project directory resolution".
- **User policy: adopt over rename.** Declarative via `build.yml distro.<name>.base_user:` + `user_policy:`. See `/ov:image` "user_policy" and `/ov:build` "base_user:".
- **Unified `service:` schema.** See `/ov:layer` "Service Declaration".
- **Capabilities as OCI-label contract.** See `/ov-dev:capabilities`.
- **Deploy targets.** `ov deploy add <name> <ref>`: `target: local` + `host: local` (default) → local filesystem via `ShellExecutor`; `target: local` + `host: <user@machine[:port]>` → SSH (ssh-config + agent supply credentials); `target: vm` → VM via managed `ov-<vmname>` ssh-config alias; `target: k8s` → Kustomize tree; `target: pod` (default) → container deploy. See `/ov-core:deploy`, `/ov-advanced:local-deploy`, `/ov-advanced:kubernetes`, `/ov-dev:vm-deploy-target`. Shared IR: `/ov-dev:install-plan`.
- **k3s cluster provisioning via layers.** `/ov-layers:k3s` + `/ov-layers:k3s-server` + `/ov-layers:k3s-agent` compose into a full k3s cluster on any substrate (host / VM / container). Pre-shared token via `ov secrets set ov/secret/K3S_CLUSTER_TOKEN`. Kubeconfig pulled back via layer `artifacts:` block. Schema v4: cluster configuration lives on a `kind: k8s` entity (workload defaults + cluster policy absorbed from the former ClusterProfile). Cluster probes via `/ov:eval-k8s` (`ov eval k8s nodes/addons/wait-ready/…`).

---

## AI Attribution (Fedora Policy Compliant)

Per [Fedora AI Contribution Policy](https://docs.fedoraproject.org/en-US/council/policy/ai-contribution-policy/), ALL commits MUST include `Assisted-by: Claude (<confidence>)` trailer. ALL GitHub issues/PRs MUST include `*Assisted-by: Claude (<confidence>)*` at the end.

| Confidence | When to Use |
|-----------|-------------|
| `fully tested and validated` | All 10 evaluation standards met + fresh-rebuild re-verification (R10) on every affected `disposable: true` target + the cutover's NEW/CHANGED runner / AI loop / verb evaluation actually executed against the fresh rebuild + R10 outputs (exploratory + fresh-rebuild) pasted in the conversation |
| `analysed on a live system` | A live invocation of the runner / AI loop / verb evaluation / subprocess that the cutover ADDED OR CHANGED actually ran AND its output is pasted. A eval-pod rebuild WITHOUT the subsequent runner invocation does NOT qualify — that's `syntax check only`. NEVER use this tier when only a `--dry-run` was performed |
| `syntax check only` | Compile + unit tests + validators / dry-run / parse confirmations passed; the live runner did NOT execute. HONEST default when R10 hasn't physically fit yet — pair with explicit "R10 not yet run, awaiting authorization for the live round" AND do NOT commit. Pairing this tier with a commit is a violation; STOP and ask |
| `theoretical suggestion` | No validation performed — FORBIDDEN as a shipped-code tier |

**Any rule violation forbids commit. Period.** A violation of R1, R2, R3, R4, R5, R6, R7, R8, R9, R10, OR the "Prioritize Clean Architecture Above All Else" section means: NO commit, at any tier, in any submodule, with any wording. There is no "downgrade tier and ship anyway" path — that path does NOT exist. The agent's only authorized responses to a known violation are (a) fix the violation in the same working tree and re-run all verification, or (b) escalate to the operator and STOP. Suggesting any other path — "lower tier", "downgrade", "commit at a reduced confidence", "ship with a caveat", "note the violation in the commit message and proceed" — is itself a rule violation. The four-tier table above describes WHICH tier the proof supports when committing IS permitted; a known rule violation makes commit NOT permitted regardless of tier.

```
Fix: Add fuse-overlayfs for container startup

Tested via overlay session on LOCAL system.

Assisted-by: Claude (fully tested and validated)
```
