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

`ov` (Go CLI, source in `ov/`) has a **hard namespace split** between
**build mode** (the `ov image …` family — only these commands read
`image.yml`) and **deploy mode** (every other command — reads OCI labels
+ `deploy.yml`, never `image.yml`). See `/ov:image` for the build-mode
family and `/ov:config`, `/ov:deploy`, `/ov:pull` for the deploy side.
Build vocabulary (distro bootstrap, builders, init systems) lives in
`build.yml` — see `/ov:build`. Go implementation map: `/ov-dev:go`.

**Key subsystems** — invoke the skill for full details. Each skill is
the single source of truth for its area; don't copy their contents here.

| Subsystem | Skill |
|-----------|-------|
| Image family (build mode) | `/ov:image`, `/ov:build`, `/ov:generate`, `/ov:validate`, `/ov:pull` |
| Install tasks (`tasks:` verb catalog, `vars:`, `${VAR}`, YAML anchors) | `/ov:layer` (authoritative) |
| Credentials & Secrets | `/ov:secrets`, `/ov:config` |
| Credential-backed env vars (`secret_accepts` / `secret_requires`) | `/ov:layer`, `/ov:secrets` |
| Volumes & Encrypted Storage | `/ov:deploy`, `/ov:config`, `/ov:enc` |
| env/mcp provides/requires/accepts | `/ov:config`, `/ov:layer` |
| Sidecars & Tunnels (deploy.yml-only) | `/ov:sidecar`, `/ov:deploy` |
| Init Systems | `/ov:generate`, `/ov:layer` |
| Multi-distro | `/ov:build`, `/ov:layer` |
| Desktop Automation | `/ov:cdp`, `/ov:wl`, `/ov:vnc`, `/ov:wl-overlay` |
| Keyboard & Locale | `/ov-layers:labwc`, `/ov-layers:selkies` |
| GPU Auto-detection | `/ov:doctor`, `/ov:shell` |
| Missing-image recovery | `/ov:pull` (`ErrImageNotLocal` sentinel in `ov/labels.go`) |
| Declarative testing (`tests:` / `deploy_tests:` / `org.overthinkos.tests`) | `/ov:test` — verb catalog, runtime variables, deploy.yml overlay, and 10 authoring gotchas |
| Containerfile generation (LABELs-at-end, `shellAnsiQuote`, `writeJSONLabel`) | `/ov:generate`, `/ov-dev:generate`, `/ov-dev:go` |

**`task` (Taskfile)** -- bootstrap only: builds `ov` from source. Source: `Taskfile.yml` + `taskfiles/{Build,Setup}.yml`.

---

## Repository Layout

Project directory tree, skills submodule (`plugins/`), and sync
conventions (public git vs private Syncthing for memory/settings) live
in `/ov-dev:go` (directory structure) and `/ov-dev:skills` (plugin
structure + two-layer sync). 4 plugins with skills (`ov`, `ov-layers`,
`ov-images`, `ov-dev`) + 1 empty (`ov-jupyter`, MCP integration only).
**243 skills total — 1:1 coverage for every ov command, layer, and
image.**

---

## Key Rules

- MUST invoke skills before exploring the codebase — skills are the primary knowledge source.
- Lowercase-hyphenated names for layers and images.
- All logic lives in `ov`; Taskfiles are strictly bootstrap (build the `ov` binary). See `Taskfile.yml` + `taskfiles/{Build,Setup}.yml`.
- **Tests ship with the image**: every layer that installs a service ships a `tests:` block (see `/ov:test`). LABEL directives are emitted last in each Containerfile so test edits rebuild in ~2 seconds instead of minutes.

**Authoring + deployment specifics live in skills** — look them up, don't duplicate:

- Authoring (the building blocks): `/ov:layer`, `/ov:image`, `/ov:build`, `/ov:test`.
- Deployment (running the blocks): `/ov:config`, `/ov:deploy`, `/ov:sidecar`, `/ov:enc`. Quadlet default; `ov config` before `ov start`; tunnel is deploy.yml-only.

---

## Skills First (Blocking)

Invoke matching skills BEFORE reading source, launching Explore agents, or grepping. Order: skills → CLAUDE.md → memory → explore (last resort).

- `/ov:<cmd>` for operations, `/ov-layers:<name>` for layer internals, `/ov-images:<name>` for image composition, `/ov-dev:go` for Go code edits.
- Multi-step workflows: invoke ALL skills in the chain.
- For desktop automation routing (CDP / WL / VNC / SPA / AT-SPI hierarchy), see `/ov:cdp`.
- For skill chains, workflow positions, maintenance guidelines, and the 3 blocking enforcement agents (layer-validator, root-cause-analyzer, testing-validator): see `/ov-dev:skills` and `/ov-dev:go`.

Each skill's trailing `## Related …` and `Workflow position` sections enumerate chains — do not duplicate them here.


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