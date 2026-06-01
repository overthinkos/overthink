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
| `ov update` / `ov vm *` / VM entities in `vm.yml` or `vm:` | `/ov-vm:vm` + `/ov-internals:vm-deploy-target` |
| `ov deploy add/del` / pod or container deploys | `/ov-core:deploy` |
| local-target deploy / `target: local` / `host: local` (default) / SSH-host deploys / `user:` / `ssh_arg:` | `/ov-local:local-deploy` + `/ov-internals:local-infra` |
| Editing `local.yml` / authoring `kind: local` templates | `/ov-local:local-spec` |
| Managed `~/.config/ov/ssh_config` fragment / `ov vm create` writes Host stanza | `/ov-vm:vm` + `/ov-local:local-deploy` |
| `ov eval run <bed>` (kind:eval R10 bed) / authoring `kind: eval` beds in `eval.yml` / `ov eval live` / `ov eval cdp/wl/dbus/vnc/mcp/record/spice/libvirt` | `/ov-eval:eval` |
| `ov eval k8s <verb>` / cluster probes | `/ov-kubernetes:eval-k8s` |
| `ov eval adb <method>` / Android Debug Bridge from host (devices, shell, install, getprop, screencap, logcat, wait-for-device) | `/ov-eval:adb` + `/ov-eval:eval` |
| `ov eval appium <method>` / Android UI automation / W3C WebDriver / APK install via mobile:installApp / session lifecycle / element introspection (get-text/get-attribute/clear/find-all/source) / per-class sugar groups (`gesture-*`/`app-*`/`key-*`/`device-*`) / generic WebDriver escape hatch (`execute`/`raw`) | `/ov-eval:appium` + `/ov-eval:eval` |
| `kind: android` device / `target: android` deploy / `apk:` package format in layers / installing Android apps declaratively / remote-or-emulator adb endpoint / nested `pod → android` | `/ov-eval:android` + `/ov-core:deploy` |
| Editing `layer.yml`, layer authoring, layer tasks/services | `/ov-image:layer` |
| Editing `image.yml`, image composition | `/ov-image:image` |
| `ov image build` / `ov image generate` / Containerfile | `/ov-build:build` + `/ov-build:generate` + `/ov-internals:generate-source` |
| `ov image validate` / schema error | `/ov-build:validate` |
| `ov clean` / build-artifact retention / `keep_images` / `keep_eval_runs` / image-tag pruning / `.eval` run cleanup | `/ov-core:clean` |
| Secret management / `ov secrets` / Secret Service / GPG `.secrets` | `/ov-build:secrets` |
| `ov migrate` / schema migration / legacy → latest CalVer / CalVer schema version | `/ov-build:migrate` |
| Git/`gh` workflow — `feat/` branch, commit, push, ff-merge to main, tag, worktree, sync-to-upstream, branch/worktree prune, PR create, `gh` approve/merge, cross-repo R10 landing | `/ov-internals:git-workflow` |
| `ov image reconcile` / cross-repo `@github` pin alignment / layer-version-mismatch cleanup | `/ov-build:reconcile` |
| Hard-cutover concerns / rename sweeps | `/ov-internals:cutover-policy` |
| Engineering-discipline triggers (failure surfaced / dup pattern / ad-hoc fix tempting / "out of scope" framing) | `/ov-internals:strict-policy` |
| Disposable-flag semantics / `disposable: true` authorization | `/ov-internals:disposable` |
| Go source work (adding/modifying `ov` commands) | `/ov-internals:go` |
| IR / InstallPlan / DeployTarget / OCITarget | `/ov-internals:install-plan` |
| OCI labels / capabilities contract | `/ov-internals:capabilities` |
| VmSpec / libvirt / cloud-init / OVMF internals | `/ov-internals:vm-spec` (+ renderer skills as needed) |
| Unexpected failure / error / anomaly | `/ov-internals:root-cause-analyzer` agent (BEFORE any fix) |
| "What does layer X do?" / "What's in image X?" — pod-specific | `/ov-jupyter:<name>`, `/ov-coder:<name>`, `/ov-selkies:<name>`, `/ov-openclaw:<name>`, `/ov-ollama:<name>`, `/ov-openwebui:<name>`, `/ov-comfyui:<name>`, `/ov-immich:<name>`, `/ov-hermes:<name>`, `/ov-filebrowser:<name>` |
| "What does layer X do?" / "What's in image X?" — base distros / GPU runtime / bootc | `/ov-distros:<name>` (archlinux, fedora, debian, ubuntu, cachyos, nvidia, cuda, rocm, bootc-base, …) |
| CachyOS images / `cachyos*` / `ov-cachyos` workstation profile / `image/cachyos` submodule | `/ov-distros:cachyos` + `/ov-vm:cachyos` + `/ov-local:ov-cachyos` |
| Debian images / `debian*` / `image/debian` submodule | `/ov-distros:debian` + `/ov-distros:debian-builder` + `/ov-distros:debian-debootstrap` + `/ov-coder:debian-coder` + `/ov-vm:debian` |
| Ubuntu images / `ubuntu*` / `image/ubuntu` submodule | `/ov-distros:ubuntu` + `/ov-distros:ubuntu-builder` + `/ov-distros:ubuntu-debootstrap` + `/ov-coder:ubuntu-coder` + `/ov-vm:ubuntu` |
| Fedora images / `fedora*` / `image/fedora` submodule / `fedora-base.yml` | `/ov-distros:fedora` + `/ov-distros:fedora-builder` + `/ov-distros:fedora-nonfree` + `/ov-coder:fedora-coder` + `/ov-distros:fedora-ov` + `/ov-distros:fedora-test` |
| bootc images / `bazzite` / `aurora` / `*-bootc` / `image/bootc` submodule | `/ov-distros:bazzite` + `/ov-distros:aurora` + `/ov-selkies:selkies-desktop-bootc` + `/ov-distros:bootc-base` + `/ov-vm:vm` |
| "What does layer X do?" — language runtime | `/ov-languages:<name>` (python, python-ml, pixi) |
| "What does layer X do?" — infrastructure service | `/ov-infrastructure:<name>` (postgresql, redis, k3s, traefik, supervisord, tailscale, gocryptfs, virtualization, dbus-layer, tmux-layer, …) |
| "What does layer X do?" — CLI utility / ov binary | `/ov-tools:<name>` (ripgrep, himalaya, whisper, ov, ov-full, …) |
| Skill authoring / skill maintenance | `/ov-internals:skills` |
| `ov eval *` / `eval.yml` `recipe:`/`score:` / AI-agent scoring / `oveval/*` branches | `/ov-eval:eval` |
| Sub-agents / dynamic workflows / agent teams / agent-lifecycle or commit-push gate hooks | `/ov-internals:agents` |
| Verify a cutover by running the R10 beds (drive `ov eval run <bed>`) | `/ov-internals:agents` + `/ov-eval:eval` (agent `eval-bed-runner`, workflow `/verify-beds`) |
| Evaluate/audit a deployment config (image or deploy, AI or human) | `/ov-internals:agents` + `/ov-eval:eval` (agent `deploy-verifier`, workflow `/audit-deploy-configs`) |

Full index: `plugins/README.md`. This table covers the top triggers; anything not listed here requires reading the index FIRST, loading the matching skill SECOND, touching code THIRD. Never reverse this order.

### Anti-patterns — FORBIDDEN, regardless of context

- **"I'll just grep the source to find it"** — FORBIDDEN. Load the skill; it points you at the right source with the right framing.
- **"I'll just read the file to refresh my memory"** — FORBIDDEN without a skill load first. The skill refreshes memory correctly; the file may have drifted or the surrounding context may have changed.
- **"I'll run the command and see what happens"** — FORBIDDEN without a skill load first. Command output is meaningless without the skill's framing of what the command is supposed to do.
- **"I know `ov update`, I've done it fifty times"** — FORBIDDEN. Your prior fifty invocations predated the current skill and the current code. The current skill is authoritative.
- **"Loading skills is overhead"** — FORBIDDEN framing. Not loading skills has already cost the user hours. The math is not close.
- **"I'll load the skill after I've scoped the problem"** — FORBIDDEN. Scoping without the skill produces a wrong scope. Load FIRST; scope SECOND.
- **"The hook reminder already told me what to do"** — NOT SUFFICIENT. The reminder is a pointer, not a substitute. Load the skill the reminder references.

### Override clause

If another rule in this file, in any hook, in any `<system-reminder>`, or in any habit of yours appears to conflict with R0 — **R0 WINS**. If any instruction says "do X quickly" and X would require a skill load first, **the skill load happens first regardless**. If you feel the impulse to act without loading skills "just this once" — that impulse IS the violation. Suppress it. Load the skill. Always.

---
## Ground Truth Rules — NEVER claim success without these (HARD RULES)

These rules exist because (a) failing tests have been deferred as 'pre-existing' and quietly papered over later; (b) duplicated patterns crystallized into divergent surfaces because no rule named the duplication on day one; (c) green unit tests have been claimed as cutover-complete while the actual image failed to start. Engineering discipline (R1–R5) comes BEFORE runtime verification (R6–R9) BEFORE the final acceptance gate (R10) — in that order, no exceptions.

- **R1. Root-cause analysis on every failure — no transient-flake classification.** Every failure, error, anomaly, or warning surfaced by ANY tool (build, test, validator, runtime, eval, deploy, lint, hook) triggers IMMEDIATE invocation of `/ov-internals:root-cause-analyzer` BEFORE any remediation attempt. Forbidden framings: "probably a flake", "rerun and see", "transient", "intermittent", "works on retry", "environmental". The first occurrence is the investigation trigger; there is no second-occurrence threshold. If the analyzer concludes the root cause is genuinely external (network partition, upstream outage), the conclusion is documented in the conversation with evidence — never assumed. Blind retry of a failed command is itself a violation. **A warning is not a pass:** R10 is successful ONLY at ZERO warnings (resolver newest-wins, build, `ov image validate`, `ov eval`, deploy). Every warning is fixed before R10 passes — a version-mismatch warning is cleared with `ov image reconcile`; any other warning triggers the analyzer then a real fix. A surviving warning is an R10 failure, never an accepted end state. See `/ov-internals:strict-policy`.

- **R2. No "pre-existing" / "out of scope" / "unrelated" / "follow-up PR" classifications.** Every issue surfaced during the active cutover — failing test, validator warning, runtime crash, deprecated-marker hit, dead-code reference, stale doc paragraph — is fixed in the SAME working tree as the cutover (the default — the AI fixes what it finds without asking), or — only when the issue is itself a genuine crossroad the AI cannot resolve from the request, code, skills, or sensible defaults — escalated to the operator for explicit re-scoping. The classifications "pre-existing", "unrelated to this change", "out of scope", "follow-up PR", "tracked separately", "we'll get to it later" are FORBIDDEN. **Blocking vs non-blocking — the ONE legitimate way an issue leaves the current cutover.** Classify every surfaced issue. A **blocking** issue — the current change is incorrect, incomplete, or unsafe without it — is fixed in the SAME working tree and proved under the CURRENT cutover's R10. A **non-blocking** issue — the current change is correct AND complete without it, and it is genuinely separable from this change — is STILL fixed immediately, but as its OWN cutover with its OWN full R10, opened the moment the current cutover is R10-passed and committed; it is NEVER parked as an indefinite "follow-up / someday" (that stays forbidden). The discriminator: *would shipping the current cutover WITHOUT this fix leave the tree correct and the cutover's claim true?* Yes → non-blocking (its own immediate-next cutover); No → blocking (this cutover); unsure → treat as blocking. **Objective test for "separable":** the current cutover's OWN R10 (its eval-coverage + fresh-rebuild) passes and proves the cutover's claim WITHOUT the fix — the fix is neither exercised by nor changes the verdict of this cutover's test coverage; a fix that would alter this cutover's R10 result or eval-coverage gate is BLOCKING. Mislabeling a blocking issue "non-blocking" to ship faster, or carving the current change's OWN scope into two, is the forbidden split — a genuinely separate concern getting its own cutover is not. (See `CHANGELOG.md` for the incident that motivated this rule.) See `/ov-internals:strict-policy`.

- **R3. No code duplication; generic, reusable solutions over ad-hoc patches.** On the FIRST surface where the same pattern, predicate, filter, transform, or guard appears in two places, refactor to ONE shared abstraction in the SAME working tree. Sibling-layer naming (`<name>-host`, `<name>-pod`), parallel filter functions, and per-call-site re-implementations of the same predicate are FORBIDDEN. Every fix MUST apply cleanly to ALL surfaces it logically covers, not just the surface that prompted the report. Generic > ad-hoc, every time. (See `CHANGELOG.md` for the worked examples that motivated this rule.) See `/ov-internals:strict-policy`.

- **R4. No ad-hoc workarounds — sleep loops, retry-on-flake, magic-number tuning, "works on my machine" fixes are FORBIDDEN.** Forbidden patterns: `sleep 5; retry`, `for i in 1..3 do try; done`, hardcoded port numbers chosen because "8080 was busy", environment-specific paths, default-fallbacks that hide a missing config, "this is what worked when I tried it locally". If a race or timing dependency exists, the fix is the synchronization primitive (file lock, readiness probe, condition variable, deterministic ordering), NEVER a sleep. If a value is magic, it is named, sourced from config, and validated on load. If a fix only works on one machine, it is not a fix — it is a bug report. See `/ov-internals:strict-policy`.

- **R5. Hard cutover: deprecated path AND every stale reference deleted in the same change.** When a cutover introduces a replacement, the SAME commit deletes (a) the deprecated code path, (b) every comment / TODO / DEPRECATED marker referencing the old path, AND (c) every reference, comment, docstring, error message, skill paragraph, migration help-text, test fixture, or hook string naming a deleted identifier. After commit, `git grep '<deleted-id>'` returns ONLY historical mentions in `CHANGELOG.md` or migration help-text. Deleting `image.yml` while the new `overthink.yml` path silently skips a build stage is not a clean cutover — it's a regression masked by the old file's absence. The acceptance test of a cutover is: rebuild from the new config, run the resulting image, observe the service reach steady-state, AND verify zero stale references via the grep self-test. See `/ov-internals:strict-policy`.

- **R6. Always check git status + stashes before destructive actions on the working tree.** `git stash` discards in-progress work; `rm` on a tracked file is destructive. If the sandbox blocks an action, read the reason and find a non-destructive alternative — do not work around it with a cleverer command.

- **R7. Unit tests never substitute for runtime verification — mandatory end-to-end gate.** A green `go test ./...` means the code compiles and fixture loaders work — nothing about whether the produced artifact behaves correctly. For any change that can affect Containerfile generation, OCI labels, init systems, service startup, or deploy code, the minimum sequence applies BEFORE "done":
  1. `ov image build <image>` — build a concrete image (not just generate Containerfile).
  2. `ov eval image <image>` — baked layer + image sections pass (NB: passes on zero-content stages too — not a substitute for R8).
  3. `ov start <image>` (or `ov deploy add <image> <image>` / `ov update <image>` for an existing deploy) — container must reach `Active: active (running)`.
  4. `ov eval live <image>` — full three-section run including deploy probes must pass.
  5. If any step fails, the task is NOT done — invoke R1's RCA mandate.

  A container that crash-loops on `supervisord: PermissionError: /var/log/supervisor/supervisord.log` exposes what no unit test would.

  **Which eval verb for R10 — pick by what you're proving:**
  - `ov eval image <image>` — build-scope invariants only (binary / package presence) in a disposable `podman run --rm`. No deploy, no live runtime state.
  - `ov eval live <name>` — deploy-scope probes against an ALREADY-running deployment you brought up yourself.
  - `ov eval run <kind:eval-bed>` — the WHOLE sequence above (steps 1-4) automated on a disposable bed: build → eval image → deploy → eval live → fresh update (the R10 acceptance gate) → tear down. **This is the canonical R10 gate.** Pick the bed whose kind matches what you changed — `eval-pod` (the combined image/layer/pod/DeployTarget mechanism bed) / `eval-local` / `eval-k3s-vm`, or a feature bed like `eval-android-emulator-pod`. `ov eval run --all-beds` runs every bed (name-sorted). Beds are `kind: eval` entities in `eval.yml`; `disposable: true` is the sole authorization for the unattended destroy+rebuild.
  - `ov eval run <kind:score>` — the multi-hour AI-iteration benchmark, NOT a quick gate. The same `ov eval run` verb dispatches by the kind the name resolves to.

  **`ov eval` exit codes** (goss/pytest-style; scripts and R10 automation rely on this): `0` = all checks passed; `1` = command/usage/infra error (the eval never ran a verdict — bad args, container not running, build/deploy/vm-create failed); `2` = the eval RAN and one or more **checks FAILED**. `ov eval image`/`live` return `2` on check failure; `ov eval run <bed>` propagates `2` when the bed's eval step fails but `1` for an infra step. Do NOT treat exit `1` as "tests failed" — that's a setup error; only exit `2` means the thing under test is broken. See `/ov-eval:eval` "Exit codes".

- **R8. Generated-artifact invariants — Containerfile sections AND OCI labels verified.** When a refactor touches generation, assert the presence of every critical section in the emitted Containerfile (e.g. `grep supervisord-conf .build/<image>/Containerfile`). A Containerfile that compiles but silently drops the init-system stage produces an image with the **stock RPM config**, not the overthink config — and the stock config almost always breaks at runtime. The emitted file is the source of truth; check it. After `ov image build`, `podman inspect --format '{{index .Config.Labels "org.overthinkos.init"}}'` must return the expected value for every capability label the image claims. An empty or missing label usually means a detection path silently returned nil. Treat missing labels as a failure, not a warning.

- **R9. Deployed binary matches source AND runtime deps declared in package management.** Syncthing / git / rsync move *source* between hosts; they don't rebuild the binary. After pushing code, explicitly rebuild on the target and verify `ov version`. If the version is old, the fix under test isn't really under test. A change that relies on an OS package at runtime (`nc`, `socat`, `xorriso`, `qemu-guest-agent`, …) MUST add that package to `pkg/arch/PKGBUILD` `depends=` (the single source of truth). A manual install on one host is a bug report disguised as a fix. (See `CHANGELOG.md` for the war-stories that motivated this rule.)

See `/ov-eval:eval` "DO NOT fake success" section for the mandatory sequence applied to test authoring specifically. See `/ov-internals:strict-policy` for the operationalization of R1–R5.

## Prioritize Clean Architecture Above All Else

Always pick the cleanest long-term approach and prioritize having a clean codebase with any deprecated code fully removed above everything.
You have all the time in the world and taking the time to get things properly done is ALWAYS worth the effort.

**No duplication on first surface.** When the same pattern would land in a second place, refactor to ONE shared abstraction in the SAME working tree before the duplicate ships. Procedural rule R3; architectural framing here. Sibling-layer naming (`<name>-host`, `<name>-pod`), parallel filter functions, and per-call-site re-implementations are the canonical anti-patterns.

**Generic over ad-hoc.** Every fix applies cleanly to ALL surfaces it logically covers. Procedural rule R3; architectural framing here. (See `CHANGELOG.md` for the canonical worked example.)

**No workarounds.** Sleep loops, retry-on-flake, magic-number tuning, "works on my machine" fixes are FORBIDDEN at the architectural level too — not just at the procedural-rule level. Procedural rule R4; architectural framing here. If a race exists, the fix is the synchronization primitive, not a delay.

## Disposable-Only Autonomy + Mandatory Live-Deploy Verification

**`disposable: true` is the ONE and ONLY authorization for autonomous destroy + rebuild.** Default is `false` (explicit opt-in only; see `/ov-internals:disposable`). No derivation from other fields. No "this looks like a test bed" heuristic. No hostname-based assumptions. A deploy is either explicitly marked `disposable: true` in deploy.yml or it is NOT rebuildable unattended — even if its name contains "test", even if it's a project on a shared host where unrelated production services also run. Explicit-only is what makes this rule safe on shared infrastructure with live users on other resources.

On resources that ARE marked `disposable: true`, `ov update <name>` performs destroy → (optional image rebuild) → create → start unattended, and is the preferred path. Hesitating to rebuild a disposable target when verification demands it is the OPPOSITE failure mode, and the one that leads to claimed-but-unverified fixes.

**Every change is proved on a freshly built binary on the target host** (the 10 evaluation standards in `/ov-eval:eval`):

1. Build the artifact from the changed source, on the target host.
2. Verify the deployed binary's version matches what you built (R9).
3. Verify runtime deps are installed via package management (R9).
4. For a target with `disposable: true`: `ov update <name>` — unattended. For any other resource: confirm with the user before any destroy.
5. Exercise the feature end-to-end.
6. Paste the runtime output back into the conversation.
7. Leave the target healthy (running, not paused, not crashed).
8. **After committing the source-level fix, `ov update` the disposable target from clean and re-run the full sequence. This fresh-rebuild re-verification is the acceptance gate** (R10).

### R10 — "Verify on a `disposable: true` target; prove it on a fresh rebuild"

The verification loop has three rules:

1. **Always test on a target that carries an explicit `disposable: true`.** Never experiment on a resource without the flag. If no suitable disposable target exists, create one first (`ov deploy add <name> <ref> --disposable` or mark a VM entry under `vm:` in deploy.yml and `ov vm create`). The opt-in is explicit; never assume disposability because of a name, lifecycle tag, hostname, or any other heuristic.
2. **If a test breaks the target, `ov update` it back to the committed config before doing anything else.** Never layer experiments on broken state.
3. **After committing the real fix in source, re-verify on a FRESH `ov update` of the disposable target.** A fix that passes only on a hand-patched target is not a real fix — it's a regression waiting for the next rebuild. Pasteable proof of the fresh-rebuild re-verification is the acceptance gate.

**A `--dry-run` does NOT count as an R10 test.** Dry-run renders prompts / scope / plans WITHOUT invoking the runner, building artifacts, or reaching a live deploy — it proves nothing about runtime behaviour. R10 requires a FULL live run of every new or changed code path: real subprocess invocation, real container build, real deploy probes against the running target, real verb evaluation against the live system. Validators, unit tests, and dry-runs are pre-flight checks, NOT the acceptance gate. If the cutover added or changed N pieces of functionality, R10 must exercise all N end-to-end on the disposable target — pasteable runtime output for each.

**An eval-sandbox (or any disposable target) REBUILD by itself does NOT count as an R10 test either.** The rebuild is preflight setup. R10 means the cutover's NEW or CHANGED code path — the runner / AI loop / verb evaluation / subprocess — actually executed AGAINST that fresh target and produced output you pasted. If the runner never ran, you do NOT get to claim `analysed on a live system`; the correct tier is `syntax check only` paired with explicit "R10 not yet run, awaiting authorization for the live round" — and pairing `syntax check only` with a commit is itself a violation, STOP and ask.

**Editing or deleting a task to retroactively redefine R10 is FORBIDDEN (see `CHANGELOG.md` for the attribution-fraud incident that motivated this).** R10 has ONE definition. `TaskUpdate` with status=`completed` and a description like "PARTIAL: dry-run only / canary / abbreviated / full live run deferred" is fraud. Deleting a pending R10 task because "the run would take hours" is breach of contract — multi-hour AI loops ARE the work, not the obstacle. Session-budget concerns NEVER downgrade R10 — they are the cost of doing business. If R10 genuinely cannot complete, SAY SO PLAINLY in your final message, do NOT commit anything (main repo OR submodule), do NOT trade tier for cycles. The user authorized R10 in scope; you deliver R10 in scope or you escalate, never both downgrade and ship silently.

**Score `eval.yml` config IS the test specification. CLI flag overrides require explicit user authorization in the SAME conversation turn (see `CHANGELOG.md` for the test-spec scope-shrink incident that motivated this).** Passing `--plateau-iteration`, `--max-scenario`, `--tag`, `--skip-rebuild`, `--on-pod`/`--on-vm`/`--on-host`, `--keep-repo`, `--dry-run`, OR the kind:eval bed flags `--no-rebuild` (skips the R10 fresh-rebuild gate) / `--keep` / `--all-beds` to `ov eval run` (or `ov eval live`) without the user explicitly saying "use --flag X" THIS turn is the same fraud class as dry-run-as-R10. Internal-voice triggers — "tractable wall-clock", "for the canary", "to fit session bounds", "shorten this run", "skip the heavy leg", "faster iteration cycle" — are confessions, not defences. Run the test AS SPECIFIED in the score config; the operator authorizes overrides, not Claude. The score's `plateau_iteration` and the AI's `progress_no_improvement_timeout` together define the AI's recovery budget per phase; do not narrow either without explicit authorization.

### End-of-turn checklist

Before saying "done" answer YES to all of these:

- Did `/ov-internals:root-cause-analyzer` run on every failure / warning / anomaly observed during the session (R1)?
- Was every issue surfaced during the session fixed in this cutover or explicitly escalated (R2)?
- Does `git grep` on every removed identifier return ONLY `CHANGELOG.md` / migration-help-text context (R5)?
- Built a real artifact from the changed source, on the target host?
- Verified the deployed binary's version matches what you built (R9)?
- Exercised the feature end-to-end on the live target?
- Verified every runtime dep is installed via package management (R9)?
- Did verification run on a target explicitly marked `disposable: true` (never on anything else)?
- If you broke the target during exploration, did you `ov update` it back to clean before continuing?
- After committing the source-level fix, did you `ov update` the disposable target from clean and re-run the full verification against the fresh rebuild (R10)?
- Post-action state of every target is healthy?
- Pasted BOTH the exploratory verification output AND the fresh-rebuild re-verification output into the conversation?

See `/ov-eval:eval` for the 10 evaluation standards and `/ov-internals:disposable` for the classification schema.

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
- **Authorizing a commit from an intermediate-state run.** The commit is
  gated on the full live test of EVERYTHING against the FINAL code, pasted. A
  bed run that passes on an *intermediate* state does NOT authorize a commit —
  only the full final-code live test does. (Running the beds on intermediate
  states to *verify* is encouraged; see the permits list. What is forbidden is
  treating such a run as the commit gate.)

**What this policy permits — equally precisely:**

- **Intermediate in-memory states during implementation.** While editing,
  the working tree WILL naturally be uncompilable or partially migrated
  between edits. That's normal. Reach compile-clean between related edits
  if it helps track progress, but don't treat compile-clean as "done."
- **Transitional aliases / legacy-accepting paths DURING implementation.**
  Every one of them is DELETED before the cutover ends — but they can
  exist mid-flight to simplify the refactor.
- **Running `ov` to verify, at any stage, as often as useful.**
  `ov image build`, `ov update`, `ov eval run`, `ov vm create`, `ov start`
  against a `disposable: true` target — in parallel or in the background — are
  ENCOURAGED throughout the cutover. **Verify before you change** (the
  proactive twin of R1): validate every assumption + error diagnosis on a live
  bed BEFORE editing, so you are never disproven hours later. Only the COMMIT
  is gated (on the full final-code test); running the beds to verify is not.
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
`ov update <disposable>` + `ov eval live <disposable>` exercises every code
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
3. **Verify continuously; the commit gate is the full final-code test.**
   Run `ov` commands freely throughout — `ov image build`, `ov update`,
   `ov deploy add`, `ov vm create`, `ov eval run`, `ov start` against a
   `disposable: true` target — at any stage, in parallel or in the
   background, to validate assumptions and diagnose errors BEFORE you edit
   (the proactive twin of R1). What the COMMIT is gated on is the full live
   test of EVERYTHING against the FINAL code (pasted): a run that passes on
   an intermediate state never authorizes a commit. Cheap smoke (`go build`,
   `go test`, `ov image validate`) is good hygiene between tasks but is NOT
   the gate.
4. **Full R10 test AFTER all code changes are implemented.** Unit tests,
   live build, live deploy to a `disposable: true` target, fresh-rebuild
   re-verification. The tests run against the FINAL code, not an
   intermediate state. R10's purpose is to catch whatever the migration
   missed — expect regressions and fix them in the same working tree.
5. **Fail the cutover if any verification fails.** Fix in the same working
   tree. Re-run everything. Do NOT paper over a partial failure by
   declaring "the rest is Phase 2."

The single idempotent `ov migrate` command transforms legacy configs
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

See `/ov-internals:cutover-policy` for forbidden patterns, required deliverables,
and the anti-pattern catalog. See `/ov-build:migrate` for the `ov migrate`
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
- **Committing — or claiming the cutover done — on the strength of an
  intermediate-state bed run.** The commit is gated on the full final-code
  live test (pasted); a run that passed before the last task landed does not
  authorize it. (Running the beds throughout to *verify* is encouraged — only
  the commit is gated, never the act of running `ov`.)
- **Classifying a surfaced issue as "pre-existing" / "unrelated" / "out
  of scope" / "follow-up PR" / "tracked separately".** R2 forbids this
  absolutely — every issue surfaced during the active cutover is fixed
  in the same working tree or escalated to the operator.
- **Adding a band-aid to one surface when the same pattern exists on
  N surfaces.** R3 demands the generic fix on first refactor, applied
  to ALL N surfaces in the same commit.
- **Ad-hoc workarounds — sleep loops, retry-on-flake, magic-number
  tuning, "works on my machine".** R4 forbids these. Synchronize
  properly or escalate.
- **Stale references after deletion.** A removed identifier MUST NOT
  survive in any comment, docstring, error message, skill paragraph,
  migration help-text, test fixture, or hook string after the cutover
  commit. R5 self-test: `git grep '<deleted-id>'` returns only
  `CHANGELOG.md` or migration help-text.

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
3. **Auto-land the `feat/` branch — gated by R10, NEVER force-push.**
   The change was developed on a `feat/<slug>` branch off up-to-date
   `main` (see `/ov-internals:git-workflow`). The **R10 pass is the sole
   landing trigger** — there is no per-change human "push" step anymore
   (this SUPERSEDES the older "push only if the user asked"). On R10
   PASS, automatically and in order: (a) push `feat/<slug>`; (b)
   `git merge --ff-only feat/<slug>` into `main` (if `main` advanced,
   re-sync, rebase `feat/` onto it, and re-run R10 first); (c) tag the
   new `main` HEAD with a fresh `v<CalVer>` and push `main --follow-tags`;
   (d) delete `feat/<slug>` local + remote. NOTHING is ever pushed or
   merged on unverified state. **NEVER force-push** — no `git push
   --force`, no `--force-with-lease`, on ANY branch (`feat/` included) in
   ANY repo, ever; `main` only fast-forwards, tags are immutable/add-only.
   The whole flow is designed so a force push is never needed.
   - **No write access?** Same `feat/` discipline via a fork +
     `gh pr create` (body ends with `*Assisted-by: Claude (<tier>)*`).
     The AI may `gh pr review --approve` + `gh pr merge` an open PR ONLY
     after fetching its head, reviewing the diff, and running R10 to a
     PASS — never a blind approve; branch protection is respected.
   - **Multi-repo / cross-repo:** one logical change uses the SAME
     `feat/<slug>` in each repo and lands in dependency order (deepest
     submodule → `plugins` → superproject). A change a consumer pins via
     `@github` lands the producer + tag FIRST, then `ov image reconcile`
     repoints the consumer, whose authoritative R10 runs against the real
     pushed tag (a local `feat/` branch is never enough). See
     `/ov-internals:git-workflow` B6.
   - **Tag computation (load-bearing).** `v<YYYY.DDD.HHMM>` from the
     current UTC push time; day-of-year NOT zero-padded — compute
     `v$(date -u +%Y).$((10#$(date -u +%j))).$(date -u +%H%M)`, never bare
     `+%Y.%j.%H%M`. ONE fresh tag per push, immutable (only ever ADD;
     never move/force-push), INDEPENDENT of the `overthink.yml` `version:`
     field. Tag EVERY push, including at an unchanged `version:`. The
     `version:` field is the SCHEMA version, bumped ONLY by a
     `MigrationStep` raising `LatestSchemaVersion()` (never above it —
     newer configs hard-fail at load). **Every YAML schema/format change
     MUST do BOTH: (1) raise `LatestSchemaVersion()` via a new
     `MigrationStep`, AND (2) carry the fresh `v<CalVer>` tag on the
     landing push.** Push order across repos: submodule(s) first, then
     the superproject, then the tag on the pushed superproject HEAD.
     Repos without an `overthink.yml` (`plugins`, `pkg/arch`) are
     tag-exempt. See `/ov-internals:git-workflow`, `/ov-build:migrate`.
   - **Eval-coverage gate.** R10 does not pass — and the change is NOT
     landable — unless it ships the test coverage that PROVES its new
     functionality (`eval:` checks for new/changed layers & images; Go
     tests for `ov` code) AND the live run exercised it. See R7/R10,
     `/ov-eval:eval`.
   - **Zero-warnings gate.** R10 is NOT successful while ANY warning
     remains — resolver newest-wins warnings, build, `ov image validate`,
     `ov eval`, or deploy warnings. Each is fixed before R10 passes: a
     version-mismatch warning is cleared with `ov image reconcile`; any
     other warning triggers `/ov-internals:root-cause-analyzer` then a
     real fix. A surviving warning is an R10 failure, never an accepted
     end state (this is R1 made a hard gate).
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

1. **Run `/ov-internals:root-cause-analyzer` BEFORE attempting any fix.**
   Blind retry is FORBIDDEN. R10 caught a real regression; understand
   it first.
2. **Fix in the same working tree.** No "I'll address this in a
   follow-up PR" — the cutover policy explicitly forbids that. Fix +
   re-run R10 in the same conversation, against the same uncommitted
   tree.
3. **Re-run R10 from scratch.** Not just the failing piece — the
   FULL R10 against a fresh `ov update`. A fix that survives only
   the targeted re-run but breaks something else is a regression in
   waiting.
4. **Only commit when R10 passes end-to-end on the FINAL code.** No
   commits of half-fixed states.

### What is NOT post-execution

- **Folding new work INTO the current cutover** is FORBIDDEN — picking up
  "the next thing" mid-cutover re-creates a half-migrated state. But
  STARTING the next cutover is the AI's job, not something it waits on the
  user for. **Default to autonomous action:** the moment the current
  cutover is R10-passed and committed, the AI AUTOMATICALLY opens the next
  cutover to solve ANY issue it has found — whether this cutover surfaced
  it or not — each as its own atomic, fully-R10'd change. It does NOT queue
  routine work for authorization. The AI pauses to ASK only at a genuine
  **unexpected/unplanned crossroad** — a decision it cannot resolve from
  the request, the code, the loaded skills, or sensible defaults (a design
  choice with material trade-offs; a hard-to-reverse or outward-facing
  action without standing authorization; a contradiction between the plan
  and CLAUDE.md/skills; genuinely ambiguous requirements). Everything else
  it solves automatically and reports. A blocking issue is fixed IN the
  current cutover (never split out).
- **Backporting / cherry-picking.** Out of scope for the CURRENT cutover's
  post-execution flow — but it is its own atomic, fully-R10'd cutover the AI
  opens automatically when needed (it does not wait for a user follow-up),
  pausing only if the backport target or release strategy is a genuine
  crossroad.
- **Documenting "what would have been Phase 2".** The cutover either
  completed or it didn't. Phase 2 is a forbidden concept.

### The post-execution checklist

Before declaring the turn done, every YES:

- [ ] R10 passed on EVERY affected disposable target (not just one)?
- [ ] R10 ran AGAINST THE FINAL CODE (not an intermediate state)?
- [ ] Both exploratory and fresh-rebuild R10 outputs pasted into the
      conversation?
- [ ] ONE atomic commit per repo (on the `feat/<slug>` branch), with the
      AI-attribution trailer at the tier the proof supports (no inflation)?
- [ ] The change ships the test coverage that PROVES its functionality
      (`eval:` checks / Go tests) and R10 exercised it (eval-coverage gate)?
- [ ] Auto-landed on R10 PASS: `feat/` fast-forward-merged into `main`,
      `main` HEAD tagged `v<CalVer>`, pushed, `feat/` deleted — with NO
      force-push anywhere (no `--force` / `--force-with-lease`)?
- [ ] `git status` clean after landing; `feat/` branches pruned?
- [ ] No "Phase 2 / TODO / will do next time" deferred work
      surfaced in this plan?

## Agents, Workflows & Teams

Overthink is built to be driven from Claude Code's multi-agent primitives —
**sub-agents** (`plugins/internals/agents/*.md`), **dynamic workflows**
(`.claude/workflows/*.js`, run `/<name>`), and **agent teams** (experimental,
**enabled in the committed `.claude/settings.json`** via
`env.CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1`; experimental caveats remain — no
in-process session resume, one team at a time, no nested teams, fixed lead).
Full reference: `/ov-internals:agents`. This is the brief.

**Prefer agents over background tasks — everything that CAN run as an agent SHOULD
run as an agent.** When a unit of work can be done by an addressable,
operator-visible **sub-agent** or **agent-team teammate**, use that — never an
opaque background workflow. **Team agents are the DEFAULT for parallel work** (the
operator watches and messages them live). A background dynamic workflow
(`Workflow` tool) is a LAST RESORT, reserved for deterministic scripted control
flow (loops / conditionals / large fan-out) that a team genuinely cannot express —
and even then it surfaces its work as agents and obeys the same bed-scoped
discipline. Operator-facing agents > opaque background tasks, always; visibility
and interactive control are the point. **The one exception is long-running
work that outlives a single turn** (a VM/emulator eval bed): no agent can
reliably hold it — a sub-agent returns synchronously (its background children
die on return) and a teammate is torn down on idle — so it runs as a
harness-tracked background task owned by the persistent session, driven by the
completion notification (see "Handling a long-running eval bed" below). "Prefer
agents" governs BOUNDED work; long-running work uses the
background-task-plus-notification mechanism, not a who-owns-it rule.

**Teams test in parallel on real deployments — the eval bed is the unit of
ownership.** The lead partitions the `kind: eval` beds so no two teammates own
the same bed; distinct beds have disjoint container/VM/image names + ports
(`validateEvalBeds` guarantees it), so they run concurrently and safely with no
worktree. Each teammate runs its own bed's full `ov eval run <bed>` on a real
deployment and **verifies before it changes** (validate assumptions on a live
bed before editing). One cutover stays one phase / one commit, owned by the
lead; teammates never commit or push independently.

**Dynamic workflows that IMPLEMENT a cutover obey the SAME bed-scoped discipline
as teams — not optional.** A workflow that fans implementation out across
`agent()` calls MUST partition the parallel work by `kind: eval` bed (one
disjoint disposable bed per parallel owner), each owner **verifying before it
changes** and running its bed's real `ov eval run <bed>` — eval-testing at EVERY
stage of development, never deferred to the end. Read-only diff review is an
ADDITIONAL adversarial layer, NEVER a substitute for real-deployment bed testing;
a workflow that swaps bed runs for code review is a protocol violation. Single-Go-
package compile-coherence is handled STRUCTURALLY — the lead lands the shared core
FIRST, each parallel unit is an independent `init()`-registered file, and the one
shared `ov` binary rebuild is a single barrier between parallel-implement and
parallel-bed-R10 — it is NEVER a license to serialize the whole implementation or
to trade beds for review. Canonical shape: `Core (seq) → Implement (parallel by
bed) → Integrate+build (seq barrier) → BedR10 (parallel by bed) → Review (parallel,
read-only, optional)`. Same R10 gate + disposable-only + no-scope-shrinking-flags +
paste-proof rules as teams.

**Agent roster** — *executors* run `ov eval` and return verbatim proof:
`eval-bed-runner` (runs `ov eval run <bed>` — the R10 acceptance executor),
`deploy-verifier` (read-only `ov eval image`/`live` + `ov status` for an image
or a user's deploy). *Enforcers* gate claims: `root-cause-analyzer` (R1 RCA),
`testing-validator` (proof-before-"works"), `layer-validator` (pre-edit
`layer.yml`).

**Workflows** — `/verify-beds [bed …]` fans the `kind: eval` beds out as the
R10 gate; `/audit-deploy-configs [target …]` evaluates deploy configs
(validate + `ov eval image`/`live` + `deploy-verifier`) for AI and humans.

**Binding rule — running a bed is R10-class.** Any agent or workflow that runs
`ov eval run <bed>` / `ov update` obeys: disposable-only authorization (Law 4),
the commit is gated on a full final-code live test that is pasted (Law 5) —
beds run freely throughout to verify; only the commit is gated — no
scope-shrinking flags (Law 3.6), and **paste-proof survives delegation** — the
executor returns the verbatim verdict + exit code, and the delegating agent
PASTES it (a delegated bed run whose failure is summarized away is fraud).
**Handling a long-running eval bed — by mechanism, not by who owns it.** A
VM/emulator bed (`eval-k3s-vm`, `eval-android-emulator-pod`, the bootstrap-VM
beds) runs for minutes-to-tens-of-minutes and spawns a libvirt domain / emulator
that OUTLIVES a single turn. (1) **Launch it as a harness-tracked background
task** (`run_in_background`) — never in the foreground (the Bash 120s/600s
timeout kills the call mid-`vm-create`, orphaning the domain) and never kept
alive by a sleep/poll loop (that busy-poll is the R4 bandaid this guidance
replaces). (2) **Let the completion notification drive the next step** — the
harness re-invokes the LAUNCHING session when the run exits, so the launcher must
SURVIVE to completion to receive it: the persistent main session does; an
ephemeral sub-agent (returns synchronously, its background children die) and an
idle teammate (process tree torn down on idle) do NOT and orphan the bed. Long
beds therefore belong to a session that lives to be notified; short beds (finish
within one turn / the 600s foreground budget) can be sub-agent- or
teammate-owned. (3) **Reconnect via durable state, not a held handle** —
`.eval/<bed>/<calver>/summary.yml` (overall `ok:` + per-step status) + the live
domain/container ARE the source of truth: "done + verdict" = `summary.yml`
present; "still alive" = the `ov eval run` orchestrator is in the process table;
on a suspected orphan (`running` domain, no live orchestrator) `ov vm destroy
<entity>` before re-running. (4) **Paste-proof still holds** (Law 5) — the owner
reports the verbatim verdict + exit code; the lead pastes it. Detail:
`/ov-internals:agents`, `/ov-eval:eval`.

**Hooks doctrine.** Hooks are LEAN POINTERS to this file + skills (never copies
of R0–R10 — duplication drifts) PLUS deterministic `PreToolUse` gates
(`pre-commit-gate.sh`, `pre-push-gate.sh`) that BLOCK only unambiguous
invariants (`--no-verify`, illegal/absent attribution tier, `--force`). Hooks
gate mechanical invariants; agents judge proof. Never re-bloat the hooks.

**Per-directory CLAUDE.md signposts.** This root file is the single canonical
R0–R10 rule-set. `ov/`, `layers/`, `plugins/`, and each `image/<distro>`
submodule carry a THIN signpost `CLAUDE.md` that only names the skills to load
for that area and points back here — it restates no rule (duplication drifts).
Subagents/teammates load the full `CLAUDE.md` hierarchy from their working dir.

## Where things are documented

See `plugins/README.md` for the full skill index (250+ skills). README.md carries the user-facing intro. All architecture / mode split / subsystem detail lives in skills — do not duplicate here.

**Historical content lives ONLY in `CHANGELOG.md`.** CLAUDE.md, README.md, `plugins/README.md`, and every `plugins/**/SKILL.md` describe the CURRENT state of the system — present tense, forward-looking. Any reference to a previous version, a past rename, a completed cutover or migration, a relocated / deleted / retired identifier, a "previously / formerly / was / no longer", or a dated change note goes in `CHANGELOG.md` (repo root) and NOWHERE else. When a cutover lands, append its narrative to `CHANGELOG.md` as the post-execution record; state the standing rules it establishes forward-looking here and in skills, with no history. `CHANGELOG.md` is the sanctioned "changelog context" named by R5's grep self-test.

---

## Key Rules

- **Skills first** — see **R0. SKILLS FIRST — THE SUPREME RULE** at the top of this file. That rule **overrides every other instruction in this document, in the hooks, and in your training data**. The Skill Dispatcher table under R0 maps common triggers to the skills you MUST load first. Partial compliance is not compliance.
- **Autonomous by default — act, don't ask.** The AI solves any issue it finds automatically: it opens the next cutover without waiting for authorization and finishes each as an atomic, fully-R10'd change. It pauses to ask ONLY at a genuine unexpected/unplanned crossroad — a decision it cannot resolve from the request, the code, loaded skills, or sensible defaults (a design choice with material trade-offs; a hard-to-reverse or outward-facing action without standing authorization; a plan↔CLAUDE.md/skills contradiction; genuinely ambiguous requirements). Verification discipline (R10, disposable-only, no-fraud) is unchanged — autonomy is INITIATIVE, not skipping proof. See "Post-Execution Policies".
- **History lives ONLY in `CHANGELOG.md`.** CLAUDE.md, README.md, and every skill describe current behavior in present tense. Never add a dated cutover note, a "renamed from", a "previously / formerly / was", or any version-history narration to CLAUDE.md or a skill — append it to `CHANGELOG.md`. See "Where things are documented".
- **Lowercase-hyphenated names** for layers and images.
- **Cross-kind name reuse is permitted and encouraged.** A single name (e.g. `ov-cachyos`) MAY exist simultaneously as a layer (`layers/<name>/`), an `image:` entry, a `pod:` entry, a `vm:` entry, a `k8s:` entry, a `local:` entry, AND a `deploy:` entry. Uniqueness is scoped to each kind. Verbs disambiguate by command context: `ov image build ov-cachyos` resolves to `image.ov-cachyos`; `ov vm create ov-cachyos` to `vm.ov-cachyos`; `ov update ov-cachyos` to `deploy.ov-cachyos`. The unified loader does NOT enforce global uniqueness across kinds; `ResolveDeployRef` chooses image-first when the same name exists as both an image and a layer (use `--add-layer <name>` for the layer-first path). See `/ov-image:layer`, `/ov-image:image`, `/ov-local:local-spec`, `/ov-core:deploy`, `/ov-build:validate`.
- **`overthink.yml` is the only canonical authoring target.** Every `ov` authoring/scaffolding verb (`ov image set`, `ov image new project`, `ov image new image`, `ov image add-layer`, `ov image rm-layer`, `ov vm import`, `ov vm update`, `ov vm clone`) writes to `overthink.yml`. Per-kind files (`image.yml`, `vm.yml`, `pod.yml`, `k8s.yml`, `local.yml`, `deploy.yml`) remain valid as flat `import:` items in `overthink.yml` but are NEVER the default authoring target. Missing `overthink.yml` → hard error pointing at `ov image new project .` or `ov migrate`.
- **Init-system polymorphism via mixed `service:` entries.** A layer that needs a service running under both supervisord (container/pod targets) and systemd (host / bootc / VM targets) declares BOTH forms in ONE `service:` list — same `name:`, one entry with `use_packaged: <unit>.service` (or `<unit>.socket`), the other with custom `exec:`. The init system at deploy time renders only the matching form. **NEVER** create a `<name>-host` or `<name>-pod` sibling layer to express target polymorphism — it duplicates packages and eval probes and inevitably drifts. Canonical worked examples: `/ov-coder:sshd` (mixed), `/ov-infrastructure:virtualization` (mixed), `/ov-infrastructure:postgresql` (use_packaged-only). See `/ov-image:layer` "Service Declaration" + "Anti-pattern: `<name>-host` / `<name>-pod` sibling layers".
- **Tests ship with the image.** See `/ov-eval:eval`.
- **Unified YAML + `import:` (Go-style namespaces).** `overthink.yml` is the single project entry point. The SINGLE composition statement is `import:` (the legacy `include:` key is DELETED — a residual `include:` is a hard load-time error pointing at `ov migrate`). `import:` is a LIST whose items are either a **bare string** (flat import into THIS repo's root namespace — same-repo per-kind files + the shared `build.yml` distro/builder/init vocabulary) or a **single-key map `alias: ref`** (a namespaced child import of another project; entries referenced QUALIFIED as `alias.entry`, e.g. `base: cachyos.cachyos`, `builder: {pixi: ov.arch-builder}`). Resolution is namespace-relative (Go package-member semantics); `distro:`/`build:` inherit across a namespace boundary but `builder:` does NOT (the consumer declares its own builder map). The **main repo stays multi-file** (`base.yml` = arch+fedora stacks, plus `eval.yml`/`image.yml`/`build.yml`/…); **every `image/<distro>` submodule is a single `overthink.yml`** that imports main under the `ov` namespace and `build.yml` flat. The main↔cachyos mutual import is cycle-broken at load. See `/ov-image:image`, `/ov-internals:go`, `/ov-build:migrate`.
- **Schema.** Seven singular kinds (`image`, `pod`, `vm`, `k8s`, `local`, `android`, `deploy`) with singular root-shape keys throughout (filename and kind name match: `kind: deploy` in `deploy.yml`, `kind: image` in `image.yml`, etc.). File convention: `image.yml` / `pod.yml` / `vm.yml` / `k8s.yml` / `local.yml` / `android.yml` / `deploy.yml` all optionally flat-imported (`import:` string items) into `overthink.yml`, or inlined in a single file. The schema version is a CalVer string (e.g. `2026.144.1443`), the same scheme as image tags; configs older than `LatestSchemaVersion()` migrate via the single idempotent `ov migrate`. Nesting of deployments uses `nested:`. See `/ov-build:migrate`, `/ov-image:image`, `/ov-core:deploy`, `/ov-vm:vm`, `/ov-local:local-spec`, `/ov-eval:android`.
- **Hard cutover by default.** See `/ov-internals:cutover-policy` and the "Hard Cutover by Default" section above.
- **Tag every push with a fresh CalVer timestamp.** When pushing an ov-project repo (one with an `overthink.yml`), the push carries a **fresh** annotated git tag `v<YYYY.DDD.HHMM>` from the current UTC push time — ONE per push, accumulating multiple tags per repo over time, INDEPENDENT of the `overthink.yml` `version:` field (the schema version, bumped only by a `MigrationStep`). Tag every push, **including at an unchanged `version:`**. Tags are immutable — only ever added, never moved or force-pushed. Day-of-year is NOT zero-padded; compute `v$(date -u +%Y).$((10#$(date -u +%j))).$(date -u +%H%M)` (not bare `+%Y.%j.%H%M`). Push order: submodule(s) first, then the superproject, then the tag on the pushed HEAD. Repos without an `overthink.yml` (`plugins`, `pkg/arch`) are exempt. See "Post-Execution Policies" and `/ov-build:migrate`.
- **Per-kind versioning: `version:` is the authoritative identity.** Every `layer` MUST declare a `version:` CalVer (validator hard-errors otherwise); it is OPTIONAL for every other kind. An image's emitted `org.overthinkos.version` LABEL is the **content-derived `EffectiveVersion`** — its dedicated `version:` if set, else the highest layer `version:` across the whole base chain (computed in `ov/effective_version.go`). The label is STABLE across builds when no layer changed, so a child's `FROM <base>` SHA doesn't shift and cache-misses don't cascade; bare distro bases carry a dedicated `version:` for the same reason. Short-name resolution + `ov clean` retention prefer the **label-CalVer over the tag-CalVer** (the per-build tag is only a tiebreaker). `ov migrate` backfills both (`entity-version` step); the runtime hard-errors on a non-conformant config (no compat fallback).
- **Layer-version resolution: per-entity version, post-fetch.** The `@github…:vTAG` git tag is ONLY the FETCH coordinate (which commit to clone); a layer's OWN `version:` (read AFTER fetch) is what's compared. The resolver collects EVERY distinct `(repo, git-tag)`, fetches each, and `pickLayerVersion` arbitrates per bare ref: same per-entity version across different git tags → **no warning** (a repo re-tag of an unchanged layer is silent — this is the fix for spurious warnings), the newest git tag winning for freshness; different per-entity versions → **warn once and use the newest** (highest CalVer). This ONE arbiter covers direct AND transitive refs; a fetched layer with no `version:` is a hard error. Remote-ref collection is **reachability-scoped**: only layers reachable from the enabled images' `base:`/`builder:` chains are fetched — a namespace's unreferenced images and its `kind:local` templates are NOT collected. `ov image reconcile` aligns the git-tag pins. See `/ov-internals:go` "Remote-layer resolver", `/ov-build:validate`, `/ov-build:reconcile`, `/ov-internals:capabilities`.
- **Branch-per-change, R10-gated auto-landing — NEVER force-push.** Every change is developed on a `feat/<slug>` branch off up-to-date `main`. The **R10 pass is the sole landing gate**: on R10 PASS the AI automatically commits (atomic, with the attribution trailer), pushes `feat/<slug>`, **fast-forward-only** merges into `main`, tags `main` HEAD with a fresh `v<CalVer>`, pushes, and deletes the `feat/` branch — **no per-change confirmation** (this supersedes the older "push only if the user asked"). NOTHING is pushed/merged on unverified state. **NEVER force-push** — no `git push --force`, no `--force-with-lease`, on ANY branch (`feat/` included) in ANY repo, ever; `main` only fast-forwards, tags are add-only. Contributors without write access use the same `feat/` discipline via a fork + `gh pr create`; the AI may `gh`-approve/merge open PRs but ONLY after running R10 against the PR head (never a blind approve). Multi-repo changes use the same `feat/<slug>` in each repo and land producer→consumer in dependency order; cross-repo `@github` changes land the producer + tag FIRST, then `ov image reconcile` repoints the consumer, whose authoritative R10 runs against the real pushed tag. See `/ov-internals:git-workflow` and "Post-Execution Policies".
- **Every change ships proof of its functionality.** A change is acceptable ONLY if it adds/updates the test coverage that PROVES what it does — `eval:` declarative checks (build- and deploy-scope as appropriate) for new/changed layers & images, Go unit tests for `ov` code — and the R10 live run exercises that path. A change whose new functionality has no test that would FAIL without it does not pass R10 and is not landable. See `/ov-eval:eval`, R7/R10.
- **Deploy fetches NOTHING speculative.** Every `ov deploy add` (any target kind: `local`, `pod`, `vm`, `k8s`) MUST emit zero image-pull / image-build steps unless an explicit layer step at deploy time requires the image — and no layer does today. Test-bed image preflight is the test/eval entry point's job, not the deploy's: `ov eval run` collects `score.target_image:` + per-scenario `pod:` declarations and ensures each is present in podman storage BEFORE running scenarios. A `kind: local` template carries no `image:` field. See `/ov-local:local-spec`, `/ov-eval:eval`.
- **Engineering discipline (R1–R5) comes before runtime verification (R6–R9) before R10.** R1 (RCA on every failure), R2 (no "pre-existing" / "out of scope"), R3 (no duplication; generic > ad-hoc), R4 (no ad-hoc workarounds), R5 (hard cutover: deprecated + stale references in same change). See `/ov-internals:strict-policy` for the operationalization. R10 (disposable + fresh-rebuild) is the final acceptance gate.
- **Mode purity.** `LoadUnified` reads `overthink.yml` only; never merges `deploy.yml`. See `/ov-internals:go` "Mode purity".
- **Project directory resolution.** See `/ov-image:image` "Project directory resolution".
- **User policy: adopt over rename.** Declarative via `build.yml distro.<name>.base_user:` + `user_policy:`. See `/ov-image:image` "user_policy" and `/ov-build:build` "base_user:".
- **Unified `service:` schema.** See `/ov-image:layer` "Service Declaration".
- **Capabilities as OCI-label contract.** See `/ov-internals:capabilities`.
- **Deploy targets.** `ov deploy add <name> <ref>`: `target: local` + `host: local` (default) → local filesystem via `ShellExecutor`; `target: local` + `host: <user@machine[:port]>` → SSH (ssh-config + agent supply credentials); `target: vm` → VM via managed `ov-<vmname>` ssh-config alias; `target: k8s` → Kustomize tree; `target: android` → install `apk:` packages onto a `kind: android` device (in-pod emulator or remote adb endpoint) via `AndroidDeployTarget`; `target: pod` (default) → container deploy. See `/ov-core:deploy`, `/ov-local:local-deploy`, `/ov-kubernetes:kubernetes`, `/ov-internals:vm-deploy-target`, `/ov-eval:android`. Shared IR: `/ov-internals:install-plan`.
- **`kind: android` + the `apk` package format.** Android is a first-class deploy SUBSTRATE modeled on `kind: k8s`: a `kind: android` entity is a DEVICE (an in-pod emulator referenced by `image:`, or a remote/physical adb endpoint referenced by `adb: {host: …}`). `apk` is a layer-declared PACKAGE FORMAT (NOT a kind) — a layer's `apk:` list (per-app `package:`/`apk:` + `source`/`arch`/`version`) parallels `package:`/`aur:` but is device-scoped; it compiles to an `ApkInstallStep` that ONLY `target: android` executes (every other target skips it — there is no device at image-build time). A `target: android` deploy applies its `add_layer:` layers' `apk:` packages onto the device via ONE shared installer (`ov/android_install.go`, also driving `ov eval adb install-app`/`install` — R3). Nested deployment: `pod → android` (the device on its emulator pod) mirrors `vm → k8s`. See `/ov-eval:android`, `/ov-eval:adb`, `/ov-eval:appium`.
- **k3s cluster provisioning via layers.** `/ov-infrastructure:k3s` + `/ov-infrastructure:k3s-server` + `/ov-infrastructure:k3s-agent` compose into a full k3s cluster on any substrate (host / VM / container). Pre-shared `K3S_CLUSTER_TOKEN` auto-generates on first deploy via `ensureLayerSecret` (`ov/layer_secrets.go`) — server and every agent automatically share the persisted value with zero operator setup; override with `ov secrets set ov/secret/K3S_CLUSTER_TOKEN <value>` only when reproducing a specific cluster identity. Kubeconfig pulled back via layer `artifact:` block (with `wait_seconds: 120` so retrieval waits for k3s to write `/etc/rancher/k3s/k3s.yaml`). Cluster configuration lives on a `kind: k8s` entity (workload defaults + cluster policy). Cluster probes via `/ov-kubernetes:eval-k8s` (`ov eval k8s nodes/addons/wait-ready/…`).

---

## AI Attribution (Fedora Policy Compliant)

Per [Fedora AI Contribution Policy](https://docs.fedoraproject.org/en-US/council/policy/ai-contribution-policy/), ALL commits MUST include `Assisted-by: Claude (<confidence>)` trailer. ALL GitHub issues/PRs MUST include `*Assisted-by: Claude (<confidence>)*` at the end.

| Confidence | When to Use |
|-----------|-------------|
| `fully tested and validated` | All 10 evaluation standards met + fresh-rebuild re-verification (R10) on every affected `disposable: true` target + the cutover's NEW/CHANGED runner / AI loop / verb evaluation actually executed against the fresh rebuild + R10 outputs (exploratory + fresh-rebuild) pasted in the conversation |
| `analysed on a live system` | A live invocation of the runner / AI loop / verb evaluation / subprocess that the cutover ADDED OR CHANGED actually ran AND its output is pasted. An eval-sandbox rebuild WITHOUT the subsequent runner invocation does NOT qualify — that's `syntax check only`. NEVER use this tier when only a `--dry-run` was performed |
| `syntax check only` | Compile + unit tests + validators / dry-run / parse confirmations passed; the live runner did NOT execute. HONEST default when R10 hasn't physically fit yet — pair with explicit "R10 not yet run, awaiting authorization for the live round" AND do NOT commit. Pairing this tier with a commit is a violation; STOP and ask (this targets CODE with a pending R10 — a docs/policy-only cutover is governed by the provision below) |
| `theoretical suggestion` | No validation performed — FORBIDDEN as a shipped-code tier |

**Docs/policy-only cutovers — the runtime tiers are read against the APPLICABLE standards.** A cutover that touches ONLY documentation/policy (`CLAUDE.md`, `plugins/**/SKILL.md`, `README.md`, `plugins/README.md`, `CHANGELOG.md` — no Go, no YAML schema, no `layer.yml`/`image.yml`, no other runtime surface) has NO R10 bed to run. Its applicable evaluation standards are the non-runtime ones: adversarial consistency review, the R5 grep self-test, cross-reference validation, markdown integrity, and the `pre-commit-gate.sh` / `pre-push-gate.sh` gates. Such a cutover earns `fully tested and validated` when ALL applicable (non-runtime) standards pass; the `syntax check only → do NOT commit` clause does NOT apply to it (that clause targets code with a pending R10). The moment a cutover ALSO touches code or config it is NOT docs-only — it is gated on that surface's R10 as usual, at the tier its runtime proof supports, and the docs ride along in the same commit.

**Any rule violation forbids commit. Period.** A violation of R1, R2, R3, R4, R5, R6, R7, R8, R9, R10, OR the "Prioritize Clean Architecture Above All Else" section means: NO commit, at any tier, in any submodule, with any wording. There is no "downgrade tier and ship anyway" path — that path does NOT exist. The agent's only authorized responses to a known violation are (a) fix the violation in the same working tree and re-run all verification, or (b) escalate to the operator and STOP. Suggesting any other path — "lower tier", "downgrade", "commit at a reduced confidence", "ship with a caveat", "note the violation in the commit message and proceed" — is itself a rule violation. The four-tier table above describes WHICH tier the proof supports when committing IS permitted; a known rule violation makes commit NOT permitted regardless of tier.

```
Fix: Add fuse-overlayfs for container startup

Tested via overlay session on LOCAL system.

Assisted-by: Claude (fully tested and validated)
```
