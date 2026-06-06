# ov/ — signpost (not the rule-set)

You are in the **Go CLI** source for `ov`.

**Load these skills FIRST (R0):**

- `/ov-internals:go` — source structure, building the `ov` binary, running
  tests, adding/modifying commands.
- `/ov-internals:install-plan` — the InstallPlan IR + DeployTarget /
  OCITarget pipeline (before touching `install_plan.go`, `install_build.go`,
  `build_target_oci.go`, `deploy_target_*.go`, `k8s_target.go`).
- `/ov-internals:capabilities` — the OCI-label capability contract.
- `/ov-internals:generate-source` — what `ov box generate` emits.
- Plus the renderer skills (`/ov-internals:vm-spec`, `cloud-init-renderer`,
  `libvirt-renderer`, `ovmf`) when touching VM/cloud-init code.

**Authoritative rules live in the repo-root `CLAUDE.md`** (one level up). R0–R10,
the hard-cutover policy, AI attribution, and the git-workflow are defined
there — this file only signposts and restates no rule. Go changes are R7/R8/R10
gated: `go build`/`go test` are cheap smoke, NOT the acceptance gate; the gate
is a live `ov eval run <bed>` on a `disposable: true` target. History lives in
`CHANGELOG.md`.
