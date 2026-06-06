# Overthink

**The container management experience for you and your agents.**

Describe what you need in a simple layer list, and `ov` composes it
into optimized multi-stage container images — from an interactive
dev shell to a running service to a systemd unit to a bootable VM,
to an agent's desktop running inside a candybox. Works the
same way whether you're at the keyboard or your agents are
driving.

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

> **New here?** [VISION.md](VISION.md) is the one-page thesis — why Overthink
> secures the box and fills it with the whole candy store, and where the
> factory is heading.

## Table of contents

- [What's in the chocolate factory](#whats-in-the-chocolate-factory)
- [Core concepts](#core-concepts)
- [Why Overthink?](#why-overthink)
- [Install](#install)
- [Quickstart](#quickstart)
- [Lifecycle](#lifecycle)
  - [Build](#build)
  - [Run](#run)
  - [Deploy](#deploy)
  - [Evaluate](#evaluate)
  - [Author with agents](#author-with-agents)
  - [Manage](#manage)
- [Command reference](#command-reference)
- [Catalogs](#catalogs)
- [Troubleshooting](#troubleshooting)
- [Adding a layer](#adding-a-layer)
- [Works with Claude Code](#works-with-claude-code)
- [License](#license)

## What's in the chocolate factory

`ov` is a Swiss Chocolate Factory. Each production line is a stage of
the container lifecycle — **build, run, deploy, evaluate** — driven
from one config and one mental model:

| Reach for `ov` when you want to…                            | …and you get                                       | Stage                 |
|-------------------------------------------------------------|----------------------------------------------------|-----------------------|
| compose a reproducible image from a layer list              | `kind: box` / `kind: candy`, `ov box build`    | [Build](#build)       |
| run one or more containers as a managed pod                 | `kind: pod`, `ov deploy add`, `ov start`           | [Run](#run)           |
| apply the same layers to a host, VM, k8s, or Android device | `ov deploy add` + `target:`                        | [Deploy](#deploy)     |
| prove a config actually works, end-to-end                   | `kind: eval`, `ov eval run`, baked `eval:` checks  | [Evaluate](#evaluate) |

The same `ov` drives two further stages — it
[authors layers and images with an agent in the loop](#author-with-agents)
and [manages](#manage) the running lifecycle (cleanup, diagnostics,
schema upgrades, runtime config).

> One `candy.yml`, one image, one `deploy.yml`, and one `kind: eval`
> bed drive all four stages — the build, the local run, the remote
> deploy, and the test harness. The binary that wires them together is
> also an MCP server, so your agent reaches every verb over the
> same RPC.

## Core concepts

A handful of ideas recur everywhere. Four of them are the heart of
Overthink — **layers & images**, **candyboxing**, **Risk Driven
Development**, and the **build → run → deploy → evaluate** lifecycle —
and the rest is the schema vocabulary that ties them together.

### Layers & images

Overthink treats container images as composable building blocks. Each
**layer** is a self-contained unit; an **image** is an ordered list of
layers on top of a base. `ov` resolves the dependency graph, generates
multi-stage Containerfiles with cache mounts, and builds in the right
order — handling the hard parts so you (and your agents) don't
have to.

- **Layer** (`kind: candy` in `candy.yml`) — packages (per-distro),
  tasks (eight verbs: `cmd`/`mkdir`/`copy`/`write`/`link`/`download`/
  `setcap`/`build`), services (one unified `service:` list — see
  init-system polymorphism below), volumes, env, ports, eval probes,
  `env_provide`/`env_require`/`mcp_provide`/`mcp_accept` for
  cross-container discovery, plus a `version:` CalVer.
  → `/ov-image:layer`.
- **Image** (`kind: box`) — base + ordered layer list. Multi-stage
  Containerfile, content-derived `org.overthinkos.version` OCI label,
  stable cache. → `/ov-image:image`.

### Candyboxing

Secure the *box* — a disposable, rootless container or VM with real,
kernel-enforced isolation — then hand your agent the whole candy store
inside it: every `ov` verb, every layer, every `ov eval` probe, a real
registry, a real GPU. Far more capability than a locked-down sandbox, and
a mistake costs one rebuild.
→ [VISION.md](VISION.md) (why), CLAUDE.md "Candyboxing" (the rule),
`/ov-internals:disposable` (the lifecycle boundary).

### Risk Driven Development (early)

Prove the riskiest unknown — above all whether a particular *combination*
of layers, at their latest versions, actually builds and runs together —
empirically on a disposable `kind: eval` bed EARLY, before a design rests
on it. `ov eval` makes that proof cheap, for agents and humans alike.
→ [VISION.md](VISION.md) (why), CLAUDE.md "Risk Driven Development (RDD)"
(the rule), `/ov-eval:eval` (usage).

### Agent Driven Development (acceptance)

What an image is *supposed* to do is written as runnable Gherkin scenarios
on the layer that provides the behaviour, baked into the image as a label.
A step with a check verb is verified deterministically; a prose-only step
is graded by an **agent** probing the live deployment. Author with
`ov candy add-scenario`, run with `ov box feature run` /
`ov eval feature run`, or let the `ov eval run <score>` AI loop drive it to
green. The spec is the test, and agents both write it and grade it.
→ [VISION.md](VISION.md) (why), CLAUDE.md "Agent Driven Development (ADD)"
(the rule), `/ov-eval:eval` (usage).

### Build → run → deploy → evaluate

The container lifecycle is four verbs, and the same declarative inputs
flow through all of them:

- **Build** — a `kind: box` composes layers into a reproducible
  multi-stage image.
- **Run** — a `kind: pod` brings containers up as systemd-managed
  Podman quadlets.
- **Deploy** — `ov deploy add` applies the same layers to a host, VM,
  k8s cluster, or Android device via `target:`.
- **Evaluate** — `kind: eval` beds and baked `eval:` checks prove any
  image or deployment works end-to-end.

See [Lifecycle](#lifecycle) for the full verb families (plus
authoring-with-agents and management).

### Schema kinds

Beyond `layer` and `image`, the schema has these kinds — each a `kind:`
discriminator in its file:

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
  (via `box:`) or remote/physical adb endpoint. `apk:` is a layer
  package format scoped to Android targets. → `/ov-eval:android`.
- **Deploy** (`kind: deploy`) — a named deployment of one of the
  kinds above to a `target:` (`pod` default, `vm`, `k8s`, `local`,
  `android`). Carries env overlays, port remaps, volume backings,
  sidecars, tunnels, secrets, and the `disposable: true` opt-in.
  → `/ov-core:deploy`.
- **Eval** (`kind: eval`) — a *disposable* deploy used as an R10 test
  bed: `ov eval run <bed>` runs build → deploy → probe →
  fresh-update → tear-down. The `kind: recipe` / `kind: score` /
  `kind: ai` overlays drive the agent-iteration harness on top.
  → `/ov-eval:eval`.

### Cross-cutting rules

**`overthink.yml` is the single project entry point.** Every other
file is composed in via `import:` — a bare string for a flat
same-repo import (`build.yml`, `box.yml`, `vm.yml`, `pod.yml`,
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

## Why Overthink?

Containers are a great idea with rough edges. Real-world needs pile
up fast: GPU passthrough with the right driver stack, containers
that need `/dev/kvm` or virtualization access without blanket
`--privileged`, multiple services managed together, encrypted
volumes, VNC or browser-streamed desktops, device permissions that
don't compromise your host. Each is solvable — but solving them all
at once, reliably, across images, is where things get hard. And if
your agent has to build and manage these containers too, the
complexity compounds.

Overthink treats container images as composable building blocks (see
[Core concepts](#core-concepts)) — handling the hard parts so you (and
your agents) don't have to.

**Testing and evaluating deployment configs is a first-class goal —
for agents and humans.** A deploy config is only useful if you can prove
it works, so any image or deployment is self-verifiable end-to-end — the
same surface whether a human drives it at the keyboard or an agent drives
it autonomously. See [Evaluate](#evaluate) for the framework and
[Works with Claude Code](#works-with-claude-code) for the agents and
workflows. → `/ov-eval:eval`, `/ov-internals:agents`.

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

**Sandboxed agent desktops.** [Candyboxing](#candyboxing) applied to a
whole desktop: `/ov-openclaw:openclaw-desktop` is the all-in-one CachyOS
streaming desktop — Selkies desktop + openclaw-full gateway + agent CLIs
(claude-code, codex, gemini) + CPU ollama + nested `ov`. The agent (or the
user) builds images, launches nested rootless pods, and creates libvirt
VMs from a terminal inside the browser-accessible desktop — uid 1000, no
`--privileged`, no added capabilities.

## Install

**Recommended — Go install** (requires Go 1.25.3+):

```bash
go install github.com/overthinkos/overthink/ov@latest
```

This puts `ov` in your `$GOPATH/bin`. Create an `overthink.yml` and
a `candy/` directory and you're done. Legacy projects (predating
the unified schema, the `kind:` discriminators, or the singular
field names) convert in one shot with `ov migrate` — a single
idempotent chain to the latest CalVer schema. See `/ov-build:migrate`.

**Full project bootstrap** (to build images from this repo):

```bash
git clone --recurse-submodules https://github.com/overthinkos/overthink.git
cd overthink
task build:ov         # on Arch: delegates to makepkg -si; elsewhere: portable install to ~/.local/bin/ov
ov box build        # build everything
```

**Arch / CachyOS / Manjaro** — install system-wide via `pacman`, building this
repo's bundled `overthink-git` PKGBUILD (it is LOCAL-ONLY — NOT published to the
AUR):

```bash
cd pkg/arch && makepkg -si     # build + pacman-install overthink-git from this repo
# or, equivalently, from the repo root:
task build:ov                  # pre-installs the AUR-only deps via your AUR helper, then runs makepkg -sefi in pkg/arch
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
ov box build fedora

# Build a CachyOS image (in submodule; ov resolves cross-repo refs)
ov -C image/cachyos image build cachyos

# Drop into an interactive shell
ov shell fedora

# Build and run a GPU-accelerated Jupyter server
ov box build jupyter
ov start jupyter

# Configure as a systemd service (quadlet + secrets + encrypted volumes)
ov config jupyter

# Build a bootable VM disk image
ov box build bazzite               # the kind:image
ov vm build  bazzite-bootc --type qcow2 # the kind:vm
ov vm create bazzite-bootc

# Apply layers directly to your workstation (no container)
ov deploy add host ripgrep
ov deploy add host fedora-coder --with-services --yes
ov deploy del host                  # reverses everything via ReverseOps + ledger

# Run a kind:eval test bed end-to-end (the R10 acceptance gate)
ov eval run eval-pod
```

## Lifecycle

The same six stages cover everything `ov` does — **build, run, deploy,
evaluate, author, manage**. Each maps to a family of `ov` verbs that
share the same declarative inputs.

### Build

> Declarative layer list → reproducible, fully-cached multi-stage
> image.

Each image declares a `base:`, an ordered `candy:` list, a `distro:`
identity, and a `build:` set of package formats. The planner
resolves the dependency graph, generates a multi-stage Containerfile
with cache-mounted package archives + AUR srcdest + pixi/npm/cargo
workdirs, and runs `podman build` (or `docker build` — switch with
`ov settings set engine.build podman`). Like conching chocolate, the
planner grinds every candy smooth — deduplicated, ordered, and
cache-warmed — before it sets into a box. The emitted image carries
OCI labels for every capability it claims: `org.overthinkos.eval`,
`org.overthinkos.init`, `org.overthinkos.version` (content-derived
`EffectiveVersion`, stable across no-op rebuilds), `.ports`, etc.

Commands: `ov box build` (build), `ov box generate` (write
`.build/` only), `ov box validate`, `ov box inspect`,
`ov box list`, `ov box merge`, `ov box pull`,
`ov box reconcile`. MCP-driven authoring — `ov box {set,
add-candy, rm-candy, fetch, refresh, write, cat}`, `ov candy {set,
add-rpm, add-deb, add-pac, add-aur}` — gives agents
comment-preserving YAML edits over RPC.

Cross-repo refs: `import:` items and layer references can name
`@github.com/owner/repo:tag`. The resolver fetches every distinct
`(repo, git-tag)` and arbitrates per per-entity `version:` — same
`version:` across different git tags → silent (re-tag);
different `version:` → warn once and use the newest. `ov box
reconcile` aligns the cross-repo pins when a layer's CalVer moves.

→ `/ov-build:build`, `/ov-build:generate`, `/ov-build:validate`,
`/ov-build:inspect`, `/ov-build:reconcile`, `/ov-image:image`,
`/ov-image:layer`, `/ov-internals:capabilities`.

### Run

> Multiple containers, one declaration, one start command — as
> systemd-native units.

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
  (`selkies-labwc`, `sway-desktop`, `sway-browser-vnc`) bundles a
  Wayland compositor (sway or labwc) + Chrome + `wayvnc` on port
  5900 + Pipewire audio. Browser pane at `:3000`.
- **Per-image MCP servers** — `chrome-devtools-mcp` on `:9224`,
  `jupyter-mcp` at `:8888/mcp`, `marimo-mcp` at `:2718/mcp/server`,
  nested `ov-mcp`. Declared via `mcp_provide:` and auto-discovered
  by consumers (Hermes, Claude Code) through `OV_MCP_SERVERS`.
- **Auto service discovery** — a layer's `env_provide:` declares
  env vars with `{{.ContainerName}}` templates injected into every
  co-deployed container at `ov config` time. Deploy `ollama` and
  every other pod sees `OLLAMA_HOST=http://ov-ollama:11434`.
  `mcp_provide:` works the same way for MCP URLs.
  `env_require:` / `env_accept:` document consumer dependencies
  so `ov config` warns early.

→ `/ov-core:start`, `/ov-core:logs`, `/ov-core:cmd`,
`/ov-core:service`, `/ov-core:ov-status`, `/ov-automation:sidecar`,
`/ov-automation:enc`, `/ov-automation:udev`, `/ov-pod:pod`,
`/ov-selkies:selkies-desktop-layer`, `/ov-selkies:sway`.

### Deploy

> The same `candy.yml` applied to a host, a remote ssh box, a VM, a
> k3s cluster, or an Android device.

`ov deploy add <name> <ref>` is the unified verb; `target:`
discriminates where it lands:

- **`target: pod`** (default) — Podman + quadlet, as in [Run](#run).
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
  via `add_candy:` in `~/.config/ov/deploy.yml`. Ledger at
  `~/.config/overthink/installed/` records every ReverseOp so
  `ov deploy del host` reverses precisely what was applied.
  → `/ov-local:local-deploy`, `/ov-local:local-spec`.
- **`target: android`** — `kind: android` device (in-pod emulator
  via `box:` or remote adb endpoint via `adb: {host: …}`);
  `apk:` packages installed by `apkeep` (Google Play) or pushed
  from committed `.apk` files via goadb. Nested `pod → android`
  mirrors `vm → k8s`. → `/ov-eval:android`, `/ov-eval:adb`.

`ov deploy del`, `ov deploy sync` (apply K8s changes live),
`ov deploy from-box` (source-less deploy from OCI labels), and
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

### Evaluate

> Build → deploy → probe → fresh-update → tear down — disposable beds
> with the same DSL as production deploys.

Tests are first-class. Every `candy.yml` / `box.yml` /
`deploy.yml` can declare an `eval:` block of goss-style checks
(files, packages, services, ports, processes, commands, HTTP, DNS,
mounts, users, groups, kernel params, interfaces, matchers). Checks
bake into a three-section OCI label
(`org.overthinkos.eval` → `{layer, image, deploy}`) so any pulled
image is self-testable without its source repo.

Three execution modes:

- **`ov eval box <image>`** — disposable `podman run --rm` of the
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

**Agents drive these beds.** Claude Code sub-agents
(`eval-bed-runner`, `deploy-verifier`) and dynamic workflows
(`/verify-beds`, `/audit-deploy-configs`) run `ov eval
run`/`live`/`image` against the existing beds and return verbatim
pass/fail — the same disposable-bed verification, whether you run it
or your agent does. → `/ov-internals:agents`.

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
`/ov-eval:record`, `/ov-kubernetes:eval-k8s`, `/ov-eval:adb`,
`/ov-eval:appium`, `/ov-eval:android`.

### Author with agents

> Agents in the loop, authoring and iterating on layers and
> images — `ov`-specific.

The agent iteration harness sits on top of `kind: eval` and
adds three overlay kinds:

- **`kind: ai`** — reusable agent CLI catalog (`claude`,
  `codex`, `gemini`, …). Each entry declares a command, a version
  probe, an output format (typically `stream-json`), and credential
  paths. The harness parses each NDJSON line into
  `iteration[].runner_event`.
- **`kind: recipe`** — deterministic test specification: scenarios,
  each with a `pod:` declaring the container its probes target.
  Pure check catalogs and Gherkin scenario descriptions; no agent
  involved here (the agent grader is opt-in via `ov eval feature run`).
- **`kind: score`** — runner config naming the agent, the
  target `eval-sandbox`, the recipes, the plateau iteration count,
  the prompt, and the watchdog timeout. `ov eval run <score>` runs
  the multi-hour benchmark: the agent reads scope
  (`ov eval scope`) + prior tag (`ov eval last-tag`) + live results →
  rebuilds + redeploys → harness re-scores → continues until plateau
  detection or the watchdog fires. Progressive recipe disclosure
  means the agent sees recipes one at a time as it earns them.

Cross-cutting: **`ov mcp serve`** is the MCP gateway. Every leaf
Kong command auto-exposes as an MCP tool (Streamable HTTP or
stdio), so Claude Code, Codex, or any MCP client drives the full
`ov` surface over RPC. `--read-only` filters destructive tools;
auto-fallback to `overthinkos/overthink` when no project is wired
(opt out with `--no-default-repo`).

→ `/ov-eval:eval`, `/ov-build:ov-mcp-cmd`, `/ov-coder:ov-mcp`,
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
input sets — **build mode** (`ov box …` reads `overthink.yml`),
**test mode** (`ov eval …` reads OCI labels + `deploy.yml` overlays,
never `overthink.yml`), and **deploy mode** (everything else reads
OCI labels + `deploy.yml`) — plus the cross-mode `ov mcp serve`
gateway exposing the entire surface as MCP tools.

| Area | Commands | Skill |
|---|---|---|
| **Image (build mode)** | `ov box {build, generate, validate, merge, new, inspect, list, pull, reconcile}` | `/ov-image:image` + `/ov-build:build`, `/ov-build:generate`, `/ov-build:validate`, `/ov-build:merge`, `/ov-build:new`, `/ov-build:inspect`, `/ov-build:list`, `/ov-build:pull`, `/ov-build:reconcile` |
| **Image authoring (MCP-first)** | `ov box {set, add-candy, rm-candy, fetch, refresh, write, cat}` and `ov candy {set, add-rpm, add-deb, add-pac, add-aur}` | `/ov-image:image` "Authoring" + `/ov-image:layer` |
| **Deployment** | `ov deploy {add, del, sync, from-box, export, import, show, reset, status, path}`; `ov config`; `ov start`, `ov stop`, `ov restart`, `ov update`, `ov remove` | `/ov-core:deploy`, `/ov-core:ov-config`, `/ov-core:start`, `/ov-core:stop`, `/ov-core:ov-update`, `/ov-core:remove`, `/ov-local:local-deploy`, `/ov-kubernetes:kubernetes`, `/ov-internals:vm-deploy-target` |
| **Runtime** | `ov shell`, `ov cmd`, `ov service`, `ov status`, `ov logs`, `ov tmux` | `/ov-core:shell`, `/ov-core:cmd`, `/ov-core:service`, `/ov-core:ov-status`, `/ov-core:logs`, `/ov-automation:tmux` |
| **Test + probes** | `ov eval {image, live, run}` + the 11 live probe verbs (`cdp`, `wl`, `dbus`, `vnc`, `mcp`, `record`, `spice`, `libvirt`, `k8s`, `adb`, `appium`); `ov feature {list, pending, validate}` | `/ov-eval:eval`, `/ov-eval:cdp`, `/ov-eval:wl`, `/ov-eval:dbus`, `/ov-eval:vnc`, `/ov-eval:spice`, `/ov-eval:libvirt`, `/ov-eval:record`, `/ov-kubernetes:eval-k8s`, `/ov-eval:adb`, `/ov-eval:appium` |
| **MCP gateway** | `ov mcp {serve, ping, servers, list-tools, list-resources, list-prompts, call, read}` | `/ov-build:ov-mcp-cmd`, `/ov-coder:ov-mcp` |
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

- **Layer library** (`candy/` + submodule `image/<distro>/candy/`,
  187 layers total). Foundation: `/ov-distros:*` (40 skills — base
  OS, GPU runtime, bootc, per-distro builders),
  `/ov-languages:*`, `/ov-infrastructure:*` (22), `/ov-tools:*`
  (19). Per-pod: `/ov-jupyter:*`, `/ov-coder:*` (33),
  `/ov-selkies:*` (40), `/ov-openclaw:*`, `/ov-versa:*`,
  `/ov-ollama:*`, `/ov-openwebui:*`, `/ov-comfyui:*`,
  `/ov-immich:*`, `/ov-hermes:*`, `/ov-filebrowser:*`.
- **Image catalog** (`box.yml` + `image/*/box.yml`) — 53 images,
  39 enabled by default. Same plugin namespaces; per-pod images
  carry an MCP server hint in `plugins/README.md`.
- **VM catalog** (`vm.yml` + `image/cachyos/vm.yml`) — cloud_image
  + bootc entries. → `/ov-vm:vms-catalog`.
- **Deploy-target catalog** — pod / vm / k8s / local / android.
  Each has a dedicated kind file.
- **Eval bed catalog** (`eval.yml`) — `kind: eval` beds for R10,
  plus `kind: recipe` / `score` / `ai` for the agent harness.
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

**Data layers / data images** — `data:` block in `candy.yml` stages
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
| Build cache stale | `ov box build --no-cache <image>` (`/ov-build:build`) |
| Chrome stuck or crash-looping | `/ov-selkies:chrome` Resource Caps & Circuit Breaker section |
| Encrypted volume locked at boot | `ov config mount` waits for keyring unlock automatically — zero CPU, event-driven (`/ov-automation:enc`) |
| GPU not detected | `ov doctor` then `/ov-automation:udev` |
| Tunnel not appearing on a new instance | Tunnel config is `deploy.yml`-only — add manually per instance (`/ov-core:deploy`) |
| Service built fine but broken in production | `ov eval live <image>` runs the baked layer + image + deploy checks (`/ov-eval:eval`) |
| `ov vm build` fails: "no kind:vm entity in vm.yml" | Declare a `kind: vm` entity (`/ov-vm:vms-catalog`) |
| SPICE console blank on cloud_image VM | Known `simpledrm → qxldrmfb` race under UEFI; switch to `firmware: bios` (`/ov-vm:arch`) |
| `ov deploy add vm:<name>` errors "VM does not exist" | Run `ov vm create <name>` first — VM deploy is not auto-provisioning (`/ov-core:deploy`) |
| Resolver "referenced at multiple versions" warning | `ov box reconcile` aligns the cross-repo `@github` pins (`/ov-build:reconcile`) |
| `ov box pull` says "image is not available locally" | `ov box pull` accepts short name + project, fully-qualified ref, or `@github` remote ref. See `/ov-build:pull` |
| Newer-than-binary config rejected at load | `ov migrate` brings the project to the latest schema CalVer (`/ov-build:migrate`) |
| Schema/format change won't apply | `ov migrate` is idempotent; auto-invoked on remote-cache fetches |

## Adding a layer

```bash
ov box new candy my-layer             # Scaffold the directory
# Edit candy/my-layer/candy.yml        # Declare packages, deps, env, ports,
#                                       # services, eval probes, and tasks:
#                                       # (see /ov-image:layer for the verb catalog)
# Optionally add pixi.toml / package.json / Cargo.toml for auto-detected builders.

# Add to an image's layer list in overthink.yml (or box.yml):
#   candy: [..., my-layer]

ov box build my-image                 # Build it
ov eval box my-image                  # Run the baked checks
```

`/ov-image:layer` is the canonical reference for the eight `task:`
verbs (`cmd`, `mkdir`, `copy`, `write`, `link`, `download`,
`setcap`, `build`), the unified `service:` schema, `vars:`
substitution, YAML anchors, and execution-order rules.
`/ov-eval:eval` covers the matcher forms, runtime variable table,
gold-standard pattern (`candy/redis/candy.yml`), and the 10
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
the agent reaches the full build / deploy / test surface over
RPC. Per-image MCP servers (chrome-devtools-mcp, jupyter-mcp,
marimo-mcp, ov-mcp) auto-discover via `mcp_provide:` when their
containers are running.

**Sub-agents, dynamic workflows, and agent teams.** Beyond skills, the
project ships Claude Code **sub-agents** (`plugins/internals/agents/`):
executors `eval-bed-runner` and `deploy-verifier` that drive the `ov eval`
beds and return verbatim proof, plus enforcers `root-cause-analyzer`,
`testing-validator`, and `layer-validator`. Two **dynamic workflows**
(`.claude/workflows/`) fan the work out — `/verify-beds` runs every
`kind: eval` bed as the R10 gate, `/audit-deploy-configs` evaluates your
deploy configs — and the same agent definitions reuse as **agent-team**
teammates. Whether you drive `ov` from the keyboard or hand it to an
agent, testing and verifying deployments uses the one surface.
→ `/ov-internals:agents`.

See [VISION.md](VISION.md) for the long-term thesis and direction,
[CLAUDE.md](CLAUDE.md) for the project's rules and mandates,
[plugins/README.md](plugins/README.md) for the full skill index (usage
and architecture live in the skills), and [CHANGELOG.md](CHANGELOG.md)
for dated history (by policy, never duplicated here or in skills).

## License

MIT
