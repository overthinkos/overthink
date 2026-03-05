# Overthink

Composable container images from a library of layers. Build any combination into images that can layer on top of each other, across multiple platforms and package managers.

Built on `supervisord` and `ov` (Go CLI). Supports both Docker and Podman.

## Quick Start

```bash
# Prerequisites: go, docker (or podman) with buildx

# Setup (one-time) -- downloads task, builds ov
bash setup.sh

# Build all images
ov build

# Build single image for host platform
ov build fedora

# Shell into a built image
ov shell fedora

# Start a service container
ov start fedora-test
```

## Project Structure

```
overthink/
├── images.yml              # Image definitions (base, layers, ports, merge)
├── layers/                 # Reusable layer components (~58 layers)
│   ├── pixi/               # Pixi binary + default env
│   ├── python/             # Python 3.13 via pixi
│   ├── nodejs/             # Node.js + npm
│   ├── rust/               # Rust + Cargo
│   ├── supervisord/        # Process manager
│   ├── traefik/            # Reverse proxy
│   ├── cuda/               # NVIDIA CUDA toolkit
│   ├── openclaw/           # OpenClaw gateway (npm, volumes, service)
│   └── ...                 # build-toolchain, dev-tools, docker-ce, etc.
├── ov/                     # Go CLI source
│   ├── main.go             # CLI (Kong)
│   ├── config.go           # images.yml parsing, inheritance
│   ├── layers.go           # Layer scanning, file detection
│   ├── generate.go         # Containerfile generation
│   ├── validate.go         # All validation rules
│   ├── graph.go            # Topological sort (layers + images)
│   ├── env.go              # ENV config merging, path expansion
│   ├── merge.go            # Post-build layer merging
│   ├── shell.go            # Shell command (docker run)
│   ├── start.go            # Start/stop service containers
│   ├── quadlet.go          # Podman quadlet systemd services
│   ├── security.go         # Container security config
│   ├── envfile.go          # .env file parsing, env var resolution
│   ├── seed.go             # Bind mount data seeding
│   ├── remote_image.go     # Remote image ref resolution
│   ├── gpu.go              # GPU auto-detection + passthrough
│   ├── volumes.go          # Named volume collection + mounting
│   ├── registry.go         # Remote image inspection
│   ├── version.go          # CalVer computation
│   ├── scaffold.go         # Layer scaffolding
│   ├── vm.go               # VM lifecycle (create, start, stop, destroy, list, console, ssh)
│   ├── vm_build.go         # VM disk image builds (qcow2, raw via bcvk)
│   └── libvirt.go          # Libvirt XML snippet injection
├── setup.sh                # Bootstrap: downloads task, builds ov
├── Taskfile.yml            # Bootstrap tasks only
├── taskfiles/              # Build.yml, Setup.yml
├── templates/              # Supervisord header
└── config/                 # Bootc Image Builder configs
```

## Commands

### Bootstrap (task)

Task is used only for bootstrapping. All other operations use `ov` directly.

| Command | Description |
|---------|-------------|
| `task build:ov` | Build ov from source into `bin/ov` |
| `task build:install` | Build and install ov to `~/.local/bin` |
| `task setup:builder` | Create multi-platform buildx builder |
| `task setup:all` | Full setup (build ov + create builder) |

### ov Commands

| Command | Description |
|---------|-------------|
| `ov generate [--tag TAG]` | Write .build/ (Containerfiles) |
| `ov validate` | Check images.yml + layers |
| `ov inspect <image> [--format FIELD]` | Print resolved config (JSON or single field) |
| `ov list images` | List images from images.yml |
| `ov list layers` | List layers from filesystem |
| `ov list targets` | Build targets in dependency order |
| `ov list services` | List layers with service definitions |
| `ov list routes` | List layers with route definitions |
| `ov list volumes` | List layers with volume declarations |
| `ov list aliases` | List layers with alias declarations |
| `ov build [image...]` | Build for local platform |
| `ov build --push [image...]` | Build for all platforms and push |
| `ov build --no-cache [image...]` | Build without any cache |
| `ov build --cache registry [image...]` | Build with registry cache (read+write) |
| `ov build --cache image [image...]` | Use registry image as cache source (read-only) |
| `ov build --cache gha [image...]` | GitHub Actions cache |
| `ov merge <image> [--max-mb N] [--tag TAG] [--dry-run]` | Merge small layers in a built image |
| `ov merge --all [--dry-run]` | Merge all images with merge.auto enabled |
| `ov mod get/download/tidy/verify/update/list` | Remote module management |
| `ov new layer <name>` | Scaffold a new layer |
| `ov seed <image> [--tag TAG]` | Seed empty bind mount dirs from image |
| `ov shell <image> [-w PATH] [-c CMD] [-e K=V] [--env-file] [-i INST] [--build]` | Bash shell in a container |
| `ov start <image> [-w PATH] [-e K=V] [--env-file] [-i INST] [--build]` | Start service container (detached) |
| `ov stop <image> [-i INST]` | Stop a running service container |
| `ov enable <image> [-w PATH] [-e K=V] [--env-file] [-i INST] [--build]` | Generate quadlet file, daemon-reload |
| `ov disable <image> [-i INST]` | Disable quadlet auto-start |
| `ov status <image> [-i INST]` | Show service status |
| `ov logs <image> [-f] [-i INST]` | Show service logs |
| `ov update <image> [--tag TAG] [-i INST] [--build]` | Update image + restart |
| `ov remove <image> [-i INST]` | Stop + remove service |
| `ov alias install/uninstall/add/remove/list <image>` | Host command aliases |
| `ov crypto init/mount/unmount/status/passwd <image>` | Encrypted bind mounts |
| `ov vm build <image> [--type qcow2\|raw] [--ssh-keygen] [--console]` | Build disk image from bootc container |
| `ov vm create <image> [--ram SIZE] [--cpus N] [--gpu]` | Create VM from disk image |
| `ov vm start/stop/destroy <image>` | VM lifecycle management |
| `ov vm list [-a]` | List VMs |
| `ov vm console/ssh <image>` | VM access |
| `ov config get/set/list/reset/path` | Runtime configuration |
| `ov version` | Print CalVer tag |

## Adding a Layer

```bash
# Create layer directory
ov new layer my-layer

# Edit layers/my-layer/layer.yml for packages, deps, env, ports, etc.
# Add pixi.toml, package.json, Cargo.toml, root.yml, user.yml as needed

# Add to an image in images.yml
# Build
ov build my-image
```

## Layer Files

| File | Purpose | Runs as |
|------|---------|---------|
| `layer.yml` | Layer config: rpm/deb packages, depends, env, path_append, ports, route, service, volumes, security | root (packages) / metadata |
| `root.yml` | Custom root install (Taskfile) | root |
| `pixi.toml` / `pyproject.toml` / `environment.yml` | Python/conda packages (multi-stage build) | user |
| `package.json` | npm packages (multi-stage build) | user |
| `Cargo.toml` | Rust crate (requires `src/`) | user |
| `user.yml` | Custom user install (Taskfile) | user |

## Documentation

See [CLAUDE.md](CLAUDE.md) for the complete system specification.

## License

MIT
