# Overthink — The Container Management Experience for You and Your AI

Compose, build, deploy, and manage container images from a library of fully configurable layers.
Built on a generic init system framework (`init.yml`) and `ov` (Go CLI). Designed to work equally well from the command line and from AI agents like Claude Code. Supports both Docker and Podman.

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

Two components with a clean split:

**`ov` (Go CLI)** -- all computation, building, and deployment. Two operational modes with a **hard namespace split**:
- **Build mode:** The `ov image …` family (`build`, `generate`, `validate`, `list`, `merge`, `new`, `inspect`, `pull`). **Only** these commands read `images.yml`. See `/ov:image` for the family overview and subcommand index.
- **Deploy mode:** Every other command. Reads **exclusively** from OCI labels (via `ExtractMetadata`) + `deploy.yml`. Never touches `images.yml`. `ov config` is the single entry point (quadlet + secrets + volumes + data). Tunnel config is deploy.yml-only (not in labels). When an image isn't in local storage, deploy-mode commands surface the `ErrImageNotLocal` recommendation pointing to `ov image pull`. See `/ov:config`, `/ov:deploy`, `/ov:pull`.

Source: `ov/`. Registry inspection via go-containerregistry.

**Key subsystems** — invoke the skill for full details:

| Subsystem | Skill |
|-----------|-------|
| Image family (build mode) | `/ov:image`, `/ov:pull`, `/ov:build`, `/ov:generate`, `/ov:validate` |
| Install tasks (verb catalog: `cmd`/`mkdir`/`copy`/`write`/`link`/`download`/`setcap`/`build`, `vars:`, `${VAR}`, YAML anchors) | `/ov:layer` (authoritative), `/ov:generate`, `/ov:validate`, `/ov-dev:generate` |
| Credentials & Secrets | `/ov:secrets`, `/ov:config` |
| Credential-backed layer env vars (`secret_accepts` / `secret_requires`) | `/ov:layer`, `/ov:secrets` |
| Volumes & Encrypted Storage | `/ov:deploy`, `/ov:config`, `/ov:enc` |
| env/mcp provides/requires/accepts | `/ov:config`, `/ov:layer` |
| Sidecars & Tunnels (deploy.yml-only) | `/ov:sidecar`, `/ov:deploy` |
| Init Systems | `/ov:generate`, `/ov:layer` |
| Multi-distro | `/ov:build`, `/ov:layer` |
| Desktop Automation | `/ov:cdp`, `/ov:wl`, `/ov:vnc`, `/ov:wl-overlay` |
| Keyboard & Locale | `/ov-layers:labwc`, `/ov-layers:selkies` |
| NO_PROXY Enrichment | `/ov:config` |
| GPU Auto-detection | `/ov:doctor`, `/ov:shell` |
| Missing-image recovery | `/ov:pull` (`ErrImageNotLocal` sentinel in `ov/labels.go`) |

**`task` (Taskfile)** -- bootstrap only: builds `ov` from source. Source: `Taskfile.yml` + `taskfiles/{Build,Setup}.yml`.

**Builder internals**: See `/ov:build`, `/ov:generate`.

---

## Directory Structure

```
project/
+-- bin/ov                    # Built by `task build:ov` (gitignored)
+-- ov/                       # Go module (go 1.25.3, kong CLI, go-containerregistry)
+-- distro.yml                # Distro bootstrap + package format definitions (referenced via images.yml)
+-- builder.yml               # Multi-stage builder definitions (referenced via images.yml)
+-- init.yml                  # Init system definitions: supervisord, systemd (referenced via images.yml)
+-- .build/                   # Generated (gitignored)
+-- images.yml                # Image definitions
+-- setup.sh                  # Bootstrap: downloads task, builds ov
+-- Taskfile.yml              # Bootstrap tasks only
+-- taskfiles/                # Build.yml, Setup.yml
+-- layers/<name>/            # Layer directories (160 layers)
+-- plugins/                  # Git submodule (overthink-plugins)
+-- templates/                # supervisord.header.conf (referenced by init.yml header_file)
```

### Two-Layer Sync Architecture

Git handles public/shared artifacts. Syncthing handles private/machine-specific state. `.gitignore` is the boundary.

| What | Synced by | Visibility |
|------|-----------|------------|
| Code, CLAUDE.md, skills, layers | Git | Public (committed) |
| `.claude/memory/` | Syncthing | Private (gitignored) |
| `.claude/settings.local.json` | Syncthing | Private (gitignored) |
| `.claude/settings.json` | Git | Public (committed) |

Memory setup: `autoMemoryDirectory: ".claude/memory"` in `.claude/settings.local.json`. Both settings.local.json and memory/ sync via Syncthing automatically.

### Plugins Submodule

Skills, agents, and MCP servers live in `plugins/` (git submodule: `git@github.com:overthinkos/overthink-plugins.git`). Contains 5 plugins: `ov` (38 operation skills — `/ov:pull` added with the `ov image` refactor), `ov-dev` (3 dev skills, 3 agents), `ov-jupyter` (MCP server), `ov-layers` (160 layer skills), `ov-images` (41 image skills) — 242 total. Enabled via `.claude/settings.json`. Clone: `git clone --recurse-submodules`. Update: `git submodule update --remote plugins`. See `/ov-dev:skills` for skill maintenance guidelines.

---

## Key Rules

**Project-wide:**
- Lowercase-hyphenated names for layers and images
- Pixi is the only Python package manager — never `pip install`, `conda install`, or `dnf install python3-*`
- `.build/` is disposable; all generated files start with `# <path> (generated -- do not edit)`
- `USER <UID>` (numeric) not `USER <name>` in generated Containerfiles
- All logic lives in `ov`; Taskfiles are bootstrap-only (building `ov`); every public task has `desc:`
- MUST invoke skills before exploring the codebase — skills are the primary knowledge source

**Authoring + deployment rules live in skills:** `/ov:layer`, `/ov:image`, `/ov:build` (authoring); `/ov:config`, `/ov:deploy`, `/ov:sidecar`, `/ov:enc` (deployment). Quadlet default; `ov config` before `ov start`; tunnel is deploy.yml-only; `-e` merges env vars, `-c` replaces.

---

## Host-side Task Commands (bootstrap only)

Taskfiles are strictly for building `ov` from source — not for in-container install logic. All layer-install logic lives in `layer.yml` `tasks:` (see `/ov:layer`).

- `task build:ov` — Compile `bin/ov` and install via distro dispatch.
- `task setup:builder` — Create multi-platform buildx builder.
- `task setup:all` — Full setup (build ov + create builder).

See the source Taskfiles (`Taskfile.yml` + `taskfiles/{Build,Setup}.yml`) for flag-level detail — they're the only Taskfiles in the project.

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