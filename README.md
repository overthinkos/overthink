# Overthink

Composable container images from a library of layers. Build any combination into images that can layer on top of each other, across multiple platforms and package managers.

Built on `docker buildx bake` (HCL), `supervisord`, and `task` ([taskfile.dev](https://taskfile.dev)).

## Quick Start

```bash
# Prerequisites: task, go, docker with buildx

# Setup (one-time)
task setup:all

# Build all images
task build:all

# Build single image for host platform
task build:local -- base
```

## Project Structure

```
overthink/
├── build.json         # Image definitions
├── layers/            # Reusable layer components
│   ├── pixi/          # Python package manager
│   ├── python/        # Python via pixi
│   ├── nodejs/        # Node.js + npm
│   ├── rust/          # Rust toolchain
│   └── supervisord/   # Process manager
├── ov/                # Go CLI source
├── taskfiles/         # Task automation
├── templates/         # Supervisord header
└── config/            # Bootc Image Builder configs
```

## Commands

### Task Commands

| Command | Description |
|---------|-------------|
| `task setup:all` | Build ov + create buildx builder |
| `task build:all` | Generate + build all images |
| `task build:local -- <image>` | Build single image (host platform) |
| `task build:push` | Build + push all images |
| `task run:container -- <image>` | Run image interactively |
| `task run:shell -- <image>` | Shell into image |

### ov Commands

| Command | Description |
|---------|-------------|
| `ov generate` | Write .build/ (Containerfiles + HCL) |
| `ov validate` | Check build.json + layers |
| `ov inspect <image>` | Print resolved config (JSON) |
| `ov list images` | List images from build.json |
| `ov list layers` | List layers from filesystem |
| `ov new layer <name>` | Scaffold a new layer |
| `ov version` | Print CalVer tag |

## Adding a Layer

```bash
# Create layer directory
ov new layer my-layer

# Edit layers/my-layer/rpm.list with packages
# Or add pixi.toml, package.json, Cargo.toml, etc.

# Add to an image in build.json
# Build
task build:local -- my-image
```

## Layer Files

| File | Purpose | Runs as |
|------|---------|---------|
| `rpm.list` | RPM packages | root |
| `deb.list` | Deb packages | root |
| `root.yml` | Custom root install | root |
| `pixi.toml` | Python/conda packages | user |
| `package.json` | npm packages | user |
| `Cargo.toml` | Rust crate | user |
| `user.yml` | Custom user install | user |
| `depends` | Layer dependencies | - |
| `supervisord.conf` | Service config | - |

## Documentation

See [CLAUDE.md](CLAUDE.md) for the complete system specification.

## License

MIT
