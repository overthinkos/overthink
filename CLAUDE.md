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

## Disposable-Only Autonomy + Mandatory Live-Deploy Verification

**`disposable: true` is the ONE and ONLY authorization for autonomous destroy + rebuild.** Default is `false` (explicit opt-in only; see `/ov-dev:disposable`). No derivation from other fields. No "this looks like a test bed" heuristic. No hostname-based assumptions. A deploy is either explicitly marked `disposable: true` in vms.yml / deploy.yml or it is NOT rebuildable unattended — even if its name contains "test", even if it's a project on a shared host where unrelated production services also run. Explicit-only is what makes this rule safe on shared infrastructure with live users on other resources.

On resources that ARE marked `disposable: true`, `ov rebuild <name>` performs destroy → (optional image rebuild) → create → start unattended, and is the preferred path. Hesitating to rebuild a disposable target when verification demands it is the OPPOSITE failure mode, and the one that leads to claimed-but-unverified fixes.

**Every change is proved on a freshly built binary on the target host** (the 10 testing standards in `/ov:test`):

1. Build the artifact from the changed source, on the target host.
2. Verify the deployed binary's version matches what you built (R8).
3. Verify runtime deps are installed via package management (R9).
4. For a target with `disposable: true`: `ov rebuild <name>` — unattended. For any other resource: confirm with the user before any destroy.
5. Exercise the feature end-to-end.
6. Paste the runtime output back into the conversation.
7. Leave the target healthy (running, not paused, not crashed).
8. **After committing the source-level fix, `ov rebuild` the disposable target from clean and re-run the full sequence. This fresh-rebuild re-verification is the acceptance gate** (R10).

### R8 — "Binary ≠ source"

Syncthing / git / rsync move *source* between hosts. They don't rebuild the binary. After pushing code, explicitly rebuild on the target and verify `ov version`. If the version is old, the fix under test isn't really under test. Live war-story: `ov test spice status` returned the old binary's output against a remote host while claimed success — the new code had been synced but not built.

### R9 — "Runtime deps are part of the contract"

A change that relies on an OS package at runtime (`nc`, `socat`, `xorriso`, `qemu-guest-agent` …) MUST add that package to `setup.sh` (per-distro blocks) AND to `pkg/arch/PKGBUILD` `depends=`. A manual install on one host is a bug report disguised as a fix. Live war-story: virt-manager needed `nc` on the libvirt host; a manual install would have silently broken virt-manager on the next freshly-installed synced host.

### R10 — "Verify on a `disposable: true` target; prove it on a fresh rebuild"

The verification loop has three rules:

1. **Always test on a target that carries an explicit `disposable: true`.** Never experiment on a resource without the flag. If no suitable disposable target exists, create one first (`ov deploy add <name> <ref> --disposable` or mark a VM in vms.yml and `ov vm create`). The opt-in is explicit; never assume disposability because of a name, lifecycle tag, hostname, or any other heuristic.
2. **If a test breaks the target, `ov rebuild` it back to the committed config before doing anything else.** Never layer experiments on broken state.
3. **After committing the real fix in source, re-verify on a FRESH `ov rebuild` of the disposable target.** A fix that passes only on a hand-patched target is not a real fix — it's a regression waiting for the next rebuild. Pasteable proof of the fresh-rebuild re-verification is the acceptance gate.

### End-of-turn checklist

Before saying "done" answer YES to all of these:

- Built a real artifact from the changed source, on the target host?
- Verified the deployed binary's version matches what you built (R8)?
- Exercised the feature end-to-end on the live target?
- Verified every runtime dep is installed via package management (R9)?
- Did verification run on a target explicitly marked `disposable: true` (never on anything else)?
- If you broke the target during exploration, did you `ov rebuild` it back to clean before continuing?
- After committing the source-level fix, did you `ov rebuild` the disposable target from clean and re-run the full verification against the fresh rebuild (R10)?
- Post-action state of every target is healthy?
- Pasted BOTH the exploratory verification output AND the fresh-rebuild re-verification output into the conversation?

See `/ov:test` for the 10 testing standards and `/ov-dev:disposable` for the classification schema.

## Hard Cutover by Default

Every schema change, API rename, or deprecation ships as a single hard-cutover
PR — no backcompat shims, no phased-migration coexistence, no "Phase 2" TODOs.
A matching one-shot `ov migrate <name>` command transforms legacy configs
in-place; residual legacy fields raise hard load-time errors with a remediation
hint. Exception: explicit user instruction to phase the cutover, recorded in
the plan file.

See `/ov-dev:cutover-policy` for forbidden patterns, required deliverables, and
rationale. See `/ov:migrate` for the `ov migrate <name>` command surface.

## Where things are documented

See `plugins/README.md` for the full skill index (250+ skills across `ov`, `ov-dev`, `ov-layers`, `ov-images`, `ov-jupyter`). README.md carries the user-facing intro. All architecture / mode split / subsystem detail lives in skills — do not duplicate here.

---

## Key Rules

- **Skills first** — invoke matching skills BEFORE reading source, launching Explore agents, or grepping. Order: skills → CLAUDE.md → memory → explore (last resort). See `/ov-dev:skills`.
- **Lowercase-hyphenated names** for layers and images.
- **Tests ship with the image.** See `/ov:test`.
- **Unified YAML.** `overthink.yml` is the single project entry point. See `/ov:layer`, `/ov:image`, `/ov:migrate`.
- **VMs are `kind: vm` entities** in `vms.yml`. See `/ov-vms:vms`, `/ov:vm`, `/ov:migrate`.
- **Hard cutover by default.** See `/ov-dev:cutover-policy` and the "Hard Cutover by Default" section above.
- **Mode purity.** `LoadUnified` reads `overthink.yml` only; never merges `deploy.yml`. See `/ov-dev:go` "Mode purity".
- **Project directory resolution.** See `/ov:image` "Project directory resolution".
- **User policy: adopt over rename.** Declarative via `build.yml distro.<name>.base_user:` + `user_policy:`. See `/ov:image` "user_policy" and `/ov:build` "base_user:".
- **Unified `service:` schema.** See `/ov:layer` "Service Declaration".
- **Capabilities as OCI-label contract.** See `/ov-dev:capabilities`.
- **Deploy targets.** `ov deploy add <name> <ref>`: literal `host` → local filesystem; `vm:<name>` → VM via SSH; `kubernetes` → Kustomize tree; any other → container deploy. See `/ov:deploy`, `/ov:host-deploy`, `/ov:kubernetes`, `/ov-dev:vm-deploy-target`. Shared IR: `/ov-dev:install-plan`.

---

## AI Attribution (Fedora Policy Compliant)

Per [Fedora AI Contribution Policy](https://docs.fedoraproject.org/en-US/council/policy/ai-contribution-policy/), ALL commits MUST include `Assisted-by: Claude (<confidence>)` trailer. ALL GitHub issues/PRs MUST include `*Assisted-by: Claude (<confidence>)*` at the end.

| Confidence | When to Use |
|-----------|-------------|
| `fully tested and validated` | Overlay testing + all 10 testing standards met (see `/ov:test`) + fresh-rebuild re-verification on a `disposable: true` target (R10) |
| `analysed on a live system` | Observed live system behavior, logs checked |
| `syntax check only` | Pre-commit hooks passed, no functional testing |
| `theoretical suggestion` | No validation performed — AVOID |

```
Fix: Add fuse-overlayfs for container startup

Tested via overlay session on LOCAL system.

Assisted-by: Claude (fully tested and validated)
```
