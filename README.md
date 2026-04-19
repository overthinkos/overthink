# Overthink

**The container management experience for you and your AI.**

Building containers sounds simple — until you need CUDA drivers, a Wayland desktop inside a container, fine-grained device access for KVM without giving away root, or half a dozen services wired together with the right permissions. Overthink takes care of all of that. Describe what you need in a simple layer list, and `ov` composes it into optimized multi-stage container images — from an interactive dev shell to a running service to a systemd unit to a bootable VM. Works the same way whether you're at the keyboard or your AI agent is driving.

160 layers. 41 image definitions (31 enabled by default). Docker and Podman. `linux/amd64`. Fedora, Debian, and Arch Linux. One CLI: `ov`. Every layer, image, and command has a dedicated skill — 243 skills across 4 plugins (`ov`, `ov-layers`, `ov-images`, `ov-dev`).

*The name comes from the German "überdenken" — to think something through carefully. Not quite the same as the English "overthink," but let's be honest: `ov` really is trying its best to overthink absolutely everything.*

## Why Overthink?

Containers are a great idea with rough edges. The basics work well enough, but real-world needs pile up fast: GPU passthrough with the right driver stack, containers that need `/dev/kvm` or virtualization access without blanket `--privileged`, multiple services managed together, encrypted volumes, VNC or browser-streamed desktops, device permissions that don't compromise your host. Each of these is solvable — but solving them all at once, reliably, across images, is where things get hard. And if you're working with an AI agent that needs to build and manage these containers too, the complexity compounds.

Overthink treats container images like composable building blocks. Each **layer** is a self-contained unit — its packages, environment variables, services, volumes, security declarations, and dependencies described in a simple `layer.yml`. An **image** is just a list of layers on top of a base. The `ov` CLI resolves the dependency graph, generates optimized Containerfiles with multi-stage builds and cache mounts, and builds everything in the right order — handling the hard parts so you (and your AI) don't have to.

Want a GPU-accelerated Jupyter notebook? That's `cuda` + `jupyter` — two layers, one image definition. Need to add Ollama for local LLMs? Add the `ollama` layer. Want a full AI workstation with a Wayland desktop, Chrome, VNC, and an AI gateway? Still just a list of layers in `image.yml`. Overthink handles the rest: dependency resolution, build ordering, supervisor configs, traefik routes, volume declarations, security mounts, and GPU passthrough.

### Sandboxed AI Desktops

One of Overthink's design goals is running sandboxed [OpenClaw](https://github.com/overthinkos/openclaw) systems. The approach flips the usual AI sandboxing model: instead of restricting what the AI agent can do, Overthink gives it full access to a complete desktop environment — Chrome, a Wayland compositor, development tools, network services — and sandboxes the entire desktop inside a container managed by `ov`. The AI agent operates freely within its environment while the host stays fully isolated. This is how images like `openclaw-sway-browser` and `openclaw-ollama-sway-browser` work: a full AI workstation with no host compromise.

### AI Agent Integration

Overthink includes the [Hermes Agent](https://github.com/NousResearch/hermes-agent) — a self-improving AI agent with voice, messaging, and tool-calling. Deploy it with a single command and it auto-configures its LLM provider from environment variables:

```bash
# Ollama Cloud (no local GPU needed)
ov config hermes -e OLLAMA_API_KEY=your-key
ov start hermes

# Or OpenRouter
ov config hermes -e OPENROUTER_API_KEY=sk-or-xxx

# Or local Ollama sidecar (auto-discovered via env_provides)
ov config ollama --update-all && ov start ollama
ov config hermes && ov start hermes
```

All providers whose keys are present get registered simultaneously — the priority order (`OLLAMA_HOST` > `OLLAMA_API_KEY` > `OPENROUTER_API_KEY`) only determines the default. Switch mid-session with `hermes chat --provider openrouter`. MCP servers from co-deployed services are auto-discovered too — deploy `jupyter` alongside `hermes` and hermes automatically connects to the jupyter MCP server (13 tools for notebook manipulation) via `OV_MCP_SERVERS` — no manual MCP configuration needed.

Deploy as separate pods for a full AI workstation: `selkies-desktop` (desktop Chrome at `:3000`), `hermes` (agent + AI CLIs + dev tools), and `jupyter` (notebooks at `:8888`). The chrome layer's `env_provides: BROWSER_CDP_URL` auto-injects `http://ov-selkies-desktop:9222` into the hermes quadlet. Hermes browser tools (`browser_navigate`, `browser_click`, `browser_snapshot`) control the desktop Chrome across the container network — the user watches hermes browse in real-time. A `cdp-proxy` in the chrome layer handles Chrome 146+ Host header validation for cross-container compatibility.

## Key Concepts

### Layers, Images, and Multi-Service Containers

A layer is a reusable building block — packages, config, services. An image is layers stacked on a base. The key insight: **you can combine multiple services into a single container image** just by listing layers. Need PostgreSQL, Redis, a Python API, and a reverse proxy in one container? Add those four layers to your image. `ov` resolves dependencies, generates an optimized Containerfile, and wires up the init system (supervisord for containers, systemd for bootc VMs) to run all services together when the container starts.

When services run as separate containers, **service discovery happens automatically**. A layer can declare `env_provides` — environment variables (with `{{.ContainerName}}` templates) that get injected into all other deployed containers at `ov config` time. For example, deploying `ollama` automatically provides `OLLAMA_HOST=http://ov-ollama:11434` to every other container — no manual environment setup needed. Similarly, `mcp_provides` declares MCP servers that get auto-discovered by consumers like Hermes — deploying `jupyter` automatically registers its MCP server (`http://{{.ContainerName}}:8888/mcp`) with any hermes instance, even when they run in the same container (pod-aware resolution to `localhost`). Layers can also declare `env_requires`/`mcp_requires` (mandatory) and `env_accepts`/`mcp_accepts` (optional) for documentation and deploy-time validation.

### Building Layers: Package Managers & Config Files

Each layer lives in its own directory under `layers/` and can use any combination of these files:

- **`layer.yml`** — The layer's manifest: system packages with tag-based dispatch (`rpm:` for Fedora/RHEL, `deb:` for Debian/Ubuntu, `pac:` for Arch Linux, `aur:` for AUR, plus distro/version tags like `fedora:`, `fedora:43:`), dependencies on other layers, environment variables, cross-container env injection (`env_provides`), MCP server discovery (`mcp_provides`), dependency declarations (`env_requires`/`env_accepts`, `mcp_requires`/`mcp_accepts`), ports, services, volumes, routes, metadata (`version`, `status`, `info`), layer-local build variables (`vars:` for `${VAR}` substitution), and the `tasks:` install list.
- **`tasks:` inside `layer.yml`** — Ordered install operations. Eight verbs: `cmd` (shell), `mkdir`, `copy` (layer-dir file → container), `write` (inline content → container — no shell heredoc), `link` (symlink), `download` (curl + extract), `setcap` (file capabilities), `build` (explicit pixi/npm/cargo placement). Each task carries a `user:` field (`root` / `${USER}` / literal username / `uid:gid`). Strict author-controlled ordering. YAML anchors + `${VAR}` substitution for DRY. See `/ov:layer` for the full verb catalog.
- **`pixi.toml`** / **`pyproject.toml`** / **`environment.yml`** — Python and conda packages via the Pixi package manager (multi-stage build, runs as user).
- **`package.json`** — npm packages for Node.js (multi-stage build, runs as user).
- **`Cargo.toml`** + **`src/`** — Rust crate compilation (multi-stage build, runs as user).

`ov` detects which files are present and generates the appropriate build stages automatically. You only include what you need — a layer with just `layer.yml` listing rpm packages is perfectly valid.

The vocabulary layers draw from — per-distro bootstrap commands, multi-stage builder templates (pixi/npm/cargo/aur), and init-system definitions (supervisord/systemd) — all lives in a single `build.yml` at the repo root. Three top-level sections (`distro:`, `builder:`, `init:`), one loader, one ref from `image.yml`. See `/ov:build` for the full layout.

### Multi-Distro Support: `distro:` and `build:`

A single layer can target multiple distros. Two fields in `image.yml` control the behavior:

```yaml
fedora:
  base: "quay.io/fedora/fedora:43"
  distro: ["fedora:43", fedora]    # identity tags, priority order
  build: [rpm]                      # package formats, all installed in order
  builds: [pixi, npm, cargo]       # multi-stage build capabilities

archlinux:
  base: "docker.io/library/archlinux:latest"
  distro: [archlinux]
  build: [pac]
  builds: [pixi, npm, cargo, aur]
```

These fields flow through to `layer.yml`:
- **Package sections** — `distro:` tags are checked first (first match wins, prevents version conflicts). If no distro section matches, `build:` formats install ALL matching sections in order.
- **`tasks:`** — Not dispatched by tag. If a task must run on only one distro, guard it in-task: put a distro-specific package in the matching `rpm:`/`pac:` section, or add a shell `if [ -f /etc/fedora-release ]; then …; fi` inside a `cmd:` block.

This means `fedora-ov` and `arch-ov` share the exact same layer list — only the package sets (and rarely, a few shell-guarded tasks) differ per distro.

### Docker or Podman — Your Choice

Docker is the container tool most people know. Podman is a newer alternative from Red Hat that runs without a background daemon and integrates natively with Linux systemd. `ov` works with either — same commands, same images, same results. Switch with `ov settings set engine.build podman`.

### Init Systems: Generic, Configurable, Extensible

**Inside containers**, Overthink uses an **init system** to manage services. The default is **supervisord** — a lightweight process manager. When a layer declares `service:` in `layer.yml`, `ov` generates a supervisord config and bundles it into the image. The container starts supervisord as its main process, and supervisord starts and monitors all your services. This is how you get PostgreSQL, Traefik, and your application all running in one container. Images without init system services (like `fedora-ov`) use `sleep infinity` as the container entrypoint instead — keeping the container alive for `ov shell` to exec into.

**On the host**, Overthink uses **systemd** — the init system that already manages your Linux machine. When you run `ov config`, it generates a Podman quadlet that registers your container as a systemd service, provisions secrets, and mounts any encrypted volumes — all in one step. So systemd manages the container, and the configured init system (or `sleep infinity`) manages what runs inside it. Two levels, cleanly separated.

**In bootc VM images**, systemd takes over completely — it's PID 1 at the OS level. Layers use `system_services:` to declare systemd units (like sshd) or add `*.service` files for user-level services. No supervisord needed because it's a real operating system, not a container.

**Adding new init systems** (like s6-linux-init, runit, or dinit) requires only editing the `init:` section of `build.yml` — zero Go code changes. Each init system declares detection rules, fragment templates, entrypoint commands, and service management commands in YAML.

### Declarative Testing

Images and deployments come with inline checks. A `tests:` block on any `layer.yml`, `image.yml`, or `deploy.yml` authors goss-style declarative checks — files, packages, ports, processes, HTTP endpoints, DNS, mounts, services, kernel params, and more. Checks bake into a three-section OCI label (`org.overthinkos.tests` → `{layer, image, deploy}`) so any pulled image is self-testable without its source repo. `ov image test <image>` runs build-scope checks against a disposable container; `ov test <image>` runs all three sections against a live service, substituting deploy-time variables (`${HOST_PORT:N}`, `${VOLUME_PATH:name}`, `${CONTAINER_IP}`, `${ENV_*}`) so a check written once survives `deploy.yml` port remaps and volume rebindings. Local `deploy.yml` can add or override baked checks by `id:`.

`ov test` is also the parent router for live-container drive verbs: `ov test cdp` (Chrome DevTools), `ov test wl` (Wayland), `ov test dbus` (D-Bus / notifications), `ov test vnc` (VNC) — see `/ov:cdp`, `/ov:wl`, `/ov:dbus`, `/ov:vnc`. **All four are also authorable as declarative check verbs** (`cdp: eval`, `wl: screenshot`, `dbus: call`, `vnc: status`, etc.) inside any `tests:` block, wiring Chrome/Wayland/D-Bus/VNC assertions into the same three-section OCI-label pipeline as the built-in verbs.

Seven running images ship comprehensive coverage (371 checks total, 0 failing): `filebrowser` (24), `jupyter` (29), `openwebui` (24), `hermes` (50), `immich-ml` (63), `selkies-desktop` (91), `sway-browser-vnc` (90). LABEL directives emit at the end of each Containerfile so test edits rebuild in ~2 seconds.

See `/ov:test` for the verb catalog, matcher forms, runtime variable table, gold-standard pattern (`layers/redis/layer.yml`), 10 authoring gotchas, and deploy.yml overlay rules.

### Quadlets: Containers as System Services

With Docker, you'd use `docker compose` or a restart policy to keep a container running. Podman quadlets are different: they describe a container as a native systemd service — the same system that manages SSH, networking, and everything else on your Linux box. `ov config <image>` generates the quadlet file, provisions secrets, and mounts encrypted volumes — all in one command. After that, `systemctl start/stop/status` just work — your container starts on boot, restarts on failure, and shows up in `journalctl` logs like any other service. Services can be exposed via Tailscale (tailnet-private) or Cloudflare (public internet) tunnels with full backend scheme support — HTTP, HTTPS, HTTPS with self-signed certs, TCP, TLS-terminated TCP, SSH, RDP, and SMB.

### Bootc: The Container *Is* the OS

Normally a container runs *inside* an operating system. Bootc flips this: the container image *becomes* the operating system. Fedora publishes bootc base images that are full Linux systems packaged as container images. Add layers with Overthink just like any other image — install packages, configure services, add a desktop — and the result can boot directly as a real OS.

### Containers That Become Virtual Machines

This is where it all comes together. Take a bootc-based image, and `ov vm build` converts it into a QCOW2 or raw disk image. `ov vm create` sets up a libvirt/QEMU virtual machine from that disk — same layers, same composition, but now a full VM with its own kernel, SSH access, GPU passthrough, and persistent storage. Define it once in `image.yml`, use it everywhere.

## Install

**Recommended — Go install** (requires Go 1.25.3+):

```bash
go install github.com/overthinkos/overthink/ov@latest
```

This puts `ov` in your `$GOPATH/bin`. No other setup needed — just create an `image.yml` and a `layers/` directory.

**Full project bootstrap** (to build images from this repo):

```bash
git clone https://github.com/overthinkos/overthink.git
cd overthink
bash setup.sh    # downloads task, builds ov into bin/
ov image build         # build all images
```

**From source:**

```bash
cd ov && go build -o ../bin/ov .
```

### Secret Management

Project-level secrets (API keys, credentials) are stored in `.secrets` — a GPG-encrypted file that `ov secrets gpg env` decrypts in memory when direnv loads the directory. No plaintext on disk. Requires a GPG key + gpg-agent (locally or SSH-forwarded), direnv hooked into your shell, and a one-time `direnv allow`. After that, `cd`ing into the project auto-decrypts `.secrets` and exports the variables via `.envrc`'s `eval "$(ov secrets gpg env)"`.

Manage `.secrets` with `ov secrets gpg {env, show, set, unset, edit, encrypt, recipients, import-key, export-key, setup, doctor}`. See `/ov:secrets` for the full command reference, KeePassXC integration for key backup/restore, and headless/SSH workflows.

## Quick Taste

```bash
# Build a single image for your platform
ov image build fedora

# Build an Arch Linux image (auto-builds base + builder dependencies)
ov image build arch-test

# Drop into an interactive shell
ov shell fedora

# Build and run a GPU-accelerated Jupyter server
ov image build jupyter
ov start jupyter

# Configure as a systemd service (quadlet + secrets + encrypted volumes)
ov config jupyter

# Build a bootable VM disk image
ov vm build openclaw-browser-bootc --type qcow2
ov vm create openclaw-browser-bootc --ram 8G --cpus 4
ov vm start openclaw-browser-bootc
```

## The Layer Library

160 layers compose into images via `image.yml`. Dependencies resolve automatically. Every layer has a dedicated skill — invoke `/ov-layers:<name>` (or see [plugins/README.md](plugins/README.md) for the full index) for the details and composition recipe of any specific layer.

| Category | Representative layers | Purpose |
|---|---|---|
| **Foundations** | `pixi`, `python`, `nodejs`, `nodejs24`, `rust`, `golang`, `build-toolchain`, `yay` | Package managers and language runtimes |
| **Services & Infrastructure** | `supervisord`, `traefik`, `postgresql`, `vectorchord`, `redis`, `valkey`, `docker-ce`, `kubernetes` | Init, reverse proxy, databases, container-in-container |
| **GPU & ML** | `cuda`, `rocm`, `nvidia`, `llama-cpp`, `python-ml`, `jupyter`, `jupyter-ml`, `unsloth`, `unsloth-studio`, `ollama`, `comfyui` | NVIDIA/AMD runtimes and ML stacks |
| **Desktop Compositors** | `sway`, `labwc`, `niri`, `mutter`, `kwin`, `wayvnc`, `pipewire`, `selkies` | Wayland/X11 servers, audio, browser-streamed desktops |
| **Chrome variants** | `chrome`, `chrome-sway`, `chrome-niri`, `chrome-mutter`, `chrome-kwin`, `chrome-x11` | Chrome DevTools on `:9222` + DevTools MCP on `:9224` (29 tools) per compositor |
| **AI & Agents** | `openclaw`, `hermes`, `hermes-full`, `hermes-playwright`, `openwebui`, `claude-code`, `codex`, `gemini` | AI gateways, agents, LLM UIs, and coding CLIs |
| **Applications** | `immich`, `immich-ml`, `github-runner`, `steam`, `heroic`, `vscode`, `dev-tools`, `filebrowser`, `devops-tools` | End-user apps and workstation tooling |
| **Desktop Utilities** | `ffmpeg`, `wf-recorder`, `wl-record-pixelflux`, `wl-screenshot-pixelflux`, `wl-overlay`, `asciinema`, `libnotify`, `fastfetch` | Multimedia, recording, overlays, notifications |
| **Security & Identity** | `agent-forwarding`, `gnupg`, `direnv`, `ssh-client`, `sshd`, `gocryptfs`, `container-nesting` | Agent forwarding, encrypted storage, nesting |
| **OS / Bootc** | `bootc-base`, `bootc-config`, `cloud-init`, `os-config`, `os-system-files`, `qemu-guest-agent`, `socat` | Bootable disk image and VM integration |

**Composition meta-layers** — `sway-desktop`, `sway-desktop-vnc`, `niri-desktop`, `x11-desktop`, `mutter-desktop`, `kwin-desktop`, `selkies-desktop`, `bootc-base`, `openclaw-full`, `openclaw-full-ml`, `python-ml`, `jupyter-ml`, `unsloth-studio` bundle curated layer sets. See the matching `/ov-layers:<name>` skill for the exact composition recipe.

### Data Layers

Some layers provide **data** instead of packages or services via the `data:` field in `layer.yml`:

```yaml
# layers/notebook-templates/layer.yml
volumes:
  - name: workspace
    path: ~/workspace
data:
  - src: data/notebooks
    volume: workspace
```

At build time, data files are staged at `/data/<volume>/` inside the image. At deploy time, `ov config --bind <volume>` provisions the data into bind-backed volume directories; `ov update` merges new data non-destructively. **Data images** (`data_image: true`) take this further: scratch-based images containing only data + OCI labels, consumed via `ov config --data-from <data-image>`. See `/ov:config` and `/ov-layers:notebook-templates` for examples.

## The Lifecycle

Overthink covers the full lifecycle — from development to production — whether you're driving or your AI agent is:

**Develop** — `ov shell <image>` drops you into an interactive container with all your layers, volumes mounted, GPU passed through. Change code, rebuild, iterate.

**Run** — `ov start <image>` launches a detached service container with the configured init system managing your processes, traefik routing your services, and persistent volumes for data.

**Deploy** — `ov config <image>` is the single entry point for deployment. It reads the image's embedded labels, generates a quadlet, provisions secrets (with `--password auto` for hands-free setup or `--password manual` to prompt), configures volume backing (`--bind name` for host bind mounts, `--encrypt name` for gocryptfs, or `--volume name:encrypt:/path` for explicit per-volume encrypted paths), provisions data from data layers into bind-backed volumes (`--seed` by default, `--force-seed` to overwrite, `--data-from <image>` for external data sources), saves deployment state to `~/.config/ov/deploy.yml`, and registers with systemd. `ov config` must be run before `ov start` in quadlet mode. For services with encrypted volumes, boot behavior depends on the credential backend: **Secret Service (keyring)** auto-starts after login — the quadlet's ExecStartPre waits for the keyring to unlock via event-driven DBus signal subscription (zero CPU cost, unbounded wait), while **KeePass or no backend** requires `ov start` after login to prompt for the passphrase. When a service declares `env_provides` or `mcp_provides`, `ov config` injects those entries into `deploy.yml` under a unified `provides:` section for cross-container discovery — env vars and MCP server URLs are resolved from `{{.ContainerName}}` templates at deploy time (use `--update-all` to propagate to already-deployed services). MCP provides are pod-aware: when provider and consumer share a container, URLs resolve to `localhost`. If the image declares `env_requires` or `mcp_requires`, `ov config` warns about missing dependencies. No project source needed — just the image.

**Ship** — `ov image build --push` builds for all platforms and pushes to your registry. `ov vm build` turns bootc images into bootable disk images.

**Manage** — `ov update` pulls new images, syncs data from data layers into bind-backed volumes (non-destructive merge by default, `--force-seed` to overwrite), and restarts services. `ov config mount/unmount` handles encrypted volumes (each mount runs as an independent `ov-enc-<image>-<volume>.scope` systemd unit that survives container restart/stop). `ov settings migrate-secrets` moves plaintext credentials to the system keyring (GNOME Keyring, KDE Wallet, KeePassXC). For headless/SSH environments, `ov secrets init` creates a KeePass `.kdbx` database — the master password is cached in the Linux kernel keyring for 1 hour (configurable via `ov settings set secrets.kdbx_cache_timeout`), so you only enter it once per session. `ov alias install` creates host-level command aliases that transparently run inside containers.

## Command Reference

The `ov` CLI has 22 top-level command families split across three modes with disjoint input sets: **build mode** (`ov image …` reads `image.yml` + `build.yml`), **test mode** (`ov test` + `ov image test` read OCI labels + `deploy.yml` tests overlay, never `image.yml`), and **deploy mode** (everything else reads OCI labels + `deploy.yml`). Each command has a dedicated skill — invoke `/ov:<cmd>` (or run `ov <cmd> --help`) for full flag listings and examples. This section is a scannable index.

| Area | Commands | Skill |
|---|---|---|
| **Image family (build mode)** | `ov image {build, generate, validate, merge, new, inspect, list, pull}` | `/ov:image` (umbrella) + `/ov:build`, `/ov:generate`, `/ov:validate`, `/ov:merge`, `/ov:new`, `/ov:inspect`, `/ov:list`, `/ov:pull` |
| **Deployment** | `config`, `deploy`, `start`, `stop`, `update`, `remove` | `/ov:config`, `/ov:deploy`, `/ov:start`, `/ov:stop`, `/ov:update`, `/ov:remove` |
| **Runtime** | `shell`, `cmd`, `service`, `status`, `logs`, `tmux` | `/ov:shell`, `/ov:cmd`, `/ov:service`, `/ov:status`, `/ov:logs`, `/ov:tmux` |
| **Desktop recording** | `record` | `/ov:record` |
| **Testing + live-container drive** | `test` (runs declarative tests AND hosts nested verbs: `test cdp`, `test wl`, `test dbus`, `test vnc`), `image test` | `/ov:test` (parent router), `/ov:cdp`, `/ov:wl`, `/ov:dbus`, `/ov:vnc` |
| **Secrets & config** | `secrets`, `settings`, `alias` | `/ov:secrets`, `/ov:settings`, `/ov:alias` |
| **Host & VM** | `doctor`, `udev`, `vm` | `/ov:doctor`, `/ov:udev`, `/ov:vm` |
| **Misc** | `version` | `/ov:version` |

A few sample invocations:

```bash
ov image build jupyter                 # Build an image (see /ov:build for --push, --no-cache, --jobs)
ov image pull jupyter                  # Fetch into local storage (see /ov:pull for short/full/remote refs)
ov config jupyter                      # Unified deploy setup (see /ov:config for --bind, --encrypt, --sidecar, -i, --update-all)
ov start jupyter                       # Launch as a systemd service
ov shell jupyter                       # Interactive dev shell with volumes + GPU
ov test cdp open selkies-desktop "https://example.com"   # Browser automation (see /ov:cdp)
ov test wl screenshot selkies-desktop       # Compositor-agnostic screenshot (see /ov:wl)
ov vm build openclaw-browser-bootc --type qcow2     # Build a bootable VM disk (see /ov:vm)
```

### Pulling images from registries

Deploy-mode commands (`ov shell`, `ov start`, `ov config`, `ov alias add`, `ov vm create`, …) read image configuration from OCI labels, which requires the image to be in local storage. If it isn't, the command fails with a recommendation:

```
Error: image "jupyter:latest" is not available locally.
       Run 'ov image pull jupyter:latest' to fetch it first
```

`ov image pull` accepts three input forms: short names (resolved via `image.yml`, requires project directory), fully-qualified registry refs (pullable from anywhere), and `@github.com/org/repo/image[:version]` remote refs (downloads the repo and pulls its declared registry ref). See `/ov:pull` for details.

### Multiple Instances

Run multiple containers of the same image with `-i <instance>`. Each instance gets its own container (`ov-<image>-<instance>`), quadlet file, and `deploy.yml` entry (keyed as `<image>/<instance>`). MCP server names are auto-disambiguated with an `-<instance>` suffix so consumers can distinguish them. All `ov` commands accept `-i`.

```bash
ov config selkies-desktop -i work -e TS_HOSTNAME=work -p 3001:3000
ov config selkies-desktop -i personal -p 3002:3000
ov start selkies-desktop -i work
```

**Tunnel inheritance caveat:** tunnel config is **not** auto-inherited by instances — you must add `tunnel: {provider: tailscale, private: all}` to each instance's `deploy.yml` entry manually, then re-run `ov config` to regenerate the quadlet with Tailscale serve commands. Tunnel config is deploy.yml-only (read-skipped from OCI labels at `labels.go:238`). The `-e` flag merges env vars (upsert by key); `-c` replaces. See `/ov:deploy` for full inheritance semantics and `/ov:config` for the `--update-all` propagation model.

### Sidecar Containers

Attach sidecar containers at deploy time. Sidecars run alongside the app in a shared Podman pod (shared network namespace). Templates are built into the `ov` binary.

```bash
ov config --list-sidecars                                                        # List available templates
ov config <image> --sidecar tailscale \
  -e TS_HOSTNAME=my-app \
  -e "TS_EXTRA_ARGS=--exit-node=100.80.254.4 --exit-node-allow-lan-access"
```

The Tailscale sidecar routes outbound traffic through a Tailscale exit node while keeping the pod on the `ov` bridge for container-to-container connectivity (**dual networking**). Sidecar-related `-e` flags (e.g., `TS_*`) are automatically routed to the sidecar instead of the app container. Assignments persist in `deploy.yml`. See `/ov:sidecar` for the full template list and routing model.

### Wayland Overlays

`ov test wl overlay` drives fullscreen Wayland overlays for screen recordings — title cards, lower-thirds, watermarks, countdowns, region highlights, fade transitions. Rendered by the compositor with true RGBA transparency; no post-production needed. See `/ov:wl-overlay` for the full API.

## Troubleshooting

Each entry points to the canonical skill — details belong there, not here.

| Symptom | First step |
|---------|-----------|
| Service won't start | `ov status <image>` then `ov logs <image>` (`/ov:status`, `/ov:logs`) |
| Quadlet out of sync with deploy.yml | `ov config <image> --update-all` (`/ov:config`) |
| Chrome stuck or crash-looping | `/ov-layers:chrome` Resource Caps & Circuit Breaker section |
| Encrypted volume locked at boot | `ov config mount` waits for keyring unlock automatically — zero CPU, event-driven (`/ov:enc`) |
| GPU not detected | `ov doctor` then `/ov:udev` |
| Resource caps not applying | `ov config <image> --update-all` to regenerate the quadlet (`/ov:config`) |
| Build cache stale | `ov image build --no-cache <image>` (`/ov:build`) |
| Tunnel not appearing on a new instance | Tunnel config is deploy.yml-only — add manually per instance (`/ov:deploy`) |
| Service built fine but broken in production | `ov test <image>` runs the baked layer + image + deploy checks against the live container; `ov image test <image>` checks the disposable build (`/ov:test`) |

## Adding a Layer

```bash
ov image new layer my-layer            # Scaffold the directory
# Edit layers/my-layer/layer.yml       # Declare packages, deps, env, ports,
#                                      # and tasks: (see /ov:layer for the verb catalog)
# Optionally add tests: for file / port / http / command checks (see /ov:test)
# Optionally add pixi.toml, package.json, or Cargo.toml for auto-detected builders

# Add to an image in image.yml:
#   layers: [..., my-layer]

ov image build my-image                # Build it
```

See [Building Layers](#building-layers-package-managers--config-files) above for the full list of supported config files. The `/ov:layer` skill is the canonical reference for the `tasks:` verb catalog (`cmd`, `mkdir`, `copy`, `write`, `link`, `download`, `setcap`, `build`), `vars:` substitution, YAML anchors, and execution-order rules.

## Works with Claude Code

Overthink is designed to work hand-in-hand with [Claude Code](https://claude.com/claude-code). The [overthink-plugins](https://github.com/overthinkos/overthink-plugins) repository provides skills that teach Claude how to compose, build, deploy, and manage your container images.

**Quick setup** — add this to your project's `.claude/settings.json`:

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

Then clone with the plugins submodule:

```bash
git clone --recurse-submodules https://github.com/overthinkos/overthink.git
```

This gives Claude Code access to 243 skills covering every layer, image, and operation — so it can build images, debug services, author new layers, and manage deployments just like you would from the command line. The skill graph is densely cross-linked: invoking one skill surfaces its neighbors, and every layer skill references `/ov:layer` (authoring) and `/ov:test` (declarative testing).

The `chrome` layer auto-includes a **Chrome DevTools MCP server** at `http://localhost:9224/mcp` (via `chrome-devtools-mcp` sub-layer), providing 29 browser automation and inspection tools. This is auto-discovered by Hermes and other MCP consumers alongside the Jupyter MCP server.

The `ov-jupyter` plugin also registers a **Jupyter MCP server** (named `jupyter`) at `http://localhost:8888/mcp` (when the `jupyter` or `jupyter-ml` container is running). Claude Code can then use 13 MCP tools to create, read, edit, execute, and watch notebooks — with real-time collaboration alongside human users via CRDT. `jupyter` is the lightweight multi-arch variant (no GPU); `jupyter-ml` adds the full CUDA ML stack (PyTorch, vLLM, Unsloth, LangChain); `jupyter-ml-notebook` adds 37 Unsloth fine-tuning notebooks, 6 Ollama integration notebooks, and 15 LLM course notebooks. See `/ov-layers:jupyter`, `/ov-layers:jupyter-ml`, and their image counterparts for details.

See [CLAUDE.md](CLAUDE.md) for the complete system specification and [plugins/README.md](plugins/README.md) for the full skill reference.

## License

MIT
