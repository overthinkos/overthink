# Overthink — The Container Management Experience for You and Your AI

Compose, build, deploy, and manage container images from a library of fully configurable layers.
Built on a generic init system framework (`init.yml`) and `ov` (Go CLI). Designed to work equally well from the command line and from AI agents like Claude Code. Supports both Docker and Podman.

---


## Always follow the Five Cornerstones of AI Scut Testing

### Your Assumptions Are the Enemy

- The thing you didn't think to test is the thing that will break.

### Small Bugs Have Big Friends

- Every issue you dismissed as nonessential is tomorrow's catastrophe.

### It's Broken Until It Runs Live

- Localhost and mocks are deceptive liars.

### Check Every Damn Thing

- Methodically. Tediously. No shortcuts.

### Then Check It Again

Because you missed something. You always do.

## Prioritize Clean Architecture Above All Else

Always pick the cleanest long-term approach and prioritize having a clean codebase with any deprecated code fully removed above everything.
You have all the time in the world and taking the time to get things properly done is ALWAYS worth the effort.

## Architecture Overview

Two components with a clean split:

**`ov` (Go CLI)** -- all computation, building, and deployment. Two operational modes:
- **Build mode:** Parses `images.yml`, scans `layers/`, resolves dependency graphs, validates, generates Containerfiles, builds images via `<engine> build`.
- **Deploy mode:** Reads OCI image labels + `~/.config/ov/deploy.yml` (no `images.yml` needed). `ov config`/`start`/`stop`/`status`/`logs`/`update`/`remove` all work standalone with just the container image. `ov config` is the single entry point for deployment setup (quadlet + secrets + encrypted volumes + data provisioning).

Source: `ov/`. Registry inspection via go-containerregistry.

**Credential & Secret Management** -- Abstracted via `CredentialStore` interface:
- **Host-side credentials** (VNC passwords) stored in system keyring (GNOME Keyring, KDE Wallet, KeePassXC) or plaintext config fallback. Backend auto-detected; override with `secret_backend` config key.
- **KeePass .kdbx backend** for systems without Secret Service (headless servers, SSH sessions). `ov secrets init` creates a database; auto-detected when keyring is unavailable and `secrets.kdbx_path` is configured. Override with `ov --kdbx <path>` global flag. `ov secrets` commands manage entries directly.
- **KeePass password caching** via Linux kernel keyring (`KEY_SPEC_USER_KEYRING`). The kdbx master password is cached for 1 hour by default after the first interactive prompt, so subsequent `ov` commands reuse it automatically. Resolution chain: `OV_KDBX_PASSWORD` env var > kernel keyring lookup (key: `ov-kdbx-password`) > interactive prompt (systemd-ask-password or terminal) > auto-store in kernel keyring with configured TTL. Config keys: `secrets.kdbx_cache` (env: `OV_KDBX_CACHE`, default: `true`), `secrets.kdbx_cache_timeout` (env: `OV_KDBX_CACHE_TIMEOUT`, default: `3600`). Uses `golang.org/x/sys/unix` keyctl syscalls. Source: `ov/keyctl.go`, `ov/credential_kdbx.go`.
- **Container secrets** declared in `layer.yml` `secrets` field. Metadata stored in OCI image labels (`org.overthinkos.secrets`). At configure time, `ov config <image>` provisions Podman secrets and generates `Secret=` quadlet directives. **Secret provisioning is idempotent** — existing Podman secrets are never overwritten. This prevents overwriting passwords that stateful services (e.g., PostgreSQL) have already been initialized with. To force re-provisioning: `podman secret rm <name> && ov config setup <image>`. `--password auto` generates all secrets automatically; `--password manual` prompts for each. Docker falls back to env var injection. Encrypted volumes are mounted via `ExecStartPre=ov config mount` in the quadlet. Each gocryptfs mount runs inside a `systemd-run --scope --user --unit=ov-enc-<image>-<volume>` scope unit, decoupling its lifecycle from the container service. Scope units survive container stop/restart, keeping mounts accessible to the host user. The `-allow_other` flag is required for rootless podman with `--userns=keep-id` (crun's mount setup runs as inner uid 0 → host subuid, not the mount owner). `ov config unmount` stops scope units after fusermount. With Secret Service backend: auto-starts after login (waits for keyring unlock, `TimeoutStartSec=0`). The keyring wait and quadlet `KeyringBackend` flag both check the *configured* `secret_backend` setting via `resolveSecretBackend()` (not the runtime probe result), so the quadlet is correct even when generated with a locked keyring. Resets the cached credential store + keyring state on each retry so the keyring is detected once D-Bus becomes available at boot. With KeePass or no backend: requires `ov start` (prompts for password). Per-volume explicit paths supported via `--volume name:encrypt:/path`.
- **Resolution chain:** env var > keyring > config file > default. Migration: `ov settings migrate-secrets`.
- Source: `ov/credential_store.go` (interface), `ov/credential_keyring.go`, `ov/credential_config.go`, `ov/credential_kdbx.go`, `ov/secrets.go`

**Project-Level Environment Secrets (direnv + GPG)** -- Separate from ov's credential store:
- Project-level env vars (e.g., `GMAIL_USER`, `GMAIL_PASSWORD`) are stored in `.secrets` — a GPG-encrypted file containing `KEY=VALUE` lines (same format as `.env`)
- `.envrc` calls `eval "$(ov secrets gpg env)"` which decrypts `.secrets` in memory via gpg-agent — no plaintext on disk. No external `direnvrc` dependency needed
- Prerequisites: gpg-agent running with passphrase cached (locally via KeePassXC/pinentry, or remotely via SSH agent forwarding)
- Managed via `ov secrets gpg` subcommands: `show`, `env`, `edit`, `encrypt`, `decrypt`, `set`, `unset`, `add-recipient`, `recipients`, `import-key`, `export-key`, `setup`, `doctor`. All shell out to `gpg`. Example: `eval "$(ov secrets gpg env)"` to load secrets, `ov secrets gpg set API_KEY sk-xxx`, `ov secrets gpg show`, `ov secrets gpg edit`
- `ov secrets gpg env` silently exits 0 if `.secrets` doesn't exist (safe for `.envrc`). Outputs `export KEY='value'` lines parsed via `ParseEnvBytes` (skips comments/blanks, strips quotes)
- **GPG key management:** `ov secrets gpg import-key <path>` imports from file/directory, `--from-keystore` restores from KeePassXC Secret Service. `ov secrets gpg export-key <dir>` exports to filesystem, `--to-keystore` backs up to KeePassXC. `ov secrets gpg setup` configures gpg-agent (pinentry-qt + 8h cache), enables systemd sockets, imports/generates key, stores passphrase in Secret Service for all keygrips (primary + subkeys). `ov secrets gpg doctor` validates the full chain. `--prompt-passphrase` / `-p` flag on setup for secure interactive entry
- **KeePassXC/Secret Service integration:** pinentry-qt (linked against libsecret) queries `org.freedesktop.secrets` (KeePassXC) for passphrase lookup using schema `org.gnupg.Passphrase` with `keygrip` attribute. `presetPassphrasesFromSS()` injects passphrases from Secret Service into gpg-agent cache via `gpg-preset-passphrase`, bypassing pinentry for non-interactive contexts (doctor, setup verification, CI). Key backups stored with schema `org.gnupg.Key` and `keyid` attribute. On decryption failure, `diagnoseGPGDecryptionFailure()` prints actionable diagnostics: recipient key ID, keyring status, Secret Service availability, and specific `ov secrets gpg` fix commands
- `.secrets` is gitignored (`.gitignore`). `.env` is also gitignored and Syncthing-ignored
- **Distinction:** `.secrets`/direnv handles project-level shell env vars loaded before any command. ov's credential store (`ov secrets`, keyring, kdbx) handles container-level secrets (VNC passwords, service credentials) provisioned at `ov config` time
- Source: `ov/secrets_gpg.go` (`SecretsGpgEnvCmd`, `SecretsGpgSetupCmd`, `SecretsGpgDoctorCmd`, `SecretsGpgImportKeyCmd`, `SecretsGpgExportKeyCmd`, `diagnoseGPGDecryptionFailure`, `presetPassphrasesFromSS`), `ov/envfile.go` (`ParseEnvBytes`)

**Volume Management** -- Unified deploy-time volume backing:
- Layers declare `volumes:` in `layer.yml` (name + container path) -- what persistent storage is needed
- All volumes default to Docker/Podman named volumes (`ov-<image>-<name>`)
- At `ov config` time, any volume's backing can be changed per-volume: named volume (default), host bind mount, or encrypted (gocryptfs)
- Flags: `--volume name:type[:path]` (canonical), `--bind name[=path]` (shorthand), `--encrypt name` (shorthand). Type accepts both `encrypted` and `encrypt` (normalized)
- Per-volume encrypted path: `--volume name:encrypt:/path` stores `cipher/` and `plain/` directly inside the specified path (no `ov-<image>-<name>` prefix). Without explicit path, uses global `encrypted_storage_path` with prefix (backward compat)
- Env var automation: `OV_VOLUMES_<IMAGE>` (e.g., `OV_VOLUMES_IMMICH="library:bind:/mnt/nas,import:bind"`)
- Auto-path for bind mounts without explicit host path: `<volumes_path>/<image>/<name>` (default: `~/.local/share/ov/volumes/`)
- Configurable base: `ov settings set volumes_path /mnt/nas/ov-volumes` (env: `OV_VOLUMES_PATH`)
- Deploy.yml persists volume config: `volumes: [{name: data, type: bind, host: ~/data}]`. For encrypted type, `host:` stores the per-volume storage directory
- Encrypted volumes (default, no host): gocryptfs at `<encrypted_storage_path>/ov-<image>-<name>/{cipher,plain}`. With explicit host path: `<host>/{cipher,plain}`
- **Data provisioning:** `ov config` automatically provisions data from data layers into bind-backed volumes (`--seed` default true). `ov update` merges new data non-destructively (`cp -an`). `--force-seed` overwrites. `--data-from <image>` seeds from a separate data image. Deploy.yml tracks `data_seeded` (bool) and `data_source` (image:tag) per volume
- There is NO `bind_mounts` field in `images.yml` or OCI labels -- volume backing is purely a deploy-time decision
- Source: `ov/deploy.go` (`DeployVolumeConfig`, `ResolveVolumeBacking`), `ov/data.go` (`provisionData`), `ov/enc.go` (`ResolvedBindMount`), `ov/runtime_config.go` (`VolumesPath`)

**Environment Provides (`env_provides`)** -- Cross-container environment injection:
- Layers declare `env_provides:` in `layer.yml` — a `map[string]string` of env vars to inject into OTHER containers when this service is deployed. Distinct from `env:` which is baked into the service's own image
- Templates support `{{.ContainerName}}` which resolves to the actual container name (e.g., `ov-ollama`, or `ov-ollama-staging` with `--instance staging`)
- At `ov config` time, `injectEnvProvides()` resolves templates and writes vars into the global `env:` list in `deploy.yml` (top-level, shared across all images)
- `env_provides_sources:` in `deploy.yml` tracks which image injected each key (for cleanup and self-exclusion)
- `filterOwnEnvProvides()` excludes an image's own injected vars from its own env (e.g., ollama's `env: OLLAMA_HOST=0.0.0.0` is its server bind, not the client URL)
- `--update-all` flag on `ov config` regenerates quadlets for all other deployed images to pick up new global env vars
- On `ov config remove` / `ov remove`, `cleanDeployEntry()` removes global env vars injected by the removed image
- Env var priority (last wins): global env (env_provides) < per-image deploy env < deploy env_file < workspace .env < CLI --env-file < CLI -e flags
- OCI label `org.overthinkos.env_provides` stores the templates in the image for deploy-only scenarios (no `images.yml` needed)
- Source: `ov/config_image.go` (`injectEnvProvides`, `updateAllDeployedQuadlets`), `ov/deploy.go` (`filterOwnEnvProvides`, `appendOrReplaceEnv`, `removeEnvByKey`, `cleanDeployEntry`), `ov/envfile.go` (`ResolveEnvVars`), `ov/validate.go` (`validateEnvProvides`), `ov/generate.go` (emits label), `ov/labels.go` (`LabelEnvProvides`, `ImageMetadata.EnvProvides`)

**Environment Requires & Accepts (`env_requires`, `env_accepts`)** -- Declared env var dependencies:
- Layers declare `env_requires:` and `env_accepts:` in `layer.yml` — lists of `EnvDependency` structs (name, description, optional default)
- `env_requires:` — env vars the layer MUST have from the environment to function (e.g., API keys). At `ov config` time, `warnMissingEnvRequires()` prints warnings for missing required vars
- `env_accepts:` — env vars the layer CAN optionally use (e.g., optional messaging tokens). No warnings if missing — purely for documentation and discoverability
- Both fields use the same struct: `EnvDependency{Name, Description, Default}`
- Multiple layers in an image merge their declarations; deduplicated by name (last wins, sorted for deterministic output)
- OCI labels: `org.overthinkos.env_requires` and `org.overthinkos.env_accepts` (JSON arrays)
- Validation: name must be valid env var format, no duplicates within same layer, same var cannot appear in both requires and accepts
- `env_requires` and `env_accepts` are distinct from `env:` (baked into image), `env_provides:` (injected into other containers), and `secrets:` (provisioned from credential store)
- Source: `ov/layers.go` (`EnvDependency`, `LayerYAML.EnvRequires/EnvAccepts`), `ov/validate.go` (`validateEnvDeps`), `ov/config_image.go` (`warnMissingEnvRequires`), `ov/generate.go` (emits labels), `ov/labels.go` (`LabelEnvRequires`, `LabelEnvAccepts`, `ImageMetadata.EnvRequires/EnvAccepts`)

**Tunnel Backend Schemes** -- Port protocol annotations control tunnel behavior:
- Layers declare port protocols in `layer.yml`: `ports: ["https+insecure:3000", "tcp:5900", 8888]`
- Protocol determines the backend URL scheme for `tailscale serve/funnel` and `cloudflared` ingress
- Stored in OCI label `org.overthinkos.port_protos` (JSON map, only non-http entries)
- Tailscale schemes: `http` (default), `https`, `https+insecure`, `tcp`, `tls-terminated-tcp`
- Cloudflare schemes: `http` (default), `https`, `tcp`, `ssh`, `rdp`, `smb`
- `udp` ports are never tunneled (warning printed); accessible directly between tailnet nodes
- Scheme → Tailscale flag: `http/https/https+insecure` → `--https`, `tcp` → `--tcp`, `tls-terminated-tcp` → `--tls-terminated-tcp`
- Scheme → target URL: `schemeTarget(scheme, port)` builds `scheme://127.0.0.1:port` (TCP-family uses `tcp://`)
- Validation: `ov validate` checks port schemes against provider capabilities (e.g., `ssh` valid for cloudflare but not tailscale)
- Source: `ov/tunnel.go` (`schemeTarget`, `tailscaleFlag`, `isTCPFamily`, `validTailscaleSchemes`, `validCloudflareSchemes`), `ov/quadlet.go`, `ov/validate.go`

**Agent Forwarding (SSH & GPG)** -- Runtime socket forwarding into containers:
- SSH: host `$SSH_AUTH_SOCK` → container `/run/host-ssh-auth.sock` + `SSH_AUTH_SOCK` env var
- GPG: host `S.gpg-agent` (detected via `gpgconf --list-dirs agent-socket`) → container `$HOME/.gnupg/S.gpg-agent` (home from `org.overthinkos.home` image label)
- Settings: `forward_gpg_agent` and `forward_ssh_agent` (default: `true`). Env: `OV_FORWARD_GPG_AGENT`, `OV_FORWARD_SSH_AGENT`
- Per-image override: `deploy.yml` `forward_gpg_agent` / `forward_ssh_agent` boolean fields on `DeployImageConfig`
- Applied in: `ov shell` (new container: volumes + env; exec into running: env only), `ov start` (direct mode only), `ov cmd` (env only)
- **NOT applied in quadlet mode** — socket paths are session-bound (change between SSH sessions, reboots); quadlets are static systemd units
- Container has its own keyring — public keys must be imported separately (`gpg --export --armor KEY_ID | ov shell <image> -c 'gpg --import'`)
- The `agent-forwarding` metalayer (`gnupg` + `direnv` + `ssh-client`) provides the container-side binaries. Included in all application images
- Source: `ov/agent_forward.go` (socket detection, mount resolution), `ov/runtime_config.go` (settings)

**`task` (Taskfile)** -- bootstrap only: builds `ov` from source and creates the buildx builder. Source: `Taskfile.yml` + `taskfiles/{Build,Setup}.yml`. All other operations use `ov` directly.

**What gets generated** (`ov generate`):
- `.build/<image>/Containerfile` -- one per image, unconditional `RUN` steps only
- `.build/<image>/traefik-routes.yml` -- traefik dynamic config (only for images with `route` layers)
- `.build/<image>/<fragment_dir>/*.conf` -- init system service configs (driven by `init.yml`, e.g., `supervisor/` for supervisord, `systemd/` for systemd)
- `.build/_layers/<name>` -- symlinks to remote layer directories (only when remote layers used)

Generation is idempotent. `.build/` is disposable and gitignored.

**Generic init system support via `init.yml`:**
- Init systems (supervisord, systemd, s6, etc.) are fully defined in `init.yml` at project root
- Each init system declares: detection rules (`layer_fields`, `layer_files`), build model (`fragment_assembly` or `file_copy`), Go templates for fragment generation, Containerfile stage emission, config assembly, entrypoint, runtime service management commands, and OCI labels
- Adding a new init system requires only editing `init.yml` -- no Go code changes
- `service:` field in layer.yml maps to supervisord (via `layer_fields: [service]`), `*.service` files and `system_services:` map to systemd (via `layer_files` and `layer_fields`)
- Images use `org.overthinkos.init` OCI label to identify their init system at runtime
- Per-init service list stored in `org.overthinkos.services.<init>` label
- Source: `init.yml` (definitions), `ov/init_config.go` (Go structs + loading + template rendering)

**Multi-distro support via `distro:` and `build:` fields:**
- `distro:` — Distro identity tags in priority order: `distro: ["fedora:43", fedora]`. For packages: first matching section wins (override). For tasks: all matching run (additive).
- `build:` — Package formats tied to builders: `build: [rpm]` or `build: [pac, aur]`. ALL formats installed in order. Replaces old `pkg:` field.
- `builds:` — Builder capabilities on builder images (unchanged): `builds: [pixi, npm, cargo]`
- Tags union (`org.overthinkos.tags`) = `["all"]` + distro + build formats — used for task matching
- Source: `ov/config.go` (`ResolvedImage.Distro`, `ResolvedImage.BuildFormats`, `MatchingTasks`), `ov/format_config.go` (YAML config loading), `ov/format_template.go` (template rendering), `ov/init_config.go` (init system config), `distro.yml` + `builder.yml` + `init.yml` (format definitions at project root, referenced via `format_config:` in `images.yml`)

**Pixi manylinux fix:** `ov generate` injects `[system-requirements] libc = { family = "glibc", version = "2.39" }` into every pixi.toml during build if not already present. This fixes pixi 0.66.0's resolver which incorrectly detects the platform as `manylinux_2_28` on glibc 2.42, rejecting `manylinux_2_34` wheels (e.g., pixelflux 1.5.9). Source: `builder.yml` `manylinux_fix` template, rendered by `ov/generate.go`.

**Pixi build scripts:** The pixi builder supports an optional `build_script: build.sh` field in `builder.yml`. If a layer with `pixi.toml` also has a `build.sh`, the script runs in the pixi builder stage after `pixi install` completes. The script is bind-mounted from the layer context (same pattern as the cargo builder's `--mount=type=bind,from=<layer>-ctx`). This allows layers to run build-time logic (compiling C extensions, npm builds, binary patching) without installing build dependencies in the final image. Example: the `selkies` layer uses `build.sh` to pip-install selkies (C extensions need gcc) and build the web UI (needs nodejs/npm) — all in the builder image. Source: `builder.yml` `build_script` field, `ov/generate.go` `buildStageContext()`, `ov/format_template.go` `BuildStageContext.HasBuildScript`.

---

## Directory Structure

```
project/
+-- bin/ov                    # Built by `task build:ov` (gitignored)
+-- ov/                       # Go module (go 1.25.3, kong CLI, go-containerregistry)
+-- distro.yml                # Distro bootstrap + package format definitions (referenced via images.yml)
+-- builder.yml               # Multi-stage builder definitions (referenced via images.yml)
+-- init.yml                  # Init system definitions: supervisord, systemd (referenced via images.yml)
+-- .build/                   # Generated (gitignored)
+-- images.yml                # Image definitions
+-- setup.sh                  # Bootstrap: downloads task, builds ov
+-- Taskfile.yml              # Bootstrap tasks only
+-- taskfiles/                # Build.yml, Setup.yml
+-- layers/<name>/            # Layer directories (157 layers)
+-- plugins/                  # Git submodule (overthink-plugins)
+-- templates/                # supervisord.header.conf (referenced by init.yml header_file)
```

### Two-Layer Sync Architecture

Git handles public/shared artifacts. Syncthing handles private/machine-specific state. `.gitignore` is the boundary.

| What | Synced by | Visibility |
|------|-----------|------------|
| Code, CLAUDE.md, skills, layers | Git | Public (committed) |
| `.claude/memory/` | Syncthing | Private (gitignored) |
| `.claude/settings.local.json` | Syncthing | Private (gitignored) |
| `.claude/settings.json` | Git | Public (committed) |

Memory setup: `autoMemoryDirectory: ".claude/memory"` in `.claude/settings.local.json`. Both settings.local.json and memory/ sync via Syncthing automatically.

### Plugins Submodule

Skills, agents, and MCP servers live in a separate git submodule at `plugins/`.

**Repository:** `git@github.com:overthinkos/overthink-plugins.git`

```
plugins/
+-- .claude-plugin/marketplace.json   # Central plugin registry
+-- ov/                               # Operations (36 skills)
+-- ov-dev/                           # Development (2 skills, 3 agents, GitHub MCP)
+-- ov-jupyter/                       # Jupyter MCP server (notebook collaboration via Streamable HTTP)
+-- ov-layers/                        # Layer reference (157 skills)
+-- ov-images/                        # Image reference (42 skills)
```

Each plugin has a `.claude-plugin/plugin.json` manifest. Skills are at `plugins/<plugin>/skills/<name>/SKILL.md`.

**Enabled via** `.claude/settings.json` (committed):

```json
{
  "enabledPlugins": {
    "ov@ov-plugins": true,
    "ov-dev@ov-plugins": true,
    "ov-jupyter@ov-plugins": true,
    "ov-layers@ov-plugins": true,
    "ov-images@ov-plugins": true
  },
  "extraKnownMarketplaces": {
    "ov-plugins": {
      "source": { "source": "directory", "path": "./plugins" }
    }
  }
}
```

**Submodule operations:**
- Clone with plugins: `git clone --recurse-submodules`
- Update plugins: `git submodule update --remote plugins`
- After pulling main repo: `git submodule update --init`

---

## Key Rules

- Lowercase-hyphenated names for layers and images
- Taskfiles for bootstrap only (building ov), Go for all other logic
- Never `pip install`, `conda install`, or `dnf install python3-*`. Pixi is the only Python package manager
- `.build/` is disposable; all generated files start with `# <path> (generated -- do not edit)`
- `USER <UID>` (numeric) not `USER <name>` in generated Containerfiles
- All logic belongs in `ov`. Tasks are only for bootstrap. Every public task has `desc:`
- Always recommend quadlet mode for deployment. Direct mode is only a fallback for platforms without quadlet support
- MUST invoke skills before exploring the codebase. Skills are the primary knowledge source, not the code itself
- `root.yml`/`user.yml` use `all:` task for common logic, with optional tag-specific tasks (`rpm:`, `pac:`, `fedora:`, etc.). Never use `install:` as a task name
- `distro:` field defines identity tags: `distro: ["fedora:43", fedora]`. First matching section overrides packages. Inherited through base chain
- `build:` field defines package formats: `build: [rpm]` or `build: [pac, aur]`. ALL formats installed in order. Inherited through base chain. Default: `[rpm]`
- Images with layers that trigger an init system (via `service:`, `port_relay:`, `system_services:`, or `*.service` files) must include the init system's `depends_layer` in their dependency chain. `ov validate` enforces this as a hard error (e.g., supervisord layers need the `supervisord` layer). Detection rules and dependencies are defined in `init.yml`, not hardcoded

- Data layers use `data:` field in layer.yml to map source directories to volume targets. Data is staged at `/data/<volume>/` in the image at build time. Provisioned into bind-backed volumes by `ov config` (initial seed) and `ov update` (non-destructive merge). Data layers are valid with only `data:` and `volumes:` — no packages or install files needed
- Data images use `data_image: true` in images.yml — always FROM scratch, no base OS, no runtime, no init system. Only data staging + labels. Used as seed sources via `--data-from`. `ov validate` enforces: no base, no services, no ports
- Layers needing ffmpeg codecs MUST depend on the `ffmpeg` layer (`depends: [ffmpeg]`) rather than independently adding the negativo17 fedora-multimedia repo. The `ffmpeg` layer is the single authoritative install point for nonfree codecs. This avoids repo duplication and ensures consistent codec builds across all images
- `ov merge` handles OCI whiteout semantics: regular whiteouts (`.wh.<name>`), opaque whiteouts (`.wh..wh..opq`), and reintroduction-supersedes-whiteout cases. This prevents EEXIST errors when merging layers that contain file deletions. Source: `ov/merge.go` (`whiteoutTarget`, `mergeLayers`)
- `env_provides:` in `layer.yml` declares env vars injected into OTHER containers at deploy time. Template syntax: `{{.ContainerName}}` (only supported variable). `env:` and `env_provides:` may declare the same key — `env:` is baked into the service's own image (e.g., `OLLAMA_HOST=0.0.0.0`), `env_provides:` is injected into consumers (e.g., `OLLAMA_HOST=http://ov-ollama:11434`). Cleanup is automatic on `ov config remove` / `ov remove`. `--update-all` on `ov config` propagates to all deployed quadlets
- `env_requires:` in `layer.yml` declares env vars the layer MUST have from the environment (e.g., `OPENROUTER_API_KEY`). At `ov config` time, missing required vars produce warnings. Structure: list of `{name, description, default?}`
- `env_accepts:` in `layer.yml` declares env vars the layer CAN optionally use (e.g., `TELEGRAM_BOT_TOKEN`). No warnings if missing — for documentation only. Same structure as `env_requires`
- `ov start` in quadlet mode requires `ov config` first — no auto-configuration. Direct mode still supports inline flags
- Port protocol annotations control tunnel backend schemes: `"https+insecure:3000"` tells Tailscale to use `https+insecure://` when proxying. Default is `http`. Supported: `http`, `https`, `https+insecure`, `tcp`, `tls-terminated-tcp` (Tailscale); `http`, `https`, `tcp`, `ssh`, `rdp`, `smb` (Cloudflare). Ports with HTTPS backends (like Traefik self-signed) MUST use `https+insecure` to avoid 404 errors from plain HTTP proxying

### Two-Tier Layer Architecture for ML/Python Layers

ML layers follow a two-tier pattern that separates environment ownership from post-install steps:

**Tier 1 — Post-install layers** (no pixi.toml): Install binaries or pip packages into whatever pixi env exists. Reusable across images.
- `llama-cpp`: downloads prebuilt binaries + GGUF tools. Sets `LLAMA_CPP_PATH` env and PATH
- `unsloth`: pip installs vLLM wheel + unsloth + unsloth-zoo + vLLM torch.compile patch (`patch_vllm_size_nodes.py`) for `_decompose_size_nodes` bug (upstream: vllm-project/vllm#38360). vLLM 0.19 runtime deps (opentelemetry-*) installed via pip --no-deps after wheel (pixi conda/PyPI resolver conflict). Sets `HF_HOME`, `UNSLOTH_SKIP_LLAMA_CPP_INSTALL`
- `jupyter-colab-mcp`: CRDT MCP server extension (fastmcp + jupyter_colab_mcp). Installs into parent pixi env

**Tier 2 — Environment-owner layers** (have pixi.toml): Define the complete Python environment. Compose Tier 1 layers via `layers:` field.
- `python-ml`: core ML env (PyTorch, vLLM runtime deps, HF core). `layers: [llama-cpp]`
- `jupyter-colab`: lightweight Jupyter + CRDT collaboration. `layers: [jupyter-colab-mcp]`
- `jupyter-colab-ml`: full ML + Jupyter + CRDT MCP. Includes gcc/gcc-c++ for triton 3.6+ JIT compilation. `layers: [llama-cpp, unsloth, jupyter-colab-mcp]`
- `unsloth-studio`: fine-tuning env with studio UI. `layers: [llama-cpp, unsloth]`

**Key constraint:** The generator creates intermediate images for shared layers. Tier 1 layers with pip installs (like `unsloth`) must NOT be extracted into intermediates — they need the pixi env from Tier 2. The generator handles this correctly as long as pip-install layers don't have standalone pixi.toml. Only Tier 2 layers own pixi.toml; Tier 1 user.yml runs after pixi COPY in the final image build.

**ML training gotchas:**
- Ministral/Pixtral models require `UNSLOTH_ENABLE_FLEX_ATTENTION=0` due to nested torch.compile bug between unsloth + transformers 5.5 masking_utils
- Pixtral-12B requires `max_memory={0: "14GB"}` in `from_pretrained()` because accelerate's device_map uses uncompressed BF16 sizes
- TRL 1.0 requires `packing=True` in SFTConfig when unsloth auto-enables `padding_free=True`

- Meta-layers CAN have both `depends:` and `layers:` (e.g., `unsloth-studio` has `depends: [cuda, supervisord]` + `layers: [llama-cpp, unsloth]`)
- Meta-layers CAN own pixi.toml (environment-owner pattern — exactly one pixi.toml per image)

**Hermes Agent layer** (`hermes`) follows the Tier 2 pattern with `build.sh` (same as selkies): pixi.toml defines the Python env, build.sh clones the hermes-agent repo, pip installs it, and sets up npm deps. The `hermes-playwright` layer is a Tier 1 add-on for Playwright + Chromium.

**Build.sh and npm gotchas** (discovered during hermes testing):
- Playwright `npx playwright install --with-deps` does NOT support Fedora — falls back to Ubuntu's `apt-get`. Workaround: install Chromium system deps via rpm packages in `layer.yml`, browser binary via `npx playwright install chromium` (without `--with-deps`) in `root.yml`
- npm packages installed globally (via the npm builder's `package.json`) are in `~/.npm-global/lib/node_modules/` and need `NODE_PATH` to be `require()`d. For project-local deps (like agent-browser), install in `build.sh` instead of `package.json`
- `sounddevice` Python library needs `portaudio` rpm at runtime (not just build time)
- When `root.yml` installs Playwright browsers with `HOME=/tmp`, set `PLAYWRIGHT_BROWSERS_PATH=/tmp/.cache/ms-playwright` in `layer.yml` env so the runtime user finds them

For layer-specific rules (install files, packages, port_relay, secrets, data, env_provides, env_requires, env_accepts, cache mounts): `/ov:layer`

**Credential security:** Config files (`settings.yml`, `deploy.yml`) are written with `0600` permissions for new files. `ov` warns if existing files have overly permissive permissions but does not change them — the user must `chmod 600` themselves. Credentials are stored in system keyring when available; plaintext config file is the fallback. `ov settings migrate-secrets` migrates existing plaintext credentials to keyring. `ov doctor` reports credential storage health.

**GPU auto-detection:** `ov` detects host GPU hardware and injects appropriate config at runtime:
- **NVIDIA:** CUDA images get `--gpus all` / CDI device injection automatically
- **AMD ROCm:** Auto-detects `/dev/kfd` and `/dev/dri/renderD*`, injects `HSA_OVERRIDE_GFX_VERSION`, adds `video`/`render` groups. `ov udev` manages KFD device rules. `ov doctor` reports AMD GPU info
- Source: `ov/devices.go` (`DetectNvidiaGPU`, `DetectAMDGPU`)

**Security mounts:** `security.mounts` in `layer.yml` declares host bind mounts or tmpfs needed for device access. Stored in image labels, applied by `ov config`/`ov start`. Format: `host:container:options` (bind mount) or `tmpfs:path:options` (tmpfs). Generates `Volume=` or `Tmpfs=` in quadlets.
- Source: `ov/config.go` (`SecurityConfig.Mounts`), `ov/quadlet.go`, `ov/start.go`

---

## Command Map

Use `ov --help` and `ov <cmd> --help` for quick flag reference. For detailed usage, load the skill.

| Commands | Skill |
|----------|-------|
| `generate` | `/ov:generate` |
| `validate` | `/ov:validate` |
| `inspect` | `/ov:inspect` |
| `list` (images, layers, targets, services, routes, volumes, aliases) | `/ov:list` |
| `new layer` | `/ov:new` |
| `build` | `/ov:build` |
| `merge` | `/ov:merge` |
| `cmd <image> <command>` | `/ov:cmd` |
| `shell` | `/ov:shell` |
| `dbus` (notify, call, list, introspect) | `/ov:dbus` |
| `config <image>` (setup: quadlet + secrets + encrypted volumes + data provisioning + env_provides injection + env_requires validation), `config --update-all`, `config remove`, `config status/mount/unmount/passwd` | `/ov:config`, `/ov:deploy`, `/ov:enc` |
| `start` | `/ov:start` (requires `ov config` first in quadlet mode) |
| `stop` | `/ov:stop` |
| `status` (`--all`, `--json`) | `/ov:status` |
| `logs` | `/ov:logs` |
| `update` (`--seed`, `--force-seed`, `--data-from`) | `/ov:update` |
| `remove` (`--purge`, `--keep-deploy`) | `/ov:remove` |
| `deploy show/export/import/reset/status/path` | `/ov:deploy` |
| `service start/stop/restart/status` | `/ov:service` |
| `cdp`, `cdp spa` (click, type, key, key-combo, mouse, status) | `/ov:cdp` |
| `wl sway` | `/ov:wl` (sway subgroup) |
| `wl overlay show/hide/list/status` | `/ov:wl-overlay` |
| `record start/stop/list/cmd` | `/ov:record` |
| `tmux shell/cmd/run/attach/list/capture/send/kill` | `/ov:tmux` |
| `vnc` | `/ov:vnc` |
| `wl` | `/ov:wl` |
| `alias` | `/ov:alias` |
| `settings` (get, set, list, reset, path, migrate-secrets) | `/ov:settings` |
| `version` | `/ov:version` |
| `secrets` (init, list, get, set, delete, import, export, path) | `/ov:secrets` |
| `secrets gpg` (show, env, edit, encrypt, decrypt, set, unset, add-recipient, recipients, import-key, export-key, setup, doctor) | `/ov:secrets` |
| `udev status/generate/install/remove` | `/ov:udev` |
| `vm` | `/ov:vm` |
| `doctor` | `/ov:doctor` |

---

## Workflows

**Add a layer:** `ov new layer <name>` -> edit `layer.yml` -> add install files -> add to image in `images.yml` -> `ov build <image>`
Skills: `/ov:layer` -> `/ov-layers:<similar>` (pattern reference) -> `/ov:image` -> `/ov:build`

**Add an image:** add entry to `images.yml` -> `ov build <image>`
Skills: `/ov:image` -> `/ov-images:<similar>` (pattern reference) -> `/ov:build`

**Layer images:** set `base` to another image name in `images.yml`. The generator handles dependency ordering and tag resolution.

**Deploy a service:** `ov config <image> -w ~/project` -> saves all deployment state to `~/.config/ov/deploy.yml` -> generates quadlet + provisions secrets + mounts encrypted volumes + provisions data from data layers into bind-backed volumes + injects `env_provides` vars into global deploy.yml env for cross-container discovery + warns about missing `env_requires` vars. `--password auto` generates all secrets; `--password manual` prompts. `--seed` (default true) provisions data layers; `--force-seed` re-provisions; `--data-from <image>` seeds from a separate data image. `--update-all` regenerates quadlets for all other deployed images to pick up env_provides changes. `ov start <image>` requires `ov config` first in quadlet mode. No `images.yml` needed for deployment.
Skills: `/ov:config` (setup) -> `/ov:deploy` (deploy.yml) -> `/ov:start` -> `/ov:update` (data sync) -> `/ov:service` (lifecycle)

**Record a session:**
`ov record start <image> --mode terminal` (asciinema) or `--mode desktop` (pixelflux/wf-recorder) -> `ov record cmd` (interact) -> `ov record stop <image> -o output`
Skills: `/ov:record` -> `/ov-layers:wl-record-pixelflux` or `/ov-layers:wf-recorder` (desktop) or `/ov-layers:asciinema` (terminal)
Use `/ov:wl-overlay` for in-recording overlays (title cards, lower-thirds, countdowns, fade transitions) — composited by the compositor, appear natively in recordings without post-production.

**Host bootstrap (first time):** requires `go`, `docker` (or `podman`). Run `bash setup.sh` to download `task`, build `ov`, then `ov build` to build all images. To use podman: `ov settings set engine.build podman`.

---

## Task Commands (bootstrap only)

- `task build:ov` -- Build ov from source into `bin/ov` and install as Arch package (auto-calls `build:install`)
- `task build:install` -- Install ov as Arch package (uses pre-built binary from `bin/ov` via PKGBUILD, fast ~2s)
- `task setup:builder` -- Create multi-platform buildx builder
- `task setup:all` -- Full setup (build ov + create builder)

---

## Skills: Decision Architecture

### MANDATORY: Skills Before Exploration

**CRITICAL: You MUST invoke matching skills BEFORE reading source files, launching Explore agents, or using Grep/Glob to search the codebase.** This is a BLOCKING REQUIREMENT -- not a suggestion.

The skills system contains curated, structured knowledge for every component. Raw codebase exploration is slower, noisier, and misses context that skills provide.

**Required order:**
1. **Invoke skills** -- ALWAYS first. Match the task to skills using the tables below.
2. **Read CLAUDE.md** -- project rules already in context
3. **Read memory** -- prior learnings and user preferences
4. **Explore codebase** -- ONLY after confirming no skill covers the topic

**Hard rules:**
- If a skill exists for the topic, you MUST invoke it. No exceptions.
- For development tasks: invoke BOTH `/ov-dev:go` (code structure) AND the relevant `/ov:*` skill (expected behavior) before touching any `.go` file.
- For multi-step workflows: invoke ALL skills in the chain (e.g., build -> deploy -> service -> image).
- Explore agents are a LAST RESORT, not a first step. Justify why no skill covers the topic before launching one.

**Self-check before any codebase exploration:**
> "Is there a skill that covers this topic? If yes, invoke it first."

### First Branch: Using vs Developing

- **Using ov** (building/running images): `ov` + `ov-layers` + `ov-images` plugins
- **Developing ov** (Go CLI code): `ov-dev` plugin
- Bug fixes in ov often need both: `ov-dev` (how code works) + `ov:*` (expected behavior)

### Plugin Namespaces

| Plugin | Skills | Role | Question it answers |
|--------|--------|------|---------------------|
| `ov` | 36 | Operations | "How do I use X?" |
| `ov-dev` | 2 + 3 agents | Contributing | "How does the code work?" |
| `ov-jupyter` | 1 MCP server | Notebook MCP | "How do I use the notebook MCP tools?" |
| `ov-layers` | 157 | Layer reference | "What does layer X contain?" |
| `ov-images` | 42 | Image reference | "What does image X look like?" |

### Common Skill Chains

Real tasks chain through skills in predictable patterns:

**Author a new layer:**
`/ov:layer` (format, rules) -> `/ov-layers:<similar>` (existing pattern) -> `/ov:image` (add to image) -> `/ov:build`

**Debug a runtime issue:**
`/ov:<operation>` (how it works) -> `/ov-layers:<layer>` (config, deps, ports) -> `/ov:settings` or `/ov:service` (state)

**Desktop automation:**
`/ov:cdp` (DOM: click, type, eval) -> `/ov:wl` (compositor-agnostic: screenshots, input, window mgmt, clipboard, AT-SPI2) -> `/ov:wl` sway subgroup (sway-only: tree, layout, move, resize) -> `/ov:wl-overlay` (recording overlays: title cards, lower-thirds, countdowns, highlights, fades)
Use CDP first. Use `ov cdp click --wl` for selkies-desktop (no VNC). Use `ov wl` for screenshots, input, window management (`toplevel`, `close`, `fullscreen`), clipboard, and AT-SPI2 accessibility (`ov wl atspi find/click`). Use `ov wl sway` for sway-specific IPC features (tree, workspaces, layout, move, resize).
On NVIDIA headless: Both `ov vnc screenshot` and `ov wl screenshot` work correctly. VNC images use pixman (software renderer) via `sway-desktop-vnc`, with a DPMS workaround for wayvnc 0.9.1's headless power event bug.
For selkies-desktop (labwc): `ov wl` provides full automation. `ov wl sway` commands are sway-specific and won't work on labwc.

**Deploy a service:**
`/ov:deploy` (quadlet, tunnels) + `/ov:config` (setup: secrets, encrypted volumes) -> `/ov-images:<name>` (image config) -> `/ov:service` (lifecycle)

**Set up Selkies streaming (browser-accessible — working):**
`/ov-layers:selkies` (streaming engine) -> `/ov-layers:labwc` (compositor) -> `/ov-layers:waybar-labwc` (panel) -> `/ov-images:selkies-desktop` (image)
Uses labwc nested inside pixelflux's Wayland compositor. Access via `https://localhost:3000` (HTTPS with self-signed Traefik cert — required for WebCodecs secure context). NVENC detected but fails with driver 590.48 (pixelflux compat issue); CPU x264enc-striped at 60fps works well. Image: `selkies-desktop`.
**Host-side automation:** `ov wl` provides full compositor-agnostic control: screenshots (pixelflux-screenshot via capture bridge), input (wtype, wlrctl), window management (wlrctl toplevel), clipboard (wl-copy/paste), resolution (wlr-randr), AT-SPI2 introspection (atspi). Use `ov cdp click --wl` for selector-based clicks via Wayland pointer (no VNC needed). Screenshots work with or without a browser connected (capture bridge auto-switches between controller/viewer modes). Includes `wl-tools` + `a11y-tools` layers.
**Client-side interaction (browser-based RD):** The Selkies SPA uses a transparent `input#overlayInput` (z-index 3) on top of `canvas#videoCanvas` (z-index 2, pointer-events: none) to capture mouse/keyboard events. Events pass through the SPA's JavaScript → WebSocket → labwc. Keyboard passthrough works via VNC type, wtype, or CDP Input.dispatchKeyEvent — the SPA's onkeydown handler captures with stopImmediatePropagation. **Limitation:** Super key consumed by the client's compositor, Ctrl+T/W consumed by the client's Chrome — browser-based RD cannot forward compositor or browser shortcuts. Mouse coordinates have ~0.82x scaling between input and remote cursor position. Session state (all windows, typed text) survives client disconnection. See `/ov-images:selkies-desktop` for full DOM structure and coordinate mapping.

**Programmatic notebook access (MCP):**
`/ov-layers:jupyter-colab` (lightweight, no GPU) or `/ov-layers:jupyter-colab-ml` (full CUDA ML stack) or `/ov-layers:jupyter-colab-ml` + `/ov-layers:notebook-finetuning` + `/ov-layers:notebook-ollama` + `/ov-layers:notebook-llm-on-supercomputers` (ML + fine-tuning + Ollama + LLM course notebooks) -> `/ov-images:jupyter-colab` or `/ov-images:jupyter-colab-ml` or `/ov-images:jupyter-colab-ml-notebook` (deployment) -> `/ov:service` (lifecycle)
Start the service, then use MCP tools (`list_notebooks`, `open_notebook_session`, `insert_cell`, `execute_cell`, `watch_notebook`) for AI-driven notebook editing with real-time collaboration. Multiple MCP clients can edit the same notebook simultaneously — changes sync via CRDT. Use `jupyter-colab-ml-notebook` for GPU/ML with fine-tuning, Ollama, and LLM course notebooks; `jupyter-colab-ml` for GPU/ML without; `jupyter-colab` for lightweight multi-arch environments.

**Fix a bug in ov:**
`/ov-dev:go` (source map, tests) + `/ov:<relevant>` (expected behavior) -> `/ov:validate` (verify)

**Modify a metalayer:**
`/ov:layer` (metalayer patterns) -> `/ov-layers:<metalayer>` (current composition) + `/ov-layers:<addition>` (what to add)

**Deploy Hermes Agent:**
`/ov-layers:hermes` (layer properties) -> `/ov-images:hermes` (image config) -> `/ov:config` (setup) -> `/ov:start` -> `/ov:service` (lifecycle)
For browser automation, use `/ov-images:hermes-playwright` instead. Hermes npm deps (agent-browser, camoufox-browser) are project-local (in `~/hermes-agent/node_modules/`), not global.

**Deploy Hermes with Selkies desktop:**
`/ov-images:selkies-desktop-hermes` (image config) -> `/ov:config` -> `/ov:start` -> access `https://localhost:3000`
Combines Selkies remote desktop with Hermes AI agent + Claude Code + Codex + Gemini. `/ov-images:selkies-desktop-hermes-jupyter` adds Jupyter at `:8888` with MCP notebook access.

**Full image lifecycle (build -> deploy -> test):**
`/ov:build` (build image) -> `/ov:deploy` (quadlet, tunnels, volume backing) -> `/ov:service` (config, start, status, logs) -> `/ov-images:<name>` (ports, verification)

### Continuous Improvement: Feeding Insights Back Into Skills

Skills are living documents. When real-world usage reveals gaps, update them:

**What triggers a skill update:**
- A deployment step fails or requires undocumented workarounds
- A verification check is missing from an image skill
- A skill's recommended order or defaults are wrong (e.g., direct vs quadlet)
- A gotcha or prerequisite is discovered during actual usage

**How to feed back:**
1. During the session, update the relevant skill file at `plugins/<plugin>/skills/<skill-name>/SKILL.md`
2. If the insight affects cross-skill behavior, update CLAUDE.md too
3. After any non-trivial deployment session, ask: "Did we learn anything that future sessions should know?"

**When NOT to update skills:** ephemeral issues, user-specific config (use memory), bug fixes in ov code (use git)

### Disambiguating Overlapping Skills

Rule of thumb:
- `/ov:X` = "how do I USE X?" (operations, commands, flags)
- `/ov-layers:X` = "what does layer X CONTAIN?" (deps, ports, volumes, env, packages)
- `/ov-images:X` = "what does image X LOOK LIKE?" (base, layers, platforms, lifecycle)

Examples where multiple skills cover one topic:
- **Jupyter:** `/ov-layers:jupyter` (legacy GPU/ML monolithic layer) vs `/ov-layers:jupyter-colab` (lightweight, no GPU + collaboration + MCP server with 13 tools) vs `/ov-layers:jupyter-colab-ml` (full CUDA ML + collaboration + MCP, meta-layer composing llama-cpp + unsloth) vs `/ov-images:jupyter` (legacy GPU image) vs `/ov-images:jupyter-colab` (lightweight image) vs `/ov-images:jupyter-colab-ml` (GPU image with full ML stack + MCP) vs `/ov-images:jupyter-colab-ml-notebook` (GPU image + 37 Unsloth fine-tuning notebooks + 6 Ollama integration notebooks + 15 LLM course notebooks). The `ov-jupyter` plugin provides the Streamable HTTP MCP server at `/mcp` for programmatic notebook access
- **OpenClaw:** `/ov:openclaw` (gateway config) vs `/ov-layers:openclaw` (layer properties) vs `/ov-images:openclaw` (image definition)
- **Chrome/CDP:** `/ov:cdp` (CDP commands) vs `/ov-layers:chrome` (ports, relay, shm_size) vs `/ov-layers:chrome-sway` (sway integration)
- **Sway:** `/ov:wl` sway subgroup (`ov wl sway <cmd>`, compositor commands) vs `/ov-layers:sway` (layer properties) vs `/ov-layers:sway-desktop` (desktop metalayer)
- **VNC:** `/ov:vnc` (VNC commands, auth) vs `/ov-layers:wayvnc` (VNC server layer properties)
- **Niri:** `/ov-layers:niri` (compositor, built from source) vs `/ov-layers:niri-desktop` (desktop metalayer)
- **KWin:** `/ov-layers:kwin` (compositor, virtual backend) vs `/ov-layers:kwin-desktop` (desktop metalayer)
- **Mutter:** `/ov-layers:mutter` (compositor, headless) vs `/ov-layers:mutter-desktop` (desktop metalayer)
- **X11 Desktop:** `/ov-layers:xorg-headless` (display server) vs `/ov-layers:openbox` (window manager) vs `/ov-layers:x11-desktop` (desktop metalayer)
- **D-Bus/Notifications:** `ov dbus` (native Go D-Bus commands) vs `/ov-layers:dbus` (session bus layer) vs `/ov-layers:swaync` (notification daemon) vs `/ov-layers:libnotify` (`notify-send` CLI)
- **Command Execution:** `ov cmd` (single command with notification) vs `ov shell -c` (full container setup) vs `ov tmux cmd` (send to tmux session) vs `ov record cmd` (send to recording session)
- **Recording:** `/ov:record` (recording commands, lifecycle) vs `/ov-layers:asciinema` (terminal recording layer) vs `/ov-layers:wf-recorder` (sway desktop recording) vs `/ov-layers:wl-record-pixelflux` (selkies desktop recording)
- **Overlays:** `/ov:wl-overlay` (overlay commands, types, recording workflow) vs `/ov-layers:wl-overlay` (layer properties, gtk4-layer-shell deps)
- **Selkies:** `/ov-layers:selkies` (streaming engine, pixelflux/pcmflux) vs `/ov-layers:labwc` (nested compositor) vs `/ov-layers:waybar-labwc` (panel for labwc) vs `/ov-layers:selkies-desktop` (desktop metalayer) vs `/ov-images:selkies-desktop` (image)
- **Hermes:** `/ov-layers:hermes` (agent layer: pixi env, build.sh, service, volumes) vs `/ov-layers:hermes-playwright` (Playwright + Chromium system deps) vs `/ov-images:hermes` (headless agent) vs `/ov-images:hermes-playwright` (with browser automation) vs `/ov-images:selkies-desktop-hermes` (Selkies desktop + hermes + claude-code + codex + gemini) vs `/ov-images:selkies-desktop-hermes-jupyter` (+ jupyter-colab at `:8888`)
- **Tunnels:** `/ov:deploy` (tunnel providers, backend schemes, quadlet integration, deploy.yml) vs `/ov:layer` (port protocol annotations, `ports:` field syntax) vs `/ov:config` (tunnel setup at deploy time)

### Desktop Automation Hierarchy

Seven abstraction levels for interacting with container desktops:

| Level | Skill | Interface | When to use |
|-------|-------|-----------|-------------|
| SPA | `/ov:cdp` spa | CDP Input events via SPA overlay | Remote desktop through browser (selkies) -- bypasses local compositor/Chrome shortcuts |
| Semantic | `/ov:wl` atspi | AT-SPI2 tree | Find elements by name/role -- most reliable for non-web UIs |
| DOM | `/ov:cdp` | CSS selectors, JS eval | Chrome content -- structured, fast |
| AX Tree | `/ov:cdp` axtree | CDP Accessibility | Chrome UI elements, menus, buttons via CDP |
| Wayland | `/ov:wl` | grim, wtype, wlrctl | Screenshots, input, windows -- compositor-agnostic (sway + labwc) |
| Pixel | `/ov:vnc` | VNC coordinates, framebuffer | Remote access -- when TCP connectivity needed |
| Window | `ov wl sway` | Sway IPC (swaymsg) | Sway-only: tree, layout, move, resize, workspaces |
| Overlay | `/ov:wl-overlay` | gtk4-layer-shell | Recording overlays -- title cards, lower-thirds, countdowns, fades |

**CDP → SPA bridge:** Use `ov cdp spa key-combo <image> <tab> super+e` to send modifier combos (Super, Ctrl+T, Alt+F4) through the SPA to the remote desktop. CDP Input events bypass the local compositor and Chrome shortcut handlers -- this is the only way to send these combos to the remote desktop. Use `ov cdp spa click --scale 0.824,0.836` for coordinate-corrected mouse clicks on the SPA canvas.
**CDP → WL bridge:** Use `ov cdp click <image> <tab> <selector> --wl` to find elements by CSS selector and click via wlrctl. Critical for selkies-desktop (no VNC server). Same pattern as `--vnc` but uses Wayland pointer.

### ov-dev Agents

The `ov-dev` plugin includes 3 blocking enforcement agents (automatic, not invoked manually):

| Agent | Trigger | Purpose |
|-------|---------|---------|
| layer-validator | Before editing `layer.yml` | Validates structure and field types |
| root-cause-analyzer | Any error in output | Deep 8-step root cause analysis |
| testing-validator | Claiming something "works" | Verifies actual local test results |


## AI Attribution (Fedora Policy Compliant)

Per [Fedora AI Contribution Policy](https://docs.fedoraproject.org/en-US/council/policy/ai-contribution-policy/), Claude **MUST** include the `Assisted-by: Claude` trailer with a **confidence statement** in all commits:

```
<commit message>

Assisted-by: Claude (fully tested and validated)
```

## Confidence Statements (Required)

All AI-assisted contributions **MUST** include a confidence statement indicating verification level:

| Statement | When to Use | Evidence |
|-----------|-------------|----------|
| `fully tested and validated` | Overlay testing + all 9 testing standards met | Complete LOCAL system verification |
| `analysed on a live system` | Observed live system behavior, logs checked | Partial testing, live analysis |
| `syntax check only` | Pre-commit hooks passed, no functional testing | ShellCheck, yamllint, etc. passed |
| `theoretical suggestion` | No validation performed | AVOID - indicates unverified code |

**Choosing the Right Level:**

1. **Used overlay testing + verified all functionality?** → `fully tested and validated`
2. **Observed live system behavior, checked logs?** → `analysed on a live system`
3. **Only ran pre-commit hooks?** → `syntax check only`
4. **No validation at all?** → `theoretical suggestion` (avoid when possible)

**Examples:**

```
Fix: Add fuse-overlayfs for container startup

Tested via overlay session on LOCAL system.
All 9 testing standards verified.

Assisted-by: Claude (fully tested and validated)
```

```
Refactor: Simplify build cache logic

Reviewed logic and checked logs on live system.

Assisted-by: Claude (analysed on a live system)
```

```
Feat: Add initial WinBoat support structure

Skeleton implementation, pre-commit validation passed.
Requires testing on Windows environment.

Assisted-by: Claude (syntax check only)
```

**MANDATORY for Claude:**

- **ALWAYS** include confidence statement - this is non-negotiable
- Trailer goes after commit body, separated by blank line
- Required for ALL Claude-assisted commits (code, docs, configs)
- Only exception: trivial grammar/spelling corrections

**GitHub Issues and PRs:**

When creating issues or PR descriptions, include at the end:

```markdown
---
*Assisted-by: Claude (fully tested and validated)*
```