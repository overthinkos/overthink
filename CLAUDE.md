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

**`ov` (Go CLI)** -- all computation, building, and deployment. Two operational modes:
- **Build mode:** Parses `images.yml`, resolves layers, generates Containerfiles, builds images. See `/ov:build`, `/ov:generate`.
- **Deploy mode:** Reads OCI labels + `deploy.yml`. `ov config` is the single entry point (quadlet + secrets + volumes + data). Tunnel config is deploy.yml-only (not in labels). See `/ov:config`, `/ov:deploy`.

Source: `ov/`. Registry inspection via go-containerregistry.

**Key subsystems** — invoke the skill for full details:

| Subsystem | Skill |
|-----------|-------|
| Credentials & Secrets | `/ov:secrets`, `/ov:config` |
| Volumes & Encrypted Storage | `/ov:deploy`, `/ov:config`, `/ov:enc` |
| env/mcp provides/requires/accepts | `/ov:config`, `/ov:layer` |
| Sidecars & Tunnels (deploy.yml-only) | `/ov:sidecar`, `/ov:deploy` |
| Init Systems | `/ov:generate`, `/ov:layer` |
| Multi-distro | `/ov:build`, `/ov:layer` |
| Desktop Automation | `/ov:cdp`, `/ov:wl`, `/ov:vnc`, `/ov:wl-overlay` |
| Keyboard & Locale | `/ov-layers:labwc`, `/ov-layers:selkies` |
| NO_PROXY Enrichment | `/ov:config` |
| GPU Auto-detection | `/ov:doctor`, `/ov:shell` |

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
+-- layers/<name>/            # Layer directories (161 layers)
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

Skills, agents, and MCP servers live in `plugins/` (git submodule: `git@github.com:overthinkos/overthink-plugins.git`). Contains 5 plugins: `ov` (37 operation skills), `ov-dev` (3 dev skills, 3 agents), `ov-jupyter` (MCP server), `ov-layers` (161 layer skills), `ov-images` (41 image skills) — 242 total. Enabled via `.claude/settings.json`. Clone: `git clone --recurse-submodules`. Update: `git submodule update --remote plugins`. See `/ov-dev:skills` for skill maintenance guidelines.

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

## Command Map

Use `ov --help` and `ov <cmd> --help` for flags. Every command has a matching `/ov:<cmd>` skill with full documentation. Invoke the skill before reading source code. Key skill groupings: `/ov:config` + `/ov:deploy` + `/ov:sidecar` + `/ov:enc` (deployment), `/ov:cdp` + `/ov:wl` + `/ov:vnc` + `/ov:wl-overlay` (desktop automation), `/ov:build` + `/ov:generate` + `/ov:validate` (build pipeline).

---

## Task Commands (bootstrap only)

- `task build:ov` -- Build ov from source into `bin/ov` and install (auto-detects distro, auto-calls `build:install`)
- `task build:install` -- Install ov for the current host via distro dispatch. On Arch family (Arch/Manjaro/EndeavourOS) it runs `makepkg -efi --noconfirm` (pacman package to `/usr/bin/ov`). On all other distros (Fedora/Bazzite/Debian/Ubuntu/...) it uses `install -D -m 0755 bin/ov $HOME/.local/bin/ov` and warns if `~/.local/bin` is not on `$PATH`. Escape hatches: `task build:install-arch` forces makepkg, `task build:install-portable` forces the `install -D` path.
- `task setup:builder` -- Create multi-platform buildx builder
- `task setup:all` -- Full setup (build ov + create builder)

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