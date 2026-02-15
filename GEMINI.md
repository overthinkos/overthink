# GEMINI.md - Overthink Project Context

## Project Overview
**Overthink** is a specialized build system for creating composable container images from a library of reusable "layers." It aims to simplify the creation of complex, layered images across multiple platforms and package managers (RPM, Deb, Pixi, npm, Cargo).

### Core Technologies
- **Go**: Powers the `ov` CLI tool for generation and logic.
- **Docker Buildx Bake**: Used for efficient, parallel, multi-platform image builds via HCL.
- **Task (go-task)**: Orchestrates build commands and execution inside container layers.
- **Supervisord**: Manages multiple processes within the final images.
- **Bootc**: Supports building bootable container images and disk images (ISO, QCOW2).

---

## Architecture & Concepts

### 1. Layers (`layers/`)
A layer is a reusable component that installs a specific tool or service.
- **Declarative Files**: `rpm.list`, `deb.list`, `copr.repo`, `pixi.toml`, `package.json`, `Cargo.toml`.
- **Procedural Files**: `root.yml` (runs as root), `user.yml` (runs as user) – both are Taskfiles.
- **Dependencies**: `depends` file for topological sorting.
- **Services**: `supervisord.conf` fragments for process management.

### 2. Images (`build.json`)
Images are defined by a base (external or project-internal) and a list of layers.
- **Inheritance**: Images can inherit from other images, forming a DAG.
- **Defaults**: Global settings for registry, tag (CalVer), platforms, and package manager (`pkg`).

### 3. Build Process
- **Generation**: `ov generate` reads the configuration and filesystem to produce `.build/docker-bake.hcl` and a `Containerfile` for each image.
- **Bake**: `docker buildx bake` executes the builds based on the generated HCL.
- **Cache**: Uses BuildKit cache mounts for all package managers to speed up subsequent builds.

---

## Building and Running

### Prerequisites
- `go`, `task`, `docker` (with buildx), and optionally `podman` (for bootc/disk images).

### Key Commands

| Task Command | Description |
|--------------|-------------|
| `task setup:all` | Initial setup: builds `ov` and creates buildx builder. |
| `task build:all` | Generates and builds all images defined in `build.json`. |
| `task build:local -- <image>` | Builds a single image for the host platform. |
| `task build:iso -- <image>` | Builds a bootable ISO (requires bootc base). |
| `task run:container -- <image>` | Runs the built image interactively. |

| ov Command | Description |
|------------|-------------|
| `ov generate` | Computes the build graph and writes `.build/` files. |
| `ov validate` | Performs deep validation of layers and image configurations. |
| `ov inspect <image>` | Prints the fully resolved configuration for an image. |
| `ov new layer <name>` | Scaffolds a new layer directory. |

---

## Development Conventions

### 1. Layer Design
- **Single Concern**: Each layer should focus on one tool or service.
- **Permission Split**: Only use `root.yml` or package lists for root-level changes. Use `user.yml`, `pixi.toml`, etc., for user-level (UID 1000) installations.
- **Idempotency**: All installation scripts must be idempotent.
- **Cross-Distro**: Include both `rpm.list` and `deb.list` if the layer should support both Fedora and Ubuntu/Debian bases.

### 2. Project Structure
- `ov/`: Go source code for the build logic.
- `layers/`: Library of reusable layers.
- `taskfiles/`: Modular Taskfiles for build, run, and setup orchestration.
- `config/`: Configuration for Bootc Image Builder (disk images).
- `.build/`: Generated artifacts (gitignored).

### 3. Versioning
- Uses **CalVer** (`YYYY.DDD.HHMM`) for automated tags.
- The `ov version` command provides the current session's version string.

---

## Key Files Summary

- `build.json`: The central configuration for all images.
- `Taskfile.yml`: Entry point for all automation.
- `CLAUDE.md`: Comprehensive system specification and deep technical details.
- `ov/main.go`: Entry point for the `ov` CLI.
- `templates/supervisord.header.conf`: Shared header for assembled supervisord configs.
