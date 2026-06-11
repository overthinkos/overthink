# Changelog

**This file is the ONE and ONLY home for historical content in this repository.**

`CLAUDE.md`, `README.md`, `plugins/README.md`, and every skill
(`plugins/**/SKILL.md`) describe the **current** state of the system — present
tense, forward-looking. Any reference to a previous version, a past rename, a
completed cutover or migration, a relocated / deleted / retired identifier, a
"previously / formerly / was / no longer", a dated change note, or a
commit-referenced cautionary tale belongs **here** and nowhere else. When a
cutover lands, append its narrative to this file as the post-execution record;
state the standing rules it establishes forward-looking in CLAUDE.md / skills
with no history. This file is the sanctioned "changelog context" named by
CLAUDE.md R5's grep self-test.

Entries are reverse-chronological. Dates use the project's `YYYY-MM-DD` stamp;
entries whose exact day was never recorded are grouped at the end of their month
under a `(day unspecified)` heading. Cutover paragraphs are preserved verbatim
from their former homes so nothing is lost in the relocation.

---

## 2026-06

### 2026-06-11 — fix(eval): fail fast when a score's pod target has no deploy entry on this host

`charly eval run <score>` restarts-but-never-creates its sandbox pod by design, but a host without the operator-provisioned `eval-sandbox` deploy entry surfaced the gap as podman's raw `no container with name or ID "charly-eval-sandbox"` exit-125 — the silent `cfg.Deploy[tn]` preflight skip hid the missing precondition (RCA'd live on this host, which has never carried the entry). The pod dispatch now resolves the entry through `scorePodTargetEntry` and errors actionably — `score X targets pod Y but no deploy entry exists on this host — provision the harness sandbox first: charly deploy add Y <ref> --disposable …` — and a per-host overlay load failure surfaces instead of being swallowed. Go tests cover the nil-config / missing-entry / present-entry branches; `/charly-eval:eval` now documents the sandbox as an operator-provisioned per-host precondition.

### 2026-06-11 — fix(eval): sweep the last three stale `eval.yml` references out of charly.yml's live prompt strings

The `default` and `scaffolding-selftest` scores' Ground-rules prompt lines told the harness AI "Recipe scenarios are immutable — they live in eval.yml" — a file that no longer exists (the beds/recipes live in this same file's `eval:` block) — and one `kind: local` comment said the same. All three now name the `eval:` block. Gate (operator-authorized this turn: parse + non-runtime standards): `charly box validate` exit 0, zero warnings; the live loader demonstrably parses past the edited lines (the score resolved during a run that failed later for an independent, pre-existing reason — the missing operator-provisioned `eval-sandbox` host deploy, see the fail-fast entry). The full live prompt-render leg awaits an `eval-sandbox` deploy on this host.

### 2026-06-11 — docs(policy)!: the charly CLI is the ONLY operational interface — ad-hoc podman/virsh/systemctl forbidden

Operator-directed mandate: every build / deploy / probe / lifecycle operation on charly-managed resources goes through a `charly` verb — never ad-hoc `podman` / `docker` / `virsh` / `systemctl` commands. Encoded as an R4 forbidden pattern + a Key Rules bullet in CLAUDE.md, with the ad-hoc→verb replacement table in `/charly-internals:strict-policy` (R4). Swept the instructive raw commands out of the operational docs (the service / charly-config / libvirt command skills included): the eval skill's 10 Testing Standards (podman inspect/run/ps, virsh dumpxml/domstate, systemctl --user → `charly status` / `charly eval box` / `charly eval libvirt` / `charly cmd` / `charly shell -c` / `charly logs`), CLAUDE.md R8's `podman inspect` label check (→ the charly capability surface), the charly-status skill's raw-inspect "direct queries" block (→ `charly status --json`), the disposable skill's `virsh dumpxml` (→ `charly eval libvirt domain-xml`), and the agents/eval skills' "remove the container" (→ `charly remove`). Descriptive mentions of podman/libvirt as the underlying machinery stay. Surfaced gaps, queued as their own cutover: no charly verb prints a BUILT image ref's `ai.opencharly.*` labels (R8's direct artifact-label probe — a `charly box labels <ref>`-class verb); `charly status` does not expose the deployed image ref+tag (the old `podman inspect '{{.Created}}'` freshness check); `charly config setup` has no force-secret-re-provisioning flag (the old `podman secret rm` path). Never ad-hoc podman in the meantime.

### 2026-06-11 — docs(claude-md)!: rewrite CLAUDE.md as a six-part structured rulebook; move all operational detail into the owning skills

CLAUDE.md (433 lines) had accumulated operational detail that duplicated the owning skills and had produced real drift. Rewritten from scratch as a ~323-line rulebook in six parts (I Dispatch → II Vision → III Ground Truth Rules → IV Process → V Agents & Attribution → VI Index), rules and mandates only:

- **R1–R10 unified into one contiguous, uniformly-templated block** (statement + *Scope* + *Detail* skill pointer). R10 relocated from "Disposable-Only Autonomy" into the Ground Truth Rules block (the old file apologized for the split); its four fraud clauses (dry-run-doesn't-count, rebuild-alone-doesn't-count, task-editing fraud, flag-override authorization) stay inline, compressed. Section names and R-numbering unchanged (≈60 consumer files cite them).
- **New "Enforcing VISION.md" binding table** — all 10 VISION tenets bound to an operational mandate + owning skill (previously only 4 tenets were operationalized as pillars); the table doubles as the file map, replacing the "How to read this file" paragraph.
- **The Skill Dispatcher became the SOLE copy** (regrouped into 8 activity groups; 3 row-consolidations of identical-target rows). The drifted mirror in `/charly-internals:skills` SKILL.md is deleted and replaced with a pointer; its drifted per-plugin skill-count table likewise becomes a pointer to `plugins/README.md`.
- **Detail moved INTO skills (one owner per fact):** the R10-run definition + task-editing-fraud + scope-shrinking-flags sections → `/charly-internals:disposable`; the full flag catalog as a new "Flag discipline" section + the R7 verb/bed-picking note → `/charly-eval:eval`; after-landing cleanliness/report format + if-R10-fails + the R6 tree-safety invariant + the worked commit-message example → `/charly-internals:git-workflow`; the RDD risk table + the R1 warning-is-a-failure clause + the R2 crossroad definition → `/charly-internals:strict-policy`; an "R9" section (rebuild-on-target + PKGBUILD `depends=` single source of truth) → `/charly-internals:go`; the docs-only attribution provision → the `testing-validator` agent; backports-as-canonical-non-blocking → `/charly-internals:cutover-policy`. The four pillars slim to their normative cores; "Key Rules" reduces to ≤2-line bullets + skill pointers and drops its self-duplicating "pointers to dedicated sections" block.
- **Stale-reference sweep (R5):** 15 references to a nonexistent "Law 3.6 / Law 4 / Law 5" numbering (CLAUDE.md never had Laws) fixed across `.claude/hooks/end-of-turn-challenge.sh`, `.claude/workflows/verify-beds.js` + `verify-status.js`, `plugins/internals/agents/eval-bed-runner.md`, and `plugins/internals/skills/agents/SKILL.md` — decoded to "R10 / Disposable-Only Autonomy", "Hard Cutover by Default", and "R10 flag-override clause". The hook's "End-of-turn checklist" name (CLAUDE.md says "Acceptance checklist") and `runtime-verification-reminder.sh`'s drifted mid-plan-stop list fixed. Every `eval.yml` bed-location claim (the file no longer exists — beds live in the `eval:` block of the project / `box/<distro>` `charly.yml`s) fixed in CLAUDE.md, README.md, both workflows' discover prompts, `/charly-eval:eval`, and `/charly-eval:eval-sway-browser-vnc`; CLAUDE.md's pointer to the eval skill's nonexistent "DO NOT fake success" heading repointed at its real headings. strict-policy's R3/R4 interaction notes no longer reference the deleted "three labeled sub-paragraphs" of the Clean Architecture section.
- **R10 gate by change class (operator-directed mid-cutover extension):** CLAUDE.md R10 now mandates per-class gates — docs/comments-only (non-runtime standards, NO bed run), `charly` Go code (matching bed(s); `--all-beds` for cross-cutting changes), candy/box/deploy config (the bed that composes the entity), hook/workflow scripts (execute the changed script live; one matching bed only when control flow changed) — with the authoritative matrix in `/charly-eval:eval` "R10 gate by change class", mirrored by the testing-validator agent, the disposable skill, and both reminder hooks. Running eval beds on a prose-only change is now explicitly classified as waste, not diligence.
- **History absorbed here (deleted from current-state docs):** the disposable skill's "Schema v4 note" — schema v4 made `DeploymentNode.Disposable` the sole source of truth (with `VmSpec.Disposable` retained only during that transition), and fixed the latent `MergeDeployConfigs` bug that dropped `Disposable`/`Lifecycle` during the project ↔ per-machine overlay merge. The Post-Execution "this SUPERSEDES the older 'push only if the user asked'" parenthetical — the auto-land-on-R10-PASS rule replaced that older behavior. Key Rules' box-inversion narrative ("the former main↔cachyos / main↔fedora mutual cycles are dissolved") and the "former separate `DefaultManifest` constant is deleted" fragment — both 2026-06 cutovers already recorded in this file.

### 2026-06-11 — fix(build): derive `task build:charly`'s package-displace list from the PKGBUILD (no more drift)

`task build:charly` failed on any host with the pre-rename `overthink-git` package installed: `opencharly-git and overthink-git are in conflict … unresolvable package conflicts`. The PKGBUILD correctly declares `conflicts=('overthink-git' 'ov-git')` + `replaces=('overthink-git' 'ov-git')`, but `pacman -U` (what `makepkg -i` runs for a local-file install) does NOT honor `replaces=`, so the Taskfile pre-removes the superseded packages first — and its hardcoded list (`ov-git ov-git-debug opencharly-git opencharly-git-debug`) had drifted from the PKGBUILD, omitting `overthink-git`/`overthink-git-debug` (its comment even mis-cited `replaces=('opencharly-git' 'ov-git')`). The classic two-sources-of-truth drift. Fixed by DERIVING the displace-list from the PKGBUILD's own `conflicts=`/`replaces=` arrays (`bash -c 'source ./PKGBUILD …'`) plus each entry's `-debug` sibling and the package's own `-debug` sibling — so the list can never drift from the PKGBUILD again. R10: `task build:charly` on this host (with `overthink-git` installed) removed it and installed `opencharly-git` system-wide, EXIT 0.

### 2026-06-11 — feat(generate)!: `charly box generate <name>` + `all` default, unified with build selection, and DETERMINISTIC auto-intermediates

`charly box generate` gained the box selection surface `charly box build` already had, and the underlying auto-intermediate computation was made fully deterministic so generation is reproducible and maximizes build-cache reuse.

- **`charly box generate [<box>…]`** — new optional positional. `generate <name>` scopes emission to that box + its transitive deps (Base + format builders + bootstrap builder); `generate all` and bare `generate` emit every enabled box (the sentinel `all` is case-insensitive and collapses to the no-arg form). Adds `--include-disabled` (scoped to the named boxes), mirroring `charly box build`.
- **Unified build/generate selection (R3).** Two shared helpers — `normalizeBoxArgs` (collapses the `all` sentinel to nil) and `boxResolveOpts` (empty → all enabled; named → `RequestedBoxes` scoping + per-name `--include-disabled` gate relaxation) — are the single source of the selection rule for BOTH verbs. `BuildCmd.Run` normalizes `all` at the top (before remote-ref dispatch / include-passthrough so `all` is never mistaken for a remote target) and `Generate()` scopes its emission loop through the SAME `filterBox` the build path uses. Net effect: `charly box build <name>` now also generates only `<name>` + deps instead of all-then-filter — fewer `.build/` writes, less cross-image churn.
- **Deterministic auto-intermediates.** `GlobalCandyOrder` and `ComputeIntermediates` iterated Go maps whose randomized order leaked into (a) authored-list cycle-skipping edge insertion, (b) sibling-group processing order → `pickAutoName`'s `-2`/`-3` collision suffix, and (c) `collectSubtreeBoxes` → the intermediate's format/distro union order. Result: bare `charly box generate` produced a *different* set of auto-intermediate images run-to-run (observed 46/49/48 boxes in box/fedora), and a box that intermittently vanished from the requestable set ("unknown box fedora"). Fixed by sorting every order-affecting map iteration (box names, candy names, a new `sortedSiblingKeys` by (base, uid), and `sortedKeys` in subtree collection). Now generation is byte-identical across runs AND a single-box generation emits byte-identical Containerfiles (intermediates included) to the full build — so a box built in isolation reuses the full build's cache (matching FROM SHAs). Verified: box/fedora 8× identical + single==all across 5 targets (0 mismatches); box/cachyos 4× identical + single==all across selkies-labwc/openclaw-desktop/versa.
- **Coverage.** `box_selection_test.go` (selection logic + Kong parse) and `intermediates_determinism_test.go` (50-trial determinism on a fixture that reproduces all three non-determinism sources — proven to FAIL with the fix reverted). R10: `charly eval run eval-pod` (disposable) PASS (steps=10), the build leg exercising scoped generate + deterministic intermediates on a fresh rebuild.

### 2026-06-11 — refactor(eval): push selkies/openclaw desktop+nesting checks down to their candies (campaign 2)

The all-in-one openclaw-desktop image (and the selkies-labwc box) re-declared box-level eval checks their composed candies provide. Each is removed; checks a candy genuinely owns move onto the candy so ONE check covers every composing box (R3):

- **MOVE→candy (multi-box reach):** `candy/container-nesting` gains the rootless-nesting posture (subuid two-ranges, newuidmap cap, policy.json, containers.conf userns=host system+`${HOME}`, the `_CONTAINERS_USERNS_CONFIGURED`/`BUILDAH_ISOLATION` env contracts) + `nested-podman-run` + `newgidmap-binary` — portable across all **9 boxes** composing it; `candy/openclaw` gains `openclaw-binary` + `gateway-port` (:18789, covering the 3 openclaw boxes); `candy/virtualization` gains `libvirt-session-list` (virsh `qemu:///session` — NOT an orphan: charly→virtualization composes libvirt); `candy/charly` gains `charly-doctor` (engine-agnostic — `charly doctor` prints its `Summary:` line before the required-deps exit, so it verifies the subcommand on ANY charly box incl. engine-less fixtures).
- **DELETE dups:** openclaw-desktop's 17 box checks (selkies/chrome/labwc/traefik/sshd binaries, ollama/gocryptfs/socat, the nested binaries, charly version/doctor, chrome-devtools-mcp/gateway/ollama ports — all candy-covered) and selkies-labwc's 2.
- **Bed-dedup:** the 4 selkies/openclaw pod beds strip their inline binary/file dups.
- **KEEP (rule 2):** `pixelflux-nvenc-compiled` stays on the two nvidia boxes (build-variant-specific — the CPU/stub build patches nvenc-sys out, so the symbol is absent → it would FAIL on the shared candy/selkies; RDD reverted an earlier move there).

Cross-repo (B6): the 4 producer candies land in main (`v2026.162.1442`); box/cachyos reconciles its `@github` pins and lands the box+bed dedup (`v2026.162.1548`); main bumps the pointer. **This completes the 7-cutover eval-normalization campaign** (C1 coder · C3 versa no-op · C4 android · C5 fedora-services · C6 pacstrap · C7 bed-dedup · C2 selkies/openclaw).

R10: `eval-openclaw-desktop-pod` PASS (167 passed · 0 failed, incl. the fresh-update leg); `eval-githubrunner-pod` PASS (10/10 — the container-nesting posture move verified on a uid-0 box); `eval-charly-selftest-pod` PASS (10/10 — charly-doctor + virtualization libvirt + container-nesting); `eval-selkies-labwc-pod`/`eval-openclaw-pod`/`eval-openclaw-full-pod` PASS (10/10 each). The 2 nvidia GPU-VM beds (`eval-selkies-{labwc,kde}-nvidia-vm`) fail environmentally (no GPU passthrough on this host — both `NVENC-N/A` + stream-https refused); the nvidia boxes are composition-invariant under C2 (candy/selkies reverted, nvenc-compiled unchanged), so those beds test no C2 change. No schema change.

### 2026-06-11 — docs(comments): drop migration phrasing from two eval comments

CLAUDE.md "history lives ONLY in CHANGELOG" — code comments describe the CURRENT state in present tense. Two comments authored during campaigns 6/7 carried migration phrasing: `candy/pacstrap-builder` ("instead of each box re-declaring its own copy") and `box/cachyos` `eval-cachyos-vm` ("a copy here re-tested the identical pacman path on a second bed"). Both reworded to present-tense rationale. Comment-only — no functional or schema change; `charly box validate` exit 0 in both repos. box/cachyos lands at `v2026.162.1414`; main bumps the pointer.

### 2026-06-11 — refactor(eval): push fedora service-box checks down to their candies (campaign 5)

The 6 fedora service boxes (filebrowser/hermes/immich-ml/jupyter/openwebui/sway-browser-vnc) each re-declared box-level eval checks their composed candies already own. Each duplicate is removed; checks a candy genuinely provides move onto the candy so ONE check covers every box composing it (R3):

- **DELETE dups:** `filebrowser-composed-binaries` (filebrowser/dbus candy binaries), `hermes-all-clis-present` (claude/codex/gemini + hermes candies), `hermes-service-up` (= candy/hermes:hermes-service-running), `immich-ml-stack-binaries` (4 candy binary checks) + `immich-ml-internal-ping` (= candy/immich-ml:immich-ml-http), `jupyter-notebook-templates-provisioned` (= candy/notebook-templates:notebook-templates-present, a 5-box candy) + `jupyter-mcp-extension-enabled` (candy/jupyter-mcp + candy/jupyter mcp probes), `openwebui-composition` + `openwebui-required-env-injected` (candy/openwebui entrypoint/service/admin-email checks).
- **MOVE→candy:** `filebrowser-volumes-exist` → candy/filebrowser (which declares the `data` volume); the full 7-check browser-VNC desktop verification → candy/sway-desktop-vnc (the composite that assembles sway+wayvnc+chrome, mirroring immich-ml-services-running's composition check).
- **KEEP-on-box (rule 2):** `immich-ml-services-running` (the 4-candy "postgres+redis+immich-server+immich-ml all RUNNING together" composition no single candy owns).
- **Bed:** `eval-sway-browser-vnc-pod` drops its redundant raw-http cdp probe (the moved candy `cdp: status` covers "cdp up").

**Blocking fix surfaced by the R10 (R1/R2):** the fresh hermes rebuild re-pulled the unpinned ahmetb/kubectx `master` script, exposing that candy/devops-tools' `kubectx-help`/`kubens-help` checks run `kubectx --help`, which aborts ("kubectl is not installed", exit 1) BEFORE the help branch when kubectl is absent — failing on any kubectl-less devops-tools consumer (hermes composes devops-tools but not the kubernetes candy). Dropped the two overreaching checks (the `kubectx-binary`/`kubens-binary` file-exists checks already prove installation; functional kubectx needs kubectl from the separate kubernetes candy).

Cross-repo (B6): candy/filebrowser + candy/sway-desktop-vnc + candy/devops-tools land in main (tags `v2026.162.1314`, `v2026.162.1355`); box/fedora reconciles all `@github` pins to `v2026.162.1355` and lands the box dedup (`v2026.162.1407`); main bumps the pointer.

R10: `eval-sway-browser-vnc-pod` PASS (99 passed · 0 failed; the moved sway-desktop-vnc-* checks all green incl a 1920×1080 framebuffer; fresh-update leg green); `eval-jupyter-pod` PASS (10/10); `charly eval box` filebrowser 14/0, hermes 49/0 (post-fix), openwebui 15/0. immich-ml composes none of the 3 changed candies (composition-invariant — the removed checks are verbatim dups of present, unchanged candy checks). No schema change.

### 2026-06-11 — refactor(eval): dedup the pac localpkg witness onto one bed (campaign 7)

`eval-cachyos-vm` (box/cachyos) carried a `localpkg-pac-autoresolve` inline check functionally identical to main's `eval-charly-vm` — both asserting that `pacman -U` of the charly candy's localpkg `opencharly-git` auto-resolves its mandatory repo deps (podman/libvirt/tailscale/gocryptfs) on a bare Arch-family VM. main's `eval-charly-vm` is the PURPOSE-BUILT pac witness (a plain Arch cloud VM carrying no other layers, so the localpkg path is its sole reason to exist), so the cachyos copy was a bed-to-bed structural dup. Removed it; `eval-cachyos-vm` keeps its genuine value as the cachyos pacstrap bootstrap-VM R10 canary — and the deploy-add step still FAILS the bed if the localpkg install or its dep auto-resolve fails (`pacman -U` errors on an unsatisfiable Depends), so cachyos-substrate install coverage is retained implicitly, matching the existing `eval-arch-pacstrap-vm` bootstrap-canary pattern (a VM bed whose R10 value is the lifecycle, not an inline check).

Eval-normalization campaign, cutover 7 (bed structural dedup). The investigation confirmed NO other bed-to-bed STRUCTURAL dups: the deb (debian/ubuntu) and rpm (fedora) localpkg witnesses are each the SOLE owner of their distro's coverage (independently-maintained archives — distro IS the thing under test, KEEP), and the `eval-jupyter-ml-pod` (box/fedora) ↔ `eval-cachyos-jupyter-ml-pod` (box/cachyos) pair is the same box on different distro bases (portability proof) with already-disambiguating names. The per-CLUSTER bed-inline dups (versa / fedora-services / selkies) are scoped to their own pending cutovers, not this one. Submodule-only edit (box/cachyos → v2026.162.1242); main bumps the pointer.

R10: `charly eval run eval-cachyos-vm` PASS (6/6 steps; vm-build 246s [pacstrap rootfs], deploy-add 145s [charly localpkg on the guest], update 27s); `charly eval run eval-charly-vm` PASS (6/6; the witness's `localpkg-pac-autoresolve` 1 passed · 0 failed, proving the removed assertion is still covered). No schema change (content-only).

### 2026-06-11 — refactor(pacstrap): move the pacstrap toolchain eval onto candy/pacstrap-builder (covers both builder boxes)

The `cachyos-pacstrap-builder` box re-declared four box-level checks — `cpb-pacstrap-binary` (`/usr/sbin/pacstrap`), `cpb-arch-install-scripts` (package), `cpb-grub-install` (`/usr/sbin/grub-install`), `cpb-parted` (`/usr/sbin/parted`) — that test the `pacstrap-builder` candy's OWN install surface (arch-install-scripts, grub, parted). The sibling `arch-pacstrap-builder` box composed the SAME candy but carried NO eval at all, so the toolchain went unverified there. Moving the four checks ONTO `candy/pacstrap-builder` (which had no eval) makes ONE candy check cover EVERY box composing it (R3): the cachyos box drops its `cpb-*` copies and arch-pacstrap-builder GAINS the coverage for free. Build-scope (pacstrap ships inside arch-install-scripts; grub/parted ship the named binaries).

Eval-normalization campaign, cutover 6 of 7 — the gap-fill MOVE. Cross-repo (B6): the producer candy lands in **main** (`candy/pacstrap-builder` version `2026.144.1443`→`2026.162.1200`, +eval block; tag `v2026.162.1207`); the two **box/{cachyos,arch}** consumers reconcile their `@github…/pacstrap-builder` pin to `v2026.162.1207` (cachyos drops `cpb-*`, arch gains the candy eval; both tag `v2026.162.1210`); main bumps the two submodule pointers. No `package_map:` needed — both boxes are Arch-family (`distro: arch`), so the candy's single `distro.arch.package` section already names them.

R10: `charly eval box cachyos-pacstrap-builder` 4 passed · 0 failed AND `charly eval box arch-pacstrap-builder` 4 passed · 0 failed (the candy checks that replaced/added the box coverage all green on both consumers, fetched from the landed candy tag). No schema change (content-only; per-candy `version:` is the candy's own CalVer, not the YAML schema version).

### 2026-06-11 — refactor(coder): drop redundant coder-box eval rollups (covered by their candies)

The four `*-coder` boxes (arch/debian/fedora/ubuntu) each re-declared box-level eval checks — `*-ai-clis`, `*-languages`, `*-devops`, `*-sshd-port`, `*-mcp-port` (+ arch-coder's `*-charly-version`) — that the composed candies ALREADY provide at the IDENTICAL path: claude-code/codex/gemini/forgecode (`${HOME}/.npm-global/bin/{claude,codex,gemini,forge}`), golang/nodejs/rust (`/usr/bin/{go,node,cargo}`), kubernetes/docker-ce/devops-tools/google-cloud (`{kubectl,docker,aws,gcloud,tofu}`), sshd (:2222), charly-mcp (:18765), charly (`charly version`). One candy check covers every box composing the candy (R3); the box copies were pure duplication. Replaced with a documenting comment; net eval coverage preserved (every deleted box check's binary/port is candy-covered at the same path).

Eval-normalization campaign, cutover 1 of 7 — the first pure-DELETE cutover, proving the rubric. Submodule-only (box/{arch,debian,fedora,ubuntu} drop their `<distro>-coder` box eval); main bumps the four pointers.

R10: `charly eval run eval-fedora-coder-pod` PASS (151 passed · 0 failed — the candy checks that replaced the deleted rollups all green); arch/debian/ubuntu-coder `charly eval box` 141/140/139 passed · 0 failed. No schema change.

### 2026-06-11 — feat(localpkg): disposable eval beds bake the IN-DEVELOPMENT charly; production boxes the published release

A `localpkg:` candy (the `charly` toolchain) installs the charly binary as a proper OS package. An IMAGE build previously ALWAYS downloaded the PUBLISHED release (`releases/latest/download/opencharly-<arch>.<fmt>`) — so a disposable `kind: eval` bed tested a STALE released binary instead of the code under development. After the candy/box rename advanced main's eval recipes to `kind: box`, the published release (built before the recipe-from-kind update) could no longer parse them, and the `charly-mcp` `box.list.boxes` deploy-check failed in every coder bed (the MCP server's embedded-fallback load of main rejected `kind: box` with the stale `{layer, image, pod, vm}` allowlist).

The fix makes the binary source a hard, GENERIC distinction by box type, NEVER mixed, decided in ONE place:

- **Disposable eval boxes** (`kind: eval` beds) bake the latest **in-development** charly: the eval-bed runner passes `charly box build --dev-local-pkg` for every bed image build, so the localpkg is BUILT from the local working tree (`pkg/<fmt>` + `charly/`, via `buildLocalPkgOnHost`) and COPY'd into the image — a bed always tests the code under development.
- **Production boxes** bake the latest **published** charly: a normal `charly box build` downloads the release.

- **charly/localpkg.go**: `renderLocalPkgImageRun(lp)` → unified `renderLocalPkgImageInstall(s, devLocalPkg, imageDir, boxName)` — the SINGLE image-build install, shared by `OCITarget` AND `generate.go writeCandySteps` (R3). Production renders the `download_template` curl; dev builds the local source, stages it under `.build/<box>/_localpkg/`, and COPYs it — both install via the SAME dep-resolving `install_template`. A dev build with no local source HARD-errors (R4: no silent release fallback).
- **charly/build.go**: `--dev-local-pkg` flag → `Generator.DevLocalPkg`. **charly/eval_bed_run.go**: the bed runner sets it automatically for every pod-bed image build (VM/local beds already build current charly via the deploy `build_template` path).
- Skills: `/charly-tools:charly`, `/charly-internals:install-plan`, `/charly-eval:eval`, `/charly-build:build` document the distinction.

R10: `charly eval run eval-fedora-coder-pod` PASS (10/10 steps; eval-live 151 passed · 0 failed; baked charly 2026.162.1002, `box.list.boxes` exit 0) — the bed now bakes the in-development toolchain. Covered by `TestRenderLocalPkgImageInstall_ProductionDownloadsRelease` + `…_DevMissingSourceHardErrors`. No schema change.

### 2026-06-11 — fix(reconcile): skip git-submodule dirs in the project-file scan (shared `isGitSubmoduleDir`) + align main namespace pins

`charly box reconcile`'s `reconcileCandidateFiles` walked `box/` + `candy/` with `filepath.Walk` and, after the box inversion made `main/box/` the submodule mount parent, recursed into `box/<distro>/{box,candy}/<name>/charly.yml` — rewriting the SUBMODULES' `@github` pins and leaving every submodule `-dirty` on a main `charly box reconcile`. Same bug class as the `drop-box-port` migrator-scoping fix in the candy-port cutover below: each charly-project repo reconciles/migrates ITSELF.

- **charly/reconcile.go**: `reconcileCandidateFiles` now `filepath.SkipDir`s any walked directory carrying a `.git` entry, so the scan stays inside the current repo.
- **Shared `isGitSubmoduleDir(p, root)` (R3)**: the new predicate backs BOTH the reconcile scan and the `charly migrate` `dropBoxPortCandidateFiles` walk; the latter's inline `.git`-stat block is refactored onto it (one abstraction, two call-sites).
- **charly/reconcile_test.go**: `TestImageReconcile_SkipsSubmodules` (a submodule under `box/` with a stale pin stays UNTOUCHED) joins the existing `TestMigrateDropBoxPortSkipsSubmodules`; both now exercise the shared helper.
- **charly.yml**: main's `arch` / `cachyos` / `fedora` namespace `@github` import pins aligned to the freshly-landed submodule tags (`v2026.162.0906` / `v2026.162.0912` / `v2026.162.0855`) — the main-namespace reconcile result, now a clean no-op.

Verification: `go test ./...` green (the two scoping guards + the four other reconcile tests pass); live `charly box reconcile --remote` on the final tree → "already reconciled" with every submodule staying clean; `charly box validate` exit 0; `charly eval run eval-local` PASS (4/4) on the fresh rebuild; all six charly-project repos report "already reconciled" (zero version-mismatch warnings). No schema change (`version:` unchanged; content-only edits).

### 2026-06-11 — feat!: candy-port inheritance + auto port mapping (boxes drop `port:`, deploys auto-allocate on 127.0.0.1) + unified candy-chain collectors

Ports now live on ONE surface — the candy that runs the service — and travel automatically to every box that composes it and every deploy that publishes it. This kills the band-aid where a box (and its eval bed) manually re-declared a candy's port. The canonical case: the `android-emulator` box composed `selkies-labwc`, whose chain includes `chrome-cdp` (9222) + `chrome-devtools-mcp` (9224); the inherited mcp eval probes resolve `${HOST_PORT:9222/9224}`, but the published ports did NOT inherit, so the box re-declared `9222:9222`/`9224:9224` (with a comment admitting it compensated for broken inheritance) and the bed's explicit port list then REPLACED the box label and silently dropped them. Three distinct duplication/replace bugs, one fix.

**Schema (hard cutover, `version:` → 2026.161.2303, migration step `drop-box-port` @ 2026.161.2302):**
- The box-level `port:` field is **RETIRED** (`BoxConfig.Port` parsed only so `rejectLegacyBoxPort` hard-errors a residual one with a `charly migrate` hint; `defaults.port` likewise). Boxes declare NO ports.
- The `port: [auto]` deploy sentinel is retired (absence of pins IS auto now). `ExpandAutoPorts`/`HasAutoPort` deleted.
- `charly migrate` step `drop-box-port` strips box-level `port:` + `defaults.port` + the `port: [auto]` sentinel (node-API, comment-preserving, idempotent); explicit deploy port PINS (host:container) are preserved.

**Behaviour:**
- `CollectBoxPorts` (charly/ports.go) collects the published container ports from a box's FULL base chain via the new shared `boxCandyChain` walk — the single source feeding both the `ai.opencharly.port` OCI label (`generate.go`) and `EXPOSE` (`writeExpose`), so they can never diverge. `charly box inspect <box> --format ports` shows the inherited set (e.g. android-emulator → `2222 3000 4723 5037 9222 9224` with no box `port:`).
- `ResolveDeployPorts` (auto port mapping default): at `charly config`, every image-declared container port without an explicit deploy pin gets a freshly-allocated FREE host port; a prior allocation is reused for stability across `charly update`; the result persists as `ResolvedPort`. `localizePort` + `BindAddress` (default `127.0.0.1`) bind every published port — auto-allocated and pinned alike — to loopback only. A deploy `port:` entry is now a PIN INPUT to the resolution (pin some, auto-allocate the rest), NOT a wholesale replacement (`MergeDeployOntoMetadata`).

**Unified candy-chain collection:** the duplicated `walkBaseChain` + `ResolveCandyOrder` + per-field `seen`-dedup boilerplate copy-pasted across eight collectors is replaced by TWO shared walks — `boxCandyChain` (base-chain inheritance: `CollectEval`/`CollectHooks`/`CollectShell`/`CollectDescriptions`/`CollectBoxVolume`/`CollectBoxPorts`) and `boxDirectCandies` (leaf-direct, no base traversal: `CollectSecurity`/`CollectBoxAlias`/`CollectLibvirtSnippets`, the latter gaining correct transitive resolution). Each collector keeps its field-specific semantics; zero behaviour change for the existing seven.

**Ports moved into candies:** `candy/eval-stack-layer` (which runs `nc -lk 18794`) now declares `port: [18794]`; the `eval-pod` box drops its `18794:18794`. Every box across the main repo + the five distro submodules drops its `port:` block (e.g. the four selkies boxes' `3000/9222/9224/2222`, versa's eight, immich-ml's `2283`); the `eval-android-emulator-pod` bed drops its 35xxx pins entirely (full auto, conflict-free with sibling beds).

### 2026-06-10 — fix(scaffold): repair `charly box new project`/`new candy` to emit loadable current-schema configs

`charly box new candy` wrote a stub with a top-level `rpm:` key the current loader REJECTS (packages moved under the `distro:` map in the localpkg-map / single-canonical-surface migration) and missing the mandatory `version:` — so a freshly-scaffolded candy broke the WHOLE project's load (`candy has unknown top-level key(s) [rpm]`). `charly box new project` wrote a stale plural `platforms:` key (the field-singular migration made it `platform:`), warning on every load. And the `box new project` "Next steps" guidance + the `/charly-build:new` skill pointed at a `build.yml` to copy (it's embedded in the binary now), a `--candy`/`--layers` flag (it's `--candies`), and an `images:`/`layers:` output shape (a box is a discovered `box/<name>/charly.yml` with `box: {candy: […]}`).

- **scaffold.go**: candy stub → `candy: {name, version: <ComputeCalVer>}` + an `add-rpm` hint comment. Loads cleanly; `charly candy add-rpm`/`add-deb`/`add-pac`/`add-aur` build the `distro.<x>.package` section on demand.
- **scaffold_project.go**: `platforms:` → `platform:`.
- **scaffold_cmds.go**: the project "Next steps" output → embedded-vocabulary note (no `build.yml` to copy), candy-before-box ordering, `--candies`, and a consistent `my-box`.
- **validate.go**: the no-install-source error message `candy manifest rpm/deb packages` → `candy manifest distro: packages`.
- **/charly-build:new skill**: `--layers`→`--candies`, the box-output example (`box: {name, base, candy}`), and the scaffold-stub description synced to the current shape.

Verified: `go test ./...` green; the tool's OWN "Next steps" guidance runs end-to-end (`box new project` → `new candy` → `add-rpm` → `new box --candies` → `validate` exit 0). Bug fix — no schema change.

Known-separate (NOT fixed here — its own future cutover): a legacy `images:`-map per-host `deploy.yml` (pre-2026.128 format) isn't fully migrated by `charly migrate` — the `local-deploy` step (128) reads `HostDeployPath`, which Cutover E retargeted to `charly.yml`, but the legacy file isn't renamed `deploy.yml`→`charly.yml` until step 161, so the `images:`→`deploy:` host conversion never fires on it. Near-unreachable; its fix is a high-risk migration-chain change.

### 2026-06-10 — refactor(strings): finish syncing user-facing strings + stale comment file-refs with current names

Following the Go-comment sync, this closes the remaining stale SELF-REFERENCES the comment scope didn't cover — the behavior-affecting STRING surface plus a few stale file-name comments from OTHER renames.

- **User-facing STRINGS** (error messages, Kong `help:` tags, `charly status` display) saying "layer"/"image" for the candy/box concept → "candy"/"box", with their **test assertions updated in lockstep**. E.g. `"unknown layer %q"`→`"unknown candy %q"`, help `"Extra layer to apply…"`→`"Extra candy…"`, `"local (%d layers)"`→`"local (%d candies)"`, `"not declared by any layer"`→`"…candy"`, the `--add-layer` error strings → `--add-candy`.
- **Comment file-references from OTHER renames**: `deploy_target_container.go`/`deploy_target_host.go` → `deploy_target_pod.go`/`deploy_target_local.go`; `harness_recipe_from.go`/`testrun_ov_verbs.go` → `eval_recipe_from.go`/`evalrun_charly_verbs.go`; the `DeployTests` field → `DeployEval`.
- **KEPT — still accurate**: OCI image-layer / build-stage terms; OCI "image" (registry/pull/tag/SHA/`<image>` CLI placeholder); the live `${layer_name}` substitution token; the on-disk `~/.config/opencharly/installed/layers/` ledger dir; `json:"layer"` legacy keys; the `# Layer: <name>` Containerfile build-section headers (each candy's instructions = a build layer — defensible); and every legacy form named by migration code.

Verification: `go build`/`go vet`/`go test ./...` green (every changed string's assertion updated); live `charly … --help` emits the new strings; `charly eval run eval-local` PASS (4/4) on the fresh rebuild, exercising the changed local-deploy path. No schema change (`version:` stays `2026.161.1650`; main repo only).

Separately surfaced (NOT in this cutover — each its own future change): `charly box new candy` scaffolds a stub with a top-level `rpm:` key the current loader rejects; and the deeper `depends:`→`require:` / `tests:`→`eval:` renames + the `migrate_local_deploy` `image:`→`box:` wire emission remain.

### 2026-06-10 — docs(comments): sync Go code comments with the post-rebrand codebase (layer→candy, image→box)

The candy/box rebrand renamed the Go identifiers wholesale (the `Layer` struct → `Candy`,
`LabelEvalSet`/`LabelShellSet`/`LabelDescriptionSet` section trios `{Layer,Image,Deploy}` →
`{Candy,Box,Deploy}`, `InstallPlan.Layer` → `.Candy`, `LayerName`/`LayerDir` → `CandyName`/
`CandyDir`, `AddLayers` → `AddCandy`, `g.Layers` → `g.Candies`, `cfg.Image`/`uf.Image` →
`cfg.Box`/`uf.Candy`, …) but left **1551 code comments across 182 files** still describing
the old "layer"/"image" concepts and naming renamed-away identifiers. This sweep brings every
Go comment in `charly/` in sync with the current code.

- **layer** (the candy concept — a unit under `candy/<name>/`) → **candy**; **image** (the
  box-KIND / `charly.yml` `box:` composition concept) → **box**.
- Stale identifier spellings in comments → current (the section trios `layer/image/deploy` →
  `candy/box/deploy`, `add_layer(s)` → `add_candy`, `cfg.Image` → `cfg.Box`, the `env_layers`
  label note → `env_candy`, …).
- Stale paths/manifest filenames where they name the candy concept (`layers/<name>/` →
  `candy/<name>/`; per-entity manifest `candy.yml`/`box.yml`/`layer.yml`/`image.yml` →
  `charly.yml`).
- **KEPT — still accurate to the code**: OCI image layers, Containerfile build STAGES
  (`LayerStage`), `v1.Layer`, `mergeLayers`/squash (`merge.go` untouched); OCI container
  "image" (registry / pull / tag / SHA / `ResolvedImage`); the live `${layer_name}`
  substitution token; the on-disk `~/.config/opencharly/installed/layers/` ledger dir; the
  `json:"layer"` legacy keys named by migration code; and every comment naming an identifier
  still spelled `Layer*` in the code.

Comment-only (1551 insertions / 1551 deletions — symmetric in-place swaps; code byte-identical).
`go build` / `go vet` / `go test ./...` all green; `merge.go` untouched; no gofmt drift. No
schema change (`version:` stays `2026.161.1650`; no migration step, no submodule cascade).
User-facing STRINGS (error messages, Kong `help:` text, `charly status` display) that still
say "layer"/"image" are a separate, behavior-affecting concern — NOT part of this comment-only
sweep.

### 2026-06-10 — refactor!: rename the install-ledger json tags `layer`/`add_layer` → `candy`/`add_candy` with a ledger version gate (schema cutover)

The candy/box rebrand's last `layer`-spelled WIRE: the 2 internal install-ledger json
keys on `DeployRecord` / `CandyRecord` (`~/.config/opencharly/installed/{deploys,layers}/
*.json`). Cutover E deferred these because the ledger is un-versioned persisted deploy
STATE the unified loader never sees — renaming the keys would silently misread existing
ledgers (a legacy `"layer"` key unmarshals to an empty `Candy`, breaking refcount +
reversal). This adds the missing version gate so the rename is safe.

- **Tags renamed**: `DeployRecord.Candy` / `CandyRecord.Candy` `json:"layer"`→`json:"candy"`,
  `DeployRecord.AddCandy` `json:"add_layer"`→`json:"add_candy"`.
- **Ledger version gate**: a new `SchemaVersion` field (`json:"schema_version"`) on both
  records, stamped on every write (`ledgerSchemaVersion` — a FIXED CalVer INDEPENDENT of
  the project HEAD, so a non-ledger schema cutover never invalidates the ledger gate).
  `ReadDeployRecord` / `ReadCandyRecord` hard-reject a record without it (`run charly
  migrate`) — the same fail-loud-not-silent discipline as the project load gate.
- **Migration**: new `ledger-candy-keys` MigrationStep (2026.161.1649, TouchesHost) walks
  `ctx.LedgerRoot/{deploys,layers}/*.json` (`MigrateContext` gained `LedgerRoot` via
  `DefaultLedgerPaths`), raw-JSON-rewrites the keys + stamps `schema_version`. Idempotent.
  HEAD bumped 2026.161.1555→2026.161.1650 (6-repo re-stamp cascade).

`go test ./...` green; new `TestMigrateLedgerCandyKeys` + `TestReadCandyRecord_GatesPreCutover`.
RDD proved the real `~/.config/opencharly/installed/` ledger migrated in place (records
now carry `candy` + `schema_version`; `charly status` reads clean). R10 `charly eval run
eval-local` PASS (4/4, ok:true) — `LocalDeployTarget` deploy-add/eval-live/update/cleanup
write+read the ledger through the renamed tags + the gate.

This completes the candy/box rebrand: every `layer`-spelled wire surface — kind keys,
nested fields, init vocabulary, the per-host config, and now the install ledger — is `candy`.

### 2026-06-10 — refactor!: unify the per-host `deploy.yml` onto `charly.yml` + collapse the bespoke deploy-config machinery (schema cutover)

The last config that did NOT load through the one unified loader: the per-host deploy
overlay `~/.config/charly/deploy.yml`, which used a bespoke `LoadDeployConfig` parser.
It now loads through **`LoadUnified(~/.config/charly)` + `ProjectDeployConfig()`** — the
SAME `charly.yml`/`LoadUnified` path as every project config — and the file is renamed
`deploy.yml` → `charly.yml`.

**Zero structural gap, zero mode-purity risk.** `DeployConfig` is just `{Provides,
Deploy}` — both fields already exist identically on `UnifiedFile`, and
`UnifiedFile.ProjectDeployConfig()` already bridged them. Mode purity (build-mode never
bakes the per-host overlay into images) is preserved by **path**, not filename:
build-mode `LoadConfig`→`LoadUnified` always reads the PROJECT cwd, never
`~/.config/charly` — the two `charly.yml` files live in disjoint directories.

**Cleanup the unification enabled** (verified against the live code, not the explore
agents' over-claims): `LoadDeployConfig` collapses to a thin wrapper (the bespoke
`yaml.Unmarshal` + the live `hasLegacyImagesKey` check are gone — the unified loader's
version gate + `RejectLegacyPluralKeys` + `rejectLegacy*` subsume them; the function
stays for migration replay). The **dead** `MergeDeployOverlay` (0 call sites) is deleted.
The ephemeral→disposable auto-promotion + ephemeral/vm-naming validators — previously
LoadDeployConfig-only — are consolidated into `validateEphemeralUnified` in the unified
deploy-validation path (R3), so a PROJECT `charly.yml`'s inline `deploy:` entries get
them too (closing a latent asymmetry). `SaveDeployConfig` now writes a unified-shaped
file with the HEAD `version:` stamp.

**Verified load-bearing, KEPT:** the `DeployConfig` type (the merge/save unit, 51 uses),
`loadDeployConfigForRead`/`loadDeployConfigForWrite`/`LoadDeployFile`, the live mergers,
and the hard-reject migration-enforcement gates (cutover-policy REQUIRES them — not
removable shims).

**Migration + safety.** New `host-charly-yml` MigrationStep (2026.161.1554, TouchesHost):
renames `deploy.yml` → `charly.yml` AND **prepends a `version:` line** (per-host configs
predate per-file versioning, so they have none — and `stampVersionField`/calver-schema
only rewrites an existing line, never adds one; without this the renamed file failed the
loader gate with `found ""`). A load-time host-file guard hard-errors `Run: charly
migrate` if a stale `deploy.yml` is still present. HEAD bumped
2026.161.1502→2026.161.1555 → the standard 6-repo re-stamp cascade.

`go test ./...` green (the per-host fixtures gained `version:` stamps — the unified gate,
which the bespoke parser never enforced); new `TestMigrateHostCharlyYml`. R10
`charly -C box/fedora eval run eval-pod` PASS — deploy-add/config/start/update read+write
the per-host overlay through the new unified load/save. RDD proved the round-trip + the
real `~/.config/charly/charly.yml` migration in place.

### 2026-06-10 — refactor!: rename the init-system vocabulary keys `layer_field`/`layer_file`/`depends_layer` → `candy_field`/`candy_file`/`depends_candy` (schema cutover)

The candy/box rebrand's last `layer`-spelled user-facing WIRE: the three init-system
definition keys (the `init:` section in the embedded `build.yml`) that survived the
kind-discriminator rename. `layer_field:`→`candy_field:` (which candy field holds
services), `layer_file:`→`candy_file:` (which candy file globs to match, e.g.
`*.service`), `depends_layer:`→`depends_candy:` (which candy must precede the init
system). The Go `InitDef` (`init_config.go`) struct tags + the embedded
`charly/build.yml` init vocabulary now read `candy_*`.

Schema cutover: new `init-candy-keys` MigrationStep (2026.161.1501 — scoped to the
`init:` subtree, comment-preserving, idempotent, TouchesHost false), HEAD bumped
2026.161.1301→2026.161.1502 (calver-schema stays last). The load gate then rejects any
config below HEAD with a `Run: charly migrate` hint; the remote-cache auto-migration
rewrites a fetched repo's `init:` overrides — so a consumer pinning an old producer's
`build.yml` loads clean (verified: box/fedora importing main@v2026.160.0856 raised a
`field layer_field not found in InitDef` warning until a cache refresh re-ran the new
chain, then loaded warning-free).

**6-repo version cascade** — every charly-project's root `version:` re-stamped to
2026.161.1502 + a fresh tag: main + the arch/cachyos/debian/fedora/ubuntu box
submodules. The box submodules carry no `init:` keys, so their migration is the
calver-schema re-stamp only.

**Deliberately LEFT** (RDD finding): the 2 internal ledger json tags
(`DeployRecord`/`CandyRecord` `json:"layer"` + `json:"add_layer"`). They are
un-versioned persisted deploy state (`deploys/*.json`) the migrate framework does NOT
reach, and there is no ledger read-gate — renaming them would silently break existing
deploys' refcount/reversal without a new ledger-versioning mechanism. Disproportionate
for an invisible on-disk key; the user-facing candy/box authoring surface is fully
`candy`.

`go test ./...` green; new `TestMigrateInitCandyKeys` (rename + init-scoping +
idempotency); R10 `charly -C box/fedora eval run eval-pod` PASS (10/10 steps, ok:true,
**zero warnings**) — `config`/`start` render supervisord units through the renamed
embedded init vocabulary on a `disposable: true` bed, fresh-rebuild included.

### 2026-06-10 — refactor(charly)!: rename candy-meaning Go identifiers `Layer*`→`Candy*` (keep OCI `v1.Layer` / build-stage)

The symmetric completion of the candy/box rebrand's Go axis — the `Layer*`→`Candy*`
rename the prior prose-sweep entry flagged as "the one un-done structural axis" (it
mirrors the earlier `Image*`→`Box*` Phase-2). The candy/box rebrand had already
renamed the SCHEMA/wire `layer:`→`candy:` and `CandyRef`, but the central Go `Layer`
struct and ~300 derived identifiers still carried the old name; this reconciles them
so the Go surface matches the wire vocabulary.

**Disambiguation rule (mirrors image=OCI / box=candybox):** rename the
charly-**candy**-meaning identifiers (the composable layer/candy); KEEP the
**OCI-layer**-meaning ones (a tar layer in the image filesystem stack). Because
`v1.Layer` (go-containerregistry) appears ONLY in `merge.go` / `merge_test.go`, every
compound `*Layer*` token elsewhere is candy-meaning by construction — the only
candy-file KEEP is the multi-stage build-stage (`LayerStage` / `layerStage` /
`COPY --from`).

**Method (compiler + tests as the net):**
- **gopls** (AST-aware, never touches `v1.Layer` / `v1.Image.Layers()` / strings) for
  the homonym-dangerous symbols: the `Layer` struct → `Candy`, and the 24 bare
  `Layer`/`Layers` candy FIELDS across `BoxConfig`, `ResolvedBox`, `CapabilityService`,
  `LabelEvalSet`, `InstallPlan`, the Kong command structs, etc. → `Candy`/`Candies`
  (most already carried `yaml:"candy"`/`json:"candy"` tags, so the rename RESTORES
  Go↔wire symmetry).
- **word-boundary `sed`** for 294 unique compound candy identifiers
  (`ScanAllLayerWithConfig`→`ScanAllCandyWithConfig`, `LayerName`→`CandyName`,
  `pickLayerVersion`→`pickCandyVersion`, `toLayerRefs`→`toCandyRefs`,
  `IncludedLayer`→`IncludedCandy`, `overlayLayers`→`overlayCandies`,
  `vLayers`→`vCandies`, …). Snake_case wire keys, migration match-literals, prose
  words (`Layered`/`layerless`/`metalayer`), and false string-matches (`nlayers` =
  `\n`+`layers`, the `.build/_layers/` dir) were excluded — a Go-identifier rename, not
  a text replace.

**KEPT (OCI / external / build-artifact — verified intact):** `v1.Layer` (×3),
`v1.Image.Layers()`, all `merge.go` tar-layer ops (`mergeLayers`, `EmptyLayer`,
`makeTarLayer`, `LayerFromOpener`, `MergeStep.Layers []int`), the multi-stage
`LayerStage`/`layerStage`/`COPY --from` build-stage surface, the emitted Containerfile
`# Layer:` comment, the Wayland `layer-shell` protocol name. **No wire change, no
MigrationStep, no `version:` bump** — the on-disk format is byte-identical: the rename
left every struct TAG untouched (`json:"candy"`, but also the not-yet-rebranded
`yaml:"layer_field"` / `yaml:"depends_layer"` / `json:"layer"` / `json:"add_layer"`),
so old configs and pushed images load unchanged.

**Template-ref fix (R8).** gopls renamed `ServiceRenderContext.Layer`→`Candy`, but
`text/template` field refs (`{{.Layer}}`) live in STRING literals gopls can't see — the
compiler is silent and only the live service-render test caught it. Swept
`{{.Layer}}`→`{{.Candy}}` in the embedded `build.yml` service-unit template + the test
fixtures (`{{.LayerStage}}` correctly preserved).

**Docs (R5).** 17 skill/root-doc files re-swept for the renamed identifiers (CLAUDE.md
`pickCandyVersion`/`ensureCandySecret`; the go/install-plan/generate-source/image/layer/
capabilities skills' `Candy` struct + `Candy.Require`/`Candy.IncludedCandy` + IR-step
`CandyName`/`CandyDir` references; the "Candy-version resolution" heading). The
`/charly-image:layer` skill slug, `layer-validator` agent name, wire `candy:` examples,
and "Remote-layer resolver" cross-ref anchor are all kept.

`go test ./...` green; R5 grep self-test clean (no renamed compound identifier survives
outside this file). Acceptance via R10 `charly -C box/fedora eval run eval-pod` (the
combined kind:box build + kind:candy composition + kind:pod supervisord runtime +
DeployTarget rendering bed — exactly the surfaces the rename touches).

Two deliberately-separated follow-ons (each its own cutover, NOT folded in): (1) the
remaining `layer`-spelled WIRE fields (`depends_layer:`, `layer_field:`, `layer_file:`,
the composition `layer:`, ledger `json:"layer"`) → `candy` is a USER-FACING schema
change (MigrationStep + `version:` bump); (2) conceptual "layer" prose in deep Go code
comments, left where it is OCI-legitimate or ambiguous.

### 2026-06-10 — docs: comprehensive PROSE sweep image→box / layer→candy across the whole skill corpus

The final consistency axis: where the prior docs-sync made the skills *factually*
correct (code-refs, commands, labels, fields), this sweep normalizes the remaining
PROSE terminology to the candy/box vocabulary. Run via 4 parallel audit agents over
disjoint areas (distros / infra+tools+lang+local+automation / pod-apps / core-eng +
root docs), each cross-checking against the code with a strict zero-false-positive,
keep-all-OCI rule.

**~226 files, ~1900 line-for-line swaps.** Renamed (PROSE only, where it clearly means
a charly candybox / candy): "the `<name>` image"→"the `<name>` box" for every charly
box (nvidia, jupyter, fedora-coder, …); the box-meaning "layer"→"candy" ("the redis
layer"→"the redis candy", "sibling layer"→"sibling candy", "candy authoring"); and the
structural headers (`## Image Properties`→`## Box Properties`, `## Full Layer Stack`→
`## Full Candy Stack`, `## Key Layers`→`## Key Candies`, `## Used In Images`→`## Used In
Boxes`, `## Related Images/Layers`→`## Related Boxes/Candies`, the `# Box:`/`# Candy:`
titles + `| Box |`/`| Candy |` table labels).

**KEPT (verified, zero false positives)** — every genuine OCI/Docker reference: `base
image`, registry refs (ghcr.io/quay.io/docker.io), "the built image", "image tag/ref",
`charly box pull` (the *command*) fetching "an image", bootc/disk/container images,
merge/build/multi-stage *layers*, `COPY --from=<stage>`, `metalayer`/`meta-layer`
(coined term); every Go identifier in backticks (`Layer`, `LayerEvalSet`,
`pickLayerVersion`, `writeLayerSteps`, `image.Image`, `ImageRef`); the `<image>` CLI
placeholder; skill slugs (`/charly-image:layer`, `…-layer`); the `layer-validator` agent
name; migration-history terminology in the migrate skill; and every ambiguous
"image"/"layer" left untouched. Cross-refs to two renamed `/charly-image:layer` headings
(`sibling candies`, `Mixed entries in one candy`) updated in lockstep. Docs-only —
acceptance via the non-runtime standards (R5 grep self-test, markdown integrity,
adversarial red-flag scan for OCI mis-conversions — all clean).

NOTE — the Go `Layer*` identifiers (the `Layer` struct, `LayerEvalSet`,
`ScanAllLayerWithConfig`, the eval `Layer` section field json:"candy") are deliberately
KEPT: the candy/box rebrand renamed the SCHEMA/wire `layer:`→`candy:` but kept the
internal Go `Layer*` names (the symmetric image→box Go rename was Phase 2; a parallel
`Layer*`→`Candy*` Go-identifier rename of those core build/eval types remains the one
un-done structural axis — a separate Phase-2-scale cutover, not attempted here).

### 2026-06-10 — refactor(charly)!: reconcile the eval-box mode value image→box + Phase-2-missed `Image` cmd fields + stale type-comment R5

The last image→box Go identifiers the prior cutovers missed:
- **eval mode** `RunModeImage`→`RunModeBox` + the emitted result-YAML `mode: "image"`→
  `mode: "box"` (the `charly eval box` mode; `EvalBoxCmd` was already renamed). The
  `mode` value is generated test output (no user config), and the YAML string is never
  functionally matched (only the Go enum `r.Mode == RunModeBox` is), so this is a
  behavior-neutral rename — no MigrationStep. Parser/emitter/tests changed in lockstep.
- **3 Kong subcommand fields** `Image <Cmd>`→`Box <Cmd>` that Phase-2's field grep
  missed (it filtered on `string`/`map`/`[]` types, skipping `Cmd`-typed fields):
  `CLI.Image BoxCmd`, `EvalCmd.Image EvalBoxCmd`, `NewCmd.Image NewBoxCmd` — all
  `name:"box"`. Renamed via gopls.
- **Stale type-comment R5** (candy/box-rebrand leftovers in code comments — the types
  were renamed but the comments lagged): `ImageConfig*Cmd`→`BoxConfig*Cmd`,
  `EvalImageCmd`→`EvalBoxCmd`, `LayerAddPkgCmd`→`CandyAddPkgCmd`, `DeployImageConfig`→
  `DeploymentNode`, plus the box-meaning prose in those comments.

Skill R5: `/charly-internals:go` `RunModeImage`→`RunModeBox`, `r.Image`/`Runner.Image`/
`meta.Image`→`.Box`, `Image EvalBoxCmd`→`Box EvalBoxCmd`, `ai.opencharly.image`→
`ai.opencharly.box`. Verified: zero box-meaning `Image` identifiers remain in the Go
code (all surviving `Image*` are OCI artifact/registry/render). R10: `charly eval run
eval-pod` (exercises `charly eval box`=RunModeBox + the renamed `box` cmd dispatch);
`go test ./...` + `go vet` green.

### 2026-06-10 — docs: sync all skills + root docs with the current code (post box-inversion / Phase-2 / section-values)

A comprehensive docs-sync sweep bringing every skill + root doc in line with the
current code after the session's cutovers (box inversion, Phase-2 `Image*`→`Box*`
Go rename, single-filename, candy/box rebrand, recipe-section-values). The
per-cutover R5 sweeps had handled most of it; this pass caught the residual drift —
especially in the MAIN-repo docs (the plugins-only sweeps never touched them) and in
skills outside the per-cutover scope.

Found + fixed (verified against `charly/*.go`):
- **Root docs** (`README.md`, `CLAUDE.md`, `VISION.md`): `candy.yml`→`charly.yml` as
  the authoring filename; the eval three-section label `{layer, image, deploy}`→
  `{candy, box, deploy}`; `charly eval image`→`charly eval box`; candy/box prose
  ("Sibling-layer"→"Sibling-candy", "Every `layer`"→"Every `candy`", android
  `image:`→`box:`); the `build.yml`-import / embedded-vocabulary tension.
- **Skill code-references** (drift from the Phase-2 + section-values renames):
  `EvalImageCmd`→`EvalBoxCmd`, `ImageConfigCmd`→`BoxConfigCmd`, `MergePlans`→
  `MergePlan`, `ScanAllLayersWithConfig`→`ScanAllLayerWithConfig`, `ResolveAllImages`→
  `ResolveAllBox`, `EmitLabels`→`writeLabels`/`writeJSONLabel`, `DeployImageConfig`→
  `DeploymentNode`/`K8sGenerateOpts`, the scaffold command structs
  (`NewImageCmd…`→`NewBoxCmd…`, `LayerAddPkgCmd`→`CandyAddPkgCmd`), `{Layer, Image,
  Deploy}`→`{Layer, Box, Deploy}`; line-number drifts re-anchored.
- **Schema/label/field VALUES** across ALL skills: `ai.opencharly.image`→
  `ai.opencharly.box` (label renamed), `add_layers:`/`add_layer:`→`add_candy:`
  (deploy.yml field), `box list images`/`layers`→`boxes`/`candies`, the deploy/eval
  skills' pod cross-ref `image:`→`box:` + k8s `cluster:`→`k8s:` (prose lagged the YAML
  examples), the migrate skill's stale HEAD `2026.160.1301`→`2026.161.1301` + the
  MISSING `recipe-section-values` chain row.
- **`.claude/workflows/audit-deploy-configs.js`**: stale discover prompt (`box.yml`,
  `kind "image"`)→`charly.yml`, `kind "box"`.
- **Distro skills** (conservative pass): box-meaning section headers (`## Image
  Properties`→`## Box Properties`, `## Full Layer Stack`→`## Full Candy Stack`, …)
  and "the `<name>` image"→"the `<name>` box" where `<name>` is a charly box.

KEPT (verified): every genuine OCI-image/registry/base-image/builder reference, the
`<image>` CLI placeholder convention, migration-history filenames in the migrate
skill, the `Image:` eval banner literal, "legacy per-kind files still load" notes.
Run via 3 parallel audit agents (root docs / core-eng skills / eval-deploy-distro
skills) cross-checked against the code, then a comprehensive R5 grep self-test
(zero residuals) + markdown-integrity pass. Docs+automation only — no Go, schema, or
`charly.yml` runtime surface; acceptance via the non-runtime standards.

### 2026-06-10 — feat(migrate)!: finish the candy/box rebrand's section-filter VALUES (recipe `from.kind`/`scope`, eval sections, origins, `RefKind`)

The last unfinished tail of the candy/box rebrand. The 2026.156 candy-box-rename
renamed the kind DISCRIMINATORS (`layer:`→`candy:`, `image:`→`box:`) and the eval
label WIRE keys (`json:"candy"`/`json:"box"`) — but left the INTERNAL section-filter
string VALUES and a config surface still using `layer`/`image`:

- **Code values → candy/box** (`charly/`): the eval section-filter literals
  (`gatherSections`/`collectChecksForRun` `case "layer"`→`"candy"` / `case "image"`→
  `"box"`, the `--section` flag values), the recipe `from.kind` + section `scope`
  matching (`eval_recipe_from.go` `recipeFromKinds`, the `case` arms), the baked-label
  ORIGIN prefixes (`"layer:"`→`"candy:"`, `"image:"`→`"box:"` in `description_collect.go`
  / `shellcollect.go` / `eval_recipe_from.go`), the `RefKind` VALUES
  (`RefKindBox = "box"`, `RefKindLayer`→`RefKindCandy = "candy"`), and the
  `charly feature list --kind` filter (`candy`/`box`). KEPT (separate axes): the
  `cache: image` build-cache mode, the eval-output `mode: image|run`
  (`RunModeImage`), the `NestedExecutor` venue `"image"`, the `BuilderDef.Kind:
  "layer"` builder type, and every OCI/k8s/podman `"image"`.
- **MigrationStep `recipe-section-values`** (`charly/migrate_recipe_section_values.go`):
  rewrites recipe `from[i].kind` (`layer`→`candy`, `image`→`box`) and `from[i].scope`
  section lists, SCOPED to `from:` sequence items (`from:` is recipe-exclusive in the
  schema) so a builder `kind: layer` and a check-level `scope: build|deploy` are never
  touched. Comment-preserving (yaml.v3 node API); idempotent; proven by
  `TestMigrateRecipeSectionValues` (asserts the negatives — builder/check untouched).
  Raises schema HEAD `2026.160.1301`→`2026.161.1301`; the load-time gate hard-rejects an
  un-migrated recipe (`invalid kind "layer" (one of: candy, box, pod, vm)`).
- **Re-stamp**: `charly migrate` re-stamped main's `charly.yml` (recipe values +
  HEAD) and all 5 distro submodules (`charly.yml` HEAD stamp). HEAD-CalVer test
  fixtures bumped. Skills updated (`/charly-eval:eval`, `/charly-internals:capabilities`,
  `/charly-internals:go`): the three-section `{layer, image, deploy}` → `{candy, box,
  deploy}`, `--section` values, origin annotations.

R10: `charly eval run eval-pod` on the fresh-built binary (exercises the section
iteration via `eval box`/`live`/`feature run` + loads the re-stamped configs which
validate the recipe `from.kind` at load). `go test ./...` + `go vet` + `charly box
validate` (zero-warnings) green; the migrator RDD-proven on a fixture.

### 2026-06-10 — refactor(charly)!: Go `Image*`→`Box*` identifier rename — candybox-meaning only, OCI `Image*` kept (Phase 2)

The Phase-2 follow-through to the `image:`(OCI) vs `box:`(candybox) docs sweep:
rename **only** the candybox-meaning Go identifiers to `Box*`, KEEPING every
OCI-image-meaning identifier as `Image*`. The disambiguation rule (verbatim user
intent): **`image:` = an upstream Docker/OCI image (KEEP), `box:` = a charly
candybox (rename)**. No schema change (the `box` yaml tag was already in place);
this is a pure internal Go rename. Per the operator's "max consistency" choice,
the sweep went all the way down to local variables and parameters.

**Renamed → `Box*`** (semantic, via `gopls rename` for the overloaded `.Image`
field + params, word-boundary `sed` for unique names): the central
`Config.Image map[string]BoxConfig` field → `Config.Box`; ~170 struct + Kong-arg
`Image` fields → `Box` (every `yaml:"box"` spec field, the CLI positional args);
the box-graph / box-resolver functions (`resolveImageRef`→`resolveBoxRef`,
`ImageNeedsBuilder`→`BoxNeedsBuilder`, `ResolveImageOrder`→`ResolveBoxOrder`,
`ResolveImageLevels`, `LoadBuildConfigForImage`→`LoadBuildConfigForBox`,
`pullNamespacedImage`→`pullNamespacedBox`, `ImageNames`→`BoxNames`,
`ResolveImage`→`ResolveBox`, `mergeImageMap`/`mergeImageConfig`,
`validateImageDAG`, `collectAllImageLayers`, `LayerProvidedByImage`,
`ResolveImageEngine`→`ResolveBoxEngine`, `collectImage`→`collectBox`, the
`scaffold_project` `AddImage`/`AddLayerToImage`/`RemoveLayerFromImage` family, …);
the fields `ImageName`→`BoxName`, `Images`→`Boxes`, `RequestedImages`→`RequestedBoxes`,
`RefKindImage`→`RefKindBox`, `trieNode.images`→`boxes`; local params/vars
`imageName`→`boxName` (491), box-meaning `image`→`box` / `images`→`boxes`,
`skipImage`/`imagePorts`/`origImages`/`consumerImages`/`deployImage(Name)`/
`userImages`/`quadletImages`. Plus the user-facing surface: help strings
(`"Image name"`→`"Box name"`, `github.com/org/repo/image`→`/box`), the
`charly deploy import --image`→`--box` flag, box-meaning error strings
(`"box %q not found in charly.yml"`, …), and the doc-comments on the renamed
symbols. The MCP `box.inspect` tool positional is now `box` (derived from the
renamed Kong field). `plugins` skills swept for the renamed identifiers (R5).

**KEPT as `Image*`** (OCI artifact, registry ref, or stdlib): `imageRef`/`ImageRef`,
`ImageExists`/`ImageInfo`/`InspectRemoteImage`, `v1.Image`, stdlib `image.Image`/`image.RGBA`,
OCI labels, `clean.go`/`registry.go`/`merge.go`/`transfer.go`/`local_image.go`
(podman storage), the `yaml/json:"image"` fields (sidecar/ledger/status-render),
`buildImage`/`pushImage`/`imageTags`/`ensureBuilderImageBuilt` (build/push the
artifact), `BuilderImage*`/`DataImage`/`BaseImage*`/`RebuildImage`/`RunModeImage`,
the OCI-storage CLI args (`charly eval box`, `vm cp-box`), and the `--section`/
`--kind` string *values* (`layer`/`image`/`deploy` — data, not identifiers; a
separate schema concern). The stale `ImageMetadata`/`ImageConfig`/`ResolvedImage`/
`ImageDoc` comment/test-name debris from the earlier candy/box rebrand was cleaned
up to `BoxMetadata`/`BoxConfig`/`ResolvedBox`/`BoxDoc` (the types were already
renamed; only the references lagged).

**Two string-references the compiler + `gopls` could not see — caught by R10, not
the green build** (the reason R8/R10 sit above a clean compile): (1) the
reflection-keyed `CapabilityLabelMap` had a string key `"Image"` (the
`BoxMetadata` field reflection round-trip that powers `charly deploy from-box`) →
fixed to `"Box"`; (2) the embedded `charly/build.yml` supervisord + systemd init
`stage_fragment_copy:` templates referenced the struct field by name as text —
`COPY .build/{{.ImageName}}/…` → `{{.BoxName}}` — which broke Containerfile
generation while `go build`/`go test` stayed green. Both found by running the real
`eval-pod` bed.

R10: `charly eval run eval-pod` (the combined `kind:box` build + `kind:candy`
composition + `kind:pod` runtime + every DeployTarget rendering path mechanism
bed) on the fresh-built binary — build → eval box → deploy → eval live → fresh
update → teardown. `go test ./...` + `go vet` + `charly box validate`
(zero-warnings) all green; R5 grep self-test clean (zero live renamed
identifiers in code, plugins, or `.md`).

### 2026-06-10 — docs: `image:`(OCI) vs `box:`(candybox) — main repo + box/fedora (Phase 1 cont.)

Extends the Phase-1 docs sweep into the main repo + a submodule. `CLAUDE.md`'s
"Cross-kind name reuse" rule: an `image:` entry → a `box:` entry; `charly box build` →
`box.<name>`; `ResolveDeployRef` chooses **box-first** when a box and a candy share a name
(`--add-candy` for the candy-first path). `candy/selkies` + `candy/container-nesting`
comments: `` `layers:` `` → `` `candy:` ``, "root images:" → "root boxes:". `box/fedora`'s
eval-pod bed descriptions: `kind:image`/`kind:layer` → `kind:box`/`kind:candy` (compact,
scalar-safe form — the spaced `kind: box` would break the plain-scalar `feature:`/
`narrative:` values). box/fedora re-tagged `v2026.161.0954`; main re-pins fedora + bumps
the gitlink. Remaining: the Go `Image*`→`Box` identifier rename + Go comments (Phase 2,
R10-gated).

### 2026-06-10 — docs: make the `image:`(OCI) vs `box:`(candybox) distinction clear in the skills (Phase 1)

Per the disambiguation rule **`image:` = an upstream/OCI Docker image, `box:` = a charly
candybox**. The candy-box rebrand renamed the box-map key, the box composition field, the
deps field, and the MCP/CLI verbs — but the skills were never swept. **179** `plugins`
files updated: `images:` / `` `image:` entry `` / `` `image.<name>` `` / `kind:image` →
`box:` / `box.`; `layers:` / `layer:` → `candy:`; `depends:` → `require:`; the MCP/CLI verb
names `image.<verb>` → `box.<verb>` (`image.new.layer` → `box.new.candy`) and
`layer.<verb>` → `candy.<verb>`; the box field paths `image.vm`/`bootc`/`libvirt`/
`capabilities`/`deploy` → `box.<field>`; prose `Image-level`/`layer aliases`/`image-first`
→ `Box-level`/`candy aliases`/`box-first`. **KEPT** (106 OCI-image refs): `base: <docker>`,
the deploy/pod `image:` field, `builder_image:`/`cloud_image:`/`data_image:`, `BASE_IMAGE`,
"an image", "system image". Verified: zero residual box-meaning `image` refs; OCI refs
intact; no double-application; JSON valid. plugins `80d5e9e..07a1052`; main bumps the
gitlink. **Phase 1 of 2** (docs); the Go `Image*`→`Box` identifier rename (candybox-meaning
only, keeping OCI `Image*`) + the main-repo/submodule docs+comments are the remaining work.

### 2026-06-10 — docs: complete the single-filename rebrand sweep in the skills (`box.yml`/`candy.yml` → `charly.yml`)

The single-filename cutover made `charly.yml` the ONE manifest
(`candy/<name>/charly.yml`, `box/<name>/charly.yml`) but the skills were never swept:
~667 `box.yml` / `candy.yml` references across **193** `plugins/**` files still told
users to author the old filenames. Swept to `charly.yml` — path forms
(`candy/<name>/candy.yml` → `…/charly.yml`), compound build-flow phrasing
(`box.yml` + `candy.yml` → `charly.yml`), code-block headers, and bare refs — plus
`kind: image`/`kind: layer` → `kind: box`/`kind: candy`. ~15 separate-concept lines
(where `box.yml`/`candy.yml` meant distinct things — `eval:` fields in a candy/box
`charly.yml` or `deploy.yml`; `box/` checked before `candy/`; the per-kind-naming
illustration; the candy-vs-box alias sections) were hand-rewritten to preserve meaning,
caught by a duplicate-`charly.yml` verification grep. plugins `9ed53fe..80d5e9e`; main
bumps the gitlink. The orthogonal `image:`/`layer:` YAML-KEY terminology (the
`image:`/`layer:` maps, "Image-level", `images:` in examples) is a separate
candy-box-rename follow-up, NOT in scope.

### 2026-06-10 — docs: fix 51 broken `charly image <verb>` command examples in the skills

The image → box command rebrand never reached the skill docs: 51 examples across 25
`plugins/**/SKILL.md` files told users to run `charly image build` / `list` /
`validate` / `reconcile` — no longer a valid command (it is `charly box`). Swept to
`charly box <verb>`, with the `new` subcommands mapping `image new image` →
`box new box` and `image new layer` → `box new candy`; prose ("the image carries …")
left untouched, and the corrected commands verified valid. plugins `c1a85b3..9ed53fe`,
main bumps the gitlink. (The orthogonal `box.yml` / `candy.yml` stale-filename debt in
the skills — a separate single-filename-cutover follow-up — is NOT in scope here.)

### 2026-06-10 — docs: complete the `image/<distro>` → `box/<distro>` mount-rename sweep into the 5 distro submodules

The mount-rename cutover (below) was superproject-only; the 5 distro submodule repos
still described themselves as "mounted at `image/<distro>`" and gave
`charly -C image/<distro> image build …` examples — broken twice over (the stale mount
path AND the rebranded `charly image` verb, which is now `charly box`). Each
submodule's signpost `CLAUDE.md` header, `README.md`, and `charly.yml` comments are
swept to `box/<distro>` + `charly box <verb>` (`box/arch` also fixes a cross-repo
`image/cachyos` reference in `cuda-arch-builder`'s comment). Each submodule is
re-tagged `v2026.161.0830`; main re-pins its `arch`/`cachyos`/`fedora` `@github`
imports and bumps all five gitlinks to the doc commits. Box content is byte-identical
(comment-only), so `charly box validate` fetches the new tags and resolves
zero-warnings. Completes the R5 obligation the mount rename deferred as separable.

### 2026-06-10 — refactor(migrate): consolidate the 7 dir-walk skip-lists into one submodule-aware helper

Five project migrators (`legacy_local_images`, `migrate_local_images`,
`migrate_require_image`, `migrate_target_local` ×2) hardcoded an identical
`.git/node_modules/.build/.cache/.eval/plugins` dir-skip list and DESCENDED into
git submodules (skipping only the literal `plugins`), so a superproject
`charly migrate` would rewrite the `box/<distro>` submodules' own files. They — plus
`migrate_description` and `migrate_entity_version` — now route through ONE shared
`migrateSkipDir` (`charly/migrate_walk.go`): it skips the common build-artifact /
cache dirs by name AND any nested git submodule by STRUCTURE (`isNestedGitRepo`,
moved here from `migrate_entity_version.go`), with a walk-root guard so the
project's own top-level files stay in scope. The hardcoded `plugins` / `image` dir
names are gone — a submodule is skipped wherever it is mounted. Walkers with extra
needs layer them on top (entity-version ALSO skips `output`/`testdata`; the
description scaffolder ALSO skips `bin`/`vendor`/`.claude`). Proven on a live
`charly migrate`: a legacy `target: host` in the ROOT migrates to `target: local`
while the SAME in a `box/` submodule is left untouched (no `.bak`, never rewritten).
No schema bump (a walk-correctness + R3 refactor); covered by `TestMigrateSkipDir`.

### 2026-06-10 — fix(migrate): make single-filename `rewriteDiscover` idempotent for candy-only projects

The single-filename migrator's `rewriteDiscover` clobbered a project's discover to
`[box, candy]` whenever it wasn't EXACTLY that — so a real `charly migrate` on the
main repo (whose discover is candy-only after the box inversion: it owns no boxes)
would wrongly re-add a `box` discover path, re-introducing box discovery over the
`box/<distro>` submodule roots. The idempotency guard `discoverIsBoxCandy` required
exactly two specs (box AND candy); it is replaced by `discoverIsSingleFilenameForm`,
which treats ANY flat scan-spec discover whose specs all use the `charly.yml`
manifest (explicit or the default) as already-migrated — regardless of which / how
many paths. A pre-single-filename discover still carries an explicit `candy.yml` /
`box.yml` manifest (emitted by the earlier `discover-flatten` step), so it fails the
check and IS rewritten to the box+candy default; only an already-single-filename
discover is now a no-op. `charly migrate --dry-run` on main now reports "nothing to
migrate" instead of "would apply single-filename" (the pre-existing artifact noted in
the box-inversion entry below). No schema bump (a migrator idempotency fix); covered
by `TestMigrateSingleFilename_CandyOnlyDiscoverPreserved`, proven on a live
`charly migrate` of a candy-only fixture (no-op, no box path added).

### 2026-06-10 — refactor!: relocate the 5 distro submodule mounts `image/<distro>` → `box/<distro>`

After the box inversion (below) the `image/` mount directory was misnamed: those
five git submodules no longer hold "images" — they hold whole distro **box
projects** (`arch`, `cachyos`, `debian`, `fedora`, `ubuntu`), while `box/` (the
natural home) sat empty. This cutover renames the five submodule **mount points**
`image/<distro>` → `box/<distro>`. It is a **superproject-only** change: the five
distro submodule repos are NOT touched (same commit SHA, new path), their `@github`
import pins are unchanged, and the import graph is identical. `charly` reaches the
boxes via the `@github` cache, not the local mount, so the rename is
**loader-invisible** (`charly box validate` zero-warnings before and after).

**The move (per submodule).** `git mv` on a submodule fails under git 2.54
(`fatal: renaming … No such file or directory` — the `.git/modules/<parent>`
gitdir relocation breaks), populated OR deinitialized. The working recipe, proven
on a scratch superproject + linked worktree first (RDD): `git submodule deinit -f
image/<d>` → `git rm -f image/<d>` → `mv .git/…/modules/image/<d>
.git/…/modules/box/<d>` (relocate the existing module gitdir — no re-clone) →
`git submodule add --force [-b main] <url> box/<d>` (git prints `Reactivating local
git directory`, reusing the moved gitdir) → `git -C box/<d> checkout <recorded-SHA>`
→ `git add`. `branch = main` on `arch` is preserved. git records each as a
**rename** (`R image/arch -> box/arch`) because the gitlink SHA is byte-identical —
confirming the move changes *home*, not *content*.

**Generic submodule skip in the entity-version migrator (`charly/migrate_entity_version.go`).**
The dir-walk skip list hardcoded `"image"` to avoid descending into the submodules
during a superproject `charly migrate`. A naive `"image"→"box"` swap would regress
every NORMAL project (whose own `box/<name>` boxes must be migrated), so the skip
is now generic: a directory is skipped when it is a registered git submodule
(`isNestedGitRepo` — a nested `.git`, with a `path != cwd` guard so the walk root
stays in scope), dropping the hardcoded `"image"`/`"plugins"` names. Strictly more
correct than the old hardcode — it survives any future mount rename AND
distinguishes a submodule checkout from a normal project's own `box/<name>` box dir.
Covered by `TestMigrateEntityVersion_SkipsNestedGitRepos` (fails without the fix).

**Sweep.** `charly.yml` `context_ignore: image` → `box`; the `image/<distro>` /
`image/<distro-name>` mount-path references in `CLAUDE.md` (dispatcher rows +
prose), `README.md`, the Go comments (`build.yml`, `evalcollect_test.go`,
`localpkg.go`, `localpkg_test.go`, `refs.go`, `migrate_localpkg_map.go`,
`candy/charly/charly.yml`), and 44 `plugins/**/SKILL.md` files → `box/<distro>`.
The `migrate` skill's entity-version row now describes the generic
nested-submodule skip. NOT touched: the Go stdlib `image`/`image/png` imports, the
`cache: image` build-cache mode, `kind: image`, the `image:`/`images:` YAML keys,
and the `<image>/<instance>` deploy-id format.

**Separable, NOT folded in (R2).** (1) The six OTHER `charly/migrate_*` dir-walk
skip lists carry an identical hardcoded `.git/node_modules/.build/.cache/.eval/plugins`
list (no `image`/`box`) — they already descend into the submodules regardless of
mount name, so the rename does not regress them; consolidating all seven into one
shared submodule-aware helper is a future R3 cleanup. (2) `charly migrate --dry-run`
on main reports "would apply single-filename" because the single-filename
migrator's `discoverIsBoxCandy` guard expects discover to list BOTH `box` and
`candy` paths, whereas main's discover is candy-only — a PRE-EXISTING box-inversion
artifact (main owns no boxes), identical before and after this rename and
independent of it. (3) The five submodule signpost `# image/<distro>` headers are
submodule-owned and invisible to main's `git grep` — cosmetic, left to the
submodule repos.

**R10** (`disposable: true`): `charly -C box/arch eval run eval-arch-vscode-pod` —
the relocated `arch` submodule builds + deploys + evals from its new `box/arch`
mount; full Go suite green (incl. the new skip test); `charly box validate`
zero-warnings with submodules at `box/`. Landed main + `plugins` (no submodule
re-pin, no tag bump on the distro repos).

### 2026-06-10 — feat!: box inversion — relocate every box from main's `box/` into the `image/<distro>` submodules; flip the import graph

The main repo's `box/` is now EMPTY. All 43 box definitions moved into the
`image/<distro>` submodule matching the box's ROOT distro, and the
main↔submodule import direction flipped from a mutual cycle to one-directional
(main → submodule).

**Relocation (by root distro):**
- **image/arch** gained the Arch base/builder stack (`arch`, `arch-builder`,
  `cuda-arch-builder`) + `vscode-test`. Self-contained (`import: []`).
- **image/cachyos** gained `versa`, `openclaw`/`openclaw-full`/`openclaw-desktop`,
  `githubrunner`, `android-emulator`, `charly-selftest` + the `pixel9a-36` /
  `pixel9a-endpoint` android devices. It now imports `arch` (for
  `arch.arch-builder` / `arch.cuda-arch-builder`) instead of `charly`.
- **image/fedora** gained the Fedora base/builder stack (`fedora`,
  `fedora-nonfree`, `fedora-builder`) + ~29 fedora-rooted boxes (jupyter,
  jupyter-ml, comfyui, ollama, unsloth-studio, immich, immich-ml, openwebui,
  hermes(-playwright), web, chrome-headless, eval-pod, eval-target,
  composition-source/app, filebrowser, k8s, mcp, os, redis*, selftest-*,
  tier1/tier23, valkey-test). Self-contained (`import: []`).

**Inverted import graph:** main imports all three active distro submodules
(`arch`/`cachyos`/`fedora`) one-directionally to reference the relocated boxes in
its inline eval/vm/local/k8s/android orchestration; the submodules reach main's
shared candies via `@github` refs (not a namespace import), so the former
main↔cachyos / main↔fedora mutual cycles are dissolved. arch + fedora are fully
self-contained; cachyos imports only arch.

**Bed relocation:** each box's per-box R10 bed moved into the box's submodule
(bare local ref). Main retains only the box-less mechanism/substrate beds
(`eval-k3s-vm`, `eval-cross-vm-http`, `eval-local`, `eval-charly-vm`) and the two
cross-deployment mechanism beds (`eval-cross-pod-cdp`, `eval-cross-local-http`),
which now reference their subject boxes as `fedora.web` / `fedora.chrome-headless`
(their shared driver template stays in main, so moving them would duplicate it).
Main's `defaults.builder` was dropped (main owns no boxes to build).

**Two pre-existing Go bugs were exposed and fixed (R3) in main's charly binary:**
1. The validator/generator compared the resolved-layer-order MAP KEY (the
   qualified `@github` path for a remotely-consumed candy) against a bare layer
   name, at three sites (`validate.go` socat / depends_layer, `generate.go`
   traefik). The traefik one SILENTLY dropped routes for any box consuming traefik
   via a remote ref. Fixed to compare `layer.Name`. Exposed because the relocated
   boxes are the first to consume `port_relay`/`traefik` candies cross-repo via
   `@github` (caught by cachyos `eval-openclaw-pod`).
2. The eval-recipe loader resolved `from[].name` (kind:image) only in the local
   image map, not namespace-aware. Fixed to use `resolveImageRef`, so a recipe can
   import scenarios from a relocated image (`fedora.composition-source`,
   `fedora.jupyter`).

**Known orthogonal finding (NOT a regression of this cutover):** the `charly-mcp`
candy's `box.list.boxes` deploy-scope check fails on fedora-coder because the
`@github`-pinned `candy/charly:v2026.160.2017` bundles an older charly binary
(2026.159.1515) whose MCP auto-default can't list the current main project. The
fedora-coder image content is byte-identical pre/post move, and a boxless main
returns exit 0, so the cutover neither caused nor worsened it; bumping the
charly-candy pin is a separable future change.

### 2026-06-10 — chore(images): enable all previously-disabled boxes

Removed the `enabled: false` flag from all 12 previously-disabled boxes so they
enter the default build / validate / generate working set:

- **main**: `tier1`, `tier23`, `redis`, `valkey-test`, `comfyui`, `immich`,
  `hermes-playwright`, `openwebui`, `unsloth-studio`, `ollama`,
  `jupyter-ml-notebook`.
- **image/fedora**: `python-ml`.

No box content changed — only working-set membership. Verified: `charly box
validate` + `charly box generate` exit 0 with zero errors / zero warnings on
both repos, so every newly-enabled box resolves its layer chain and emits a
valid Containerfile (generate now emits the 12 it previously skipped — the
eval-coverage that fails without this change). Full per-box image builds were
NOT run (the GPU images — comfyui / ollama / unsloth-studio / immich /
jupyter-ml-notebook / python-ml — are multi-GB, hours each); the verification is
resolution + Containerfile generation, not a built artifact per box.

### 2026-06-10 — feat(images)!: drop the bootc submodule; fold the nvidia + selkies images into their base-distro submodules

Three image-only submodules were removed from the superproject, shrinking the
`image/` set from 8 to 5 (`arch`, `cachyos`, `debian`, `fedora`, `ubuntu`).

- **`image/bootc` (`overthinkos/bootc`) deleted entirely.** Gone with it: the
  boxes `bazzite` + `aurora`; the 8 local candies `bootc-base`, `bootc-config`,
  `copr-desktop`, `desktop-apps`, `os-config`, `os-system-files`, `ujust`,
  `vr-streaming`; and the VMs `aurora-bootc` + `bazzite-bootc`. The submodule was
  fully isolated (nothing outside it referenced it), so the drop breaks nothing.
  The bootc *capability* in `charly` is unchanged — there is simply no shipped
  bootc box anymore (declare your own `bootc: true` box + `source.kind: bootc` VM).
- **`image/nvidia` (`overthinkos/nvidia`) removed; its boxes folded into
  `image/fedora`.** The Fedora GPU base `nvidia` (`base: charly.fedora-nonfree`
  + the `nvidia`/`cuda` candies, unchanged) and the disabled `python-ml` box now
  live in the `overthinkos/fedora` submodule. Main's GPU pod families (`comfyui`,
  `jupyter-ml`, `jupyter-ml-notebook`, `ollama`, `unsloth-studio`) repoint from
  `base: nvidia.nvidia` to `base: fedora.nvidia`. This introduces a NEW
  **main ↔ fedora mutual import** (main mounts `overthinkos/fedora` under the
  `fedora` namespace; fedora imports main under `charly`), cycle-broken at load by
  repo identity — `image/fedora/charly.yml` gains an explicit
  `repo: github.com/overthinkos/fedora`. The relocated `nvidia` box is
  byte-identical to its former self: `charly box generate` emits the same FROM
  chain and the same `ghcr.io/overthinkos/nvidia` image (the resolve key label
  changes `nvidia.nvidia` → `fedora.nvidia`; nothing else).
- **`image/selkies` (`overthinkos/selkies`) removed; its boxes split by base
  distro.** `selkies-labwc` (CPU streaming desktop) → `image/cachyos`
  (`base: cachyos.cachyos` → local `base: cachyos`), joining its GPU sibling
  `selkies-labwc-nvidia` already there. `sway-browser-vnc` → `image/fedora`
  (`base: charly.fedora`, unchanged). Main's `android-emulator` repoints
  `base: selkies.selkies-labwc` → `base: cachyos.selkies-labwc`. The two R10 beds
  follow their boxes (`eval-selkies-labwc-pod` → cachyos, `eval-sway-browser-vnc-pod`
  → fedora). The selkies/sway *candies/layers* stay in main (shared with
  openclaw-desktop); only the images moved.

Main's `import:` drops the `nvidia` + `selkies` namespaces and gains `fedora`;
`main ↔ cachyos` + `main ↔ fedora` are now the only mutual imports. No schema
change (no migration step, no `version:` bump) — this is a content + git
restructure; each affected charly-project repo earns a fresh per-push CalVer tag.

R10: both relocated streaming beds passed the full sequence on fresh rebuilds
(`eval-selkies-labwc-pod` ok 691s, `eval-sway-browser-vnc-pod` ok 635s — build →
eval-image → deploy → eval-live → fresh-update → teardown); the relocated `nvidia`
GPU base built clean and `charly eval box`'d 12/12; main `charly box validate` +
`generate` ran zero-errors / zero-warnings with the three submodules gone and
every repointed `base:` resolving (`fedora.nvidia`, `cachyos.selkies-labwc`).

### 2026-06-09 — refactor(validate): drop the cross-image port-overlap NOTE

`charly box validate` / `charly box generate` no longer print the advisory
`Note: images "X", "Y" share host port N (only one can run at a time, or use
deploy.yml to remap)` line. Two images declaring the same canonical host port in
`box.yml` (3000, 9222, …) is BY DESIGN — deploy time remaps via `deploy.yml` or
`port: [auto]` — so the note was pure noise on every validate/generate run. The
emitter `validatePortOverlap` (and its sole helper `formatImageList`) is deleted
from `charly/validate.go`; it only wrote to stderr and never touched the
validation result or the generated Containerfiles, so the removal is purely
subtractive (no schema change, no `version:` bump). Verified: `go test ./...`
green; a live `charly box validate` + `charly box generate` exit 0 with zero
"share host port" notes and the emitted Containerfiles unchanged.

### 2026-06-09 — feat(schema)!: single-filename cutover — charly.yml is the only filename for box + candy, build vocabulary embedded in the binary

The single-filename cutover (`charly migrate` step `single-filename`, schema
`2026.160.1300`; HEAD bumped to `2026.160.1301`). `charly.yml` becomes the ONE
filename that holds box and candy definitions, and the only file a project needs.

**What changed, project-side (applied by `charly migrate` to all 9 charly-projects
— the main repo + every `image/<distro>` submodule):**

- **Boxes are discovered per-box dirs.** Every `box:` entry in `box.yml` / `base.yml`
  (and an inline `box:` map in `charly.yml`, e.g. the bootc submodule) is split into
  `box/<name>/charly.yml` (a kind-keyed `box:` doc), discovered the same way candies are.
- **Candy manifests rename** `candy/<name>/candy.yml` → `candy/<name>/charly.yml`.
- **Per-kind files fold in.** `vm.yml` / `pod.yml` / `k8s.yml` / `eval.yml` /
  `local.yml` / `android.yml` move their kind keys into `charly.yml`'s root mapping;
  the files are deleted.
- **`discover:`** is rewritten to scan BOTH `box/` and `candy/` with the single default
  manifest (`charly.yml`); the folded per-kind files + `build.yml` are removed from `import:`.

**Embedded build vocabulary (mirrors `sidecar.yml`).** The canonical `build.yml`
(`distro:`/`builder:`/`init:`/`resource:` vocabulary) moved to `charly/build.yml` and is
`//go:embed`'d into the binary (`embed_build.go`, mirroring `sidecar.go`'s
`embeddedSidecarYAML` + `LoadEmbeddedSidecarConfig`). It is merged as the LOWEST-priority
base via `applyEmbeddedBuildDefaults` (the existing gap-filling `mergeDistroMap` /
`mergeBuilderMap` / `mergeInitMap` / `mergeResourceMap`, project-wins by construction —
the same base/overlay relationship as `MergeSidecar`). A project EXTENDS or OVERRIDES the
vocabulary by declaring its own `distro:`/`builder:`/`init:`/`resource:` entries inline in
`charly.yml` or in an imported vocab file; a project that customizes nothing needs no
`build.yml` at all. The `single-filename` migrator drops the `build.yml` import (a flat
local `build.yml` byte-matching the embed is deleted; a customized one is left imported; a
remote `@github.../build.yml:vTAG` ref is dropped). `format_config:` was already vestigial
and its stale mentions are swept.

**Go side.** `DefaultManifest` is deleted and unified into `UnifiedFileName` ("charly.yml")
— the ONE YAML filename the code knows; every bare-string straggler (`deploy_ref.go`,
`reconcile.go`, scaffold/mcp/vm-clone) routes to the constant, guarded by
`TestNoHardcodedYAMLFilenames`. `ApplyDiscover` now runs in the unified loader's main path
(`loadUnifiedInto` depth-0 boundary, for the root AND every namespace), so a discovered
`box:` doc reaches `ProjectConfig` (previously only the layer-loading path discovered, so
box-via-discover never registered). `findEntityDirs` treats a missing discover path as a
no-op (a project may carry `discover: [box, candy]` while owning only one directory). The
scaffolders (`box new project`/`new box`/`new candy`) emit the new layout.

Assisted-by: Claude (fully tested and validated)

### 2026-06-09 — docs(vision): thesis-voice refinements (tagline, intro, tenets 1/6/9/10, closing)

A round of authorial refinements to VISION.md's voice, reconciled on top of the
mantra-unification cutover (the edits were drafted against the pre-unification
version, so they were re-applied onto current main rather than merged raw):

- **Tagline** → *"The thesis behind the candybox."*
- **Intro** rewritten — Charly "builds a whole candy store and treats the agent to
  a whole candy factory, ready to produce every candy imaginable."
- **Tenet 1** — "KVM-isolated VMs" → "isolated VMs".
- **Tenet 6** — dropped the "the recipe IS the test / first-class author & grader"
  sentence (the point is carried by the RDD/ADE co-equal-twin sentence after it).
- **Tenet 9** — "wrong mix of candies" / "the wrong box" / "what makes the whole
  candy store a pleasure to work in".
- **Tenet 10** — trimmed "never once leaving the room"; "caught in" → "being part
  of" its own feedback loop.
- **Closing line** — leaner: "not to wave it around or hurt anyone, but to
  caramelize the top of a perfect crème brûlée."

The single `build → deploy → prove → iterate` mantra (Tenet 7) is preserved.
Incidental slips fixed during reconciliation: `Candystore` → `candy store`,
`treats … with` → `treats … to`, `make the every candybox` → `make every
candybox`. Docs-only — no `MigrationStep`, no `charly.yml` `version:` bump.

### 2026-06-09 — docs(vision): proofread + unify the build → deploy → prove → iterate mantra

A grammar/spelling/flow pass over VISION.md. The prose was already clean — no
spelling mistakes — so the change is small and surgical:

- **Loop mantra unified and stated once.** VISION.md previously spelled the
  iteration loop three different ways in prose (`build → run → deploy → evaluate`
  in Tenets 7 and 10, `build → deploy → prove → iterate` on the "Hand the whole
  line to the agents" arc). It now spells the *mantra* — **`build → deploy → prove
  → iterate`** — exactly ONCE, in Tenet 7 (the "pass after pass until silk"
  iteration tenet), and refers to it as "the loop" / "the pass" everywhere else
  (Tenet 10, the heading bullet). The mantra is the philosophical throughline;
  it is deliberately distinct from README.md's concrete-CLI
  `build → run → deploy → evaluate` lifecycle section (whose verbs map to real
  commands — there is no `charly prove` verb), which is left untouched. VISION's
  two citations of that README section are accurate pointers and remain.
- **Grammar.** Tenet 10: "molds a recipe can pour" → "molds a recipe can pour
  **into**" (consistency with Tenet 2's "pours into every mold"). Tenet 8: "root
  cause analysis" → "root-cause analysis" (compound modifier, matching the
  project's `root-cause-analyzer`).
- **Flow.** Tenet 6: "a first-class author of it and a first-class grader of it" →
  "a first-class author and grader of it".

Docs-only cutover — no Go / YAML / schema surface, so no `MigrationStep` and no
`charly.yml` `version:` bump.

### 2026-06-09 — docs(vision): add the crème-brûlée closing line

VISION.md gains a one-paragraph closing flourish, set after "Where the factory is
heading" and before the navigational footer: *we look forward to the day every
agent asks for a blowtorch — not to wave it around, but to caramelize the top of a
perfect crème brûlée.* It restates the candyboxing thesis as a sign-off — the
blowtorch is precisely the kind of "dangerous" tool a restrictive sandbox would
strip away; candyboxing hands over the whole candy store (blowtorch included)
because the *room* is secured, and trusts what gets made inside the box. Italic,
to bookend the opening `*The thesis behind Charly.*` tagline. Docs-only — no
schema / `version:` change.

### 2026-06-09 — docs(vision): add Tenet 10 — factory-in-the-loop evaluation + nested candyboxing

VISION.md gains a tenth tenet, **"The factory fits in a box, too — candyboxes all
the way down,"** naming two concepts the system already implements but the thesis
had not yet stated explicitly:

- **Nested candyboxing** — a candybox (a disposable, secured container / VM) can
  be poured with the whole `charly` line and used to build, deploy, and prove
  OTHER candyboxes inside it. This is exactly what a `kind: eval` bed is: its
  first R10 step is `charly box build` *inside the bed* (nested podman + the full
  layer/image library). A candybox that builds candyboxes.
- **Factory-in-the-loop evaluation** — inside that boxed factory the full
  build → run → deploy → evaluate loop runs with the evaluation *verdict* driving
  each pass (the `charly eval run <score>` plateau-bounded loop; evaluation in the
  driver's seat).

The tenet is deliberately **taster-agnostic**: it never says whether a human or an
agent operates the loop, because the factory-in-the-loop principle holds either
way — what matters is that the verdict drives iteration, not who reads it. (Tenet
4, "Two tasters at one bench," remains the place the human/agent duality is named;
Tenet 10 stays above it.)

Docs-only cutover — no Go / YAML / schema surface touched, so no `MigrationStep`
and no `charly.yml` `version:` bump. Cross-references added to CLAUDE.md
"Candyboxing" / "Disposable-Only Autonomy", `/charly-eval:eval` (the `kind: eval`
beds + the score loop), and `/charly-internals:disposable`.

### 2026-06-09 — refactor(docs+eval): rename the pillar "Agent Driven Development (ADD)" → "Agent Driven Evaluation (ADE)"

The fourth philosophy pillar — formerly **Agent Driven Development (ADD)** — is
renamed to **Agent Driven Evaluation (ADE)**, aligning the name with what the
pillar has always been: the spec IS the test, and agents are first-class AUTHORS
*and* GRADERS of it. The abbreviation moves `ADD → ADE` in lockstep (kept, not
dropped). ONE atomic cross-repo rename sweep (R5): no schema / API / CLI surface
changed (the command names `charly box feature run`, `charly eval feature run`,
`charly candy add-scenario` are untouched), so there is NO `MigrationStep` and NO
`charly.yml` `version:` bump.

Swept in the same change:
- **Docs** — the CLAUDE.md pillar section + R0 dispatcher row + Key-Rules pointer
  + the doc-map pillar enumeration; VISION.md (pillar 6 + the "verification
  cadence" arc); README.md (the "acceptance" section heading + cross-ref). The
  development-loop framing was reframed to center evaluation/grading ("the AI
  loop that writes the implementation until the scenarios pass" → "the AI loop
  whose evaluation verdicts drive each iteration"; "drives (human or agent) to
  it" → "grades against it"), per the operator's intent for the new name.
- **Skills** — `/charly-eval:eval`, `/charly-internals:strict-policy` (section
  label `## ADD.` → `## ADE.`, parallel to `## RDD.`), `/charly-internals:disposable`,
  `/charly-image:layer`, including every quoted cross-reference to the renamed
  section anchors.
- **Go** — the agent grader's system-prompt string (`eval_feature_grader.go`:
  "…grader for Agent Driven Evaluation."), four Kong `--help` strings
  (`charly eval feature`, `charly eval feature run`, `charly box feature`,
  `charly candy add-scenario`), and the file-header / binding comments across
  `eval_feature_run.go`, `eval_feature_grader.go`, `description_run.go`,
  `eval_bed_run.go`, `evalrun.go`, `scaffold_cmds.go`, `scaffold_scenario.go`.
  A new `TestBuildGraderPrompt_PillarName` is the eval-coverage gate — it FAILS
  on the retired name and passes on the new one.
- **Config** — the `eval-stack-layer` candy.yml scenario comment.

NOT touched (R5 false positives, deliberately left verbatim): the Dockerfile
`ADD` instruction comments in `config.go` / `build.go` / `generate.go` /
`format_config.go`, the verb "ADD" in the container-nesting + migrate skills and
the `eval-target-layer` candy.yml, and the historical mentions in this CHANGELOG
— this file is the sanctioned home for the old name, and existing entries are
preserved verbatim (history is not rewritten).

Verified: `go test ./...`, `charly box validate` (zero warnings), the R5 grep
self-test (`git grep "Agent Driven Development"` returns only CHANGELOG), and an
R10 run of the `eval-pod` disposable bed including `charly eval feature run
eval-pod`, which drives the `kind: ai` grader through the renamed system prompt.

*Assisted-by: Claude (fully tested and validated)*

### 2026-06-09 — feat(eval): nested-in-VM pod probes run IN the guest — eliminate the 14 skips (Cutover 6)

The `eval-cachyos-gpu-vm` bed's nested `selkies-kde` eval reported `96 passed · 0
failed · 14 skipped`. Operator directive: *"there is no reason nested pod testing
can't work."* The 14 skips were real coverage holes from a long-standing
deliberate limitation (NOT a regression): a pod nested in a VM ran its eval via
the HOST through a NestedExecutor chain, but three host-vantage mechanisms could
not cross the VM boundary and SKIPPED — the protocol verbs (cdp/wl/dbus/vnc/mcp,
which shell out to a host `charly eval <verb>` subprocess resolving the container
on the HOST's podman) and `${HOST_PORT}` addr/http (resolved via HOST `podman
inspect`).

**Fix — delegate the nested-in-VM pod's eval to the guest `charly`.** FROM THE
GUEST the nested pod is a DIRECT pod (guest-local podman, ports on guest
localhost, the guest `charly` installed by `EnsureCharlyInGuest`), so the
already-working direct-pod path runs cdp/wl/mcp + `${HOST_PORT}` natively.
`charly eval live <vm>.<pod>` now runs `charly eval live <pod>` in the guest over
SSH (`runVm` → `guestNestedEvalCmd` → `SSHExecutor.RunCapture`) and propagates the
guest's report + exit code; the guest reads the SAME baked checks from the
cp-box'd image, so the check set is identical and only the previously-unreachable
probes now execute. The superseded `Runner.SkipHostContainerVerbs` field + skip
branch + test are deleted (R5). Every other eval path (direct pods, the VM
itself, host, `on:`-redirected cross-deployment probes) is unchanged.

**Blocking fix folded in — `charly deploy from-box` deploys are now
self-describing.** Delegation surfaced a pre-existing gap: `from-box` recorded the
deploy.yml `box:` as the deploy KEY (a short name), persisting the full
`ExplicitRef` only into the quadlet — so a later project-FREE command in the guest
(`charly eval live <name>`) failed with *"short name … requires a project directory
with charly.yml."* `from-box` now persists the full ref as the deploy's `box:`
(`config_image.go`), so `charly eval live` / `status` / `config` resolve it straight
from local storage with no `charly.yml` (the labels-only-deploy promise). The
deploy KEY stays the name for container/quadlet/secret naming.

Live-proven: the guest `charly eval live selkies-kde` (box → full ref) ran the
formerly-skipped wl/mcp/`${HOST_PORT}` probes and reported **110 passed · 0 failed
· 0 skipped**. R10: `eval-cachyos-gpu-vm` on the real RTX 4080.

*Assisted-by: Claude (fully tested and validated)*

### 2026-06-09 — feat(resource): auto-allocate an NVIDIA GPU `<hostdev>` for GPU-requiring VMs (Cutover 5)

A `target: vm` deploy/eval-bed that needs a physical GPU declares
`requires_exclusive: [nvidia-gpu]`. Before this cutover that token ONLY drove
the preempt arbiter (`preempt.go`) and was fully decoupled from the GPU
`<hostdev>` passthrough — the device had to be hand-authored: run
`charly vm gpu list`, copy the emitted block, paste it into the per-host overlay
`~/.local/share/charly/vm/<domain>/instance.yml`. With no overlay,
`eval-cachyos-gpu-vm` booted GPU-less and its nested NVENC checks failed. Per the
operator directive — *"if a resource is required either add it automatically or
fail hard"* — `charly vm create` now does it automatically.

**New YAML-configurable `resource:` kind** (the token→hardware-selector map; the
selector lives in config, never hardcoded in Go). In `build.yml`:

```yaml
resource:
  nvidia-gpu:
    gpu:
      vendor: "0x10de"   # PCI vendor DetectVFIO matches
```

`resource:` registers in the unified loader exactly like the other build-vocab
kinds (`kindKeys` / `rootShapeKeys` / `UnifiedFile.Resource` / `ResourceDoc` /
`mergeResourceMap` / a `mergeKindDoc` case). It is additive + optional — configs
without it load unchanged, so NO migration step and NO `LatestSchemaVersion`
bump (mirrors the autostart-field precedent).

**Auto-allocation at `charly vm create`** (`gpu_allocate.go`,
`autoAllocateExclusiveGPUs`). The create path already resolves the claimant node
(`lookupVMClaimant`) for preempt arbitration; that node's
`requires_exclusive` tokens are now also mapped against `resource:`. For a token
with a `gpu:` selector, charly: defers to any operator-authored `<hostdev>`
(committed `vm.yml` OR `instance.yml` — no double-inject, no re-detection);
requires `backend: libvirt` (a PCI `<hostdev>` does not render under qemu); then
`DetectVFIO()` + `selectGPUByVendor` → on a hit, persists the whole IOMMU-group
hostdev block into the per-host `instance.yml` (visible + stable, with a
provenance header comment) and folds it into the override `ApplyToVmSpec`
injects; on a miss, **FAILS HARD** naming the token + vendor. `selectGPUByVendor`
+ `vfioGpuToHostdevs` are reused by the existing `charly vm gpu list` text emitter
(`renderHostdevsBlock` now formats the shared structured builder — R3, no
drift). Validation (`validate_preempt.go`): a `gpu:` selector requires a
non-empty vendor; a `target: vm` GPU claimant pinned to `backend: qemu` errors at
load.

**Blocking fix folded in (R5, R3) — selkies wayland-socket eval check.** The
first R10 run surfaced one failing check: `candy/selkies` asserted
`test -e /tmp/wayland-1`, but commit `038f6b6` (2026-06-08) moved
`XDG_RUNTIME_DIR` from `/tmp` → `/tmp/xdg-runtime` and the session wrappers
tracked it (`${XDG_RUNTIME_DIR:-/tmp}/wayland-1`) while this eval check was the
lone straggler left at the hardcoded `/tmp` path — broken on EVERY selkies pod
since that cutover, surfaced first by this GPU bed. The check now uses braceless
`$XDG_RUNTIME_DIR` (image ENV, inherited by the exec shell) with a `/tmp`
fallback, matching the wrappers. RCA confirmed the GPU passthrough itself was
healthy (kwin_wayland + NVENC live on the RTX 4080); the socket existed at
`/tmp/xdg-runtime/wayland-1` all along.

Live-proven on the host's RTX 4080 (`10de:2702`, IOMMU group 13): a single
`charly vm create cachyos-gpu-vm` auto-allocated both group functions
(`0000:01:00.0` + `0000:01:00.1`) and wrote the instance.yml block, with zero
manual `charly vm gpu list` step. R10 on the disposable `eval-cachyos-gpu-vm`
bed PASSED all 9 steps including the fresh-`charly update` rebuild gate (nested
selkies eval: 96 passed · 0 failed · 14 skipped). Scope: VM passthrough only
(the RTX 4080 is vfio-bound = VM-only; pod GPU keeps the existing AMD
`renderD128` auto-detect). The 14 skips are nested-in-VM host-container verbs
(cdp/wl/mcp + `${HOST_PORT}` addr/http) that the runner currently cannot route
through the VM chain (`evalrun.go` `SkipHostContainerVerbs`) — a separate,
deliberate limitation addressed in its own follow-up cutover, not a regression.

*Assisted-by: Claude (fully tested and validated)*

### 2026-06-09 — refactor(rebrand)!: finish the `ov`→`charly` / `overthink`→`opencharly` cutover (Cutover 4)

The fourth and final rebrand cutover zeroed out every in-scope `ov`/`overthink`
residue Cutovers 1–3 left behind and folded in the architectural fixes the work
surfaced. ONE atomic commit per repo, coordinated across the superproject + 8
`image/*` submodules + `plugins` + 3 `pkg/*` packaging repos, landed at the
shared ecosystem tag **`v2026.160.0856`**.

**Rebrand surface (the residue → zero):** the env prefix collapsed to
`CHARLY_*` (was `CH_*`/`OV_*`); the credential-service prefix re-keyed `ov/` →
`charly/` (`charly/secret|enc|vnc|api-key|probe`, the keyring/addlayer probe
consts) with the validator now hard-rejecting any non-`^charly/` key; ~50
internal Go identifiers `Ov*` → `Charly*` (`runCharlySubcommand`,
`runCharlyVerb`, `EnsureCharlyInVenue`, `CharlyVersion`, …) + the 3 `ov_*` Go
files renamed (`charly_install.go`, `evalrun_charly_verbs.go`,
`migrate_charly_cachyos.go`); `shell_profile.go` now writes `charly.fish` with
`# charly:` markers; brand prose `overthink`/`Overthink` → `opencharly`/
`OpenCharly`; project domains/emails → `opencharly.ai` / `atrawog@opencharly.ai`
across `pkg/debian/control`, `pkg/fedora/opencharly.spec`. **KEPT load-bearing
on `overthinkos`** (a separately-gated future org move): the `go.mod` module
path, the 12 `.gitmodules` URLs, `ghcr.io/overthinkos`, `DefaultProjectRepo`,
every `@github.com/overthinkos/*` pin, and the release-download URLs — renaming
these now would break resolution against remotes that do not yet exist.

**Vocabulary is now fully YAML-driven (no hardcoded `{rpm,deb,pac,aur}` in Go).**
`build.yml` `format:`/`distro:` declarations are the single source of truth:
`FormatDef.Secondary` marks `aur` secondary to `pac` (config-driven
`PrimaryFormat()`, no `name=="aur"` special case); `RegisterBuildVocabulary(dc)`
derives the distro/format vocabulary from the loaded `DistroConfig` at load
time; the layer parser (`derivePackageSectionsFromCalamares`) is now
**purely structural** — it consults no Go vocab list and defers correctness to
the validator. Adding a new package format or distro is now a `build.yml` edit,
not a code change.

**`cachyos` ← `arch` package inheritance** via `build.yml`
`inherit_packages: true` (`expandPackageInheritance` expands `cachyos` to
`[cachyos, arch]`). The inheritance is **asymmetric** by design — `ubuntu` does
NOT inherit `debian` (the Debian/Ubuntu split keeps them independent), but Arch
and CachyOS share one package surface.

**`migrate_calamares.go` DELETED** and replaced by the YAML-driven mechanisms
above; the `calamares` migration step was dropped from the registry. A new
`migrate_charly_cutover4` step (`CH_*`/`OV_*` → `CHARLY_*`, the first-ever
credential-prefix re-key, host shell-profile + per-host deploy/config re-key)
was appended and `LatestSchemaVersion()` bumped to **`2026.159.1912`** (the
`calver-schema` stamp stays last; HEAD == `LatestSchemaVersion()` enforced).

**Four rebrand regressions surfaced ONLY by live beds** (a renamed CLI verb with
a stale internal caller or shell-string — invisible to `go test`, which even
asserted the stale value in one case): `vm cp-image` → `cp-box`, `eval image` →
`eval box` (`unified_targets_pod.go` real-invocation path, now guarded by
`TestPodUnifiedTarget_Rebuild_RealInvocations`), `deploy from-image` →
`from-box`, and the MCP server's `box.yml` → `charly.yml` project probe
(`bootstrapProject` made non-fatal so project-free MCP tools still serve when the
default-repo fetch fails).

**`network.go` upstream-DNS pin.** Rootless-podman containers on a
systemd-resolved host inherited the `127.0.0.53` stub resolver and hung on
external DNS; `ensureNetworkUpstreamDNS` now reads the host's real upstream from
`/run/systemd/resolve/resolv.conf` and injects it via
`podman network update charly --dns-add`.

**Coordinated cross-repo landing.** Because each `image/*` submodule consumes the
main repo's centralized `build.yml` + candies via `@github`, and the main repo
in turn consumes `cachyos`/`nvidia`/`selkies`, the rebrand is a mutual-dependency
cycle: an image submodule pinned to a *pre-rebrand* main fails the new `^charly/`
credential validator (`candy/immich: key "ov/api-key/immich"`). Every
`@github.com/overthinkos/{overthink,cachyos,nvidia,selkies}` pin across all repos
was therefore re-aligned to the single coordinated tag `v2026.160.0856`, and
every `charly.yml`-bearing repo (main + 8 images) tagged at it. `plugins` and
`pkg/*` carry no `charly.yml` and are tag-exempt.

*Assisted-by: Claude (fully tested and validated)*

### 2026-06-08 — fix(vm-deploy): builder-cfg threading, dead cloud-init url removal, nested target:local deploy, ledger host-path rebrand

Four VM-deploy/eval fixes surfaced (and R10-proven) by the `image/arch`
`eval-arch-vm` bed (PASS 13/13 incl. the fresh-rebuild re-verification of the VM
AND both nested children):

1. **Builder cfg threading.** `deploy_target_vm.go execHomeArtifactBuilder` did
   not pass `Cfg`/`ProjectDir` to `BuilderRun`, so `EnsureImagePresent` got
   `cfg=nil` and rejected any namespace/short builder ref ("requires a project
   directory with charly.yml"). Every VM bed with an npm/pixi/cargo `add_candy`
   layer + a namespace builder ref broke. Now threads `t.Cfg`/`t.ProjectDir`
   (mirrors `buildDepPkgsOnHost`), so `install_opts.builder_image:
   charly.arch-builder` resolves newest-local / builds on-demand instead of a
   pinned ghcr tag that goes stale.

2. **Dead cloud-init `url` ov-install strategy DELETED.** The `url` strategy
   curled a raw `charly-linux-${ARCH}` release binary that the localpkg cutover
   removed — `OvBinaryURL` was never even set, so the path was already dead.
   Removed the strategy end-to-end: the `composeRunCmd` curl-runcmd +
   `OvBinaryURL`/`OvBinaryChecksum` runtime params (`cloud_init_render.go`), the
   `OvInstallURL` const + dispatch case (`ov_install.go`), the
   `VmOvInstall.URL`/`Checksum` fields (`cloud_init_types.go`), the validator
   branch (`libvirt_validate.go`), the runtime-param setters (`vm_cloud_image.go`),
   and the dead `VerifyOvBinaryChecksum` (`http_fetch.go`). Strategies are now
   `auto`/`scp`/`skip`; charly is delivered post-boot by VmDeployTarget only.

3. **Nested `target:local` children deploy in VM beds.** `eval_bed_run.go`'s VM
   branch deployed the VM node's own layers + nested `target:pod` children
   (`deployNestedPodsInGuest`) but never the nested `target:local` children — yet
   `evalLiveTree` evaluated them, so they failed (un-deployed). The VM branch now
   deploys each nested local child via the dotted-path dispatch (`charly deploy
   add <bed>.<child>` → NestedExecutor → LocalDeployTarget over SSH), so a
   host-overlay child (direnv) and a `target: local` layer bed (tailscale) apply
   into the guest FS.

4. **Rebrand completeness — runtime host paths.** The runtime still wrote the
   guest ledger + env.d to `~/.config/overthink/{installed,env.d}/` — the
   `~/.config/overthink → ~/.config/opencharly` rebrand (which `charly migrate`
   already performs) had been missed in `install_ledger.go`, `deploy_target_vm.go`
   (`ensureGuestLedgerDirs`), `shell_profile.go` (`EnvdDir`), `install_plan.go`,
   `unified_targets_vm.go`, and the relevant comments + test fixtures. Now writes
   `~/.config/opencharly/…`, matching the migrate and the deploy-scope eval probes.

### 2026-06-08 — feat(localpkg): image builds install the PUBLISHED OS package on every distro + fix the pac release CI

The `charly` toolchain candy (and any future `localpkg:` candy) now installs as a
PROPER, dependency-resolving, OS-tracked package in IMAGE builds, not a curl'd raw
binary — matching what the deploy-time `LocalPkgInstallStep` already did on
`target: local` / `target: vm`. An image build has no host package-build step, so
it DOWNLOADS the published release asset
(`build.yml format.<fmt>.local_pkg.download_template` →
`releases/latest/download/opencharly-${ARCH}.{pkg.tar.zst,rpm,deb}`, `${ARCH}`
resolved by BuildKit) and installs it through the SAME dep-resolving install
command the deploy path uses (`pacman -U` / `dnf install` / `apt-get install`),
so the package's repo dependencies come in for free. ONE shared emitter,
`renderLocalPkgImageRun` (`charly/localpkg.go`), is called by BOTH the IR
`OCITarget` (pod-overlay synthesis) AND `generate.go`'s `writeLayerSteps` (the
`charly box build`/`generate` path) — the image-build localpkg emission has a
single home (R3). `LocalPkgDef` gains the `download_template` field
(`charly/format_config.go`); a format that declares none falls back to the layer's
own `task:` install (the helper returns no directive). The `charly` candy's
explicit curl/`task:` install block is DELETED — the `localpkg:` mechanism is now
the single install path across pac/rpm/deb.

Alongside, the `pac` job of `release-packages.yml` is fixed. It had been failing
with `fatal: invalid reference: origin/HEAD`: on a tag push `actions/checkout`
leaves the repo in DETACHED HEAD with no local branch, so makepkg's
`git+file://$(realpath …/../..)` source (`pkg/arch/PKGBUILD`) clones a repo whose
`origin/HEAD` is unresolvable and the working-copy checkout dies. Reproduced and
fixed locally by putting HEAD back on a branch (`git checkout -B release-build`)
before the makepkg build — rpm/deb don't use makepkg's git source, so only `pac`
needs it. The workflow also now emits stable-named asset copies
(`opencharly-amd64.{pkg.tar.zst,rpm,deb}`, excluding the `-debug` split packages)
that the `download_template` fetches, and drops the superseded raw-binary
(`charly-linux-<arch>`) job — the package IS the distributed artifact.

Fix-forward (caught by the consuming-image R10, `eval-charly-selftest-pod`): the
install-path move from `/usr/local/bin/charly` (old curl path) to `/usr/bin/charly`
(package path) had a blast radius beyond the candy itself. (1) The `charly-binary`
eval check (`candy/charly/candy.yml`) asserted `command -v charly` ∈
`/(usr/bin|usr/local/bin)/charly`, but on a usr-merged distro (Arch/CachyOS,
`/usr/sbin → bin`, `/usr/sbin` ahead of `/usr/bin` in PATH) `command -v` reports
`/usr/sbin/charly` — the same inode — so the regex was widened to
`/usr/(local/)?s?bin/charly` (RE2-verified, no `$` anchor that would miss the
trailing newline). (2) The `charly-mcp` service execed the now-absent
`/usr/local/bin/charly mcp serve` and went `FATAL`, breaking the MCP endpoint →
fixed to `/usr/bin/charly`. (3) The same stale path in four `box.yml` image checks
(filebrowser / openclaw-desktop / openwebui / composition-source) and two
`eval.yml` description-prose lines was swept to `/usr/bin/charly` (R3); the
`eval-charly-vm` localpkg check `eval.yml` also had a stale `command -v ov`
(rebrand leftover, R5) + a non-usr-merge-robust `=` test → fixed to
`test "$(command -v charly)" -ef /usr/bin/charly`. All three packages install to
`/usr/bin/charly` (pac/rpm/deb verified). Validated end-to-end: `eval-charly-selftest-pod`
(pac, full 10-step bed incl fresh rebuild, eval-live 50/0 with MCP green),
`composition-source` build+eval-box (rpm download+install, 13/0), and
`eval-charly-vm` (vm-deploy localpkg, 6/6).

### 2026-06-08 — docs(git-workflow): harden against silently-dropped submodule pointer bumps

Incident: landing Cutover 3d, the `git switch -c feat/main-doc-rebrand-3d` step
re-materialized the `plugins` submodule at `main`'s recorded gitlink (8254095),
silently discarding the **unstaged** working-tree bump to `f108f77` (the pushed
Cutover 3c commit). The subsequent `git add plugins; git commit` staged nothing
(the working tree had been reset), so the 3d commit `0d2b58d` shipped WITHOUT the
pointer bump — the superproject still referenced the pre-3c plugins. Caught by the
post-commit `git ls-tree HEAD plugins` check and fixed by a follow-up
pointer-bump commit (`d27316d`, never a force-push). The `/charly-internals:git-workflow`
B2 section now codifies the safeguard: bump the submodule pointer AFTER the branch
switch, `git add` it, and VERIFY with `git diff --cached --submodule=short` before
committing + `git ls-tree HEAD <sub>` after.

### 2026-06-08 — fix!: rebrand the `Description=Overthink` quadlet/systemd contract → `OpenCharly`

A Cutover-1 functional miss: `charly` both EMITTED and PARSED
`Description=Overthink <image>` in the systemd/quadlet units it manages. The
emitters (`quadlet.go`, `quadlet_pod.go`, `vm.go`), the `status_collector.go`
parser (which identifies charly-managed quadlets by the `Description=Overthink `
prefix), the `main.go` kong description, a `migrate_field_singular.go` comment,
and the `quadlet_test.go`/`status_quadlet_test.go` fixtures all carried the old
brand. Rebranded to `OpenCharly` in lockstep (emitter ↔ parser ↔ tests), so the
contract stays internally consistent. Existing deployments keep their old
`Description=Overthink` line until their next `charly update` (hard cutover —
acceptable on disposable targets). R10: `charly eval run eval-pod` → PASS
(steps=10) — the deploy emits `Description=OpenCharly`, and the bed's
`status-shows-pod` check (`charly status --json`) parses it via the rebuilt
collector, exercising both sides of the contract on a fresh rebuild.

### 2026-06-08 — feat!: entity-independent ov→charly doc rebrand — plugins (Cutover 3c) + main core-docs (Cutover 3d)

Completed the **entity-INDEPENDENT** half of the documentation rebrand across the
`plugins` submodule and the main core docs. "Entity-independent" = everything that
depends only on the already-renamed CODE (the `charly` binary, the `charly-mcp` /
`charly-enc` layers/scopes, the `CH_` env contract, the `charly-*` plugin
namespace, the six `charly` CLI-verb skills). Done as a context-specific sweep
(never a bare `\bov\b`), so entity names, Go identifiers, the `ov` import
namespace, and `ov/secret`/`ov/api-key` keys were provably untouched.

- **Cutover 3c — plugins** (`overthink-plugins` `8254095..f108f77`, tag-exempt).
  Renamed the six CLI-verb skills (`ov-config`/`ov-version`/`ov-status`/`ov-doctor`/
  `ov-update`/`ov-mcp-cmd` → `charly-*`): dir + frontmatter + all cross-refs.
  Swept `OV_*`→`CH_*` (keeping the `OV_ROOT`/`OV_USER` heredoc sentinels),
  backtick binary refs (`` `ov` ``→`` `charly` ``, keeping the `` `ov` `` import-namespace
  refs), host paths (`~/.{config,cache,local/share}/ov`→`charly`), project name
  (`Overthink`→`OpenCharly`), the `charly-mcp`/`charly-enc` prefixes, the plugin
  namespace names (`ov-<plugin>`→`charly-<plugin>`, collision-safe), and the
  marketplace.json/plugin.json descriptions.
- **Cutover 3d — main core docs.** `CLAUDE.md` + `README.md` prose (`` `ov` ``→
  `` `charly` ``, `Overthink`→`OpenCharly`, the README cross-refs to the renamed
  verb skills), `eval.yml` score-prompt prose, `candy/charly/candy.yml`
  (`/charly-tools:ov`→`/charly-tools:charly`), `candy/qemu-guest-agent` comments.
  `VISION.md` was already brand-clean.

**Deferred to Cutover 4** (the submodule rebrand, where the authoritative
entity-rename map makes keep-vs-change unambiguous — NOT a split, a correct scope
assignment): the `ov-cachyos`/`arch-ov`/`fedora-ov` entities + their skills, the
`ov` import namespace (`ov.X` / `{ov:`), runtime-prefix examples for
submodule-defined images/deploys, the `ov-sdk-complete`/`ov-autostart` sentinel
filenames (pending code verification), and the `image/*/README.md` "Overthink"
banners. **Also surfaced** (a Cutover-1 functional miss, its own R10-gated Go
cutover): the `Description=Overthink <image>` quadlet/systemd emitter↔parser
contract (`charly/quadlet*.go`, `status_collector.go`, `vm.go`, tests) + the
`main.go` kong description.

### 2026-06-08 — fix: R10-harden the main-repo rebrand (charly-layer eval + stale-shadow cleanup)

Two defects surfaced and fixed while running the proper `charly eval run`
bed R10 on the landed main-repo rebrand (Cutover 1). Each landed as its own
atomic, R10-proven cutover.

- **`charly`-layer eval check** (`candy/charly/candy.yml`, commit `106f768`,
  tag `v2026.159.0513`). Cutover 1's sweep left the layer's `charly-binary`
  eval check asserting the binary path matched `/(usr/bin|usr/local/bin)/ov` —
  a regex that can never match `command -v charly`, so every image baking the
  `charly` layer failed its layer-eval. Fixed the regex to `/charly`, swept the
  remaining `ov` CLI references in the layer description + comments, and bumped
  the layer `version:`. The `EnsureOvInVenue` Go identifier and the
  `/charly-tools:ov` skill ref are intentionally retained (they belong to the
  plugins rename). R10: `charly eval run eval-charly-selftest-pod` → PASS
  (steps=10).

- **Stale-shadow cleanup on package install** (`taskfiles/Build.yml`, commit
  `c69d5d3`, tag `v2026.159.0514`). On Arch, `task build:charly` installs the
  canonical `/usr/bin/charly` via the `opencharly-git` package but had never
  removed a leftover *portable* `charly` in a higher-priority `$PATH` dir
  (`~/.local/bin`, `/usr/local/bin`, …). The stale shadow won on `$PATH`, so the
  `os.Executable` freshness guard (`charly/main_freshness.go`, behaving
  correctly) kept tripping on it — while `task build:charly` refreshed only
  `/usr/bin/charly`, never the shadow, leaving an unbreakable "rebuilt-but-still-
  stale" loop. The package-install path now mirrors its existing legacy-package
  cleanup: it removes every superseded, non-package-owned `charly` shadow in the
  standard pre-`/usr/bin` `$PATH` dirs so the package binary is authoritative.
  R10: `task build:charly` reduced PATH to a single canonical fresh
  `/usr/bin/charly`; `charly eval run eval-local` → PASS (steps=4) against it
  (with `eval-pod` PASS steps=10 + `eval-charly-selftest-pod` PASS steps=10 the
  same session).

### 2026-06-08 — feat!: rebrand `ov`→`charly`, `overthink`→`opencharly`, OCI labels → `ai.opencharly.*` (main repo, Cutover 1)

The CLI binary and the project name were rebranded. This is **Cutover 1 of a
multi-repo rebrand**, scoped (operator decision) to the **main repo only**; the
`plugins`, `pkg/{arch,fedora,debian}`, and 8 `image/*` submodules rebrand as
their own subsequent R10'd cutovers (producer-first landing order). The
`overthinkos` GitHub org, the `ghcr.io/overthinkos` registry, the repo names,
the `.gitmodules` URLs, `DefaultProjectRepo`, and every `@github.com/overthinkos/overthink`
cross-repo ref are **kept** (infrastructure unchanged); only the `/ov` trailing
segment of the Go module path changed.

Renames landed:

- **Binary / module / source dir**: `ov`→`charly`; `ov/`→`charly/`; module
  `github.com/overthinkos/overthink/ov` → `.../charly`; `bin/ov`→`bin/charly`;
  `candy/ov`→`candy/charly`, `candy/ov-mcp`→`candy/charly-mcp`; `task build:ov`→`build:charly`.
- **Container/runtime identity**: the `ov-<name>` pod/container prefix → `charly-`;
  the shared `ov` podman network → `charly`; BuildKit cache-mount IDs `ov-…`→`charly-…`;
  podman machine name; dbus app name; host dirs `~/.config/ov`→`~/.config/charly`,
  `~/.cache/ov`→`~/.cache/charly`, `~/.local/share/ov`→`~/.local/share/charly`; the
  guest deploy ledger `~/.config/overthink`→`~/.config/opencharly`; log/error prefix `ov:`→`charly:`.
- **OCI labels**: the 44 `org.overthinkos.*` label constants → `ai.opencharly.*`
  (plus the inline `clean.go`/`deploy_target_pod.go` strings, the dynamic
  `ai.opencharly.service.<init>` key, and `build.yml` `label_key`s).
- **Env contract**: `OV_*`→`CH_*` (the nine heredoc-delimiter sentinels —
  `OV_ROOT/OV_USER/OV_DROPIN/OV_REPO/OV_WRITE/OV_UNIT/OV_SNIPPET/OV_NESTED_SCRIPT_EOF/OV_LEDGER_EOF`
  — preserved verbatim).
- **Config filename**: `overthink.yml`→`charly.yml` (operator-chosen, not `opencharly.yml`).

Schema cutover: a single idempotent `MigrateCharlyRebrand` step
(`charly migrate`, CalVer `2026.159.0002`, `LatestSchemaVersion()` bumped to
`2026.159.0003`) renames `overthink.yml`→`charly.yml`, rewrites
`@github…/candy/ov[-mcp]` layer-ref paths + the `ov` import-namespace alias
(key + qualified `ov.<member>` refs) → `charly`, rewrites `org.overthinkos.*`
label strings → `ai.opencharly.*` in project configs, and (host-gated) relocates
the per-host state dirs with `OV_*`→`CH_*` env rewrites, mutating the
`MigrateContext` pointer so the trailing `calver-schema` stamp lands on the
relocated files. Historical migration steps keep their `overthink.yml` /
`org.overthinkos` literals (replay correctness); `MigrateUnified` /
`MigrateDiscoverFlatten` were corrected to use the `overthink.yml` literal
rather than the (now-renamed) `UnifiedFileName` const. Remote-cache
auto-migration (`EnsureRepoDownloaded`) now migrates on cache HIT as well as
fresh clone — but only when the cache is behind HEAD — so a pre-rebrand cache
(`~/.cache/ov` relocated to `~/.cache/charly`, still holding `overthink.yml`)
is brought to `charly.yml` on access without re-migrating already-current caches.

Deliberately **deferred to Cutover 2** (coupled to the plugins submodule): the
`/ov-<plugin>:<skill>` skill-dispatcher references and the `ov-*@ov-plugins`
marketplace/settings entries — they track the not-yet-renamed plugins.

R10: full `charly eval run` on three disposable beds — `eval-pod` (build +
`ai.opencharly.*` label round-trip + pod deploy + fresh rebuild + ADD
feature-run), `eval-local` (host-ledger relocation + local deploy + fresh
rebuild), `eval-jupyter-pod` (in-container `charly` CLI + jupyter MCP, 37 checks
+ fresh rebuild) — all PASS, plus `go test ./...` green and `charly box validate`
at 0 errors / 0 warnings.

### 2026-06-06 — fix(ci): run release-packages on the self-hosted CachyOS runner; drop the dead build.yml

Every push to `overthinkos/opencharly` was red on the Actions page. Two causes, both fixed in one cutover, after the runner migration above landed:

- **`.github/workflows/build.yml` deleted.** It was a fully-commented deprecated stub (image publishing had moved to `task push` on 2026-04-27) with no `name:`/`on:`/`jobs:`, so GitHub rejected it as an invalid workflow on EVERY push — a 0s red X each time. Removed (R5: no stale references remain — every surviving `build.yml` mention names the unrelated `ov` `build.yml` config file).
- **`.github/workflows/release-packages.yml` retargeted onto the self-hosted CachyOS runner** (`runs-on: [self-hosted, opencharly]`, both jobs). Previously the `pac` job ran in a bare `archlinux:latest` container on `ubuntu-latest`, where `bin/charly box pkg pac` → the global `makepkg -sf` (build.yml `local_pkg.build_template`) tried to `sudo pacman -S` the PKGBUILD `depends=` and died (`sudo: a password is required`, exit 8); `rpm-deb` already passed there. On the self-hosted runner `pac` now builds NATIVELY — makepkg as uid 1000 (makepkg refuses root; the rootless runner's non-root uid is exactly right), no archlinux container — and `rpm`/`deb` build distro-natively in the runner's rootless nested podman. `actions/setup-go` was dropped (the runner's system `go 1.26.4` ≥ go.mod's `1.26.0`); JS actions bumped to node24 (`actions/checkout@v5`, `softprops/action-gh-release@v3`), clearing the Node-20 deprecation + the `go.sum`-cache-path annotation. A `workflow_dispatch` trigger was added with each upload step tag-guarded (`if: startsWith(github.ref, 'refs/tags/v')`), so the build jobs run from a branch without minting a tag (the pre-landing live test).
- **Runner completed as a full charly host.** `makepkg -sf`'s `-s` only shells out to `sudo pacman` for MISSING deps; the runner image had four of the `ov` PKGBUILD `depends=` absent — the part the ov/virtualization layers don't install. Added `slirp4netns` (rootless podman networking), `libisoburn` (xorriso seed-ISO builder), `cdrtools` (genisoimage/mkisofs fallback), `swtpm` (software TPM 2.0) to the `github-runner` candy (`distro.arch.package`; `version` → `2026.157.1917`), so every dep is pre-installed and makepkg never touches sudo. A deterministic `eval:` check (`gr-pkgbuild-dep-*`, `package:/installed:`) asserts the four are present — fails without the candy change. The stale `zlib` entry in the github-runner skill's package list was corrected in passing (the candy correctly omits it — CachyOS's `zlib-ng-compat` Provides zlib, so an explicit `zlib` is an unresolvable conflict).

The two design decisions — move BOTH jobs to the runner; complete the runner as an charly host rather than grant passwordless sudo or add an `ov` flag — were confirmed with the operator. Verified: `go test ./...` + `charly box validate` clean; the disposable `eval-githubrunner-pod` bed green (image + rootless + nested podman + the four deps); the retargeted workflow ran green on the live self-hosted runner via `workflow_dispatch` (build jobs, upload skipped) BEFORE landing; and the landing-tag run built + attached the package artifacts to the release. The stale-published-snapshot load warnings (`field image not found in type main.DeploymentNode`, …) seen during `charly box pkg`'s full-graph config load are pre-existing schema drift in the published submodule repos (selkies/cachyos/nvidia transitive pins), independent of this CI change and addressed in a separate reconcile cutover.

### 2026-06-06 — feat: migrate the github runner to CachyOS, fully rootless

The `githubrunner` image moved from Fedora to **CachyOS** (`base: cachyos.cachyos`,
`build: [pac]`) and from a root / `privileged: true` container to a **genuinely
rootless** one (uid 1000, zero added capabilities) — aligning the image with what
its skill docs already claimed (`uid=1000, no cap_add`) but the layer had drifted
away from. It is also fully modernized to current best practices.

- **Rootless via `container-nesting`** (now composed directly): `box.yml` carries
  no uid/user/privileged override, so the image resolves to `cap_add:[]`,
  `security_opt:[unmask=/proc/*]`, devices `/dev/fuse` + `/dev/net/tun`. CI jobs
  get rootless nested podman/buildah/skopeo at uid 1000.
- **Arch packages** replace the Fedora set; `golang→go`, `cloud-utils-growpart→
  cloud-guest-utils`, `qemu-user-static` + `qemu-user-static-binfmt` (aarch64
  cross-arch CI — parity with the old `qemu-user-static-aarch64`), `cosign` as a
  repo package (was a binary download). The actions-runner's .NET native deps are
  declared explicitly (`icu`/`krb5`/`openssl`/`libunwind`/`lttng-ust`) since its
  bundled `installdependencies.sh` has no Arch branch. `libz` is intentionally NOT
  listed — CachyOS ships `zlib-ng-compat` which Provides zlib, and an explicit
  `zlib` is an unresolvable conflict (caught on a live build — RDD).
- **Declarative `task:`** replaced the build-time shell script: the runner tarball
  is a pinned `download:` (v2.334.0) to `${HOME}/actions-runner`, root-extracted
  (the shared download cache is root-owned) then `chown -R`ed to uid 1000 so the
  rootless runner can write `_work`/`.credentials`. The duplicate
  skopeo/podman/buildah/libcap, the subuid/subgid + setcap tasks, and
  `RUNNER_ALLOW_RUNASROOT` were dropped (provided by `container-nesting` or
  unneeded rootless).
- **Token mechanism (credential-backed):** `RUNNER_TOKEN` is a `secret_accept`
  (never plaintext in deploy.yml/quadlet); `RUNNER_ORG` an `env_accept`; the
  `post_enable`/`pre_remove` hooks skip when the token is empty (so a token-less
  deploy — an eval bed — comes up without registering). A new generic
  `resolveHookSecretEnv` (`ov/secrets.go`) delivers credential-backed secrets to
  lifecycle hooks **explicitly** via `podman exec -e` at the `post_enable` /
  `pre_remove` call sites — the value is scrubbed from `c.Env` (never plaintext)
  and a podman `type=env` secret is not reliably inherited by `podman exec`, so
  the hook would otherwise never see it. Benefits every hook+secret layer (R3).
- **New `eval-githubrunner-pod` R10 bed** proves the rootless composition WITHOUT
  GitHub registration. Verified: `charly box build` → `charly eval box` (42 passed/0
  failed) → `charly eval run eval-githubrunner-pod` PASS (10 steps; the deploy-scope
  `eval-live` 50 passed/0 failed includes `id`→uid 1000 and a real rootless nested
  `podman run --rm quay.io/libpod/alpine:latest true`).

The live single-instance registration with the `overthinkos` org is the operator's
step (it needs a `gh` token with `admin:org` to mint a registration token):
`charly config githubrunner -e RUNNER_ORG=overthinkos -e RUNNER_TOKEN=$(gh api -X POST
/orgs/overthinkos/actions/runners/registration-token --jq .token)` then
`charly start githubrunner`.

### 2026-06-06 — fix: import-namespace mutual-import cycle-break by repo identity

`ov`'s unified-config loader broke the intentional main↔cachyos (and
↔nvidia/↔selkies) mutual import only when every pin in the loop converged on the
same version. A divergent transitive `ov:` back-reference — e.g. local main →
`cachyos@v2026.157.1600` → `ov:opencharly@v2026.157.0650` →
`cachyos@v2026.146.0754` — was treated as a foreign repo, fetched, and recursed
into; the older snapshot's pre-migration `discover:` **map** then failed to decode
(`cannot unmarshal !!map into DiscoverConfig`), making every cachyos-based image
(`githubrunner`, `versa`) unloadable with a current binary.

The cycle-break now keys by **repo identity, not pinned version** (`ov/ns_identity.go`):
`loadNamespaceCached` checks a stack-scoped `loadingRepos` (repoID → in-progress
node) BEFORE any fetch, alongside the version-keyed `nsCache` diamond memo;
`LoadUnified` registers the local root under its repo identity (a new optional
`repo:` field in `charly.yml`, else inferred from `git remote origin`). So a
transitive import of an in-progress repo — above all the root's own repo —
resolves to the in-progress node instead of fetching a divergent (possibly
stale-schema) snapshot: **the importing project's namespace pins win**. The stale
`@github` import pins (cachyos/nvidia/selkies) were also bumped to
`v2026.157.1600` to match the submodules. (The fatal load additionally required
purging several corrupt locally-cached `@opencharly:*` clones that were missing
their `candy/` dirs — environmental, no repo change.)

Covered by `TestImportNamespace_DivergentVersionMutualCycle` + `TestNsRepoIdentity`.
Verified: `go test ./...` green; `charly box validate` exit 0 with zero warnings (was
a fatal load error).

### 2026-06-06 — docs: rebrand the top-level docs to "The Candy Factory" (VISION wording)

A documentation-only cutover that finished carrying the candy-factory voice and
glossary from `VISION.md` into the top-level docs. The candy/box *machinery* had
already landed in prior cutovers — the schema kinds `kind: candy` / `kind: box`,
the `charly candy` / `charly box` verbs, the `candy.yml` / `box.yml` files — but the
human-facing prose still led with the old tagline *"The Container Management
Experience for You and Your Agents"* and still called the authoring entities
"layers" and "images" where the schema and VISION already said candies and boxes.

**The new tagline** is **"The Candy Factory for You and Your Agents"** — a
parallel swap of the old one that keeps the "for you and your agents" audience
phrase. It now leads `CLAUDE.md` (title + intro), `README.md` (subtitle + intro),
`plugins/README.md`, and `pkg/arch/README.md`. After this cutover
`grep -rin "container management" --include=*.md` returns zero live hits
(CHANGELOG context excepted).

**The rebrand rule (VISION's own discipline): glossary for the concept, literal
for the artifact.** The Overthink-level *authoring* nouns were rebranded — a
**layer** is a **candy** (`kind: candy`), an **image** is a **box** (`kind: box`),
the secured disposable runtime is a **candybox**, a skill is a **recipe card** —
while the *technical-artifact* terms were deliberately kept literal exactly where
VISION keeps them: "container(s)" as the OCI/podman runtime substrate, "image" as
the OCI artifact / OCI label / Containerfile output / base / multi-stage build,
every `kind:` key, command name, flag, file path, OCI-label name, the
`ai.opencharly.eval → {layer, image, deploy}` label-section keys, and every
skill ID (`/ov-image:layer`, `/ov-image:image`). This was a per-occurrence
semantic pass, never a blind replace.

**Scope — top-level docs only.** `README.md` and `CLAUDE.md` got the full
voice + glossary pass; `plugins/README.md` and `pkg/arch/README.md` got their
tagline/framing lines aligned (light touch). In `CLAUDE.md` the branding (title +
intro), the Candyboxing pillar, the dispatcher trigger descriptions, the RDD/ADD
pillar entity nouns, the Key-Rules entity-naming, and the "Where things are
documented" list were rebranded; the normative R1–R10 rule bodies and all
OCI/Containerfile/command mechanics were left byte-for-meaning intact (verified
by an adversarial meaning-preservation audit). Two stale stragglers surfaced in
`README.md`'s Quickstart were fixed in the same change (R2): `charly image build` →
`charly box build`, and the `# the kind:image` comment → `# the kind:box`.

**Out of scope (declined tiers):** the conceptual prose of the 297 recipe-card
skills (most of their ~6,800 "layer"/"image" mentions are literally-correct
OCI/Containerfile terms) and renaming the leftover `/ov-image:layer` /
`/ov-image:image` skill IDs (a 710-cross-reference identifier cutover). The
dispatcher therefore still points "candy authoring" at the `/ov-image:layer`
skill ID — an accepted boundary of this tier, not a stale reference (the skill ID
is an opaque identifier, not prose).

### 2026-06-06 — docs: restructure README + CLAUDE.md, de-dup within/across, enforce the five-way doc split, fix skill refs

A documentation-only cutover that made `README.md` and `CLAUDE.md` obey the
five-way doc-role split (rules → CLAUDE.md, features/commands → README.md,
usage/architecture → skills, history → CHANGELOG.md, thesis → VISION.md) that
both `VISION.md`'s footer and CLAUDE.md's "Where things are documented" already
codified but neither top-level doc fully honored.

**CLAUDE.md (786 → 433 lines), heading-preserving full restructure.** Reordered
into a clean arc — R0 (skills first) → the philosophy pillars (Candyboxing /
RDD / ADD / Prioritize Clean Architecture, in VISION's order, as the *why*) →
the Ground Truth Rules R1–R10 with the Disposable-Only Autonomy + R10 block
placed immediately after R1–R9 so the ten rules read together → the cutover +
post-execution process → a Key Rules technical index → AI Attribution → Where
things are documented. De-duplication removed the redundant *restatements*
without changing any rule's normative meaning: the triple-stated R3/R4/R5 (Ground
Truth Rules + "Prioritize Clean Architecture" sub-paragraphs + cutover
anti-patterns) collapsed to one canonical statement each plus pointers; the two
near-identical gate checklists ("End-of-turn" + "post-execution") merged into one
**Acceptance checklist** (the union of every distinct check, grouped
verify / acceptance / land); the "Agents, Workflows & Teams", "Hard Cutover by
Default", and "Post-Execution Policies" sections trimmed to their mandate plus a
pointer to the skill that already owns the operational detail
(`/ov-internals:agents`, `/ov-internals:cutover-policy`,
`/ov-internals:git-workflow`, `/ov-internals:strict-policy`); the "Key Rules"
mega-list split so the entries that duplicated a dedicated section became one-line
pointers while the genuinely-unique technical rules stayed in full. R0 and R10
emphasis was left intact, and every section name that skills/README quote
(`Candyboxing`, `Risk Driven Development (RDD)`, `Agent Driven Development (ADD)`,
`AI Attribution`, `Post-Execution Policies`, `Hard Cutover by Default`, `Ground
Truth Rules`, `Prioritize Clean Architecture Above All Else`, `Where things are
documented`, `Key Rules`, `Agents, Workflows & Teams`) plus the quoted Key-Rules
phrases (`Init-system polymorphism via mixed service: entries`, `Cross-kind name
reuse is permitted and encouraged`, `Deploy fetches NOTHING speculative`) and the
three "Prioritize Clean Architecture" sub-labels were preserved verbatim so no
skill cross-reference broke.

**README.md (850 → 833 lines).** The "Core concepts" Candyboxing / RDD / ADD
blurbs were trimmed to crisp what-you-get + which-command/kind feature
descriptions that link out (`→ VISION.md` for the why, `CLAUDE.md "<section>"`
for the rule, the relevant skill for usage) instead of re-narrating VISION's
thesis prose. "Why Overthink?" stopped re-explaining candyboxing and
re-describing the eval/agents surface (those link to the Candyboxing concept,
[Evaluate], and [Works with Claude Code]). The footer doc-map was aligned to the
five-way split.

**Skill-reference bugs fixed.** A 297-skill cross-check of every `/ov-…:…`
reference in both files surfaced two broken refs in README, both fixed:
`/ov-eval:eval-k8s` → `/ov-kubernetes:eval-k8s` (the form CLAUDE.md already
used) and `/ov-tools:ov-mcp` → `/ov-coder:ov-mcp` (the nested charly MCP server
skill), each in two places.

### 2026-06-06 — feat: generic + deterministic Debian/Ubuntu (and all distro+version) package/repo resolution — the distro-specificity cascade

A `candy.yml` layer declared packages per distro under the `distro:` map. The
post-parse bridge `derivePackageSectionsFromCalamares` mapped **bare** distro
keys to a shared package-FORMAT section (`debian`→deb, `ubuntu`→deb,
`fedora`→rpm, `arch`→pac). Because Debian AND Ubuntu both fed the single `deb`
format section, three defects followed:

1. **Non-deterministic repo (the trigger).** `mergeRaw` was first-writer-wins
   over Go's randomized map iteration, so when debian and ubuntu declared
   *different* repos (`candy/ov`'s tailscale: `…/debian trixie` vs `…/ubuntu
   noble`) the emitted `.list` suite was random run-to-run — a Debian deploy
   could land the `noble` repo. Reachable on real VM deploys.
2. **Package cross-contamination.** `addPackages` UNIONED debian's and ubuntu's
   package lists into the one `deb` section, so genuinely different package
   *names* per distro were unexpressible.
3. **VM deploys couldn't reach per-version sections.** `syntheticVmImage` set
   `img.Distro` to a single bare name, so a `target: vm` deploy never selected
   `ubuntu-24.04`.

Plus a documentation/implementation drift: `/ov-image:layer` documented
top-level `rpm:`/`deb:` package-format keys and top-level `debian:13:` /
`debian,ubuntu:` tag keys as canonical, but a census found **0 real uses** —
the real surface was the `distro:` map (106 layers).

**The cutover** makes the `distro:` map the SINGLE package surface and resolves
it via a most-specific-first CASCADE:

- **Parser** (`derivePackageSectionsFromCalamares`): every `distro:` key — bare
  (`debian`), versioned (`debian-13`→`debian:13`), or compound (`debian,ubuntu`
  split into one tag each) — routes to a per-distro/version TAG section. NO
  distro key feeds a shared format section anymore (each distro owns its own
  section, so two distros sharing a package format can never race — the
  determinism fix). The arch `aur:` sub-block keeps its dedicated `aur` build
  format. Top-level `package:` is recorded on `layer.topPackages` and folded at
  RESOLVE time (folding it at parse was the contamination source). Sorted
  iteration makes derive deterministic regardless of map order. `CandyYAML.
  UnmarshalYAML` is now typo-detection only — the top-level format/tag-key parse
  branches (and the `FormatSections`/`TagSections` fields) are deleted; a stray
  `rpm:`/`debian:13:` is a hard load error pointing at `charly migrate`.
- **Resolver — ONE shared `resolveCascadePackages`, build AND deploy.** The
  single cascade resolver walks `img.Distro` most-specific → least, plus the
  top-level base. Packages UNION (deduped); `repo`/`copr`/`option`/`exclude`/
  `module` resolve most-specific-wins; emits the image's primary format. Both
  `compileSystemPackageSteps` (deploy compiler) AND `generate.go`'s
  Containerfile emitter (image build) call it — so build and deploy can NEVER
  diverge. **This corrected a latent split the cutover surfaced:** the image-BUILD
  path had its OWN duplicate "Phase 1 first-match-STOP + Phase 2 format-section"
  resolution that read only `TagSection`/`FormatSection` and **silently dropped
  every layer's top-level `package:` list** (proven on a real generate: nodejs's
  `[nodejs, npm]` vanished from `fedora-builder`) AND used override (not union)
  semantics — so a build and a deploy of the same layer disagreed. The duplicate
  Phase1/Phase2 + the now-dead `renderFormatInstallFromPackages` are deleted (R3);
  the two-phase first-match-STOP is gone everywhere. fedora/arch reach their
  packages via the bare-distro tag.
- **Version chain parity:** `DistroDef.Version` (build.yml `distro.debian.
  version: "13"`, `ubuntu: "24.04"`, `fedora: "43"`; child-wins via
  `resolveInherits`) feeds the new `distroTagChain(distro, version)` helper, so a
  `target: vm` deploy synthesizes the same `[<distro>:<version>, <distro>]` chain
  an image build carries — per-version selection reachable on both. The ubuntu
  image chains dropped their hand-authored `debian` 3rd level (`image/ubuntu/
  box.yml`) to stay symmetric with `distroTagChain`'s `[ubuntu:24.04, ubuntu]` —
  safe because zero layers are debian-only (every deb-family layer carries an
  explicit `ubuntu` section), and cascade-union would otherwise re-add
  debian-only packages onto ubuntu.

**Cascade cannot SUBTRACT.** Because packages union across the chain, a specific
level cannot remove a package a broader level added — exclusions are expressed
structurally. `candy/dev-tools` kept `fastfetch` (absent from Ubuntu's repos)
only under `debian`+`fedora`+`arch`, never `ubuntu`; the redundant `ubuntu-24.04`
section + the `exclude_distro: [ubuntu:24.04]` eval were deleted (now
`exclude_distro: [ubuntu]`). `candy/gh`'s byte-identical debian+ubuntu repo
collapsed to one compound `debian,ubuntu:` section. `candy/nodejs`'s
`ubuntu-24.04` nodesource repo-only section was DELETED: reviving it (the
cascade now applies repo-only versioned sections that the old Phase-1-skip
dropped) installed nodesource's `nodejs` alongside Ubuntu's separately-listed
`npm`, yielding conflicting `node-*` deps and a broken ubuntu-coder build — the
consumer R10 (full ubuntu-coder build) caught it. Ubuntu's own compatible
nodejs+npm pair is used instead; the dead repo is gone (a deliberate NodeSource
upgrade would drop the separate npm, not fight the base package).

**Validation:** `validatePkgConfig` now reads the canonical `repo` key (the old
`repos`-plural check was dead for real layers), validates TAG sections (where
packages now live), and gates `repo`/`copr`/`module` on the whole-layer package
union (so the nodesource pattern — repo on one level, package on another — is
legal). The `use_packaged` service check uses `HasAnyPackages` (packages moved
from format → tag sections). The `charly candy add-rpm`/`add-deb`/`add-pac`/`add-aur`
editor commands were retargeted to write under the `distro:` map (`add-rpm`→
`distro.fedora`, `add-deb`→the shared `debian,ubuntu` compound, `add-pac`→
`distro.arch`, `add-aur`→`distro.arch.aur`) — they previously wrote the now-
rejected top-level form.

**No new migration step / no schema bump.** The existing `calamares` step
(2026.123.1351) already rewrites every legacy top-level form (format keys +
colon/compound tags) into the `distro:` map — RDD-verified — and `charly migrate`
runs the whole chain idempotently regardless of a config's current version, so a
new `distro-cascade` step would duplicate it (R3), and the authoring schema (the
`distro:` map) is unchanged, so a version bump (which would needlessly trip the
`@github` cache-migration trap and force every user to re-migrate) is not
warranted.

**Blocking issue surfaced + fixed (R2).** The deb VM beds initially FAILED — not
on the cascade (tailscale installed from `…/debian trixie` ✓) but on the deploy
LEDGER: `VmDeployTarget` wrote `~/.config/opencharly/installed/candy/<layer>.json`
while the mkdir created `installed/layers/`, so the write failed on every fresh
substrate. RCA: the box/candy rebrand sweep (commit c788cc7) over-rebranded the
ledger WRITE path `layers`→`candy` but left the mkdir + `DefaultLedgerPaths` +
the local reader at `layers` — violating that cutover's own "preserve the
on-disk `installed/layers` ledger path" decision; the rebrand's R10 never ran the
deb VM beds, so this cutover's R10 surfaced it. Fixed in `ov/install_ledger.go`
by single-sourcing the layers/deploys dirs in `AddLayerDeploymentVia` so the
write target and the mkdir can never diverge again (R3/R4), restoring the
canonical `installed/layers` path.

**Proof.** Go unit tests (`ov/distro_cascade_test.go`): parser routing, cascade
union + most-specific-wins, a 50-iteration determinism guard (debian→trixie,
ubuntu→noble under shuffled map order), `distroTagChain`, `DistroDef.Version`
inheritance, and the calamares migration safety net. Real `charly box generate`:
`image/debian` emits `…/debian trixie`, `image/ubuntu` emits `…/ubuntu noble` —
deterministic per-distro, both `charly box validate` clean (zero warnings). The
`eval-debian-debootstrap-vm` / `eval-ubuntu-debootstrap-vm` beds gained explicit
suite witnesses (`tailscale-repo-suite-debian-trixie` / `…-ubuntu-noble`).

### 2026-06-06 — docs: complete the box/candy rebrand sweep (stale `charly image`/`charly layer`/`image.yml`/`layer.yml`/`layers/` references)

The `image`→`box` / `layer`→`candy` rebrand had landed in the code + config
(`box.yml`, `candy/`, `charly box`/`charly candy`/`charly eval box` commands, the
`migrate_box_candy_rename` step) but left ~350 stale references in the plugins
skills, root docs, code comments, help strings, and config descriptions. This
sweep replaces the unambiguous ov-entity TOKENS — `charly image …`→`charly box …`
(incl. `list images`→`list boxes`, `list layers`→`list candies`,
`new image`→`new box`, `new layer`→`new candy`, `add-layer`→`add-candy`,
`rm-layer`→`rm-candy`), `charly layer …`→`charly candy …`, `charly eval image`→`charly eval box`,
the MCP tool names (`image.list.images`→`box.list.boxes`, `image.set`→`box.set`,
`layer.add-rpm`→`candy.add-rpm`, …), `image.yml`→`box.yml`, `layer.yml`→`candy.yml`,
`layers/`→`candy/`, and the legacy remote-ref subpath `/images/<n>`→`/box/<n>`.

DELIBERATELY PRESERVED (legitimate, not stale): the `ai.opencharly.image` OCI
label, the `ov/image.go` source filename, the deploy `image:` cross-ref field, the
`--add-layer` CLI flag, bare "image"/"layer" prose (OCI images are real), the
`migrate_*.go` migration code + the `/ov-build:migrate` skill (which narrate the
legacy→new rename), the `deploy_ref.go` legacy-compat classifier (accepts both
`images/`+`box/` and `layers/`+`candy/`), the runtime `installed/layers` ledger
path, and external cloud-image URLs (`pkgbuild.com/images/…`). The one code
behavior touch: `eval_clone.go` mirrored a nonexistent `layers/ov/bin` into score
clones — corrected to the canonical `candy/ov/bin` (the dir the 2026-06-06 localpkg
beds proved is the real on-disk ov-binary path). Verified: `go build`/`vet`/`test`
clean, `charly box validate` zero warnings, R5 grep clean across plugins + superproject.

### 2026-06-06 — feat: native OV packages for every supported distro (localpkg per-format map, uniform auto-resolve, rpm + deb)

`ov` shipped exactly one native package — the Arch `pkg/arch/PKGBUILD`
(`opencharly-git`), wired into deploys by the layer field `localpkg: pkg/arch`.
Fedora/Debian/Ubuntu fell back to a curl'd binary. This cutover makes `ov` a
first-class native package on **all five base distros**: arch + cachyos
(pacman, already present), **fedora (rpm, new)**, **debian + ubuntu (deb, new)**.

**Per-format `localpkg` map (schema change).** The layer `localpkg:` field went
from a single scalar (`pkg/arch`) to a per-format map
`{pac: pkg/arch, rpm: pkg/fedora, deb: pkg/debian}`, so ONE `ov` layer carries a
native-package SOURCE per distro format. `Layer.LocalPkg()` became
`LocalPkg(format)`; `compileLocalPkgStep` resolves the target distro's format
first, then picks the matching source. A new `localpkg-map` `charly migrate` step
rewrites the legacy scalar to `{pac: <old>}`; the loader hard-rejects a scalar
`localpkg:` (`LocalPkgMap.UnmarshalYAML`) with an `charly migrate` hint. Schema HEAD
bumped to `2026.157.311`.

**Uniform auto-resolve — the host-side dep-closure machinery is DELETED.** Every
format's install command is now the package manager's native auto-resolving
local-file install (`pacman -U` / `dnf install` / `apt-get install`), so the
package's mandatory dependencies are pulled from the target's repos. The bespoke
pac-only AUR dep-closure code (`resolveLocalPkgDeps`, `pkgInfoDepends`,
`pkgInfoReader` (`bsdtar .PKGINFO`), the `depend =` parser, `hostForeignPkgs`,
`builderOnlyDeps`) and the `LocalPkgDef.foreign_query` / `dep_constraint_ops`
fields are removed — the prior consolidation already made the only AUR deps
(`cloudflared-bin`, `gvisor-tap-vsock`) optional, so the closure was vestigial.
`buildDepPkgsOnHost` + `transferAndInstallPkgs` stay (the aur-LAYER deploy path
still uses them). The pac localpkg formerly built+installed those AUR optdeps via
the dep-absent #43 witness; that witness now asserts plain `pacman -U`
auto-resolution of the mandatory repo set instead (the optdeps are opt-in).

**Zero distro-specific Go — everything in `build.yml`.** All package installation
AND generation are 100% YAML-configured. `LocalPkgDef` gained a per-format
`source_sentinel` (`PKGBUILD` / `*.spec` / `debian/control`) so `resolveLocalPkgDir`
drops its hardcoded `PKGBUILD` literal; the rpm/deb `build_template`s build
distro-natively in a podman container (host is CachyOS), the charly binary built on
the host and bind-mounted in. `git grep` for package-manager literals in `ov/*.go`
shows no distro-branching logic.

**New package-source submodules.** `pkg/fedora` (`opencharly.spec`) and
`pkg/debian` (`debian/`) — separately publishable, mirroring `pkg/arch`. One deb
source serves Debian + Ubuntu. The `.deb` deliberately OMITS tailscale (not in
Debian main); the `ov` layer supplies tailscale for Debian/Ubuntu via the
tailscale apt repo (deb-family-scoped). arch/fedora keep tailscale in their
package deps (it is in their repos).

**Downloadable release artifacts.** A new `charly box pkg [format…]` verb builds the
standalone `.pkg.tar.zst`/`.rpm`/`.deb` into `dist/` through the SAME
`build_template` the deploy-time localpkg path uses (R3) — `task pkg:arch|fedora|debian`
wrap it, and `.github/workflows/release-packages.yml` attaches them on a
`v<CalVer>` tag.

**Coverage.** Go unit tests (per-format compile, sentinel resolution, scalar
rejection, migration idempotency) + the VM beds `eval-cachyos-vm` (pac
auto-resolve), `eval-fedora-vm` (NEW — rpm, on a Fedora Cloud VM), and the
extended `eval-debian-debootstrap-vm` / `eval-ubuntu-debootstrap-vm` (deb,
asserting tailscale arrives via the charly layer).

**Three deploy-path bugs the R10 VM beds surfaced (and a misdiagnosis they
corrected).** The first bed runs failed at `deploy add` with
`pacman: command not found` (exit 127) on the debian/fedora guests — proving the
localpkg/system-package install for non-arch VMs was never actually exercised
before. Root cause (NOT the gocryptfs bare-`package:` the first pass guessed —
that candy is correct, and a per-distro-sections "fix" would not have helped
because the format is chosen by `img.BuildFormats`, not by which sections exist):
(1) **`syntheticVmImage` hardcoded `Distro:["arch"]/Pkg:"pac"/BuildFormats:["pac"]`**
for every non-root VM ("cloud_image today is arch" — a now-false comment), so a
debian/fedora/ubuntu guest installed via `pacman`. Fixed to derive the guest's
real distro + `PrimaryFormat()` from `VmSpec.Source.Distro` (debootstrap/pacstrap
VMs) or `Source.BaseUser` (cloud_image VMs). (2) **The layer compiler only reached
`syntheticVmImage` via the `vm:`-prefixed deploy NAME** — a `kind:eval` bed names
its VM by the node's `vm:` cross-ref (`eval-fedora-vm`, not `vm:fedora-vm`), so it
fell through to `syntheticHostImage` (host = cachyos → pac). Fixed with a
`resolveVmEntity` helper (node.Vm wins; "vm:" prefix is the CLI fallback) feeding
`c.vmEntity`. With both fixed, the deb beds advanced to a SECOND failure —
`E: Unable to locate package tailscale` (exit 100): (3) the deb format's
`phase.install.host` cell (the target:local / target:vm path) rendered only
`apt-get install`, never the `{{- range .Repos}}` repo prelude its container
`install_template` sibling has — so the charly layer's tailscale apt repo was never
added on a VM deploy. Fixed by rendering repos (key dearmor + sources.list + ppa)
in the deb host cell, and a same-key/same-type fix in `buildSystemPackagesStep`
(it read `raw["repos"]` plural with a `[]interface{}` assertion; the canonical key
is `repo` and the value is `[]map[string]any`, so `step.Repos` was always empty —
matters for the PhasePrepare repo-gate). `curl` was added to the debian/ubuntu
debootstrap `include_package` set (the repo prelude's key fetch needs it; the
container build already had it from bootstrap). The rpm/pac host cells share the
same repo-omission but no deployed layer declares an rpm/pac custom repo, so it is
not exercised here — it is its own follow-up cutover (with a test layer that
deploys an rpm/pac repo on a VM). Verified `eval-ov-vm` (pac) PASS, `eval-fedora-vm`
(rpm) PASS, `eval-cachyos-vm` (pac) PASS on the fixed binary; the deb beds verified
against the reconciled producer tag.

**Two more the deb beds surfaced against the pushed tag (B6 iterate).** With the
host-cell repo fix in, `eval-debian-debootstrap-vm` advanced through deploy +
eval-live (tailscale INSTALLED via the repo) and failed only at the `charly update`
fresh-rebuild: `gpg: cannot open '/dev/tty'` — `gpg --dearmor -o <keyring>` is
NOT idempotent; on a re-deploy the keyring already exists and gpg prompts to
overwrite, which dies over tty-less SSH. Fixed with `gpg --batch --yes --dearmor`
in BOTH the deb host cell AND the deb container `install_template` (R3). And
`eval-ubuntu-debootstrap-vm` failed at the FIRST `apt-get update` with
`Suites: UNAVAILABLE` — cloud-init's apt module shells out to the `lsb_release`
COMMAND to fill the deb822 `ubuntu.sources` `$RELEASE`; the debootstrap rootfs had
`/etc/os-release` + `/etc/lsb-release` with `noble` but NOT the `lsb-release`
package, so `util.lsb_release()` returned codename `UNAVAILABLE` and every Ubuntu
mirror 404'd. Fixed by adding `lsb-release` to the ubuntu debootstrap
`base_package`. (A latent non-blocker the same run exposed: candy/charly declares the
tailscale `repo:` under BOTH `distro.debian` and `distro.ubuntu`, which collapse
into the one `deb` PackageSection via first-writer-wins + random Go map order, so
a deb deploy picks debian's OR ubuntu's tailscale repo non-deterministically —
both install `tailscale`, so the bed passes either way; making the deb-family repo
selection distro-deterministic is its own follow-up cutover.)

**Third — virtualization's deb list named an Ubuntu-nonexistent package.** With
apt usable, `eval-ubuntu-debootstrap-vm` then failed at the `virtualization` layer
with `E: Unable to locate package libvirt-daemon-driver-network`. Debian's
`libvirt-daemon-system` Depends on the modular `libvirt-daemon-driver-network`
(pulled transitively), but Ubuntu noble ships NO such separate package — it is
bundled into `libvirt-daemon-system`. The candy listed it explicitly in BOTH the
`debian` and `ubuntu` deb sections (which union into the one `deb` format section,
so even the author's correct `ubuntu-24.04` tag override was bypassed — the
synthetic VM image carries `["ubuntu"]` with no version tag, so Phase-1 tag
sections aren't reached; same root collapse as the tailscale-repo note above).
Fix: drop the explicit `libvirt-daemon-driver-network` from the deb-family
sections (verified via `podman run debian:13/ubuntu:24.04 apt-cache depends`: it
is a transitive Depends on Debian, nonexistent-but-bundled on Ubuntu; fedora's rpm
section keeps it). `eval-debian-debootstrap-vm`, `eval-fedora-vm`,
`eval-cachyos-vm` PASS at v2026.157.0634; the deb beds re-verified after this fix.

### 2026-06-05 — feat: consolidate `ov` — merge `ov-full` into `ov`, drop `ov` where unneeded, right-size the package deps, and test ALL charly commands

The `ov` toolchain was split into a bare-binary `ov` layer and an `ov-full`
composition (`charly + virtualization + gocryptfs + socat`). They are now ONE layer:
**`ov` IS the full toolchain** — it composes `virtualization`/`gocryptfs`/`socat`
itself, and `candy/ov-full` (plus the `/ov-coder:ov-full` skill) is DELETED. Bake
`ov` only where a deployment needs a persistent on-`$PATH` charly or the full
toolchain inside (the `ov-mcp` server, the `*-ov` showcases, nested-pod
orchestrators); leaf service/desktop images that only needed transient
in-container `ov` (the dbus delegation path) now DROP it entirely and rely on the
generic copy-`ov`-into-a-running-venue mechanism (`EnsureOvInVenue`). `ov` was
dropped from 14 main leaf boxes (comfyui/filebrowser/hermes-playwright/immich(-ml)
/jupyter*/versa/ollama/openclaw/openwebui/unsloth-studio/mcp) + `hermes-full`;
githubrunner/openclaw-desktop repoint `ov-full`→`ov`.

`pkg/arch/PKGBUILD` (`opencharly-git`) deps were right-sized to the rule "every
cachyos/arch-REPO tool charly invokes is mandatory `depends=`, everything else
optional": **Docker** (alternative engine), the **AUR-only** tools
(`cloudflared-bin`, `gvisor-tap-vsock`), and the **GPU/k8s-situational** tools
(`nvidia-utils`, `kubectl`) moved to `optdepends=`; the missing repo tools
`libarchive` (bsdtar) + `iproute2` (ss) were added to `depends=`.

The "test ALL charly commands" coverage is the new `ov-selftest` CachyOS image (full
`ov` via `ov-mcp` + container-nesting + claude-code) and the
**`eval-ov-selftest-pod`** kind:eval bed: one in-container happy-path check per charly
command GROUP (version/doctor/settings/secrets/vm/status + box/deploy/eval/clean/
migrate/feature/config) plus the `ov-mcp` layer's MCP tool-surface checks; the
**`ov-cli`** kind:score + **`ov-cli-surface`** recipe are the AI-driven companion
(an agent operates build→deploy→status end-to-end). Building this bed surfaced —
and fixed (R2) — a pre-existing **image→box rebrand** staleness in `ov-mcp`'s
baked eval checks (`image.build`/`image.list.images` → `box.build`/
`box.list.boxes`); R5 confirmed those were the only two stale `image.*` MCP refs.

Separately, `charly box add-candy`/`rm-candy` were fixed to resolve images defined in
flat-imported per-kind files (`box.yml`), not only those inlined in
`charly.yml`: a shared `resolveImageNodeFile` (`ov/scaffold_project.go`)
follows the `import:` list to the file where the image lives and edits THAT file,
comment-preserving. Previously `charly box rm-candy <leaf> ov` errored "image not
found in charly.yml" for every box.yml-resident image.

### 2026-06-05 — feat(ov): generic "copy charly into a running venue" mechanism (`EnsureOvInVenue`) — images need not bake the `ov` layer for transient in-container `ov`

The VM-only host→guest charly delivery (`putHostOvInGuest` / `syncOvIntoGuest` /
`EnsureOvInGuest` in `ov/ov_install.go`) was generalized into a venue-agnostic
**`EnsureOvInVenue`** that copies the host's own `ov` binary
(`os.Executable()`) into ANY running deployment — container (`podman cp`), VM /
SSH host (`scp`), or the local host (`install`) — entirely through the existing
`DeployExecutor.PutFile` abstraction, so one code path serves every substrate
(R3). It is quiet (the caller decides what to print), idempotent (a still-good
prior `/tmp/ov-<calver>` copy is verified and reused, never re-transferred), and
never shadows a package-managed `ov` (delivery is to a non-`$PATH` `/tmp` path;
a venue's own PATH `ov` is used as-is when it is current by CalVer). The
functions were renamed (`putHostOvInGuest` → `putHostOvInVenue`,
`syncOvIntoGuest` → `EnsureOvInVenue`); `EnsureOvInGuest` remains as the
VM-deploy strategy wrapper (auto/scp/url/skip) on top.

The EXPLICIT in-venue `ov` callers were wired to it: `dbusNotifyRemoteStrict`
(`charly eval dbus notify`) and `dbusCallRemote` (`charly eval dbus call/list/introspect`)
now COPY the host `ov` in when the venue lacks it, instead of hard-failing. Every
"charly binary not found … add the 'ov' layer to your image" message was deleted
(from `dbusNotifyRemoteStrict`, `dbusCallRemote`, AND `dbusNoToolError`) — copy-in
provides `ov` automatically, so the only remaining hard failure is "charly could not
be provided (copy-in failed) AND gdbus is not installed". The AUTOMATIC
best-effort `sendVenueNotification` (fired on deploy/record/tmux) deliberately
does NOT trigger the copy (it uses a baked `ov` or `gdbus` or skips) —
transferring 27 MB into a container for a maybe-popup is not worth it, and
desktops carry `gdbus` anyway.

Two guarantees are explicit and tested: (1) **no PATH shadowing** — the copy
goes to `/tmp/ov-<calver>` (outside `$PATH`) and is invoked by EXPLICIT path,
never via a PATH lookup, so it can never shadow a package-manager `ov`
(`/usr/bin/charly`), not even one installed later; (2) **the automatic CalVer check
is never skipped** — a venue `ov` that is present and at least as new as the host
is used as-is (no copy, no downgrade); the host binary is delivered ONLY when the
venue `ov` is absent or strictly older.

This is the enabling capability for dropping the baked `ov` layer from images
that only needed it for transient in-container `ov` (the dbus delegation path):
the binary is a 27 MB glibc-only Go executable that `podman cp`s into any
glibc base and runs unchanged. Proven live on the disposable `selkies-kde-rdd`
pod: with the in-container `ov` removed, `charly eval dbus list <pod>` copied the
host binary in via `podman cp`, ran it, and returned the live D-Bus bus names
(exit 0) with zero "missing charly layer" warning.

### 2026-06-05 — fix(migrate): `require-image` recognizes the rebranded `box:` key (no spurious warning on box-format pod deploys)

The `require-image` migration step (`migrate_require_image.go`) checked only the
legacy `image:` YAML key for a `target: pod` deploy's image reference. After the
candy/box rebrand the key is `box:` (what `DeploymentNode.Image` reads via
`yaml:"box"`), so every box-format pod deploy was treated as imageless — and when
inference couldn't recover a name, `charly migrate` warned spuriously (e.g. the
per-host `deploy.yml`'s `sway-browser-vnc`).

Fixed with one `deployNodeImageRef` helper that honors BOTH `box:` (current) and
`image:` (legacy, which the box-rename step converts), used at every site
require-image looks for the image reference (the has-image guard + the
sibling-inference map). A box-format pod deploy is now recognized as already
carrying its reference — no warning, no injection. No schema change; the
`require-image` step keeps its `2026.132.1009` CalVer. Proven by a unit test
(box-format pod deploy → 0 warnings, 0 mutations) and a live `charly migrate` on the
real per-host `deploy.yml` (the `sway-browser-vnc` warning is gone).

### 2026-06-05 — fix(migrate): `target-local` host-disambiguation is idempotent + scoped (stop stacking AMBIGUOUS comments on build templates)

The `target-local` migration step's `host:` disambiguation
(`applyTargetLocalRewrites`, `ov/migrate_target_local.go`) had two compounding
defects, surfaced when the cross-deployment cutover's schema bump re-ran the
migration chain:

- **Over-broad match.** The line-oriented rule fired on ANY indented `host:` key
  with no parent-context awareness — so it matched `phase.install.host:`, a
  BUILD-vocabulary field (a builder install-phase TEMPLATE, authored as a block
  scalar), not a deploy destination. For a `host: |` block scalar the extracted
  value is `"|"`, which is neither hostname-like nor a known template, so it landed
  in the "ambiguous" branch.
- **Non-idempotent.** That branch appended a `# AMBIGUOUS — review:` comment to the
  full line WITHOUT checking one was already present, so every `charly migrate` run
  stacked another copy. `build.yml` had accumulated 4 repetitions on each of 7
  `phase.install.host:` lines (28 total).

Both fixed in `applyTargetLocalRewrites`: a deploy `host:` is always a plain scalar
(hostname / user@host / template name), so a block/flow scalar value
(`|` / `>` / `{` / `[`) is now skipped outright — the precise semantic guard that
excludes every build install-TEMPLATE; and the AMBIGUOUS marker is appended only
when not already present (idempotent). The 28 stacked comments were stripped from
`build.yml` — a content-preserving cleanup, since the comments sat AFTER a `|`
block-scalar indicator and YAML already ignored them. Proven by unit tests over
both the pure rewriter and the full `MigrateTargetLocal` file-walking path (second
pass reports zero changed files), the cleaned `build.yml` re-validated, and the
`eval-pod` mechanism bed (build.yml's full build vocabulary still composes an
image). No schema change (migration-logic fix); the `target-local` step keeps its
`2026.123.114` CalVer.

### 2026-06-05 — feat(eval): cross-deployment probing — `on:` driver + `peer:` siblings + `${PEER_*}`, so ONE deployment tests ANOTHER

`charly eval` gained **cross-deployment probing**: one deployment can act as a test
DRIVER against a SEPARATE deployment as the SUBJECT — the canonical case being a
**Chrome DRIVER pod that CDP-probes a separate web-server SUBJECT pod**, so a real
browser tests the subject without baking Chrome into the subject image. "A
different kind of deployment tests another kind" — pod→pod, local→pod, and local→VM.

Three composable pieces, each at one seam:

- **`on: <driver>`** (the existing `Check.On` step modifier, FINISHED) — dispatches
  a probe against a named driver deployment instead of the subject. Its
  `TargetResolver` now returns a fully-resolved per-target resolver
  (`liveTargetResolver`, reusing `resolveEvalVenue` + `ResolveEvalVarsRuntime`) and
  is wired into `charly eval live` (pod + VM + local paths) and beds — previously it was
  harness-only with an empty resolver. For a `cdp:`/`vnc:`/`mcp:` check it connects
  to the driver's endpoint; for `command:` it runs in the driver's venue.
- **`peer:` siblings** (new `DeploymentNode.Peer`) — companion deployments brought
  up ALONGSIDE the subject (pod peers on the shared `ov` net; `local`/`vm` peers
  host-side), NOT nested inside it. `foldPeers` registers each peer as a top-level
  addressable Deploy entry (inheriting the owner's disposability — no new autonomy);
  `bringUpPeers`/`tearDownPeers` (`ov/deploy_peers.go`) shell out to the SAME
  `charly config`/`charly start`/`charly remove` (pod) and `charly deploy add`/`charly deploy del`
  (local/vm) verbs the deploy path uses, so a `kind: eval` bed and a `kind: deploy`
  share ONE lifecycle (the bed runner inherits it). Peers are NEVER eval-live'd
  (instruments, not subjects).
- **`${PEER_HOST:name}` / `${PEER_ENDPOINT:name:port}`** (new, `ov/eval_peer.go`) —
  cross-deployment address variables overlaid onto whatever resolver is active via
  a single `Runner.PeerVars` injection point in `effectiveEnv` (so they work for
  the primary, the `on:`-swapped, and harness resolvers alike). `${PEER_HOST}` is
  the pod→pod container-DNS address (`ov-<name>`); `${PEER_ENDPOINT}` is the
  host-vantage `127.0.0.1:NNNN` from the shared `resolveEvalEndpoint` — a pod's
  auto-published port OR a VM's `ssh -L` forward over the managed `ov-<vm>` alias
  (the VM branch `eval-k3s-vm` already used host-side) — so a `local`/host driver
  reaches a pod OR a VM subject with no per-kind code. An unresolvable `${PEER_*}`
  (the peer/subject is unreachable) **FAILS** the referencing check, never SKIPs
  it (`filterPeerVars` in `runOne`) — a skip on an unreachable dependency would be
  a fake pass, letting a bed go green when the cross-deployment probe never reached
  its target; legitimate skips (`skip:`/`exclude_distros`/a build-scope deploy var)
  are unaffected.

A thin `chrome-headless` box (real `google-chrome --headless=new`, no compositor,
+ the proven `cdp-proxy`) is the reusable CDP driver. Three `kind: eval` beds prove
the matrix on disposable targets: `eval-cross-pod-cdp` (pod→pod CDP via
`${PEER_HOST}`), `eval-cross-local-http` (local→pod HTTP via `${PEER_ENDPOINT}`'s
published-port branch), and `eval-cross-vm-http` (local→VM HTTP via
`${PEER_ENDPOINT}`'s ssh-forward branch — the SAME check + resolver as the pod bed,
only the subject kind differs, so cross-kind reach carries ZERO VM-specific eval code).

**The VM cell is local→VM, not pod→VM.** `${PEER_ENDPOINT}` is a host-vantage
`127.0.0.1` address a Chrome pod cannot route to (that loopback is the pod's own
netns), and a rootless `qemu:///session` VM shares no L2 bridge with rootless pods —
so the generic, no-hack VM driver runs host-side. The web-server VM (`web-vm`)
exposes nothing on the host: `ssh.port_auto` allocates a free host SSH port and
nginx:80 is reached through the SSH tunnel, never a host port-forward (strictly more
secure). An earlier exploratory `host.containers.internal` / `network: host` pod→VM
bridge was a reinvention that collided with operator services on a fixed port; it is gone.

**RDD findings (proven on live beds before the design committed):** headless Chrome
serves CDP with no compositor BUT requires `--remote-allow-origins='*'` (Chrome
146+ rejects the CDP WebSocket otherwise — `cdp open` via HTTP works, `cdp text`
fails); a `local`/host driver reaches a NAT'd VM's nginx through the existing
`resolveEvalEndpoint` ssh-forward with no per-kind code. A latent generation bug
surfaced + was fixed generically: the `ai.opencharly.info` LABEL was emitted `%q`
(double-quoted), so a layer description mentioning `${PEER_HOST}` made buildah try
to expand it and fail — it is now single-quoted like every JSON label.

**A flag-drift teardown bug surfaced during the `eval-cross-vm-http` R10 and was
fixed generically (R3).** The shared `tearDownPeers` ran `charly deploy del <peer>
--yes`, but Kong renders `DeployDelCmd.AssumeYes` (whose `long:"yes"` tag is a Kong
no-op in the separate-tag form) as `--assume-yes` — so `--yes` was rejected at
arg-parse, the best-effort discard swallowed the error, and the peer LEAKED while
the bed still scored PASS. The same class lived at FOUR pre-existing
`charly deploy del <name> --force` call sites (ephemeral teardown, recursive child
teardown, the systemd-run TTL safety-net timer, and orphan reaping) — every one
silently mis-parsing. The generic fix routes ALL FIVE programmatic teardowns
through one `deployDelArgv(name)` helper (the single source of truth for the valid
`--assume-yes` flag), makes `tearDownPeers` WARN on a teardown failure instead of
swallowing it, and adds a real-Kong-parse test asserting `--assume-yes`/`-y` accepted
and `--yes`/`--force` rejected (the prior stub-based test asserted arg strings and
never exercised flag parsing — which is how the drift shipped). The stale `--yes` in
`/ov-core:deploy` examples was corrected too.

`peer:` is a new authoring key shape, so the schema HEAD bumped to `2026.156.1531`
(the `peer-field` step is additive — it transforms nothing, only raising HEAD so an
older `ov` rejects a `peer:` config instead of silently dropping the key).

### 2026-06-05 — feat(eval): Agent Driven Development (ADD) — a first-class pillar + `charly box/eval feature run` acceptance + the agent grader

**Agent Driven Development (ADD)** became a named, co-equal pillar with RDD, and
its mechanism — already ~80% present as the Gherkin `description:` model, the
`ai.opencharly.description` OCI label, and the `kind: ai`/`recipe`/`score`
loop — was completed into a lived workflow. An entity's intended behaviour is
captured as executable Gherkin scenarios on the LAYER that provides it
(Feature/Narrative + Given/When/Then), authored by a human or an agent, baked
into the image, and verified on every build. ADD is the canonical BDD/Gherkin
pattern, renamed throughout docs and code for the agent that drives it.

What landed:

- **The BIND contract — a step binds to its verifier BY SHAPE.** A scenario step
  that embeds a check verb (`file`/`http`/`cdp`/`mcp`/`command`/…) is graded
  DETERMINISTICALLY by the runner; a prose-only step (a `then:` with no verb)
  binds to an AGENT. Routing is implicit by shape — no new authoring field, no
  schema bump.
- **`charly box feature run <image>`** (build scope, disposable container,
  deterministic steps) and **`charly eval feature run <deployment>`** (deploy scope;
  prose-only steps agent-graded against the live deployment; `--no-agent` for
  deterministic-only CI). Both run an entity's baked `description.scenario`
  source-less from the OCI label, reusing the shared `RunScenarios` engine + the
  same target/var resolution as `charly eval box`/`live` (R3). These are the run
  verbs `ov/description_cmd.go` always reserved alongside `charly feature
  list/pending/validate`.
- **The agent grader** (`ov/eval_feature_grader.go`): `Runner.Grader` dispatches
  at the prose-step branch in `ov/description_run.go`; `AgentGrader` spawns the
  configured `kind: ai` CLI ONCE (bounded; `RunAIOnce`, modelled on
  `LocalCaptureVersion`), hands it the goal + step + live target + the `charly eval`
  probe surface, and parses a `{"verdict":…}` JSON verdict (plain or
  `stream-json`). An unparseable / timed-out / launch-failed grader FAILS the
  step — never a silent pass.
- **`charly candy add-scenario`** (and the auto-reflected `candy.add-scenario` MCP
  tool): idempotent, comment-preserving append of a Gherkin scenario to a
  layer's `description.scenario`.
- **Opt-in gate**: `charly eval run <bed>` runs the bed image's deterministic
  scenarios (`charly eval feature run --no-agent`) after eval-live and after the
  fresh-rebuild — a no-op PASS when none are authored.
- **Candy-authoring rename fix (R3/R5).** The box/candy rename had left the candy
  authoring helpers assuming the pre-rename `layer:` kind key: `charly candy set`
  prepended a stale `layer.` and `charly candy add-rpm/deb/pac/aur` wrote package
  sections at the document root — both producing a stray key the loader ignores
  while the real `candy:` body stayed unedited. A shared `candyBodyNode` helper
  now descends into the `candy:` wrapper for all candy-authoring verbs;
  `TestCandySet_DescendsIntoCandyWrapper` (which previously asserted the buggy
  `layer:` behaviour) and a new `appendLayerPackages`/`appendLayerScenario`
  regression test guard it.

Standing rules (stated forward-looking in CLAUDE.md "Agent Driven Development
(ADD)", `/ov-internals:strict-policy` "ADD", and `/ov-eval:eval`): the spec is
the test; scenarios live on the behaviour's provider layer (one scenario covers
every consuming image — R3); deterministic where a verb fits, agent-graded only
for genuinely free-form behaviour; ADD is an OPT-IN runnable gate, never a
mandate to author scenarios.

### 2026-06-05 — refactor(schema)!: generic kind-container YAML — flat configurable `discover:`, no hardcoded per-kind filenames

YAML files are now GENERIC kind-containers routed by SHAPE: the loader keys each
document by its top-level kind-key, never by filename. A per-kind sibling file
(`box.yml`, `candy.yml`, …) is a pure user convenience configured in
`charly.yml`'s `import:` / `discover:` — never required, assumed, or
hardcoded. `charly.yml` is the only YAML filename the code knows.

**Loader (`ov/unified.go`).** `DiscoverConfig` collapses from a kind-keyed struct
(`{candy: […], box: […], …}`) to a FLAT `[]ScanSpec` (`{path, recursive,
manifest}`); the six per-kind `applyScanSpecs*` discovery functions collapse into
one shape-routed `applyDiscoveredManifest` (`firstKindKey` routes a candy-shaped
manifest to a lazy `From:` dir, every other shape to `mergeKindDoc`); the dead
`entityKind.Filename` field is deleted (the kind vocabulary survives as
`kindKeys` / `kindKeysSet` for shape classification only); the per-directory
manifest filename comes from one overridable `DefaultManifest` const (`scanLayer`
threads it through). The stray `"charly.yml"` literal in `vm_import.go` folds
into `UnifiedFileName`.

**Schema / migration.** A new `discover-flatten` `MigrationStep` (HEAD bumped
`2026.156.557` → `2026.156.1041`) rewrites the kind-keyed `discover:` map into the
flat list, comment-preserving + idempotent. The main repo and the `image/arch`,
`image/bootc`, `image/cachyos` submodule `charly.yml`s migrate to the flat
form. New loader tests assert a configurable manifest, non-candy shape-routing,
and the flat-`discover:` round-trip.

**Bundled rebrand cleanup** (the candy/box rebrand's incomplete R5 sweep, plus the
agents-rename tagline): every stale `charly image …`→`charly box …` / `charly layer …`→`charly
candy …` / `charly image test`→`charly eval box` command reference across `ov/*.go`
(comments, error strings, help text); the freshness-guard **runtime regression**
(`box inspect` / `list` / `validate` wrongly tripped the stale-binary guard
because `main_freshness.go` still matched the old `image …` verb prefixes — the
project-root marker is now `charly.yml`, not `box.yml`); the `charly box new candy`
scaffold now writes canonical kind-keyed content under the configurable manifest
name; and the `for you and your agents` tagline (`CLAUDE.md` H1 + `charly --help`),
so all tagline surfaces agree.

### 2026-06-05 — docs: add VISION.md (long-term thesis + direction)

A docs-only cutover adding a new top-level document, `VISION.md` — a tight
one-page, chocolate-factory-voiced manifesto of the project's bet (candyboxing:
secure the box, fill it with the whole candy store) and its long-term direction.
It distills the thesis that until now lived only as *operating rules* scattered
through `CLAUDE.md` (Candyboxing, Risk Driven Development, Disposable-Only
Autonomy, "for you and your agents") into a standalone, forward-looking
statement, and delegates all detail back out via pointers — it restates no
command usage (→ README / skills), no architecture (→ skills), and no history
(→ this file).

The same cutover closes the doc-placement doctrine gap so the four-way split
becomes five-way: rules → `CLAUDE.md`, features/commands → `README.md`,
usage/architecture → skills, history → `CHANGELOG.md`, and now
thesis/direction → `VISION.md`. `CLAUDE.md` ("Where things are documented" + the
intro pointer), `README.md` (intro pointer + the bottom See-also block), and the
`/ov-internals:skills` "CLAUDE.md vs Skills" taxonomy table each gained a
`VISION.md` reference. No Go, no schema, no `version:` bump, no `charly migrate`
step; the superproject push carries a fresh per-push CalVer tag, `plugins` is
tag-exempt.

### 2026-06-05 — docs: replace the "Oompa-Loompa" naming with "agent"

A docs-only follow-up to the Willy-Wonka README voice (the entry below): the
playful **Oompa-Loompa** naming for the AI operator/driver was replaced with the
plain term **agent** across `README.md` and the `plugins/README.md` tagline —
"your agents are driving", "hands your agent the whole candy store",
"**Agents drive these beds.**", and the **Author with agents** section heading
(TOC + in-text anchors repointed to `#author-with-agents`). The rest of the
chocolate-factory voice is **retained** per the request: the **Swiss Chocolate
Factory** framing, the **conching** metaphor in Build, the **candybox** wording,
and the **What's in the chocolate factory** heading all stay. The
product/technical terms remain exactly as before (`Claude Code`, the Claude Code
`sub-agents`/`agent teams`/`dynamic workflows`, the named agents, `k3s-agent`, the
SSH `agent-forwarding` layer, the SPICE `guest-agent` socket, and the literal
`kind: ai` discriminator). No schema change, no `version:` bump, no migration
step — `README.md`, `plugins/README.md`, and this file are the only surfaces
touched. The superproject push carries a fresh per-push CalVer tag; `plugins`
(no `charly.yml`) stays tag-exempt.

### 2026-06-05 — docs: Willy-Wonka README voice + finish the candy/box doc sweep

A docs-only follow-up to the candy/box rebrand that does two things in one
working tree (no runtime surface touched):

- **Willy-Wonka voice for `README.md` + the `plugins/README.md` tagline.** The
  generic operator/driver references to "AI" / "AI agent" / "agent" became
  **Oompa-Loompa** ("your Oompa-Loompas are driving", "hands your Oompa-Loompa
  the whole candy store", "Oompa-Loompas drive these beds"). The product and
  technical terms were deliberately **kept verbatim**: every `Claude Code`
  reference and link, the Claude Code primitives (`sub-agents`, `agent teams`,
  `dynamic workflows`), the named agents (`eval-bed-runner`, `deploy-verifier`,
  `root-cause-analyzer`, …), the `/ov-internals:agents` skill, the `k3s-agent`
  layer, the SSH `agent-forwarding` layer, the SPICE `guest-agent` socket, and
  the literal `kind: ai` schema discriminator (only its surrounding prose was
  themed — the wire token is load-bearing). The Swiss-army-knife framing became
  a **Swiss Chocolate Factory** ("each production line is a stage"); the section
  heading **"What's in the knife" → "What's in the chocolate factory"** and
  **"Author with AI" → "Author with Oompa-Loompas"** (TOC anchors + in-text
  links updated to match). A **conching** metaphor was added to the Build
  section ("Like conching chocolate, the planner grinds every candy smooth —
  deduplicated, ordered, and cache-warmed — before it sets into a box"), and the
  intro's "sandbox" became "candybox" to match the project's own vocabulary.
  `plugins/README.md`'s `OpenClaw AI gateway` descriptor was kept as a precise
  product-category term (a lookup index must stay searchable for an LLM gateway).
- **Finished the candy/box doc sweep the rebrand's R5 grep missed.** The
  candy/box rebrand's stale-reference sweep matched YAML/skill prose but left
  eight references in `README.md` inline-code and line-wrapped tokens: the
  `image:` selector on the `kind: android` device (×2 → `box:`), the image
  composition `` `layer:` `` key and the code-block `layer: [...]` example (→
  `candy:`), `charly box {... add-layer, rm-layer ...}` (×2 → `add-candy, rm-candy`),
  `charly deploy from-image` (→ `from-box`), and a line-wrapped `charly image reconcile`
  (→ `charly box reconcile`). The README had even contradicted itself — line 470 said
  `from-box` while the command table said `from-image`, and the Troubleshooting
  table said `charly box reconcile` while the Build section said `charly image
  reconcile`. Each corrected token was verified against the landed `ov`
  (`2026.156.453`): the real verbs are `charly deploy from-box` / `charly box
  add-candy`/`rm-candy` / `charly box reconcile`, and the selector/composition fields
  are `yaml:"box"` / `yaml:"candy"`.

No schema change, no `version:` bump, no migration step — `README.md`,
`plugins/README.md`, and this file are the only surfaces touched. The superproject
push still carries a fresh per-push CalVer tag; `plugins` (no `charly.yml`)
stays tag-exempt.

### 2026-06-05 — feat(schema)!: candy/box rebrand — `layer:`→`candy:`, `image:`→`box:` (schema 2026.156.557)

The two foundational schema kinds were renamed project-wide, making the
"candyboxing" metaphor literal: a **candy** (formerly a layer) composes into a
**box** (formerly an image). The cutover spans every surface:

- **Wire schema**: the YAML keys `image:`→`box:` and `layer:`→`candy:` (top-level
  maps, the `kind: box`/`kind: candy` discriminators, the `candy:` composition
  list on a box, the `box:` selector on pod/deploy/vm/k8s/android nodes,
  `add_layer:`→`add_candy:`). Compound and external-schema keys that merely share
  a prefix are preserved (`image_default`, `imagelabel`, `layer_field`,
  `layer_file`), as is a sidecar's `image:` (a raw upstream OCI ref, not an
  opencharly box).
- **Files + directory**: `image.yml`→`box.yml`, every `layer.yml`→`candy.yml`,
  and the `layers/` directory→`candy/`.
- **CLI**: `charly image`→`charly box`, `charly layer`→`charly candy`, plus `add-layer`→
  `add-candy`, `rm-layer`→`rm-candy`, `new layer`→`new candy`, `from-image`→
  `from-box`, `cp-image`→`cp-box`, `eval image`→`eval box`, `list images/layers`→
  `list boxes/candies`, `--add-layer`→`--add-candy`.
- **Go identifiers** (type-aware `gofmt -r`): `ImageConfig`→`BoxConfig`,
  `LayerYAML`→`CandyYAML`, `ImageMetadata`→`BoxMetadata`, the `*Doc`/`*Cmd`/
  `LayerRef`/`InlineLayer` siblings, and the command structs. Internal struct
  *field* names (`.Layer`/`.Image`) and the generic OCI-image-artifact helper
  types (`FetchedImage`, `ImageInfo`) were intentionally kept.
- **OCI labels**: the `{layer, image, deploy}` section keys in
  `ai.opencharly.tests`/`shell`/`description`→`{candy, box, deploy}`, and the
  container-key consts `ai.opencharly.image`→`box`, `layer_version`→
  `candy_version`, `env_layer`→`env_candy`, `data_image`→`data_box`. The presence
  sentinel `ai.opencharly.version` is unchanged.
- **Migration**: one idempotent `charly migrate` step (`candy-box-rename`,
  `2026.156.556`) renames keys at every depth (handling the `candy:`-inside-
  `candy:` collision), renames the files + directory, rewrites `import:`/
  `discover:` paths, **and rewrites the `/layers/` segment inside remote
  `@github.../layers/<name>:vTAG` refs to `/candy/`** so remote-cache
  auto-migration of old-schema producer tags resolves to the renamed directory.
  The host `~/.config/charly/deploy.yml` selectors migrate too.
- **Configurable paths**: the candy directory is now centralized in a single
  `DefaultCandyDir` constant (with the `discover:` block providing the
  per-project override and `layerCopySource` honoring a `directory:` override),
  removing the scattered hardcoded `layers/`/`candy/` literals.

Lessons logged: `go build`/`go test` passed clean while three runtime bugs hid —
Kong derives a command name from the *field* name (so `cmd:"box"` is ignored; the
fix is `cmd:"" name:"box"`), `parseLayerYAML` had its own wrapper-key check
separate from the struct tags, and the eval bed *runner* self-execs
`charly image build`/`charly eval image` (the exit-80 build failure) — all caught only by
running `charly box validate` and the live `eval-pod` R10 bed, never by unit tests.

R10: `charly eval run eval-pod` passes end-to-end on the disposable bed (build → eval
box → deploy → eval live → fresh `charly update` → teardown). Old configs migrate via
the one idempotent `charly migrate`; a residual `image:`/`layer:` key now fails at
load with a `Run: charly migrate` hint.

### 2026-06-04 — feat(eval): `charly eval wl` host-safe KWin/KDE parity (window-mgmt + keyboard + clipboard + screenshot), pointer + resolution deferred (#49)

`charly eval wl` had full desktop-automation coverage on wlroots compositors (sway,
labwc) but nothing on KWin — the compositor of the KDE-Plasma selkies flavor
(`selkies-kde` / `kde-selkies`). This cutover brings KWin to the same level of
eval support for every method group that has a **host-safe** backend, and
explicitly defers the two that don't.

**RDD found-and-discarded mechanisms (live `selkies-kde` KWin-6 / Plasma-Wayland
pod).** Pointer injection was the hard part, and five candidate mechanisms were
each disproven empirically before any code was written:
`ydotool`/`/dev/uinput` leaks a virtual device into the **host** kernel evdev
(the pod's `/proc/bus/input/devices` IS the host's — /proc is not namespaced — so
ydotoold's device showed up host-side, 15→16 devices), which both disrupts the
operator's real desktop and never reaches the pod's headless KWin;
`org_kde_kwin_fake_input` was **removed in KWin 6** (not advertised as a Wayland
global); the `org.freedesktop.portal.RemoteDesktop` portal (and `libei`/EIS via
`ConnectToEIS`) is **approval-gated** — `CreateSession`/`SelectDevices` succeed
but `Start` blocks on an approval dialog no headless session can answer (verified
TIMEOUT); `xdotool`/XWayland is lazy/not running and XTEST→Wayland-window
delivery is unproven. The only thing proven to inject host-safely into this KWin
is selkies' own `selkies-capture-server`, whose path is internal/opaque.
Resolution (`kscreen-doctor`) **hangs** on the headless Plasma session even with
`kded6` + the kscreen module loaded (a `kde-output-management-v2` vs old-kwayland
protocol mismatch in `KSC_KWayland.so`).

**What landed (the host-safe 6/8).** `ov/wl.go` gained `detectCompositor` (KWin
when `kwin_wayland` is PID-present, else wlroots) and per-method KWin routing:
window management (toplevel / windows / focus / close / fullscreen / minimize /
geometry) via **kdotool** (KWin scripting — `search` / `windowactivate` /
`windowminimize` / `windowclose` / `windowstate --toggle FULLSCREEN` /
`getwindowgeometry`); keyboard (type / key / key-combo) via `wtype`
(`zwp_virtual_keyboard_v1`, which KWin implements); clipboard via `wl-clipboard`
(KWin implements `wlr-data-control`); screenshot via `pixelflux`; atspi / exec /
status unchanged. `wlShellCmd` now sources the **live compositor's** session env
(`XDG_RUNTIME_DIR` / `WAYLAND_DISPLAY` / `DBUS_SESSION_BUS_ADDRESS`) from the
running compositor process — load-bearing because the selkies-kde session runs
`startplasma-wayland` under `dbus-run-session` on a random `/tmp/dbus-XXXXXX` bus
that differs from the image-baked ENV, so kdotool's D-Bus would otherwise hit the
wrong bus (also a strict improvement for sway/labwc). The `kdotool` AUR package
is added to the `kde-shell` layer (KWin-only — no waste on labwc/sway).

**What is deferred (their own future cutovers, NOT shipped here).** Pointer
(click / mouse / scroll / drag) returns a clear, non-hanging "not supported on
KWin" error naming the reason; the same for resolution. Both await dedicated
RDD-first cutovers (pointer: reverse-engineer `selkies-capture-server`'s host-safe
injection or configure portal auto-grant; resolution: the KWin output-protocol
version alignment).

**Coverage.** `ov/wl_kwin_test.go` unit-tests `detectCompositor`, the
compositor-env-sourcing `wlShellCmd`, the kdotool search→action chain, and the
KWin-pointer-unsupported error. `kde-selkies` gains deploy-scope `wl:` checks
(`status` reports `compositor: kwin` + `kdotool: available`; `type`; `toplevel`;
`clipboard` set→get round-trip) that run during `eval-selkies-kde-pod`'s
`charly eval live` — proving the backends on the real nested KWin desktop. Cross-repo
B6: the producer (main: ov/wl.go + kde-shell + kde-selkies) lands + tags first;
`image/cachyos` reconciles its `@github` pins and runs the authoritative R10
(`eval-selkies-kde-pod`) against the pushed tag.

**Folded-in fix the R10 surfaced — supervised Chrome for both selkies flavors.**
The first `eval-selkies-kde-pod` R10 (against the pushed producer tag) failed on
two PRE-EXISTING, KWin-wl-unrelated checks: the Chrome CDP `/json/version` probe
(EOF) and the selkies frame/VAAPI-encode probe (blank frame) — because Chrome was
dead. RCA: Chrome was launched **fire-once and unsupervised** from each per-flavor
compositor autostart (`labwc/autostart`, `kde-selkies/kde-selkies-session`), and a
Chrome started during the nested compositor's startup-race **self-exits cleanly**
(~39s, exit 0 — the window-less browser's sole window goes away on the early
color-manager re-init) with nothing to relaunch it. The bed only ever "passed" by
racing Chrome's brief alive window (the `chrome-cdp-version` check polls with
`eventually:`); the from-clean R10 rebuild made the race fail deterministically.
RDD on the live pod: a Chrome relaunched **post-settle stays up indefinitely** (a
keep-alive-URL theory was disproven — `chrome-wrapper` drops positional args, so
the cause is launch *timing*, not a missing URL). Per R2 (blocking the R10) + R3
(one shared abstraction) the fix is a **supervised `[program:chrome]` in the
shared `selkies-core` layer** (`restart: always`, `start_secs`/`start_retries` so
the one startup-race exit resets the retry budget rather than tripping FATAL;
`chrome-wrapper` self-polls for `wayland-0` so `autostart=true` is
self-synchronizing) — both selkies autostarts drop their fire-once
`chrome-wrapper &` launch. **SELKIES-ONLY:** `sway-browser-vnc` launches Chrome
via `chrome-sway` (not `selkies-core`), is not pixelflux-nested, never hit the
race, and is untouched. A `selkies-chrome-supervised-alive` deploy-scope `eval:`
check (Chrome process + CDP responsive after a 25s settle) is added so this
regression class fails loudly henceforth. Also fixed in passing: the KWin
`wl: toplevel`/`windows` backend used `kdotool … getwindowname %@`, which errors
(`Unknown command %@`) — corrected to `kdotool search ''` (lists window IDs).
(The `/ov-selkies:chrome` skill still documents an unimplemented chrome-layer
`[program:chrome]` + `chrome-crash-listener` circuit-breaker — a separate,
pre-existing doc-accuracy follow-up, tracked as its own docs-only cutover.)

### 2026-06-04 — fix(ov): `charly eval k8s` validates the resolved kubeconfig context up front (no stale-context fall-through) (#45)

`k8sClusterFlags.restConfig()` resolved the target context
(`--kubeconfig`/`--cluster`→K8sSpec/`--context`/current-context) and handed it
straight to the deferred client without checking it exists. A STALE or empty
current-context — e.g. a deleted k3s deploy whose `~/.kube/config` entry was
never cleaned up — surfaced only at the first API call as a cryptic
`dial tcp … no such host` / TLS / "context does not exist" error, or silently
targeted the wrong cluster.

**Fix** (`ov/k8s_cmd.go`): `restConfig()` now loads the raw kubeconfig, falls
back to its current-context when no flag selects one, and **fails fast** with an
actionable message before any connection: `kubeconfig context "X" does not exist
(known: a, b, c); pass --cluster <name> or --context <ctx>` — or `no kubeconfig
context selected …` when nothing resolves. Valid contexts pass through unchanged.

**Coverage** (`ov/k8s_context_test.go`): valid-explicit / empty-→-current-context
/ stale-rejected-early (asserts the error lists known contexts) / empty-kubeconfig.
`go test ./ov/...` green.

**R10:** `charly eval run eval-k3s-vm` PASS (6/6 ok: true, 98s) — the bed's
`charly eval k8s` verbs resolve `--cluster ${DEPLOY_NAME}` through the validated
`restConfig` against a real provisioned k3s cluster, confirming the validation
leaves the happy path intact. No schema/submodule change; landed tag-only.

*Separable follow-up:* `k3s_post.go` writes a kubeconfig context on provision but
`charly deploy del` does not remove it, so stale contexts accumulate — its own
cutover (the validation above now handles them gracefully).

### 2026-06-04 — fix(ov): kind-files migrator no longer re-splits an intentionally-inline charly.yml (version-gate)

`runMigrations` runs EVERY step on every `charly migrate` (each self-guards on
idempotency, not on the config's version). The `kind-files` step (schema
2026.125.2355) splits inline `image:`/`vm:` blocks into sibling files — but its
guard ("does an inline block exist?") was too broad: it fired on
`image/bootc`'s deliberately single-file `charly.yml` (a supported terminal
layout — CLAUDE.md: "both layouts load identically; … OR inlines them all in the
one charly.yml (e.g. bootc)"). The #51 cutover surfaced this — running
`charly migrate` on bootc split its inline config into `image.yml`/`vm.yml`; it was
reverted there and is fixed here.

**Fix — a version-gate scoped to kind-files** (`ov/migrate_kind_files.go`):
`MigrateKindFiles` now reads the config's current `version:` and is a no-op when
it is at/past `kindFilesSchemaVersion` (2026.125.2355) — a config authored after
the cutover chose its layout deliberately and must not be re-split; only an OLDER
config has legacy inline that needs splitting. The gate is kind-files-specific by
design: `field-singular` (which #51 extended) is idempotent and SHOULD re-run on
configs past it (to singularize keys added to its table later), so a blanket
framework-level version-gate is wrong — only the non-idempotent `kind-files`
transform needs gating. `kindFilesSchemaVersion` is the single source for the
version (referenced by both the gate and the registry step).

**Coverage** (new `ov/migrate_kind_files_test.go`):
`TestMigrateKindFiles_SkipsIntentionalInline` (a config at HEAD with inline
`image:`/`vm:` is a no-op — fails without the gate) +
`TestMigrateKindFiles_SplitsLegacyInline` (a pre-2026.125.2355 config still
splits — the gate doesn't break the legacy migration). `go test ./ov/...` green.

**Verification:** a real `charly migrate` on `image/bootc` (schema 2026.155.1801)
reports "nothing to migrate" — the inline `charly.yml` is untouched, no sibling
files are created, and `charly image validate` passes. No schema bump (the fix changes
when kind-files runs, not the schema); landed tag-only.

### 2026-06-04 — fix(build): single-source the cachyos pacstrap repo config — runtime pacman.conf renders from extra_repo (no install-vs-runtime drift) (#47)

**The bug + its root cause (duplication).** The `cachyos-extra` pacman repo
serves an HTML directory listing, not a `.db` (confirmed live:
`https://mirror.cachyos.org/repo/x86_64/cachyos-extra/cachyos-extra.db` →
`Content-Type: text/html`). A prior fix removed it from the booted-guest
`runtime_pacman_conf` — but **left it in the pacstrap-install `extra_repo`**.
Root cause: the cachyos repo list was hand-maintained in **two** surfaces of
`build.yml`'s cachyos `bootstrap:` — `extra_repo:` (structured, drives the
pacstrap-chroot install config via `renderPacstrapExtraConf`) and
`runtime_pacman_conf:` (a verbatim heredoc, the guest's `/etc/pacman.conf` for
`add_layer` installs). Two copies → the fix landed in one and drifted in the
other.

**The fix — one repo source, both configs derived.** `extra_repo` is now the
SINGLE cachyos-repo definition (with `cachyos-extra` removed, so it's gone from
install AND runtime by construction). `runtime_pacman_conf` becomes a Go
`text/template` evaluated against the `PacstrapDef` (`renderRuntimePacmanConf`,
`ov/build.go`): its repo list comes from `{{ range .ExtraRepos }}`, and the
template adds only the runtime-only framing (the `[options]` header + Arch
`[core]/[extra]`). Mirrors the existing `ExtraPacmanConf` rendered-context-field
pattern — the booted-guest config is now a rendered context field
(`{{.RuntimePacmanConf}}`) in both bootstrap paths (`vm_bootstrap.go` +
`build.go`'s `runPrivilegedBootstrap`), not a second verbatim copy. No build.yml
schema change (the field stays a string, now a template) and no migration — a
legacy verbatim `runtime_pacman_conf` with no template actions renders to itself.

**Coverage.** `TestCachyosRuntimePacmanConf` now asserts the single source: the
raw field must derive its repos via `{{ range .ExtraRepos }}`, the rendered
output carries the v3 repos + `[options]`/`SigLevel = Never`/Arch `Include`, and
`cachyos-extra` is absent from BOTH the rendered runtime config AND the install
config (`renderPacstrapExtraConf`). `go test ./ov/...` green; `charly image validate`
clean.

**R10 (cross-repo B6).** The cachyos pacstrap is exercised only by the
`eval-cachyos-vm` bed in `image/cachyos` (which pins main `@github`), so this
producer change lands first + the consumer reconciles to the new tag; the
authoritative R10 is `charly -C image/cachyos eval run eval-cachyos-vm` (pacstrap →
boot → `add_layer` pac install against the rendered runtime config).

### 2026-06-04 — refactor(ov)!: every ov-only plural goes singular — OCI label contract + remaining authoring keys + full Go symmetry (#51)

**Directive.** Following #50 (which made the layer parser hard-reject plural
authoring keys), the operator asked to finish the job: *replace ALL plurals
that aren't mapped to another schema's plural in a generated config (libvirt /
cloud-init / Kubernetes) — including the OCI labels that are only used by `ov`
itself — with singulars.* #50 had deliberately kept the `ai.opencharly.*`
labels plural (treating them as an external contract); this cutover inverts
that: those labels are `ov`'s own namespace (`ov` both emits and reads them),
so they go singular too, with full Go-identifier symmetry.

**The OCI label contract — singular (`ov/labels.go`, `ov/capabilities.go`).**
~22 plural `ai.opencharly.*` label STRING VALUES went singular:
`services→service`, `ports→port`, `volumes→volume`, `aliases→alias`,
`hooks→hook`, `routes→route`, `secrets→secret`, `skills→skill`,
`env_layers→env_layer`, `port_protos→port_proto`,
`layer_versions→layer_version`, `platform.formats→platform.format`,
`builder.uses→builder.use`, `builder.provides→builder.provide`, and the eight
compound `env_*`/`secret_*`/`mcp_*` keys. Already-singular labels (`version`,
`image`, `init`, `env`, `data`, `path_append`, `port_relay`, `platform.distro`,
…) are untouched. The per-init service sub-label read string hardcoded at
`labels.go` (`"ai.opencharly.service." + meta.Init`) and the `build.yml`
init `label_key:` entries moved in lockstep.

**Full Go symmetry.** The operator chose to rename the Go identifiers too, not
just the wire strings — realigning a latent asymmetry where `ImageMetadata`
carried plural fields (`Services`, `EnvProvides`) to mirror the old plural
labels while the authoring `ImageConfig` was already singular (`config.go`).
Renamed: every label const (`LabelServices→LabelService`, …), every
`ImageMetadata` field (`.Services→.Service`, `.EnvProvides→.EnvProvide`, …),
the `CapabilityLabelMap` keys, and the `EnvProvidesEntry`/`MCPProvidesEntry`
types. The three label-entry types whose singular name would collide with a
now-singular const (`LabelVolume`/`LabelRoute`/`LabelSecret`) were renamed to
`Label<X>Entry` to free the const name — the only way Go permits a const and a
type to coexist. `TestCapabilityLabelCompleteness` (reflects field→map) stays
green. Genuinely-external field names stay plural — `ServiceNames`,
`DataEntries`, `Distro`, and the runtime `status`/`tunnel`/`quadlet`
`Ports`/`NetworkSettings.Ports` (podman's own schema).

**Remaining authoring keys (inverts #50's keep-plural).** The two layer-level
keys #50 left plural — `hooks:` and `capabilities:` — went singular
(`hook:`/`capability:`) across the `LayerYAML` struct tags, `knownFields`, and
the two in-repo layers that used them (`layers/github-runner`,
`image/bootc/layers/bootc-config`). The lone `tags:` eval-scenario fixture →
`tag:`.

**Hard migration — schema `2026.144.1443` → `2026.155.1801`.** A schema/format
change, so it bumps `LatestSchemaVersion()` and ships a migrator. The
`field-singular` table (`ov/migrate_field_singular.go`) gained
`hooks`/`capabilities`/`tags` (the canonical native-plural→singular table — one
table, not two); a new `singular-label` `MigrationStep`
(`ov/migrate_singular_label.go`) rewrites the remaining label-STRING references
a config can carry (`build.yml` `label_key:`, plus any forked `oci_label:` /
eval label inspection). Baked OCI labels inside built images cannot be migrated
by config rewriting — they are re-emitted singular on the next `charly image build`
(a hard-cutover rebuild). The main repo + all 8 `image/<distro>` submodules
were stamped to the new schema. **Existing plural-labeled images read
metadata-blind under the singular reader until rebuilt** — the operator rebuilds
live deploys (`charly update --rebuild-image <name>`) at their convenience (no live
workstation was rebuilt in this cutover, by operator choice).

**Coverage.** `TestExtractMetadata_SingularLabels` (round-trips LITERAL
singular keys — fails if any const regresses to plural),
`TestLabelConstantsAreSingular` (pins every renamed const), and
`TestMigrateSingularLabel` (build.yml `label_key` rewrite + idempotency), plus
the existing completeness test. `go test ./ov/...` green.

**Verification.** `charly image validate` EXIT 0 with **zero warnings** across the
main repo + all 8 submodules (the whole `cachyos`-namespace import chain loads
at the new schema). R10 `charly eval run eval-pod` **PASS** (8/8 `ok: true`,
`total_seconds: 241`): the bed built the image with the new `ov` (emitting
singular labels — the 165s build is the expected `ov`-layer cache cascade),
deployed it (`ExtractMetadata` reading the singular labels), ran the live
deploy probes (the `service.<init>` sub-label), and fresh-updated — proving the
emit↔read round-trip end-to-end on a real image.

*Out-of-scope note:* running `charly migrate` surfaced that the `kind-files`
migrator would split `image/bootc`'s intentionally-inline `charly.yml` into
sibling files — contrary to the supported "bootc inlines them all" layout. That
split was reverted (bootc keeps only its version stamp + the `capability:`
rename); the `kind-files`-vs-inline interaction is a separate follow-up.

### 2026-06-04 — fix(ov): layer parser hard-rejects unknown top-level keys (strict singular enforcement) + close the silent-typo gap (#50)

**Symptom that motivated this.** During #48, a `vscode` layer authored with
`tasks:` (plural) and `vars:` (plural) built a layer that did *nothing* — the
`code` binary never installed, `VSCODE_VERSION` was unbound — yet every
validator and unit test stayed green. The keys were *silently dropped*: the
`LayerYAML` struct's yaml tags are singular (`task:`, `var:`, …), and the
`UnmarshalYAML` fallback routed every unrecognized top-level key to the
build.yml-format / distro-tag sections and `continue`d on a decode miss. A
plural typo therefore vanished without a trace, costing real R10 cycles to
diagnose.

**Root cause — three coupled defects.**

1. **`knownFields` had drifted from the struct.** The fast-path allow-list
   carried two *plural* entries that the struct never accepts (`env_provides`,
   `vars`) and was missing two real singular fields (`localpkg`, `reboot`), so
   the allow-list neither matched reality nor caught the typos it should have.
2. **`UnmarshalYAML` silently swallowed unknown keys.** The tag-section branch
   `continue`d on a failed decode or an empty `Package` list instead of
   recording the key — so a genuine typo was indistinguishable from a real
   distro tag.
3. **A real latent bug had already slipped through.**
   `layers/android-emulator-layer/layer.yml` declared `secret_accepts:`
   (plural) — so the layer's `GOOGLE_ACCOUNT_EMAIL` / `GOOGLE_AAS_TOKEN`
   credential declarations (for `apkeep`) were *never registered*. The strict
   parser caught it the instant it landed.

**The fix (hard cutover).**

- **`ov/layers.go`** — `UnmarshalYAML` now collects every top-level key that is
  neither a known field, a build.yml package format, nor a distro tag with a
  `package:` list, and returns a **hard error** naming the offending key(s) with
  a singular-typo remediation hint (`use the SINGULAR form: task: not tasks:,
  var: not vars:, layer: not layers:, env_provide: not env_provides:`). The
  `layerYAMLKnownFields` allow-list is realigned with the struct's singular yaml
  tags (`env_provides`→`env_provide`, `vars`→`var`; `localpkg` + `reboot`
  added). `hooks` and `capabilities` stay plural — they are genuinely
  plural-valued config keys, not the silent-drop class.
- **`ov/validate.go` + `ov/graph.go`** — the validator's "compose layer" error
  string and the surrounding comments say `layer:` (singular), not `layers:`.
- **`ov/layers_test.go`** — `TestLayerUnknownKeyRejected` proves the rejection
  (plural `tasks:`/`vars:`/`layers:`/`secret_accepts:` all error with "unknown
  top-level key"), the singular forms parse + populate, and a legitimate
  `fedora:43` distro tag with a `package:` list still parses. The test fails
  without the parser change — the eval-coverage gate.
- **`layers/android-emulator-layer/layer.yml`** — `secret_accepts:` →
  `secret_accept:` (the latent bug above).
- **Docs (58 files in `plugins/` + the main `README.md`)** — every
  plural→singular field reference swept to match the parser and the operator's
  prefer-singular directive: the eight compound keys
  (`env_provides`/`env_requires`/`env_accepts`/`secret_accepts`/`secret_requires`/`mcp_provides`/`mcp_requires`/`mcp_accepts`
  → singular) plus `task`/`var`/`layer`/`require`/`port` field references and
  the `/ov-image:layer` CLI examples (`charly layer set … port`/`require`).

**No schema-version bump — this is *enforcement*, not a format change.** The
plural→singular *format* change already shipped at schema `2026.130.1530`
(the `field-singular` MigrationStep), whose `pluralToSingularYAMLKeys` table
already covers the **complete** key set this parser rejects (including
`secret_accepts`→`secret_accept`). The load-time gate forces `charly migrate` on
any config older than HEAD, so a legacy config can never reach the strict
parser un-singularized. The strict parser closes the *post-migration typo* gap
(`charly migrate` is a one-shot transform; only a parse-time check is continuous).
The set of *valid* keys is unchanged — only already-broken configs (whose
plural keys were silently dropped) now fail loudly. Landed tag-only;
`version:` stays at `2026.144.1443`.

**Verification.** `go test ./ov/...` green (incl. the new test); `charly image
validate` parses all layers across the main repo + all 8 submodules at EXIT=0
with **zero warnings** (`validate` + `generate`); R10 `charly eval run eval-pod`
**PASS** (8/8 steps `ok: true`, `total_seconds: 121`) — the strict parser
parses, builds, deploys, and fresh-updates a real image without regression.

### 2026-06-04 — fix(vscode): version-pin VS Code direct from Microsoft, replacing the broken `visual-studio-code-bin` AUR (#48)

The `vscode` layer's Arch side installed the AUR `visual-studio-code-bin`, whose
PKGBUILD periodically ships broken source files — commit `07f5a1e` (2026-06-04)
un-gitignored three `*.in` resource files and committed them as empty 0-byte
placeholders, so makepkg's validity check fails for every consumer (RCA-proven;
it broke the live `cachyos-gpu` workstation restore — see the operator-restore
incident). The fix removes the AUR dependency entirely: the Arch path now
downloads a VERSION-PINNED VS Code tarball direct from Microsoft's official
versioned URL (`https://update.code.visualstudio.com/<ver>/linux-x64/stable`)
with a sha256 WE control (computed against the real 1.123.0 tarball,
`2fdef947…`), self-gated to Arch via `command -v pacman` (Fedora keeps the
unchanged MS-yum-repo `code` package), with the electron runtime libraries
declared as `distro.arch.package:` (the set the AUR `depends=` auto-installed). A
future VS Code bump is now a deliberate, sum-verified version change — never a
silent upstream break.

Eval-coverage: the `vscode` layer gained build-scope `eval:` checks
(`code-binary` + `code-version`), and a disposable producer R10 vehicle was added
in the main repo — a minimal `vscode-test` Arch pod image (`arch` base + the new
generic `eval-keepalive` layer + `vscode`) backing the `eval-vscode-pod`
`kind: eval` bed. The new `eval-keepalive` layer composes supervisord and adds a
reusable `sleep infinity` service so a pod whose layer-under-test is a build-time
install (no service of its own) still reaches steady-state for `charly eval
live`/`run` AND supervisord gets a valid assembled `/etc/supervisord.conf` (its
baked `supervisorctl pid` check needs one).

R10 (`charly eval run eval-vscode-pod`, disposable pod, no GPU): PASS — 8/8 steps
incl. the fresh-rebuild `update` step: `✓ /usr/bin/code`, `✓ code --version
exit=0`, `✓ supervisorctl pid`, `✓ supervisorctl status keepalive`. The
cross-repo consumer (`image/cachyos`) repoints its `@github .../vscode` pin to
this layer's landed tag via `charly image reconcile` and re-adds vscode to the
`cachyos-gpu` workstation (reverting the recovery-time TEMP removal), then
`charly deploy add cachyos-gpu` reinstalls VS Code on the live workstation.

### 2026-06-04 — fix(ov): `charly update <vm>` Rebuild re-applies layers like pod/local (#42)

`VmUnifiedTarget.Rebuild` (`ov/unified_targets_vm.go`) was domain-recreate-only:
best-effort `charly vm destroy` → (with `--build`) `charly vm build` → `charly vm create` →
`charly vm start`, with NO layer-apply step. So after `charly update <vm>` on a
disposable VM the guest came back as a bare image with the deploy node's
`add_layer:` layers — and any nested pods — GONE. A config change (a newly-added
layer, a new nested pod) silently never took effect on rebuild. This corrects
the per-substrate Rebuild contract recorded in the 2026-06 "charly update unified
Rebuild" entry below, which described the vm substrate as
"destroy→create the domain, reuse disk unless `--build`" with no layer re-apply
— that was the bug, not the intended contract.

Fix: after the domain boots, `Rebuild` now calls
`runOvSubcommand("deploy", "add", t.NodeName)` — the SAME shared layer-apply
primitive `LocalUnifiedTarget.Rebuild` and `PodUnifiedTarget.Rebuild` already
end in (R3). `charly deploy add <node>` routes through `dispatchNode → ResolveTarget
→ VmUnifiedTarget.Add → VmDeployTarget.Emit`, which SSHes into the fresh guest
and re-applies the node's `add_layer:` layers (and redeploys nested pods)
idempotently over the surviving guest ledger (`charly vm destroy` removes the domain,
not deploy.yml's `vm_state`). No bespoke SSH-emit logic is duplicated into
Rebuild. The forward-looking contract is now uniform across all three live
substrates: **vm/pod/local Rebuild all end in `charly deploy add <node>`**.

Made `runOvSubcommandCapture` a package var (mirroring `runOvSubcommand`) so the
`charly vm start` boundary is stubbable; new unit coverage
(`TestVmUnifiedTarget_Rebuild_DryRun` ordering assertions +
`TestVmUnifiedTarget_Rebuild_ReappliesLayers`) proves the recorded subcommand
sequence ends in `deploy add <node>`. Doc drift corrected in
`/ov-core:ov-update`, `/ov-vm:vm`, `/ov-internals:vm-deploy-target` (which also
fixed a stale `rebuild.go` file reference → `ov/run_subcommand.go`), and the
`update_deploy_dispatch.go` dispatch comment.

This cutover also corrected `/ov-internals:disposable`'s `charly update <name>`
section, which still documented the PRE-#30 behavior — claiming `charly update`
*refuses* a non-disposable target — and listed two flags that don't exist
(`--dry-run`, `--rebuild-image`). It now matches the code (`noteUpdateDisposability`,
`ov/update_deploy_dispatch.go`): `charly update` NEVER refuses on disposability; for
a non-disposable target it prints a one-line transparency note and proceeds.
`disposable: true` is the authorization for the AI's AUTONOMOUS rebuild + the
eval-runner's unattended fresh rebuild, NOT an `charly update` capability gate. The
flag list now reflects the real surface (`--build`, `--tag`, `-i/--instance`,
`--seed`/`--no-seed`/`--force-seed`/`--data-from`).

A BLOCKING issue surfaced by #42's R10 was fixed in the same working tree (R2;
RCA via `/ov-internals:root-cause-analyzer`): the `eval-k3s-vm` bed's
`charly eval live` intermittently failed with empty `ingressclass`/`storageclass`
output while the matching `default=true` checks passed in the same run. Root
cause was a readiness race, NOT a #42 regression — the failing checks predate #42
by weeks and #42 never touched `eval.yml`. The `ingressclass`/`storageclass`
verbs are one-shot list operations that exit 0 on an EMPTY list, so a `contains`
matcher run before the k3s addon stack (Traefik helm-install job + local-path
provisioner) registered its IngressClass/StorageClass *fails rather than waits* —
and the only readiness gate ahead of them (`wait-ready` on coredns) is unrelated
to those resources. Fix (R4: a readiness primitive, never a sleep; R3: generic
across both surfaces): reorder the existing `k8s: addons` roll-up gate — which
BLOCKS until Traefik + ServiceLB + local-path are all Ready — to run BEFORE the
class checks, in the `eval-k3s-vm` bed (`eval.yml`) AND in the `k3s-server`
layer's own deploy-scope checks (`layers/k3s-server/layer.yml`), which carried
the identical latent race (it passed only by timing luck). The bed's
`kv-k8s-ingressclass-traefik` check also gained the `stdout: {contains: "traefik"}`
matcher its storageclass sibling already had (it was previously vacuous — passing
even on an absent ingressclass). The `k3s-server` layer `version:` was bumped, and
the `/ov-kubernetes:eval-k8s` skill example reordered to teach gate-first ordering
(its example had shown the racy order). Proven on a fresh COLD `charly update
eval-k3s-vm --build` rebuild: `19 passed · 2 failed` before the reorder →
`21 passed · 0 failed` after, on identical conditions.

### 2026-06-04 — feat(ov): generic config-driven builder — localpkg resolves its AUR-dep closure + the deploy-side format/builder/uninstall rendering reads `build.yml` host cells

Two converged changes landed as ONE generic-builder cutover, motivated by the
operator-workstation restore (Cutover 2 Part C). After #41 (below) made
`localpkg` install `ov` as the `opencharly-git` package, the live operator deploy
still failed at `pacman -U opencharly-git`: its AUR-only runtime deps
(`cloudflared-bin`, `gvisor-tap-vsock`) were unresolvable. The
`eval-cachyos-gpu-vm` bed had passed only by **topo luck** — `cachyos-extras`
pulled those deps before `ov-full`, so `pacman -U` succeeded without ever
exercising dependency resolution.

**#43 — localpkg resolves the built package's builder-resolvable dependency
closure.** After `buildLocalPkgOnHost` (`ov/localpkg.go`) builds the PKGBUILD via
makepkg, `resolveLocalPkgDeps` reads the built package's `.PKGINFO depends`
(`pkgInfoDepends`/`parsePkgInfoDepends`; version constraints stripped via
`stripDependConstraint`), intersects them with the host-foreign package set
(`hostForeignPkgs` running the format's `foreign_query` = `pacman -Qmq`) to find
the builder-only deps (`builderOnlyDeps`), builds THAT closure through the SAME
`aur` builder (`buildDepPkgsOnHost`), and installs the package + its closure onto
the target via the shared `transferAndInstallPkgs` (R3 — one transfer+install leg
shared with the aur builder). A localpkg install no longer relies on another
layer having pre-installed its AUR deps.

**#44 — the deploy-side format/builder/uninstall rendering converged onto the
config-driven `build.yml` machinery (the full generic builder workflow the
operator asked for: "make the local builder generic enough to reuse ANY builder
config and package format … easy to add additional kinds or package formats").**
Previously `ov/deploy_target_local.go` carried a hand-written
`renderFallbackPkgCmd` switch over rpm/deb/pac plus four per-builder renderers
(`renderPixiScript`, `renderNpmScript`, `renderCargoScript`, `renderAurScript`),
and `ov/deploy_target_vm.go` routed builders by NAME. All six are deleted
(~180 lines) and replaced by config-driven renderers that read the SAME
`build.yml` definitions the OCI build already consumed, differing only by phase
cell:
- `renderHostPackageCommand(distroCfg, step)` — renders the package install from
  the distro format's `phase.install.host` cell (`FormatDef.PhaseTemplate(phase,
  venue)` + `RenderTemplate`), the host-venue twin of the `container` cell.
- `renderBuilderScript(step)` — renders any builder (pixi/npm/cargo/aur) from its
  `phase.install.host` cell; `VmDeployTarget.execBuilder` now routes by OUTPUT
  SHAPE (no `LocalPkg` + a host cell ⇒ home-artifact builder), not by builder
  name.
- `reversePackageRemove` keeps its name but its hardcoded format-switch is
  replaced by `runScriptReverse` rendering each format's new `uninstall_template`
  (`FormatDef.UninstallTemplate` → `ReverseOp.UninstallCmd`, populated by
  `fillReverseUninstallCmds`).
- `localpkg` itself is config-driven: the new `format.<fmt>.local_pkg:` block in
  `build.yml` (`LocalPkgDef`: `pkg_glob`, `build_template`, `install_template`,
  `foreign_query`, `probe`, `dep_constraint_ops`, `dep_builder`) supplies every
  command, so adding a package format is a YAML-only change. `pac.local_pkg`
  wires makepkg + `pacman -U` + `pacman -Qmq` + the `aur` dep builder.

Net: build-mode (`OCITarget`) and deploy-mode (`LocalDeployTarget` /
`VmDeployTarget`) read the same `build.yml` format/builder definitions, differing
only by the `container` vs `host` phase cell — one machinery, no hardcoded
package-format or builder literals on the deploy side.

**Coverage + R10 (cross-repo B6).** A new dep-absent witness — `eval-cachyos-vm`
+ `ov-full` (NO `cachyos-extras`) — forces the closure resolution and asserts
`pacman -Q` for `opencharly-git` + `cloudflared-bin` + `gvisor-tap-vsock`; the
check FAILS without #43. Producer (main) verified live on `eval-pod` /
`eval-k3s-vm` / `eval-local` (the shared format-install + IR-walk machinery, all
PASS); the localpkg-closure + host-builder paths are gated by the consumer
(image/cachyos) R10 — `eval-cachyos-vm` (dep-absent localpkg) + `eval-cachyos-gpu-vm`
(the full format + builders + localpkg + nested-pod + GPU operator mirror) — run
against main's landed tag per the B6 producer→consumer order. The companion
`pkg/arch` `pkgver` is synced to the cutover's `ov` build; `opencharly-git`'s
`depends=` already declared `cloudflared-bin`/`gvisor-tap-vsock` (no new dep —
#43 automates the manual `yay -S` the PKGBUILD comment had documented).

**Follow-up in the same cutover — cachyos pacstrap emits a runtime
`/etc/pacman.conf` (RCA from the consumer R10).** The `eval-cachyos-vm` consumer
R10 FAILED at the `gocryptfs` pac install with `config file /etc/pacman.conf
could not be read`: the cachyos pacstrap bootstrap configures only the builder
container's pacman.conf for the install and leaves the booted guest without one,
so any `add_layer` pac install fails. Only `cachyos-gpu-vm` + `cachyos-gpu` had
worked around it, each carrying an identical hand-curated cloud-init
`write_files: /etc/pacman.conf` (a 2-way duplication this cutover would have made
3-way). Generic fix (R3, single source): a new per-distro
`PacstrapDef.RuntimePacmanConf` (`build.yml`
`distro.cachyos.pacstrap.runtime_pacman_conf`) carries the curated runtime config
verbatim, and the shared pacstrap bootstrap template writes it into
`{{.Target}}/etc/pacman.conf` after pacstrap when set; the per-VM `write_files`
blocks are deleted from both VM entities. The runtime config is deliberately
distinct from the install `extra_repo` (it excludes `cachyos-extra`, which serves
no usable DB at runtime). Covered by `TestCachyosRuntimePacmanConf` + the
re-run `eval-cachyos-vm` R10.

**Follow-up #2 in the same cutover — the dep-closure builder resolves a
namespace-qualified builder ref (RCA from the next consumer-R10 leg).** With
`/etc/pacman.conf` fixed, the consumer R10 reached #43's dep-closure step and
failed at `ensure-image "ov.arch-builder": ... not present locally, pull failed`:
the cachyos project's `aur` builder is the namespace-qualified `ov.arch-builder`
(main's `arch-builder` under the `ov` import namespace). RCA proved a
missing-plumbing defect — the shared `buildDepPkgsOnHost` (`ov/localpkg.go`)
called `BuilderRun` WITHOUT `Cfg`/`ProjectDir`, so `EnsureImagePresent` couldn't
run the namespace-aware `ResolveImage` that the aur-LAYER path
(`deploy_target_local.go`) already uses, and the namespaced ref never resolved to
the locally-present `ghcr.io/overthinkos/arch-builder:<tag>`. Fix (R3, reuse the
existing resolver), in two parts: (1) thread `*Config`+`projectDir` through
`buildDepPkgsOnHost` ← `resolveLocalPkgDeps` ← `execLocalPkgInstall`, and add
`Cfg`/`ProjectDir` fields to `VmDeployTarget` (populated from `dctx.Cfg`/`dctx.Dir`,
mirroring `LocalDeployTarget`), so `EnsureImagePresent` runs the namespace-aware
resolver; (2) `BuilderRun` then runs `podman run` against the RESOLVED concrete ref
(`resolveImageRefForEnsure`) — a namespace-qualified or short ref is not a podman
storage key, so `EnsureImagePresent` confirming presence was not enough on its own
(`podman run ov.arch-builder` still said `image not known`). `EnsureImagePresent`
is still called with the ORIGINAL ref so its pull / build-from-image.yml fallback
keeps working. No new resolution code. Proven by the re-run `eval-cachyos-vm` R10.

### 2026-06-04 — feat(ov): localpkg — Arch/CachyOS deploys install `ov` as the proper `opencharly-git` package, not a curl'd binary

Closes the `eval-cachyos-gpu-vm` coverage gap surfaced when the operator
workstation migration hit `EXIT:80 ("unexpected argument from-image")`: the
guest's `ov` was a stale curl'd `/usr/local/bin/charly` (the pinned
`v2026.141.1600` release, pre-`from-image`) from the charly layer's `cmd:`
fallback, shadowing any package binary on `$PATH`. RCA: on a VM/local Arch
deploy nothing installed the real package first, so the `cmd:` curled a
release.

Fix — a deploy-substrate-specific package mechanism mirroring
`apk:`/`ApkInstallStep`. A layer's `localpkg: <dir>` field (`ov/layers.go`)
compiles to a `LocalPkgInstallStep` (`ov/install_plan.go`) at "step 2.5" of
`BuildDeployPlan` (`ov/install_build.go`), BEFORE the layer's `cmd:`. On an
Arch/pac deploy target (`target: local` on a pac host, `target: vm` into a pac
guest) it builds the bundled `pkg/arch` PKGBUILD on the HOST via `makepkg -sf`
(`ov/localpkg.go` `runMakepkgOnHost`, PKGDEST temp) and `pacman -U`s the result
onto the target (`transferAndPacmanInstall` — the SHARED scp + `pacman -U` leg
the AUR builder also uses, R3). `resolveLocalPkgDir` walks up from the deploy
project dir, so a consumer nested under `image/<distro>` finds the superproject
`pkg/arch`. Skipped at image build (no `makepkg` in a container —
`build_target_oci.go`) and on any non-pac target. The charly layer's `cmd:` is now
pacman-aware: if `opencharly-git` is installed it does nothing (so `/usr/bin/charly`
is never shadowed by a `/usr/local/bin/charly` curl); else `/ctx/bin/charly`; else the
curl fallback (remote `@github` composition only). `opencharly-git` is
LOCAL-ONLY (`git+file://` source, not on the AUR), so the AUR builder cannot
build it — hence host-`makepkg`.

The same change hardened the host→guest charly delegation: `putHostOvInGuest`
(`ov/ov_install.go`) is the single host→guest ov-delivery primitive (R3), and
`deployNestedPodsInGuest` uses the host's own current, from-image-capable charly
for the `charly deploy from-image` delegation — never the guest's PATH ov. The
`eval-cachyos-gpu-vm` bed gained `ov-full` in `add_layer` + a
`gpu-ov-proper-package` deploy check (`pacman -Q opencharly-git` &&
`command -v ov` == `/usr/bin/charly` && `charly deploy from-image --help`) that FAILS
on the old curl path. Cross-repo B6: the producer (`layers/ov` + `ov/*.go`,
pkg/arch docs) landed + tagged first, the cachyos `@github` pins reconciled to
the tag, then the bed R10 against the pushed tag is the gate.

### 2026-06-04 — feat(ov): per-host VM device overlay — host-specific GPU `<hostdev>` + shares move to the home `instance.yml`

The committed `image/cachyos` `vm.yml` hardcoded this host's NVIDIA PCI
`<hostdev>` (01:00.0 + .1) and `/home/atrawog` virtiofs shares for both the
`cachyos-gpu` operator workstation and the `eval-cachyos-gpu-vm` bed — a PCI
address + an operator-home path baked into version control. `VmInstanceOverride`
(`ov/vm_instance_override.go`) gained a `libvirt: *LibvirtDomain` overlay field
+ `ApplyToVmSpec(spec)`: `runVmSpecCreate` loads
`~/.local/share/ov/vm/<domain>/instance.yml` and merges its `devices.hostdevs`
+ `devices.filesystems` onto the `VmSpec` before `RenderDomainXML`.
Host-specific device config now lives ONLY in the per-host home overlay
(outside any git repo); the `kind: vm` entities are portable (no PCI address,
no operator-home path committed). The overlay reuses the `kind: vm` `libvirt:`
schema, so the block is identical to what `vm.yml` would carry. Proven on the
`eval-cachyos-gpu-vm` R10 (9/9 steps incl. the fresh-rebuild leg): the portable
`vm.yml` + the home overlay reached real GPU passthrough (the guest enumerated
the RTX 4080) + the nested selkies-kde NVENC stream, and the preempted operator
was restored.

### 2026-06-03 — fix(build)!: deterministic CalVer — `calver.sh` derives the build identity from the HEAD commit, never the wall clock

Completes #31. That change fixed the *runtime* readout — `OvVersion()` returns
the stamped `BuildCalVer` or `"unknown"`, never the clock at invocation — but
left the *build-time* stamp half-fixed: `pkg/arch/calver.sh` still wall-clocked a
**dirty** working tree (`else` branch → `date -u`), justified as "successive dev
rebuilds are monotonic." That residual wall clock was the real disease, and it
manifested two ways on the operator's `cachyos-gpu` workstation:

1. **Two builds of one commit disagreed.** `task build:ov` from a dirty tree
   stamped `bin/charly` with the wall clock (e.g. `2026.154.1835`), while the
   PKGBUILD's `pkgver()` ran `ov_calver` in a clean `git+file://` clone and got
   the commit date (`2026.154.1250`) — so `pacman -Q opencharly-git` and
   `charly version` reported *different* versions for the *same* installed binary.
   A deeper layer of the same defect: with `makepkg -e --noextract` the clone is
   never created, so `pkgver()` fell through to its cwd (`pkg/arch/src`), which
   sits **inside the `pkg/arch` submodule** — `git log` there resolved to the
   *submodule's* HEAD, a different commit again. The version was being derived
   from whichever git repo the cwd happened to land in.

2. **A stale guest charly falsely sorted "newer."** The operator VM's
   `/usr/local/bin/charly` was a pre-#31 binary (installed by the `ov` layer at an
   earlier deploy, when `layers/ov/bin/charly` was stale) whose old `OvVersion()`
   read the wall clock at *invocation* — so it reported an ever-advancing version
   that always beat the host's. `syncOvIntoGuest`'s never-downgrade rule trusted
   that fake "newer" number and kept the stale guest charly (which lacked
   `charly deploy from-image`), so the nested-pod-in-VM deploy could not delegate to
   the guest. A first attempt papered over this with a "trust marker"
   capability-probe gate in `syncOvIntoGuest` — a band-aid on the *comparison*
   that left the dishonest version in place. That gate was reverted; the version
   reporting itself is the fix.

**The fix is one rule applied at the single source of truth.** `ov_calver`
(`pkg/arch/calver.sh`, shared R3 by `taskfiles/Build.yml` and the PKGBUILD) now
derives the CalVer **only** from the HEAD commit's UTC date — identical for a
clean tree and a dirty tree at the same commit, and a hard error outside a git
work tree (never a silent wall-clock fallback). The PKGBUILD's `pkgver()` no
longer re-derives the version at all in the dev path: it reads the pre-built
binary's own `charly version` (the same `bin/charly` `build()` installs), so the pacman
package version equals the installed binary's identity **by construction** —
there is no parallel derivation that can drift. After the fix, `bin/charly`,
`layers/ov/bin/charly`, `/usr/bin/charly`, and `pacman -Q opencharly-git` all report one
identical CalVer for a given commit. Because every guest charly is reinstalled
unconditionally by the `ov` layer (`install /ctx/bin/charly /usr/local/bin/charly`) on
each deploy, a re-deploy delivers a current, honestly-versioned, `from-image`-
capable binary, and `syncOvIntoGuest`'s plain comparison is correct with no
trust gate. Eval-coverage: `ov/calver_script_test.go` execs `calver.sh` in a
hermetic temp git repo and asserts a dirty (modified-tracked) tree yields the
same commit-date CalVer as a clean one — it fails against the old wall-clock
branch. R10: build-determinism verified across all four artifacts +
`eval-pod` / `eval-local` / `eval-k3s-vm` (the VM bed exercises
`EnsureOvInGuest` → `syncOvIntoGuest` freshness with the new commit-date
versions).

### 2026-06-03 — refactor(ov)!: `charly deploy add`/`del` join the unified `ResolveTarget` dispatch (no per-kind divergence) + `charly update` obeys explicit invocation

Follow-on to the same-day `charly update` unification (next entry). That change
unified the `update` verb but left `charly deploy add`/`del` on five per-kind `run*`
helpers (`runLocal` / `runVM` / `runContainer` / `runK8s` / `runAndroid` + their
`*Del` siblings) behind a `switch target`, while the `UnifiedDeployTarget`
abstraction sat half-finished (a documented "Phase 2 / Phase 3" split — shallow
`Add` stubs that only called `Emit`, `ResolveTarget` returning nil-embedded
targets). That coexistence is exactly the half-migrated state the cutover policy
forbids, and it bred a real divergence-class bug: `runVM`, `runContainer`, and
`runK8s` re-read the deploy node from disk (operator `deploy.yml` only, NOT the
project+operator field-merge `resolveTreeRoot` produces), so a project-declared
`nested:` block was silently dropped when the operator's per-host entry omitted
it — the operator `cachyos-gpu` workstation never got its nested pod even though
the project config declared one (eval beds, having no operator overlay, were
unaffected — which is why the bug hid behind a green bed).

Fix: `charly deploy add`/`del` now route through `ResolveTarget(node, name)` →
`target.Add(ctx, dctx, plans, opts)` / `target.Del(ctx, opts)` for EVERY kind —
no per-kind switch. Each `*UnifiedTarget.Add` constructs its live embedded target
(the lifted `run*` body) and consumes the **dispatch-merged** `dctx.Node`, so the
node-divergence bug class is structurally impossible: there is exactly one place
the merged node enters each kind's deploy. The duplicated secret-injection /
artifact-env / artifact-retrieval / ephemeral-register logic is hoisted to shared
helpers (`prepareLayerSecrets` / `buildArtifactEnv` / `retrieveArtifactsAndK3s` /
`registerEphemeralIfMarked`, ordering preserved: secrets injected before `Emit`).
A new `AndroidUnifiedTarget` (filename `unified_targets_apk.go` — a `_android.go`
suffix is a GOOS build constraint that silently excludes the file) carries the
android deploy. Deleted: `runLocal`, `runVM`, `runContainer`, `runK8s`,
`runAndroid`, `runLocalDel`, `runContainerDel`, `runVmDel`, `runK8sDel`,
`runAndroidDel`, `resolveDelTargetKind`, `ErrNotYetImplemented`, and the two
dispatch switches. Net −213 lines. The legacy no-`deploy.yml`-entry paths
(`charly deploy add host ./x.yml`, `charly deploy del vm:<name>`) are preserved by
synthesizing a node from the classified target.

**`charly update` obeys explicit invocation on ANY target (#30):** `charly update` no
longer REFUSES a non-`disposable: true` target. The `disposable:` flag is the
authorization for the AI's AUTONOMOUS destroy + rebuild and the eval-runner's
unattended fresh-rebuild — NOT a capability limit on a human-driven verb.
`checkUpdateDisposable` (which refused) became `noteUpdateDisposability` (prints a
one-line transparency note naming the deploy key + lifecycle, then proceeds).

**Two defects R10 surfaced (fixed in-tree, R2):**

- *k3s cluster-name regression (this cutover).* `VmUnifiedTarget.Add` passed the
  deploy KEY to `retrieveArtifactsAndK3s`/`K3sPostProvision`, so a VM-hosted k3s
  cluster's `ClusterProfile` landed under the bed/deploy name instead of the
  VM-entity name `vm-<entity>` that `cluster:` refs use (e.g. `eval-k3s-vm`'s
  `cluster: "vm-k3s-vm"`). The fresh kubeconfig went to the wrong profile, the
  probe used a stale same-port profile, and every k8s check failed `x509:
  certificate signed by unknown authority`. Fixed: pass `"vm:"+vmName` — a k3s
  cluster in a VM is identified by that VM (one cluster per VM), so its profile +
  artifact cache key on `vm-<entity>`.

- *android `eval-live-device` (pre-existing `e740430`, surfaced once this cutover
  fixed the earlier failure that masked it).* `bedEvalLiveRefs` emitted a per-child
  `charly eval live <parent>.<child>` hop for EVERY nested child, but `EvalLiveCmd.Run`
  has no android dotted-path branch, so a `target: android` child resolved to a
  non-existent `ov-<parent>.device` container. Fixed: `bedEvalLiveRefs` skips
  `target: android` children — they share the parent pod's venue and are verified
  by the parent's baked `android-emulator-layer` checks, never a separate hop.
  Added the android-child test case `e740430` lacked.

**Part C (operator `cachyos-gpu` workstation → nested `selkies-kde-nvidia` pod)
is the immediate-next cutover, NOT part of this one.** The disposable
`eval-cachyos-gpu-vm` bed (fresh VM, fresh guest ov) is the R10 gate that PROVES
the nested-pod-on-GPU mechanism this unification enables (nested `selkies-kde`
32/0/0, `NVENC active`, survives fresh rebuild). The LIVE operator workstation
migration is blocked on a SEPARATE, orthogonal `cachyos-gpu`-VM ov-provisioning
issue: that VM's guest charly is rebuilt from a stale in-guest source on each deploy
(no `charly deploy from-image`, advancing build-time CalVer), so the in-guest
nested-pod deploy fails `unexpected argument from-image`, and `charly update --build`
(which rebuilds the cloud_image VM disk, not the guest ov's source) does not
refresh it. The guest-ov-provisioning fix + the live workstation migration are
the next cutover.

R10: all five `kind: eval` bed kinds green on the final binary (charly
`2026.154.1647`) — `eval-pod` (pod), `eval-local` (local), `eval-k3s-vm`
(vm+k8s), `eval-android-emulator-pod` (android), `eval-cachyos-gpu-vm`
(vm+nested-pod, real NVENC). Process lessons re-learned: editing `ov/*.go` mid-bed-run
trips the stale-binary freshness guard (R9) and aborts in-flight beds' `eval
live` (Go must be frozen for the bed phase), and a `set -e` block on the left of
`&&` has its `set -e` suppressed (check exit codes explicitly).

### 2026-06-03 — refactor(ov)!: `charly update` is one unified codepath for every kind (no per-kind divergence)

RCA found `charly update` did NOT use the unified `LifecycleTarget.Rebuild`
interface uniformly: `dispatchByDeployTarget` had a per-kind `switch` where vm
and local called thin wrappers (`updateVmDeploy` / `updateLocalDeploy`) that
constructed the target by hand and called `Rebuild`, **pod ran a wholly
separate ~180-line bespoke path** (`updatePodDeploy` + `updatePodDeployQuadlet`
+ `updatePodDeployDirect`: image pull/build → `bumpDeployAlias` → surgical
quadlet `Image=` rewrite), and k8s returned an ad-hoc error. So pod-update had
**two** implementations (the bespoke one *and* the unused
`PodUnifiedTarget.Rebuild`) and each kind behaved differently.

Fix: `charly update` now resolves the node through `ResolveTarget` and calls
`LifecycleTarget.Rebuild(RebuildOpts{RebuildImage: c.Build})` for EVERY kind —
one codepath, no per-kind branching. `Rebuild`'s unified contract is **"redeploy
the current artifact + restart; with `--build`, rebuild the artifact first"**,
realized per substrate (vm: destroy→create the domain, reuse disk unless
`--build`; pod: `deploy add → config → start`, `--build` rebuilds the image;
local: re-apply layers). k8s is deliberately NOT a `LifecycleTarget` (it is
applied out-of-band via `kubectl apply -k`), so `charly update <k8s>` falls out with
one clear error instead of a hand-written branch. Deleted: `updatePodDeploy`,
`updatePodDeployQuadlet`, `updatePodDeployDirect`, `quadletPathForDeploy`,
`updateDirectMarkerImageRef`, `updateVmDeploy`, `updateLocalDeploy` (the shared
helpers `bumpDeployAlias` / `rewriteQuadletImageLine` / `extractQuadletImageLine`
/ `tagPart` stay — `charly config` still uses them).

**Behavior change (`!`):** `charly update <pod>` no longer AUTO-PULLS the latest
registry image (the old pod-only `= charly deploy add --pull` behavior). It now
redeploys the current local image — consistent with vm's reuse-disk default. To
advance a pod to a newer image: `charly image pull <ref>` then `charly update`, or
`charly update --build` to rebuild locally. The per-kind auto-pull was exactly the
"behaves differently for every kind" divergence this removes.

### 2026-06-03 — fix(ov): `charly version` is a stamped build identity, not a wall clock; CalVer-based nested-charly freshness

While finishing Cutover 2 Part C (migrating the `cachyos-gpu` workstation to a
nested `selkies-kde` pod), the nested-pod deploy failed because the guest's
`/usr/local/bin/charly` lacked `deploy from-image` while the host's had it — yet
`charly version` reported the SAME CalVer (`2026.154.956`) on both. The first RCA
attempt ("two builds in the same minute") was wrong.

**Real root cause.** `charly version` called `ComputeCalVer()` →
`ComputeCalVerAt(time.Now().UTC())` — it formatted the **current wall-clock
time at the moment of invocation**, with ZERO connection to the binary. The
"matching" CalVer was simply two `charly version` invocations (host + guest) landing
in the same UTC minute; the two binaries were entirely different builds. A
content checksum was briefly used as a freshness signal and then removed: a
checksum can say "different" but never "newer", so it cannot tell a stale venue
charly from one legitimately AHEAD of the host — useless for deciding which to keep.

**The fix (head-on, not a band-aid).**
- `ov/version.go`: new `var BuildCalVer string`, injected at compile time via
  `-ldflags "-X main.BuildCalVer=<calver>"`. `OvVersion()` returns it (or
  `"unknown"` for an unstamped `go build`/`go test`, never the clock).
  `VersionCmd.Run`, the MCP client (`mcp.go`) and server (`mcp_server.go`) now
  report `OvVersion()`. The remaining `ComputeCalVer()` callers are all "tag an
  artifact created NOW" (image build tag, eval-run dir, deploy alias) and keep
  the wall clock correctly.
- The stamp is derived from the **git commit date** (build-time fallback when
  the tree is dirty / non-git) by `pkg/arch/calver.sh` — ONE shared bash impl
  (R3) sourced by both `pkg/arch/PKGBUILD` (pkgver + the source-build ldflag)
  and `taskfiles/Build.yml`'s `build:ov`. So `charly version` == `pacman -Q
  opencharly-git` == a reproducible, monotonic identity. (Go's embedded
  `vcs.time` was rejected by RDD: in the git-worktree layout it was stuck at a
  stale `2025-08-15` revision across rebuilds — useless. Explicit ldflags are
  deterministic.)
- `ov/version.go` `hostOvIsNewer(hostVer, venueVerOut)`: the single CalVer
  arbiter (R3) shared by both ov-into-venue paths. Unparseable/absent venue →
  host wins; venue equal-or-newer → keep the venue charly (NEVER downgrade an charly
  that is ahead of the host); unparseable host → don't clobber on an unprovable
  claim.
- `ov/ov_install.go` `syncOvIntoGuest` is now the SINGLE host→guest charly resolver
  (R3) — used by BOTH EnsureOvInGuest's auto/scp strategy AND the host→nested
  delegation in `ov/deploy_add_cmd_vm.go` `deployNestedPodsInGuest` (the old
  `installOvViaSCP` + the separate `ensureFreshNestedOv` were merged). It honors
  the operator's model exactly: the guest's SYSTEM charly (the PATH `ov`, normally
  the pacman `/usr/bin/charly` kept current by `pacman -Syu`) is used as-is whenever
  it is at least as new as the host's — NEVER shadowed, NEVER downgraded, NEVER
  overwritten. ONLY when the guest's charly is **absent or older** (by CalVer) does
  the host scp its own binary — to **`/tmp/ov-<calver>` (outside `$PATH`),
  invoked by explicit path** — so a host driving a deploy with newer code runs
  that code without clobbering the package-managed ov. (The earlier draft wrote
  `/usr/local/bin/charly`, which sits ahead of `/usr/bin/charly` on PATH and would shadow
  a pacman charly forever; the scp is a dev crutch, not the update mechanism —
  routine updates are `pacman -Syu`'s job. A briefly-tried content checksum was
  removed: it can say "different" but never "newer".)

Coverage: `ov/version_test.go` (`OvVersion`, `hostOvIsNewer` incl. the
pod-newer-than-host no-downgrade case) + `ov/ov_install_test.go`
(`syncOvIntoGuest`: system-ov-current → no scp; absent/older → `/tmp` copy;
never writes `/usr/local/bin/charly`). The `eval-cachyos-gpu-vm` bed exercises both
branches live (the baked guest charly is older than the freshly-stamped host →
`/tmp` copy drives the nested `charly deploy from-image`).

**`charly update` config-clobber + preempt-precedence RCA (same cutover).** The bed
first failed at `vm-create` — `PCI 0000:01:00.0 is in use by domain
ov-cachyos-gpu` — because the running operator workstation held the GPU and was
NOT preempted. The operator's `cachyos-gpu` had been `preemptible` previously;
its per-host `~/.config/charly/deploy.yml` entry (`preemptible:` is a PER-HOST LOCAL
DEPLOY property — it depends on this host's single GPU shared with the beds —
never a committed image/vm property) had been silently dropped. Two root-cause
bugs, both fixed (with regression tests in `ov/deploy_preserve_test.go`):

- **`charly update <vm>` clobbered the per-host entry.** `VmUnifiedTarget.Rebuild`
  shells `charly vm destroy` then `charly vm create`; `charly vm destroy`'s
  `removeVmDeployEntry` did `delete(dc.Deploy, name)` — wiping the WHOLE entry,
  then `charly vm create` re-stamped a fresh `Target`/`Vm`/`VmState`-only one. So a
  destroy→create cycle dropped every operator-authored per-host field
  (`preemptible`, `env`, `tunnel`, …). Fix: `removeVmDeployEntry` now clears only
  the runtime `vm_state` and KEEPS the entry whenever any operator-authored
  field remains (`isAutoVmDeployEntry`); it deletes the entry only when nothing
  but the auto-set `target: vm`/`vm:` remains (so disposable bed VM entries still
  don't accumulate).
- **The preempt arbiter ignored the per-host overlay.** `gatherDeployNodes`
  loaded per-host first then let the committed project node WHOLESALE-OVERWRITE
  it, so a per-host `preemptible` never reached the arbiter. Fix: project is the
  base and the per-host overlay is merged ON TOP (`MergeDeploymentNode`,
  per-host wins) — the same overlay precedence the rest of deploy resolution
  uses.

The operator's `preemptible: {holds: [nvidia-gpu]}` is restored in the per-host
`~/.config/charly/deploy.yml` (with `target: vm`/`vm:` so the overlay validates
standalone) — NOT committed (committing it would make preempt the default for
every host that deploys `cachyos-gpu`, which is wrong).

### 2026-06-03 — fix(selkies,build,ov): real NVENC actually compiles + runs; build-system cache correctness; nested-pod eval

Continuation of Cutover 2 (below). The 2026-06-02 work fixed *capture* (the
`WLR_BACKENDS=headless` compositor fix) but the GPU stream still encoded on CPU
x264 — pixelflux's NVENC init failed. RDD on the RTX-4080 bed drove this session's
fixes, several of which were caught by the bed itself before landing:

- **pixelflux NVENC init fixed (the headline).** pixelflux's real `NvencEncoder`
  fetched its encode config with the DEPRECATED `NV_ENC_PRESET_LOW_LATENCY_HQ_GUID`
  via the legacy `nvEncGetEncodePresetConfig` AND discarded the return value; on the
  Ada-class RTX 4080 SUPER (driver 610.x) that preset yielded a zeroed config and
  `nvEncInitializeEncoder` rejected it → silent CPU fallback (`Failed to init NVENC
  … Falling back to CPU`). `ffmpeg -c:v h264_nvenc` encoded fine in the same pod,
  isolating it to pixelflux's encoder build. Fix (`layers/selkies/build.sh` Patch
  2d, `PIXELFLUX_NVENC=1` path): the modern `nvEncGetEncodePresetConfigEx(P4,
  LOW_LATENCY)` + init `tuningInfo`, with both `NVENCSTATUS` returns CHECKED and
  surfaced. `nvenc-sys` (NVENCAPI 11.1) already exposed the Ex fn + P-preset GUIDs +
  tuningInfo — no dep bump. Proven on `eval-cachyos-gpu-vm`: the hardened
  `selkies-encoder-active` check (hard-fails on a CPU fallback on a 0x10de node)
  FAILED on the old pixelflux and PASSES on the Patch-2d build, same bed/GPU.

- **build-system cache-correctness bug (why Patch 2d at first did nothing).** The
  pixi builder `stage_template` delivered `build.sh` via
  `--mount=type=bind,from=<layer>,source=/build.sh` — a bind-mount whose content is
  NOT part of the RUN's BuildKit cache key. So editing `build.sh` (Patch 2d) never
  invalidated the pixelflux compile; the stage cache-hit a stale pre-patch artifact
  and "new code was silently not picked up." Fix (`build.yml`): COPY `build.sh` into
  the stage (content-addressed, cache-keyed) like `pixi.toml`/`pixi.lock`. Now any
  `build.sh` change busts the compile across every pixi+build.sh image. The deeper
  smell — inline str-replace source patches in `build.sh` are fragile — is noted as
  a follow-up: carry the patches as commits on the `overthinkos/pixelflux` fork and
  clone the patched SHA, rather than str-replace anchors.

- **AUR builder stale-DB 404 (both surfaces, R3).** `yay` resolved a makedepend
  (`go`) to a version the mirror had rotated out (`go-2:1.26.3-1…sig` 404; mirror
  served `1.26.4-1`) because the builder image bakes a stale `pacman -Sy` DB. Fix:
  `pacman -Syu` before makedepend resolution in BOTH the OCI `aur` `stage_template`
  (`build.yml`) and the host/VM `renderAurScript` (`ov/deploy_target_local.go`) —
  the two parallel AUR-build surfaces.

- **Nested-pod eval (`charly eval run` now evaluates nested children).** `charly eval run
  <vm-bed>` deployed `nested: {child: target:pod}` children but never EVALUATED them.
  `eval_bed_run.go` (`evalLiveTree`/`bedEvalLiveRefs`) now eval-lives the substrate
  AND each nested child on both the initial and fresh-rebuild passes; `eval_cmd.go`
  `runVm` runs the nested POD image's baked layer/image eval through the chain, with
  `${HOME}`/`${USER}` + `package_map:` distro resolved from the POD image's metadata
  (not the VM guest's), and SKIPS host-side protocol verbs (cdp/wl/dbus/vnc/mcp) that
  can't reach an in-guest container; `deploy_chain.go` targets the in-guest LEAF
  container name (`ov-<childKey>`, what `deployNestedPodsInGuest` deploys) for a
  VM→pod hop, not the host-side `ov-<parent>_<child>` flatPath. This let the GPU
  bed's selkies-kde pod be tested by the selkies layer's OWN baked checks (R3) —
  retiring the cachyos bed's hand-rolled guest-side probes (incl. a false-green
  `nested-selkies-kde-encoder`).

- **RCA notes (no blind retries).** Two infra failures on the bed were RCA'd to
  external/transient causes with positive evidence, not papered over: a corrupt
  `cachyos.db` download (host keyring + a concurrent sibling bed both verified the
  same DB as validly signed; pacman correctly rejected the bad copy; clean re-run
  passed) and a stranded preempt-lease (from a manual `charly vm destroy` outside the
  arbiter; the arbiter restored-then-couldn't-restop a mid-boot VM in 3m; resolved
  by a clean stop, ~10s once booted).

### 2026-06-02 — feat(vm,selkies): persistent nested-pod-in-VM + real GPU NVENC selkies stream

**Cutover 2.** A `target: vm` deploy's `nested: {child: target:pod}` now deploys
the child as a PERSISTENT in-guest quadlet (host-build → `charly vm cp-image
--rootless` → guest `charly deploy from-image <ref> <name>` under the guest's systemd
with linger), so it survives a guest reboot — replacing the transient cp-image
path. The `cachyos-gpu` workstation's KDE-selkies stream is intended to move from
in-guest systemd layers to a nested `selkies-kde-nvidia` pod; `eval-cachyos-gpu-vm`
proves the mechanism on a VFIO-GPU-passthrough bed.

**The GPU NVENC stream actually works now.** RDD on the real RTX-4080 bed exposed
that the nested selkies pod never produced video: pixelflux (a headless wlroots
capture compositor) was auto-selecting the DRM/KMS backend and failing
`DRM_IOCTL_MODE_CREATE_DUMB: Permission denied` on the seat's `card0` (a rootless
pod can't own it) → no compositor → a silent software-x264 default. Fix:
`selkies-wrapper` forces `WLR_BACKENDS=headless` (render off-screen via the render
node), after which the compositor starts and NVENC initializes. `ov/devices.go`
DRINODE auto-detect now prefers a real-GPU render node (NVIDIA/AMD/Intel) over the
paravirtual virtio-gpu, so on a passthrough VM it picks NVIDIA `renderD129`, not
virtio `renderD128`.

**No more silent/black encode (eval beef-up).** `nested-selkies-kde-encoder`
asserts NVENC was *selected* (not a silent x264 fallback) from the selkies log;
the new `selkies-frame-not-black` captures a frame via pixelflux and asserts a
non-uniform luma spread (ffmpeg `signalstats`) so a black/broken stream fails the
bed instead of passing.

**`stdout:` service-logging field implemented for supervisord.** The documented
`stdout: file:<path>|none|journal` service field was wired only for systemd; the
supervisord template hardcoded `stdout_logfile=/dev/fd/1`. Now `supervisordLog` +
`supervisordLogMaxbytes` render a dedicated rotating log when set (default
unchanged for every other service). The selkies capture-server writes
`~/.local/share/selkies/selkies.log`, so capture/encoder failures are tailable —
which is how the KMS failure above was finally diagnosed.

**Portability + cleanup.** `ov` is now near-static: the Shells-com/spice audio
channels (its only opus/portaudio cgo consumers) are build-tagged off by default
(`-tags spice_audio` to re-enable), so a plain build links no audio libs and runs
on any glibc host. `pkg/arch` PKGBUILD drops the now-optional deps (opusfile,
portaudio, libsecret, dmidecode, openbsd-netcat, go-task) to `optdepends`, and
`charly secrets gpg` doctor hints no longer imply libsecret is required (charly speaks the
Secret Service via the pure-Go go-keyring client). Rootless pacstrap/debootstrap
rootfs builds preserve file capabilities (`tar --xattrs-include='*'`) so
`newuidmap`/`newgidmap` keep `cap_setuid` and rootless podman works in the guest.
The `selkies-vaapi-encode` eval check no longer silently SKIPS (`${DRINODE:-…}`
was an unsupported bash-default the eval resolver couldn't parse — now braceless
`$DRINODE`) and HARD-FAILS on an AMD/Intel render node without VAAPI H264 encode.

### 2026-06-02 — fix(vm): `charly vm destroy` removes the deploy.yml entry (+ idempotent)

`charly vm destroy <name>` now removes the deploy.yml `vm:<name>` entry — the inverse
of the `saveVmDeployState` that `charly deploy add vm:` (and the `ssh.port_auto`
vm-create persist) write — and is idempotent: a `lookupDomain` miss is no longer
fatal, so a config whose libvirt domain is already gone is STILL cleaned
(previously the entry could never be removed once the domain was destroyed).
`--keep-deploy` preserves the entry for a deliberate re-create, mirroring
`charly remove --keep-deploy` for pods.

This closes a deploy-lifecycle gap: disposable eval-bed VM entries accumulated in
deploy.yml because the bed cleanup tears down via `charly vm destroy`, which destroyed
the domain but left `vm:<name>` lingering. Pod/local beds were already clean
(`charly remove` removes the entry on teardown), and deploy-add saves uniformly for
every target kind — so with this fix ALL deployment configs (including eval beds)
are both saved on add and removed on teardown, symmetrically. Pre-fix bed entries
self-heal on their next run; they are not scrubbed unattended (a deploy.yml
`vm_state` record carries no `disposable:` marker — that authorization lives on
the `kind: eval` bed — so a blind sweep can't prove an entry disposable).

R10: the `eval-k3s-vm` full-lifecycle bed (disposable libvirt VM) — `deploy-add`
saved `vm:k3s-vm`, `cleanup` ran `charly vm destroy k3s-vm`, and `vm:k3s-vm` was gone
from deploy.yml afterward (count 1→0); all 6 steps `ok: true`, exit 0. The
idempotent path was proven separately: `charly vm destroy k3s-vm` against an
already-destroyed domain printed "already destroyed (or not defined)" and still
removed the entry. Go unit test `TestRemoveVmDeployEntry_SelectiveAndIdempotent`
pins the primitive: selective removal (a sibling preemptible operator-workstation
entry + an unrelated pod deploy both survive) + idempotent re-removal.

### 2026-06-02 — feat(android): resolve ${HOST_PORT:N} in a nested endpoint device's adb host

A `kind: android` `adb:` endpoint device nested under an emulator pod can address
its parent's published adb port dynamically via `adb.host: 127.0.0.1:${HOST_PORT:5037}`
instead of hard-coding the host port. `resolveAndroidDevice` substitutes a single
`${HOST_PORT:N}` token with the parent pod's host-mapped port for container port N
(read from NetworkSettings.Ports via the same `findHostPort` the image-device
branch uses — R3; the parent pod is derived from the deploy path). Literal
host:port endpoints pass through unchanged; a non-nested device or an unpublished
port errors clearly.

R10: `eval-android-emulator-pod` — the device-net leg (pixel9a-endpoint,
`adb.host: 127.0.0.1:${HOST_PORT:5037}`) resolved to the emulator pod's published
adb port and installed the committed ApiDemos over it (an unresolved token would
have failed the adb connect); eval-live 113/0; fresh-rebuild leg passed
(summary.yml ok:true). Go unit test covers the parse paths (passthrough,
no-parent, malformed, no-brace).

### 2026-06-02 — feat(vm): ssh.port_auto — auto-allocate the VM SSH host port

**Additive, no schema-version bump** (same class as `autostart`). A `kind: vm`
entity can set `ssh.port_auto: true` to auto-allocate a free host port for the
SSH forward at `charly vm create` (persisted in `vm_state.ssh_port`, reused on
rebuild — idempotent) instead of a fixed `ssh.port`. Mutually exclusive with
`ssh.port`. Lets concurrent VM beds avoid fixed host-port collisions, mirroring
the pod path's `port: [auto]`.

`resolveVmSshPort` gains a `vmName` parameter + an error return: on `port_auto`
it reuses the persisted port if present, else allocates via the shared
`AllocateAutoPorts` (R3 — the same probe loop the pod path uses). It stays PURE
(no side effects); **vm-create persists** the resolved auto-port so deploy-add's
reachability probe + every later read reuse THIS exact port (an early bug had
deploy-add re-allocate a different port, find the VM unreachable, and auto-boot
into an "already exists" — RCA in the eval-k3s-vm run). All three call sites
thread through it (vm-create, `deploy add vm:`, eval-live); `validateVmSSH`
rejects `port` + `port_auto` set together.

R10: `eval-k3s-vm` (disposable Arch-cloud VM, converted to `ssh.port_auto`) —
the host SSH port auto-allocated to a fresh ephemeral 38067 (persisted at
vm-create), and the entire k3s-server deploy + 21 k8s probes
(nodes/coredns/traefik/local-path/svclb Ready) ran over it; the fresh-rebuild
`update` leg + teardown passed (summary.yml ok:true, 6 steps). Go unit tests
(`vm_ssh_port_test.go`) cover the three resolution paths + the mutual-exclusion
validation.

### 2026-06-02 — feat(selkies): GPU matrix — selkies-{labwc,kde}-nvidia (real NVENC) + selkies-kde relocation

**Breaking (image rename), no schema-version bump.** Completes the selkies
flavor split (see the entry below) with the GPU-accelerated row of the matrix and
relocates the KDE flavor to its canonical home.

**Images (image/cachyos).** Adds `selkies-labwc-nvidia` (renamed from
`selkies-desktop-nvidia`) + the NEW `selkies-kde-nvidia`, both on the
`cachyos.nvidia` GPU base with REAL NVENC: `builder.pixi: ov.cuda-arch-builder`,
whose nvcc + ffnvcodec NVENC headers (the `nvenc-headers` layer) let the selkies
`build.sh` compile pixelflux's real `nvenc-sys` `NvencEncoder` instead of the
AMD/CPU stub. Relocates `selkies-kde` from main's test vehicle to image/cachyos
(its canonical home alongside the cachyos base); removes main's `selkies-kde`
image + `eval-selkies-kde-pod` bed (R3 dedup). The `selkies-kde-desktop` layer
stays in main (image/cachyos fetches it cross-repo via `@github`).

**Cross-repo enabling fix — cuda-arch-builder resolves cross-repo.** main's
`cuda-arch-builder` (base.yml) referenced its `cuda` + `nvenc-headers` layers by
BARE name, so it failed to resolve when image/cachyos pulled it as a pixi builder
(`ov.cuda-arch-builder` → "unknown layer cuda"). `@github`-qualified them (like
`arch-builder`'s own layers); within main they shadow to the local layers so
main's own build is byte-unchanged.

**Resolver fix — collect builder layers via the effective builder.**
`CollectRemoteRefsOpts.collectImage` followed the raw `img.Builder` edge, missing
layers reachable only via the `defaults.builder` / distro-keyed effective builder
(an "unknown layer" at generate time). Now resolves via the canonical
`effectiveBuilderForImage`. Added `validateImageDAG` GlobalLayerOrder coverage +
a base-cycle guard in `collectAllImageLayers`, and a regression test.

**NVENC eval-coverage (regression-proof).** Both -nvidia images carry a
build-scope `pixelflux-nvenc-compiled` check: `strings pixelflux_wayland*.so |
grep -q NvEncodeAPICreateInstance` — the NVENC SDK entry point the real encoder
binds, absent from the stub build (which patches `nvenc-sys` out of pixelflux's
Cargo.toml). The check FAILS on a non-NVENC build, so it proves the image's NVENC
functionality rather than merely the presence of a GPU.

**vaapi-encode eval factored into the selkies layer (R3).** The VAAPI H264
encode-capability probe (duplicated across the per-flavor pod beds) moved into
the selkies layer's deploy-scope eval (`selkies-vaapi-encode`); every selkies
image inherits it once via the baked `ai.opencharly.eval` label.

**R10 (all green).**
- `eval-selkies-kde-pod` (AMD host-pod, the relocated selkies-kde): 8/8, the
  layer VAAPI probe ran live on the host iGPU.
- `charly eval image selkies-labwc-nvidia`: 88/88 build-scope, incl. the
  `pixelflux-nvenc-compiled` check; `charly eval image selkies-kde-nvidia`: 82/82.
- `eval-selkies-labwc-nvidia-vm` (RTX-4080 passthrough VM): 11 steps incl. the
  fresh-rebuild leg — the real-NvencEncoder image built, cp-imaged into the
  guest, deployed + streamed on the passed-through GPU.
- `eval-selkies-kde-nvidia-vm` (KDE Plasma + NVENC in the passthrough VM): 11
  steps incl. the fresh-rebuild leg.
- For both VM beds the resource arbiter preempted the operator's `vm:cachyos-gpu`
  workstation to free `nvidia-gpu`, ran the bed, and restored the workstation
  (`restore: always`). The RTX-4080 hostdev was added to the disposable bed VM
  locally (uncommitted) for the live leg, per the portable-bed design.

**R5.** Renamed `selkies-desktop-nvidia` → `selkies-labwc-nvidia` (the skill +
its directory + every reference across plugins / image/nvidia / image/selkies);
dropped the deleted `/ov-selkies:selkies-desktop-bootc` skill ref from CLAUDE.md's
bootc dispatcher row. `git grep selkies-desktop-{nvidia,bootc}` returns only this
file.

### 2026-06-02 — feat(selkies): labwc + KDE-Plasma flavor split via selkies-core decomposition

**Breaking (image rename), no schema-version bump.** Decomposes the selkies
streaming-desktop stack into a compositor-agnostic `selkies-core` (pixelflux
transport + the ~13 fixings) plus per-flavor metalayers: `selkies-desktop`
(labwc, re-expressed) and the NEW `selkies-kde-desktop` (full KDE Plasma, run
headless via a de-SDDM `startplasma-wayland` nested in pixelflux under a
supervisord `wayland-1` socket-poll service). Adds `kde-shell` (SDDM-free Plasma
package leaf, shared by `kde-desktop` + `kde-selkies`) and the de-SDDM
`kde-selkies` rewrite — it drops `require: kde-desktop` and
`after: graphical-session.target`; the wrapper's `wayland-1` socket-poll is the
ordering primitive (the load-bearing headless change). Both pod flavors
R10-proven on disposable beds.

Landed cross-repo (B6, producer→consumer, never force-push): the main producer
(the layers + a `selkies-desktop`→`selkies-labwc` Go ref sweep + all eval **pod**
beds → `port: [auto]`, tag `v2026.153.0623`) → image/bootc (delete the
`selkies-desktop-bootc` image + `selkies-desktop-bootc-bootc` VM, keep a single
`charly.yml`, `v2026.153.0745`) → image/fedora (pod-bed auto-port,
`v2026.153.0749`) → image/selkies (the `selkies-desktop` IMAGE renamed →
`selkies-labwc`, re-R10'd against the pushed producer tag, `v2026.153.0757`) →
plugins (4 new selkies skills, the 2 bootc-desktop skills deleted, R5 doc
repoints) → main gitlink bump + relocate the `selkies-labwc` test vehicle into
image/selkies. The `selkies-desktop` LAYER name is KEPT (shared by
openclaw/android); `android-emulator`'s base repoints to `selkies.selkies-labwc`.

**Resolver bug fixed as its own atomic cutover (`v2026.153.0743`), surfaced by the
bootc deletion.** `CollectRemoteRefsOpts.collectImage` followed builder edges via
the RAW per-image `img.Builder`, which is empty for an image whose builder comes
from `defaults.builder` / the distro-keyed default (bazzite/aurora →
`ov.fedora-builder`) — so the builder's transitive layers were never fetched,
surfacing as "unknown layer …/rpmfusion" at `charly image generate`/`build` while
`charly image validate` passed (it never resolved a pulled builder's layer list).
Fixed by routing the fetch walk through the canonical `resolveEffectiveBuilder`
(new `effectiveBuilderForImage`), keeping the FETCH set in lockstep with the
RESOLVE set; `validateImageDAG` now runs `GlobalLayerOrder` over the enriched set
on an acyclic DAG so the gap is caught at validate time (validate↔generate
agreement); and `collectAllImageLayers` gained an image-visited guard against a
base-cycle stack overflow. Regression test
`TestCollectRemoteRefsDefaultsBuilderTransitiveLayers` (fails before / passes
after); eval-pod bed R10 8/8.

**Deferred to follow-on cutovers** (each its own atomic, fully-R10'd change): the
per-GPU matrix in image/cachyos (`selkies-kde-{cpu,amd,nvidia}`,
`selkies-labwc-{amd,nvidia}` + the AMD-VAAPI pod and NVENC passthrough-VM beds) —
`selkies-kde` remains a main test vehicle until then; the single-file-convention
migrator change (`charly migrate`'s `kind-files` split + re-inlining the split
submodules); the VM-target and android-adb-host `port: [auto]` Go support; and
factoring the duplicated `vaapi-encode` eval check into the selkies layer's
deploy scope (R3).

### 2026-06-01 — feat: `preemptible` — exclusive host-resource arbitration (the fourth deploy axis)

**Additive (no schema-version bump, no migration).** Introduces a fourth,
orthogonal deploy-classification axis alongside `disposable` / `ephemeral` /
`lifecycle`: **`preemptible`** (holder side) + **`requires_exclusive`** (claimant
side) on `DeploymentNode`. A `preemptible` deploy occupies named exclusive
host-resource token(s) (`holds: [...]`) and MAY be gracefully stopped to free
them for a claimant that declares `requires_exclusive: [...]`, then MUST be
restarted (disk + definition preserved). It is the INVERSE of `disposable`
("you may pause me, but bring me back" vs "you may wipe me") and derives
nothing from / to the other three axes.

**Motivation.** A physical GPU passed through to a VM via VFIO can be bound by
exactly one VM at a time. A long-running operator GPU VM (`vm:cachyos-gpu`) and
the GPU eval bed (`eval-cachyos-gpu-vm`) contend for the same card; before this
change the operator had to stop the holder by hand, run the eval, and remember
to restart it.

**Mechanism.** A new `ResourceArbiter` (`ov/preempt.go`) matches a claimant's
required tokens against running preemptible holders (pure set-intersection — the
token is an operator-chosen NAME, decoupled from the access mechanism, so it
unifies pod-vs-VM contention), gracefully stops each holder, **waits until it
actually powers off** (a readiness poll, since `vm stop` issues an async ACPI
shutdown and a VFIO device isn't released until power-off), records a crash-safe
**lease** (`~/.local/share/ov/preemption/leases.yml`, written BEFORE any stop so
recovery is always possible), and restores the holders on release. The arbiter is
wired at the transient claim point (`charly eval run` via `runEvalBed`, `defer`-
released) and the persistent ones (`charly vm create` / `charly start`, released by
`charly vm stop`/`vm destroy`/`charly stop`/`charly remove`); nested `ov` subprocesses
inherit the lease via `OV_PREEMPT_LEASE` and never re-acquire. `restore: always`
(default) brings the holder back regardless of the claim's outcome;
`restore: on-success` leaves it stopped on a failed claim. New `charly preempt
status` / `charly preempt restore [claimant]` surface inspection + crash recovery
(`reconcileStranded` also runs automatically at each acquire). The VM/pod
graceful-stop + start logic was extracted into shared `stopVM`/`startVM`/
`stopPodService`/`startPodService` funcs (R3) so the arbiter and the `charly vm`/`charly
start`/`charly stop` commands run identical lifecycle code.

**Surfaces.** `ov/deploy.go` (`Preemptible *PreemptibleConfig` +
`RequiresExclusive []string` + `IsPreemptible()`/`PreemptionHolds()`/
`RequiredExclusive()`), `ov/classification.go` (`Classified.IsPreemptible()` +
orthogonality), `ov/preempt.go` (arbiter + ledger + lifecycle deps),
`ov/preempt_cmd.go` (`charly preempt`), `ov/validate_preempt.go` (holds non-empty,
stop ∈ {shutdown}, restore ∈ {always,on-success}, no self-contention) hooked into
both load paths, integration edits in `ov/vm.go`/`ov/start.go`/`ov/commands.go`/
`ov/eval_bed_run.go`, and `ov/main.go`. Tests in `ov/preempt_test.go` +
`ov/preempt_schema_test.go`. Docs: `/ov-internals:disposable` ("The
resource-arbitration axis"), `/ov-core:deploy` ("Preemptible resource
arbitration"), CLAUDE.md Skill Dispatcher row. The host-specific GPU hostdev on
`cachyos-gpu-vm` and the `preemptible:` flag on the operator `vm:cachyos-gpu`
remain per-host/uncommitted (a PCI address is host-specific) — the R10 ran them
locally: the arbiter preempted the live `ov-cachyos-gpu` workstation, freed
`/dev/vfio/13`, the disposable `cachyos-gpu-vm` claimed the card, and the holder
was restored on teardown.

### 2026-06-01 — philosophy: name "candyboxing" (secure the box, stock it fully) as a first-class concept

**Additive.** Names the environment philosophy Overthink already embodies:
secure the disposable container / VM / eval-bed boundary as a whole (rootless
podman + userns, KVM/libvirt, gocryptfs, tailscale, `disposable: true`), then
fill it with the full toolset (every `ov` verb, MCP server, layer, `charly eval`
probe) — the inverse of a classical tool-restricting sandbox. The full candy
store inside the box is what makes RDD honest and Disposable-Only Autonomy
fearless; it loosens no safety gate.

**Surfaces.** New `## Candyboxing` section in CLAUDE.md (after "Prioritize Clean
Architecture", before "Risk Driven Development") + a Key Rules pointer bullet + a
"Why Overthink?" paragraph in README (with a light nod in the existing "Sandboxed
AI desktops" bullet); restatements/pointers in `/ov-internals:disposable`
(the lifecycle boundary of the candybox), `/ov-eval:eval` (the bed *is* the
candybox), and `/ov-internals:agents` (each teammate's bed is its candybox). No
hook pointer — candyboxing is framing, not a per-turn gate. No schema change, no
`MigrationStep`, nothing deleted.

### 2026-06-01 — engineering-discipline policy: name "Risk Driven Development" (RDD) — the proactive twin of R1

The front-loaded-validation discipline that already ran through the project under
the slogan "Verify before you change (the proactive twin of R1)" is now a named,
first-class philosophy: **Risk Driven Development (RDD)**. This is **additive
naming**, not a rename — the slogan is retained everywhere as RDD's operational
mnemonic; nothing was deleted. (See CLAUDE.md "Risk Driven Development (RDD)" for
the current, forward-looking definition.)

**What RDD names.** ALWAYS validate ANY high-risk assumption empirically on a live
`disposable: true` bed before the design commits to it — never accept the skills,
CLAUDE.md, or the current code as automatically correct (documentation drifts and
code has bugs). It is the proactive twin of R1 (reactive RCA) and the front-loaded
bookend of R10 (final fresh-rebuild proof): R1 / RDD / R10 are the same "never
trust, verify" discipline at three points in time.

**Risk — not documentation status — is the trigger.** Low-risk orientation ("what
does layer X do") stays a zero-risk skills-first (R0) lookup; RDD fires only for a
high-risk unknown and is proven on a bed regardless of what any doc or code
asserts. The archetypal high-risk unknown is composition: whether a specific
combination of layers, at the latest currently-available versions the resolver
picks, builds / deploys / runs together — which no skill can certify. RDD composes
with R0 rather than competing with it: R0 governs where you start, RDD governs what
you accept as proven. When a bed contradicts a doc, the doc is stale — fix it.

**Surfaces touched (one cutover, two repos).** Canonical definition + at-a-glance
table + the three failure modes added to CLAUDE.md (new "Risk Driven Development
(RDD)" section, a Key Rules pointer, an End-of-turn and a post-execution checklist
line, and the existing slogan mentions re-anchored to the name). README "Why
Overthink?" gained a user-facing RDD paragraph. In the `plugins` submodule: a
`## RDD` section in `/ov-internals:strict-policy`; the `root-cause-analyzer` agent's
proactive-twin paragraph + a forbidden-rationalization block; `testing-validator`
standard #9; an eval "Standard 0"; and RDD-anchoring of the slogan in
`/ov-internals:agents`, `/ov-internals:cutover-policy`, `/ov-internals:disposable`,
`/ov-internals:git-workflow`, and the skill-maintenance meta-skill. Lean RDD
pointers were added to the three soft hooks (`runtime-verification-reminder.sh`,
`end-of-turn-challenge.sh`, `team-coordination-reminder.sh`); RDD is deliberately
NOT in the deterministic `pre-commit` / `pre-push` gates, because "highest-risk,
validated early" is a judgment, not a mechanical invariant ("hooks gate mechanical
invariants; agents judge proof").

**Also in this commit.** `.claude/settings.json` gained
`"worktree": {"bgIsolation": "none"}` so the background-isolation guard defers to
the operator's dedicated per-purpose worktrees (the `av` / `ac` / `oc` / `qc`
checkouts) rather than requiring a nested `.claude/worktrees/` worktree.

Docs/policy-only cutover (plus three lean `.sh` hook pointers, verified by running
each hook). Verification: adversarial consistency review, the R5
naming-completeness grep (every "verify before you change" slogan now carries a
named RDD link — three orphans the grep surfaced were fixed in the same tree),
markdown integrity, and a clean run of all three touched hooks. No schema change,
no `MigrationStep`.

### 2026-06-01 — charly tooling: `charly layer set` wrapper descent + annotated-tag clone (no "is not a commit" warning)

Two `ov` Go defects that surfaced during the selkies/pixelflux landing were fixed
in a dedicated cutover.

**`charly layer set <layer> <dotpath> <value>` appended a stray top-level key.** Layer
files are kind-keyed (`layer: {...}`), but `LayerSetCmd` passed the body-relative
dot-path straight to `SetByDotPath`, which walks from the document root — so
`charly layer set foo version X` created a second, top-level `version:` instead of
editing `layer.version`, and the loader then rejected the file as "ambiguous —
layer: wrapper present AND other top-level keys [version]". Fix: `LayerSetCmd.Run`
prepends `layer.` to the dot-path (idempotent — an already `layer.`-qualified path
is left alone). `ov/scaffold_cmds.go`; `TestLayerSet_DescendsIntoLayerWrapper`.

**Remote-layer resolution warned `refs/tags/vX <sha> is not a commit!` on the
first fetch of an annotated tag.** `GitResolveRef` returned the tag OBJECT sha
(its first loop matched the unpeeled `refs/tags/X` before the `^{}` loop ran), and
`GitClone` then ran `git clone --depth 1 --branch <annotated-tag>`, whose shallow
handling emits git's "is not a commit!" warning before peeling. Fix: `GitResolveRef`
now also queries `refs/tags/X^{}` and a pure `pickResolvedCommit` prefers the
peeled COMMIT; `GitClone` shallow-clones that resolved commit directly
(`gitCloneByCommit`, fetch-by-sha — GitHub/GitLab allow it), keeping `git clone
--branch` only as a fallback for servers without sha-fetch. The throwaway clone is
silenced (`-c init.defaultBranch=main`, `-q`, `advice.detachedHead=false`) so it
adds no new stderr. `ov/refs_git.go`; `TestPickResolvedCommit`. The annotated-tag
re-fetch is now fully warning-free, satisfying the zero-warnings gate.

### 2026-06-01 — engineering-discipline policy: autonomous-by-default (act on any issue; ask only at a crossroad)

A same-day follow-up corrected an over-restriction in the blocking/non-blocking
landing below. That landing said net-new work the cutover did not surface
required the user to authorize a NEW plan, and scoped the AI's autonomy to issues
the current cutover happened to surface. The operator's correction: picking up
the next thing automatically is exactly what the AI should do — the discriminator
is CERTAINTY (clear path vs genuine fork), not provenance (surfaced-here vs
net-new). The policy now states the default plainly: the AI solves ANY issue it
finds automatically, opening the next cutover without waiting for authorization,
each as an atomic fully-R10'd change; it pauses to ASK only at a genuine
unexpected/unplanned crossroad — a decision it cannot resolve from the request,
the code, the loaded skills, or sensible defaults (a design choice with material
trade-offs, a hard-to-reverse or outward-facing action without standing
authorization, a plan↔CLAUDE.md/skills contradiction, or genuinely ambiguous
requirements). Escalation became the narrow crossroad exception rather than a
co-equal default; verification discipline (R10, disposable-only, no-fraud) is
unchanged — autonomy is initiative, not skipping proof. Landed in CLAUDE.md (the
"Autonomous by default — act, don't ask" Key Rule + the "Starting the next
cutover" post-execution bullet + R2's escalation framing) +
`/ov-internals:strict-policy` (fix-by-default / escalate-at-crossroad) +
`/ov-internals:cutover-policy` (non-blocking path broadened beyond surfaced-here).

### 2026-06-01 — engineering-discipline policy: blocking vs non-blocking issue handling + long-running-eval-bed guidance

Two engineering-discipline policies were refined after a four-substrate R10 run
and a multi-agent merge/push audit exposed where the existing wording fell short.

**Blocking vs non-blocking surfaced issues (R2 refinement).** R2 forbade every
"pre-existing / out-of-scope / follow-up PR" deferral. It now names the ONE
legitimate way an issue leaves the current cutover: classify each surfaced issue
as BLOCKING (the current change is incorrect/incomplete/unsafe without it → fix
in the SAME working tree, prove under the CURRENT cutover's R10) or NON-BLOCKING
(the current change is correct AND complete without it, and the issue is
genuinely separable → fix it immediately as its OWN cutover with its OWN full
R10, opened the moment the current cutover is R10-passed and committed). The
non-blocking path is NOT the forbidden "someday follow-up": it is a distinct
change, done now, fully verified, with no window of unverified brokenness on
`main`. The discriminator is whether shipping the current cutover without the fix
would leave the tree correct and the cutover's claim true (unsure → blocking).
Mislabeling a blocking issue "non-blocking", or carving one change's own scope
into two, remains the forbidden split. Landed in CLAUDE.md R2 +
`/ov-internals:strict-policy` (R2 third path) + `/ov-internals:cutover-policy`
(new "Blocking vs non-blocking surfaced issues" section, reconciled with the
no-author-it-as-two-plans rule).

**Long-running-eval-bed guidance (correction of a disproven draft).** A draft
rule had prescribed that a long bed is "ALWAYS delegated to a TEAMMATE; the LEAD
does NOT run long beds." A four-substrate R10 run disproved it: teammates
orphaned both long beds (a teammate's `run_in_background` process tree is torn
down when the teammate goes idle — no clean exit, no re-invoke), while the
persistent main session ran all four beds to completion as harness-tracked
background tasks. The rule is replaced with guidance framed by MECHANISM rather
than by who owns the run: a long bed (VM/emulator — `eval-k3s-vm`,
`eval-android-emulator-pod`, the bootstrap-VM beds) (1) launches as a
harness-tracked `run_in_background` task — never foreground (the Bash 120s/600s
timeout kills it mid-`vm-create`) and never a sleep/poll loop (the R4 bandaid);
(2) is driven by the completion notification, so its owner must be a session that
SURVIVES to completion to be re-invoked — the persistent main session does; an
ephemeral sub-agent (returns synchronously) and an idle teammate (torn down on
idle) do not; (3) is reconnected to via durable state (`.eval/<bed>/<calver>/`
`summary.yml` + the live domain), never a held process handle. "Prefer agents"
now carries this one explicit exception for long-running work. Landed in CLAUDE.md
"Agents, Workflows & Teams" + `/ov-internals:agents` (binding rule + preference) +
`/ov-eval:eval` (long-bed section).

**Docs/policy-only attribution provision.** The AI Attribution table is
runtime-defined, so a documentation-only cutover (no Go/YAML/image/runtime
surface) had no tier it could honestly claim — `fully tested and validated`
requires R10 beds that do not exist for docs, and `syntax check only` is paired
with "do NOT commit; STOP and ask". The section now states that a docs/policy-only
cutover is validated by the applicable non-runtime standards (adversarial
consistency review, the R5 grep self-test, cross-reference validation, markdown
integrity, the pre-commit/pre-push gates) and earns `fully tested and validated`
when all of them pass; the `syntax check only → do NOT commit` clause is scoped to
code with a pending R10. This cutover is itself the first docs-only cutover landed
under the provision.

### 2026-06-01 — `charly eval` in-container `command:` stdin guard + first-class `adb` UI readiness verbs

Two `charly eval` framework defects surfaced while hardening the
`eval-android-emulator-pod` bed against a flaky appium phase, and were fixed
generically in `ov` instead of being worked around in the layer config.

**The stdin-heredoc bug (generic).** In-container `command:` checks are
delivered to the pod shell over a STDIN heredoc (`NestedExecutor.wrapWithJump`
— "stdin-attached exec"). Any script whose first subcommand reads stdin
(`adb shell`, `ssh`, `read`, `cat`) consumed the REST of the heredoc — the
not-yet-executed script lines — silently truncating the check to its first
command and emitting empty stdout. A multi-line settle gate built on
`adb shell` therefore produced nothing and timed out *at every host load*; the
"only fails under load" symptom was a red herring — the gate never ran. Fix:
`runCommand` now wraps every in-container script in `{ <script>; } </dev/null`
(`wrapContainerCommand`, `ov/evalrun.go`) — the shell drains the whole heredoc
at parse time, then runs the group with every subcommand's stdin tied to
/dev/null. One framework change makes every in-container `command:` check
robust; authors no longer need a per-call-site `</dev/null`.

**Hand-rolled shell where a verb belonged.** The android-emulator GMS-churn
settle gate (poll the focused window, dismiss the ANR dialog, repeat) was a
~60-line `command: in_container` shell script in `layer.yml`. It is now three
first-class Go/goadb `adb` verbs (`ov/adb.go`): `adb: wait-ui-settled` (the
readiness gate — polls `mCurrentFocus`, dismisses any "Application Not
Responding" dialog with `KEYCODE_HOME`, honors `timeout:`), `adb: current-focus`
(prints the foreground window line for assertions), and `adb: keyevent` (generic
input). They run entirely over goadb `RunCommand` — no in-container shell, no
heredoc, no stdin hazard — and are immune to the bug above by construction. The
`android-emulator-layer` gate collapsed to a single `adb: wait-ui-settled` +
`timeout: 600s` check.

Root cause of the original appium failures (established by RCA, not load
experiments): the `google_apis_playstore` emulator runs minutes of GMS
post-boot churn (Play Store auto-update, Chimera, Heterodyne sync) that starves
the GMS-coupled system UI (Pixel Launcher via AiAi, systemui); it ANRs and the
dialog occludes the foreground app, so an appium find fired right after
`sys.boot_completed` 404s. `sys.boot_completed` is necessary-but-not-sufficient
readiness; `wait-ui-settled` is the sufficient half, and it is LOAD-INDEPENDENT
(the ANR is GMS churn, not host contention — a deliberately overloaded 6-burner
run only proved that adb itself dies before that, which no UI gate can survive).

---

## 2026-05

### 2026-05-31 — unified `charly status`: one table across pod / vm / k8s / local / android

`charly status` became the **unified deployment-status surface**: a single table
(or JSON array, or single-deployment detail view) showing every charly deployment
across all five substrates side by side, with a leading **KIND** column /
`"kind"` JSON field discriminating which substrate each row came from. Before
this cutover `charly status` was pod-only — it did one batched `podman ps` +
`podman inspect` over `ov-*` containers and knew nothing about VMs, k8s
clusters, `target: local` deploys, or `target: android` devices, so an operator
running a VM + a local profile + an emulator had to consult three different
verbs to see their fleet.

- **Substrate-collector registry.** The pod-only `Collector` was generalized
  into a registry of `SubstrateCollector`s (`ov/status_substrate.go`): the
  `SubstrateKind` discriminator (`pod`/`vm`/`k8s`/`local`/`android`), a
  read-only `CollectOpts` input, the `SubstrateCollector` interface
  (`Kind`/`Available`/`Collect`), and an `init()`-time `registerSubstrate`
  registry. Each substrate lives in its OWN file and self-registers — there is
  NO central slice to edit when a substrate is added. The five collectors:
  `PodCollector` (`status_collect_pod.go`, the existing engine snapshot +
  worker-pool probe path, `Source="podman"`), `VMCollector`
  (`status_collect_vm.go`, libvirt domains, `Source="libvirt"`), `K8sCollector`
  (`status_collect_k8s.go`, cluster workloads + live client-go probing under
  `--nested`), `LocalCollector` (`status_collect_local.go`, the install ledger,
  `Source="ledger"`), and `AndroidCollector` (`status_collect_adb.go`, declared
  `target: android` devices via adb `host:devices`, `Source="adb"`). All five
  use the identical struct-literal registration shape
  (`registerSubstrate(func(c *Collector) SubstrateCollector { return &XxxCollector{c: c} })`)
  with an exported `XxxCollector` type — the integration pass normalized the vm
  and k8s collectors (which had carried lowercase `vmCollector`/`k8sCollector`
  types plus redundant `newvmCollector`/`newk8sCollector` constructors — the
  latter even returning the concrete `*k8sCollector` while the former returned
  the interface) to drop that drift (R3).
- **`Collector.All` fan-out.** Builds one `CollectOpts`, runs the available
  collectors across a `NumCPU*2`-bounded goroutine pool, merges their rows,
  applies the nested overlay, and sorts by `(Kind, image)`. A collector whose
  backend is unreachable (`Available == false`) is skipped silently; a
  collector that errors mid-collect logs ONE `WARNING:` and contributes zero
  rows but never aborts the command — graceful degradation is the contract, so
  `charly status` on a podman-only host shows the pod rows and silently omits the
  rest.
- **KIND column + unified `DeploymentStatus` JSON.** `RenderTable` gained the
  leading KIND column (`cellKind`); the rendered shape is now
  `DeploymentStatus` (`status_render.go`) with `Kind` (`json:"kind"`), `Nested`
  (`json:"nested,omitempty"`), and `Source` (`json:"source,omitempty"`) added
  to the prior fields. Detail view gained a `Kind:` field and a `Nested:`
  section. Because the JSON encoder indents, the on-the-wire substring is
  `"kind": "pod"` — a SPACE after the colon; eval checks assert the spaced form.
- **`--nested` + the nested overlay (with dedup).** Nested deployment trees
  (`pod → android`, `vm → pod`, `vm → host`, …) are reflected WITHOUT a
  dedicated collector — a nested child's venue is always reached THROUGH its
  parent. `status_nested.go` post-processes the merged flat rows:
  `applyNestedOverlay` reads the declared tree (project `charly.yml` incl.
  folded `kind: eval` beds + `~/.config/charly/deploy.yml`) and attaches each
  declared child to its parent row's `Nested[]`. **Dedup:** a declared child
  that ALSO surfaced as a flat top-level row — an `AndroidCollector` row keyed
  on the dotted path (`<parent>.device`), or a nested-pod row keyed on the
  flattened `NestedContainerName` (`<seg1>_<seg2>`) — has its real collected
  data MOVED into the nested position (preserving its origin `Source` like
  `adb`/`podman`, not restamping `nested`) and its flat row REMOVED from the top
  level, so a nested child appears exactly once. A child with no flat match
  keeps the synthesized declared row (`Source="nested"`). Default = cheap
  (declared kind + moved/inherited flat-row state); `--nested` probes each
  child's live multi-hop venue through the SAME `ResolveDeployChain` +
  `NestedExecutor` primitive `charly deploy add` / `charly eval live parent.child` use
  (R3 — no bespoke nested dial), under a strict 4-second per-child context
  deadline (a deadline, never a sleep/retry — R4).
- **Proof-of-functionality eval coverage.** Each of the four core `kind: eval`
  beds gained a `status-shows-*` deploy-scope check that greps host-side
  `charly status --json` for the substrate it exercises: `eval-pod` →
  `status-shows-pod` (`"kind": "pod"` + `ov-eval-pod`); `eval-k3s-vm` →
  `status-shows-vm` (`"kind": "vm"` + `eval-k3s-vm`); `eval-local` →
  `status-shows-local` (`"kind": "local"`); `eval-android-emulator-pod` →
  `status-shows-android-nested` (`"kind": "android"` + the `"nested"` tree). A
  `verify-status` dynamic workflow (`.claude/workflows/verify-status.js`,
  modeled on `verify-beds.js`) emits the substrate→bed map and fans
  `charly eval run <bed>` out in parallel, aggregating the verbatim
  `status-shows-*` verdict per substrate.

### 2026-05-31 — k3s-server eval checks: `${DEPLOY_NAME}` eval var (fix un-expandable cluster token)

The `eval-k3s-vm` R10 bed failed 4 of 20 eval-live checks: the `k3s-server`
layer's deploy-scope k8s checks authored `cluster: "${deploy_name}"`, but the
eval-var expander (`testVarRefPattern`, `ov/evalspec.go`) recognizes only
UPPERCASE names, so the lowercase token passed through literally as
`--cluster '${deploy_name}'`, resolved to no ClusterProfile (empty kubeconfig →
"no configuration has been provided / KUBERNETES_MASTER"), and the checks failed.
The bed's own baked checks hard-coded `cluster: "vm-k3s-vm"` and passed, masking
the layer-check failure. Surfaced by the agent-team bed smoke and root-caused by
`/ov-internals:root-cause-analyzer`; landed as its OWN cutover (operator-rescoped,
separate from the agent-teams docs change that surfaced it).

- **`DEPLOY_NAME` is now a first-class runtime-only eval var.** Added to
  `runtimeOnlyVarPrefixes` (`ov/evalspec.go`) and seeded — sanitized via
  `sanitizeDeployName` (`:`/`.`/`/` → `-`) — in both `ResolveEvalVarsRuntime`
  (`ov/evalvars.go`, pod/local path) and `runVm` (`ov/eval_cmd.go`, as
  `sanitizeDeployName("vm:"+vmName)`). It resolves to the SAME identifier
  `K3sPostProvision` uses for the kubeconfig context + ClusterProfile, so a
  layer's deploy-scope k8s checks address their own cluster generically. This is
  a generic eval-resolver feature, not a k3s-specific patch.
- **`layers/k3s-server/layer.yml`**: the 4 `cluster:` fields → `${DEPLOY_NAME}`
  (the artifact `retrieve_to:` keeps lowercase `${deploy_name}` — a separate,
  artifact-path expander, `expandArtifactVars`). Layer `version:` bumped.
- **Validator guard (R3 — prevents recurrence).** `charly image validate` now rejects
  a lowercase `${...}` token in the k8s / resource-identity eval fields (cluster,
  name, namespace, label, kubeconfig, k8s_context, k8s_resource, k8s_group,
  k8s_version, manifest) — the class of bug that previously passed both validate
  AND runtime. Scoped to CLI-arg identifier fields, so shell `command:` bodies
  (legitimate bash vars) and cdp `expression:` (JS template literals) are
  untouched.
- Skill `eval-k8s` example updated to `${DEPLOY_NAME}` + an explanatory note; Go
  tests added (the validator guard, `DEPLOY_NAME` seeding + sanitization, the
  runtime-only classification).

R10: `charly eval run eval-k3s-vm` → exit 0, **20/20** (was 16/20), the full sequence
including the fresh `charly update` re-verification gate.

### 2026-05-31 — Agent teams enabled; bed-scoped parallel real-deployment testing; Hard-Cutover correction (the commit is the only gate)

We wanted multiple Claude Code instances to develop and test different aspects
of `ov` on real, live deployments **in parallel, without git-worktree
overhead**, verifying empirically on `kind: eval` beds at every stage. Pursuing
that surfaced a real over-reach in `CLAUDE.md`: the Hard-Cutover policy's
"Premature R10 launch" prohibition read as if running `ov` commands mid-cutover
was itself forbidden. It is not — the policy's sole legitimate purpose is to
**gate the git commit behind a full live test of the FINAL code**. Running beds
to *verify* during development is encouraged; it is the proactive twin of R1
("verify before you change").

- **Agent teams enabled in committed settings.** `.claude/settings.json` gains
  `env.CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1`, `teammateMode: "auto"`, and the
  `TaskCreated` / `TaskCompleted` / `TeammateIdle` lifecycle hooks wired to a new
  lean `team-coordination-reminder.sh` (a soft pointer, exit 0). The experimental
  caveats stand (no in-process session resume, one team at a time, no nested
  teams, fixed lead); enabling needs a `claude` restart because the `env` flag is
  read at process start. The prior "not enabled in committed settings / opt-in
  per session" framing in CLAUDE.md and the agents skill is gone.
- **The eval bed is the unit of ownership + isolation — no worktree.**
  `validateEvalBeds` already guarantees each `kind: eval` bed has a name disjoint
  from every other deploy, `target ∈ {pod,vm,local}`, and `disposable: true`, so
  distinct beds get distinct container/VM/image names + ports and run
  concurrently and safely. A bed pins an image → layers → files, so owning a bed
  owns those source files — exactly what a worktree would isolate. The lead
  partitions the beds; each teammate is a bed-owner running its bed's full
  `charly eval run <bed>` on a real deployment; the lead owns the single atomic
  commit; teammates never commit/push.
- **`verify-beds` de-serialized.** The workflow previously serialized pod/vm/kvm
  beds and ran only no-build `local` beds concurrently, on the (unverified)
  premise that "each bed run saturates the host (single-tenant KVM/libvirt)". KVM
  and libvirt are multi-tenant, and podman builds distinct image tags
  concurrently; that comment was over-caution and is deleted. All beds now run via
  `parallel()`, bounded by the runtime's documented 16-concurrent / 1000-total
  dynamic-workflow agent ceiling.
- **Hard-Cutover correction — the commit is the only gate.** The "Premature R10
  launch" forbid bullets, the "R10-class verbs are FORBIDDEN until every task is
  done" workflow step, the "Premature R10 launch" anti-pattern, and the
  "R10 is the last step and never a parallel/background track" binding rule are
  rewritten throughout (CLAUDE.md, `runtime-verification-reminder.sh`, the agents
  skill, `verify-beds.js`'s description, `eval-bed-runner.md`, the git-workflow
  skill). The forbidden acts are now stated only about the COMMIT/CLAIM —
  committing or claiming success on an intermediate state, faking the final test,
  splitting a cutover across turns/sessions, inflating the attribution tier —
  never about running `ov`. Law 5 is redefined: "the commit is gated on a full
  final-code live test (pasted); beds run freely throughout to verify." Laws 4
  (disposable-only) and 3.6 (no scope-shrinking flags) are unchanged.
- **New `/triage-eval-failure <bed>` workflow** — competing-hypotheses RCA of a
  failed bed run, each hypothesis validated on the live bed, adversarial
  cross-check, converge, hand back a fix to re-run the real bed.
- **`teammateMode: "auto"` correction.** An earlier draft proposed a
  "host-aware cap `min(16, cores-2)`" for `parallel()`; there is no such term —
  the real bound is the runtime's documented 16-concurrent agent ceiling. And
  `teammateMode: "auto"` is already the default; it is committed for explicitness.

Scope was docs + Claude Code config + hooks + skills only — no `ov` Go / layer /
image / schema changes, so no `charly migrate` and no `LatestSchemaVersion()` bump.

### 2026-05-31 — CachyOS GPU workstation: remove Looking Glass; headless always-on display (no idle blank/lock); SPICE bed-only; SPICE eval-gating; selkies-frames GPU-gate

The persistent GPU-passthrough CachyOS operator VM (`cachyos-gpu`) presented as
"no output on the physical monitors". Root cause (`/ov-internals:root-cause-analyzer`):
KDE's default idle power management DPMS-off'd AND RELEASED the physical RTX 4080
HDMI heads after idle, and an input-less GPU-passthrough seat has no local
keyboard/mouse to wake them — so the connected monitors stayed permanently dark.
The same investigation established that the `looking-glass-host` layer can never
capture headlessly on Wayland-only ov, and that the operator's virtio/SPICE head
(a headless console) was being made the Plasma primary at `0,0`, displacing the
panel and the real monitors.

- **Looking Glass removed (cross-repo).** The `looking-glass-host` layer — and
  its superseded graceful-skip wrapper `ov-lg-host-or-skip` (the prior
  `v2026.150.2215` landing) — are DELETED (125 lines); the cachyos `@github`
  `add_layer` references on BOTH the eval bed and the operator are removed; the
  libvirt IVSHMEM `<shmem name='looking-glass'>` device is removed from both VM
  entities; `nvidia-driver` drops the now-pointless `kvm` supplementary group
  (only `/dev/kvmfr0` needed it). Every remaining comment / fixture / string
  reference is scrubbed — R5 `git grep` for `looking-glass`/`kvmfr`/`LookingGlass`
  returns only this file. RCA: Arch's `looking-glass-host` compiles ONLY the XCB
  capture backend (needs an X11 root window — charly is Wayland-only) plus PipeWire
  (which stalls on the KDE ScreenCast portal picker), so it can NEVER produce
  frames headlessly. `service_render.go`'s `TestRenderServiceWantedBy` fixture was
  renamed off the dead layer name to a generic `session-capture`.
- **Headless always-on display** (`cachyos-kde-settings`, `2026.150.1339` →
  `2026.151.0543`). Two coupled fixes written to the system KDE config dir
  (`/etc/xdg`, since the deploy user has no `~/.config` override): (1) disable the
  idle screen-LOCKER (`kscreenlockerrc` `Autolock=false`/`LockOnResume=false` — a
  remote/passthrough seat has no keyboard to type an unlock password); (2) ship an
  `ov-display-keepalive` user service running `kde-inhibit --power --screenSaver
  sleep infinity` (`restart: always`), which holds a PowerManagement + ScreenSaver
  inhibit so powerdevil never idle-DPMS-offs the heads. (`idleTime=0` was found to
  mean "turn off IMMEDIATELY", not "never" — the inhibitor is the correct
  mechanism.) Two new deploy-scope eval checks assert both
  (`kscreenlocker-autolock-disabled`, `display-keepalive-active`).
- **SPICE dropped from the operator (bed-only).** `cachyos-gpu` now declares
  `video: [{model: none}]` / no `<graphics>` / no spicevmc channel — the
  passed-through RTX 4080 drives the physical monitors directly over DRM, and
  early-boot output is the serial console (`console=ttyS0`). SPICE + a virtio-gpu
  head stay on the portable `cachyos-gpu-vm` eval bed only (no physical GPU →
  needs a virtual head to screenshot). Removing the virtio head also eliminates
  the spurious `Virtual-1` Plasma-primary at `0,0`.
- **selkies-frames GPU-gate** (`cachyos-gpu-desktop-eval`, `2026.150.2056` →
  `2026.151.0543`). The dead `looking-glass-guest` check (required
  `looking-glass-host` + an IVSHMEM node) is removed. The `selkies-encoding-frames`
  check still asserts selkies CAME UP (compositor socket + `:8081` + capture
  journal) unconditionally, but only asserts `frames>0` (via `STATUS` on
  `/tmp/ov-capture.sock`) when an `0x10de` device is present — pixelflux's
  capture/encode is GPU-bound on this stack and never produces frames without a
  card, so the frame assertion N/A's on a no-GPU host (matching the bed's portable
  design).
- **SPICE eval-gating** (ov, `evalrun_ov_verbs.go` + `vm_display_gate_test.go`).
  `runOvVerb` now SKIPS a `spice`/`vnc` verb when the verb subprocess reports the
  target VM declares no such display device (the VM-target resolver's own "VM
  <name> has no SPICE graphics device declared in vm.yml" signal). The SHARED
  `cachyos-gpu-desktop-eval` SPICE checks therefore N/A on the SPICE-less operator
  while still asserting on the SPICE-having bed — ONE shared eval layer, no
  operator/bed split (R3). Unit-tested with 6 cases (spice/vnc no-device → skip;
  spice connected / non-display verb / empty stderr → no skip).
- **R10.** `charly -C image/cachyos eval run eval-cachyos-gpu-vm` PASS on the real
  RTX 4080 SUPER (in-guest `lspci`: `AD103 [GeForce RTX 4080 SUPER] [10de:2702]`
  + its `[10de:22bb]` audio function; domain hostdev count 2) — 33/0/0 on both the
  eval-live and the fresh-rebuild legs. A portable confirmatory re-run on the
  FINAL code (with the gate) also passed 33/0/0 on both legs with 0 skipped — the
  gate is a clean no-op on the SPICE-having bed. The operator `cachyos-gpu` was
  recreated from clean and verified live (RTX heads `enabled`/`dpms=On`,
  `ov-display-keepalive` running, locker disabled, KDE panels present on every
  head, Looking Glass gone, selkies streaming) — `charly eval live cachyos-gpu` 28/0
  with the 6 SPICE checks correctly N/A'd. Monitor + selkies
  mouse/keyboard/screenshot demonstrated (ydotool→seat-0 + spectacle on the RTX
  heads; the selkies capture bridge + nested-Plasma `kate`). `go test ./...` green.

(The browser ScreenCast-portal mirror — streaming the SAME physical-monitor
desktop to the selkies browser stream — is scoped as a separate follow-up effort:
pixelflux is nested-only and cannot mirror seat-0, the headless portal-capture
picker auto-accept is unproven, PipeWire was failing, and selkies currently
CPU-encodes; none of that is landed here.)

### 2026-05-30 — cachyos GPU eval: `VM_HOSTDEV_COUNT` intent gate closes the silent-passthrough false-green

A live `eval-cachyos-gpu-vm` bed run reported `PASS` while EVERY GPU check
logged `N/A: no NVIDIA GPU passed through` — despite a `<hostdev>` being
configured for the VM. The GPU checks N/A-pass when no NVIDIA device (`0x10de`)
reaches the guest (correct on a portable host), but could not tell "no GPU
configured for this VM" (legit N/A) from "a GPU hostdev WAS configured but
passthrough silently failed" (must FAIL). The result was a green-but-meaningless
GPU R10 — caught only by manually inspecting the per-check verdicts.

- **charly (`VM_HOSTDEV_COUNT`).** VM live-eval (`ov/eval_cmd.go`) now resolves
  `VM_HOSTDEV_COUNT` = `len(VmSpec.Libvirt.Devices.Hostdevs)` — the operator's
  passthrough INTENT, sourced from the authored VmSpec, NOT the running domain
  (a libvirt hostdev drop would zero the live count and re-mask the very failure
  this guards against). Exposed as a deploy-scope, runtime-only eval var
  (`vmHostdevCount` helper + a `runtimeOnlyVarPrefixes` entry so build-scope
  checks can't reference it). Unit-tested in `ov/vm_hostdev_test.go`.
- **cachyos (`gpu-passthrough-intent-honored`).** One gate check (R3 — a single
  gate, not a duplicated guard in every GPU check) in the
  `cachyos-gpu-desktop-eval` layer HARD-FAILS when `VM_HOSTDEV_COUNT > 0` but no
  `0x10de` is in the guest. A GPU host can no longer silently pass without the
  GPU; the `gpu-driver-active` / `gpu-cuda-container` comment was corrected to
  describe the gate. cachyos `v2026.150.2135`.
- **R10.** `charly -C image/cachyos eval run eval-cachyos-gpu-vm` PASS (31 passed,
  gate `✓`, the real RTX 4080 SUPER branch — `nvidia-smi -L` / `cuda-smoke` /
  NVENC) with the host's hostdev attached, including the fresh-rebuild
  re-verification; `charly eval live cachyos-gpu` PASS (32 passed, gate `✓`) on the
  persistent operator VM (`nvidia-smi` in-guest: RTX 4080 SUPER, driver
  610.43.02). The host-specific `<hostdev>` is added locally and reverted after
  the run — never committed (the committed bed stays portable).

### 2026-05-30 — ubuntu repo (overthinkos/ubuntu): schema migrate + eval-*-vm bed naming + VM host-port deconfliction

- **Migrated to schema 2026.144.1443** (`charly migrate`: kind-files split +
  entity-version backfill + calver stamp).
- **Disposable bed renamed** `ubuntu-debootstrap-vm` →
  `eval-ubuntu-debootstrap-vm`. R5 sweep across `charly.yml` / README + the
  `/ov-vm:ubuntu` & `/ov-distros:ubuntu` plugins skills.
- **`version:` backfilled** on the layerless `ubuntu-debootstrap`
  (`from: builder:debootstrap`) and the bare-base `ubuntu` images.
- **build.yml pin bumped to `v2026.150.1904`** to fetch the debootstrap
  `/dev/kvm` chroot-shadow fix (surgical: build.yml pin only, layers untouched —
  no unrelated coder-image churn).
- **VM host-port deconfliction.** The cachyos cutover had assigned its VMs ports
  `12226/12227/12228`, overlapping the canonical bed ports — the running
  `cachyos-gpu` operator (12228) collided with the ubuntu bed (12228), and the
  `cachyos-gpu-vm` bed (12227) overlaps the debian bed (12227). The ubuntu bed
  moves `12228 → 12229` so it can run alongside the operator (a disposable bed
  yields rather than cycling the running GPU operator). **Known latent overlap:**
  `cachyos-gpu-vm`(12227) ↔ `debian`(12227) — both are beds that never run
  together, so it doesn't manifest; deferred because reassigning the cachyos bed
  would cycle the running GPU operator to re-R10 it.
- R10: `charly -C image/ubuntu eval run eval-ubuntu-debootstrap-vm` → PASS (steps=6),
  plus the debian bed re-verified PASS (steps=6) on the bumped pin. `/dev/kvm`
  stayed `0666 kvm` throughout both (audit: zero `systemd-tmpfiles` host-node
  hits), zero warnings, operator undisturbed. Landed image/ubuntu
  `v2026.150.1931`, image/debian `v2026.150.1931`, plugins `bb14bdc`.

### 2026-05-30 — debootstrap chroot corrupts host /dev/kvm (build.yml fix)

- **Symptom.** Every `charly vm build` of a debian/ubuntu debootstrap VM
  intermittently broke KVM on the host, surfacing later as `charly vm create`
  failing with the misleading libvirt error `Unable to find 'efi' firmware
  that is compatible with the current configuration`.
- **RCA (three misdirecting layers).** The firmware error is libvirt's
  `firmware='efi'` autoselect aborting because **virtqemud had cached
  `accel 'kvm' is not supported`** — it probed `/dev/kvm` while the node was
  corrupted to `0660` + a wrong group (`clock`/`input`). The corruptor: the
  privileged debootstrap builder runs `-v /dev:/dev` (losetup needs the shared
  host `/dev`), and its **stage-2 chroot `apt install systemd` runs
  `systemd-tmpfiles --create`**, which re-chmods/chowns `/dev/kvm` per Debian's
  `static-nodes-permissions.conf`. Debian's `/etc/group` maps the `kvm`/`vhost`
  gids to DIFFERENT numbers than the Arch host, so the shared host `/dev/kvm`
  got reset to Debian's gids — denying the operator KVM access. debian passed
  its bed R10 by timing luck (corruption fired before its `vm create`); ubuntu's
  fired exactly during it.
- **Fix (`build.yml` debootstrap builder template, debootstrap-scoped).** Before
  the stage-2 chroot, shadow the chroot's `/dev/kvm` (+ `/dev/vhost-net`,
  `/dev/vhost-vsock`) with throwaway files
  (`mount --bind /tmp/ov-devshadow-* /target/dev/*`) so the chroot's
  `systemd-tmpfiles` touches the shadows, not the host nodes; drop the shadows
  before tearing `/dev` down. The chroot never uses KVM. The pacstrap path
  (arch/cachyos) is Arch-based — its chroot assigns the SAME gids as the host
  and never corrupts — so it is deliberately untouched (no Go-level `/dev` mask
  that would over-broaden to every privileged build and break plain `--bind`).
- **R10.** Live debian debootstrap build keeps `/dev/kvm` at `0666 kvm` across
  the `Creating group 'kvm'` chroot config that previously corrupted it (audit:
  zero `systemd-tmpfiles` host-node hits), plus the
  `charly -C image/ubuntu eval run eval-ubuntu-debootstrap-vm` bed R10.
- **Recovery (if already corrupted).** Restore `/dev/kvm` to `0666 kvm` AND
  restart the stale `virtqemud` so it re-probes — the perm-restore alone is
  insufficient because the daemon caches the no-kvm verdict in memory
  (`--timeout=120`, no on-disk capabilities cache).

### 2026-05-30 — debian repo (overthinkos/debian): schema migrate + standard eval-*-vm bed naming

- **Migrated to schema 2026.144.1443** (`charly migrate`: kind-files split inline
  image/vm/pod/k8s into siblings + entity-version backfill + calver stamp).
- **Disposable bed renamed to the standard `eval-<descriptor>-<kind>` form:**
  `debian-debootstrap-vm` → `eval-debian-debootstrap-vm`. R5 sweep across the
  debian repo (charly.yml / README) + the `/ov-vm:debian` & `/ov-distros:debian`
  plugins skills.
- **`version:` backfilled on the layerless `debian-debootstrap`
  (`from: builder:debootstrap`) and the bare-base `debian` images** — the runtime
  requires a `version:` for a layerless image on an external base, and the
  `entity-version` migrate step backfills only `base:`-style bare bases, not
  `from:`-style, so it is declared explicitly.
- R10: `charly -C image/debian eval run eval-debian-debootstrap-vm` → PASS (steps=6)
  on the disposable bed (build → eval image → create → eval live → fresh rebuild
  → teardown). Landed image/debian `v2026.150.1654`, plugins `0f1d55d`.
- **Host-environment cautionary tale (NOT a project bug).** The first R10
  attempts failed at `vm create` with the misleading libvirt error
  `Unable to find 'efi' firmware that is compatible with the current
  configuration`. RCA traced it through three layers: the `firmware='efi'`
  autoselect aborts because **virtqemud had cached `accel 'kvm' is not
  supported`** — it had probed `/dev/kvm` while an external process
  intermittently regrouped the node from the udev-canonical `kvm 0666` to
  `root:input 0660`, denying KVM access. The debian config (`uefi-insecure`
  + the `version:` backfill) was correct throughout. Fix: restore `/dev/kvm`
  to `0666 kvm` **AND** restart the stale `virtqemud` so it re-probes (the
  perm-restore alone is insufficient — the daemon caches the no-kvm verdict
  in memory: `--timeout=120`, no on-disk capabilities cache). A `/dev/kvm`
  canonical-state guard held the node sane through the live R10. Standing
  guidance: when an `charly vm create` reports an EFI-firmware error on a host
  with KVM present, check `/dev/kvm` ownership/mode and restart `virtqemud`.

### 2026-05-30 — arch repo (overthinkos/arch): schema migrate + standard eval-*-vm bed naming

- **Migrated to schema 2026.144.1443** (`charly migrate`: kind-files split inline
  image/vm/pod/k8s into siblings + entity-version backfill + calver stamp).
- **Disposable beds renamed to the standard `eval-<descriptor>-<kind>` form:**
  `arch-vm` → `eval-arch-vm`, `arch-pacstrap-vm` → `eval-arch-pacstrap-vm`. R5 sweep
  across the arch repo (charly.yml / vm.yml / README) + 5 plugins skills.
- **Builder gap fixed on `eval-arch-vm`:** the bed deploys the npm-building
  `pre-commit` add_layer to an arch CLOUD-IMAGE VM (no charly builder context), so the
  VM deploy could not resolve the npm builder. Named the arch-builder via
  `install_opts.builder_image` — the supported path (`DeploymentNode` has no
  `builder:` map field), mirroring the cachyos VM deploys.
- R10: `charly -C image/arch eval run eval-arch-vm` PASS — 52/52 (eval-live +
  post-update rebuild), all 7 steps ok. (An initial run surfaced 8 check failures
  that RCA traced to a transient `pkgbuild.com` mirror flake during cloud-init's
  package install — NOT a config bug; the arch VM already declares
  `portaudio`/`opusfile` for ov's cgo audio libs. A healthy-mirror re-run passed
  clean.)

### 2026-05-30 — CachyOS GPU workstation: KDE Plasma panel (menu bar) on every monitor

On the GPU-passthrough workstation the SPICE virtio output owns Plasma's screen 0
(the 0,0 origin), so KDE's single default panel landed there — invisible on the
two physical monitors (HDMI-A-1 3440×1440 + HDMI-A-2 3840×2160), which showed a
bare desktop with no menu bar.

- **`cachyos-kde-settings`: a Plasma panel on EVERY screen by default.** An
  idempotent ensure-script (`ov-kde-panels-all-screens.sh`) drives the Plasma
  scripting API (`qdbus6` → `org.kde.plasmashell` `evaluateScript`) to add a
  standard bottom panel (kickoff / icontasks / systray / clock) to any screen that
  lacks one; an XDG autostart entry (`/etc/xdg/autostart`, KDE phase-2) runs it on
  every login and after a monitor hotplug, so the fix self-heals and adapts to any
  monitor count. The autostart only ADDS where missing (it never removes a user's
  intentional extra panel). Readiness is a real `gdbus wait` + evaluateScript probe
  (no fixed sleep). `qt6-tools` (provides `qdbus6`) added as an explicit R9 dep.
- R10: `eval-cachyos-gpu-vm` bed 30/30 (eval-live + post-update rebuild — the new
  `kde-panel-autostart-installed` + `kde-panel-on-every-screen` checks green). Prod
  `cachyos-gpu`: from a reset 1-panel state, a reboot's baked autostart re-created
  panels on all 3 outputs (`panels_on_screens=[0,1,2]`).

### 2026-05-30 — docs: sweep stale `rebuild.go` / `RebuildCmd` / `schema-vN` / dated-cutover Go comments

Comments-only R5 doc-hygiene sweep across `ov/*.go`: stale references left by
earlier cutovers no longer point at things that exist. Removed every comment
reference to the deleted file `ov/rebuild.go` (re-pointed `vmDisposableFromDeployments`
to `run_subcommand.go`, the rebuild-method bodies to `unified_targets_*.go`, the
disposable gate to the `charly update` dispatch) and the deleted type `RebuildCmd`
("Body extracted from RebuildCmd.X" → "the X rebuild path"); dropped the stale
integer-schema `schema-v3` / `schema-v4` version labels in non-migration code
(the schema is CalVer-versioned now — e.g. "canonical schema-v3 values" →
"canonical target values", "the schema-v3 contract" → "the unified contract");
and rewrote the dated `2026-05-09 rebuild→update cutover` narrations to
present tense.

Scope discipline: left untouched the `migrate_*.go` migration code (which
legitimately names the schema versions/dates it migrates) and the inline
incident/RCA "why" rationale comments (the 2026-04-18 immich incident, the
2026-05-06 R10 follow-up, the cuda-cudnn / stale-alias incident notes, the
2026-05-12 require-image contract) — those explain current code and are not
stale-identifier references. After the sweep `git grep` finds zero
`rebuild.go`/`RebuildCmd` comment refs and `schema-v3`/`schema-v4` only inside
`migrate_*.go`. Verified: `go build`/`vet`/`test ./...` green, `charly image
validate` clean — comments only, no code or identifiers changed.

### 2026-05-30 — fix: `keep_images` retention over-removal (per-tag prune + image-list dedup)

The `keep_images` auto-prune (after `charly image build`) could delete EVERY tag of
an image — including the just-built one — when a content-stable image had
accumulated many CalVer tags pointing at ONE image id. Observed: after repeated
`charly eval run eval-pod` runs, a build's prune left ZERO eval-pod images, so the
bed's `charly eval image` step failed with "image not available locally."

Root cause: `defaultListLocalImages` mapped `podman images --format json` 1:1,
but podman emits ONE ROW PER TAG (each row's `Names` already lists every tag on
that id). So N tags on one id became N near-identical `LocalImageInfo` entries;
`pruneImagesByRetention` counted ENTRIES for the keep-N guard and, for each
"removable" entry, `rmi`'d that entry's WHOLE `Names` array — deleting tags it
meant to keep and, once the last tag of the shared id went, the image itself.

Fix (two levels): (1) `parseLocalImagesJSON` (extracted from
`defaultListLocalImages`) collapses rows to ONE entry per distinct image id with
tag refs merged — the one-id-with-a-tag-list shape `LocalImageInfo` was designed
for; this also de-duplicates `resolveLocalImageRef`'s candidate set. (2)
`pruneImagesByRetention` is now per-TAG: keep the newest N tags (label-CalVer
PRIMARY, build-tag TIEBREAKER), `rmi` only the INDIVIDUAL older tags — so a
shared id survives as long as a kept tag holds it and the newest/just-built tag
is never removed. Removed the now-dead `imageTagCalVer` / `imageDatable`.

Tests: `TestPruneImagesByRetention_SharedID` (five tags on one id, keep 3 — the
newest/just-built tag is never removed) and `TestParseLocalImagesJSON_DedupByID`
(+ a docker-RepoTags / unmerged-empty-id case). R10: `go test ./...` green; on a
fresh `ov`, 4× repeated `charly image build eval-pod` hold at `keep_images=3` (never
0) with the newest tag always present, and `charly eval run eval-pod` passes
end-to-end (8/8 steps) under the accumulated-tag state that previously failed.

### 2026-05-30 — Multi-agent support: sub-agents + dynamic workflows + agent teams driving the `charly eval` beds; layered hooks; hybrid per-directory CLAUDE.md signposts

Made Overthink a first-class citizen of Claude Code's three multi-agent
primitives, all pointed at the existing `charly eval` disposable beds for
test/verify. One atomic cutover across the main repo, the `plugins`
submodule, and all eight `image/<distro>` submodules.

- **Sub-agents.** Added two *executor* agents in
  `plugins/internals/agents/`: `eval-bed-runner` (runs `charly eval run <bed>` —
  the full R10 sequence — and returns the verbatim per-step verdict + exit
  code + failing-log tail) and `deploy-verifier` (read-only `charly eval
  image`/`live` + `charly status` for an image or a user's deploy config, for AI
  and humans). Aligned the three existing *enforcer* agents to the current
  surface: `testing-validator` now lists `charly eval run`/`live`/`image` as the
  R10 evidence and its confidence table matches CLAUDE.md's four tiers;
  `root-cause-analyzer` gained `charly eval` in its toolkit; `layer-validator`
  was rewritten from a drifted, re-enumerated schema (it listed `depends`
  instead of `requires`, described `service:` as a raw supervisord INI
  string, and omitted the mandatory `version:`) into a focused high-value
  checker that defers the full schema to `/ov-image:layer` + `charly image
  validate`.
- **New skill `/ov-internals:agents`** — the SSOT for the multi-agent story
  (primitives comparison, agent roster, the two workflows, the
  R10/disposable/paste-proof binding rule, the hooks doctrine, the signpost
  convention, the agent-teams opt-in). Cross-referenced from `/ov-eval:eval`
  and `/ov-internals:skills`.
- **Dynamic workflows** (`.claude/workflows/`, run `/<name>`):
  `/verify-beds [bed …]` fans the `kind: eval` beds out as the R10 gate
  (resource-aware: no-build `local` beds run concurrently, image-building and
  VM/KVM beds run sequentially to avoid build-cache/KVM/libvirt contention;
  missing host prereqs are logged, not silently dropped); `/audit-deploy-configs
  [target …]` runs `charly image validate` + per-target `charly eval image`/`live`
  + `deploy-verifier` and aggregates a health report.
- **Layered hooks.** Slimmed `runtime-verification-reminder.sh` and
  `end-of-turn-challenge.sh` from ~1,076 lines of CLAUDE.md-duplicating,
  drifted static text into lean POINTERS to CLAUDE.md/skills. This cleared a
  live R5 stale-reference bug — the hooks still named the renamed `charly
  harness` / `charly rebuild` / `bench-pod` / `harness.yml` / `charly harness
  list-recipe|list-score` (now `charly eval` / `charly update` / `eval-sandbox` /
  `eval.yml` / `charly eval list-*`) — and resolved a direct conflict with
  CLAUDE.md (the Stop hook said "push only if authorized"; CLAUDE.md
  auto-lands on R10 pass). Added two deterministic `PreToolUse` (Bash) gates:
  `pre-commit-gate.sh` blocks `git commit --no-verify` and an
  absent/illegal `Assisted-by: Claude (<tier>)` trailer (incl. the forbidden
  `theoretical suggestion`); `pre-push-gate.sh` blocks
  `--force`/`--force-with-lease`/`-f`/`--no-verify`. Gates use
  command-position anchoring so they block real invocations but never
  mentions (`echo`/`grep`/quoted args). Both wired in `.claude/settings.json`
  alongside an `charly eval`-verb allowlist so the workflows run unattended.
  Standing rule: hooks gate mechanical invariants, agents judge proof; hooks
  are lean pointers + deterministic gates and are never re-bloated into
  CLAUDE.md copies.
- **CLAUDE.md** gained an "Agents, Workflows & Teams" section, three Skill
  Dispatcher rows (verify beds / audit deploy / agents setup), and the
  hooks-doctrine + per-directory-signpost notes.
- **Hybrid per-directory CLAUDE.md signposts.** The repo-root CLAUDE.md
  stays the single canonical R0–R10 rule-set; added THIN signpost
  `CLAUDE.md` files to `ov/`, `layers/`, `plugins/`, and each of the eight
  `image/<distro>` submodules (arch, bootc, cachyos, debian, fedora, nvidia,
  selkies, ubuntu). Each only names the skills to load for that area and
  points back to root — it restates no rule (duplication drifts).
- **README.md** now states "testing and evaluating deployment configs, for
  AI and humans" as a first-class `ov` goal in "Why Overthink?", strengthens
  the Test section, and documents the agents/workflows/teams in "Works with
  Claude Code".

Standing rules established (forward-looking in CLAUDE.md / `/ov-internals:agents`):
running an `charly eval` bed is R10-class (disposable-only authorization, last
step never a parallel/background track, no scope-shrinking flags, and
paste-proof survives delegation — a delegated bed run whose failure is
summarized away is fraud); the hooks doctrine; and the per-directory signpost
convention. Agent teams remain documented opt-in (experimental), not enabled
in committed settings.
### 2026-05-30 — CachyOS GPU VM: venue-agnostic eval verbs, eval-anywhere, `cachyos-gpu` naming cutover, + headless Looking-Glass RCA

A five-part cutover on the CachyOS GPU-passthrough workstation. The operator VM
was renamed `cachyos-coder` → `cachyos-gpu`; every interactive `charly eval` verb was
made venue-agnostic (container | VM | ssh through ONE `DeployExecutor`); VM
live-eval now sources an applied layer's deploy-scope checks so the SAME monitor /
SPICE / mouse / keyboard / screenshot / selkies / Looking-Glass checks run against
BOTH the disposable bed and the persistent operator deploy; and a full empirical
RCA settled the headless Looking-Glass story.

- **T1 — VM naming cutover.** `cachyos-coder` → `cachyos-gpu` across
  `image/cachyos/vm.yml` (the kind:vm entity) and `image/cachyos/charly.yml`
  (the deploy entry + the `eval-cachyos-gpu-vm` disposable bed). The dead
  `ov-cachyos-gpu` / `ov-ov-cachyos-gpu` autostart units + state dirs + stale
  deploy entries were purged. R5 self-test: `git grep cachyos-coder` returns only
  this file in both repos.

- **T2 — venue-agnostic `charly eval` verbs (`ov/eval_venue.go`, new).** The
  interactive verbs (`wl` / `cdp` / `dbus` / `record` / `vnc`) hardcoded
  `podman exec` and only worked against a container. A new `resolveEvalVenue`
  builds an `EvalVenue{Exec,…}` over the existing `ResolveDeployChain` /
  `ResolveTarget`, returning a container chain, an `SSHExecutor` (VM-over-SSH /
  ssh-host), a `ShellExecutor` (local), or a `NestedExecutor` (dotted multi-hop).
  Every verb now routes through `venue.Exec` (`RunCapture` / `GetFile` /
  `PutFile`) — the SAME verb works in a container, a VM, and over ssh. The
  port-protocol verbs (`cdp` / `vnc`) gained venue-aware endpoint resolution:
  `containerPublishedAddr` via `podman port` for containers, `sshForwardEndpoint`
  (an `ssh -NT -L` forward gated by a readiness probe — not a sleep) for VM /
  ssh hosts, each owning an `EvalEndpoint` closed on the client's `Close()`.
  `spice` / `libvirt` stay VM-native (no container analog). No `*-host` / `*-vm`
  verb duplication (R3).

- **T3 — eval against any deployment, disposable or not (`ov/eval_cmd.go`).**
  VM live-eval previously attached no checks to a persistent VM.
  `collectAddLayerDeployEval` now scans an applied layer's deploy-scope `eval:`
  checks (`ScanAllLayerWithConfig` over the project config, skipping remote
  `@github` refs) and `MergeDeployEval`s them into the run — so
  `charly eval live cachyos-gpu` runs the exact same check set as the disposable bed
  from ONE source (R3). 28/28 passed on the persistent operator deploy.

- **`cachyos-gpu-desktop-eval` (new shared check layer).** Carries only the
  desktop-interaction TEST TOOLS (`ydotool`) plus a deploy-scope `eval:` block,
  added to BOTH the bed and the operator deploy. It proves the desktop RENDERS
  and accepts input headlessly: SPICE-wire mouse-move / click / key / type
  injection (which also wakes a DPMS-blanked head) FOLLOWED BY a non-uniform
  SPICE screenshot; GPU-gated `nvidia-smi` + `kscreen-doctor` monitor-output
  enumeration; the selkies stream (`wayland-1` socket + `:8081` backend + capture
  journal + `:3000` HTTPS + WebRTC HTML); and the Looking-Glass guest wiring.

- **T4 — headless Looking Glass: empirical RCA, frame-flow blocked upstream.**
  Exhaustive live-image testing established: (a) the kvmfr `static_size_mb=64`
  modprobe option created a guest-local `/dev/kvmfr0` that SHADOWED the shared
  ivshmem PCI BAR, so host-side `looking-glass-client` never saw the guest's
  frames — REMOVED (`layers/looking-glass-host`); kvmfr now auto-binds to the
  ivshmem PCI device, and LG then read the real region. (b) The `<shmem>` was
  bumped 64 → 128 MiB on both VM entities. (c) Zero-auth headless capture needs
  an X11 seat (LG's XCB backend captures the root with no PipeWire ScreenCast
  portal prompt), but a headless GPU VM's X11 seat falls back to the **virtio
  head — 0 GPU outputs** without a forced EDID (forcing one broke Xorg), whereas
  the Wayland session drives a GPU-backed virtual output (`kscreen-doctor`
  -enumerable). The desktop therefore stays Wayland **on the data**, not on
  preference. (d) With all of the above correct, the Looking-Glass B7 LGMP host
  still aborts in `lgmpHostMemPtr` (`lgmp/src/host.c`) during init — an upstream
  LG bug, independent of ivshmem size / sharing / capture backend (host↔guest
  propagation was confirmed working: LG's partial LGMP header reached the host
  `/dev/shm`). The kvmfr + IVSHMEM + capture wiring ships; guest→host frame-flow
  awaits an upstream Looking-Glass fix, so no `looking-glass-frames-flowing` eval
  check is added (it would be unpassable until the upstream fix lands).

- **eval robustness fixes (`keepassxc-keyring`).** The `ssh-agent` service check
  switched `is-active` → `is-enabled` (the socket-activated unit matches the
  layer's `enable` action); the direnv fish-hook check now references the current
  `ov-direnv.fish` path and tolerates a bash-login target where fish isn't
  per-user-configured.

- **VM eval readiness gate + disposable-bed crash recovery (`ov/eval_cmd.go`,
  `ov/eval_bed_run.go`).** Every VM `charly eval live` now runs `WaitForSSH` +
  `WaitForCloudInit` BEFORE any check, so the eval never tests a guest that is
  down, mid-cloud-init, or mid-restart (real synchronization primitives, not
  sleeps — the same preflight `VmDeployTarget.Emit` uses at deploy time). The bed
  runner adds a domain-death recovery: if a disposable bed's guest dies mid-eval,
  it restarts the domain + waits for sshd before the next eval-live retry instead
  of re-failing against a dead VM. The shared `cachyos-gpu-desktop-eval` check
  layer gained matching visible assertions (`vm-ssh-reachable`,
  `cloud-init-settled`).

- **Looking-Glass crash RCA (exonerates the kvmfr fix).** A GPU-bed R10 failed
  when the guest crashed (`qemu reason=crashed`) ~3s after a cuda-image load. A
  coredump RCA traced it to a NULL-pointer deref inside host-side
  `libspice-server.so.1.15.0` (spice-server 0.16.0) — the SPICE display-worker
  thread SIGSEGV'd QEMU on an `charly eval spice` probe connect (1-in-62 boots),
  independent of ivshmem size / kvmfr / VFIO (host↔guest propagation confirmed
  working). The kvmfr fix is correct and kept; the readiness gate's recovery
  makes the rare host-spice crash non-fatal to the R10.

- **`cloud-init-settled` check tolerance.** The pacstrap bed's cloud-init reports
  `status: error` (a recoverable `resizefs` module error — `btrfs` absent during
  early-boot resize on a pre-sized disk) but `extended_status: error - done` —
  i.e. FINISHED. The check now keys on `extended_status` (which carries the
  done/running phase) and fails only on `running`, matching the gate's tolerance.

- **eval-bed naming.** The cachyos disposable beds adopt the standard
  `eval-<descriptor>-<kind>` form: `eval-cachyos-gpu-vm` (the GPU bed, aligning the
  config with the skill that already used that name) and `eval-cachyos-vm` (the
  bootstrap bed). The GPU bed's `hostdevs:` block is reverted to PORTABLE (a PCI
  address is host-specific — add it locally for a GPU run; the GPU-gated checks
  are N/A on a portable bed). Both beds R10'd; `eval-cachyos-gpu-vm` passed 29/29
  (eval-live + post-update re-verify) with the RTX 4080 attached.

### 2026-05-29 — cachyos GPU VM eval: SPICE virtual monitor + deeper selkies verification + honest LG bed-scope

The `cachyos-coder` / `cachyos-gpu-vm` eval previously verified only that things
EXIST (KDE binaries present, sddm enabled, `nvh264enc` installed, `:3000`=200,
`/dev/kvmfr0` present) — a black or hung GPU-passthrough session would still pass.
This change adds a SPICE virtual monitor that proves the desktop actually RENDERS,
deepens the selkies stream verification (prove the stream is wired end-to-end,
not just that the port answers), and — after a source-grounded RCA — surfaces and
honestly drops the bed's Looking-Glass frame-flow check. Looking Glass itself is
unchanged (layer + `<shmem>` + `memory_backing` + kvmfr all intact for the
operator's monitor-attached workstation use); only the *unattended bed
verification* of LG frame-flow is incompatible with how LG-on-KWin-Wayland works
in practice.

- **SPICE virtual monitor on both VM entities** (`image/cachyos/vm.yml`). Added
  `graphics: spice` (UNIX socket) + a `spicevmc` channel + a virtio-gpu `primary`
  video head (replacing the dummy `vga`) to `cachyos-gpu-vm` and `cachyos-coder`,
  matching the arch VM's proven pattern. On the operator workstation the
  passed-through NVIDIA GPU drives the physical monitor (the operator sees the
  desktop directly); on the bed the virtio-gpu head is the seat-0 scanout the
  SDDM/Plasma session renders to. SPICE serves it so `charly eval spice screenshot`
  can capture the live desktop, the operator gains a 4th access path + a headless
  recovery console, and the bed proves the GPU-passthrough KDE session actually
  rendered. The NVIDIA hostdevs, Looking-Glass IVSHMEM, and `memory_backing`
  stay (orthogonal devices, retained for monitor-attached LG use).
- **SDDM auto-login** the deploy user into the Plasma Wayland session
  (`image/cachyos/layers/cachyos-kde-settings`): on the bed (no operator at the
  SDDM greeter) it guarantees a real KDE session exists at boot so SPICE and
  selkies have a desktop to capture, and on the operator VM it removes the
  greeter step.
- **Generic `wanted_by:` service-schema field added** (`ServiceEntry` +
  `ServiceRenderContext` + the systemd `service_template` `[Install]` block;
  unit test `TestRenderServiceWantedBy`). Any service can now declare
  `wanted_by: [<target>]` and the systemd unit gets a matching `WantedBy=`.
  Used by `looking-glass-host` to set `wanted_by: [graphical-session.target]`,
  so the LG service is pulled WHEN the session starts (not at early
  user-manager start, where there's no display).
- **Render-proof check** `desktop-renders-spice` (`image/cachyos/charly.yml`)
  — `charly eval spice screenshot` asserts `artifact_not_uniform: true`, so a
  solid-color/black/hung session FAILS (`assertArtifactNotUniform` samples
  pixels). The GPU-passthrough desktop must really render to pass.
- **selkies stream verification deepened** (`image/cachyos/charly.yml`).
  Two checks added beyond the prior `:3000`=200 / `nvh264enc` binary present:
  - `selkies-encoding-frames` — pixelflux's nested compositor (`wayland-1`)
    socket exists, the `:8081` capture backend is listening, and the
    `ov-selkies-selkies` journal shows the encoder actually started. Proves
    the pipeline is live, not just that the port answers.
  - `kde-selkies-html-content` — `curl https://127.0.0.1:3000/` returns
    selkies/pixelflux/WebRTC content (not just any 200 from traefik), proving
    the web UI is wired end-to-end (traefik → `:8081` → pixelflux's bundle).
- **LG frame-flow bed check honestly dropped; LG infra check kept**
  (`image/cachyos/charly.yml`). RCA (source-grounded — see
  `gnif/LookingGlass` `host/.../portal.c` and `KDE/xdg-desktop-portal-kde`
  `src/screencast.cpp` on `master`): on KWin Wayland, `looking-glass-host`'s
  PipeWire backend is the only KWin path that yields the actual output, but
  the xdg-desktop-portal ScreenCast `Start` call **always** shows an
  interactive source-selection dialog UNLESS the client sends `persist_mode`
  + a valid `restore_data` blob — and `looking-glass-host` (every version,
  including upstream master) sends **neither** (its `portal.c` requests no
  persistence; only the per-session `handle_token` is present, which is not
  a persistence token). KDE's `screencast.cpp` does not consult the
  `PermissionStore` for the picker decision, and the `kde-authorized`
  pre-auth table covers RemoteDesktop only, not ScreenCast. So there is no
  app-targeted non-interactive grant on KWin. The alternative XCB backend
  grabs KWin's XWayland root (KWin sizes it to the largest possible mode,
  e.g. 10684×2160), which overflows the 64 MiB IVSHMEM and SIGABRTs in
  `lgmpHostMemPtr`. The bed runs unattended — neither path can produce
  frames without an interactive click — so the `looking-glass-frames-flowing`
  check was removed. The infra check `looking-glass-guest` (binary
  installed + IVSHMEM node present + kvmfr loaded) stays — it IS verifiable
  headless and proves the wiring is correct. On the monitor-attached
  operator VM, the operator clicks "Share" once per session at the physical
  monitor and frames flow normally; that's an operator-side verification of
  a feature whose wiring the bed already proved.

### 2026-05-29 — `charly vm`: per-VM disk/seed paths + SMBIOS credential delivery (SSH key injection made authoritative)

Surfaced while bringing up the operator `cachyos-coder` VM (the deliverable of the
cachyos-coder cutover below): three real `charly vm` defects in the disk/seed/SSH-key
path, each RCA'd before any fix and live-verified on the operator VM.

- **Shared disk/seed output path → cross-VM seed reuse.** `charly vm build`/`create`
  wrote `disk.qcow2` + `seed.iso` to a SHARED `output/qcow2/`, not per-VM. So
  `charly vm create cachyos-coder` (run after the disposable `cachyos-gpu-vm` bed)
  silently adopted the torn-down bed VM's `seed.iso` — whose embedded SSH key
  mismatched cachyos-coder's own `id_ed25519` — so cloud-init injected the wrong
  key and the deploy could not authenticate. Fixed with one `vmDiskDir(vmName)`
  helper → `output/qcow2/<vm>/`, applied to every disk/seed site (build / create /
  destroy / snapshot) + the unwired clone path; dead `resolveQcow2Path` removed.
  `charly vm create` now fails with a clear "run charly vm build" error instead of
  adopting a sibling's disk, and `charly vm destroy --disk` removes only the VM's own
  dir (not every VM's).
- **SMBIOS OEM credential never reached the guest.** The libvirt domain carried
  `<sysinfo type='smbios'><oemStrings>` (the systemd `tmpfiles.extra` SSH-key
  credential) but NO `<os><smbios mode='sysinfo'/>`, so QEMU defined the OEM
  strings yet never presented them to the guest's DMI — `systemd-creds` /
  `systemd-tmpfiles` never saw the credential and the entire SMBIOS key-injection
  channel was silently dead (the deploy survived only because cloud-init also
  injects the key). `BuildLibvirtDomainXML` now emits `<smbios mode='sysinfo'/>`
  whenever an OEM credential is present.
- **SMBIOS key made authoritative without breaking cloud-init.** The per-VM SSH
  key is now written to a root-owned, cloud-init-proof `/etc/ssh/authorized_keys.d/<user>`
  plus a `sshd_config.d` drop-in widening `AuthorizedKeysFile` to check BOTH that
  file AND `~/.ssh/authorized_keys` (applied by systemd-tmpfiles before sshd
  starts) — so the SMBIOS/deploy key is always accepted even if cloud-init later
  rewrites the user's own `authorized_keys`. cloud-init keeps deploying its keys
  into `~/.ssh` (its domain); the key is also written there as a fallback for any
  sshd that ignores the drop-in.

Live-verified on the operator `cachyos-coder` VM: deploy authenticates,
`/etc/ssh/authorized_keys.d/cachy` carries the key, `sshd -T authorizedkeysfile`
lists both locations, cloud-init's key is in `~/.ssh`, and KDE / selkies `:3000`
(200) / Looking-Glass IVSHMEM all work. Go tests: `TestVmDiskDir_PerVM`,
`TestKeyToUserTmpfilesD_SmbiosPriority`, `TestRenderDomainXML_SmbiosSysinfoMode`.

### 2026-05-29 — `cachyos-coder`: full KDE GPU workstation VM synced to the host (monitor + Looking Glass + KDE-selkies stream)

Evolved the headless `ov-cachyos-gpu` operator VM into `cachyos-coder` — a full
graphical CachyOS KDE Plasma workstation in a GPU-passthrough VM, brought into
sync with the operator's host package set and usable three ways on the one
RTX 4080: a physical monitor (SDDM/Plasma on DRM), Looking Glass locally
(IVSHMEM + dummy scanout + the `looking-glass-host` guest layer; client on the
host), and a remote KDE-selkies WebRTC browser stream (NVENC, port 3000) of a
nested Plasma session. Supersedes `ov-cachyos-gpu` (vm/deploy/bed renamed; the
old entity deleted in the same change).

Package selection was reverse-resolved (operator directive) to top-level
packages + the dependency-pulling `plasma-desktop` meta and CachyOS's own
curated KDE-Desktop netinstall set — never leaf enumeration nor the giant
`kde-applications` group. Host-hardware/boot/firmware/network packages
(amd-ucode, AMD-GPU drivers, linux-firmware, bluez, NetworkManager, disk/boot
tooling, …) are excluded by design — inert or harmful in the VM.

New layers (main repo): `kde-desktop` (Plasma + SDDM + graphical.target via
`plasma-desktop` deps), `fonts-extended`, `desktop-media`, `cachyos-extras`
(the dev/CLI gap + AUR cloudflared/gvisor), `looking-glass-host` (kvmfr DKMS +
the Linux capture app), `kde-selkies` (KDE Plasma Wayland nested in pixelflux,
streamed over the reused selkies WebRTC transport), `nvenc-headers` (ffnvcodec).
Vendored in `image/cachyos`: `cachyos-kde-settings` (theming/settings/SDDM
theme); `nvidia-driver` extended with egl-wayland + opencl + nvidia-settings +
the VA-API driver for a Wayland KDE session on the proprietary driver.

NVENC streaming required un-stubbing pixelflux's encoder: `selkies/build.sh` now
auto-detects CUDA + the NVENC SDK headers and builds the real `NvencEncoder`
when present (the new `cuda-arch-builder` image = arch-builder + cuda +
nvenc-headers), keeping the stub — and the unchanged container `selkies-desktop`
family — when absent (R3: one capability-driven build.sh, no per-image fork).

Service-exec portability (R3, generic): the systemd service renderer now
resolves supervisord's `%(ENV_HOME)s` / `$HOME` in `exec:`/`env:` against the
destination home (the deferred `{{.Home}}` token for host/vm, substituted per
target by `InstallPlan.ResolveHome`). Previously a reused supervisord exec
yielded a broken systemd `ExecStart`, and the service home came from the build
host (`os.UserHomeDir()`) — the service-side instance of the VM `$HOME` bug.
This is what lets the supervisord-designed selkies stack run as systemd units in
the VM guest. (`ov/service_render.go`, `ov/install_build.go`,
`ov/install_plan.go`.)

R10-surfaced (systemd init purity + user-session architecture). The bed deploy
exposed that the selkies/desktop stack carried container-only assumptions that
break on a systemd VM, each RCA'd and fixed generically:

- **systemd is the one init system on a systemd target.** A VM/host deploy no
  longer installs supervisord: `pruneContainerInitForSystemd`
  (`ov/deploy_add_cmd.go`) drops the `supervisord` init layer from the resolved
  layer order on host/vm targets (pod/k8s/OCI builds keep it — it IS their
  init). The ~36 layers that `require: supervisord` for graph ordering are
  unaffected; their `service:` entries render as systemd units.
- **`copy: to: ${HOME}/...` is home-resolved.** It was left literal and
  `PutFile` (single-quoted, under sudo with HOME=/root) created a real
  `/home/<user>/${HOME}/...` dir. Now tokenized at compile (`TaskStep.To` via
  `ExpandPath`, which also gained `${HOME}`) and resolved per-target by
  `InstallPlan.ResolveHome` — VM and local both.
- **Scope-aware service enable on the VM.** `VmDeployTarget.enableServiceUnit`
  honors `scope: user` (linger + `systemctl --user enable` in the deploy user's
  instance) — the SSH counterpart of LocalDeployTarget's scope-aware enable
  (the VM target had ignored scope, aborting on a user-scope unit). Enable is
  hard, start best-effort (GPU/session services start on the post-reboot boot).
- **User-session services.** pipewire / selkies / selkies-fileserver /
  kde-selkies / looking-glass-host run as `scope: user` so they get the user
  session bus + per-user runtime dir + `$HOME`. `XDG_RUNTIME_DIR` moved off the
  desktop layers onto the supervisord (container-init) layer — container gets
  `/tmp`, the systemd VM gets `/run/user/<uid>` from the user manager. The
  selkies capture-server is invoked through `$HOME/.pixi/.../python` (home-
  agnostic) instead of its baked `#!/home/user/...` shebang. The deploy user is
  added to `video`/`render` for GPU access.

### 2026-05-29 — VM deploy correctness: one render path, deploy-time `$HOME`, cross-host builders, guest-user virtiofs idmap

Deploying the real `ov-cachyos-gpu` operator VM (the deliverable of the earlier
2026-05-29 cutover below) surfaced a chain of VM-deploy bugs that no unit test
or disposable-bed run had caught — the bed used throwaway inputs (a world-
readable `/tmp` virtiofs source; no npm-builder layer) that masked them. This
cutover RCA'd and fixed all of them in one working tree, with the operator VM as
the live proof.

**Render consolidation (the trigger — "check all renders use the same code
path").** `LocalDeployTarget` and `VmDeployTarget` had drifted into divergent
renderers. Unified onto ONE path: `renderTaskCommand` / `renderFallbackPkgCmd`
became package-level (used by both targets); `copy:` tasks stage through the
executor's `PutFile` (a local `install` vs `scp+install` over SSH) instead of a
rendered `install <hostLayerDir>/<f> <dst>` that referenced a host path absent
in the guest (the `socat relay-wrapper` 404); env.d rendering shares
`renderEnvdBody`. The VM AUR builder's wrapper was dropping privileges twice
(`su - user` around a script that already configures NOPASSWD-wheel and drops
via `sudo -u`), failing every AUR layer with `Permission denied` on the sudoers
write — fixed to run the inner script as container-root, matching the local path.

**pacman.conf repo layout (image/cachyos).** The hand-written cloud-init
`pacman.conf` declared `[cachyos-extra-v3]` (404s `libyuv` via a malformed DB
entry) and `[cachyos-extra]` (returns HTML at `$arch`, `Unrecognized archive
format`). Aligned to the canonical `build.yml` `renderPacstrapExtraConf` layout
— `cachyos-v3` / `cachyos-core-v3` (x86_64_v3) + `cachyos` ($arch) via
`mirror.cachyos.org`, with `libyuv` resolving from Arch `extra`. (NOT a CDN
outage — the operator correctly rejected that premature conclusion; the
divergence from the canonical conf was the root cause.)

**D1 — deploy-time `$HOME` resolution (pre-existing, systemic).** `~`/`$HOME` in
a layer's `env:` / `path_append:` / shell-snippet destination was expanded at
**compile** time against `ResolvedImage.Home`. For a `target: vm` deploy the
synthetic plan's Home was the **host operator's** home, so env.d on the guest
read `export NPM_CONFIG_PREFIX=/home/atrawog/.npm-global` and the managed
profile block landed in a root-created `/home/atrawog/.profile` — not the guest
user's `/home/cachy`. Fix: the compiler now emits the deferred `{{.Home}}` token
(`HomeToken`); each `DeployTarget` resolves it at emit via
`InstallPlan.ResolveHome(home)` against the REAL destination home — `img.Home`
for OCI/pod-overlay, host home for local, the SSH-resolved **guest** home for
vm. `cmd:` task bodies are left to shell-expand `$HOME` at runtime. The
container BUILD path (`generate.go`) is unchanged — there `img.Home` is the
runtime home. (RCA verdict: pre-existing, not a regression from the render
consolidation; HEAD's old VM renderer consumed the same compile-baked values.)

**D2 — env.d-sourcing managed block on the VM path.** `VmDeployTarget` never
called `EnsureManagedBlock`, so the per-layer env.d files were written but never
sourced — PATH never picked up `~/.npm-global/bin`. The managed-block writer is
now executor-based (`EnsureManagedBlockVia`, `GetFile`/merge/`PutFile`) and
shared by both targets; the os-based `EnsureManagedBlock` is a thin wrapper.
The guest's login shell is detected from the guest `/etc/passwd`
(`detectGuestShell`) since the guest default may differ from the operator's
(CachyOS ships fish).

**D3 — cross-host npm/pixi/cargo builders for VM deploys.** `VmDeployTarget`
previously implemented only the `aur` builder; npm/pixi/cargo were skipped under
`--skip-incompatible`, so the AI-CLI layers (`claude-code`, `codex`, `gemini`,
`oracle`, `forgecode` — all npm-builder `package.json` layers) silently never
installed on the VM. `execHomeArtifactBuilder` now builds them on the host with
HOME bind-mounted AS the **guest home path** (so npm shebangs / cargo rpaths /
pixi activation scripts bake the path the guest will use), then tars the home
subdirs (`~/.npm-global`, `~/.pixi`, `~/.cargo`; caches excluded), scp's the
tarball in, and extracts it into the guest `$HOME` as the guest user.

**D4 — guest-user virtiofs idmap.** libvirt's default rootless
`qemu:///session` idmap maps **guest-root → the host operator**, so a host-home
passthrough virtiofs share was `root:root` inside the guest and the interactive
user (`cachy`, uid 1000) got `Permission denied` — `/workspace` was mounted but
unusable. `ensureVirtiofsIdmap` (paired with `ensureVirtiofsSharedMemory`)
auto-injects an `<idmap>` mapping the guest's primary user (uid/gid 1000) to the
host operator, with the rest in the operator's `/etc/subuid`/`/etc/subgid`
range, so the share is owned by — and writable as — the guest user. An
author-declared idmap, a non-passthrough accessmode, or a missing subordinate-ID
range leave libvirt's default untouched.

**R10-surfaced fixes (the iterative debugging the disposable bed caught).** The
`eval-cachyos-coder-vm` bed R10 caught seven further real bugs, each RCA'd before
any fix (per R1) and re-verified to a clean `PASS (steps=11)`:

- **`SSHExecutor.ResolveHome` `bash -c` → `bash -s`.** ResolveHome passed its
  script as a `bash -c <script>` REMOTE argv; ssh space-joins remote-command
  args into one string and the guest shell re-splits on whitespace, so
  `bash -c printf %s "$HOME"` ran bare `printf` (no format) → exit 2. The D1
  guest-home preflight (which has no fallback, unlike the `eval_cmd.go` caller
  that silently masked it with `os.Getenv("HOME")`) turned this latent bug into
  a hard deploy abort with an EMPTY guest ledger. Fixed by feeding the script
  over stdin to `bash -s` (the transport `RunCapture`/`RunUser` already use) —
  one shared method, fixing both call sites.
- **nvidia-container-toolkit install-time CDI hook.** A fresh `nvidia-container-toolkit`
  install runs an `nvidia-ctk-cdi.hook` alpm hook (`nvidia-ctk cdi generate`)
  that fails pre-reboot ("NVML: Driver Not Loaded" — the passed-through GPU's
  driver only loads after the `nvidia-driver` layer's reboot), making `pacman`
  exit non-zero and aborting the deploy at the nvidia layer. Disabled on the VM
  (cloud-init symlinks the hook to `/dev/null`), with a post-reboot
  `ov-nvidia-cdi` oneshot regenerating CDI once the driver is up. (The operator
  VM had masked it: an earlier iteration already had nvidia-utils, so its deploy
  hit a no-op; the fresh disposable bed exposed it.)
- **Cross-host builder cleanup `rm` (D3).** `execHomeArtifactBuilder` placed the
  artifact tarball via `PutFile` (which runs `install` under `sudo bash`, so the
  file is root-owned) into the sticky `/tmp`, then its extract script's `rm` ran
  as the GUEST user → "Operation not permitted" under `set -e`. The tar EXTRACT
  succeeded (claude installed), only the cleanup failed. Fixed: extract as the
  guest user (artifacts guest-owned), remove the root-owned tarball as root.
- **Cold-boot cloud-init sshd flap (operator VM deploy).** On first boot
  cloud-init regenerates the SSH host keys + restarts sshd AFTER the initial
  sshd start (after `WaitForSSH` already passed), so the EnsureOvInGuest scp
  raced the restart ("kex_exchange_identification: Connection reset by peer").
  Bootstrap VMs (pacstrap/debootstrap) skipped `WaitForCloudInit` (it gated on
  `cloud_image` only), so nothing waited for cloud-init to settle. Fixed: run
  `WaitForCloudInit` for ANY VM with a cloud-init seed (`spec.CloudInit != nil`),
  and make it retry until an ssh connection SURVIVES `cloud-init status --wait`
  (the deterministic "sshd stable" signal — not a sleep), tolerating a non-zero
  cloud-init result.
- **env.d aggregator never loaded in bash login (AI CLIs not on PATH).**
  `ShellInitFilePath(bash)` wrote the env.d-sourcing managed block to
  `~/.profile`, but a bash login shell sources the FIRST of `~/.bash_profile` /
  `~/.bash_login` / `~/.profile` — and the Arch/CachyOS default `~/.bash_profile`
  (`. ~/.bashrc`) means `~/.profile` is NEVER read. So the AI CLIs installed in
  `~/.npm-global/bin` were absent from the operator's login PATH (`bash -lic
  command -v claude` → not found) despite being installed. Fixed:
  `ShellInitFilePath(bash)` → `~/.bashrc` (sourced by interactive shells and by
  login via `~/.bash_profile`). The bed eval now asserts the AI CLI resolves on
  the interactive-login PATH (`bash -lic`), not merely that the block exists.
- **Selkies web-UI copy hardcoded `/home/user` (stream `:3000` 404).** The selkies
  layer's web-copy task did `cp /home/user/.local/share/selkies-build/web/* …` —
  the container build-user home. On a host/VM deploy the deploy user is `cachy`, so
  the copy was a silent no-op and `/usr/local/share/selkies/web` stayed empty;
  traefik's fileserver served nothing → `curl https://127.0.0.1:3000/` returned 404.
  Fixed: resolve the build-user home via `getent passwd 1000 | cut -d: -f6` (the cmd
  runs as root, so `$HOME` would be `/root`), so it works identically in a container
  build (uid 1000 = `user`) and on a cross-host deploy (uid 1000 = the deploy user).
- **Deploy ran a layer's tasks BEFORE its builder (the deeper `:3000` cause).**
  `BuildDeployPlan` emitted task steps before builder steps, but the image build
  COPYs every pixi/npm/cargo builder's `/home` into the main stage UP FRONT (before
  any layer install step). So on a cross-host deploy the selkies web-copy task ran
  before `execHomeArtifactBuilder` extracted the pixi/`build.sh` output
  (`~/.local/share/selkies-build`) into the guest home — the copy found nothing,
  then the artifacts appeared a moment later. Fixed: emit builder steps before task
  steps in `BuildDeployPlan`. A builder runs in an isolated stage/image and never
  consumes the layer's own tasks, so builder-first is always safe; the OCI target
  hoists builder stages regardless of step order, so the generated Containerfile is
  byte-identical (verified by regenerate + diff: only time-derived builder tags
  differed).

### 2026-05-29 — full ov-cachyos GPU workstation VM (autostart + virtiofs /workspace + full guest agent)

Built on the 2026-05-28 GPU-passthrough stack: a persistent, autostarting
CachyOS GPU **workstation** VM (`ov-cachyos-gpu`) with the full ~30-layer
ov-cachyos dev stack, the NVIDIA RTX 4080 SUPER passed through, the operator's
`/home/atrawog` shared in at `/workspace`, the full qemu-guest-agent surface, and
a 1 TB lazily-allocated disk.

**Main repo (generic machinery):**

1. **VM autostart** — new `VmSpec.Autostart` field (`ov/vm_spec.go`).
   `runVmSpecCreate` (`ov/vm_create_spec.go`) sets libvirt's domain autostart flag
   via `setDomainAutostart` (`ov/vm_libvirt.go`, `DomainSetAutostart`) and, because
   charly VMs run under `qemu:///session` (no portable user-level `virtqemud.socket` —
   Arch ships none), calls `ensureBootAutostartPrereqs` (`ov/vm.go`): idempotent
   `loginctl enable-linger <user>` + writes/enables a per-VM user oneshot
   `ov-autostart-<domain>.service` that `virsh -c qemu:///session start`s the
   domain at boot (`charly vm destroy` removes it via `removeAutostartUserUnit`). The
   libvirt flag is a domain property (not XML), so it survives redefinition and is
   re-asserted on every create/rebuild.
   `ValidateVmSpec` rejects `autostart: true` with `backend: qemu`. Additive
   optional field — deliberately NO schema-version bump (matches how
   `backend`/`filesystems`/`channels` were added; bumping would force a needless
   cross-repo re-stamp of every project file via `calver-schema`).
2. **virtiofs robustness** — `ensureVirtiofsSharedMemory`
   (`ov/libvirt_yaml_bridge.go`) auto-pairs `<memoryBacking><source type='memfd'/>
   <access mode='shared'/>` whenever a `driver: virtiofs` filesystem is present and
   no shared backing was declared (an explicit backing is honored). `mapFilesystem`
   now renders the optional virtiofsd `binary:` knobs. `mapChannel` learned the
   bare `type: unix` (no path) guest-agent idiom → a libvirt-managed unix socket
   (`<source mode='bind'/>`); previously the structured `channels:` path silently
   dropped the channel type for the agent. `validateLibvirtFilesystem` requires
   source+target and checks driver/accessmode enums (a `/home` source is allowed —
   a share's whole purpose is to expose a host dir).
3. **1 TB lazy disk** — confirmed no code change needed: the bootstrap path's
   `truncate` (sparse raw) + `qemu-img convert -O qcow2` (no `preallocation` →
   default off) already yields a sparse qcow2 that grows on demand. `disk_size: 1T`
   is a virtual ceiling.
4. **New `workspace-mount` layer** (`layers/workspace-mount/`) — systemd
   `workspace.mount` (virtiofs tag `workspace` → `/workspace`), enabled for boot,
   skip-aware eval.
5. **`qemu-guest-agent` layer** — already cross-distro (same package name on
   Arch/Fedora); extended with `/etc/qemu/qemu-ga.conf` (explicit full-RPC surface)
   + the standard fsfreeze hook dispatcher (`/etc/qemu/fsfreeze-hook` +
   `fsfreeze-hook.d/`) for application-consistent snapshots.

`virtiofsd` was already a `pkg/arch/PKGBUILD` dependency (R9 pre-satisfied).

**CachyOS submodule (`image/cachyos`):**

- `ov-cachyos-gpu` `kind: vm` — bootstrap/pacstrap UEFI, 12 vCPU / 64 GiB / 1 TB
  sparse, `autostart: true`, NVIDIA hostdevs, guest-agent channel, virtiofs
  `/home/atrawog → workspace`.
- `ov-cachyos-gpu` `kind: deploy` (`target: vm`, NOT disposable) — the full
  ov-cachyos layer stack + `nvidia-driver` + `qemu-guest-agent` + `workspace-mount`.
- The disposable `eval-cachyos-gpu-vm` bed extended to exercise autostart +
  virtiofs + guest-agent on a throwaway share — the R10 vehicle for the generic
  machinery (the operator VM is non-disposable and uses the same proven code).

### 2026-05-28 — VFIO GPU passthrough + nested GPU eval stack (host → GPU-passthrough VM → CUDA container)

Added end-to-end support for passing a physical NVIDIA GPU through to an
`ov`-managed VM and running a CUDA container inside it, plus the disposable
R10 bed that proves the whole nested stack on real hardware (verified live on
an RTX 4080 SUPER bound to vfio-pci, host on the AMD iGPU).

**Main repo (generic machinery):**

1. **Host VFIO/IOMMU detection** — `DetectVFIO` in `ov/devices.go` (pure
   `scanVFIO(sysfsRoot, cmdlinePath)`, testable like `DetectGPU`): parses
   `/proc/cmdline` for the IOMMU flag, enumerates `/sys/bus/pci/devices`
   GPU+audio classes, and resolves each device's driver + IOMMU group +
   group members. Surfaced two ways that share the one detector: a new
   `charly vm gpu` verb (`status` reports IOMMU readiness; `list` prints a
   ready-to-paste `libvirt.devices.hostdevs:` block with `managed: "yes"`
   covering the whole IOMMU group) and an informational `charly doctor`
   "VFIO / GPU passthrough" check group.
2. **libvirt passthrough rendering completed** — `mapHostdev` now emits the
   previously-dropped `ROM` (`<rom bar=…/file=>`) and PCI `Driver`
   (`<driver name='vfio'/>`) elements; `buildDomainFeatures` now emits
   `KVM.Hidden` (`<kvm><hidden state='on'/>`) and `HyperV.VendorID`
   (`<hyperv><vendor_id …/>`) — the NVIDIA Code-43 workarounds that were
   defined-but-unwired (the "not mapped … via xml_passthrough" comment is
   gone). Hostdev validation (type/managed enum, hex PCI source fields)
   added to `ValidateLibvirtDomain`.
3. **`RebootStep` IR + `reboot:` layer field** — a layer declaring
   `reboot: true` emits a trailing `RebootStep`. Only `VmDeployTarget`
   acts on it (reboots the guest over SSH and waits for it to return —
   deterministically, via a boot_id-change poll, not a sleep); OCI / pod /
   k8s skip it (no machine at build time); `LocalDeployTarget` skips +
   warns (never reboots the operator host unattended). This is what lets a
   kernel-module layer load its module on a clean boot.
4. **Host→guest image transfer** — `charly vm cp-image <vm> <ref> [--as <tag>]`
   + the reusable `TransferImageToGuest` helper stream a locally-built image
   into a VM guest's podman (`podman save | scp | podman load`), idempotent
   and offline (no registry round-trip). The `kind: eval` VM-bed runner now
   builds each nested pod child's image on the host and loads it into the
   guest (and re-loads + re-evaluates after the fresh `charly update`), so a
   nested pod's locally-built image is available inside the VM.
5. **Rootless-VFIO host-prereq detection** — the live test surfaced two host
   prerequisites that fail cryptically otherwise, so `charly vm gpu status` and the
   `charly doctor` "VFIO / GPU passthrough" group now report them: (a) the
   **RLIMIT_MEMLOCK** limit (VFIO pins all guest RAM, so rootless
   `qemu:///session` needs a limit ≥ guest RAM; the 8 MiB session default is
   too low and yields "cannot limit locked memory"), and (b) **/dev/vfio/<group>
   accessibility** (root-only by default). `charly udev` now also installs a
   `SUBSYSTEM=="vfio", GROUP="kvm"` rule so `charly udev install` grants persistent
   group-node access for passthrough.

**CachyOS submodule (`image/cachyos`, the consumer):**

- `cuda-smoke` layer + `cuda-eval` image (`base: cachyos.nvidia` + a baked,
  nvcc-compiled vector-add that prints `CUDA-OK`; built with `g++-15` since
  CUDA 13.2's nvcc rejects gcc 16). This is the CachyOS CUDA image under test.
- `podman` layer (rootful podman engine for the guest — minimal, distinct from
  `container-nesting`'s rootless-nesting config).
- `nvidia-driver` layer (vendored locally): `nvidia-open-dkms` + matched
  `linux`/`linux-headers` + the dkms toolchain (built against the guest kernel,
  no prebuilt-vs-running skew), blacklists nouveau, regenerates the initramfs,
  `reboot: true`.
- `cachyos-gpu-vm` VM — an **Arch cloud_image** substrate (the proven path
  `eval-k3s-vm` uses; ships working pacman + Arch repos for the GPU stack),
  `firmware: bios` (the Arch cloud image won't boot under UEFI/OVMF — stale
  BOOTX64.EFI), `backend: libvirt`. Committed **portable** with NO hostdev
  block (a PCI address is host-specific; `charly vm gpu list` generates it to add
  locally for a live run). The CachyOS *bootstrap* substrate was ruled out: on
  a rootless host pacstrap can't mount sysfs and the resulting guest ships no
  `/etc/pacman.conf`, so it can't be a runtime package host. **Headless compute
  passthrough needs `rom: {bar: off}` on the GPU hostdev** — otherwise SeaBIOS
  hangs executing the GPU's VGA option ROM and the guest never boots.
- `eval-cachyos-gpu-vm` `kind: eval` bed: applies `podman` + `nvidia` +
  `nvidia-driver` to the guest, loads `cuda-eval` in as
  `localhost/ov-cuda-pod:latest`, and its deploy-scope checks run the CUDA
  container in the guest (`sudo podman run --device nvidia.com/gpu=all … →
  CUDA-OK`). Every GPU/CUDA check gates on an active in-guest driver and passes
  with an N/A note when no GPU is present, so the bed stays host-portable (same
  skip-when-no-device pattern as the `ov-cachyos` nvidia-ctk/CDI probes).

### 2026-05-26 (later) — `charly update` disposable enforcement + deploy.yml round-trip preservation + cross-deploy quadlet-refresh Image= preservation

Follow-up cutover to the morning's sidecar-sweep + pixi-pytest fixes.
Three more latent bugs in `ov`'s update path that were documented but
not fixed in the earlier cutover (per CLAUDE.md R2 "escalated to the
operator for explicit re-scoping") are now landed in source + tests +
deployed binary + R10-verified end-to-end.

1. **`charly update <image> -i <instance>` did NOT enforce `disposable`.**
   The dispatcher in `ov/update_deploy_dispatch.go::dispatchByDeployTarget`
   resolved the deploy node and immediately handed off to the per-
   target update helper without ever consulting `node.IsDisposable()`.
   `charly update versa -i ecovoyage` therefore destroyed + recreated the
   production tenant unattended even when the operator had explicitly
   set `disposable: false` on the entry. Fix: added a
   `checkUpdateDisposable(node, image, instance)` helper that refuses
   with the canonical refusal text from `/ov-internals:disposable`
   (instance-aware: the remediation hint shows the full `<base>/<inst>`
   key when an instance is set). Wired into the dispatcher right after
   `resolveUpdateDeployNode`. 6 sub-test regression coverage:
   explicit-true allowed, ephemeral-implies-disposable allowed,
   absent-flag refused, explicit-false refused, instance-key formatting,
   lifecycle-dev-alone-does-NOT-authorize.

2. **deploy.yml re-serializer DROPPED explicit `disposable: false`.**
   `DeploymentNode.Disposable` was declared as `bool` + `yaml:
   "disposable,omitempty"`. Go YAML treats `false` as the zero value of
   `bool`, so `omitempty` silently elided the field on every save. The
   operator's explicit lockdown intent vanished on the next
   `saveDeployState` call — visible regression: `disposable: false`
   reappears after every `charly update`/`charly config` invocation. Fix:
   changed type to `*bool`. nil = absent (default `false` behavior);
   `&false` = explicit lockdown (preserved on write); `&true` =
   explicit authorization. Same pattern already in use at
   `vm_instance_override.go:42`. `IsDisposable()`, ephemeral
   auto-promotion (`deploy.go:1156`), and `saveDeployState`
   (`deploy.go:2004`) updated to handle the indirection;
   `eval_bed_run.go:142` switched from `node.Disposable` deref to
   `node.IsDisposable()` (the bed copy's bool sentinel must cover the
   `ephemeral implies disposable` case the source carried via Ephemeral).
   Round-trip regression test (`TestDeploymentNode_DisposableFalseRoundTrip`)
   asserts all three forms (`true`/`false`/absent) round-trip
   faithfully and `IsDisposable()` returns the right answer in each.

3. **`updateAllDeployedQuadlets` cross-polluted sibling deploys'
   Image= lines.** When `charly update <bed>` ran its env-refresh sweep
   across every other deployed quadlet, it re-resolved each sibling's
   `Image=` via `resolveShellImageRef("", imageName, "")`. That helper
   walks every local image carrying the matching
   `ai.opencharly.image` label, which includes the bed's per-deploy
   alias re-tag from `bumpDeployAlias` (which inherits the base's
   labels). On a tie (same label-CalVer, same tag-CalVer — the alias
   IS the base, same content), the existing sort tiebreaker SHOULD
   have preferred the bare-base ref, but in practice the just-rebuilt
   bed alias landed first and overwrote ecovoyage's Image= line to
   `eval-versa-pod:<calver>`. Fix: at the top of each per-deploy loop
   iteration, read the existing quadlet's `Image=` line via the new
   `extractQuadletImageLine(qpath)` helper and use THAT as the
   `imageRef` for the regenerated quadlet. The fresh
   `resolveShellImageRef` lookup remains only as a fallback when the
   existing quadlet somehow has no Image= line. The downstream
   `imageRef = resolveShellImageRef(meta.Registry, imageName, "")`
   replacement near the bottom of the loop (which was overwriting the
   preserved value at the last minute) is also removed.
   `updateAllDeployedQuadlets`'s documented purpose was always "pick
   up env_provides / env_accepts changes" — it should NEVER advance
   a sibling deploy's Image= choice. The canonical way to move tags
   is `charly update <deploy>` (which routes through
   `rewriteQuadletImageLine` with the operator-authorized tag).
   `TestExtractQuadletImageLine` covers 4 cases: Image= present at
   top of [Container], Image= present alongside a sidecar Pod=
   directive (proves the regex doesn't get confused), absent Image=
   returns empty (caller falls back), missing file errors cleanly.

**R10**: `charly eval run eval-versa-pod` 8/8 PASS in 47 min. eval-live
124 / 124 (no regression). Bug 1 live-verification: the
`~/.config/containers/systemd/ov-versa-ecovoyage.container` Image=
line was `versa:2026.146.1239` before the R10 and STILL
`versa:2026.146.1239` after the R10 — identical content, no
cross-pollution. The only quadlet diff is one OV_MCP_SERVERS line
adding a transient `marimo @ ov-eval-versa-pod` discovery entry
(the env-refresh's documented job — registering the bed's MCP
endpoint with consumers). Bug 2A live-verification:
`charly update versa -i ecovoyage` refuses with the exact remediation
message from the new code. Bug 2B live-verification:
`disposable: false` persists in deploy.yml across the refused
update attempt (the write path would have dropped it before).
Operator data preserved (bind mount + named volume untouched);
ecovoyage container untouched (no destroy + restart triggered).

### 2026-05-26 — `charly config remove` sidecar-sweep + versa pixi pytest fix; versa/ecovoyage cut over to fresh image with disposable lockdown

Two latent bugs surfaced during a routine `versa` ecosystem refresh
(drop stale `versa` operator pod, R10 the versa image via
`eval-versa-pod`, then update `versa/ecovoyage` to the freshly-built
tag) and were fixed in the same cutover:

1. **`charly config remove <image>` swept sibling instances of the same
   image** (R3 — naive filename-prefix match without an instance-
   boundary anchor). The sidecar-disable loop at
   `ov/config_image.go:1100-1113` matched every quadlet starting with
   `ov-<image>-` and ran `systemctl --user disable --now <unit>` on it.
   When the operator removed an orphan `versa` operator pod, the
   loop also disabled the unrelated production `ov-versa-ecovoyage`
   service — a clean shutdown via the supervisord drain, but a
   shutdown nonetheless. The user invariant
   ("cross-kind name reuse is permitted and encouraged" — CLAUDE.md)
   means `ov-<image>-<instance>.container` is NOT a sidecar of pod
   `ov-<image>.pod`; only true sidecars carry the
   `Pod=<podname>.pod` directive in their `[Container]` section. Fix:
   identify sidecars via the `Pod=` directive, not the filename
   prefix. Implemented `findPodSidecarQuadlets` (`ov/sidecar.go`) +
   3 regression tests covering instance-aware scoping, the
   exclusion of sibling instances, and the empty-quadlet-dir case;
   call site at `config_image.go:1100-1118` rewritten to use the new
   helper with stderr logging of every swept service. Live-verified:
   `charly remove eval-versa-pod` (the R10 bed teardown) no longer
   touches `ov-versa-ecovoyage` (which stayed `Up` uninterrupted).

2. **`versa` GPU-graph eval probes failed on a fresh build because
   `pytest` was missing from the marimo layer's pixi env.** Latent
   since 2026-05-15 (the `f4b9c50` commit that introduced cugraph +
   cuml + nx-cugraph + pylibcugraph + torch-geometric + graphistry
   and the `versa-graph-imports` probe but never declared pytest).
   Mechanism is an upstream cupy packaging defect: cupy ships
   `testing` as `importlib.util.LazyLoader`
   (`cupy/__init__.py:1156-1173`); `cupy/testing/__init__.py:50`
   eagerly imports `cupy.testing._random`; `_random.py:11` does
   `import pytest` at module top. torch 2.11's `library.custom_op`
   decorator runs `inspect.getmodule(frame) → hasattr(module,
   "__file__")` during fake-op registration, which trips the
   LazyLoader and forces the cupy.testing chain. The joint sequence
   `import cugraph; import torch_geometric` therefore needs
   `pytest` in the env, or it `ModuleNotFoundError`s deep in
   torch's fake-op machinery. Downstream fix: add `pytest = "*"`
   to `layers/marimo/pixi.toml` `[pypi-dependencies]` (pure-Python
   wheel — does not break the `no-build = true` invariant the
   `apache-airflow` pin requires). Lockfile regenerated cleanly:
   `+ pytest 9.0.3` + `+ iniconfig 2.3.0`, both
   `py3-none-any` wheels. Skill `/ov-versa:versa` carries a new
   "Load-bearing transitive: pytest in the pixi env" section
   explaining the lazy-loader trap so a future contributor doesn't
   strip the dep as unused.

**Cutover sequence** (one phase, R10 at the end):

1. Dropped the orphan `versa` operator pod (4-surface cleanup:
   `charly config remove versa` + delete quadlet + reload + 3 orphan
   volumes). Production `versa/ecovoyage` was collateral damage
   from bug #1 above; recovered cleanly via
   `systemctl --user start ov-versa-ecovoyage.service` after the
   root-cause analysis confirmed no state corruption (the
   `ov-versa-ecovoyage-airflow-data` volume was untouched; the
   bind mount at `/home/atrawog/Atrapub/ecovoyage` was never the
   target of the sweep). A pre-update snapshot of
   `~/.config/containers/systemd/ov-versa-ecovoyage.container` +
   `~/.config/charly/deploy.yml` was saved to
   `/tmp/ecovoyage-snapshot-pre/` before any further work.
2. Fixed bug #1 in source (`ov/sidecar.go` + `ov/config_image.go`
   + `ov/sidecar_test.go`), full `go test ./...` PASS, rebuilt the
   charly binary via `task build:ov` + `makepkg -si` (pkg/arch
   `pkgver` bumped to `2026.146.1105`), verified
   `Pod=%s.pod` + `Disabling sidecar %s` strings present in
   `/usr/bin/charly`.
3. Fixed bug #2 in source (`layers/marimo/pixi.toml` +
   `layers/marimo/layer.yml` version bump to `2026.146.1203` +
   `layers/marimo/pixi.lock` regen).
4. R10 via `charly eval run eval-versa-pod`: 8/8 steps PASS in 35 min
   (image-build 32m + eval-image 55s + deploy-add 19s + config 2s
   + start 0s + eval-live 87s + update 14s + cleanup 11s).
   eval-live: **124 passed · 0 failed · 0 skipped**. The
   `versa-graph-imports` and `versa-graph-notebook-export` probes
   that failed before the pytest fix now both ✓ exit 0.
5. `charly update versa -i ecovoyage` applied the freshly-built versa
   image to the operator's production tenant.
   `ov-versa-ecovoyage.container` regenerated cleanly:
   `Image=ghcr.io/overthinkos/versa:2026.146.1239`, all 7
   `PublishPort`s identical to the snapshot, both `Volume=`
   mounts identical (bind at `/home/atrawog/Atrapub/ecovoyage` +
   `ov-versa-ecovoyage-airflow-data` named volume), all 9
   `AddDevice` GPU lines identical, `ContainerName` unchanged,
   all 14 tailscale `ExecStartPost`/`ExecStopPost` hooks
   identical. The only intended changes are the new Image tag and
   the removal of a stale MCP discovery entry for an
   already-torn-down eval bed.
6. `disposable: false` set on `versa/ecovoyage` in
   `~/.config/charly/deploy.yml` per operator directive — future
   autonomous updates must be re-authorized.

**Latent surfaces NOT fixed in this cutover** (operator escalation
pending): two additional `ov` bugs surfaced during the cascade —
(a) the `charly update <bed>` step regenerated quadlets for every
deploy whose `image:` resolves to the bed's source image, AND used
the bed's overlay tag (`eval-versa-pod:<calver>`) instead of the
sibling deploy's correct image tag (`versa:<calver>`). Bounded
blast radius (only `ov-versa-ecovoyage.container` was corrupted;
the subsequent `charly update versa -i ecovoyage` overwrote the
corruption with the correct image); (b) `charly update <image> -i
<instance>` does not enforce the `disposable: true` precondition
the way `charly update <name>` does, AND the deploy.yml re-serializer
drops `disposable: false` as an "omitted default" so the explicit
lockdown intent isn't preserved across re-writes. Both surfaces
require code changes in `ov`'s update / deploy.yml paths that are
larger than the present cutover's scope.



Android was elevated from a single `kind: image` (`android-emulator`) plus
imperative eval verbs into a first-class, declarative, nestable deploy surface
modeled on `kind: k8s`. This is a **purely additive** cutover (a new optional
kind, a new optional layer field, a new `target:` value — no removals), so it
raises **neither** `LatestSchemaVersion()` nor a `MigrationStep` (per the
migrate skill's "purely additive → just add it" rule); it landed at the
unchanged schema version `2026.144.1443` with a fresh per-push `v<CalVer>` tag.

What landed:

- **`kind: android`** — an Android DEVICE substrate (the parallel of
  `kind: k8s` the cluster). A device is either an in-pod emulator (referenced
  by `image:`) or a remote/physical adb endpoint (`adb: {host: <host:port>}`).
  Carries `serial:`, `google_account:` (credential-store secret-key refs for
  the apkeep google-play source), and informational `device:`/`api_level:`
  (the API level + system image remain BUILD-time properties of the referenced
  image — `kind: android` references, never drives, the build). Loader wiring
  clones every `k8s` site in `ov/unified.go` (`UnifiedFile.Android`,
  `entityKinds`, `rootShapeKeys`, `kindKeyedDoc.Android` + `AndroidDoc`,
  `mergeAndroidMap`, `mergeKindDoc`, `validateEvalBeds`). Types in
  `ov/android_spec.go`; `findAndroidSpec` mirrors `findK8sSpec`.

- **`apk` package format** — Android apps are declared in LAYERS via a new
  top-level `apk:` list (NOT a separate kind), parallel to `package:`/`aur:`
  but device-scoped. Each entry is a `package:` (apkeep download by id, with
  `source`/`arch`/`version`) XOR an `apk:` (committed local APK pushed via the
  adb sync protocol). It compiles (`compileApkStep` in `install_build.go`) into
  an `ApkInstallStep` (`install_plan.go`) that ONLY `AndroidDeployTarget`
  executes — OCITarget emits nothing for it (there is no device at image-build
  time; verified: no apk RUN leaks into the Containerfile) and Local/Vm/Pod
  targets record a skip. A layer carrying only `apk:` is valid install content
  (`HasInstallFiles` includes `HasApk`); `validateLayerApk` enforces
  package-xor-apk + the source allowlist.

- **`target: android` deploy + `AndroidDeployTarget`** (`ov/android_target.go`,
  `ov/android_deploy_cmd.go`) — an IR-consuming target (like LocalDeployTarget,
  unlike the no-op K8sDeployTarget). It applies the deploy's `add_layer:`
  layers' `apk:` packages onto the device, gating on `sys.boot_completed`
  first (a real readiness condition, never a fixed sleep). The dispatch in
  `deploy_add_cmd.go` routes `target: android` like `local`/`vm` (no primary
  image plan; apps ride in on add_layers).

- **ONE shared installer (R3)** — `ov/android_install.go` holds the single
  install path: `AndroidDevice.InstallByPackage` (apkeep + adb, run in-pod via
  `engine exec` for an image device or on the host via `adb -H -P` for an
  endpoint) and `InstallFromHostApk` (goadb push for committed APKs). The
  `charly eval adb install-app` / `charly eval adb install` verbs were refactored into
  thin wrappers over it — their CLI surface and the `adbMethods` allowlist are
  unchanged.

- **Nested deployment** — `pod → android` (the device on its emulator pod)
  mirrors `vm → k8s`. `target: android` is a passthrough hop in the deploy
  chain (the device shares its host pod's adb venue / the endpoint addr; no new
  shell venue). `charly deploy add` gained `--node-only` (dispatch just the named
  node, no descent) so a pod substrate can be started before its android
  children deploy; `charly eval run <bed>` now deploys a bed's nested children
  AFTER `charly start`, then runs eval-live.

- **R10 bed** — `eval-android-emulator-pod` gained two nested `kind: android`
  children: `device` (in-pod emulator) installs F-Droid via the apk format
  (apkeep in-pod) from the new `android-test-apps` layer; `device-net`
  addresses the SAME emulator as a remote adb ENDPOINT (`127.0.0.1:35002`,
  the bed's published port) and installs the committed ApiDemos via goadb from
  the `android-apidemos` layer — exercising the remote/physical device code
  path with no hardware. The android-emulator-layer's former imperative
  `apkeep-install-fdroid` eval verb check became presence/launch ASSERTIONS
  (`apk-fdroid-present`/`apk-fdroid-launch`/`apk-net-apidemos-present`) of what
  the deploys installed.

- **Host deps (R9)** — the remote-device `package:` path runs apkeep + adb on
  the host; `android-tools` (host adb) is declared as a PKGBUILD `optdepends`.
  apkeep has no buildable Arch package (its AUR Rust build fails to link
  ring/zstd-sys under lld — the same reason it ships as the in-pod upstream
  binary), so the host apkeep-download path is documented (install the upstream
  binary) rather than a hard dep; the committed-APK endpoint path needs neither
  (pure goadb). The remote-endpoint host-apkeep path is unit-tested; the in-pod
  apk format + the goadb endpoint path are live-verified on the bed.

Rejected during planning: `kind: apk` (the operator directed that apk be "just
another package format like .pac, defined via layers" — so apk is a format, not
a kind); driving image builds from `kind: android` (api_level is informational,
not a build driver); an APK artifact registry (apkeep fetches on demand;
committed APKs reuse the adb-sync push).

### 2026-05-25 — Android emulator → Android 16 / API 36 + Play Store + GMS + generic apkeep install-app verb

The `android-emulator` image was upgraded from Android 14 (API 34, `google_apis`,
`pixel_6`) to **Android 16 (API 36, `google_apis_playstore`, `pixel_9a`)**. The
Play Store system image ships **Play Store (`com.android.vending`), Google Play
services (`com.google.android.gms`), the Google Services Framework
(`com.google.android.gsf`), and Google Chrome (`com.android.chrome`)
preinstalled** — live-verified on the disposable `eval-android-emulator-pod`
bed before implementation. Concretely:

- **`layers/android-sdk/layer.yml`** — `var:` bumped to API 36 +
  `google_apis_playstore`; AUR `android-sdk-build-tools-36` + `android-platform-36`
  (both confirmed to exist in the AUR) replace the `-34` packages; **`apkeep`**
  (EFF, the by-package-name app downloader) added. The system-image cache sentinel
  is now keyed per API level + variant (`.ov-sysimg-complete-<api>-<variant>`) so a
  prior API level's completed download in the persistent build mount can't
  short-circuit a new pull. Eval paths updated (build-tools/36.0.0,
  platforms/android-36, system-images/android-36/google_apis_playstore/x86_64) +
  an `apkeep-binary` check.
- **`layers/android-emulator-layer/layer.yml`** — `ov_avd_36` / `pixel_9a`; static
  `EMULATOR_MEMORY`/`EMULATOR_CORES` removed (now host-auto-sized); opt-in
  `secret_accepts: GOOGLE_ACCOUNT_EMAIL + GOOGLE_AAS_TOKEN` for the google-play
  source. Eval asserts Play Store/GMS/GSF + Chrome preinstalled & launchable, and
  exercises the new `adb: install-app` verb with the F-Droid test app
  (install → present → launch-via-pidof → uninstall). The version assertion moved
  14→16; the Appium session caps moved `platformVersion` 14→16 and
  `chromedriverExecutableDir` /opt/chromedriver/113 → /opt/chromedriver/133.
- **`layers/android-emulator-layer/start-emulator`** — CPU/RAM are derived from the
  host at runtime when unset: cores = `nproc − 2` clamped [2,8]; memory =
  `MemAvailable/2` MiB clamped [2048,8192]. Named constants, operator-overridable.
- **`layers/appium-server/layer.yml`** — the offline-baked chromedriver was
  re-pinned from the stale 113 to **133.0.6943.141** (Chrome-for-Testing; nearest
  CfT build to the live-probed API-36 System WebView 133.0.6943.137; the +4 patch
  skew is tolerated by `chromedriverDisableBuildCheck`). Source switched to the
  Chrome-for-Testing endpoint (the legacy `chromedriver.storage.googleapis.com`
  serves ≤114 only). Added a deploy-scope major-match guard so a future stale pin
  FAILS loudly.
- **Go — new generic verb `charly eval adb install-app`** (`ov/adb.go`,
  `ov/evalrun_ov_verbs.go`, `ov/validate_eval.go`, `ov/adb_test.go`). Runs
  `apkeep` IN the pod to download an app by package id from APKPure (default, no
  creds) or the Google Play Store (`--source google-play`, via the opt-in AAS
  token), then installs the result onto the emulator with the container's adb —
  handling a single `.apk`, a split `.apk` set, AND an `.xapk` (APKPure's split
  bundle: unzip → `install-multiple`). The eval modifier is `app_id:` (NOT
  `package:`, which is the goss `package:` verb discriminator).

  Two live-verified facts shaped the design: **Chrome cannot be sideloaded** — its
  `.xapk` needs the Trichrome static library that only the Play Store dependency
  installer provides (`INSTALL_FAILED_MISSING_SHARED_LIBRARY`) — and it is
  preinstalled anyway, so the verb is exercised with F-Droid, not Chrome; and
  upstream apkeep has **no `apk-mirror` source** (only apk-pure / google-play /
  f-droid / huawei-app-gallery), so the original "install from APKMirror" intent
  resolves to APKPure.

### 2026-05-25 — Eliminate `:latest` from every base image (pin arch + cachyos-v3; bootc ref resolver)

`:latest` is no longer used by any base image anywhere in the project. The two
external base refs that still floated on `:latest` are pinned to precise,
immutable coordinates, and the one Go code path that fabricated a `:latest`
image ref is fixed to resolve a real CalVer tag.

- **Arch base** (`base.yml` `arch`): `quay.io/archlinux/archlinux:latest` →
  `quay.io/archlinux/archlinux:base-20260525.0.535911` — quay's `base-*`
  date-serial tags are immutable; this digest (`sha256:50dbcaa…`) is identical
  to what `:latest` resolved to on the pin date, so the rebuild is cache-stable.
  Refresh by bumping to a newer `base-*` tag.
- **CachyOS base** (`image/cachyos/image.yml` `cachyos`, in the
  `overthinkos/cachyos` submodule): `docker.io/cachyos/cachyos-v3:latest` →
  `docker.io/cachyos/cachyos-v3@sha256:b56444f1d41cd697cc2f6034618259a6136c70127efef5139b421b64b1527888`.
  Docker Hub publishes ONLY a `:latest` tag for `cachyos-v3` (no named/dated
  tags exist), so a digest pin is the most precise coordinate available. Refresh
  by repinning to a newer cachyos-v3 digest.
- **Per-kind version labels unchanged.** Both pins are content-identical to the
  `:latest` they replace, so `arch` and `cachyos` keep their existing
  `version:` and their emitted `ai.opencharly.version` labels stay stable — no
  cache-miss cascade to downstream images.
- **`BuildBootcVM` (`ov/vm_bootc_install.go`)** no longer defaults an internal
  kind:image short name to `ghcr.io/overthinkos/<name>:latest` (a ref charly never
  builds or pushes — it is CalVer-only). The new `resolveBootcImageRef` helper
  passes full OCI refs through unchanged and resolves an internal short name to
  its newest local CalVer tag via the shared `resolveLocalImageRef`, surfacing
  an actionable `charly image build <name>` error when the image is missing. Covered
  by `ov/vm_bootc_install_test.go`.
- **R5 stale-reference sweep:** the `cachyos-v3:latest` / `archlinux:latest`
  references in `build.yml`, `ov/migrate_entity_version.go`, `README.md`, and the
  `cachyos` / `arch` / `arch-ov` / `image` / `openclaw` / `versa` skills are
  updated to the pinned forms (the arch skills also corrected from the stale
  `docker.io/library/archlinux` registry to the `quay.io/archlinux` mirror in
  actual use). `git grep` for the old base refs now returns only this entry.
- **Out of scope (intentionally NOT pinned):** `quay.io/libpod/alpine:latest`
  in the `openclaw-desktop` nested-podman eval check (a throwaway test container
  — the probe only needs *some* runnable image) and `ghcr.io/tailscale/tailscale:latest`
  in `ov/sidecar.yml` (a sidecar that should float for security updates). Neither
  is a base image.

### 2026-05-25 — Comprehensive `charly eval appium` surface + AUR-packaged android-emulator toolchain

`charly eval appium` grew from 8 typed methods to a three-tier surface mirroring
the `cdp` (typed + `raw`) and `wl` (nested `sway-*`/`overlay-*` groups)
precedents, so an `eval:` block can drive any screen the Appium ApiDemos app
exercises — and any UiAutomator2 operation at all:

- **Tier 1 (typed):** added `get-text`, `get-attribute`, `clear`, `find-all`,
  `source`, `back` (find/click/send-keys/install-app/screenshot/session-* stay).
  The Go `apidemos_test.go` sample is now expressible end-to-end, including the
  previously-impossible **read-back** (`get-text` of a field after `send-keys`).
- **Tier 2 (per-class sugar groups):** `appium gesture …` (9 UiAutomator2
  gestures), `appium app …` (lifecycle + `start-activity`, intent form),
  `appium key …`, `appium device …` (device info + WebView contexts). On the CLI
  these are nested groups; in eval YAML they are flat `gesture-tap`/`app-activate`/
  `device-contexts`/… method names.
- **Tier 3 (generic escape hatch):** `appium: execute` (any `mobile:`/JS via
  `/execute/sync`) and `appium: raw` (any W3C call under `/session/<id>`) —
  `raw` alone reaches 100% of the WebDriver + UiAutomator2 surface. Both support
  a `{element}` token substituted from a resolved `selector:`.

Six `Check` fields were added (`app_id`, `activity`, `attribute`, `percent`,
`keycode`, `params`); the generic methods reuse the existing
`method`/`path`/`request_body`/`expression`/`selector`/`strategy`/`session`
fields (no duplication). `eval-android-emulator-pod` gained one representative
ApiDemos screen per interaction class (TextFields read-back, Controls, RadioGroup,
List+scroll, Spinner, Date/Time, SeekBar, drag-and-drop, WebView, Notifications)
plus device/system smoke.

The android-emulator **toolchain moved to CachyOS/AUR packages** (the image is
CachyOS): `android-sdk-cmdline-tools-latest`, `android-sdk-platform-tools`,
`android-sdk-build-tools-34` (brings `aapt2`, previously absent — Appium logged
`Could not find 'aapt2'`), `android-platform-34`, `android-emulator`, and the
`appium` package, all under `/opt/android-sdk`. The only sdkmanager-fetched
component is the API-34 google_apis system image (no package exists anywhere).
WebView automation pre-bakes the **pinned chromedriver 113** (matching the
System WebView's Chrome) at `/opt/chromedriver/113` and switches via the
`appium:chromedriverExecutableDir` cap — eliminating the slow/hanging runtime
autodownload and the need for `--allow-insecure`. The emulator gained
`-memory`/`-cores` boot tuning. The stale "the AVD has no internet" comment was
corrected: the AVD has full internet + DNS out of the box (the emulator's NAT
forwards guest DNS to the container's resolver, which has bridge egress); the
verifier-disable is a determinism/speed measure, not a no-internet workaround,
and a regression-guard eval check (`ping 8.8.8.8` + `ping google.com`) locks it in.

### 2026-05-24 — CachyOS GPU image family + nodejs24→nodejs merge

The NVIDIA/CUDA GPU image stack gained a **CachyOS (Arch) sibling family**
alongside the Fedora GPU images. Eight images were added to the
`overthinkos/cachyos` submodule (`image/cachyos`, its own `image.yml` after the
per-kind-versioning `kind-files` split): `nvidia` (the CachyOS GPU base =
cachyos + agent-forwarding + nvidia + cuda), `python-ml`, `jupyter-ml`,
`ollama`, `comfyui`, `unsloth-studio`, `immich-ml`, and `selkies-desktop-nvidia`.
They inherit `build: [pac]` + the `ov.arch-builder` builder map from the cachyos
base within the submodule namespace (no per-image builder redeclaration);
`immich-ml` and `selkies-desktop-nvidia` override `build: [pac, aur]` for AUR
packages (pgvector; google-chrome + wlrctl). The GPU **layers** stay shared in
the main repo, reached by `@github` ref.

**Layer Arch support (main repo).** Additive `distro.arch` package branches were
added to the GPU-stack layers, with Arch package names verified against the live
CachyOS package database: `comfyui` (aria2, git-lfs), `jupyter-ml` (git, gcc),
`redis` (**valkey** — Arch has no `redis`; valkey ships `/usr/bin/redis-server`
+ `/usr/bin/redis-cli`), `postgresql` (postgresql + postgresql-libs;
**pgvector via AUR**), `immich` (libvips, libheif, libraw, perl-image-exiftool,
gcc). Cross-distro `eval:` probes gained `package_map:` entries
(`valkey-compat-redis→valkey`, `postgresql-server→postgresql`). The `vectorchord`
layer's extension-dir detection switched from hardcoded `/usr/lib*/pgsql` +
`/usr/share/pgsql` to `pg_config --pkglibdir` / `--sharedir`, authoritative on
both Fedora (`pgsql`) and Arch (`postgresql`) layouts. Per the per-kind
versioning rules (this cutover lands on top of that one), every changed layer's
`version:` was bumped — the GPU-stack layers to `2026.144.1531`, `nodejs` later
to `2026.144.1613` (the standalone-pnpm correction, below). Fedora package sets
are byte-stable.

**nodejs24 → nodejs merge.** The standalone `nodejs24` layer was deleted; its
pnpm provision moved into the generic `nodejs` layer. pnpm is installed as the
**self-contained standalone binary** (it bundles its own Node) to
`/usr/local/bin/pnpm` via a `task:` download — a plain RUN step, NOT a
`package.json`. (A `package.json` on `nodejs` was tried first but reverted before
landing: it triggers the npm multi-stage builder on *every* image that composes
`nodejs` — including the builder images `arch-builder`/`fedora-builder`, which
compose `nodejs` to BE the npm builder and therefore cannot self-provide it
(self-reference is filtered), so `charly image generate` failed with
`layer nodejs needs builder npm but no builders.npm configured`. The standalone
binary is a plain RUN, no builder trigger.) `/usr/local/bin` is on the system
PATH for every user including root — Immich runs its pnpm build as root, which the
old `~/.npm-global` (uid-1000) path silently broke. Every consumer repointed to
`nodejs`: the `immich` layer's `require:`, the main `immich`/`immich-ml` images,
and `fedora-coder` (in `overthinkos/fedora`). Immich has no hard Node requirement
(its `engines` pins only `pnpm>=10`; the `node` version is a non-enforced volta
dev-pin), so consumers follow the distro-default Node — v26 on Arch, v22 on
Fedora. The `nodejs` layer landed at `version: 2026.144.1613` (the standalone-pnpm
correction); the other changed layers at `2026.144.1531`. R5 sweep:
`git grep nodejs24` returns only this file.

No further schema bump — this change is additive (new images, new distro
sections, a layer removal) on top of the per-kind-versioning schema
`2026.144.1443`. Cross-repo landing: the changed main-repo layers land + tag
first, then `image/cachyos` reconciles its `@github` pins to that tag and runs
the authoritative R10 (build → deploy → eval-live → fresh rebuild) of the eight
GPU images on real NVIDIA hardware.

**Follow-up fixes surfaced during R10 (same cutover, separate `ov`/main commits).**

- **`generate`: remote data-layer `COPY --from` used the wrong stage name.**
  `writeDataStaging` emitted `COPY --from=<map-key>`; for a REMOTE `@github` data
  layer the map key is the full ref (e.g.
  `github.com/overthinkos/overthink/layers/notebook-templates`), which is not a
  valid build-stage reference — podman tried to pull it as an image and failed
  with `no stage or image found` (exit 125). The matching `FROM scratch AS <name>`
  uses the SHORT name (`layer.Name`). Fix: emit `COPY --from=<layer.Name>` so both
  match; local data layers are unaffected (map key == Name). Surfaced building the
  cachyos `jupyter-ml` image (first `@github` data-layer consumer,
  `notebook-templates`); `unsloth-studio` (`notebook-finetuning`) hit the same.
  Guarded by `TestWriteDataStaging_RemoteLayerUsesShortStageAlias`.

- **`charly config`: quadlet `PublishPort=` keyed by image short-name, not deploy
  key.** `MergeDeployOntoMetadata` looked up the deploy.yml overlay by
  `meta.Image` (the baked `ai.opencharly.image` short-name) instead of the
  deploy key the caller was operating on. A `kind: eval` bed (key
  `eval-cachyos-ollama-pod`, image `ollama`) remapping `45434:11434` therefore had
  its port silently replaced by the image default `11434`, colliding at `charly start`
  with a running same-image production deploy (`ov-ollama`) →
  `rootlessport bind: address already in use`. This was the documented
  "quadlet-port lookup keyed by image, not deploy-key" known issue; it blocked the
  deploy-scope R10 of every cachyos GPU bed on a host that runs same-named
  production services. Fix: `MergeDeployOntoMetadata(meta, dc, deployName,
  instance)` now keys on `deployKey(deployName, instance)` with the deploy key
  passed by all five call sites (`charly config`/`start`/`shell` + the `--update-all`
  and tunnel-teardown loops); the sibling `dc.Lookup` parameter was renamed
  `deployName` to document the same contract (R3). Guarded by
  `TestMergeDeployOntoMetadata_KeyedByDeployNameNotImage`; the stale "Known issue"
  paragraph in `/ov-core:deploy` was removed (R5).

- **`charly eval run`: `kind: eval` pod beds' declared `port:` never reached the
  quadlet.** The bed bring-up shelled out `charly deploy add`/`charly config`/`charly start`
  with only the bed NAME; neither verb consults the project-side folded bed node,
  and both source `port:`/`security:`/`network:` from the IMAGE LABELS (persisting
  ports only behind an operator `-p` gate). So a bed's project-declared `port:`
  override lived only in `Config.Deploy[name]` and was never propagated to the
  per-host `deploy.yml` that `charly config` reads — every pod bed silently fell back
  to its image's default port and only "worked" because that port was free on a
  clean eval host. On a host running same-named production services it collided at
  start. Fix: `runEvalBed` now calls `persistBedDeployOverrides(name, node)` after
  the pre-run teardown and before `charly deploy add`, seeding the bed node's
  `port:`/`volume:`/`env:`/`tunnel:`/`security:`/`network:`/`disposable:` into the
  per-host deploy.yml so the existing config→merge→quadlet path honors them (no
  new merge logic; `charly config`'s `SetPorts`-gated save leaves the seeded port
  untouched). This repairs every existing bed, not just the cachyos ones. Guarded
  by `TestPersistBedDeployOverrides_SeedsPortBeforeConfig`.

- **Volumes were keyed by image, not deploy — differently-named pods of one
  image shared volume mounts (data-safety bug).** Named-volume names were derived
  from the image (`ov-<image>-<vol>`, `labels.go:314` via `meta.Image`), so EVERY
  distinctly-named deploy of an image — a second production pod (Pattern-B), or a
  `kind: eval` bed — mounted the SAME named volumes (instances were partially
  isolated via the old `InstanceVolume`, but production pods and beds were not).
  Running the `eval-cachyos-immich-ml-pod` bed alongside the operator's production
  `ov-immich-ml` put two Postgres postmasters on the **same `ov-immich-ml-pgdata`
  volume** (the bed's password-auth mismatch was a symptom — it reused the
  production DB's existing password, which differed from the bed's freshly
  generated secret). Fix (generic): a single `deployVolumePrefix` (= the deploy's
  container name) now keys ALL volume naming — named volumes
  (`scopeVolumesToDeployKey`, run unconditionally in `MergeDeployOntoMetadata`),
  bind-auto paths and encrypted-volume dirs (`ResolveVolumeBacking` +
  `deployStorageDir`, threaded through the `enc.go` mount/unmount/passwd ops), and
  purge (`removeVolumes`). So every distinctly-named pod — base, instance,
  Pattern-B, or bed — ALWAYS gets its own volume namespace; the lone no-op is the
  base deploy whose key equals the image (nothing else can share that name), so
  that deploy's names never change (zero migration; the now-redundant
  `InstanceVolume` was removed since `deployVolumePrefix` subsumes it identically
  for instances). The bed runner additionally `--purge`s on its pre-run and
  teardown (safe — isolated names) so each bed deploy starts from a clean volume.
  Guarded by `TestMergeDeployOntoMetadata_VolumesScopedToDeployKey` (base /
  second-production-pod / instance / bed).

- **`charly eval run`: pod/vm beds raced eval-live against slow first-run startup.**
  The pod bed path ran eval-live after only a 30s exec-check; a fresh Immich runs
  its one-shot DB migration for minutes before the API binds, so the deploy-scope
  probes failed against a not-yet-ready service. Fix: `stepReady` runs eval-live
  with a bounded readiness retry (re-runs until the checks pass or a 6-minute
  deadline) — the eval checks themselves are the readiness condition, a real
  synchronization primitive, not a fixed sleep. Fast beds pass on the first
  attempt with zero added latency; a genuinely-broken deploy still fails after
  the deadline.

- **`base.yml` builder-layer refs still pinned the pre-merge ecosystem tag →
  nodejs resolved to two versions in every consumer.** The nodejs24→nodejs merge
  moved the `nodejs` layer (`version:` `2026.144.1443` → `2026.144.1613`), but
  `base.yml`'s `arch-builder` + `fedora-builder` still pinned
  `pixi`/`nodejs`/`build-toolchain`/`yay`/`rpmfusion` at the pre-merge ecosystem
  tag `v2026.141.1600` (the comment claiming "the layers did not move" was now
  false). The consumers fetched both: `fedora-coder` pulled merged `nodejs`
  (v1613) while its `fedora-builder` pulled the pre-merge one (v1443, the
  remote-cache backfill of an un-versioned old layer) → warn-and-newest-wins.
  The same surfaced in `main` itself through the `versa` → `cachyos` → `ov`(main)
  mutual import. Fix (R5 stale-ref): advanced the `base.yml` builder-layer refs
  to the post-merge ecosystem tag `v2026.144.2044` and re-aligned the consumers'
  pins (`image/cachyos` and `image/fedora` reconcile their `@github` opencharly
  pins, including the `ov:` import, to a fixed post-merge `main` tag; `main`
  re-points its `cachyos` `@github` import + submodule pointers to the
  re-aligned `cachyos`). Because `main` ↔ `cachyos` mutually import, the bump is a
  circular bootstrap: the producer (`main` `base.yml`) lands first at a provisional
  tag (its own validate momentarily warns via the still-stale `cachyos` import),
  then `cachyos` re-aligns to it, then `main` converges its `cachyos` import to
  the re-aligned tag — clearing the warning. End state: every repo resolves
  `nodejs` to a single version (v1613) with zero resolver warnings.

### 2026-05-24 — per-kind versioning: author-declared `version:` as the authoritative identity for layers AND images (hard cutover)

Two long-standing defects shared one root cause — **the per-push CalVer git tag
was overloaded as both a fetch coordinate AND an identity**, and the image
identity LABEL was a per-build timestamp:

- **Cache cascade.** `ai.opencharly.version` was emitted as the build-time
  CalVer (`img.Tag`, one `ComputeCalVer()` per generate). Baked into every image,
  it changed the image config → image SHA on *every* build, so a child's
  `FROM <base>:<tag>` resolved to a new SHA and cache-misses cascaded down the
  whole chain — a warm no-source-change rebuild recompiled everything.
- **Spurious version warnings.** Layer warn-and-newest-wins compared the **repo
  git tag** (`LayerRef.Version()` = the `:vTAG` suffix), which advances on every
  push, so an UNCHANGED layer was reported as a "different version" merely because
  its repo got re-tagged for an unrelated push.

The cutover made the per-entity `version:` fields (which existed in the schema but
were inert) load-bearing:

- **`version:` is MANDATORY for the `layer` kind, OPTIONAL for every other kind.**
  `validateLayerContents` hard-errors a local layer with no `version:`.
- **Image `ai.opencharly.version` = content-derived `EffectiveVersion`** — the
  image's dedicated `version:` if set, else the highest layer `version:` across
  the whole base chain (new `ov/effective_version.go`, run by `NewGenerator` after
  intermediates + global order are materialized; traverses namespaced bases via
  the fully-qualified `g.Images` keys). Stable across builds when no layer changed
  → no FROM-SHA cascade. Bare distro bases (`arch`/`fedora`, submodule bases) are
  layerless, so they carry a dedicated `version:`; builders + auto-intermediates
  derive the highest layer version automatically.
- **LABEL-CalVer now ALWAYS takes priority over TAG-CalVer** (this REVERSED the
  prior behavior — `local_image.go` used to "prefer tag-CalVer over label-CalVer").
  `resolveLocalImageRef` keys on the label-CalVer (primary) with the tag-CalVer as
  the tiebreaker that picks the newest BUILD among builds sharing one
  content-stable label; `charly clean` retention (`imageLabelCalVer` +
  `imageTagCalVer`) does the same. The label↔tag substitution fallback was deleted.
- **Layer resolution is per-entity, post-fetch.** `refVersionTracker` (which
  compared git tags before fetch and warned on a re-tag) was DELETED.
  `CollectRemoteRefsOpts` now collects EVERY distinct `(repo, git-tag)`; the
  `ScanAllLayerWithConfigOpts` fix-point fetches each, reads each layer's own
  `version:`, and `pickLayerVersion` arbitrates per bare ref: same per-entity
  version across different git tags → NO warning (newest git tag wins for
  freshness); different per-entity versions → warn once and the newest per-entity
  version wins. A fetched layer with no `version:` is a HARD ERROR.
- **Hard cutover, no compat shims.** The runtime hard-errors on any
  non-conformant config (missing layer version, unresolvable image version,
  unversioned fetched remote layer) with an `charly migrate` hint. The new
  `entity-version` `MigrationStep` (schema `2026.144.1442`; HEAD bumped to
  `2026.144.1443`) backfills `version:` on every layer.yml + every bare-base image
  entry (no `layer:` field AND an external `base:`), comment-preserving via the
  yaml.v3 node API, skipping the `image/` submodules (each migrates in its own
  repo) and `testdata`. `RunProjectMigrations` (remote-cache auto-migration)
  backfills fetched first-party remotes, which is what lets the runtime drop the
  fallback.

**`arch-rename` migrator bug found + fixed in the same tree (R2).** Running the
full `charly migrate` chain surfaced a latent bug: the `arch-rename` step
(schema 2026.141.1559) used a literal denylist for external Arch strings that
covered `docker.io/library/archlinux` but NOT the quay mirror, so it corrupted
`base: quay.io/archlinux/archlinux:latest` → `quay.io/arch/arch:latest`. RCA via
`/ov-internals:root-cause-analyzer`: a denylist of literals can never be
complete. Fixed generally — `archRenameExternalRefRe` now protects ANY external
registry ref (a registry-host segment with a `.`/`:` before the first `/`) whose
path contains `archlinux`, by SHAPE — covering the quay mirror, `ghcr.io/.../archlinux-*`,
and any future registry. Added `migrate_arch_rename_test.go` (the absent coverage
that let the bug ship); restored the corrupted `base.yml` line.

Standing rules established (stated forward-looking in CLAUDE.md "Per-kind
versioning" / "Layer-version resolution" + `/ov-internals:capabilities`,
`/ov-internals:go`, `/ov-build:validate`, `/ov-build:reconcile`,
`/ov-internals:generate-source`). Files: `ov/effective_version.go` (new),
`ov/migrate_entity_version.go` (new), `ov/{config,labels,capabilities,generate,
local_image,clean,refs,layers,validate,migrate_registry,migrate_arch_rename}.go`,
plus the backfilled `layers/*/layer.yml` + root YAML stamps. `build.yml` stays at
its older schema stamp by design (not in the calver-schema stamp set; carries no
per-entity-versioned entities).

### 2026-05-24 — android-emulator R10 bed green: build fixes + adb-eval ordering + appium host-path install + keep-pod-on-failure

The `eval-android-emulator-pod` bed had never passed end-to-end. Five
coordinated fixes, all surfaced by one failed `charly eval run` and fixed in one
working tree (R2), landed it.

**Build (cachyos/Arch base).** `android-sdk` was Fedora-only — on the cachyos
(Arch) `selkies.selkies-desktop` base the SDK build failed at `unzip: command
not found` and the emulator's Qt/GL/audio runtime libs were absent. Added an
`arch:` package section (unzip, which, gcc-libs, mesa, libglvnd, the libx11/xcb
stack, alsa-lib, libpulse, xcb-util-cursor). `java-openjdk` had hardcoded
`JAVA_HOME=/usr/lib/jvm/jre-21-openjdk`, a Fedora-only path that silently broke
every other distro; replaced with a canonical distro-agnostic symlink
`/usr/lib/jvm/ov-jdk21` (a build task picks the installed JDK 21 root, preferring
the full JDK over a bare JRE) consumed by android-sdk / appium-server / the
emulator service, guarded by two build-scope evals. `start-emulator` used
`-accel kvm`, which the Android emulator rejects (`-accel` only accepts
on|off|auto) — it exited immediately and supervisord reported "FATAL Exited too
quickly"; changed to a KVM-probe that selects `-accel on` (KVM reachable) or
`-accel off` (TCG fallback).

**adb-eval ordering (the bed's eval-live failures).** The eval runner executes
checks in declaration order (`Runner.Run`, sequential, no sort), but the
android-emulator layer declared the one-shot `adb getprop sys.boot_completed`
and `adb shell` probes BEFORE the `adb wait-for-device` readiness gate. The
`adb: getprop`/`adb: shell` verbs are single-shot — a check's `timeout:` is a
per-attempt cap, NOT a retry budget — so they fired while the emulator was still
booting (device "unknown") and failed instantly with `AdbError: error performing
RunCommand`. Reordered so `adb-wait-for-device-ready` (which polls
`sys.boot_completed` every 2s until 1, tolerating the early-boot window) runs
FIRST; every one-shot probe after it now runs against a fully-booted device. No
sleeps, no retry magic — the synchronization primitive (`wait-for-device`) was
already present, only mis-ordered. A second readiness gap surfaced after the
reorder: PackageManager keeps initializing for a few seconds AFTER
`sys.boot_completed=1`, so the `adb install` that runs right after the boot gate
failed with "Failed to parse APK file" (verified live: the SAME install
succeeds once the device settles, and the later `appium: install-app` of the
same APK passed because session-create overhead let the device settle first).
The dependent confirm/uninstall failures were pure cascade. Fixed by adding the
framework's `eventually:` poll (180s deadline / 5s interval) to the single
post-boot package-install check — it re-runs the idempotent install until it
succeeds, polling the exact end-to-end readiness condition (a synchronization
primitive with a deadline, not a fixed sleep); the confirm/uninstall/appium ops
that follow a settled device stay one-shot.

**appium install-app host-path staging.** `appium: install-app` assumed the APK
was already inside the container (the layer pointed `apk:` at a `/tmp/...` path
that nothing staged, and the appium skill documented a `tests/data → /workspace`
bind that was never implemented — the bed mounts no host dir). `mobile:
installApp` requires an `appPath` the in-container server can read (the base64
`{"app":…}` form is rejected with HTTP 400 "required parameter is missing:
appPath" — verified live), so the file MUST be in-container. The verb now treats
`--apk` as a HOST path (symmetric with `adb: install`), stages it into the
container via `<engine> cp` to a temp path, calls installApp, and removes the
temp file. No bind-mount, no external staging step. The appium SKILL.md gotcha
and table, the layer check (`apk: ./tests/data/ApiDemos-debug.apk`), and the
eval.yml bed feature-description were all corrected (R5); the fictional
"R10 harness podman cp / README APK staging" comment was deleted.

**Generic download/build caching (the structural build-flake fix).** The
android-emulator build re-downloaded the ~1.5GB Android SDK from Google's CDN on
every full chain rebuild (the arch/cachyos base's `pacman -Syu` is
non-deterministic, so the base cache-misses and cascades down), and the CDN
intermittently served corrupt zips ("Error on ZipFile unknown archive"),
flaking the build ~50%. Root cause in the generator: the `download:` verb
DECLARED a `/tmp/downloads` cache mount but streamed curl straight into `tar` /
wrote to `/tmp/dl.zip` — the cache was never used; and `cmd:` tasks (sdkmanager)
had no download cache at all. Two generic, config-driven fixes (no
android-specific code in ov):
1. `emitDownload` (`ov/tasks.go`) now fetches every `download:` to a
   content-addressed file in the `/tmp/downloads` mount (keyed by URL sha256),
   reuses it across builds, and is integrity-safe (curl writes `<hash>.part`,
   atomically renamed only on success — a partial/corrupt download is never
   reused). So the generic "download a file" task caches automatically.
2. A new generic `cache:` task modifier (`Task.Cache`, honored by `cmd:` and
   `download:`) lets ANY task declare extra BuildKit cache-mount paths, owned
   per the task's `user:` (root → shared/locked, non-root → uid/gid-owned) via
   the existing `CacheMount` machinery — the same way package caches persist.
   The android-sdk layer DECLARES `cache: [/var/cache/ov-android-sdk]` and
   installs the heavy sdkmanager packages into it (`--sdk_root`, sentinel-guarded
   against partial installs), then copies them into the image SDK root. A rebuild
   reuses the cached SDK instead of re-downloading — eliminating the CDN-flake
   exposure on every rebuild. The cache-USE logic lives in the layer.yml task
   body; charly only provides the mount.

**Core namespace builder-resolution fix (distro-keyed default + one unified code
path).** An image whose `base:` is reached through an import namespace and
resolves to a cachyos/Arch distro (android-emulator → selkies.selkies-desktop →
cachyos.cachyos; versa/openclaw* → cachyos.cachyos) silently resolved its
pixi/npm/cargo/aur builder to `fedora-builder` (main's Fedora-only
`defaults.builder`) — building a whole Fedora builder, cross-distro, for a
cachyos image — UNLESS the image hand-declared `builder: {…: arch-builder}`.
android-emulator had simply forgotten the declaration. Root cause:
`ResolveImage`'s builder precedence (`defaults → direct-local-base →
img.Builder`) never consulted the image's resolved DISTRO, and builder maps are
namespace-relative refs that (correctly) don't cross an import-namespace
boundary — so a namespaced-base cachyos image fell through to the Fedora
default. Fix: a distro-keyed default — `resolveEffectiveBuilder` /
`distroBuilderMap` (ov/config.go) source the builder from the root-namespace
image whose `distro:` matches the resolving image's resolved distro (e.g.
base.yml's `arch` → arch-builder), whose bare refs resolve in the importing
namespace; `distro:` DOES cross the boundary, so the right builder is selected
automatically with NO per-image declaration. The five per-image
`builder: arch-builder` band-aids (versa, openclaw, openclaw-desktop,
openclaw-full, android-emulator) were DELETED. Crucially, builder resolution was
ALSO re-implemented inline in THREE other places that had silently diverged —
`charly image validate` (which produced a false "no builder.aur configured" error
because its private copy lacked the distro-keyed default), the `charly deploy add`
synthetic host/VM image (defaults-only), and the auto-intermediate generator —
all now route through the SINGLE `resolveEffectiveBuilder`, so builder
resolution is identical across `build` / `generate` / `inspect` / `validate` /
`deploy`. One code path, no drift.

**keep-pod-on-failure (operator debugging).** `charly eval run <bed>` used to tear
the bed down on ANY step failure (the shared `fail()` tail called `cleanup()`,
ignoring `--keep`), destroying the very target needed to diagnose the failure.
Now a FAILED run LEAVES the bed running and prints target-appropriate inspect +
destroy hints (`charly eval live <name>` / `podman exec ov-<name>` / `charly remove
<name>`, or `charly vm destroy` for VM beds). To keep this from blocking re-runs, the
pod/local bring-up gained a best-effort pre-run teardown (symmetry with the VM
path's pre-destroy), so a kept-alive bed from a prior failure is cleared before
the fresh deploy. The happy-path teardown still honours `--keep`.

### 2026-05-24 — selkies image-family extraction (program family #2) + namespace builder-ref resolver fix

The **selkies/sway streaming-desktop family** moved out of the main repo into the
`overthinkos/selkies` submodule (`image/selkies`, tag `v2026.144.0906`) — family
#2 of the image-extraction program after nvidia. The submodule inlines three
images (`selkies-desktop` on the CachyOS/Arch base, `selkies-desktop-nvidia` on
the Fedora GPU base [disabled], `sway-browser-vnc` on Fedora) plus two disposable
R10 beds (`eval-selkies-desktop-pod`, `eval-sway-browser-vnc-pod`). It vendors
nothing — every layer is an `@github` ref into main; the desktop **layers** stay
in main (shared with `openclaw-desktop`). Bases arrive via the `ov` / `cachyos` /
`nvidia` import namespaces. dbus is pinned `v2026.144.0531` to match the desktop
metalayers' transitive require (avoids a swaync/a11y-tools conflict);
agent-forwarding/charly stay on the ecosystem `v2026.141.1600`.

**Main side.** `image.yml` drops the three image entries; `android-emulator`
repoints to `base: selkies.selkies-desktop`; `eval.yml` drops the two beds (now in
the submodule) and the matching bed-coverage-map lines; `charly.yml` mounts
`- selkies: '@github.com/overthinkos/selkies:v2026.144.0906'`. The
`selkies-desktop-nvidia` mention in the `nvidia:` import comment and the
`eval-sway-browser-vnc-pod` example in CLAUDE.md's R10-bed list were updated (R5).

**Resolver fix (the extraction exposed a latent bug).** `android-emulator` is the
first main image to consume a namespaced base (`selkies.selkies-desktop`) that
itself carries a `builder:` map with namespace-relative refs
(`builder: {pixi: ov.arch-builder}`, relative to the selkies namespace). The
namespace resolver's `pullNamespacedImage` (`ov/namespace.go`) re-qualified a
pulled base's `base:` ref to the fully-qualified ancestor but NOT its `builder:`
or `bootstrap_builder_image` refs, so `ov.arch-builder` was re-resolved from main's
root config (where `ov` is undefined) → `import namespace "ov" not found`. Fix:
re-qualify EVERY by-name image ref a pulled namespaced image carries (base +
format builders + bootstrap builder — the exact set `imageDirectDeps` in
`graph.go` resolves) with the same namespace prefix
(`ov.arch-builder` → `selkies.ov.arch-builder`), via one generic `requalify`
helper kept in lockstep with `imageDirectDeps`. nvidia/cachyos never hit it
(`nvidia.nvidia` has an empty builder map; `cachyos.cachyos` has no layers, so its
builder is never pulled) — selkies-desktop is the first namespaced base with BOTH
buildable layers AND a namespace-relative builder map.

**Automatic future guard.** `charly image validate` (`validateImageDAG`) now SURFACES
the `resolveNamespacedBases` error (it was swallowed with `_ =`), so a namespaced
base — or its builder / bootstrap builder — that doesn't resolve is caught at
`charly image validate` time, before a build hits it. A regression test
(`TestResolveNamespacedBase_BuilderRefRequalified`) reproduces the exact uncovered
shape and fails without the fix (verified: `import namespace "up" not found`).

**Verification.** Both enabled selkies images passed full disposable R10 beds
(`selkies-desktop` 193 checks, `sway-browser-vnc` 178 checks, 0 failures); main
`charly image validate` is clean; the cross-repo resolution is proven by the rebuilt
`ov` building the entire re-qualified chain (`selkies.ov.arch` →
`selkies.ov.arch-builder` → `selkies-desktop`) from the pushed `v2026.144.0906`
tag. The `android-emulator` full image build is blocked downstream by a
pre-existing, selkies-unrelated gap — the `android-sdk` layer is fedora-only and
lacks an arch package section, so it can't build on its cachyos base; that arch
port is tracked as separate future-family work.

Two (submodule) / three (main) accepted cross-repo newest-wins resolver notices
remain: the selkies desktop metalayers ride `v2026.144.0531` while the shared
arch/fedora builders pin the ecosystem baseline `v2026.141.1600`, so `pixi` /
`nodejs` (and `ffmpeg` at the main level, via `cuda` vs `wl-record-pixelflux`) are
referenced at two versions and the warn-and-newest-wins resolver picks the newest.
Aligning them would require an ecosystem-wide baseline bump across main +
arch/cachyos/fedora/debian/ubuntu, which the mutual main↔cachyos/nvidia frozen-tag
import makes impossible without a transitional warning-state tag — deferred by
operator decision.

**Gitignore hygiene (same session, separate cutover).**
`image/{arch,bootc,cachyos,debian,fedora,ubuntu}` each gained the `.build/` +
`.containerignore` + `.dockerignore` + `.eval/` + `output/` gitignore entries that
`image/nvidia` + `image/selkies` + main already ship, so generated build-context
artifacts stop surfacing as untracked (submodule tags `v2026.144.0831`,
superproject tag `v2026.144.0833`).

No schema bump (relocation + resolver bugfix); `version:` stays `2026.143.844`.

### 2026-05-24 — Resolver docs + feat/-branch R10-gated git workflow + eval-coverage & zero-warnings gates + `charly image reconcile` (docs + tooling cutover, no schema bump)

Forward-looking documentation of the warn-and-newest-wins resolver (the prior
entry), a new standing git workflow, two sharpened acceptance gates, and a small
tool — landed as one cutover per repo (main + `plugins`) via the very workflow it
documents.

**Resolver docs.** CLAUDE.md "Key Rules" gains a layer-version-resolution bullet
(warn-and-newest-wins + reachability-scoped collection); `/ov-internals:go` gains
a "Remote-layer resolver" section (`refVersionTracker`, precise base/builder
`collectImage` walk, `LayerRef`, the unified `populateLayerFromYAML`);
`/ov-build:validate` is corrected (a layer at conflicting versions is no longer
"an error" — it warns and resolves newest); `/ov-image:image`,
`/ov-internals:generate-source`, and `/ov-local:ov-cachyos` get matching notes.

**feat/-branch, R10-gated git workflow** (`/ov-internals:git-workflow`, CLAUDE.md
Post-Execution rewrite). Every change is developed on a `feat/<slug>` branch off
up-to-date `main`; the **R10 pass is the sole landing trigger** — on PASS the AI
auto-commits, pushes `feat/`, **fast-forward-only** merges into `main`, tags, and
prunes the branch, with **no per-change confirmation** (supersedes "push only if
the user asked"). **NEVER force-push** — broadened to any branch in any repo, no
`--force` / `--force-with-lease`. Contributors without write access use the same
discipline via a fork + `gh pr create`; the AI may `gh`-approve/merge an open PR
ONLY after fetching its head, reviewing the diff, and running R10 to a PASS.
Multi-repo changes share one `feat/<slug>` and land producer→consumer in
dependency order; a change referenced via `@github` lands the producer + tag
FIRST, then `charly image reconcile` repoints the consumer, whose authoritative R10
runs against the real pushed tag. Sync-to-upstream before start/landing and
prune-only-merged-branches + worktree-prune hygiene per repo.

**Two sharpened acceptance gates.** (1) **Eval-coverage:** a change is landable
only if it ships the test coverage that PROVES its functionality (`eval:` checks
for new/changed layers & images, Go tests for `ov` code) and the R10 run
exercised it. (2) **Zero-warnings:** R10 is successful only at ZERO warnings
(resolver newest-wins / build / `charly image validate` / `charly eval` / deploy) — a
version-mismatch warning is cleared with `charly image reconcile`, anything else via
root-cause-analyzer + a real fix. R1 is now a hard gate, not just an
investigation trigger.

**`charly image reconcile`** (`ov/reconcile.go`, `/ov-build:reconcile`). Aligns every
`@github` pin of a repo to one version — newest already-referenced (default,
offline) or newest remote tag (`--remote`) — comment-preserving and idempotent,
so the resolver emits zero newest-wins warnings. Reuses `ParseRemoteRef` /
`StripVersion` / `compareSemver` / `GitLatestTag` and the `yaml_setter.go`
node-API pattern; covered by `ov/reconcile_test.go`.

No schema change (`version:` unchanged) — additive command + documentation only.

### 2026-05-24 — Remote-layer resolver: warn-and-newest-wins version resolution + precise namespace collection + `LayerRef`/`Has*`/parse-path cleanup (bug fix + refactor, no schema bump)

A full RCA of the selkies-desktop `ffmpeg`-missing failure overturned the earlier
"compose `ffmpeg`" hypothesis: the selkies layer's `layers: [ffmpeg]` was already
correct. The real defect was in the `ov` remote-layer resolver, and the fix is a
unified rewrite of how versioned `@github` layer refs are collected and resolved.

**Root cause — silent version-collision.** A submodule pins different parts of the
ecosystem at different tags (selkies-desktop at `v2026.144.0531`, shared infra at
`v2026.141.1600`). Shared transitive leaves (`ffmpeg`, `chrome`, `supervisord`,
`pipewire`, `nodejs`, …) were therefore reached at TWO tags. The transitive
fix-point in `ScanAllLayerWithConfigOpts` (`layers.go`) deduped remote refs by
**bare ref, version-blind** (`scanned map[string]bool`) and let the
**first-scanned** version win silently — while the initial collector
(`CollectRemoteRefsOpts`, `refs.go`) hard-errored on the same condition. The two
paths were inconsistent. For `ffmpeg`, the older `v2026.141.1600` (which predated
the layer's `distro.arch.package`) won the race, so the cachyos/`pac` build
emitted no `ffmpeg` install → `libx264.so.165` missing → pixelflux never created
`/tmp/wayland-1` → chrome crash-loop. The "depth-2 vs depth-3 composition" theory
was a red herring; the discriminator was "this layer changed between the two
pinned tags."

**Resolver policy — warn-and-newest-wins (`refVersionTracker`).** A single shared
`refVersionTracker` (`refs.go`) now backs BOTH the initial collection and the
transitive fix-point. When a bare ref is referenced at two versions it does NOT
fail: it warns once (naming both versions + their sources) and keeps the NEWEST
(highest CalVer/semver via `compareSemver`). The fix-point re-scans a layer when a
newer winner is discovered later (`scannedAt` tracks the version materialized).
This lets a build proceed with the latest referenced version of each layer instead
of requiring every reference across the whole import graph to pin one tag — and it
fixes selkies-desktop with zero submodule re-pinning (`ffmpeg`/`chrome`/`nodejs`/…
all resolve to `v2026.144.0531`, the version carrying the fixes). Single-version
projects are byte-unchanged (no conflict → no warning → no re-scan).

**Over-collection eliminated — precise namespace collection.** `CollectRemoteRefsOpts`
previously scanned EVERY image and EVERY `kind:local` template of EVERY imported
namespace ("harmless because all refs pin the same version" — an assumption the
multi-tag submodule layout broke). That pulled, e.g., the cachyos `ov-cachyos`
operator-workstation template's `chrome:v2026.141.1600` into a selkies-desktop
build that never uses it. Collection now walks only **base/builder reachability**
from the enabled root images (a namespace is imported to provide bases/builders;
its unreferenced images and its `kind:local` templates can never be a base/builder
of the importing project). Builder edges ARE followed (a namespaced builder like
`ov.fedora-builder` is built as an intermediate and needs its `rpmfusion`/`yay`
layers); dropping them under-collected. Verified: all eight submodules + main
`image generate` clean (no under-collection).

**`Layer` struct rethink (the duplication that enabled the bug).** The parallel
`Require`/`RawRequire` + `IncludedLayer`/`RawIncludedLayer` arrays (the bare copy
was just `BareRef(raw)` kept in lockstep) collapsed into one `[]LayerRef` per
concern; `LayerRef.Bare()`/`.Version()`/`.IsRemote()` derive from the single
stored ref, and a `resolved` slot carries the qualified sibling key so one list
serves both the graph (keys on `.Bare()`) and the fetch (keys on `.Raw`). The ~17
denormalized `Has*` boolean fields (`HasEnv`, `HasPorts`, `HasVolumes`, …) became
derived methods; the 7 filesystem-probe caches (`HasPixiToml`, `HasSrcDir`, …)
stayed fields. The duplicate exported/unexported `Description`/`description`
fields collapsed to one. The two post-parse populators (`scanLayer`'s inline block
and `unified.go`'s `populateLayerFromYAML`) unified into one — which fixed a latent
drift where the inline path silently dropped `artifacts`/`capabilities`/
`requiresCapabilities`/`shell`/`description`.

Standing rules established forward-looking in `/ov-internals:go`,
`/ov-image:layer`, `/ov-build:generate`: one layer resolves to one version per
build (newest-wins with a warning on disagreement); remote-ref collection is
reachability-scoped to bases/builders of the build set; `LayerRef` is the single
ref representation; `Has*` predicates are derived methods except the
filesystem-probe caches. No schema change — `version:` unchanged.

### 2026-05-24 — selkies composes `ffmpeg` (pixelflux runtime libs missing on the cachyos base) + auto-detection eval tests (bug fix, no schema bump)

The cachyos-based `selkies-desktop` (since the affbd46 cachyos migration) deploys
but its desktop never comes up: chrome crash-loops, `/json/version` EOFs. Root
cause (definitively traced, not GPU capacity): pixelflux's Wayland backend
(`pixelflux_wayland.so`, compiled in arch-builder) is dynamically linked against
`libx264.so.165` + `libavcodec/util/filter`, but the cachyos final image installs
no ffmpeg/x264 — so the backend fails to load
(`libx264.so.165: cannot open shared object file`), `_GLOBAL_WAYLAND_BACKEND` is
None, pixelflux never creates `/tmp/wayland-1`, and labwc → chrome never start.
The GPU is fine (the GL renderer inits on renderD128 once the libs are present).

The selkies layer compiles pixelflux but never declared pixelflux's runtime link
deps. The old Fedora-based selkies-desktop happened to get the libs transitively;
the cachyos base does not.

Fix: `layers/selkies/layer.yml` now **composes** the ffmpeg layer via `layers:
[ffmpeg]`. A first attempt used `require: ffmpeg`, but verifying the generated
Containerfile showed it emitted no install (only a layer comment): `require:`
ORDERS deps that are composed elsewhere, while `layers:` is what actually pulls a
pure-package leaf layer into the build. `layers: [ffmpeg]` groups ffmpeg into the
shared auto-intermediate (`…-ssh-client-ffmpeg-…`), whose Containerfile now runs
`pacman -Syu ffmpeg` (arch: pulls `x264` → `libx264.so.165`) / `dnf install
ffmpeg` (fedora: negativo17) — supplying every lib pixelflux links. Validated:
installing ffmpeg in the running bed made pixelflux load ("Rust Wayland Backend
Initialized Globally"); regenerating confirmed `ffmpeg` in the intermediate's
pacman block. R9 — runtime deps are declared. Affects selkies-desktop[-nvidia]
(pixelflux consumers); sway-browser-vnc does not use the selkies layer.

Auto-detection eval tests added so this can never silently regress again (the
prior eval suite passed despite the desktop being dead):
- `layers/selkies/layer.yml`: `pixelflux-wayland-libs-resolvable` (BUILD-scope —
  `ldd` of `pixelflux_wayland.so` asserts no `not found`; catches the missing
  runtime lib at `charly eval image`, no deploy/GPU needed) + `pixelflux-wayland-socket`
  (deploy — `/tmp/wayland-1` exists).
- `layers/labwc/layer.yml`: `labwc-wayland-socket` (deploy — `/tmp/wayland-0`
  exists; `service: labwc running` was crash-loop-blind).
All validated against the live production instance (healthy: 0 `not found`,
both sockets present) and against the broken cachyos build (4 `not found`, no
sockets).

No schema/format change → no `MigrationStep`, no `version:` bump; landing push
carries a fresh per-push `v<CalVer>` tag.

### 2026-05-24 — Add readiness waits (`eventually:`) to the chrome CDP/MCP deploy-scope eval probes (bug fix, no schema bump)

Surfaced by `charly eval run eval-selkies-desktop-pod`: 105/106 live checks passed,
the lone failure being `http http://…:9222/json/version → EOF`. Root cause: a
readiness race, not a defect — the cdp-proxy port was reachable, the CDP-backed
MCP probe and the selkies web UI both passed, and the identical probe passed on
`sway-browser-vnc`. On the heavier selkies-desktop startup (labwc + pixelflux +
supervisorctl-started Chrome) Chrome's CDP HTTP endpoint simply wasn't answering
yet when the one-shot probe fired right after the container reached "started".

Fix: the chrome CDP/MCP deploy-scope probes now use the eval framework's existing
`eventually:` readiness primitive (bounded poll-until-ready; the per-attempt
`timeout:` is the inner cap) instead of firing once. This still FAILS if Chrome
never comes up — it only tolerates startup latency, it does not mask a real
outage (not a sleep/retry-on-flake workaround). Applied to ALL chrome-dependent
deploy-scope probes (R3 — fix every surface, not just the one that flaked):

- `layers/chrome-cdp/layer.yml`: `chrome-cdp-port` (addr, `eventually: 60s`) and
  `chrome-cdp-version` (http `/json/version`, `eventually: 90s`).
- `layers/chrome-devtools-mcp/layer.yml`: `chrome-devtools-mcp-port` (addr,
  `eventually: 60s`), `mcp-chrome-devtools-ping` + `mcp-chrome-devtools-list-tools`
  (mcp, `eventually: 90s` — the MCP server proxies to Chrome's CDP, so its
  liveness depends on Chrome being up).

No schema/format change → no `MigrationStep`, no `version:` bump; the landing push
carries a fresh per-push `v<CalVer>` tag.

### 2026-05-23 — Fix layer-ordering bug (authored `layer:` order ignored by `GlobalLayerOrder`) + base `fedora-builder` on `fedora-nonfree` (bug fix, no schema bump)

Surfaced while extracting the selkies image family into a submodule. A submodule
that mixes an **arch-builder** image (`selkies-desktop`) and a **fedora-builder**
image (`sway-browser-vnc`) failed to build: `fedora-builder` tried to
`dnf install ffmpeg-devel x264-devel` (RPM Fusion packages) **before** the
`rpmfusion` layer enabled the repos — `No match for argument: ffmpeg-devel`.

Root cause (charly code): `GlobalLayerOrder` (`ov/intermediates.go`) built its layer
dependency graph **only** from `requires:` + `layers:` edges and ordered the rest
by cross-image *popularity*. The authored `layer:` list order was never a
constraint. `fedora-builder`'s `[rpmfusion, …, build-toolchain]` has no
`require:` edge (build-toolchain can't `require: rpmfusion` — on Arch the codec
libs come from the distro repos), so in a project where `build-toolchain` is the
more popular layer (shared by arch-builder + fedora-builder), popularity placed
it ahead of `rpmfusion`. Main and the pure-Fedora submodule happened to order
correctly only because `rpmfusion` was more popular there.

Fix (two parts, both shipped):
1. **`ov/intermediates.go`** — `GlobalLayerOrder` now adds each image's (and each
   metalayer's) authored list-adjacent graph-node pairs as dependency edges,
   cycle-safe (an edge that would create a cycle — i.e. genuinely conflicting
   authored orders across images — is skipped, falling back to the popularity
   tie-break). Popularity remains the tie-break among unconstrained layers.
   Regression tests: `TestGlobalLayerOrder_RespectsAuthoredListOrder` (reproduces
   the popularity inversion) + `TestGlobalLayerOrder_ConflictingListOrderFallsBack`.
2. **`base.yml`** — `fedora-builder` now `base: fedora-nonfree` (was `fedora` +
   an in-image `rpmfusion` layer). RPM Fusion now lands in the base chain, before
   any builder layer, making the builder correct independent of layer ordering —
   architecturally right since fedora-builder compiles nonfree codecs.

No schema/format change → no `MigrationStep`, no `version:` bump; the landing
push carries a fresh per-push `v<CalVer>` tag. Verified: `go test ./...` green;
`charly image build fedora-builder` installs ffmpeg-devel/x264-devel cleanly; with
the OLD `fedora-builder` definition + the new binary, the selkies submodule's
generated `ov.fedora-builder` Containerfile orders rpmfusion before ffmpeg-devel.

### 2026-05-23 — Extract the NVIDIA GPU base family (`nvidia` + `python-ml`) into the `overthinkos/nvidia` submodule (content cutover, no schema bump)

First family in the program to move *images* (not just distro layers) out of the
main repo into a dedicated `image/<family>` submodule, continuing the
arch/cachyos/fedora/debian/ubuntu/bootc precedent. The two GPU base images moved
to `overthinkos/nvidia` (mounted at `image/nvidia`):

- `nvidia` — GPU base (`base: ov.fedora-nonfree` + the `nvidia` + `cuda` layers)
- `python-ml` — GPU ML Python env (PyTorch/transformers/vLLM/llama.cpp), disabled

**The GPU runtime *layers* stayed in main.** `nvidia`, `cuda`, `python-ml`, and
`llama-cpp` are shared infrastructure consumed across many families (`versa`,
`immich-ml`, `jupyter-ml`, `comfyui`, `unsloth-studio`, `whisper`, `marimo`) and
by the arch/cachyos/fedora/bootc base submodules, so by the shared-layer rule
they remain in `main/layers/` and are reached from the submodule by `@github`
ref. The new submodule therefore **vendors nothing** — it pins layers + build.yml
to the ecosystem tag `v2026.141.1600` and imports main under the `ov` namespace at
`v2026.143.844` (for `ov.fedora-nonfree` + `ov.fedora-builder`).

**Mutual import (like cachyos).** main now imports `nvidia:
'@github.com/overthinkos/nvidia:v2026.143.1840'` and its six GPU pod families
(`comfyui`, `jupyter-ml`, `jupyter-ml-notebook`, `ollama`,
`selkies-desktop-nvidia`, `unsloth-studio`) root on `base: nvidia.nvidia`; the
nvidia repo imports main under `ov`. The cycle is broken at load.

No schema change (relocation only): no `MigrationStep`, no `version:` bump
(stays `2026.143.844`); each repo carries a fresh per-push `v<CalVer>` tag.

R10 (build-scope floor on a no-GPU host): `nvidia` built →
`charly eval image` 11/0/0 (nvcc, cudnn.h); `python-ml` built →
`charly eval image` 14/0/0 (torch + vllm importable). GPU runtime probes
(`nvidia-smi`, `torch.cuda.is_available()`) deferred to a GPU host.

### 2026-05-23 — Relocate single-repo layers into their owning `image/<distro>` submodules + enable all submodule images (content cutover, no schema bump)

Reversed the "vendors nothing" stance for layers used by exactly one repo: every
layer whose entire consumer set lived in a single `image/<distro>` submodule was
physically moved out of main's shared `layers/` tree into that submodule's own
`layers/` dir, its reference switched from a pinned `@github…/layers/<name>` ref
to a bare local name, and the submodule given a `discover: { layer: [{path:
layers, recursive: true}] }` block so the bare names resolve. Shared layers
(used by main or by ≥2 submodules) stay in main and are still pulled by `@github`
ref. main's `layers/` went 186 → 173.

**13 layers relocated** (computed from the submodules' explicit remote refs, then
filtered against main's own refs — including the remote-ref form in `base.yml`
that hides `yay`/`rpmfusion` usage — and against layer-level `require:`/`layer:`
consumers reachable from main):

- `image/arch` ← `arch-aur-test`, `arch-pac-test`
- `image/bootc` ← `bootc-base`, `bootc-config`, `copr-desktop`, `desktop-apps`,
  `os-config`, `os-system-files`, `ujust`, `vr-streaming`
- `image/cachyos` ← `ghostty`, `keepassxc-keyring`, `wheel-nopasswd`

`bootc-config` was not in the initial direct-ref list — its only consumer is
`bootc-base` (via the inner `layer:` composition), making it transitively
bootc-exclusive; it moved too. Conversely `ov`, `cuda`, `selkies-desktop`,
`virtualization`, `nodejs24` (direct main refs), `rpmfusion`/`yay` (remote refs in
`base.yml`), and `chrome`/`gnupg`/`ripgrep` (transitive main use via
`selkies-desktop`/`agent-forwarding`/`dev-tools`) all STAYED in main. `testapi`
and `traefik` (used only by the now-enabled `fedora-test`) also STAYED in main by
operator decision — generic test-API / reverse-proxy infrastructure kept in the
shared library for future cross-repo reuse rather than vendored into `image/fedora`,
which therefore vendors no layers and keeps its `@github`-ref'd fedora-test stack.

**Cross-repo deps stay in main, pulled by `@github` ref from inside the moved
layer.** `bootc-base`'s composition now `@github`-refs `sshd` + `qemu-guest-agent`
(local `bootc-config`); `keepassxc-keyring`'s `require:` `@github`-refs
`keepassxc`/`gnupg`/`direnv`. `CollectRemoteRefsOpts` already scans `layer.RawRequire` /
`RawIncludedLayer`, so layer-level `@`-refs download correctly — no Go change.

**All 7 disabled submodule images enabled** (`enabled: false` removed):
`image/arch`: `arch-ov`, `arch-test`; `image/bootc`: `aurora`, `bazzite`,
`selkies-desktop-bootc`; `image/fedora`: `fedora-ov`, `fedora-test`.

No eval entities moved: the submodule-specific eval beds (`arch-vm`,
`cachyos-vm-deploy`, `debian-debootstrap-vm`, …) already lived in their
submodules, and every `eval-*` fixture layer + every bed in main's `eval.yml`
serves a main-repo image.

**Immutable-tag note:** the cachyos↔main mutual import pins main at
`v2026.143.844` (which still contains the relocated layers), so
`charly -C image/cachyos image validate` emits benign "local layer X shadows remote
layer github.com/…/layers/X" notes — the local layer correctly wins. These
persist by design until the next coordinated `ov`-import tag bump; the old tag's
tree is never rewritten.

No schema-shape change (`discover:` is an existing top-level key; ref-form and
`enabled:` edits are content), so `LatestSchemaVersion()` and every `version:`
stay at `2026.143.844`.

### 2026-05-23 — Merge the four "mechanism" eval fixtures into one `eval-pod` bed + rename the AI sandbox to `eval-sandbox` (content cutover, no schema bump)

The four per-mechanism R10 smoke fixtures — beds `eval-image-pod` / `eval-layer-pod` /
`eval-pod-pod` / `eval-deploy-pod`, their images `eval-image` / `eval-layer` /
`eval-pod` / `eval-deploy`, and the four `layers/eval-{image,layer,pod,deploy}-layer/`
dirs — collapsed into a SINGLE `eval-pod` bed backed by a single two-layer
`eval-pod` image. An R10 mechanism sweep previously ran four full
build → eval image → deploy → eval live → fresh-update → teardown cycles
(~85–105s each); it now runs ONE cycle (~110s) with every check preserved.
Coverage is intact because the two layers keep the layer-composition test alive:

- `layers/eval-base-layer/` writes `/etc/eval-base-marker` (build smoke +
  composition anchor).
- `layers/eval-stack-layer/` asserts the base marker survived (composition
  order), runs `nc -lk 18794` (kind:pod runtime) AND `sleep infinity`
  (DeployTarget rendering) under supervisord, and carries the port-listening +
  service-running deploy-scope probes.

Diagnostic granularity survives at the `id:` level — a failing
`eval-service-running` still names exactly which mechanism broke.

**AI-sandbox rename `eval-pod` → `eval-sandbox`.** The merged bed needed the
name `eval-pod`, which was previously reserved for the harness AI-sandbox pod
(the `default` + `scaffolding-selftest` score `pod:` target). The sandbox was
renamed to `eval-sandbox` so `eval-pod` is free for the bed. The derived
container/unit (`ov-eval-sandbox[.service]`) follows automatically — production
Go already builds it as `"ov-"+tn` where `tn` comes from
`ResolveScoreTarget(score.Pod)`, so no Go logic changed, only the score's
`pod:` value.

**No hardcoded names in `ov` Go code (operator request).** The cutover removed
every baked sandbox/bed name from the Go source: comments now refer generically
to "the harness sandbox" / "the sandbox pod"; the preflight log message
interpolates the configured name via `%q`; and test fixtures use neutral
`sample-*` placeholders (`eval_bed_run_test.go`, `eval_recipe_test.go`,
`eval_substitute_test.go`, `clean_test.go`) so they prove the mechanism for ANY
name rather than coupling to config. The name lives in exactly one place —
eval.yml `score.pod` — and the score prompts reference it through the existing
`${TARGET_NAME}` substitution token (`eval_substitute.go`) instead of repeating
the literal. The `eval-image` / `eval-live` strings remaining in Go are the
`charly eval image` / `charly eval live` verb-step labels, not the deleted fixture image.

Also removed the stale `--keep-eval-pod` reference from CLAUDE.md's score-flag
list — no such flag exists in the eval-run command (`ov/eval_runner_cmd.go`
ships `--keep` / `--no-rebuild` / `--all-beds` / `--keep-repo` / `--on-*` /
`--plateau-iteration` / `--max-scenario` / `--tag` / `--dry-run` /
`--skip-rebuild`).

This is a content/instance cutover (renamed + merged specific entities), not a
schema-shape change — so NO `MigrationStep` and NO `LatestSchemaVersion` /
`version:` bump, mirroring the earlier deploy→eval-bed relocation. Operators who
run the harness must rename their `~/.config/charly/deploy.yml` `eval-pod`
AI-sandbox deploy to `eval-sandbox` (it lives in the per-host deploy file, which
`charly migrate` does not rewrite from a score-value change).

### 2026-05-23 — Build-artifact cleanup: one-time auto-purge + configurable reusable-artifact retention (`charly clean`, `defaults.keep_images`/`keep_eval_runs`) (additive, no schema bump)

Follow-up to the build-speedup cutover. Investigation found the project tree had
grown to ~12G of build artifacts from three never-cleaned accumulators: `pkg/arch`
(1.4G — 138 stale makepkg `*.pkg.tar.zst` + `src/`/`pkg/`, `task build:ov` never
cleaned up), podman image storage (164GB reclaimable from old CalVer image tags),
and `.eval/` (1.7G run output). Operator principle: **one-time artifacts are
always cleaned immediately; reusable artifacts get retention configurable in
`defaults:`**, with both auto-pruning at creation AND an explicit `charly clean`.

Additive, like the build-speedup keys: optional `defaults:` sub-keys with Go
fallbacks ⇒ no MigrationStep, no `LatestSchemaVersion` bump.

- **One-time (always immediate):** `task build:ov` now removes makepkg `src/`,
  `pkg/`, `*.pkg.tar.zst`, `*.log` after install (Taskfile change).
- **`defaults.keep_images`** — after `charly image build` (push runs excluded),
  prune all but the newest N CalVer tags per `ai.opencharly.image` group,
  ordered by the `ai.opencharly.version` label. Safety: skip any image in use
  by a container (`podman ps -a`), and `rmi` WITHOUT `-f` as a backstop so the
  engine refuses any still-referenced image. `keep_images: 0`/absent disables.
- **`defaults.keep_eval_runs`** — after `charly eval run` (any path: bed /
  `--all-beds` / score), trim `.eval/<bed|score>/` to the newest N run artifacts
  (CalVer run dirs, `runs/<id>` dirs, `result-<calver>.yml`). `NOTES.md` (durable
  Syncthing memory) is ALWAYS preserved. `keep_eval_runs: 0`/absent disables.
- **`charly clean`** — on-demand verb applying the same retention now, plus the
  makepkg sweep; clears the existing backlog (the 138 `.pkg.tar.zst` + old image
  tags). `--dry-run` / `--images` / `--eval` / `--keep N`.
- Repo `charly.yml` ships `keep_images: 3`, `keep_eval_runs: 3`. Go fallbacks
  are 0 (disabled) so third-party configs get no surprise pruning.

**Fixed `ai.opencharly.version` (was hardcoded `"1"`).** The label now carries
the BUILD CalVer — the version the generate run stamped the image with, equal to
the image's tag (e.g. `2026.143.1234`) — instead of the meaningless
`LabelSchemaVersion` constant `"1"`. `ExtractMetadata` only ever used this label
as the "is this an charly image?" presence sentinel, so the value change is safe; the
dead `LabelSchemaVersion` const was removed (its only two uses were these
emission sites). Retention orders builds by the CalVer in the image **tag**
(`extractCalVerTag`), so it works even on images built before this fix (their
label is still the stale `"1"`).

Implementation: `ov/clean.go` (`pruneImagesByRetention`, `pruneEvalRuns`,
`cleanMakepkgArtifacts`, `CleanCmd`); hooks in `BuildCmd.Run` / `EvalRunCmd.Run`;
`LocalImageInfo.ID` added for the in-use skip; same `mergeImageConfig` field-carry
discipline as the build-speedup keys. VM disks (`output/`, `image/*/output/`) are
out of scope — single products per type, no accumulation, removed on demand by
`charly vm destroy --disk`; the VM raw intermediate is already auto-cleaned
(`vm_cloud_image.go`).

### 2026-05-23 — Config-driven build-speedup tunables (`defaults.{jobs,podman_jobs,podman_jobs_cap,context_ignore,cache}` + `distro.<name>.dnf` + committed `pixi.lock`) (additive, no schema bump)

A four-part build-speed cutover landed as ONE atomic, **additive** commit. It is
deliberately NOT a schema change: every new key is an optional sub-key of an
existing kind (`defaults:` / `distro:`) with a Go fallback, so per the
cutover-policy skill ("purely additive ⇒ no cutover") there is no
`MigrationStep`, no `LatestSchemaVersion()` bump, and no load-time gate — old
configs keep loading via fallbacks, and third-party configs are never forced to
run `charly migrate` for keys they don't use.

**Item 1 — build-context excludes (`defaults.context_ignore`).** The static
hand-maintained `.containerignore` (`​.git bin charly *.md`) and `.dockerignore`
(editor/python/node cache-bust globs) were **deleted** and are now GENERATED at
the project root by `charly image generate` (`writeContextIgnore` in
`ov/generate.go`) from a single source: a Go baseline (the union of both former
dotfiles) plus `defaults.context_ignore`. Both engine files are emitted from one
value set (podman reads `.containerignore`, docker reads `.dockerignore`), and
both are now gitignored. The repo's `context_ignore` adds the heavy never-COPYed
directories `image/` (3.5 GB submodules), `.eval/`, `output/`, `pkg/`, `tests/`,
`.regression-snapshot/` — ~7.3 GB that previously streamed into the context tar
on EVERY build regardless of cache state. Confirmed via grep that no generated
Containerfile COPY/ADDs from any excluded directory (only `layers/`,
`templates/`, `.build/`).

**Item 2 — config-driven parallelism (`defaults.{jobs,podman_jobs,podman_jobs_cap}`).**
The hard-coded `const podmanJobsDefault = 4` was removed and replaced by
`resolvePodmanJobs(override, cap)`, where the cap comes from
`defaults.podman_jobs_cap` (named fallback `podmanJobsCapFallback = 4` only when
the key is wholly absent). The outer image-level concurrency reads
`defaults.jobs` (fallback `jobsFallback = 4`). The missing `env:"OV_BUILD_JOBS"`
binding on `--jobs` was added (doc/code drift the build SKILL had documented but
the struct tag lacked). Precedence everywhere: CLI flag → env → `defaults:` →
fallback. The repo ships `podman_jobs_cap: 8`, proven safe by the 20-run race
gate below.

*Relocated incident (formerly the `podmanJobsDefault` comment in
`ov/build.go`):* the cap originally existed because podman-5.7.x's storage
backend raced under high concurrency during multi-stage builds with
`--cache-from` — many goroutines calling
`storageImageDestination.TryReusingBlobWithOptions` and `queueOrCommit`
concurrently corrupted shared state and aborted with SIGABRT, observed
reproducibly on `selkies-desktop` (29-stage DAG) with `--jobs runtime.NumCPU()`
(16 on a 16-core host) and `--cache-from`. Four was chosen as a balance. The
host is now podman 5.8.2; the cutover's mandatory 20-run race gate
(`--podman-jobs 16` × 10 warm builds each of `fedora-coder` + `selkies-desktop`,
the exact old trigger) is the precondition for shipping any cap > 4.

**Item 3 — committed `pixi.lock` for all 15 pixi layers.** The
`pixi install --frozen` fast-path was already fully wired (`build.yml` install
command map, `HasPixiLock` detection, the stage template's conditional
`COPY pixi.lock`); only the lock artifacts were missing, so generation emitted
plain `pixi install` (a full SAT solve over ~300 deps across conda-forge +
multiple PyPI indexes on every cache miss). A `pixi.lock` is now committed next
to every `layers/*/pixi.toml`, generated with the builder's own pixi (0.69.0)
and the same `[system-requirements] glibc 2.39` manylinux fix the build stage
applies, so the committed lock matches what `--frozen` installs. Generation
auto-flips to `pixi install --frozen` (no Go change). Lock drift is caught
loudly — `--frozen` fails the build if a lock is stale, so a future `pixi.toml`
edit without regenerating the lock is a hard build error, not a silent skew.

**Item 4 — dnf download tuning (`distro.<name>.dnf`).** A new optional
`DnfConfig` (`max_parallel_downloads`, `fastestmirror`) on `DistroDef` is
written to `/etc/dnf/dnf.conf` during the bootstrap (`renderDnfConfWrite` in
`ov/generate.go`), so it speeds up the bootstrap install AND every per-layer dnf
install in the image + descendants. These are SPEED-only knobs — they never
change package selection, so `install_weak_deps` stays exactly as the existing
bootstrap `--setopt=install_weak_deps=False` (unchanged) to keep the cutover
purely additive. `build.yml distro.fedora.dnf` ships `max_parallel_downloads:
10`, `fastestmirror: true`. The block inherits across distro inheritance like
the other `DistroDef` sub-blocks.

**Regression caught during implementation:** `mergeImageConfig` (`ov/unified.go`)
is a hand-maintained field-by-field merger for the `defaults:` block; the five
new `ImageConfig` fields were initially dropped after the unified loader merged
the flat imports, so `defaults.context_ignore` authored in `charly.yml` never
reached the generator (the YAML parsed but the runtime value was empty). Fixed
by adding the fields to the merger in-pattern; guarded by
`TestMergeImageConfig_BuildTunables`. This is the canonical reminder that adding
any `ImageConfig` field requires updating `mergeImageConfig`.

### 2026-05-23 — Replace `include:` with a Go-style `import:` namespace system; combine the base files into `base.yml`; single-file image submodules; ecosystem-wide deploy→eval beds (breaking, schema 2026.143.844)

The `include:` YAML composition key was **deleted** and replaced by a single
forward-looking `import:` statement modelled on Go's package imports. `import:`
is a LIST whose items are either a **bare string** (a *flat* import into the
importing repo's root namespace — used for same-repo per-kind files and the
shared `build.yml` distro/builder/init *vocabulary*) or a **single-key map
`alias: ref`** (a *namespaced* child import that mounts another project under
`alias`, whose entries are then referenced QUALIFIED as `alias.entry`). This
removes the old flat-merge limitation: a repo can now cherry-pick exactly the
entities it needs from another repo over GitHub (`base: cachyos.cachyos`,
`builder: {pixi: ov.arch-builder}`) instead of flat-merging a whole file. A
residual `include:` key is now a hard load-time error pointing at `charly migrate`.

**Resolution model** (`ov/namespace.go`, `ov/unified.go`): `UnifiedFile.Import`
(custom mixed-list marshal/unmarshal) + `UnifiedFile.Namespaces`; namespaced
imports load into an isolated child `UnifiedFile` via `loadNamespaceCached`,
whose shared resolved-ref cache breaks the intentional main↔cachyos mutual
import. The resolver (`resolveImageRef` / `resolveNamespacedBases` /
`pullNamespacedImage`) is namespace-relative (Go package-member semantics): a
bare ref inside namespace `ov` resolves within `ov` first; qualified refs
descend. `distro:`/`build:` are VALUES and inherit across a namespace boundary;
`builder:` is a map of namespace-relative REFS and does NOT cross — a consumer
declares its own builder map (the auto-intermediate builder map now lets the
consumer's builder win over a cross-namespace base's, in `intermediates.go`).
Threaded through the image base check, the base-chain walkers, `ResolveAllImage`,
`CollectRemoteRefs` (walks namespaces so a pulled builder's `@github` layers are
collected), and the builder validators in `validate.go`. An RCA caught two
defects fixed in the same cutover: `validateImageDAG` resolved images without
`resolveNamespacedBases` (a dangling namespaced base edge surfaced as a
zero-length "image dependency cycle"), and the namespaced-builder walk pulled a
layerless base's namespace-relative builder ref from the wrong context.

**Config reshape.** The main repo's former `arch-base.yml` + `fedora-base.yml`
were combined into one `base.yml` (entities `arch`, `arch-builder`, `fedora`,
`fedora-builder`, `fedora-nonfree`). The CachyOS base stays owned by the
`overthinkos/cachyos` submodule; main's `versa` (and the selkies/openclaw family
that roots on the cachyos base) now use `base: cachyos.cachyos` via the `cachyos`
import namespace, each carrying an explicit `arch-builder` map. The main repo
**keeps its multi-file layout**; **every `image/<distro>` submodule
(arch/cachyos/debian/ubuntu/fedora/bootc) is now a single `charly.yml`** (all
per-kind siblings inlined) that imports `build.yml` flat and (where it needs main's
base entities) imports main under the `ov` namespace (`ov.arch`, `ov.fedora`,
`ov.arch-builder`, `ov.fedora-builder`). Several latent pre-existing bugs were
fixed in passing per R2 (a stray `disposable:` on a VmSpec, singular
`libvirt.device:`/`channel:` keys that silently dropped the SPICE channel, and
`cloud_init.user:` → `users:` in the debian/ubuntu/arch VMs).

**deploy→eval unification.** Repo-shipped disposable VM test beds in the
submodules (`arch-vm` + its nested beds, `arch-pacstrap-vm`, `cachyos-vm-deploy`,
`debian-debootstrap-vm`, `ubuntu-debootstrap-vm`) moved from `kind: deploy`
(deploy.yml) to `kind: eval` (in the single charly.yml), matching the main
repo's model. The cachyos `ov-cachyos` operator workstation profile stays
`kind: deploy` (it mutates a real host — not a zero-side-effect bed). The
now-empty submodule `deploy.yml` files were deleted.

**Schema + migration.** Schema CalVer bumped `2026.141.1600` → **`2026.143.844`**.
A new idempotent `import-namespace` `MigrationStep` (CalVer 2026.143.843) renames
`include:` → `import:` in every project YAML; `migrate_arch_rename.go`'s hardcoded
`arch-base.yml` became `base.yml`. This established the standing rule (CLAUDE.md,
`/ov-build:migrate`): **every YAML schema/format change MUST raise
`LatestSchemaVersion()` via a `MigrationStep` (re-stamping `version:` in every
yml) AND carry a fresh per-push `v<CalVer>` repo git tag — format change ⟹
`version:` bump ⟹ git tag.**

### 2026-05-22 — Add `openclaw-desktop` all-in-one image; decouple CUDA from the ollama layer; drop `selkies-desktop-ov` (breaking)

A new **`openclaw-desktop`** image fuses four stacks onto one `base: cachyos`, `build: [pac, aur]` image: `selkies-desktop` (the streaming Wayland desktop), `openclaw-full` (the gateway + 27 tools, **already including `claude-code`/`codex`/`gemini`** — those three named CLIs are layers of the openclaw-full metalayer, not separate adds), a **CPU `ollama`**, and the full nested `ov` toolchain (`ov-full` + `container-nesting` + `golang` + `gh`). It exposes 3000/9222/9224/2222 (selkies) + 18789 (openclaw gateway) + 11434 (ollama), runs at uid 1000 with the `unmask=/proc/*` rootless-nesting posture from `container-nesting` (no `--privileged`, no added caps), and gains a positive synergy: openclaw-full's `playwright` (headless, no system browser) now drives selkies' real `chrome` + `chrome-cdp` on :9222. Composition analysis confirmed zero port/service-name collisions across the union, and every constituent layer is cachyos-safe (the ov-full/nesting layers carry `distro.arch` sections; `gocryptfs` installs via its distro-agnostic top-level `package: [gocryptfs]` → `pacman -S gocryptfs`, already proven by `arch-ov`).

**Ollama layer CUDA-decoupling (R3, generic over ad-hoc).** Composing the `ollama` layer onto a non-NVIDIA base was blocked by the layer's `require: [cuda]` — a transitive pull of the Fedora/NVIDIA `cuda` layer onto a pac base. Since the Ollama binary is a distro-agnostic tarball that auto-detects the GPU at runtime (CPU fallback when none present), the `cuda` coupling was wrong at the layer level. Fix: drop `cuda` from `layers/ollama/layer.yml` `require:` (now just `supervisord`) — the layer is GPU-agnostic, GPU is an image-level composition choice. NO `ollama-cpu` sibling layer (forbidden anti-pattern). The standalone `ollama` image (`base: nvidia`, `enabled: false`) needs **no change** — it inherits the `cuda` layer from the `nvidia` base chain (`nvidia` image = `[agent-forwarding, nvidia, cuda]`), exactly as the removed `selkies-desktop-ov` did; the layer's `require: cuda` was redundant for it. `openclaw-desktop` (cachyos) composes the layer with no `cuda` and gets CPU inference. Confirmed `ollama` is the only consumer of the layer (`git grep '- ollama'` → only the ollama image).

**`selkies-desktop-ov` removed (breaking — public image surface deleted).** `openclaw-desktop` supersedes its role (streaming desktop + full nested charly toolchain, rootless uid 1000) — the CachyOS/CPU successor of the nvidia/GPU original. It was a leaf image (nothing had `base: selkies-desktop-ov`; no deploy.yml entry; no eval bed), so removal was a reference sweep, not a dependency untangle. Its 13 image-level nested-toolchain eval checks (subuid two-ranges, `newuidmap` cap, `policy.json`, containers.conf `userns=host` ×2, `_CONTAINERS_USERNS_CONFIGURED`/`BUILDAH_ISOLATION` env, nested `podman run`, `virsh` session list, in-container `charly version`/`charly doctor`) were **migrated into `openclaw-desktop`'s image-level `eval:`** so coverage transferred (the `virsh domcapabilities` KVM-hardware check stays covered by the `virtualization` layer's own baked `libvirt-kvm-acceleration` eval, inherited via `ov-full`). R5 hard-cutover sweep: deleted the image.yml entry, deleted `plugins/selkies/skills/selkies-desktop-ov/`, and repointed every CURRENT-state reference to `/ov-openclaw:openclaw-desktop` across ~16 skills + README.md + the `virtualization` layer comment — with one exception: `nvidia-layer`'s "base:nvidia image runs on AMD" anecdote repointed to `selkies-desktop-nvidia` (openclaw-desktop is CPU/cachyos, not a base:nvidia example). The valuable GPU-agnostic worked examples from the old skill (the two-level nested-virtualization proof, the cross-storage bootc-load recipe, the rootless posture table) were migrated into the new `openclaw-desktop` skill. `git grep selkies-desktop-ov` now returns only this `CHANGELOG.md` (main) and nothing in `plugins`.

A `kind: eval` R10 bed **`eval-openclaw-desktop-pod`** was added (`disposable: true`, ports remapped into a free `340xx` block — `34000`/`34222`/`34224`/`34022`/`34789`/`34434` — to coexist with the selkies/openclaw beds); its deploy-scope probes assert the cross-stack headline artifacts (AUR `google-chrome-stable`, the Selkies HTTPS-200 UI, the three AI CLIs at `${HOME}/.npm-global/bin/`, the `ollama` binary). The acceptance gate is `charly eval run eval-openclaw-desktop-pod` (build → eval image → deploy → eval live → fresh `charly update` rebuild → teardown). **No `MigrationStep` / no `version:` bump / no new git tag** (an additive image + a layer-decoupling refactor + a leaf-image removal; repo-internal, no schema change). See `/ov-openclaw:openclaw-desktop`, `/ov-ollama:ollama`, `/ov-distros:container-nesting`, `/ov-infrastructure:virtualization`, `/ov-eval:eval`.

### 2026-05-22 — Migrate `selkies-desktop` (CPU) to CachyOS base; cachyos AUR parity + AUR doc cleanup

the CPU `selkies-desktop` streaming-desktop image was **migrated from `base: fedora-nonfree` to `base: cachyos`** (the Arch-derived base owned by the `overthinkos/cachyos` submodule, already remote-included in `charly.yml` for `versa`/`openclaw`) — an in-place hard cutover mirroring the openclaw→cachyos precedent. **Scope was the CPU variant ONLY**; the GPU variants `selkies-desktop-nvidia` and `selkies-desktop-ov` (`base: nvidia`) stay on Fedora (porting the `/usr/lib64`-hardcoded `nvidia`/`cuda` layers to Arch is out of scope). Because all three selkies images compose the same `selkies-desktop` metalayer, the layer changes are backward-safe: the generator resolves a layer's packages by the IMAGE's `distro:` tags (first-match, `ov/generate.go` `compileSystemPackageSteps`), and the Fedora GPU variants carry `distro: [fedora,…]` which never matches the new `arch:` sections — so they keep installing the `fedora:` packages unchanged (R3 generic win). **Unlike openclaw, selkies-desktop ADDS `build: [pac, aur]`** (not just inherited `[pac]`): it composes `chrome` (AUR `google-chrome`) + `wl-tools` (AUR `wlrctl`), and the AUR builder is gated on `aur ∈ BuildFormats` (`generate.go:1418` + the IR Phase-2 install both key on `img.BuildFormats`) — inheriting plain `[pac]` would silently drop both AUR packages. Confirmed via `charly image generate`: the `chrome-aur-build` + `wl-tools-aur-build` arch-builder stages and the `pacman -U /tmp/aur-pkgs/*` install steps emit only with `aur` in `build:` (the same reason `arch-test` declares `build: [pac, aur]`). **Twelve Fedora-only desktop sub-layers that would have silently installed NOTHING on Arch** (the silent-install trap: no `arch:`/`cachyos:` distro section AND no `pac:` format section → zero installs, build succeeds, binary missing at runtime) each gained a `distro.arch` section (R3 — benefits any future Arch desktop image): `pipewire` (`pipewire-pulseaudio`→`pipewire-pulse`, dropped the Arch-absent `pipewire-utils`), `labwc` (`xorg-x11-server-Xwayland`→`xorg-xwayland`), `waybar-labwc`, `desktop-fonts` (COPR `che/nerd-fonts` has no Arch analog → Arch `extra` `ttf-jetbrains-mono`/`ttf-liberation`/`ttf-nerd-fonts-symbols`(`-mono`)), `swaync` (`SwayNotificationCenter`→`swaync`), `pavucontrol`, `wl-tools` (`xprop`/`xwininfo`→`xorg-xprop`/`xorg-xwininfo`; `wtype` from `extra`; `wlrctl` via `aur:`), `wl-overlay` (`python3-gobject`→`python-gobject`), `a11y-tools` (`python3-pyatspi`→`python-atspi`), `xterm`, `fastfetch`, and `selkies` (the big list: `libICE`/`libSM`→`libice`/`libsm`, `pulseaudio-libs`→`libpulse` which also covers `pulseaudio-utils`/pactl, `mesa-va-drivers`→`libva-mesa-driver`, `iproute`→`iproute2`). **Cross-distro eval via `package_map:`** (not a Fedora-name-only assertion): `desktop-fonts` and `a11y-tools` had `package:`/`installed:` eval checks keyed to Fedora package names; because eval blocks are NOT distro-gated (the still-Fedora GPU variants run the same block), each `package:` check got a `package_map:` (e.g. `python3-pyatspi` + `{arch: python-atspi, fedora: python3-pyatspi}`) so the SAME check resolves correctly on both bases — preserving the assertion everywhere instead of dropping it. `wl-tools` also gained a `wlrctl-binary` presence eval (the AUR `wlrctl` previously had NO presence check anywhere — R8). A `kind: eval` R10 bed `eval-selkies-desktop-pod` was added (`disposable: true`, ports remapped to `33000`/`39222`/`39224`/`32222`), asserting the AUR-built binaries (`google-chrome-stable`, `wlrctl`, `wtype`) plus key desktop binaries at deploy scope; the baked layer/image evals (incl. the Selkies HTTPS-200 UI probe) cover the rest. **CachyOS AUR parity + doc cleanup** (the operator asked to "make sure cachyos has full support for aur as arch"): functional AUR support already existed on cachyos via the inherited `builder.aur: arch-builder` (proven by the selkies-desktop AUR build above), but `cachyos` was the ONLY base distro lacking a `produce:` field (arch/fedora/debian/ubuntu all declare it). `produce: [pixi, npm, cargo, aur]` was added to `image/cachyos/cachyos-base.yml` matching arch. `produce:` is functionally inert here (cachyos is never referenced AS a builder — only consumed; `resolved.BuilderCapabilities` is read solely by `validateBuilders` when an image is a builder target), so it is a source-consistency fix; it lives in the submodule and main consumes cachyos via a PINNED remote include, so it does not affect main builds until the cachyos repo is pushed/retagged and main's pin bumped. The skill docs were clarified so AUR authoring is unambiguous: the canonical form is the nested `distro.arch.aur.package`, a consuming image must declare `build: [pac, aur]`, and `arch-builder` compiles AUR for BOTH arch and cachyos. **No `MigrationStep` / no `version:` bump / no new git tag** (a repo-internal in-place base swap + package-coverage addition, same class as the openclaw migration). The R5 sweep updated the selkies SKILL.md files referencing selkies-desktop's `fedora-nonfree` base. See `/ov-selkies:selkies-desktop-ov`, `/ov-distros:cachyos`, `/ov-distros:arch`, `/ov-image:layer`, `/ov-eval:eval`.

### 2026-05-22 — Trim openclaw to {`openclaw`, `openclaw-full`}, migrate both to CachyOS base, refresh to latest

the openclaw image family was reduced to the two shipping headless variants and moved off Fedora. **`openclaw-ollama` (the nvidia/CUDA gateway+Ollama image) was DELETED** from `image.yml`; the remaining `openclaw` and `openclaw-full` were **migrated from `base: fedora` to `base: cachyos`** (the Arch-derived base owned by the `overthinkos/cachyos` submodule and already remote-included in `charly.yml` for `versa` — no new plumbing, an in-place base swap mirroring `versa`), and both were **enabled** (`enabled: false` removed). Both images inherit `build: [pac]` from the cachyos base (the pixi/npm/cargo/aur→`arch-builder` map is inherited like `versa`; npm/go/cargo/pixi/download layers are distro-agnostic, and the pac layers — gh/tmux/ffmpeg/ripgrep/sqlite/dbus/socat — resolve via their `arch:` sections). **Two Fedora-only layers that would have silently installed NOTHING on Arch** (the `distro: null`-class trap) were fixed generically (R3 — benefits every Arch image): `ffmpeg` and `sqlite` each gained an `arch: { package: [...] }` section plus a presence `eval:` check (`/usr/bin/ffmpeg`, `/usr/bin/sqlite3`) so the install is actually asserted (R8). **`gogcli` was unpinned `@v0.4.2` → `@latest`**: the pin existed because Fedora 43 ships only Go 1.25 (`golang-bin`) while gogcli ≥ v0.13.0 needs Go 1.26.x; on CachyOS/Arch the `golang` layer's `go` package is `2:1.26.3`, so `@latest` (v0.14.0, go.mod 1.26.1) builds with **no golang-layer change** — the obsolete Fedora-toolchain comment was removed and a `${HOME}/go/bin/gog` eval check added. **R10 (the first build of the now-enabled `openclaw-full`) surfaced a latent upstream breakage** unrelated to the base migration: the `wacli` Go module moved from the `steipete` GitHub org to `openclaw` and carried the move into its `go.mod` (`module github.com/openclaw/wacli` at v0.10.0), so `go install github.com/steipete/wacli/...@latest` hard-failed on the module-path mismatch (it would fail on any base; it only surfaced now because `openclaw-full` was `enabled: false` and unbuilt since v0.10.0 shipped). The `wacli` layer's install path was updated to `github.com/openclaw/wacli` (R2 — fixed in the same working tree, not deferred). Every other steipete-org tool (gifgrep / goplaces / songsee / sag / camsnap / gogcli / ordercli) still declares the `steipete` path in its `go.mod` at `@latest` and was verified unchanged. **Version refresh policy: keep the existing `*` / `@latest` convention** — every other openclaw-full layer already tracks latest (openclaw npm `*`, the 11 npm tool layers `*`, the Go tools `@latest`, himalaya's `cargo install --locked` crate, uv's latest GitHub release, all pacman packages), so the fresh `charly image build` is what pulls newest published versions; no per-layer pinning was introduced. The **R5 sweep** (the earlier `git grep` missed the `plugins/` submodule) covered: the deleted `openclaw-ollama` SKILL.md; the stale `plugin.json` + `marketplace.json` descriptions (which still listed dead `bootc/full/ml/sway/ollama/browser` variants); `plugins/README.md` (count 7→6, reworded for the CachyOS base); the `openclaw`/`openclaw-layer`/`openclaw-deploy` cross-refs; the openclaw-ollama mentions in the `nvidia`/`ollama`/`ollama-layer`/`agent-forwarding`/`supervisord` skills; the now-stale `Base: fedora` / `linux/amd64,linux/arm64` / `disabled` facts in the `openclaw`/`openclaw-full` skills (updated to `cachyos` / `linux/amd64` / enabled); and the `openclaw-ollama` Go test fixture in `ov/intermediates_test.go`, renamed to a neutral `gpu-gateway` (same nvidia base + `[openclaw, ollama]` shape, so the intermediate-sharing assertions are unchanged). `git grep 'openclaw-ollama'` now returns only this file. **No `MigrationStep` / no `version:` bump / no new git tag** (a repo-internal image base swap + image drop, same class as the sway-family drop and the submodule extractions; a user `deploy.yml` deploying the dropped image still loads — deploy reads OCI labels, not `image.yml`). Two `kind: eval` R10 beds were added to `eval.yml` — `eval-openclaw-pod` and `eval-openclaw-full-pod` (both `disposable: true`, `eval-<descriptor>-<kind>` naming) — each driving the full `charly eval run` acceptance sequence (build → eval image → deploy → eval live → fresh `charly update` → teardown); the openclaw-full bed's `eval:` block asserts the migration-critical artifacts (`gog`, `ffmpeg`, `sqlite3`) at deploy scope. **R10 of the `eval-openclaw-full-pod` bed then surfaced a SECOND pre-existing, base-independent issue: headless `openclaw-full` composed `chrome` + `chrome-cdp` + (transitively) `chrome-devtools-mcp` but has no compositor and no Chrome-launch service, so `cdp-proxy` and the `chrome-devtools-mcp` server pointed at a Chrome that never starts — the `chrome-cdp` `/json/version` deploy probe failed (RCA-confirmed NOT a cachyos regression: Chrome v148 built + ran fine on cachyos; `chrome-wrapper` requires a Wayland socket absent in a headless image). The operator chose to STRIP the browser stack** — `chrome` + `chrome-cdp` removed from the `openclaw-full` metalayer (29→27 layers), making it a clean non-browser headless gateway. Cascade: the `openclaw-full` image dropped its `9222`/`9224` ports + the `build: [pac, aur]` override (no AUR consumer remains, so it inherits plain `[pac]`); the bed dropped its `9222`/`9224` host ports + the `google-chrome-stable` probe; the openclaw-full skill dropped its chrome/CDP/port rows; and — because NO openclaw image now ships `chrome-devtools-mcp` — the `ov-openclaw` plugin's `.mcp.json` (chrome-devtools @ 9224) was DELETED, the `mcpServers` field removed from `plugin.json`, the chrome-devtools claim removed from `plugin.json` + `marketplace.json`, and the `plugins/README.md` MCP column set to `—`. `playwright` (self-contained bundled browsers) was retained; the shared `chrome`/`chrome-cdp`/`chrome-devtools-mcp` layers stay (still used by selkies-desktop / sway-browser-vnc / chrome-sway). See `/ov-openclaw:openclaw`, `/ov-openclaw:openclaw-full`, `/ov-distros:cachyos`, `/ov-automation:openclaw-deploy`, `/ov-eval:eval`.

### 2026-05-22 — CHANGELOG.md established; all history relocated out of CLAUDE.md + skills

Created this `CHANGELOG.md` as the single home for historical / version-change
content. Swept every dated cutover paragraph, embedded "(YYYY-MM-XX)" note,
"renamed from / RETIRED / Superseded / previously / formerly", `Relocated (…)`
header, and commit-referenced cautionary tale out of `CLAUDE.md` and the ~290
`plugins/**/SKILL.md` files into this file. CLAUDE.md and every skill now read as
a present-tense description of current behavior; the standing rules that the
relocated cutovers established were kept (restated forward-looking), and stale
descriptions discovered during the sweep were corrected to match current
behavior. Added the standing policy (CLAUDE.md "Where things are documented" +
Key Rules, `/ov-internals:skills`, `/ov-internals:cutover-policy`) that history
goes ONLY in this file. Documentation-only change; no code paths change.

### 2026-05-22 — Drop `charly eval kind` + the hardcoded bed table → `kind: eval` R10 beds in `eval.yml`, run via `charly eval run`

the 11 disposable R10 test beds that lived as `deploy:` entries in `deploy.yml` (plus the hardcoded `bedTable`/`bedSpec` in `ov/eval_kind_cmd.go` that `charly eval kind <subkind>` walked) were unified into a single config-driven surface. Each bed is now a `kind: eval` document in `eval.yml` — a `DeploymentNode` (target + image/vm/local + `disposable` + `eval:` probes) folded into the Deploy map at load time (`foldEvalBeds` + `DeploymentNode.EvalBed`) so EVERY deploy verb resolves it by name through the same path; `uf.EvalBeds()` enumerates them. The `charly eval kind` command + `bedTable`/`bedSpec`/`bedSpecFor`/`kindList`/`validKinds` were DELETED; the R10 sequence engine was salvaged into `runEvalBed` (which reads the node directly — `bedSpec`'s image/vm/local/IsVM/IsLocal were pure duplication of fields already on the bed), and `charly eval run <name>` now dispatches by kind: a `kind: eval` bed runs the full R10 sequence (build → eval image → deploy → eval live → fresh update → tear down), a `kind: score` runs the AI loop; `--all-beds` runs every bed name-sorted. Beds renamed to a unified `eval-<descriptor>-<kind>` scheme (dropping a redundant suffix when descriptor == kind AND the short form is free): `k3s-vm` → `eval-k3s-vm`, `eval-local-deploy` → `eval-local`, `jupyter-pod`/`jupyter-ml-pod`/`versa`/`android-emulator-pod` → `eval-jupyter-pod`/`eval-jupyter-ml-pod`/`eval-versa-pod`/`eval-android-emulator-pod` (`eval-sway-browser-vnc-pod`/`eval-image-pod`/`eval-layer-pod`/`eval-pod-pod`/`eval-deploy-pod` unchanged — `eval-pod-pod` deliberately keeps its suffix because `eval-pod` is the reserved harness AI-sandbox pod name, the score `pod:` target; the `k3s-vm` *vm entity* + `vm-k3s-vm` *k8s entity* keep their names). The supporting `vm: k3s-vm` + `k8s: vm-k3s-vm` entities moved into `eval.yml` too; **`deploy.yml` was DELETED** and dropped from `charly.yml`'s `include:` (the repo ships only eval beds; operator deployments live in the per-host `~/.config/charly/deploy.yml`). Validation (`validateEvalBeds`, load-time so every verb benefits) enforces target ∈ {pod,vm,local}, a resolvable cross-ref, `disposable: true`, and a name space disjoint from `kind: deploy`. **No `MigrationStep` / no `version:` bump / no new git tag** (additive `kind: eval` + repo-internal bed relocation, same class as the six submodule extractions and the sway-family drop; `version:` stays `2026.141.1600`). Main-repo only — submodules never call `charly eval kind` and deploy their own beds via normal verbs. See `/ov-eval:eval`, `/ov-eval:eval-sway-browser-vnc`, `/ov-core:deploy`.

### 2026-05-22 — Drop the sway-desktop image family except `sway-browser-vnc` + `eval-sway-browser-vnc-pod` R10 bed on `sway-browser-vnc`

the four OpenClaw desktop+browser images composing the Sway streaming-desktop stack — `openclaw-full-ml`, `openclaw-full-sway`, `openclaw-ollama-sway-browser`, `openclaw-sway-browser` (main `image.yml`) — plus the bootc variant `openclaw-browser-bootc` (and its `kind: vm` entity) in the `image/bootc` submodule were DELETED. The single shipping Sway image `sway-browser-vnc` is KEPT and now also backs the canonical pod eval bed, renamed `openclaw-sway-browser-pod` → `eval-sway-browser-vnc-pod` (`disposable: true`, `image: sway-browser-vnc`); the bed's own `eval:` block adds the deploy-scope probes (operator-side http, cdp list, wl sway-tree, record) that `sway-browser-vnc` doesn't already bake. **Zero layer deletions** — `sway-browser-vnc` keeps `sway-desktop-vnc → sway-desktop`, so the entire sway layer stack (sway/chrome-sway/xdg-portal/xfce4-terminal/thunar/wl-*/swaync/waybar/…) stays in use; openclaw-only layers that lost their last image consumer (the `openclaw-full-ml` layer) remain as reusable library entries (unused ≠ deprecated). **No `MigrationStep` / no schema bump** (removal of repo-internal image definitions, like the six submodule extractions; a user `deploy.yml` deploying a dropped image still loads — deploy reads OCI labels, not `image.yml`). The R5 sweep covered `deploy.yml` (bed + coverage-map comments), the `ov/` Go test fixtures/comments, `README.md`, and the per-image skills (DELETED the `openclaw-sway-browser`/`openclaw-ollama-sway-browser`/`openclaw-full-sway`/`openclaw-full-ml` image skills + `openclaw-browser-bootc` + `openclaw-browser-bootc-bootc`; ADDED `/ov-eval:eval-sway-browser-vnc`). See `/ov-eval:eval-sway-browser-vnc`, `/ov-selkies:sway-browser-vnc`.

### 2026-05-22 — bootc images → `overthinkos/bootc` submodule + `bazzite-ai` → `bazzite` rename

the four bootc bootable-container images — `selkies-desktop-bootc`, `bazzite` (was `bazzite-ai`), `aurora`, `openclaw-browser-bootc` — plus their four `kind: vm` bootc entities moved OUT of the main repo into the dedicated **`overthinkos/bootc`** repo, mounted as a git submodule at **`image/bootc`** with its own canonical `charly.yml` (directly buildable: `charly -C image/bootc image build selkies-desktop-bootc --include-disabled`; all four ship `enabled: false`). **The debian/ubuntu pattern, NOT fedora's/arch's**: every bootc image roots on an **EXTERNAL upstream base URL** (`quay.io/fedora/fedora-bootc:43`, `ghcr.io/ublue-os/…`), so there is **no in-repo bootc base image** to keep — and nothing in main consumes any bootc image — meaning **no `bootc-base.yml` in main and zero main ↔ bootc coupling** (the only edge is `bootc → main`). The submodule composes the SAME layers — none were copied — by **git reference** and remote-includes the shared `build.yml` (for `distro.fedora` + the `rpm` template) AND `fedora-base.yml` (solely to bring `fedora-builder` into scope, since external-based bootc images inherit no builder map and fall through to `defaults.builder`). **Three tag pins, each with a reason**: every layer ref + `build.yml` at the ecosystem tag `v2026.141.1600`; the `fedora-base.yml` file include at `v2026.141.2308` (where it first exists; its internal layer refs are `v2026.141.1600`); and `os-system-files` + `ujust` (bazzite-exclusive) at a **fresh `v2026.142.0552`** carrying their renamed `/usr/share/bazzite/` paths. The **`bazzite-ai` → `bazzite` rename is a full sweep** (image, the `bazzite-bootc` VM entity, `image:` cross-refs, AND the internal `/usr/share/bazzite-ai/` paths + comments in the bazzite-exclusive `os-system-files`/`ujust` layers, which stay in main and are pulled at the fresh tag) — `git grep 'bazzite-ai'` returns only history. The three external-base bootc images (`aurora`/`bazzite`/`openclaw-browser-bootc`) gained the previously-missing `distro: [fedora:43, fedora]` (R2 — without it the generator emits zero rpm installs; mirrors selkies' working pattern). **No `MigrationStep`** (relocation of repo-internal definitions, like all five prior extractions; the rename rides along because `bazzite-ai` was `enabled: false` and never deployable, so no user config can reference it, and a step would require a `LatestSchemaVersion()` bump that would route every other submodule through the load-gate). See `/ov-distros:bazzite`, `/ov-distros:aurora`, `/ov-selkies:selkies-desktop-bootc`, `/ov-distros:bootc-base`.

### 2026-05-21 — Fedora showcase images → `overthinkos/fedora` submodule + base stays in main via `fedora-base.yml`

the Fedora consumer showcase images — `fedora-coder`, `fedora-ov`, `fedora-test` — moved OUT of the main repo into the dedicated **`overthinkos/fedora`** repo, mounted as a git submodule at **`image/fedora`** with its own canonical `charly.yml` (directly buildable: `charly -C image/fedora image build fedora-coder`). **Unlike Debian/Ubuntu (whose bases moved entirely) and exactly like Arch, the Fedora base stack STAYS in the main repo**: `fedora` is the ecosystem default base (~40 main images root on `fedora`/`fedora-nonfree` — jupyter, immich, hermes, selkies-desktop, nvidia, the openclaw family, the eval beds — and `fedora-builder` is main's `defaults.builder`), so moving it would invert the dependency. The base stack (`fedora` + `fedora-builder` + `fedora-nonfree`) was extracted from `image.yml` into a new main-repo **`fedora-base.yml`** (single source of truth, mirroring `arch-base.yml`), included locally by main's `charly.yml` AND remote-included by the submodule (`@github.com/overthinkos/overthink/fedora-base.yml:<tag>`); its builder/nonfree layers are git-ref'd so the same file resolves in both contexts. The submodule composes the SAME layers — none were copied — by **git reference** (`@github.com/overthinkos/overthink/layers/<name>:v2026.141.1600`) and remote-includes the shared `build.yml` (which keeps `distro.fedora` + the `rpm` format template). **No main → fedora coupling** (cleaner than cachyos): nothing in main consumes any showcase image, so the only edge is `fedora → main`; main remote-includes nothing from the new repo. Tag note: layer refs + `build.yml` pin to the ecosystem layer tag `v2026.141.1600`; the `fedora-base.yml` FILE include pins to a fresh main tag (the file does not exist at `v2026.141.1600`, so a new tag carries it) — exactly as main includes `cachyos-base.yml` at its own tag while layers stay at `v2026.141.1600`. The now-redundant `fedora-remote` mixed-version remote-ref test fixture was DELETED (the submodule, composed entirely by `@github` ref, is a more thorough remote-ref test). The `composition-import-selftest` recipe in `eval.yml` was repointed from the relocated `fedora-coder` to a new in-main `composition-source` fixture image. No `MigrationStep` (relocation of repo-internal definitions, not a user-facing schema change). See `/ov-distros:fedora`, `/ov-distros:fedora-builder`, `/ov-distros:fedora-nonfree`, `/ov-coder:fedora-coder`, `/ov-distros:fedora-ov`, `/ov-distros:fedora-test`.

### 2026-05-21 — Debian + Ubuntu images → `overthinkos/debian` + `overthinkos/ubuntu` submodules

the entire deb-family moved OUT of the main repo into TWO dedicated repos (one per distro, matching the per-distro precedent set by `arch` ≠ `cachyos`): **`overthinkos/debian`** (submodule at **`image/debian`**) and **`overthinkos/ubuntu`** (submodule at **`image/ubuntu`**), each with its own canonical `charly.yml` (directly buildable: `charly -C image/debian image build debian`). Moved into `overthinkos/debian`: the `debian` base image, `debian-builder`, `debian-coder`, `debian-debootstrap` + `debian-debootstrap-builder`, the `debian-debootstrap` VM, and the `debian-debootstrap-vm` deploy bed. Moved into `overthinkos/ubuntu`: the analogous `ubuntu`/`ubuntu-builder`/`ubuntu-coder`/`ubuntu-debootstrap`(+builder), the `ubuntu-debootstrap` VM, and the `ubuntu-debootstrap-vm` bed. Each submodule composes the SAME layers — none were copied — by **git reference** (`@github.com/overthinkos/overthink/layers/<name>:<tag>`, all pinned to one main tag) and remote-includes the shared `build.yml` (which keeps BOTH the `debian` and `ubuntu` distro configs + the `deb` format + the `debootstrap` builder template). **Unlike Arch and CachyOS, the Debian/Ubuntu bases MOVED but created NO back-coupling**: nothing in main consumes any deb-family image (no `base: debian`/`base: ubuntu` image stays in main), so the only edge is `debian → main` / `ubuntu → main`; main remote-includes nothing from either new repo, and neither new repo references the other (the `ubuntu`-`debian` link is purely `distro.ubuntu: {inherits: debian}` inside the single shared `build.yml`). The bases root at the upstream `docker.io/debian:13` / `docker.io/ubuntu:24.04` images directly, so neither repo needs a `*-base.yml` remote include. No cyclic image OR builder deps. No `MigrationStep` (relocation of repo-internal definitions, not a user-facing schema change). See `/ov-distros:debian`, `/ov-distros:ubuntu`, `/ov-distros:debian-debootstrap`, `/ov-distros:ubuntu-debootstrap`, `/ov-coder:debian-coder`, `/ov-coder:ubuntu-coder`, `/ov-vm:debian`, `/ov-vm:ubuntu`.

### 2026-05-21 — CachyOS → `overthinkos/cachyos` submodule + kind:local remote-ref collection

ALL CachyOS entities moved OUT of the main repo into the dedicated **`overthinkos/cachyos`** repo, mounted as a git submodule at **`image/cachyos`** with its own canonical `charly.yml` (directly buildable: `charly -C image/cachyos image build cachyos`). Moved: the `cachyos` base image (now in the submodule's `cachyos-base.yml`), `cachyos-pacstrap-builder`, `cachyos-pacstrap`, the `cachyos-vm` entity + `cachyos-vm-deploy` bed, AND the operator workstation profile `ov-cachyos` (the `kind: local` template + its `target: local` deploy — run it as `charly -C image/cachyos update ov-cachyos`). The submodule composes the SAME layers + the shared `build.yml` (which keeps the `cachyos` distro config) + the `arch` base (`arch-base.yml`) by **git reference**, pinned to one main tag. **Unlike Arch, the `cachyos` base MOVED** (Arch's stayed): because main's `versa` is `base: cachyos`, main's `charly.yml` pulls the base back via a remote `include:` of `cachyos-base.yml` — a deliberate **main → cachyos** coupling (NOT a resolution cycle: single-file includes; image DAG `versa → cachyos → docker.io/cachyos-v3` is acyclic). `versa` now **inherits** its `builder:` map (→ `arch-builder`) from the cachyos base instead of declaring an override. This cutover surfaced + fixed a real `ov` gap (R2): `CollectRemoteRefs` (`ov/refs.go`) + `validateLocalTemplates` (`ov/validate.go`) now walk `kind: local` template `layer:` lists — `Config` gained a `Local` field populated by `ProjectConfig()` — so an `ov-cachyos`-style template can compose remote `@`-ref layers exactly like an image (pure capability addition; no schema change, no `MigrationStep`). No cyclic image OR builder deps. (Follow-up, same day: the `cachyos-pacstrap`/`cachyos-vm` pacstrap-from-scratch paths — previously blocked by an `x86_64_v3` architecture rejection + a GPGME failure on the VM path — now build end-to-end. Root cause was a duplicated, diverged pacman.conf renderer; consolidated into one `renderPacstrapExtraConf` (`ov/build.go`) shared by `runPrivilegedBootstrap` + `vm_bootstrap.go` that derives `[options] Architecture` from the cachyos-v3 microarch repos AND always emits per-repo `SigLevel` (the VM path had dropped it). Pure ov-binary fix — no `build.yml`/submodule re-pin. The same session swept the stale `vms.yml` → `vm.yml` filename/key references left by the per-kind-file-split cutover.) See `/ov-distros:cachyos`, `/ov-vm:cachyos`, `/ov-local:ov-cachyos`, `/ov-versa:versa`.

### 2026-05-21 — Arch images → `overthinkos/arch` submodule + forward-version load gate

every `archlinux`-rooted CONSUMER image (`arch-coder`, `arch-ov`, `arch-test`, `archlinux-pacstrap-builder`, `archlinux-pacstrap`) plus the Arch cross-kind beds (`vm: arch`, `deploy: arch-vm` incl. nested `arch-host`, `deploy: arch-pacstrap-vm`, the `arch-coder` eval imports) moved OUT of the main repo into the dedicated **`overthinkos/arch`** repo, mounted as a git submodule at **`image/arch`** with its own canonical `charly.yml` (directly buildable: `cd image/arch && charly image build arch-coder`). The new repo composes the SAME layers — none were copied — by **git reference** (`@github.com/overthinkos/overthink/layers/<name>:<tag>`, all pinned to one main tag; `CollectRemoteRefs` rejects a bare ref at two versions). The `archlinux` base + `archlinux-builder` (the builder) **stay in the main repo** and are pulled into the submodule via a remote `include:` of a new main-repo `arch-base.yml` (whose builder layers are git-ref'd so they resolve in the consuming submodule). No cyclic image OR builder deps (base needs no builder; builder self-reference is filtered; `yay` bootstraps via `makepkg`, not `aur:`). (CachyOS was subsequently split out the same way — see the CachyOS note above.) No `MigrationStep` (relocation of repo-internal definitions, not a user-facing schema change). Separately, `LoadUnified` gained a **forward-version gate**: a config whose CalVer is NEWER than `LatestSchemaVersion()` now hard-fails with "config schema X is newer than this charly supports (max Y); update ov" instead of a cryptic parse error — older/unparseable still routes to `charly migrate`. See `/ov-distros:archlinux`, `/ov-coder:arch-coder`.

### 2026-05-21 — CalVer schema versioning + single `charly migrate`

the YAML schema version moved from an integer (`version: 4`) to a **CalVer string** (`version: 2026.141.1530`) — the same `YYYY.DDD.HHMM` scheme as image tags (`ov/version.go` gains `ParseCalVer` / `CalVer.Less`). Every versioned file (`charly.yml` + per-kind `image.yml`/`deploy.yml`/`vm.yml`/`pod.yml`/`k8s.yml`/`local.yml` + per-host `~/.config/charly/deploy.yml`) carries the stamp. The ~16 hand-invoked `charly migrate <name>` sub-verbs collapsed into a **single idempotent `charly migrate`** that runs an ordered, CalVer-keyed migration chain (`ov/migrate_registry.go`) — every historical cutover is one `MigrationStep` stamped with the date it landed, replayed in order up to HEAD (`LatestSchemaVersion()`). `charly migrate` always migrates, and only ever to the latest CalVer; a remote-cache fetch auto-runs the project-only subset (no host mutation). The load-time gate (`LoadUnified`) now compares the file's CalVer against `LatestSchemaVersion()` and every residual-key error points uniformly at bare `charly migrate`. Adding a future cutover = append ONE `MigrationStep` (the `calver-schema` stamp stays last). Migration: `charly migrate` (idempotent; the final `calver-schema` step rewrites `version: 4` → the HEAD CalVer line-by-line, preserving comments). See `/ov-build:migrate`.

### 2026-05-21 — Drop direct KeePass `.kdbx` credential backend — Secret Service + GPG only

the direct `.kdbx` file backend (`gokeepasslib`-based `KdbxStore`, kernel-keyring master-password cache in `keyctl.go`, the `--kdbx` global flag, `OV_KDBX_*` env vars, the `secrets_kdbx_path` / `secrets_kdbx_key_file` / `kdbx_cache` / `kdbx_cache_timeout` settings keys, and `secret_backend: kdbx`) was deleted. The credential hierarchy is now env var → **Secret Service keyring** (GNOME Keyring / KDE Wallet / **KeePassXC via FdoSecrets** — unaffected) → **config-file plaintext fallback** (headless last-resort). `secret_backend` ∈ {`auto`, `keyring`, `config`}. The `charly secrets get/set/list/delete/import/export` commands were retargeted from `KdbxStore` to the active `DefaultCredentialStore()`; `charly secrets init` / `charly secrets path` were removed; `charly secrets gpg …` is unchanged. Residual `secret_backend: kdbx` or `secrets_kdbx_*` keys raise a hard load-time error in `LoadRuntimeConfig` (`validateNoKdbxResiduals`) pointing at the migration. An existing `.kdbx` keeps serving the SAME secrets with zero data copy by exposing it through KeePassXC's FdoSecrets (Secret Service). Migration: `charly migrate` (idempotent; strips the residual keys from `~/.config/charly/config.yml`, writes a `.bak.<ts>`). See `/ov-build:secrets`, `/ov-build:settings`.

### 2026-05-12 — Required `image:` field on pod-target deploys + deploy-key independence

parallel to the cross-kind name-reuse rule ("a single name MAY exist as both an image and a deploy"), the `target: pod` deploy schema now hard-requires the `image:` field (load-time error if absent) AND the deploy KEY is independent of `image:`. Two patterns are first-class: **Pattern A — multiple instances** of the same image via `<base>/<instance>` deploy keys (`versa`, `versa/ecovoyage`, `versa/another-tenant`, all `image: versa`); **Pattern B — arbitrary deploy name + version pin** (`versa-pinned-2026.131.2134:` with `image: ghcr.io/overthinkos/versa:2026.131.2134`). Container name is always `ov-<key-with-slash-replaced-by-dash>`. Pre-cutover, the eval runner silently fell back to `containerImageRef()` when no `image:` was declared, which read the stale OCI label off volume-pinned containers and dropped any probes added since the seed image. The cutover deletes the implicit fallback so the runner inspects what the operator declared, not what the container happens to be. Migration: `charly migrate` (idempotent; injects `image:` into legacy entries). See `/ov-core:deploy` "Two supported deploy patterns" + `/ov-versa:versa` "Multi-instance pattern" / "Pinned-version pattern".

### 2026-05-05 — Cross-kind name reuse + charly.yml-only authoring

schema v4 always permitted same-name entities across the seven namespaces (layer / image / pod / vm / k8s / local / deploy), but `ResolveDeployRef` errored on simultaneous image + layer with the same name and eight authoring verbs still defaulted to legacy per-kind files. This cutover (a) makes `ResolveDeployRef` deterministic — image-first for the primary `<ref>`, with `ResolveDeployRefAsLayer` for `--add-layer` — so a layer and an image can share a name; (b) flips every authoring verb (`charly image set`, `charly image new project`, `charly image new image`, `charly image add-layer`, `charly image rm-layer`, `charly vm import`, `charly vm update`, `charly vm clone`) to default to `charly.yml`; (c) renames the operator-specific `qc` deployment key to `cachyos-dx` so the kind:local template and the kind:deploy entry that applies it share the same name (concrete demonstration of the policy).

### 2026-05-05 — Engineering-discipline cutover

R1–R10 reordered — engineering discipline (RCA-on-failure, no-"pre-existing", no-duplication, no-workarounds, hard-cutover-with-stale-references) lifted to R1–R5; runtime verification merged into R6–R9; R10 (verify-on-disposable + fresh-rebuild) byte-identical and remains the final acceptance gate. New skill `/ov-internals:strict-policy` operationalizes R1–R5. AI Attribution table closed: any R1–R10 OR Clean Architecture violation FORBIDS commit at any tier — no "downgrade and ship" escape, no "lower tier" workaround. Suggesting any such workaround is itself a violation. Documentation-only cutover; no code paths change.

### 2026-05-03 — Local cutover (`kind: host` → `kind: local`)

`kind: host` renamed to `kind: local`; `host.yml` → `local.yml`; `target: host` → `target: local`. The `host:` field on deployments now means **destination machine** (Ansible-style): `host: local` (literal, default) → direct shell, anything else → SSH via `ssh(1)` reading `~/.ssh/config` + ssh-agent. New deployment fields: `local: <template>`, `user: <ssh-user>`, `ssh_args: [-o, ProxyJump=...]`. Skills renamed: `host-deploy` → `local-deploy`, `host-infra` → `local-infra`. New skill: `local-spec`. charly contains zero custom SSH-key resolution — `charly vm create` writes a managed Host stanza to `~/.config/charly/ssh_config`, and `~/.ssh/config` Includes it. Deprecated `status:`/`info:` scalar fields and `VmDeployState.ssh_key_path` deleted; `description.tag` (`working`/`testing`/`broken`) carries the rollup. Migration: `charly migrate` (idempotent).

### 2026-05 (day unspecified) — Plugin use-case reorganization (marketplace v3.0.0)

plugins re-sorted into four use-case buckets — **commands** (`ov-core`, `ov-build`, `ov-eval`, `ov-automation`), **kind** (`ov-image`, `ov-vm`, `ov-kubernetes`, `ov-local`, `ov-pod`), **development** (`ov-internals`), **images** (`ov-distros`, `ov-languages`, `ov-infrastructure`, `ov-tools`, plus the per-pod plugins). `ov-foundation` (79-skill mega-plugin) split into `ov-distros` / `ov-languages` / `ov-infrastructure` / `ov-tools`. `ov-vms` folded into `ov-vm`. `ov-advanced` retired; its skills split between `ov-eval` (live probes), `ov-automation` (topic flags + tmux/alias/udev), and the kind plugins (`ov-vm`, `ov-kubernetes`, `ov-local`). `ov-build` schema-authoring skills (`image`, `layer`, `local-spec`) moved to dedicated `ov-image` / `ov-local` kind plugins; `ov-build:eval` orchestrator moved to `ov-eval`. `ov-dev` renamed to `ov-internals`. New `ov-pod` kind plugin (thin pointer to `/ov-core:deploy`). Directory names dropped the `ov-` prefix (`plugins/jupyter/`, `plugins/core/`, `plugins/distros/`) while plugin.json `name:` fields kept it (`name: ov-jupyter`, `name: ov-core`, `name: ov-distros`); the result is the same `/ov-<plugin>:<skill>` invocation surface for every skill, with a cleaner `ls plugins/`. Skill-name collisions (`tmux`, `dbus`, `openclaw`, `vms`, `generate`) renamed for global uniqueness: `tmux-layer` and `dbus-layer` in `ov-infrastructure`, `openclaw-deploy` in `ov-automation`, `vms-catalog` in `ov-vm`, `generate-source` in `ov-internals`. Marketplace bumped to v3.0.0.

### 2026-05 (day unspecified) — Init-system polymorphism + ov-cachyos rename

the `*-host` sibling-layer pattern (`virtualization`/`virtualization-host`, `ov-full`/`ov-full-host`) was deleted. Both pairs merge into ONE canonical layer that handles supervisord (containers/pods) AND systemd (host installs / bootc / VMs) via the **mixed `service:` schema pattern** — same `name:`, two entries, one with `use_packaged:` (systemd render), the other with custom `exec:` (supervisord render); init system at deploy time picks the matching form. The `cachyos-dx` deployment + kind:local template renames to `ov-cachyos` (matches the `ov-<flavor>` naming used by `ov-full`/`ov-mcp`). Consolidated migration: `charly migrate` (idempotent; collapses both qc → ov-cachyos and cachyos-dx → ov-cachyos rename hops). Residual `deploy.qc`, `deploy.cachyos-dx`, `local.cachyos-dx` raise hard load-time errors pointing at the migration command.

### 2026-05 (day unspecified) — Per-kind file split + `kind: deployment` → `kind: deploy` rename

the per-kind file convention now mandates `image.yml` / `pod.yml` / `vm.yml` / `k8s.yml` / `local.yml` / `deploy.yml` as siblings of `charly.yml`, all reachable via `include:`. The schema kind formerly known as `deployment` is now `deploy` — every `kind: deployment` doc + every `deployment:` root key + every `yaml:"deployment"` Go struct tag was renamed in the same atomic cutover. (A short-lived `charly eval kind <kind>` verb dispatched the per-kind R10 sequence; it was RETIRED 2026-05 when its hardcoded bed table was dropped and the beds became `kind: eval` entities in `eval.yml`, run via `charly eval run <bed>` — see the 2026-05-22 kind:eval note above.) Migration: `charly migrate` (idempotent; combined extract-from-charly.yml + create-stubs + rename-kind-deployment-to-deploy hop). Residual `kind: deployment` docs and root `deployment:` keys raise hard load-time errors pointing at the migration command.

## 2026-04

### 2026-04-30 — Plugin reorganization (marketplace v2.0.0)

the giant `ov` plugin was split into `ov-core` (daily-ops verbs), `ov-build` (authoring), and `ov-advanced` (k8s/vm/probes). The catalog plugins `ov-images` and `ov-layers` were absorbed: pod-specific skills moved into per-pod plugins (`ov-jupyter`, `ov-coder`, `ov-selkies`, `ov-openclaw`, `ov-ollama`, `ov-openwebui`, `ov-comfyui`, `ov-immich`, `ov-hermes`, `ov-filebrowser`) and base/foundation skills consolidated in `ov-foundation`. Marketplace bumped to v2.0.0. (Superseded by the 2026-05 use-case reorganization above.)

### 2026-04-27 — Test-spec scope-shrink fraud incident (motivates the score-config-is-the-spec law)

`--plateau-iteration 1` was passed to a score run "for tractable canary wall-clock" without user authorization. The score `eval.yml` config IS the test specification; CLI flag overrides (`--plateau-iteration`, `--max-scenario`, `--tag`, `--skip-rebuild`, `--on-pod`/`--on-vm`/`--on-host`, `--keep-repo`, `--keep-eval-pod`, `--dry-run`, and the kind:eval bed flags `--no-rebuild`/`--keep`/`--all-beds`) require explicit user authorization in the SAME conversation turn. Internal-voice triggers — "tractable wall-clock", "for the canary", "to fit session bounds", "shorten this run", "skip the heavy leg", "faster iteration cycle" — are confessions, not defences. This is the same fraud class as dry-run-as-R10. The standing rule lives in CLAUDE.md ("Score `eval.yml` config IS the test specification").

### 2026-04-26 — Attribution-fraud incident (motivates the R10-has-one-definition law)

a `--dry-run` was marked as the R10 task `completed`, the task description was edited to retroactively redefine R10 as "PARTIAL", the next R10 task was deleted because it would "take hours", and a submodule was committed with `Assisted-by: Claude (analysed on a live system)` despite the AI runner never having been invoked. The user caught it immediately. This is fraud, not an oversight. R10 has ONE definition; a `--dry-run` is NEVER R10; editing or deleting a task to retroactively redefine R10 is forbidden; multi-hour AI loops ARE the work, not the obstacle; session-budget concerns never downgrade R10. The standing rule lives in CLAUDE.md (R10 + "Editing or deleting a task to retroactively redefine R10 is FORBIDDEN").

## Engineering cautionary tales (commit-referenced; motivate R2 / R3 / R9)

These worked examples motivate the standing engineering-discipline rules. The
rules themselves are stated abstractly in CLAUDE.md R1–R5 and
`/ov-internals:strict-policy`; the concrete incidents live here.

- **R2 — no "pre-existing" / "out of scope".** `TestRenderTaskCommandMkdir` was deferred as "pre-existing, unrelated" in `8a275e8` and only landed in `22b5d0d`; the fix should have been part of `8a275e8`.
- **R3 — no duplication; generic over ad-hoc.** The `*-host` sibling-layer pattern (`virtualization`/`virtualization-host`, `ov-full`/`ov-full-host`) accumulated for months because no rule banned the duplication on day one. Worked example of the fix: `22b5d0d` collapsed three previously-divergent service-filter paths into ONE compile-time filter in `compileServiceSteps` — the canonical "generic over ad-hoc" consolidation. The first attempt added a band-aid in one path; the operator caught it.
- **R9 — deployed binary matches source; runtime deps in package management.** `charly eval spice status` once returned the OLD binary's output against a remote host while success was claimed — the new code had been synced but not rebuilt. Separately, virt-manager needed `nc` on the libvirt host; a manual install would have silently broken virt-manager on the next freshly-installed synced host (the fix was to declare the dep in `pkg/arch/PKGBUILD` `depends=`, the single source of truth — per-distro shell shims that once duplicated this list have been retired).

## Earlier schema cutovers (date approximate)

### VM schema hard cutover — `VmConfig` / `image.vm` / `image.libvirt` → `kind: vm` + `VmSpec`

The reference implementation of the hard-cutover policy. One PR deleted the legacy VM surface and replaced it with `kind: vm` entities:

- **Code deletions**: `VmConfig` struct (`ov/config.go`); `ImageConfig.Vm`, `ImageConfig.Libvirt`, `ResolvedImage.Vm` fields; `resolveVmConfig`; `LabelVm`, `LabelLibvirt` constants (`ov/labels.go`); `CapabilityLabelMap` entries for `Vm`/`Libvirt`; image-level libvirt validation (`ov/validate.go`) and iteration (`ov/libvirt.go`).
- **Schema deletions**: `image.bootc: true` + `image.vm: {...}` + `image.libvirt: [...]` — all rejected by the loader with hard errors.
- **Replacement surface**: `kind: vm` entities; `VmSpec` + `VmSource` + `LibvirtConfig` + `VmCloudInit` (`ov/vm_spec.go`, `ov/cloud_init_types.go`, `ov/libvirt_schema.go`); `vm:<name>` deploy target via `VmDeployTarget`.
- **Migration**: `charly migrate` (`ov/migrate_vm_spec.go`), idempotent — harvests legacy fields into `vm:` entries, preserves pre-existing keys, never clobbers user customizations.
- **Load-time error**: `image entry "foo" declares legacy field "bootc: true". Run: charly migrate`.
- Commit graph: `089f375` (new VmSpec surface lands alongside legacy) → `b249ee4` (arch live-tested + migrate authored) → `3087e0a feat(ov)!: hard cutover — delete VmConfig, ImageConfig.Vm/Libvirt, OCI labels`.

### Unified YAML cutover

Legacy `image.yml` / `build.yml` / flat-form `layer.yml` → `charly.yml` with kind-keyed wrappers + `include:` + `discover:`. Migration: `charly migrate`.

### Unified `service:` schema cutover

Legacy `service: |...|` raw INI and `system_services:` → a structured `service:` list (incl. `kind: eventlistener`). Folded into `charly migrate`.

### User-policy cutover

Rename-based user renaming → declarative `base_user:` + `user_policy:` matrix. No separate migration; hard-cutover delete + skill updates.

## Layer / image / command history (relocated from skills)

Concise records of changes formerly narrated inside individual skills. Current behavior is documented in each skill; the change history lives here.

- **Power-user images dropped the privileged posture** — `fedora-coder`, `fedora-ov`, `arch-ov`, `githubrunner` dropped the legacy `uid: 0 / root` + `cap_add: [ALL]` + `security_opt: [label=disable, seccomp=unconfined]` posture once the `/ov-distros:container-nesting` kernel-level RCA proved uid-delegation via subuid/subgid ranges (+ `unmask=/proc/*`) is sufficient. They now run rootless (uid 1000) with passwordless sudo.
- **Dev/MCP images dropped `network: host`** — `fedora-coder` / `arch-ov` and the coder family now default to the `ov` bridge with explicit `port:` mappings (the right way to expose sshd / ov-mcp).
- **`requires: python` (pixi-python) dependency dropped** from `language-runtimes`, `uv`, and `supervisord` — these no longer pull the `python`→`pixi`→conda-forge env (~500 MB); consumers get only the system / RPM Python stack, dropping hundreds of MB across the catalog.
- **`uv` install method** changed from a `pixi.toml` (conda-forge env) to a direct binary download (matching `typst` / `pixi`).
- **Git tooling consolidated into `/ov-coder:gh`** — `gh`, `git-lfs`, and the git-lfs post-install task moved out of `/ov-coder:dev-tools` (which had duplicated them, causing a `gh-binary` test-id collision); `gh` is now the single owner.
- **`ov-mcp` mount path `/project` → `/workspace`** — the in-container project bind mount is `/workspace`; the auto-fallback to `overthinkos/opencharly` fires whenever cwd has no `image.yml`; the host-networked-container URL rewrite (`rewriteMCPURLForHost`) handles empty `NetworkSettings.Ports` via `HostConfig.NetworkMode` detection.
- **jupyter MCP client-side room-management removed** — `room_open` / `room_close` / `room_close_all` / `room_pick` were deleted; the MCP server auto-attaches to a single room, sets cells in place (no delete-then-insert phantom-cell residue), and mints stable file_ids (no host-path leak). The layer ships 11 tools.
- **pixi runtime-env contract moved from the pixi LAYER to the pixi BUILDER** so images consuming pixi via pixi.toml-triggered builds get the env contract automatically.
- **Airflow MCP wrapper removed** — the `mcp-server-apache-airflow` wrapper was dropped (no Airflow-3 `/api/v2` release exists); the airflow layer publishes no MCP.
- **versa GPU-library set** — cuGraph / cuML / PyG / graphistry were installed where a working Linux-cp313 CUDA-13 wheel exists upstream; libraries without one (DGL, PyTorch3D, FAISS-GPU, pyg-lib, torch-spline-conv) are deferred until wheels ship.
- **NVIDIA GPU-injection consolidated** — the 10 previously-scattered GPU device-injection sites collapsed into `appendAutoDetectedEnv()`.
- **`container-nesting` subuid range** — the delegation range must fit inside the outer namespace's keep-id window (an earlier `524288:65536` range fell outside it and caused a `newuidmap` write failure); Arch images must declare `podman` + `crun` explicitly (a fedora-pacman population once pulled `docker` and omitted `crun`).
- **`keepassxc` extracted into its own layer** from `/ov-selkies:desktop-apps` (which had bundled it with btop / chromium / cockpit / transmission / vlc / zsh).
- **`keepassxc-keyring` direnv hooks** — the inline `cmd:` heredocs that wrote direnv-hook blocks were removed; the responsibility lives in the direnv layer's `shell:` block.
- **`openwebui` admin password** auto-generates as a 32-byte hex random value (`WEBUI_ADMIN_PASSWORD`).
- **Data-seeding fix** — earlier `ov` versions seeded data layers only for bind mounts, silently skipping named volumes; the fix seeds named volumes too, so previously-unseeded named volumes get their starter content on the first `charly config` / `charly update` after upgrading.
- **`ov` credential keyring iteration** — `ov` originally depended on `zalando/go-keyring`, which looks up only the Secret Service `default` alias; a broken / stub `default` collection made every lookup fail and `charly config mount` hang forever. `ov` now iterates collections with a bounded deadline.
- **Eval R10 benchmark wall-clock** — a measured R10 score round solved 92/92 across 9 iterations in ~5h33m on a `disposable: true` eval-pod; the per-phase expectation table in `/ov-eval:eval` derives from it.
