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
- Taskfiles for bootstrap only (building ov), Go for all other logic
- Never `pip install`, `conda install`, or `dnf install python3-*`. Pixi is the only Python package manager
- `.build/` is disposable; all generated files start with `# <path> (generated -- do not edit)`
- `USER <UID>` (numeric) not `USER <name>` in generated Containerfiles
- All logic belongs in `ov`. Tasks are only for bootstrap. Every public task has `desc:`
- MUST invoke skills before exploring the codebase. Skills are the primary knowledge source

**Layer/image authoring:** See `/ov:layer` and `/ov:build` for all rules (task names, distro/build tags, init deps, env/mcp provides).

**Deployment:** See `/ov:config`, `/ov:deploy`, `/ov:sidecar`. Quadlet mode is default. `ov config` before `ov start`. Tunnel config is deploy.yml-only. `-e` merges env vars (use `-c` for clean replace).

---

## Command Map

Use `ov --help` and `ov <cmd> --help` for flags. Every command has a matching `/ov:<cmd>` skill with full documentation. Invoke the skill before reading source code. Key skill groupings: `/ov:config` + `/ov:deploy` + `/ov:sidecar` + `/ov:enc` (deployment), `/ov:cdp` + `/ov:wl` + `/ov:vnc` + `/ov:wl-overlay` (desktop automation), `/ov:build` + `/ov:generate` + `/ov:validate` (build pipeline).

---

## Task Commands (bootstrap only)

- `task build:ov` -- Build ov from source into `bin/ov` and install as Arch package (auto-calls `build:install`)
- `task build:install` -- Install ov as Arch package (uses pre-built binary from `bin/ov` via PKGBUILD, fast ~2s)
- `task setup:builder` -- Create multi-platform buildx builder
- `task setup:all` -- Full setup (build ov + create builder)

---

## Skills: Decision Architecture

### MANDATORY: Skills Before Exploration

**BLOCKING REQUIREMENT:** Invoke matching skills BEFORE reading source, launching Explore agents, or grepping. Order: skills → CLAUDE.md → memory → explore (last resort).

- If a skill exists, invoke it. No exceptions.
- Dev tasks: invoke `/ov-dev:go` AND the relevant `/ov:*` skill before touching `.go` files.
- Multi-step workflows: invoke ALL skills in the chain.

### First Branch: Using vs Developing

- **Using ov** (building/running images): `ov` + `ov-layers` + `ov-images` plugins
- **Developing ov** (Go CLI code): `ov-dev` plugin
- Bug fixes in ov often need both: `ov-dev` (how code works) + `ov:*` (expected behavior)

### Plugin Namespaces

| Plugin | Skills | Role | Question it answers |
|--------|--------|------|---------------------|
| `ov` | 37 | Operations | "How do I use X?" |
| `ov-dev` | 3 + 3 agents | Contributing | "How does the code work?" |
| `ov-jupyter` | 1 MCP server | Notebook MCP | "How do I use the notebook MCP tools?" |
| `ov-layers` | 161 | Layer reference | "What does layer X contain?" |
| `ov-images` | 41 | Image reference | "What does image X look like?" |

### Common Skill Chains

| Task | Skill chain |
|------|-------------|
| Author a layer | `/ov:layer` -> `/ov-layers:<similar>` -> `/ov:image` -> `/ov:build` |
| Debug runtime | `/ov:<operation>` -> `/ov-layers:<layer>` -> `/ov:service` |
| Desktop automation | `/ov:cdp` -> `/ov:wl` -> `/ov:wl` sway -> `/ov:wl-overlay` |
| Deploy a service | `/ov:config` -> `/ov:deploy` -> `/ov:service` -> `/ov-images:<name>` |
| Selkies streaming | `/ov-layers:selkies` -> `/ov-layers:labwc` -> `/ov-images:selkies-desktop` |
| Jupyter MCP | `/ov-layers:jupyter-colab` -> `/ov-images:jupyter` -> `/ov:service` |
| Fix ov bug | `/ov-dev:go` + `/ov:<relevant>` -> `/ov:validate` |
| Deploy Hermes | `/ov-images:hermes` -> `/ov:config` -> `/ov:service` |
| Deploy Open WebUI | `/ov-images:openwebui` -> `/ov:config` -> `/ov:secrets` -> `/ov:service` |
| Hermes + Selkies | `ov config selkies-desktop` -> `ov config jupyter --update-all` -> `ov config hermes --update-all` |
| Open WebUI + Ollama + Jupyter | `ov config ollama` -> `ov config jupyter --update-all` -> `ov config openwebui --update-all` |
| Full lifecycle | `/ov:build` -> `/ov:deploy` -> `/ov:service` -> `/ov-images:<name>` |

For desktop automation: use CDP first, `--wl` for selkies-desktop (no VNC). See `/ov:cdp`, `/ov:wl`, `/ov-images:selkies-desktop` for detailed usage patterns.

For skill maintenance guidelines (when/how to update skills): see `/ov-dev:skills`.

### Disambiguating Overlapping Skills

Rule of thumb:
- `/ov:X` = "how do I USE X?" (operations, commands, flags)
- `/ov-layers:X` = "what does layer X CONTAIN?" (deps, ports, volumes, env, packages)
- `/ov-images:X` = "what does image X LOOK LIKE?" (base, layers, platforms, lifecycle)

Start with `/ov:X` for usage, drill into `/ov-layers:X` or `/ov-images:X` for configuration. Each skill's cross-references section lists related skills.

### Desktop Automation Hierarchy

Seven abstraction levels for interacting with container desktops:

| Level | Skill | Interface | When to use |
|-------|-------|-----------|-------------|
| SPA | `/ov:cdp` spa | CDP Input events via SPA overlay | Remote desktop through browser (selkies) -- bypasses local compositor/Chrome shortcuts |
| Semantic | `/ov:wl` atspi | AT-SPI2 tree | Find elements by name/role -- most reliable for non-web UIs |
| DOM | `/ov:cdp` | CSS selectors, JS eval | Chrome content -- structured, fast |
| AX Tree | `/ov:cdp` axtree | CDP Accessibility | Chrome UI elements, menus, buttons via CDP |
| Wayland | `/ov:wl` | grim, wtype, wlrctl | Screenshots, input, windows -- compositor-agnostic (sway + labwc) |
| Pixel | `/ov:vnc` | VNC coordinates, framebuffer | Remote access -- when TCP connectivity needed |
| Window | `ov wl sway` | Sway IPC (swaymsg) | Sway-only: tree, layout, move, resize, workspaces |
| Overlay | `/ov:wl-overlay` | gtk4-layer-shell | Recording overlays -- title cards, lower-thirds, countdowns, fades |

See `/ov:cdp` for SPA/WL bridge patterns and coordinate mapping.

### ov-dev Agents

The `ov-dev` plugin includes 3 blocking enforcement agents (layer-validator, root-cause-analyzer, testing-validator). See `/ov-dev:go` for details.


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