# Overthink

**The container management experience for you and your AI.**

Building containers sounds simple — until you need CUDA drivers, a Wayland desktop inside a container, fine-grained device access for KVM without giving away root, or half a dozen services wired together with the right permissions. Overthink takes care of all of that. Describe what you need in a simple layer list, and `ov` composes it into optimized multi-stage container images — from an interactive dev shell to a running service to a systemd unit to a bootable VM. Works the same way whether you're at the keyboard or your AI agent is driving.

164 layers. 49 image definitions (41 enabled by default, as of 2026-04-20). 5 VM definitions (1 cloud_image + 4 bootc, in `vms.yml`). Docker and Podman. `linux/amd64`. Fedora, Debian, Ubuntu, and Arch Linux. One CLI: `ov`. Every layer, image, VM, and command has a dedicated skill — 275+ skills across 25 plugins organized into four use-case buckets (commands, kind, development, images). See `plugins/README.md` for the full skill index.

*The name comes from the German "überdenken" — to think something through carefully. Not quite the same as the English "overthink," but let's be honest: `ov` really is trying its best to overthink absolutely everything.*

## Why Overthink?

Containers are a great idea with rough edges. The basics work well enough, but real-world needs pile up fast: GPU passthrough with the right driver stack, containers that need `/dev/kvm` or virtualization access without blanket `--privileged`, multiple services managed together, encrypted volumes, VNC or browser-streamed desktops, device permissions that don't compromise your host. Each of these is solvable — but solving them all at once, reliably, across images, is where things get hard. And if you're working with an AI agent that needs to build and manage these containers too, the complexity compounds.

Overthink treats container images like composable building blocks. Each **layer** is a self-contained unit — its packages, environment variables, services, volumes, security declarations, and dependencies described in a simple `layer.yml`. An **image** is just a list of layers on top of a base. The `ov` CLI resolves the dependency graph, generates optimized Containerfiles with multi-stage builds and cache mounts, and builds everything in the right order — handling the hard parts so you (and your AI) don't have to.

Want a GPU-accelerated Jupyter notebook? That's `cuda` + `jupyter` — two layers, one image definition. Need to add Ollama for local LLMs? Add the `ollama` layer. Want a full AI workstation with a Wayland desktop, Chrome, VNC, and an AI gateway? Still just a list of layers under an `image:` entry in `overthink.yml`. Overthink handles the rest: dependency resolution, build ordering, supervisor configs, traefik routes, volume declarations, security mounts, and GPU passthrough.

### Rootless-first power-user images

The four "power-user" images that carry the full `ov` toolchain —
`fedora-coder`, `fedora-ov`, `arch-ov`, `githubrunner` — all run as
uid=1000 with passwordless sudo (via the `sshd` layer's
`/etc/sudoers.d/ov-user` drop-in). Four cross-distro "coder" images —
`/ov-selkies:fedora-coder`, `/ov-selkies:arch-coder`,
`/ov-selkies:debian-coder`, `/ov-selkies:ubuntu-coder` — share the
identical ~30 layers and 80-line test block, differing only in each
layer's package-format section (`rpm:` / `pac:` / `deb:`) and the
resolved uid-1000 user (`user` via create mode on fedora/arch/debian;
`ubuntu` via adopt mode on ubuntu-coder — see
[user_policy](#user-policy-adopt-vs-create) below). Rootless nested containers and
rootless libvirt VMs work with zero additive capabilities via the
surgical `unmask=/proc/*` security_opt from the `container-nesting`
layer. Historic `uid: 0` / `cap_add: [ALL]` postures were dropped in
2026-04 once the kernel-level RCA was complete. See
`/ov-distros:container-nesting` for the `mount_too_revealing()` RCA and
`/ov-selkies:fedora-coder` (32-layer kitchen sink) or
`/ov-selkies:selkies-desktop-ov` (streaming-desktop variant) for
canonical compositions.

### Sandboxed AI Desktops

One of Overthink's design goals is running sandboxed [OpenClaw](https://github.com/overthinkos/openclaw) systems. The approach flips the usual AI sandboxing model: instead of restricting what the AI agent can do, Overthink gives it full access to a complete desktop environment — Chrome, a Wayland compositor, development tools, network services — and sandboxes the entire desktop inside a container managed by `ov`. The AI agent operates freely within its environment while the host stays fully isolated. This is how images like `openclaw-sway-browser` and `openclaw-ollama-sway-browser` work: a full AI workstation with no host compromise.

`/ov-selkies:selkies-desktop-ov` takes this a step further: the sandboxed streaming desktop itself carries the full `ov` toolchain, so the AI (or the user) can build images, launch nested rootless pods, and create libvirt VMs from a terminal inside the browser-accessible desktop — all at uid 1000 with no `--privileged` and no added capabilities. The rootless-in-rootless recipe (kernel `mount_too_revealing()` RCA, surgical `unmask=/proc/*`, `virtqemud` session daemon) is documented in `/ov-distros:container-nesting` and `/ov-infrastructure:virtualization`.

### AI Agent Integration

Overthink includes the [Hermes Agent](https://github.com/NousResearch/hermes-agent) — a self-improving AI agent with voice, messaging, and tool-calling. Deploy it with a single command and it auto-configures its LLM provider from environment variables:

```bash
# Ollama Cloud (no local GPU needed)
ov config hermes -e OLLAMA_API_KEY=your-key
ov start hermes

# Or OpenRouter
ov config hermes -e OPENROUTER_API_KEY=sk-or-xxx

# Or local Ollama sidecar (auto-discovered via env_provides)
ov config ollama --update-all && ov start ollama
ov config hermes && ov start hermes
```

All providers whose keys are present get registered simultaneously — the priority order (`OLLAMA_HOST` > `OLLAMA_API_KEY` > `OPENROUTER_API_KEY`) only determines the default. Switch mid-session with `hermes chat --provider openrouter`. MCP servers from co-deployed services are auto-discovered too — deploy `jupyter` alongside `hermes` and hermes automatically connects to the jupyter MCP server (13 tools for notebook manipulation) via `OV_MCP_SERVERS` — no manual MCP configuration needed.

Deploy as separate pods for a full AI workstation: `selkies-desktop` (desktop Chrome at `:3000`), `hermes` (agent + AI CLIs + dev tools), and `jupyter` (notebooks at `:8888`). The chrome layer's `env_provides: BROWSER_CDP_URL` auto-injects `http://ov-selkies-desktop:9222` into the hermes quadlet. Hermes browser tools (`browser_navigate`, `browser_click`, `browser_snapshot`) control the desktop Chrome across the container network — the user watches hermes browse in real-time. A `cdp-proxy` in the chrome layer handles Chrome 146+ Host header validation for cross-container compatibility.

## Key Concepts

### Layers, Images, and Multi-Service Containers

A layer is a reusable building block — packages, config, services. An image is layers stacked on a base. The key insight: **you can combine multiple services into a single container image** just by listing layers. Need PostgreSQL, Redis, a Python API, and a reverse proxy in one container? Add those four layers to your image. `ov` resolves dependencies, generates an optimized Containerfile, and wires up the init system (supervisord for containers, systemd for bootc VMs) to run all services together when the container starts.

When services run as separate containers, **service discovery happens automatically**. A layer can declare `env_provides` — environment variables (with `{{.ContainerName}}` templates) that get injected into all other deployed containers at `ov config` time. For example, deploying `ollama` automatically provides `OLLAMA_HOST=http://ov-ollama:11434` to every other container — no manual environment setup needed. Similarly, `mcp_provides` declares MCP servers that get auto-discovered by consumers like Hermes — deploying `jupyter` automatically registers its MCP server (`http://{{.ContainerName}}:8888/mcp`) with any hermes instance, even when they run in the same container (pod-aware resolution to `localhost`). Layers can also declare `env_requires`/`mcp_requires` (mandatory) and `env_accepts`/`mcp_accepts` (optional) for documentation and deploy-time validation.

### The Unified YAML: `overthink.yml`

A single `overthink.yml` at the repo root is the project's entry point — everything else (distro / builder / init vocabulary, image definitions, layer definitions) is expressed as kind-keyed `build:` / `image:` / `layer:` entries in that file (or in files pulled in via `include:`). `ov` never reads `image.yml` directly any more; the loader (`LoadUnified`) resolves includes, auto-discovers layers via `discover:`, and fetches remote `@host/org/repo:version` refs into a transparent cache at `~/.cache/ov/repos/`.

```yaml
# overthink.yml
version: 1
includes:
  - build.yml                           # distro/builder/init vocabulary (kind: build)
  - image.yml                           # image definitions (kind: image)
  - vms.yml                             # VM definitions (kind: vm)
discover:
  - layers                              # scan layers/*/layer.yml for kind: layer
  - "@github.com/team/private/layers"   # remote repo (any ref form)
```

Each `layer.yml` uses a strict kind-keyed wrapper (`layer: {...}`); flat-form files are rejected at parse time. Projects predating this format convert in one shot with `ov migrate` — the command is idempotent and auto-invoked on remote-cache fetches so external repos pull through cleanly. See `/ov-build:migrate` and `/ov-image:layer`.

### Building Layers: Package Managers & Config Files

Each layer lives in its own directory under `layers/` and can use any combination of these files:

- **`layer.yml`** — The layer's manifest: system packages with tag-based dispatch (`rpm:` for Fedora/RHEL, `deb:` for Debian/Ubuntu, `pac:` for Arch Linux, `aur:` for AUR, plus distro/version tags like `fedora:`, `fedora:43:`), dependencies on other layers, environment variables, cross-container env injection (`env_provides`), MCP server discovery (`mcp_provides`), dependency declarations (`env_requires`/`env_accepts`, `mcp_requires`/`mcp_accepts`), ports, services, volumes, routes, metadata (`version`, `status`, `info`), layer-local build variables (`vars:` for `${VAR}` substitution), and the `task:` install list.
- **`task:` inside `layer.yml`** — Ordered install operations. Eight verbs: `cmd` (shell), `mkdir`, `copy` (layer-dir file → container), `write` (inline content → container — no shell heredoc), `link` (symlink), `download` (curl + extract, supports `strip_components: N` for tarballs nested under a top-level arch/version dir — new 2026-04), `setcap` (file capabilities), `build` (explicit pixi/npm/cargo placement). Each task carries a `user:` field (`root` / `${USER}` / literal username / `uid:gid`). Strict author-controlled ordering. YAML anchors + `${VAR}` substitution for DRY. See `/ov-image:layer` for the full verb catalog.
- **`pixi.toml`** / **`pyproject.toml`** / **`environment.yml`** — Python and conda packages via the Pixi package manager (multi-stage build, runs as user).
- **`package.json`** — npm packages for Node.js (multi-stage build, runs as user).
- **`Cargo.toml`** + **`src/`** — Rust crate compilation (multi-stage build, runs as user).

`ov` detects which files are present and generates the appropriate build stages automatically. You only include what you need — a layer with just `layer.yml` listing rpm packages is perfectly valid.

The vocabulary layers draw from — per-distro bootstrap commands, multi-stage builder templates (pixi/npm/cargo/aur), and init-system definitions (supervisord/systemd) — all lives in a `build:` entry (commonly split out to `build.yml` and pulled in via `overthink.yml includes:`). Three top-level sections (`distro:`, `builder:`, `init:`), one loader. See `/ov-build:build` for the full layout.

### Multi-Distro Support: `distro:` and `build:`

A single layer can target multiple distros. Two fields on each `image:` entry in `overthink.yml` control the behavior:

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

These fields flow through to `layer.yml`:
- **Package sections** — `distro:` tags are checked first (first match wins, prevents version conflicts). If no distro section matches, `build:` formats install ALL matching sections in order.
- **`task:`** — Not dispatched by tag. If a task must run on only one distro, guard it in-task: put a distro-specific package in the matching `rpm:`/`pac:` section, or add a shell `if [ -f /etc/fedora-release ]; then …; fi` inside a `cmd:` block.

This means `fedora-ov` and `arch-ov` share the exact same layer list — only the package sets (and rarely, a few shell-guarded tasks) differ per distro. The same applies to the four cross-distro coder images (`fedora-coder` / `arch-coder` / `debian-coder` / `ubuntu-coder`) which share ~30 layers and an identical 80-line test block.

**Tag sections support full install surface (2026-04)** — distro-version tag sections (`debian:13:`, `ubuntu:24.04:`, `fedora:43:`) can carry `repos:`, `keys:`, `options:`, and `package:`, not just packages. Useful when upstream apt-repo URLs differ per codename (Docker CE, Kubernetes). See `/ov-image:layer`.

#### user_policy: adopt vs create

Base images differ in whether they ship a pre-existing uid-1000 account. Ubuntu 24.04 ships `ubuntu:ubuntu`; Fedora / Arch / Debian 13 ship nothing at uid 1000. Overthink handles this declaratively via `build.yml distro.<name>.base_user:` + an image-level `user_policy:` field:

| Policy | Behavior |
|---|---|
| `auto` (default) | Adopt `base_user` if declared AND the image didn't explicitly set `user:`; otherwise create the configured user. |
| `adopt` | Always adopt. Hard-errors without a declaration. |
| `create` | Always create via `useradd`. |

So `ubuntu-coder` runs as `ubuntu:/home/ubuntu` (adopt) while `debian-coder`, `fedora-coder`, `arch-coder` run as `user:/home/user` (create) — zero image-level diff, zero layer-level special cases. Layers that need to reference the uid-1000 account by name use `getent passwd 1000` discovery (see `/ov-coder:sshd`) rather than hardcoding a literal.

See `/ov-image:image` "user_policy" and `/ov-build:build` "base_user:" for the full reference and decision matrix.

### Two deploy targets: containers and the host

Images typically run as containers. But Overthink can also **apply the same layer recipe directly to your workstation** via `ov deploy add host <ref>` — the layers' packages, files, and services land on your local filesystem via the host's native package manager, systemd, and shell profile, without standing up a container. The same `InstallPlan` IR drives container builds (`ov image build`), container deploys (`ov deploy add <name>`), and host deploys (`ov deploy add host`) — so whatever works in an image behaves the same way on the host.

```bash
# Install one layer to the host
ov deploy add host ripgrep

# Apply a whole image's layer set to your workstation
ov deploy add host fedora-coder --with-services --yes

# Overlay a private layer onto a shared base
ov deploy add host fedora-coder --add-layer ./private-overlay.yml

# Reverse it (runs ReverseOps: dnf remove, systemctl disable, rm env.d file, etc.)
ov deploy del host --yes
```

Everything installed is recorded in a ledger at `~/.config/overthink/installed/` (per-layer JSON with deploy-refcount), so `ov deploy del host` reverses the exact operations that ran. Opt-in gates (`--with-services`, `--allow-repo-changes`, `--allow-root-tasks`) make intent explicit for side-effects that mutate global host state. See `/ov-local:local-deploy` for the full executor model, ledger layout, and the 15 ReverseOp kinds.

Layer overlays work on both targets: `ov deploy add my-dev fedora-coder --add-layer team-extras` synthesizes an overlay Containerfile and deploys the overlay image; `ov deploy add host fedora-coder --add-layer team-extras` merges the extra layers into the host-target plan. Same `add_layer:` field in `deploy.yml` drives both.

### Docker or Podman — Your Choice

Docker is the container tool most people know. Podman is a newer alternative from Red Hat that runs without a background daemon and integrates natively with Linux systemd. `ov` works with either — same commands, same images, same results. Switch with `ov settings set engine.build podman`.

### Init Systems: Generic, Configurable, Extensible

**Inside containers**, Overthink uses an **init system** to manage services. The default is **supervisord** — a lightweight process manager. When a layer declares a `service:` list in `layer.yml` (unified structured schema — 22 fields per entry including `kind: eventlistener` for supervisord circuit breakers like chrome's 3-strike crash detector — see `/ov-image:layer` and `/ov-selkies:chrome`), `ov` renders it through the init-system's `service_schema` in `build.yml` (supervisord INI or systemd unit file, depending on the target init) and bundles the result into the image. The container starts supervisord as its main process, and supervisord starts and monitors all your services. This is how you get PostgreSQL, Traefik, and your application all running in one container. Images without init system services (like `fedora-ov`) use `sleep infinity` as the container entrypoint instead — keeping the container alive for `ov shell` to exec into.

**On the host**, Overthink uses **systemd** — the init system that already manages your Linux machine. When you run `ov config`, it generates a Podman quadlet that registers your container as a systemd service, provisions secrets, and mounts any encrypted volumes — all in one step. So systemd manages the container, and the configured init system (or `sleep infinity`) manages what runs inside it. Two levels, cleanly separated. When you use `ov deploy add host` to apply layers directly (no container), the same `service:` entries are rendered as systemd units on the host's `systemd` — user-scope at `~/.config/systemd/user/` or system-scope at `/etc/systemd/system/` depending on `scope:`.

**In bootc VM images**, systemd takes over completely — it's PID 1 at the OS level. Layers declare services via the same unified `service:` list; entries with `use_packaged: <unit>` reuse the distro-shipped unit (e.g., `sshd.service` from openssh-server) with optional drop-in overrides, while custom entries become new `.service` files. No supervisord needed because it's a real operating system, not a container.

**Adding new init systems** (like s6-linux-init, runit, or dinit) requires only editing the `init:` section of `build.yml` — zero Go code changes. Each init system declares detection rules, fragment templates, entrypoint commands, and service management commands in YAML.

### Declarative Testing

Images and deployments come with inline checks. A `eval:` block on any `layer:`, `image:`, or `deploy.yml` entry authors goss-style declarative checks — files, packages, ports, processes, HTTP endpoints, DNS, mounts, services, kernel params, and more. Checks bake into a three-section OCI label (`org.overthinkos.eval` → `{layer, image, deploy}`) so any pulled image is self-testable without its source repo. `ov eval image <image>` runs build-scope checks against a disposable container; `ov eval live <image>` runs all three sections against a live service, substituting deploy-time variables (`${HOST_PORT:N}`, `${VOLUME_PATH:name}`, `${CONTAINER_IP}`, `${ENV_*}`) so a check written once survives `deploy.yml` port remaps and volume rebindings. Local `deploy.yml` can add or override baked checks by `id:`.

Checks can be filtered per-distro via `exclude_distros: [<tag>, ...]` for probes that only apply on some distros (canonical example: the dev-tools layer's `fastfetch-binary` test sets `exclude_distros: [ubuntu:24.04]` because fastfetch is dropped from Ubuntu noble's package list). Cross-distro package naming is handled via `package:` + `package_map:` (see `/ov-eval:eval`).

`ov eval` is also the parent router for live-container drive verbs: `ov eval cdp` (Chrome DevTools), `ov eval wl` (Wayland), `ov eval dbus` (D-Bus / notifications), `ov eval vnc` (VNC), `ov eval mcp` (Model Context Protocol clients), `ov eval adb` (Android Debug Bridge), `ov eval appium` (Android UI automation / W3C WebDriver) — see `/ov-eval:cdp`, `/ov-eval:wl`, `/ov-eval:dbus`, `/ov-eval:vnc`, `/ov-build:ov-mcp-cmd`, `/ov-eval:adb`, `/ov-eval:appium`. **All seven are also authorable as declarative check verbs** (`cdp: eval`, `wl: screenshot`, `dbus: call`, `vnc: status`, `mcp: list-tools`, `adb: getprop`, `appium: click`, etc.) inside any `eval:` block, wiring Chrome/Wayland/D-Bus/VNC/MCP/ADB/Appium assertions into the same three-section OCI-label pipeline as the built-in verbs. The `mcp:` verb uses [github.com/modelcontextprotocol/go-sdk](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk) to speak Streamable HTTP (default) or SSE; URLs from `mcp_provides` metadata are auto-rewritten from container-network hostnames to the host's published port. The `adb:` verb uses [github.com/zach-klippenstein/goadb](https://pkg.go.dev/github.com/zach-klippenstein/goadb) against the host-published ADB server port; the `appium:` verb uses [github.com/tebeka/selenium](https://pkg.go.dev/github.com/tebeka/selenium) for W3C session creation plus a thin in-tree HTTP client for subsequent ops, with persistent session state at `~/.cache/ov/appium/sessions/<image>[_<instance>].json`.

Running images ship comprehensive coverage: `filebrowser` (24), `jupyter` (32), `openwebui` (24), `hermes` (50), `immich-ml` (63), `selkies-desktop` (91), `sway-browser-vnc` (92), `selkies-desktop-ov` (91 image-scope · 118 live-service). The jupyter and sway-browser-vnc counts include the `mcp:` declarative checks (3 and 2 respectively) introduced with the `ov eval mcp` verb. `selkies-desktop-ov` adds extra gates for the nested-podman recipe: its live-service run (`ov eval live selkies-desktop-ov`) verifies nested `podman run quay.io/libpod/alpine:latest`, `virsh -c qemu:///session` domcapabilities, KVM hardware acceleration, and in-container `ov version` / `ov doctor`. LABEL directives emit at the end of each Containerfile so test edits rebuild in ~2 seconds.

See `/ov-eval:eval` for the verb catalog, matcher forms, runtime variable table, gold-standard pattern (`layers/redis/layer.yml`), 10 authoring gotchas, and deploy.yml overlay rules.

### Quadlets: Containers as System Services

With Docker, you'd use `docker compose` or a restart policy to keep a container running. Podman quadlets are different: they describe a container as a native systemd service — the same system that manages SSH, networking, and everything else on your Linux box. `ov config <image>` generates the quadlet file, provisions secrets, and mounts encrypted volumes — all in one command. After that, `systemctl start/stop/status` just work — your container starts on boot, restarts on failure, and shows up in `journalctl` logs like any other service. Services can be exposed via Tailscale (tailnet-private) or Cloudflare (public internet) tunnels with full backend scheme support — HTTP, HTTPS, HTTPS with self-signed certs, TCP, TLS-terminated TCP, SSH, RDP, and SMB.

### Bootc: The Container *Is* the OS

Normally a container runs *inside* an operating system. Bootc flips this: the container image *becomes* the operating system. Fedora publishes bootc base images that are full Linux systems packaged as container images. Add layers with Overthink just like any other image — install packages, configure services, add a desktop — and the result can boot directly as a real OS.

### Containers That Become Virtual Machines

This is where it all comes together. Take a bootc-based image, and `ov vm build` converts it into a QCOW2 or raw disk image. `ov vm create` sets up a libvirt/QEMU virtual machine from that disk — same layers, same composition, but now a full VM with its own kernel, SSH access, GPU passthrough, and persistent storage. Define it once as an `image:` entry in `overthink.yml`, use it everywhere. `selkies-desktop-bootc` is the canonical worked example: a Fedora 43 bootc VM that boots straight into a browser-streamed desktop with Tailscale and KeePassXC. See `/ov-selkies:selkies-desktop-bootc` for the full composition, known caveats, and verification recipes; `/ov-vm:vm` for VM lifecycle + bootc-specific build caveats.

VM creation also works **rootless** via `qemu:///session` and a supervisord-managed `virtqemud` daemon, so `ov vm create` runs from inside a rootless container (e.g., `/ov-selkies:selkies-desktop-ov`) as uid 1000 with only `/dev/kvm` passthrough — no root, no `--privileged`, no `CAP_SYS_ADMIN`. See `/ov-infrastructure:virtualization` for the supervisord program definitions and the session-mode setup.

### VMs: `kind: vm` entities in `vms.yml`

VMs are a first-class authoring surface alongside container images. `vms.yml` declares `kind: vm` entities with a discriminated-union `source:` block — either `source.kind: cloud_image` (external qcow2 + cloud-init) or `source.kind: bootc` (pairs with a `bootc: true` container image in the repo). Resolved through `overthink.yml includes:` into `VmSpec` Go types, then consumed by `ov vm build/create` and the `ov deploy add vm:<name>` target.

```yaml
# vms.yml
vms:
  arch:                                 # cloud_image source
    source:
      kind: cloud_image
      url: https://fastly.mirror.pkgbuild.com/images/latest/Arch-Linux-x86_64-cloudimg.qcow2
      checksum: {type: sha256}                     # value auto-resolves from <url>.SHA256 sidecar
      base_user: arch                              # adopt-user pattern: no useradd, just append pubkey
    disk_size: 40G
    ram: 8G
    cpus: 4
    firmware: bios                                 # BIOS preferred for cloud images — see /ov-vm:arch
    ssh: {port: 2224, key_source: generate}
    cloud_init:
      packages: [sudo, spice-vdagent]
      ov_install: {strategy: auto}                 # auto-install ov inside the guest

  selkies-desktop-bootc-bootc:                     # bootc source
    source:
      kind: bootc
      image: selkies-desktop-bootc                 # references kind:image entry in image.yml
    disk_size: 40 GiB
    ram: 8G
```

The legacy `image.bootc: true` + `image.vm: {...}` + `image.libvirt: [...]` fields are **removed** from `kind: image` entries in the hard cutover and are rejected at load time. Projects predating this schema re-declare those VMs as `kind: vm` entities in `vm.yml` (see `/ov-vm:vms-catalog`). See `/ov-internals:cutover-policy` for the policy.

`ov deploy add vm:<name> <ref>` applies host-deploy-style layer recipes **inside** a provisioned VM over SSH. The same `InstallPlan` IR drives container, host, VM, and K8s deploys — write a layer once, deploy it anywhere. See `/ov-internals:vm-deploy-target` for the SSH-executor model and `/ov-vm:vms-catalog` for the full authoring reference.

```bash
ov vm create arch                              # provision VM
ov deploy add vm:arch fedora-coder \           # apply kitchen-sink layers in the guest
    --add-layer team-extras \
    --add-layer github.com/team/configs/layers/sshkeys
ov deploy del vm:arch                          # reverse applied layers; VM stays up
```

See `/ov-vm:arch` for the canonical cloud_image VM with BIOS-firmware + virtio-gpu + resource-sizing RCA write-up, and `/ov-vm:selkies-desktop-bootc-bootc` (+ paired `/ov-selkies:selkies-desktop-bootc`) for the canonical bootc VM.

## Install

**Recommended — Go install** (requires Go 1.25.3+):

```bash
go install github.com/overthinkos/overthink/ov@latest
```

This puts `ov` in your `$GOPATH/bin`. No other setup needed — just create an `overthink.yml` and a `layers/` directory. Legacy `image.yml`/`build.yml`/flat-form `layer.yml` projects convert in one shot with `ov migrate` (see `/ov-build:migrate`).

**Full project bootstrap** (to build images from this repo):

```bash
git clone https://github.com/overthinkos/overthink.git
cd overthink
task build:ov          # builds ov; on Arch, delegates to makepkg -si; elsewhere, portable install to ~/.local/bin/ov
ov image build         # build all images
```

On Arch the canonical install is `cd pkg/arch && makepkg -si` (or via an AUR helper — see below); `task build:ov` invokes that path automatically when run on Arch. `task` itself ships in the Arch package as a hard dep, so once `overthink-git` is installed any subsequent rebuilds work directly.

**Arch Linux package** (Arch / CachyOS / Manjaro — installs `ov` system-wide via `pacman`, with all runtime deps pulled in):

```bash
# From the AUR (mirrors this repo's PKGBUILD)
yay -S overthink-git
# or: paru -S overthink-git

# Or build directly from this checkout — `task install` pre-installs the
# two AUR deps via your AUR helper, then runs `makepkg -sefi` inside
# pkg/arch. (Do NOT use `yay -B` / `yay -Bi pkg/arch` against the local
# checkout — that mode runs `git pull` on pkg/arch's subrepo and can
# reset uncommitted edits in the working tree.)
task build:ov
```

The PKGBUILD's `pkgver()` derives the same CalVer string (`YYYY.DDD.HHMM`) that `ov version` prints from the last commit date, so `pacman -Q overthink-git` and `ov version` always agree. `depends=` covers the full runtime surface — `podman` + `docker` + `fuse-overlayfs` + `slirp4netns` for rootless/rootful containers, `qemu-full` + `libvirt` + `edk2-ovmf` + `swtpm` + `libisoburn` for `ov vm`, `portaudio` + `opusfile` for `ov eval spice` audio channels, `openbsd-netcat` so virt-manager's `qemu+ssh://` SPICE tunnel works, `gnupg` + `pinentry` + `libsecret` + `gocryptfs` + `tailscale` for the secrets/encrypted-volume/tunnel surfaces, and `go-task` so `task build:ov` works from any fresh checkout. Two AUR-only mandatory deps (`cloudflared-bin`, `gvisor-tap-vsock`) are why an AUR helper is required; bare `makepkg -si` cannot resolve them and will fail at the dep-check step. A fresh install is ready for `ov image build`, `ov vm create`, and `ov eval spice` with no further setup; the bundled pacman post-install hook (`overthink-git.install`) enables `docker.service` / `tailscaled.service` / `virtqemud.socket` and adds the installing user to the `docker` and `libvirt` groups automatically.

**From source:**

```bash
cd ov && go build -o ../bin/ov .
```

### Secret Management

Project-level secrets (API keys, credentials) are stored in `.secrets` — a GPG-encrypted file that `ov secrets gpg env` decrypts in memory when direnv loads the directory. No plaintext on disk. Requires a GPG key + gpg-agent (locally or SSH-forwarded), direnv hooked into your shell, and a one-time `direnv allow`. After that, `cd`ing into the project auto-decrypts `.secrets` and exports the variables via `.envrc`'s `eval "$(ov secrets gpg env)"`.

Manage `.secrets` with `ov secrets gpg {env, show, set, unset, edit, encrypt, recipients, import-key, export-key, setup, doctor}`. See `/ov-build:secrets` for the full command reference, KeePassXC integration for key backup/restore, and headless/SSH workflows.

## Quick Taste

```bash
# Build a single image for your platform
ov image build fedora

# Build an Arch Linux image. The Arch consumer images (arch-coder, arch-ov,
# arch-test, …) live in the overthinkos/arch submodule at image/arch and pull
# their layers from this repo by git ref; the archlinux base + builder stay here.
cd image/arch && ov image build arch-test     # auto-builds base + builder deps

# Build a CachyOS image. ALL CachyOS entities (the cachyos base, cachyos-pacstrap*,
# the cachyos-vm, and the ov-cachyos workstation profile) live in the
# overthinkos/cachyos submodule at image/cachyos and pull their layers from this
# repo by git ref. Unlike Arch, the cachyos BASE moved too — this repo's `versa`
# image pulls it back via a remote include (main → cachyos coupling).
ov -C image/cachyos image build cachyos

# Drop into an interactive shell
ov shell fedora

# Build the kitchen-sink developer image (coding CLIs + DevOps + language runtimes)
ov image build fedora-coder
ov start fedora-coder
ssh -p 2222 user@localhost           # uid=1000, passwordless sudo

# Build and run a GPU-accelerated Jupyter server
ov image build jupyter
ov start jupyter

# Configure as a systemd service (quadlet + secrets + encrypted volumes)
ov config jupyter

# Build a bootable VM disk image (selkies-desktop-bootc is the canonical bootc example)
ov image build selkies-desktop-bootc                 # build the kind:image first
ov vm build selkies-desktop-bootc-bootc --type qcow2 # kind:vm entity in vms.yml
ov vm create selkies-desktop-bootc-bootc
ov vm start selkies-desktop-bootc-bootc

# Build and run a cloud_image VM (Arch Linux + cloud-init, canonical /ov-vm:arch example)
ov vm build arch                          # fetches qcow2, resizes, renders seed ISO
ov vm create arch                         # BIOS firmware + virtio-gpu + passt portForward
ssh -p 2224 -i ~/.local/share/ov/vm/ov-arch/id_ed25519 arch@127.0.0.1

# Apply layers directly to the local filesystem (no container)
ov deploy add host ripgrep
ov deploy add host fedora-coder --with-services --yes
ov deploy add host fedora-coder \
    --add-layer github.com/team/configs/layers/sshkeys \
    --add-layer ./private-overlay.yml
ov deploy del host                    # reverses everything via ReverseOps + ledger
```

## The Layer Library

164 layers compose into images via `overthink.yml`. Dependencies resolve automatically. Every layer has a dedicated skill — invoke `/ov-distros:<name> / /ov-languages:<name> / /ov-infrastructure:<name> / /ov-tools:<name>` (or see [plugins/README.md](plugins/README.md) for the full index) for the details and composition recipe of any specific layer.

| Category | Representative layers | Purpose |
|---|---|---|
| **Foundations** | `pixi`, `python`, `nodejs`, `nodejs24`, `rust`, `golang`, `build-toolchain`, `yay` | Package managers and language runtimes |
| **Services & Infrastructure** | `supervisord`, `traefik`, `postgresql`, `vectorchord`, `redis`, `valkey`, `docker-ce`, `kubernetes` | Init, reverse proxy, databases, container-in-container |
| **GPU & ML** | `cuda`, `rocm`, `nvidia`, `llama-cpp`, `python-ml`, `jupyter`, `jupyter-ml`, `unsloth`, `unsloth-studio`, `ollama`, `comfyui` | NVIDIA/AMD runtimes and ML stacks |
| **Desktop Compositors** | `sway`, `labwc`, `wayvnc`, `pipewire`, `selkies` | Wayland/X11 servers, audio, browser-streamed desktops |
| **Chrome variants** | `chrome`, `chrome-sway` | Chrome DevTools on `:9222` + DevTools MCP on `:9224` (29 tools) per compositor |
| **AI & Agents** | `openclaw`, `hermes`, `hermes-full`, `hermes-playwright`, `openwebui`, `claude-code`, `codex`, `gemini`, `forgecode`, `oracle` | AI gateways, agents, LLM UIs, and coding CLIs |
| **Applications** | `immich`, `immich-ml`, `github-runner`, `steam`, `heroic`, `vscode`, `dev-tools`, `filebrowser`, `devops-tools` | End-user apps and workstation tooling |
| **Desktop Utilities** | `ffmpeg`, `wf-recorder`, `wl-record-pixelflux`, `wl-screenshot-pixelflux`, `wl-overlay`, `asciinema`, `libnotify`, `fastfetch` | Multimedia, recording, overlays, notifications |
| **Security & Identity** | `agent-forwarding`, `gnupg`, `direnv`, `ssh-client`, `sshd`, `gocryptfs`, `container-nesting`, `tailscale`, `keepassxc` | Agent forwarding, encrypted storage, mesh VPN, password manager, nesting |
| **OS / Bootc** | `bootc-base`, `bootc-config`, `cloud-init`, `os-config`, `os-system-files`, `qemu-guest-agent`, `socat` | Bootable disk image and VM integration |

**Composition meta-layers** — `sway-desktop`, `sway-desktop-vnc`, `selkies-desktop`, `bootc-base`, `openclaw-full`, `openclaw-full-ml`, `python-ml`, `jupyter-ml`, `unsloth-studio` bundle curated layer sets. See the matching `/ov-distros:<name> / /ov-languages:<name> / /ov-infrastructure:<name> / /ov-tools:<name>` skill for the exact composition recipe.

### Data Layers

Some layers provide **data** instead of packages or services via the `data:` field in `layer.yml`:

```yaml
# layers/notebook-templates/layer.yml
volumes:
  - name: workspace
    path: /workspace
data:
  - src: data/notebooks
    volume: workspace
```

At build time, data files are staged at `/data/<volume>/` inside the image. At deploy time, `ov config --bind <volume>` provisions the data into bind-backed volume directories; `ov update` merges new data non-destructively. **Data images** (`data_image: true`) take this further: scratch-based images containing only data + OCI labels, consumed via `ov config --data-from <data-image>`. See `/ov-core:ov-config` and `/ov-jupyter:notebook-templates` for examples.

## The Lifecycle

Overthink covers the full lifecycle — from development to production — whether you're driving or your AI agent is:

**Develop** — `ov shell <image>` drops you into an interactive container with all your layers, volumes mounted, GPU passed through. Change code, rebuild, iterate.

**Run** — `ov start <image>` launches a detached service container with the configured init system managing your processes, traefik routing your services, and persistent volumes for data.

**Deploy** — `ov deploy add <name> <ref>` is the unified entry point for applying a deployment. The literal name `host` targets the local filesystem; any other name is a container deployment. `ov config <image>` remains the primary way to set up a container deploy (quadlet + secrets + encrypted volumes), and is the single entry point when you're not using `add_layer:` overlays. It reads the image's embedded labels, generates a quadlet, provisions secrets (with `--password auto` for hands-free setup or `--password manual` to prompt), configures volume backing (`--bind name` for host bind mounts, `--encrypt name` for gocryptfs, or `--volume name:encrypt:/path` for explicit per-volume encrypted paths), provisions data from data layers into bind-backed volumes (`--seed` by default, `--force-seed` to overwrite, `--data-from <image>` for external data sources), saves deployment state to `~/.config/ov/deploy.yml`, and registers with systemd. `ov config` must be run before `ov start` in quadlet mode. For services with encrypted volumes, boot behavior depends on the credential backend: **Secret Service (keyring)** auto-starts after login — the quadlet's ExecStartPre waits for the keyring to unlock via event-driven DBus signal subscription (zero CPU cost, unbounded wait), while **KeePass or no backend** requires `ov start` after login to prompt for the passphrase. When a service declares `env_provides` or `mcp_provides`, `ov config` injects those entries into `deploy.yml` under a unified `provides:` section for cross-container discovery — env vars and MCP server URLs are resolved from `{{.ContainerName}}` templates at deploy time (use `--update-all` to propagate to already-deployed services). MCP provides are pod-aware: when provider and consumer share a container, URLs resolve to `localhost`. If the image declares `env_requires` or `mcp_requires`, `ov config` warns about missing dependencies. No project source needed — just the image.

**Ship** — `ov image build --push` builds for all platforms and pushes to your registry. `ov vm build` turns bootc images into bootable disk images.

**Manage** — `ov update` pulls new images, syncs data from data layers into bind-backed volumes (non-destructive merge by default, `--force-seed` to overwrite), and restarts services. `ov config mount/unmount` handles encrypted volumes (each mount runs as an independent `ov-enc-<image>-<volume>.scope` systemd unit that survives container restart/stop). `ov settings migrate-secrets` moves plaintext credentials to the system keyring via Secret Service (GNOME Keyring, KDE Wallet, or KeePassXC's FdoSecrets plugin). Credentials resolve in order: env var → Secret Service → config-file fallback (`~/.config/ov/config.yml`, 0600, used on headless hosts with no Secret Service). Project-level shell secrets live in a GPG-encrypted `.secrets` file managed by `ov secrets gpg`. `ov alias install` creates host-level command aliases that transparently run inside containers.

## Command Reference

The `ov` CLI has 24 top-level command families split across three modes with disjoint input sets — **build mode** (`ov image …` reads `overthink.yml`), **test mode** (`ov eval` reads OCI labels + `deploy.yml` tests overlay, never `overthink.yml`), and **deploy mode** (everything else reads OCI labels + `deploy.yml`) — plus one cross-mode gateway command (`ov mcp serve`) that exposes the entire CLI surface as an MCP server. Each command has a dedicated skill — invoke `/ov:<cmd>` (or run `ov <cmd> --help`) for full flag listings and examples. This section is a scannable index.

| Area | Commands | Skill |
|---|---|---|
| **Image family (build mode)** | `ov image {build, generate, validate, merge, new, inspect, list, pull, test}` | `/ov-image:image` (umbrella) + `/ov-build:build`, `/ov-build:generate`, `/ov-build:validate`, `/ov-build:merge`, `/ov-build:new`, `/ov-build:inspect`, `/ov-build:list`, `/ov-build:pull` |
| **Image authoring (MCP-first surface)** | `ov image {new project, new image, set, add-layer, rm-layer, fetch, refresh, write, cat}` and `ov layer {set, add-rpm, add-deb, add-pac, add-aur}` — comment-preserving YAML edits + escape-hatch file writes, all auto-exposed as MCP tools so an agent can author a project from scratch over RPC | `/ov-image:image` "Authoring" table + `/ov-build:new`, `/ov-image:layer` |
| **Deployment** | `deploy add`/`del` (unified verb; four targets: `host` → local filesystem, `vm:<name>` → VM via SSH, `kubernetes` → Kustomize tree, any other name → container deploy); `deploy from-image` (source-less deploy from OCI labels); `deploy sync <name>` (apply K8s changes live); `config`, `start`, `stop`, `update`, `remove` | `/ov-core:deploy`, `/ov-local:local-deploy`, `/ov-kubernetes:kubernetes`, `/ov-core:ov-config`, `/ov-core:start`, `/ov-core:stop`, `/ov-core:ov-update`, `/ov-core:remove`, `/ov-internals:vm-deploy-target` |
| **Schema migration** | `migrate unified` (legacy `image.yml`/`build.yml`/flat-form `layer.yml` → unified `overthink.yml`); `migrate vm-spec` (legacy `image.bootc`/`image.vm:`/`image.libvirt:` → `vms.yml` `kind: vm` entities). Both idempotent; auto-invoked on remote-cache downloads | `/ov-build:migrate` |
| **Runtime** | `shell`, `cmd`, `service`, `status`, `logs`, `tmux` | `/ov-core:shell`, `/ov-core:cmd`, `/ov-core:service`, `/ov-core:ov-status`, `/ov-core:logs`, `/ov-automation:tmux` |
| **Desktop recording** | `record` | `/ov-eval:record` |
| **Testing + live-container drive** | `test` (runs declarative tests AND hosts nested verbs: `test cdp`, `test wl`, `test dbus`, `test vnc`, `test mcp`), `image test` | `/ov-eval:eval` (parent router), `/ov-eval:cdp`, `/ov-eval:wl`, `/ov-eval:dbus`, `/ov-eval:vnc`, `/ov-build:ov-mcp-cmd` |
| **MCP gateway (cross-mode)** | `mcp serve` — 190 tools from Kong reflection (every CLI leaf becomes one MCP tool); Streamable HTTP / stdio; `--read-only` filter; auto-fallback to `overthinkos/overthink` when no project is wired (disable with `--no-default-repo`); new in 2026: includes project-scaffolding + YAML-editing + free-form file-write verbs so agents can build projects from scratch over RPC | `/ov-build:ov-mcp-cmd` + `/ov-tools:ov-mcp` |
| **Secrets & config** | `secrets`, `settings`, `alias` | `/ov-build:secrets`, `/ov-build:settings`, `/ov-automation:alias` |
| **Host & VM** | `doctor`, `udev`, `vm` (reads `kind: vm` entities from `vms.yml` — not `image.yml`; `<name>` on `ov vm build <name>` is a VM entity key) | `/ov-core:ov-doctor`, `/ov-automation:udev`, `/ov-vm:vm`, `/ov-vm:vms-catalog` |
| **Misc** | `version` | `/ov-core:ov-version` |

**Global flags** (apply to every command):

- `-C <dir>` / `--dir <dir>` / `OV_PROJECT_DIR=<dir>` — override the project directory (where `overthink.yml` lives) for build-mode commands. Honoured before Kong dispatches the subcommand.
- `--repo <OWNER/REPO[@REF]>` / `OV_PROJECT_REPO=…` — read `overthink.yml` from a remote git repo instead of a local directory. Bare `owner/repo` auto-prefixes `github.com/`; the literal `default` expands to `overthinkos/overthink`. Cached in `~/.cache/ov/repos/` (override with `OV_REPO_CACHE`). Remote refs containing legacy `image.yml` are auto-migrated on download (see `/ov-build:migrate`). Mutually exclusive with `--dir`. See `/ov-image:image` "Project directory resolution".

Load-bearing detail for `ov mcp serve` inside a container: either bind-mount the host project at `/workspace` with `ov config --bind project=/host/path` (the `ov-mcp` layer default, world-writable), set `OV_PROJECT_REPO=owner/repo@<ref>` to pin an upstream, or let the auto-fallback to `overthinkos/overthink` kick in. **Refined in 2026-04**: the fallback now fires whenever the resolved cwd has no `overthink.yml`, regardless of `OV_PROJECT_DIR` being set. Previously the fallback only fired when `OV_PROJECT_DIR` was empty — but the `ov-mcp` layer permanently sets `OV_PROJECT_DIR=/workspace`, so the fallback was effectively dead code. Now a deployer who forgets `--bind` still gets a working MCP server (backed by the upstream repo) with a clear log line naming the reason. The top-level CLI never auto-fetches — only `ov mcp serve` does; `--no-default-repo` opts out.

A few sample invocations:

```bash
ov image build jupyter                 # Build an image (see /ov-build:build for --push, --no-cache, --jobs)
ov image pull jupyter                  # Fetch into local storage (see /ov-build:pull for short/full/remote refs)
ov config jupyter                      # Unified deploy setup (see /ov-core:ov-config for --bind, --encrypt, --sidecar, -i, --update-all)
ov start jupyter                       # Launch as a systemd service
ov shell jupyter                       # Interactive dev shell with volumes + GPU
ov eval cdp open selkies-desktop "https://example.com"   # Browser automation (see /ov-eval:cdp)
ov eval wl screenshot selkies-desktop       # Compositor-agnostic screenshot (see /ov-eval:wl)
ov vm build selkies-desktop-bootc --type qcow2     # Build a bootable VM disk (see /ov-vm:vm)
ov mcp serve --listen :18765                 # Run ov itself as an MCP server (see /ov-build:ov-mcp-cmd Part 2)
```

### Pulling images from registries

Deploy-mode commands (`ov shell`, `ov start`, `ov config`, `ov alias add`, `ov vm create`, …) read image configuration from OCI labels, which requires the image to be in local storage. If it isn't, the command fails with a recommendation:

```
Error: image "jupyter:latest" is not available locally.
       Run 'ov image pull jupyter:latest' to fetch it first
```

`ov image pull` accepts three input forms: short names (resolved via `overthink.yml`'s `image:` entries, requires project directory), fully-qualified registry refs (pullable from anywhere), and `@github.com/org/repo/image[:version]` remote refs (downloads the repo and pulls its declared registry ref). See `/ov-build:pull` for details.

### Multiple Instances

Run multiple containers of the same image with `-i <instance>`. Each instance gets its own container (`ov-<image>-<instance>`), quadlet file, and `deploy.yml` entry (keyed as `<image>/<instance>`). MCP server names are auto-disambiguated with an `-<instance>` suffix so consumers can distinguish them. All `ov` commands accept `-i`.

```bash
ov config selkies-desktop -i work -e TS_HOSTNAME=work -p 3001:3000
ov config selkies-desktop -i personal -p 3002:3000
ov start selkies-desktop -i work
```

**Tunnel inheritance caveat:** tunnel config is **not** auto-inherited by instances — you must add `tunnel: {provider: tailscale, private: all}` to each instance's `deploy.yml` entry manually, then re-run `ov config` to regenerate the quadlet with Tailscale serve commands. Tunnel config is deploy.yml-only (read-skipped from OCI labels at `labels.go:238`). The `-e` flag merges env vars (upsert by key); `-c` replaces. See `/ov-core:deploy` for full inheritance semantics and `/ov-core:ov-config` for the `--update-all` propagation model.

### Sidecar Containers

Attach sidecar containers at deploy time. Sidecars run alongside the app in a shared Podman pod (shared network namespace). Templates are built into the `ov` binary.

```bash
ov config --list-sidecars                                                        # List available templates
ov config <image> --sidecar tailscale \
  -e TS_HOSTNAME=my-app \
  -e "TS_EXTRA_ARGS=--exit-node=100.80.254.4 --exit-node-allow-lan-access"
```

The Tailscale sidecar routes outbound traffic through a Tailscale exit node while keeping the pod on the `ov` bridge for container-to-container connectivity (**dual networking**). Sidecar-related `-e` flags (e.g., `TS_*`) are automatically routed to the sidecar instead of the app container. Assignments persist in `deploy.yml`. See `/ov-automation:sidecar` for the full template list and routing model.

### Wayland Overlays

`ov eval wl overlay` drives fullscreen Wayland overlays for screen recordings — title cards, lower-thirds, watermarks, countdowns, region highlights, fade transitions. Rendered by the compositor with true RGBA transparency; no post-production needed. See `/ov-eval:wl-overlay` for the full API.

## Troubleshooting

Each entry points to the canonical skill — details belong there, not here.

| Symptom | First step |
|---------|-----------|
| Service won't start | `ov status <image>` then `ov logs <image>` (`/ov-core:ov-status`, `/ov-core:logs`) |
| Quadlet out of sync with deploy.yml | `ov config <image> --update-all` (`/ov-core:ov-config`) |
| Chrome stuck or crash-looping | `/ov-selkies:chrome` Resource Caps & Circuit Breaker section |
| Encrypted volume locked at boot | `ov config mount` waits for keyring unlock automatically — zero CPU, event-driven (`/ov-automation:enc`) |
| GPU not detected | `ov doctor` then `/ov-automation:udev` |
| Resource caps not applying | `ov config <image> --update-all` to regenerate the quadlet (`/ov-core:ov-config`) |
| Build cache stale | `ov image build --no-cache <image>` (`/ov-build:build`) |
| Tunnel not appearing on a new instance | Tunnel config is deploy.yml-only — add manually per instance (`/ov-core:deploy`) |
| Service built fine but broken in production | `ov eval live <image>` runs the baked layer + image + deploy checks against the live container; `ov eval image <image>` checks the disposable build (`/ov-eval:eval`) |
| `ov vm build` fails: "no kind:vm entity in vms.yml" | Declare a `kind: vm` entity in `vm.yml` (`/ov-vm:vms-catalog`) |
| SPICE console blank on cloud_image VM | Known `simpledrm → qxldrmfb` race under UEFI + stale BOOTX64.EFI; switch to `firmware: bios` in `vms.yml` (`/ov-vm:arch` Finding B) |
| `virsh` cannot connect to session / "End of file while reading data" | virtqemud-sock path on libvirt ≥ 8.0 (`/ov-vm:vm` Backend matrix) |
| `ov deploy add vm:<name>` errors "VM does not exist" | Run `ov vm create <name>` first — VM deploy is not auto-provisioning (`/ov-core:deploy` "VM target") |

## Adding a Layer

```bash
ov image new layer my-layer            # Scaffold the directory
# Edit layers/my-layer/layer.yml       # Declare packages, deps, env, ports,
#                                      # and tasks: (see /ov-image:layer for the verb catalog)
# Optionally add tests: for file / port / http / command checks (see /ov-eval:eval)
# Optionally add pixi.toml, package.json, or Cargo.toml for auto-detected builders

# Add to an image: entry in overthink.yml:
#   layers: [..., my-layer]

ov image build my-image                # Build it
```

See [Building Layers](#building-layers-package-managers--config-files) above for the full list of supported config files. The `/ov-image:layer` skill is the canonical reference for the `task:` verb catalog (`cmd`, `mkdir`, `copy`, `write`, `link`, `download`, `setcap`, `build`), `vars:` substitution, YAML anchors, and execution-order rules.

## Works with Claude Code

Overthink is designed to work hand-in-hand with [Claude Code](https://claude.com/claude-code). The [overthink-plugins](https://github.com/overthinkos/overthink-plugins) repository provides skills that teach Claude how to compose, build, deploy, and manage your container images.

**Quick setup** — add this to your project's `.claude/settings.json`:

```json
{
  "enabledPlugins": {
    "ov-core@ov-plugins": true,
    "ov-build@ov-plugins": true,
    "ov-eval@ov-plugins": true,
    "ov-image@ov-plugins": true,
    "ov-internals@ov-plugins": true,
    "ov-distros@ov-plugins": true,
    "ov-infrastructure@ov-plugins": true,
    "ov-jupyter@ov-plugins": true,
    "ov-coder@ov-plugins": true
  },
  "extraKnownMarketplaces": {
    "ov-plugins": {
      "source": { "source": "directory", "path": "./plugins" }
    }
  }
}
```

This is a representative subset; see `plugins/.claude-plugin/marketplace.json` for the full 25-plugin catalog (commands, kind, development, images).

Then clone with the plugins submodule:

```bash
git clone --recurse-submodules https://github.com/overthinkos/overthink.git
```

This gives Claude Code access to 250+ skills covering every layer, image, and operation — so it can build images, debug services, author new layers, and manage deployments just like you would from the command line. The skill graph is densely cross-linked: invoking one skill surfaces its neighbors, and every layer skill references `/ov-image:layer` (authoring) and `/ov-eval:eval` (declarative testing).

The `chrome` layer auto-includes a **Chrome DevTools MCP server** at `http://localhost:9224/mcp` (via `chrome-devtools-mcp` sub-layer), providing 29 browser automation and inspection tools. This is auto-discovered by Hermes and other MCP consumers alongside the Jupyter MCP server, and can be probed end-to-end with `ov eval mcp` (see `/ov-build:ov-mcp-cmd`).

The `ov-jupyter` plugin is **manifest-only** — it contains a `.mcp.json` that registers a **Jupyter MCP server** (named `jupyter`) at `http://localhost:8888/mcp` with Claude Code, automatically connecting when the `jupyter` or `jupyter-ml` container is running. It ships no SKILL.md files itself; Jupyter's operational docs live in `/ov-jupyter:jupyter`, `/ov-jupyter:jupyter-mcp`, and `/ov-selkies:jupyter*`. Once connected, Claude Code can use 13 MCP tools to create, read, edit, execute, and watch notebooks — with real-time collaboration alongside human users via CRDT. `jupyter` is the lightweight multi-arch variant (no GPU); `jupyter-ml` adds the full CUDA ML stack (PyTorch, vLLM, Unsloth, LangChain); `jupyter-ml-notebook` adds 37 Unsloth fine-tuning notebooks, 6 Ollama integration notebooks, and 15 LLM course notebooks. See `/ov-jupyter:jupyter`, `/ov-jupyter:jupyter-ml`, and their image counterparts for details. Use `ov eval mcp` (see `/ov-build:ov-mcp-cmd`) to verify any `mcp_provides` endpoint is alive and exposes the expected tool catalog.

See [CLAUDE.md](CLAUDE.md) for the complete system specification and [plugins/README.md](plugins/README.md) for the full skill reference.

## License

MIT
