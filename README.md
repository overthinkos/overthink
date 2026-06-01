# Overthink

**The container management experience for you and your AI.**

Describe what you need in a simple layer list, and `ov` composes it
into optimized multi-stage container images — from an interactive
dev shell to a running service to a systemd unit to a bootable VM,
to an AI desktop running inside a sandbox. Works the same way
whether you're at the keyboard or your AI agent is driving.

187 layers across this repo and its submodules. 53 image definitions
(39 enabled by default). 2 VM definitions, 2 Android devices, and a
growing catalog of `kind: local` host templates and `kind: eval`
test beds. Docker and Podman. `linux/amd64`. Fedora, Debian, Ubuntu,
Arch, and CachyOS. One CLI: `ov` (29 top-level verbs). Every layer,
image, VM, and command has a dedicated skill — ~290 skills across
25 plugins. See `plugins/README.md` for the full index.

*The name comes from the German "überdenken" — to think something
through carefully. Not quite the same as the English "overthink,"
but let's be honest: `ov` really is trying its best to overthink
absolutely everything.*

## Table of contents

- [What's in the knife](#whats-in-the-knife)
- [Why Overthink?](#why-overthink)
- [Core concepts](#core-concepts)
- [Install](#install)
- [Quickstart](#quickstart)
- [Lifecycle](#lifecycle)
  - [Build — the repo2docker slot](#build--the-repo2docker-slot)
  - [Run — the docker-compose slot](#run--the-docker-compose-slot)
  - [Deploy — the pyinfra slot](#deploy--the-pyinfra-slot)
  - [Test — the Molecule slot](#test--the-molecule-slot)
  - [Author with AI](#author-with-ai)
  - [Manage](#manage)
- [Command reference](#command-reference)
- [Catalogs](#catalogs)
- [Troubleshooting](#troubleshooting)
- [Adding a layer](#adding-a-layer)
- [Works with Claude Code](#works-with-claude-code)
- [License](#license)

## What's in the knife

`ov` is a Swiss-army knife. Each subcommand replaces a tool you'd
otherwise install separately, in a simplified form, driven from one
config and one mental model:

| If you reach for…  | …`ov` gives you (simplified)                     | `ov` surface                                                |
|--------------------|--------------------------------------------------|-------------------------------------------------------------|
| docker-compose     | multi-container pod orchestration via quadlets   | `kind: pod`, `ov deploy add`, `ov start` → [Run](#run--the-docker-compose-slot)        |
| repo2docker        | reproducible images from a declarative spec      | `kind: image` / `kind: layer`, `ov image build` → [Build](#build--the-repo2docker-slot) |
| Ansible Molecule   | disposable build → deploy → probe → tear-down beds | `kind: eval`, `ov eval run` → [Test](#test--the-molecule-slot)                          |
| pyinfra            | agentless ssh deploy to hosts and VMs            | `kind: local`, `target: local` + `host: user@machine` → [Deploy](#deploy--the-pyinfra-slot) |

> `ov` isn't trying to win on any single column — Compose has a
> richer service graph, repo2docker handles notebooks-as-input,
> Molecule integrates deeper with Ansible roles, pyinfra runs
> arbitrary Python on the remote. The value of `ov` is the
> *integration glue between them*: the same `layer.yml` + same image
> + same `deploy.yml` + same `kind: eval` bed drive the build, the
> local run, the remote deploy, and the test harness — and the
> binary that wires them together is also an MCP server, so your
> AI agent reaches every verb over the same RPC.

## Why Overthink?

Containers are a great idea with rough edges. Real-world needs pile
up fast: GPU passthrough with the right driver stack, containers
that need `/dev/kvm` or virtualization access without blanket
`--privileged`, multiple services managed together, encrypted
volumes, VNC or browser-streamed desktops, device permissions that
don't compromise your host. Each is solvable — but solving them all
at once, reliably, across images, is where things get hard. And if
your AI agent has to build and manage these containers too, the
complexity compounds.

Overthink treats container images as composable building blocks.
Each **layer** is a self-contained unit; an **image** is a list of
layers on top of a base. `ov` resolves the dependency graph,
generates multi-stage Containerfiles with cache mounts, and builds
in the right order — handling the hard parts so you (and your AI)
don't have to.

**Testing and evaluating deployment configs is a first-class goal —
for AI and humans.** A deploy config is only useful if you can prove it
works. `ov` ships a goss-style declarative test framework (`eval:` checks
baked into every image's OCI labels) plus disposable `kind: eval` test
beds, so any image or deployment config is self-verifiable end-to-end:
`ov eval image` (build-scope), `ov eval live` (a running deploy), and
`ov eval run <bed>` (a full build → deploy → eval → fresh-rebuild →
teardown cycle). The same surface serves a human operator at the keyboard
and an AI agent driving it: Claude Code sub-agents (`eval-bed-runner`,
`deploy-verifier`) and dynamic workflows (`/verify-beds`,
`/audit-deploy-configs`) run these beds to test and verify autonomously.
→ `/ov-eval:eval`, `/ov-internals:agents`.

**Designed around Risk Driven Development.** Documentation drifts and code
has bugs, so Overthink never lets a high-risk assumption ride on "the docs
say so." The riskiest unknown — whether a particular *combination* of layers,
at their latest versions, actually builds and runs together — gets proven
empirically on a disposable bed EARLY, before a design is built on it.
`kind: eval` beds and `ov eval` make that proof cheap, for AI agents and humans
alike: read the skill for the design intent, then confirm the high-risk parts
against a real, running system. → CLAUDE.md "Risk Driven Development (RDD)".

**Rootless-first power-user images.** The four images carrying the
full `ov` toolchain (`fedora-coder`, `fedora-ov`, `arch-ov`,
`githubrunner`) all run as uid=1000 with passwordless sudo. Four
cross-distro coder images (`/ov-coder:fedora-coder`/`arch-coder`/
`debian-coder`/`ubuntu-coder`) share ~30 layers, differing only in
package sections and how the uid-1000 user lands (create vs. adopt
mode). Rootless nested containers and rootless libvirt VMs work
with zero additive capabilities via the surgical `unmask=/proc/*`
security_opt from the `container-nesting` layer.
→ `/ov-distros:container-nesting`, `/ov-coder:fedora-coder`.

**Sandboxed AI desktops.** Overthink flips the usual AI sandboxing
model: instead of restricting what the AI can do, give it full
access to a complete desktop (Chrome, Wayland compositor, dev tools,
network services) and sandbox the *entire desktop* inside a
container. `/ov-openclaw:openclaw-desktop` is the all-in-one CachyOS
streaming desktop: Selkies desktop + openclaw-full gateway + AI
CLIs (claude-code, codex, gemini) + CPU ollama + nested `ov`. The
AI (or the user) builds images, launches nested rootless pods, and
creates libvirt VMs from a terminal inside the browser-accessible
desktop — uid 1000, no `--privileged`, no added capabilities.

## Core concepts

A handful of terms recur everywhere — one definition each:

- **Layer** (`kind: layer` in `layer.yml`) — packages (per-distro),
  tasks (eight verbs: `cmd`/`mkdir`/`copy`/`write`/`link`/`download`/
  `setcap`/`build`), services (one unified `service:` list — see
  init-system polymorphism below), volumes, env, ports, eval probes,
  `env_provides`/`env_requires`/`mcp_provides`/`mcp_accepts` for
  cross-container discovery, plus a `version:` CalVer.
  → `/ov-image:layer`.
- **Image** (`kind: image`) — base + ordered layer list. Multi-stage
  Containerfile, content-derived `org.overthinkos.version` OCI label,
  stable cache. → `/ov-image:image`.
- **Pod** (`kind: pod`) — multi-container deploy shape: containers,
  sidecars, tunnels. Deployed as a Podman pod with a quadlet per
  container. → `/ov-pod:pod`.
- **VM** (`kind: vm`) — `source: {kind: cloud_image | bootc}`,
  disk/ram/cpu, libvirt+QEMU. `ov vm build/create/start/stop/clone/
  snapshot/console`. → `/ov-vm:vm`.
- **K8s** (`kind: k8s`) — Kubernetes cluster (in-pod k3s or external)
  with provisioning + workload defaults. → `/ov-kubernetes:kubernetes`.
- **Local** (`kind: local`) — host-side layer stack applied to the
  operator's machine (or any ssh-reachable host) via the native
  package manager + systemd + shell profile. → `/ov-local:local-spec`.
- **Android** (`kind: android`) — Android device: in-pod emulator
  (via `image:`) or remote/physical adb endpoint. `apk:` is a layer
  package format scoped to Android targets. → `/ov-eval:android`.
- **Deploy** (`kind: deploy`) — a named deployment of one of the
  kinds above to a `target:` (`pod` default, `vm`, `k8s`, `local`,
  `android`). Carries env overlays, port remaps, volume backings,
  sidecars, tunnels, secrets, and the `disposable: true` opt-in.
  → `/ov-core:deploy`.
- **Eval** (`kind: eval`) — a *disposable* deploy used as an R10 test
  bed: `ov eval run <bed>` runs build → deploy → probe →
  fresh-update → tear-down. The `kind: recipe` / `kind: score` /
  `kind: ai` overlays drive the AI-iteration harness on top.
  → `/ov-eval:eval`.

**`overthink.yml` is the single project entry point.** Every other
file is composed in via `import:` — a bare string for a flat
same-repo import (`build.yml`, `image.yml`, `vm.yml`, `pod.yml`,
`local.yml`, `android.yml`, `k8s.yml`, `eval.yml`), or a
single-key `alias: ref` map for a namespaced cross-repo import (Go
package-member semantics — `base: cachyos.cachyos`, fetched from
`@github.com/owner/repo:tag` and cached under `~/.cache/ov/repos/`).

**Init-system polymorphism — one place, no siblings.** A layer that
needs the same service under supervisord (containers) and systemd
(hosts / VMs / bootc) declares BOTH forms in one `service:` list,
same `name:`, one entry with `use_packaged: <unit>.service`, another
with custom `exec:`. The init system at deploy time picks the
matching form. *Never* create a `<name>-host` / `<name>-pod` sibling
layer for this. Canonical examples: `/ov-coder:sshd`,
`/ov-infrastructure:virtualization`, `/ov-infrastructure:postgresql`.

**Distro + package-format dispatch.** Layer declares `distro:` tag
sections (`fedora:43:` / `ubuntu:24.04:`) and package-format sections
(`rpm:` / `deb:` / `pac:` / `aur:` / `apk:`). Image declares its
`distro:` identity and `build:` formats. Distro tag first-match wins;
`build:` formats install in declared order. `fedora-coder` /
`arch-coder` / `debian-coder` / `ubuntu-coder` share ~30 layers,
differing only in package sections.

**Disposability — explicit opt-in.** `disposable: true` on a
`kind: deploy` is the *one and only* authorization for `ov update`'s
autonomous destroy + rebuild. No hostname heuristic, no inference.
Explicit-only is what makes `ov update <name>` safe on shared
infrastructure. → `/ov-internals:disposable`.

## Install

**Recommended — Go install** (requires Go 1.25.3+):

```bash
go install github.com/overthinkos/overthink/ov@latest
```

This puts `ov` in your `$GOPATH/bin`. Create an `overthink.yml` and
a `layers/` directory and you're done. Legacy projects (predating
the unified schema, the `kind:` discriminators, or the singular
field names) convert in one shot with `ov migrate` — a single
idempotent chain to the latest CalVer schema. See `/ov-build:migrate`.

**Full project bootstrap** (to build images from this repo):

```bash
git clone --recurse-submodules https://github.com/overthinkos/overthink.git
cd overthink
task build:ov         # on Arch: delegates to makepkg -si; elsewhere: portable install to ~/.local/bin/ov
ov image build        # build everything
```

**Arch / CachyOS / Manjaro** — install system-wide via `pacman`:

```bash
yay -S overthink-git           # mirrors this repo's PKGBUILD
# or build from this checkout:
task build:ov                  # pre-installs AUR deps via your AUR helper, then runs makepkg -sefi in pkg/arch
```

The PKGBUILD `pkgver()` derives the same CalVer
(`YYYY.DDD.HHMM`) `ov version` prints, so `pacman -Q overthink-git`
and `ov version` always agree. `depends=` covers the full runtime
surface — `podman`/`docker`/`fuse-overlayfs`/`slirp4netns` for
rootless containers, `qemu-full`/`libvirt`/`edk2-ovmf`/`swtpm` for
`ov vm`, `gnupg`/`pinentry`/`libsecret`/`gocryptfs`/`tailscale` for
secrets/encrypted volumes/tunnels, `go-task` so `task build:ov`
works from any fresh checkout. The pacman post-install hook enables
`docker.service` / `tailscaled.service` / `virtqemud.socket` and
adds the user to the `docker` and `libvirt` groups automatically.

**From source:**

```bash
cd ov && go build -o ../bin/ov .
```

## Quickstart

```bash
# Build a single image
ov image build fedora

# Build a CachyOS image (in submodule; ov resolves cross-repo refs)
ov -C image/cachyos image build cachyos

# Drop into an interactive shell
ov shell fedora

# Build and run a GPU-accelerated Jupyter server
ov image build jupyter
ov start jupyter

# Configure as a systemd service (quadlet + secrets + encrypted volumes)
ov config jupyter

# Build a bootable VM disk image
ov image build selkies-desktop-bootc                  # the kind:image
ov vm build  selkies-desktop-bootc-bootc --type qcow2 # the kind:vm
ov vm create selkies-desktop-bootc-bootc

# Apply layers directly to your workstation (no container)
ov deploy add host ripgrep
ov deploy add host fedora-coder --with-services --yes
ov deploy del host                  # reverses everything via ReverseOps + ledger

# Run a kind:eval test bed end-to-end (the R10 acceptance gate)
ov eval run eval-pod
```

## Lifecycle

The same six stages cover everything `ov` does — develop, run,
deploy, test, author, manage. Each stage opens with a callback to
its row in [What's in the knife](#whats-in-the-knife) and closes
with the humility line that anchors `ov`'s posture against the
external tool it loosely resembles.

### Build — the repo2docker slot

> Declarative input → reproducible image. Same idea, simpler
> vocabulary.

Each image declares a `base:`, an ordered `layer:` list, a `distro:`
identity, and a `build:` set of package formats. The planner
resolves the dependency graph, generates a multi-stage Containerfile
with cache-mounted package archives + AUR srcdest + pixi/npm/cargo
workdirs, and runs `podman build` (or `docker build` — switch with
`ov settings set engine.build podman`). The emitted image carries
OCI labels for every capability it claims: `org.overthinkos.eval`,
`org.overthinkos.init`, `org.overthinkos.version` (content-derived
`EffectiveVersion`, stable across no-op rebuilds), `.ports`, etc.

Commands: `ov image build` (build), `ov image generate` (write
`.build/` only), `ov image validate`, `ov image inspect`,
`ov image list`, `ov image merge`, `ov image pull`,
`ov image reconcile`. MCP-driven authoring — `ov image {set,
add-layer, rm-layer, fetch, refresh, write, cat}`, `ov layer {set,
add-rpm, add-deb, add-pac, add-aur}` — gives AI agents
comment-preserving YAML edits over RPC.

Cross-repo refs: `import:` items and layer references can name
`@github.com/owner/repo:tag`. The resolver fetches every distinct
`(repo, git-tag)` and arbitrates per per-entity `version:` — same
`version:` across different git tags → silent (re-tag);
different `version:` → warn once and use the newest. `ov image
reconcile` aligns the cross-repo pins when a layer's CalVer moves.

→ `/ov-build:build`, `/ov-build:generate`, `/ov-build:validate`,
`/ov-build:inspect`, `/ov-build:reconcile`, `/ov-image:image`,
`/ov-image:layer`, `/ov-internals:capabilities`.

*This is simplified, not tougher than repo2docker — it doesn't
ingest notebooks-as-input; it does compose any image you can spell
in YAML, on five distros, with full multi-stage caching.*

### Run — the docker-compose slot

> Multiple containers, one declaration, one start command. Same
> shape as Compose, different mechanism.

`kind: pod` is the multi-container deploy shape. `ov deploy add
<name> <pod-ref>` materializes it; `ov start` brings it up via
Podman quadlets (`~/.config/containers/systemd/`) so a deployment is
a real systemd user unit — `journalctl`, `systemctl status`,
auto-restart on failure, start on login. `ov stop`, `ov restart`,
`ov status`, `ov logs`, `ov cmd`, `ov shell`, and `ov service`
(drive the inner supervisord) operate it; `ov remove` deletes the
quadlet and containers.

Images with multiple co-resident services in one container use
supervisord as their init (declared via the same unified `service:`
list); images that deploy as separate containers get one quadlet
each in a shared pod. Either way, the same `service:` schema is the
input.

- **Multiple instances** (`-i <instance>`) — every command takes
  `-i`; instances get distinct quadlet names
  (`ov-<image>-<instance>.container`), `deploy.yml` entries
  (`<image>/<instance>`), and disambiguated MCP server names.
- **Sidecars** (`--sidecar <name>`) — attach a Tailscale,
  cloudflare-tunnel, or other container template into a shared pod.
  Sidecar-related env (`TS_*`, `CF_*`) routes to the sidecar, not
  the app. List with `ov config --list-sidecars`.
- **Tunnels** — `tunnel:` block declares Cloudflare (public) or
  Tailscale (tailnet-private) exposure with full backend scheme
  support (HTTP / HTTPS / TCP / TLS / SSH / RDP / SMB).
- **Encrypted volumes** — `--encrypt <vol>` or `type: encrypted`;
  gocryptfs masterkey provisioned into the Secret Service, mounted
  via independent `ov-enc-<image>-<volume>.scope` systemd units
  that survive container restart. Manage with `ov config {mount,
  unmount, status, passwd}`.
- **GPU access** — NVIDIA via CDI (`gpu.nvidia.com` annotation);
  ROCm for AMD; `ov udev install/remove` writes the host-side
  rules. CUDA toolkit + cuDNN + ONNX Runtime in the `cuda` layer.
- **Wayland desktop streaming** — the Selkies family
  (`selkies-desktop`, `sway-desktop`, `sway-browser-vnc`) bundles a
  Wayland compositor (sway or labwc) + Chrome + `wayvnc` on port
  5900 + Pipewire audio. Browser pane at `:3000`.
- **Per-image MCP servers** — `chrome-devtools-mcp` on `:9224`,
  `jupyter-mcp` at `:8888/mcp`, `marimo-mcp` at `:2718/mcp/server`,
  nested `ov-mcp`. Declared via `mcp_provides:` and auto-discovered
  by consumers (Hermes, Claude Code) through `OV_MCP_SERVERS`.
- **Auto service discovery** — a layer's `env_provides:` declares
  env vars with `{{.ContainerName}}` templates injected into every
  co-deployed container at `ov config` time. Deploy `ollama` and
  every other pod sees `OLLAMA_HOST=http://ov-ollama:11434`.
  `mcp_provides:` works the same way for MCP URLs.
  `env_requires:` / `env_accepts:` document consumer dependencies
  so `ov config` warns early.

→ `/ov-core:start`, `/ov-core:logs`, `/ov-core:cmd`,
`/ov-core:service`, `/ov-core:ov-status`, `/ov-automation:sidecar`,
`/ov-automation:enc`, `/ov-automation:udev`, `/ov-pod:pod`,
`/ov-selkies:selkies-desktop-layer`, `/ov-selkies:sway`.

*This is simplified, not tougher than docker-compose — Compose has
the richer service-graph DSL; `ov` gives you systemd-native
lifecycle, encrypted volumes, GPU/Wayland/MCP/tunnel sugar, and the
same YAML on both sides of the host boundary.*

### Deploy — the pyinfra slot

> Agentless. Same `layer.yml` on the host, on a remote ssh box, in
> a VM, in k3s, on an Android emulator.

`ov deploy add <name> <ref>` is the unified verb; `target:`
discriminates where it lands:

- **`target: pod`** (default) — Podman + quadlet, as in [Run](#run--the-docker-compose-slot).
- **`target: vm`** — libvirt + QEMU. Layers are applied *inside* the
  booted VM over SSH via the same InstallPlan IR. `ov vm build`
  (bootc → QCOW2/RAW), `ov vm create/destroy/start/stop`, `ov vm
  clone` (snapshot fork), `ov vm snapshot`, `ov vm console`. The
  managed `~/.config/ov/ssh_config` fragment gets a `Host
  ov-<vmname>` stanza written on `ov vm create`.
  → `/ov-vm:vm`, `/ov-internals:vm-deploy-target`.
- **`target: k8s`** — Kustomize tree applied to k3s in-pod (layer
  triplet `/ov-infrastructure:k3s` + `k3s-server` + `k3s-agent`) or
  to an external cluster. `K3S_CLUSTER_TOKEN` auto-generated on
  first deploy via `ensureLayerSecret` and shared with every joining
  agent — zero operator setup. → `/ov-kubernetes:kubernetes`,
  `/ov-infrastructure:k3s`.
- **`target: local`** — applies the layers' packages / files /
  systemd units to the host filesystem. `host: local` (default)
  uses the local shell executor; `host: user@machine[:port]` (or a
  configured alias) re-execs `ov` over SSH. Per-machine overlays
  via `add_layer:` in `~/.config/ov/deploy.yml`. Ledger at
  `~/.config/overthink/installed/` records every ReverseOp so
  `ov deploy del host` reverses precisely what was applied.
  → `/ov-local:local-deploy`, `/ov-local:local-spec`.
- **`target: android`** — `kind: android` device (in-pod emulator
  via `image:` or remote adb endpoint via `adb: {host: …}`);
  `apk:` packages installed by `apkeep` (Google Play) or pushed
  from committed `.apk` files via goadb. Nested `pod → android`
  mirrors `vm → k8s`. → `/ov-eval:android`, `/ov-eval:adb`.

`ov deploy del`, `ov deploy sync` (apply K8s changes live),
`ov deploy from-image` (source-less deploy from OCI labels), and
`ov update` complete the lifecycle. `ov update <name>` performs
destroy + (optional rebuild) + create + start unattended *only*
when the deploy carries `disposable: true`.

**Secrets.** Credentials resolve in order: env var → Secret Service
(systemd keyring; GNOME Keyring, KDE Wallet, or KeePassXC
FdoSecrets) → config-file fallback (`~/.config/ov/config.yml`,
0600). Project-level shell secrets live in a GPG-encrypted
`.secrets` file: `ov secrets gpg env` decrypts in memory when
direnv loads the project; no plaintext on disk. Manage with `ov
secrets gpg {env, show, set, unset, edit, encrypt, recipients,
import-key, export-key, setup, doctor}`. Layer-private secrets
(like `K3S_CLUSTER_TOKEN`) get auto-provisioned via
`ensureLayerSecret` and stored under `ov/secret/<key>` in the
Secret Service. **Agent forwarding** — the `agent-forwarding` layer
binds host `SSH_AUTH_SOCK` / `GPG_AGENT_SOCK` into the container.
→ `/ov-build:secrets`.

→ `/ov-core:deploy`, `/ov-core:ov-config`, `/ov-core:ov-update`,
`/ov-internals:disposable`, `/ov-vm:vms-catalog`.

*This is simplified, not tougher than pyinfra — pyinfra runs
arbitrary Python on the remote and has a deeper inventory model;
`ov` gives you the same layer recipe on host / VM / k8s / android
and the same eval probes verifying each.*

### Test — the Molecule slot

> Build → deploy → probe → fresh-update → tear down. Disposable
> beds with the same DSL as production deploys.

Tests are first-class. Every `layer.yml` / `image.yml` /
`deploy.yml` can declare an `eval:` block of goss-style checks
(files, packages, services, ports, processes, commands, HTTP, DNS,
mounts, users, groups, kernel params, interfaces, matchers). Checks
bake into a three-section OCI label
(`org.overthinkos.eval` → `{layer, image, deploy}`) so any pulled
image is self-testable without its source repo.

Three execution modes:

- **`ov eval image <image>`** — disposable `podman run --rm` of the
  baked layer + image checks. Build-scope; no deploy state.
- **`ov eval live <image>`** — runs all three sections against a
  *running* deployment, substituting deploy-time variables
  (`${HOST_PORT:N}`, `${VOLUME_PATH:name}`, `${CONTAINER_IP}`,
  `${ENV_*}`) so the same check survives port remaps and volume
  rebindings.
- **`ov eval run <bed>`** — the canonical R10 acceptance gate.
  Picks a `kind: eval` bed in `eval.yml` (a disposable deploy
  carrying `disposable: true`) and runs build → eval image → deploy
  → eval live → fresh `ov update` → eval live again → teardown.
  Pick the bed whose kind matches what you changed: `eval-pod`,
  `eval-local`, `eval-k3s-vm`, `eval-android-emulator-pod`.
  `ov eval run --all-beds` iterates the catalog.

Exit codes are goss-style: `0` = all checks passed, `1` =
infra/usage error (the eval never reached a verdict), `2` =
checks failed. R10 automation treats `1` as "did not run",
not "failed".

**Agents drive these beds.** Claude Code sub-agents (`eval-bed-runner`,
`deploy-verifier`) and dynamic workflows (`/verify-beds`,
`/audit-deploy-configs`) run `ov eval run`/`live`/`image` against the
existing beds and return verbatim pass/fail — the same disposable-bed
verification, whether you run it or your agent does. → `/ov-internals:agents`.

Eleven live-container probe verbs — authorable inline as
declarative checks inside any `eval:` block (`cdp: eval`, `wl:
screenshot`, `dbus: call`, `vnc: status`, `mcp: list-tools`, `adb:
getprop`, `appium: click`, …):

- `ov eval cdp` — Chrome DevTools Protocol (open, click, eval JS,
  screenshot).
- `ov eval wl` — Wayland / sway / labwc automation; `wl overlay`
  for fullscreen recording overlays.
- `ov eval dbus` — D-Bus method calls and signal subscriptions.
- `ov eval vnc` — RFB handshake, pointer/keyboard, clipboard,
  screenshot.
- `ov eval mcp` — Model Context Protocol clients (list-tools,
  list-resources, read-resource, call-tool).
- `ov eval spice` — SPICE display protocol with guest-agent socket.
- `ov eval libvirt` — libvirt API (VM info, screenshot, send-key,
  QMP, snapshots, event stream).
- `ov eval record` — terminal asciinema or desktop ffmpeg.
- `ov eval k8s` — Kubernetes probes (nodes, pods, ingress,
  wait-ready, storageclass, addons, raw kubectl).
- `ov eval adb` — Android Debug Bridge (devices, shell, install,
  getprop, screencap, logcat, wait-for-device).
- `ov eval appium` — W3C WebDriver session lifecycle, find, click,
  send-keys, screenshot.

`ov feature {list, pending, validate}` authors and validates
Gherkin-shaped descriptions on the same entries.

→ `/ov-eval:eval`, `/ov-eval:cdp`, `/ov-eval:wl`, `/ov-eval:dbus`,
`/ov-eval:vnc`, `/ov-eval:spice`, `/ov-eval:libvirt`,
`/ov-eval:record`, `/ov-eval:eval-k8s`, `/ov-eval:adb`,
`/ov-eval:appium`, `/ov-eval:android`.

*This is simplified, not tougher than Molecule — Molecule wires
deeper into Ansible roles; `ov` gives you build-scope and
deploy-scope checks from one DSL and the same disposable bed
abstraction across pod / vm / k8s / local / android targets.*

### Author with AI

> No equivalent in the four-tool comparison. `ov`-specific.

The AI iteration harness sits on top of `kind: eval` and adds three
overlay kinds:

- **`kind: ai`** — reusable AI-CLI catalog (`claude`, `codex`,
  `gemini`, …). Each entry declares a command, a version probe, an
  output format (typically `stream-json`), and credential paths.
  The harness parses each NDJSON line into `iteration[].runner_event`.
- **`kind: recipe`** — deterministic test specification: scenarios,
  each with a `pod:` declaring the container its probes target.
  Pure check catalogs and BDD descriptions; no AI involved here.
- **`kind: score`** — runner config naming the AI, the target
  `eval-sandbox`, the recipes, the plateau iteration count, the
  prompt, and the watchdog timeout. `ov eval run <score>` runs the
  multi-hour benchmark: AI reads scope (`ov eval scope`) + prior
  tag (`ov eval last-tag`) + live results → rebuilds + redeploys →
  harness re-scores → continues until plateau detection or the
  watchdog fires. Progressive recipe disclosure means the AI sees
  recipes one at a time as it earns them.

Cross-cutting: **`ov mcp serve`** is the MCP gateway. Every leaf
Kong command auto-exposes as an MCP tool (Streamable HTTP or
stdio), so Claude Code, Codex, or any MCP client drives the full
`ov` surface over RPC. `--read-only` filters destructive tools;
auto-fallback to `overthinkos/overthink` when no project is wired
(opt out with `--no-default-repo`).

→ `/ov-eval:eval`, `/ov-build:ov-mcp-cmd`, `/ov-tools:ov-mcp`,
`/ov-coder:claude-code`, `/ov-coder:codex`, `/ov-coder:gemini`.

### Manage

> Ops verbs: cleanup, diagnostics, schema upgrades, runtime config,
> host-side aliases.

- `ov clean` — prune build artifacts by CalVer retention
  (`keep_images`, `keep_eval_runs`); sweeps stale makepkg
  leftovers. Label-CalVer wins over tag-CalVer.
- `ov doctor` — host dependency check (`podman`/`docker`/`libvirt`/
  `qemu`/`gnupg`/`gocryptfs`/`tailscale`/…).
- `ov reap-orphans` — find ephemeral deployments whose underlying
  pod/vm/scope is gone and remove the stale quadlet.
- `ov migrate` — single idempotent chain to the latest CalVer
  schema. Auto-invoked on remote-cache downloads. The
  `LatestSchemaVersion()` gate hard-errors newer-than-binary
  configs.
- `ov settings {get, set, list, reset, path, migrate-secrets}` —
  engine (`engine.build podman|docker`), secret backend, host
  aliases (`hosts.<name> user@machine`), VM backend.
- `ov version` — print computed CalVer tag.
- `ov tmux {ls, attach}` — drive tmux sessions inside containers.
- `ov ssh tunnel {spice, vnc, …}` — forward SPICE/VNC/unix sockets
  from a remote libvirt host to the local machine.
- `ov alias install` — register image-scoped shell aliases
  (bash/zsh/fish) so `<image>` on the host transparently runs
  inside the container.
- `ov udev install/remove` — host-side udev rules for GPU device
  access (CDI symlinks).

→ `/ov-core:clean`, `/ov-core:ov-doctor`, `/ov-core:ov-update`,
`/ov-build:migrate`, `/ov-build:settings`, `/ov-core:ssh`,
`/ov-automation:tmux`, `/ov-automation:alias`,
`/ov-automation:udev`.

## Command reference

The `ov` CLI has 29 top-level verbs across three modes with disjoint
input sets — **build mode** (`ov image …` reads `overthink.yml`),
**test mode** (`ov eval …` reads OCI labels + `deploy.yml` overlays,
never `overthink.yml`), and **deploy mode** (everything else reads
OCI labels + `deploy.yml`) — plus the cross-mode `ov mcp serve`
gateway exposing the entire surface as MCP tools.

| Area | Commands | Skill |
|---|---|---|
| **Image (build mode)** | `ov image {build, generate, validate, merge, new, inspect, list, pull, reconcile}` | `/ov-image:image` + `/ov-build:build`, `/ov-build:generate`, `/ov-build:validate`, `/ov-build:merge`, `/ov-build:new`, `/ov-build:inspect`, `/ov-build:list`, `/ov-build:pull`, `/ov-build:reconcile` |
| **Image authoring (MCP-first)** | `ov image {set, add-layer, rm-layer, fetch, refresh, write, cat}` and `ov layer {set, add-rpm, add-deb, add-pac, add-aur}` | `/ov-image:image` "Authoring" + `/ov-image:layer` |
| **Deployment** | `ov deploy {add, del, sync, from-image, export, import, show, reset, status, path}`; `ov config`; `ov start`, `ov stop`, `ov restart`, `ov update`, `ov remove` | `/ov-core:deploy`, `/ov-core:ov-config`, `/ov-core:start`, `/ov-core:stop`, `/ov-core:ov-update`, `/ov-core:remove`, `/ov-local:local-deploy`, `/ov-kubernetes:kubernetes`, `/ov-internals:vm-deploy-target` |
| **Runtime** | `ov shell`, `ov cmd`, `ov service`, `ov status`, `ov logs`, `ov tmux` | `/ov-core:shell`, `/ov-core:cmd`, `/ov-core:service`, `/ov-core:ov-status`, `/ov-core:logs`, `/ov-automation:tmux` |
| **Test + probes** | `ov eval {image, live, run}` + the 11 live probe verbs (`cdp`, `wl`, `dbus`, `vnc`, `mcp`, `record`, `spice`, `libvirt`, `k8s`, `adb`, `appium`); `ov feature {list, pending, validate}` | `/ov-eval:eval`, `/ov-eval:cdp`, `/ov-eval:wl`, `/ov-eval:dbus`, `/ov-eval:vnc`, `/ov-eval:spice`, `/ov-eval:libvirt`, `/ov-eval:record`, `/ov-eval:eval-k8s`, `/ov-eval:adb`, `/ov-eval:appium` |
| **MCP gateway** | `ov mcp {serve, ping, servers, list-tools, list-resources, list-prompts, call, read}` | `/ov-build:ov-mcp-cmd`, `/ov-tools:ov-mcp` |
| **VM** | `ov vm {build, create, start, stop, destroy, snapshot, clone, console, ssh, import, list}` | `/ov-vm:vm`, `/ov-vm:vms-catalog`, `/ov-internals:vm-deploy-target` |
| **Schema migration** | `ov migrate` (single idempotent chain) | `/ov-build:migrate` |
| **Secrets & config** | `ov secrets`, `ov settings`, `ov alias`, `ov udev` | `/ov-build:secrets`, `/ov-build:settings`, `/ov-automation:alias`, `/ov-automation:udev` |
| **Host & admin** | `ov doctor`, `ov clean`, `ov reap-orphans`, `ov ssh`, `ov version` | `/ov-core:ov-doctor`, `/ov-core:clean`, `/ov-core:ssh`, `/ov-core:ov-version` |

**Global flags** (apply to every command):

- `-C <dir>` / `--dir <dir>` / `OV_PROJECT_DIR=<dir>` — override the
  project directory.
- `--repo <OWNER/REPO[@REF]>` / `OV_PROJECT_REPO=…` — read
  `overthink.yml` from a remote git repo. Bare `owner/repo`
  auto-prefixes `github.com/`; the literal `default` expands to
  `overthinkos/overthink`. Cached in `~/.cache/ov/repos/`. Mutually
  exclusive with `--dir`.
- `--host <alias|user@machine[:port]>` / `OV_HOST=…` — re-exec the
  command on a remote host over SSH. Commands marked LocalOnly
  (`settings`, `version`, `ssh tunnel`) always run locally.

## Catalogs

Content lives in the working tree and in the skill index — pointers,
not enumerations:

- **Layer library** (`layers/` + submodule `image/<distro>/layers/`,
  187 layers total). Foundation: `/ov-distros:*` (40 skills — base
  OS, GPU runtime, bootc, per-distro builders),
  `/ov-languages:*`, `/ov-infrastructure:*` (22), `/ov-tools:*`
  (19). Per-pod: `/ov-jupyter:*`, `/ov-coder:*` (33),
  `/ov-selkies:*` (40), `/ov-openclaw:*`, `/ov-versa:*`,
  `/ov-ollama:*`, `/ov-openwebui:*`, `/ov-comfyui:*`,
  `/ov-immich:*`, `/ov-hermes:*`, `/ov-filebrowser:*`.
- **Image catalog** (`image.yml` + `image/*/image.yml`) — 53 images,
  39 enabled by default. Same plugin namespaces; per-pod images
  carry an MCP server hint in `plugins/README.md`.
- **VM catalog** (`vm.yml` + `image/cachyos/vm.yml`) — cloud_image
  + bootc entries. → `/ov-vm:vms-catalog`.
- **Deploy-target catalog** — pod / vm / k8s / local / android.
  Each has a dedicated kind file.
- **Eval bed catalog** (`eval.yml`) — `kind: eval` beds for R10,
  plus `kind: recipe` / `score` / `ai` for the AI harness.
  → `/ov-eval:eval`.

Layers used by only one image family are vendored in that
`image/<distro>` submodule (e.g. bootc-exclusive set in
`image/bootc`, `ghostty`/`keepassxc-keyring` in `image/cachyos`,
`arch-*-test` fixtures in `image/arch`). Shared layers are pulled by
`@github` ref.

**Composition meta-layers** — `sway-desktop`, `sway-desktop-vnc`,
`selkies-desktop`, `bootc-base`, `openclaw-full`, `openclaw-full-ml`,
`python-ml`, `jupyter-ml`, `unsloth-studio` bundle curated layer
sets.

**Data layers / data images** — `data:` block in `layer.yml` stages
files at `/data/<volume>/`; `ov config --bind <volume>` provisions
them at deploy time; `ov update` merges new data non-destructively.
`data_image: true` scratch-based images carry data + OCI labels,
consumed via `ov config --data-from <data-image>`.

See `plugins/README.md` for the authoritative skill index and
`CHANGELOG.md` for the dated history of cutovers.

## Troubleshooting

Each entry points to the canonical skill — details belong there,
not here.

| Symptom | First step |
|---------|-----------|
| Service won't start | `ov status <image>` then `ov logs <image>` (`/ov-core:ov-status`, `/ov-core:logs`) |
| Quadlet out of sync with deploy.yml | `ov config <image> --update-all` (`/ov-core:ov-config`) |
| Build cache stale | `ov image build --no-cache <image>` (`/ov-build:build`) |
| Chrome stuck or crash-looping | `/ov-selkies:chrome` Resource Caps & Circuit Breaker section |
| Encrypted volume locked at boot | `ov config mount` waits for keyring unlock automatically — zero CPU, event-driven (`/ov-automation:enc`) |
| GPU not detected | `ov doctor` then `/ov-automation:udev` |
| Tunnel not appearing on a new instance | Tunnel config is `deploy.yml`-only — add manually per instance (`/ov-core:deploy`) |
| Service built fine but broken in production | `ov eval live <image>` runs the baked layer + image + deploy checks (`/ov-eval:eval`) |
| `ov vm build` fails: "no kind:vm entity in vm.yml" | Declare a `kind: vm` entity (`/ov-vm:vms-catalog`) |
| SPICE console blank on cloud_image VM | Known `simpledrm → qxldrmfb` race under UEFI; switch to `firmware: bios` (`/ov-vm:arch`) |
| `ov deploy add vm:<name>` errors "VM does not exist" | Run `ov vm create <name>` first — VM deploy is not auto-provisioning (`/ov-core:deploy`) |
| Resolver "referenced at multiple versions" warning | `ov image reconcile` aligns the cross-repo `@github` pins (`/ov-build:reconcile`) |
| `ov image pull` says "image is not available locally" | `ov image pull` accepts short name + project, fully-qualified ref, or `@github` remote ref. See `/ov-build:pull` |
| Newer-than-binary config rejected at load | `ov migrate` brings the project to the latest schema CalVer (`/ov-build:migrate`) |
| Schema/format change won't apply | `ov migrate` is idempotent; auto-invoked on remote-cache fetches |

## Adding a layer

```bash
ov image new layer my-layer             # Scaffold the directory
# Edit layers/my-layer/layer.yml        # Declare packages, deps, env, ports,
#                                       # services, eval probes, and tasks:
#                                       # (see /ov-image:layer for the verb catalog)
# Optionally add pixi.toml / package.json / Cargo.toml for auto-detected builders.

# Add to an image's layer list in overthink.yml (or image.yml):
#   layer: [..., my-layer]

ov image build my-image                 # Build it
ov eval image my-image                  # Run the baked checks
```

`/ov-image:layer` is the canonical reference for the eight `task:`
verbs (`cmd`, `mkdir`, `copy`, `write`, `link`, `download`,
`setcap`, `build`), the unified `service:` schema, `vars:`
substitution, YAML anchors, and execution-order rules.
`/ov-eval:eval` covers the matcher forms, runtime variable table,
gold-standard pattern (`layers/redis/layer.yml`), and the 10
authoring gotchas.

## Works with Claude Code

Overthink works hand-in-hand with
[Claude Code](https://claude.com/claude-code). The bundled
[plugins/](plugins/) directory provides skills that teach Claude
how to compose, build, deploy, and manage your container images.
Every layer, every image, every command has a dedicated skill.

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

Representative subset; see `plugins/.claude-plugin/marketplace.json`
for the full 25-plugin catalog. Clone with submodules to get the
plugins directory: `git clone --recurse-submodules
https://github.com/overthinkos/overthink.git`.

**MCP gateway as the universal channel.** `ov mcp serve` exposes
every `ov` CLI leaf as an MCP tool (Streamable HTTP or stdio), so
the agent reaches the full build / deploy / test surface over RPC.
Per-image MCP servers (chrome-devtools-mcp, jupyter-mcp,
marimo-mcp, ov-mcp) auto-discover via `mcp_provides:` when their
containers are running.

**Sub-agents, dynamic workflows, and agent teams.** Beyond skills, the
project ships Claude Code **sub-agents** (`plugins/internals/agents/`):
executors `eval-bed-runner` and `deploy-verifier` that drive the `ov eval`
beds and return verbatim proof, plus enforcers `root-cause-analyzer`,
`testing-validator`, and `layer-validator`. Two **dynamic workflows**
(`.claude/workflows/`) fan the work out — `/verify-beds` runs every
`kind: eval` bed as the R10 gate, `/audit-deploy-configs` evaluates your
deploy configs — and the same agent definitions reuse as **agent-team**
teammates. Whether you drive `ov` from the keyboard or hand it to an AI,
testing and verifying deployments uses the one surface.
→ `/ov-internals:agents`.

See [CLAUDE.md](CLAUDE.md) for the complete system specification,
[plugins/README.md](plugins/README.md) for the full skill index,
and [CHANGELOG.md](CHANGELOG.md) for dated history (by policy,
never duplicated here or in skills).

## License

MIT
