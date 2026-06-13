# OpenCharly

**The candy factory for you and your agents.**

Describe what you need in a simple candy list, and `charly` composes it
into optimized multi-stage **boxes** (container images) — from an
interactive dev shell to a running service to a systemd unit to a
bootable VM, to an agent's desktop running inside a candybox. Works the
same way whether you're at the keyboard or your agents are
driving.

187 candies across this repo and its submodules. 53 box definitions
(39 enabled by default). 2 VM definitions, 2 Android devices, and a
growing catalog of `kind: local` host templates and `kind: eval`
test beds. Docker and Podman. `linux/amd64`. Fedora, Debian, Ubuntu,
Arch, and CachyOS. One CLI: `charly` (29 top-level verbs). Every candy,
box, VM, and command has a dedicated recipe card (skill) — ~290 skills
across 25 plugins. See `plugins/README.md` for the full index.

*The name comes from the German "überdenken" — to think something
through carefully. Not quite the same as the English "opencharly,"
but let's be honest: `charly` really is trying its best to opencharly
absolutely everything.*

> **New here?** [VISION.md](VISION.md) is the one-page thesis — why OpenCharly
> secures the box and fills it with the whole candy store, and where the
> factory is heading.

## Table of contents

- [What's in the chocolate factory](#whats-in-the-chocolate-factory)
- [Core concepts](#core-concepts)
- [Why OpenCharly?](#why-opencharly)
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
- [Adding a candy](#adding-a-candy)
- [Works with Claude Code](#works-with-claude-code)
- [License](#license)

## What's in the chocolate factory

`charly` is a Swiss Chocolate Factory. Each production line is a stage of
the lifecycle — **build, run, deploy, evaluate** — driven
from one config and one mental model:

| Reach for `charly` when you want to…                            | …and you get                                       | Stage                 |
|-------------------------------------------------------------|----------------------------------------------------|-----------------------|
| compose a reproducible box from a candy list                | `kind: box` / `kind: candy`, `charly box build`    | [Build](#build)       |
| run one or more containers as a managed pod                 | `kind: pod`, `charly deploy add`, `charly start`           | [Run](#run)           |
| apply the same candies to a host, VM, k8s, or Android device | `charly deploy add` + `target:`                        | [Deploy](#deploy)     |
| prove a config actually works, end-to-end                   | `kind: eval`, `charly eval run`, baked `eval:` checks  | [Evaluate](#evaluate) |

The same `charly` drives two further stages — it
[authors candies and boxes with an agent in the loop](#author-with-agents)
and [manages](#manage) the running lifecycle (cleanup, diagnostics,
schema upgrades, runtime config).

> One `charly.yml`, one box, one per-host `charly.yml` overlay, and one `kind: eval`
> bed drive all four stages — the build, the local run, the remote
> deploy, and the test harness. The binary that wires them together is
> also an MCP server, so your agent reaches every verb over the
> same RPC.

## Core concepts

A handful of ideas recur everywhere. Four of them are the heart of
OpenCharly — **candies & boxes**, **candyboxing**, **Risk Driven
Development**, and the **build → run → deploy → evaluate** lifecycle —
and the rest is the schema vocabulary that ties them together.

### Candies & boxes

OpenCharly treats boxes (container images) as composable building
blocks. Each **candy** is a self-contained unit; a **box** is an
ordered list of candies on top of a base. `charly` resolves the dependency graph, generates
multi-stage Containerfiles with cache mounts, and builds in the right
order — handling the hard parts so you (and your agents) don't
have to.

- **Candy** (`kind: candy` in `candy/<name>/charly.yml`) — packages (per-distro),
  tasks (eight verbs: `cmd`/`mkdir`/`copy`/`write`/`link`/`download`/
  `setcap`/`build`), services (one unified `service:` list — see
  init-system polymorphism below), volumes, env, ports, eval probes,
  `env_provide`/`env_require`/`mcp_provide`/`mcp_accept` for
  cross-container discovery, plus a `version:` CalVer.
  → `/charly-image:layer`.
- **Box** (`kind: box`) — base + ordered candy list. Multi-stage
  Containerfile, content-derived `ai.opencharly.version` OCI label,
  stable cache. → `/charly-image:image`.

### Candyboxing

Secure the *box* — a disposable, rootless container or VM with real,
kernel-enforced isolation — then hand your agent the whole candy store
inside it: every `charly` verb, every candy, every `charly eval` probe, a real
system, a real GPU. Far more capability than a locked-down sandbox, and
a mistake costs one rebuild.
→ [VISION.md](VISION.md) (why), CLAUDE.md "Candyboxing" (the rule),
`/charly-internals:disposable` (the lifecycle boundary).

### Risk Driven Development (early)

Prove the riskiest unknown — above all whether a particular *combination*
of candies, at their latest versions, actually builds and runs together —
empirically on a disposable `kind: eval` bed EARLY, before a design rests
on it. `charly eval` makes that proof cheap, for you and your agents alike.
→ [VISION.md](VISION.md) (why), CLAUDE.md "Risk Driven Development (RDD)"
(the rule), `/charly-eval:eval` (usage).

### Agent Driven Evaluation (acceptance)

What a box is *supposed* to do is written as runnable Gherkin scenarios
on the candy that provides the behaviour, baked into the box as a label.
A step with a check verb is verified deterministically; a prose-only step
is graded by an **agent** probing the live deployment. Author with
`charly candy add-scenario`, run with `charly box feature run` /
`charly eval feature run`, or let the `charly eval run <score>` AI loop drive it to
green. The spec is the test, and agents both write it and grade it. Every
candy MUST ship a non-empty `description.feature:` AND a `scenario:` list with
≥1 deterministic `do: assert` step — `charly box validate` hard-errors otherwise.
→ [VISION.md](VISION.md) (why), CLAUDE.md "Agent Driven Evaluation (ADE)"
(the rule), `/charly-eval:eval` (usage).

### Build → run → deploy → evaluate

The lifecycle is four verbs, and the same declarative inputs
flow through all of them:

- **Build** — a `kind: box` composes candies into a reproducible
  multi-stage image.
- **Run** — a `kind: pod` brings containers up as systemd-managed
  Podman quadlets.
- **Deploy** — `charly deploy add` applies the same candies to a host, VM,
  k8s cluster, or Android device via `target:`.
- **Evaluate** — `kind: eval` beds and baked `eval:` checks prove any
  box or deployment works end-to-end.

See [Lifecycle](#lifecycle) for the full verb families (plus
authoring-with-agents and management).

### Schema kinds

Beyond `candy` and `box`, the schema has these kinds — each a `kind:`
discriminator in its file:

- **Pod** (`kind: pod`) — multi-container deploy shape: containers,
  sidecars, tunnels. Deployed as a Podman pod with a quadlet per
  container. → `/charly-pod:pod`.
- **VM** (`kind: vm`) — `source: {kind: cloud_image | bootc}`,
  disk/ram/cpu, libvirt+QEMU. `charly vm build/create/start/stop/clone/
  snapshot/console`. → `/charly-vm:vm`.
- **K8s** (`kind: k8s`) — Kubernetes cluster (in-pod k3s or external)
  with provisioning + workload defaults. → `/charly-kubernetes:kubernetes`.
- **Local** (`kind: local`) — host-side candy stack applied to the
  operator's machine (or any ssh-reachable host) via the native
  package manager + systemd + shell profile. → `/charly-local:local-spec`.
- **Android** (`kind: android`) — Android device: in-pod emulator
  (via `box:`) or remote/physical adb endpoint. `apk:` is a candy
  package format scoped to Android targets. → `/charly-eval:android`.
- **Deploy** (`kind: deploy`) — a named deployment of one of the
  kinds above to a `target:` (`pod` default, `vm`, `k8s`, `local`,
  `android`). Carries env overlays, port remaps, volume backings,
  sidecars, tunnels, secrets, and the `disposable: true` opt-in.
  → `/charly-core:deploy`.
- **Eval** (`kind: eval`) — a *disposable* deploy used as an R10 test
  bed: `charly eval run <bed>` runs build → deploy → probe →
  fresh-update → tear-down. The `kind: recipe` / `kind: score` /
  `kind: agent` overlays drive the agent-iteration harness on top.
  → `/charly-eval:eval`.

### Cross-cutting rules

**`charly.yml` is the single project entry point.** Boxes are
discovered as `box/<name>/charly.yml`, candies as
`candy/<name>/charly.yml`, and the remaining kinds
(`vm`/`pod`/`k8s`/`eval`/`local`/`android`) live inline in
`charly.yml`'s root; the distro/builder/init/resource build
vocabulary is embedded in the `charly` binary. `import:` composes
other files or repos — a bare string for a flat same-repo import
(legacy per-kind files like `box.yml` / `vm.yml` still load this
way, but are no longer the canonical layout), or a
single-key `alias: ref` map for a namespaced cross-repo import (Go
package-member semantics — `base: cachyos.cachyos`, fetched from
`@github.com/owner/repo:tag` and cached under `~/.cache/charly/repos/`).

**Init-system polymorphism — one place, no siblings.** A candy that
needs the same service under supervisord (containers) and systemd
(hosts / VMs / bootc) declares BOTH forms in one `service:` list,
same `name:`, one entry with `use_packaged: <unit>.service`, another
with custom `exec:`. The init system at deploy time picks the
matching form. *Never* create a `<name>-host` / `<name>-pod` sibling
candy for this. Canonical examples: `/charly-coder:sshd`,
`/charly-infrastructure:virtualization`, `/charly-infrastructure:postgresql`.

**Distro + package-format dispatch.** A candy declares `distro:` tag
sections (`fedora:43:` / `ubuntu:24.04:`) and package-format sections
(`rpm:` / `deb:` / `pac:` / `aur:` / `apk:`). A box declares its
`distro:` identity and `build:` formats. Distro tag first-match wins;
`build:` formats install in declared order. `fedora-coder` /
`arch-coder` / `debian-coder` / `ubuntu-coder` share ~30 candies,
differing only in package sections.

**Disposability — explicit opt-in.** `disposable: true` on a
`kind: deploy` is the *one and only* authorization for `charly update`'s
autonomous destroy + rebuild. No hostname heuristic, no inference.
Explicit-only is what makes `charly update <name>` safe on shared
infrastructure. → `/charly-internals:disposable`.

## Why OpenCharly?

Containers are a great idea with rough edges. Real-world needs pile
up fast: GPU passthrough with the right driver stack, containers
that need `/dev/kvm` or virtualization access without blanket
`--privileged`, multiple services managed together, encrypted
volumes, VNC or browser-streamed desktops, device permissions that
don't compromise your host. Each is solvable — but solving them all
at once, reliably, across boxes, is where things get hard. And if
your agent has to build and manage these containers too, the
complexity compounds.

OpenCharly treats boxes as composable building blocks (see
[Core concepts](#core-concepts)) — handling the hard parts so you (and
your agents) don't have to.

**Testing and evaluating deployment configs is a first-class goal —
for you and your agents.** A deploy config is only useful if you can prove
it works, so any box or deployment is self-verifiable end-to-end — the
same surface whether you drive it at the keyboard or your agents drive
it autonomously. See [Evaluate](#evaluate) for the framework and
[Works with Claude Code](#works-with-claude-code) for the agents and
workflows. → `/charly-eval:eval`, `/charly-internals:agents`.

**Rootless-first power-user boxes.** The four boxes carrying the
full `charly` toolchain (`fedora-coder`, `charly-fedora`, `charly-arch`,
`githubrunner`) all run as uid=1000 with passwordless sudo. Four
cross-distro coder boxes (`/charly-coder:fedora-coder`/`arch-coder`/
`debian-coder`/`ubuntu-coder`) share ~30 candies, differing only in
package sections and how the uid-1000 user lands (create vs. adopt
mode). Rootless nested containers and rootless libvirt VMs work
with zero additive capabilities via the surgical `unmask=/proc/*`
security_opt from the `container-nesting` candy.
→ `/charly-distros:container-nesting`, `/charly-coder:fedora-coder`.

**Sandboxed agent desktops.** [Candyboxing](#candyboxing) applied to a
whole desktop: `/charly-openclaw:openclaw-desktop` is the all-in-one CachyOS
streaming desktop — Selkies desktop + openclaw-full gateway + agent CLIs
(claude-code, codex, gemini) + CPU ollama + nested `charly`. The agent (or the
user) builds boxes, launches nested rootless pods, and creates libvirt
VMs from a terminal inside the browser-accessible candybox desktop — uid 1000, no
`--privileged`, no added capabilities.

## Install

**Recommended — Go install** (requires Go 1.25.3+):

```bash
go install github.com/overthinkos/overthink/charly@latest
```

This puts `charly` in your `$GOPATH/bin`. Create an `charly.yml` and
a `candy/` directory and you're done. Legacy projects (predating
the unified schema, the `kind:` discriminators, or the singular
field names) convert in one shot with `charly migrate` — a single
idempotent chain to the latest CalVer schema. See `/charly-build:migrate`.

**Full project bootstrap** (to build boxes from this repo):

```bash
git clone --recurse-submodules https://github.com/overthinkos/overthink.git
cd opencharly
task build:charly         # on Arch: delegates to makepkg -si; elsewhere: portable install to ~/.local/bin/charly
charly box build        # build everything
```

**Arch / CachyOS / Manjaro** — install system-wide via `pacman`, building this
repo's bundled `opencharly-git` PKGBUILD (it is LOCAL-ONLY — NOT published to the
AUR):

```bash
cd pkg/arch && makepkg -si     # build + pacman-install opencharly-git from this repo
# or, equivalently, from the repo root:
task build:charly                  # pre-installs the AUR-only deps via your AUR helper, then runs makepkg -sefi in pkg/arch
```

The PKGBUILD `pkgver()` derives the same CalVer
(`YYYY.DDD.HHMM`) `charly version` prints, so `pacman -Q opencharly-git`
and `charly version` always agree. `depends=` covers the full runtime
surface — `podman`/`docker`/`fuse-overlayfs`/`slirp4netns` for
rootless containers, `qemu-full`/`libvirt`/`edk2-ovmf`/`swtpm` for
`charly vm`, `gnupg`/`pinentry`/`libsecret`/`gocryptfs`/`tailscale` for
secrets/encrypted volumes/tunnels, `go-task` so `task build:charly`
works from any fresh checkout. The pacman post-install hook enables
`docker.service` / `tailscaled.service` / `virtqemud.socket` and
adds the user to the `docker` and `libvirt` groups automatically.

**From source:**

```bash
cd charly && go build -o ../bin/charly .
```

## Quickstart

```bash
# Build a single box
charly box build fedora

# Build a CachyOS box (in submodule; charly resolves cross-repo refs)
charly -C box/cachyos box build cachyos

# Drop into an interactive shell
charly shell fedora

# Build and run a GPU-accelerated Jupyter server
charly box build jupyter
charly start jupyter

# Configure as a systemd service (quadlet + secrets + encrypted volumes)
charly config jupyter

# Build a bootable VM disk image from a bootc box
charly box build <my-bootc-box>             # a kind:box with bootc: true
charly vm build  <my-bootc-vm> --type qcow2 # a kind:vm with source.kind: bootc
charly vm create <my-bootc-vm>

# Apply candies directly to your workstation (no container)
charly deploy add host ripgrep
charly deploy add host fedora-coder --with-services --yes
charly deploy del host                  # reverses everything via ReverseOps + ledger

# Run a kind:eval test bed end-to-end (the R10 acceptance gate)
charly eval run eval-pod
```

## Lifecycle

The same six stages cover everything `charly` does — **build, run, deploy,
evaluate, author, manage**. Each maps to a family of `charly` verbs that
share the same declarative inputs.

### Build

> Declarative candy list → reproducible, fully-cached multi-stage
> image.

Each box declares a `base:`, an ordered `candy:` list, a `distro:`
identity, and a `build:` set of package formats. The planner
resolves the dependency graph, generates a multi-stage Containerfile
with cache-mounted package archives + AUR srcdest + pixi/npm/cargo
workdirs, and runs `podman build` (or `docker build` — switch with
`charly settings set engine.build podman`). Like conching chocolate, the
planner grinds every candy smooth — deduplicated, ordered, and
cache-warmed — before it sets into a box. The emitted image carries
OCI labels for every capability it claims: `ai.opencharly.description`
(the baked Gherkin scenarios), `ai.opencharly.eval_level`,
`ai.opencharly.init`, `ai.opencharly.version` (content-derived
`EffectiveVersion`, stable across no-op rebuilds), `.ports`, etc.

Commands: `charly box build` (build), `charly box generate` (write
`.build/` only), `charly box validate`, `charly box inspect`,
`charly box list`, `charly box merge`, `charly box pull`,
`charly box reconcile`. MCP-driven authoring — `charly box {set,
add-candy, rm-candy, fetch, refresh, write, cat}`, `charly candy {set,
add-rpm, add-deb, add-pac, add-aur}` — gives agents
comment-preserving YAML edits over RPC.

Cross-repo refs: `import:` items and candy references can name
`@github.com/owner/repo:tag`. The resolver fetches every distinct
`(repo, git-tag)` and arbitrates per per-entity `version:` — same
`version:` across different git tags → silent (re-tag);
different `version:` → warn once and use the newest. `charly box
reconcile` aligns the cross-repo pins when a candy's CalVer moves.

→ `/charly-build:build`, `/charly-build:generate`, `/charly-build:validate`,
`/charly-build:inspect`, `/charly-build:reconcile`, `/charly-image:image`,
`/charly-image:layer`, `/charly-internals:capabilities`.

### Run

> Multiple containers, one declaration, one start command — as
> systemd-native units.

`kind: pod` is the multi-container deploy shape. `charly deploy add
<name> <pod-ref>` materializes it; `charly start` brings it up via
Podman quadlets (`~/.config/containers/systemd/`) so a deployment is
a real systemd user unit — `journalctl`, `systemctl status`,
auto-restart on failure, start on login. `charly stop`, `charly restart`,
`charly status`, `charly logs`, `charly cmd`, `charly shell`, and `charly service`
(drive the inner supervisord) operate it; `charly remove` deletes the
quadlet and containers.

Boxes with multiple co-resident services in one container use
supervisord as their init (declared via the same unified `service:`
list); boxes that deploy as separate containers get one quadlet
each in a shared pod. Either way, the same `service:` schema is the
input.

- **Multiple instances** (`-i <instance>`) — every command takes
  `-i`; instances get distinct quadlet names
  (`charly-<image>-<instance>.container`), `charly.yml` entries
  (`<image>/<instance>`), and disambiguated MCP server names.
- **Sidecars** (`--sidecar <name>`) — attach a Tailscale,
  cloudflare-tunnel, or other container template into a shared pod.
  Sidecar-related env (`TS_*`, `CF_*`) routes to the sidecar, not
  the app. List with `charly config --list-sidecars`.
- **Tunnels** — `tunnel:` block declares Cloudflare (public) or
  Tailscale (tailnet-private) exposure with full backend scheme
  support (HTTP / HTTPS / TCP / TLS / SSH / RDP / SMB).
- **Encrypted volumes** — `--encrypt <vol>` or `type: encrypted`;
  gocryptfs masterkey provisioned into the Secret Service, mounted
  via independent `charly-enc-<image>-<volume>.scope` systemd units
  that survive container restart. Manage with `charly config {mount,
  unmount, status, passwd}`.
- **GPU access** — NVIDIA via CDI (`gpu.nvidia.com` annotation);
  ROCm for AMD; `charly udev install/remove` writes the host-side
  rules. CUDA toolkit + cuDNN + ONNX Runtime in the `cuda` candy.
- **Wayland desktop streaming** — the Selkies family
  (`selkies-labwc`, `sway-desktop`, `sway-browser-vnc`) bundles a
  Wayland compositor (sway or labwc) + Chrome + `wayvnc` on port
  5900 + Pipewire audio. Browser pane at `:3000`.
- **Per-box MCP servers** — `chrome-devtools-mcp` on `:9224`,
  `jupyter-mcp` at `:8888/mcp`, `marimo-mcp` at `:2718/mcp/server`,
  nested `charly-mcp`. Declared via `mcp_provide:` and auto-discovered
  by consumers (Hermes, Claude Code) through `CHARLY_MCP_SERVERS`.
- **Auto service discovery** — a candy's `env_provide:` declares
  env vars with `{{.ContainerName}}` templates injected into every
  co-deployed container at `charly config` time. Deploy `ollama` and
  every other pod sees `OLLAMA_HOST=http://charly-ollama:11434`.
  `mcp_provide:` works the same way for MCP URLs.
  `env_require:` / `env_accept:` document consumer dependencies
  so `charly config` warns early.

→ `/charly-core:start`, `/charly-core:logs`, `/charly-core:cmd`,
`/charly-core:service`, `/charly-core:charly-status`, `/charly-automation:sidecar`,
`/charly-automation:enc`, `/charly-automation:udev`, `/charly-pod:pod`,
`/charly-selkies:selkies-desktop-layer`, `/charly-selkies:sway`.

### Deploy

> The same `charly.yml` applied to a host, a remote ssh box, a VM, a
> k3s cluster, or an Android device.

`charly deploy add <name> <ref>` is the unified verb; `target:`
discriminates where it lands:

- **`target: pod`** (default) — Podman + quadlet, as in [Run](#run).
- **`target: vm`** — libvirt + QEMU. Candies are applied *inside* the
  booted VM over SSH via the same InstallPlan IR. `charly vm build`
  (bootc → QCOW2/RAW), `charly vm create/destroy/start/stop`, `charly vm
  clone` (snapshot fork), `charly vm snapshot`, `charly vm console`. The
  managed `~/.config/charly/ssh_config` fragment gets a `Host
  charly-<vmname>` stanza written on `charly vm create`.
  → `/charly-vm:vm`, `/charly-internals:vm-deploy-target`.
- **`target: k8s`** — Kustomize tree applied to k3s in-pod (candy
  triplet `/charly-infrastructure:k3s` + `k3s-server` + `k3s-agent`) or
  to an external cluster. `K3S_CLUSTER_TOKEN` auto-generated on
  first deploy via `ensureCandySecret` and shared with every joining
  agent — zero operator setup. → `/charly-kubernetes:kubernetes`,
  `/charly-infrastructure:k3s`.
- **`target: local`** — applies the candies' packages / files /
  systemd units to the host filesystem. `host: local` (default)
  uses the local shell executor; `host: user@machine[:port]` (or a
  configured alias) re-execs `charly` over SSH. Per-machine overlays
  via `add_candy:` in `~/.config/charly/charly.yml`. Ledger at
  `~/.config/opencharly/installed/` records every ReverseOp so
  `charly deploy del host` reverses precisely what was applied.
  → `/charly-local:local-deploy`, `/charly-local:local-spec`.
- **`target: android`** — `kind: android` device (in-pod emulator
  via `box:` or remote adb endpoint via `adb: {host: …}`);
  `apk:` packages installed by `apkeep` (Google Play) or pushed
  from committed `.apk` files via goadb. Nested `pod → android`
  mirrors `vm → k8s`. → `/charly-eval:android`, `/charly-eval:adb`.

`charly deploy del`, `charly deploy sync` (apply K8s changes live),
`charly deploy from-box` (source-less deploy from OCI labels), and
`charly update` complete the lifecycle. `charly update <name>` performs
destroy + (optional rebuild) + create + start unattended *only*
when the deploy carries `disposable: true`.

**Secrets.** Credentials resolve in order: env var → Secret Service
(systemd keyring; GNOME Keyring, KDE Wallet, or KeePassXC
FdoSecrets) → config-file fallback (`~/.config/charly/config.yml`,
0600). Project-level shell secrets live in a GPG-encrypted
`.secrets` file: `charly secrets gpg env` decrypts in memory when
direnv loads the project; no plaintext on disk. Manage with `charly
secrets gpg {env, show, set, unset, edit, encrypt, recipients,
import-key, export-key, setup, doctor}`. Candy-private secrets
(like `K3S_CLUSTER_TOKEN`) get auto-provisioned via
`ensureCandySecret` and stored under `charly/secret/<key>` in the
Secret Service. **Agent forwarding** — the `agent-forwarding` candy
binds host `SSH_AUTH_SOCK` / `GPG_AGENT_SOCK` into the container.
→ `/charly-build:secrets`.

→ `/charly-core:deploy`, `/charly-core:charly-config`, `/charly-core:charly-update`,
`/charly-internals:disposable`, `/charly-vm:vms-catalog`.

### Evaluate

> Build → deploy → probe → fresh-update → tear down — disposable beds
> with the same DSL as production deploys.

Tests are first-class. Every `charly.yml` (box + candy) /
`charly.yml` declares a top-level `scenario:` list whose steps are
one unified `Op` vocabulary — verb × `do:` (`act`|`assert`|`instruct`)
× `context:` — covering goss-style checks (files, packages, services,
ports, processes, commands, HTTP, DNS, mounts, users, groups, kernel
params, interfaces, matchers) as `do: assert`, configuration as
`do: act`, and free-form agent instructions (`agent:`) as `do: instruct`.
Scenarios bake into a three-section OCI label
(`ai.opencharly.description` → `{candy, box, deploy}`) so any pulled
box is self-testable without its source repo.

Three execution modes:

- **`charly eval box <image>`** — disposable `podman run --rm` of the
  baked layer + image checks. Build-scope; no deploy state.
- **`charly eval live <image>`** — runs all three sections against a
  *running* deployment, substituting deploy-time variables
  (`${HOST_PORT:N}`, `${VOLUME_PATH:name}`, `${CONTAINER_IP}`,
  `${ENV_*}`) so the same check survives port remaps and volume
  rebindings.
- **`charly eval run <bed>`** — the canonical R10 acceptance gate.
  Picks a `kind: eval` bed from the project `charly.yml` `eval:` block (a disposable deploy
  carrying `disposable: true`) and runs build → eval box → deploy
  → eval live → fresh `charly update` → eval live again → teardown.
  Pick the bed whose kind matches what you changed: `eval-pod`,
  `eval-local`, `eval-k3s-vm`, `eval-android-emulator-pod`.
  `charly eval run --all-beds` iterates the catalog.

Exit codes are goss-style: `0` = all checks passed, `1` =
infra/usage error (the eval never reached a verdict), `2` =
checks failed. R10 automation treats `1` as "did not run",
not "failed".

**Agents drive these beds.** Claude Code sub-agents
(`eval-bed-runner`, `deploy-verifier`) and dynamic workflows
(`/verify-beds`, `/audit-deploy-configs`) run `charly eval
run`/`live`/`box` against the existing beds and return verbatim
pass/fail — the same disposable-bed verification, whether you run it
or your agent does. → `/charly-internals:agents`.

Eleven live-container probe verbs — authorable inline as
`scenario:` steps (`cdp: eval`, `wl:
screenshot`, `dbus: call`, `vnc: status`, `mcp: list-tools`, `adb:
getprop`, `appium: click`, …):

- `charly eval cdp` — Chrome DevTools Protocol (open, click, eval JS,
  screenshot).
- `charly eval wl` — Wayland / sway / labwc automation; `wl overlay`
  for fullscreen recording overlays.
- `charly eval dbus` — D-Bus method calls and signal subscriptions.
- `charly eval vnc` — RFB handshake, pointer/keyboard, clipboard,
  screenshot.
- `charly eval mcp` — Model Context Protocol clients (list-tools,
  list-resources, read-resource, call-tool).
- `charly eval spice` — SPICE display protocol with guest-agent socket.
- `charly eval libvirt` — libvirt API (VM info, screenshot, send-key,
  QMP, snapshots, event stream).
- `charly eval record` — terminal asciinema or desktop ffmpeg.
- `charly eval k8s` — Kubernetes probes (nodes, pods, ingress,
  wait-ready, storageclass, addons, raw kubectl).
- `charly eval adb` — Android Debug Bridge (devices, shell, install,
  getprop, screencap, logcat, wait-for-device).
- `charly eval appium` — W3C WebDriver session lifecycle, find, click,
  send-keys, screenshot.

`charly feature {list, pending, validate}` authors and validates
Gherkin-shaped descriptions on the same entries.

→ `/charly-eval:eval`, `/charly-eval:cdp`, `/charly-eval:wl`, `/charly-eval:dbus`,
`/charly-eval:vnc`, `/charly-eval:spice`, `/charly-eval:libvirt`,
`/charly-eval:record`, `/charly-kubernetes:eval-k8s`, `/charly-eval:adb`,
`/charly-eval:appium`, `/charly-eval:android`.

### Author with agents

> Agents in the loop, authoring and iterating on candies and
> boxes — `charly`-specific.

The agent iteration harness sits on top of `kind: eval` and
adds three overlay kinds:

- **`kind: agent`** — reusable agent CLI catalog (`claude`,
  `codex`, `gemini`, …). Each entry declares a command, a version
  probe, an output format (typically `stream-json`), and credential
  paths. The harness parses each NDJSON line into
  `iteration[].runner_event`.
- **`kind: recipe`** — deterministic test specification: scenarios,
  each with a `pod:` declaring the container its probes target.
  Pure check catalogs and Gherkin scenario descriptions; no agent
  involved here (authoring the description + eval is mandatory per candy; the
  live agent grader via `charly eval feature run` stays opt-in).
- **`kind: score`** — runner config naming the agent, the
  target `eval-sandbox`, the recipes, the plateau iteration count,
  the prompt, and the watchdog timeout. `charly eval run <score>` runs
  the multi-hour benchmark: the agent reads scope
  (`charly eval scope`) + prior tag (`charly eval last-tag`) + live results →
  rebuilds + redeploys → harness re-scores → continues until plateau
  detection or the watchdog fires. Progressive recipe disclosure
  means the agent sees recipes one at a time as it earns them.

Cross-cutting: **`charly mcp serve`** is the MCP gateway. Every leaf
Kong command auto-exposes as an MCP tool (Streamable HTTP or
stdio), so Claude Code, Codex, or any MCP client drives the full
`charly` surface over RPC. `--read-only` filters destructive tools;
auto-fallback to `overthinkos/overthink` when no project is wired
(opt out with `--no-default-repo`).

→ `/charly-eval:eval`, `/charly-build:charly-mcp-cmd`, `/charly-coder:charly-mcp`,
`/charly-coder:claude-code`, `/charly-coder:codex`, `/charly-coder:gemini`.

### Manage

> Ops verbs: cleanup, diagnostics, schema upgrades, runtime config,
> host-side aliases.

- `charly clean` — prune build artifacts by CalVer retention
  (`keep_images`, `keep_eval_runs`); sweeps stale makepkg
  leftovers. Label-CalVer wins over tag-CalVer.
- `charly doctor` — host dependency check (`podman`/`docker`/`libvirt`/
  `qemu`/`gnupg`/`gocryptfs`/`tailscale`/…).
- `charly reap-orphans` — find ephemeral deployments whose underlying
  pod/vm/scope is gone and remove the stale quadlet.
- `charly migrate` — single idempotent chain to the latest CalVer
  schema. Auto-invoked on remote-cache downloads. The
  `LatestSchemaVersion()` gate hard-errors newer-than-binary
  configs.
- `charly settings {get, set, list, reset, path, migrate-secrets}` —
  engine (`engine.build podman|docker`), secret backend, host
  aliases (`hosts.<name> user@machine`), VM backend.
- `charly version` — print computed CalVer tag.
- `charly tmux {ls, attach}` — drive tmux sessions inside containers.
- `charly ssh tunnel {spice, vnc, …}` — forward SPICE/VNC/unix sockets
  from a remote libvirt host to the local machine.
- `charly alias install` — register box-scoped shell aliases
  (bash/zsh/fish) so `<image>` on the host transparently runs
  inside the container.
- `charly udev install/remove` — host-side udev rules for GPU device
  access (CDI symlinks).

→ `/charly-core:clean`, `/charly-core:charly-doctor`, `/charly-core:charly-update`,
`/charly-build:migrate`, `/charly-build:settings`, `/charly-core:ssh`,
`/charly-automation:tmux`, `/charly-automation:alias`,
`/charly-automation:udev`.

## Command reference

The `charly` CLI has 29 top-level verbs across three modes with disjoint
input sets — **build mode** (`charly box …` reads `charly.yml`),
**test mode** (`charly eval …` reads OCI labels + `charly.yml` overlays,
never `charly.yml`), and **deploy mode** (everything else reads
OCI labels + `charly.yml`) — plus the cross-mode `charly mcp serve`
gateway exposing the entire surface as MCP tools.

| Area | Commands | Skill |
|---|---|---|
| **Box (build mode)** | `charly box {build, generate, validate, merge, new, inspect, list, pull, reconcile}` | `/charly-image:image` + `/charly-build:build`, `/charly-build:generate`, `/charly-build:validate`, `/charly-build:merge`, `/charly-build:new`, `/charly-build:inspect`, `/charly-build:list`, `/charly-build:pull`, `/charly-build:reconcile` |
| **Box authoring (MCP-first)** | `charly box {set, add-candy, rm-candy, fetch, refresh, write, cat}` and `charly candy {set, add-rpm, add-deb, add-pac, add-aur}` | `/charly-image:image` "Authoring" + `/charly-image:layer` |
| **Deployment** | `charly deploy {add, del, sync, from-box, export, import, show, reset, status, path}`; `charly config`; `charly start`, `charly stop`, `charly restart`, `charly update`, `charly remove` | `/charly-core:deploy`, `/charly-core:charly-config`, `/charly-core:start`, `/charly-core:stop`, `/charly-core:charly-update`, `/charly-core:remove`, `/charly-local:local-deploy`, `/charly-kubernetes:kubernetes`, `/charly-internals:vm-deploy-target` |
| **Runtime** | `charly shell`, `charly cmd`, `charly service`, `charly status`, `charly logs`, `charly tmux` | `/charly-core:shell`, `/charly-core:cmd`, `/charly-core:service`, `/charly-core:charly-status`, `/charly-core:logs`, `/charly-automation:tmux` |
| **Test + probes** | `charly eval {box, live, run}` + the 11 live probe verbs (`cdp`, `wl`, `dbus`, `vnc`, `mcp`, `record`, `spice`, `libvirt`, `k8s`, `adb`, `appium`); `charly feature {list, pending, validate}` | `/charly-eval:eval`, `/charly-eval:cdp`, `/charly-eval:wl`, `/charly-eval:dbus`, `/charly-eval:vnc`, `/charly-eval:spice`, `/charly-eval:libvirt`, `/charly-eval:record`, `/charly-kubernetes:eval-k8s`, `/charly-eval:adb`, `/charly-eval:appium` |
| **MCP gateway** | `charly mcp {serve, ping, servers, list-tools, list-resources, list-prompts, call, read}` | `/charly-build:charly-mcp-cmd`, `/charly-coder:charly-mcp` |
| **VM** | `charly vm {build, create, start, stop, destroy, snapshot, clone, console, ssh, import, list}` | `/charly-vm:vm`, `/charly-vm:vms-catalog`, `/charly-internals:vm-deploy-target` |
| **Schema migration** | `charly migrate` (single idempotent chain) | `/charly-build:migrate` |
| **Secrets & config** | `charly secrets`, `charly settings`, `charly alias`, `charly udev` | `/charly-build:secrets`, `/charly-build:settings`, `/charly-automation:alias`, `/charly-automation:udev` |
| **Host & admin** | `charly doctor`, `charly clean`, `charly reap-orphans`, `charly ssh`, `charly version` | `/charly-core:charly-doctor`, `/charly-core:clean`, `/charly-core:ssh`, `/charly-core:charly-version` |

**Global flags** (apply to every command):

- `-C <dir>` / `--dir <dir>` / `CHARLY_PROJECT_DIR=<dir>` — override the
  project directory.
- `--repo <OWNER/REPO[@REF]>` / `CHARLY_PROJECT_REPO=…` — read
  `charly.yml` from a remote git repo. Bare `owner/repo`
  auto-prefixes `github.com/`; the literal `default` expands to
  `overthinkos/overthink`. Cached in `~/.cache/charly/repos/`. Mutually
  exclusive with `--dir`.
- `--host <alias|user@machine[:port]>` / `CHARLY_HOST=…` — re-exec the
  command on a remote host over SSH. Commands marked LocalOnly
  (`settings`, `version`, `ssh tunnel`) always run locally.

## Catalogs

Content lives in the working tree and in the skill index — pointers,
not enumerations:

- **Candy library** (`candy/` + submodule `box/<distro>/candy/`,
  187 candies total). Foundation: `/charly-distros:*` (40 skills — base
  OS, GPU runtime, bootc, per-distro builders),
  `/charly-languages:*`, `/charly-infrastructure:*` (22), `/charly-tools:*`
  (19). Per-pod: `/charly-jupyter:*`, `/charly-coder:*` (33),
  `/charly-selkies:*` (40), `/charly-openclaw:*`, `/charly-versa:*`,
  `/charly-ollama:*`, `/charly-openwebui:*`, `/charly-comfyui:*`,
  `/charly-immich:*`, `/charly-hermes:*`, `/charly-filebrowser:*`.
- **Box catalog** (discovered `box/<name>/charly.yml` in the `box/<distro>` submodules — main owns none after the 2026-06 box inversion) — boxes,
  39 enabled by default. Same plugin namespaces; per-pod boxes
  carry an MCP server hint in `plugins/README.md`.
- **VM catalog** (`vm.yml` + `box/cachyos/vm.yml`) — cloud_image
  + bootc entries. → `/charly-vm:vms-catalog`.
- **Deploy-target catalog** — pod / vm / k8s / local / android.
  Each has a dedicated kind file.
- **Eval bed catalog** (the `eval:` blocks in the project and
  `box/<distro>` `charly.yml`s) — `kind: eval` beds for R10,
  plus `kind: recipe` / `score` / `ai` for the agent harness.
  → `/charly-eval:eval`.

Candies used by only one box family are vendored in that
`box/<distro>` submodule (e.g. `ghostty`/`keepassxc-keyring` in
`box/cachyos`, `arch-*-test` fixtures in `box/arch`). Shared
candies are pulled by `@github` ref.

**Composition meta-candies** — `sway-desktop`, `sway-desktop-vnc`,
`selkies-desktop`, `openclaw-full`, `openclaw-full-ml`,
`python-ml`, `jupyter-ml`, `unsloth-studio` bundle curated candy
sets.

**Data candies / data boxes** — `data:` block in `charly.yml` stages
files at `/data/<volume>/`; `charly config --bind <volume>` provisions
them at deploy time; `charly update` merges new data non-destructively.
`data_image: true` scratch-based boxes carry data + OCI labels,
consumed via `charly config --data-from <data-image>`.

See `plugins/README.md` for the authoritative skill index and
`CHANGELOG.md` for the dated history of cutovers.

## Troubleshooting

Each entry points to the canonical skill — details belong there,
not here.

| Symptom | First step |
|---------|-----------|
| Service won't start | `charly status <image>` then `charly logs <image>` (`/charly-core:charly-status`, `/charly-core:logs`) |
| Quadlet out of sync with charly.yml | `charly config <image> --update-all` (`/charly-core:charly-config`) |
| Build cache stale | `charly box build --no-cache <image>` (`/charly-build:build`) |
| Chrome stuck or crash-looping | `/charly-selkies:chrome` Resource Caps & Circuit Breaker section |
| Encrypted volume locked at boot | `charly config mount` waits for keyring unlock automatically — zero CPU, event-driven (`/charly-automation:enc`) |
| GPU not detected | `charly doctor` then `/charly-automation:udev` |
| Tunnel not appearing on a new instance | Tunnel config is `charly.yml`-only — add manually per instance (`/charly-core:deploy`) |
| Service built fine but broken in production | `charly eval live <image>` runs the baked layer + image + deploy checks (`/charly-eval:eval`) |
| `charly vm build` fails: "no kind:vm entity in vm.yml" | Declare a `kind: vm` entity (`/charly-vm:vms-catalog`) |
| SPICE console blank on cloud_image VM | Known `simpledrm → qxldrmfb` race under UEFI; switch to `firmware: bios` (`/charly-vm:arch`) |
| `charly deploy add vm:<name>` errors "VM does not exist" | Run `charly vm create <name>` first — VM deploy is not auto-provisioning (`/charly-core:deploy`) |
| Resolver "referenced at multiple versions" warning | `charly box reconcile` aligns the cross-repo `@github` pins (`/charly-build:reconcile`) |
| `charly box pull` says "image is not available locally" | `charly box pull` accepts short name + project, fully-qualified ref, or `@github` remote ref. See `/charly-build:pull` |
| Newer-than-binary config rejected at load | `charly migrate` brings the project to the latest schema CalVer (`/charly-build:migrate`) |
| Schema/format change won't apply | `charly migrate` is idempotent; auto-invoked on remote-cache fetches |

## Adding a candy

```bash
charly box new candy my-candy             # Scaffold the directory
# Edit candy/my-candy/charly.yml        # Declare packages, deps, env, ports,
#                                       # services, eval probes, and tasks:
#                                       # (see /charly-image:layer for the verb catalog)
# Optionally add pixi.toml / package.json / Cargo.toml for auto-detected builders.

# Add to a box's candy list in box/<name>/charly.yml:
#   candy: [..., my-candy]

charly box build my-image                 # Build it
charly eval box my-image                  # Run the baked checks
```

`/charly-image:layer` is the canonical reference for the eight `task:`
verbs (`cmd`, `mkdir`, `copy`, `write`, `link`, `download`,
`setcap`, `build`), the unified `service:` schema, `vars:`
substitution, YAML anchors, and execution-order rules.
`/charly-eval:eval` covers the matcher forms, runtime variable table,
gold-standard pattern (`candy/redis/charly.yml`), and the 10
authoring gotchas.

## Works with Claude Code

OpenCharly works hand-in-hand with
[Claude Code](https://claude.com/claude-code). The bundled
[plugins/](plugins/) directory provides skills that teach Claude
how to compose, build, deploy, and manage your boxes.
Every candy, every box, every command has a dedicated skill.

**Quick setup** — add this to your project's `.claude/settings.json`:

```json
{
  "enabledPlugins": {
    "charly-core@charly-plugins": true,
    "charly-build@charly-plugins": true,
    "charly-eval@charly-plugins": true,
    "charly-image@charly-plugins": true,
    "charly-internals@charly-plugins": true,
    "charly-distros@charly-plugins": true,
    "charly-infrastructure@charly-plugins": true,
    "charly-jupyter@charly-plugins": true,
    "charly-coder@charly-plugins": true
  },
  "extraKnownMarketplaces": {
    "charly-plugins": {
      "source": { "source": "directory", "path": "./plugins" }
    }
  }
}
```

Representative subset; see `plugins/.claude-plugin/marketplace.json`
for the full 25-plugin catalog. Clone with submodules to get the
plugins directory: `git clone --recurse-submodules
https://github.com/overthinkos/overthink.git`.

**MCP gateway as the universal channel.** `charly mcp serve` exposes
every `charly` CLI leaf as an MCP tool (Streamable HTTP or stdio), so
the agent reaches the full build / deploy / test surface over
RPC. Per-box MCP servers (chrome-devtools-mcp, jupyter-mcp,
marimo-mcp, charly-mcp) auto-discover via `mcp_provide:` when their
containers are running.

**Sub-agents, dynamic workflows, and agent teams.** Beyond skills, the
project ships Claude Code **sub-agents** (`plugins/internals/agents/`):
executors `eval-bed-runner` and `deploy-verifier` that drive the `charly eval`
beds and return verbatim proof, plus enforcers `root-cause-analyzer`,
`testing-validator`, and `layer-validator`. Two **dynamic workflows**
(`.claude/workflows/`) fan the work out — `/verify-beds` runs every
`kind: eval` bed as the R10 gate, `/audit-deploy-configs` evaluates your
deploy configs — and the same agent definitions reuse as **agent-team**
teammates. Whether you drive `charly` from the keyboard or hand it to an
agent, testing and verifying deployments uses the one surface.
→ `/charly-internals:agents`.

See [VISION.md](VISION.md) for the long-term thesis and direction,
[CLAUDE.md](CLAUDE.md) for the project's rules and mandates,
[plugins/README.md](plugins/README.md) for the full skill index (usage
and architecture live in the skills), and [CHANGELOG.md](CHANGELOG.md)
for dated history (by policy, never duplicated here or in skills).

## License

MIT
