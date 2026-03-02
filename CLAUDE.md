# Overthink Build System

Compose container images from a library of fully configurable layers.
Built on `supervisord` and `task` ([taskfile.dev](https://taskfile.dev)). Supports both Docker and Podman as build/run engines.

---

## Architecture Overview

Two components with a clean split:

**`ov` (Go CLI)** -- all computation and building. Parses `images.yml`, scans `layers/`, resolves dependency graphs, validates, generates Containerfiles, builds images via `<engine> build`. Source: `ov/`. Registry inspection via go-containerregistry. `ov shell`/`ov start`/`ov stop`/`ov merge`/`ov enable` use the configured engine (Docker or Podman).

**`task` (Taskfile)** -- thin wrappers that call `ov` commands. No YAML parsing, no graph logic. Source: `Taskfile.yml` + `taskfiles/{Build,Run,Setup}.yml`. Run `task -l` for all available commands.

**What gets generated** (`ov generate`):
- `.build/<image>/Containerfile` -- one per image, unconditional `RUN` steps only
- `.build/<image>/traefik-routes.yml` -- traefik dynamic config (only for images with `route` layers)
- `.build/<image>/supervisor/*.conf` -- supervisord service configs (only for images with `service` layers)

Generation is idempotent. `.build/` is disposable and gitignored.

---

## Layer Definition

A **layer** is a directory under `layers/<name>/` that installs a single concern. It must contain at least one install file.

### Install Files (processed in this order)

| File | Runs as | Purpose |
|---|---|---|
| `layer.yml` `rpm`/`deb` | root | System packages declared in `layer.yml`. See [Layer Config](#layer-config-layeryml). |
| `root.yml` | root | Custom root install logic (Taskfile). Binary downloads, system config. |
| `pixi.toml` / `pyproject.toml` / `environment.yml` | user | Python/conda packages. Multi-stage build (see Pixi section). Only one per layer. |
| `package.json` | user | npm packages -- installed globally via `npm install -g`. |
| `Cargo.toml` | user | Rust crate -- built via `cargo install --path`. Requires `src/` directory. |
| `user.yml` | user | Custom user install logic (Taskfile). Post-install config, workspace setup. |

### Layer Config (`layer.yml`)

Optional YAML file consolidating all layer metadata. Parsed by `ov/layers.go:parseLayerYAML()`.

| Field | Type | Purpose |
|---|---|---|
| `depends` | `[]string` | Layer dependencies. Resolved transitively; topologically sorted. |
| `env` | `map[string]string` | Environment variables (`KEY: "value"`). Merged across layers, emitted as `ENV` directives. See [ENV from layer.yml](#env-from-layeryml). |
| `path_append` | `[]string` | Paths to append to `$PATH`. Accumulated across layers. |
| `ports` | `[]int` | Exposed ports (1-65535). Collected across layers, deduplicated, emitted as `EXPOSE` directives. |
| `route` | `{host: string, port: int}` | Traefik reverse proxy route. Generates dynamic traefik config. Requires traefik layer. |
| `service` | multiline string (`\|`) | Supervisord service fragment (`[program:<name>]`). Triggers supervisord assembly in images. |
| `rpm` | `RpmConfig` | RPM package config. See [System Packages](#system-packages-rpmdeb). |
| `deb` | `DebConfig` | Debian package config. See [System Packages](#system-packages-rpmdeb). |
| `volumes` | `[]VolumeYAML` | Persistent named volumes. Each entry has `name` + `path` fields. `~`/`$HOME` expanded. See [Volume Management](#volume-management). |
| `aliases` | `[]AliasYAML` | Host command aliases. Each entry has `name` + `command` fields. See [Command Aliases](#command-aliases). |

**`rpm` section fields:**

| Field | Type | Purpose |
|---|---|---|
| `packages` | `[]string` | Package names to install via `dnf install` |
| `copr` | `[]string` | COPR repos (`owner/project`). Enabled before install, disabled after. |
| `repos` | `[]RpmRepo` | External repos with `name`, `url`, `gpgkey` fields. Added disabled, enabled per-install. |
| `exclude` | `[]string` | `--exclude` patterns passed to dnf |
| `options` | `[]string` | Extra dnf flags (e.g. `--setopt=tsflags=noscripts`) |

**`deb` section fields:**

| Field | Type | Purpose |
|---|---|---|
| `packages` | `[]string` | Package names to install via `apt-get install` |

### Root vs User Rule

System packages in `layer.yml` and `root.yml` run as root. Everything else (`pixi.toml`, `package.json`, `Cargo.toml`, `user.yml`) runs as user. pixi, npm, and cargo must never run as root.

### Layer Dependencies

Layers declare dependencies via the `depends` field in `layer.yml`. The generator resolves transitively, topologically sorts, and pulls in missing dependencies automatically. Circular dependencies are a validation error. Layers already installed by a parent image (via `base` chain) are skipped.

---

## Image Definition

An **image** is a named build target in `images.yml`. Example configuration:

```yaml
defaults:
  registry: ghcr.io/overthinkos
  tag: auto
  platforms:
    - linux/amd64
    - linux/arm64
  pkg: rpm
  merge:
    auto: false
    max_mb: 128
  builder: fedora-builder

images:
  fedora:
    base: "quay.io/fedora/fedora:43"
    pkg: rpm

  fedora-builder:
    base: fedora
    layers:
      - pixi
      - nodejs
      - build-toolchain

  nvidia:
    base: fedora
    layers:
      - cuda
    platforms:
      - linux/amd64

  openclaw-sway-browser:
    base: fedora
    layers:
      - openclaw
      - pipewire
      - wayvnc
      - google-chrome-sway
      - pcmanfm-qt
      - quickshell
    ports:
      - "18789:18789"
      - "5900:5900"
      - "9222:9222"
    platforms:
      - linux/amd64

  bazzite-ai:
    enabled: false
    base: "ghcr.io/ublue-os/bazzite-nvidia-open:stable-43.20260120.1"
    bootc: true
    platforms:
      - linux/amd64
    layers:
      - build-toolchain
      - language-runtimes
      - dev-tools
      - cuda
      - desktop-apps
      - os-config
      - os-system-files
```

### Inheritance Chain

Every setting resolves through: **image -> defaults -> hardcoded fallback** (first non-null wins).

| Field | Default | Description |
|---|---|---|
| `enabled` | `true` | Set to `false` to disable (skipped by generate, validate, list) |
| `base` | `quay.io/fedora/fedora:43` | External OCI image or name of another image in `images.yml` |
| `bootc` | `false` | Adds `bootc container lint` and enables disk image builds |
| `platforms` | `["linux/amd64", "linux/arm64"]` | Target architectures |
| `tag` | `"auto"` | Image tag. `"auto"` for CalVer. |
| `registry` | `""` | Container registry prefix |
| `pkg` | `"rpm"` | System package manager: `"rpm"` or `"deb"` |
| `layers` | (required) | Layer list (image-specific, not inherited) |
| `ports` | `[]` | Runtime port mappings (`"host:container"` or `"port"`). Used by `ov shell` for `-p` flags. |
| `user` | `"user"` | Username for non-root operations |
| `uid` | `1000` | User ID |
| `gid` | `1000` | Group ID |
| `merge` | `null` | Layer merge settings (`auto: true, max_mb: 128`). See [Layer Merging](#layer-merging). |
| `aliases` | `[]` | Command aliases (`name` + optional `command`). See [Command Aliases](#command-aliases). |
| `builder` | `""` | Builder image name (per-image, falls back to defaults). See [Builder Image](#builder-image). |
| `bind_mounts` | `[]` | Bind mount declarations (`name` + `host`/`path` + optional `encrypted`). Image-level only, not inherited. See [Bind Mounts](#bind-mounts). |

When `base` references another image in `images.yml`, the generator resolves it to the full registry/tag and creates a build dependency. The referenced image must be built first.

---

## Generated Containerfile Structure

The Containerfile emitted by `ov/generate.go:generateContainerfile()` follows this structure:

1. **Multi-stage build stages** -- scratch stages per layer (`COPY layers/<layer>/ /`), pixi build stages (`FROM <builder>`), npm build stages, supervisord config assembly stage, traefik routes stage. Pixi install varies by manifest: `pixi.toml` (`pixi install`), `pyproject.toml` (`--manifest-path`), `environment.yml` (`pixi project import` first).
2. **`FROM ${BASE_IMAGE}`** -- external bases get bootstrap (install `task`, create user/group at UID/GID, set `WORKDIR`); internal bases get `USER root`.
3. **Image metadata** -- consolidated `ENV` directives (from all layers' `env`/`path_append`), `EXPOSE` ports, `org.overthink.*` labels.
4. **COPY build artifacts** -- pixi environments, pixi binary, npm global packages from their respective build stages.
5. **Per-layer install steps** -- for each layer in order: rpm/deb packages, `root.yml`, `Cargo.toml`, `user.yml` (only files that exist). `USER` directives toggle between root and UID as needed.
6. **Final assembly** -- supervisord config concatenation (`cat /supervisor/*.conf`), traefik routes COPY, `USER <UID>`, `RUN bootc container lint` (bootc only).

Source: `ov/generate.go`.

---

## Image Labels

Built images embed runtime metadata as OCI `LABEL` directives (prefix: `org.overthink.`), making images self-describing for runtime commands (`ov shell`, `ov start`, `ov enable`, `ov alias install`).

| Label | Type | Example |
|---|---|---|
| `org.overthink.version` | string | `"1"` (schema version) |
| `org.overthink.image` | string | `"openclaw"` |
| `org.overthink.registry` | string | `"ghcr.io/overthinkos"` (omitted if empty) |
| `org.overthink.uid` / `.gid` | string | `"1000"` |
| `org.overthink.user` / `.home` | string | `"user"` / `"/home/user"` |
| `org.overthink.ports` | JSON | `["18789:18789"]` |
| `org.overthink.volumes` | JSON | `[{"name":"data","path":"/home/user/.openclaw"}]` |
| `org.overthink.aliases` | JSON | `[{"name":"openclaw","command":"openclaw"}]` |

Volumes use short names in labels (prefix `ov-<image>-` added at runtime). Empty arrays are omitted. JSON built from sorted slices for cache stability. Runtime commands try `LoadConfig` (images.yml) first, falling back to `<engine> inspect` labels -- enabling `ov shell myimage` from any directory.

Source: `ov/labels.go`, `ov/generate.go` (`writeLabels`).

---

## User Resolution

Configurable via `user`, `uid`, `gid` fields in `images.yml` (defaults: `"user"`, 1000, 1000).

For external base images, `ov` calls `registry.go:InspectImageUser()` which:
1. Pulls the base image via go-containerregistry
2. Extracts `/etc/passwd` from image layers (top layer first)
3. Finds user at configured UID
4. If found: overrides username, home directory, and GID (e.g., `ubuntu` with home `/home/ubuntu` for `ubuntu:24.04`)
5. If not found: uses configured defaults, bootstrap creates the user

For internal base images, user context is inherited from the parent image.

The bootstrap conditionally creates the user:
```dockerfile
RUN getent passwd <UID> >/dev/null 2>&1 || \
    (getent group <GID> >/dev/null 2>&1 || groupadd -g <GID> <user> && \
     useradd -m -u <UID> -g <GID> -s /bin/bash <user>)
```

---

## ENV from layer.yml

Layers declare environment variables in `layer.yml` using two fields:

```yaml
env:
  PIXI_CACHE_DIR: "~/.cache/pixi"
  RATTLER_CACHE_DIR: "~/.cache/rattler"

path_append:
  - "~/.pixi/bin"
  - "~/.pixi/envs/default/bin"
```

- `env` -- key-value map. Later layers override earlier for the same key.
- `path_append` -- list of paths appended to `$PATH`. Accumulated across layers.
- `~` and `$HOME` are expanded to the resolved home directory at generation time.
- Setting `PATH` directly in `env` is a validation error (use `path_append`).

Source: `ov/env.go`. The generator collects env configs from all layers via `writeLayerEnv()`, merges them (`MergeEnvConfigs`), expands paths (`ExpandEnvConfig`), and emits consolidated `ENV` directives.

---

## Package Managers

### System Packages (rpm/deb)

Controlled by the `pkg` field. Packages are declared in `layer.yml` under `rpm:` or `deb:` sections.

| `pkg` | Config section | Install command | Cache mount |
|---|---|---|---|
| `"rpm"` | `rpm.packages` | `dnf install -y` | `/var/cache/libdnf5` |
| `"deb"` | `deb.packages` | `apt-get update && apt-get install -y --no-install-recommends` | `/var/cache/apt` + `/var/lib/apt` |

**COPR repos** (`rpm.copr`): rpm-only. Each `owner/project` entry is enabled before install and disabled after. **External repos** (`rpm.repos`): added disabled via `dnf5 config-manager addrepo`, enabled per-install with `--enable-repo`. GPG keys imported if specified. **Excludes** (`rpm.exclude`): passed as `--exclude` patterns. **Options** (`rpm.options`): extra dnf flags like `--setopt=tsflags=noscripts`.

### Pixi (Python/Conda)

Multi-stage build: dedicated `FROM <builder>` build stage per layer, using the configured builder image. The builder has pixi, gcc, cmake, git pre-installed, so no `apt-get install` is needed. Environment installed to `<home>/.pixi/envs/default`, then `COPY`'d into the final image. Pixi binary also copied from the build stage. No rattler cache mount in the final image.

The `pixi` layer itself installs the pixi binary via `root.yml` (curl + tar download). It does **not** have a `pixi.toml` — it only provides the binary and env/path config.

Supported manifests: `pixi.toml`, `pyproject.toml`, `environment.yml` (checked in that priority order by `layers.go:PixiManifest()`). Only one per layer.

Rules: never `pip install`, `conda install`, or `dnf install python3-*`. Pixi is the only Python package manager.

### npm

Multi-stage build: dedicated `FROM <builder>` build stage per npm layer, using the configured builder image. The builder has node, npm, git pre-installed. Packages from `package.json` `dependencies` are installed globally via `npm install -g` into `/npm-global`, then `COPY`'d into the final image at `<home>/.npm-global/`. Requires `nodejs` layer earlier in the image (for PATH/env setup).

### Cargo

`cargo install --path /ctx` from bind-mounted layer context. Binaries go to `<home>/.cargo/bin/`. Requires `rust` layer earlier (or `depends`).

---

## Cache Mounts

| Step type | Cache path | Options |
|---|---|---|
| `rpm.packages`, `root.yml` (rpm) | `/var/cache/libdnf5` | `sharing=locked` |
| `deb.packages`, `root.yml` (deb) | `/var/cache/apt` + `/var/lib/apt` | `sharing=locked` |
| `user.yml` | `<home>/.cache/npm` | `uid=<UID>,gid=<GID>` |
| `Cargo.toml` | `<home>/.cargo/registry` | `uid=<UID>,gid=<GID>` |

UID/GID in cache mounts are dynamic (from resolved image config, not hardcoded 1000). Pixi builds happen in separate stages; pixi/rattler cache dirs are set via `layer.yml` `env` fields, not cache mounts.

---

## Volume Management

Layers can declare persistent named volumes in `layer.yml`:

```yaml
volumes:
  - name: data
    path: "~/.openclaw"
```

- **Name**: lowercase alphanumeric with hyphens (`^[a-z0-9]+(-[a-z0-9]+)*$`). Must be unique within a layer.
- **Path**: container mount path. `~` and `$HOME` expanded to resolved home directory.
- **Naming convention**: Docker/podman volume names are `ov-<image>-<name>` (e.g. `ov-openclaw-data`).
- **Collection**: `CollectImageVolumes()` traverses the full image base chain (image -> base -> base's base), collecting volumes from all layers. Deduplicated by name (first declaration wins -- outermost image takes priority).
- **Integration**: volumes are automatically mounted by `ov shell`, `ov start`, and `ov enable` via `-v <volume>:<path>` flags.

Source: `ov/volumes.go` (collection, expansion), `ov/layers.go` (`VolumeYAML` struct, `HasVolumes`, `Volumes()`).

---

## Bind Mounts

Image-level host-path bind mounts, declared in `images.yml`. Two modes: plain (direct bind) and encrypted (gocryptfs-managed). Not inherited from defaults -- deployment-specific.

### Configuration

```yaml
images:
  myapp:
    layers: [nodejs, myapp]
    bind_mounts:
      - name: data
        host: "~/data/myapp"        # host dir, required for plain mounts
        path: "~/.myapp"            # container path, ~ expanded to resolved home

      - name: secrets
        path: "~/.myapp/secrets"    # container path
        encrypted: true             # gocryptfs, host managed by ov
```

**Rules:**
- `encrypted: false` (default): `host` is required -- direct bind mount
- `encrypted: true`: `host` is forbidden -- ov manages cipher/plain dirs at `encrypted_storage_path`
- `path`: container mount path (required). `~`/`$HOME` expanded to the image's resolved home dir
- `host`: host mount path (plain mounts only). `~`/`$HOME` expanded to the user's actual home
- `name`: unique identifier, same regex as volumes: `^[a-z0-9]+(-[a-z0-9]+)*$`
- Bind mount names must not collide with layer volume names (same namespace)

### Runtime Config

| Setting | Env | Default |
|---------|-----|---------|
| `encrypted_storage_path` | `OV_ENCRYPTED_STORAGE_PATH` | `~/.local/share/ov/encrypted` |

Encrypted storage layout:
```
~/.local/share/ov/encrypted/
  ov-myapp-secrets/
    cipher/     # gocryptfs encrypted data
    plain/      # FUSE mount point (bind-mounted into container)
```

### CLI Commands

```
ov crypto init <image> [--volume NAME]     # Initialize gocryptfs cipher dirs
ov crypto mount <image> [--volume NAME]    # Mount encrypted volumes (interactive password)
ov crypto unmount <image> [--volume NAME]  # Unmount encrypted volumes
ov crypto status <image>                   # Show status of all encrypted bind mounts
```

### Integration

- **`ov shell` / `ov start` (direct)**: resolves bind mounts, verifies plain dirs exist and encrypted volumes are mounted, appends `-v <host>:<container>` flags
- **`ov enable` (quadlet)**: plain mounts as `Volume=` lines; encrypted mounts generate a companion `ov-<image>-crypto.service` with `Requires=`/`After=` in the `.container` file
- **`ov remove`**: removes companion crypto service file alongside tunnel service file
- **`ov inspect --format bind_mounts`**: outputs `NAME\tHOST\tPATH\tENCRYPTED`

### Validation

- Name, path required; name must match volume name regex
- No duplicate names within an image
- Encrypted: host must be empty; plain: host required
- Names must not collide with layer volume names
- Warning if `gocryptfs` not in PATH (when encrypted mounts exist)

Source: `ov/crypto.go` (types, commands, resolution, crypto unit generation), `ov/validate.go` (`validateBindMounts`).

---

## Command Aliases

Distrobox-style wrapper scripts that let you run container commands transparently from the host. Type `openclaw` on the host and it runs inside the right container automatically.

### Declaration

Aliases can be declared in `layer.yml` and/or `images.yml`:

```yaml
# layers/openclaw/layer.yml
aliases:
  - name: openclaw
    command: openclaw

# images.yml (image-level, can override layer aliases)
images:
  openclaw:
    aliases:
      - name: openclaw        # command defaults to name if omitted
```

Layer aliases require both `name` and `command`. Image-level aliases default `command` to `name` if omitted. Image-level aliases override layer aliases with the same name.

### Wrapper Scripts

`ov alias add` or `ov alias install` writes shell scripts to `~/.local/bin/`:

```sh
#!/bin/sh
# ov-alias
# image: openclaw
# command: openclaw
_ov_q(){ printf "'"; printf '%s' "$1" | sed "s/'/'\\\\''/g"; printf "' "; }
c="openclaw"; for a in "$@"; do c="$c $(_ov_q "$a")"; done
exec ov shell openclaw -c "$c"
```

The `# ov-alias` marker enables safe list/delete scanning. `ov alias remove` verifies this marker before deleting (won't remove non-ov files).

The `_ov_q()` helper properly single-quotes each argument (handles spaces, quotes, special chars). POSIX sh compatible. Aliases always start an ephemeral container via `ov shell`.

### Collection

`CollectImageAliases()` gathers aliases from the image's own layers (in dependency order) plus image-level config. **No base chain traversal** — aliases are leaf-image specific (unlike volumes). Layer aliases come first; image-level overrides by name.

### Validation Rules

- Alias name must match `^[a-zA-Z0-9][a-zA-Z0-9._-]*$` (valid filename)
- Layer aliases require both `name` and `command`
- Image-level `command` is optional (defaults to `name`)
- No duplicate alias names within a layer or within an image

Source: `ov/alias.go` (wrapper gen, collection, CLI commands), `ov/layers.go` (`AliasYAML`, `HasAliases`, `Aliases()`), `ov/config.go` (`AliasConfig`).

---

## GPU Passthrough

`ov shell`, `ov start`, and `ov enable` support GPU passthrough via `--gpu` / `--no-gpu` flags.

| Mode | Behavior |
|---|---|
| `--gpu` | Force GPU passthrough |
| `--no-gpu` | Disable GPU passthrough |
| (neither) | Auto-detect via `nvidia-smi` |

- **Docker** (`engine.run=docker`): passes `--gpus all`
- **Podman** (`engine.run=podman`): passes `--device nvidia.com/gpu=all`
- **Podman quadlet** (`ov enable`): adds `AddDevice=nvidia.com/gpu=all` to the `.container` file

Source: `ov/gpu.go` (detection), `ov/engine.go` (engine-specific args). `GPUFlags` struct is embedded in `ShellCmd`, `StartCmd`, and `EnableCmd`.

---

## Cross-Engine Image Transfer

When `engine.build` and `engine.run` differ (e.g., build with Docker, run with Podman), images built by one engine aren't available in the other's store. `ov` automatically transfers images between engines on demand.

| Function | Purpose |
|---|---|
| `LocalImageExists(engine, imageRef)` | Check if image exists in an engine's local store. Docker: `docker image inspect`. Podman: `podman image exists`. Package-level var for testability. |
| `TransferImage(srcEngine, dstEngine, imageRef)` | Bidirectional pipe: `<src> save <ref> \| <dst> load`. Logs to stderr. |
| `EnsureImage(imageRef, rt)` | 1. Image in run engine? Return (no-op). 2. Same engine, missing? Error with "build it first". 3. Missing from both? Error naming both engines. 4. Otherwise: transfer from build engine to run engine. |

Transfer is called automatically by `ov shell`, `ov start`, `ov enable`, and `ov update` before running containers.

Source: `ov/transfer.go`.

---

## Versioning

CalVer in semver format: `YYYY.DDD.HHMM` (year, day-of-year, UTC time). Computed once per `ov generate` invocation. Source: `ov/version.go`.

| `tag` value | Generated tag(s) | Example |
|---|---|---|
| `"auto"` | `YYYY.DDD.HHMM` + `latest` | `ghcr.io/overthinkos/fedora:2026.46.1415`, `...:latest` |
| `"nightly"` | `nightly` only | No `latest` alias |
| `"1.2.3"` | `1.2.3` only | Pinned release |

Override: `ov generate --tag <value>` replaces all `"auto"` resolutions.

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
ov build [image...]                    # Build for local platform, load into engine store
ov build --push [image...]             # Build for all platforms and push to registry
ov build --platform linux/amd64 [image...]  # Specific platform
ov build --cache registry|gha [image...]   # Enable build cache (registry or GitHub Actions)
ov merge <image> [--max-mb N] [--tag TAG] [--dry-run]
                                       # Merge small layers in a built image
ov merge --all [--dry-run]             # Merge all images with merge.auto enabled
ov new layer <name>                    # Scaffold a layer directory
ov shell <image> [-w PATH] [-c CMD] [--tag TAG] [--gpu|--no-gpu]
                                       # Bash shell in a container (mounts cwd at /workspace)
ov start <image> [-w PATH] [--tag TAG] [--gpu|--no-gpu]
                                       # Start service container (direct or quadlet per run_mode)
                                       # Quadlet: auto-enables if auto_enable=true, else requires ov enable first
ov stop <image>                        # Stop a running service container
ov enable <image> [-w PATH] [--tag TAG] [--gpu|--no-gpu]
                                       # Generate quadlet .container file, daemon-reload (quadlet only)
                                       # Auto-transfers image from Docker if build engine is docker
ov disable <image>                     # Disable service auto-start (quadlet only)
ov status <image>                      # Show service status (quadlet: systemctl, direct: engine inspect)
ov logs <image> [-f]                   # Show service logs (quadlet: journalctl, direct: engine logs)
ov update <image> [--tag TAG]          # Update image, restart if active (quadlet) or print message (direct)
ov remove <image>                      # Remove service (quadlet: delete .container, direct: stop + rm)
ov config get <key>                    # Print resolved value
ov config set <key> <value>            # Set in user config
ov config list                         # Show all settings with source
ov config reset [key]                  # Remove from user config (revert to default)
ov crypto init <image> [--volume NAME]  # Initialize gocryptfs cipher directories
ov crypto mount <image> [--volume NAME] # Mount encrypted volumes
ov crypto unmount <image> [--volume NAME] # Unmount encrypted volumes
ov crypto status <image>               # Show status of encrypted bind mounts
ov config path                         # Print config file path
ov version                             # Print computed CalVer tag
```

**Output conventions:** `generate`/`validate`/`new`/`merge` write to stderr. `inspect`/`list`/`version` write to stdout (pipeable). `inspect --format <field>` outputs bare value for shell substitution (`tag`, `base`, `builder`, `pkg`, `registry`, `platforms`, `layers`, `ports`, `volumes`, `aliases`, `bind_mounts`).

**Error handling:** validation collects all errors at once. Exit codes: 0 = success, 1 = validation/user error, 2 = internal error. All validation rules are in `ov/validate.go`.

---

## Directory Structure

```
project/
+-- bin/ov                              # Built by `task build:ov` (gitignored)
+-- ov/                                 # Go module (go 1.25.6)
|   +-- go.mod                          # kong v1.14.0, go-containerregistry v0.20.7
|   +-- main.go                         # CLI (Kong)
|   +-- config.go                       # images.yml parsing, inheritance resolution
|   +-- labels.go                       # OCI label constants, ImageMetadata, ExtractMetadata
|   +-- layers.go                       # Layer scanning, file detection
|   +-- env.go                          # env config merging, path expansion
|   +-- graph.go                        # Topological sort (layers + images)
|   +-- intermediates.go               # Auto-intermediate image computation (trie analysis)
|   +-- generate.go                     # Containerfile generation
|   +-- validate.go                     # All validation rules
|   +-- version.go                      # CalVer computation
|   +-- scaffold.go                     # `new layer` scaffolding
|   +-- build.go                        # `build` command (sequential image building)
|   +-- merge.go                        # `merge` command (post-build layer merging)
|   +-- registry.go                     # Remote image inspection (go-containerregistry)
|   +-- runtime_config.go              # Runtime config (~/.config/ov/config.yml)
|   +-- engine.go                       # Engine abstraction (docker/podman)
|   +-- shell.go                        # `shell` command (execs engine run)
|   +-- start.go                        # `start`/`stop` commands (engine run -d)
|   +-- commands.go                     # `enable`/`disable`/`status`/`logs`/`update`/`remove` commands
|   +-- quadlet.go                      # Quadlet .container file generation + helpers
|   +-- gpu.go                          # GPU auto-detection + passthrough flags
|   +-- transfer.go                     # Cross-engine image transfer (LocalImageExists, TransferImage, EnsureImage)
|   +-- volumes.go                      # Named volume collection + mounting
|   +-- alias.go                        # Command aliases (wrapper scripts, collection, CLI commands)
|   +-- crypto.go                       # Bind mounts (plain + gocryptfs encrypted), crypto CLI commands
|   +-- *_test.go                       # Tests for each file
+-- .build/                             # Generated (gitignored)
|   +-- <image>/Containerfile
|   +-- <image>/supervisor/*.conf        # Supervisord configs (from layer.yml service)
+-- images.yml                          # Configuration
+-- Taskfile.yml                        # Root: includes + PATH setup
+-- taskfiles/
|   +-- Build.yml                       # ov, all, local, push, merge, iso, qcow2, raw
|   +-- Run.yml                         # container, shell, enable, disable, start, stop, status, logs, update, remove, alias-install, alias-uninstall, vm
|   +-- Setup.yml                       # builder, all
+-- layers/<name>/                      # Layer directories (see Layer Definition)
+-- templates/
|   +-- supervisord.header.conf
+-- config/
    +-- disk.toml                       # QCOW2/RAW disk layout
    +-- iso-gnome.toml                  # ISO installer config (GNOME/Anaconda)
```

---

## Shipped Layers (44 total)

**Foundation:** `pixi` (pixi binary + env/PATH), `nodejs` (Node.js + npm via rpm/deb), `rust` (Rust + Cargo via rpm/deb), `python` (Python 3.13 via pixi), `language-runtimes` (Go, PHP, .NET, nodejs-devel, python3-devel)

**Build:** `build-toolchain` (gcc, cmake, autoconf, ninja, git, pkg-config), `pre-commit` (git hooks framework)

**Services:** `supervisord` (process manager via pixi; depends: python), `traefik` (reverse proxy on :8000/:8080; depends: supervisord), `testapi` (FastAPI test service on :9090, routed via `testapi.localhost`)

**Desktop/Wayland:** `sway` (Sway compositor + dbus), `cage` (kiosk-mode headless Wayland), `niri` (Niri compositor; depends: cage), `quickshell` (bar/launcher via COPR; depends: sway), `pcmanfm-qt` (file manager; depends: sway)

**Display/Audio:** `wayvnc` (VNC server on :5900), `pipewire` (audio/media server + wireplumber)

**Browser:** `google-chrome` (Chrome on niri, DevTools :9222, volume: chrome-data), `google-chrome-sway` (Chrome on sway, same ports/volume)

**GPU/ML:** `cuda` (CUDA toolkit + cuDNN + onnxruntime), `python-ml` (ML Python env; depends: cuda), `jupyter` (Jupyter + ML libs on :8888; depends: cuda, supervisord), `ollama` (LLM server on :11434; depends: cuda, supervisord; volume: models; alias: ollama), `comfyui` (image generation on :8188; depends: cuda, supervisord; volume: comfyui)

**Applications:** `openclaw` (AI gateway on :18789 via npm; depends: nodejs, supervisord; volume: data; alias: openclaw), `claude-code` (Claude Code CLI; depends: nodejs)

**DevOps/CI:** `docker-ce` (Docker CE + buildx + compose), `kubernetes` (kubectl + Helm), `devops-tools` (bind-utils, jq, rsync; depends: nodejs), `github-runner` (Actions runner as service; uid: 0), `github-actions` (Act CLI via COPR + guestfs), `google-cloud` (Google Cloud SDK), `google-cloud-npm` (GCP npm packages; depends: google-cloud, nodejs), `grafana-tools` (Grafana tooling)

**Dev Tools:** `dev-tools` (bat, ripgrep, neovim, gh, direnv, fd-find, htop, podman-compose), `vscode` (VS Code via Microsoft repo), `pre-commit` (git hooks), `typst` (document processor), `ujust` (task runner)

**Desktop Apps:** `desktop-apps` (Chromium, VLC, KeePassXC, btop, cockpit, zsh), `copr-desktop` (COPR desktop packages), `vr-streaming` (OpenXR, OpenVR, GStreamer), `virtualization` (QEMU/KVM/libvirt stack)

**OS (bootc):** `os-config` (OS configuration), `os-system-files` (system files/configs)

---

## Task Commands

Task commands are thin wrappers around `ov` CLI commands. Run `task -l` for the full list. Key commands: `task setup:all` (build ov + create builder), `task build:all` (generate + build + merge), `task build:local -- <image>`, `task build:push`, `task run:shell -- <image>`, `task run:enable -- <image>`. Disk image tasks: `task build:iso`, `task build:qcow2`, `task build:raw`, `task run:vm`.

Direct `ov` commands (`ov list images`, `ov validate`, etc.) don't need `task`.

---

## Workflows

**Add a layer:** `ov new layer <name>` -> edit `layer.yml` (rpm/deb packages, depends, env, ports, route, service) -> add install files -> add to an image in `images.yml` -> `task build:local -- <image>`

**Add an image:** add entry to `images.yml` -> `task build:local -- <image>`

**Layer images:** set `base` to another image name in `images.yml`. The generator handles dependency ordering and tag resolution.

**Host bootstrap (first time):** requires `task`, `go`, `docker` (or `podman`). Run `task setup:all` then `task build:all`. To use podman: `ov config set engine.build podman`.

---

## Style Guide

### General Rules
- Lowercase-hyphenated names for layers and images
- No shell scripts -- Taskfiles for automation, Go for logic
- No Docker layer cleanup -- cache mounts handle it
- No cosign -- image signing is external to this build system
- `.build/` is disposable; all generated files start with `# <path> (generated -- do not edit)`

### Layer Taskfiles (root.yml / user.yml)
- Single task: `install`. No other tasks, no parameters. Idempotent.
- `root.yml`: binary downloads, post-install system config. Never `dnf clean all`.
- `user.yml`: post-install config, workspace setup. Never `sudo`.
- System packages, repos, COPR repos belong in `layer.yml` `rpm:`/`deb:` sections. Python in `pixi.toml`. npm in `package.json`. Rust in `Cargo.toml`.
- Binary downloads: detect arch with `uname -m`, map via `case`, fail on unsupported.

### Containerfile Conventions
- Pixi builds in multi-stage before `FROM base`; per-layer steps handle everything else
- `USER <UID>` (numeric) not `USER <name>` -- emitted by generator
- No conditionals in generated Containerfiles

### Task Conventions
- Tasks are thin wrappers. Complex logic belongs in `ov`.
- Every public task has `desc:`. Arguments via `{{.CLI_ARGS}}`.
- Preconditions check for required tools.

---

## Layer Merging

Post-build optimization: `ov merge` takes an already-built image, inspects Docker layer sizes, and merges consecutive small layers into fewer larger ones. No rebuild needed.

### Configuration

Add `merge` to `images.yml` defaults or per-image:

```yaml
defaults:
  merge:
    auto: true
    max_mb: 128
```

- **`auto`**: Enable automatic merging after builds via `ov merge --all` (default: false)
- **`max_mb`**: Maximum size of a merged layer (MB) (default: 128)

CLI flag `--max-mb` overrides `images.yml`. The `auto` field is only used by `ov merge --all` to select which images to merge; `ov merge <image>` always merges regardless.

### Algorithm

1. Load image from engine via `<engine> save` -> `tarball.ImageFromPath()`
2. Get compressed sizes via `layer.Size()`
3. Group consecutive layers into groups totaling <= `max_mb`
4. Single-layer "groups" are kept as-is (need 2+ layers to merge)
5. For each merge group: read uncompressed tarballs, deduplicate entries by path (last writer wins), write combined tar into a single new layer
6. Reconstruct image with `mutate.Append()`, preserving OCI history alignment (empty-layer entries for ENV/USER/EXPOSE kept in correct positions)
7. Save via `tarball.WriteToFile()` -> `<engine> load`

Source: `ov/merge.go`. Uses the configured build engine (`engine.build` from `ov config`) for save/load. No new Go dependencies -- uses `pkg/v1/tarball`, `pkg/v1/mutate`, `pkg/v1/empty` from go-containerregistry.

### Usage

```
# Preview what would be merged
ov merge fedora --dry-run

# Merge a single image
ov merge fedora

# Merge all images with merge.auto enabled (used by build tasks)
ov merge --all

# Custom threshold
ov merge fedora --max-mb 512

# Specific tag
ov merge fedora --tag 2026.46.1415
```

When `merge.auto` is set in `images.yml` defaults, `ov build` automatically runs `ov merge --all` after building.

Merge is idempotent -- running again after merging shows all layers as `[keep]`.

---

## Bootc / Disk Images

Set `"bootc": true` on an image. The Containerfile ends with `RUN bootc container lint`. Package installation is identical to regular images.

Disk images (ISO, QCOW2, RAW) use Bootc Image Builder (BIB). Requires `podman` (rootful). Config files: `config/disk.toml` (QCOW2/RAW layout), `config/iso-gnome.toml` (ISO/Anaconda). The container image must be built first.

Commands: `task build:iso -- <image> [tag]`, `task build:qcow2 -- <image> [tag]`, `task build:raw -- <image> [tag]`, `task run:vm -- <image> [tag]`.

---

## Runtime Configuration

`ov config` manages per-machine settings stored in `~/.config/ov/config.yml`.

```yaml
engine:
  build: docker    # "docker" or "podman"
  run: docker      # "docker" or "podman"
run_mode: direct   # "direct" or "quadlet"
auto_enable: false # auto-enable quadlet on first ov start
bind_address: "127.0.0.1"  # port binding address
```

**Resolution chain:** env var (`OV_BUILD_ENGINE`, `OV_RUN_ENGINE`, `OV_RUN_MODE`, `OV_AUTO_ENABLE`, `OV_BIND_ADDRESS`) > config file > default.

| Setting | Values | Default | Purpose |
|---|---|---|---|
| `engine.build` | `docker`, `podman` | `docker` | Engine for `ov build` and `ov merge` |
| `engine.run` | `docker`, `podman` | `docker` | Engine for `ov shell` and `ov start` |
| `run_mode` | `direct`, `quadlet` | `direct` | How `ov start`/`ov stop` and other service commands dispatch |
| `auto_enable` | `true`, `false` | `false` | When `run_mode=quadlet`, auto-run `ov enable` on first `ov start` |
| `bind_address` | `127.0.0.1`, `0.0.0.0` | `127.0.0.1` | Address for port bindings in `ov shell`, `ov start`, and quadlet |
| `encrypted_storage_path` | path string | `~/.local/share/ov/encrypted` | Base directory for gocryptfs encrypted bind mount storage |

When `run_mode=quadlet`, `ov start` checks for an existing `.container` file. If none exists and `auto_enable=true`, it auto-enables (generates the quadlet file). If `auto_enable=false`, it errors with a message to run `ov enable` first. `ov stop` uses `systemctl --user stop`. This requires `engine.run=podman` (a warning is emitted otherwise).

When `run_mode=direct`, `ov start`/`ov stop` use `<engine> run -d`/`<engine> stop`. Commands like `ov status`, `ov logs`, and `ov remove` work in both modes. `ov enable` and `ov disable` are quadlet-only.

Source: `ov/runtime_config.go` (config struct, load/save/resolve), `ov/engine.go` (engine binary names, GPU args).

---

## Tunnel Configuration

Images can expose services outside the container host via tunnels. Three modes are supported:

### Tailscale Serve (tailnet-private, default)

Exposes a port to your Tailscale network only. All tailnet nodes can access it via MagicDNS (`https://<machine>.tailnet.ts.net`). No FQDN or ACME email needed -- Tailscale handles TLS certificates automatically.

```yaml
# Bare string (serve mode with default https=443)
tunnel: tailscale

# Expanded form
tunnel:
  provider: tailscale
  port: 2283          # container port to proxy to
  https: 443          # external HTTPS port (default: 443)
```

Allowed HTTPS ports for serve: 80, 443, 3000-10000, 4443, 5432, 6443, 8443.

CLI: `tailscale serve --bg --https=PORT localhost:PORT` / `tailscale serve --https=PORT off`.

### Tailscale Funnel (public internet)

Exposes a port to the public internet via Tailscale's edge network. Requires `funnel: true`.

```yaml
tunnel:
  provider: tailscale
  funnel: true
  port: 8080
  https: 443          # must be 443, 8443, or 10000
```

CLI: `tailscale funnel --bg --https=PORT localhost:PORT` / `tailscale funnel PORT off`.

### Cloudflare Tunnel

Routes traffic through Cloudflare's network. Requires `fqdn` and optionally `acme_email`.

```yaml
tunnel:
  provider: cloudflare
  port: 3001
  tunnel: my-tunnel   # optional, defaults to ov-<image>
fqdn: "app.example.com"
```

### Resolution

`tunnel` inherits from defaults (image -> defaults -> nil). The `port` field defaults to the first route port from layers if not specified. For tailscale, `https` defaults to 443.

Source: `ov/tunnel.go` (TunnelYAML, TunnelConfig, start/stop dispatch), `ov/validate.go` (`validateTunnel`), `ov/quadlet.go` (systemd integration).

---

## Building Images

`ov build` generates Containerfiles and builds images sequentially in dependency order using the configured build engine.

```
ov build [image...]                    # Build for local platform
ov build --push [image...]             # Build for all platforms and push
ov build --platform linux/amd64 [image...]  # Specific platform
ov build --cache registry|gha [image...]   # Enable build cache
```

**Build cache** (`--cache`, env: `OV_BUILD_CACHE`): `registry` uses `<registry>/cache:<image>` as cache backend (`--cache-from`/`--cache-to type=registry`). `gha` uses GitHub Actions cache scoped by image name. Requires registry to be configured for `registry` mode.

**Flow:**
1. Run `ov generate` internally (produces Containerfiles)
2. Resolve runtime config to get build engine (`engine.build`)
3. Get image build order from `ResolveImageOrder()`
4. Filter to requested images (and their base dependencies)
5. For each image: `<engine> build -f .build/<image>/Containerfile -t <tags> --platform <platform> .`
6. After all builds: `ov merge --all` (if merge.auto enabled, skipped for `--push`)

**Internal base images** use exact CalVer tags in Containerfiles (`FROM ghcr.io/overthinkos/fedora:2026.46.1415`). This ensures each image references the precise version of its parent. Both Docker and Podman resolve local images before pulling from registry.

**Push mode** uses `docker buildx build --push` (Docker) or `podman build --manifest` + `podman manifest push` (Podman) for multi-platform builds.

Source: `ov/build.go`.

---

## Builder Image

A dedicated image used as the base for all pixi, npm, and cargo multi-stage build stages. Replaces the previous approach of pulling external images and installing build dependencies on every build.

### Configuration

Set `builder` in `images.yml` defaults (applies to all images) or per-image (overrides defaults):

```yaml
defaults:
  builder: fedora-builder    # default builder for all images

images:
  fedora-builder:
    base: fedora
    layers:
      - pixi            # pixi binary (via root.yml) + env vars/PATH
      - nodejs          # node + npm (via dnf)
      - build-toolchain # gcc, cmake, make, git (via dnf)

  special-app:
    builder: special-builder  # per-image override
    layers:
      - mylib
```

Resolution chain: **image.builder -> defaults.builder -> ""** (first non-empty wins). The builder image itself inherits `defaults.builder` but the generator recognizes self-reference and skips it.

The builder image itself has **no pixi build stages** (the pixi layer has no pixi.toml) and **no npm build stages** (none of its layers have package.json). It's a straightforward image: bootstrap + system packages + pixi binary download.

### How it works

All pixi/npm/cargo build stages in derived images use `FROM <builder>:<tag>` instead of external images:

```dockerfile
FROM ghcr.io/overthinkos/fedora-builder:2026.48.1808 AS supervisord-pixi-build
WORKDIR /home/user
COPY layers/supervisord/pixi.toml pixi.toml
RUN pixi install
```

No `apt-get install` is needed in build stages since the builder has pixi, node, npm, gcc, cmake, git pre-installed. The builder itself is excluded from using builder stages (to avoid circular dependency).

### Build ordering

Builder dependency is **conditional**: `ImageNeedsBuilder()` checks whether an image's own layers (excluding parent-provided) have pixi manifests, `package.json`, or `Cargo.toml`. Images that only install system packages or binaries build in parallel with the builder, not blocked waiting for it.

Source: `ov/generate.go` (`builderRefForImage`), `ov/graph.go` (`ResolveImageOrder`, `ImageNeedsBuilder`), `ov/validate.go` (`validateBuilder`).

---

## Intermediate Images

When multiple images share the same base and a common prefix of layers, `ov` auto-generates **intermediate images** at branch points to maximize Docker layer cache reuse.

### How it works

`ComputeIntermediates()` runs during generation:
1. `GlobalLayerOrder()` computes a deterministic layer ordering across all images, prioritizing layers by popularity (how many images need them) for cache efficiency.
2. Images are grouped by their direct parent (base). For each sibling group with 2+ images, a **prefix trie** is built from their relative layer sequences.
3. The trie is walked to detect branch points (where sibling layer sequences diverge). At each branch, an auto-intermediate image is created (e.g., `fedora-supervisord` if multiple images fork after the `supervisord` layer).
4. Original images are rebased to the nearest intermediate, so shared layers are built once.

### Example

```
fedora (external)
  -> fedora-supervisord (auto: pixi + python + supervisord)
     -> fedora-test (adds: traefik, testapi)
     -> openclaw (adds: nodejs, openclaw)
```

Without intermediates, both `fedora-test` and `openclaw` would independently install pixi, python, and supervisord. With intermediates, those layers are built once in `fedora-supervisord` and cached.

Auto-intermediate images are marked with `Auto: true` and appear in `ov list targets`. They are not user-defined in `images.yml`.

Source: `ov/intermediates.go` (`ComputeIntermediates`, `GlobalLayerOrder`, `walkTrieScoped`).

---

## Quadlet Services

`ov enable` manages containers as systemd user services via podman quadlet `.container` files. Unlike direct mode (`<engine> run -d`), quadlet services auto-restart on failure, integrate with `journalctl`, and can start at boot via `WantedBy=default.target`.

User-level quadlets only (`~/.config/containers/systemd/`). No root required. Requires `podman` and a systemd user session (`loginctl enable-linger <user>`).

### Image Transfer

Docker and podman have separate image stores. When `engine.build=docker`, images built with `ov build` exist only in Docker's store. `ov enable` automatically detects if the image is missing from podman and transfers it via `docker save | podman load`. When `engine.build=podman`, the image is already in podman's store and no transfer is needed. `ov update` re-transfers (if needed) and restarts the service if active.

### Workflow

```
ov config set run_mode quadlet
ov config set engine.run podman

ov enable fedora-test -w ~/project        # Generate .container file, transfer image, daemon-reload
ov start fedora-test                       # systemctl --user start
ov status fedora-test                      # systemctl --user status
ov logs fedora-test -f                     # journalctl --user -u (follow)
ov update fedora-test                      # Re-transfer image after rebuild, restart service
ov stop fedora-test                        # systemctl --user stop
ov disable fedora-test                     # Disable auto-start
ov remove fedora-test                      # Stop + remove .container file, daemon-reload
```

With `auto_enable=true`, `ov start` auto-generates the quadlet file on first run, so `ov enable` can be skipped.

### Generated File

`ov enable` writes `~/.config/containers/systemd/ov-<image>.container`. The systemd service name is `ov-<image>.service`. Container name is `ov-<image>` (same as direct mode). Ports are bound to the configured `bind_address`. The container runs `supervisord -n -c /etc/supervisord.conf` as its entrypoint.

Source: `ov/quadlet.go` (generation), `ov/commands.go` (command structs).
