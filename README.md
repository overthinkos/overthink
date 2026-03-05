# Overthink

**Composable container images from a library of snap-together layers.**

Stop writing Dockerfiles. Define what you need — Python, CUDA, Jupyter, a reverse proxy, a Wayland desktop — and Overthink composes it into optimized multi-stage container images. Same definition takes you from an interactive dev shell to a running service to a systemd unit to a bootable VM disk image.

65 layers. 31 pre-built image definitions. Docker and Podman. `linux/amd64` and `linux/arm64`. One CLI: `ov`.

## Why Overthink?

Every container project starts the same way: copy a Dockerfile, paste in package installs, fight with layer ordering, repeat for the next variant. Need GPU support? Another Dockerfile. Want a desktop environment inside the container? Good luck.

Overthink treats container images like composable building blocks. Each **layer** is a self-contained unit — its packages, environment variables, services, volumes, and dependencies declared in a simple `layer.yml`. An **image** is just a list of layers on top of a base. The `ov` CLI resolves the dependency graph, generates optimized Containerfiles with multi-stage builds and cache mounts, and builds everything in the right order.

Want a GPU-accelerated Jupyter notebook? That's `cuda` + `jupyter` — two layers, one image definition, done. Need to add Ollama for local LLMs? Drop in the `ollama` layer. Want a full AI workstation with a Wayland desktop, Chrome, VNC, and an AI gateway? That's still just a list of layers in `images.yml`. Overthink handles the rest: dependency resolution, build ordering, supervisor configs, traefik routes, volume declarations, and GPU passthrough.

## Key Concepts

### Layers, Images, and Multi-Service Containers

A layer is a reusable building block — packages, config, services. An image is layers stacked on a base. The key insight: **you can combine multiple services into a single container image** just by listing layers. Need PostgreSQL, Redis, a Python API, and a reverse proxy in one container? Add those four layers to your image. `ov` resolves dependencies, generates an optimized Containerfile, and wires up supervisord to run all services together when the container starts.

### Building Layers: Package Managers & Config Files

Each layer lives in its own directory under `layers/` and can use any combination of these files:

- **`layer.yml`** — The layer's manifest: system packages (`rpm:` for Fedora/RHEL, `deb:` for Debian/Ubuntu), dependencies on other layers, environment variables, ports, services, volumes, and routes
- **`pixi.toml`** / **`pyproject.toml`** / **`environment.yml`** — Python and conda packages via the Pixi package manager (multi-stage build, runs as user)
- **`package.json`** — npm packages for Node.js (multi-stage build, runs as user)
- **`Cargo.toml`** + **`src/`** — Rust crate compilation (multi-stage build, runs as user)
- **`root.yml`** — Custom install script (Taskfile format) that runs as root — for anything packages can't cover
- **`user.yml`** — Custom install script (Taskfile format) that runs as the container user

`ov` detects which files are present and generates the appropriate build stages automatically. You only include what you need — a layer with just `layer.yml` listing rpm packages is perfectly valid.

### Docker or Podman — Your Choice

Docker is the container tool most people know. Podman is a newer alternative from Red Hat that runs without a background daemon and integrates natively with Linux systemd. `ov` works with either — same commands, same images, same results. Switch with `ov config set engine.build podman`.

### Two Process Managers, Two Levels

**Inside containers**, Overthink uses **supervisord** — a lightweight process manager that runs multiple services within a single container. When a layer declares a `service:` in its `layer.yml`, `ov` generates a supervisord config and bundles it into the image. The container starts supervisord as its main process, and supervisord starts and monitors all your services. This is how you get PostgreSQL, Traefik, and your application all running in one container.

**On the host**, Overthink uses **systemd** — the init system that already manages your Linux machine. When you run `ov enable`, it generates a Podman quadlet that registers your container as a systemd service. So systemd manages the container, and supervisord manages the services inside it. Two levels, cleanly separated.

**In bootc VM images**, systemd takes over completely — it's PID 1 at the OS level, running services like sshd and cloud-init directly. No supervisord needed because it's a real operating system, not a container.

### Quadlets: Containers as System Services

With Docker, you'd use `docker compose` or a restart policy to keep a container running. Podman quadlets are different: they describe a container as a native systemd service — the same system that manages SSH, networking, and everything else on your Linux box. `ov enable <image>` generates the quadlet file and registers it. After that, `systemctl start/stop/status` just work — your container starts on boot, restarts on failure, and shows up in `journalctl` logs like any other service.

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

## Quick Taste

```bash
# Build a single image for your platform
ov build fedora

# Drop into an interactive shell
ov shell fedora

# Build and run a GPU-accelerated Jupyter server
ov build jupyter
ov start jupyter

# Deploy as a systemd service
ov enable jupyter

# Build a bootable VM disk image
ov vm build openclaw-browser-bootc --type qcow2
ov vm create openclaw-browser-bootc --ram 8G --cpus 4 --gpu
ov vm start openclaw-browser-bootc
```

## The Layer Library

Layers compose. Pick what you need, and dependencies resolve automatically.

### Foundations

**pixi** — The Pixi package manager, foundation for Python and conda environments.
**python** — Python 3.13 via Pixi. **nodejs** / **node24** — Node.js + npm. **rust** — Rust + Cargo. **golang** — Go compiler. **language-runtimes** — Go, PHP, .NET, and more. **build-toolchain** — gcc, cmake, autoconf, ninja, git, pkg-config.

### Services & Infrastructure

**supervisord** — Process manager that ties multi-service containers together. **traefik** — Reverse proxy with automatic route discovery (`:8000`/`:8080`). **postgresql** — Postgres on `:5432` with a persistent volume. **redis** — Redis on `:6379`. **docker-ce** — Docker CE + buildx + compose inside containers. **kubernetes** — kubectl + Helm.

### GPU & Machine Learning

**cuda** — NVIDIA CUDA toolkit + cuDNN + ONNX Runtime. **python-ml** — ML Python environment on top of CUDA. **jupyter** — Jupyter + ML libraries on `:8888`. **ollama** — LLM inference server on `:11434` with model volume. **comfyui** — Image generation UI on `:8188`.

### Desktop Environments

**sway** / **niri** / **cage** — Wayland compositors (full desktop, tiling, kiosk mode). **wayvnc** — VNC server on `:5900`. **pipewire** — Audio/media server. **google-chrome** / **google-chrome-sway** — Chrome with DevTools on `:9222`. **quickshell** / **dank-material-shell** / **noctalia** — Desktop shells and launchers.

### Applications

**openclaw** — AI gateway on `:18789`. **claude-code** — Claude Code CLI. **immich** / **immich-ml** — Self-hosted photo management with ML backend. **github-runner** — GitHub Actions runner as a service. **vscode** — VS Code. **dev-tools** — bat, ripgrep, neovim, gh, direnv, fd-find, htop.

### Utilities

**gocryptfs** — Encrypted filesystem for `ov crypto` operations. **socat** — Socket relay for VM console access.

### OS / Bootc

**sshd** — SSH server. **cloud-init** — Cloud instance initialization. **bootc-config** — Bootc system configuration (autologin, graphical target). **bcvk** — Bootc virtualization kit for building disk images. **qemu-guest-agent** — VM guest agent with libvirt channel.

### Composing Layers

Some layers are pure composition — they pull in a curated set of other layers:
**sway-desktop** = pipewire + wayvnc + chrome + file manager + shell.
**bootc-base** = sshd + guest agent + bootc config.

## The Lifecycle

Overthink covers the full journey from development to production:

**Develop** — `ov shell <image>` drops you into an interactive container with all your layers, volumes mounted, GPU passed through. Change code, rebuild, iterate.

**Run** — `ov start <image>` launches a detached service container with supervisord managing your processes, traefik routing your services, and persistent volumes for data.

**Deploy** — `ov enable <image>` generates a quadlet and registers it with systemd. Your container starts on boot, restarts on failure, and integrates with `systemctl`.

**Ship** — `ov build --push` builds for all platforms and pushes to your registry. `ov vm build` turns bootc images into bootable disk images.

**Manage** — `ov update` pulls new images and restarts services. `ov crypto init/mount` handles encrypted bind-mount volumes. `ov alias install` creates host-level command aliases that transparently run inside containers.

## Command Reference

### Build & Generate

```
ov build [image...]                    # Build for local platform
ov build --push [image...]             # Build + push (all platforms)
ov build --no-cache [image...]         # Clean build
ov build --jobs N [image...]           # Max concurrent builds (default: 4)
ov generate [--tag TAG]                # Write Containerfiles to .build/
ov validate                            # Check everything
ov merge <image> [--dry-run]           # Merge small layers in built images
```

### Run & Manage

```
ov shell <image> [-c CMD] [--gpu]      # Interactive shell
ov start <image> [--gpu] [--build]     # Start service container
ov stop <image>                        # Stop container
ov enable <image>                      # Systemd quadlet service
ov disable/status/logs/update <image>  # Service lifecycle
ov remove <image> [--volumes]          # Remove service (optionally with volumes)
```

### Virtual Machines

```
ov vm build <image> [--type qcow2|raw] # Build disk image
ov vm create <image> [--ram] [--cpus] [--gpu]
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
ov seed <image>                                # Seed bind mount dirs
ov alias install/uninstall <image>             # Host command aliases
ov crypto init/mount/unmount/status <image>    # Encrypted volumes
ov crypto passwd <image>                       # Change encryption password
ov config get/set/list/reset/path              # Runtime configuration
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

## Documentation

See [CLAUDE.md](CLAUDE.md) for the complete system specification.

## License

MIT
