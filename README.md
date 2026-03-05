# Overthink

Composable container images from a library of layers. Build any combination into images that can layer on top of each other, across multiple platforms and package managers.

Built on `supervisord` and `task` ([taskfile.dev](https://taskfile.dev)). Supports both Docker and Podman.

## Quick Start

```bash
# Prerequisites: task, go, docker with buildx

# Setup (one-time)
task setup:all

# Build all images
task build:all

# Build single image for host platform
task build:local -- fedora

# Shell into a built image
ov shell fedora

# Start a service container
ov start fedora-test
```

## Project Structure

```
overthink/
├── images.yml              # Image definitions (base, layers, ports, merge)
├── layers/                 # Reusable layer components (~52 layers)
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
│   └── scaffold.go         # Layer scaffolding
├── taskfiles/              # Task automation
│   ├── Build.yml           # ov, all, local, push, merge, iso, qcow2, raw
│   ├── Run.yml             # container, shell, pod:*
│   └── Setup.yml           # builder, all
├── templates/              # Supervisord header
└── config/                 # Bootc Image Builder configs
```

## Commands

### Task Commands

| Command | Description |
|---------|-------------|
| `task setup:all` | Build ov + create buildx builder |
| `task build:all` | Generate + build all images + merge |
| `task build:local -- <image>` | Build single image (host platform) + merge |
| `task build:push` | Build + push all images |
| `task build:merge -- <image>` | Merge small layers in a built image |
| `task build:iso -- <image> [tag]` | Build ISO via Bootc Image Builder |
| `task build:qcow2 -- <image> [tag]` | Build QCOW2 VM image |
| `task build:raw -- <image> [tag]` | Build RAW disk image |
| `task run:shell -- <image>` | Shell into image (delegates to `ov shell`) |
| `task run:start -- <image>` | Start service |
| `task run:stop -- <image>` | Stop service |
| `task run:status -- <image>` | Service status |
| `task run:logs -- <image>` | Service logs |
| `task run:update -- <image>` | Update and restart |
| `task run:remove -- <image>` | Remove service |
| `task run:enable -- <image>` | Enable quadlet service |
| `task run:disable -- <image>` | Disable quadlet service |
| `task run:vm -- <image> [tag]` | Run QCOW2 in QEMU |

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
| `ov build --cache registry [image...]` | Build with registry cache |
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
task build:local -- my-image
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
