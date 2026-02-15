# Overthink Build System

Composable container images from a library of layers. Build any combination into images that can layer on top of each other, across multiple platforms and package managers.

Built on `docker buildx bake` (HCL), `supervisord`, and `task` ([taskfile.dev](https://taskfile.dev)).

---

## Architecture

### Core Concepts

**Layer** -- a reusable component (`layers/<n>/`). Installs a single concern: a GPU runtime, a language toolchain, a library, or an application service. A layer is defined by one or more of these files:

| File | Purpose | Runs as | Order |
|---|---|---|---|
| `rpm.list` / `deb.list` | System packages -- one per line, `#` comments (parsed by `ov`, inlined into Containerfile) | root | 1st |
| `copr.repo` | Fedora COPR repos to enable during `dnf install` -- one `owner/project` per line (rpm only, parsed by `ov`) | root | (modifies 1st) |
| `root.yml` | Custom install logic as root (Taskfile) | root | 2nd |
| `pixi.toml` / `pyproject.toml` | Python/conda packages -- installed via multi-stage build | user | 3rd |
| `package.json` | npm packages -- installed globally via `npm install -g` (parsed by `ov`) | user | 4th |
| `Cargo.toml` | Rust crates -- built and installed to `/home/user/.cargo/bin/` | user | 5th |
| `user.yml` | Custom install logic as user (Taskfile) | user | 6th |

Additional optional files:

- `depends` -- layer dependencies, one layer name per line, `#` comments, blank lines ignored. The generator pulls in dependencies transitively and topologically sorts all layers before processing.
- `supervisord.conf` -- a `[program:<n>]` fragment. If present, the layer is a service that supervisord can manage.

A layer needs at least one of the install files. It can have any combination, including both `rpm.list` and `deb.list` for cross-distro layers. The generator reads which files are present and emits the appropriate Containerfile steps in the fixed order above. Which system package file is used depends on the `pkg` setting (default: `"rpm"`).

**Root vs user rule:** only `rpm.list`/`deb.list` and `root.yml` run as root (system packages and system-level setup). Everything else -- `pixi.toml`, `package.json`, `Cargo.toml`, `user.yml` -- runs as user. `pixi`, `npm`, and `cargo` must never run as root, not even for "global" installs. The user-space prefix paths (`/home/user/.pixi/`, `/home/user/.npm-global/`, `/home/user/.cargo/bin/`) are on PATH and do not require root.

The filename determines the execution context. No marker files, no ambiguity.

**Image** -- a named build target defined in `build.json`. Each image has a base (an external OCI image or another image from `build.json`), a list of layers, and build settings (platforms, tag, registry, pkg). Images that reference other images form a dependency graph -- the generator resolves build order automatically.

Any image whose layers include `supervisord.conf` fragments gets supervisord assembly. There is no separate "service" or "composition" concept -- it's just an image that happens to run services.

### Base Image

The base image is configurable per image via the `base` field in `build.json`. Defaults to `fedora:43` if not specified. The value can be:

- An **external OCI image** (e.g., `fedora:43`, `ubuntu:24.04`, `ghcr.io/ublue-os/bazzite:stable`)
- The **name of another image** in `build.json` (e.g., `"cuda"`, `"ml-cuda"`) -- creates a build dependency

When `base` references another image, the generator resolves the full registry/tag and emits `FROM` accordingly. The referenced image must be built first.

Common external base images:

| Image | Use case |
|---|---|
| `fedora:43` | Minimal Fedora container (default) |
| `ubuntu:24.04` | Minimal Ubuntu container |
| `quay.io/fedora/fedora-bootc:43` | Fedora bootc (bootable) |
| `ghcr.io/ublue-os/bazzite:stable` | Universal Blue gaming/desktop |
| `ghcr.io/ublue-os/bluefin:stable` | Universal Blue developer workstation |
| `ghcr.io/ublue-os/aurora:stable` | Universal Blue KDE desktop |
| `ghcr.io/ublue-os/base-main:latest` | Universal Blue minimal base |

### Bootstrap

There are two bootstraps: one for the host (developer machine / CI), one for images (inside Containerfiles).

**Host bootstrap** -- getting from a fresh clone to a working system:

Prerequisites: `task` ([taskfile.dev](https://taskfile.dev)), `go` (Go toolchain), `docker` with buildx. Optionally `podman` for bootc disk images.

```
git clone <repo> && cd <repo>
task setup:all    # builds ov, creates buildx builder
task build:all    # generates + builds everything
```

`task setup:all` runs `task build:ov` (compiles `ov` and installs it to the project's `bin/` directory which is added to PATH) then creates a multi-platform buildx builder if one doesn't exist. This is a one-time step -- subsequent builds only need `task build:all`. To rebuild `ov` after source changes, run `task build:ov`.

**Image bootstrap** -- the preamble `ov` prepends to Containerfiles for images with an external `base`:

1. **Install `task`** -- binary download to `/usr/local/bin`. Required because `root.yml` and `user.yml` are executed via `task`.
2. **Create `user` account** -- `useradd` with UID 1000, GID 1000, home `/home/user`. Skipped if the user already exists (ublue images have one). Required because user-mode layer steps run as this account.
3. **Set environment** -- `ENV` and `WORKDIR` directives:
   - `ENV NPM_CONFIG_PREFIX="/home/user/.npm-global"`
   - `ENV npm_config_cache="/home/user/.cache/npm"`
   - `ENV PATH="/home/user/.npm-global/bin:/home/user/.cargo/bin:/home/user/.pixi/envs/default/bin:${PATH}"`
   - `WORKDIR /home/user`
   - `USER user`

The PATH includes directories for pixi, npm, and cargo even if those tools aren't installed yet. Empty directories on PATH are harmless, and setting it once in the bootstrap means layers that install these tools don't need to modify PATH themselves.

Note: `ov` is host-only -- it generates files but is never installed inside images. `task` is needed in images to execute layer recipes. Docker/podman are host-only.

When `base` references another Overthink image, the image bootstrap is skipped entirely -- the parent image already has it.

**Everything else is a layer.** The project ships these standard layers:

| Layer | What it installs | How | Needed by |
|---|---|---|---|
| `pixi` | pixi binary + environment | `pixi.toml` (multi-stage build) | Any layer with `pixi.toml` |
| `supervisord` | supervisord | `pixi.toml` | Any image running services |
| `nodejs` | Node.js + npm | `rpm.list` or `deb.list` | Any layer with `package.json` |
| `rust` | Rust + Cargo | `root.yml` (rustup) | Any layer with `Cargo.toml` |

These are regular layers -- they follow the same rules as any other layer. They're listed early in an image's layer list so that later layers can use the tools they provide. There is nothing special about them from the generator's perspective.

**Example: a minimal ML image needs `pixi` before `python` and `ml-libs`:**

```json
{
  "images": {
    "ml": { "layers": ["pixi", "python", "ml-libs"] }
  }
}
```

**Example: a service image needs `supervisord` plus tool layers before app layers:**

```json
{
  "images": {
    "inference": {
      "base": "ml",
      "layers": ["supervisord", "ollama", "vllm"]
    }
  }
}
```

**Example: full toolchain image:**

```json
{
  "images": {
    "dev": { "layers": ["pixi", "nodejs", "rust", "python", "dev-tools"] }
  }
}
```

Layer ordering is resolved automatically via `depends` files. If a layer declares a dependency, the generator ensures it is installed first, pulling it in if it's not already listed in the image or provided by a parent image.

All images inherit the bootstrap foundation (directly or through the dependency chain). Any layer that adds Python packages via pixi builds on the same `/home/user` pixi project and the same default environment.

### Package Manager

The `pkg` field controls which system package tool and list file the generator uses. It defaults to `"rpm"` and can be set per image or in defaults.

| `pkg` | List file | COPR support | Install command | Cache mount |
|---|---|---|---|---|
| `"rpm"` | `rpm.list` | `copr.repo` (optional) | `dnf install -y` | `/var/cache/libdnf5` |
| `"deb"` | `deb.list` | n/a | `apt-get install -y` | `/var/cache/apt` + `/var/lib/apt` |

A layer can include both `rpm.list` and `deb.list` for cross-distro portability. The generator reads only the file matching the image's resolved `pkg`. If the matching file is absent, the package step is skipped (same as any other optional file).

### Bootc / Universal Blue Images

When the base image is a bootc-compatible image (ublue, Fedora bootc, CentOS bootc), the generated Containerfile adds a lint step at the end:

```dockerfile
RUN bootc container lint
```

This validates the image is a properly structured bootc-bootable image. Package installation (`dnf install`) and cache mounts are identical to regular Fedora images -- there is no difference in how rpm packages are handled.

**No cosign signing.** Image signing is not part of this build system. If needed, add it as a separate CI step after the build.

Set `"bootc": true` on the image (or in defaults) to enable bootc mode.

### Layer Processing

The generator processes each layer's files in a fixed order. Each step is emitted only if the corresponding file exists:

**1. `rpm.list` or `deb.list`** (as root) -- `ov` reads the list file at generation time, parses out packages, and inlines them directly into the Containerfile. No bind mount needed for this step. If the layer also has a `copr.repo` file (rpm only), `ov` reads the COPR identifiers and adds `--enable-repo` flags to the `dnf install` command.

`pkg: "rpm"` (default):
```dockerfile
# ov read layers/cuda/rpm.list and inlined the packages
RUN --mount=type=cache,dst=/var/cache/libdnf5,sharing=locked \
    dnf install -y cuda-toolkit cuda-libraries
```

`pkg: "rpm"` with `copr.repo`:
```dockerfile
# ov read layers/ublue-extras/copr.repo and layers/ublue-extras/rpm.list
RUN --mount=type=cache,dst=/var/cache/libdnf5,sharing=locked \
    dnf install \
      --enable-repo="copr:copr.fedorainfracloud.org:ublue-os:packages" \
      -y ublue-setup-services
```

`pkg: "deb"`:
```dockerfile
# ov read layers/base-tools/deb.list and inlined the packages
RUN --mount=type=cache,dst=/var/cache/apt,sharing=locked \
    --mount=type=cache,dst=/var/lib/apt,sharing=locked \
    apt-get update && apt-get install -y --no-install-recommends \
      build-essential cmake pkg-config
```

List file format: one package per line, `#` starts a comment (rest of line ignored), blank lines ignored. All parsing happens in `ov` at generation time — no shell parsing at build time.

COPR file format (`copr.repo`): one `owner/project` per line, `#` comments, blank lines ignored. `ov` expands each entry to `--enable-repo="copr:copr.fedorainfracloud.org:<owner>:<project>"`. Only valid when `pkg` is `"rpm"` -- ignored with a warning for deb images. A layer with `copr.repo` but no `rpm.list` is a validation error.

**2. `root.yml`** (as root) -- the generator emits a `RUN` with the standard scratch context pattern:

```dockerfile
RUN --mount=type=bind,from=ollama,source=/,target=/ctx \
    cd /ctx && task -t root.yml install
```

For custom root logic that system package lists can't express: adding non-COPR repos, downloading binaries, writing system config. For COPR repos, use `copr.repo` instead -- it's simpler and the generator handles the `--enable-repo` flag automatically.

**3. `pixi.toml` / `pyproject.toml` / `environment.yml`** (as user) -- the generator uses a multi-stage build process. For each layer with a pixi manifest:
   - A dedicated build stage (`FROM ghcr.io/prefix-dev/pixi:latest AS <layer>-pixi-build`) is created.
   - The manifest is copied into the build stage.
   - `pixi install` runs to resolve and install dependencies into the final environment path (e.g., `/home/user/.pixi/envs/default`).
   - The resulting environment and binary are copied from the build stage into the final image.

```dockerfile
# Build stage
FROM ghcr.io/prefix-dev/pixi:latest AS python-pixi-build
WORKDIR /home/user
COPY layers/python/pixi.toml pixi.toml
RUN pixi install

# Final image
COPY --from=python-pixi-build --chown=1000:1000 /home/user/.pixi/envs/default /home/user/.pixi/envs/default
```

**4. `package.json`** (as user) -- the generator installs the layer's packages globally from the bind-mounted context:

```dockerfile
USER user
RUN --mount=type=bind,from=web-ui,source=/,target=/ctx \
    --mount=type=cache,dst=/home/user/.cache/npm,uid=1000,gid=1000 \
    npm install -g /ctx
```

All packages install to `$NPM_CONFIG_PREFIX` (`/home/user/.npm-global/`), with binaries at `/home/user/.npm-global/bin/` which is on PATH. Same pattern as every other package manager: bind mount at `/ctx`, run the install command, only side effects persist.

**5. `Cargo.toml`** (as user) -- the generator builds the crate and installs binaries to `/home/user/.cargo/bin/`:

```dockerfile
USER user
RUN --mount=type=bind,from=my-tool,source=/,target=/ctx \
    --mount=type=cache,dst=/home/user/.cargo/registry,uid=1000,gid=1000 \
    cargo install --path /ctx
```

The entire layer directory is the crate root (Cargo.toml + `src/`). Compiled binaries land in `/home/user/.cargo/bin/` which is on PATH. Build artifacts are discarded -- only the installed binaries persist in the image.

**6. `user.yml`** (as user) -- the generator emits a `RUN` as user:

```dockerfile
USER user
RUN --mount=type=bind,from=app,source=/,target=/ctx \
    cd /ctx && task -t user.yml install
```

For custom user logic that the declarative files can't express: post-install configuration, downloading models, workspace setup.

After all steps for a layer, the generator emits `USER root` to reset the context for the next layer (whose first step may be `rpm.list` which requires root). The final Containerfile ends with `USER user` after all layers are processed. Within a layer, the generator emits `USER user` before the first user-mode step (`pixi.toml`, `package.json`, `Cargo.toml`, or `user.yml`) and never switches back to root mid-layer.

All steps share the same scratch stage (`FROM scratch AS <n>` / `COPY layers/<n>/ /`). The `--mount=type=bind,from=<n>` appears in each `RUN`.

**Examples of combinations:**

| Layer has | What happens | Use case |
|---|---|---|
| `rpm.list` only | dnf install | cuda, system libs |
| `rpm.list` + `copr.repo` | dnf install --enable-repo=copr:... | packages from COPR |
| `deb.list` only | apt-get install | cuda on Ubuntu |
| `pixi.toml` only | pixi install (multi-stage) | python, ml-libs |
| `package.json` only | npm install -g | node.js packages |
| `Cargo.toml` only | cargo install | rust CLI tool |
| `root.yml` only | root script | ollama (binary download) |
| `user.yml` only | user script | custom user config |
| `rpm.list` + `pixi.toml` | dnf, then pixi | system deps + Python |
| `rpm.list` + `package.json` | dnf, then npm install -g | native deps + Node packages |
| `pixi.toml` + `user.yml` | pixi, then user script | Python deps + config |
| `rpm.list` + `deb.list` | one or the other | cross-distro layer |
| all six | everything in order | full custom layer |

### Layer Dependencies

Layers can declare dependencies on other layers via a `depends` file — one layer name per line, `#` starts a comment (rest of line ignored), blank lines ignored:

```
# layers/python/depends
pixi
```

```
# layers/ml-libs/depends
python
```

```
# layers/web-ui/depends
nodejs
```

The generator reads `depends` files from all layers referenced in an image, transitively resolves the full dependency graph, and topologically sorts the result. Dependencies not explicitly listed in the image's `layers` array are pulled in automatically.

This means an image only needs to list what it directly wants:

```json
{ "layers": ["ml-libs", "ollama"] }
```

The generator resolves this to: `pixi` → `python` → `ml-libs`, `ollama` (with `pixi` and `python` pulled in via `ml-libs`'s dependency chain).

**Rules:**

- Circular dependencies are a validation error.
- A layer already installed by a parent image (via the `base` chain) is skipped -- the generator tracks which layers each image in the DAG provides.
- When two layers at the same depth have no ordering constraint, the generator preserves the order from the `layers` array in `build.json`.
- Explicit ordering in the `layers` array is respected as long as it doesn't violate dependency constraints. If it does, the generator fails with an error rather than silently reordering.

### Python and Pixi

All Python packages are managed by [pixi.sh](https://pixi.sh) in a single default environment at `/home/user`. No exceptions -- never use `pip install`, `conda`, or system Python packages for application dependencies.

The simplest way to add Python packages is a `pixi.toml` file with no Taskfile:

```toml
# layers/python/pixi.toml
[workspace]
name = "python-base"
channels = ["conda-forge"]
platforms = ["linux-64", "linux-aarch64"]

[dependencies]
python = ">=3.13,<3.14"
```

The generator creates a dedicated build stage for the layer, installs the dependencies using `pixi install`, and copies the resulting environment into the final image.

**The `pixi.toml` in a layer is a standalone pixi manifest.** It requires `[workspace]` (or `[project]`), `channels`, and `platforms` fields. Each layer declares its own `pixi.toml` independently of what other layers need.

**Rules:**

- Never `pip install`, `pip install --user`, `conda install`, or `dnf install python3-<package>`. Pixi is the only Python package manager.
- For PyPI-only packages not in conda-forge, add them under `[pypi-dependencies]` in `pixi.toml`.
- Layers that need both system packages and Python packages use `rpm.list` (or `deb.list`) + `pixi.toml` together.

### Node.js and npm

npm packages are managed via `package.json` manifests. The generator runs `npm install -g /ctx` which installs all dependencies globally to `$NPM_CONFIG_PREFIX` (`/home/user/.npm-global/`). Binaries land at `/home/user/.npm-global/bin/` which is on PATH.

The simplest Node.js layer is just a `package.json`:

```
layers/web-ui/
+-- package.json
+-- supervisord.conf    # optional
```

This follows the same pattern as every other package manager: bind mount at `/ctx`, run the install, only side effects persist. No files from the layer end up in the image.

**Prerequisites:** Node.js and npm must be available in the image before any layer using `package.json` is processed. Provide them via an earlier layer (e.g., `layers/nodejs/` with `rpm.list` containing `nodejs npm`, or use `depends`).

**Rules:**

- `package.json` for all npm packages. Never `npm install` in a Taskfile.
- Layers that need native build dependencies use `rpm.list` (or `deb.list`) + `package.json` together (e.g., `gcc-c++ make` / `build-essential` for native addons).

### Rust and Cargo

Rust crates are built from `Cargo.toml` in the layer directory. The generator runs `cargo install --path /ctx` which compiles the crate and installs binaries to `/home/user/.cargo/bin/` (already on PATH).

The simplest Rust layer is a full crate in the layer directory:

```
layers/my-tool/
+-- Cargo.toml
+-- src/
    +-- main.rs
```

**Prerequisites:** Rust and Cargo must be available in the image before any layer using `Cargo.toml` is processed. Provide them via an earlier layer (e.g., a `layers/rust/` with `root.yml` that installs via rustup, or `rpm.list` with `rust cargo`).

**Rules:**

- Always include a `src/` directory -- `cargo install --path` requires it.
- For crates that need system libraries, use `rpm.list` (or `deb.list`) + `Cargo.toml` together (e.g., `openssl-devel` / `libssl-dev`).

### What Gets Built

The system produces container images -- one per entry in `build.json` `images`. Each image gets a single generated Containerfile that starts `FROM <resolved base>`, prepends the bootstrap preamble (if the base is external), then processes layers in order.

When an image's `base` references another image in `build.json`, the generator resolves it to the full registry/tag. This creates a build dependency: the referenced image must be built first. Images can chain arbitrarily deep (`base` → `cuda` → `ml-cuda` → `inference`), forming a directed acyclic graph (DAG).

Images whose layers include `supervisord.conf` fragments get supervisord assembly automatically -- a `supervisord.conf` is assembled from the header template plus all service fragments. There is no separate concept for "service images"; any image with service layers becomes one.

For bootc images, the Containerfile ends with `RUN bootc container lint`.

### The Scratch Context Pattern

Each layer's files (`pixi.toml`, `package.json`, `Cargo.toml`, `root.yml`, `user.yml`, etc.) need to be available inside the `RUN` steps that process them. The pattern uses BuildKit multi-stage builds:

```dockerfile
# Named stage -- holds only this layer's files
FROM scratch AS cuda
COPY layers/cuda/ /

# The actual image being built (base from build.json)
ARG BASE_IMAGE=fedora:43
FROM ${BASE_IMAGE}

# Package install -- ov inlined the packages, no bind mount needed
RUN --mount=type=cache,dst=/var/cache/libdnf5,sharing=locked \
    dnf install -y cuda-toolkit cuda-libraries

# root.yml -- bind-mount the named stage at /ctx
RUN --mount=type=bind,from=cuda,source=/,target=/ctx \
    cd /ctx && task -t root.yml install
```

What Docker is doing:

- `FROM scratch AS cuda` + `COPY layers/cuda/ /` creates a throwaway build stage. It is **not** a Docker layer in the final image. It exists only as a mount source during the build.
- `--mount=type=bind,from=cuda,source=/,target=/ctx` makes the stage's contents available at `/ctx` for this `RUN` step only. The mount is read-only and temporary. Nothing from `/ctx` itself ends up in the image. Only the side effects of the install command (installed packages, written config files, downloaded binaries) become part of the final image.
- `--mount=type=cache,dst=/var/cache/libdnf5,sharing=locked` is a persistent cache mount shared across all builds. The dnf package cache persists between builds but is never part of any final image.

Package lists (`rpm.list`/`deb.list`) and COPR repos (`copr.repo`) are inlined by `ov` at generation time -- they don't need a bind mount. All other files (`root.yml`, `pixi.toml`, `package.json`, `Cargo.toml`, `user.yml`) are accessed via bind mount at build time because docker needs to execute them or pass them to tools.

BuildKit checksums each `COPY` independently. Changing any file in `layers/cuda/` invalidates only the `cuda` stage and its `RUN` step(s). Other stages rebuild from cache.

The same scratch stage is reused for all of a layer's bind-mount steps (root.yml, pixi.toml, package.json, Cargo.toml, user.yml). The `--mount=type=bind,from=cuda` appears in each `RUN` that needs it. The scratch stage is still emitted even if the layer only has an `rpm.list` (it's harmless and keeps generation logic simple).

### Cache Mounts

Every package manager gets a persistent cache mount that survives across builds but is never part of any final image. The generator attaches the appropriate cache mount(s) to each `RUN` step based on the layer file type and the resolved `pkg`.

| Package manager | Cache mount | Applied to | Options |
|---|---|---|---|
| dnf | `/var/cache/libdnf5` | `rpm.list`, `root.yml` | `sharing=locked` |
| apt | `/var/cache/apt` + `/var/lib/apt` | `deb.list`, `root.yml` | `sharing=locked` |
| pixi / rattler | `/home/user/.cache/rattler` | `pixi.toml` | `uid=1000,gid=1000` |
| npm | `/home/user/.cache/npm` | `package.json`, `user.yml` | `uid=1000,gid=1000` |
| cargo | `/home/user/.cargo/registry` | `Cargo.toml` | `uid=1000,gid=1000` |

All user-mode cache mounts use `uid=1000,gid=1000` so the `user` account can write to them.

The `user.yml` step always gets the npm cache mount (since it may need npm). The `root.yml` step always gets the system package cache mount (dnf or apt depending on `pkg`). This means Taskfile recipes can freely use their respective package managers without extra configuration.

### Multi-Platform

Buildx builds multi-platform images by running the entire Containerfile once per target platform. For non-native platforms, QEMU emulates the target architecture transparently. Inside `RUN` steps:

- `uname -m` returns the **target** architecture (`x86_64` or `aarch64`), not the host
- `dnf install` / `apt-get install` resolves packages for the target architecture automatically
- Binary downloads must use `uname -m` to select the correct variant

The resulting manifest list (multi-arch image) is pushed as a single tag. Docker clients pull the correct platform variant automatically.

Images can restrict platforms (e.g., CUDA amd64-only). When an image builds on top of a platform-restricted image, it inherits that restriction unless it overrides `platforms` explicitly.

### Generation

Nothing in `.build/` is hand-written. `ov generate` reads `build.json`, scans the filesystem (layer directories and their files), resolves the dependency graph, validates, and produces:

- **`.build/docker-bake.hcl`** -- one explicit target block per image. Fully expanded: no HCL variables, no matrices, no functions. Every tag, platform list, and registry is a final resolved value. Target dependencies (`depends_on`) reflect the image DAG.
- **`.build/<image>/Containerfile`** -- one per image. Bootstrap preamble (for external base images), scratch stages for each layer, with `USER` directives determined by each layer's file types. `FROM <resolved base>`. Images with service layers get supervisord assembly. Bootc images end with `RUN bootc container lint`.

All Containerfiles are unconditional -- no `if`, no `ARG`-dependent branching in `RUN` steps. The generator emits exactly the stages and steps each target needs.

Generation is idempotent. Delete `.build/`, run `ov generate`, get everything back.

### Supervisord Assembly

Layers that include a `supervisord.conf` provide a config fragment containing only their `[program:<n>]` section. Images with service layers assemble the full config at build time:

1. A `supervisord-conf` scratch stage gathers all service fragments into `/fragments/` alongside `templates/supervisord.header.conf`
2. A final `RUN` step bind-mounts that stage and concatenates header + fragments into `/etc/supervisord.conf`

No layer ever contains a `[supervisord]` section. The header template is shared.

### Bake and Build Order

`docker buildx bake` reads the generated HCL and builds targets respecting their `depends_on` declarations. The generator emits dependency edges that mirror the image DAG: if image `inference` has `base: "ml-cuda"`, then the `inference` target `depends_on` the `ml-cuda` target.

Bake resolves the topological order automatically. Images with no internal dependencies build first (and in parallel). Downstream images build once their dependencies are ready.

`task build:all` builds everything. `task build:local -- <image>` builds a single image for the host platform only (and its dependencies if not already built).

### Disk Images (Bootc Images)

Images with bootc-compatible base images can produce installable disk images (ISO, QCOW2, RAW) using [Bootc Image Builder (BIB)](https://osbuild.org/docs/bootc/). This converts a container image into a bootable disk.

**Prerequisites:** the container image must already be built. Disk image building requires `podman` and runs rootful (sudo).

**Configuration files** live in `config/`:

- `disk.toml` -- filesystem layout for QCOW2/RAW images (root partition size, filesystem type)
- `iso-gnome.toml` / `iso-kde.toml` -- Anaconda installer configuration for ISOs (kickstart, enabled modules)

The ISO configs contain a `bootc switch` reference to the target image. The generator updates these refs when producing disk images, substituting the actual registry/image/tag.

**Commands:**

```bash
task build:qcow2 -- bazzite latest    # Build QCOW2 VM image
task build:iso -- bazzite latest       # Build installable ISO
task build:raw -- bazzite latest       # Build RAW disk image
task run:vm -- bazzite latest          # Run QCOW2 in a VM (QEMU)
```

The build disk commands:
1. Load the container image into rootful podman (via `podman image scp` if built rootless)
2. Run BIB (`quay.io/centos-bootc/bootc-image-builder:latest`) with the appropriate config
3. Output to `output/<type>/`

Disk image building is only available for bootc images. Running `task build:iso` on a non-bootc image fails with an error.

---

## Configuration

### build.json

The single config file. Defines what can't be discovered from the filesystem.

```json
{
  "defaults": {
    "registry": "ghcr.io/myorg",
    "tag": "auto",
    "base": "fedora:43",
    "platforms": ["linux/amd64", "linux/arm64"],
    "pkg": "rpm"
  },
  "images": {
    "base":        { "layers": ["pixi"] },
    "cuda":        { "layers": ["pixi", "cuda"], "platforms": ["linux/amd64"] },
    "ml-cuda":     { "base": "cuda", "layers": ["python", "ml-libs"] },
    "inference":   { "base": "ml-cuda", "layers": ["supervisord", "ollama", "vllm"], "tag": "nightly" },
    "full-stack":  { "base": "ml-cuda", "layers": ["supervisord", "ollama", "vllm", "redis"] },
    "bazzite":     { "base": "ghcr.io/ublue-os/bazzite:stable", "bootc": true, "layers": ["custom-packages"], "platforms": ["linux/amd64"] },
    "ubuntu-dev":  { "base": "ubuntu:24.04", "layers": ["pixi", "nodejs", "dev-tools"], "pkg": "deb" }
  }
}
```

The dependency graph for this example:

```
fedora:43 ← base (pixi)
fedora:43 ← cuda (pixi, cuda) ← ml-cuda (python, ml-libs) ← inference (supervisord, ollama, vllm)
                                                             ← full-stack (supervisord, ollama, vllm, redis)
ghcr.io/ublue-os/bazzite:stable ← bazzite
ubuntu:24.04 ← ubuntu-dev (pixi, nodejs, dev-tools)
```

Note: `ml-cuda` doesn't list `pixi` because it inherits it from `cuda`. `inference` doesn't list `pixi` either -- it's already in the parent chain. Standard layers like `pixi` and `supervisord` only need to appear once in the ancestry.

### Versioning

When `tag` resolves to `"auto"`, the generator computes a CalVer version in semver format:

```
YYYY.DDD.HHMM
```

- **MAJOR** = year (2026)
- **MINOR** = day of year (1--366)
- **PATCH** = time of day as HHMM in UTC (0000--2359)

Examples for builds on February 14, 2026 (day 45):

```
2026.45.0830    # morning build at 08:30 UTC
2026.45.1415    # afternoon build at 14:15 UTC
2026.45.2201    # evening build at 22:01 UTC
```

This is valid semver: all three components are non-negative integers with no leading zeros. Versions sort correctly both lexically and numerically. No state or registry queries are needed -- the version is computed from the current UTC time.

The version is computed once per `ov generate` invocation and written into every `"auto"` target in the generated HCL. All targets within a single generation run share the same version, which is critical: images that reference other images by tag need consistent tags across the build.

**Tag examples** from the build.json above:

```
ghcr.io/myorg/base:2026.45.1415
ghcr.io/myorg/cuda:2026.45.1415
ghcr.io/myorg/ml-cuda:2026.45.1415
ghcr.io/myorg/inference:nightly
ghcr.io/myorg/full-stack:2026.45.1415
ghcr.io/myorg/bazzite:2026.45.1415
ghcr.io/myorg/ubuntu-dev:2026.45.1415
```

**`latest` alias:** every target with `"auto"` also gets a `latest` tag in the generated HCL, so each target produces two tags:

```hcl
target "full-stack" {
  tags = [
    "ghcr.io/myorg/full-stack:2026.45.1415",
    "ghcr.io/myorg/full-stack:latest"
  ]
}
```

This means `latest` always points to the most recent auto build, while every build is individually addressable by its versioned tag.

**Pinned tags bypass auto-versioning entirely.** When an image sets `"tag": "nightly"`, that string is used as-is: `inference:nightly`. No `latest` alias is added for pinned tags.

**Tag resolution summary:**

| `tag` value | Generated tag(s) | Use case |
|---|---|---|
| `"auto"` | `YYYY.DDD.HHMM` + `latest` | Rolling builds, CI/CD |
| `"nightly"` | `nightly` | Named channels |
| `"1.2.3"` | `1.2.3` | Pinned releases |

**CI override:** the auto version can be overridden via the `--tag` flag, which the generator reads. If set, it replaces all `"auto"` resolutions:

```bash
ov generate --tag 2026.45.1415   # force a specific version
ov generate --tag rc1            # use a release candidate label
```

### Inheritance Chain

Every setting resolves through: **image -> defaults** (first non-null wins).

| Field | Available on | Description |
|---|---|---|
| `base` | defaults, images | Base container image or name of another image. Default: `fedora:43` |
| `bootc` | defaults, images | Bootc image (`true`/`false`, default: `false`). Adds `bootc container lint` and enables disk image builds. |
| `platforms` | defaults, images | Target architectures |
| `tag` | defaults, images | Image tag. `"auto"` for CalVer. |
| `registry` | defaults, images | Container registry |
| `pkg` | defaults, images | System package manager: `"rpm"` (default) or `"deb"` |
| `layers` | images | Layer list |

Layers not referenced in any image are ignored by the generator. The filesystem is the source of truth for what layers exist; `build.json` controls what gets built.

### Validation

`ov generate` validates:

- Layers referenced in images exist as directories with at least one install file (`rpm.list`, `deb.list`, `root.yml`, `pixi.toml`, `package.json`, `Cargo.toml`, or `user.yml`).
- `copr.repo` without `rpm.list` in the same layer is a validation error.
- `copr.repo` in a layer used by a `pkg: "deb"` image emits a warning (COPR repos are ignored for deb).
- `Cargo.toml` layers include `src/`.
- Image references (`base` pointing to another image name) resolve to an existing image entry.
- The image dependency graph is acyclic (no circular references).
- `pkg` is `"rpm"` or `"deb"`.

Fails loudly on any mismatch.

---

## Directory Structure

```
project/
+-- bin/                             # Local binaries (gitignored)
|   +-- ov                          # Built by `task build:ov`
|
+-- ov/                             # ov source (Go module, host-only tool)
|   +-- go.mod
|   +-- go.sum
|   +-- main.go                     # Kong CLI definition and dispatch
|   +-- config.go                   # Parse build.json, resolve inheritance chain
|   +-- layers.go                   # Scan layers/ directory, read depends files
|   +-- graph.go                    # Topological sort for layer deps and image DAG
|   +-- generate.go                 # Emit Containerfiles and docker-bake.hcl
|   +-- validate.go                 # All validation rules
|   +-- version.go                  # CalVer computation
|   +-- scaffold.go                 # `new layer` scaffolding
|   +-- registry.go                 # Image inspection via go-containerregistry
|
+-- .build/                         # Generated (gitignored)
|   +-- docker-bake.hcl
|   +-- <image>/Containerfile
|
+-- build.json                      # Configuration
|
+-- Taskfile.yml                    # Root: includes + PATH setup
+-- taskfiles/                      # One Taskfile per command group
|   +-- Build.yml                   # ov, all, local, push, iso, qcow2, raw
|   +-- Run.yml                     # container, shell, vm
|   +-- Setup.yml                   # builder, all (build:ov + builder)
|
+-- layers/<n>/                    # At least one install file must be present:
|   +-- depends                    # Layer dependencies, one per line (optional)
|   +-- rpm.list                    # RPM packages, one per line (optional)
|   +-- deb.list                    # Deb packages, one per line (optional)
|   +-- copr.repo                   # COPR repos, owner/project per line (optional, rpm only)
|   +-- root.yml                    # Custom root install task (optional, Taskfile)
|   +-- pixi.toml                   # pixi/Python dependencies (optional)
|   +-- package.json                # npm dependencies (optional)
|   +-- Cargo.toml                  # Rust crate manifest (optional)
|   +-- src/                        # Rust source (required if Cargo.toml present)
|   +-- user.yml                    # Custom user install task (optional, Taskfile)
|   +-- supervisord.conf            # [program:<n>] fragment (optional)
+-- templates/
|   +-- supervisord.header.conf
+-- config/                    # Bootc Image Builder configs
    +-- disk.toml                   # QCOW2/RAW disk config
    +-- iso-gnome.toml              # ISO config (GNOME installer)
    +-- iso-kde.toml                # ISO config (KDE installer)
```

### Layer Directory Examples

**Standard layers** (shipped with the project):

```
layers/pixi/
+-- pixi.toml         # install: pixi binary via multi-stage build
```

```
layers/supervisord/
+-- pixi.toml         # supervisor (conda package)
```

```
layers/nodejs/
+-- rpm.list          # nodejs npm
+-- deb.list          # nodejs npm
```

```
layers/rust/
+-- root.yml          # install: curl rustup, install stable toolchain
```

**User layers:**

A pixi.toml layer (declares dependency on pixi):
```
layers/python/
+-- depends           # pixi
+-- pixi.toml         # python = ">=3.13,<3.14"
```

A pixi.toml layer (ML libraries, depends on python):
```
layers/ml-libs/
+-- depends           # python
+-- pixi.toml         # numpy, pandas, scikit-learn
```

A package.json layer (Node.js app + service):
```
layers/web-ui/
+-- package.json
+-- supervisord.conf  # [program:web-ui] command=next start
```

A package.json layer (global npm CLI tools):
```
layers/dev-tools/
+-- package.json      # typescript, eslint, prettier
```

A Cargo.toml layer (Rust CLI tool):
```
layers/my-tool/
+-- Cargo.toml
+-- src/
    +-- main.rs
```

An rpm.list + pixi.toml layer (system deps + Python packages):
```
layers/pytorch/
+-- rpm.list          # system CUDA/BLAS libraries needed by torch
+-- pixi.toml         # pytorch, torchvision
```

An rpm.list + package.json layer (native deps + Node app):
```
layers/sharp-app/
+-- rpm.list          # gcc-c++, make, vips-devel
+-- package.json
```

A root.yml layer (custom binary download, service):
```
layers/ollama/
+-- root.yml          # install: download ollama binary to /usr/local/bin
+-- supervisord.conf  # [program:ollama]
```

An rpm.list + root.yml + user.yml layer (full custom):
```
layers/code-server/
+-- rpm.list          # system dependencies
+-- root.yml          # install: download and install code-server binary
+-- user.yml          # install: configure user settings, install extensions
+-- supervisord.conf  # [program:code-server]
```

An rpm.list + copr.repo layer (packages from a COPR repository):
```
layers/ublue-extras/
+-- rpm.list          # ublue-setup-services
+-- copr.repo         # ublue-os/packages
```

**Example `rpm.list`:**

```
# layers/cuda/rpm.list
cuda-toolkit       # NVIDIA compiler and tools
cuda-libraries     # runtime libraries (cublas, cufft, etc.)
```

**Example `deb.list`:**

```
# layers/dev-tools/deb.list
build-essential    # gcc, g++, make
cmake
pkg-config
```

**Example `copr.repo`:**

```
# layers/ublue-extras/copr.repo
ublue-os/packages           # ublue setup services and tools
ublue-os/staging             # staging builds
```

The generator expands each line to a `--enable-repo` flag:
```
ublue-os/packages → --enable-repo="copr:copr.fedorainfracloud.org:ublue-os:packages"
```

**Example `pixi.toml` (Python 3.13):**

```toml
# layers/python/pixi.toml
[workspace]
name = "python-base"
channels = ["conda-forge"]
platforms = ["linux-64", "linux-aarch64"]

[dependencies]
python = ">=3.13,<3.14"
```

**Example `pixi.toml` (ML libraries with PyPI package):**

```toml
# layers/ml-libs/pixi.toml
[workspace]
name = "ml-libs"
channels = ["conda-forge"]
platforms = ["linux-64", "linux-aarch64"]

[dependencies]
numpy = "*"
pandas = "*"
scikit-learn = "*"

[pypi-dependencies]
some-pypi-only-package = "*"
```

**Example `package.json` (Node.js packages):**

```json
{
  "name": "web-ui",
  "dependencies": {
    "next": "^15.0.0",
    "react": "^19.0.0",
    "react-dom": "^19.0.0"
  }
}
```

**Example `package.json` (CLI tools):**

```json
{
  "name": "dev-tools",
  "dependencies": {
    "typescript": "^5.0.0",
    "eslint": "^9.0.0",
    "prettier": "^3.0.0"
  }
}
```

**Example `Cargo.toml` (CLI tool):**

```toml
# layers/my-tool/Cargo.toml
[package]
name = "my-tool"
version = "0.1.0"
edition = "2021"

[dependencies]
clap = { version = "4", features = ["derive"] }
serde = { version = "1", features = ["derive"] }
```

**Example `root.yml` (binary download):**

```yaml
# layers/ollama/root.yml
version: '3'
tasks:
  install:
    cmds:
      - |
        ARCH=$(uname -m)
        case "$ARCH" in
          x86_64)  ARCH=amd64 ;;
          aarch64) ARCH=arm64 ;;
          *) echo "Unsupported: $ARCH" >&2; exit 1 ;;
        esac
        curl -fsSL "https://ollama.com/download/ollama-linux-$ARCH" -o /usr/local/bin/ollama
        chmod +x /usr/local/bin/ollama
```

**Example `user.yml` (user config):**

```yaml
# layers/code-server/user.yml
version: '3'
tasks:
  install:
    cmds:
      - mkdir -p ~/.config/code-server
      - echo "bind-addr: 0.0.0.0:8080" > ~/.config/code-server/config.yaml
```

---

## Implementation

### Separation of Concerns

The system has two components with a clean split:

**`ov` (Go + Kong)** -- all computation. Parses `build.json`, scans the filesystem, resolves dependency graphs, validates, generates Containerfiles and HCL, scaffolds new layers, computes versions. Can also inspect remote registries via go-containerregistry. Pure logic, deterministic, testable. Never calls docker or podman to build. Source lives in `ov/` as a standard Go module.

**`Taskfile.yml` (Task)** -- all execution. Thin tasks that call `ov` for generation and `docker`/`podman` for builds and runs. No JSON parsing, no graph resolution, no template logic. A task is typically 1-3 commands. Three includes: `Build.yml` (build/push/disk images), `Run.yml` (containers/VMs), `Setup.yml` (create builder).

This means:
- `jq` is not a dependency (Go parses JSON natively)
- All complex logic is in compiled, tested Go code
- Taskfile tasks are trivially readable
- The `ov` binary can be used without Task (CI pipelines, scripts)

### `ov` CLI

```
ov generate [--tag TAG]                # Write .build/ (Containerfiles + HCL)
ov validate                            # Check build.json + layers, exit 0 or 1
ov inspect <image> [--format FIELD]    # Print resolved config (JSON) or single field
ov list images                         # Images from build.json
ov list layers                         # Layers from filesystem
ov list targets                        # Bake targets from generated HCL
ov list services                       # Layers with supervisord.conf
ov new layer <n>                       # Scaffold a layer directory
ov version                             # Print computed CalVer tag
```

`ov generate` is the core command. It:

1. Reads `build.json`
2. Scans `layers/` for directories and their files
3. Resolves layer dependencies (`depends` files) via topological sort
4. Resolves image DAG (`base` references between images) via topological sort
5. Validates everything (see Validation section)
6. Computes the CalVer tag (or uses `--tag`)
7. Writes `.build/docker-bake.hcl` and `.build/<image>/Containerfile` for each image

`ov inspect <image>` outputs the fully resolved config for a single image -- resolved `base`, effective `pkg`, platform list, all layers in topological order (including transitive dependencies), tags. Useful for debugging. `--format <field>` outputs a bare value for shell substitution.

`ov validate` runs all checks without writing files. Returns exit code 0 on success, non-zero with structured error messages on failure.

### Go Module

The `ov/` directory is a standard Go module. Source structure is in the Directory Structure section above.

```go
// ov/go.mod
module github.com/<owner>/overthink/ov

go 1.24

require (
    github.com/alecthomas/kong v1.6
    github.com/google/go-containerregistry v0.20
)
```

Dependencies: `kong` (CLI), `go-containerregistry` (registry inspection). JSON parsing and error handling use the Go standard library.

`task build:ov` compiles the module and copies the binary to the project's `bin/` directory. No other runtime dependencies.

### `ov` Implementation Guidelines

**CLI structure (Kong):** top-level struct with subcommand fields. Nested structs for groups (`list`, `new`). Kong uses struct tags for help text and argument parsing:

```go
package main

import "github.com/alecthomas/kong"

type CLI struct {
    Generate GenerateCmd `cmd:"" help:"Write .build/ (Containerfiles + HCL)"`
    Validate ValidateCmd `cmd:"" help:"Check build.json + layers, exit 0 or 1"`
    Inspect  InspectCmd  `cmd:"" help:"Print resolved config for an image (JSON)"`
    List     ListCmd     `cmd:"" help:"List components"`
    New      NewCmd      `cmd:"" help:"Scaffold new components"`
    Version  VersionCmd  `cmd:"" help:"Print computed CalVer tag"`
}

type GenerateCmd struct {
    Tag string `long:"tag" help:"Override tag (default: CalVer)"`
}

type InspectCmd struct {
    Image  string `arg:"" help:"Image name"`
    Format string `long:"format" help:"Output a single field instead of full JSON"`
}

type ListCmd struct {
    Images   ListImagesCmd   `cmd:"" help:"Images from build.json"`
    Layers   ListLayersCmd   `cmd:"" help:"Layers from filesystem"`
    Targets  ListTargetsCmd  `cmd:"" help:"Bake targets from generated HCL"`
    Services ListServicesCmd `cmd:"" help:"Layers with supervisord.conf"`
}

type NewCmd struct {
    Layer NewLayerCmd `cmd:"" help:"Scaffold a layer directory"`
}

type NewLayerCmd struct {
    Name string `arg:"" help:"Layer name"`
}

func main() {
    var cli CLI
    ctx := kong.Parse(&cli, kong.Name("ov"), kong.Description("Overthink build system"))
    err := ctx.Run()
    ctx.FatalIfErrorf(err)
}
```

Each command type implements a `Run() error` method containing the command logic.

**Error handling:** return `error` values, wrap with `fmt.Errorf("context: %w", err)`. Validation errors list all problems at once (don't stop at the first one):

```
error: 3 validation errors

  layers/ml-libs/depends: unknown layer "pixie" (did you mean "pixi"?)
  build.json: image "inference" has circular base reference
  build.json: image "cuda" pkg "deb" but layer "cuda" has no deb.list
```

**Exit codes:** 0 = success, 1 = validation/user error, 2 = internal error.

**Output conventions:**
- `generate`, `validate`, `new layer` -- human-readable to stderr, silent on success (unless `--verbose`)
- `inspect` -- JSON to stdout (pipeable)
- `list *` -- one item per line to stdout (pipeable, greppable)
- `version` -- bare version string to stdout

**Registry access via go-containerregistry.** `ov` uses `github.com/google/go-containerregistry` to inspect remote images without shelling out to docker or podman. This enables `ov inspect` to resolve digests and check image existence in registries. All other `ov` commands are pure filesystem operations with no network access.

**Testing:** each file has `_test.go` tests. `generate` tests use snapshot testing -- expected Containerfile and HCL output compared against golden files in a `testdata/` directory. `graph` tests cover cycle detection, diamond dependencies, transitive resolution. `validate` tests cover every error case.

**File I/O pattern:** read everything upfront (build.json + full filesystem scan of `layers/`), compute everything in memory, write everything at the end. No interleaved reads and writes. This makes the code easier to test (mock the input, assert the output) and avoids partial writes on error.

### Task as Thin Orchestration

Every task is a thin wrapper -- `ov` for smart stuff, docker/podman for execution:

```yaml
# Taskfile.yml
version: '3'

dotenv: ['.env']

env:
  PATH: "{{.ROOT_DIR}}/bin:{{.PATH}}"

includes:
  build:
    taskfile: ./taskfiles/Build.yml
    dir: "{{.ROOT_DIR}}"
  run:
    taskfile: ./taskfiles/Run.yml
    dir: "{{.ROOT_DIR}}"
  setup:
    taskfile: ./taskfiles/Setup.yml
    dir: "{{.ROOT_DIR}}"

tasks:
  default:
    desc: Show available tasks
    cmds:
      - task --list
```

```yaml
# taskfiles/Setup.yml
version: '3'

tasks:
  builder:
    desc: Create multi-platform buildx builder
    status:
      - docker buildx inspect overthink
    cmds:
      - docker buildx create --name overthink --use --bootstrap

  all:
    desc: Full setup (first time after clone)
    cmds:
      - task: :build:ov
      - task: builder
```

```yaml
# taskfiles/Build.yml
version: '3'

vars:
  BIB_IMAGE: quay.io/centos-bootc/bootc-image-builder:latest

tasks:
  ov:
    desc: Build ov from source
    dir: "{{.ROOT_DIR}}"
    cmds:
      - mkdir -p bin
      - cd ov && go build -o ../bin/ov .

  all:
    desc: Build all images (respects dependency order)
    preconditions:
      - sh: command -v ov
        msg: "ov not found -- run 'task build:ov' first"
    cmds:
      - ov generate
      - docker buildx bake -f .build/docker-bake.hcl

  local:
    desc: Build a single image for host platform only
    preconditions:
      - sh: command -v ov
        msg: "ov not found -- run 'task build:ov' first"
    vars:
      ARCH:
        sh: uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/'
    cmds:
      - ov generate
      - >-
        docker buildx bake -f .build/docker-bake.hcl
        --set '*.platform=linux/{{.ARCH}}' {{.CLI_ARGS}}

  push:
    desc: Build and push all images
    cmds:
      - ov generate
      - docker buildx bake -f .build/docker-bake.hcl --push

  iso:
    desc: "Build ISO disk image (bootc only). Usage: task build:iso -- <image> [tag]"
    preconditions:
      - sh: command -v ov
        msg: "ov not found -- run 'task build:ov' first"
      - sh: command -v podman
        msg: "podman required for disk image builds"
    cmds:
      - ov generate
      - |
        set -- {{.CLI_ARGS}}
        IMAGE="$1"; TAG="${2:-latest}"
        IMAGE_REF=$(ov inspect --format tag "$IMAGE")
        echo "Building ISO for $IMAGE_REF"
        mkdir -p output/iso
        docker save "$IMAGE_REF" | sudo podman load
        sudo podman run --rm --privileged \
          --pull=newer \
          --security-opt label=type:unconfined_t \
          -v ./config/iso-gnome.toml:/config.toml:ro \
          -v ./output/iso:/output \
          -v /var/lib/containers/storage:/var/lib/containers/storage \
          {{.BIB_IMAGE}} \
          --type iso \
          --config /config.toml \
          "$IMAGE_REF"
        echo "ISO written to output/iso/"

  qcow2:
    desc: "Build QCOW2 virtual machine image (bootc only). Usage: task build:qcow2 -- <image> [tag]"
    preconditions:
      - sh: command -v ov
        msg: "ov not found -- run 'task build:ov' first"
      - sh: command -v podman
        msg: "podman required for disk image builds"
    cmds:
      - ov generate
      - |
        set -- {{.CLI_ARGS}}
        IMAGE="$1"; TAG="${2:-latest}"
        IMAGE_REF=$(ov inspect --format tag "$IMAGE")
        echo "Building QCOW2 for $IMAGE_REF"
        mkdir -p output/qcow2
        docker save "$IMAGE_REF" | sudo podman load
        sudo podman run --rm --privileged \
          --pull=newer \
          --security-opt label=type:unconfined_t \
          -v ./config/disk.toml:/config.toml:ro \
          -v ./output/qcow2:/output \
          -v /var/lib/containers/storage:/var/lib/containers/storage \
          {{.BIB_IMAGE}} \
          --type qcow2 \
          --config /config.toml \
          "$IMAGE_REF"
        echo "QCOW2 written to output/qcow2/"

  raw:
    desc: "Build RAW disk image (bootc only). Usage: task build:raw -- <image> [tag]"
    preconditions:
      - sh: command -v ov
        msg: "ov not found -- run 'task build:ov' first"
      - sh: command -v podman
        msg: "podman required for disk image builds"
    cmds:
      - ov generate
      - |
        set -- {{.CLI_ARGS}}
        IMAGE="$1"; TAG="${2:-latest}"
        IMAGE_REF=$(ov inspect --format tag "$IMAGE")
        echo "Building RAW for $IMAGE_REF"
        mkdir -p output/raw
        docker save "$IMAGE_REF" | sudo podman load
        sudo podman run --rm --privileged \
          --pull=newer \
          --security-opt label=type:unconfined_t \
          -v ./config/disk.toml:/config.toml:ro \
          -v ./output/raw:/output \
          -v /var/lib/containers/storage:/var/lib/containers/storage \
          {{.BIB_IMAGE}} \
          --type raw \
          --config /config.toml \
          "$IMAGE_REF"
        echo "RAW image written to output/raw/"
```

```yaml
# taskfiles/Run.yml
version: '3'

tasks:
  container:
    desc: "Run a built image. Usage: task run:container -- <image>"
    cmds:
      - docker run --rm -it $(ov inspect --format tag {{.CLI_ARGS}})

  shell:
    desc: "Shell into a built image. Usage: task run:shell -- <image>"
    cmds:
      - docker run --rm -it --entrypoint bash $(ov inspect --format tag {{.CLI_ARGS}})

  vm:
    desc: "Run a QCOW2 image in QEMU. Usage: task run:vm -- <image> [tag]"
    preconditions:
      - sh: command -v qemu-system-x86_64 || command -v qemu-system-aarch64
        msg: "qemu not found -- install qemu-system"
      - sh: test -d output/qcow2
        msg: "No QCOW2 output found -- run 'task build:qcow2' first"
    vars:
      QEMU_RAM: '{{default "4G" .QEMU_RAM}}'
      QEMU_CPUS: '{{default "2" .QEMU_CPUS}}'
    cmds:
      - |
        set -- {{.CLI_ARGS}}
        IMAGE="$1"; TAG="${2:-latest}"
        DISK="output/qcow2/disk.qcow2"
        if [ ! -f "$DISK" ]; then
          echo "error: $DISK not found" >&2
          exit 1
        fi
        ARCH=$(uname -m)
        case "$ARCH" in
          x86_64)
            qemu-system-x86_64 \
              -m {{.QEMU_RAM}} -smp {{.QEMU_CPUS}} \
              -cpu host -enable-kvm \
              -drive file="$DISK",format=qcow2,if=virtio \
              -nic user,model=virtio-net-pci,hostfwd=tcp::2222-:22 \
              -serial mon:stdio -nographic
            ;;
          aarch64)
            qemu-system-aarch64 \
              -m {{.QEMU_RAM}} -smp {{.QEMU_CPUS}} \
              -cpu host -machine virt -enable-kvm \
              -drive file="$DISK",format=qcow2,if=virtio \
              -nic user,model=virtio-net-pci,hostfwd=tcp::2222-:22 \
              -serial mon:stdio -nographic
            ;;
          *) echo "Unsupported architecture: $ARCH" >&2; exit 1 ;;
        esac
```

`ov inspect` outputs full JSON by default (for piping to other tools). `ov inspect --format <field>` outputs a single value with no quoting (for use in shell substitutions).

### What `ov` Does NOT Do

`ov` never:
- Calls `docker`, `podman`, `buildx`, or any external build tool
- Pushes images to registries
- Runs containers or VMs

`ov inspect` may query remote registries via go-containerregistry (to resolve digests or check image existence), but all other commands are pure filesystem operations.

It reads files, computes, writes files. Building and running is Task's job.

---

## Commands

The user interface is `task`. Under the hood, `task` calls `ov` (Go CLI) for generation and validation, and `docker`/`podman` for builds and execution.

| Command | What runs |
|---|---|
| `task setup:all` | `task build:ov` + `docker buildx create` |
| `task setup:builder` | `docker buildx create` |
| | |
| `task build:ov` | `go build` → `bin/ov` |
| `task build:all` | `ov generate` → `docker buildx bake` |
| `task build:local -- <image>` | `ov generate` → `docker buildx bake` (host platform) |
| `task build:push` | `ov generate` → `docker buildx bake --push` |
| | |
| `task run:container -- <image>` | `docker run` |
| `task run:shell -- <image>` | `docker run --entrypoint bash` |
| | |
| `task build:iso -- <image> [tag]` | `ov generate` → `podman` + BIB |
| `task build:qcow2 -- <image> [tag]` | `ov generate` → `podman` + BIB |
| `task build:raw -- <image> [tag]` | `ov generate` → `podman` + BIB |
| `task run:vm -- <image> [tag]` | `qemu` |

Commands that are pure computation go directly through `ov` (no `task` wrapper needed):

| Command | What runs |
|---|---|
| `ov list images` | List images from build.json |
| `ov list layers` | List layers from filesystem |
| `ov list targets` | List bake targets |
| `ov list services` | List layers with supervisord.conf |
| `ov inspect <image>` | Show resolved config |
| `ov validate` | Validate everything |
| `ov new layer <n>` | Scaffold a layer directory |
| `ov version` | Print CalVer tag |

---

## Workflows

**Add a layer:** `ov new layer <n>` -> add `rpm.list`, `deb.list`, `pixi.toml`, `package.json`, `Cargo.toml`, `root.yml`, and/or `user.yml` as needed, optionally add `depends` and `supervisord.conf` -> add to an image in `build.json` -> `task build:local -- <image>`

**Add an image:** add entry to `build.json` images -> `task build:local -- <image>`

**Layer images:** set `base` to the name of an existing image in `build.json` -> `ov` handles dependency ordering and tag resolution

**Create a custom ublue image:**
1. Add an image with `base` set to a ublue image (e.g., `ghcr.io/ublue-os/bazzite:stable`) and `"bootc": true`
2. Add layers for your customizations (rpm.list for packages, root.yml for config, etc.)
3. `task build:local -- bazzite` to build
4. `task build:iso -- bazzite` to create an installable ISO
5. `sudo bootc switch ghcr.io/<owner>/<image>:<tag>` to deploy

**Build an Ubuntu-based image:**
1. Add an image with `"base": "ubuntu:24.04"` and `"pkg": "deb"`
2. Use `deb.list` files in layers (or cross-distro layers with both `rpm.list` and `deb.list`)
3. `task build:local -- ubuntu-dev`

---

## Style Guide

### Taskfile Conventions

**Schema version:** every Taskfile starts with `version: '3'`.

**Include structure:** the root `Taskfile.yml` uses `includes:` to pull in `taskfiles/Build.yml`, `taskfiles/Run.yml`, and `taskfiles/Setup.yml`. Tasks are namespaced: `build:all`, `run:container`, `setup:builder`.

**Keep tasks thin.** A task calls `ov` and/or `docker`. If a task needs conditional logic, data parsing, or error formatting, that logic belongs in `ov`, not in shell commands.

**`desc:`** every public task has a `desc:` field (powers `task --list`).

**Naming:** lowercase, single words for task names. The namespace provides grouping (e.g., `build:local`, not `build-local`).

**Arguments:** use `{{.CLI_ARGS}}` for positional arguments passed after `--`. Example: `task build:local -- myimage`.

**Cross-task calls:** use `task: :namespace:taskname` syntax to call tasks across includes. Example: `task: :build:ov` from `Setup.yml`.

**Preconditions:** use `preconditions:` to check for required tools:

```yaml
tasks:
  all:
    preconditions:
      - sh: command -v ov
        msg: "ov not found -- run 'task build:ov' first"
      - sh: command -v docker
        msg: "docker not found"
```

### Layer Install Taskfiles

Layers with a `root.yml` or `user.yml` follow this contract:

- Single task: `install`. No other task names. No parameters.
- Idempotent
- The filename determines the execution context -- `root.yml` runs as root, `user.yml` runs as user
- `root.yml`: binary downloads, custom (non-COPR) repo setup, system config. Never `dnf clean all` or `apt-get clean`. Use `rpm.list` or `deb.list` for simple package installs. Use `copr.repo` for COPR repositories.
- `user.yml`: post-install configuration, downloading models, workspace setup. Never `sudo`.
- **Python packages belong in `pixi.toml`.** Never `pip install`, `conda install`, or `pixi add` in a Taskfile. Never run pixi as root.
- **npm packages belong in `package.json`.** Never `npm install` in a Taskfile. Never run npm as root.
- **Rust crates belong in `Cargo.toml`.** Never `cargo install` in a Taskfile. Never run cargo as root.
- **System packages belong in `rpm.list` or `deb.list` when possible.** Use `copr.repo` for Fedora COPR repositories. Only use `dnf install` / `apt-get install` in `root.yml` for non-COPR custom repos or post-install config.
- **Binary downloads in `root.yml`:** detect arch with `uname -m`, map via `case`, fail on unsupported
- `ov` generates `RUN` steps that invoke `task -t root.yml install` or `task -t user.yml install`

### Generated Files

Every generated file starts with `# <path> (generated -- do not edit)`.

Containerfile rules:
- `ARG BASE_IMAGE=<resolved base>` + `FROM ${BASE_IMAGE}` at the top
- One `FROM scratch AS <layer-name>` + `COPY layers/<n>/ /` per layer
- Up to six `RUN` steps per layer, in order: rpm.list/deb.list (root), root.yml (root), pixi.toml (user), package.json (user), Cargo.toml (user), user.yml (user)
- rpm.list/deb.list packages are inlined by `ov` -- no bind mount for this step. COPR repos from `copr.repo` are inlined as `--enable-repo` flags on the `dnf install` command.
- All other `RUN` steps use `--mount=type=bind,from=<layer-name>,source=/,target=/ctx`
- Cache mounts per the Cache Mounts table (dnf/apt, rattler, npm, cargo -- attached based on step type and `pkg`)
- `USER root` only for rpm.list/deb.list and root.yml steps. `USER user` for pixi.toml, package.json, Cargo.toml, user.yml. pixi, npm, and cargo never run as root.
- Taskfiles invoked as `task -t root.yml install` or `task -t user.yml install`
- No conditionals -- unconditional `RUN` only
- Final directive is always `USER user`
- Bootc images: final line is `RUN bootc container lint`
- Generated HCL: fully expanded, no variables, no matrices. `depends_on` reflects image DAG.

### General

- **Naming:** lowercase-hyphenated for layers and images
- **No shell scripts:** all automation in Taskfiles, all logic in `ov`
- **No Docker layer cleanup:** cache mounts handle it
- **No cosign:** image signing is not part of this build system
- **Cache mounts:** one per package manager (see Cache Mounts section), never in final image
- **Base image:** configurable per image via `base` in build.json (default: `fedora:43`); can reference another image for layering
- **Package manager:** `"rpm"` (default) or `"deb"`, configurable per image via `pkg`
- **Bootc support:** set `"bootc": true` to add `bootc container lint` and enable disk image builds. Default: `false`. No difference in package installation.
- **Python:** all packages via pixi.sh, single default environment at `/home/user`, user permissions only
- **Node.js:** all packages via `package.json` (`npm install -g`), prefix at `/home/user/.npm-global/`
- **Rust:** crates via Cargo.toml, binaries installed to `/home/user/.cargo/bin/`
- **Default user:** `user` (UID 1000) -- all containers run as non-root by default
- **Root only for system packages:** `rpm.list`/`deb.list` and `root.yml`. pixi, npm, and cargo always run as user, never root.
- **`.build/` is disposable**
- **Disk images:** ISO/QCOW2/RAW via Bootc Image Builder for bootc images