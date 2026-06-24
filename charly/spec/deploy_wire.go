package spec

// deploy_wire.go — the deploy IR wire types shared between charly's core
// (package main) and the plugin SDK / out-of-tree plugins.
//
// These types live in package spec — the ONE importable home — because BOTH
// the host (package main, via the `= spec.X` alias surface) AND an external
// deploy/step/builder plugin (through charly/plugin/sdk, which imports spec)
// construct and exchange them across the go-plugin process boundary. There is
// NO duplicate type for any of these concepts (R3): package main aliases them,
// the SDK references them directly.
//
// Moved here from package main install_plan.go: Scope, ReverseOpKind + its
// constants, and ReverseOp. Added here for the out-of-proc deploy wire:
// InstallPlanView, DeployVenue, DeployReply, DeployReplyRecord.

// ---------------------------------------------------------------------------
// Scope — where a step's / reverse-op's effect lands on the target filesystem.
// ---------------------------------------------------------------------------

// Scope classifies what kind of filesystem mutation a step makes. Steps are
// grouped by scope (and venue) when the host target batches into sudo vs user
// heredocs — mixing scopes in one batch would need per-command sudo. It is the
// integer-valued enum the ledger serializes (omitempty omits ScopeSystem=0), so
// the wire form is UNCHANGED by the move into spec.
type Scope int

const (
	// ScopeSystem mutates global host state: /etc, /usr, /var, systemd system
	// units, package DB. Requires sudo on host; emitted as USER root in the
	// Containerfile.
	ScopeSystem Scope = iota

	// ScopeUser mutates the invoking user's home or user-owned paths:
	// $HOME/.pixi, $HOME/.cargo, $HOME/.npm-global, $HOME/.local, systemd
	// user units, etc. No sudo needed on host; emitted as USER ${UID} in the
	// Containerfile.
	ScopeUser

	// ScopeUserProfile writes to the user's shell init surface:
	// ~/.bashrc / ~/.zshenv / fish conf.d + ~/.config/opencharly/env.d/.
	// Separate from ScopeUser because the host target has special handling
	// (managed blocks, shell detection) and the OCI target renders these as
	// ENV directives + path additions rather than file writes.
	ScopeUserProfile
)

func (s Scope) String() string {
	switch s {
	case ScopeSystem:
		return "system"
	case ScopeUser:
		return "user"
	case ScopeUserProfile:
		return "user-profile"
	}
	return "unknown"
}

// ---------------------------------------------------------------------------
// ReverseOp — what the ledger records to un-do a step at teardown time.
// ---------------------------------------------------------------------------

// ReverseOpKind discriminates the kinds of teardown actions Reverse() produces.
// Ledger entries serialize these verbatim so a later `charly bundle del` can
// walk them without re-compiling the plan.
type ReverseOpKind string

const (
	ReverseOpPackageRemove  ReverseOpKind = "package-remove"
	ReverseOpCargoUninstall ReverseOpKind = "cargo-uninstall"
	ReverseOpNpmUninstallG  ReverseOpKind = "npm-uninstall-g"
	ReverseOpPixiEnvRemove  ReverseOpKind = "pixi-env-remove"
	ReverseOpRmFileSystem   ReverseOpKind = "rm-file-system"
	ReverseOpRmFileUser     ReverseOpKind = "rm-file-user"
	ReverseOpRmDirRecursive ReverseOpKind = "rm-dir-recursive"
	ReverseOpServiceDisable ReverseOpKind = "service-disable"
	ReverseOpServiceRemove  ReverseOpKind = "service-remove"
	ReverseOpRemoveDropin   ReverseOpKind = "remove-dropin"
	ReverseOpRestoreEnabled ReverseOpKind = "restore-enabled"
	ReverseOpRemoveManaged  ReverseOpKind = "remove-managed-block"
	ReverseOpRemoveEnvdFile ReverseOpKind = "remove-envd-file"
	ReverseOpRemoveRepoFile ReverseOpKind = "remove-repo-file"
	ReverseOpCoprDisable    ReverseOpKind = "copr-disable"

	// ReverseOpPluginScript is the GENERIC recordable reverse op an external
	// (out-of-process) deploy/step/builder plugin returns: a shell script + its
	// scope, run verbatim at teardown via the ReverseExecutor (system → sudo,
	// user → no sudo). The script lives in Extra["script"]; Scope picks the
	// privilege. It preserves the record-and-replay invariant — only RECORDED
	// ops are replayed, never recomputed — without any new struct shape.
	ReverseOpPluginScript ReverseOpKind = "plugin-script"
)

// ReverseOpPluginScriptKey is the Extra map key carrying a ReverseOpPluginScript's
// shell-script body. Exported so both the host handler and the SDK builder name
// the one key (R3 — no magic-string drift across the process boundary).
const ReverseOpPluginScriptKey = "script"

// ReverseOp is a single teardown action. Serialized into the ledger so uninstall
// can reverse a deploy without re-reading the candy manifest.
type ReverseOp struct {
	Kind    ReverseOpKind     `json:"kind"`
	Format  string            `json:"format,omitempty"`  // package format for package-remove (rpm/deb/pac)
	Targets []string          `json:"targets,omitempty"` // package names, file paths, env names, …
	Scope   Scope             `json:"scope,omitempty"`   // system vs user for disambiguation
	Extra   map[string]string `json:"extra,omitempty"`   // op-specific details (e.g. unit name, layer name, plugin-script body)

	// UninstallCmd is the rendered host-venue package-removal command for a
	// ReverseOpPackageRemove op, filled at record time from the format's
	// uninstall_template (the embedded build vocabulary, charly/charly.yml) by
	// fillReverseUninstallCmds — the deploy target has the DistroConfig at
	// install time, the teardown (which reads the persisted ledger) does not, so
	// the command is rendered up front and persisted. reverse_ops.go runs it
	// verbatim, so there is NO hardcoded per-format removal switch in the
	// teardown path.
	UninstallCmd string `json:"uninstall_cmd,omitempty"`
}

// ---------------------------------------------------------------------------
// Out-of-proc deploy wire — what an external deploy provider exchanges with the
// host across the go-plugin boundary on an OpExecute Invoke.
// ---------------------------------------------------------------------------

// InstallPlanView is the JSON-roundtrippable wire VIEW of an InstallPlan. The
// rich in-core InstallPlan (package main) carries a `Steps []InstallStep`
// interface slice that cannot round-trip across the process boundary, so the
// host marshals this provenance-only view into op.Params and the plugin decodes
// it via sdk.DecodeInstallPlans. (Serializable per-step IR for external plugins
// that EXECUTE steps is a future cutover — this view proves the plan travels.)
type InstallPlanView struct {
	DeployID        string            `json:"deploy_id,omitempty"`
	Box             string            `json:"box,omitempty"`
	Version         string            `json:"version,omitempty"`
	Distro          string            `json:"distro,omitempty"`
	Candy           string            `json:"candy,omitempty"`
	CandiesIncluded []string          `json:"candies_included,omitempty"`
	AddCandies      []string          `json:"add_candies,omitempty"`
	BuilderImage    string            `json:"builder_image,omitempty"`
	Meta            map[string]string `json:"meta,omitempty"`
}

// DeployVenue is the venue descriptor the host puts in op.Env for an external
// deploy Invoke: the deploy's name plus the merged deploy-node env (KEY=VALUE
// lines flattened to a map). The plugin reads it to locate where to apply its
// effects (e.g. a scratch dir) — the analogue of snapshotCheckEnv for a verb.
type DeployVenue struct {
	DeployName string            `json:"deploy_name"`
	Env        map[string]string `json:"env,omitempty"`
}

// DeployReply is the structured result an external deploy provider returns from
// an OpExecute Invoke: the teardown ops the host records into the ledger, plus a
// provenance record. The host writes both via the SAME install_ledger.go path a
// built-in Add uses — identical persistence, record-and-replay preserved.
type DeployReply struct {
	ReverseOps []ReverseOp       `json:"reverse_ops,omitempty"`
	Record     DeployReplyRecord `json:"record"`
}

// DeployReplyRecord names the ledger CandyRecord the host writes for an external
// deploy: the logical candy whose ReverseOps drive teardown, plus its version.
type DeployReplyRecord struct {
	Candy   string `json:"candy"`
	Version string `json:"version,omitempty"`
}
