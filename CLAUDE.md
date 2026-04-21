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

See `plugins/README.md` for the full skill index (250+ skills across `ov`, `ov-dev`, `ov-layers`, `ov-images`, `ov-jupyter`). README.md carries the user-facing intro. All architecture / mode split / subsystem detail lives in skills — do not duplicate here.

---

## Key Rules

- **Skills first** — invoke matching skills BEFORE reading source, launching Explore agents, or grepping. Order: skills → CLAUDE.md → memory → explore (last resort). See `/ov-dev:skills`.
- **Lowercase-hyphenated names** for layers and images.
- **All logic lives in `ov`** — Taskfiles are strictly bootstrap (build the `ov` binary).
- **Tests ship with the image** — every layer that installs a service has a `tests:` block. See `/ov:test`.
- **Mode purity** — `LoadConfig` reads `image.yml` only; never merges `deploy.yml`. See `/ov-dev:go` "Mode purity".
- **Project directory resolution** — `-C`/`--dir`/`OV_PROJECT_DIR` (local) or `--repo`/`OV_PROJECT_REPO` (remote). See `/ov:image` "Project directory resolution".
- **Don't declare defensive deps** — layer `depends:` ships the target in every downstream image whether runtime uses it or not. Declare only when the layer *actually uses* the target. See `/ov-layers:supervisord` etc.
- **User policy: adopt over rename** — `build.yml distro.<name>.base_user:` + `user_policy: auto` (default). Never `usermod -l`. Layers needing the uid-1000 name use `getent passwd 1000` inside `cmd:` (see `/ov-layers:sshd`). See `/ov:image` "user_policy" and `/ov:build` "base_user:".
- **Unified `services:` schema** — layer.yml uses `services:` (one list) for both `use_packaged:` entries (reuse distro-shipped systemd units) and structured custom entries. All 40 in-tree layers migrated 2026-04. Legacy `service:` (raw INI) and `system_services:` (unit-name list) still parse for external layer sources but are retired in-tree. See `/ov:layer` "Service Declaration".
- **Deploy targets** — `ov deploy add <name> <ref>` is the unified entry point. Literal name `host` applies layers to the local filesystem via `HostDeployTarget` (ledger at `~/.config/overthink/installed/`, gated by `--with-services`/`--allow-repo-changes`/`--allow-root-tasks`); any other name is a container deploy. `ov start`/`ov stop` remain as ergonomic wrappers for the common single-image container case. See `/ov:deploy`, `/ov:host-deploy`. Internal IR shared across build + deploy: `/ov-dev:install-plan`.

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
