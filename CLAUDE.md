# Overthink Build System

Compose container images from a library of fully configurable layers.
Built on `supervisord` and `ov` (Go CLI). Supports both Docker and Podman as build/run engines.

---

## Architecture Overview

Two components with a clean split:

**`ov` (Go CLI)** -- all computation and building. Parses `images.yml`, scans `layers/`, resolves dependency graphs, validates, generates Containerfiles, builds images via `<engine> build`. Source: `ov/`. Registry inspection via go-containerregistry. `ov shell`/`ov start`/`ov stop`/`ov merge`/`ov enable` use the configured engine (Docker or Podman).

**`task` (Taskfile)** -- bootstrap only: builds `ov` from source and creates the buildx builder. Source: `Taskfile.yml` + `taskfiles/{Build,Setup}.yml`. All other operations use `ov` directly.

**What gets generated** (`ov generate`):
- `.build/<image>/Containerfile` -- one per image, unconditional `RUN` steps only
- `.build/<image>/traefik-routes.yml` -- traefik dynamic config (only for images with `route` layers)
- `.build/<image>/supervisor/*.conf` -- supervisord service configs (only for images with `service` layers)
- `.build/_layers/<name>` -- symlinks to remote layer directories (only when remote layers used)

Generation is idempotent. `.build/` is disposable and gitignored.

---

## Directory Structure

```
project/
+-- bin/ov                    # Built by `task build:ov` (gitignored)
+-- ov/                       # Go module (go 1.25.3, kong CLI, go-containerregistry)
+-- .build/                   # Generated (gitignored)
+-- images.yml                # Image definitions
+-- setup.sh                  # Bootstrap: downloads task, builds ov
+-- Taskfile.yml              # Bootstrap tasks only
+-- taskfiles/                # Build.yml, Setup.yml
+-- layers/<name>/            # Layer directories
+-- templates/                # supervisord.header.conf
```

---

## ov CLI Reference

```
ov generate [--tag TAG]                # Write .build/ (Containerfiles)
ov validate                            # Check images.yml + layers, exit 0 or 1
ov inspect <image> [--format FIELD]    # Print resolved config (JSON) or single field
ov list images                         # Images from images.yml
ov list layers                         # Layers from filesystem
ov list targets                        # Build targets in dependency order
ov list services                       # Layers with service in layer.yml
ov list routes                         # Layers with route in layer.yml (host + port)
ov list volumes                        # Layers with volumes in layer.yml
ov list aliases                        # Layers with aliases in layer.yml
ov alias add <name> <image> [command]  # Create a host command alias
ov alias remove <name>                 # Remove an alias
ov alias list                          # List all installed aliases
ov alias install <image>               # Install default aliases from layer.yml / images.yml
ov alias uninstall <image>             # Remove all aliases for an image
ov build [image...]                    # Build with auto cache (image cache from registry)
ov build --push [image...]             # Build+push with registry cache (read+write)
ov build --no-cache [image...]         # Build without any cache
ov build --platform linux/amd64 [image...]  # Specific platform
ov build --cache registry [image...]       # Explicit registry cache (read+write)
ov build --cache image [image...]          # Explicit image cache (read-only)
ov build --cache gha [image...]            # GitHub Actions cache
ov build --cache none [image...]           # Same as --no-cache
ov build --jobs N [image...]               # Max concurrent builds per level (default: 4)
ov merge <image> [--max-mb N] [--tag TAG] [--dry-run]
ov merge --all [--dry-run]             # Merge all images with merge.auto enabled
ov new layer <name>                    # Scaffold a layer directory
ov seed <image> [--tag TAG]                # Seed empty bind mount dirs from image data
ov shell <image> [-w PATH] [-c CMD] [--tag TAG] [--tty] [--no-autodetect] [-e KEY=VALUE] [--env-file PATH] [-i INSTANCE] [--build]
ov start <image> [-w PATH] [--tag TAG] [--no-autodetect] [-e KEY=VALUE] [--env-file PATH] [-i INSTANCE] [--build]
ov stop <image> [-i INSTANCE]          # Stop a running service container
ov enable <image> [-w PATH] [--tag TAG] [--no-autodetect] [-e KEY=VALUE] [--env-file PATH] [-i INSTANCE] [--build]
ov disable <image> [-i INSTANCE]       # Disable service auto-start (quadlet only)
ov status <image> [-i INSTANCE]        # Show service status
ov logs <image> [-f] [-i INSTANCE]     # Show service logs
ov update <image> [--tag TAG] [-i INSTANCE] [--build]  # Update image, restart if active
ov remove <image> [-i INSTANCE] [--volumes] [-e KEY=VALUE]  # Remove service (--volumes removes named volumes)
ov config get <key>                    # Print resolved value
ov config set <key> <value>            # Set in user config
ov config list                         # Show all settings with source
ov config reset [key]                  # Remove from user config
ov service status <image> [-i INSTANCE]    # Show supervisord service status
ov service start <image> <service> [-i INSTANCE]   # Start a supervisord service
ov service stop <image> <service> [-i INSTANCE]    # Stop a supervisord service
ov service restart <image> <service> [-i INSTANCE] # Restart a supervisord service
ov browser open <image> <url> [-i INSTANCE]                  # Open URL in container's Chrome
ov browser list <image> [-i INSTANCE]                         # List open Chrome tabs
ov browser close <image> <tab-id> [-i INSTANCE]               # Close a Chrome tab
ov browser text <image> <tab-id> [-i INSTANCE]                # Get page text content
ov browser html <image> <tab-id> [-i INSTANCE]                # Get page HTML
ov browser url <image> <tab-id> [-i INSTANCE]                 # Get page title and URL
ov browser screenshot <image> <tab-id> [file] [-i INSTANCE]   # Capture screenshot (PNG)
ov browser click <image> <tab-id> <selector> [-i INSTANCE]    # Click element by CSS selector
ov browser type <image> <tab-id> <selector> <text> [-i INSTANCE]  # Type into input field
ov browser eval <image> <tab-id> <expression> [-i INSTANCE]   # Evaluate JavaScript
ov browser wait <image> <tab-id> <selector> [-i INSTANCE] [--timeout 30s]  # Wait for element
ov browser cdp <image> <tab-id> <method> [params-json] [-i INSTANCE]  # Raw CDP command
ov vnc screenshot <image> [file] [-i INSTANCE]              # Capture VNC framebuffer as PNG
ov vnc click <image> <x> <y> [-i INSTANCE] [--button left]  # Click at coordinates
ov vnc type <image> <text> [-i INSTANCE]                     # Type text as key events
ov vnc key <image> <key-name> [-i INSTANCE]                  # Send key (Return, Escape, F1, etc.)
ov vnc mouse <image> <x> <y> [-i INSTANCE]                   # Move mouse without clicking
ov vnc status <image> [-i INSTANCE]                           # Show desktop name, resolution
ov vnc passwd <image> [-i INSTANCE] [--generate]             # Set VNC password for a deployment
ov vnc rfb <image> <method> [params-json] [-i INSTANCE]      # Raw RFB command
ov crypto init <image> [--volume NAME]
ov crypto mount <image> [--volume NAME]
ov crypto unmount <image> [--volume NAME]
ov crypto status <image>
ov crypto passwd <image>               # Change encryption password
ov vm build <image> [--type qcow2|raw] [--size SIZE] [--root-size SIZE] [--console] [--transport TRANSPORT]
ov vm create <image> [--ram SIZE] [--cpus N] [--ssh-key SSH_KEY] [--no-autodetect] [-i INSTANCE]
ov vm start <image> [-i INSTANCE]      # Start a VM
ov vm stop <image> [-i INSTANCE] [--force]  # Stop a VM
ov vm destroy <image> [-i INSTANCE] [--disk]  # Remove VM, optionally delete disk
ov vm list [-a]                        # List VMs (--all includes stopped)
ov vm console <image> [-i INSTANCE]    # Attach to VM serial console
ov vm ssh <image> [-i INSTANCE] [-p PORT] [-l USER] [args...]
ov config path                         # Print config file path
ov version                             # Print computed CalVer tag
```

**Output conventions:** `generate`/`validate`/`new`/`merge` write to stderr. `inspect`/`list`/`version` write to stdout (pipeable). `inspect --format <field>` outputs bare value for shell substitution (`tag`, `base`, `builder`, `pkg`, `registry`, `platforms`, `layers`, `ports`, `volumes`, `aliases`, `bind_mounts`, `tunnel`, `network`).

**Remote image refs:** All runtime commands (`shell`, `start`, `enable`, `update`) accept remote image references as `github.com/org/repo/image[@version]`. Registry-first approach: attempts pull, falls back to local build. Use `--build` to force local builds.

**Remote layer refs:** Layer references starting with `@` are remote: `@github.com/org/repo/layers/name:version`. The path after the repo maps directly to the directory in the repo (e.g., `layers/name`). Auto-downloaded to `~/.cache/ov/repos/` on first use. Version is optional -- when omitted, the repo's default branch is used. Different layers from the same repo can use different versions. Used in `images.yml` layers and `layer.yml` depends/layers fields.

**Error handling:** validation collects all errors at once. Exit codes: 0 = success, 1 = validation/user error, 2 = internal error.

---

## Shipped Layers (64 total)

**Foundation:** `pixi` (pixi binary + env/PATH), `nodejs` (Node.js + npm via rpm/deb), `node24` (Node.js 24 via rpm/deb), `rust` (Rust + Cargo via rpm/deb), `golang` (Go compiler via rpm), `python` (Python 3.13 via pixi), `language-runtimes` (Go, PHP, .NET, nodejs-devel, python3-devel)

**Build:** `build-toolchain` (gcc, cmake, autoconf, ninja, git, pkg-config), `pre-commit` (git hooks framework)

**Services:** `supervisord` (process manager via pixi; depends: python), `traefik` (reverse proxy on :8000/:8080; depends: supervisord), `testapi` (FastAPI test service on :9090, routed via `testapi.localhost`), `postgresql` (PostgreSQL server on :5432; volume: pgdata), `redis` (Redis on :6379; service)

**Desktop/Wayland:** `sway` (Sway compositor + dbus), `cage` (kiosk-mode headless Wayland), `niri` (Niri compositor; depends: cage), `quickshell` (bar/launcher via COPR; depends: sway), `pcmanfm-qt` (file manager; depends: sway), `dank-material-shell` (DMS shell/launcher via COPR; depends: sway), `noctalia` (Quickshell-based shell via COPR; depends: sway)

**Display/Audio:** `wayvnc` (VNC server on tcp:5900; protocol annotation; VeNCrypt/TLS auth via `ov vnc passwd`; reads `~/.config/wayvnc/config` automatically), `pipewire` (audio/media server + wireplumber)

**Browser:** `chrome-deps` (Chrome runtime dependencies: fonts, graphics, audio libs; shm_size: 1g, procps-ng, iproute for debugging), `google-chrome` (Chrome on niri, DevTools :9222, volume: chrome-data; layers: chrome-deps), `google-chrome-sway` (Chrome on sway, same ports/volume; layers: chrome-deps; port_relay on :9222, browser-open via CDP, BROWSER env)

**GPU/ML:** `cuda` (CUDA toolkit + cuDNN + onnxruntime), `python-ml` (ML Python env; depends: cuda), `jupyter` (Jupyter + ML libs on :8888; depends: cuda, supervisord), `ollama` (LLM server on :11434; depends: cuda, supervisord; volume: models; alias: ollama), `comfyui` (image generation on :8188; depends: cuda, supervisord; volume: comfyui)

**Applications:** `openclaw` (AI gateway on :18789 via npm; depends: nodejs, supervisord; volume: data; alias: openclaw), `claude-code` (Claude Code CLI; depends: nodejs), `immich` (photo management on :2283; depends: node24, postgresql, redis, supervisord), `immich-ml` (ML backend on :3003; depends: immich; volume: models)

**DevOps/CI:** `docker-ce` (Docker CE + buildx + compose), `kubernetes` (kubectl + Helm), `devops-tools` (bind-utils, jq, rsync; depends: nodejs), `github-runner` (Actions runner as service; uid: 0), `github-actions` (Act CLI via COPR + guestfs), `google-cloud` (Google Cloud SDK), `google-cloud-npm` (GCP npm packages; depends: google-cloud, nodejs), `grafana-tools` (Grafana tooling)

**Dev Tools:** `dev-tools` (bat, ripgrep, neovim, gh, direnv, fd-find, htop, podman-compose), `vscode` (VS Code via Microsoft repo), `pre-commit` (git hooks), `typst` (document processor), `ujust` (task runner)

**Desktop Apps:** `desktop-apps` (Chromium, VLC, KeePassXC, btop, cockpit, zsh), `copr-desktop` (COPR desktop packages), `vr-streaming` (OpenXR, OpenVR, GStreamer), `virtualization` (QEMU/KVM/libvirt stack)

**Utilities:** `gocryptfs` (encrypted filesystem for ov crypto operations), `socat` (socket relay for VM console access and port_relay for loopback-only services)

**OS (bootc):** `os-config` (OS configuration), `os-system-files` (system files/configs), `rpmfusion` (RPM Fusion repository configuration), `bootc-config` (bootc system config: autologin, graphical target, pipewire/wireplumber), `cloud-init` (cloud instance init; depends: sshd), `qemu-guest-agent` (QEMU guest agent; libvirt channel config), `sshd` (SSH server on :22), `ov` (ov binary for container/VM use)

**Composing (layer groups):** `sway-desktop` (pipewire + wayvnc + chrome-sway + pcmanfm-qt + quickshell), `sway-desktop-dank` (same with dank-material-shell), `sway-desktop-noctalia` (same with noctalia), `bootc-base` (sshd + qemu-guest-agent + bootc-config), `ov-full` (ov + virtualization + gocryptfs + socat)

---

## Style Guide

- Lowercase-hyphenated names for layers and images
- Taskfiles for bootstrap only (building ov), Go for all other logic
- No Docker layer cleanup -- cache mounts handle it
- `.build/` is disposable; all generated files start with `# <path> (generated -- do not edit)`
- Layer Taskfiles (`root.yml`/`user.yml`): single `install` task, no parameters, idempotent
- System packages in `layer.yml` `rpm:`/`deb:` sections. Python in `pixi.toml`. npm in `package.json`. Rust in `Cargo.toml`
- Composing layers: use `layers:` in `layer.yml` to include other layers. Layers with `layers:` and no install files are valid (pure composition). Build cache defaults to `image` (read-only from registry); use `--no-cache` to disable
- Never `pip install`, `conda install`, or `dnf install python3-*`. Pixi is the only Python package manager
- Binary downloads: detect arch with `uname -m`, map via `case`, fail on unsupported
- `USER <UID>` (numeric) not `USER <name>` in generated Containerfiles
- All logic belongs in `ov`. Tasks are only for bootstrap (building ov). Every public task has `desc:`
- Port protocol annotations: `tcp:5900` in layer.yml ports for non-HTTP protocols (default: http)
- `port_relay:` in layer.yml for services binding to loopback only -- auto-adds socat relay
- `security.shm_size:` in layer.yml for shared memory requirements (e.g., `"1g"`)
- `BROWSER` env in chrome layers points to `browser-open` for in-container URL opening via CDP
- VNC password management: `ov vnc passwd` for deployments, password stored in `ov config` as `vnc.password.<image>`, wayvnc reads `~/.config/wayvnc/config` by default (no layer/wrapper changes needed for auth)

---

## Workflows

**Add a layer:** `ov new layer <name>` -> edit `layer.yml` -> add install files -> add to an image in `images.yml` -> `ov build <image>`

**Add an image:** add entry to `images.yml` -> `ov build <image>`

**Layer images:** set `base` to another image name in `images.yml`. The generator handles dependency ordering and tag resolution.

**Host bootstrap (first time):** requires `go`, `docker` (or `podman`). Run `bash setup.sh` to download `task`, build `ov`, then `ov build` to build all images. To use podman: `ov config set engine.build podman`.

---

## Task Commands (bootstrap only)

Task is used only for bootstrapping. All other operations use `ov` directly.

- `task build:ov` — Build ov from source into `bin/ov`
- `task build:install` — Build and install ov to `~/.local/bin`
- `task setup:builder` — Create multi-platform buildx builder
- `task setup:all` — Full setup (build ov + create builder)

---

## Skill Reference

For detailed documentation on specific topics, use the corresponding skill:

| Topic | Skill | Covers |
|-------|-------|--------|
| Layer authoring | `/overthink:layer` | layer.yml fields, install files, packages, deps, env, volumes, cache mounts, port_relay, protocol annotations |
| Image composition | `/overthink:image` | images.yml, inheritance chain, builder image, intermediates, versioning |
| Building images | `/overthink:build` | ov build, push mode, layer merging algorithm, build cache, inline merge |
| Shell & execution | `/overthink:shell` | ov shell, --tty, -c commands, exec into running containers, port_relay |
| Service management | `/overthink:service` | ov start/stop/enable/disable/status/logs/update/remove, supervisord services |
| Browser automation | `/overthink:browser` | ov browser commands, CDP, Chrome DevTools, OAuth flows |
| VNC automation | `/overthink:vnc` | ov vnc commands, RFB protocol, screenshots, keyboard/mouse input, VNC password |
| Aliases | `/overthink:alias` | ov alias add/remove/list/install/uninstall |
| Configuration | `/overthink:config` | ov config get/set/list/reset/path, bind_address, engine settings |
| Deployment | `/overthink:deploy` | Quadlet services, bind mounts, tunnels, deploy.yml, tailscale serve |
| Virtual machines | `/overthink:vm` | ov vm build/create/start/stop/destroy/list/console/ssh, bootc images |
| Encrypted storage | `/overthink:crypto` | ov crypto init/mount/unmount/status/passwd, gocryptfs |
| OpenClaw gateway | `/overthink:openclaw` | Gateway config, model auth, browser integration, channels, agent setup |
| Validation | `/overthink:validate` | Layer rules, image rules, bind mount rules, tunnel rules, port_relay rules |
| Go CLI development | `/overthink-dev:go` | Source code map, testing, adding commands |
| Containerfile generation | `/overthink-dev:generate` | Generated structure, multi-stage builds, labels, user resolution, cache mounts |