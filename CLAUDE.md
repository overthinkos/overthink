# Overthink — The Container Management Experience for You and Your AI

Compose, build, deploy, and manage container images from a library of fully configurable layers.
Built on a generic init system framework (`build.yml` → `init:` section) and `ov` (Go CLI). Designed to work equally well from the command line and from AI agents like Claude Code. Supports both Docker and Podman.

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

## Architecture Overview

`ov` (Go CLI, source in `ov/`) has a three-mode namespace split with strictly disjoint input sets — each mode owns a distinct input file and never reads another mode's input:

- **Build mode** — `ov image {build, generate, validate, merge, new, inspect, list, pull}`. Reads `image.yml` + `build.yml`. Writes Containerfiles, built images, OCI labels. See `/ov:image`, `/ov:build`.
- **Test mode** — `ov test` (all forms: `run`, `cdp`, `wl`, `dbus`, `vnc`) + `ov image test`. Reads only OCI labels (`org.overthinkos.tests`) + local `deploy.yml` tests overlay + container/image runtime state. Never reads `image.yml`. Writes nothing persistent. The tests OCI label is baked at build time from `image.yml` + `layer.yml` authoring only — `deploy.yml` contributes the local tests overlay at test-run time, not to the label. See `/ov:test`.
- **Deploy mode** — every other command (`config`, `deploy`, `start`, `stop`, `update`, `remove`, `shell`, `cmd`, `service`, `status`, `logs`, `tmux`, `doctor`, `udev`, `vm`, `secrets`, `settings`, `alias`, `record`, `version`). Reads OCI labels + `deploy.yml`. Writes `deploy.yml`, quadlet files, credential stores. See `/ov:config`, `/ov:deploy`, `/ov-dev:go`.
- **Gateway (cross-mode)** — `ov mcp serve` exposes the *entire* CLI surface (all three modes) as MCP tools over Streamable HTTP or stdio, auto-generated from Kong reflection. Used by LLM agents driving `ov` remotely. Not a fourth mode: it is a remote-procedure surface onto the same modes above. See `/ov:mcp`.

**Key subsystems** — each skill is the single source of truth for its area; don't copy their contents here.

| Subsystem | Skill |
|-----------|-------|
| Image family (build mode) | `/ov:image`, `/ov:build`, `/ov:generate`, `/ov:validate`, `/ov:pull` |
| Testing (test mode) | `/ov:test` (parent router + nested verbs `/ov:cdp`, `/ov:wl`, `/ov:dbus`, `/ov:vnc`, `/ov:mcp`), `/ov-dev:go` for impl map |
| Install tasks (`tasks:` verb catalog, `vars:`, `${VAR}`, YAML anchors) | `/ov:layer` (authoritative) |
| Credentials & Secrets | `/ov:secrets`, `/ov:config` |
| Credential-backed env vars (`secret_accepts` / `secret_requires`) | `/ov:layer`, `/ov:secrets` |
| Volumes & Encrypted Storage | `/ov:deploy`, `/ov:config`, `/ov:enc` |
| env/mcp provides/requires/accepts | `/ov:config`, `/ov:layer` |
| Sidecars & Tunnels (deploy.yml-only) | `/ov:sidecar`, `/ov:deploy` |
| Init Systems | `/ov:generate`, `/ov:layer` |
| Multi-distro | `/ov:build`, `/ov:layer` |
| Desktop Automation | `/ov:test` (parent router) with nested verbs `/ov:cdp`, `/ov:dbus`, `/ov:vnc`, `/ov:wl`; plus `/ov:wl-overlay` (Wayland overlay helpers) |
| Keyboard & Locale | `/ov-layers:labwc`, `/ov-layers:selkies` |
| GPU Auto-detection | `/ov:doctor`, `/ov:shell` |
| Missing-image recovery | `/ov:pull` (`ErrImageNotLocal` sentinel in `ov/labels.go`) |
| Declarative testing (`tests:` / `deploy_tests:` / `org.overthinkos.tests`) | `/ov:test` (verb catalog, runtime variables, deploy.yml overlay, authoring gotchas) |
| Containerfile generation (LABELs-at-end, `shellAnsiQuote`, `writeJSONLabel`) | `/ov:generate`, `/ov-dev:generate`, `/ov-dev:go` |
| Bootc-specific boot wiring | `/ov-layers:bootc-config`, `/ov-layers:supervisord`, `/ov-images:selkies-desktop-bootc`, `/ov:vm` |
| Rootless nested containers & rootless VMs | `/ov-layers:container-nesting` (kernel RCA), `/ov-layers:virtualization` (libvirt session), `/ov-images:selkies-desktop-ov` (streaming-desktop composition), `/ov-images:fedora-coder` (headless composition) |
| MCP server (`ov mcp serve`) — gateway exposing every CLI leaf as an MCP tool | `/ov:mcp` (server architecture + Kong reflection + auto-fallback semantics), `/ov-layers:ov-mcp` (deployment layer + `/workspace` bind-mount + 3 deployment patterns) |
| Cross-distro test package names (`package_map:` on the `package:` verb) | `/ov:test`, `/ov-layers:sshd` |

**`task` (Taskfile)** -- bootstrap only: builds `ov` from source. Source: `Taskfile.yml` + `taskfiles/{Build,Setup}.yml`.

---

## Repository Layout

See `/ov-dev:go` for directory structure and `/ov-dev:skills` for plugin/skill organization.

---

## Key Rules

- MUST invoke skills before exploring the codebase — skills are the primary knowledge source.
- Lowercase-hyphenated names for layers and images.
- All logic lives in `ov`; Taskfiles are strictly bootstrap (build the `ov` binary). See `Taskfile.yml` + `taskfiles/{Build,Setup}.yml`.
- **Tests ship with the image**: every layer that installs a service ships a `tests:` block (see `/ov:test`). LABEL directives are emitted last in each Containerfile so test edits rebuild in ~2 seconds instead of minutes.
- **Mode purity**: `LoadConfig` reads `image.yml` only — never merges `deploy.yml`. See `/ov-dev:go` "Mode purity".
- **Project directory resolution** (build mode): `-C` / `--dir` / `OV_PROJECT_DIR` (local) or `--repo` / `OV_PROJECT_REPO` (remote, cached in `~/.cache/ov/repos/`). `--repo` + `--dir` are mutually exclusive. `ov mcp serve` auto-falls back to `overthinkos/overthink` whenever the resolved cwd has no `image.yml` (refined 2026-04). See `/ov:image` "Project directory resolution" and `/ov:mcp` "Project-dir wiring".
- **Don't declare defensive deps**: a layer's `depends:` on another layer carries a correctness cost — the depended layer ships in every downstream image whether the runtime uses it or not. Declare deps only when the layer *actually uses* the target at runtime. Historical examples removed in 2026-04: `supervisord` and `language-runtimes` both declared `depends: python` (the pixi-python ov-layer) when the real dep was the RPM `python3` package — dropping both cut several hundred MB from every deployable image. `uv` similarly dropped `depends: python` + its `pixi.toml` once it was rewritten as a direct-download Rust binary. See `/ov-layers:supervisord`, `/ov-layers:language-runtimes`, `/ov-layers:uv`.

**Authoring + deployment specifics live in skills** — see the subsystems table above for the full mapping. Quick entry points: authoring → `/ov:layer`, `/ov:image`, `/ov:build`, `/ov:test`; deployment → `/ov:config`, `/ov:deploy`, `/ov:sidecar`, `/ov:enc`. Quadlet is default; `ov config` before `ov start`; tunnel is deploy.yml-only.

---

## Skills First (Blocking)

Invoke matching skills BEFORE reading source, launching Explore agents, or grepping. Order: skills → CLAUDE.md → memory → explore (last resort). Multi-step workflows: invoke ALL skills in the chain. See `/ov-dev:skills` for skill routing, chains, maintenance guidelines, and the 3 blocking enforcement agents (layer-validator, root-cause-analyzer, testing-validator).


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