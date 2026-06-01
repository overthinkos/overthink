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

### 2026-06-01 — `ov eval` in-container `command:` stdin guard + first-class `adb` UI readiness verbs

Two `ov eval` framework defects surfaced while hardening the
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

### 2026-05-31 — unified `ov status`: one table across pod / vm / k8s / local / android

`ov status` became the **unified deployment-status surface**: a single table
(or JSON array, or single-deployment detail view) showing every ov deployment
across all five substrates side by side, with a leading **KIND** column /
`"kind"` JSON field discriminating which substrate each row came from. Before
this cutover `ov status` was pod-only — it did one batched `podman ps` +
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
  `ov status` on a podman-only host shows the pod rows and silently omits the
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
  `applyNestedOverlay` reads the declared tree (project `overthink.yml` incl.
  folded `kind: eval` beds + `~/.config/ov/deploy.yml`) and attaches each
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
  `NestedExecutor` primitive `ov deploy add` / `ov eval live parent.child` use
  (R3 — no bespoke nested dial), under a strict 4-second per-child context
  deadline (a deadline, never a sleep/retry — R4).
- **Proof-of-functionality eval coverage.** Each of the four core `kind: eval`
  beds gained a `status-shows-*` deploy-scope check that greps host-side
  `ov status --json` for the substrate it exercises: `eval-pod` →
  `status-shows-pod` (`"kind": "pod"` + `ov-eval-pod`); `eval-k3s-vm` →
  `status-shows-vm` (`"kind": "vm"` + `eval-k3s-vm`); `eval-local` →
  `status-shows-local` (`"kind": "local"`); `eval-android-emulator-pod` →
  `status-shows-android-nested` (`"kind": "android"` + the `"nested"` tree). A
  `verify-status` dynamic workflow (`.claude/workflows/verify-status.js`,
  modeled on `verify-beds.js`) emits the substrate→bed map and fans
  `ov eval run <bed>` out in parallel, aggregating the verbatim
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
- **Validator guard (R3 — prevents recurrence).** `ov image validate` now rejects
  a lowercase `${...}` token in the k8s / resource-identity eval fields (cluster,
  name, namespace, label, kubeconfig, k8s_context, k8s_resource, k8s_group,
  k8s_version, manifest) — the class of bug that previously passed both validate
  AND runtime. Scoped to CLI-arg identifier fields, so shell `command:` bodies
  (legitimate bash vars) and cdp `expression:` (JS template literals) are
  untouched.
- Skill `eval-k8s` example updated to `${DEPLOY_NAME}` + an explanatory note; Go
  tests added (the validator guard, `DEPLOY_NAME` seeding + sanitization, the
  runtime-only classification).

R10: `ov eval run eval-k3s-vm` → exit 0, **20/20** (was 16/20), the full sequence
including the fresh `ov update` re-verification gate.

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
  `ov eval run <bed>` on a real deployment; the lead owns the single atomic
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
image / schema changes, so no `ov migrate` and no `LatestSchemaVersion()` bump.

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
  capture backend (needs an X11 root window — ov is Wayland-only) plus PipeWire
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
- **R10.** `ov -C image/cachyos eval run eval-cachyos-gpu-vm` PASS on the real
  RTX 4080 SUPER (in-guest `lspci`: `AD103 [GeForce RTX 4080 SUPER] [10de:2702]`
  + its `[10de:22bb]` audio function; domain hostdev count 2) — 33/0/0 on both the
  eval-live and the fresh-rebuild legs. A portable confirmatory re-run on the
  FINAL code (with the gate) also passed 33/0/0 on both legs with 0 skipped — the
  gate is a clean no-op on the SPICE-having bed. The operator `cachyos-gpu` was
  recreated from clean and verified live (RTX heads `enabled`/`dpms=On`,
  `ov-display-keepalive` running, locker disabled, KDE panels present on every
  head, Looking Glass gone, selkies streaming) — `ov eval live cachyos-gpu` 28/0
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

- **ov (`VM_HOSTDEV_COUNT`).** VM live-eval (`ov/eval_cmd.go`) now resolves
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
- **R10.** `ov -C image/cachyos eval run eval-cachyos-gpu-vm` PASS (31 passed,
  gate `✓`, the real RTX 4080 SUPER branch — `nvidia-smi -L` / `cuda-smoke` /
  NVENC) with the host's hostdev attached, including the fresh-rebuild
  re-verification; `ov eval live cachyos-gpu` PASS (32 passed, gate `✓`) on the
  persistent operator VM (`nvidia-smi` in-guest: RTX 4080 SUPER, driver
  610.43.02). The host-specific `<hostdev>` is added locally and reverted after
  the run — never committed (the committed bed stays portable).

### 2026-05-30 — ubuntu repo (overthinkos/ubuntu): schema migrate + eval-*-vm bed naming + VM host-port deconfliction

- **Migrated to schema 2026.144.1443** (`ov migrate`: kind-files split +
  entity-version backfill + calver stamp).
- **Disposable bed renamed** `ubuntu-debootstrap-vm` →
  `eval-ubuntu-debootstrap-vm`. R5 sweep across `overthink.yml` / README + the
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
- R10: `ov -C image/ubuntu eval run eval-ubuntu-debootstrap-vm` → PASS (steps=6),
  plus the debian bed re-verified PASS (steps=6) on the bumped pin. `/dev/kvm`
  stayed `0666 kvm` throughout both (audit: zero `systemd-tmpfiles` host-node
  hits), zero warnings, operator undisturbed. Landed image/ubuntu
  `v2026.150.1931`, image/debian `v2026.150.1931`, plugins `bb14bdc`.

### 2026-05-30 — debootstrap chroot corrupts host /dev/kvm (build.yml fix)

- **Symptom.** Every `ov vm build` of a debian/ubuntu debootstrap VM
  intermittently broke KVM on the host, surfacing later as `ov vm create`
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
  `ov -C image/ubuntu eval run eval-ubuntu-debootstrap-vm` bed R10.
- **Recovery (if already corrupted).** Restore `/dev/kvm` to `0666 kvm` AND
  restart the stale `virtqemud` so it re-probes — the perm-restore alone is
  insufficient because the daemon caches the no-kvm verdict in memory
  (`--timeout=120`, no on-disk capabilities cache).

### 2026-05-30 — debian repo (overthinkos/debian): schema migrate + standard eval-*-vm bed naming

- **Migrated to schema 2026.144.1443** (`ov migrate`: kind-files split inline
  image/vm/pod/k8s into siblings + entity-version backfill + calver stamp).
- **Disposable bed renamed to the standard `eval-<descriptor>-<kind>` form:**
  `debian-debootstrap-vm` → `eval-debian-debootstrap-vm`. R5 sweep across the
  debian repo (overthink.yml / README) + the `/ov-vm:debian` & `/ov-distros:debian`
  plugins skills.
- **`version:` backfilled on the layerless `debian-debootstrap`
  (`from: builder:debootstrap`) and the bare-base `debian` images** — the runtime
  requires a `version:` for a layerless image on an external base, and the
  `entity-version` migrate step backfills only `base:`-style bare bases, not
  `from:`-style, so it is declared explicitly.
- R10: `ov -C image/debian eval run eval-debian-debootstrap-vm` → PASS (steps=6)
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
  guidance: when an `ov vm create` reports an EFI-firmware error on a host
  with KVM present, check `/dev/kvm` ownership/mode and restart `virtqemud`.

### 2026-05-30 — arch repo (overthinkos/arch): schema migrate + standard eval-*-vm bed naming

- **Migrated to schema 2026.144.1443** (`ov migrate`: kind-files split inline
  image/vm/pod/k8s into siblings + entity-version backfill + calver stamp).
- **Disposable beds renamed to the standard `eval-<descriptor>-<kind>` form:**
  `arch-vm` → `eval-arch-vm`, `arch-pacstrap-vm` → `eval-arch-pacstrap-vm`. R5 sweep
  across the arch repo (overthink.yml / vm.yml / README) + 5 plugins skills.
- **Builder gap fixed on `eval-arch-vm`:** the bed deploys the npm-building
  `pre-commit` add_layer to an arch CLOUD-IMAGE VM (no ov builder context), so the
  VM deploy could not resolve the npm builder. Named the arch-builder via
  `install_opts.builder_image` — the supported path (`DeploymentNode` has no
  `builder:` map field), mirroring the cachyos VM deploys.
- R10: `ov -C image/arch eval run eval-arch-vm` PASS — 52/52 (eval-live +
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
disposable gate to the `ov update` dispatch) and the deleted type `RebuildCmd`
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
`migrate_*.go`. Verified: `go build`/`vet`/`test ./...` green, `ov image
validate` clean — comments only, no code or identifiers changed.

### 2026-05-30 — fix: `keep_images` retention over-removal (per-tag prune + image-list dedup)

The `keep_images` auto-prune (after `ov image build`) could delete EVERY tag of
an image — including the just-built one — when a content-stable image had
accumulated many CalVer tags pointing at ONE image id. Observed: after repeated
`ov eval run eval-pod` runs, a build's prune left ZERO eval-pod images, so the
bed's `ov eval image` step failed with "image not available locally."

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
fresh `ov`, 4× repeated `ov image build eval-pod` hold at `keep_images=3` (never
0) with the newest tag always present, and `ov eval run eval-pod` passes
end-to-end (8/8 steps) under the accumulated-tag state that previously failed.

### 2026-05-30 — Multi-agent support: sub-agents + dynamic workflows + agent teams driving the `ov eval` beds; layered hooks; hybrid per-directory CLAUDE.md signposts

Made Overthink a first-class citizen of Claude Code's three multi-agent
primitives, all pointed at the existing `ov eval` disposable beds for
test/verify. One atomic cutover across the main repo, the `plugins`
submodule, and all eight `image/<distro>` submodules.

- **Sub-agents.** Added two *executor* agents in
  `plugins/internals/agents/`: `eval-bed-runner` (runs `ov eval run <bed>` —
  the full R10 sequence — and returns the verbatim per-step verdict + exit
  code + failing-log tail) and `deploy-verifier` (read-only `ov eval
  image`/`live` + `ov status` for an image or a user's deploy config, for AI
  and humans). Aligned the three existing *enforcer* agents to the current
  surface: `testing-validator` now lists `ov eval run`/`live`/`image` as the
  R10 evidence and its confidence table matches CLAUDE.md's four tiers;
  `root-cause-analyzer` gained `ov eval` in its toolkit; `layer-validator`
  was rewritten from a drifted, re-enumerated schema (it listed `depends`
  instead of `requires`, described `service:` as a raw supervisord INI
  string, and omitted the mandatory `version:`) into a focused high-value
  checker that defers the full schema to `/ov-image:layer` + `ov image
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
  [target …]` runs `ov image validate` + per-target `ov eval image`/`live`
  + `deploy-verifier` and aggregates a health report.
- **Layered hooks.** Slimmed `runtime-verification-reminder.sh` and
  `end-of-turn-challenge.sh` from ~1,076 lines of CLAUDE.md-duplicating,
  drifted static text into lean POINTERS to CLAUDE.md/skills. This cleared a
  live R5 stale-reference bug — the hooks still named the renamed `ov
  harness` / `ov rebuild` / `bench-pod` / `harness.yml` / `ov harness
  list-recipe|list-score` (now `ov eval` / `ov update` / `eval-sandbox` /
  `eval.yml` / `ov eval list-*`) — and resolved a direct conflict with
  CLAUDE.md (the Stop hook said "push only if authorized"; CLAUDE.md
  auto-lands on R10 pass). Added two deterministic `PreToolUse` (Bash) gates:
  `pre-commit-gate.sh` blocks `git commit --no-verify` and an
  absent/illegal `Assisted-by: Claude (<tier>)` trailer (incl. the forbidden
  `theoretical suggestion`); `pre-push-gate.sh` blocks
  `--force`/`--force-with-lease`/`-f`/`--no-verify`. Gates use
  command-position anchoring so they block real invocations but never
  mentions (`echo`/`grep`/quoted args). Both wired in `.claude/settings.json`
  alongside an `ov eval`-verb allowlist so the workflows run unattended.
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
running an `ov eval` bed is R10-class (disposable-only authorization, last
step never a parallel/background track, no scope-shrinking flags, and
paste-proof survives delegation — a delegated bed run whose failure is
summarized away is fraud); the hooks doctrine; and the per-directory signpost
convention. Agent teams remain documented opt-in (experimental), not enabled
in committed settings.
### 2026-05-30 — CachyOS GPU VM: venue-agnostic eval verbs, eval-anywhere, `cachyos-gpu` naming cutover, + headless Looking-Glass RCA

A five-part cutover on the CachyOS GPU-passthrough workstation. The operator VM
was renamed `cachyos-coder` → `cachyos-gpu`; every interactive `ov eval` verb was
made venue-agnostic (container | VM | ssh through ONE `DeployExecutor`); VM
live-eval now sources an applied layer's deploy-scope checks so the SAME monitor /
SPICE / mouse / keyboard / screenshot / selkies / Looking-Glass checks run against
BOTH the disposable bed and the persistent operator deploy; and a full empirical
RCA settled the headless Looking-Glass story.

- **T1 — VM naming cutover.** `cachyos-coder` → `cachyos-gpu` across
  `image/cachyos/vm.yml` (the kind:vm entity) and `image/cachyos/overthink.yml`
  (the deploy entry + the `eval-cachyos-gpu-vm` disposable bed). The dead
  `ov-cachyos-gpu` / `ov-ov-cachyos-gpu` autostart units + state dirs + stale
  deploy entries were purged. R5 self-test: `git grep cachyos-coder` returns only
  this file in both repos.

- **T2 — venue-agnostic `ov eval` verbs (`ov/eval_venue.go`, new).** The
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
  `ov eval live cachyos-gpu` runs the exact same check set as the disposable bed
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
  `ov/eval_bed_run.go`).** Every VM `ov eval live` now runs `WaitForSSH` +
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
  thread SIGSEGV'd QEMU on an `ov eval spice` probe connect (1-in-62 boots),
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
  SDDM/Plasma session renders to. SPICE serves it so `ov eval spice screenshot`
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
- **Render-proof check** `desktop-renders-spice` (`image/cachyos/overthink.yml`)
  — `ov eval spice screenshot` asserts `artifact_not_uniform: true`, so a
  solid-color/black/hung session FAILS (`assertArtifactNotUniform` samples
  pixels). The GPU-passthrough desktop must really render to pass.
- **selkies stream verification deepened** (`image/cachyos/overthink.yml`).
  Two checks added beyond the prior `:3000`=200 / `nvh264enc` binary present:
  - `selkies-encoding-frames` — pixelflux's nested compositor (`wayland-1`)
    socket exists, the `:8081` capture backend is listening, and the
    `ov-selkies-selkies` journal shows the encoder actually started. Proves
    the pipeline is live, not just that the port answers.
  - `kde-selkies-html-content` — `curl https://127.0.0.1:3000/` returns
    selkies/pixelflux/WebRTC content (not just any 200 from traefik), proving
    the web UI is wired end-to-end (traefik → `:8081` → pixelflux's bundle).
- **LG frame-flow bed check honestly dropped; LG infra check kept**
  (`image/cachyos/overthink.yml`). RCA (source-grounded — see
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

### 2026-05-29 — `ov vm`: per-VM disk/seed paths + SMBIOS credential delivery (SSH key injection made authoritative)

Surfaced while bringing up the operator `cachyos-coder` VM (the deliverable of the
cachyos-coder cutover below): three real `ov vm` defects in the disk/seed/SSH-key
path, each RCA'd before any fix and live-verified on the operator VM.

- **Shared disk/seed output path → cross-VM seed reuse.** `ov vm build`/`create`
  wrote `disk.qcow2` + `seed.iso` to a SHARED `output/qcow2/`, not per-VM. So
  `ov vm create cachyos-coder` (run after the disposable `cachyos-gpu-vm` bed)
  silently adopted the torn-down bed VM's `seed.iso` — whose embedded SSH key
  mismatched cachyos-coder's own `id_ed25519` — so cloud-init injected the wrong
  key and the deploy could not authenticate. Fixed with one `vmDiskDir(vmName)`
  helper → `output/qcow2/<vm>/`, applied to every disk/seed site (build / create /
  destroy / snapshot) + the unwired clone path; dead `resolveQcow2Path` removed.
  `ov vm create` now fails with a clear "run ov vm build" error instead of
  adopting a sibling's disk, and `ov vm destroy --disk` removes only the VM's own
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
   ov VMs run under `qemu:///session` (no portable user-level `virtqemud.socket` —
   Arch ships none), calls `ensureBootAutostartPrereqs` (`ov/vm.go`): idempotent
   `loginctl enable-linger <user>` + writes/enables a per-VM user oneshot
   `ov-autostart-<domain>.service` that `virsh -c qemu:///session start`s the
   domain at boot (`ov vm destroy` removes it via `removeAutostartUserUnit`). The
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
   `ov vm gpu` verb (`status` reports IOMMU readiness; `list` prints a
   ready-to-paste `libvirt.devices.hostdevs:` block with `managed: "yes"`
   covering the whole IOMMU group) and an informational `ov doctor`
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
4. **Host→guest image transfer** — `ov vm cp-image <vm> <ref> [--as <tag>]`
   + the reusable `TransferImageToGuest` helper stream a locally-built image
   into a VM guest's podman (`podman save | scp | podman load`), idempotent
   and offline (no registry round-trip). The `kind: eval` VM-bed runner now
   builds each nested pod child's image on the host and loads it into the
   guest (and re-loads + re-evaluates after the fresh `ov update`), so a
   nested pod's locally-built image is available inside the VM.
5. **Rootless-VFIO host-prereq detection** — the live test surfaced two host
   prerequisites that fail cryptically otherwise, so `ov vm gpu status` and the
   `ov doctor` "VFIO / GPU passthrough" group now report them: (a) the
   **RLIMIT_MEMLOCK** limit (VFIO pins all guest RAM, so rootless
   `qemu:///session` needs a limit ≥ guest RAM; the 8 MiB session default is
   too low and yields "cannot limit locked memory"), and (b) **/dev/vfio/<group>
   accessibility** (root-only by default). `ov udev` now also installs a
   `SUBSYSTEM=="vfio", GROUP="kvm"` rule so `ov udev install` grants persistent
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
  block (a PCI address is host-specific; `ov vm gpu list` generates it to add
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

### 2026-05-26 (later) — `ov update` disposable enforcement + deploy.yml round-trip preservation + cross-deploy quadlet-refresh Image= preservation

Follow-up cutover to the morning's sidecar-sweep + pixi-pytest fixes.
Three more latent bugs in `ov`'s update path that were documented but
not fixed in the earlier cutover (per CLAUDE.md R2 "escalated to the
operator for explicit re-scoping") are now landed in source + tests +
deployed binary + R10-verified end-to-end.

1. **`ov update <image> -i <instance>` did NOT enforce `disposable`.**
   The dispatcher in `ov/update_deploy_dispatch.go::dispatchByDeployTarget`
   resolved the deploy node and immediately handed off to the per-
   target update helper without ever consulting `node.IsDisposable()`.
   `ov update versa -i ecovoyage` therefore destroyed + recreated the
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
   reappears after every `ov update`/`ov config` invocation. Fix:
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
   Image= lines.** When `ov update <bed>` ran its env-refresh sweep
   across every other deployed quadlet, it re-resolved each sibling's
   `Image=` via `resolveShellImageRef("", imageName, "")`. That helper
   walks every local image carrying the matching
   `org.overthinkos.image` label, which includes the bed's per-deploy
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
   is `ov update <deploy>` (which routes through
   `rewriteQuadletImageLine` with the operator-authorized tag).
   `TestExtractQuadletImageLine` covers 4 cases: Image= present at
   top of [Container], Image= present alongside a sidecar Pod=
   directive (proves the regex doesn't get confused), absent Image=
   returns empty (caller falls back), missing file errors cleanly.

**R10**: `ov eval run eval-versa-pod` 8/8 PASS in 47 min. eval-live
124 / 124 (no regression). Bug 1 live-verification: the
`~/.config/containers/systemd/ov-versa-ecovoyage.container` Image=
line was `versa:2026.146.1239` before the R10 and STILL
`versa:2026.146.1239` after the R10 — identical content, no
cross-pollution. The only quadlet diff is one OV_MCP_SERVERS line
adding a transient `marimo @ ov-eval-versa-pod` discovery entry
(the env-refresh's documented job — registering the bed's MCP
endpoint with consumers). Bug 2A live-verification:
`ov update versa -i ecovoyage` refuses with the exact remediation
message from the new code. Bug 2B live-verification:
`disposable: false` persists in deploy.yml across the refused
update attempt (the write path would have dropped it before).
Operator data preserved (bind mount + named volume untouched);
ecovoyage container untouched (no destroy + restart triggered).

### 2026-05-26 — `ov config remove` sidecar-sweep + versa pixi pytest fix; versa/ecovoyage cut over to fresh image with disposable lockdown

Two latent bugs surfaced during a routine `versa` ecosystem refresh
(drop stale `versa` operator pod, R10 the versa image via
`eval-versa-pod`, then update `versa/ecovoyage` to the freshly-built
tag) and were fixed in the same cutover:

1. **`ov config remove <image>` swept sibling instances of the same
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
   `ov remove eval-versa-pod` (the R10 bed teardown) no longer
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
   `ov config remove versa` + delete quadlet + reload + 3 orphan
   volumes). Production `versa/ecovoyage` was collateral damage
   from bug #1 above; recovered cleanly via
   `systemctl --user start ov-versa-ecovoyage.service` after the
   root-cause analysis confirmed no state corruption (the
   `ov-versa-ecovoyage-airflow-data` volume was untouched; the
   bind mount at `/home/atrawog/Atrapub/ecovoyage` was never the
   target of the sweep). A pre-update snapshot of
   `~/.config/containers/systemd/ov-versa-ecovoyage.container` +
   `~/.config/ov/deploy.yml` was saved to
   `/tmp/ecovoyage-snapshot-pre/` before any further work.
2. Fixed bug #1 in source (`ov/sidecar.go` + `ov/config_image.go`
   + `ov/sidecar_test.go`), full `go test ./...` PASS, rebuilt the
   ov binary via `task build:ov` + `makepkg -si` (pkg/arch
   `pkgver` bumped to `2026.146.1105`), verified
   `Pod=%s.pod` + `Disabling sidecar %s` strings present in
   `/usr/bin/ov`.
3. Fixed bug #2 in source (`layers/marimo/pixi.toml` +
   `layers/marimo/layer.yml` version bump to `2026.146.1203` +
   `layers/marimo/pixi.lock` regen).
4. R10 via `ov eval run eval-versa-pod`: 8/8 steps PASS in 35 min
   (image-build 32m + eval-image 55s + deploy-add 19s + config 2s
   + start 0s + eval-live 87s + update 14s + cleanup 11s).
   eval-live: **124 passed · 0 failed · 0 skipped**. The
   `versa-graph-imports` and `versa-graph-notebook-export` probes
   that failed before the pytest fix now both ✓ exit 0.
5. `ov update versa -i ecovoyage` applied the freshly-built versa
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
   `~/.config/ov/deploy.yml` per operator directive — future
   autonomous updates must be re-authorized.

**Latent surfaces NOT fixed in this cutover** (operator escalation
pending): two additional `ov` bugs surfaced during the cascade —
(a) the `ov update <bed>` step regenerated quadlets for every
deploy whose `image:` resolves to the bed's source image, AND used
the bed's overlay tag (`eval-versa-pod:<calver>`) instead of the
sibling deploy's correct image tag (`versa:<calver>`). Bounded
blast radius (only `ov-versa-ecovoyage.container` was corrupted;
the subsequent `ov update versa -i ecovoyage` overwrote the
corruption with the correct image); (b) `ov update <image> -i
<instance>` does not enforce the `disposable: true` precondition
the way `ov update <name>` does, AND the deploy.yml re-serializer
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
  `ov eval adb install-app` / `ov eval adb install` verbs were refactored into
  thin wrappers over it — their CLI surface and the `adbMethods` allowlist are
  unchanged.

- **Nested deployment** — `pod → android` (the device on its emulator pod)
  mirrors `vm → k8s`. `target: android` is a passthrough hop in the deploy
  chain (the device shares its host pod's adb venue / the endpoint addr; no new
  shell venue). `ov deploy add` gained `--node-only` (dispatch just the named
  node, no descent) so a pod substrate can be started before its android
  children deploy; `ov eval run <bed>` now deploys a bed's nested children
  AFTER `ov start`, then runs eval-live.

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
- **Go — new generic verb `ov eval adb install-app`** (`ov/adb.go`,
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
  `version:` and their emitted `org.overthinkos.version` labels stay stable — no
  cache-miss cascade to downstream images.
- **`BuildBootcVM` (`ov/vm_bootc_install.go`)** no longer defaults an internal
  kind:image short name to `ghcr.io/overthinkos/<name>:latest` (a ref ov never
  builds or pushes — it is CalVer-only). The new `resolveBootcImageRef` helper
  passes full OCI refs through unchanged and resolves an internal short name to
  its newest local CalVer tag via the shared `resolveLocalImageRef`, surfacing
  an actionable `ov image build <name>` error when the image is missing. Covered
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

### 2026-05-25 — Comprehensive `ov eval appium` surface + AUR-packaged android-emulator toolchain

`ov eval appium` grew from 8 typed methods to a three-tier surface mirroring
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
(self-reference is filtered), so `ov image generate` failed with
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

- **`ov config`: quadlet `PublishPort=` keyed by image short-name, not deploy
  key.** `MergeDeployOntoMetadata` looked up the deploy.yml overlay by
  `meta.Image` (the baked `org.overthinkos.image` short-name) instead of the
  deploy key the caller was operating on. A `kind: eval` bed (key
  `eval-cachyos-ollama-pod`, image `ollama`) remapping `45434:11434` therefore had
  its port silently replaced by the image default `11434`, colliding at `ov start`
  with a running same-image production deploy (`ov-ollama`) →
  `rootlessport bind: address already in use`. This was the documented
  "quadlet-port lookup keyed by image, not deploy-key" known issue; it blocked the
  deploy-scope R10 of every cachyos GPU bed on a host that runs same-named
  production services. Fix: `MergeDeployOntoMetadata(meta, dc, deployName,
  instance)` now keys on `deployKey(deployName, instance)` with the deploy key
  passed by all five call sites (`ov config`/`start`/`shell` + the `--update-all`
  and tunnel-teardown loops); the sibling `dc.Lookup` parameter was renamed
  `deployName` to document the same contract (R3). Guarded by
  `TestMergeDeployOntoMetadata_KeyedByDeployNameNotImage`; the stale "Known issue"
  paragraph in `/ov-core:deploy` was removed (R5).

- **`ov eval run`: `kind: eval` pod beds' declared `port:` never reached the
  quadlet.** The bed bring-up shelled out `ov deploy add`/`ov config`/`ov start`
  with only the bed NAME; neither verb consults the project-side folded bed node,
  and both source `port:`/`security:`/`network:` from the IMAGE LABELS (persisting
  ports only behind an operator `-p` gate). So a bed's project-declared `port:`
  override lived only in `Config.Deploy[name]` and was never propagated to the
  per-host `deploy.yml` that `ov config` reads — every pod bed silently fell back
  to its image's default port and only "worked" because that port was free on a
  clean eval host. On a host running same-named production services it collided at
  start. Fix: `runEvalBed` now calls `persistBedDeployOverrides(name, node)` after
  the pre-run teardown and before `ov deploy add`, seeding the bed node's
  `port:`/`volume:`/`env:`/`tunnel:`/`security:`/`network:`/`disposable:` into the
  per-host deploy.yml so the existing config→merge→quadlet path honors them (no
  new merge logic; `ov config`'s `SetPorts`-gated save leaves the seeded port
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

- **`ov eval run`: pod/vm beds raced eval-live against slow first-run startup.**
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
  pins (`image/cachyos` and `image/fedora` reconcile their `@github` overthink
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

- **Cache cascade.** `org.overthinkos.version` was emitted as the build-time
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
- **Image `org.overthinkos.version` = content-derived `EffectiveVersion`** — the
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
  content-stable label; `ov clean` retention (`imageLabelCalVer` +
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
  unversioned fetched remote layer) with an `ov migrate` hint. The new
  `entity-version` `MigrationStep` (schema `2026.144.1442`; HEAD bumped to
  `2026.144.1443`) backfills `version:` on every layer.yml + every bare-base image
  entry (no `layer:` field AND an external `base:`), comment-preserving via the
  yaml.v3 node API, skipping the `image/` submodules (each migrates in its own
  repo) and `testdata`. `RunProjectMigrations` (remote-cache auto-migration)
  backfills fetched first-party remotes, which is what lets the runtime drop the
  fallback.

**`arch-rename` migrator bug found + fixed in the same tree (R2).** Running the
full `ov migrate` chain surfaced a latent bug: the `arch-rename` step
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
coordinated fixes, all surfaced by one failed `ov eval run` and fixed in one
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
   body; ov only provides the mount.

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
`ov image validate` (which produced a false "no builder.aur configured" error
because its private copy lacked the distro-keyed default), the `ov deploy add`
synthetic host/VM image (defaults-only), and the auto-intermediate generator —
all now route through the SINGLE `resolveEffectiveBuilder`, so builder
resolution is identical across `build` / `generate` / `inspect` / `validate` /
`deploy`. One code path, no drift.

**keep-pod-on-failure (operator debugging).** `ov eval run <bed>` used to tear
the bed down on ANY step failure (the shared `fail()` tail called `cleanup()`,
ignoring `--keep`), destroying the very target needed to diagnose the failure.
Now a FAILED run LEAVES the bed running and prints target-appropriate inspect +
destroy hints (`ov eval live <name>` / `podman exec ov-<name>` / `ov remove
<name>`, or `ov vm destroy` for VM beds). To keep this from blocking re-runs, the
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
agent-forwarding/ov stay on the ecosystem `v2026.141.1600`.

**Main side.** `image.yml` drops the three image entries; `android-emulator`
repoints to `base: selkies.selkies-desktop`; `eval.yml` drops the two beds (now in
the submodule) and the matching bed-coverage-map lines; `overthink.yml` mounts
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

**Automatic future guard.** `ov image validate` (`validateImageDAG`) now SURFACES
the `resolveNamespacedBases` error (it was swallowed with `_ =`), so a namespaced
base — or its builder / bootstrap builder — that doesn't resolve is caught at
`ov image validate` time, before a build hits it. A regression test
(`TestResolveNamespacedBase_BuilderRefRequalified`) reproduces the exact uncovered
shape and fails without the fix (verified: `import namespace "up" not found`).

**Verification.** Both enabled selkies images passed full disposable R10 beds
(`selkies-desktop` 193 checks, `sway-browser-vnc` 178 checks, 0 failures); main
`ov image validate` is clean; the cross-repo resolution is proven by the rebuilt
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

### 2026-05-24 — Resolver docs + feat/-branch R10-gated git workflow + eval-coverage & zero-warnings gates + `ov image reconcile` (docs + tooling cutover, no schema bump)

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
FIRST, then `ov image reconcile` repoints the consumer, whose authoritative R10
runs against the real pushed tag. Sync-to-upstream before start/landing and
prune-only-merged-branches + worktree-prune hygiene per repo.

**Two sharpened acceptance gates.** (1) **Eval-coverage:** a change is landable
only if it ships the test coverage that PROVES its functionality (`eval:` checks
for new/changed layers & images, Go tests for `ov` code) and the R10 run
exercised it. (2) **Zero-warnings:** R10 is successful only at ZERO warnings
(resolver newest-wins / build / `ov image validate` / `ov eval` / deploy) — a
version-mismatch warning is cleared with `ov image reconcile`, anything else via
root-cause-analyzer + a real fix. R1 is now a hard gate, not just an
investigation trigger.

**`ov image reconcile`** (`ov/reconcile.go`, `/ov-build:reconcile`). Aligns every
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
  runtime lib at `ov eval image`, no deploy/GPU needed) + `pixelflux-wayland-socket`
  (deploy — `/tmp/wayland-1` exists).
- `layers/labwc/layer.yml`: `labwc-wayland-socket` (deploy — `/tmp/wayland-0`
  exists; `service: labwc running` was crash-loop-blind).
All validated against the live production instance (healthy: 0 `not found`,
both sockets present) and against the broken cachyos build (4 `not found`, no
sockets).

No schema/format change → no `MigrationStep`, no `version:` bump; landing push
carries a fresh per-push `v<CalVer>` tag.

### 2026-05-24 — Add readiness waits (`eventually:`) to the chrome CDP/MCP deploy-scope eval probes (bug fix, no schema bump)

Surfaced by `ov eval run eval-selkies-desktop-pod`: 105/106 live checks passed,
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

Root cause (ov code): `GlobalLayerOrder` (`ov/intermediates.go`) built its layer
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
`ov image build fedora-builder` installs ffmpeg-devel/x264-devel cleanly; with
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
`ov eval image` 11/0/0 (nvcc, cudnn.h); `python-ml` built →
`ov eval image` 14/0/0 (torch + vllm importable). GPU runtime probes
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
`ov -C image/cachyos image validate` emits benign "local layer X shadows remote
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
`ov eval image` / `ov eval live` verb-step labels, not the deleted fixture image.

Also removed the stale `--keep-eval-pod` reference from CLAUDE.md's score-flag
list — no such flag exists in the eval-run command (`ov/eval_runner_cmd.go`
ships `--keep` / `--no-rebuild` / `--all-beds` / `--keep-repo` / `--on-*` /
`--plateau-iteration` / `--max-scenario` / `--tag` / `--dry-run` /
`--skip-rebuild`).

This is a content/instance cutover (renamed + merged specific entities), not a
schema-shape change — so NO `MigrationStep` and NO `LatestSchemaVersion` /
`version:` bump, mirroring the earlier deploy→eval-bed relocation. Operators who
run the harness must rename their `~/.config/ov/deploy.yml` `eval-pod`
AI-sandbox deploy to `eval-sandbox` (it lives in the per-host deploy file, which
`ov migrate` does not rewrite from a score-value change).

### 2026-05-23 — Build-artifact cleanup: one-time auto-purge + configurable reusable-artifact retention (`ov clean`, `defaults.keep_images`/`keep_eval_runs`) (additive, no schema bump)

Follow-up to the build-speedup cutover. Investigation found the project tree had
grown to ~12G of build artifacts from three never-cleaned accumulators: `pkg/arch`
(1.4G — 138 stale makepkg `*.pkg.tar.zst` + `src/`/`pkg/`, `task build:ov` never
cleaned up), podman image storage (164GB reclaimable from old CalVer image tags),
and `.eval/` (1.7G run output). Operator principle: **one-time artifacts are
always cleaned immediately; reusable artifacts get retention configurable in
`defaults:`**, with both auto-pruning at creation AND an explicit `ov clean`.

Additive, like the build-speedup keys: optional `defaults:` sub-keys with Go
fallbacks ⇒ no MigrationStep, no `LatestSchemaVersion` bump.

- **One-time (always immediate):** `task build:ov` now removes makepkg `src/`,
  `pkg/`, `*.pkg.tar.zst`, `*.log` after install (Taskfile change).
- **`defaults.keep_images`** — after `ov image build` (push runs excluded),
  prune all but the newest N CalVer tags per `org.overthinkos.image` group,
  ordered by the `org.overthinkos.version` label. Safety: skip any image in use
  by a container (`podman ps -a`), and `rmi` WITHOUT `-f` as a backstop so the
  engine refuses any still-referenced image. `keep_images: 0`/absent disables.
- **`defaults.keep_eval_runs`** — after `ov eval run` (any path: bed /
  `--all-beds` / score), trim `.eval/<bed|score>/` to the newest N run artifacts
  (CalVer run dirs, `runs/<id>` dirs, `result-<calver>.yml`). `NOTES.md` (durable
  Syncthing memory) is ALWAYS preserved. `keep_eval_runs: 0`/absent disables.
- **`ov clean`** — on-demand verb applying the same retention now, plus the
  makepkg sweep; clears the existing backlog (the 138 `.pkg.tar.zst` + old image
  tags). `--dry-run` / `--images` / `--eval` / `--keep N`.
- Repo `overthink.yml` ships `keep_images: 3`, `keep_eval_runs: 3`. Go fallbacks
  are 0 (disabled) so third-party configs get no surprise pruning.

**Fixed `org.overthinkos.version` (was hardcoded `"1"`).** The label now carries
the BUILD CalVer — the version the generate run stamped the image with, equal to
the image's tag (e.g. `2026.143.1234`) — instead of the meaningless
`LabelSchemaVersion` constant `"1"`. `ExtractMetadata` only ever used this label
as the "is this an ov image?" presence sentinel, so the value change is safe; the
dead `LabelSchemaVersion` const was removed (its only two uses were these
emission sites). Retention orders builds by the CalVer in the image **tag**
(`extractCalVerTag`), so it works even on images built before this fix (their
label is still the stale `"1"`).

Implementation: `ov/clean.go` (`pruneImagesByRetention`, `pruneEvalRuns`,
`cleanMakepkgArtifacts`, `CleanCmd`); hooks in `BuildCmd.Run` / `EvalRunCmd.Run`;
`LocalImageInfo.ID` added for the in-use skip; same `mergeImageConfig` field-carry
discipline as the build-speedup keys. VM disks (`output/`, `image/*/output/`) are
out of scope — single products per type, no accumulation, removed on demand by
`ov vm destroy --disk`; the VM raw intermediate is already auto-cleaned
(`vm_cloud_image.go`).

### 2026-05-23 — Config-driven build-speedup tunables (`defaults.{jobs,podman_jobs,podman_jobs_cap,context_ignore,cache}` + `distro.<name>.dnf` + committed `pixi.lock`) (additive, no schema bump)

A four-part build-speed cutover landed as ONE atomic, **additive** commit. It is
deliberately NOT a schema change: every new key is an optional sub-key of an
existing kind (`defaults:` / `distro:`) with a Go fallback, so per the
cutover-policy skill ("purely additive ⇒ no cutover") there is no
`MigrationStep`, no `LatestSchemaVersion()` bump, and no load-time gate — old
configs keep loading via fallbacks, and third-party configs are never forced to
run `ov migrate` for keys they don't use.

**Item 1 — build-context excludes (`defaults.context_ignore`).** The static
hand-maintained `.containerignore` (`​.git bin ov *.md`) and `.dockerignore`
(editor/python/node cache-bust globs) were **deleted** and are now GENERATED at
the project root by `ov image generate` (`writeContextIgnore` in
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
the flat imports, so `defaults.context_ignore` authored in `overthink.yml` never
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
residual `include:` key is now a hard load-time error pointing at `ov migrate`.

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
(arch/cachyos/debian/ubuntu/fedora/bootc) is now a single `overthink.yml`** (all
per-kind siblings inlined) that imports `build.yml` flat and (where it needs main's
base entities) imports main under the `ov` namespace (`ov.arch`, `ov.fedora`,
`ov.arch-builder`, `ov.fedora-builder`). Several latent pre-existing bugs were
fixed in passing per R2 (a stray `disposable:` on a VmSpec, singular
`libvirt.device:`/`channel:` keys that silently dropped the SPICE channel, and
`cloud_init.user:` → `users:` in the debian/ubuntu/arch VMs).

**deploy→eval unification.** Repo-shipped disposable VM test beds in the
submodules (`arch-vm` + its nested beds, `arch-pacstrap-vm`, `cachyos-vm-deploy`,
`debian-debootstrap-vm`, `ubuntu-debootstrap-vm`) moved from `kind: deploy`
(deploy.yml) to `kind: eval` (in the single overthink.yml), matching the main
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

**`selkies-desktop-ov` removed (breaking — public image surface deleted).** `openclaw-desktop` supersedes its role (streaming desktop + full nested ov toolchain, rootless uid 1000) — the CachyOS/CPU successor of the nvidia/GPU original. It was a leaf image (nothing had `base: selkies-desktop-ov`; no deploy.yml entry; no eval bed), so removal was a reference sweep, not a dependency untangle. Its 13 image-level nested-toolchain eval checks (subuid two-ranges, `newuidmap` cap, `policy.json`, containers.conf `userns=host` ×2, `_CONTAINERS_USERNS_CONFIGURED`/`BUILDAH_ISOLATION` env, nested `podman run`, `virsh` session list, in-container `ov version`/`ov doctor`) were **migrated into `openclaw-desktop`'s image-level `eval:`** so coverage transferred (the `virsh domcapabilities` KVM-hardware check stays covered by the `virtualization` layer's own baked `libvirt-kvm-acceleration` eval, inherited via `ov-full`). R5 hard-cutover sweep: deleted the image.yml entry, deleted `plugins/selkies/skills/selkies-desktop-ov/`, and repointed every CURRENT-state reference to `/ov-openclaw:openclaw-desktop` across ~16 skills + README.md + the `virtualization` layer comment — with one exception: `nvidia-layer`'s "base:nvidia image runs on AMD" anecdote repointed to `selkies-desktop-nvidia` (openclaw-desktop is CPU/cachyos, not a base:nvidia example). The valuable GPU-agnostic worked examples from the old skill (the two-level nested-virtualization proof, the cross-storage bootc-load recipe, the rootless posture table) were migrated into the new `openclaw-desktop` skill. `git grep selkies-desktop-ov` now returns only this `CHANGELOG.md` (main) and nothing in `plugins`.

A `kind: eval` R10 bed **`eval-openclaw-desktop-pod`** was added (`disposable: true`, ports remapped into a free `340xx` block — `34000`/`34222`/`34224`/`34022`/`34789`/`34434` — to coexist with the selkies/openclaw beds); its deploy-scope probes assert the cross-stack headline artifacts (AUR `google-chrome-stable`, the Selkies HTTPS-200 UI, the three AI CLIs at `${HOME}/.npm-global/bin/`, the `ollama` binary). The acceptance gate is `ov eval run eval-openclaw-desktop-pod` (build → eval image → deploy → eval live → fresh `ov update` rebuild → teardown). **No `MigrationStep` / no `version:` bump / no new git tag** (an additive image + a layer-decoupling refactor + a leaf-image removal; repo-internal, no schema change). See `/ov-openclaw:openclaw-desktop`, `/ov-ollama:ollama`, `/ov-distros:container-nesting`, `/ov-infrastructure:virtualization`, `/ov-eval:eval`.

### 2026-05-22 — Migrate `selkies-desktop` (CPU) to CachyOS base; cachyos AUR parity + AUR doc cleanup

the CPU `selkies-desktop` streaming-desktop image was **migrated from `base: fedora-nonfree` to `base: cachyos`** (the Arch-derived base owned by the `overthinkos/cachyos` submodule, already remote-included in `overthink.yml` for `versa`/`openclaw`) — an in-place hard cutover mirroring the openclaw→cachyos precedent. **Scope was the CPU variant ONLY**; the GPU variants `selkies-desktop-nvidia` and `selkies-desktop-ov` (`base: nvidia`) stay on Fedora (porting the `/usr/lib64`-hardcoded `nvidia`/`cuda` layers to Arch is out of scope). Because all three selkies images compose the same `selkies-desktop` metalayer, the layer changes are backward-safe: the generator resolves a layer's packages by the IMAGE's `distro:` tags (first-match, `ov/generate.go` `compileSystemPackageSteps`), and the Fedora GPU variants carry `distro: [fedora,…]` which never matches the new `arch:` sections — so they keep installing the `fedora:` packages unchanged (R3 generic win). **Unlike openclaw, selkies-desktop ADDS `build: [pac, aur]`** (not just inherited `[pac]`): it composes `chrome` (AUR `google-chrome`) + `wl-tools` (AUR `wlrctl`), and the AUR builder is gated on `aur ∈ BuildFormats` (`generate.go:1418` + the IR Phase-2 install both key on `img.BuildFormats`) — inheriting plain `[pac]` would silently drop both AUR packages. Confirmed via `ov image generate`: the `chrome-aur-build` + `wl-tools-aur-build` arch-builder stages and the `pacman -U /tmp/aur-pkgs/*` install steps emit only with `aur` in `build:` (the same reason `arch-test` declares `build: [pac, aur]`). **Twelve Fedora-only desktop sub-layers that would have silently installed NOTHING on Arch** (the silent-install trap: no `arch:`/`cachyos:` distro section AND no `pac:` format section → zero installs, build succeeds, binary missing at runtime) each gained a `distro.arch` section (R3 — benefits any future Arch desktop image): `pipewire` (`pipewire-pulseaudio`→`pipewire-pulse`, dropped the Arch-absent `pipewire-utils`), `labwc` (`xorg-x11-server-Xwayland`→`xorg-xwayland`), `waybar-labwc`, `desktop-fonts` (COPR `che/nerd-fonts` has no Arch analog → Arch `extra` `ttf-jetbrains-mono`/`ttf-liberation`/`ttf-nerd-fonts-symbols`(`-mono`)), `swaync` (`SwayNotificationCenter`→`swaync`), `pavucontrol`, `wl-tools` (`xprop`/`xwininfo`→`xorg-xprop`/`xorg-xwininfo`; `wtype` from `extra`; `wlrctl` via `aur:`), `wl-overlay` (`python3-gobject`→`python-gobject`), `a11y-tools` (`python3-pyatspi`→`python-atspi`), `xterm`, `fastfetch`, and `selkies` (the big list: `libICE`/`libSM`→`libice`/`libsm`, `pulseaudio-libs`→`libpulse` which also covers `pulseaudio-utils`/pactl, `mesa-va-drivers`→`libva-mesa-driver`, `iproute`→`iproute2`). **Cross-distro eval via `package_map:`** (not a Fedora-name-only assertion): `desktop-fonts` and `a11y-tools` had `package:`/`installed:` eval checks keyed to Fedora package names; because eval blocks are NOT distro-gated (the still-Fedora GPU variants run the same block), each `package:` check got a `package_map:` (e.g. `python3-pyatspi` + `{arch: python-atspi, fedora: python3-pyatspi}`) so the SAME check resolves correctly on both bases — preserving the assertion everywhere instead of dropping it. `wl-tools` also gained a `wlrctl-binary` presence eval (the AUR `wlrctl` previously had NO presence check anywhere — R8). A `kind: eval` R10 bed `eval-selkies-desktop-pod` was added (`disposable: true`, ports remapped to `33000`/`39222`/`39224`/`32222`), asserting the AUR-built binaries (`google-chrome-stable`, `wlrctl`, `wtype`) plus key desktop binaries at deploy scope; the baked layer/image evals (incl. the Selkies HTTPS-200 UI probe) cover the rest. **CachyOS AUR parity + doc cleanup** (the operator asked to "make sure cachyos has full support for aur as arch"): functional AUR support already existed on cachyos via the inherited `builder.aur: arch-builder` (proven by the selkies-desktop AUR build above), but `cachyos` was the ONLY base distro lacking a `produce:` field (arch/fedora/debian/ubuntu all declare it). `produce: [pixi, npm, cargo, aur]` was added to `image/cachyos/cachyos-base.yml` matching arch. `produce:` is functionally inert here (cachyos is never referenced AS a builder — only consumed; `resolved.BuilderCapabilities` is read solely by `validateBuilders` when an image is a builder target), so it is a source-consistency fix; it lives in the submodule and main consumes cachyos via a PINNED remote include, so it does not affect main builds until the cachyos repo is pushed/retagged and main's pin bumped. The skill docs were clarified so AUR authoring is unambiguous: the canonical form is the nested `distro.arch.aur.package`, a consuming image must declare `build: [pac, aur]`, and `arch-builder` compiles AUR for BOTH arch and cachyos. **No `MigrationStep` / no `version:` bump / no new git tag** (a repo-internal in-place base swap + package-coverage addition, same class as the openclaw migration). The R5 sweep updated the selkies SKILL.md files referencing selkies-desktop's `fedora-nonfree` base. See `/ov-selkies:selkies-desktop-ov`, `/ov-distros:cachyos`, `/ov-distros:arch`, `/ov-image:layer`, `/ov-eval:eval`.

### 2026-05-22 — Trim openclaw to {`openclaw`, `openclaw-full`}, migrate both to CachyOS base, refresh to latest

the openclaw image family was reduced to the two shipping headless variants and moved off Fedora. **`openclaw-ollama` (the nvidia/CUDA gateway+Ollama image) was DELETED** from `image.yml`; the remaining `openclaw` and `openclaw-full` were **migrated from `base: fedora` to `base: cachyos`** (the Arch-derived base owned by the `overthinkos/cachyos` submodule and already remote-included in `overthink.yml` for `versa` — no new plumbing, an in-place base swap mirroring `versa`), and both were **enabled** (`enabled: false` removed). Both images inherit `build: [pac]` from the cachyos base (the pixi/npm/cargo/aur→`arch-builder` map is inherited like `versa`; npm/go/cargo/pixi/download layers are distro-agnostic, and the pac layers — gh/tmux/ffmpeg/ripgrep/sqlite/dbus/socat — resolve via their `arch:` sections). **Two Fedora-only layers that would have silently installed NOTHING on Arch** (the `distro: null`-class trap) were fixed generically (R3 — benefits every Arch image): `ffmpeg` and `sqlite` each gained an `arch: { package: [...] }` section plus a presence `eval:` check (`/usr/bin/ffmpeg`, `/usr/bin/sqlite3`) so the install is actually asserted (R8). **`gogcli` was unpinned `@v0.4.2` → `@latest`**: the pin existed because Fedora 43 ships only Go 1.25 (`golang-bin`) while gogcli ≥ v0.13.0 needs Go 1.26.x; on CachyOS/Arch the `golang` layer's `go` package is `2:1.26.3`, so `@latest` (v0.14.0, go.mod 1.26.1) builds with **no golang-layer change** — the obsolete Fedora-toolchain comment was removed and a `${HOME}/go/bin/gog` eval check added. **R10 (the first build of the now-enabled `openclaw-full`) surfaced a latent upstream breakage** unrelated to the base migration: the `wacli` Go module moved from the `steipete` GitHub org to `openclaw` and carried the move into its `go.mod` (`module github.com/openclaw/wacli` at v0.10.0), so `go install github.com/steipete/wacli/...@latest` hard-failed on the module-path mismatch (it would fail on any base; it only surfaced now because `openclaw-full` was `enabled: false` and unbuilt since v0.10.0 shipped). The `wacli` layer's install path was updated to `github.com/openclaw/wacli` (R2 — fixed in the same working tree, not deferred). Every other steipete-org tool (gifgrep / goplaces / songsee / sag / camsnap / gogcli / ordercli) still declares the `steipete` path in its `go.mod` at `@latest` and was verified unchanged. **Version refresh policy: keep the existing `*` / `@latest` convention** — every other openclaw-full layer already tracks latest (openclaw npm `*`, the 11 npm tool layers `*`, the Go tools `@latest`, himalaya's `cargo install --locked` crate, uv's latest GitHub release, all pacman packages), so the fresh `ov image build` is what pulls newest published versions; no per-layer pinning was introduced. The **R5 sweep** (the earlier `git grep` missed the `plugins/` submodule) covered: the deleted `openclaw-ollama` SKILL.md; the stale `plugin.json` + `marketplace.json` descriptions (which still listed dead `bootc/full/ml/sway/ollama/browser` variants); `plugins/README.md` (count 7→6, reworded for the CachyOS base); the `openclaw`/`openclaw-layer`/`openclaw-deploy` cross-refs; the openclaw-ollama mentions in the `nvidia`/`ollama`/`ollama-layer`/`agent-forwarding`/`supervisord` skills; the now-stale `Base: fedora` / `linux/amd64,linux/arm64` / `disabled` facts in the `openclaw`/`openclaw-full` skills (updated to `cachyos` / `linux/amd64` / enabled); and the `openclaw-ollama` Go test fixture in `ov/intermediates_test.go`, renamed to a neutral `gpu-gateway` (same nvidia base + `[openclaw, ollama]` shape, so the intermediate-sharing assertions are unchanged). `git grep 'openclaw-ollama'` now returns only this file. **No `MigrationStep` / no `version:` bump / no new git tag** (a repo-internal image base swap + image drop, same class as the sway-family drop and the submodule extractions; a user `deploy.yml` deploying the dropped image still loads — deploy reads OCI labels, not `image.yml`). Two `kind: eval` R10 beds were added to `eval.yml` — `eval-openclaw-pod` and `eval-openclaw-full-pod` (both `disposable: true`, `eval-<descriptor>-<kind>` naming) — each driving the full `ov eval run` acceptance sequence (build → eval image → deploy → eval live → fresh `ov update` → teardown); the openclaw-full bed's `eval:` block asserts the migration-critical artifacts (`gog`, `ffmpeg`, `sqlite3`) at deploy scope. **R10 of the `eval-openclaw-full-pod` bed then surfaced a SECOND pre-existing, base-independent issue: headless `openclaw-full` composed `chrome` + `chrome-cdp` + (transitively) `chrome-devtools-mcp` but has no compositor and no Chrome-launch service, so `cdp-proxy` and the `chrome-devtools-mcp` server pointed at a Chrome that never starts — the `chrome-cdp` `/json/version` deploy probe failed (RCA-confirmed NOT a cachyos regression: Chrome v148 built + ran fine on cachyos; `chrome-wrapper` requires a Wayland socket absent in a headless image). The operator chose to STRIP the browser stack** — `chrome` + `chrome-cdp` removed from the `openclaw-full` metalayer (29→27 layers), making it a clean non-browser headless gateway. Cascade: the `openclaw-full` image dropped its `9222`/`9224` ports + the `build: [pac, aur]` override (no AUR consumer remains, so it inherits plain `[pac]`); the bed dropped its `9222`/`9224` host ports + the `google-chrome-stable` probe; the openclaw-full skill dropped its chrome/CDP/port rows; and — because NO openclaw image now ships `chrome-devtools-mcp` — the `ov-openclaw` plugin's `.mcp.json` (chrome-devtools @ 9224) was DELETED, the `mcpServers` field removed from `plugin.json`, the chrome-devtools claim removed from `plugin.json` + `marketplace.json`, and the `plugins/README.md` MCP column set to `—`. `playwright` (self-contained bundled browsers) was retained; the shared `chrome`/`chrome-cdp`/`chrome-devtools-mcp` layers stay (still used by selkies-desktop / sway-browser-vnc / chrome-sway). See `/ov-openclaw:openclaw`, `/ov-openclaw:openclaw-full`, `/ov-distros:cachyos`, `/ov-automation:openclaw-deploy`, `/ov-eval:eval`.

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

### 2026-05-22 — Drop `ov eval kind` + the hardcoded bed table → `kind: eval` R10 beds in `eval.yml`, run via `ov eval run`

the 11 disposable R10 test beds that lived as `deploy:` entries in `deploy.yml` (plus the hardcoded `bedTable`/`bedSpec` in `ov/eval_kind_cmd.go` that `ov eval kind <subkind>` walked) were unified into a single config-driven surface. Each bed is now a `kind: eval` document in `eval.yml` — a `DeploymentNode` (target + image/vm/local + `disposable` + `eval:` probes) folded into the Deploy map at load time (`foldEvalBeds` + `DeploymentNode.EvalBed`) so EVERY deploy verb resolves it by name through the same path; `uf.EvalBeds()` enumerates them. The `ov eval kind` command + `bedTable`/`bedSpec`/`bedSpecFor`/`kindList`/`validKinds` were DELETED; the R10 sequence engine was salvaged into `runEvalBed` (which reads the node directly — `bedSpec`'s image/vm/local/IsVM/IsLocal were pure duplication of fields already on the bed), and `ov eval run <name>` now dispatches by kind: a `kind: eval` bed runs the full R10 sequence (build → eval image → deploy → eval live → fresh update → tear down), a `kind: score` runs the AI loop; `--all-beds` runs every bed name-sorted. Beds renamed to a unified `eval-<descriptor>-<kind>` scheme (dropping a redundant suffix when descriptor == kind AND the short form is free): `k3s-vm` → `eval-k3s-vm`, `eval-local-deploy` → `eval-local`, `jupyter-pod`/`jupyter-ml-pod`/`versa`/`android-emulator-pod` → `eval-jupyter-pod`/`eval-jupyter-ml-pod`/`eval-versa-pod`/`eval-android-emulator-pod` (`eval-sway-browser-vnc-pod`/`eval-image-pod`/`eval-layer-pod`/`eval-pod-pod`/`eval-deploy-pod` unchanged — `eval-pod-pod` deliberately keeps its suffix because `eval-pod` is the reserved harness AI-sandbox pod name, the score `pod:` target; the `k3s-vm` *vm entity* + `vm-k3s-vm` *k8s entity* keep their names). The supporting `vm: k3s-vm` + `k8s: vm-k3s-vm` entities moved into `eval.yml` too; **`deploy.yml` was DELETED** and dropped from `overthink.yml`'s `include:` (the repo ships only eval beds; operator deployments live in the per-host `~/.config/ov/deploy.yml`). Validation (`validateEvalBeds`, load-time so every verb benefits) enforces target ∈ {pod,vm,local}, a resolvable cross-ref, `disposable: true`, and a name space disjoint from `kind: deploy`. **No `MigrationStep` / no `version:` bump / no new git tag** (additive `kind: eval` + repo-internal bed relocation, same class as the six submodule extractions and the sway-family drop; `version:` stays `2026.141.1600`). Main-repo only — submodules never call `ov eval kind` and deploy their own beds via normal verbs. See `/ov-eval:eval`, `/ov-eval:eval-sway-browser-vnc`, `/ov-core:deploy`.

### 2026-05-22 — Drop the sway-desktop image family except `sway-browser-vnc` + `eval-sway-browser-vnc-pod` R10 bed on `sway-browser-vnc`

the four OpenClaw desktop+browser images composing the Sway streaming-desktop stack — `openclaw-full-ml`, `openclaw-full-sway`, `openclaw-ollama-sway-browser`, `openclaw-sway-browser` (main `image.yml`) — plus the bootc variant `openclaw-browser-bootc` (and its `kind: vm` entity) in the `image/bootc` submodule were DELETED. The single shipping Sway image `sway-browser-vnc` is KEPT and now also backs the canonical pod eval bed, renamed `openclaw-sway-browser-pod` → `eval-sway-browser-vnc-pod` (`disposable: true`, `image: sway-browser-vnc`); the bed's own `eval:` block adds the deploy-scope probes (operator-side http, cdp list, wl sway-tree, record) that `sway-browser-vnc` doesn't already bake. **Zero layer deletions** — `sway-browser-vnc` keeps `sway-desktop-vnc → sway-desktop`, so the entire sway layer stack (sway/chrome-sway/xdg-portal/xfce4-terminal/thunar/wl-*/swaync/waybar/…) stays in use; openclaw-only layers that lost their last image consumer (the `openclaw-full-ml` layer) remain as reusable library entries (unused ≠ deprecated). **No `MigrationStep` / no schema bump** (removal of repo-internal image definitions, like the six submodule extractions; a user `deploy.yml` deploying a dropped image still loads — deploy reads OCI labels, not `image.yml`). The R5 sweep covered `deploy.yml` (bed + coverage-map comments), the `ov/` Go test fixtures/comments, `README.md`, and the per-image skills (DELETED the `openclaw-sway-browser`/`openclaw-ollama-sway-browser`/`openclaw-full-sway`/`openclaw-full-ml` image skills + `openclaw-browser-bootc` + `openclaw-browser-bootc-bootc`; ADDED `/ov-eval:eval-sway-browser-vnc`). See `/ov-eval:eval-sway-browser-vnc`, `/ov-selkies:sway-browser-vnc`.

### 2026-05-22 — bootc images → `overthinkos/bootc` submodule + `bazzite-ai` → `bazzite` rename

the four bootc bootable-container images — `selkies-desktop-bootc`, `bazzite` (was `bazzite-ai`), `aurora`, `openclaw-browser-bootc` — plus their four `kind: vm` bootc entities moved OUT of the main repo into the dedicated **`overthinkos/bootc`** repo, mounted as a git submodule at **`image/bootc`** with its own canonical `overthink.yml` (directly buildable: `ov -C image/bootc image build selkies-desktop-bootc --include-disabled`; all four ship `enabled: false`). **The debian/ubuntu pattern, NOT fedora's/arch's**: every bootc image roots on an **EXTERNAL upstream base URL** (`quay.io/fedora/fedora-bootc:43`, `ghcr.io/ublue-os/…`), so there is **no in-repo bootc base image** to keep — and nothing in main consumes any bootc image — meaning **no `bootc-base.yml` in main and zero main ↔ bootc coupling** (the only edge is `bootc → main`). The submodule composes the SAME layers — none were copied — by **git reference** and remote-includes the shared `build.yml` (for `distro.fedora` + the `rpm` template) AND `fedora-base.yml` (solely to bring `fedora-builder` into scope, since external-based bootc images inherit no builder map and fall through to `defaults.builder`). **Three tag pins, each with a reason**: every layer ref + `build.yml` at the ecosystem tag `v2026.141.1600`; the `fedora-base.yml` file include at `v2026.141.2308` (where it first exists; its internal layer refs are `v2026.141.1600`); and `os-system-files` + `ujust` (bazzite-exclusive) at a **fresh `v2026.142.0552`** carrying their renamed `/usr/share/bazzite/` paths. The **`bazzite-ai` → `bazzite` rename is a full sweep** (image, the `bazzite-bootc` VM entity, `image:` cross-refs, AND the internal `/usr/share/bazzite-ai/` paths + comments in the bazzite-exclusive `os-system-files`/`ujust` layers, which stay in main and are pulled at the fresh tag) — `git grep 'bazzite-ai'` returns only history. The three external-base bootc images (`aurora`/`bazzite`/`openclaw-browser-bootc`) gained the previously-missing `distro: [fedora:43, fedora]` (R2 — without it the generator emits zero rpm installs; mirrors selkies' working pattern). **No `MigrationStep`** (relocation of repo-internal definitions, like all five prior extractions; the rename rides along because `bazzite-ai` was `enabled: false` and never deployable, so no user config can reference it, and a step would require a `LatestSchemaVersion()` bump that would route every other submodule through the load-gate). See `/ov-distros:bazzite`, `/ov-distros:aurora`, `/ov-selkies:selkies-desktop-bootc`, `/ov-distros:bootc-base`.

### 2026-05-21 — Fedora showcase images → `overthinkos/fedora` submodule + base stays in main via `fedora-base.yml`

the Fedora consumer showcase images — `fedora-coder`, `fedora-ov`, `fedora-test` — moved OUT of the main repo into the dedicated **`overthinkos/fedora`** repo, mounted as a git submodule at **`image/fedora`** with its own canonical `overthink.yml` (directly buildable: `ov -C image/fedora image build fedora-coder`). **Unlike Debian/Ubuntu (whose bases moved entirely) and exactly like Arch, the Fedora base stack STAYS in the main repo**: `fedora` is the ecosystem default base (~40 main images root on `fedora`/`fedora-nonfree` — jupyter, immich, hermes, selkies-desktop, nvidia, the openclaw family, the eval beds — and `fedora-builder` is main's `defaults.builder`), so moving it would invert the dependency. The base stack (`fedora` + `fedora-builder` + `fedora-nonfree`) was extracted from `image.yml` into a new main-repo **`fedora-base.yml`** (single source of truth, mirroring `arch-base.yml`), included locally by main's `overthink.yml` AND remote-included by the submodule (`@github.com/overthinkos/overthink/fedora-base.yml:<tag>`); its builder/nonfree layers are git-ref'd so the same file resolves in both contexts. The submodule composes the SAME layers — none were copied — by **git reference** (`@github.com/overthinkos/overthink/layers/<name>:v2026.141.1600`) and remote-includes the shared `build.yml` (which keeps `distro.fedora` + the `rpm` format template). **No main → fedora coupling** (cleaner than cachyos): nothing in main consumes any showcase image, so the only edge is `fedora → main`; main remote-includes nothing from the new repo. Tag note: layer refs + `build.yml` pin to the ecosystem layer tag `v2026.141.1600`; the `fedora-base.yml` FILE include pins to a fresh main tag (the file does not exist at `v2026.141.1600`, so a new tag carries it) — exactly as main includes `cachyos-base.yml` at its own tag while layers stay at `v2026.141.1600`. The now-redundant `fedora-remote` mixed-version remote-ref test fixture was DELETED (the submodule, composed entirely by `@github` ref, is a more thorough remote-ref test). The `composition-import-selftest` recipe in `eval.yml` was repointed from the relocated `fedora-coder` to a new in-main `composition-source` fixture image. No `MigrationStep` (relocation of repo-internal definitions, not a user-facing schema change). See `/ov-distros:fedora`, `/ov-distros:fedora-builder`, `/ov-distros:fedora-nonfree`, `/ov-coder:fedora-coder`, `/ov-distros:fedora-ov`, `/ov-distros:fedora-test`.

### 2026-05-21 — Debian + Ubuntu images → `overthinkos/debian` + `overthinkos/ubuntu` submodules

the entire deb-family moved OUT of the main repo into TWO dedicated repos (one per distro, matching the per-distro precedent set by `arch` ≠ `cachyos`): **`overthinkos/debian`** (submodule at **`image/debian`**) and **`overthinkos/ubuntu`** (submodule at **`image/ubuntu`**), each with its own canonical `overthink.yml` (directly buildable: `ov -C image/debian image build debian`). Moved into `overthinkos/debian`: the `debian` base image, `debian-builder`, `debian-coder`, `debian-debootstrap` + `debian-debootstrap-builder`, the `debian-debootstrap` VM, and the `debian-debootstrap-vm` deploy bed. Moved into `overthinkos/ubuntu`: the analogous `ubuntu`/`ubuntu-builder`/`ubuntu-coder`/`ubuntu-debootstrap`(+builder), the `ubuntu-debootstrap` VM, and the `ubuntu-debootstrap-vm` bed. Each submodule composes the SAME layers — none were copied — by **git reference** (`@github.com/overthinkos/overthink/layers/<name>:<tag>`, all pinned to one main tag) and remote-includes the shared `build.yml` (which keeps BOTH the `debian` and `ubuntu` distro configs + the `deb` format + the `debootstrap` builder template). **Unlike Arch and CachyOS, the Debian/Ubuntu bases MOVED but created NO back-coupling**: nothing in main consumes any deb-family image (no `base: debian`/`base: ubuntu` image stays in main), so the only edge is `debian → main` / `ubuntu → main`; main remote-includes nothing from either new repo, and neither new repo references the other (the `ubuntu`-`debian` link is purely `distro.ubuntu: {inherits: debian}` inside the single shared `build.yml`). The bases root at the upstream `docker.io/debian:13` / `docker.io/ubuntu:24.04` images directly, so neither repo needs a `*-base.yml` remote include. No cyclic image OR builder deps. No `MigrationStep` (relocation of repo-internal definitions, not a user-facing schema change). See `/ov-distros:debian`, `/ov-distros:ubuntu`, `/ov-distros:debian-debootstrap`, `/ov-distros:ubuntu-debootstrap`, `/ov-coder:debian-coder`, `/ov-coder:ubuntu-coder`, `/ov-vm:debian`, `/ov-vm:ubuntu`.

### 2026-05-21 — CachyOS → `overthinkos/cachyos` submodule + kind:local remote-ref collection

ALL CachyOS entities moved OUT of the main repo into the dedicated **`overthinkos/cachyos`** repo, mounted as a git submodule at **`image/cachyos`** with its own canonical `overthink.yml` (directly buildable: `ov -C image/cachyos image build cachyos`). Moved: the `cachyos` base image (now in the submodule's `cachyos-base.yml`), `cachyos-pacstrap-builder`, `cachyos-pacstrap`, the `cachyos-vm` entity + `cachyos-vm-deploy` bed, AND the operator workstation profile `ov-cachyos` (the `kind: local` template + its `target: local` deploy — run it as `ov -C image/cachyos update ov-cachyos`). The submodule composes the SAME layers + the shared `build.yml` (which keeps the `cachyos` distro config) + the `arch` base (`arch-base.yml`) by **git reference**, pinned to one main tag. **Unlike Arch, the `cachyos` base MOVED** (Arch's stayed): because main's `versa` is `base: cachyos`, main's `overthink.yml` pulls the base back via a remote `include:` of `cachyos-base.yml` — a deliberate **main → cachyos** coupling (NOT a resolution cycle: single-file includes; image DAG `versa → cachyos → docker.io/cachyos-v3` is acyclic). `versa` now **inherits** its `builder:` map (→ `arch-builder`) from the cachyos base instead of declaring an override. This cutover surfaced + fixed a real `ov` gap (R2): `CollectRemoteRefs` (`ov/refs.go`) + `validateLocalTemplates` (`ov/validate.go`) now walk `kind: local` template `layer:` lists — `Config` gained a `Local` field populated by `ProjectConfig()` — so an `ov-cachyos`-style template can compose remote `@`-ref layers exactly like an image (pure capability addition; no schema change, no `MigrationStep`). No cyclic image OR builder deps. (Follow-up, same day: the `cachyos-pacstrap`/`cachyos-vm` pacstrap-from-scratch paths — previously blocked by an `x86_64_v3` architecture rejection + a GPGME failure on the VM path — now build end-to-end. Root cause was a duplicated, diverged pacman.conf renderer; consolidated into one `renderPacstrapExtraConf` (`ov/build.go`) shared by `runPrivilegedBootstrap` + `vm_bootstrap.go` that derives `[options] Architecture` from the cachyos-v3 microarch repos AND always emits per-repo `SigLevel` (the VM path had dropped it). Pure ov-binary fix — no `build.yml`/submodule re-pin. The same session swept the stale `vms.yml` → `vm.yml` filename/key references left by the per-kind-file-split cutover.) See `/ov-distros:cachyos`, `/ov-vm:cachyos`, `/ov-local:ov-cachyos`, `/ov-versa:versa`.

### 2026-05-21 — Arch images → `overthinkos/arch` submodule + forward-version load gate

every `archlinux`-rooted CONSUMER image (`arch-coder`, `arch-ov`, `arch-test`, `archlinux-pacstrap-builder`, `archlinux-pacstrap`) plus the Arch cross-kind beds (`vm: arch`, `deploy: arch-vm` incl. nested `arch-host`, `deploy: arch-pacstrap-vm`, the `arch-coder` eval imports) moved OUT of the main repo into the dedicated **`overthinkos/arch`** repo, mounted as a git submodule at **`image/arch`** with its own canonical `overthink.yml` (directly buildable: `cd image/arch && ov image build arch-coder`). The new repo composes the SAME layers — none were copied — by **git reference** (`@github.com/overthinkos/overthink/layers/<name>:<tag>`, all pinned to one main tag; `CollectRemoteRefs` rejects a bare ref at two versions). The `archlinux` base + `archlinux-builder` (the builder) **stay in the main repo** and are pulled into the submodule via a remote `include:` of a new main-repo `arch-base.yml` (whose builder layers are git-ref'd so they resolve in the consuming submodule). No cyclic image OR builder deps (base needs no builder; builder self-reference is filtered; `yay` bootstraps via `makepkg`, not `aur:`). (CachyOS was subsequently split out the same way — see the CachyOS note above.) No `MigrationStep` (relocation of repo-internal definitions, not a user-facing schema change). Separately, `LoadUnified` gained a **forward-version gate**: a config whose CalVer is NEWER than `LatestSchemaVersion()` now hard-fails with "config schema X is newer than this ov supports (max Y); update ov" instead of a cryptic parse error — older/unparseable still routes to `ov migrate`. See `/ov-distros:archlinux`, `/ov-coder:arch-coder`.

### 2026-05-21 — CalVer schema versioning + single `ov migrate`

the YAML schema version moved from an integer (`version: 4`) to a **CalVer string** (`version: 2026.141.1530`) — the same `YYYY.DDD.HHMM` scheme as image tags (`ov/version.go` gains `ParseCalVer` / `CalVer.Less`). Every versioned file (`overthink.yml` + per-kind `image.yml`/`deploy.yml`/`vm.yml`/`pod.yml`/`k8s.yml`/`local.yml` + per-host `~/.config/ov/deploy.yml`) carries the stamp. The ~16 hand-invoked `ov migrate <name>` sub-verbs collapsed into a **single idempotent `ov migrate`** that runs an ordered, CalVer-keyed migration chain (`ov/migrate_registry.go`) — every historical cutover is one `MigrationStep` stamped with the date it landed, replayed in order up to HEAD (`LatestSchemaVersion()`). `ov migrate` always migrates, and only ever to the latest CalVer; a remote-cache fetch auto-runs the project-only subset (no host mutation). The load-time gate (`LoadUnified`) now compares the file's CalVer against `LatestSchemaVersion()` and every residual-key error points uniformly at bare `ov migrate`. Adding a future cutover = append ONE `MigrationStep` (the `calver-schema` stamp stays last). Migration: `ov migrate` (idempotent; the final `calver-schema` step rewrites `version: 4` → the HEAD CalVer line-by-line, preserving comments). See `/ov-build:migrate`.

### 2026-05-21 — Drop direct KeePass `.kdbx` credential backend — Secret Service + GPG only

the direct `.kdbx` file backend (`gokeepasslib`-based `KdbxStore`, kernel-keyring master-password cache in `keyctl.go`, the `--kdbx` global flag, `OV_KDBX_*` env vars, the `secrets_kdbx_path` / `secrets_kdbx_key_file` / `kdbx_cache` / `kdbx_cache_timeout` settings keys, and `secret_backend: kdbx`) was deleted. The credential hierarchy is now env var → **Secret Service keyring** (GNOME Keyring / KDE Wallet / **KeePassXC via FdoSecrets** — unaffected) → **config-file plaintext fallback** (headless last-resort). `secret_backend` ∈ {`auto`, `keyring`, `config`}. The `ov secrets get/set/list/delete/import/export` commands were retargeted from `KdbxStore` to the active `DefaultCredentialStore()`; `ov secrets init` / `ov secrets path` were removed; `ov secrets gpg …` is unchanged. Residual `secret_backend: kdbx` or `secrets_kdbx_*` keys raise a hard load-time error in `LoadRuntimeConfig` (`validateNoKdbxResiduals`) pointing at the migration. An existing `.kdbx` keeps serving the SAME secrets with zero data copy by exposing it through KeePassXC's FdoSecrets (Secret Service). Migration: `ov migrate` (idempotent; strips the residual keys from `~/.config/ov/config.yml`, writes a `.bak.<ts>`). See `/ov-build:secrets`, `/ov-build:settings`.

### 2026-05-12 — Required `image:` field on pod-target deploys + deploy-key independence

parallel to the cross-kind name-reuse rule ("a single name MAY exist as both an image and a deploy"), the `target: pod` deploy schema now hard-requires the `image:` field (load-time error if absent) AND the deploy KEY is independent of `image:`. Two patterns are first-class: **Pattern A — multiple instances** of the same image via `<base>/<instance>` deploy keys (`versa`, `versa/ecovoyage`, `versa/another-tenant`, all `image: versa`); **Pattern B — arbitrary deploy name + version pin** (`versa-pinned-2026.131.2134:` with `image: ghcr.io/overthinkos/versa:2026.131.2134`). Container name is always `ov-<key-with-slash-replaced-by-dash>`. Pre-cutover, the eval runner silently fell back to `containerImageRef()` when no `image:` was declared, which read the stale OCI label off volume-pinned containers and dropped any probes added since the seed image. The cutover deletes the implicit fallback so the runner inspects what the operator declared, not what the container happens to be. Migration: `ov migrate` (idempotent; injects `image:` into legacy entries). See `/ov-core:deploy` "Two supported deploy patterns" + `/ov-versa:versa` "Multi-instance pattern" / "Pinned-version pattern".

### 2026-05-05 — Cross-kind name reuse + overthink.yml-only authoring

schema v4 always permitted same-name entities across the seven namespaces (layer / image / pod / vm / k8s / local / deploy), but `ResolveDeployRef` errored on simultaneous image + layer with the same name and eight authoring verbs still defaulted to legacy per-kind files. This cutover (a) makes `ResolveDeployRef` deterministic — image-first for the primary `<ref>`, with `ResolveDeployRefAsLayer` for `--add-layer` — so a layer and an image can share a name; (b) flips every authoring verb (`ov image set`, `ov image new project`, `ov image new image`, `ov image add-layer`, `ov image rm-layer`, `ov vm import`, `ov vm update`, `ov vm clone`) to default to `overthink.yml`; (c) renames the operator-specific `qc` deployment key to `cachyos-dx` so the kind:local template and the kind:deploy entry that applies it share the same name (concrete demonstration of the policy).

### 2026-05-05 — Engineering-discipline cutover

R1–R10 reordered — engineering discipline (RCA-on-failure, no-"pre-existing", no-duplication, no-workarounds, hard-cutover-with-stale-references) lifted to R1–R5; runtime verification merged into R6–R9; R10 (verify-on-disposable + fresh-rebuild) byte-identical and remains the final acceptance gate. New skill `/ov-internals:strict-policy` operationalizes R1–R5. AI Attribution table closed: any R1–R10 OR Clean Architecture violation FORBIDS commit at any tier — no "downgrade and ship" escape, no "lower tier" workaround. Suggesting any such workaround is itself a violation. Documentation-only cutover; no code paths change.

### 2026-05-03 — Local cutover (`kind: host` → `kind: local`)

`kind: host` renamed to `kind: local`; `host.yml` → `local.yml`; `target: host` → `target: local`. The `host:` field on deployments now means **destination machine** (Ansible-style): `host: local` (literal, default) → direct shell, anything else → SSH via `ssh(1)` reading `~/.ssh/config` + ssh-agent. New deployment fields: `local: <template>`, `user: <ssh-user>`, `ssh_args: [-o, ProxyJump=...]`. Skills renamed: `host-deploy` → `local-deploy`, `host-infra` → `local-infra`. New skill: `local-spec`. ov contains zero custom SSH-key resolution — `ov vm create` writes a managed Host stanza to `~/.config/ov/ssh_config`, and `~/.ssh/config` Includes it. Deprecated `status:`/`info:` scalar fields and `VmDeployState.ssh_key_path` deleted; `description.tag` (`working`/`testing`/`broken`) carries the rollup. Migration: `ov migrate` (idempotent).

### 2026-05 (day unspecified) — Plugin use-case reorganization (marketplace v3.0.0)

plugins re-sorted into four use-case buckets — **commands** (`ov-core`, `ov-build`, `ov-eval`, `ov-automation`), **kind** (`ov-image`, `ov-vm`, `ov-kubernetes`, `ov-local`, `ov-pod`), **development** (`ov-internals`), **images** (`ov-distros`, `ov-languages`, `ov-infrastructure`, `ov-tools`, plus the per-pod plugins). `ov-foundation` (79-skill mega-plugin) split into `ov-distros` / `ov-languages` / `ov-infrastructure` / `ov-tools`. `ov-vms` folded into `ov-vm`. `ov-advanced` retired; its skills split between `ov-eval` (live probes), `ov-automation` (topic flags + tmux/alias/udev), and the kind plugins (`ov-vm`, `ov-kubernetes`, `ov-local`). `ov-build` schema-authoring skills (`image`, `layer`, `local-spec`) moved to dedicated `ov-image` / `ov-local` kind plugins; `ov-build:eval` orchestrator moved to `ov-eval`. `ov-dev` renamed to `ov-internals`. New `ov-pod` kind plugin (thin pointer to `/ov-core:deploy`). Directory names dropped the `ov-` prefix (`plugins/jupyter/`, `plugins/core/`, `plugins/distros/`) while plugin.json `name:` fields kept it (`name: ov-jupyter`, `name: ov-core`, `name: ov-distros`); the result is the same `/ov-<plugin>:<skill>` invocation surface for every skill, with a cleaner `ls plugins/`. Skill-name collisions (`tmux`, `dbus`, `openclaw`, `vms`, `generate`) renamed for global uniqueness: `tmux-layer` and `dbus-layer` in `ov-infrastructure`, `openclaw-deploy` in `ov-automation`, `vms-catalog` in `ov-vm`, `generate-source` in `ov-internals`. Marketplace bumped to v3.0.0.

### 2026-05 (day unspecified) — Init-system polymorphism + ov-cachyos rename

the `*-host` sibling-layer pattern (`virtualization`/`virtualization-host`, `ov-full`/`ov-full-host`) was deleted. Both pairs merge into ONE canonical layer that handles supervisord (containers/pods) AND systemd (host installs / bootc / VMs) via the **mixed `service:` schema pattern** — same `name:`, two entries, one with `use_packaged:` (systemd render), the other with custom `exec:` (supervisord render); init system at deploy time picks the matching form. The `cachyos-dx` deployment + kind:local template renames to `ov-cachyos` (matches the `ov-<flavor>` naming used by `ov-full`/`ov-mcp`). Consolidated migration: `ov migrate` (idempotent; collapses both qc → ov-cachyos and cachyos-dx → ov-cachyos rename hops). Residual `deploy.qc`, `deploy.cachyos-dx`, `local.cachyos-dx` raise hard load-time errors pointing at the migration command.

### 2026-05 (day unspecified) — Per-kind file split + `kind: deployment` → `kind: deploy` rename

the per-kind file convention now mandates `image.yml` / `pod.yml` / `vm.yml` / `k8s.yml` / `local.yml` / `deploy.yml` as siblings of `overthink.yml`, all reachable via `include:`. The schema kind formerly known as `deployment` is now `deploy` — every `kind: deployment` doc + every `deployment:` root key + every `yaml:"deployment"` Go struct tag was renamed in the same atomic cutover. (A short-lived `ov eval kind <kind>` verb dispatched the per-kind R10 sequence; it was RETIRED 2026-05 when its hardcoded bed table was dropped and the beds became `kind: eval` entities in `eval.yml`, run via `ov eval run <bed>` — see the 2026-05-22 kind:eval note above.) Migration: `ov migrate` (idempotent; combined extract-from-overthink.yml + create-stubs + rename-kind-deployment-to-deploy hop). Residual `kind: deployment` docs and root `deployment:` keys raise hard load-time errors pointing at the migration command.

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
- **R9 — deployed binary matches source; runtime deps in package management.** `ov eval spice status` once returned the OLD binary's output against a remote host while success was claimed — the new code had been synced but not rebuilt. Separately, virt-manager needed `nc` on the libvirt host; a manual install would have silently broken virt-manager on the next freshly-installed synced host (the fix was to declare the dep in `pkg/arch/PKGBUILD` `depends=`, the single source of truth — per-distro shell shims that once duplicated this list have been retired).

## Earlier schema cutovers (date approximate)

### VM schema hard cutover — `VmConfig` / `image.vm` / `image.libvirt` → `kind: vm` + `VmSpec`

The reference implementation of the hard-cutover policy. One PR deleted the legacy VM surface and replaced it with `kind: vm` entities:

- **Code deletions**: `VmConfig` struct (`ov/config.go`); `ImageConfig.Vm`, `ImageConfig.Libvirt`, `ResolvedImage.Vm` fields; `resolveVmConfig`; `LabelVm`, `LabelLibvirt` constants (`ov/labels.go`); `CapabilityLabelMap` entries for `Vm`/`Libvirt`; image-level libvirt validation (`ov/validate.go`) and iteration (`ov/libvirt.go`).
- **Schema deletions**: `image.bootc: true` + `image.vm: {...}` + `image.libvirt: [...]` — all rejected by the loader with hard errors.
- **Replacement surface**: `kind: vm` entities; `VmSpec` + `VmSource` + `LibvirtConfig` + `VmCloudInit` (`ov/vm_spec.go`, `ov/cloud_init_types.go`, `ov/libvirt_schema.go`); `vm:<name>` deploy target via `VmDeployTarget`.
- **Migration**: `ov migrate` (`ov/migrate_vm_spec.go`), idempotent — harvests legacy fields into `vm:` entries, preserves pre-existing keys, never clobbers user customizations.
- **Load-time error**: `image entry "foo" declares legacy field "bootc: true". Run: ov migrate`.
- Commit graph: `089f375` (new VmSpec surface lands alongside legacy) → `b249ee4` (arch live-tested + migrate authored) → `3087e0a feat(ov)!: hard cutover — delete VmConfig, ImageConfig.Vm/Libvirt, OCI labels`.

### Unified YAML cutover

Legacy `image.yml` / `build.yml` / flat-form `layer.yml` → `overthink.yml` with kind-keyed wrappers + `include:` + `discover:`. Migration: `ov migrate`.

### Unified `service:` schema cutover

Legacy `service: |...|` raw INI and `system_services:` → a structured `service:` list (incl. `kind: eventlistener`). Folded into `ov migrate`.

### User-policy cutover

Rename-based user renaming → declarative `base_user:` + `user_policy:` matrix. No separate migration; hard-cutover delete + skill updates.

## Layer / image / command history (relocated from skills)

Concise records of changes formerly narrated inside individual skills. Current behavior is documented in each skill; the change history lives here.

- **Power-user images dropped the privileged posture** — `fedora-coder`, `fedora-ov`, `arch-ov`, `githubrunner` dropped the legacy `uid: 0 / root` + `cap_add: [ALL]` + `security_opt: [label=disable, seccomp=unconfined]` posture once the `/ov-distros:container-nesting` kernel-level RCA proved uid-delegation via subuid/subgid ranges (+ `unmask=/proc/*`) is sufficient. They now run rootless (uid 1000) with passwordless sudo.
- **Dev/MCP images dropped `network: host`** — `fedora-coder` / `arch-ov` and the coder family now default to the `ov` bridge with explicit `port:` mappings (the right way to expose sshd / ov-mcp).
- **`requires: python` (pixi-python) dependency dropped** from `language-runtimes`, `uv`, and `supervisord` — these no longer pull the `python`→`pixi`→conda-forge env (~500 MB); consumers get only the system / RPM Python stack, dropping hundreds of MB across the catalog.
- **`uv` install method** changed from a `pixi.toml` (conda-forge env) to a direct binary download (matching `typst` / `pixi`).
- **Git tooling consolidated into `/ov-coder:gh`** — `gh`, `git-lfs`, and the git-lfs post-install task moved out of `/ov-coder:dev-tools` (which had duplicated them, causing a `gh-binary` test-id collision); `gh` is now the single owner.
- **`ov-mcp` mount path `/project` → `/workspace`** — the in-container project bind mount is `/workspace`; the auto-fallback to `overthinkos/overthink` fires whenever cwd has no `image.yml`; the host-networked-container URL rewrite (`rewriteMCPURLForHost`) handles empty `NetworkSettings.Ports` via `HostConfig.NetworkMode` detection.
- **jupyter MCP client-side room-management removed** — `room_open` / `room_close` / `room_close_all` / `room_pick` were deleted; the MCP server auto-attaches to a single room, sets cells in place (no delete-then-insert phantom-cell residue), and mints stable file_ids (no host-path leak). The layer ships 11 tools.
- **pixi runtime-env contract moved from the pixi LAYER to the pixi BUILDER** so images consuming pixi via pixi.toml-triggered builds get the env contract automatically.
- **Airflow MCP wrapper removed** — the `mcp-server-apache-airflow` wrapper was dropped (no Airflow-3 `/api/v2` release exists); the airflow layer publishes no MCP.
- **versa GPU-library set** — cuGraph / cuML / PyG / graphistry were installed where a working Linux-cp313 CUDA-13 wheel exists upstream; libraries without one (DGL, PyTorch3D, FAISS-GPU, pyg-lib, torch-spline-conv) are deferred until wheels ship.
- **NVIDIA GPU-injection consolidated** — the 10 previously-scattered GPU device-injection sites collapsed into `appendAutoDetectedEnv()`.
- **`container-nesting` subuid range** — the delegation range must fit inside the outer namespace's keep-id window (an earlier `524288:65536` range fell outside it and caused a `newuidmap` write failure); Arch images must declare `podman` + `crun` explicitly (a fedora-pacman population once pulled `docker` and omitted `crun`).
- **`keepassxc` extracted into its own layer** from `/ov-selkies:desktop-apps` (which had bundled it with btop / chromium / cockpit / transmission / vlc / zsh).
- **`keepassxc-keyring` direnv hooks** — the inline `cmd:` heredocs that wrote direnv-hook blocks were removed; the responsibility lives in the direnv layer's `shell:` block.
- **`openwebui` admin password** auto-generates as a 32-byte hex random value (`WEBUI_ADMIN_PASSWORD`).
- **Data-seeding fix** — earlier `ov` versions seeded data layers only for bind mounts, silently skipping named volumes; the fix seeds named volumes too, so previously-unseeded named volumes get their starter content on the first `ov config` / `ov update` after upgrading.
- **`ov` credential keyring iteration** — `ov` originally depended on `zalando/go-keyring`, which looks up only the Secret Service `default` alias; a broken / stub `default` collection made every lookup fail and `ov config mount` hang forever. `ov` now iterates collections with a bounded deadline.
- **Eval R10 benchmark wall-clock** — a measured R10 score round solved 92/92 across 9 iterations in ~5h33m on a `disposable: true` eval-pod; the per-phase expectation table in `/ov-eval:eval` derives from it.
