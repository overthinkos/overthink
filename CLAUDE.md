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

## Prioritize Clean Architecture Above All Else

Always pick the cleanest long-term approach and prioritize having a clean codebase with any deprecated code fully removed above everything.
You have all the time in the world and taking the time to get things properly done is ALWAYS worth the effort.

## Where things are documented

- **README.md** — user-facing intro: features, key concepts, install, quick taste, lifecycle, command reference.
- **`plugins/README.md`** — index of all 250+ skills across five plugins.
- **`plugins/ov/skills/<cmd>/SKILL.md`** — one skill per `ov` subcommand (`/ov:image`, `/ov:build`, `/ov:config`, `/ov:test`, …).
- **`plugins/ov-layers/skills/<name>/SKILL.md`** — one skill per layer (164 of them).
- **`plugins/ov-images/skills/<name>/SKILL.md`** — one skill per defined image (49 of them).
- **`plugins/ov-dev/skills/`** — developer-facing (Go codebase map, generate internals, skill maintenance).

Architecture, mode split (build / test / deploy / mcp-gateway), subsystem mapping — all delegated to skills. Don't duplicate them here.

---

## Key Rules

- **Skills first** — invoke matching skills BEFORE reading source, launching Explore agents, or grepping. Order: skills → CLAUDE.md → memory → explore (last resort). Multi-step workflows: invoke ALL skills in the chain. See `/ov-dev:skills` for routing, chains, and the 3 blocking enforcement agents (layer-validator, root-cause-analyzer, testing-validator).
- **Lowercase-hyphenated names** for layers and images.
- **All logic lives in `ov`** — Taskfiles are strictly bootstrap (build the `ov` binary). Source: `Taskfile.yml` + `taskfiles/{Build,Setup}.yml`.
- **Tests ship with the image** — every layer that installs a service ships a `tests:` block (see `/ov:test`). LABEL directives emit last in each Containerfile so test edits rebuild in ~2 seconds.
- **Mode purity** — `LoadConfig` reads `image.yml` only; never merges `deploy.yml`. See `/ov-dev:go` "Mode purity".
- **Project directory resolution** (build mode) — `-C` / `--dir` / `OV_PROJECT_DIR` (local) or `--repo` / `OV_PROJECT_REPO` (remote, cached in `~/.cache/ov/repos/`). `--repo` + `--dir` are mutually exclusive. `ov mcp serve` auto-falls back to `overthinkos/overthink` whenever the resolved cwd has no `image.yml`. See `/ov:image` "Project directory resolution" and `/ov:mcp`.
- **Don't declare defensive deps** — a layer's `depends:` on another layer ships that layer in every downstream image whether the runtime uses it or not. Declare deps only when the layer *actually uses* the target at runtime. Historical examples removed 2026-04: `supervisord`, `language-runtimes`, and `uv` all dropped vestigial `depends: python` (they use system `python3`, not the pixi env). See `/ov-layers:supervisord`, `/ov-layers:language-runtimes`, `/ov-layers:uv`.
- **User policy: adopt over rename** — when an upstream base image ships a pre-existing uid-1000 account (Ubuntu 24.04 ships `ubuntu:ubuntu`), declare it via `build.yml distro.<name>.base_user:` and let `user_policy: auto` (the default) adopt it. Never `usermod -l` rename — it fights the base image's conventions and breaks cloud-init / docs assumptions. Layers that need the uid-1000 account's name use `getent passwd 1000` discovery inside `cmd:` (see `/ov-layers:sshd`), not hardcoded literals or `$USER`. See `/ov:image` "user_policy" and `/ov:build` "base_user:".

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
