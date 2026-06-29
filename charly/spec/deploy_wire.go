package spec

import "encoding/json"

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
//
// Substrate carries a substrate-SPECIFIC preresolved payload a registered
// host-side deploy preresolver produced for the external substrate word (e.g.
// AndroidDeployVenue for deploy:android — the resolved adb endpoint + the apk
// install specs). It is OPAQUE to the generic externalDeployTarget (which never
// branches on the substrate); only the matching plugin decodes it. nil for an
// external substrate with no preresolver (the marker-only example).
type DeployVenue struct {
	DeployName string            `json:"deploy_name"`
	Env        map[string]string `json:"env,omitempty"`
	Substrate  json.RawMessage   `json:"substrate,omitempty"`
}

// AndroidDeployVenue is the preresolved deploy:android substrate payload the
// host's android deploy preresolver produces (in DeployVenue.Substrate) and the
// candy/plugin-adb deploy:android provider decodes. The host resolves the device
// endpoint (adb-server addr / in-pod engine+container / serial / google-play
// creds) and collects the apk install specs from the deploy's compiled plans
// (committed-APK Apk fields rewritten to ABSOLUTE host paths the plugin reads;
// package entries carry package/source/arch/app_version), so the plugin needs no
// project context and no goadb-less host resolution — it only drives the device.
type AndroidDeployVenue struct {
	AdbAddr     string           `json:"adb_addr"`
	Engine      string           `json:"engine,omitempty"`
	Container   string           `json:"container,omitempty"`
	Serial      string           `json:"serial,omitempty"`
	GoogleEmail string           `json:"google_email,omitempty"`
	GoogleToken string           `json:"google_token,omitempty"`
	Installs    []ApkPackageSpec `json:"installs,omitempty"`
	// BootTimeout / InstallDeadline / InstallInterval are the readiness +
	// install-retry windows the host ships (no magic numbers in the plugin):
	// boot gates sys.boot_completed; the install retries past PackageManager
	// post-boot init (a real synchronization condition, not a fixed sleep).
	BootTimeout     string `json:"boot_timeout,omitempty"`
	InstallDeadline string `json:"install_deadline,omitempty"`
	InstallInterval string `json:"install_interval,omitempty"`
}

// K8sDeployVenue is the preresolved deploy:k8s substrate payload the host's k8s
// deploy preresolver (charly/k8s_deploy_preresolve.go) produces in
// DeployVenue.Substrate and the candy/plugin-kube deploy:k8s provider decodes.
//
// Unlike deploy:android — where the plugin DRIVES the device and the host only
// resolves the endpoint + apk specs — the k8s Kustomize GENERATOR
// (GenerateK8sKustomize) stays in charly's core: it consumes the package-main
// Capabilities/BoxMetadata type (read from the image's OCI labels via
// ExtractMetadata) AND the CUE egress gate (#K8sObject / #Kustomization) the
// out-of-process plugin cannot reach, AND it has a SECOND in-core consumer
// (`charly bundle from-box --target k8s`, k8s_deploy_from_box.go). So the host
// generates the egress-validated Kustomize tree under .opencharly/k8s/<name>/ and
// ships only the resolved overlay path + tree root. The plugin owns the LIVE
// cluster I/O — `kubectl apply -k <OverlayPath>` at deploy, and the recorded
// teardown (`kubectl delete -k` + remove the tree) replayed at `charly bundle del`
// — the k8s analogue of plugin-adb installing apps after the host resolves the
// specs. The host generates what needs core machinery; the plugin does the live
// external-system I/O it owns.
type K8sDeployVenue struct {
	OverlayPath string `json:"overlay_path"`           // <root>/overlays/<inst> — the `kubectl apply -k` argument
	TreeRoot    string `json:"tree_root,omitempty"`    // <root> = .opencharly/k8s/<name> — removed at teardown
	KubeContext string `json:"kube_context,omitempty"` // kind:k8s template's kubeconfig_context → `kubectl --context` (empty → current-context)
	DeployName  string `json:"deploy_name,omitempty"`  // for plugin-side log messages
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

// ---------------------------------------------------------------------------
// Out-of-proc build-time wire — what a plugin verb/builder exchanges with the
// host across the go-plugin boundary on an OpEmit Invoke at IMAGE BUILD time.
// ---------------------------------------------------------------------------

// BuildEnv is the build-context descriptor the host puts in op.Env for an OpEmit
// Invoke at image-generation time: the image's distro tags + name, so a plugin can
// tailor its emitted Containerfile fragment per distro/arch. The build-time
// analogue of DeployVenue (deploy) / the verb check-env. Placement-agnostic: a
// builtin reads it in-proc, an external over gRPC.
type BuildEnv struct {
	Distros []string `json:"distros,omitempty"`
	Image   string   `json:"image,omitempty"`
}

// EmitReply is what a plugin verb/builder returns from an OpEmit Invoke at build
// time: a verbatim Containerfile FRAGMENT (RUN/COPY/… directives) the generator
// splices into the emitted .build/<image>/Containerfile (egress-validated with the
// rest of the Containerfile before it hits disk). This is the build-context
// counterpart of a builtin verb's ProvisionActor.RenderProvisionScript (a shell
// RUN) generalized to any directive an external plugin owns. The host appends a
// trailing newline if absent.
type EmitReply struct {
	Fragment string `json:"fragment"`
}

// BuilderResolveReply is what an external builder plugin returns from an OpResolve
// Invoke at image-generation time — the build-time BUILDER leg, the multi-stage
// counterpart of a verb/step's EmitReply. Stage is the `FROM <ref> AS <name>` block
// (its RUN/COPY body included) spliced PRE-main-FROM by emitExternalBuilderStages
// (alongside the embedded builder: vocabulary's StageTemplate output); CopyArtifacts
// are the `COPY --from=<stage> …` directives spliced POST-main-FROM by
// emitExternalBuilderArtifacts to pull the built artifacts into the final image. A
// candy SELECTS the external builder via its `external_builder:` field; the same
// reply travels both splice points (cached per candy on the Generator). Both the
// Stage and the CopyArtifacts are egress-validated with the rest of the Containerfile
// before it hits disk.
type BuilderResolveReply struct {
	Stage         string   `json:"stage"`
	CopyArtifacts []string `json:"copy_artifacts,omitempty"`
}
