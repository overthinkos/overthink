# Overthink

**The container management experience for you and your AI.**

Building containers sounds simple — until you need CUDA drivers, a Wayland desktop inside a container, fine-grained device access for KVM without giving away root, or half a dozen services wired together with the right permissions. Overthink takes care of all of that. Describe what you need in a simple layer list, and `ov` composes it into optimized multi-stage container images — from an interactive dev shell to a running service to a systemd unit to a bootable VM. Works the same way whether you're at the keyboard or your AI agent is driving.

147 layers. 36 image definitions. Docker and Podman. `linux/amd64`. Fedora, Debian, and Arch Linux. One CLI: `ov`.

*The name comes from the German "überdenken" — to think something through carefully. Not quite the same as the English "overthink," but let's be honest: `ov` really is trying its best to overthink absolutely everything.*

## Why Overthink?

Containers are a great idea with rough edges. The basics work well enough, but real-world needs pile up fast: GPU passthrough with the right driver stack, containers that need `/dev/kvm` or virtualization access without blanket `--privileged`, multiple services managed together, encrypted volumes, VNC or browser-streamed desktops, device permissions that don't compromise your host. Each of these is solvable — but solving them all at once, reliably, across images, is where things get hard. And if you're working with an AI agent that needs to build and manage these containers too, the complexity compounds.

Overthink treats container images like composable building blocks. Each **layer** is a self-contained unit — its packages, environment variables, services, volumes, security declarations, and dependencies described in a simple `layer.yml`. An **image** is just a list of layers on top of a base. The `ov` CLI resolves the dependency graph, generates optimized Containerfiles with multi-stage builds and cache mounts, and builds everything in the right order — handling the hard parts so you (and your AI) don't have to.

Want a GPU-accelerated Jupyter notebook? That's `cuda` + `jupyter` — two layers, one image definition. Need to add Ollama for local LLMs? Add the `ollama` layer. Want a full AI workstation with a Wayland desktop, Chrome, VNC, and an AI gateway? Still just a list of layers in `images.yml`. Overthink handles the rest: dependency resolution, build ordering, supervisor configs, traefik routes, volume declarations, security mounts, and GPU passthrough.

### Sandboxed AI Desktops

One of Overthink's design goals is running sandboxed [OpenClaw](https://github.com/overthinkos/openclaw) systems. The approach flips the usual AI sandboxing model: instead of restricting what the AI agent can do, Overthink gives it full access to a complete desktop environment — Chrome, a Wayland compositor, development tools, network services — and sandboxes the entire desktop inside a container managed by `ov`. The AI agent operates freely within its environment while the host stays fully isolated. This is how images like `openclaw-sway-browser` and `openclaw-ollama-sway-browser` work: a full AI workstation with no host compromise.

## Key Concepts

### Layers, Images, and Multi-Service Containers

A layer is a reusable building block — packages, config, services. An image is layers stacked on a base. The key insight: **you can combine multiple services into a single container image** just by listing layers. Need PostgreSQL, Redis, a Python API, and a reverse proxy in one container? Add those four layers to your image. `ov` resolves dependencies, generates an optimized Containerfile, and wires up the init system (supervisord for containers, systemd for bootc VMs) to run all services together when the container starts.

### Building Layers: Package Managers & Config Files

Each layer lives in its own directory under `layers/` and can use any combination of these files:

- **`layer.yml`** — The layer's manifest: system packages with tag-based dispatch (`rpm:` for Fedora/RHEL, `deb:` for Debian/Ubuntu, `pac:` for Arch Linux, `aur:` for AUR, plus distro/version tags like `fedora:`, `fedora:43:`), dependencies on other layers, environment variables, ports, services, volumes, routes, and metadata (`version`, `status`, `info`)
- **`pixi.toml`** / **`pyproject.toml`** / **`environment.yml`** — Python and conda packages via the Pixi package manager (multi-stage build, runs as user)
- **`package.json`** — npm packages for Node.js (multi-stage build, runs as user)
- **`Cargo.toml`** + **`src/`** — Rust crate compilation (multi-stage build, runs as user)
- **`root.yml`** — Custom install script (Taskfile format) with tag-based task dispatch (`all:` for common, `rpm:`/`pac:`/`fedora:` for specific) that runs as root
- **`user.yml`** — Custom install script (Taskfile format) with the same tag-based dispatch that runs as the container user

`ov` detects which files are present and generates the appropriate build stages automatically. You only include what you need — a layer with just `layer.yml` listing rpm packages is perfectly valid.

### Multi-Distro Support: `distro:` and `build:`

A single layer can target multiple distros. Two fields in `images.yml` control the behavior:

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

These flow through to all three layer file types:
- **`layer.yml`** — `distro:` tags are checked first (first match wins, prevents version conflicts). If no distro section matches, `build:` formats install ALL matching sections in order.
- **`root.yml` / `user.yml`** — **Additive**: all matching tasks run (`all:` → distro tags → build formats)

This means `fedora-ov` and `arch-ov` share the exact same layer list — only the packages and scripts differ per distro.

### Docker or Podman — Your Choice

Docker is the container tool most people know. Podman is a newer alternative from Red Hat that runs without a background daemon and integrates natively with Linux systemd. `ov` works with either — same commands, same images, same results. Switch with `ov settings set engine.build podman`.

### Init Systems: Generic, Configurable, Extensible

**Inside containers**, Overthink uses an **init system** to manage services. The default is **supervisord** — a lightweight process manager. When a layer declares `service:` in `layer.yml`, `ov` generates a supervisord config and bundles it into the image. The container starts supervisord as its main process, and supervisord starts and monitors all your services. This is how you get PostgreSQL, Traefik, and your application all running in one container. Images without init system services (like `fedora-ov`) use `sleep infinity` as the container entrypoint instead — keeping the container alive for `ov shell` to exec into.

**On the host**, Overthink uses **systemd** — the init system that already manages your Linux machine. When you run `ov config`, it generates a Podman quadlet that registers your container as a systemd service, provisions secrets, and mounts any encrypted volumes — all in one step. So systemd manages the container, and the configured init system (or `sleep infinity`) manages what runs inside it. Two levels, cleanly separated.

**In bootc VM images**, systemd takes over completely — it's PID 1 at the OS level. Layers use `system_services:` to declare systemd units (like sshd) or add `*.service` files for user-level services. No supervisord needed because it's a real operating system, not a container.

**Adding new init systems** (like s6-linux-init, runit, or dinit) requires only editing `init.yml` — zero Go code changes. Each init system declares detection rules, fragment templates, entrypoint commands, and service management commands in YAML.

### Quadlets: Containers as System Services

With Docker, you'd use `docker compose` or a restart policy to keep a container running. Podman quadlets are different: they describe a container as a native systemd service — the same system that manages SSH, networking, and everything else on your Linux box. `ov config <image>` generates the quadlet file, provisions secrets, and mounts encrypted volumes — all in one command. After that, `systemctl start/stop/status` just work — your container starts on boot, restarts on failure, and shows up in `journalctl` logs like any other service.

### Bootc: The Container *Is* the OS

Normally a container runs *inside* an operating system. Bootc flips this: the container image *becomes* the operating system. Fedora publishes bootc base images that are full Linux systems packaged as container images. Add layers with Overthink just like any other image — install packages, configure services, add a desktop — and the result can boot directly as a real OS.

### Containers That Become Virtual Machines

This is where it all comes together. Take a bootc-based image, and `ov vm build` converts it into a QCOW2 or raw disk image. `ov vm create` sets up a libvirt/QEMU virtual machine from that disk — same layers, same composition, but now a full VM with its own kernel, SSH access, GPU passthrough, and persistent storage. Define it once in `images.yml`, use it everywhere.

## Install

**Recommended — Go install** (requires Go 1.25.3+):

```bash
go install github.com/overthinkos/overthink/ov@latest
```

This puts `ov` in your `$GOPATH/bin`. No other setup needed — just create an `images.yml` and a `layers/` directory.

**Full project bootstrap** (to build images from this repo):

```bash
git clone https://github.com/overthinkos/overthink.git
cd overthink
bash setup.sh    # downloads task, builds ov into bin/
ov build         # build all images
```

**From source:**

```bash
cd ov && go build -o ../bin/ov .
```

### Secret Management

Project-level secrets (API keys, credentials) are stored in `.secrets` — a GPG-encrypted file that `ov secrets gpg env` decrypts in memory when direnv loads the directory. No plaintext on disk.

**Prerequisites:**
- GPG key and gpg-agent running (locally or forwarded via SSH)
- direnv installed and hooked into your shell
- `ov` installed (provides `ov secrets gpg env`)

**Setup:**
```bash
# Allow direnv in this project (one-time)
direnv allow
```

After setup, `cd`ing into the project automatically decrypts `.secrets` and exports the variables. The `.envrc` uses `eval "$(ov secrets gpg env)"` — no external direnvrc dependency needed.

**Managing .secrets with ov:**
```bash
ov secrets gpg env                                        # Decrypt for direnv/eval
ov secrets gpg encrypt -r <KEY_ID> -i .env -o .secrets   # Encrypt
ov secrets gpg show                                       # View contents
ov secrets gpg set API_KEY sk-test-abc                    # Add/update a key
ov secrets gpg edit                                       # Edit in $EDITOR
ov secrets gpg unset OLD_KEY                              # Remove a key
ov secrets gpg recipients                                 # List who can decrypt
```

## Quick Taste

```bash
# Build a single image for your platform
ov build fedora

# Build an Arch Linux image (auto-builds base + builder dependencies)
ov build arch-test

# Drop into an interactive shell
ov shell fedora

# Build and run a GPU-accelerated Jupyter server
ov build jupyter
ov start jupyter

# Configure as a systemd service (quadlet + secrets + encrypted volumes)
ov config jupyter

# Build a bootable VM disk image
ov vm build openclaw-browser-bootc --type qcow2
ov vm create openclaw-browser-bootc --ram 8G --cpus 4
ov vm start openclaw-browser-bootc
```

## The Layer Library

Layers compose. Pick what you need, and dependencies resolve automatically.

### Foundations

**pixi** — The Pixi package manager, foundation for Python and conda environments.
**python** — Python 3.13 via Pixi. **nodejs** / **nodejs24** — Node.js + npm. **rust** — Rust + Cargo. **golang** — Go compiler. **language-runtimes** — Go, PHP, .NET, and more. **build-toolchain** — gcc, cmake, autoconf, ninja, git, pkg-config. **yay** — AUR helper for Arch Linux images (base-devel + yay binary).

### Services & Infrastructure

**supervisord** — Default init system for managing multiple services in container images (via `service:` field in layer.yml). Configurable via `init.yml`. **traefik** — Reverse proxy with automatic route discovery (`:8000`/`:8080`). **postgresql** — Postgres on `:5432` with a persistent volume. **vectorchord** — VectorChord vector similarity extension for PostgreSQL (smart search). **redis** — Redis on `:6379`. **docker-ce** — Docker CE + buildx + compose inside containers. **kubernetes** — kubectl + Helm.

### GPU & Machine Learning

**cuda** — NVIDIA CUDA toolkit + cuDNN + ONNX Runtime. **rocm** — AMD ROCm runtime + OpenCL (auto-detects `/dev/kfd` and `HSA_OVERRIDE_GFX_VERSION`). **python-ml** — ML Python environment on top of CUDA. **jupyter** — Jupyter + ML libraries on `:8888`. **jupyter-colab** — Lightweight JupyterLab with real-time collaboration (jupyter-collaboration) on `:8888`, plus a built-in MCP server at `/mcp` for programmatic notebook access (13 tools: create/read/edit/execute cells, watch for changes, manage collaboration rooms). AI agents and humans can edit the same notebook simultaneously via CRDT. No GPU required. **unsloth** — Unsloth LLM fine-tuning library with vLLM and llama.cpp. **unsloth-studio** — Unsloth Studio fine-tuning web UI on `:8888` + vLLM API on `:8000`. **ollama** — LLM inference server on `:11434` with model volume. **comfyui** — Image generation UI on `:8188`.

### Desktop Environments

**sway** — Wayland compositor (wlroots, full desktop). **labwc** — Lightweight Wayland compositor (wlroots, nested desktop for Selkies streaming). **niri** — Wayland compositor (Smithay, built from source with virtual output support for headless streaming). **mutter** — GNOME compositor (headless, portal-native screen capture via D-Bus ScreenCast). **wayvnc** — VNC server on `:5900`. **pipewire** — Audio/media server. **chrome** / **chrome-sway** / **chrome-niri** / **chrome-mutter** — Chrome with DevTools on `:9222`. **selkies** — Browser-accessible desktop streaming via pixelflux (Wayland capture) and pcmflux (audio) on `:3000` (HTTPS via Traefik with self-signed cert — required for WebCodecs). Full mouse and keyboard passthrough via WebSocket. H.264 video at 60fps + Opus audio. Session state survives client disconnection.

### Applications

**openclaw** — AI gateway on `:18789`. **claude-code** — Claude Code CLI. **immich** / **immich-ml** — Self-hosted photo management with ML backend. **github-runner** — GitHub Actions runner as a service. **steam** — Steam client with gamescope. **heroic** — Heroic Games Launcher for Epic, GOG, and Amazon Prime Gaming with mangohud and gamemode. **vscode** — VS Code. **dev-tools** — bat, ripgrep, neovim, gh, direnv, fd-find, htop.

### Utilities

**agent-forwarding** = gnupg + direnv + ssh-client — SSH/GPG agent socket forwarding into containers (included in all application images). **gnupg** — GnuPG encryption and signing tools. **direnv** — Automatic environment variable loading from `.envrc` files. **ssh-client** — OpenSSH client tools (lighter than sshd). **fastfetch** — Fast system information tool (neofetch successor). **asciinema** — Terminal session recording to `.cast` files. **wf-recorder** — Wayland screen recorder for wlroots compositors (sway-desktop). **wl-overlay** — Fullscreen Wayland overlays via gtk4-layer-shell for screen recordings (title cards, lower-thirds, watermarks, countdowns, highlights, fade transitions — rendered by the compositor with true RGBA transparency, no post-production needed). **libnotify** — `notify-send` CLI for desktop notifications (optional; `ov dbus notify` uses native Go D-Bus instead). **gocryptfs** — Encrypted filesystem for `ov config` encrypted volume operations. **socat** — Socket relay for VM console access. **container-nesting** — Container-in-container support: podman, buildah, fuse-overlayfs, rootless config, tailscale tunnels, nested `containers.conf`.

### OS / Bootc

**sshd** — SSH server. **cloud-init** — Cloud instance initialization. **bootc-config** — Bootc system configuration (autologin, graphical target). **qemu-guest-agent** — VM guest agent with libvirt channel.

### Composing Layers

Some layers are pure composition — they pull in a curated set of other layers:
**sway-desktop** = pipewire + xdg-portal + wl-tools + wl-screenshot-grim + wl-overlay + wf-recorder + chrome-sway + xfce4-terminal + thunar + waybar + desktop-fonts + swaync + pavucontrol + tmux + asciinema + fastfetch. Base desktop — no display server.
**sway-desktop-vnc** = sway-desktop + wayvnc. VNC remote access on port 5900.
**niri-desktop** = pipewire + xdg-portal-niri + niri + chrome-niri + niri-apps. Smithay-based desktop — experimental alternative to sway-desktop.
**x11-desktop** = pipewire + openbox + chrome-x11 + x11-apps. Xorg headless (dummy driver + libinput) + Openbox desktop — no Wayland compositor.
**mutter-desktop** = pipewire + xdg-portal-gnome + chrome-mutter + mutter-apps. GNOME Mutter headless desktop.
**selkies-desktop** = pipewire + chrome + labwc + waybar-labwc + desktop-fonts + swaync + pavucontrol + wl-tools + wl-screenshot-pixelflux + wl-overlay + wl-record-pixelflux + a11y-tools + xterm + tmux + asciinema + fastfetch + selkies. Browser-accessible Wayland desktop streamed via pixelflux WebSocket on port 3000 (HTTPS via Traefik with self-signed cert — required for WebCodecs). labwc runs nested inside pixelflux's Wayland compositor. Screenshots and video recording via a self-healing capture bridge that taps into the selkies WebSocket stream, auto-switching between controller mode (no browser) and viewer mode (browser active). Full `ov wl` automation and `ov record` support. No VNC needed — just a web browser.
**bootc-base** = sshd + guest agent + bootc config.
**openclaw-full** = openclaw + chrome + claude-code + 25 tool layers for maximal OpenClaw skill coverage.
**openclaw-full-ml** = openclaw-full + whisper + sherpa-onnx for ML capabilities.

## The Lifecycle

Overthink covers the full lifecycle — from development to production — whether you're driving or your AI agent is:

**Develop** — `ov shell <image>` drops you into an interactive container with all your layers, volumes mounted, GPU passed through. Change code, rebuild, iterate.

**Run** — `ov start <image>` launches a detached service container with the configured init system managing your processes, traefik routing your services, and persistent volumes for data.

**Deploy** — `ov config <image>` reads the image's embedded labels, generates a quadlet, provisions secrets (with `--password auto` for hands-free setup or `--password manual` to prompt), configures volume backing (`--bind name` for host bind mounts, `--encrypt name` for gocryptfs, or `--volume name:encrypt:/path` for explicit per-volume encrypted paths), saves deployment state to `~/.config/ov/deploy.yml`, and registers with systemd. For services with encrypted volumes, boot behavior depends on the credential backend: **Secret Service (keyring)** auto-starts after login (the quadlet waits for the keyring to unlock), while **KeePass or no backend** requires `ov start` after login to prompt for the passphrase. No project source needed — just the image. `ov start` also auto-configures on first launch (disable with `--enable=false`).

**Ship** — `ov build --push` builds for all platforms and pushes to your registry. `ov vm build` turns bootc images into bootable disk images.

**Manage** — `ov update` pulls new images and restarts services. `ov config mount/unmount` handles encrypted volumes (each mount runs as an independent `ov-enc-<image>-<volume>.scope` systemd unit that survives container restart/stop). `ov settings migrate-secrets` moves plaintext credentials to the system keyring (GNOME Keyring, KDE Wallet, KeePassXC). For headless/SSH environments, `ov secrets init` creates a KeePass `.kdbx` database — the master password is cached in the Linux kernel keyring for 1 hour (configurable via `ov settings set secrets.kdbx_cache_timeout`), so you only enter it once per session. `ov alias install` creates host-level command aliases that transparently run inside containers.

## Command Reference

### Build & Generate

```
ov build [image...]                    # Build for local platform
ov build --push [image...]             # Build + push (all platforms)
ov build --no-cache [image...]         # Clean build
ov build --jobs N [image...]           # Max concurrent builds (default: 4)
ov generate [--tag TAG]                # Write Containerfiles to .build/
ov validate                            # Check everything
ov merge <image> [--dry-run] [--max-total-mb N]  # Merge small layers in built images
```

### Run & Manage

```
ov shell <image> [-c CMD] [--tty]      # Interactive shell (--tty allocates PTY)
ov start <image> [--build]             # Start service container (auto-configures on first start)
ov start <image> --enable=false        # Start without auto-configuring
ov stop <image>                        # Stop container
ov config <image> [-w PATH]            # Unified setup: quadlet + secrets + volume backing
ov config <image> --password auto      # Auto-generate all secrets
ov config <image> --password manual    # Prompt for each secret
ov config <image> --bind name[=path]   # Configure volume as host bind mount
ov config <image> --encrypt name       # Configure volume as encrypted (gocryptfs)
ov config <image> -v name:type[:path]  # Per-volume backing (volume|bind|encrypted)
ov config remove <image>               # Remove quadlet + deploy.yml entry
ov config mount/unmount <image>        # Mount/unmount encrypted volumes
ov config status <image>               # Encrypted volume status
ov config passwd <image>               # Change encryption password
ov status [<image>] [--all] [--json]   # Service status (table/detail/JSON)
ov logs/update <image>                 # Service lifecycle
ov remove <image> [--purge]            # Remove service + deploy.yml entry (--purge also removes volumes)
ov remove <image> --keep-deploy        # Remove service, keep deploy.yml
ov service status/start/stop/restart   # Manage services inside container
```

### Desktop Automation

```
ov cdp open/list/close <image>         # Chrome tab management via DevTools
ov cdp click <image> <tab> <selector>  # Click element (--vnc for VNC, --wl for Wayland)
ov cdp axtree <image> <tab> [query]   # Chrome accessibility tree
ov cdp type/eval/wait/screenshot       # Form filling, JS eval, element wait, capture
ov cdp coords <image> <tab> <selector> # Show element position in viewport + desktop
ov cdp status <image>                  # Check CDP availability and port
ov cdp spa click <image> <tab> <x> <y> # Click at canvas coords (SPA scale correction)
ov cdp spa type <image> <tab> <text>   # Type into remote desktop via SPA
ov cdp spa key <image> <tab> <key>     # Send key press via SPA
ov cdp spa key-combo <image> <tab> <combo> # Modifier combo (super+e, ctrl+t, alt+F4)
ov cdp spa mouse/status                # Move pointer, show SPA state
ov vnc screenshot/click/type/key       # VNC framebuffer interaction
ov vnc mouse <image> <x> <y>           # Move cursor (verify position before clicking)
ov vnc status <image>                  # Check VNC server, show resolution
ov wl screenshot/click/type/key/mouse   # Compositor-agnostic desktop interaction
ov wl key-combo <image> <keys>         # Key combinations (ctrl+c, alt+tab)
ov wl double-click/scroll/drag         # Advanced input (scroll, drag, double-click)
ov wl toplevel/windows <image>         # List windows (wlrctl toplevel, xdotool)
ov wl focus/close/fullscreen/minimize  # Window management via wlrctl toplevel
ov wl exec <image> <command>           # Launch application in container
ov wl resolution <image> <WxH>         # Set output resolution (wlr-randr)
ov wl clipboard <image> get/set/clear  # Read/write Wayland clipboard
ov wl geometry/xprop <image>           # Window position and X11 properties
ov wl atspi <image> tree/find/click    # Accessibility tree introspection (AT-SPI2)
ov wl status <image>                   # Check all tool availability
ov wl sway msg/tree/workspaces/outputs # Sway IPC commands (requires sway)
ov wl sway focus/move/resize/kill      # Sway window management
ov wl sway layout/workspace/floating   # Sway layout and workspace control
ov wl sway reload                      # Reload sway configuration
ov wl overlay show <image> --type text --text "Hello" --name intro  # Show overlay
ov wl overlay show <image> --type lower-third --text "Name" --subtitle "Role"
ov wl overlay show <image> --type countdown --seconds 3    # Auto-hiding countdown
ov wl overlay show <image> --type highlight --region "X,Y,W,H"  # Highlight region
ov wl overlay show <image> --type fade --color black       # Fade to black
ov wl overlay show <image> --type watermark --text "DRAFT" # Corner watermark
ov wl overlay hide <image> --name intro   # Remove specific overlay
ov wl overlay hide <image> --all          # Remove all overlays
ov wl overlay list <image>                # List active overlays (JSON)
ov wl overlay status <image>              # Check overlay daemon health
```

### Command Execution

```
ov cmd <image> "command"               # Run command in running container (with notification)
ov dbus notify <image> "title" "body"  # Send desktop notification via D-Bus
ov dbus list <image>                   # List available D-Bus services
ov dbus call <image> <dest> <path> <method> [args...]  # Generic D-Bus method call
ov dbus introspect <image> <dest> <path>  # Introspect D-Bus service
```

### Recording

```
ov record start <image> [-n NAME]      # Start recording (auto-detects mode)
ov record start <image> -m terminal    # Record terminal session (asciinema)
ov record start <image> -m desktop     # Record desktop video (pixelflux/wf-recorder)
ov record stop <image> [-n NAME] [-o F] # Stop recording, optionally copy to host
ov record list <image>                 # List active recordings
ov record cmd <image> "command"        # Send command to recording terminal
```

### Persistent Sessions

```
ov tmux shell <image>                  # Persistent shell (survives disconnects)
ov tmux cmd <image> "cmd" -s <name>    # Send command to tmux session (with notification)
ov tmux run <image> -s <name> "cmd"    # Start command in detached tmux session
ov tmux attach <image> -s <name>       # Attach to session interactively
ov tmux list <image>                   # List active sessions
ov tmux capture <image> -s <name>      # Read output (for automation)
ov tmux send <image> -s <name> "text"  # Send keystrokes
ov tmux kill <image> -s <name>         # Kill session
```

### Deploy Configuration

```
ov deploy status                       # Audit deploy.yml vs quadlet sync
ov deploy show [image]                 # Display deploy.yml contents
ov deploy export [image] [-o FILE]     # Export effective config
ov deploy import <files> [--replace]   # Import deploy.yml file(s)
ov deploy reset [image]                # Remove deploy.yml overrides
ov deploy path                         # Print deploy.yml file path
```

### Virtual Machines

```
ov vm build <image> [--type qcow2|raw] # Build disk image
ov vm create <image> [--ram] [--cpus] [--ssh-key]
ov vm start/stop/destroy <image>
ov vm console/ssh <image>
ov vm list [-a]
```

### Inspect & Discover

```
ov list images/layers/targets/services/routes/volumes/aliases
ov inspect <image> [--format FIELD]
ov version
```

### Layers & Tools

```
ov new layer <name>                            # Scaffold a new layer
ov seed <image>                                # Seed bind-backed volume dirs
ov alias install/uninstall <image>             # Host command aliases
ov --kdbx <path> <command>                     # Use specific kdbx database
ov settings get/set/list/reset/path            # Runtime configuration
ov settings set forward_gpg_agent false        # Disable GPG agent forwarding
ov settings set forward_ssh_agent false        # Disable SSH agent forwarding
ov settings migrate-secrets [--dry-run]        # Move plaintext creds to system keyring
ov secrets init [path]                         # Create KeePass .kdbx database
ov secrets list/get/set/delete                 # Manage kdbx entries directly
ov secrets import [--dry-run]                  # Import creds into kdbx from config/keyring
ov secrets gpg env [-f FILE]                   # Decrypt .secrets for shell eval / direnv
ov secrets gpg show/edit/encrypt/decrypt       # Manage GPG-encrypted .secrets files
ov secrets gpg set/unset KEY [VALUE]           # Add/remove keys in .secrets
ov secrets gpg add-recipient/recipients        # Manage GPG recipients
ov udev status                                 # Show GPU device access status
ov udev generate                               # Print udev rules to stdout
ov udev install                                # Install udev rules (requires sudo)
ov udev remove                                 # Remove installed udev rules
ov doctor                                      # Check host dependencies
```

## Adding a Layer

```bash
ov new layer my-layer                  # Scaffold the directory
# Edit layers/my-layer/layer.yml      # Declare packages, deps, env, ports
# Add pixi.toml, package.json, root.yml, user.yml as needed

# Add to an image in images.yml:
#   layers: [..., my-layer]

ov build my-image                      # Build it
```

See [Building Layers](#building-layers-package-managers--config-files) above for the full list of supported config files.

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

This gives Claude Code access to 205 skills covering every layer, image, and operation — so it can build images, debug services, author new layers, and manage deployments just like you would from the command line.

The `ov-jupyter` plugin also registers a **Jupyter MCP server** at `http://localhost:8888/mcp` (when the `jupyter-colab` container is running). Claude Code can then use 13 MCP tools to create, read, edit, execute, and watch notebooks — with real-time collaboration alongside human users via CRDT. See `/ov-layers:jupyter-colab` and `/ov-images:jupyter-colab` for details.

See [CLAUDE.md](CLAUDE.md) for the complete system specification and [plugins/README.md](plugins/README.md) for the full skill reference.

## License

MIT
