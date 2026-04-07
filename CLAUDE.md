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
- **Build mode:** Parses `images.yml`, resolves layers, generates Containerfiles, builds images. See `/ov:build`, `/ov:generate`.
- **Deploy mode:** Reads OCI labels + `deploy.yml`. `ov config` is the single entry point (quadlet + secrets + volumes + data). See `/ov:config`, `/ov:deploy`.

Source: `ov/`. Registry inspection via go-containerregistry.

**Key subsystems** (refer to skills for full details):

| Subsystem | Summary | Skill |
|-----------|---------|-------|
| Credentials & Secrets | Keyring, KeePass `.kdbx`, GPG-encrypted `.secrets`, kernel keyring caching | `/ov:secrets`, `/ov:config` |
| Volumes | Named, bind, or encrypted (gocryptfs) — deploy-time choice per volume | `/ov:deploy`, `/ov:config` |
| env_provides / requires / accepts | Cross-container env injection with pod-aware resolution, `--update-all` propagation | `/ov:config`, `/ov:layer` |
| mcp_provides / requires / accepts | Cross-container MCP server discovery, consumers receive `OV_MCP_SERVERS` JSON | `/ov:config`, `/ov:layer` |
| Hermes auto-configuration | First-start LLM + MCP + browser config, sentinel-guarded. Delete `config.yaml` to reconfigure | `/ov-layers:hermes` |
| Sidecars | Deploy-time pod composition (`--sidecar tailscale`), dual networking | `/ov:sidecar` |
| Tunnels | Tailscale/Cloudflare with backend schemes (`http`, `https+insecure`, `tcp`, etc.) | `/ov:deploy` |
| Agent Forwarding | SSH/GPG socket forwarding into containers | `/ov:shell` |
| Init Systems | supervisord/systemd, fully defined in `init.yml` — no Go code changes to add new ones | `/ov:generate`, `/ov:layer` |
| Multi-distro | `distro:` identity tags + `build:` package formats, tag-based dispatch in layer files | `/ov:build`, `/ov:layer` |
| Generation | Containerfiles, service configs, traefik routes in `.build/` (disposable, gitignored) | `/ov:generate` |

**`task` (Taskfile)** -- bootstrap only: builds `ov` from source and creates the buildx builder. Source: `Taskfile.yml` + `taskfiles/{Build,Setup}.yml`. All other operations use `ov` directly.

**Pixi manylinux fix:** `ov generate` injects `[system-requirements] libc = { family = "glibc", version = "2.39" }` into every pixi.toml during build if not already present. This fixes pixi 0.66.0's resolver which incorrectly detects the platform as `manylinux_2_28` on glibc 2.42, rejecting `manylinux_2_34` wheels (e.g., pixelflux 1.5.9). Source: `builder.yml` `manylinux_fix` template, rendered by `ov/generate.go`.

**Pixi build scripts:** The pixi builder supports an optional `build_script: build.sh` field in `builder.yml`. If a layer with `pixi.toml` also has a `build.sh`, the script runs in the pixi builder stage after `pixi install` completes. The script is bind-mounted from the layer context (same pattern as the cargo builder's `--mount=type=bind,from=<layer>-ctx`). This allows layers to run build-time logic (compiling C extensions, npm builds, binary patching) without installing build dependencies in the final image. Example: the `selkies` layer uses `build.sh` to pip-install selkies (C extensions need gcc) and build the web UI (needs nodejs/npm) — all in the builder image. Source: `builder.yml` `build_script` field, `ov/generate.go` `buildStageContext()`, `ov/format_template.go` `BuildStageContext.HasBuildScript`.

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
+-- layers/<name>/            # Layer directories (159 layers)
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
+-- ov/                               # Operations (36 skills)
+-- ov-dev/                           # Development (2 skills, 3 agents, GitHub MCP)
+-- ov-jupyter/                       # Jupyter MCP server (notebook collaboration via Streamable HTTP)
+-- ov-layers/                        # Layer reference (159 skills)
+-- ov-images/                        # Image reference (42 skills)
```

Each plugin has a `.claude-plugin/plugin.json` manifest. Skills are at `plugins/<plugin>/skills/<name>/SKILL.md`.

**Enabled via** `.claude/settings.json` (committed):

```json
{
  "enabledPlugins": {
    "ov@ov-plugins": true,
    "ov-dev@ov-plugins": true,
    "ov-jupyter@ov-plugins": true,
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

- Data layers use `data:` field in layer.yml to map source directories to volume targets. Data is staged at `/data/<volume>/` in the image at build time. Provisioned into bind-backed volumes by `ov config` (initial seed) and `ov update` (non-destructive merge). Data layers are valid with only `data:` and `volumes:` — no packages or install files needed
- Data images use `data_image: true` in images.yml — always FROM scratch, no base OS, no runtime, no init system. Only data staging + labels. Used as seed sources via `--data-from`. `ov validate` enforces: no base, no services, no ports
- Layers needing ffmpeg codecs MUST depend on the `ffmpeg` layer (`depends: [ffmpeg]`) rather than independently adding the negativo17 fedora-multimedia repo. The `ffmpeg` layer is the single authoritative install point for nonfree codecs. This avoids repo duplication and ensures consistent codec builds across all images
- `ov merge` handles OCI whiteout semantics: regular whiteouts (`.wh.<name>`), opaque whiteouts (`.wh..wh..opq`), and reintroduction-supersedes-whiteout cases. This prevents EEXIST errors when merging layers that contain file deletions. Source: `ov/merge.go` (`whiteoutTarget`, `mergeLayers`)
- `env_provides:` in `layer.yml` declares env vars for consumers via pod-aware resolution. Template syntax: `{{.ContainerName}}` (only supported variable). Same-container: hostname rewritten to `localhost`. Cross-container: hostname kept as-is. `env:` and `env_provides:` may declare the same key — `env:` is baked into the service's own image (e.g., `OLLAMA_HOST=0.0.0.0`), `env_provides:` is for consumers (e.g., `OLLAMA_HOST=http://ov-ollama:11434`). For same-container, `env:` has higher priority and overrides provides. Cleanup is automatic on `ov config remove` / `ov remove`. `--update-all` on `ov config` propagates to all deployed quadlets
- `env_requires:` in `layer.yml` declares env vars the layer MUST have from the environment (e.g., `OPENROUTER_API_KEY`). At `ov config` time, missing required vars produce warnings. Structure: list of `{name, description, default?}`
- `env_accepts:` in `layer.yml` declares env vars the layer CAN optionally use (e.g., `TELEGRAM_BOT_TOKEN`). No warnings if missing — for documentation only. Same structure as `env_requires`
- `mcp_provides:` in `layer.yml` declares MCP servers injected into OTHER containers at deploy time. List of `{name, url, transport}`. Template: `{{.ContainerName}}`. Pod-aware: same-container entries resolve to `localhost`. Cleanup automatic on remove. `--update-all` propagates
- `mcp_requires:` / `mcp_accepts:` mirror `env_requires` / `env_accepts` but for MCP server dependencies. Same `{name, description}` struct. `mcp_requires` warns at `ov config` time
- `ov start` in quadlet mode requires `ov config` first — no auto-configuration. Direct mode still supports inline flags
- Port protocol annotations control tunnel backend schemes: `"https+insecure:3000"` tells Tailscale to use `https+insecure://` when proxying. Default is `http`. Supported: `http`, `https`, `https+insecure`, `tcp`, `tls-terminated-tcp` (Tailscale); `http`, `https`, `tcp`, `ssh`, `rdp`, `smb` (Cloudflare). Ports with HTTPS backends (like Traefik self-signed) MUST use `https+insecure` to avoid 404 errors from plain HTTP proxying

### Two-Tier Layer Architecture for ML/Python Layers

ML layers follow a two-tier pattern that separates environment ownership from post-install steps:

**Tier 1 — Post-install layers** (no pixi.toml): Install binaries or pip packages into whatever pixi env exists. Reusable across images.
- `llama-cpp`: downloads prebuilt binaries + GGUF tools. Sets `LLAMA_CPP_PATH` env and PATH
- `unsloth`: pip installs vLLM wheel + unsloth + unsloth-zoo + vLLM torch.compile patch (`patch_vllm_size_nodes.py`) for `_decompose_size_nodes` bug (upstream: vllm-project/vllm#38360). vLLM 0.19 runtime deps (opentelemetry-*) installed via pip --no-deps after wheel (pixi conda/PyPI resolver conflict). Sets `HF_HOME`, `UNSLOTH_SKIP_LLAMA_CPP_INSTALL`
- `jupyter-colab-mcp`: CRDT MCP server extension (fastmcp + jupyter_colab_mcp). Installs into parent pixi env

**Tier 2 — Environment-owner layers** (have pixi.toml): Define the complete Python environment. Compose Tier 1 layers via `layers:` field.
- `python-ml`: core ML env (PyTorch, vLLM runtime deps, HF core). `layers: [llama-cpp]`
- `jupyter-colab`: lightweight Jupyter + CRDT collaboration. `layers: [jupyter-colab-mcp]`
- `jupyter-colab-ml`: full ML + Jupyter + CRDT MCP. Includes gcc/gcc-c++ for triton 3.6+ JIT compilation. `layers: [llama-cpp, unsloth, jupyter-colab-mcp]`
- `unsloth-studio`: fine-tuning env with studio UI. `layers: [llama-cpp, unsloth]`

**Key constraint:** The generator creates intermediate images for shared layers. Tier 1 layers with pip installs (like `unsloth`) must NOT be extracted into intermediates — they need the pixi env from Tier 2. The generator handles this correctly as long as pip-install layers don't have standalone pixi.toml. Only Tier 2 layers own pixi.toml; Tier 1 user.yml runs after pixi COPY in the final image build.

**ML training gotchas:**
- Ministral/Pixtral models require `UNSLOTH_ENABLE_FLEX_ATTENTION=0` due to nested torch.compile bug between unsloth + transformers 5.5 masking_utils
- Pixtral-12B requires `max_memory={0: "14GB"}` in `from_pretrained()` because accelerate's device_map uses uncompressed BF16 sizes
- TRL 1.0 requires `packing=True` in SFTConfig when unsloth auto-enables `padding_free=True`

- Meta-layers CAN have both `depends:` and `layers:` (e.g., `unsloth-studio` has `depends: [cuda, supervisord]` + `layers: [llama-cpp, unsloth]`)
- Meta-layers CAN own pixi.toml (environment-owner pattern — exactly one pixi.toml per image)

**Hermes Agent layer** (`hermes`) follows the Tier 2 pattern with `build.sh` (same as selkies): pixi.toml defines the Python env, build.sh clones the hermes-agent repo, pip installs it, and sets up npm deps. The `hermes-playwright` layer is a Tier 1 add-on for Playwright + Chromium.

**Hermes automatic LLM provider configuration** — The `hermes-entrypoint` auto-detects and configures LLM providers on first start based on environment variables. ALL providers whose env vars are set get registered as `custom_providers` in `config.yaml`; priority only determines the default model:

| Priority | Env var | Provider | Default model | Base URL |
|----------|---------|----------|---------------|----------|
| 1 | `OLLAMA_HOST` | Local Ollama (`custom`) | `qwen2.5-coder:32b` | `${OLLAMA_HOST}/v1` |
| 2 | `OLLAMA_API_KEY` | Ollama Cloud (`custom`) | `kimi-k2.5:cloud` | `https://ollama.com/v1` |
| 3 | `OPENROUTER_API_KEY` | OpenRouter (built-in) | `qwen/qwen3.6-plus:free` | *(managed by hermes)* |

Single-phase configuration: on first start (guarded by `# ov:auto-configured` sentinel), registers ALL available LLM providers and MCP servers, sets the default model. API keys synced to `.env` on every start for rotation. Override model with `HERMES_MODEL` env var. Switch providers mid-session: `hermes chat --provider openrouter` or `/model custom:ollama-cloud:kimi-k2.5:cloud`. To reconfigure: delete `/opt/data/config.yaml` and restart.

**Provider-specific notes:**
- **Ollama Cloud** uses Bearer token auth (`OLLAMA_API_KEY`) at `https://ollama.com/v1`. `kimi-k2.5:cloud` is a thinking/reasoning model — responses include a `reasoning` field before `content`
- **OpenRouter** is a built-in hermes provider — no `custom_providers` entry needed, just `OPENROUTER_API_KEY` in `.env`
- **Local Ollama** requires `api_key: 'ollama'` in `custom_providers` — the OpenAI SDK requires a non-empty value but Ollama ignores it
- Auxiliary tasks (`compression`, `web_extract`, `vision`) are routed through the default provider
- Source: `layers/hermes/hermes-entrypoint` (auto-config logic), `layers/hermes/layer.yml` (`env_accepts`)

**Build.sh and npm gotchas** (discovered during hermes testing):
- Playwright `npx playwright install --with-deps` does NOT support Fedora — falls back to Ubuntu's `apt-get`. Workaround: install Chromium system deps via rpm packages in `layer.yml`, browser binary via `npx playwright install chromium` (without `--with-deps`) in `root.yml`
- npm packages installed globally (via the npm builder's `package.json`) are in `~/.npm-global/lib/node_modules/` and need `NODE_PATH` to be `require()`d. For project-local deps (like agent-browser), install in `build.sh` instead of `package.json`
- `sounddevice` Python library needs `portaudio` rpm at runtime (not just build time)
- When `root.yml` installs Playwright browsers with `HOME=/tmp`, set `PLAYWRIGHT_BROWSERS_PATH=/tmp/.cache/ms-playwright` in `layer.yml` env so the runtime user finds them

For layer-specific rules (install files, packages, port_relay, secrets, data, env_provides, env_requires, env_accepts, cache mounts): `/ov:layer`

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
| `generate` | `/ov:generate` |
| `validate` | `/ov:validate` |
| `inspect` | `/ov:inspect` |
| `list` (images, layers, targets, services, routes, volumes, aliases) | `/ov:list` |
| `new layer` | `/ov:new` |
| `build` | `/ov:build` |
| `merge` | `/ov:merge` |
| `cmd <image> <command>` | `/ov:cmd` |
| `shell` | `/ov:shell` |
| `dbus` (notify, call, list, introspect) | `/ov:dbus` |
| `config <image>` (setup: quadlet + secrets + encrypted volumes + data provisioning + env_provides + sidecars), `config --sidecar`, `config --list-sidecars`, `config --update-all`, `config remove`, `config status/mount/unmount/passwd` | `/ov:config`, `/ov:deploy`, `/ov:sidecar`, `/ov:enc` |
| `start` | `/ov:start` (requires `ov config` first in quadlet mode) |
| `stop` | `/ov:stop` |
| `status` (`--all`, `--json`) | `/ov:status` |
| `logs` | `/ov:logs` |
| `update` (`--seed`, `--force-seed`, `--data-from`) | `/ov:update` |
| `remove` (`--purge`, `--keep-deploy`) | `/ov:remove` |
| `deploy show/export/import/reset/status/path` | `/ov:deploy` |
| `service start/stop/restart/status` | `/ov:service` |
| `cdp`, `cdp spa` (click, type, key, key-combo, mouse, status) | `/ov:cdp` |
| `wl sway` | `/ov:wl` (sway subgroup) |
| `wl overlay show/hide/list/status` | `/ov:wl-overlay` |
| `record start/stop/list/cmd` | `/ov:record` |
| `tmux shell/cmd/run/attach/list/capture/send/kill` | `/ov:tmux` |
| `vnc` | `/ov:vnc` |
| `wl` | `/ov:wl` |
| `alias` | `/ov:alias` |
| `settings` (get, set, list, reset, path, migrate-secrets) | `/ov:settings` |
| `version` | `/ov:version` |
| `secrets` (init, list, get, set, delete, import, export, path) | `/ov:secrets` |
| `secrets gpg` (show, env, edit, encrypt, decrypt, set, unset, add-recipient, recipients, import-key, export-key, setup, doctor) | `/ov:secrets` |
| `udev status/generate/install/remove` | `/ov:udev` |
| `vm` | `/ov:vm` |
| `doctor` | `/ov:doctor` |

---

## Workflows

**Add a layer:** `ov new layer <name>` -> edit `layer.yml` -> add install files -> add to image in `images.yml` -> `ov build <image>`
Skills: `/ov:layer` -> `/ov-layers:<similar>` (pattern reference) -> `/ov:image` -> `/ov:build`

**Add an image:** add entry to `images.yml` -> `ov build <image>`
Skills: `/ov:image` -> `/ov-images:<similar>` (pattern reference) -> `/ov:build`

**Layer images:** set `base` to another image name in `images.yml`. The generator handles dependency ordering and tag resolution.

**Deploy a service:** `ov config <image> -w ~/project` -> saves all deployment state to `~/.config/ov/deploy.yml` -> generates quadlet + provisions secrets + mounts encrypted volumes + provisions data from data layers into bind-backed volumes + injects `env_provides` vars into global deploy.yml env for cross-container discovery + warns about missing `env_requires` vars. `--password auto` generates all secrets; `--password manual` prompts. `--seed` (default true) provisions data layers; `--force-seed` re-provisions; `--data-from <image>` seeds from a separate data image. `--update-all` regenerates quadlets for all other deployed images to pick up env_provides changes. `ov start <image>` requires `ov config` first in quadlet mode. No `images.yml` needed for deployment.
Skills: `/ov:config` (setup) -> `/ov:deploy` (deploy.yml) -> `/ov:start` -> `/ov:update` (data sync) -> `/ov:service` (lifecycle)

**Record a session:**
`ov record start <image> --mode terminal` (asciinema) or `--mode desktop` (pixelflux/wf-recorder) -> `ov record cmd` (interact) -> `ov record stop <image> -o output`
Skills: `/ov:record` -> `/ov-layers:wl-record-pixelflux` or `/ov-layers:wf-recorder` (desktop) or `/ov-layers:asciinema` (terminal)
Use `/ov:wl-overlay` for in-recording overlays (title cards, lower-thirds, countdowns, fade transitions) — composited by the compositor, appear natively in recordings without post-production.

**Deploy with Tailscale exit node:**
`ov secrets gpg set TS_AUTHKEY <key>` -> `ov config <image> --sidecar tailscale -e TS_HOSTNAME=<name> -e "TS_EXTRA_ARGS=--exit-node=<ip> --exit-node-allow-lan-access"` -> `ov start <image>` -> `podman exec ov-<image>-tailscale tailscale set --exit-node=<ip> --exit-node-allow-lan-access` (first time only)
Skills: `/ov:sidecar` (templates, pod architecture) + `/ov:secrets` (auth key) -> `/ov:config` (--sidecar flag) -> `/ov-images:<name>` (verification)

**Host bootstrap (first time):** requires `go`, `docker` (or `podman`). Run `bash setup.sh` to download `task`, build `ov`, then `ov build` to build all images. To use podman: `ov settings set engine.build podman`.

---

## Task Commands (bootstrap only)

- `task build:ov` -- Build ov from source into `bin/ov` and install as Arch package (auto-calls `build:install`)
- `task build:install` -- Install ov as Arch package (uses pre-built binary from `bin/ov` via PKGBUILD, fast ~2s)
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
| `ov` | 36 | Operations | "How do I use X?" |
| `ov-dev` | 2 + 3 agents | Contributing | "How does the code work?" |
| `ov-jupyter` | 1 MCP server | Notebook MCP | "How do I use the notebook MCP tools?" |
| `ov-layers` | 159 | Layer reference | "What does layer X contain?" |
| `ov-images` | 42 | Image reference | "What does image X look like?" |

### Common Skill Chains

Real tasks chain through skills in predictable patterns:

**Author a new layer:**
`/ov:layer` (format, rules) -> `/ov-layers:<similar>` (existing pattern) -> `/ov:image` (add to image) -> `/ov:build`

**Debug a runtime issue:**
`/ov:<operation>` (how it works) -> `/ov-layers:<layer>` (config, deps, ports) -> `/ov:settings` or `/ov:service` (state)

**Desktop automation:**
`/ov:cdp` (DOM: click, type, eval) -> `/ov:wl` (compositor-agnostic: screenshots, input, window mgmt, clipboard, AT-SPI2) -> `/ov:wl` sway subgroup (sway-only: tree, layout, move, resize) -> `/ov:wl-overlay` (recording overlays: title cards, lower-thirds, countdowns, highlights, fades)
Use CDP first. Use `ov cdp click --wl` for selkies-desktop (no VNC). Use `ov wl` for screenshots, input, window management (`toplevel`, `close`, `fullscreen`), clipboard, and AT-SPI2 accessibility (`ov wl atspi find/click`). Use `ov wl sway` for sway-specific IPC features (tree, workspaces, layout, move, resize).
On NVIDIA headless: Both `ov vnc screenshot` and `ov wl screenshot` work correctly. VNC images use pixman (software renderer) via `sway-desktop-vnc`, with a DPMS workaround for wayvnc 0.9.1's headless power event bug.
For selkies-desktop (labwc): `ov wl` provides full automation. `ov wl sway` commands are sway-specific and won't work on labwc.

**Deploy a service:**
`/ov:deploy` (quadlet, tunnels) + `/ov:config` (setup: secrets, encrypted volumes) -> `/ov-images:<name>` (image config) -> `/ov:service` (lifecycle)

**Set up Selkies streaming (browser-accessible — working):**
`/ov-layers:selkies` (streaming engine) -> `/ov-layers:labwc` (compositor) -> `/ov-layers:waybar-labwc` (panel) -> `/ov-images:selkies-desktop` (image)
Uses labwc nested inside pixelflux's Wayland compositor. Access via `https://localhost:3000` (HTTPS with self-signed Traefik cert — required for WebCodecs secure context). NVENC detected but fails with driver 590.48 (pixelflux compat issue); CPU x264enc-striped at 60fps works well. Image: `selkies-desktop`.
**Host-side automation:** `ov wl` provides full compositor-agnostic control: screenshots (pixelflux-screenshot via capture bridge), input (wtype, wlrctl), window management (wlrctl toplevel), clipboard (wl-copy/paste), resolution (wlr-randr), AT-SPI2 introspection (atspi). Use `ov cdp click --wl` for selector-based clicks via Wayland pointer (no VNC needed). Screenshots work with or without a browser connected (capture bridge auto-switches between controller/viewer modes). Includes `wl-tools` + `a11y-tools` layers.
**Client-side interaction (browser-based RD):** The Selkies SPA uses a transparent `input#overlayInput` (z-index 3) on top of `canvas#videoCanvas` (z-index 2, pointer-events: none) to capture mouse/keyboard events. Events pass through the SPA's JavaScript → WebSocket → labwc. Keyboard passthrough works via VNC type, wtype, or CDP Input.dispatchKeyEvent — the SPA's onkeydown handler captures with stopImmediatePropagation. **Limitation:** Super key consumed by the client's compositor, Ctrl+T/W consumed by the client's Chrome — browser-based RD cannot forward compositor or browser shortcuts. Mouse coordinates have ~0.82x scaling between input and remote cursor position. Session state (all windows, typed text) survives client disconnection. See `/ov-images:selkies-desktop` for full DOM structure and coordinate mapping.

**Programmatic notebook access (MCP):**
`/ov-layers:jupyter-colab` (lightweight, no GPU) or `/ov-layers:jupyter-colab-ml` (full CUDA ML stack) or `/ov-layers:jupyter-colab-ml` + `/ov-layers:notebook-finetuning` + `/ov-layers:notebook-ollama` + `/ov-layers:notebook-llm-on-supercomputers` (ML + fine-tuning + Ollama + LLM course notebooks) -> `/ov-images:jupyter-colab` or `/ov-images:jupyter-colab-ml` or `/ov-images:jupyter-colab-ml-notebook` (deployment) -> `/ov:service` (lifecycle)
Start the service, then use MCP tools (`list_notebooks`, `open_notebook_session`, `insert_cell`, `execute_cell`, `watch_notebook`) for AI-driven notebook editing with real-time collaboration. Multiple MCP clients can edit the same notebook simultaneously — changes sync via CRDT. Use `jupyter-colab-ml-notebook` for GPU/ML with fine-tuning, Ollama, and LLM course notebooks; `jupyter-colab-ml` for GPU/ML without; `jupyter-colab` for lightweight multi-arch environments.

**Fix a bug in ov:**
`/ov-dev:go` (source map, tests) + `/ov:<relevant>` (expected behavior) -> `/ov:validate` (verify)

**Modify a metalayer:**
`/ov:layer` (metalayer patterns) -> `/ov-layers:<metalayer>` (current composition) + `/ov-layers:<addition>` (what to add)

**Deploy Hermes Agent:**
`/ov-layers:hermes` (layer properties) -> `/ov-images:hermes` (image config) -> `/ov:config` (setup + provider env vars) -> `/ov:start` -> `/ov:service` (lifecycle)
For browser automation, use `/ov-images:hermes-playwright` instead. Hermes npm deps (agent-browser, camoufox-browser) are project-local (in `~/hermes-agent/node_modules/`), not global. LLM provider auto-configured from `OLLAMA_HOST` / `OLLAMA_API_KEY` / `OPENROUTER_API_KEY` env vars passed via `ov config -e`.

**Deploy Hermes with Selkies desktop (separate pods):**
Deploy three separate containers communicating via `env_provides`/`mcp_provides`:
```
ov config selkies-desktop && ov start selkies-desktop          # provides BROWSER_CDP_URL
ov config jupyter-colab --update-all && ov start jupyter-colab  # provides jupyter-colab MCP
ov config hermes-full -e OLLAMA_API_KEY=... --update-all && ov start hermes-full  # consumes both
```
The `--update-all` flag propagates provides to all deployed quadlets. Hermes auto-receives `BROWSER_CDP_URL=http://ov-selkies-desktop:9222` and `OV_MCP_SERVERS` with `http://ov-jupyter-colab:8888/mcp` and `http://ov-selkies-desktop:9224/mcp` (chrome-devtools, 29 browser automation tools). Browser tools (`browser_navigate`, `browser_click`, `browser_snapshot`) control the desktop Chrome visible at `:3000`. The `cdp-proxy` in the chrome layer rewrites Host headers and response URLs for Chrome 146+ cross-container compatibility (two-port architecture: Chrome on internal 9223, proxy on external 9222).

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
- **Jupyter:** `/ov-layers:jupyter` (legacy GPU/ML monolithic layer) vs `/ov-layers:jupyter-colab` (lightweight, no GPU + collaboration + MCP server with 13 tools) vs `/ov-layers:jupyter-colab-ml` (full CUDA ML + collaboration + MCP, meta-layer composing llama-cpp + unsloth) vs `/ov-images:jupyter` (legacy GPU image) vs `/ov-images:jupyter-colab` (lightweight image) vs `/ov-images:jupyter-colab-ml` (GPU image with full ML stack + MCP) vs `/ov-images:jupyter-colab-ml-notebook` (GPU image + 37 Unsloth fine-tuning notebooks + 6 Ollama integration notebooks + 15 LLM course notebooks). The `ov-jupyter` plugin provides the Streamable HTTP MCP server at `/mcp` for programmatic notebook access
- **OpenClaw:** `/ov:openclaw` (gateway config) vs `/ov-layers:openclaw` (layer properties) vs `/ov-images:openclaw` (image definition)
- **Chrome/CDP:** `/ov:cdp` (CDP commands) vs `/ov-layers:chrome` (ports, relay, shm_size, chrome-devtools-mcp sub-layer) vs `/ov-layers:chrome-devtools-mcp` (MCP server on 9224, 29 tools via mcp-proxy) vs `/ov-layers:chrome-sway` (sway integration)
- **Sway:** `/ov:wl` sway subgroup (`ov wl sway <cmd>`, compositor commands) vs `/ov-layers:sway` (layer properties) vs `/ov-layers:sway-desktop` (desktop metalayer)
- **VNC:** `/ov:vnc` (VNC commands, auth) vs `/ov-layers:wayvnc` (VNC server layer properties)
- **Niri:** `/ov-layers:niri` (compositor, built from source) vs `/ov-layers:niri-desktop` (desktop metalayer)
- **KWin:** `/ov-layers:kwin` (compositor, virtual backend) vs `/ov-layers:kwin-desktop` (desktop metalayer)
- **Mutter:** `/ov-layers:mutter` (compositor, headless) vs `/ov-layers:mutter-desktop` (desktop metalayer)
- **X11 Desktop:** `/ov-layers:xorg-headless` (display server) vs `/ov-layers:openbox` (window manager) vs `/ov-layers:x11-desktop` (desktop metalayer)
- **D-Bus/Notifications:** `ov dbus` (native Go D-Bus commands) vs `/ov-layers:dbus` (session bus layer) vs `/ov-layers:swaync` (notification daemon) vs `/ov-layers:libnotify` (`notify-send` CLI)
- **Command Execution:** `ov cmd` (single command with notification) vs `ov shell -c` (full container setup) vs `ov tmux cmd` (send to tmux session) vs `ov record cmd` (send to recording session)
- **Recording:** `/ov:record` (recording commands, lifecycle) vs `/ov-layers:asciinema` (terminal recording layer) vs `/ov-layers:wf-recorder` (sway desktop recording) vs `/ov-layers:wl-record-pixelflux` (selkies desktop recording)
- **Overlays:** `/ov:wl-overlay` (overlay commands, types, recording workflow) vs `/ov-layers:wl-overlay` (layer properties, gtk4-layer-shell deps)
- **Selkies:** `/ov-layers:selkies` (streaming engine, pixelflux/pcmflux) vs `/ov-layers:labwc` (nested Wayland compositor for selkies, waits for pixelflux socket) vs `/ov-layers:waybar-labwc` (panel for labwc) vs `/ov-layers:selkies-desktop` (desktop metalayer) vs `/ov-images:selkies-desktop` (image)
- **Hermes:** `/ov-layers:hermes` (agent layer: pixi env, build.sh, service, volumes, auto-provider-config) vs `/ov-layers:hermes-full` (metalayer: hermes + claude-code + codex + gemini + dev-tools + devops-tools + ov) vs `/ov-layers:hermes-playwright` (Playwright + Chromium system deps) vs `/ov-images:hermes` (minimal headless) vs `/ov-images:hermes-full` (full-featured standalone) vs `/ov-images:hermes-playwright` (with local browser). Deploy separately alongside `selkies-desktop` (provides `BROWSER_CDP_URL`) and `jupyter-colab` (provides MCP). Auto-provider-config: set `OLLAMA_HOST`, `OLLAMA_API_KEY`, or `OPENROUTER_API_KEY` → hermes auto-configures on first start
- **Tunnels:** `/ov:deploy` (tunnel providers, backend schemes, quadlet integration, deploy.yml) vs `/ov:layer` (port protocol annotations, `ports:` field syntax) vs `/ov:config` (tunnel setup at deploy time)
- **Sidecars/Tailscale:** `/ov:sidecar` (sidecar config, pod networking, exit node routing) vs `/ov:deploy` (tunnel: tailscale host-based serve, deploy.yml sidecars field) vs `/ov-images:selkies-desktop` (full Tailscale deployment example)

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

**CDP → SPA bridge:** Use `ov cdp spa key-combo <image> <tab> super+e` to send modifier combos (Super, Ctrl+T, Alt+F4) through the SPA to the remote desktop. CDP Input events bypass the local compositor and Chrome shortcut handlers -- this is the only way to send these combos to the remote desktop. Use `ov cdp spa click --scale 0.824,0.836` for coordinate-corrected mouse clicks on the SPA canvas.
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