# Overthink — The Container Management Experience for You and Your AI

Compose, build, deploy, and manage container images from a library of fully configurable layers.
Built on a generic init system framework (`init.yml`) and `ov` (Go CLI). Designed to work equally well from the command line and from AI agents like Claude Code. Supports both Docker and Podman.

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
- **Deploy mode:** Reads OCI image labels + `~/.config/ov/deploy.yml` (no `images.yml` needed). `ov config`/`start`/`stop`/`status`/`logs`/`update`/`remove`/`seed` all work standalone with just the container image.

Source: `ov/`. Registry inspection via go-containerregistry.

**Credential & Secret Management** -- Abstracted via `CredentialStore` interface:
- **Host-side credentials** (VNC passwords) stored in system keyring (GNOME Keyring, KDE Wallet, KeePassXC) or plaintext config fallback. Backend auto-detected; override with `secret_backend` config key.
- **KeePass .kdbx backend** for systems without Secret Service (headless servers, SSH sessions). `ov secrets init` creates a database; auto-detected when keyring is unavailable and `secrets.kdbx_path` is configured. Override with `ov --kdbx <path>` global flag. `ov secrets` commands manage entries directly.
- **KeePass password caching** via Linux kernel keyring (`KEY_SPEC_USER_KEYRING`). The kdbx master password is cached for 1 hour by default after the first interactive prompt, so subsequent `ov` commands reuse it automatically. Resolution chain: `OV_KDBX_PASSWORD` env var > kernel keyring lookup (key: `ov-kdbx-password`) > interactive prompt (systemd-ask-password or terminal) > auto-store in kernel keyring with configured TTL. Config keys: `secrets.kdbx_cache` (env: `OV_KDBX_CACHE`, default: `true`), `secrets.kdbx_cache_timeout` (env: `OV_KDBX_CACHE_TIMEOUT`, default: `3600`). Uses `golang.org/x/sys/unix` keyctl syscalls. Source: `ov/keyctl.go`, `ov/credential_kdbx.go`.
- **Container secrets** declared in `layer.yml` `secrets` field. Metadata stored in OCI image labels (`org.overthinkos.secrets`). At configure time, `ov config <image>` provisions Podman secrets and generates `Secret=` quadlet directives. **Secret provisioning is idempotent** — existing Podman secrets are never overwritten. This prevents overwriting passwords that stateful services (e.g., PostgreSQL) have already been initialized with. To force re-provisioning: `podman secret rm <name> && ov config setup <image>`. `--password auto` generates all secrets automatically; `--password manual` prompts for each. Docker falls back to env var injection. Encrypted volumes are mounted via `ExecStartPre=ov config mount` in the quadlet. With Secret Service backend: auto-starts after login (waits for keyring unlock, `TimeoutStartSec=0`). The keyring wait and quadlet `KeyringBackend` flag both check the *configured* `secret_backend` setting via `resolveSecretBackend()` (not the runtime probe result), so the quadlet is correct even when generated with a locked keyring. Resets the cached credential store + keyring state on each retry so the keyring is detected once D-Bus becomes available at boot. With KeePass or no backend: requires `ov start` (prompts for password). Per-volume explicit paths supported via `--volume name:encrypt:/path`.
- **Resolution chain:** env var > keyring > config file > default. Migration: `ov settings migrate-secrets`.
- Source: `ov/credential_store.go` (interface), `ov/credential_keyring.go`, `ov/credential_config.go`, `ov/credential_kdbx.go`, `ov/secrets.go`

**Volume Management** -- Unified deploy-time volume backing:
- Layers declare `volumes:` in `layer.yml` (name + container path) -- what persistent storage is needed
- All volumes default to Docker/Podman named volumes (`ov-<image>-<name>`)
- At `ov config` time, any volume's backing can be changed per-volume: named volume (default), host bind mount, or encrypted (gocryptfs)
- Flags: `--volume name:type[:path]` (canonical), `--bind name[=path]` (shorthand), `--encrypt name` (shorthand). Type accepts both `encrypted` and `encrypt` (normalized)
- Per-volume encrypted path: `--volume name:encrypt:/path` stores `cipher/` and `plain/` directly inside the specified path (no `ov-<image>-<name>` prefix). Without explicit path, uses global `encrypted_storage_path` with prefix (backward compat)
- Env var automation: `OV_VOLUMES_<IMAGE>` (e.g., `OV_VOLUMES_IMMICH="library:bind:/mnt/nas,import:bind"`)
- Auto-path for bind mounts without explicit host path: `<volumes_path>/<image>/<name>` (default: `~/.local/share/ov/volumes/`)
- Configurable base: `ov settings set volumes_path /mnt/nas/ov-volumes` (env: `OV_VOLUMES_PATH`)
- Deploy.yml persists volume config: `volumes: [{name: data, type: bind, host: ~/data}]`. For encrypted type, `host:` stores the per-volume storage directory
- Encrypted volumes (default, no host): gocryptfs at `<encrypted_storage_path>/ov-<image>-<name>/{cipher,plain}`. With explicit host path: `<host>/{cipher,plain}`
- `ov seed` copies image data into empty bind-backed volume directories
- There is NO `bind_mounts` field in `images.yml` or OCI labels -- volume backing is purely a deploy-time decision
- Source: `ov/deploy.go` (`DeployVolumeConfig`, `ResolveVolumeBacking`), `ov/enc.go` (`ResolvedBindMount`), `ov/runtime_config.go` (`VolumesPath`)

**`task` (Taskfile)** -- bootstrap only: builds `ov` from source and creates the buildx builder. Source: `Taskfile.yml` + `taskfiles/{Build,Setup}.yml`. All other operations use `ov` directly.

**What gets generated** (`ov generate`):
- `.build/<image>/Containerfile` -- one per image, unconditional `RUN` steps only
- `.build/<image>/traefik-routes.yml` -- traefik dynamic config (only for images with `route` layers)
- `.build/<image>/<fragment_dir>/*.conf` -- init system service configs (driven by `init.yml`, e.g., `supervisor/` for supervisord, `systemd/` for systemd)
- `.build/_layers/<name>` -- symlinks to remote layer directories (only when remote layers used)

Generation is idempotent. `.build/` is disposable and gitignored.

**Generic init system support via `init.yml`:**
- Init systems (supervisord, systemd, s6, etc.) are fully defined in `init.yml` at project root
- Each init system declares: detection rules (`layer_fields`, `layer_files`), build model (`fragment_assembly` or `file_copy`), Go templates for fragment generation, Containerfile stage emission, config assembly, entrypoint, runtime service management commands, and OCI labels
- Adding a new init system requires only editing `init.yml` -- no Go code changes
- `service:` field in layer.yml maps to supervisord (via `layer_fields: [service]`), `*.service` files and `system_services:` map to systemd (via `layer_files` and `layer_fields`)
- Images use `org.overthinkos.init` OCI label to identify their init system at runtime
- Per-init service list stored in `org.overthinkos.services.<init>` label
- Source: `init.yml` (definitions), `ov/init_config.go` (Go structs + loading + template rendering)

**Multi-distro support via `distro:` and `build:` fields:**
- `distro:` — Distro identity tags in priority order: `distro: ["fedora:43", fedora]`. For packages: first matching section wins (override). For tasks: all matching run (additive).
- `build:` — Package formats tied to builders: `build: [rpm]` or `build: [pac, aur]`. ALL formats installed in order. Replaces old `pkg:` field.
- `builds:` — Builder capabilities on builder images (unchanged): `builds: [pixi, npm, cargo]`
- Tags union (`org.overthinkos.tags`) = `["all"]` + distro + build formats — used for task matching
- Source: `ov/config.go` (`ResolvedImage.Distro`, `ResolvedImage.BuildFormats`, `MatchingTasks`), `ov/format_config.go` (YAML config loading), `ov/format_template.go` (template rendering), `ov/init_config.go` (init system config), `distro.yml` + `builder.yml` + `init.yml` (format definitions at project root, referenced via `format_config:` in `images.yml`)

**Pixi manylinux fix:** `ov generate` injects `[system-requirements] libc = { family = "glibc", version = "2.34" }` into every pixi.toml during build if not already present. This fixes pixi 0.66.0's resolver which incorrectly detects the platform as `manylinux_2_28` on glibc 2.42, rejecting `manylinux_2_34` wheels (e.g., pixelflux 1.5.9). Source: `builder.yml` `manylinux_fix` template, rendered by `ov/generate.go`.

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
+-- layers/<name>/            # Layer directories (~137 layers)
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

Skills, agents, and MCP servers live in a separate git submodule at `plugins/`.

**Repository:** `git@github.com:overthinkos/overthink-plugins.git`

```
plugins/
+-- .claude-plugin/marketplace.json   # Central plugin registry
+-- ov/                               # Operations (17 skills)
+-- ov-dev/                           # Development (2 skills, 3 agents, GitHub MCP)
+-- ov-layers/                        # Layer reference (131 skills)
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
- Images with layers that trigger an init system (via `service:`, `port_relay:`, `system_services:`, or `*.service` files) must include the init system's `depends_layer` in their dependency chain. `ov validate` enforces this as a hard error (e.g., supervisord layers need the `supervisord` layer). Detection rules and dependencies are defined in `init.yml`, not hardcoded

For layer-specific rules (install files, packages, port_relay, secrets, cache mounts): `/ov:layer`

**Credential security:** Config files (`settings.yml`, `deploy.yml`) are written with `0600` permissions for new files. `ov` warns if existing files have overly permissive permissions but does not change them — the user must `chmod 600` themselves. Credentials are stored in system keyring when available; plaintext config file is the fallback. `ov settings migrate-secrets` migrates existing plaintext credentials to keyring. `ov doctor` reports credential storage health.

**GPU auto-detection:** `ov` detects host GPU hardware and injects appropriate config at runtime:
- **NVIDIA:** CUDA images get `--gpus all` / CDI device injection automatically
- **AMD ROCm:** Auto-detects `/dev/kfd` and `/dev/dri/renderD*`, injects `HSA_OVERRIDE_GFX_VERSION`, adds `video`/`render` groups. `ov udev` manages KFD device rules. `ov doctor` reports AMD GPU info
- Source: `ov/devices.go` (`DetectNvidiaGPU`, `DetectAMDGPU`)

**Security mounts:** `security.mounts` in `layer.yml` declares host bind mounts or tmpfs needed for device access. Stored in image labels, applied by `ov config`/`ov start`. Format: `host:container:options` (bind mount) or `tmpfs:path:options` (tmpfs). Generates `Volume=` or `Tmpfs=` in quadlets.
- Source: `ov/config.go` (`SecurityConfig.Mounts`), `ov/quadlet.go`, `ov/start.go`

---

## Command Map

Use `ov --help` and `ov <cmd> --help` for quick flag reference. For detailed usage, load the skill.

| Commands | Skill |
|----------|-------|
| `generate`, `validate`, `inspect`, `list`, `new layer` | `/ov:validate` (rules), `/ov:layer` (authoring), `/ov:image` (images) |
| `build`, `merge` | `/ov:build` |
| `shell` | `/ov:shell` |
| `config <image>` (setup: quadlet + secrets + encrypted volumes), `config remove <image>`, `config status/mount/unmount/passwd` | `/ov:config`, `/ov:deploy`, `/ov:enc` (encrypted volumes) |
| `start` (`--enable/--enable=false`), `stop`, `status` (`--all`, `--json`), `logs`, `update`, `remove`, `seed` | `/ov:service` |
| `deploy show/export/import/reset/status/path` | `/ov:deploy` |
| `service start/stop/restart/status` | `/ov:service` |
| `cdp` | `/ov:cdp` |
| `wl sway` | `/ov:wl` (sway subgroup) |
| `record start/stop/list/cmd/term` | `/ov:record` |
| `tmux shell/run/attach/list/capture/send/kill` | `/ov:tmux` |
| `vnc` | `/ov:vnc` |
| `wl` | `/ov:wl` |
| `alias` | `/ov:alias` |
| `settings` (get, set, list, reset, path, migrate-secrets) | `/ov:config` |
| `secrets` (init, list, get, set, delete, import, export, path) | `/ov:secrets` |
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

**Deploy a service:** `ov config <image> -w ~/project` -> saves all deployment state to `~/.config/ov/deploy.yml` -> generates quadlet + provisions secrets + mounts encrypted volumes from image labels + deploy.yml. `--password auto` generates all secrets; `--password manual` prompts. `ov start <image>` auto-configures on first start (`--enable`, default true; suppress with `--enable=false`). No `images.yml` needed for deployment.
Skills: `/ov:deploy` -> `/ov:service` (lifecycle)

**Record a session:**
`ov record start <image> --mode terminal` (asciinema) or `--mode desktop` (pixelflux/wf-recorder) -> `ov record cmd` / `ov record term` (interact) -> `ov record stop <image> -o output`
Skills: `/ov:record` -> `/ov-layers:wl-record-pixelflux` or `/ov-layers:wf-recorder` (desktop) or `/ov-layers:asciinema` (terminal)

**Host bootstrap (first time):** requires `go`, `docker` (or `podman`). Run `bash setup.sh` to download `task`, build `ov`, then `ov build` to build all images. To use podman: `ov settings set engine.build podman`.

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
| `ov` | 17 | Operations | "How do I use X?" |
| `ov-dev` | 2 + 3 agents | Contributing | "How does the code work?" |
| `ov-layers` | 131 | Layer reference | "What does layer X contain?" |
| `ov-images` | 31 | Image reference | "What does image X look like?" |

### Common Skill Chains

Real tasks chain through skills in predictable patterns:

**Author a new layer:**
`/ov:layer` (format, rules) -> `/ov-layers:<similar>` (existing pattern) -> `/ov:image` (add to image) -> `/ov:build`

**Debug a runtime issue:**
`/ov:<operation>` (how it works) -> `/ov-layers:<layer>` (config, deps, ports) -> `/ov:settings` or `/ov:service` (state)

**Desktop automation:**
`/ov:cdp` (DOM: click, type, eval) -> `/ov:wl` (compositor-agnostic: screenshots, input, window mgmt, clipboard, AT-SPI2) -> `/ov:wl` sway subgroup (sway-only: tree, layout, move, resize)
Use CDP first. Use `ov cdp click --wl` for selkies-desktop (no VNC). Use `ov wl` for screenshots, input, window management (`toplevel`, `close`, `fullscreen`), clipboard, and AT-SPI2 accessibility (`ov wl atspi find/click`). Use `ov wl sway` for sway-specific IPC features (tree, workspaces, layout, move, resize).
On NVIDIA headless: `ov wl` is the primary tool — VNC screenshots are gray (upstream wayvnc bug), but `ov wl screenshot` works perfectly with gles2.
For selkies-desktop (labwc): `ov wl` provides full automation. `ov wl sway` commands are sway-specific and won't work on labwc.

**Deploy a service:**
`/ov:deploy` (quadlet, tunnels) + `/ov:config` (setup: secrets, encrypted volumes) -> `/ov-images:<name>` (image config) -> `/ov:service` (lifecycle)

**Set up Selkies streaming (browser-accessible — working):**
`/ov-layers:selkies` (streaming engine) -> `/ov-layers:labwc` (compositor) -> `/ov-layers:waybar-labwc` (panel) -> `/ov-images:selkies-desktop` (image)
Uses labwc nested inside pixelflux's Wayland compositor. Access via `http://localhost:3000` — no client app needed. NVENC detected but fails with driver 590.48 (pixelflux compat issue); CPU x264enc-striped at 60fps works well. Image: `selkies-desktop`.
**Host-side automation:** `ov wl` provides full compositor-agnostic control: screenshots (grim), input (wtype, wlrctl), window management (wlrctl toplevel), clipboard (wl-copy/paste), resolution (wlr-randr), AT-SPI2 introspection (atspi). Use `ov cdp click --wl` for selector-based clicks via Wayland pointer (no VNC needed). Includes `wl-tools` + `a11y-tools` layers.

**Fix a bug in ov:**
`/ov-dev:go` (source map, tests) + `/ov:<relevant>` (expected behavior) -> `/ov:validate` (verify)

**Modify a metalayer:**
`/ov:layer` (metalayer patterns) -> `/ov-layers:<metalayer>` (current composition) + `/ov-layers:<addition>` (what to add)

**Full image lifecycle (build -> deploy -> test):**
`/ov:build` (build image) -> `/ov:deploy` (quadlet, tunnels, volume backing) -> `/ov:service` (config, start, status, logs) -> `/ov-images:<name>` (ports, verification)

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
- **Recording:** `/ov:record` (recording commands, lifecycle) vs `/ov-layers:asciinema` (terminal recording layer) vs `/ov-layers:wf-recorder` (sway desktop recording) vs `/ov-layers:wl-record-pixelflux` (selkies desktop recording)
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