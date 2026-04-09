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
| env_provides / requires / accepts | Cross-container env injection: filtered by consumer `env_accepts`/`env_requires`, pod-aware resolution, `--update-all` propagation. `env_requires` is a hard error | `/ov:config`, `/ov:layer` |
| mcp_provides / requires / accepts | Cross-container MCP server discovery, consumers receive `OV_MCP_SERVERS` JSON | `/ov:config`, `/ov:layer` |
| Hermes auto-configuration | First-start LLM + MCP + browser config, sentinel-guarded. Delete `config.yaml` to reconfigure | `/ov-layers:hermes` |
| Sidecars | Deploy-time pod composition (`--sidecar tailscale`), dual networking | `/ov:sidecar` |
| Tunnels | Tailscale/Cloudflare with backend schemes (`http`, `https+insecure`, `tcp`, etc.) | `/ov:deploy` |
| Agent Forwarding | SSH/GPG socket forwarding into containers | `/ov:shell` |
| Init Systems | supervisord/systemd, fully defined in `init.yml` — no Go code changes to add new ones | `/ov:generate`, `/ov:layer` |
| Multi-distro | `distro:` identity tags + `build:` package formats, tag-based dispatch in layer files | `/ov:build`, `/ov:layer` |
| Generation | Containerfiles, service configs, traefik routes in `.build/` (disposable, gitignored) | `/ov:generate` |

**`task` (Taskfile)** -- bootstrap only: builds `ov` from source and creates the buildx builder. Source: `Taskfile.yml` + `taskfiles/{Build,Setup}.yml`. All other operations use `ov` directly.

**Builder internals** (pixi manylinux fix, pixi build scripts, build.sh pattern): See `/ov:build`, `/ov:generate`.

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

Skills, agents, and MCP servers live in `plugins/` (git submodule: `git@github.com:overthinkos/overthink-plugins.git`). Contains 5 plugins: `ov` (37 operation skills), `ov-dev` (3 dev skills, 3 agents), `ov-jupyter` (MCP server), `ov-layers` (160 layer skills), `ov-images` (40 image skills). Enabled via `.claude/settings.json`. Clone: `git clone --recurse-submodules`. Update: `git submodule update --remote plugins`. See `/ov-dev:skills` for skill maintenance guidelines.

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
- Layers needing ffmpeg codecs MUST depend on the `ffmpeg` layer (`depends: [ffmpeg]`) rather than independently adding the negativo17 fedora-multimedia repo. The `ffmpeg` layer is the single authoritative install point for nonfree codecs. This avoids repo duplication and ensures consistent codec builds across all images. **However**, any layer that installs its own packages from `fedora-multimedia` (e.g., `cuda` installing CUDA dev packages) MUST still declare `repos:` in its own `layer.yml` — the Containerfile generator only adds `--enable-repo` for repos in the layer's own section, not from dependencies
- `ov merge` handles OCI whiteout semantics: regular whiteouts (`.wh.<name>`), opaque whiteouts (`.wh..wh..opq`), and reintroduction-supersedes-whiteout cases. This prevents EEXIST errors when merging layers that contain file deletions. Source: `ov/merge.go` (`whiteoutTarget`, `mergeLayers`)
- Cross-container service discovery (`env_provides`/`env_requires`/`env_accepts`, `mcp_provides`/`mcp_requires`/`mcp_accepts`): See `/ov:layer` for declaration syntax, `/ov:config` for resolution behavior (pod-aware, `--update-all` propagation, cleanup on remove)
- `env_provides` filtering: cross-image env injection only injects vars the consumer declared in `env_accepts` or `env_requires`. Self-provides (same image) always pass for pod-local resolution. Internal services (redis, postgresql) that bind to loopback MUST NOT declare `env_provides` — they're unreachable from other containers
- `env_requires` is a hard error: `ov config` aborts before writing the quadlet if any `env_requires` var is missing, with clear instructions showing exactly what to provide and how
- Sidecar DNS: Tailscale sidecars use `TS_ACCEPT_DNS=false` to prevent DNS takeover. Pod quadlets include `--dns=10.89.0.1 --dns=100.100.100.100 --dns-search=dns.podman` for container DNS + MagicDNS + external DNS. See `/ov:sidecar`
- `ov start` in quadlet mode requires `ov config` first — no auto-configuration. Direct mode still supports inline flags
- Port protocol annotations control tunnel backend schemes: `"https+insecure:3000"` tells Tailscale to use `https+insecure://` when proxying. Ports with HTTPS backends (like Traefik self-signed) MUST use `https+insecure`. See `/ov:deploy` for supported schemes

### Instance Support

Multiple containers of the same image via `-i <instance>`:
- Container name: `ov-<image>-<instance>`, deploy key: `image/instance` in deploy.yml
- All commands accept `-i`. See `/ov:config` (MCP disambiguation, provides cleanup), `/ov:deploy` (deploy.yml structure)

For layer-specific rules (install files, packages, port_relay, secrets, data, env_provides, env_requires, env_accepts, cache mounts): `/ov:layer`

**Credential security:** See `/ov:config` for keyring, KeePass, and config file backends. `ov doctor` reports credential storage health.

**GPU auto-detection:** `ov` detects host GPU hardware and injects appropriate config at runtime. See `/ov:doctor` for detection details, `/ov-layers:nvidia` for NVIDIA, `/ov-layers:rocm` for AMD

**Security mounts:** `security.mounts` in `layer.yml` — host bind mounts or tmpfs for device access. See `/ov:layer`

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
| `ov` | 37 | Operations | "How do I use X?" |
| `ov-dev` | 3 + 3 agents | Contributing | "How does the code work?" |
| `ov-jupyter` | 1 MCP server | Notebook MCP | "How do I use the notebook MCP tools?" |
| `ov-layers` | 160 | Layer reference | "What does layer X contain?" |
| `ov-images` | 40 | Image reference | "What does image X look like?" |

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

When multiple skills cover one topic, start with the `/ov:X` skill for usage, then drill into `/ov-layers:X` or `/ov-images:X` for configuration details. Each skill's cross-references section lists related skills. Key overlapping areas: Jupyter (6 layer/image variants + MCP), Chrome/CDP (commands vs layer vs MCP sub-layer), Selkies (streaming + compositor + desktop + image), Hermes (agent + metalayer + 2 image variants), Open WebUI (web UI + auto-config, alternative to Hermes for LLM interaction), Tunnels (`/ov:deploy` vs `/ov:config` vs `/ov:sidecar`), Desktop compositors (sway/niri/kwin/mutter each have compositor + desktop metalayer skills).

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