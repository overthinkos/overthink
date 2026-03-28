# Overthink — The Container Management Experience for You and Your AI

Compose, build, deploy, and manage container images from a library of fully configurable layers.
Built on `supervisord` and `ov` (Go CLI). Designed to work equally well from the command line and from AI agents like Claude Code. Supports both Docker and Podman.

---


## Always follow the Five Cornerstones of AI Scut Testing

### Your Assumptions Are the Enemy

- The thing you didn't think to test is the thing that will break.

### Small Bugs Have Big Friends

- Every issue you dismissed as nonessential is tomorrow's catastrophe.

### It's Broken Until It Runs Live

- Localhost and mocks are deceptive liars.

### Check Every Damn Thing

- Methodically. Tediously. No shortcuts.

### Then Check It Again

Because you missed something. You always do.

## Prioritize Clean Architecture Above All Else

Always pick the cleanest long-term approach and prioritize having a clean codebase with any deprecated code fully removed above everything.
You have all the time in the world and taking the time to get things properly done is ALWAYS worth the effort.

## Architecture Overview

Two components with a clean split:

**`ov` (Go CLI)** -- all computation, building, and deployment. Two operational modes:
- **Build mode:** Parses `images.yml`, scans `layers/`, resolves dependency graphs, validates, generates Containerfiles, builds images via `<engine> build`.
- **Deploy mode:** Reads OCI image labels + `~/.config/ov/deploy.yml` (no `images.yml` needed). `ov enable`/`start`/`stop`/`status`/`logs`/`update`/`remove`/`seed` all work standalone with just the container image.

Source: `ov/`. Registry inspection via go-containerregistry.

**Credential & Secret Management** -- Abstracted via `CredentialStore` interface:
- **Host-side credentials** (VNC passwords) stored in system keyring (GNOME Keyring, KDE Wallet, KeePassXC) or plaintext config fallback. Backend auto-detected; override with `secret_backend` config key.
- **KeePass .kdbx backend** for systems without Secret Service (headless servers, SSH sessions). `ov secrets init` creates a database; auto-detected when keyring is unavailable and `secrets.kdbx_path` is configured. `ov secrets` commands manage entries directly.
- **Container secrets** declared in `layer.yml` `secrets` field. Metadata stored in OCI image labels (`org.overthinkos.secrets`). At runtime, `ov enable`/`ov start` provisions Podman secrets (`podman secret create`) and generates `Secret=` quadlet directives. Docker falls back to env var injection.
- **Resolution chain:** env var > keyring > config file > default. Migration: `ov config migrate-secrets`.
- Source: `ov/credential_store.go` (interface), `ov/credential_keyring.go`, `ov/credential_config.go`, `ov/credential_kdbx.go`, `ov/secrets.go`

**`task` (Taskfile)** -- bootstrap only: builds `ov` from source and creates the buildx builder. Source: `Taskfile.yml` + `taskfiles/{Build,Setup}.yml`. All other operations use `ov` directly.

**What gets generated** (`ov generate`):
- `.build/<image>/Containerfile` -- one per image, unconditional `RUN` steps only
- `.build/<image>/traefik-routes.yml` -- traefik dynamic config (only for images with `route` layers)
- `.build/<image>/supervisor/*.conf` -- supervisord service configs (only for images with `service` layers)
- `.build/_layers/<name>` -- symlinks to remote layer directories (only when remote layers used)

Generation is idempotent. `.build/` is disposable and gitignored.

**Multi-distro support via `distro:` and `build:` fields:**
- `distro:` — Distro identity tags in priority order: `distro: ["fedora:43", fedora]`. For packages: first matching section wins (override). For tasks: all matching run (additive).
- `build:` — Package formats tied to builders: `build: [rpm]` or `build: [pac, aur]`. ALL formats installed in order. Replaces old `pkg:` field.
- `builds:` — Builder capabilities on builder images (unchanged): `builds: [pixi, npm, cargo]`
- Tags union (`org.overthinkos.tags`) = `["all"]` + distro + build formats — used for task matching
- Source: `ov/config.go` (`ResolvedImage.Distro`, `ResolvedImage.BuildFormats`, `MatchingTasks`), `ov/format_config.go` (YAML config loading), `ov/format_template.go` (template rendering), `distro.yml` + `builder.yml` (format definitions at project root, referenced via `format_config:` in `images.yml`)

**Pixi manylinux fix:** `ov generate` injects `[system-requirements] libc = { family = "glibc", version = "2.34" }` into every pixi.toml during build if not already present. This fixes pixi 0.66.0's resolver which incorrectly detects the platform as `manylinux_2_28` on glibc 2.42, rejecting `manylinux_2_34` wheels (e.g., pixelflux 1.5.9). Source: `builder.yml` `manylinux_fix` template, rendered by `ov/generate.go`.

---

## Directory Structure

```
project/
+-- bin/ov                    # Built by `task build:ov` (gitignored)
+-- ov/                       # Go module (go 1.25.3, kong CLI, go-containerregistry)
+-- distro.yml                # Distro bootstrap + package format definitions (referenced via images.yml)
+-- builder.yml               # Multi-stage builder definitions (referenced via images.yml)
+-- .build/                   # Generated (gitignored)
+-- images.yml                # Image definitions
+-- setup.sh                  # Bootstrap: downloads task, builds ov
+-- Taskfile.yml              # Bootstrap tasks only
+-- taskfiles/                # Build.yml, Setup.yml
+-- layers/<name>/            # Layer directories (~130 layers)
+-- plugins/                  # Git submodule (overthink-plugins)
+-- templates/                # supervisord.header.conf
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

Skills, agents, and MCP servers live in a separate git submodule at `plugins/`.

**Repository:** `git@github.com:overthinkos/overthink-plugins.git`

```
plugins/
+-- .claude-plugin/marketplace.json   # Central plugin registry
+-- ov/                               # Operations (16 skills)
+-- ov-dev/                           # Development (2 skills, 3 agents, GitHub MCP)
+-- ov-layers/                        # Layer reference (127 skills)
+-- ov-images/                        # Image reference (31 skills)
```

Each plugin has a `.claude-plugin/plugin.json` manifest. Skills are at `plugins/<plugin>/skills/<name>/SKILL.md`.

**Enabled via** `.claude/settings.json` (committed):

```json
{
  "enabledPlugins": {
    "ov@ov-plugins": true,
    "ov-dev@ov-plugins": true,
    "ov-layers@ov-plugins": true,
    "ov-images@ov-plugins": true
  },
  "extraKnownMarketplaces": {
    "ov-plugins": {
      "source": { "source": "directory", "path": "./plugins" }
    }
  }
}
```

**Submodule operations:**
- Clone with plugins: `git clone --recurse-submodules`
- Update plugins: `git submodule update --remote plugins`
- After pulling main repo: `git submodule update --init`

---

## Key Rules

- Lowercase-hyphenated names for layers and images
- Taskfiles for bootstrap only (building ov), Go for all other logic
- Never `pip install`, `conda install`, or `dnf install python3-*`. Pixi is the only Python package manager
- `.build/` is disposable; all generated files start with `# <path> (generated -- do not edit)`
- `USER <UID>` (numeric) not `USER <name>` in generated Containerfiles
- All logic belongs in `ov`. Tasks are only for bootstrap. Every public task has `desc:`
- Always recommend quadlet mode for deployment. Direct mode is only a fallback for platforms without quadlet support
- MUST invoke skills before exploring the codebase. Skills are the primary knowledge source, not the code itself
- `root.yml`/`user.yml` use `all:` task for common logic, with optional tag-specific tasks (`rpm:`, `pac:`, `fedora:`, etc.). Never use `install:` as a task name
- `distro:` field defines identity tags: `distro: ["fedora:43", fedora]`. First matching section overrides packages. Inherited through base chain
- `build:` field defines package formats: `build: [rpm]` or `build: [pac, aur]`. ALL formats installed in order. Inherited through base chain. Default: `[rpm]`

For layer-specific rules (install files, packages, port_relay, secrets, cache mounts): `/ov:layer`

**Credential security:** Config files (`config.yml`, `deploy.yml`) are written with `0600` permissions for new files. `ov` warns if existing files have overly permissive permissions but does not change them — the user must `chmod 600` themselves. Credentials are stored in system keyring when available; plaintext config file is the fallback. `ov config migrate-secrets` migrates existing plaintext credentials to keyring. `ov doctor` reports credential storage health.

**GPU auto-detection:** `ov` detects host GPU hardware and injects appropriate config at runtime:
- **NVIDIA:** CUDA images get `--gpus all` / CDI device injection automatically
- **AMD ROCm:** Auto-detects `/dev/kfd` and `/dev/dri/renderD*`, injects `HSA_OVERRIDE_GFX_VERSION`, adds `video`/`render` groups. `ov udev` manages KFD device rules. `ov doctor` reports AMD GPU info
- Source: `ov/devices.go` (`DetectNvidiaGPU`, `DetectAMDGPU`)

**Security mounts:** `security.mounts` in `layer.yml` declares host bind mounts or tmpfs needed for device access. Stored in image labels, applied by `ov enable`/`ov start`. Format: `host:container:options` (bind mount) or `tmpfs:path:options` (tmpfs). Generates `Volume=` or `Tmpfs=` in quadlets.
- Source: `ov/config.go` (`SecurityConfig.Mounts`), `ov/quadlet.go`, `ov/start.go`

---

## Command Map

Use `ov --help` and `ov <cmd> --help` for quick flag reference. For detailed usage, load the skill.

| Commands | Skill |
|----------|-------|
| `generate`, `validate`, `inspect`, `list`, `new layer` | `/ov:validate` (rules), `/ov:layer` (authoring), `/ov:image` (images) |
| `build`, `merge` | `/ov:build` |
| `shell` | `/ov:shell` |
| `start`, `stop`, `enable`, `disable`, `status` (`--all`, `--json`), `logs`, `update`, `remove`, `seed` | `/ov:service` |
| `deploy show/export/import/reset/status/path` | `/ov:deploy` |
| `service start/stop/restart/status` (supervisord) | `/ov:service` |
| `cdp` | `/ov:cdp` |
| `wl sway` | `/ov:wl` (sway subgroup) |
| `tmux shell/run/attach/list/capture/send/kill` | `/ov:tmux` |
| `vnc` | `/ov:vnc` |
| `wl` | `/ov:wl` |
| `alias` | `/ov:alias` |
| `config` (get, set, list, reset, path, migrate-secrets) | `/ov:config` |
| `secrets` (init, list, get, set, delete, import, export, path) | `/ov:config` |
| `enc` | `/ov:enc` |
| `udev status/generate/install/remove` | `/ov:service` |
| `vm` | `/ov:vm` |
| `doctor` | Host dependency + secret storage checks (no skill -- standalone diagnostic) |

---

## Workflows

**Add a layer:** `ov new layer <name>` -> edit `layer.yml` -> add install files -> add to image in `images.yml` -> `ov build <image>`
Skills: `/ov:layer` -> `/ov-layers:<similar>` (pattern reference) -> `/ov:image` -> `/ov:build`

**Add an image:** add entry to `images.yml` -> `ov build <image>`
Skills: `/ov:image` -> `/ov-images:<similar>` (pattern reference) -> `/ov:build`

**Layer images:** set `base` to another image name in `images.yml`. The generator handles dependency ordering and tag resolution.

**Deploy a service:** `ov enable <image> -w ~/project` -> saves all deployment state to `~/.config/ov/deploy.yml` -> generates quadlet from image labels + deploy.yml. No `images.yml` needed for deployment.
Skills: `/ov:deploy` -> `/ov:service` (lifecycle)

**Host bootstrap (first time):** requires `go`, `docker` (or `podman`). Run `bash setup.sh` to download `task`, build `ov`, then `ov build` to build all images. To use podman: `ov config set engine.build podman`.

---

## Task Commands (bootstrap only)

- `task build:ov` -- Build ov from source into `bin/ov`
- `task build:install` -- Build and install ov to `~/.local/bin`
- `task setup:builder` -- Create multi-platform buildx builder
- `task setup:all` -- Full setup (build ov + create builder)

---

## Skills: Decision Architecture

### MANDATORY: Skills Before Exploration

**CRITICAL: You MUST invoke matching skills BEFORE reading source files, launching Explore agents, or using Grep/Glob to search the codebase.** This is a BLOCKING REQUIREMENT -- not a suggestion.

The skills system contains curated, structured knowledge for every component. Raw codebase exploration is slower, noisier, and misses context that skills provide.

**Required order:**
1. **Invoke skills** -- ALWAYS first. Match the task to skills using the tables below.
2. **Read CLAUDE.md** -- project rules already in context
3. **Read memory** -- prior learnings and user preferences
4. **Explore codebase** -- ONLY after confirming no skill covers the topic

**Hard rules:**
- If a skill exists for the topic, you MUST invoke it. No exceptions.
- For development tasks: invoke BOTH `/ov-dev:go` (code structure) AND the relevant `/ov:*` skill (expected behavior) before touching any `.go` file.
- For multi-step workflows: invoke ALL skills in the chain (e.g., build -> deploy -> service -> image).
- Explore agents are a LAST RESORT, not a first step. Justify why no skill covers the topic before launching one.

**Self-check before any codebase exploration:**
> "Is there a skill that covers this topic? If yes, invoke it first."

### First Branch: Using vs Developing

- **Using ov** (building/running images): `ov` + `ov-layers` + `ov-images` plugins
- **Developing ov** (Go CLI code): `ov-dev` plugin
- Bug fixes in ov often need both: `ov-dev` (how code works) + `ov:*` (expected behavior)

### Plugin Namespaces

| Plugin | Skills | Role | Question it answers |
|--------|--------|------|---------------------|
| `ov` | 16 | Operations | "How do I use X?" |
| `ov-dev` | 2 + 3 agents | Contributing | "How does the code work?" |
| `ov-layers` | 127 | Layer reference | "What does layer X contain?" |
| `ov-images` | 31 | Image reference | "What does image X look like?" |

### Common Skill Chains

Real tasks chain through skills in predictable patterns:

**Author a new layer:**
`/ov:layer` (format, rules) -> `/ov-layers:<similar>` (existing pattern) -> `/ov:image` (add to image) -> `/ov:build`

**Debug a runtime issue:**
`/ov:<operation>` (how it works) -> `/ov-layers:<layer>` (config, deps, ports) -> `/ov:config` or `/ov:service` (state)

**Desktop automation:**
`/ov:cdp` (DOM: click, type, eval) -> `/ov:wl` (compositor-agnostic: screenshots, input, window mgmt, clipboard, AT-SPI2) -> `/ov:wl` sway subgroup (sway-only: tree, layout, move, resize)
Use CDP first. Use `ov cdp click --wl` for selkies-desktop (no VNC). Use `ov wl` for screenshots, input, window management (`toplevel`, `close`, `fullscreen`), clipboard, and AT-SPI2 accessibility (`ov wl atspi find/click`). Use `ov wl sway` for sway-specific IPC features (tree, workspaces, layout, move, resize).
On NVIDIA headless: `ov wl` is the primary tool — VNC screenshots are gray (upstream wayvnc bug), but `ov wl screenshot` works perfectly with gles2.
For selkies-desktop (labwc): `ov wl` provides full automation. `ov wl sway` commands are sway-specific and won't work on labwc.

**Deploy a service:**
`/ov:deploy` (quadlet, tunnels) + `/ov:enc` (if encrypted) -> `/ov-images:<name>` (image config) -> `/ov:service` (lifecycle)

**Set up Selkies streaming (browser-accessible — working):**
`/ov-layers:selkies` (streaming engine) -> `/ov-layers:labwc` (compositor) -> `/ov-layers:waybar-labwc` (panel) -> `/ov-images:selkies-desktop` (image)
Uses labwc nested inside pixelflux's Wayland compositor. Access via `http://localhost:3000` — no client app needed. NVENC detected but fails with driver 590.48 (pixelflux compat issue); CPU x264enc-striped at 60fps works well. Image: `selkies-desktop`.
**Host-side automation:** `ov wl` provides full compositor-agnostic control: screenshots (grim), input (wtype, wlrctl), window management (wlrctl toplevel), clipboard (wl-copy/paste), resolution (wlr-randr), AT-SPI2 introspection (atspi). Use `ov cdp click --wl` for selector-based clicks via Wayland pointer (no VNC needed). Includes `wl-tools` + `a11y-tools` layers.

**Fix a bug in ov:**
`/ov-dev:go` (source map, tests) + `/ov:<relevant>` (expected behavior) -> `/ov:validate` (verify)

**Modify a metalayer:**
`/ov:layer` (metalayer patterns) -> `/ov-layers:<metalayer>` (current composition) + `/ov-layers:<addition>` (what to add)

**Full image lifecycle (build -> deploy -> test):**
`/ov:build` (build image) -> `/ov:deploy` (quadlet, tunnels, bind mounts) -> `/ov:service` (enable, start, status, logs) -> `/ov-images:<name>` (ports, verification)

### Continuous Improvement: Feeding Insights Back Into Skills

Skills are living documents. When real-world usage reveals gaps, update them:

**What triggers a skill update:**
- A deployment step fails or requires undocumented workarounds
- A verification check is missing from an image skill
- A skill's recommended order or defaults are wrong (e.g., direct vs quadlet)
- A gotcha or prerequisite is discovered during actual usage

**How to feed back:**
1. During the session, update the relevant skill file at `plugins/<plugin>/skills/<skill-name>/SKILL.md`
2. If the insight affects cross-skill behavior, update CLAUDE.md too
3. After any non-trivial deployment session, ask: "Did we learn anything that future sessions should know?"

**When NOT to update skills:** ephemeral issues, user-specific config (use memory), bug fixes in ov code (use git)

### Disambiguating Overlapping Skills

Rule of thumb:
- `/ov:X` = "how do I USE X?" (operations, commands, flags)
- `/ov-layers:X` = "what does layer X CONTAIN?" (deps, ports, volumes, env, packages)
- `/ov-images:X` = "what does image X LOOK LIKE?" (base, layers, platforms, lifecycle)

Examples where multiple skills cover one topic:
- **OpenClaw:** `/ov:openclaw` (gateway config) vs `/ov-layers:openclaw` (layer properties) vs `/ov-images:openclaw` (image definition)
- **Chrome/CDP:** `/ov:cdp` (CDP commands) vs `/ov-layers:chrome` (ports, relay, shm_size) vs `/ov-layers:chrome-sway` (sway integration)
- **Sway:** `/ov:wl` sway subgroup (`ov wl sway <cmd>`, compositor commands) vs `/ov-layers:sway` (layer properties) vs `/ov-layers:sway-desktop` (desktop metalayer)
- **VNC:** `/ov:vnc` (VNC commands, auth) vs `/ov-layers:wayvnc` (VNC server layer properties)
- **Niri:** `/ov-layers:niri` (compositor, built from source) vs `/ov-layers:niri-desktop` (desktop metalayer)
- **KWin:** `/ov-layers:kwin` (compositor, virtual backend) vs `/ov-layers:kwin-desktop` (desktop metalayer)
- **Mutter:** `/ov-layers:mutter` (compositor, headless) vs `/ov-layers:mutter-desktop` (desktop metalayer)
- **X11 Desktop:** `/ov-layers:xorg-headless` (display server) vs `/ov-layers:openbox` (window manager) vs `/ov-layers:x11-desktop` (desktop metalayer)
- **Selkies:** `/ov-layers:selkies` (streaming engine, pixelflux/pcmflux) vs `/ov-layers:labwc` (nested compositor) vs `/ov-layers:waybar-labwc` (panel for labwc) vs `/ov-layers:selkies-desktop` (desktop metalayer) vs `/ov-images:selkies-desktop` (image)

### Desktop Automation Hierarchy

Six abstraction levels for interacting with container desktops:

| Level | Skill | Interface | When to use |
|-------|-------|-----------|-------------|
| Semantic | `/ov:wl` atspi | AT-SPI2 tree | Find elements by name/role -- most reliable for non-web UIs |
| DOM | `/ov:cdp` | CSS selectors, JS eval | Chrome content -- structured, fast |
| AX Tree | `/ov:cdp` axtree | CDP Accessibility | Chrome UI elements, menus, buttons via CDP |
| Wayland | `/ov:wl` | grim, wtype, wlrctl | Screenshots, input, windows -- compositor-agnostic (sway + labwc) |
| Pixel | `/ov:vnc` | VNC coordinates, framebuffer | Remote access -- when TCP connectivity needed |
| Window | `ov wl sway` | Sway IPC (swaymsg) | Sway-only: tree, layout, move, resize, workspaces |

**CDP → WL bridge:** Use `ov cdp click <image> <tab> <selector> --wl` to find elements by CSS selector and click via wlrctl. Critical for selkies-desktop (no VNC server). Same pattern as `--vnc` but uses Wayland pointer.

### ov-dev Agents

The `ov-dev` plugin includes 3 blocking enforcement agents (automatic, not invoked manually):

| Agent | Trigger | Purpose |
|-------|---------|---------|
| layer-validator | Before editing `layer.yml` | Validates structure and field types |
| root-cause-analyzer | Any error in output | Deep 8-step root cause analysis |
| testing-validator | Claiming something "works" | Verifies actual local test results |


## AI Attribution (Fedora Policy Compliant)

Per [Fedora AI Contribution Policy](https://docs.fedoraproject.org/en-US/council/policy/ai-contribution-policy/), Claude **MUST** include the `Assisted-by: Claude` trailer with a **confidence statement** in all commits:

```
<commit message>

Assisted-by: Claude (fully tested and validated)
```

## Confidence Statements (Required)

All AI-assisted contributions **MUST** include a confidence statement indicating verification level:

| Statement | When to Use | Evidence |
|-----------|-------------|----------|
| `fully tested and validated` | Overlay testing + all 9 testing standards met | Complete LOCAL system verification |
| `analysed on a live system` | Observed live system behavior, logs checked | Partial testing, live analysis |
| `syntax check only` | Pre-commit hooks passed, no functional testing | ShellCheck, yamllint, etc. passed |
| `theoretical suggestion` | No validation performed | AVOID - indicates unverified code |

**Choosing the Right Level:**

1. **Used overlay testing + verified all functionality?** → `fully tested and validated`
2. **Observed live system behavior, checked logs?** → `analysed on a live system`
3. **Only ran pre-commit hooks?** → `syntax check only`
4. **No validation at all?** → `theoretical suggestion` (avoid when possible)

**Examples:**

```
Fix: Add fuse-overlayfs for container startup

Tested via overlay session on LOCAL system.
All 9 testing standards verified.

Assisted-by: Claude (fully tested and validated)
```

```
Refactor: Simplify build cache logic

Reviewed logic and checked logs on live system.

Assisted-by: Claude (analysed on a live system)
```

```
Feat: Add initial WinBoat support structure

Skeleton implementation, pre-commit validation passed.
Requires testing on Windows environment.

Assisted-by: Claude (syntax check only)
```

**MANDATORY for Claude:**

- **ALWAYS** include confidence statement - this is non-negotiable
- Trailer goes after commit body, separated by blank line
- Required for ALL Claude-assisted commits (code, docs, configs)
- Only exception: trivial grammar/spelling corrections

**GitHub Issues and PRs:**

When creating issues or PR descriptions, include at the end:

```markdown
---
*Assisted-by: Claude (fully tested and validated)*
```