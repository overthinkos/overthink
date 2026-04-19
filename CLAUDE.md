# Overthink — The Container Management Experience for You and Your AI

Compose, build, deploy, and manage container images from a library of fully configurable layers.
Built on a generic init system framework (`build.yml` → `init:` section) and `ov` (Go CLI). Designed to work equally well from the command line and from AI agents like Claude Code. Supports both Docker and Podman.

---


## Five Cornerstones of AI Scut Testing

1. **Your Assumptions Are the Enemy** — The thing you didn't think to test is the thing that will break.
2. **Small Bugs Have Big Friends** — Every issue you dismissed as nonessential is tomorrow's catastrophe.
3. **It's Broken Until It Runs Live** — Localhost and mocks are deceptive liars.
4. **Check Every Damn Thing** — Methodically. Tediously. No shortcuts.
5. **Then Check It Again** — Because you missed something. You always do.

## Prioritize Clean Architecture Above All Else

Always pick the cleanest long-term approach and prioritize having a clean codebase with any deprecated code fully removed above everything.
You have all the time in the world and taking the time to get things properly done is ALWAYS worth the effort.

## Architecture Overview

`ov` (Go CLI, source in `ov/`) has a three-mode namespace split with strictly disjoint input sets — each mode owns a distinct input file and never reads another mode's input:

- **Build mode** — `ov image {build, generate, validate, merge, new, inspect, list, pull}`. Reads `image.yml` + `build.yml`. Writes Containerfiles, built images, OCI labels. See `/ov:image`, `/ov:build`.
- **Test mode** — `ov test` (all forms: `run`, `cdp`, `wl`, `dbus`, `vnc`) + `ov image test`. Reads only OCI labels (`org.overthinkos.tests`) + local `deploy.yml` tests overlay + container/image runtime state. Never reads `image.yml`. Writes nothing persistent. The tests OCI label is baked at build time from `image.yml` + `layer.yml` authoring only — `deploy.yml` contributes the local tests overlay at test-run time, not to the label. See `/ov:test`.
- **Deploy mode** — every other command (`config`, `deploy`, `start`, `stop`, `update`, `remove`, `shell`, `cmd`, `service`, `status`, `logs`, `tmux`, `doctor`, `udev`, `vm`, `secrets`, `settings`, `alias`, `record`, `version`). Reads OCI labels + `deploy.yml`. Writes `deploy.yml`, quadlet files, credential stores. See `/ov:config`, `/ov:deploy`, `/ov-dev:go`.

**Key subsystems** — each skill is the single source of truth for its area; don't copy their contents here.

| Subsystem | Skill |
|-----------|-------|
| Image family (build mode) | `/ov:image`, `/ov:build`, `/ov:generate`, `/ov:validate`, `/ov:pull` |
| Testing (test mode) | `/ov:test` (parent router: `ov test <image>` + declarative verbs cdp/wl/dbus/vnc/mcp — see also `/ov:cdp`, `/ov:wl`, `/ov:dbus`, `/ov:vnc`, `/ov:mcp`), `/ov-dev:go` (impl map: `testspec.go`, `testrun.go`, `testrun_ov_verbs.go`, `validate_tests.go`, `mcp.go`, `mcp_client.go`) |
| Install tasks (`tasks:` verb catalog, `vars:`, `${VAR}`, YAML anchors) | `/ov:layer` (authoritative) |
| Credentials & Secrets | `/ov:secrets`, `/ov:config` |
| Credential-backed env vars (`secret_accepts` / `secret_requires`) | `/ov:layer`, `/ov:secrets` |
| Volumes & Encrypted Storage | `/ov:deploy`, `/ov:config`, `/ov:enc` |
| env/mcp provides/requires/accepts | `/ov:config`, `/ov:layer` |
| Sidecars & Tunnels (deploy.yml-only) | `/ov:sidecar`, `/ov:deploy` |
| Init Systems | `/ov:generate`, `/ov:layer` |
| Multi-distro | `/ov:build`, `/ov:layer` |
| Desktop Automation | `/ov:test` (parent router) with nested verbs `/ov:cdp`, `/ov:dbus`, `/ov:vnc`, `/ov:wl`; plus `/ov:wl-overlay` (Wayland overlay helpers) |
| Keyboard & Locale | `/ov-layers:labwc`, `/ov-layers:selkies` |
| GPU Auto-detection | `/ov:doctor`, `/ov:shell` |
| Missing-image recovery | `/ov:pull` (`ErrImageNotLocal` sentinel in `ov/labels.go`) |
| Declarative testing (`tests:` / `deploy_tests:` / `org.overthinkos.tests`) | `/ov:test` — verb catalog (file/port/command/http/package/service/process/dns/user/group/interface/kernel-param/mount/addr/matching + cdp/wl/dbus/vnc/mcp), runtime variables, deploy.yml overlay, 10 authoring gotchas |
| Containerfile generation (LABELs-at-end, `shellAnsiQuote`, `writeJSONLabel`) | `/ov:generate`, `/ov-dev:generate`, `/ov-dev:go` |
| Bootc-specific boot wiring (tty1 autologin, graphical target, systemd-user supervisord, linger sentinel, external-base `distro:` gotcha, `/dev:/dev` mount, `vm.ssh_port` plumbing, dual USER-context tests) | `/ov-layers:bootc-config`, `/ov-layers:supervisord`, `/ov-images:selkies-desktop-bootc`, `/ov:vm`, `/ov:generate`, `/ov:image`, `/ov:test` |
| Rootless nested containers & rootless VMs (kernel `mount_too_revealing()` RCA, `unmask=/proc/*`, `_CONTAINERS_USERNS_CONFIGURED=""`, `BUILDAH_ISOLATION=chroot`, subuid-fits-in-outer-userns pattern, supervisord-managed `virtqemud` / `virtnetworkd`) | `/ov-layers:container-nesting`, `/ov-layers:virtualization`, `/ov-images:selkies-desktop-ov` |

**`task` (Taskfile)** -- bootstrap only: builds `ov` from source. Source: `Taskfile.yml` + `taskfiles/{Build,Setup}.yml`.

---

## Repository Layout

See `/ov-dev:go` for directory structure and `/ov-dev:skills` for plugin/skill organization.

---

## Key Rules

- MUST invoke skills before exploring the codebase — skills are the primary knowledge source.
- Lowercase-hyphenated names for layers and images.
- All logic lives in `ov`; Taskfiles are strictly bootstrap (build the `ov` binary). See `Taskfile.yml` + `taskfiles/{Build,Setup}.yml`.
- **Tests ship with the image**: every layer that installs a service ships a `tests:` block (see `/ov:test`). LABEL directives are emitted last in each Containerfile so test edits rebuild in ~2 seconds instead of minutes.
- **Mode purity**: `LoadConfig` reads `image.yml` only — never merges `deploy.yml`. OCI labels come strictly from `image.yml` + `layer.yml`; `deploy.yml` is deploy-mode state that must never bleed into baked images. See `/ov-dev:go` "Mode purity" for the bug this prevents.

**Authoring + deployment specifics live in skills** — see the subsystems table above for the full mapping. Quick entry points: authoring → `/ov:layer`, `/ov:image`, `/ov:build`, `/ov:test`; deployment → `/ov:config`, `/ov:deploy`, `/ov:sidecar`, `/ov:enc`. Quadlet is default; `ov config` before `ov start`; tunnel is deploy.yml-only.

---

## Skills First (Blocking)

Invoke matching skills BEFORE reading source, launching Explore agents, or grepping. Order: skills → CLAUDE.md → memory → explore (last resort). Multi-step workflows: invoke ALL skills in the chain. See `/ov-dev:skills` for skill routing, chains, maintenance guidelines, and the 3 blocking enforcement agents (layer-validator, root-cause-analyzer, testing-validator).


## AI Attribution (Fedora Policy Compliant)

Per [Fedora AI Contribution Policy](https://docs.fedoraproject.org/en-US/council/policy/ai-contribution-policy/), ALL commits MUST include `Assisted-by: Claude (<confidence>)` trailer. ALL GitHub issues/PRs MUST include `*Assisted-by: Claude (<confidence>)*` at the end.

| Confidence | When to Use |
|-----------|-------------|
| `fully tested and validated` | Overlay testing + all 9 testing standards met |
| `analysed on a live system` | Observed live system behavior, logs checked |
| `syntax check only` | Pre-commit hooks passed, no functional testing |
| `theoretical suggestion` | No validation performed — AVOID |

```
Fix: Add fuse-overlayfs for container startup

Tested via overlay session on LOCAL system.

Assisted-by: Claude (fully tested and validated)
```