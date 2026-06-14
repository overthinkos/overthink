package main

// install_plan.go — the InstallPlan IR.
//
// Background (see plan file referenced in the final design): today's code
// walks Candy objects and emits Containerfile text directly in
// generate.go:writeCandySteps. That hardcodes "we're building an OCI image"
// into the generator. The IR defined here lifts the walk into structured
// data so the same plan can be consumed by:
//
//   - OCITarget        → build-mode Containerfile emission (charly box build)
//   - ContainerDeploy  → deploy-mode overlay + quadlet (charly deploy add <name>)
//   - LocalDeployTarget → deploy-mode host execution (charly deploy add host)
//
// Keeping these three code paths behind one shared IR is the load-bearing
// move: every feature (service rendering, add_candy overlay, uninstall
// reversal) now lives in one place and applies to all three targets
// uniformly.
//
// This file defines only types and interfaces — no logic. The compiler that
// turns the candy manifest → InstallPlan lives in install_build.go; the emitters live
// in build_target_oci.go / deploy_target_pod.go / deploy_target_local.go.

import (
	"os"
	"strings"
)

// HomeToken is the deferred-home placeholder the compiler bakes into
// home-bearing step fields (env.d values, path_append entries, shell-snippet
// destinations) instead of expanding `~`/`$HOME` against a compile-time home.
// Each DeployTarget resolves it at emit time via InstallPlan.ResolveHome with
// the home of the ACTUAL destination — img.Home for the OCI/pod-overlay build,
// the host home for LocalDeployTarget, the GUEST home for VmDeployTarget. This
// is what lets a `target: vm` deploy write env.d that points at the guest
// user's home (/home/<guest-user>) rather than the host operator's home.
// The `{{.Home}}` spelling matches the existing builder-artifact convention
// (generate.go:expandBuilderPath), so the two token systems stay aligned.
const HomeToken = "{{.Home}}"

// ---------------------------------------------------------------------------
// Scope — where the effect lands on the target filesystem.
// ---------------------------------------------------------------------------

// Scope classifies what kind of filesystem mutation a step makes. Steps are
// grouped by scope (and venue) when the host target batches into sudo vs
// user heredocs — mixing scopes in one batch would need per-command sudo.
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
// Venue — where the step physically runs.
// ---------------------------------------------------------------------------

// Venue classifies where the step's commands actually execute. Today's
// generator has this distinction implicit in which rendering sub-pass
// produced the step (distro-tag section, format section, tasks, inline
// builder, multi-stage builder). The IR makes it explicit so the host
// target can pick the right execution strategy per step.
type Venue int

const (
	// VenueHostNative runs commands directly on the host (or as a RUN in
	// the Containerfile for the container target). The step's shell
	// commands execute natively; no isolation container.
	VenueHostNative Venue = iota

	// VenueContainerBuilder runs the step inside the existing multi-stage
	// builder image (fedora-builder / arch-builder / ...). On the
	// host target this means `podman run --user <host-uid> -v <paths>
	// <builder> bash -c "..."`. On the OCI target this is a FROM + RUN
	// pair that becomes its own build stage, with copy_artifacts pulling
	// outputs into the final image.
	VenueContainerBuilder

	// VenueSkip records the step with a reason but doesn't execute. Used
	// for container-runtime-only fields (ports:, volumes:, tunnel:, …)
	// when compiling for the host target, and for aur: on non-Arch hosts.
	VenueSkip
)

func (v Venue) String() string {
	switch v {
	case VenueHostNative:
		return "host-native"
	case VenueContainerBuilder:
		return "container-builder"
	case VenueSkip:
		return "skip"
	}
	return "unknown"
}

// ---------------------------------------------------------------------------
// Phase — three-phase execution within each step kind.
// ---------------------------------------------------------------------------

// Phase identifies which template phase (prepare / install / cleanup) a
// rendered step belongs to. The phase lets targets treat repo-config
// mutation (prepare) distinctly from package install — which is exactly
// the granularity --allow-repo-changes gates. A single format section in
// the candy manifest typically emits three PhasePrepare/PhaseInstall/PhaseCleanup
// steps, and the compiler tags each with the appropriate phase.
type Phase int

const (
	PhasePrepare Phase = iota
	PhaseInstall
	PhaseCleanup
)

func (p Phase) String() string {
	switch p {
	case PhasePrepare:
		return "prepare"
	case PhaseInstall:
		return "install"
	case PhaseCleanup:
		return "cleanup"
	}
	return "unknown"
}

// ---------------------------------------------------------------------------
// StepKind — discriminator for InstallStep implementations.
// ---------------------------------------------------------------------------

// StepKind names the concrete type behind an InstallStep. Used for
// ledger serialization and target-specific dispatch (e.g. HostDeploy
// knows to invoke the pixi builder differently than cargo).
type StepKind string

const (
	StepKindSystemPackages  StepKind = "SystemPackages"
	StepKindBuilder         StepKind = "Builder"
	StepKindOp              StepKind = "Op"
	StepKindFile            StepKind = "File"
	StepKindServicePackaged StepKind = "ServicePackaged"
	StepKindServiceCustom   StepKind = "ServiceCustom"
	StepKindShellHook       StepKind = "ShellHook"
	StepKindShellSnippet    StepKind = "ShellSnippet"
	StepKindRepoChange      StepKind = "RepoChange"
	StepKindApkInstall      StepKind = "ApkInstall"
	StepKindLocalPkgInstall StepKind = "LocalPkgInstall"
	StepKindReboot          StepKind = "Reboot"
)

// ---------------------------------------------------------------------------
// Gate — opt-in flag names for host-state-mutating operations.
// ---------------------------------------------------------------------------

// Gate is the name of a CLI flag that must be enabled for a given step to
// run on the host target. Steps without a gate run unconditionally; steps
// with a gate are skipped (with a warning) unless the user opts in.
//
// The OCI target ignores gates — container builds are already isolated,
// so gating would only slow down image construction without adding safety.
type Gate string

const (
	GateNone             Gate = ""
	GateAllowRepoChanges Gate = "allow-repo-changes"
	GateAllowRootTasks   Gate = "allow-root-tasks"
	GateWithServices     Gate = "with-services"
)

// ---------------------------------------------------------------------------
// ReverseOp — what the ledger records to un-do a step at teardown time.
// ---------------------------------------------------------------------------

// ReverseOpKind discriminates the kinds of teardown actions Reverse()
// produces. Ledger entries serialize these verbatim so a later
// `charly deploy del` can walk them without re-compiling the plan.
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
)

// ReverseOp is a single teardown action. Serialized into the ledger so
// uninstall can reverse a deploy without re-reading the candy manifest.
type ReverseOp struct {
	Kind    ReverseOpKind     `json:"kind"`
	Format  string            `json:"format,omitempty"`  // package format for package-remove (rpm/deb/pac)
	Targets []string          `json:"targets,omitempty"` // package names, file paths, env names, …
	Scope   Scope             `json:"scope,omitempty"`   // system vs user for disambiguation
	Extra   map[string]string `json:"extra,omitempty"`   // op-specific details (e.g. unit name, layer name)

	// UninstallCmd is the rendered host-venue package-removal command for a
	// ReverseOpPackageRemove op, filled at record time from the format's
	// uninstall_template (build.yml) by fillReverseUninstallCmds — the deploy
	// target has the DistroConfig at install time, the teardown (which reads
	// the persisted ledger) does not, so the command is rendered up front and
	// persisted. reverse_ops.go runs it verbatim, so there is NO hardcoded
	// per-format removal switch in the teardown path.
	UninstallCmd string `json:"uninstall_cmd,omitempty"`
}

// ---------------------------------------------------------------------------
// InstallStep — the primary IR element. Each step has one concrete type.
// ---------------------------------------------------------------------------

// InstallStep is the common interface every concrete step implements.
// Consumers (OCITarget / LocalDeployTarget) switch on Kind() to dispatch
// to the right rendering or execution path.
type InstallStep interface {
	// Kind returns the step's concrete type discriminator.
	Kind() StepKind

	// Scope classifies where the effect lands on the target filesystem.
	Scope() Scope

	// Venue classifies where the commands physically execute.
	Venue() Venue

	// RequiresGate names the opt-in flag that must be enabled, or
	// GateNone if the step can run unconditionally. Only consulted by
	// host-target emission; the OCI target ignores gates.
	RequiresGate() Gate

	// Reverse returns the teardown actions this step contributes to the
	// ledger. Called at install time (not at teardown) so the ledger
	// captures the exact reversal actions tied to the specific artifacts
	// created. Empty return value means no reversal is recorded (e.g.
	// phases that leave no state).
	Reverse() []ReverseOp
}

// ---------------------------------------------------------------------------
// SystemPackagesStep — rpm: / deb: / pac: package install.
// ---------------------------------------------------------------------------

// RepoSpec carries a structured repo entry from the candy manifest. Matches the
// existing raw shape used by templates in build.yml; the host target
// reads this to decide whether --allow-repo-changes is required.
type RepoSpec struct {
	// Fields captured verbatim from the candy manifest. Different formats use
	// different subsets (rpm has url/id/key; deb has suite/components;
	// pac has url/keys). Carried as map so the template-rendered host
	// and container forms can pick what they need.
	Raw map[string]any
}

// SystemPackagesStep installs packages via a distro package manager. One
// step typically expands to (PhasePrepare + PhaseInstall + PhaseCleanup)
// stages; the compiler emits one SystemPackagesStep per phase so the host
// target can gate PhasePrepare on --allow-repo-changes.
type SystemPackagesStep struct {
	Format   string     // "rpm" | "deb" | "pac"
	Phase    Phase      // PhasePrepare | PhaseInstall | PhaseCleanup
	Packages []string   // package names (for Reverse)
	Repos    []RepoSpec // repository entries from the candy manifest (drives PhasePrepare)
	Options  []string   // format-specific install flags
	Copr     []string   // RPM-only: COPR repos to enable/disable
	Modules  []string   // RPM-only: DNF modules to enable
	Exclude  []string   // RPM-only: packages to exclude
	Keys     []string   // PAC-only: GPG keys to trust

	// CacheMounts and RawInstallContext are passed to template rendering.
	// These are populated by the compiler and consumed by the OCI target;
	// the host target ignores CacheMounts (no BuildKit outside containers).
	CacheMount        []CacheMountSpec
	RawInstallContext map[string]any
}

func (s *SystemPackagesStep) Kind() StepKind { return StepKindSystemPackages }
func (s *SystemPackagesStep) Scope() Scope   { return ScopeSystem }
func (s *SystemPackagesStep) Venue() Venue   { return VenueHostNative }

func (s *SystemPackagesStep) RequiresGate() Gate {
	// Prepare phase mutates repo config; needs opt-in. Install/cleanup
	// phases are structurally inspectable package ops, no gate.
	if s.Phase == PhasePrepare && (len(s.Repos) > 0 || len(s.Copr) > 0 || len(s.Modules) > 0) {
		return GateAllowRepoChanges
	}
	return GateNone
}

func (s *SystemPackagesStep) Reverse() []ReverseOp {
	ops := []ReverseOp{}
	switch s.Phase {
	case PhaseInstall:
		if len(s.Packages) > 0 {
			ops = append(ops, ReverseOp{
				Kind:    ReverseOpPackageRemove,
				Format:  s.Format,
				Targets: s.Packages,
				Scope:   ScopeSystem,
			})
		}
	case PhasePrepare:
		// Repo/COPR additions recorded here; host target also emits a
		// separate RepoChangeStep when the PhasePrepare executes, which
		// records the concrete files that were written. The package
		// manager's COPR tracking is reversed via copr-disable.
		for _, c := range s.Copr {
			ops = append(ops, ReverseOp{
				Kind:    ReverseOpCoprDisable,
				Format:  s.Format,
				Targets: []string{c},
				Scope:   ScopeSystem,
			})
		}
	}
	return ops
}

// CacheMountSpec mirrors the BuildKit cache-mount configuration carried in
// build.yml format/builder definitions. The OCI target renders these as
// `--mount=type=cache,...`; the host target ignores them.
type CacheMountSpec struct {
	Dst     string
	Sharing string // "locked" | "private" | "shared" | ""
}

// ---------------------------------------------------------------------------
// BuilderStep — pixi / npm / cargo / aur multi-stage builds.
// ---------------------------------------------------------------------------

// ArtifactRef describes an output path from a builder that needs to be
// extracted back to the host or copied into the final OCI image.
//
// For user-scope pixi/npm/cargo on the host target: the host path is
// bind-mounted into the builder, so there's no "extraction" — the
// Artifacts list is empty because the bind-mount already put the files
// in place. For aur: the list contains /tmp/aur-pkgs/*.pkg.tar.zst,
// which the host target pulls out to a staging dir then hands to pacman.
type ArtifactRef struct {
	ContainerPath string // path inside the builder stage/container
	HostPath      string // path on the deploy target (host fs or final-image fs)
	Chown         bool   // whether to re-own to the target user
}

// BuilderStep runs a multi-stage builder (pixi/npm/cargo/aur). On the OCI
// target this emits a FROM + RUN stage plus a final-stage COPY --from.
// On the host target this emits a `podman run <builder>` with HOME-remap
// and appropriate bind-mounts.
type BuilderStep struct {
	Builder      string        // "pixi" | "npm" | "cargo" | "aur"
	BuilderImage string        // resolved image ref (e.g. "fedora-builder:2026.04")
	CandyName    string        // layer that triggered this builder (for ledger)
	CandyDir     string        // absolute layer source path (bind-mounted as /work)
	Phase        Phase         // typically PhaseInstall
	Artifacts    []ArtifactRef // outputs to extract (empty for user-scope pixi/npm/cargo; populated for aur)

	// Builder-specific template context — the compiler populates this from
	// the candy's manifest files + build.yml builder definition.
	RawStageContext map[string]any

	// LocalPkg is the package format's localpkg contract, populated by the
	// compiler for the `aur` builder only. The aur builder produces package
	// files (.pkg.tar.zst) that the host/VM deploy targets install onto the
	// venue via the SAME config-driven transfer+install leg the localpkg step
	// uses (R3) — so the install command + package glob come from
	// build.yml `pac.local_pkg`, never a hardcoded literal. Nil for
	// pixi/npm/cargo (home-artifact builders, no package-file install).
	LocalPkg *LocalPkgDef

	// BuilderDef is the resolved build.yml builder definition for this builder
	// (img.BuilderConfig.Builder[Builder]), populated by the compiler. The
	// host-venue deploy targets render its phase.install.host cell via
	// renderBuilderScript — the plain-shell analog of stage_template — so the
	// host build script is config-driven, not hardcoded Go. nil only on
	// synthetic test paths that don't supply a BuilderConfig.
	BuilderDef *BuilderDef
}

func (s *BuilderStep) Kind() StepKind { return StepKindBuilder }

func (s *BuilderStep) Scope() Scope {
	// aur produces system packages; others are user-scope.
	if s.Builder == "aur" {
		return ScopeSystem
	}
	return ScopeUser
}

func (s *BuilderStep) Venue() Venue       { return VenueContainerBuilder }
func (s *BuilderStep) RequiresGate() Gate { return GateNone }

func (s *BuilderStep) Reverse() []ReverseOp {
	switch s.Builder {
	case "aur":
		// aur packages get installed into the host package DB; reverse
		// is same as SystemPackagesStep. The compiler populates the
		// package name list into RawStageContext["packages"] so the
		// ledger records them.
		if pkgs := extractStringSlice(s.RawStageContext, "packages"); len(pkgs) > 0 {
			return []ReverseOp{{
				Kind:    ReverseOpPackageRemove,
				Format:  "pac",
				Targets: pkgs,
				Scope:   ScopeSystem,
			}}
		}
	case "pixi":
		// Pixi envs land at $HOME/.pixi/envs/<env-name>/ — the env name
		// comes from the candy's pixi.toml (project name) or defaults to
		// "default". Recorded under "env_name" in RawStageContext.
		if env := extractString(s.RawStageContext, "env_name"); env != "" {
			return []ReverseOp{{
				Kind:    ReverseOpPixiEnvRemove,
				Targets: []string{env},
				Scope:   ScopeUser,
				Extra:   map[string]string{"layer": s.CandyName},
			}}
		}
	case "cargo":
		// Cargo binaries: track by name from RawStageContext["binaries"].
		if bins := extractStringSlice(s.RawStageContext, "binaries"); len(bins) > 0 {
			return []ReverseOp{{
				Kind:    ReverseOpCargoUninstall,
				Targets: bins,
				Scope:   ScopeUser,
			}}
		}
	case "npm":
		// Npm globals: track by package name from RawStageContext["packages"].
		if pkgs := extractStringSlice(s.RawStageContext, "packages"); len(pkgs) > 0 {
			return []ReverseOp{{
				Kind:    ReverseOpNpmUninstallG,
				Targets: pkgs,
				Scope:   ScopeUser,
			}}
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// OpStep — one entry from the candy manifest's tasks: list.
// ---------------------------------------------------------------------------

// OpStep wraps a raw Op so the IR compiler doesn't have to transform
// it further. The OCI target and host target both understand Op shapes
// (cmd / mkdir / copy / write / link / download / setcap / build) and
// emit the appropriate directives. CtxPath substitutes for /ctx/ in cmd
// bodies on the host target (container RUN directives keep /ctx via
// bind-mount, which doesn't exist on the host).
type OpStep struct {
	Op           *Op
	CandyName    string
	CandyDir     string
	CtxPath      string // absolute layer-dir path replacing "/ctx/" on host
	ResolvedUser string // uid:gid or "root" after resolveUserSpec

	// To is the home-resolved copy/download destination for the DEPLOY path.
	// The compiler tokenizes the task's `to:` (~ / ${HOME} / $HOME → {{.Home}})
	// here; InstallPlan.ResolveHome substitutes the destination's real home at
	// emit. execTask uses this (not the raw Task.To) for PutFile, so a
	// `copy: to: ${HOME}/...` lands in the actual guest/host home instead of a
	// literal "${HOME}" directory under sudo (HOME=/root). Empty when the task
	// has no `to:`. The OCI build emitter reads Task.To directly (build-time
	// ENV expands ${HOME}), so it is unaffected by this field.
	To string

	// CandyVars are the candy manifest `vars:` map propagated into the task
	// script as exports. Build-time gets these via Containerfile ENV
	// directives (emitVarsEnv); host/local-deploy time has no equivalent
	// mechanism, so the renderer emits `export K=V` lines from this field.
	// Without this, candies like `kubernetes` whose download URLs reference
	// ${K3D_VERSION} fetched an empty path-component at deploy time and
	// curl 404'd.
	CandyVars map[string]string
}

func (s *OpStep) Kind() StepKind { return StepKindOp }

func (s *OpStep) Scope() Scope {
	// Classify by the task's user context: anything at root is system;
	// named users and numeric non-root UIDs are user scope.
	if s.ResolvedUser == "" || s.ResolvedUser == "root" || s.ResolvedUser == "0" || s.ResolvedUser == "0:0" {
		return ScopeSystem
	}
	return ScopeUser
}

func (s *OpStep) Venue() Venue { return VenueHostNative }

func (s *OpStep) RequiresGate() Gate {
	// Free-form cmd bodies running as root can do anything; gate them.
	// Structured verbs (mkdir/copy/write/link/download/setcap) are
	// inspectable and don't need the gate.
	if s.Scope() == ScopeSystem && s.Op != nil && s.Op.Command != "" {
		return GateAllowRootTasks
	}
	return GateNone
}

func (s *OpStep) Reverse() []ReverseOp {
	if s.Op == nil {
		return nil
	}
	// Only structurally-reversible task verbs record reversal. Cmd tasks
	// are opaque shell — we can't auto-reverse them.
	switch {
	case s.Op.Copy != "" || s.Op.Write != "":
		dest := s.Op.To
		if dest == "" {
			dest = s.Op.Copy
			if dest == "" {
				dest = s.Op.Write
			}
		}
		kind := ReverseOpRmFileUser
		if s.Scope() == ScopeSystem {
			kind = ReverseOpRmFileSystem
		}
		return []ReverseOp{{
			Kind:    kind,
			Targets: []string{dest},
			Scope:   s.Scope(),
		}}
	case s.Op.Download != "":
		// When the candy author declared an explicit uninstall list,
		// use it — that's the correct target set for extract-into-a-
		// shared-dir tasks (e.g. tarballs that land multiple binaries
		// in /usr/local/bin/). Otherwise fall back to task.To.
		targets := []string{s.Op.To}
		if len(s.Op.Uninstall) > 0 {
			targets = append([]string(nil), s.Op.Uninstall...)
		}
		return []ReverseOp{{
			Kind:    reverseFileKindFor(s.Scope()),
			Targets: targets,
			Scope:   s.Scope(),
		}}
	case s.Op.Link != "":
		return []ReverseOp{{
			Kind:    reverseFileKindFor(s.Scope()),
			Targets: []string{s.Op.Link},
			Scope:   s.Scope(),
		}}
	case s.Op.Mkdir != "":
		// Directories aren't auto-removed (might contain other files);
		// only record paths for manual inspection via `charly deploy status`.
		return nil
	}
	return nil
}

func reverseFileKindFor(sc Scope) ReverseOpKind {
	if sc == ScopeSystem {
		return ReverseOpRmFileSystem
	}
	return ReverseOpRmFileUser
}

// ---------------------------------------------------------------------------
// FileStep — structured file placement (distinct from OpStep copy/write).
// ---------------------------------------------------------------------------

// FileStep places a single file. The compiler may emit these for
// candy-declared file directives that aren't wrapped as tasks (e.g.
// supervisord fragment assembly). Today's tasks.go handles most file
// placement via OpStep; FileStep exists for cases where the compiler
// synthesizes file writes that weren't in the candy manifest (e.g. service unit
// files, managed-block contents).
type FileStep struct {
	Source    string      // path to source content (under layer dir or .build/_inline/)
	Dest      string      // absolute destination path
	Mode      os.FileMode // permissions
	Owner     string      // "root" or "uid:gid" or the invoking user's name
	CandyName string      // for ledger
}

func (s *FileStep) Kind() StepKind { return StepKindFile }

func (s *FileStep) Scope() Scope {
	if pathIsSystemScoped(s.Dest) {
		return ScopeSystem
	}
	return ScopeUser
}

func (s *FileStep) Venue() Venue       { return VenueHostNative }
func (s *FileStep) RequiresGate() Gate { return GateNone }

func (s *FileStep) Reverse() []ReverseOp {
	return []ReverseOp{{
		Kind:    reverseFileKindFor(s.Scope()),
		Targets: []string{s.Dest},
		Scope:   s.Scope(),
	}}
}

// pathIsSystemScoped returns true for paths under /etc, /usr, /var, /opt,
// /root, /boot and similar system locations. Used to classify FileStep
// scope when the compiler hasn't explicitly tagged it.
func pathIsSystemScoped(p string) bool {
	if p == "" {
		return false
	}
	systemPrefixes := []string{
		"/etc/", "/usr/", "/var/", "/opt/", "/root/",
		"/boot/", "/srv/", "/run/", "/lib/", "/sbin/", "/bin/",
	}
	for _, pref := range systemPrefixes {
		if len(p) >= len(pref) && p[:len(pref)] == pref {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// ServicePackagedStep — enable a distro-shipped systemd unit.
// ---------------------------------------------------------------------------

// ServicePackagedStep enables a systemd unit that arrived via a distro
// package (e.g. postgresql.service). Optional drop-in overrides are
// written alongside the packaged unit without touching it. This step is
// only emitted when rendering to a systemd-based init system; supervisord
// targets emit a warning-and-skip.
type ServicePackagedStep struct {
	Unit          string // e.g. "postgresql.service"
	TargetScope   Scope  // ScopeSystem → /etc/systemd/system/; ScopeUser → ~/.config/systemd/user/
	Enable        bool
	OverridesText string // rendered drop-in (.conf) content; "" if no overrides
	OverridesPath string // computed absolute path for the drop-in file
	CandyName     string // for ledger + drop-in naming
	PriorEnabled  bool   // populated at install time (before enable); used for teardown restore
}

func (s *ServicePackagedStep) Kind() StepKind { return StepKindServicePackaged }
func (s *ServicePackagedStep) Scope() Scope   { return s.TargetScope }
func (s *ServicePackagedStep) Venue() Venue   { return VenueHostNative }
func (s *ServicePackagedStep) RequiresGate() Gate {
	return GateWithServices
}

func (s *ServicePackagedStep) Reverse() []ReverseOp {
	ops := []ReverseOp{}
	if s.Enable {
		ops = append(ops, ReverseOp{
			Kind:    ReverseOpServiceDisable,
			Targets: []string{s.Unit},
			Scope:   s.TargetScope,
		})
		// Record the prior state so teardown can re-enable if needed.
		if s.PriorEnabled {
			ops = append(ops, ReverseOp{
				Kind:    ReverseOpRestoreEnabled,
				Targets: []string{s.Unit},
				Scope:   s.TargetScope,
			})
		}
	}
	if s.OverridesText != "" && s.OverridesPath != "" {
		ops = append(ops, ReverseOp{
			Kind:    ReverseOpRemoveDropin,
			Targets: []string{s.OverridesPath},
			Scope:   s.TargetScope,
		})
	}
	return ops
}

// ---------------------------------------------------------------------------
// ServiceCustomStep — full custom systemd unit (or supervisord fragment).
// ---------------------------------------------------------------------------

// ServiceCustomStep writes and enables a full service definition. The
// unit text is already rendered by the compiler from the candy's generic
// `services:` entry via the target init system's service_template.
type ServiceCustomStep struct {
	Name        string // e.g. "charly-ollama-ollama"
	UnitText    string // fully rendered unit content
	UnitPath    string // absolute install path
	TargetScope Scope  // ScopeSystem or ScopeUser
	Enable      bool
	CandyName   string
}

func (s *ServiceCustomStep) Kind() StepKind { return StepKindServiceCustom }
func (s *ServiceCustomStep) Scope() Scope   { return s.TargetScope }
func (s *ServiceCustomStep) Venue() Venue   { return VenueHostNative }
func (s *ServiceCustomStep) RequiresGate() Gate {
	return GateWithServices
}

func (s *ServiceCustomStep) Reverse() []ReverseOp {
	ops := []ReverseOp{}
	if s.Enable {
		ops = append(ops, ReverseOp{
			Kind:    ReverseOpServiceDisable,
			Targets: []string{s.Name},
			Scope:   s.TargetScope,
		})
	}
	ops = append(ops, ReverseOp{
		Kind:    ReverseOpServiceRemove,
		Targets: []string{s.UnitPath},
		Scope:   s.TargetScope,
	})
	return ops
}

// ---------------------------------------------------------------------------
// ShellHookStep — env: and path_append: materialized as a shell env.d file.
// ---------------------------------------------------------------------------

// ShellHookStep records the env vars and PATH contributions a candy makes
// to the user's shell environment. On the OCI target these translate to
// `ENV K=V` directives in the Containerfile. On the host target they
// become `~/.config/opencharly/env.d/<candy>.env` plus a managed block in
// the user's shell init that sources the env.d directory.
type ShellHookStep struct {
	CandyName string
	EnvVars   map[string]string
	PathAdd   []string // already {{.Home}}-substituted to absolute paths
	EnvFile   string   // computed path (~/.config/opencharly/env.d/<candy>.env); populated at install
}

func (s *ShellHookStep) Kind() StepKind     { return StepKindShellHook }
func (s *ShellHookStep) Scope() Scope       { return ScopeUserProfile }
func (s *ShellHookStep) Venue() Venue       { return VenueHostNative }
func (s *ShellHookStep) RequiresGate() Gate { return GateNone }

func (s *ShellHookStep) Reverse() []ReverseOp {
	if s.EnvFile == "" {
		return nil
	}
	return []ReverseOp{{
		Kind:    ReverseOpRemoveEnvdFile,
		Targets: []string{s.EnvFile},
		Scope:   ScopeUserProfile,
		Extra:   map[string]string{"layer": s.CandyName},
	}}
}

// ---------------------------------------------------------------------------
// ShellSnippetStep — per-candy per-shell init snippet for the candy manifest `shell:`.
// ---------------------------------------------------------------------------

// ShellSnippetStep records a per-(candy, shell) init snippet emitted from
// a candy's `shell:` block. The compiler emits one step per (candy, shell)
// pair after applying the selection rule (per-shell ByShell entry wins
// over generic, with ${SHELL_NAME} substitution).
//
// Per-target rendering:
//   - OCITarget: snippet bytes are content-address-staged and COPYed to
//     a system-wide drop-in (/etc/profile.d/charly-<candy>.sh for bash/zsh/sh,
//     /etc/fish/conf.d/charly-<candy>.fish for fish).
//   - LocalDeployTarget / VmDeployTarget: managed-block append to the
//     user's rc file (~/.bashrc, ~/.zshrc, ~/.profile) keyed by
//     `# opencharly:begin <Marker>` fence; for fish, a per-candy drop-in at
//     ~/.config/fish/conf.d/charly-<candy>.fish (no fence needed, file IS the
//     unit). UseDropin discriminates the two paths.
//   - K8sDeployTarget: skipped (no shell in pods).
type ShellSnippetStep struct {
	CandyName   string   // candy this snippet belongs to (also Marker source)
	Origin      string   // "<candy>" or "box" or "deploy" (for ledger refcount + LabelShell origin)
	Shell       string   // bash | zsh | fish | sh
	Snippet     string   // rendered body, ${SHELL_NAME}-substituted, ready to write
	PathAppend  []string // already rendered into Snippet by the compiler; tracked here for label round-trip / overlay
	Destination string   // resolved per-target at compile time; absolute path on the target
	Marker      string   // managed-block marker tag (= CandyName) — used by replaceOrAppendManagedBlock
	UseDropin   bool     // true: write whole file (fish, container drop-in); false: managed-block append into existing rc file
	Priority    int      // load-order hint, 0 = default
}

func (s *ShellSnippetStep) Kind() StepKind { return StepKindShellSnippet }

// Scope: SnippetStep is system-wide for build-mode (Container drop-ins live
// under /etc/profile.d/ — root-owned), and user-profile for host/vm deploy-
// mode (managed block in ~/.bashrc etc.). The compiler picks the right
// Scope when constructing the step based on the destination path; the
// step records the choice so each emitter doesn't have to re-derive it.
func (s *ShellSnippetStep) Scope() Scope {
	if pathIsSystemScoped(s.Destination) {
		return ScopeSystem
	}
	return ScopeUserProfile
}

func (s *ShellSnippetStep) Venue() Venue       { return VenueHostNative }
func (s *ShellSnippetStep) RequiresGate() Gate { return GateNone }

func (s *ShellSnippetStep) Reverse() []ReverseOp {
	if s.UseDropin {
		// Whole-file write: removal is a plain rm of the destination.
		kind := ReverseOpRmFileUser
		if pathIsSystemScoped(s.Destination) {
			kind = ReverseOpRmFileSystem
		}
		return []ReverseOp{{
			Kind:    kind,
			Targets: []string{s.Destination},
			Scope:   s.Scope(),
			Extra:   map[string]string{"layer": s.CandyName, "shell": s.Shell},
		}}
	}
	// Managed-block append: removal strips just our fence pair from the
	// existing rc file (which may belong to the user and contain unrelated
	// content). Marker carries the per-candy fence tag so multiple candies
	// can coexist in one rc file.
	return []ReverseOp{{
		Kind:    ReverseOpRemoveManaged,
		Targets: []string{s.Destination},
		Scope:   s.Scope(),
		Extra:   map[string]string{"layer": s.CandyName, "shell": s.Shell, "marker": s.Marker},
	}}
}

// ---------------------------------------------------------------------------
// RepoChangeStep — concrete record of a repo config file being written.
// ---------------------------------------------------------------------------

// RepoChangeStep records a repo config file mutation (e.g. adding
// rpmfusion-free.repo to /etc/yum.repos.d/). Distinct from
// SystemPackagesStep's PhasePrepare because the compiler often synthesizes
// these from `cmd:` tasks that happen to install a -release.rpm — we want
// them tracked separately so `charly deploy del` can reverse them precisely.
type RepoChangeStep struct {
	Format    string // "rpm" | "deb" | "pac"
	File      string // absolute path of the repo file
	Content   string // rendered repo file body
	Checksum  string // sha256 of Content, for idempotency check at install
	CandyName string
}

func (s *RepoChangeStep) Kind() StepKind     { return StepKindRepoChange }
func (s *RepoChangeStep) Scope() Scope       { return ScopeSystem }
func (s *RepoChangeStep) Venue() Venue       { return VenueHostNative }
func (s *RepoChangeStep) RequiresGate() Gate { return GateAllowRepoChanges }

func (s *RepoChangeStep) Reverse() []ReverseOp {
	return []ReverseOp{{
		Kind:    ReverseOpRemoveRepoFile,
		Targets: []string{s.File},
		Scope:   ScopeSystem,
		Extra:   map[string]string{"layer": s.CandyName, "format": s.Format},
	}}
}

// ---------------------------------------------------------------------------
// ApkInstallStep — `apk:` package format (Android app install onto a device).
// ---------------------------------------------------------------------------

// ApkInstallStep installs Android apps onto a `kind: android` device. It is
// the IR form of a candy's `apk:` package section — the apk "package format"
// (parallel to SystemPackagesStep for rpm/deb/pac, and BuilderStep for aur).
//
// Unlike every other step, an apk install lands on a RUNNING Android device,
// not the build/host filesystem, so ONLY AndroidDeployTarget executes it
// (via the shared installer in android_install.go: apkeep + adb). Every
// other target SKIPS it — OCITarget emits nothing (there is no device at
// image-build time), and Local/Vm/Pod targets record a skip (a host/VM/pod
// is not an Android device). This is the same "wrong venue → skip" shape
// `aur:` uses off-Arch, expressed as a clean recorded skip rather than an
// error (an image legitimately builds without installing apps).
type ApkInstallStep struct {
	Packages  []ApkPackageSpec
	CandyName string
	CandyDir  string // candy source dir — anchors relative committed-APK paths
}

func (s *ApkInstallStep) Kind() StepKind { return StepKindApkInstall }

// Scope is system — installing an app mutates device-global package state.
func (s *ApkInstallStep) Scope() Scope { return ScopeSystem }

// Venue is host-native — AndroidDeployTarget orchestrates apkeep + adb from
// the host (apkeep itself may run in-pod, but the step is driven host-side).
func (s *ApkInstallStep) Venue() Venue       { return VenueHostNative }
func (s *ApkInstallStep) RequiresGate() Gate { return GateNone }

// Reverse returns no ledger ops — Android teardown is not ledger-based.
// `charly deploy del <android>` (AndroidUnifiedTarget.Del) re-resolves the
// deploy's apk candies and `pm uninstall`s each package directly.
func (s *ApkInstallStep) Reverse() []ReverseOp { return nil }

// ---------------------------------------------------------------------------
// LocalPkgInstallStep — build a bundled PKGBUILD on the host (makepkg) and
// install the resulting `.pkg.tar.zst` onto a pac-based deploy target.
// ---------------------------------------------------------------------------
//
// LocalPkgInstallStep is the IR form of a candy's `localpkg:` field — a
// pointer at a bundled Arch PKGBUILD directory (relative to the candy dir or
// the project root). It is the proper-package counterpart of the charly candy's
// ad-hoc curl-a-binary `cmd:` task: on an Arch/CachyOS DEPLOY target the
// package is built from the repo's bundled PKGBUILD on the HOST (`makepkg`),
// the resulting artifact is transferred to the target, and `pacman -U`-installed
// — so `charly` lands as the tracked `opencharly-git` package at /usr/bin/charly rather
// than an untracked binary at /usr/local/bin/charly.
//
// Like ApkInstallStep, the step is compiled REGARDLESS of target and each
// DeployTarget decides whether to execute or skip:
//   - LocalDeployTarget (Arch/CachyOS host) and VmDeployTarget (Arch/CachyOS
//     guest) EXECUTE it (build on host → transfer → pacman -U on the target).
//   - On a NON-pac deploy target the executor records a clean skip (a Fedora /
//     Debian host has no pacman; the candy's own `cmd:` task curls the binary
//     there as the documented fallback).
//   - OCITarget SKIPS it — no makepkg in a container image build; the image
//     bakes one self-contained binary via the candy's COPY/curl `cmd:` task.
//   - AndroidDeployTarget / K8sDeployTarget SKIP it (no Arch package surface).
//
// The PKGBUILD location is resolved at EMIT time (not compile time), so the
// step carries only the author's hint (`PkgbuildRef`) plus the candy's source
// dir + the deploy project dir for the walk-up search. When no PKGBUILD is
// found the step is a no-op (the candy's existing curl/COPY task is the
// fallback).
type LocalPkgInstallStep struct {
	PkgbuildRef string // the layer's `localpkg:` value (e.g. "pkg/arch") — a hint, resolved at emit
	CandyName   string
	CandyDir    string // layer source dir — one anchor for the relative PKGBUILD search
	ProjectDir  string // the deploy project dir (os.Getwd() at deploy time) — the other anchor

	// Format is the package-format name whose `local_pkg:` config drives this
	// step (e.g. "pac"). "" when the target distro declares no localpkg-capable
	// format — the executor then treats the step as a clean no-op.
	Format string

	// LocalPkg is the format's localpkg contract resolved from build.yml at
	// compile time (DistroDef.LocalPkgFormat). It carries the build/install
	// templates, package glob, source-dir sentinel, and probe command — so the
	// executor renders every package-manager command from config instead of
	// hardcoding build/install/glob literals. The install command auto-resolves
	// the package's dependencies from the target's repos (pacman -U / dnf
	// install / apt-get install), so there is no dep-closure builder. Nil when
	// Format == "".
	LocalPkg *LocalPkgDef
}

func (s *LocalPkgInstallStep) Kind() StepKind { return StepKindLocalPkgInstall }

// Scope is system — installing a pacman package mutates global package state.
func (s *LocalPkgInstallStep) Scope() Scope { return ScopeSystem }

// Venue is host-native — makepkg runs on the host (like the aur builder), then
// the artifact is shipped to the target and installed via pacman.
func (s *LocalPkgInstallStep) Venue() Venue       { return VenueHostNative }
func (s *LocalPkgInstallStep) RequiresGate() Gate { return GateNone }

// Reverse returns no ledger ops — the package is the deploy substrate's own
// pacman-tracked package, removed (if ever) via `pacman -R opencharly-git` by
// the operator, not by deploy teardown. Mirrors ApkInstallStep's empty Reverse.
func (s *LocalPkgInstallStep) Reverse() []ReverseOp { return nil }

// RebootStep requests a reboot of the deploy target after this candy's steps.
// It is emitted (last in the candy) when a candy declares `reboot: true` —
// the canonical case is a kernel-module candy (e.g. nvidia-open-dkms) whose
// module only loads on a fresh boot with nouveau blacklisted.
//
// Only targets that OWN a rebootable machine act on it: VmDeployTarget reboots
// the guest over SSH and waits for it to return. OCITarget / PodDeployTarget /
// K8sDeployTarget have no machine to reboot at build time and skip it;
// LocalDeployTarget refuses to reboot the operator's host unattended (skip +
// warn). Mirrors the ApkInstallStep "only one target executes" pattern.
type RebootStep struct {
	CandyName string
}

func (s *RebootStep) Kind() StepKind     { return StepKindReboot }
func (s *RebootStep) Scope() Scope       { return ScopeSystem }
func (s *RebootStep) Venue() Venue       { return VenueHostNative }
func (s *RebootStep) RequiresGate() Gate { return GateNone }

// Reverse is empty — a reboot is not a persistent artifact to undo.
func (s *RebootStep) Reverse() []ReverseOp { return nil }

// PackageIDs returns the installable package ids (committed-APK entries with
// no `package:` id are excluded — they can't be uninstalled by id).
func (s *ApkInstallStep) PackageIDs() []string {
	var out []string
	for _, p := range s.Packages {
		if p.Package != "" {
			out = append(out, p.Package)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// InstallPlan — the top-level IR container.
// ---------------------------------------------------------------------------

// InstallPlan is the full ordered list of steps for one candy or one
// whole-image deploy. Compiled by BuildDeployPlan and consumed by any
// DeployTarget implementation.
//
// The compiler produces one InstallPlan per candy (then merges them in
// topological order for whole-image deploys). A whole-image deploy keeps
// candy boundaries visible so the ledger can refcount which candies
// participate in which deploys — crucial for correct uninstall.
type InstallPlan struct {
	// Identity — populated by the compiler.
	DeployID string // per-deploy unique ID (hash of image + add_candy list)
	Box      string // deployable box name (or candy name for single-candy deploys)
	Version  string // candy/box CalVer version
	Distro   string // resolved host distro tag, e.g. "fedora:43"
	Candy    string // candy name when this plan is for a single candy; "" for whole-image merges

	// The ordered step sequence.
	Steps []InstallStep

	// Provenance — used by teardown and status.
	CandiesIncluded []string          // ordered layer names this plan composes (for whole-image merges)
	AddCandies      []string          // layers added on top via charly.yml add_layers: (for provenance)
	BuilderImage    string            // selected builder image for VenueContainerBuilder steps
	Meta            map[string]string // free-form metadata (builder image, glibc version, …)
}

// ResolveHome substitutes the deferred HomeToken with a concrete home in
// every home-bearing step field, in place. Each DeployTarget calls this once
// at emit time with the home of its real destination: img.Home for the
// OCI/pod-overlay build, the host home for LocalDeployTarget, the GUEST home
// (SSH executor ResolveHome) for VmDeployTarget. Idempotent — fields without
// the token are left untouched, so a second call is a no-op.
//
// Covered fields: ShellHookStep env values + PathAdd, ShellSnippetStep Snippet
// + Destination + PathAppend, FileStep.Dest. OpStep cmd/content bodies are
// intentionally NOT touched — `~`/`$HOME` there shell-expand at runtime on the
// destination as the deploy user, which is already correct on every venue.
// BuilderStep is also untouched — its home is resolved separately by
// renderBuilderScript against the builder/guest home (see execBuilder).
func (p *InstallPlan) ResolveHome(home string) {
	if p == nil || home == "" {
		return
	}
	sub := func(s string) string { return strings.ReplaceAll(s, HomeToken, home) }
	for _, step := range p.Steps {
		switch s := step.(type) {
		case *ShellHookStep:
			for k, v := range s.EnvVars {
				s.EnvVars[k] = sub(v)
			}
			for i, pth := range s.PathAdd {
				s.PathAdd[i] = sub(pth)
			}
		case *ShellSnippetStep:
			s.Snippet = sub(s.Snippet)
			s.Destination = sub(s.Destination)
			for i, pth := range s.PathAppend {
				s.PathAppend[i] = sub(pth)
			}
		case *FileStep:
			s.Dest = sub(s.Dest)
		case *ServiceCustomStep:
			// The systemd unit is pre-rendered at compile with {{.Home}} for
			// host/vm targets (see compileServiceSteps); resolve it — and the
			// user-scope unit install path — against the destination home here.
			s.UnitText = sub(s.UnitText)
			s.UnitPath = sub(s.UnitPath)
		case *OpStep:
			// Home-relative copy/download dest (tokenized at compile). The
			// Task body itself (cmd/content) is left alone — those shell-expand
			// $HOME at runtime as the deploy user.
			s.To = sub(s.To)
		}
	}
}

// StepsByVenue partitions the plan's steps by (Scope, Venue) tuple while
// preserving intra-partition order. Host target emission uses this to
// batch contiguous same-(scope, venue) runs into one heredoc. Not used
// by the OCI target (it walks Steps directly).
func (p *InstallPlan) StepsByVenue() []StepBatch {
	if len(p.Steps) == 0 {
		return nil
	}
	out := []StepBatch{}
	cur := StepBatch{Scope: p.Steps[0].Scope(), Venue: p.Steps[0].Venue()}
	for _, s := range p.Steps {
		if s.Scope() != cur.Scope || s.Venue() != cur.Venue {
			if len(cur.Steps) > 0 {
				out = append(out, cur)
			}
			cur = StepBatch{Scope: s.Scope(), Venue: s.Venue()}
		}
		cur.Steps = append(cur.Steps, s)
	}
	if len(cur.Steps) > 0 {
		out = append(out, cur)
	}
	return out
}

// StepBatch is a contiguous run of steps sharing the same (Scope, Venue).
// Emitted together: one sudo heredoc, one user heredoc, or one podman run
// per batch.
type StepBatch struct {
	Scope Scope
	Venue Venue
	Steps []InstallStep
}

// ---------------------------------------------------------------------------
// DeployTarget — what the emitters implement.
// ---------------------------------------------------------------------------

// EmitOpts carries cross-cutting toggles passed by command-line flags.
// Gates are checked per-step by the target; target-specific options (the
// container target's registry auth, the host target's --yes, --dry-run)
// are bundled here too.
type EmitOpts struct {
	DryRun               bool
	FormatJSON           bool // print IR as JSON on stdout instead of table
	AllowRepoChanges     bool
	AllowRootTasks       bool
	WithServices         bool
	SkipIncompatible     bool
	AssumeYes            bool // skip sudo preflight, confirmation prompts
	Verify               bool // run layer tests after install
	Pull                 bool // force re-fetch of remote refs / image pull
	BuilderImageOverride string
	K8sApply             bool // target=k8s: run `kubectl apply -k` after generating the kustomize tree

	// ParentExec is the DeployExecutor of the parent deployment in a
	// nested tree. Non-nil iff this target is dispatched as a child of
	// another — DeployAddCmd's tree walker builds the chain root-first
	// and passes the immediate ancestor's executor here. Targets that
	// support being nested (host, container, vm) compose their own
	// executor over ParentExec via NestedExecutor; leaf-only targets
	// (kubernetes) ignore it and error if non-nil.
	//
	// When nil, the target runs against its natural root venue
	// (ShellExecutor for host, a fresh SSHExecutor for vm, etc.)
	// — preserving the flat-schema behavior for v2 configs that happen
	// to have no `children:`.
	ParentExec DeployExecutor

	// ParentNode is the DeploymentNode above this target in the tree.
	// Useful for targets that need parent-level context beyond the
	// executor (e.g. a vm child wants to know its parent container's
	// name to wire network forwarding). nil at the root.
	ParentNode *DeploymentNode

	// Path is the dotted-path identifier of this node (e.g.
	// "stack.web.db"). Used for logging + ledger keying.
	Path string
}

// DeployTarget is the interface OCI + container-deploy + host-deploy
// emitters satisfy. Taking a slice of plans (rather than a single plan)
// lets whole-image deploys pass all per-candy plans at once and let the
// target merge them — useful because OCITarget may want to emit a single
// Containerfile for the image while LocalDeployTarget may batch steps
// across candies.
type DeployTarget interface {
	Name() string
	Emit(plans []*InstallPlan, opts EmitOpts) error
}

// GateEnabled returns whether the given gate is permitted under opts.
// GateNone is always enabled; named gates require the corresponding
// opt-in flag.
func GateEnabled(g Gate, opts EmitOpts) bool {
	switch g {
	case GateNone:
		return true
	case GateAllowRepoChanges:
		return opts.AllowRepoChanges || opts.AssumeYes
	case GateAllowRootTasks:
		return opts.AllowRootTasks || opts.AssumeYes
	case GateWithServices:
		return opts.WithServices || opts.AssumeYes
	}
	return false
}

// ---------------------------------------------------------------------------
// Small helpers used by step types.
// ---------------------------------------------------------------------------

// extractString returns m[key] as a string or "" if absent.
func extractString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// extractStringSlice returns m[key] as []string or nil if absent.
// Accepts []string and []interface{} (as produced by yaml.v3) inputs.
func extractStringSlice(m map[string]any, key string) []string {
	if m == nil {
		return nil
	}
	v, ok := m[key]
	if !ok {
		return nil
	}
	switch t := v.(type) {
	case []string:
		out := make([]string, len(t))
		copy(out, t)
		return out
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
