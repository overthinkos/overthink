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
- `.build/_layers/<name>` -- symlinks to remote module layer directories (only when remote layers used)

Generation is idempotent. `.build/` is disposable and gitignored.

---

## Directory Structure

```
project/
+-- bin/ov                    # Built by `task build:ov` (gitignored)
+-- ov/                       # Go module (go 1.25.6, kong CLI, go-containerregistry)
+-- .build/                   # Generated (gitignored)
+-- images.yml                # Image definitions
+-- layers.mod                # Remote module dependencies (optional)
+-- layers.lock               # Locked module versions (generated, checked in)
+-- Taskfile.yml              # Root: includes + PATH setup
+-- taskfiles/                # Build.yml, Run.yml, Setup.yml
+-- layers/<name>/            # Layer directories
+-- templates/                # supervisord.header.conf
+-- config/                   # disk.toml, iso-gnome.toml
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
ov build [image...]                    # Build for local platform, load into engine store
ov build --push [image...]             # Build for all platforms and push to registry
ov build --platform linux/amd64 [image...]  # Specific platform
ov build --cache registry|gha [image...]   # Enable build cache
ov merge <image> [--max-mb N] [--tag TAG] [--dry-run]
ov merge --all [--dry-run]             # Merge all images with merge.auto enabled
ov mod init                            # Create layers.mod with module path from git remote
ov mod get <module>@<version>          # Add/update a require entry, download, update lock
ov mod remove <module>                 # Remove from layers.mod and layers.lock
ov mod download                        # Download all required modules to cache
ov mod tidy                            # Remove unused requires, add missing ones
ov mod verify                          # Verify cached modules against layers.lock hashes
ov mod update [module]                 # Update to latest version
ov mod list                            # List modules with versions and their layers
ov new layer <name>                    # Scaffold a layer directory
ov shell <image> [-w PATH] [-c CMD] [--tag TAG] [--gpu|--no-gpu]
ov start <image> [-w PATH] [--tag TAG] [--gpu|--no-gpu]
ov stop <image>                        # Stop a running service container
ov enable <image> [-w PATH] [--tag TAG] [--gpu|--no-gpu]
ov disable <image>                     # Disable service auto-start (quadlet only)
ov status <image>                      # Show service status
ov logs <image> [-f]                   # Show service logs
ov update <image> [--tag TAG]          # Update image, restart if active
ov remove <image>                      # Remove service
ov config get <key>                    # Print resolved value
ov config set <key> <value>            # Set in user config
ov config list                         # Show all settings with source
ov config reset [key]                  # Remove from user config
ov crypto init <image> [--volume NAME]
ov crypto mount <image> [--volume NAME]
ov crypto unmount <image> [--volume NAME]
ov crypto status <image>
ov config path                         # Print config file path
ov version                             # Print computed CalVer tag
```

**Output conventions:** `generate`/`validate`/`new`/`merge` write to stderr. `inspect`/`list`/`version` write to stdout (pipeable). `inspect --format <field>` outputs bare value for shell substitution (`tag`, `base`, `builder`, `pkg`, `registry`, `platforms`, `layers`, `ports`, `volumes`, `aliases`, `bind_mounts`).

**Error handling:** validation collects all errors at once. Exit codes: 0 = success, 1 = validation/user error, 2 = internal error.

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

## Style Guide

- Lowercase-hyphenated names for layers and images
- No shell scripts -- Taskfiles for automation, Go for logic
- No Docker layer cleanup -- cache mounts handle it
- `.build/` is disposable; all generated files start with `# <path> (generated -- do not edit)`
- Layer Taskfiles (`root.yml`/`user.yml`): single `install` task, no parameters, idempotent
- System packages in `layer.yml` `rpm:`/`deb:` sections. Python in `pixi.toml`. npm in `package.json`. Rust in `Cargo.toml`
- Never `pip install`, `conda install`, or `dnf install python3-*`. Pixi is the only Python package manager
- Binary downloads: detect arch with `uname -m`, map via `case`, fail on unsupported
- `USER <UID>` (numeric) not `USER <name>` in generated Containerfiles
- Tasks are thin wrappers. Complex logic belongs in `ov`. Every public task has `desc:`

---

## Workflows

**Add a layer:** `ov new layer <name>` -> edit `layer.yml` -> add install files -> add to an image in `images.yml` -> `task build:local -- <image>`

**Add an image:** add entry to `images.yml` -> `task build:local -- <image>`

**Layer images:** set `base` to another image name in `images.yml`. The generator handles dependency ordering and tag resolution.

**Host bootstrap (first time):** requires `task`, `go`, `docker` (or `podman`). Run `task setup:all` then `task build:all`. To use podman: `ov config set engine.build podman`.

---

## Task Commands

Task commands are thin wrappers around `ov` CLI commands. Run `task -l` for the full list. Key commands: `task setup:all` (build ov + create builder), `task build:all` (generate + build + merge), `task build:local -- <image>`, `task build:push`, `task run:shell -- <image>`, `task run:enable -- <image>`. Disk image tasks: `task build:iso`, `task build:qcow2`, `task build:raw`, `task run:vm`.

Direct `ov` commands (`ov list images`, `ov validate`, etc.) don't need `task`.

---

## Skill Reference

For detailed documentation on specific topics, use the corresponding skill:

| Topic | Skill | Covers |
|-------|-------|--------|
| Layer authoring | `/overthink:layer` | layer.yml fields, install files, packages, deps, env, volumes, cache mounts |
| Image composition | `/overthink:image` | images.yml, inheritance chain, builder image, intermediates, versioning |
| Building images | `/overthink:build` | ov build, push mode, layer merging algorithm, build cache |
| Runtime operations | `/overthink:run` | ov shell, start/stop, GPU passthrough, aliases, runtime config, image transfer |
| Deployment | `/overthink:deploy` | Quadlet services, bind mounts, tunnels, deploy.yml, bootc disk images, encryption |
| Remote modules | `/overthink:module` | layers.mod, layers.lock, cache, cross-module deps |
| Validation | `/overthink:validate` | Layer rules, image rules, bind mount rules, tunnel rules |
| Go CLI development | `/overthink-dev:go` | Source code map, testing, adding commands |
| Containerfile generation | `/overthink-dev:generate` | Generated structure, multi-stage builds, labels, user resolution, cache mounts |
