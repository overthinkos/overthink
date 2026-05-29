package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// VmDeployTarget applies an InstallPlan inside a running VM over SSH.
// Uses the same InstallPlan IR that LocalDeployTarget consumes — the
// only difference is that bash bodies run via `ssh vm 'sudo bash -s'`
// instead of local `sudo bash -s`.
//
// Ledger writes land on the GUEST filesystem (under the guest user's
// ~/.config/overthink/installed/), not the host's. Teardown runs in
// the guest via SSH.
//
// See the approved plan D7.
type VmDeployTarget struct {
	// Name is the deploy name (e.g. "vm:arch" — retains the
	// `vm:` prefix for ledger keying so two VMs with the same image
	// name don't collide).
	Name string

	// VMName is the underlying kind:vm entity name (e.g. "arch").
	VMName string

	// Spec is the resolved kind:vm entity.
	Spec *VmSpec

	// State is the persisted deploy state for this VM. Written back to
	// deploy.yml after a successful Emit.
	State *VmDeployState

	// Exec is the DeployExecutor wired to the guest (typically an
	// *SSHExecutor). The executor encapsulates ssh + scp invocation
	// details so the same Emit flow would work with any future
	// transport.
	Exec DeployExecutor

	// BuilderImageResolver maps builder names to image refs for the
	// runBuilder step. Same shape as LocalDeployTarget's resolver —
	// builders run on the host, artifacts are scp'd into the guest.
	BuilderImageResolver func(builderName string) string

	// Distro mirrors LocalDeployTarget.Distro for gating decisions
	// (e.g. aur on non-Arch host). For a VM target, this is the
	// GUEST distro, not the host; resolved via ssh /etc/os-release.
	Distro *HostDistro

	// DryRunWriter receives dry-run output. Nil defaults to os.Stderr.
	DryRunWriter *os.File

	// shellsPresent caches the shell-detection probe result for the
	// duration of one Emit() call. Same shape as
	// LocalDeployTarget.shellsPresent — populated lazily on the first
	// ShellSnippetStep encountered.
	shellsPresent map[string]bool
}

// Name returns the target's display name.
func (t *VmDeployTarget) targetName() string { return "vm:" + t.VMName }

// Emit executes all plans against the guest via SSH. Pre-flight:
//  1. Ensure VM is booted (callers typically run `ov vm create` first).
//  2. Wait for SSH readiness (polls up to 120s).
//  3. Wait for cloud-init to finish (cloud_image sources only).
//  4. Ensure `ov` binary is present in the guest per
//     VmOvInstall.Strategy.
//  5. Ensure guest ledger dir exists.
//
// Then walks the plans identically to LocalDeployTarget, but with
// SSH-wrapped shell execution.
func (t *VmDeployTarget) Emit(plans []*InstallPlan, opts EmitOpts) error {
	if t.Exec == nil {
		return fmt.Errorf("VmDeployTarget: Exec is nil")
	}
	if t.Spec == nil {
		return fmt.Errorf("VmDeployTarget: Spec is nil")
	}

	ctx := opts.ContextOrDefault()

	// 1. Wait for SSH. 300s accommodates first-boot cloud-init package
	//    installs on arch / debian / ubuntu cloud images — 120s was too
	//    tight (observed failing on arch-vm with `spice-vdagent` +
	//    runcmds). Steady-state reboots still land inside 30s; the
	//    timeout is a ceiling, not a floor.
	if sshExec, ok := t.Exec.(*SSHExecutor); ok {
		fmt.Fprintf(os.Stderr, "Waiting for sshd on %s:%d...\n", sshExec.Host, sshExec.Port)
		if err := sshExec.WaitForSSH(ctx, 300); err != nil {
			return fmt.Errorf("VmDeployTarget: wait-for-sshd: %w", err)
		}
	}

	// 2. Wait for cloud-init (cloud-image sources only; bootc guests
	//    don't run cloud-init unless the layer is included).
	if t.Spec.Source.Kind == "cloud_image" {
		if sshExec, ok := t.Exec.(*SSHExecutor); ok {
			fmt.Fprintf(os.Stderr, "Waiting for cloud-init to finish in guest...\n")
			if err := sshExec.WaitForCloudInit(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: cloud-init wait returned %v (continuing)\n", err)
			}
		}
	}

	// 3. Ensure ov binary is present in guest per VmOvInstall.Strategy.
	msg, err := EnsureOvInGuest(ctx, t.Spec, t.Exec, opts)
	if err != nil {
		return fmt.Errorf("VmDeployTarget: ensure ov in guest: %w", err)
	}
	fmt.Fprintln(os.Stderr, msg)

	// 4. Ensure guest ledger + env.d directories.
	if err := t.ensureGuestLedgerDirs(ctx, opts); err != nil {
		return fmt.Errorf("VmDeployTarget: ensure guest ledger dirs: %w", err)
	}

	// 5. Iterate plans, writing per-layer ledger records INTO THE GUEST
	//    via t.Exec. VM deploys are disposable — storing ledger on
	//    the operator leaves garbage that survives `ov vm destroy` and
	//    breaks the zero-operator-side-effects invariant (see B6). The
	//    guest-side ledger path is still
	//    `~/.config/overthink/installed/…`; it just resolves to the
	//    guest's HOME via the SSH executor.
	paths, err := DefaultLedgerPaths()
	if err != nil {
		return fmt.Errorf("VmDeployTarget: ledger paths: %w", err)
	}

	deployRec := &DeployRecord{
		DeployID:   firstDeployID(plans),
		Target:     t.targetName(),
		DeployedAt: time.Now().UTC().Format(time.RFC3339),
	}

	for _, plan := range plans {
		layerRec, err := t.emitPlan(ctx, plan, opts)
		if err != nil {
			// Persist what we have so far before returning.
			if layerRec != nil {
				_ = t.recordLayer(paths, layerRec, plan, opts)
			}
			_ = WriteDeployRecordVia(t.Exec, paths, deployRec)
			return fmt.Errorf("VmDeployTarget: plan %s: %w", plan.Layer, err)
		}
		if err := t.recordLayer(paths, layerRec, plan, opts); err != nil {
			return fmt.Errorf("VmDeployTarget: recording layer %s: %w", plan.Layer, err)
		}
		deployRec.Layer = append(deployRec.Layer, plan.Layer)
		if deployRec.Image == "" && plan.Layer != "" {
			// For pure-add_layers vm deploys the deploy-id's "image" slot
			// stays as the vm: target name so `ov deploy del` can find it.
			deployRec.Image = t.targetName()
		}
	}

	deployRec.AddLayer = append(deployRec.AddLayer, deployRec.Layer...)
	if !opts.DryRun {
		if err := WriteDeployRecordVia(t.Exec, paths, deployRec); err != nil {
			return fmt.Errorf("VmDeployTarget: writing deploy record: %w", err)
		}
	}
	return nil
}

// firstDeployID returns the DeployID of the first non-nil plan. All
// plans in one `ov deploy add` pass share the same DeployID (stamped in
// Run()), so any non-nil plan's ID is the one to persist.
func firstDeployID(plans []*InstallPlan) string {
	for _, p := range plans {
		if p != nil && p.DeployID != "" {
			return p.DeployID
		}
	}
	return ""
}

// recordLayer writes the per-layer ledger entry INTO THE GUEST via
// t.Exec. Mirrors LocalDeployTarget.recordLayer's executor-routed
// pattern (B6 fix) so VM deploys obey the same
// zero-operator-side-effects invariant as nested host deploys.
func (t *VmDeployTarget) recordLayer(paths *LedgerPaths, rec *LayerRecord, plan *InstallPlan, opts EmitOpts) error {
	if opts.DryRun || plan.DeployID == "" || rec == nil {
		return nil
	}
	return AddLayerDeploymentVia(t.Exec, paths, plan.Layer, plan.DeployID, func(existing *LayerRecord) {
		existing.Version = rec.Version
		existing.Steps = append(existing.Steps, rec.Steps...)
		existing.ReverseOps = append(existing.ReverseOps, rec.ReverseOps...)
	})
}

// ensureGuestLedgerDirs makes sure ~/.config/overthink/installed/ and
// ~/.config/overthink/env.d/ exist in the guest. Without this, layer
// record writes would fail on the first apply.
func (t *VmDeployTarget) ensureGuestLedgerDirs(ctx context.Context, opts EmitOpts) error {
	script := `
set -e
mkdir -p "$HOME/.config/overthink/installed/deploys"
mkdir -p "$HOME/.config/overthink/installed/layers"
mkdir -p "$HOME/.config/overthink/env.d"
`
	return t.Exec.RunUser(ctx, script, opts)
}

// emitPlan walks a single InstallPlan and routes each step to the
// appropriate DeployExecutor method. Mirrors LocalDeployTarget.emitPlan's
// step-dispatch table but with SSH-wrapped execution. Collects
// ReverseOps from each executed step so `ov deploy del vm:<name>` can
// replay them in reverse order at teardown time.
func (t *VmDeployTarget) emitPlan(ctx context.Context, plan *InstallPlan, opts EmitOpts) (*LayerRecord, error) {
	fmt.Fprintf(os.Stderr, "\n--- plan: %s (layer=%s) ---\n", plan.DeployID, plan.Layer)
	rec := &LayerRecord{
		Layer:      plan.Layer,
		Version:    plan.Version,
		DeployedAt: time.Now().UTC().Format(time.RFC3339),
	}

	for _, step := range plan.Steps {
		switch s := step.(type) {
		case *SystemPackagesStep:
			if err := t.execSystemPackages(ctx, s, plan, opts); err != nil {
				return rec, err
			}
			rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)

		case *TaskStep:
			if err := t.execTask(ctx, s, plan, opts); err != nil {
				return rec, err
			}
			rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)

		case *FileStep:
			if err := t.execFile(ctx, s, plan, opts); err != nil {
				return rec, err
			}
			rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)

		case *ShellHookStep:
			if err := t.execShellHook(ctx, s, plan, opts); err != nil {
				return rec, err
			}
			rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)

		case *RepoChangeStep:
			if !opts.AllowRepoChanges {
				return rec, fmt.Errorf("repo change in plan %s requires --allow-repo-changes", plan.Layer)
			}
			if err := t.execRepoChange(ctx, s, plan, opts); err != nil {
				return rec, err
			}
			rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)

		case *ServicePackagedStep:
			if !opts.WithServices {
				continue // gate silent when not enabled
			}
			if err := t.execServicePackaged(ctx, s, plan, opts); err != nil {
				return rec, err
			}
			rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)

		case *ServiceCustomStep:
			if !opts.WithServices {
				continue
			}
			if err := t.execServiceCustom(ctx, s, plan, opts); err != nil {
				return rec, err
			}
			rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)

		case *BuilderStep:
			if err := t.execBuilder(ctx, s, plan, opts); err != nil {
				return rec, err
			}

		case *ShellSnippetStep:
			if err := t.execShellSnippet(ctx, s, plan, opts); err != nil {
				return rec, err
			}
			rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)

		case *ApkInstallStep:
			// apk packages install onto a `kind: android` device, not a VM
			// guest. Skip (a `target: android` deploy handles them).
			fmt.Fprintf(os.Stderr, "VmDeployTarget: skipping apk install (layer=%s) — apk installs only on a kind:android device\n", s.LayerName)

		case *RebootStep:
			if err := t.execReboot(ctx, s, opts); err != nil {
				return rec, err
			}

		default:
			fmt.Fprintf(os.Stderr, "VmDeployTarget: skipping unsupported step kind %T\n", step)
		}
	}

	return rec, nil
}

// execShellSnippet renders one (layer, shell) snippet onto the VM
// guest. Same shape as LocalDeployTarget.execShellSnippet — probes
// shell presence on the guest via SSH, writes drop-in or applies
// managed-block to existing rc file. Probe result cached on the
// target struct for the duration of Emit().
func (t *VmDeployTarget) execShellSnippet(ctx context.Context, s *ShellSnippetStep, plan *InstallPlan, opts EmitOpts) error {
	if err := t.ensureShellProbe(ctx, opts); err != nil {
		return err
	}
	if !t.shellsPresent[s.Shell] {
		fmt.Fprintf(os.Stderr, "vm:%s skip: shell-snippet %s/%s: %s not installed on guest\n",
			t.VMName, s.LayerName, s.Shell, s.Shell)
		return nil
	}
	if opts.DryRun {
		fmt.Fprintf(os.Stderr, "[dry-run] vm:%s shell-snippet %s/%s -> %s (use_dropin=%v)\n",
			t.VMName, s.LayerName, s.Shell, s.Destination, s.UseDropin)
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
		existing, err := t.Exec.GetFile(ctx, s.Destination, false, opts)
		if err != nil && !isFileNotFoundErr(err) {
			return fmt.Errorf("read %s on guest: %w", s.Destination, err)
		}
		updated := replaceOrAppendManagedBlock(string(existing), strings.TrimRight(body, "\n"), s.Marker)
		fileBytes = []byte(updated)
	}
	tmpDir, err := os.MkdirTemp("", "ov-shell-snippet-")
	if err != nil {
		return fmt.Errorf("tempdir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	tmpPath := filepath.Join(tmpDir, "snippet")
	if err := os.WriteFile(tmpPath, fileBytes, 0644); err != nil {
		return fmt.Errorf("stage snippet: %w", err)
	}
	if err := t.Exec.PutFile(ctx, tmpPath, s.Destination, 0644, false, opts); err != nil {
		return fmt.Errorf("write %s on guest: %w", s.Destination, err)
	}
	return nil
}

// ensureShellProbe populates t.shellsPresent on first call. Each shell
// in the allowlist is probed with `command -v <shell>` over the SSH
// executor; presence is cached for the rest of Emit().
func (t *VmDeployTarget) ensureShellProbe(ctx context.Context, opts EmitOpts) error {
	if t.shellsPresent != nil {
		return nil
	}
	t.shellsPresent = make(map[string]bool, len(ShellAllowlist))
	if opts.DryRun {
		for shell := range ShellAllowlist {
			t.shellsPresent[shell] = true
		}
		return nil
	}
	for shell := range ShellAllowlist {
		stdout, _, _, err := t.Exec.RunCapture(ctx,
			fmt.Sprintf("command -v %s >/dev/null 2>&1 && echo yes || echo no", shell))
		if err != nil {
			t.shellsPresent[shell] = false
			continue
		}
		t.shellsPresent[shell] = strings.TrimSpace(stdout) == "yes"
	}
	return nil
}

// execSystemPackages runs the distro's package install command on the
// guest. Currently uses the fallback renderer (dnf / apt-get / pacman);
// the structured-template path is shared with LocalDeployTarget and
// will be wired once per-distro FormatDefs are accessible from here.
func (t *VmDeployTarget) execSystemPackages(ctx context.Context, s *SystemPackagesStep, plan *InstallPlan, opts EmitOpts) error {
	if s.Phase != PhaseInstall || len(s.Packages) == 0 {
		return nil
	}
	cmd := fallbackPackageInstallCmd(s)
	if cmd == "" {
		return fmt.Errorf("VmDeployTarget: no package-install command for format %q", s.Format)
	}
	return t.Exec.RunSystem(ctx, cmd, opts)
}

// execTask runs a TaskStep's rendered shell command on the guest.
// ScopeSystem → RunSystem; ScopeUser → RunUser.
func (t *VmDeployTarget) execTask(ctx context.Context, s *TaskStep, plan *InstallPlan, opts EmitOpts) error {
	if s.Task == nil {
		return nil
	}
	cmd := renderVmTaskCommand(s)
	if cmd == "" {
		return nil
	}
	if s.Scope() == ScopeSystem {
		return t.Exec.RunSystem(ctx, cmd, opts)
	}
	return t.Exec.RunUser(ctx, cmd, opts)
}

// execReboot reboots the guest and waits for it to return. It is deterministic,
// not a sleep-and-pray: it records the kernel boot_id before the reboot, then
// polls until SSH answers AND the boot_id has changed — so the still-up sshd of
// the pre-reboot system can't be mistaken for "back up". Needed by kernel-module
// layers (e.g. nvidia-open-dkms) whose module only loads on a fresh boot.
func (t *VmDeployTarget) execReboot(ctx context.Context, s *RebootStep, opts EmitOpts) error {
	if opts.DryRun {
		fmt.Fprintf(os.Stderr, "[dry-run] reboot guest %s (layer %s) and wait for it to return\n", t.VMName, s.LayerName)
		return nil
	}

	oldBoot, _, _, _ := t.Exec.RunCapture(ctx, "cat /proc/sys/kernel/random/boot_id 2>/dev/null")
	oldBoot = strings.TrimSpace(oldBoot)

	fmt.Fprintf(os.Stderr, "vm:%s reboot: requested by layer %q — rebooting guest and waiting for it to return\n", t.VMName, s.LayerName)
	// Fire the reboot in the background so the ssh session closes cleanly
	// (a foreground `reboot` would race the connection teardown and yield an
	// ambiguous exit code). The 1s delay is for clean session close, not a
	// correctness-timing workaround.
	_ = t.Exec.RunSystem(ctx, "(sleep 1; systemctl reboot || reboot) >/dev/null 2>&1 &\nexit 0", opts)

	deadline := time.Now().Add(7 * time.Minute)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
		out, _, _, err := t.Exec.RunCapture(ctx, "cat /proc/sys/kernel/random/boot_id 2>/dev/null")
		if err != nil {
			continue // guest still down or sshd not yet accepting
		}
		newBoot := strings.TrimSpace(out)
		if newBoot == "" {
			continue
		}
		if oldBoot == "" || newBoot != oldBoot {
			fmt.Fprintf(os.Stderr, "vm:%s reboot: guest is back up (boot_id=%s)\n", t.VMName, newBoot)
			return nil
		}
	}
	return fmt.Errorf("vm:%s: guest did not return within 7m after reboot requested by layer %q", t.VMName, s.LayerName)
}

// execFile handles a FileStep — reads the file content from FileStep.Source
// on the host, then scp's it to FileStep.Dest in the guest via PutFile
// (for system-scoped paths) or RunUser install (for user-scoped paths).
func (t *VmDeployTarget) execFile(ctx context.Context, s *FileStep, plan *InstallPlan, opts EmitOpts) error {
	if s.Source == "" {
		return fmt.Errorf("VmDeployTarget: FileStep for %s has empty Source", s.Dest)
	}
	ownerRoot := s.Scope() == ScopeSystem
	return t.Exec.PutFile(ctx, s.Source, s.Dest, uint32(s.Mode), ownerRoot, opts)
}

// execShellHook writes the env.d file for a layer. Layer env vars end
// up in ~/.config/overthink/env.d/<layer>.env on the guest; the managed
// block in the guest user's shell init sources them.
func (t *VmDeployTarget) execShellHook(ctx context.Context, s *ShellHookStep, plan *InstallPlan, opts EmitOpts) error {
	// Emit a trivial script that writes the env.d file in $HOME.
	envDBody := renderEnvDFileBody(s)
	script := fmt.Sprintf(`
set -e
mkdir -p "$HOME/.config/overthink/env.d"
cat > "$HOME/.config/overthink/env.d/%s.env" <<'OV_ENVD'
%s
OV_ENVD
`, s.LayerName, envDBody)
	return t.Exec.RunUser(ctx, script, opts)
}

// execRepoChange writes a repo config file (e.g. rpmfusion-free.repo
// under /etc/yum.repos.d/) to the guest. Always sudo.
func (t *VmDeployTarget) execRepoChange(ctx context.Context, s *RepoChangeStep, plan *InstallPlan, opts EmitOpts) error {
	script := fmt.Sprintf(`
set -e
install -D -m0644 /dev/stdin %s <<'OV_REPO'
%s
OV_REPO
`, deployShellQuote(s.File), s.Content)
	return t.Exec.RunSystem(ctx, script, opts)
}

// execServicePackaged enables a distro-shipped systemd unit (with
// optional drop-ins) on the guest. --with-services gate already
// checked in emitPlan.
func (t *VmDeployTarget) execServicePackaged(ctx context.Context, s *ServicePackagedStep, plan *InstallPlan, opts EmitOpts) error {
	var b strings.Builder
	b.WriteString("set -e\n")
	if s.OverridesText != "" && s.OverridesPath != "" {
		fmt.Fprintf(&b, "install -D -m0644 /dev/stdin %s <<'OV_DROPIN'\n%s\nOV_DROPIN\n",
			deployShellQuote(s.OverridesPath), s.OverridesText)
		b.WriteString("systemctl daemon-reload\n")
	}
	if s.Enable {
		fmt.Fprintf(&b, "systemctl enable --now %s\n", deployShellQuote(s.Unit))
	}
	return t.Exec.RunSystem(ctx, b.String(), opts)
}

// execServiceCustom writes a custom systemd unit file to the guest
// and enables it.
//
// The compiler (compileServiceSteps in install_build.go) emits
// ServiceCustomStep with empty UnitText/UnitPath — the note on the
// compiler says rendering "happens at deploy time because the compiler
// doesn't know the target init system yet". For a VM deploy the init
// system is systemd (cloud_image VMs ship systemd; bootc VMs also do).
// UnitText/UnitPath are populated at compile time by `compileServiceSteps`
// in install_build.go (the unified init-system polymorphism filter); the
// previous lazy `renderCustomServiceForSystemdTarget` fallback was
// deleted in the 2026-05 unification cutover.
func (t *VmDeployTarget) execServiceCustom(ctx context.Context, s *ServiceCustomStep, plan *InstallPlan, opts EmitOpts) error {
	if s.UnitText == "" || s.UnitPath == "" {
		return fmt.Errorf("service %s: no unit text rendered (compile-time render skipped this entry; check that the layer's mixed-`service:` pair is well-formed)", s.Name)
	}
	script := fmt.Sprintf(`
set -e
install -D -m0644 /dev/stdin %s <<'OV_UNIT'
%s
OV_UNIT
systemctl daemon-reload
`, deployShellQuote(s.UnitPath), s.UnitText)
	if s.Enable {
		script += fmt.Sprintf("systemctl enable --now %s\n", deployShellQuote(s.Name))
	}
	return t.Exec.RunSystem(ctx, script, opts)
}

// execBuilder runs a builder step on the HOST (where podman is
// available), then transfers the resulting artifacts into the guest.
//
// Today only the `aur` builder is fully implemented: it produces
// .pkg.tar.zst files in a host staging dir; we tar the staging dir,
// scp it to the guest's /tmp, and run `pacman -U <files>` via SSH.
// The aur path is the most well-defined cross-machine case (pacman -U
// is the canonical install verb; no home-dir assumptions to translate).
//
// pixi / npm / cargo still emit ErrNotYetImplemented because their
// outputs land in user-home subdirectories whose mappings to the
// guest's home (different uid/gid, possibly different shell, possibly
// different user names) need per-builder translation — out of scope
// for the AUR-canary MVP. --skip-incompatible continues to skip them.
func (t *VmDeployTarget) execBuilder(ctx context.Context, s *BuilderStep, plan *InstallPlan, opts EmitOpts) error {
	// AUR is the only fully-supported builder for VM targets today.
	// Other builders honor SkipIncompatible (which the operator may
	// legitimately set for "skip rpm:/deb:-only sections" without
	// intending to skip aur) — but for aur specifically we always
	// attempt the build since the cross-host pipeline is now wired.
	if s.Builder != "aur" {
		if opts.SkipIncompatible {
			fmt.Fprintf(os.Stderr, "VmDeployTarget: skipping builder step %q (--skip-incompatible)\n", s.Builder)
			return nil
		}
		return fmt.Errorf("builder %q on VM target is not yet supported; only `aur` works cross-host today (others have user-home assumptions that need per-builder mapping). Run with --skip-incompatible to skip, or restructure the layer to use install: instead of builder:", s.Builder)
	}

	// BuilderImage resolution order:
	//   1. EmitOpts.BuilderImageOverride (--builder-image flag)
	//   2. BuilderStep.BuilderImage (compiled from image.yml's
	//      builder: section by install_build.go)
	//   3. t.BuilderImageResolver (rarely wired)
	//
	// For VM-target deploys where the deploy is a deploy.yml entity
	// (no associated image.yml), step 2 returns empty — the operator
	// must either pass --builder-image or the layer's `aur:` config
	// must reference a known builder. Pre-C9 this branch was an
	// unreachable error; C9 makes it reachable but still requires a
	// resolved image to proceed.
	image := opts.BuilderImageOverride
	if image == "" {
		image = s.BuilderImage
	}
	if image == "" && t.BuilderImageResolver != nil {
		image = t.BuilderImageResolver(s.Builder)
	}
	if image == "" {
		return fmt.Errorf("no builder image for aur (layer=%s); set --builder-image or define builder.aur in image.yml", s.LayerName)
	}

	// Stage the .pkg.tar.zst output on the host. The builder writes
	// here; we then ship it to the guest.
	hostStage, err := os.MkdirTemp("", "ov-vm-aur-")
	if err != nil {
		return fmt.Errorf("aur staging mkdir: %w", err)
	}
	RegisterTempCleanup(hostStage)
	defer func() { os.RemoveAll(hostStage); UnregisterTempCleanup(hostStage) }()

	// Re-use the same builder-script renderer as LocalDeployTarget so
	// the in-container build steps stay identical between host and
	// VM deploys. The HOME we pass is the host's HOME — the builder
	// container does HOME-remap internally via BuilderRunOpts.
	hostHome, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("UserHomeDir: %w", err)
	}
	bindMounts, err := UserScopeBindMounts(hostHome)
	if err != nil {
		return err
	}
	bindMounts["/tmp/aur-pkgs"] = hostStage
	envVars := UserScopeEnv(hostHome)

	// VM-specific aur script: runs as root inside the container,
	// configures NOPASSWD sudoers for the `user` account, then drops
	// to that user to run the existing aur build flow. The host-side
	// renderAurScript assumes a pre-baked sudoers (OCI multistage
	// builds add it via stage_template); deploy-time podman-run on a
	// stock arch-builder image doesn't have that, so we set it
	// up ourselves before invoking the inner build.
	innerScript, err := renderBuilderScript(s, hostHome)
	if err != nil {
		return err
	}
	wrappedScript := "set -e\n" +
		"echo 'user ALL=(ALL) NOPASSWD: ALL' > /etc/sudoers.d/ov-builder\n" +
		"chmod 440 /etc/sudoers.d/ov-builder\n" +
		"# Drop to user; pass HOME so $HOME inside the inner script is /home/user.\n" +
		"su - user -c " + shQuoteArg("set -e\n"+innerScript) + "\n" +
		"# Backstop find: yay -S installs the package and cleans up its\n" +
		"# build tree, so renderAurScript's find may run after yay\n" +
		"# already wiped /tmp/aur-build. Broaden the search if the\n" +
		"# inner script's find produced nothing.\n" +
		"if [ -z \"$(ls -A /tmp/aur-pkgs 2>/dev/null)\" ]; then\n" +
		"  find / -name '*.pkg.tar.zst' 2>/dev/null -exec cp {} /tmp/aur-pkgs/ \\;\n" +
		"fi\n" +
		"# Rootless-podman userns fix: files created by container user\n" +
		"# 1000 land in the host's subuid range and become unreadable to\n" +
		"# the operator. chown to 0:0 — root in container maps to the\n" +
		"# host user under rootless podman — so the bind-mount surface is\n" +
		"# host-readable for the subsequent scp+pacman -U leg.\n" +
		"chown -R 0:0 /tmp/aur-pkgs/\n"

	out, err := BuilderRun(opts.ContextOrDefault(), BuilderRunOpts{
		BuilderImage: image,
		LayerDir:     s.LayerDir,
		ScriptBody:   wrappedScript,
		BindMounts:   bindMounts,
		Env:          envVars,
		HostHome:     hostHome,
		DryRun:       opts.DryRun,
		RunAsRoot:    true,
	})
	// Always surface the builder's stdout/stderr — the operator needs to
	// see compile output to debug build failures, not just the bare exit
	// status. (BuilderRun returns combined output; non-error path
	// discards it by default.)
	if len(out) > 0 {
		os.Stderr.Write(out)
	}
	if err != nil {
		return fmt.Errorf("VM aur builder: %w", err)
	}
	if opts.DryRun {
		return nil
	}

	// Ship the staging dir to the guest via tar+ssh and run pacman -U
	// on the resulting files. Wrapping in a single shell pipeline keeps
	// the transfer atomic — if scp succeeds but pacman fails, the
	// operator can re-run and the staging dir's content is replaced
	// idempotently (-U is the upgrade form; doesn't error on already-
	// installed).
	matches, _ := filepath.Glob(filepath.Join(hostStage, "*.pkg.tar.zst"))
	if len(matches) == 0 {
		return fmt.Errorf("aur builder produced no .pkg.tar.zst in %s", hostStage)
	}
	guestStage := "/tmp/ov-aur-pkgs"
	transferScript := fmt.Sprintf(`set -e
mkdir -p %[1]s
rm -f %[1]s/*.pkg.tar.zst 2>/dev/null || true
`, guestStage)
	if err := t.Exec.RunUser(ctx, transferScript, opts); err != nil {
		return fmt.Errorf("preparing guest stage dir: %w", err)
	}

	// scp each package file. PutFile signature is
	// (ctx, localPath, remotePath, mode, ownerRoot, opts) — the
	// SSH executor turns this into scp + (optional) sudo install.
	for _, m := range matches {
		base := filepath.Base(m)
		dest := filepath.Join(guestStage, base)
		if err := t.Exec.PutFile(ctx, m, dest, 0o644, false, opts); err != nil {
			return fmt.Errorf("scp %s: %w", base, err)
		}
	}

	installScript := fmt.Sprintf("pacman -U --noconfirm %s/*.pkg.tar.zst", guestStage)
	if err := t.Exec.RunSystem(ctx, installScript, opts); err != nil {
		return fmt.Errorf("guest pacman -U: %w", err)
	}

	return nil
}

// --- Package-install fallback shared with host (mirrors
// LocalDeployTarget.renderFallbackPkgCmd). ---
//
// Each format prefixes a database-refresh step before the install: see
// the doc comment on LocalDeployTarget.renderFallbackPkgCmd for the
// rationale (apt-get and pacman do NOT auto-refresh, and a stale db
// causes 404 fetches when packages have been version-bumped upstream).
// Keep this function and renderFallbackPkgCmd in lock-step.

func fallbackPackageInstallCmd(s *SystemPackagesStep) string {
	if s.Phase != PhaseInstall || len(s.Packages) == 0 {
		return ""
	}
	opts := ""
	if len(s.Options) > 0 {
		opts = " " + strings.Join(s.Options, " ")
	}
	switch s.Format {
	case "rpm":
		return fmt.Sprintf("dnf install -y%s %s", opts, strings.Join(s.Packages, " "))
	case "deb":
		return fmt.Sprintf("DEBIAN_FRONTEND=noninteractive apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y%s %s", opts, strings.Join(s.Packages, " "))
	case "pac":
		return fmt.Sprintf("pacman -Sy --noconfirm --needed%s %s", opts, strings.Join(s.Packages, " "))
	}
	return ""
}

// renderVmTaskCommand renders the same task verbs as LocalDeployTarget
// (cmd/mkdir/link/setcap/copy/write/download). Kept a separate function
// so VmDeployTarget doesn't depend on LocalDeployTarget's receiver, but
// the supported verb set MUST stay in lock-step with the host version —
// any verb the host supports needs a parallel branch here, otherwise
// task steps silently no-op on VMs (observed live 2026-04-22: uv
// layer's `download:` task was silently skipped under VmDeployTarget,
// leaving /usr/local/bin/uv missing).
func renderVmTaskCommand(s *TaskStep) string {
	task := s.Task
	ctxPath := s.CtxPath

	// Task.Env prelude — matches LocalDeployTarget.renderTaskCommand so
	// layer authors can declare env vars and have them reach the shell
	// regardless of target (host/vm). Also required for the secret-
	// injection path (ov/layer_secrets.go InjectSecretsIntoPlans) to
	// propagate credential-store-resolved values to VM deploys. Layer
	// vars are appended via taskShellPreamble so cmd bodies templating
	// ${LAYER_VAR} resolve at deploy-time the same as build-time.
	envPrelude := taskShellPreamble(s)
	if len(task.Env) > 0 {
		keys := make([]string, 0, len(task.Env))
		for k := range task.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteString(envPrelude)
		for _, k := range keys {
			fmt.Fprintf(&b, "export %s=%s\n", k, shQuoteArg(task.Env[k]))
		}
		envPrelude = b.String()
	}

	switch {
	case task.Cmd != "":
		body := task.Cmd
		if ctxPath != "" {
			body = strings.ReplaceAll(body, "/ctx/", ctxPath+"/")
		}
		return envPrelude + body
	case task.Mkdir != "":
		mode := task.Mode
		if mode == "" {
			mode = "0755"
		}
		return fmt.Sprintf("install -d -m%s %s", mode, deployShellQuote(task.Mkdir))
	case task.Link != "":
		target := task.Target
		if target == "" {
			target = task.To
		}
		return fmt.Sprintf("ln -sfn %s %s", deployShellQuote(target), deployShellQuote(task.Link))
	case task.Setcap != "":
		return fmt.Sprintf("setcap %s %s", deployShellQuote(task.Caps), deployShellQuote(task.Setcap))
	case task.Copy != "":
		src := task.Copy
		if s.LayerDir != "" {
			src = filepath.Join(s.LayerDir, task.Copy)
		}
		dst := task.To
		if dst == "" {
			dst = task.Copy
		}
		mode := task.Mode
		if mode == "" {
			mode = "0644"
		}
		return fmt.Sprintf("install -m%s %s %s", mode, deployShellQuote(src), deployShellQuote(dst))
	case task.Write != "":
		mode := task.Mode
		if mode == "" {
			mode = "0644"
		}
		return fmt.Sprintf("install -m%s /dev/stdin %s <<'OV_WRITE'\n%s\nOV_WRITE",
			mode, deployShellQuote(task.Write), task.Content)
	case task.Download != "":
		return renderDownloadScript(task, s.LayerVars)
	}
	return ""
}

// renderEnvDFileBody renders a layer's env vars + path_append into the
// form expected by ~/.config/overthink/env.d/<layer>.env.
func renderEnvDFileBody(s *ShellHookStep) string {
	var b strings.Builder
	// EnvVars is a map[string]string — iteration order is non-deterministic;
	// sort for stable output (critical for CloudInitRenderedDigest drift detection).
	keys := make([]string, 0, len(s.EnvVars))
	for k := range s.EnvVars {
		keys = append(keys, k)
	}
	sortStrings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "export %s=%s\n", k, deployShellQuote(s.EnvVars[k]))
	}
	for _, p := range s.PathAdd {
		fmt.Fprintf(&b, "export PATH=$PATH:%s\n", deployShellQuote(p))
	}
	return b.String()
}
