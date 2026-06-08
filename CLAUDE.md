# Overthink — The Candy Factory for You and Your Agents

Secure the box, then fill it with the whole candy store: compose, build, deploy, and manage **boxes** (container images) from a library of fully configurable **candies** (layers). Built on a generic init system framework (`build.yml` → `init:` section) and `ov` (Go CLI). Designed to work equally well from the command line and from AI agents like Claude Code — for you *and* your agents. Supports both Docker and Podman.

See `VISION.md` for the long-term thesis and direction, `README.md` for the user-facing feature overview and command reference, `plugins/README.md` for the full skill index. This file carries only **project-specific rules and mandates** — architectural and usage detail lives in skills (the single source of truth). The full five-way doc split is in **Where things are documented** at the end.

**How to read this file:** R0 (skills first) → the philosophy pillars (the *why*) → the Ground Truth Rules R1–R10 (the hard gates) → the cutover + post-execution process (how a change lands) → Key Rules (the technical-rule index) → AI Attribution. Every section states the rule once and points to the skill that owns the operational detail.

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
| `charly update` / `charly vm *` / VM entities in `vm.yml` or `vm:` | `/charly-vm:vm` + `/charly-internals:vm-deploy-target` |
| `charly deploy add/del` / pod or container deploys | `/charly-core:deploy` |
| local-target deploy / `target: local` / `host: local` (default) / SSH-host deploys / `user:` / `ssh_arg:` | `/charly-local:local-deploy` + `/charly-internals:local-infra` |
| Editing `local.yml` / authoring `kind: local` templates | `/charly-local:local-spec` |
| Managed `~/.config/charly/ssh_config` fragment / `charly vm create` writes Host stanza | `/charly-vm:vm` + `/charly-local:local-deploy` |
| `charly eval run <bed>` (kind:eval R10 bed) / authoring `kind: eval` beds in `eval.yml` / `charly eval live` / `charly eval cdp/wl/dbus/vnc/mcp/record/spice/libvirt` | `/charly-eval:eval` |
| Agent Driven Development (ADD) / `charly box feature run` / `charly eval feature run` / `charly feature list/pending/validate` / `charly candy add-scenario` / `description:` Gherkin scenarios / the agent grader for prose steps | `/charly-eval:eval` + `/charly-internals:strict-policy` |
| `charly eval k8s <verb>` / cluster probes | `/charly-kubernetes:eval-k8s` |
| `charly eval adb <method>` / Android Debug Bridge from host (devices, shell, install, getprop, screencap, logcat, wait-for-device) | `/charly-eval:adb` + `/charly-eval:eval` |
| `charly eval appium <method>` / Android UI automation / W3C WebDriver / APK install via mobile:installApp / session lifecycle / element introspection (get-text/get-attribute/clear/find-all/source) / per-class sugar groups (`gesture-*`/`app-*`/`key-*`/`device-*`) / generic WebDriver escape hatch (`execute`/`raw`) | `/charly-eval:appium` + `/charly-eval:eval` |
| `kind: android` device / `target: android` deploy / `apk:` package format in layers / installing Android apps declaratively / remote-or-emulator adb endpoint / nested `pod → android` | `/charly-eval:android` + `/charly-core:deploy` |
| Editing `candy.yml`, candy authoring, candy tasks/services | `/charly-image:layer` |
| Editing `box.yml`, box composition | `/charly-image:image` |
| `charly box build` / `charly box generate` / Containerfile | `/charly-build:build` + `/charly-build:generate` + `/charly-internals:generate-source` |
| `charly box validate` / schema error | `/charly-build:validate` |
| `charly clean` / build-artifact retention / `keep_images` / `keep_eval_runs` / image-tag pruning / `.eval` run cleanup | `/charly-core:clean` |
| Secret management / `charly secrets` / Secret Service / GPG `.secrets` | `/charly-build:secrets` |
| `charly migrate` / schema migration / legacy → latest CalVer / CalVer schema version | `/charly-build:migrate` |
| Git/`gh` workflow — `feat/` branch, commit, push, ff-merge to main, tag, worktree, sync-to-upstream, branch/worktree prune, PR create, `gh` approve/merge, cross-repo R10 landing | `/charly-internals:git-workflow` |
| `charly box reconcile` / cross-repo `@github` pin alignment / layer-version-mismatch cleanup | `/charly-build:reconcile` |
| Hard-cutover concerns / rename sweeps | `/charly-internals:cutover-policy` |
| Engineering-discipline triggers (failure surfaced / dup pattern / ad-hoc fix tempting / "out of scope" framing) | `/charly-internals:strict-policy` |
| Disposable-flag semantics / `disposable: true` authorization | `/charly-internals:disposable` |
| Preemptible-flag / `requires_exclusive:` / `charly preempt` / exclusive host-resource arbitration (GPU passthrough contention) | `/charly-internals:disposable` + `/charly-core:deploy` |
| Go source work (adding/modifying `ov` commands) | `/charly-internals:go` |
| IR / InstallPlan / DeployTarget / OCITarget | `/charly-internals:install-plan` |
| OCI labels / capabilities contract | `/charly-internals:capabilities` |
| VmSpec / libvirt / cloud-init / OVMF internals | `/charly-internals:vm-spec` (+ renderer skills as needed) |
| Unexpected failure / error / anomaly | `/charly-internals:root-cause-analyzer` agent (BEFORE any fix) |
| "What does candy X do?" / "What's in box X?" — pod-specific | `/charly-jupyter:<name>`, `/charly-coder:<name>`, `/charly-selkies:<name>`, `/charly-openclaw:<name>`, `/charly-ollama:<name>`, `/charly-openwebui:<name>`, `/charly-comfyui:<name>`, `/charly-immich:<name>`, `/charly-hermes:<name>`, `/charly-filebrowser:<name>` |
| "What does candy X do?" / "What's in box X?" — base distros / GPU runtime / bootc | `/charly-distros:<name>` (archlinux, fedora, debian, ubuntu, cachyos, nvidia, cuda, rocm, bootc-base, …) |
| CachyOS images / `cachyos*` / `ov-cachyos` workstation profile / `image/cachyos` submodule | `/charly-distros:cachyos` + `/charly-vm:cachyos` + `/charly-local:ov-cachyos` |
| Debian images / `debian*` / `image/debian` submodule | `/charly-distros:debian` + `/charly-distros:debian-builder` + `/charly-distros:debian-debootstrap` + `/charly-coder:debian-coder` + `/charly-vm:debian` |
| Ubuntu images / `ubuntu*` / `image/ubuntu` submodule | `/charly-distros:ubuntu` + `/charly-distros:ubuntu-builder` + `/charly-distros:ubuntu-debootstrap` + `/charly-coder:ubuntu-coder` + `/charly-vm:ubuntu` |
| Fedora images / `fedora*` / `image/fedora` submodule / `fedora-base.yml` | `/charly-distros:fedora` + `/charly-distros:fedora-builder` + `/charly-distros:fedora-nonfree` + `/charly-coder:fedora-coder` + `/charly-distros:fedora-ov` + `/charly-distros:fedora-test` |
| bootc images / `bazzite` / `aurora` / `*-bootc` / `image/bootc` submodule | `/charly-distros:bazzite` + `/charly-distros:aurora` + `/charly-distros:bootc-base` + `/charly-vm:vm` |
| "What does candy X do?" — language runtime | `/charly-languages:<name>` (python, python-ml, pixi) |
| "What does candy X do?" — infrastructure service | `/charly-infrastructure:<name>` (postgresql, redis, k3s, traefik, supervisord, tailscale, gocryptfs, virtualization, dbus-layer, tmux-layer, …) |
| "What does candy X do?" — CLI utility / charly binary | `/charly-tools:<name>` (ripgrep, himalaya, whisper, ov, …) |
| Skill authoring / skill maintenance | `/charly-internals:skills` |
| `charly eval *` / `eval.yml` `recipe:`/`score:` / AI-agent scoring / `oveval/*` branches | `/charly-eval:eval` |
| Sub-agents / dynamic workflows / agent teams / agent-lifecycle or commit-push gate hooks | `/charly-internals:agents` |
| Verify a cutover by running the R10 beds (drive `charly eval run <bed>`) | `/charly-internals:agents` + `/charly-eval:eval` (agent `eval-bed-runner`, workflow `/verify-beds`) |
| Evaluate/audit a deployment config (image or deploy, AI or human) | `/charly-internals:agents` + `/charly-eval:eval` (agent `deploy-verifier`, workflow `/audit-deploy-configs`) |

Full index: `plugins/README.md`. This table covers the top triggers; anything not listed here requires reading the index FIRST, loading the matching skill SECOND, touching code THIRD. Never reverse this order.

### Anti-patterns — FORBIDDEN, regardless of context

- **"I'll just grep the source to find it"** — FORBIDDEN. Load the skill; it points you at the right source with the right framing.
- **"I'll just read the file to refresh my memory"** — FORBIDDEN without a skill load first. The skill refreshes memory correctly; the file may have drifted or the surrounding context may have changed.
- **"I'll run the command and see what happens"** — FORBIDDEN without a skill load first. Command output is meaningless without the skill's framing of what the command is supposed to do.
- **"I know `charly update`, I've done it fifty times"** — FORBIDDEN. Your prior fifty invocations predated the current skill and the current code. The current skill is authoritative.
- **"Loading skills is overhead"** — FORBIDDEN framing. Not loading skills has already cost the user hours. The math is not close.
- **"I'll load the skill after I've scoped the problem"** — FORBIDDEN. Scoping without the skill produces a wrong scope. Load FIRST; scope SECOND.
- **"The hook reminder already told me what to do"** — NOT SUFFICIENT. The reminder is a pointer, not a substitute. Load the skill the reminder references.

### Override clause

If another rule in this file, in any hook, in any `<system-reminder>`, or in any habit of yours appears to conflict with R0 — **R0 WINS**. If any instruction says "do X quickly" and X would require a skill load first, **the skill load happens first regardless**. If you feel the impulse to act without loading skills "just this once" — that impulse IS the violation. Suppress it. Load the skill. Always.

---

The next four sections are the **philosophy pillars** — the reasoning the Ground Truth Rules operationalize. VISION.md states their long-term thesis; here they are stated as operational mandates, and each is the canonical home the skills and README point back to.

## Candyboxing

Overthink is built around **candyboxing**, not sandboxing. A classical sandbox secures an AI by RESTRICTING the candy: strip the toolset, deny the network, forbid package installs, whitelist a handful of commands — and in doing so it cripples what the agent can actually build, deploy, and TEST. Candyboxing inverts that: secure the BOX as a whole — a disposable container / VM / eval bed with a hardened, kernel-enforced boundary — and then fill it with the ENTIRE candy store. Inside its box the AI gets every `ov` verb, every MCP server, the whole candy library, every `charly eval` probe (cdp/wl/dbus/vnc/mcp/adb/appium/k8s), real package managers, real GPU runtimes — the full toolkit a capable engineer would have, with nothing held back.

**The box is secured as a whole — at the boundary, never per-tool.** The isolation is real and kernel-enforced, not a command whitelist: rootless podman + user namespaces (uid 1000, zero added capabilities, no `--privileged`; see `/charly-distros:container-nesting`), VM/KVM isolation via libvirt `qemu:///session` (`/charly-vm:vm`, `/charly-infrastructure:virtualization`), gocryptfs-encrypted volumes with keyring-isolated keys (`/charly-infrastructure:gocryptfs`, `/charly-automation:enc`), tailscale-scoped networking (`/charly-infrastructure:tailscale`), and the `disposable: true` lifecycle boundary that makes destroy + rebuild fearless (`/charly-internals:disposable`). You don't trust the tools; you trust the walls.

**Why a full candy store, not a locked cabinet.** An AI that cannot install a package, reach a registry, build an image, or run a real deploy cannot VALIDATE a real composition — it can only guess. Candyboxing is what makes Risk Driven Development cheap and honest: the bed is fully stocked, so the AI builds the actual box, deploys the actual candies, and `charly eval`s the actual running system (RDD), and it is disposable, so a wrong move costs one `charly update`, not an incident (Disposable-Only Autonomy). The generosity is the point — it is what "Overthink" means: hand the agent an over-provisioned environment, not a minimal one, and contain it at the boundary.

**Candyboxing composes with R0 and the safety rules — it does not loosen them.** A full candy store inside the box is NOT permission to act outside it: autonomous destroy is still gated on an explicit `disposable: true` (Disposable-Only Autonomy), outward-facing / hard-to-reverse actions still require authorization, and skills-first (R0) still governs HOW the AI uses the candy. Candyboxing widens what the AI may freely reach for INSIDE a secured, disposable boundary; it never widens the boundary itself.

See `/charly-internals:disposable` (the lifecycle boundary), `/charly-eval:eval` (the `charly eval` candy store + the disposable bed), and `/charly-internals:agents` (agents and teams working inside the box).

## Risk Driven Development (RDD)

Overthink is built around **Risk Driven Development (RDD)**: ALWAYS validate ANY HIGH-RISK assumption empirically against the live system — NEVER accept the skills, CLAUDE.md, or the current code as automatically correct. Documentation drifts and code has bugs; for a high-risk decision, **reality is the only ground truth.** Prove it on a real `disposable: true` bed in the PLANNING / EARLY-CODING phase, before the design commits to it. The discipline is *never trust, verify*.

**RDD and skills-first (R0) compose — they do not conflict.** R0 governs where you START: load the matching skill first, trust it over your stale training, never grep blind. RDD governs what you accept as PROVEN: a skill or code passage is the best available HYPOTHESIS, not a settled fact, when the stakes are high. You still load the skill first (R0); you just don't bet a plan on a high-risk claim until a bed confirms it. When the bed contradicts the doc, the DOC IS STALE — fix it (skills are living documents).

**Risk — not documentation status — is the trigger.**

- **Low-risk / recoverable** (orientation: "roughly what does this candy do"): the skill lookup suffices. Do NOT burn a bed on it, and do NOT add defensive complexity "to be safe" — that over-caution is failure mode 2 below.
- **High-risk** (being wrong invalidates the plan, is costly or hard to reverse, or would send RCA down a false trail): validate it on a live bed REGARDLESS of what a skill, CLAUDE.md, or the code asserts.

| Assumption | Risk if wrong | How RDD settles it |
|---|---|---|
| "Roughly what does candy X do?" (orientation) | Low, recoverable | Skill lookup (R0) — no bed |
| "Candy X behaves EXACTLY as documented, and my plan depends on it" | High | Validate on a live bed — the skill may be stale |
| "The code does X, so my change is safe" | High | Run it — code has bugs; the emitted artifact / live run is the arbiter (R8/R9) |
| "These candies, at their latest versions, compose & run together" | High (no skill can certify) | Build + deploy + `charly eval` EARLY |

**The archetypal high-risk unknown: composition.** The single highest, least-documented risk in a candy-composition system is whether a SPECIFIC combination of candies — especially at the LATEST currently-available versions the resolver picks (newest-wins) — actually builds, deploys, and reaches steady-state TOGETHER. No skill can certify a never-composed combination. Build it, deploy it, and `charly eval` it EARLY, before the plan rests on the assumption that it works.

**RDD prevents three failure modes:**

1. **A wrong high-risk assumption baked into the design** — "the skill says X" / "the code does Y" / "these candies compose" / "the newest version is drop-in" treated as proven; every task built on it inherits the defect when reality differs from the stale doc or buggy code.
2. **Unnecessary caution / over-engineering** — guards, fallbacks, or pinned-back versions added against a danger a real check would have disproven. (For a low-risk item, spinning up a bed at all is the same waste in process form.)
3. **Erroneous root-cause analysis** — diagnosing from speculation or from a stale doc / code reading instead of a real bed run. RDD front-loads the evidence so R1's RCA reasons from a real failure, not a guess.

R1 is reactive (RCA after failure), RDD is proactive (prove the riskiest unknown first), R10 is the final proof (fresh rebuild on a `disposable: true` target). Same *never trust, verify* discipline at three points in time. See `/charly-internals:strict-policy` and the `/charly-internals:root-cause-analyzer` agent.

The cheapest moment to discover a doc is stale, code is buggy, or a composition breaks is before you build the plan on it — on a disposable bed, not after commit.

## Agent Driven Development (ADD)

Overthink is built around **Agent Driven Development (ADD)**: every entity's intended behaviour is captured as executable Gherkin scenarios (a `description:` block — Feature / Narrative + Given/When/Then steps), authored by you OR your agents, baked into the image as the `ai.opencharly.description` OCI label, and verified on every build. The spec IS the test; agents are first-class AUTHORS of it and first-class GRADERS of it. ADD is the canonical Gherkin acceptance-testing pattern, named for the agent that drives it — the natural fit for a system built "for you and your agents".

**The binding contract — a step binds to its verifier BY SHAPE.** A scenario step that embeds a check verb (`file:`/`http:`/`cdp:`/`mcp:`/`command:`/…) binds to a DETERMINISTIC check the runner executes. A prose-only step (a `then:` with no verb) binds to an AGENT: `charly eval feature run <deployment>` spawns the configured `kind: ai` CLI, which probes the live deployment with the full `charly eval` surface and returns a pass/fail verdict with evidence (an unparseable/timed-out grader FAILS the step — never a silent pass). No glue code: the "step definition" is either a declarative check or an agent.

**The lived loop — Specify → Bind → Run → Iterate → Bake → Gate.**
1. **Specify** — author the goal + scenarios on the CANDY that provides the behaviour: `charly candy add-scenario <layer> <name> --given/--when/--then` (idempotent; auto-exposed as the `candy.add-scenario` MCP tool) or edit the `description:` block.
2. **Bind** — embed a check verb (deterministic) or leave the step prose (agent-graded). `charly feature pending <entity>` lists the still-prose steps (the authoring gaps).
3. **Run** — `charly box feature run <image>` (build scope: deterministic steps against a disposable container; prose steps report unbound) or `charly eval feature run <deployment>` (deploy scope: deterministic + agent-graded prose; `--no-agent` for deterministic-only CI).
4. **Iterate** — drive red→green by hand, OR autonomously: `charly eval run <score>` is the plateau-bounded AI loop that writes the implementation until the scenarios pass (the deepest sense of "agent-driven").
5. **Bake** — `charly box build` bakes goal + scenarios into `ai.opencharly.description`; the artifact carries its own runnable acceptance spec (source-less `charly box`/`charly eval feature run` against a pulled image).
6. **Gate** — `charly eval run <bed>` runs the bed image's deterministic scenarios as an opt-in acceptance gate (a no-op PASS when none are authored).

**ADD composes with RDD, R10, and candyboxing — it does not duplicate them.** RDD proves the risky ASSUMPTIONS a behaviour rests on; ADD specifies WHAT the correct behaviour is and drives (human or agent) to it; R10 proves it on a fresh rebuild. Three points on the same *never trust, verify* arc — RDD before the edit, ADD as the spec, R10 as the final proof. The agent grader runs inside the secured, disposable box (candyboxing) with the full `charly eval` probe surface.

**ADD prevents three failure modes:** (1) ambiguous acceptance — "done" with no executable definition of correct behaviour; (2) prose that never runs — a `then:` that documents intent but verifies nothing (the agent grader makes free-form behaviour executable); (3) per-box test drift — scenarios live on the CANDY that provides the behaviour, so ONE scenario covers every box that composes the candy (no per-box copy — R3).

ADD is a co-equal pillar with RDD and an OPT-IN runnable gate: where an entity authors scenarios they run and must pass; where it authors none, nothing is forced. See `/charly-internals:strict-policy` "ADD" and `/charly-eval:eval`.

## Prioritize Clean Architecture Above All Else

Always pick the cleanest long-term approach and prioritize having a clean codebase with any deprecated code fully removed above everything.
You have all the time in the world and taking the time to get things properly done is ALWAYS worth the effort.

The architectural framing of the procedural rules — both framings binding:

- **No duplication on first surface.** Refactor to ONE shared abstraction the moment the same pattern would land in a second place. Sibling-layer naming (`<name>-host`, `<name>-pod`), parallel filter functions, and per-call-site re-implementations are the canonical anti-patterns. Procedural rule R3.
- **Generic over ad-hoc.** Every fix applies cleanly to ALL surfaces it logically covers, never just the one that prompted the report. Procedural rule R3.
- **No workarounds.** Sleep loops, retry-on-flake, magic-number tuning, "works on my machine" fixes are forbidden at the architectural level too — if a race exists, the fix is the synchronization primitive, not a delay. Procedural rule R4.

See `/charly-internals:strict-policy` for the operationalization of R3–R4 (the forbidden-pattern catalog and the worked examples).

---

## Ground Truth Rules — NEVER claim success without these (HARD RULES)

These rules exist because (a) failing tests have been deferred as 'pre-existing' and quietly papered over later; (b) duplicated patterns crystallized into divergent surfaces because no rule named the duplication on day one; (c) green unit tests have been claimed as cutover-complete while the actual image failed to start. Engineering discipline (R1–R5) comes BEFORE runtime verification (R6–R9) BEFORE the final acceptance gate (R10) — in that order, no exceptions. R1–R5 are operationalized in `/charly-internals:strict-policy`; R10 lives in **Disposable-Only Autonomy** immediately below this block, so R1–R10 read together.

- **R1. Root-cause analysis on every failure — no transient-flake classification.** Every failure, error, anomaly, or warning surfaced by ANY tool (build, test, validator, runtime, eval, deploy, lint, hook) triggers IMMEDIATE invocation of `/charly-internals:root-cause-analyzer` BEFORE any remediation attempt. Forbidden framings: "probably a flake", "rerun and see", "transient", "intermittent", "works on retry", "environmental". The first occurrence is the investigation trigger; there is no second-occurrence threshold. If the analyzer concludes the root cause is genuinely external (network partition, upstream outage), the conclusion is documented in the conversation with evidence — never assumed. Blind retry of a failed command is itself a violation. **A warning is not a pass:** R10 is successful ONLY at ZERO warnings (resolver newest-wins, build, `charly box validate`, `charly eval`, deploy). Every warning is fixed before R10 passes — a version-mismatch warning is cleared with `charly box reconcile`; any other warning triggers the analyzer then a real fix. A surviving warning is an R10 failure, never an accepted end state. See `/charly-internals:strict-policy`.

- **R2. No "pre-existing" / "out of scope" / "unrelated" / "follow-up PR" classifications.** Every issue surfaced during the active cutover — failing test, validator warning, runtime crash, deprecated-marker hit, dead-code reference, stale doc paragraph — is fixed in the SAME working tree as the cutover (the default — the AI fixes what it finds without asking), or — only when the issue is itself a genuine crossroad the AI cannot resolve from the request, code, skills, or sensible defaults — escalated to the operator for explicit re-scoping. The classifications "pre-existing", "unrelated to this change", "out of scope", "follow-up PR", "tracked separately", "we'll get to it later" are FORBIDDEN. **Blocking vs non-blocking — the ONE legitimate way an issue leaves the current cutover.** Classify every surfaced issue. A **blocking** issue — the current change is incorrect, incomplete, or unsafe without it — is fixed in the SAME working tree and proved under the CURRENT cutover's R10. A **non-blocking** issue — the current change is correct AND complete without it, and it is genuinely separable from this change — is STILL fixed immediately, but as its OWN cutover with its OWN full R10, opened the moment the current cutover is R10-passed and committed; it is NEVER parked as an indefinite "follow-up / someday" (that stays forbidden). The discriminator: *would shipping the current cutover WITHOUT this fix leave the tree correct and the cutover's claim true?* Yes → non-blocking (its own immediate-next cutover); No → blocking (this cutover); unsure → treat as blocking. **Objective test for "separable":** the current cutover's OWN R10 (its eval-coverage + fresh-rebuild) passes and proves the cutover's claim WITHOUT the fix — the fix is neither exercised by nor changes the verdict of this cutover's test coverage; a fix that would alter this cutover's R10 result or eval-coverage gate is BLOCKING. Mislabeling a blocking issue "non-blocking" to ship faster, or carving the current change's OWN scope into two, is the forbidden split — a genuinely separate concern getting its own cutover is not. (See `CHANGELOG.md` for the incident that motivated this rule.) See `/charly-internals:strict-policy`.

- **R3. No code duplication; generic, reusable solutions over ad-hoc patches.** On the FIRST surface where the same pattern, predicate, filter, transform, or guard appears in two places, refactor to ONE shared abstraction in the SAME working tree. Sibling-layer naming (`<name>-host`, `<name>-pod`), parallel filter functions, and per-call-site re-implementations of the same predicate are FORBIDDEN. Every fix MUST apply cleanly to ALL surfaces it logically covers, not just the surface that prompted the report. Generic > ad-hoc, every time. (See `CHANGELOG.md` for the worked examples that motivated this rule.) See `/charly-internals:strict-policy`.

- **R4. No ad-hoc workarounds — sleep loops, retry-on-flake, magic-number tuning, "works on my machine" fixes are FORBIDDEN.** Forbidden patterns: `sleep 5; retry`, `for i in 1..3 do try; done`, hardcoded port numbers chosen because "8080 was busy", environment-specific paths, default-fallbacks that hide a missing config, "this is what worked when I tried it locally". If a race or timing dependency exists, the fix is the synchronization primitive (file lock, readiness probe, condition variable, deterministic ordering), NEVER a sleep. If a value is magic, it is named, sourced from config, and validated on load. If a fix only works on one machine, it is not a fix — it is a bug report. See `/charly-internals:strict-policy`.

- **R5. Hard cutover: deprecated path AND every stale reference deleted in the same change.** When a cutover introduces a replacement, the SAME commit deletes (a) the deprecated code path, (b) every comment / TODO / DEPRECATED marker referencing the old path, AND (c) every reference, comment, docstring, error message, skill paragraph, migration help-text, test fixture, or hook string naming a deleted identifier. After commit, `git grep '<deleted-id>'` returns ONLY historical mentions in `CHANGELOG.md` or migration help-text. Deleting `box.yml` while the new `charly.yml` path silently skips a build stage is not a clean cutover — it's a regression masked by the old file's absence. The acceptance test of a cutover is: rebuild from the new config, run the resulting image, observe the service reach steady-state, AND verify zero stale references via the grep self-test. See `/charly-internals:strict-policy`.

- **R6. Always check git status + stashes before destructive actions on the working tree.** `git stash` discards in-progress work; `rm` on a tracked file is destructive. If the sandbox blocks an action, read the reason and find a non-destructive alternative — do not work around it with a cleverer command.

- **R7. Unit tests never substitute for runtime verification — mandatory end-to-end gate.** A green `go test ./...` means the code compiles and fixture loaders work — nothing about whether the produced artifact behaves correctly. For any change that can affect Containerfile generation, OCI labels, init systems, service startup, or deploy code, the minimum sequence applies BEFORE "done":
  1. `charly box build <image>` — build a concrete image (not just generate Containerfile).
  2. `charly eval box <image>` — baked layer + image sections pass (NB: passes on zero-content stages too — not a substitute for R8).
  3. `charly start <image>` (or `charly deploy add <image> <image>` / `charly update <image>` for an existing deploy) — container must reach `Active: active (running)`.
  4. `charly eval live <image>` — full three-section run including deploy probes must pass.
  5. If any step fails, the task is NOT done — invoke R1's RCA mandate.

  A container that crash-loops on `supervisord: PermissionError: /var/log/supervisor/supervisord.log` exposes what no unit test would.

  **Which eval verb for R10 — pick by what you're proving:**
  - `charly eval box <image>` — build-scope invariants only (binary / package presence) in a disposable `podman run --rm`. No deploy, no live runtime state.
  - `charly eval live <name>` — deploy-scope probes against an ALREADY-running deployment you brought up yourself.
  - `charly eval run <kind:eval-bed>` — the WHOLE sequence above (steps 1-4) automated on a disposable bed: build → eval image → deploy → eval live → fresh update (the R10 acceptance gate) → tear down. **This is the canonical R10 gate.** Pick the bed whose kind matches what you changed — `eval-pod` (the combined image/layer/pod/DeployTarget mechanism bed) / `eval-local` / `eval-k3s-vm`, or a feature bed like `eval-android-emulator-pod`. `charly eval run --all-beds` runs every bed (name-sorted). Beds are `kind: eval` entities in `eval.yml`; `disposable: true` is the sole authorization for the unattended destroy+rebuild.
  - `charly eval run <kind:score>` — the multi-hour AI-iteration benchmark, NOT a quick gate. The same `charly eval run` verb dispatches by the kind the name resolves to.

  **`charly eval` exit codes** (goss/pytest-style; scripts and R10 automation rely on this): `0` = all checks passed; `1` = command/usage/infra error (the eval never ran a verdict — bad args, container not running, build/deploy/vm-create failed); `2` = the eval RAN and one or more **checks FAILED**. `charly eval box`/`live` return `2` on check failure; `charly eval run <bed>` propagates `2` when the bed's eval step fails but `1` for an infra step. Do NOT treat exit `1` as "tests failed" — that's a setup error; only exit `2` means the thing under test is broken. See `/charly-eval:eval` "Exit codes".

- **R8. Generated-artifact invariants — Containerfile sections AND OCI labels verified.** When a refactor touches generation, assert the presence of every critical section in the emitted Containerfile (e.g. `grep supervisord-conf .build/<image>/Containerfile`). A Containerfile that compiles but silently drops the init-system stage produces an image with the **stock RPM config**, not the opencharly config — and the stock config almost always breaks at runtime. The emitted file is the source of truth; check it. After `charly box build`, `podman inspect --format '{{index .Config.Labels "ai.opencharly.init"}}'` must return the expected value for every capability label the image claims. An empty or missing label usually means a detection path silently returned nil. Treat missing labels as a failure, not a warning.

- **R9. Deployed binary matches source AND runtime deps declared in package management.** Syncthing / git / rsync move *source* between hosts; they don't rebuild the binary. After pushing code, explicitly rebuild on the target and verify `charly version`. If the version is old, the fix under test isn't really under test. A change that relies on an OS package at runtime (`nc`, `socat`, `xorriso`, `qemu-guest-agent`, …) MUST add that package to `pkg/arch/PKGBUILD` `depends=` (the single source of truth). A manual install on one host is a bug report disguised as a fix. (See `CHANGELOG.md` for the war-stories that motivated this rule.)

See `/charly-eval:eval` "DO NOT fake success" section for the mandatory sequence applied to test authoring specifically. See `/charly-internals:strict-policy` for the operationalization of R1–R5.

## Disposable-Only Autonomy + Mandatory Live-Deploy Verification

**`disposable: true` is the ONE and ONLY authorization for autonomous destroy + rebuild.** Default is `false` (explicit opt-in only; see `/charly-internals:disposable`). No derivation from other fields. No "this looks like a test bed" heuristic. No hostname-based assumptions. A deploy is either explicitly marked `disposable: true` in deploy.yml or it is NOT rebuildable unattended — even if its name contains "test", even if it's a project on a shared host where unrelated production services also run. Explicit-only is what makes this rule safe on shared infrastructure with live users on other resources.

On resources that ARE marked `disposable: true`, `charly update <name>` performs destroy → (optional image rebuild) → create → start unattended, and is the preferred path. Hesitating to rebuild a disposable target when verification demands it is the OPPOSITE failure mode, and the one that leads to claimed-but-unverified fixes.

**Every change is proved on a freshly built binary on the target host** (the 10 evaluation standards in `/charly-eval:eval`):

1. Build the artifact from the changed source, on the target host.
2. Verify the deployed binary's version matches what you built (R9).
3. Verify runtime deps are installed via package management (R9).
4. For a target with `disposable: true`: `charly update <name>` — unattended. For any other resource: confirm with the user before any destroy.
5. Exercise the feature end-to-end.
6. Paste the runtime output back into the conversation.
7. Leave the target healthy (running, not paused, not crashed).
8. **After committing the source-level fix, `charly update` the disposable target from clean and re-run the full sequence. This fresh-rebuild re-verification is the acceptance gate** (R10).

### R10 — "Verify on a `disposable: true` target; prove it on a fresh rebuild"

The verification loop has three rules:

1. **Always test on a target that carries an explicit `disposable: true`.** Never experiment on a resource without the flag. If no suitable disposable target exists, create one first (`charly deploy add <name> <ref> --disposable` or mark a VM entry under `vm:` in deploy.yml and `charly vm create`). The opt-in is explicit; never assume disposability because of a name, lifecycle tag, hostname, or any other heuristic.
2. **If a test breaks the target, `charly update` it back to the committed config before doing anything else.** Never layer experiments on broken state.
3. **After committing the real fix in source, re-verify on a FRESH `charly update` of the disposable target.** A fix that passes only on a hand-patched target is not a real fix — it's a regression waiting for the next rebuild. Pasteable proof of the fresh-rebuild re-verification is the acceptance gate.

**A `--dry-run` does NOT count as an R10 test.** Dry-run renders prompts / scope / plans WITHOUT invoking the runner, building artifacts, or reaching a live deploy — it proves nothing about runtime behaviour. R10 requires a FULL live run of every new or changed code path: real subprocess invocation, real container build, real deploy probes against the running target, real verb evaluation against the live system. Validators, unit tests, and dry-runs are pre-flight checks, NOT the acceptance gate. If the cutover added or changed N pieces of functionality, R10 must exercise all N end-to-end on the disposable target — pasteable runtime output for each.

**An eval-sandbox (or any disposable target) REBUILD by itself does NOT count as an R10 test either.** The rebuild is preflight setup. R10 means the cutover's NEW or CHANGED code path — the runner / AI loop / verb evaluation / subprocess — actually executed AGAINST that fresh target and produced output you pasted. If the runner never ran, you do NOT get to claim `analysed on a live system`; the correct tier is `syntax check only` paired with explicit "R10 not yet run, awaiting authorization for the live round" — and pairing `syntax check only` with a commit is itself a violation, STOP and ask.

**Editing or deleting a task to retroactively redefine R10 is FORBIDDEN (see `CHANGELOG.md` for the attribution-fraud incident that motivated this).** R10 has ONE definition. `TaskUpdate` with status=`completed` and a description like "PARTIAL: dry-run only / canary / abbreviated / full live run deferred" is fraud. Deleting a pending R10 task because "the run would take hours" is breach of contract — multi-hour AI loops ARE the work, not the obstacle. Session-budget concerns NEVER downgrade R10 — they are the cost of doing business. If R10 genuinely cannot complete, SAY SO PLAINLY in your final message, do NOT commit anything (main repo OR submodule), do NOT trade tier for cycles. The user authorized R10 in scope; you deliver R10 in scope or you escalate, never both downgrade and ship silently.

**Score `eval.yml` config IS the test specification. CLI flag overrides require explicit user authorization in the SAME conversation turn (see `CHANGELOG.md` for the test-spec scope-shrink incident that motivated this).** Passing `--plateau-iteration`, `--max-scenario`, `--tag`, `--skip-rebuild`, `--on-pod`/`--on-vm`/`--on-host`, `--keep-repo`, `--dry-run`, OR the kind:eval bed flags `--no-rebuild` (skips the R10 fresh-rebuild gate) / `--keep` / `--all-beds` to `charly eval run` (or `charly eval live`) without the user explicitly saying "use --flag X" THIS turn is the same fraud class as dry-run-as-R10. Internal-voice triggers — "tractable wall-clock", "for the canary", "to fit session bounds", "shorten this run", "skip the heavy leg", "faster iteration cycle" — are confessions, not defences. Run the test AS SPECIFIED in the score config; the operator authorizes overrides, not Claude. The score's `plateau_iteration` and the AI's `progress_no_improvement_timeout` together define the AI's recovery budget per phase; do not narrow either without explicit authorization.

Before saying "done", run the unified **Acceptance checklist** in **Post-Execution Policies** below (it merges end-of-turn verification with the landing gate). See `/charly-eval:eval` for the 10 evaluation standards and `/charly-internals:disposable` for the classification schema.

---

## Hard Cutover by Default — ONE PHASE, test EVERYTHING at the end

**Every refactor, schema change, API rename, or deprecation ships as ONE PHASE — hard cutover, no intermediate coexistence, no "I'll verify this bit now and the next bit later". Multi-phase rollouts that split a single refactor across conversation turns leave the system half-migrated and un-testable. That is FORBIDDEN.** `/charly-internals:cutover-policy` is the full source of truth (forbidden patterns, required deliverables, the anti-pattern catalog); this section is the mandate.

**What this policy forbids — precisely:**

- **Committing intermediate states.** No `git commit` of a half-migrated tree. The cutover is ONE atomic commit — schema changes + code edits + migration command + fixture updates + skill-doc updates land together.
- **Verifying / claiming success on an intermediate state.** A task marked "done" while any other task in the cutover is still open is a lie; the cutover isn't done until every task is done. Confidence attributions above `syntax check only` require R10 acceptance on the FINAL code.
- **Splitting one cutover across conversation turns.** ABSOLUTELY FORBIDDEN, with NO exception — see the **No exception clause** below.
- **Authorizing a commit from an intermediate-state run.** The commit is gated on the full live test of EVERYTHING against the FINAL code, pasted. A bed run that passes on an *intermediate* state does NOT authorize a commit — only the full final-code live test does. (Running the beds on intermediate states to *verify* is encouraged; what is forbidden is treating such a run as the commit gate.)

**What this policy permits — equally precisely:**

- **Intermediate in-memory states during implementation.** While editing, the working tree WILL naturally be uncompilable or partially migrated between edits. That's normal. Reach compile-clean between related edits if it helps track progress, but don't treat compile-clean as "done."
- **Transitional aliases / legacy-accepting paths DURING implementation.** Every one of them is DELETED before the cutover ends — but they can exist mid-flight to simplify the refactor.
- **Running `ov` to verify, at any stage, as often as useful.** `charly box build`, `charly update`, `charly eval run`, `charly vm create`, `charly start` against a `disposable: true` target — in parallel or in the background — are ENCOURAGED throughout the cutover. **Verify before you change — Risk Driven Development** (the proactive twin of R1): validate every HIGH-RISK assumption + error diagnosis on a live bed BEFORE editing, so you are never disproven hours later. Only the COMMIT is gated (on the full final-code test); running the beds to verify is not.
- **Cheap smoke-confirmation between tasks.** `go build` / `go test` / `charly box validate` after each task is good hygiene. It is NOT the acceptance gate. The acceptance gate is the FULL-STACK R10 run against the final code.

**Why R10 exists.** Full-stack R10 verification at the end of the cutover is not ceremonial — it's the ONLY way to catch issues that a complicated migration may have introduced: a migration command that missed a field, a struct rename that left a stale reference in a code path unit tests don't exercise, a candy composition that quietly produces a different effective image. Only a fresh `charly update <disposable>` + `charly eval live <disposable>` exercises every code path the cutover touched. R10 assumes the migration introduced unseen regressions and flushes them out.

**The workflow:** split into TASKS, not phases (N tasks ≠ N phases — a 15-task cutover is still ONE phase, ONE commit; marking a task `completed` is a TODO signal, never a commit signal). Implement all tasks in the same working tree (transitional aliases deleted before the end). Verify continuously, but gate the COMMIT on the full final-code R10. Fail the cutover if any verification fails — fix in the same tree, re-run everything, never "the rest is Phase 2." The single idempotent `charly migrate` command transforms legacy configs in-place; residual legacy fields raise hard load-time errors with a remediation hint. Full step-by-step: `/charly-internals:cutover-policy`; `charly migrate` surface: `/charly-build:migrate`.

**No exception clause — at planning time or at execution time.** There is no pre-approval split, no post-approval split, no phased rollout, no grace period, no "resume in the next session", no "author it as two plans" fallback. Plans are authored as full-scope, single-phase cutovers regardless of estimated time, scope, or context. Phase / scope / time concessions are FORBIDDEN at plan authoring AND at execution. Every cutover — regardless of estimated effort — runs as ONE phase in the SAME conversation through R10. ALWAYS push as far as you can. Compact context and continue, as many times as it takes. An approved plan is a CONTRACT; implement it as written. The ONLY valid stop conditions, at any stage, are (a) an error you cannot resolve that requires user input, or (b) the plan contradicts itself, CLAUDE.md, or a loaded skill — STOP and ask in either case; do NOT silently downgrade scope or commit a partial state.

**Anti-patterns that FAIL the cutover** — beyond the R2–R5 anti-patterns (pre-existing/out-of-scope classification → R2; band-aid duplication → R3; ad-hoc workarounds → R4; stale references after deletion → R5; all catalogued in `/charly-internals:strict-policy`), these are cutover-specific:

- Adding new interfaces alongside the old without deleting the old in the same change.
- "Transitional" alias tables that stay permanent because the rename sweep was deferred.
- Claiming "Phase 1 complete, Phase 2 pending" and pausing for user permission to continue mid-cutover.
- Writing fresh tests against one bed but skipping the rest "because it requires image builds".
- Declaring any confidence higher than `syntax check only` without a fresh-rebuild R10 re-verification on every affected target.
- **Committing — or claiming the cutover done — on the strength of an intermediate-state bed run.** The commit is gated on the full final-code live test (pasted); running the beds throughout to *verify* is encouraged — only the commit is gated, never the act of running `ov`.

## Post-Execution Policies — what happens AFTER R10 passes

These rules cover the gap between "R10 verified" and "user picks up the next task". Every step is sequential — do them in order. The deep landing mechanics (CalVer tag computation, multi-repo push order, the fork+PR path, `gh` approve/merge) live in `/charly-internals:git-workflow`; this section is the gate.

### After R10 passes (and only after)

1. **Commit.** ONE atomic commit covering the entire cutover — every Go edit, every YAML edit, every skill-doc edit, every new test, every deletion, in a single `git commit`. Multiple commits are FORBIDDEN for the same cutover (they re-introduce the intermediate-state problem). Use Conventional Commits with the `!` breaking-change marker for any cutover that removes a public API surface.
2. **AI attribution trailer.** EVERY commit ships with `Assisted-by: Claude (<confidence>)` at the tier the proof supports — see **AI Attribution** below. NEVER invent a higher tier than the proof supports.
3. **Auto-land the `feat/` branch — gated by R10, NEVER force-push.** The change was developed on a `feat/<slug>` branch off up-to-date `main` (see `/charly-internals:git-workflow`). The **R10 pass is the sole landing trigger** — there is no per-change human "push" step (this SUPERSEDES the older "push only if the user asked"). On R10 PASS, automatically and in order: (a) push `feat/<slug>`; (b) `git merge --ff-only feat/<slug>` into `main` (if `main` advanced, re-sync, rebase `feat/` onto it, and re-run R10 first); (c) tag the new `main` HEAD with a fresh `v<CalVer>` and push `main --follow-tags`; (d) delete `feat/<slug>` local + remote. NOTHING is ever pushed or merged on unverified state. **NEVER force-push** — no `git push --force`, no `--force-with-lease`, on ANY branch (`feat/` included) in ANY repo, ever; `main` only fast-forwards, tags are immutable/add-only. The whole flow is designed so a force push is never needed. Tag computation, multi-repo dependency order (submodule → `plugins` → superproject), cross-repo `@github` landing, and the no-write-access fork+PR path are all in `/charly-internals:git-workflow`.
4. **Eval-coverage gate.** R10 does not pass — and the change is NOT landable — unless it ships the test coverage that PROVES its new functionality (`eval:` checks for new/changed layers & images; Go tests for `ov` code) AND the live run exercised it. See R7/R10, `/charly-eval:eval`.
5. **Zero-warnings gate.** R10 is NOT successful while ANY warning remains — resolver newest-wins, build, `charly box validate`, `charly eval`, or deploy warnings. Each is fixed before R10 passes: a version-mismatch warning is cleared with `charly box reconcile`; any other warning triggers `/charly-internals:root-cause-analyzer` then a real fix. A surviving warning is an R10 failure, never an accepted end state (this is R1 made a hard gate).
6. **Working-tree cleanliness.** After commit, `git status` must be clean. Untracked files that aren't part of the cutover (test artifacts, build outputs) should already be in `.gitignore`; if they aren't, that's its own immediate-next cutover, not part of this one.
7. **Report.** Final message states: what was committed (commit subject + hash), confidence tier with the proof that supports it, and what was pushed. Pasted R10 output (both exploratory and fresh-rebuild) is part of the report.

### If R10 fails

R10 failure is NOT a stopping point — it's a return-to-implementation signal. The plan is not done.

1. **Run `/charly-internals:root-cause-analyzer` BEFORE attempting any fix.** Blind retry is FORBIDDEN. R10 caught a real regression; understand it first.
2. **Fix in the same working tree.** No "I'll address this in a follow-up PR" — the cutover policy explicitly forbids that. Fix + re-run R10 in the same conversation, against the same uncommitted tree.
3. **Re-run R10 from scratch.** Not just the failing piece — the FULL R10 against a fresh `charly update`. A fix that survives only the targeted re-run but breaks something else is a regression in waiting.
4. **Only commit when R10 passes end-to-end on the FINAL code.** No commits of half-fixed states.

### What is NOT post-execution

- **Folding new work INTO the current cutover** is FORBIDDEN — picking up "the next thing" mid-cutover re-creates a half-migrated state. But STARTING the next cutover is the AI's job, not something it waits on the user for. **Default to autonomous action:** the moment the current cutover is R10-passed and committed, the AI AUTOMATICALLY opens the next cutover to solve ANY issue it has found — whether this cutover surfaced it or not — each as its own atomic, fully-R10'd change. It does NOT queue routine work for authorization. The AI pauses to ASK only at a genuine **unexpected/unplanned crossroad** — a decision it cannot resolve from the request, the code, the loaded skills, or sensible defaults (a design choice with material trade-offs; a hard-to-reverse or outward-facing action without standing authorization; a contradiction between the plan and CLAUDE.md/skills; genuinely ambiguous requirements). Everything else it solves automatically and reports. A blocking issue is fixed IN the current cutover (never split out).
- **Backporting / cherry-picking.** Out of scope for the CURRENT cutover's post-execution flow — but it is its own atomic, fully-R10'd cutover the AI opens automatically when needed (it does not wait for a user follow-up), pausing only if the backport target or release strategy is a genuine crossroad.
- **Documenting "what would have been Phase 2".** The cutover either completed or it didn't. Phase 2 is a forbidden concept.

### Acceptance checklist

Before declaring the turn done — this single checklist merges end-of-turn verification with the landing gate. Every YES:

**Discipline & verification**
- [ ] RDD: every HIGH-RISK assumption proven EARLY on a `disposable: true` bed (above all whether this candy composition at its latest versions builds/deploys/runs together) — none carried into the final code on the strength of a (possibly stale) skill / CLAUDE.md / code reading alone? (Low-risk orientation is an R0 lookup.)
- [ ] `/charly-internals:root-cause-analyzer` ran on every failure / warning / anomaly observed during the session (R1)?
- [ ] Every issue surfaced during the session fixed in this cutover or explicitly escalated (R2)?
- [ ] `git grep` on every removed identifier returns ONLY `CHANGELOG.md` / migration-help-text context (R5)?
- [ ] Built a real artifact from the changed source, on the target host?
- [ ] Deployed binary's version matches what you built (R9)?
- [ ] Every runtime dep installed via package management (R9)?
- [ ] Feature exercised end-to-end on the live target?
- [ ] The change ships the test coverage that PROVES its functionality (`eval:` checks / Go tests) and R10 exercised it (eval-coverage gate)?
- [ ] Verification ran ONLY on targets explicitly marked `disposable: true`?
- [ ] If you broke the target during exploration, you `charly update`d it back to clean before continuing?

**Acceptance gate**
- [ ] R10 ran AGAINST THE FINAL CODE (not an intermediate state) on EVERY affected disposable target?
- [ ] Both exploratory and fresh-rebuild R10 outputs pasted into the conversation?
- [ ] Post-action state of every target is healthy (running, not paused, not crashed)?
- [ ] ZERO warnings remain (zero-warnings gate)?

**Landing**
- [ ] ONE atomic commit per repo (on the `feat/<slug>` branch), with the AI-attribution trailer at the tier the proof supports (no inflation)?
- [ ] Auto-landed on R10 PASS: `feat/` fast-forward-merged into `main`, `main` HEAD tagged `v<CalVer>`, pushed, `feat/` deleted — with NO force-push anywhere (no `--force` / `--force-with-lease`)?
- [ ] `git status` clean after landing; `feat/` branches pruned?
- [ ] No "Phase 2 / TODO / will do next time" deferred work surfaced in this plan?

## Agents, Workflows & Teams

Overthink is built to be driven from Claude Code's multi-agent primitives — **sub-agents** (`plugins/internals/agents/*.md`), **dynamic workflows** (`.claude/workflows/*.js`, run `/<name>`), and **agent teams** (experimental, enabled in the committed `.claude/settings.json` via `env.CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1`). **Full reference: `/charly-internals:agents`** (the three-primitive comparison, the bed-scoped parallel-testing model, long-running-bed handling, and the one-agent-⇄-one-bed ownership rules). This is the brief.

- **Prefer agents over background tasks.** Everything that CAN run as an addressable, operator-visible **sub-agent** or **agent-team teammate** SHOULD — never an opaque background workflow. Team agents are the DEFAULT for parallel work; a background dynamic `Workflow` is a LAST RESORT for deterministic scripted control flow a team can't express. The one exception is long-running work that outlives a turn (a VM/emulator eval bed): it runs as a harness-tracked background task owned by the persistent session and driven by the completion notification.
- **Agent roster** (`plugins/internals/agents/`) — *executors* run `charly eval` and return verbatim proof: `eval-bed-runner` (full `charly eval run <bed>`, R10 acceptance discipline), `deploy-verifier` (read-only `charly eval box`/`live` + `charly status`). *Enforcers* gate claims: `root-cause-analyzer` (R1 RCA), `testing-validator` (proof-before-"works"), `layer-validator` (pre-edit `candy.yml`).
- **Workflows** (`.claude/workflows/`) — `/verify-beds [bed …]` fans the `kind: eval` beds out as the R10 gate; `/audit-deploy-configs [target …]` evaluates deploy configs for AI and humans.
- **Binding rule — running a bed is R10-class.** Any agent or workflow that runs `charly eval run <bed>` / `charly update` obeys: disposable-only authorization (R10/disposability), the commit is gated on a full final-code live test that is pasted (run beds freely throughout to verify; only the commit is gated), no scope-shrinking flags without per-turn authorization, and **paste-proof survives delegation** — the executor returns the verbatim verdict + exit code, and the delegating agent PASTES it (a delegated bed run whose failure is summarized away is fraud).
- **Hooks doctrine.** Hooks are LEAN POINTERS to this file + skills (never copies of R0–R10 — duplication drifts) PLUS deterministic `PreToolUse` gates (`pre-commit-gate.sh`, `pre-push-gate.sh`) that BLOCK only unambiguous invariants (`--no-verify`, illegal/absent attribution tier, `--force`). Hooks gate mechanical invariants; agents judge proof. Never re-bloat the hooks.
- **Per-directory CLAUDE.md signposts.** This root file is the single canonical R0–R10 rule-set. `ov/`, `candy/`, `plugins/`, and each `image/<distro>` submodule carry a THIN signpost `CLAUDE.md` that only names the skills to load for that area and points back here — it restates no rule (duplication drifts). Subagents/teammates load the full `CLAUDE.md` hierarchy from their working dir.

---

## Key Rules

This is the index of project-specific technical rules. Each philosophy / process rule already has a dedicated section above and appears here only as a one-line pointer; the technical rules below are stated in full because no other section owns them.

**Pointers to dedicated sections (above):**

- **Skills first** → **R0. SKILLS FIRST — THE SUPREME RULE**. Overrides every other instruction, hook, and training datum; the Skill Dispatcher maps triggers → skills. Partial compliance is not compliance.
- **Engineering discipline → runtime verification → acceptance** → **Ground Truth Rules** R1–R5 (RCA / no-deferral / no-duplication / no-workarounds / hard-cutover) before R6–R9 before R10. Operationalized in `/charly-internals:strict-policy`.
- **Candyboxing, not sandboxing** → the **Candyboxing** pillar.
- **Risk Driven Development (RDD)** → the **Risk Driven Development (RDD)** pillar — never trust, verify.
- **Agent Driven Development (ADD)** → the **Agent Driven Development (ADD)** pillar — the spec is the test; agents author and grade it.
- **Hard cutover by default** → **Hard Cutover by Default** + `/charly-internals:cutover-policy`.
- **Branch-per-change, R10-gated auto-landing, NEVER force-push** → **Post-Execution Policies** + `/charly-internals:git-workflow`.
- **Tag every push with a fresh CalVer timestamp** → **Post-Execution Policies** (`v$(date -u +%Y).$((10#$(date -u +%j))).$(date -u +%H%M)`, day-of-year NOT zero-padded; ONE immutable add-only tag per push; INDEPENDENT of the `charly.yml` `version:` schema field — and a YAML schema/format change does BOTH: raise `LatestSchemaVersion()` via a `MigrationStep` AND carry the fresh tag on the landing push; `plugins`/`pkg/arch` are tag-exempt). Detail: `/charly-internals:git-workflow`, `/charly-build:migrate`.
- **Every change ships proof of its functionality** → the eval-coverage gate in **Post-Execution Policies** / R7 / R10. A change whose new functionality has no test that would FAIL without it is not landable. See `/charly-eval:eval`.
- **Autonomous by default — act, don't ask** → **What is NOT post-execution**. The AI opens the next cutover automatically and pauses only at a genuine unexpected/unplanned crossroad. Autonomy is INITIATIVE, not skipping proof.
- **History lives ONLY in `CHANGELOG.md`** → **Where things are documented**. CLAUDE.md, README.md, and every skill describe current behavior in present tense — never a dated note, "renamed from", or "previously / formerly / was".

**Technical rules (stated in full here):**

- **Lowercase-hyphenated names** for candies and boxes.
- **Cross-kind name reuse is permitted and encouraged.** A single name (e.g. `ov-cachyos`) MAY exist simultaneously as a layer (`candy/<name>/`), an `image:` entry, a `pod:` entry, a `vm:` entry, a `k8s:` entry, a `local:` entry, AND a `deploy:` entry. Uniqueness is scoped to each kind. Verbs disambiguate by command context: `charly box build ov-cachyos` resolves to `image.ov-cachyos`; `charly vm create ov-cachyos` to `vm.ov-cachyos`; `charly update ov-cachyos` to `deploy.ov-cachyos`. The unified loader does NOT enforce global uniqueness across kinds; `ResolveDeployRef` chooses image-first when the same name exists as both an image and a layer (use `--add-candy <name>` for the layer-first path). See `/charly-image:layer`, `/charly-image:image`, `/charly-local:local-spec`, `/charly-core:deploy`, `/charly-build:validate`.
- **`charly.yml` is the only canonical authoring target.** Every `ov` authoring/scaffolding verb (`charly box set`, `charly box new project`, `charly box new image`, `charly box add-candy`, `charly box rm-candy`, `charly vm import`, `charly vm update`, `charly vm clone`) writes to `charly.yml`. Per-kind files (`box.yml`, `vm.yml`, `pod.yml`, `k8s.yml`, `local.yml`, `deploy.yml`) remain valid as flat `import:` items in `charly.yml` but are NEVER the default authoring target. Missing `charly.yml` → hard error pointing at `charly box new project .` or `charly migrate`.
- **Init-system polymorphism via mixed `service:` entries.** A layer that needs a service running under both supervisord (container/pod targets) and systemd (host / bootc / VM targets) declares BOTH forms in ONE `service:` list — same `name:`, one entry with `use_packaged: <unit>.service` (or `<unit>.socket`), the other with custom `exec:`. The init system at deploy time renders only the matching form. **NEVER** create a `<name>-host` or `<name>-pod` sibling layer to express target polymorphism — it duplicates packages and eval probes and inevitably drifts. Canonical worked examples: `/charly-coder:sshd` (mixed), `/charly-infrastructure:virtualization` (mixed), `/charly-infrastructure:postgresql` (use_packaged-only). See `/charly-image:layer` "Service Declaration" + "Anti-pattern: `<name>-host` / `<name>-pod` sibling layers".
- **Tests ship with the image.** See `/charly-eval:eval`.
- **Unified YAML + `import:` (Go-style namespaces).** `charly.yml` is the single project entry point. The SINGLE composition statement is `import:` (the legacy `include:` key is DELETED — a residual `include:` is a hard load-time error pointing at `charly migrate`). `import:` is a LIST whose items are either a **bare string** (flat import into THIS repo's root namespace — same-repo per-kind files + the shared `build.yml` distro/builder/init vocabulary) or a **single-key map `alias: ref`** (a namespaced child import of another project; entries referenced QUALIFIED as `alias.entry`, e.g. `base: cachyos.cachyos`, `builder: {pixi: ov.arch-builder}`). Resolution is namespace-relative (Go package-member semantics); `distro:`/`build:` inherit across a namespace boundary but `builder:` does NOT (the consumer declares its own builder map). The **main repo stays multi-file** (`base.yml` = arch+fedora stacks, plus `eval.yml`/`box.yml`/`build.yml`/…); **each `image/<distro>` submodule is its own ov-project, structured like the main repo** — an `charly.yml` entry point that flat-imports its per-kind sibling files (`box.yml`/`pod.yml`/`k8s.yml`/`vm.yml`) where present (most submodules) OR inlines them all in the one `charly.yml` (e.g. `bootc`); both layouts load identically (the `import:` list flat-merges siblings root-wins). Each imports main under the `ov` namespace and `build.yml` flat. The main↔cachyos mutual import is cycle-broken at load. See `/charly-image:image`, `/charly-internals:go`, `/charly-build:migrate`.
- **Schema — every YAML file is a GENERIC kind-container, routed by SHAPE.** The kinds (`box`, `candy`, `pod`, `vm`, `k8s`, `local`, `android`, `deploy`, plus the build-vocabulary kinds `builder`/`distro`/`init` and the eval kinds `ai`/`recipe`/`score`/…) are `kind:` discriminators. ANY file may hold ANY mix of kinds — the loader routes each document by its top-level kind-key, **NEVER by filename**. A per-kind sibling file (`box.yml` holding boxes, `candy.yml` holding candies, …) is a pure user **CONVENIENCE** expressed in `charly.yml`'s `import:` / `discover:`; it is never required, assumed, or hardcoded. **`charly.yml` is the ONLY YAML filename the code knows** — every other name is configured there. `discover:` is a **FLAT generic scan-spec list** (`- {path, recursive, manifest}`): each spec scans a path for its manifest and routes every discovered document by shape, with the manifest filename configured per spec (one overridable `DefaultManifest` default). The schema version is a CalVer string (e.g. `2026.156.1041`), the same scheme as image tags; configs older than `LatestSchemaVersion()` migrate via the single idempotent `charly migrate`. Nesting of deployments uses `nested:`. See `/charly-build:migrate`, `/charly-image:image`, `/charly-internals:go`, `/charly-core:deploy`, `/charly-vm:vm`, `/charly-local:local-spec`, `/charly-eval:android`.
- **Per-kind versioning: `version:` is the authoritative identity.** Every `layer` MUST declare a `version:` CalVer (validator hard-errors otherwise); it is OPTIONAL for every other kind. An image's emitted `ai.opencharly.version` LABEL is the **content-derived `EffectiveVersion`** — its dedicated `version:` if set, else the highest layer `version:` across the whole base chain (computed in `ov/effective_version.go`). The label is STABLE across builds when no layer changed, so a child's `FROM <base>` SHA doesn't shift and cache-misses don't cascade; bare distro bases carry a dedicated `version:` for the same reason. Short-name resolution + `charly clean` retention prefer the **label-CalVer over the tag-CalVer** (the per-build tag is only a tiebreaker). `charly migrate` backfills both (`entity-version` step); the runtime hard-errors on a non-conformant config (no compat fallback).
- **Layer-version resolution: per-entity version, post-fetch.** The `@github…:vTAG` git tag is ONLY the FETCH coordinate (which commit to clone); a layer's OWN `version:` (read AFTER fetch) is what's compared. The resolver collects EVERY distinct `(repo, git-tag)`, fetches each, and `pickLayerVersion` arbitrates per bare ref: same per-entity version across different git tags → **no warning** (a repo re-tag of an unchanged layer is silent — this is the fix for spurious warnings), the newest git tag winning for freshness; different per-entity versions → **warn once and use the newest** (highest CalVer). This ONE arbiter covers direct AND transitive refs; a fetched layer with no `version:` is a hard error. Remote-ref collection is **reachability-scoped**: only layers reachable from the enabled images' `base:`/`builder:` chains are fetched — a namespace's unreferenced images and its `kind:local` templates are NOT collected. `charly box reconcile` aligns the git-tag pins. See `/charly-internals:go` "Remote-layer resolver", `/charly-build:validate`, `/charly-build:reconcile`, `/charly-internals:capabilities`.
- **Deploy fetches NOTHING speculative.** Every `charly deploy add` (any target kind: `local`, `pod`, `vm`, `k8s`) MUST emit zero image-pull / image-build steps unless an explicit layer step at deploy time requires the image — and no layer does today. Test-bed image preflight is the test/eval entry point's job, not the deploy's: `charly eval run` collects `score.target_image:` + per-scenario `pod:` declarations and ensures each is present in podman storage BEFORE running scenarios. A `kind: local` template carries no `image:` field. See `/charly-local:local-spec`, `/charly-eval:eval`.
- **Mode purity.** `LoadUnified` reads `charly.yml` only; never merges `deploy.yml`. See `/charly-internals:go` "Mode purity".
- **Project directory resolution.** See `/charly-image:image` "Project directory resolution".
- **User policy: adopt over rename.** Declarative via `build.yml distro.<name>.base_user:` + `user_policy:`. See `/charly-image:image` "user_policy" and `/charly-build:build` "base_user:".
- **Unified `service:` schema.** See `/charly-image:layer` "Service Declaration".
- **Capabilities as OCI-label contract.** See `/charly-internals:capabilities`.
- **Deploy targets.** `charly deploy add <name> <ref>`: `target: local` + `host: local` (default) → local filesystem via `ShellExecutor`; `target: local` + `host: <user@machine[:port]>` → SSH (ssh-config + agent supply credentials); `target: vm` → VM via managed `ov-<vmname>` ssh-config alias; `target: k8s` → Kustomize tree; `target: android` → install `apk:` packages onto a `kind: android` device (in-pod emulator or remote adb endpoint) via `AndroidDeployTarget`; `target: pod` (default) → container deploy. See `/charly-core:deploy`, `/charly-local:local-deploy`, `/charly-kubernetes:kubernetes`, `/charly-internals:vm-deploy-target`, `/charly-eval:android`. Shared IR: `/charly-internals:install-plan`.
- **Cross-deployment probing + `peer:` siblings — ONE deployment tests ANOTHER.** A `kind: eval` bed or `kind: deploy` may declare `peer:` companion deployments (a map of inline `DeploymentNode`s) brought up ALONGSIDE it on the shared `ov` network — *siblings*, NOT `nested:` children. The canonical case: a Chrome DRIVER pod CDP-probing a SEPARATE web-server SUBJECT pod (pod→pod). A check carries `on: <peer>` to DISPATCH its probe (cdp/vnc/mcp/command) against the driver while addressing the subject via `${PEER_HOST:<name>}` (the pod-net container DNS — pod→pod) or `${PEER_ENDPOINT:<name>:<port>}` (a host-vantage `127.0.0.1` address from the shared `resolveEvalEndpoint` — a pod's auto-published port OR a VM's `ssh -L` forward, so a `local`/host driver reaches a pod OR a VM subject; a pod driver can't reach a VM's host-vantage endpoint, so the VM cell uses a host-side driver). `peer:` is a `DeploymentNode` field shared by eval AND deploy through ONE lifecycle (`foldPeers` registers each peer top-level; `bringUpPeers`/`tearDownPeers` shell out to the same `charly config`/`charly start`/`charly remove` verbs the deploy path uses — the bed runner inherits it). Peers inherit the owner's disposability (no new autonomy), carry globally-unique + dot-free names, and are NEVER eval-live'd (instruments, not subjects). No new R-rule. See `/charly-eval:eval` "Cross-deployment probing" + `/charly-core:deploy` "Sibling peers".
- **`kind: android` + the `apk` package format.** Android is a first-class deploy SUBSTRATE modeled on `kind: k8s`: a `kind: android` entity is a DEVICE (an in-pod emulator referenced by `image:`, or a remote/physical adb endpoint referenced by `adb: {host: …}`). `apk` is a layer-declared PACKAGE FORMAT (NOT a kind) — a layer's `apk:` list (per-app `package:`/`apk:` + `source`/`arch`/`version`) parallels `package:`/`aur:` but is device-scoped; it compiles to an `ApkInstallStep` that ONLY `target: android` executes (every other target skips it — there is no device at image-build time). A `target: android` deploy applies its `add_candy:` layers' `apk:` packages onto the device via ONE shared installer (`ov/android_install.go`, also driving `charly eval adb install-app`/`install` — R3). Nested deployment: `pod → android` (the device on its emulator pod) mirrors `vm → k8s`. See `/charly-eval:android`, `/charly-eval:adb`, `/charly-eval:appium`.
- **k3s cluster provisioning via layers.** `/charly-infrastructure:k3s` + `/charly-infrastructure:k3s-server` + `/charly-infrastructure:k3s-agent` compose into a full k3s cluster on any substrate (host / VM / container). Pre-shared `K3S_CLUSTER_TOKEN` auto-generates on first deploy via `ensureLayerSecret` (`ov/layer_secrets.go`) — server and every agent automatically share the persisted value with zero operator setup; override with `charly secrets set ov/secret/K3S_CLUSTER_TOKEN <value>` only when reproducing a specific cluster identity. Kubeconfig pulled back via layer `artifact:` block (with `wait_seconds: 120` so retrieval waits for k3s to write `/etc/rancher/k3s/k3s.yaml`). Cluster configuration lives on a `kind: k8s` entity (workload defaults + cluster policy). Cluster probes via `/charly-kubernetes:eval-k8s` (`charly eval k8s nodes/addons/wait-ready/…`).

## AI Attribution (Fedora Policy Compliant)

Per [Fedora AI Contribution Policy](https://docs.fedoraproject.org/en-US/council/policy/ai-contribution-policy/), ALL commits MUST include `Assisted-by: Claude (<confidence>)` trailer. ALL GitHub issues/PRs MUST include `*Assisted-by: Claude (<confidence>)*` at the end.

| Confidence | When to Use |
|-----------|-------------|
| `fully tested and validated` | All 10 evaluation standards met + fresh-rebuild re-verification (R10) on every affected `disposable: true` target + the cutover's NEW/CHANGED runner / AI loop / verb evaluation actually executed against the fresh rebuild + R10 outputs (exploratory + fresh-rebuild) pasted in the conversation |
| `analysed on a live system` | A live invocation of the runner / AI loop / verb evaluation / subprocess that the cutover ADDED OR CHANGED actually ran AND its output is pasted. An eval-sandbox rebuild WITHOUT the subsequent runner invocation does NOT qualify — that's `syntax check only`. NEVER use this tier when only a `--dry-run` was performed |
| `syntax check only` | Compile + unit tests + validators / dry-run / parse confirmations passed; the live runner did NOT execute. HONEST default when R10 hasn't physically fit yet — pair with explicit "R10 not yet run, awaiting authorization for the live round" AND do NOT commit. Pairing this tier with a commit is a violation; STOP and ask (this targets CODE with a pending R10 — a docs/policy-only cutover is governed by the provision below) |
| `theoretical suggestion` | No validation performed — FORBIDDEN as a shipped-code tier |

**Docs/policy-only cutovers — the runtime tiers are read against the APPLICABLE standards.** A cutover that touches ONLY documentation/policy (`CLAUDE.md`, `plugins/**/SKILL.md`, `README.md`, `plugins/README.md`, `CHANGELOG.md` — no Go, no YAML schema, no `candy.yml`/`box.yml`, no other runtime surface) has NO R10 bed to run. Its applicable evaluation standards are the non-runtime ones: adversarial consistency review, the R5 grep self-test, cross-reference validation, markdown integrity, and the `pre-commit-gate.sh` / `pre-push-gate.sh` gates. Such a cutover earns `fully tested and validated` when ALL applicable (non-runtime) standards pass; the `syntax check only → do NOT commit` clause does NOT apply to it (that clause targets code with a pending R10). The moment a cutover ALSO touches code or config it is NOT docs-only — it is gated on that surface's R10 as usual, at the tier its runtime proof supports, and the docs ride along in the same commit.

**Any rule violation forbids commit. Period.** A violation of R1, R2, R3, R4, R5, R6, R7, R8, R9, R10, OR the "Prioritize Clean Architecture Above All Else" section means: NO commit, at any tier, in any submodule, with any wording. There is no "downgrade tier and ship anyway" path — that path does NOT exist. The agent's only authorized responses to a known violation are (a) fix the violation in the same working tree and re-run all verification, or (b) escalate to the operator and STOP. Suggesting any other path — "lower tier", "downgrade", "commit at a reduced confidence", "ship with a caveat", "note the violation in the commit message and proceed" — is itself a rule violation. The four-tier table above describes WHICH tier the proof supports when committing IS permitted; a known rule violation makes commit NOT permitted regardless of tier.

```
Fix: Add fuse-overlayfs for container startup

Tested via overlay session on LOCAL system.

Assisted-by: Claude (fully tested and validated)
```

## Where things are documented

The doc split is **five-way** — each layer has ONE owner; the others link to it, never restate it:

- **Rules & mandates → `CLAUDE.md`** (this file): R0–R10, the philosophy pillars as operational mandates, the cutover + post-execution process, and the Key Rules technical index.
- **Features & command reference → `README.md`**: the user-facing intro and the build → run → deploy → evaluate command surface.
- **Usage & architecture → skills** (`plugins/README.md` is the full index, 290+ skills): every candy, box, verb, and subsystem. The single source of truth for *how*.
- **Thesis & direction → `VISION.md`** (repo root): the long-term "why this exists and where it's going", distilled from the philosophy pillars (Candyboxing, RDD, Agent Driven Development, Disposable-Only Autonomy, "for you and your agents"), stated as ASPIRATION in present-and-future tense.
- **History → `CHANGELOG.md`** (repo root): every dated change, past rename, completed cutover/migration, relocated/deleted/retired identifier, and "previously / formerly / was". CLAUDE.md, README.md, `plugins/README.md`, and every `plugins/**/SKILL.md` describe the CURRENT state in present tense ONLY. When a cutover lands, append its narrative to `CHANGELOG.md`; state the standing rules it establishes forward-looking here and in skills, with no history. `CHANGELOG.md` is the sanctioned "changelog context" named by R5's grep self-test.
