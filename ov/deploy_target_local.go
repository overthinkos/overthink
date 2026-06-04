package main

// deploy_target_host.go — LocalDeployTarget executes an InstallPlan on
// the host filesystem.
//
// Walk strategy:
//   1. Precompute deploy-id + lock the ledger.
//   2. Group steps by (Scope, Venue) — contiguous same-scope batches
//      become one heredoc; container-builder batches become one
//      podman-run each.
//   3. For each batch:
//       - system + host-native → `sudo bash <<EOF ... EOF`
//       - user + host-native   → `bash <<EOF ... EOF` as invoking user
//       - * + container-builder → podman run <builder> ...
//       - * + skip             → no-op (just record the skip reason)
//   4. After every successful step, append to the per-layer ledger.
//   5. After the whole plan completes, write the deploy record.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// LocalDeployTarget executes plans on the host.
type LocalDeployTarget struct {
	// HostHome is the invoking user's home. Populated by DeployUpCmd
	// before calling Emit.
	HostHome string

	// LedgerPaths points to the on-disk ledger. Defaults to
	// ~/.config/overthink/installed/ when nil.
	LedgerPaths *LedgerPaths

	// Distro is the detected host distro. Used for gating aur: on
	// non-Arch hosts and picking format sections.
	Distro *HostDistro

	// InitDef is the init system used for rendering services on the
	// host. Host deploys always target systemd — caller must supply
	// a systemd InitDef with a populated ServiceSchema.
	InitDef *InitDef

	// BuilderImageResolver maps a builder name to a concrete image ref
	// for `podman run`. Caller supplies — typically derived from
	// image.yml or --builder-image flag.
	BuilderImageResolver func(builderName string) string

	// Shell is the user's login shell (detected via DetectLoginShell
	// by default). Drives env.d managed-block insertion.
	Shell ShellKind

	// Executor is the DeployExecutor used for every shell primitive.
	// Defaults to ShellExecutor{} when nil — which matches the
	// pre-tree-schema behavior of spawning bash on the invoking host.
	//
	// When non-nil (set by the tree-walking dispatcher for nested
	// `target: host` children), the executor may be a NestedExecutor
	// wrapping the parent container / VM / nested-host venue. All
	// RunSystem / RunUser / PutFile calls route through this executor,
	// so a "host deploy inside a container" runs the same InstallPlan
	// IR but lands in the nested venue's rootfs + ledger dir.
	Executor DeployExecutor

	// DryRunWriter receives dry-run output. Nil defaults to os.Stderr.
	DryRunWriter *os.File

	// shellsPresent caches the shell-detection probe result for the
	// duration of one Emit() call. Populated lazily on the first
	// ShellSnippetStep encountered. Keys are bash/zsh/fish/sh; values
	// indicate whether `command -v <shell>` returned success on the
	// target. Steps for absent shells become no-ops with a logged
	// skip reason — same shape as ScopeSkip in the IR.
	shellsPresent map[string]bool

	// LocalSpec is the resolved kind:local template. Populated by the
	// deploy dispatcher when a deployment carries `local: <name>`.
	// Used for layer-stack composition only — there is NO image-fetch
	// surface on a kind:local template (see local_spec.go for the
	// post-2026-05 contract). Nil when the deployment uses inline
	// add_layers: instead of a template.
	LocalSpec *LocalSpec

	// Cfg + ProjectDir are kept on the target for downstream callers
	// that resolve layer.yml / image.yml during the plan walk. Not
	// used for any image-fetch logic.
	Cfg        *Config
	ProjectDir string

	// DistroCfg is the resolved build.yml distro section. Used by the
	// host-venue package renderer (renderSystemPackageCommand) to look up the
	// format's phase.install.host template — the SAME config the OCI container
	// path reads. Populated by the deploy dispatcher from dctx.DistroCfg.
	DistroCfg *DistroConfig
}

// exec returns the configured executor, defaulting to a local one
// when unset. Centralized so the emit path doesn't sprinkle nil
// checks at every call site.
func (t *LocalDeployTarget) exec() DeployExecutor {
	if t.Executor == nil {
		return ShellExecutor{}
	}
	return t.Executor
}

// runSystem runs a bash script as root through the target's
// executor. Replaces direct calls to the package-level runSudoShell
// so nested host deploys (executor = NestedExecutor) land in the
// right venue.
func (t *LocalDeployTarget) runSystem(script string, opts EmitOpts) error {
	return t.exec().RunSystem(opts.ContextOrDefault(), script, opts)
}

// runUser runs a bash script as the invoking user through the
// target's executor.
func (t *LocalDeployTarget) runUser(script string, opts EmitOpts) error {
	return t.exec().RunUser(opts.ContextOrDefault(), script, opts)
}

// Name identifies this target.
func (t *LocalDeployTarget) Name() string { return "host" }

// Emit executes the full list of plans. Plans are processed in order;
// all steps from plan N run before any step from plan N+1.
func (t *LocalDeployTarget) Emit(plans []*InstallPlan, opts EmitOpts) error {
	if t.HostHome == "" {
		// Resolve the deploying-user's $HOME via the executor. For the
		// local ShellExecutor this is the operator's $HOME; for an
		// SSHExecutor (`host: user@machine`) it's the REMOTE user's
		// $HOME — fixes the long-standing bug where rc-file edits over
		// SSH were landing in the operator's $HOME instead of the
		// guest user's. Bundled with the 2026-05 shell:-schema cutover.
		home, err := t.exec().ResolveHome(opts.ContextOrDefault(), "")
		if err != nil {
			return fmt.Errorf("LocalDeployTarget: resolve HOME: %w", err)
		}
		t.HostHome = home
	}
	if t.HostHome == "" {
		return fmt.Errorf("LocalDeployTarget: cannot determine HOME")
	}
	if t.LedgerPaths == nil {
		p, err := DefaultLedgerPaths()
		if err != nil {
			return err
		}
		t.LedgerPaths = p
	}
	if t.Shell == "" {
		t.Shell = DetectLoginShell()
	}

	// Lock the ledger for the whole session.
	lock, err := AcquireLedgerLock(t.LedgerPaths)
	if err != nil {
		return err
	}
	defer lock.Release()

	// Sudo preflight: refresh the timestamp once at the start so later
	// `sudo bash <<EOF` blocks reuse the cache. --yes skips the prompt
	// (assumes cached or NOPASSWD).
	if !opts.AssumeYes && !opts.DryRun {
		if err := sudoRefresh(); err != nil {
			return fmt.Errorf("sudo preflight: %w", err)
		}
	}

	for _, plan := range plans {
		if plan == nil {
			continue
		}
		// Resolve the deferred {{.Home}} token to this destination's home
		// (host home for the local ShellExecutor, the REMOTE user's home for
		// an SSHExecutor `host: user@machine`). HostHome is already the
		// executor-resolved home, so this lands env.d/profile paths on the
		// right user even for remote local-target deploys.
		plan.ResolveHome(t.HostHome)
		if err := t.emitPlan(plan, opts); err != nil {
			return fmt.Errorf("plan %s: %w", plan.Layer, err)
		}
	}

	// Ensure the env.d-sourcing managed block is in place. Idempotent —
	// safe to run after every deploy. Goes through the executor so local
	// and VM deploys share one write path (R3).
	if _, err := EnsureManagedBlockVia(opts.ContextOrDefault(), t.exec(), t.Shell, t.HostHome, opts); err != nil {
		return fmt.Errorf("managed block: %w", err)
	}
	return nil
}

// emitPlan walks one plan's steps in IR order, batching by
// (Scope, Venue) for efficient sudo/user/container execution.
func (t *LocalDeployTarget) emitPlan(plan *InstallPlan, opts EmitOpts) error {
	rec := &LayerRecord{
		Layer:      plan.Layer,
		Version:    plan.Version,
		DeployedAt: time.Now().UTC().Format(time.RFC3339),
	}

	batches := plan.StepsByVenue()
	for _, batch := range batches {
		if batch.Venue == VenueSkip {
			t.logSkip(batch, opts)
			continue
		}
		for _, step := range batch.Steps {
			gate := step.RequiresGate()
			if !GateEnabled(gate, opts) {
				t.logGated(step, gate, opts)
				continue
			}
			if err := t.execStep(step, plan, opts, rec); err != nil {
				// Persist what we've recorded so far, then propagate.
				_ = t.recordLayer(rec, plan, opts)
				return err
			}
		}
	}

	return t.recordLayer(rec, plan, opts)
}

// recordLayer writes the per-layer ledger entry and adds the deploy
// to the refcount set. Idempotent across multiple deploys of the same
// layer.
func (t *LocalDeployTarget) recordLayer(rec *LayerRecord, plan *InstallPlan, opts EmitOpts) error {
	if opts.DryRun || plan.DeployID == "" {
		return nil
	}
	// Render the config-driven package-uninstall command into each
	// package-remove reverse op BEFORE persisting — the DistroConfig is in hand
	// here; the later teardown (which reads the ledger) is not (R3 shared filler).
	fillReverseUninstallCmds(rec.ReverseOps, t.DistroCfg)
	// Route via the executor so nested host-deploys (host-target inside
	// a VM / pod via SSH / podman exec) write the ledger on the substrate,
	// not the operator's filesystem. Local executor → operator-side
	// (unchanged behaviour).
	return AddLayerDeploymentVia(t.Executor, t.LedgerPaths, plan.Layer, plan.DeployID, func(existing *LayerRecord) {
		existing.Version = rec.Version
		existing.Steps = append(existing.Steps, rec.Steps...)
		existing.ReverseOps = append(existing.ReverseOps, rec.ReverseOps...)
	})
}

// execStep runs one step and records its reversal ops in rec.
func (t *LocalDeployTarget) execStep(step InstallStep, plan *InstallPlan, opts EmitOpts, rec *LayerRecord) error {
	start := time.Now().UTC()
	switch s := step.(type) {
	case *ShellHookStep:
		return t.execShellHook(s, plan, opts, rec, start)
	case *SystemPackagesStep:
		return t.execSystemPackages(s, plan, opts, rec, start)
	case *BuilderStep:
		return t.execBuilder(s, plan, opts, rec, start)
	case *TaskStep:
		return t.execTask(s, plan, opts, rec, start)
	case *FileStep:
		return t.execFile(s, plan, opts, rec, start)
	case *ServicePackagedStep:
		return t.execServicePackaged(s, plan, opts, rec, start)
	case *ServiceCustomStep:
		return t.execServiceCustom(s, plan, opts, rec, start)
	case *RepoChangeStep:
		return t.execRepoChange(s, plan, opts, rec, start)
	case *ShellSnippetStep:
		return t.execShellSnippet(s, plan, opts, rec, start)
	case *ApkInstallStep:
		// apk packages install onto a `kind: android` device, not a host —
		// a local deploy has no emulator. Record a skip and continue (a
		// layer carrying apk: may also carry host-relevant steps).
		t.noteStep(rec, StepKindApkInstall, s.Scope(), VenueSkip,
			fmt.Sprintf("layer=%s skipped: apk installs only on a kind:android device", s.LayerName), start)
		return nil
	case *LocalPkgInstallStep:
		return t.execLocalPkg(s, plan, opts, rec, start)
	case *RebootStep:
		// Never reboot the operator's host unattended. Record a skip and
		// warn — a host that needs a kernel module reloaded should be
		// rebooted by the operator, not by a deploy.
		fmt.Fprintf(os.Stderr, "warning: layer %q requests a reboot; skipping on target:local (reboot the host yourself if a new kernel module must load)\n", s.LayerName)
		t.noteStep(rec, StepKindReboot, s.Scope(), VenueSkip,
			fmt.Sprintf("layer=%s skipped: reboot not performed on target:local", s.LayerName), start)
		return nil
	}
	return fmt.Errorf("LocalDeployTarget: unknown step kind %T", step)
}

// execShellSnippet renders one (layer, shell) snippet onto the target
// venue. Shell-detection probe runs once per Emit() (cached on the
// target struct). Snippets for absent shells become VenueSkip-style
// no-ops with a logged reason. UseDropin=true writes the file
// outright; UseDropin=false applies replaceOrAppendManagedBlock to the
// existing rc file under a per-layer fence pair.
func (t *LocalDeployTarget) execShellSnippet(s *ShellSnippetStep, plan *InstallPlan, opts EmitOpts, rec *LayerRecord, start time.Time) error {
	if err := t.ensureShellProbe(opts); err != nil {
		return err
	}
	if !t.shellsPresent[s.Shell] {
		t.logSkipReason(fmt.Sprintf("shell-snippet %s/%s: %s not installed on target", s.LayerName, s.Shell, s.Shell), opts)
		return nil
	}
	if opts.DryRun {
		fmt.Fprintf(t.stderr(), "[dry-run] shell-snippet %s/%s -> %s (use_dropin=%v)\n",
			s.LayerName, s.Shell, s.Destination, s.UseDropin)
		t.noteStep(rec, StepKindShellSnippet, s.Scope(), s.Venue(),
			fmt.Sprintf("layer=%s shell=%s dest=%s", s.LayerName, s.Shell, s.Destination), start)
		return nil
	}
	body := s.Snippet
	var fileBytes []byte
	if s.UseDropin {
		fileBytes = []byte(body)
		if !strings.HasSuffix(body, "\n") {
			fileBytes = append(fileBytes, '\n')
		}
	} else {
		// Read existing rc file (empty if absent), apply managed-block.
		exec := t.exec()
		existing, err := exec.GetFile(opts.ContextOrDefault(), s.Destination, false, opts)
		if err != nil && !isFileNotFoundErr(err) {
			return fmt.Errorf("read %s: %w", s.Destination, err)
		}
		updated := replaceOrAppendManagedBlock(string(existing), strings.TrimRight(body, "\n"), s.Marker)
		fileBytes = []byte(updated)
	}
	// Write via tempfile + PutFile. Mode 0644 — rc files are world-
	// readable by convention. ownerRoot=false: snippets land in user-
	// scope rc files OR in container drop-ins which we don't reach via
	// LocalDeployTarget (those flow through OCITarget).
	tmpDir, err := os.MkdirTemp("", "ov-shell-snippet-")
	if err != nil {
		return fmt.Errorf("tempdir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	tmpPath := filepath.Join(tmpDir, "snippet")
	if err := os.WriteFile(tmpPath, fileBytes, 0644); err != nil {
		return fmt.Errorf("stage snippet: %w", err)
	}
	if err := t.exec().PutFile(opts.ContextOrDefault(), tmpPath, s.Destination, 0644, false, opts); err != nil {
		return fmt.Errorf("write %s: %w", s.Destination, err)
	}
	t.noteStep(rec, StepKindShellSnippet, s.Scope(), s.Venue(),
		fmt.Sprintf("layer=%s shell=%s dest=%s", s.LayerName, s.Shell, s.Destination), start)
	rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)
	return nil
}

// ensureShellProbe populates t.shellsPresent on first call. Each shell
// in the allowlist is probed with `command -v <shell>` over the
// configured executor; presence is cached for the rest of Emit().
func (t *LocalDeployTarget) ensureShellProbe(opts EmitOpts) error {
	if t.shellsPresent != nil {
		return nil
	}
	t.shellsPresent = make(map[string]bool, len(ShellAllowlist))
	if opts.DryRun {
		// In dry-run, assume all shells present so the planner can show
		// what WOULD be written. Real probes only fire on live runs.
		for shell := range ShellAllowlist {
			t.shellsPresent[shell] = true
		}
		return nil
	}
	exec := t.exec()
	for shell := range ShellAllowlist {
		stdout, _, _, err := exec.RunCapture(opts.ContextOrDefault(),
			fmt.Sprintf("command -v %s >/dev/null 2>&1 && echo yes || echo no", shell))
		if err != nil {
			// Probe failure (executor unreachable, etc.) — treat as missing.
			t.shellsPresent[shell] = false
			continue
		}
		t.shellsPresent[shell] = strings.TrimSpace(stdout) == "yes"
	}
	return nil
}

// logSkipReason emits a single line on the target's stderr describing
// why a step was skipped. Mirrors the existing logSkip / logGated
// helpers but for per-step skip reasons not tied to VenueSkip / Gate.
func (t *LocalDeployTarget) logSkipReason(reason string, opts EmitOpts) {
	if opts.DryRun {
		fmt.Fprintf(t.stderr(), "[dry-run] skip: %s\n", reason)
		return
	}
	fmt.Fprintf(t.stderr(), "skip: %s\n", reason)
}

// isFileNotFoundErr returns true when err indicates "the file we tried
// to read doesn't exist". We treat that as a recoverable case for
// managed-block writes (no existing rc file → start fresh).
func isFileNotFoundErr(err error) bool {
	if err == nil {
		return false
	}
	if os.IsNotExist(err) {
		return true
	}
	// PutFile/GetFile wrap; fall back to substring sniffing for the
	// common ssh/cat case ("No such file or directory").
	return strings.Contains(err.Error(), "No such file or directory") ||
		strings.Contains(err.Error(), "no such file")
}

// ---------------------------------------------------------------------------
// Step executors
// ---------------------------------------------------------------------------

func (t *LocalDeployTarget) execShellHook(s *ShellHookStep, plan *InstallPlan, opts EmitOpts, rec *LayerRecord, start time.Time) error {
	if opts.DryRun {
		fmt.Fprintf(t.stderr(), "[dry-run] env.d/%s.env + managed block\n", s.LayerName)
		t.noteStep(rec, StepKindShellHook, s.Scope(), s.Venue(), fmt.Sprintf("layer=%s", s.LayerName), start)
		return nil
	}
	path, err := WriteEnvdFile(t.HostHome, s.LayerName, s.EnvVars, s.PathAdd)
	if err != nil {
		return err
	}
	s.EnvFile = path
	t.noteStep(rec, StepKindShellHook, s.Scope(), s.Venue(),
		fmt.Sprintf("env=%s", path), start)
	rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)
	return nil
}

func (t *LocalDeployTarget) execSystemPackages(s *SystemPackagesStep, plan *InstallPlan, opts EmitOpts, rec *LayerRecord, start time.Time) error {
	cmd, err := t.renderSystemPackageCommand(s)
	if err != nil {
		return err
	}
	if cmd == "" {
		// Phase has no host renderer (e.g. prepare/cleanup phases whose
		// container: blocks are container-only). Quietly skip.
		return nil
	}
	if err := t.runSystem(cmd, opts); err != nil {
		return fmt.Errorf("system packages %s: %w", s.Format, err)
	}
	t.noteStep(rec, StepKindSystemPackages, s.Scope(), s.Venue(),
		fmt.Sprintf("%s: %d packages (%s)", s.Format, len(s.Packages), s.Phase), start)
	rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)
	return nil
}

// renderSystemPackageCommand looks up the format's host-venue install template
// and renders it with the step's RawInstallContext. Returns "" when there's no
// host rendering for this phase (no error — means skip). Delegates to the shared
// package-level renderer so LocalDeployTarget and VmDeployTarget render packages
// through ONE config-driven path (R3).
func (t *LocalDeployTarget) renderSystemPackageCommand(s *SystemPackagesStep) (string, error) {
	return renderHostPackageCommand(t.DistroCfg, s)
}

// renderHostPackageCommand renders the host-venue package-install command for a
// SystemPackagesStep from the format's build.yml phase.install.host cell — the
// SAME PhaseTemplate + NewInstallContext + RenderTemplate path OCITarget uses
// for the container venue (R3). No hardcoded dnf/apt/pacman dispatch: the format
// (rpm/deb/pac) selects the template; the command is config-driven.
//
// Returns ("", nil) when the step is not an install-phase step, has no packages,
// or the format declares no host cell — all "nothing to run", not errors. A
// missing DistroConfig / format definition IS an error (the deploy can't honor
// a package step it can't render).
func renderHostPackageCommand(distroCfg *DistroConfig, s *SystemPackagesStep) (string, error) {
	if s.Phase != PhaseInstall || len(s.Packages) == 0 {
		return "", nil
	}
	if distroCfg == nil {
		return "", fmt.Errorf("no distro config for format %q host install", s.Format)
	}
	formatDef := distroCfg.FindFormat(s.Format)
	if formatDef == nil {
		return "", fmt.Errorf("no format %q in distro config", s.Format)
	}
	tmpl := formatDef.PhaseTemplate(PhaseInstall, VenueHostNative)
	if tmpl == "" {
		return "", nil // no host cell for this format → skip
	}
	ctx := NewInstallContext(s.RawInstallContext, formatDefCacheMountDefs(formatDef))
	cmd, err := RenderTemplate(s.Format+"-host-install", tmpl, ctx)
	if err != nil {
		return "", fmt.Errorf("rendering %s host install template: %w", s.Format, err)
	}
	return strings.TrimSpace(cmd), nil
}

func (t *LocalDeployTarget) execBuilder(s *BuilderStep, plan *InstallPlan, opts EmitOpts, rec *LayerRecord, start time.Time) error {
	// Builder image resolution mirrors VmDeployTarget.execBuilder:
	//   1. EmitOpts.BuilderImageOverride (--builder-image flag)
	//   2. BuilderStep.BuilderImage (compiled from image.yml)
	//   3. t.BuilderImageResolver (rarely wired)
	image := opts.BuilderImageOverride
	if image == "" {
		image = s.BuilderImage
	}
	if image == "" && t.BuilderImageResolver != nil {
		image = t.BuilderImageResolver(s.Builder)
	}
	if image == "" {
		return fmt.Errorf("no builder image for %s (layer=%s); set --builder-image or define builder.%s in image.yml", s.Builder, s.LayerName, s.Builder)
	}

	// aur builds package files that the venue installs via the format's
	// package manager — gate on the format's local_pkg.probe (e.g.
	// `command -v pacman`) succeeding on the VENUE, not a hardcoded
	// distro/builder-name check. s.LocalPkg is set (the pac local_pkg block) only
	// for the aur builder; nil for pixi/npm/cargo (no package-file install).
	if s.LocalPkg != nil && !venueHasPkgManager(opts.ContextOrDefault(), t.exec(), s.LocalPkg, opts) {
		return fmt.Errorf("builder %q (layer=%s) builds %s package files but the target has no %s package manager (local_pkg.probe %q failed); cannot install the built packages",
			s.Builder, s.LayerName, s.LocalPkg.DepBuilder, s.LocalPkg.DepBuilder, s.LocalPkg.Probe)
	}

	bindMounts, err := UserScopeBindMounts(t.HostHome)
	if err != nil {
		return err
	}
	envVars := UserScopeEnv(t.HostHome)
	var aurStage string
	if s.Builder == "aur" {
		aurStage, err = os.MkdirTemp("", "ov-aur-")
		if err != nil {
			return err
		}
		RegisterTempCleanup(aurStage)
		defer func() { os.RemoveAll(aurStage); UnregisterTempCleanup(aurStage) }()
		bindMounts["/tmp/aur-pkgs"] = aurStage
	}

	// Render the builder-specific bash script. Each supported builder
	// emits the exact commands that would run inside its stage in the
	// OCI path — but adapted for bash execution inside the builder
	// container, with HOME-remap already handled by BuilderRunOpts.
	script, err := renderBuilderScript(s, t.HostHome)
	if err != nil {
		return err
	}

	_, err = BuilderRun(opts.ContextOrDefault(), BuilderRunOpts{
		BuilderImage: image,
		LayerDir:     s.LayerDir,
		ScriptBody:   script,
		BindMounts:   bindMounts,
		Env:          envVars,
		HostHome:     t.HostHome,
		DryRun:       opts.DryRun,
		Cfg:          t.Cfg,
		ProjectDir:   t.ProjectDir,
		// Rootless-podman bind-mount semantics: with `--user N:N` (N != 0)
		// the in-container user is mapped to a subordinate uid from
		// /etc/subuid that does NOT match the operator's host uid that
		// owns $HOME/.cargo / $HOME/.npm-global / etc. — bind-mounts
		// appear as "nobody"-owned and writes fail with EACCES. With
		// `--user 0:0` rootless podman maps in-container uid 0 → host
		// invoking-user uid, so files written by container-root are
		// owned by the operator on the host and bind-mounts are
		// writable. `--userns=keep-id` would also fix this on paper
		// but triggers a `readlink \`\`: No such file or directory`
		// crun bug on common podman 5.x / crun 1.27 combinations.
		//
		// AUR is a special case: yay/makepkg refuse root by design, so the
		// aur builder's phase.install.host cell (build.yml builder.aur, the
		// host analog of stage_template) starts as root, configures NOPASSWD
		// for the unprivileged user, then drops to that user via `sudo -u` for
		// the yay invocation. Result: yay runs as user (no root warnings), but
		// yay's own internal `sudo pacman -U` for build deps works via NOPASSWD.
		RunAsRoot: true,
	})
	if err != nil {
		return err
	}

	// aur host-install: pacman -U the produced packages.
	//
	// Loud-fail when the builder produced zero artifacts. yay can return
	// exit 0 even when an internal step (e.g. silent sudo prompt, fetch
	// failure, signature failure) leaves /tmp/aur-build empty. Without
	// this check, the deploy "succeeds" but the package isn't installed
	// — invisible to operators and downstream eval probes.
	if s.Builder == "aur" && !opts.DryRun {
		matches, _ := filepath.Glob(filepath.Join(aurStage, "*.pkg.tar.zst"))
		if len(matches) == 0 {
			pkgList := extractStringSlice(s.RawStageContext, "packages")
			return fmt.Errorf("aur builder for layer %q produced zero .pkg.tar.zst artifacts in %s; expected packages: %v. Check the BuilderRun output above for the actual yay/makepkg failure",
				s.LayerName, aurStage, pkgList)
		}
		// Pre-removal of `replaces:` entries — distro-repo packages
		// that conflict with the AUR build artifact (file ownership
		// collisions). `pacman -U` would otherwise abort with
		// "unresolvable package conflicts". Idempotent — entries that
		// aren't installed are skipped silently.
		if replaces := extractStringSlice(s.RawStageContext, "replaces"); len(replaces) > 0 {
			if err := removeInstalledPacmanPackages(replaces, opts); err != nil {
				return fmt.Errorf("aur replaces (pacman -Rs): %w", err)
			}
		}
		args := append([]string{"pacman", "-U", "--noconfirm"}, matches...)
		if err := runSudoArgs(args, opts); err != nil {
			return fmt.Errorf("pacman -U: %w", err)
		}
	}

	t.noteStep(rec, StepKindBuilder, s.Scope(), s.Venue(),
		fmt.Sprintf("%s (image=%s, layer=%s)", s.Builder, image, s.LayerName), start)
	rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)
	return nil
}

func (t *LocalDeployTarget) execTask(s *TaskStep, plan *InstallPlan, opts EmitOpts, rec *LayerRecord, start time.Time) error {
	if s.Task == nil {
		return nil
	}
	// copy: stages a layer file onto the venue through the executor's
	// PutFile — a plain `install` locally, scp+install over SSH. This is
	// the ONE path both LocalDeployTarget and VmDeployTarget share for
	// file-copy tasks (a rendered `install <layerDir>/<f> <dst>` only works
	// when the layer dir is on the same host the command runs on, which is
	// false for SSH/VM deploys — the historic VM copy:-task bug).
	if s.Task.Copy != "" {
		src := filepath.Join(s.LayerDir, s.Task.Copy)
		// Prefer the home-resolved dest (s.To) so `to: ${HOME}/...` expands to
		// the real host home rather than staying a literal "${HOME}".
		dst := s.To
		if dst == "" {
			dst = s.Task.To
		}
		if dst == "" {
			dst = s.Task.Copy
		}
		if err := t.exec().PutFile(opts.ContextOrDefault(), src, dst, parseTaskMode(s.Task.Mode, 0o644), s.Scope() == ScopeSystem, opts); err != nil {
			return err
		}
		kind, _ := s.Task.Kind()
		t.noteStep(rec, StepKindTask, s.Scope(), s.Venue(),
			fmt.Sprintf("%s: %s", kind, taskSummary(s.Task)), start)
		rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)
		return nil
	}
	cmd, err := renderTaskCommand(s)
	if err != nil {
		return err
	}
	if cmd == "" {
		return nil
	}
	if s.Scope() == ScopeSystem {
		if err := t.runSystem(cmd, opts); err != nil {
			return err
		}
	} else {
		if err := t.runUser(cmd, opts); err != nil {
			return err
		}
	}
	kind, _ := s.Task.Kind()
	t.noteStep(rec, StepKindTask, s.Scope(), s.Venue(),
		fmt.Sprintf("%s: %s", kind, taskSummary(s.Task)), start)
	rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)
	return nil
}

// renderTaskCommand turns a TaskStep into a shell command suitable for
// sudo/user heredoc execution. Verbs handled:
//   - mkdir      → `install -d -m<mode> <path>`
//   - download   → curl to a tmp file, optionally extract, install
//   - cmd        → the cmd body verbatim (with /ctx/ rewritten to $CTX)
//   - copy/write → install -m<mode> -o<owner> <source> <dest>
//   - link       → ln -sf <target> <link>
//   - setcap     → setcap <caps> <file>
//
// For v1 of the host target we implement cmd, mkdir, and link — the
// most common verbs. The rest fall back to a "not yet supported" error
// that tests can verify against.
// renderTaskCommand turns a non-copy TaskStep into a shell command. It is
// package-level (uses only the step, not the target) so LocalDeployTarget AND
// VmDeployTarget render every task verb through ONE code path. copy: is NOT
// handled here — it is staged via the executor's PutFile in execTask, the only
// portable way to place a layer file on a remote/SSH venue.
func renderTaskCommand(s *TaskStep) (string, error) {
	task := s.Task
	ctxPath := s.CtxPath

	switch {
	case task.Cmd != "":
		body := task.Cmd
		if ctxPath != "" {
			body = strings.ReplaceAll(body, "/ctx/", ctxPath+"/")
		}
		// Prepend BUILD_ARCH/ARCH + layer.vars exports so cmd bodies
		// templating ${ARCH} / ${MY_LAYER_VAR} resolve at deploy-time
		// the same as they do at build-time. Build-time gets these
		// from BuildKit's TARGETARCH ENV + emitVarsEnv ENV directives.
		if preamble := taskShellPreamble(s); preamble != "" {
			body = preamble + body
		}
		return body, nil
	case task.Mkdir != "":
		mode := task.Mode
		if mode == "" {
			mode = "0755"
		}
		return fmt.Sprintf("install -d -m%s %s", mode, shDoubleQuote(task.Mkdir)), nil
	case task.Link != "":
		target := task.Target
		if target == "" {
			target = task.To
		}
		return fmt.Sprintf("ln -sfn %s %s", shDoubleQuote(target), shDoubleQuote(task.Link)), nil
	case task.Setcap != "":
		caps := task.Caps
		return fmt.Sprintf("setcap %s %s", shDoubleQuote(caps), shDoubleQuote(task.Setcap)), nil
	case task.Copy != "":
		// Handled in execTask via the executor's PutFile (portable across
		// local + SSH). Reaching here means execTask didn't intercept it.
		return "", fmt.Errorf("copy: task must be staged via PutFile, not rendered")
	case task.Write != "":
		mode := task.Mode
		if mode == "" {
			mode = "0644"
		}
		return fmt.Sprintf("install -m%s /dev/stdin %s <<'OV_WRITE'\n%s\nOV_WRITE",
			mode, shDoubleQuote(task.Write), task.Content), nil
	case task.Download != "":
		return renderDownloadScript(task, s.LayerVars), nil
	}
	return "", fmt.Errorf("task has no supported verb: %+v", task)
}

// parseTaskMode parses a layer task mode string ("0644", "0o755") into a
// uint32 file mode for PutFile, falling back to def when empty/unparseable.
func parseTaskMode(mode string, def uint32) uint32 {
	if mode == "" {
		return def
	}
	v, err := strconv.ParseUint(strings.TrimPrefix(mode, "0o"), 8, 32)
	if err != nil {
		return def
	}
	return uint32(v)
}

// taskShellPreamble returns the BUILD_ARCH/ARCH exports plus any
// layer.vars exports (sorted for deterministic output) so cmd: bodies
// can reference ${ARCH} / ${MY_LAYER_VAR} at deploy-time the same way
// they do at build-time. Trailing newline included; safe to prepend
// to a script.
func taskShellPreamble(s *TaskStep) string {
	var b strings.Builder
	b.WriteString(buildArchExports())
	if len(s.LayerVars) > 0 {
		keys := make([]string, 0, len(s.LayerVars))
		for k := range s.LayerVars {
			keys = append(keys, k)
		}
		sortStrings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "export %s=%s\n", k, shQuoteArg(s.LayerVars[k]))
		}
	}
	// Task.Env (sorted) — declared env: on the task AND the secret-injection
	// path (layer_secrets.go InjectSecretsIntoPlans) which sets task.Env to
	// credential-store-resolved values. Exporting here means BOTH local and VM
	// deploys propagate them to cmd: bodies through this one shared preamble.
	if s.Task != nil && len(s.Task.Env) > 0 {
		keys := make([]string, 0, len(s.Task.Env))
		for k := range s.Task.Env {
			keys = append(keys, k)
		}
		sortStrings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "export %s=%s\n", k, shQuoteArg(s.Task.Env[k]))
		}
	}
	return b.String()
}

// renderDownloadScript emits a shell snippet that fetches task.Download
// to a temp file, optionally extracts it into task.To, then cleans up.
// Handles the same flags the container path respects: extract (tar.gz
// / tar.xz / tar.zst / zip / sh / none), strip_components, include,
// mode (applied to the resulting file or directory), env vars injected
// during the download (used by install scripts).
//
// layerVars are exported alongside task.Env so layer.yml `vars:` keys
// referenced inside the download URL (e.g. ${K3D_VERSION}) resolve
// correctly. Build-time gets these via Containerfile ENV; deploy-time
// has no equivalent without this.
func renderDownloadScript(task *Task, layerVars map[string]string) string {
	url := task.Download
	to := task.To
	extract := task.Extract
	if extract == "" {
		// Heuristic to match the container behavior: detect by URL suffix.
		switch {
		case strings.HasSuffix(url, ".tar.gz") || strings.HasSuffix(url, ".tgz"):
			extract = "tar.gz"
		case strings.HasSuffix(url, ".tar.xz"):
			extract = "tar.xz"
		case strings.HasSuffix(url, ".tar.zst"):
			extract = "tar.zst"
		case strings.HasSuffix(url, ".zip"):
			extract = "zip"
		case strings.HasSuffix(url, ".sh"):
			extract = "sh"
		default:
			extract = "none"
		}
	}

	// Emit each env var as a prefix export so the downloaded script can
	// see it (matches the container behavior). layerVars come first
	// (lower priority) so per-task task.Env values override on key
	// collision — same precedence the container path gets via ENV +
	// per-RUN env overrides.
	var envPrefix strings.Builder
	if len(layerVars) > 0 {
		lkeys := make([]string, 0, len(layerVars))
		for k := range layerVars {
			lkeys = append(lkeys, k)
		}
		sortStrings(lkeys)
		for _, k := range lkeys {
			fmt.Fprintf(&envPrefix, "export %s=%s\n", k, shQuoteArg(layerVars[k]))
		}
	}
	keys := make([]string, 0, len(task.Env))
	for k := range task.Env {
		keys = append(keys, k)
	}
	sortStrings(keys)
	for _, k := range keys {
		fmt.Fprintf(&envPrefix, "export %s=%s\n", k, shQuoteArg(task.Env[k]))
	}

	var b strings.Builder
	b.WriteString("set -e\n")
	// BUILD_ARCH=$(uname -m) so download URLs can template the arch in
	// shell-expansion form (e.g. `uv-${BUILD_ARCH}-unknown-linux-gnu.tar.gz`).
	// Must match the container-build renderer in tasks.go which exports the
	// same var — otherwise the same layer.yml download works at build time
	// but fails under `ov deploy add host/vm:<name>`.
	//
	// ARCH is the BuildKit-style triplet (amd64/arm64/arm) — same format
	// the container-build path gets from BuildKit's TARGETARCH. Without
	// this, layers that template `${ARCH}` into a download URL (e.g.
	// the kubernetes layer's `k3d-linux-${ARCH}`) get an empty value at
	// host-deploy time and curl 404s. Mapping uname-style → BuildKit
	// covers the architectures ov officially targets.
	b.WriteString(buildArchExports())
	b.WriteString(envPrefix.String())
	// tmp location deterministic per-task so retries don't leak.
	b.WriteString("ovtmp=\"$(mktemp -d)\"\n")
	b.WriteString("trap 'rm -rf \"$ovtmp\"' EXIT\n")

	// URLs are emitted in double-quoted form so ${BUILD_ARCH} (and any
	// other shell-level vars authors rely on) expand at runtime. Escape
	// the set of chars that have special meaning inside double-quotes.
	quotedURL := shDoubleQuote(url)

	if extract == "none" {
		// Download directly to the target path.
		mode := task.Mode
		if mode == "" {
			mode = "0755"
		}
		fmt.Fprintf(&b, "install -d -m0755 %s\n", shQuoteArg(filepath.Dir(to)))
		fmt.Fprintf(&b, "curl -fL --retry 3 -o %s %s\n", shQuoteArg(to), quotedURL)
		fmt.Fprintf(&b, "chmod %s %s\n", mode, shQuoteArg(to))
		return b.String()
	}

	// Fetch to the tmpdir first, then extract.
	fmt.Fprintf(&b, "curl -fL --retry 3 -o \"$ovtmp/archive\" %s\n", quotedURL)
	fmt.Fprintf(&b, "install -d -m0755 %s\n", shQuoteArg(to))

	strip := ""
	if task.StripComponents > 0 {
		strip = fmt.Sprintf(" --strip-components=%d", task.StripComponents)
	}
	includeFilter := ""
	if len(task.Include) > 0 {
		quoted := make([]string, 0, len(task.Include))
		for _, p := range task.Include {
			quoted = append(quoted, shQuoteArg(p))
		}
		includeFilter = " " + strings.Join(quoted, " ")
	}

	switch extract {
	case "tar.gz":
		fmt.Fprintf(&b, "tar -xzf \"$ovtmp/archive\" -C %s%s%s\n", shQuoteArg(to), strip, includeFilter)
	case "tar.xz":
		fmt.Fprintf(&b, "tar -xJf \"$ovtmp/archive\" -C %s%s%s\n", shQuoteArg(to), strip, includeFilter)
	case "tar.zst":
		fmt.Fprintf(&b, "tar --zstd -xf \"$ovtmp/archive\" -C %s%s%s\n", shQuoteArg(to), strip, includeFilter)
	case "zip":
		// unzip doesn't support strip_components natively; emulate when requested.
		if task.StripComponents > 0 {
			fmt.Fprintf(&b, "unzip -q \"$ovtmp/archive\" -d \"$ovtmp/unpack\"\n")
			fmt.Fprintf(&b, "(cd \"$ovtmp/unpack\" && ")
			for i := 0; i < task.StripComponents; i++ {
				b.WriteString("cd \"$(ls -1 | head -1)\" && ")
			}
			fmt.Fprintf(&b, "cp -a . %s)\n", shQuoteArg(to))
		} else {
			fmt.Fprintf(&b, "unzip -q \"$ovtmp/archive\" -d %s\n", shQuoteArg(to))
		}
	case "sh":
		// Self-installing script. Execute with configured env.
		fmt.Fprintf(&b, "chmod +x \"$ovtmp/archive\"\n")
		fmt.Fprintf(&b, "\"$ovtmp/archive\"\n")
	default:
		fmt.Fprintf(&b, "echo 'unsupported extract format: %s' >&2 && exit 1\n", extract)
	}

	return b.String()
}

func (t *LocalDeployTarget) execFile(s *FileStep, plan *InstallPlan, opts EmitOpts, rec *LayerRecord, start time.Time) error {
	mode := fmt.Sprintf("%04o", s.Mode.Perm())
	owner := ""
	if s.Owner != "" && s.Owner != "root" {
		owner = fmt.Sprintf(" -o %s", s.Owner)
	}
	cmd := fmt.Sprintf("install -m%s%s %s %s", mode, owner, shQuoteArg(s.Source), shQuoteArg(s.Dest))
	if s.Scope() == ScopeSystem {
		if err := t.runSystem(cmd, opts); err != nil {
			return err
		}
	} else {
		if err := t.runUser(cmd, opts); err != nil {
			return err
		}
	}
	t.noteStep(rec, StepKindFile, s.Scope(), s.Venue(), s.Dest, start)
	rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)
	return nil
}

func (t *LocalDeployTarget) execServicePackaged(s *ServicePackagedStep, plan *InstallPlan, opts EmitOpts, rec *LayerRecord, start time.Time) error {
	// Query prior enabled state for restore-on-teardown.
	s.PriorEnabled = systemctlIsEnabled(s.Unit, s.TargetScope)

	// Optional drop-in write.
	if s.OverridesText != "" && s.OverridesPath != "" {
		if err := writeDropin(s.OverridesPath, s.OverridesText, s.TargetScope, opts); err != nil {
			return err
		}
	}
	if s.Enable {
		if err := systemctlEnable(s.Unit, s.TargetScope, opts); err != nil {
			return err
		}
	}
	t.noteStep(rec, StepKindServicePackaged, s.Scope(), s.Venue(),
		fmt.Sprintf("enable %s (prior=%v)", s.Unit, s.PriorEnabled), start)
	rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)
	return nil
}

func (t *LocalDeployTarget) execServiceCustom(s *ServiceCustomStep, plan *InstallPlan, opts EmitOpts, rec *LayerRecord, start time.Time) error {
	if s.UnitPath == "" || s.UnitText == "" {
		return fmt.Errorf("service %s: no unit text rendered (compile-time render skipped this entry; check that the layer's mixed-`service:` pair is well-formed)", s.Name)
	}
	if err := detectPackagedUnitConflict(s.UnitPath, s.TargetScope, rec.Layer); err != nil {
		return err
	}
	if err := writeServiceUnit(s.UnitPath, s.UnitText, s.TargetScope, opts); err != nil {
		return err
	}
	if s.Enable {
		if err := systemctlEnable(s.Name, s.TargetScope, opts); err != nil {
			return err
		}
	}
	t.noteStep(rec, StepKindServiceCustom, s.Scope(), s.Venue(),
		fmt.Sprintf("%s → %s", s.Name, s.UnitPath), start)
	rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)
	return nil
}

func (t *LocalDeployTarget) execRepoChange(s *RepoChangeStep, plan *InstallPlan, opts EmitOpts, rec *LayerRecord, start time.Time) error {
	cmd := fmt.Sprintf("mkdir -p %s && cat > %s <<'OV_REPO'\n%s\nOV_REPO",
		shQuoteArg(filepath.Dir(s.File)), shQuoteArg(s.File), s.Content)
	if err := t.runSystem(cmd, opts); err != nil {
		return err
	}
	t.noteStep(rec, StepKindRepoChange, s.Scope(), s.Venue(), s.File, start)
	rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)
	return nil
}

// execLocalPkg builds the layer's bundled package source on the host and
// installs the result onto the deploy venue — the proper-package counterpart of
// the layer's curl/COPY cmd: task (for the `ov` layer this lands overthink-git
// at /usr/bin/ov instead of an untracked /usr/local/bin/ov). The build/install
// commands, package glob, and probe all come from the package format's
// `local_pkg:` config (config-driven). Gated on the VENUE having the format's
// package manager (an Arch/CachyOS host, or an Arch SSH host for a
// `host: user@machine` local deploy); an unsupported venue or a missing source
// dir is a clean no-op (the layer's curl task installs it there). The shared
// build+transfer+install body lives in localpkg.go (R3 — the VM target calls the
// same execLocalPkgInstall).
func (t *LocalDeployTarget) execLocalPkg(s *LocalPkgInstallStep, plan *InstallPlan, opts EmitOpts, rec *LayerRecord, start time.Time) error {
	ctx := opts.ContextOrDefault()
	supported := venueHasPkgManager(ctx, t.exec(), s.LocalPkg, opts)
	if err := execLocalPkgInstall(ctx, t.exec(), s, supported, t.Name(), t.Cfg, opts); err != nil {
		return err
	}
	venue := "host"
	if !supported {
		venue = "skipped (unsupported package format)"
	}
	t.noteStep(rec, StepKindLocalPkgInstall, s.Scope(), s.Venue(),
		fmt.Sprintf("layer=%s pkgbuild=%s (%s)", s.LayerName, s.PkgbuildRef, venue), start)
	return nil
}

// ---------------------------------------------------------------------------
// Shell execution helpers
// ---------------------------------------------------------------------------

// runSudoShell wraps a bash snippet in `sudo bash <<EOF`. Uses
// cmd.Stdin so the script body isn't exposed in the argv (cleaner
// ps/audit output).
//
// Always uses sudo -n (non-interactive). target:local deploys with
// allow_root_tasks: true assume the operator has NOPASSWD sudo on the
// host (sudoRefresh verifies this as a preflight). With -n, a missing
// NOPASSWD policy fails FAST with "a password is required" instead of
// either (a) hanging forever waiting for stdin, or (b) consuming the
// script body as a password in tty-less / background contexts.
func runSudoShell(script string, opts EmitOpts) error {
	if opts.DryRun {
		fmt.Fprintln(os.Stderr, "[dry-run] sudo -n bash <<OV_ROOT")
		fmt.Fprintln(os.Stderr, script)
		fmt.Fprintln(os.Stderr, "OV_ROOT")
		return nil
	}
	cmd := exec.Command("sudo", "-n", "bash", "-s")
	cmd.Stdin = strings.NewReader(script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// removeInstalledPacmanPackages runs `pacman -Rs --noconfirm <pkgs>`
// for the subset of `pkgs` that are actually installed UNDER THAT
// EXACT NAME. Used by the AUR `replaces:` mechanism to clear
// distro-repo packages that conflict (on file ownership) with an
// AUR build artifact, BEFORE the `pacman -U` install. Idempotent —
// packages not installed are silently skipped, so re-runs of the
// deploy don't error.
//
// `pacman -Qq <pkg>` resolves virtual `Provides=` aliases: querying
// `code` on a host where `visual-studio-code-bin` is installed
// (which declares `Provides=code`) returns `visual-studio-code-bin`
// and exits 0. `pacman -Rs <pkg>`, by contrast, only accepts real
// package names and exits with `target not found` for provides-only
// names. To preserve idempotency on re-runs after a successful AUR
// install, the precheck must compare the returned name to the
// queried name and only add to the remove-list on an exact match.
// Otherwise a re-run after vscode (visual-studio-code-bin Provides=code)
// halts the entire deploy with `error: target not found: code`.
func removeInstalledPacmanPackages(pkgs []string, opts EmitOpts) error {
	var installed []string
	for _, pkg := range pkgs {
		if pkg == "" {
			continue
		}
		// `pacman -Qq <pkg>` returns the real package name on stdout
		// (resolving Provides= aliases). Only treat as installed when
		// the returned name exactly matches the queried name — that's
		// the only case `pacman -Rs <pkg>` will accept downstream.
		out, err := exec.Command("pacman", "-Qq", pkg).Output()
		if err != nil {
			continue
		}
		if pacmanQqInstalledExactly(pkg, out) {
			installed = append(installed, pkg)
		}
	}
	if len(installed) == 0 {
		return nil
	}
	args := append([]string{"pacman", "-Rs", "--noconfirm"}, installed...)
	return runSudoArgs(args, opts)
}

// pacmanQqInstalledExactly returns true when `pacman -Qq <queried>`
// stdout, after trimming, equals the queried name — i.e., the package
// is actually installed under that exact name, NOT via a virtual
// Provides= alias. Pure helper, unit-tested in deploy_target_local_test.go.
func pacmanQqInstalledExactly(queried string, qqOutput []byte) bool {
	return strings.TrimSpace(string(qqOutput)) == queried
}

// runSudoArgs spawns sudo with explicit argv (no shell interpretation).
// Used for one-shot commands like `sudo pacman -U <pkg1> <pkg2> …`.
// Same -n rationale as runSudoShell.
func runSudoArgs(argv []string, opts EmitOpts) error {
	if opts.DryRun {
		fmt.Fprintln(os.Stderr, "[dry-run] sudo -n "+shellJoin(argv))
		return nil
	}
	cmd := exec.Command("sudo", append([]string{"-n"}, argv...)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// runUserShell runs a script as the invoking user (no sudo).
func runUserShell(script string, opts EmitOpts) error {
	if opts.DryRun {
		fmt.Fprintln(os.Stderr, "[dry-run] bash <<OV_USER")
		fmt.Fprintln(os.Stderr, script)
		fmt.Fprintln(os.Stderr, "OV_USER")
		return nil
	}
	cmd := exec.Command("bash", "-s")
	cmd.Stdin = strings.NewReader(script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// sudoRefresh runs `sudo -v` to refresh the sudo timestamp so later
// `sudo bash` invocations don't prompt within the ~5-minute window.
//
// Short-circuits when NOPASSWD sudo is effective: `sudo -n true` succeeds
// for users with passwordless sudo policy, so there's no credential cache
// that needs priming and `sudo -v` (which requires a TTY for the
// password-prompt fallback) is unnecessary. This makes the rebuild work
// in tty-less contexts (background tasks, CI runners, AI agents) on
// machines with NOPASSWD configured.
func sudoRefresh() error {
	if exec.Command("sudo", "-n", "true").Run() == nil {
		return nil // NOPASSWD effective; nothing to refresh.
	}
	cmd := exec.Command("sudo", "-v")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

// ---------------------------------------------------------------------------
// Systemd helpers
// ---------------------------------------------------------------------------

// packagedUnitDirs is the lookup order for OS-package-shipped systemd
// units. Tests swap this slice to point at a fixture root.
var packagedUnitDirs = []string{
	"/usr/lib/systemd/system",
	"/lib/systemd/system",
}

// detectPackagedUnitConflict returns an error when a custom system-scope
// service would shadow a unit shipped by an OS package. Writing
// /etc/systemd/system/<name>.service silently overrides
// /usr/lib/systemd/system/<name>.service and replaces socket activation
// or other distro-managed behavior. The error message points authors
// at use_packaged: as the canonical remediation.
func detectPackagedUnitConflict(unitPath string, scope Scope, layerName string) error {
	if scope != ScopeSystem {
		return nil
	}
	unitName := filepath.Base(unitPath)
	for _, dir := range packagedUnitDirs {
		packagedPath := filepath.Join(dir, unitName)
		if _, err := os.Stat(packagedPath); err == nil {
			return fmt.Errorf(
				"service %q from layer %q would override the packaged unit at %s. "+
					"To respect the distro's native unit, set `use_packaged: %s` on the service entry "+
					"(drop-in overrides are still applied). To replace it anyway, change `scope:` to "+
					"`user` for a per-user unit, or rename the service",
				unitName, layerName, packagedPath, unitName,
			)
		}
	}
	return nil
}

func systemctlIsEnabled(unit string, scope Scope) bool {
	args := []string{"is-enabled", "--quiet", unit}
	if scope == ScopeUser {
		args = append([]string{"--user"}, args...)
	}
	cmd := exec.Command("systemctl", args...)
	return cmd.Run() == nil
}

func systemctlEnable(unit string, scope Scope, opts EmitOpts) error {
	args := []string{"enable", "--now", unit}
	if scope == ScopeUser {
		args = append([]string{"--user"}, args...)
		if opts.DryRun {
			fmt.Fprintf(os.Stderr, "[dry-run] systemctl --user enable --now %s\n", unit)
			return nil
		}
		cmd := exec.Command("systemctl", args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	// System scope → sudo.
	return runSudoArgs(append([]string{"systemctl"}, args...), opts)
}

func writeDropin(path, content string, scope Scope, opts EmitOpts) error {
	return writeUnitLikeFile(path, content, scope, opts)
}

func writeServiceUnit(path, content string, scope Scope, opts EmitOpts) error {
	return writeUnitLikeFile(path, content, scope, opts)
}

func writeUnitLikeFile(path, content string, scope Scope, opts EmitOpts) error {
	if scope == ScopeUser {
		if opts.DryRun {
			fmt.Fprintf(os.Stderr, "[dry-run] write user-scope file %s:\n%s\n", path, content)
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return err
		}
		return nil
	}
	// System scope → sudo. This helper is a standalone function (not
	// a LocalDeployTarget method), so it uses the package-level
	// runSudoShell directly — writing system-scope unit files is a
	// local-host-only operation, never a nested-venue one.
	cmd := fmt.Sprintf("mkdir -p %s && cat > %s <<'OV_UNIT'\n%s\nOV_UNIT",
		shQuoteArg(filepath.Dir(path)), shQuoteArg(path), content)
	return runSudoShell(cmd, opts)
}

// ---------------------------------------------------------------------------
// Misc helpers
// ---------------------------------------------------------------------------

func (t *LocalDeployTarget) stderr() *os.File {
	if t.DryRunWriter != nil {
		return t.DryRunWriter
	}
	return os.Stderr
}

func (t *LocalDeployTarget) logSkip(batch StepBatch, opts EmitOpts) {
	for _, s := range batch.Steps {
		fmt.Fprintf(t.stderr(), "[skip] %s scope=%s reason=container-only\n", s.Kind(), s.Scope())
	}
}

func (t *LocalDeployTarget) logGated(step InstallStep, gate Gate, opts EmitOpts) {
	fmt.Fprintf(t.stderr(), "[skip] %s scope=%s requires --%s\n", step.Kind(), step.Scope(), gate)
}

func (t *LocalDeployTarget) noteStep(rec *LayerRecord, kind StepKind, scope Scope, venue Venue, summary string, start time.Time) {
	rec.Steps = append(rec.Steps, StepRecord{
		Kind:        kind,
		Scope:       scope,
		Venue:       venue,
		Summary:     summary,
		CompletedAt: time.Now().UTC().Format(time.RFC3339),
	})
}

// taskSummary returns a short one-line summary for ledger display.
func taskSummary(task *Task) string {
	var b bytes.Buffer
	switch {
	case task.Cmd != "":
		body := task.Cmd
		if len(body) > 40 {
			body = body[:40] + "…"
		}
		b.WriteString(body)
	case task.Mkdir != "":
		b.WriteString("mkdir " + task.Mkdir)
	case task.Copy != "":
		b.WriteString("copy " + task.Copy)
	case task.Write != "":
		b.WriteString("write " + task.Write)
	case task.Link != "":
		b.WriteString("link " + task.Link)
	case task.Download != "":
		b.WriteString("download " + task.Download)
	case task.Setcap != "":
		b.WriteString("setcap " + task.Setcap)
	}
	return b.String()
}

// shQuoteArg single-quotes an argument for POSIX shell embedding. Same
// semantics as shQuoteEnv in shell_profile.go but exposed here as a
// separate name to avoid a cross-file dependency during refactor.
func shQuoteArg(v string) string {
	if v == "" {
		return `''`
	}
	if !strings.ContainsAny(v, " \t\n\"'$*?[](){}<>|&;`\\!") {
		return v
	}
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}

// shDoubleQuote wraps a string in double quotes for a shell context where
// variable expansion MUST still happen (e.g. download URLs that template
// ${BUILD_ARCH}). Escapes the four metachars that break out of a double-
// quoted string: backslash, backtick, double-quote, and dollar-sign —
// but $-escaping is selective: only escape bare `$` not followed by a
// valid var-reference character so authored `${FOO}` / `$FOO` still expand.
func shDoubleQuote(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, "`", "\\`")
	v = strings.ReplaceAll(v, `"`, `\"`)
	return `"` + v + `"`
}

// ContextOrDefault returns opts' context if one's attached, or a
// background context. Used by BuilderRun callers.
func (o EmitOpts) ContextOrDefault() context.Context {
	return context.Background()
}

// hostBuilderContext is the template context for a builder's
// phase.install.host cell. Mirrors the data the deleted render*Script
// helpers used: HOME/PIXI_CACHE_DIR/NPM_CONFIG_PREFIX/CARGO_HOME are injected
// by BuilderRunOpts.Env (the cells read them as $HOME/$CARGO_HOME), so the only
// template-visible datum is the package list (consumed by the aur cell).
type hostBuilderContext struct {
	HostHome string
	Packages []string
}

// renderBuilderScript turns a BuilderStep into the bash script that runs inside
// the builder container. It is the host-side analog of the container
// stage_template, and is now fully config-driven: it renders the builder's
// phase.install.host cell (build.yml builder.<name>.phase.install.host) via the
// SAME RenderTemplate engine the OCI path uses for stage_template — no hardcoded
// per-builder Go. HOME/PIXI_CACHE_DIR/NPM_CONFIG_PREFIX/CARGO_HOME are injected
// by BuilderRunOpts.Env before the script starts.
func renderBuilderScript(s *BuilderStep, hostHome string) (string, error) {
	if s.BuilderDef == nil {
		return "", fmt.Errorf("builder %q: no builder definition (BuilderDef unset)", s.Builder)
	}
	tmpl := s.BuilderDef.PhaseTemplate(PhaseInstall, VenueHostNative)
	if tmpl == "" {
		return "", fmt.Errorf("builder %q: no phase.install.host template in build.yml", s.Builder)
	}
	ctx := hostBuilderContext{
		HostHome: hostHome,
		Packages: extractStringSlice(s.RawStageContext, "packages"),
	}
	script, err := RenderTemplate(s.Builder+"-host", tmpl, ctx)
	if err != nil {
		return "", fmt.Errorf("rendering %s host builder template: %w", s.Builder, err)
	}
	return script, nil
}
