package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// VmDeployTarget applies an InstallPlan inside a running VM over SSH.
// Uses the same InstallPlan IR that LocalDeployTarget consumes — the
// only difference is that bash bodies run via `ssh vm 'sudo bash -s'`
// instead of local `sudo bash -s`.
//
// Ledger writes land on the GUEST filesystem (under the guest user's
// ~/.config/opencharly/installed/), not the host's. Teardown runs in
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

	// guestHome is the GUEST user's home directory, resolved once at the
	// top of Emit via the SSH executor (echo $HOME in the guest). Drives
	// InstallPlan.ResolveHome (so env.d / shell-snippet destinations point
	// at /home/<guest-user>, not the host operator's home), the env.d
	// sourcing managed block, and the cross-host builder artifact transfer.
	guestHome string

	// DistroCfg is the resolved build.yml distro section. Used by
	// execSystemPackages to render the format's phase.install.host template —
	// the SAME config-driven path LocalDeployTarget + the OCI container path
	// use (R3). Populated by the deploy dispatcher from dctx.DistroCfg.
	DistroCfg *DistroConfig

	// Cfg + ProjectDir mirror LocalDeployTarget: they let the host-side
	// dep-closure builder (buildDepPkgsOnHost) thread them into BuilderRun's
	// EnsureImagePresent, so a namespace-qualified builder ref (e.g. the cachyos
	// project's aur builder `charly.arch-builder`) resolves to its concrete image.
	// Populated by the deploy dispatcher from dctx.Cfg / dctx.Dir.
	Cfg        *Config
	ProjectDir string
}

// Name returns the target's display name.
func (t *VmDeployTarget) targetName() string { return "vm:" + t.VMName }

// Emit executes all plans against the guest via SSH. Pre-flight:
//  1. Ensure VM is booted (callers typically run `charly vm create` first).
//  2. Wait for SSH readiness (polls up to 120s).
//  3. Wait for cloud-init to finish (cloud_image sources only).
//  4. Ensure `charly` binary is present in the guest per
//     VmCharlyInstall.Strategy.
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

	// 2. Wait for cloud-init to settle on ANY VM with a cloud-init seed —
	//    cloud_image sources AND bootstrap (pacstrap/debootstrap) VMs whose
	//    seed configures the guest on first boot. cloud-init regenerates the
	//    SSH host keys + restarts sshd on first boot AFTER the initial sshd
	//    start, so without this wait the EnsureCharlyInGuest scp below races that
	//    restart ("kex Connection reset by peer"). Bootstrap VMs hit this just
	//    as cloud_image VMs do — gating on cloud_image alone left bootstrap
	//    deploys flaky on a cold first boot. (bootc guests with no cloud-init
	//    seed have spec.CloudInit == nil and skip this.)
	if t.Spec.Source.Kind == "cloud_image" || t.Spec.CloudInit != nil {
		if sshExec, ok := t.Exec.(*SSHExecutor); ok {
			fmt.Fprintf(os.Stderr, "Waiting for cloud-init to finish in guest...\n")
			if err := sshExec.WaitForCloudInit(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: cloud-init wait returned %v (continuing)\n", err)
			}
		}
	}

	// 3. Ensure charly binary is present in guest per VmCharlyInstall.Strategy.
	msg, err := EnsureCharlyInGuest(ctx, t.Spec, t.Exec, opts)
	if err != nil {
		return fmt.Errorf("VmDeployTarget: ensure charly in guest: %w", err)
	}
	fmt.Fprintln(os.Stderr, msg)

	// 4. Ensure guest ledger + env.d directories.
	if err := t.ensureGuestLedgerDirs(ctx, opts); err != nil {
		return fmt.Errorf("VmDeployTarget: ensure guest ledger dirs: %w", err)
	}

	// 4b. Resolve the GUEST user's home once. Every home-bearing step field
	//     ({{.Home}} in env.d values, shell-snippet destinations, builder
	//     artifact paths) resolves against THIS home — not the host
	//     operator's. Without it env.d on the guest pointed at the operator's
	//     /home/<operator> and user-scope installs (npm -g, cargo) landed in
	//     a root-owned path the guest user couldn't write.
	if !opts.DryRun {
		guestHome, err := t.Exec.ResolveHome(ctx, "")
		if err != nil {
			return fmt.Errorf("VmDeployTarget: resolve guest HOME: %w", err)
		}
		t.guestHome = guestHome
	}

	// 5. Iterate plans, writing per-layer ledger records INTO THE GUEST
	//    via t.Exec. VM deploys are disposable — storing ledger on
	//    the operator leaves garbage that survives `charly vm destroy` and
	//    breaks the zero-operator-side-effects invariant (see B6). The
	//    guest-side ledger path is still
	//    `~/.config/opencharly/installed/…`; it just resolves to the
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
		// Resolve {{.Home}} → the guest user's home before emitting so env.d
		// content, shell-snippet destinations, and managed blocks all point
		// at the guest user's home rather than the host operator's.
		plan.ResolveHome(t.guestHome)
		candyRec, err := t.emitPlan(ctx, plan, opts)
		if err != nil {
			// Persist what we have so far before returning.
			if candyRec != nil {
				_ = t.recordCandy(paths, candyRec, plan, opts)
			}
			_ = WriteDeployRecordVia(t.Exec, paths, deployRec)
			return fmt.Errorf("VmDeployTarget: plan %s: %w", plan.Candy, err)
		}
		if err := t.recordCandy(paths, candyRec, plan, opts); err != nil {
			return fmt.Errorf("VmDeployTarget: recording layer %s: %w", plan.Candy, err)
		}
		deployRec.Candy = append(deployRec.Candy, plan.Candy)
		if deployRec.Image == "" && plan.Candy != "" {
			// For pure-add_layers vm deploys the deploy-id's "image" slot
			// stays as the vm: target name so `charly deploy del` can find it.
			deployRec.Image = t.targetName()
		}
	}

	// Ensure the env.d-sourcing managed block exists in the GUEST user's
	// shell init so the per-layer env.d files actually get sourced at login.
	// Without this the env.d files are written but never read — PATH never
	// picks up ~/.npm-global/bin etc. Uses the guest's detected login shell
	// and the same executor-based writer as the local path (R3).
	if !opts.DryRun {
		if _, err := EnsureManagedBlockVia(ctx, t.Exec, t.detectGuestShell(ctx), t.guestHome, opts); err != nil {
			return fmt.Errorf("VmDeployTarget: guest managed block: %w", err)
		}
	}

	deployRec.AddCandy = append(deployRec.AddCandy, deployRec.Candy...)
	if !opts.DryRun {
		if err := WriteDeployRecordVia(t.Exec, paths, deployRec); err != nil {
			return fmt.Errorf("VmDeployTarget: writing deploy record: %w", err)
		}
	}
	return nil
}

// firstDeployID returns the DeployID of the first non-nil plan. All
// plans in one `charly deploy add` pass share the same DeployID (stamped in
// Run()), so any non-nil plan's ID is the one to persist.
func firstDeployID(plans []*InstallPlan) string {
	for _, p := range plans {
		if p != nil && p.DeployID != "" {
			return p.DeployID
		}
	}
	return ""
}

// recordCandy writes the per-layer ledger entry INTO THE GUEST via
// t.Exec. Mirrors LocalDeployTarget.recordCandy's executor-routed
// pattern (B6 fix) so VM deploys obey the same
// zero-operator-side-effects invariant as nested host deploys.
func (t *VmDeployTarget) recordCandy(paths *LedgerPaths, rec *CandyRecord, plan *InstallPlan, opts EmitOpts) error {
	if opts.DryRun || plan.DeployID == "" || rec == nil {
		return nil
	}
	// Render the config-driven package-uninstall command into each
	// package-remove reverse op BEFORE persisting — same shared filler the
	// host target uses (R3); the guest teardown reads only the persisted ledger.
	fillReverseUninstallCmds(rec.ReverseOps, t.DistroCfg)
	return AddCandyDeploymentVia(t.Exec, paths, plan.Candy, plan.DeployID, func(existing *CandyRecord) {
		existing.Version = rec.Version
		existing.Steps = append(existing.Steps, rec.Steps...)
		existing.ReverseOps = append(existing.ReverseOps, rec.ReverseOps...)
	})
}

// ensureGuestLedgerDirs makes sure ~/.config/opencharly/installed/ and
// ~/.config/opencharly/env.d/ exist in the guest. Without this, layer
// record writes would fail on the first apply.
func (t *VmDeployTarget) ensureGuestLedgerDirs(ctx context.Context, opts EmitOpts) error {
	script := `
set -e
mkdir -p "$HOME/.config/opencharly/installed/deploys"
mkdir -p "$HOME/.config/opencharly/installed/layers"
mkdir -p "$HOME/.config/opencharly/env.d"
`
	return t.Exec.RunUser(ctx, script, opts)
}

// emitPlan walks a single InstallPlan and routes each step to the
// appropriate DeployExecutor method. Mirrors LocalDeployTarget.emitPlan's
// step-dispatch table but with SSH-wrapped execution. Collects
// ReverseOps from each executed step so `charly deploy del vm:<name>` can
// replay them in reverse order at teardown time.
func (t *VmDeployTarget) emitPlan(ctx context.Context, plan *InstallPlan, opts EmitOpts) (*CandyRecord, error) {
	fmt.Fprintf(os.Stderr, "\n--- plan: %s (layer=%s) ---\n", plan.DeployID, plan.Candy)
	rec := &CandyRecord{
		Candy:      plan.Candy,
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
				return rec, fmt.Errorf("repo change in plan %s requires --allow-repo-changes", plan.Candy)
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
			fmt.Fprintf(os.Stderr, "VmDeployTarget: skipping apk install (layer=%s) — apk installs only on a kind:android device\n", s.CandyName)

		case *LocalPkgInstallStep:
			// Build the package source on the host and install the result INTO
			// THE GUEST — the proper-package counterpart of the layer's curl/COPY
			// cmd: task. Gated (config-driven) on the GUEST having the format's
			// package manager; an unsupported guest is a clean no-op (the layer's
			// curl task installs it).
			if err := execLocalPkgInstall(ctx, t.Exec, s, venueHasPkgManager(ctx, t.Exec, s.LocalPkg, opts), "vm:"+t.VMName, opts); err != nil {
				return rec, err
			}

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
			t.VMName, s.CandyName, s.Shell, s.Shell)
		return nil
	}
	if opts.DryRun {
		fmt.Fprintf(os.Stderr, "[dry-run] vm:%s shell-snippet %s/%s -> %s (use_dropin=%v)\n",
			t.VMName, s.CandyName, s.Shell, s.Destination, s.UseDropin)
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
	tmpDir, err := os.MkdirTemp("", "charly-shell-snippet-")
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

// detectGuestShell resolves the GUEST user's login shell from the guest's
// /etc/passwd (via getent), mapping the shell basename to a ShellKind. The
// guest's default shell is NOT necessarily the operator's — CachyOS images
// ship fish as the interactive default, so writing the env.d-sourcing block
// to ~/.profile (bash) would never be sourced. Defaults to bash on any
// detection failure (POSIX-safest, matches DetectLoginShell). DryRun → bash.
func (t *VmDeployTarget) detectGuestShell(ctx context.Context) ShellKind {
	stdout, _, _, err := t.Exec.RunCapture(ctx,
		`getent passwd "$(id -un)" 2>/dev/null | awk -F: '{print $7}'`)
	if err != nil {
		return ShellBash
	}
	switch filepath.Base(strings.TrimSpace(stdout)) {
	case "zsh":
		return ShellZsh
	case "fish":
		return ShellFish
	default:
		return ShellBash
	}
}

// execSystemPackages runs the distro's package install command on the guest.
// Renders the format's phase.install.host template from build.yml via the SHARED
// config-driven renderer (renderHostPackageCommand) — the SAME path
// LocalDeployTarget uses and the same FormatDef the OCI container path reads (R3).
func (t *VmDeployTarget) execSystemPackages(ctx context.Context, s *SystemPackagesStep, plan *InstallPlan, opts EmitOpts) error {
	cmd, err := renderHostPackageCommand(t.DistroCfg, s)
	if err != nil {
		return fmt.Errorf("VmDeployTarget: %w", err)
	}
	if cmd == "" {
		return nil
	}
	return t.Exec.RunSystem(ctx, cmd, opts)
}

// execTask runs a TaskStep's rendered shell command on the guest.
// ScopeSystem → RunSystem; ScopeUser → RunUser.
func (t *VmDeployTarget) execTask(ctx context.Context, s *TaskStep, plan *InstallPlan, opts EmitOpts) error {
	if s.Task == nil {
		return nil
	}
	// copy: stages the layer file into the guest via PutFile (scp+install) —
	// the SAME shared path LocalDeployTarget uses. The old renderVmTaskCommand
	// emitted `install <hostCandyDir>/<f> <dst>`, referencing a host path that
	// doesn't exist in the guest → file-not-found on every copy: task.
	if s.Task.Copy != "" {
		src := filepath.Join(s.CandyDir, s.Task.Copy)
		// Prefer the home-resolved dest (s.To) so `to: ${HOME}/...` lands in the
		// real guest home, not a literal "${HOME}" dir created under sudo
		// (HOME=/root). Falls back to the raw Task.To, then the src name.
		dst := s.To
		if dst == "" {
			dst = s.Task.To
		}
		if dst == "" {
			dst = s.Task.Copy
		}
		return t.Exec.PutFile(ctx, src, dst, parseTaskMode(s.Task.Mode, 0o644), s.Scope() == ScopeSystem, opts)
	}
	// Every other verb renders through the ONE shared renderTaskCommand
	// (cmd/mkdir/link/setcap/write/download) so VM and local can't drift.
	cmd, err := renderTaskCommand(s)
	if err != nil {
		return err
	}
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
		fmt.Fprintf(os.Stderr, "[dry-run] reboot guest %s (layer %s) and wait for it to return\n", t.VMName, s.CandyName)
		return nil
	}

	oldBoot, _, _, _ := t.Exec.RunCapture(ctx, "cat /proc/sys/kernel/random/boot_id 2>/dev/null")
	oldBoot = strings.TrimSpace(oldBoot)

	fmt.Fprintf(os.Stderr, "vm:%s reboot: requested by layer %q — rebooting guest and waiting for it to return\n", t.VMName, s.CandyName)
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
	return fmt.Errorf("vm:%s: guest did not return within 7m after reboot requested by layer %q", t.VMName, s.CandyName)
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
// up in ~/.config/opencharly/env.d/<layer>.env on the guest; the managed
// block in the guest user's shell init sources them.
func (t *VmDeployTarget) execShellHook(ctx context.Context, s *ShellHookStep, plan *InstallPlan, opts EmitOpts) error {
	// Shared env.d renderer (shell_profile.go renderEnvdBody) so VM and local
	// produce byte-identical env.d files — including the accumulating
	// PATH-prepend (export PATH="d1:d2:$PATH") that the old VM-only renderer
	// got wrong (it emitted per-entry `export PATH=$PATH:d`, a different order
	// + no managed-by-charly header).
	envDBody := renderEnvdBody(s.CandyName, s.EnvVars, s.PathAdd)
	script := fmt.Sprintf(`
set -e
mkdir -p "$HOME/.config/opencharly/env.d"
cat > "$HOME/.config/opencharly/env.d/%s.env" <<'CHARLY_ENVD'
%s
CHARLY_ENVD
`, s.CandyName, envDBody)
	return t.Exec.RunUser(ctx, script, opts)
}

// execRepoChange writes a repo config file (e.g. rpmfusion-free.repo
// under /etc/yum.repos.d/) to the guest. Always sudo.
func (t *VmDeployTarget) execRepoChange(ctx context.Context, s *RepoChangeStep, plan *InstallPlan, opts EmitOpts) error {
	script := fmt.Sprintf(`
set -e
install -D -m0644 /dev/stdin %s <<'CHARLY_REPO'
%s
CHARLY_REPO
`, deployShellQuote(s.File), s.Content)
	return t.Exec.RunSystem(ctx, script, opts)
}

// execServicePackaged enables a distro-shipped systemd unit (with
// optional drop-ins) on the guest. --with-services gate already
// checked in emitPlan.
func (t *VmDeployTarget) execServicePackaged(ctx context.Context, s *ServicePackagedStep, plan *InstallPlan, opts EmitOpts) error {
	if s.OverridesText != "" && s.OverridesPath != "" {
		if err := t.writeGuestUnitFile(ctx, s.OverridesPath, s.OverridesText, s.TargetScope, opts); err != nil {
			return err
		}
	}
	if s.Enable {
		return t.enableServiceUnit(ctx, s.Unit, s.TargetScope, opts)
	}
	return nil
}

// writeGuestUnitFile installs a systemd unit (or drop-in) on the guest at the
// resolved path, honoring scope. A user-scope file is written AS THE USER
// (RunUser) so the ~/.config/systemd/user/ tree is USER-owned — otherwise a
// later `systemctl --user enable` (run as the user) cannot create the
// .wants symlink in a root-owned dir and fails with a misleading
// "Unit ... does not exist". System-scope goes to /etc/systemd/system/ via sudo.
// The matching daemon (user or system) is reloaded.
func (t *VmDeployTarget) writeGuestUnitFile(ctx context.Context, path, content string, scope Scope, opts EmitOpts) error {
	install := fmt.Sprintf("install -D -m0644 /dev/stdin %s <<'CHARLY_UNIT'\n%s\nCHARLY_UNIT\n", deployShellQuote(path), content)
	if scope == ScopeUser {
		return t.Exec.RunUser(ctx, "set -e\n"+install+"export XDG_RUNTIME_DIR=\"/run/user/$(id -u)\"\nsystemctl --user daemon-reload || true\n", opts)
	}
	return t.Exec.RunSystem(ctx, "set -e\n"+install+"systemctl daemon-reload\n", opts)
}

// enableServiceUnit enables (and best-effort starts) a unit on the guest,
// honoring its scope — the SSH-executor counterpart of LocalDeployTarget's
// systemctlEnable (R3: same scope semantics, target-appropriate execution).
//
//   - ScopeSystem: `systemctl enable` via sudo (RunSystem).
//   - ScopeUser: run in the deploy user's OWN systemd instance via RunUser
//     (`systemctl --user enable`), after `loginctl enable-linger` so the user
//     manager — and the unit — start on boot without an interactive login.
//     Without this, a user-scope unit (written to ~/.config/systemd/user/) is
//     invisible to a system `systemctl enable`, which fails and aborts the
//     deploy. User-scope is what pipewire / selkies / the nested-Plasma stream
//     need (session bus + per-user runtime dir).
//
// Enable is hard (the durable boot intent); the immediate start is best-effort
// everywhere — a GPU/graphical-session service that can't start mid-deploy
// (before the nvidia-driver reboot + a live session) starts on the post-reboot
// boot, and the deploy-scope eval verifies it.
func (t *VmDeployTarget) enableServiceUnit(ctx context.Context, unit string, scope Scope, opts EmitOpts) error {
	q := deployShellQuote(unit)
	if scope == ScopeUser {
		if err := t.Exec.RunUser(ctx, `sudo loginctl enable-linger "$(id -un)" >/dev/null 2>&1 || true`, opts); err != nil {
			return err
		}
		script := fmt.Sprintf("set -e\nexport XDG_RUNTIME_DIR=\"/run/user/$(id -u)\"\nsystemctl --user daemon-reload || true\nsystemctl --user enable %s\nsystemctl --user start %s || echo \"charly: deferred start (will start on boot): %s\" >&2\n",
			q, q, unit)
		return t.Exec.RunUser(ctx, script, opts)
	}
	script := fmt.Sprintf("set -e\nsystemctl enable %s\nsystemctl start %s || echo \"charly: deferred start (will start on boot): %s\" >&2\n",
		q, q, unit)
	return t.Exec.RunSystem(ctx, script, opts)
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
	if err := t.writeGuestUnitFile(ctx, s.UnitPath, s.UnitText, s.TargetScope, opts); err != nil {
		return err
	}
	if s.Enable {
		// Scope-aware enable: system via sudo, user via the deploy user's own
		// systemd instance (+ linger). See enableServiceUnit.
		return t.enableServiceUnit(ctx, s.Name, s.TargetScope, opts)
	}
	return nil
}

// execBuilder runs a builder step on the HOST (where podman is
// available), then transfers the resulting artifacts into the guest.
//
//   - aur:           produces .pkg.tar.zst files in a host staging dir; we
//     tar them, scp to the guest's /tmp, and `pacman -U` via SSH.
//   - npm/pixi/cargo: produce user-home subdirs (~/.npm-global, ~/.pixi,
//     ~/.cargo). execHomeArtifactBuilder builds with HOME set to
//     the GUEST home path (so shebangs/configs bake the right
//     path), then tars the home subdirs and extracts them into
//     the guest user's $HOME over SSH — the cross-host home
//     translation the AUR-canary MVP deferred.
//
// Unknown builders honor --skip-incompatible (set by callers that legitimately
// want to skip rpm:/deb:-only sections) and otherwise hard-error.
func (t *VmDeployTarget) execBuilder(ctx context.Context, s *BuilderStep, plan *InstallPlan, opts EmitOpts) error {
	// Route by OUTPUT shape, not builder name: a builder that produces package
	// FILES carries the format's local_pkg contract (s.LocalPkg, set by the
	// compiler for the aur builder) — those go through the build-on-host →
	// transfer → package-install leg below. Everything else is a home-artifact
	// builder (pixi/npm/cargo) whose ~/.pixi / ~/.npm-global / ~/.cargo output is
	// tarred into the guest home. An unknown builder with neither shape has no
	// host build script (renderBuilderScript errors on a nil BuilderDef cell);
	// --skip-incompatible skips it.
	if s.LocalPkg == nil {
		if s.BuilderDef == nil || s.BuilderDef.PhaseTemplate(PhaseInstall, VenueHostNative) == "" {
			if opts.SkipIncompatible {
				fmt.Fprintf(os.Stderr, "VmDeployTarget: skipping builder step %q (--skip-incompatible)\n", s.Builder)
				return nil
			}
			return fmt.Errorf("builder %q on VM target has no phase.install.host cell in build.yml (layer=%s). Run with --skip-incompatible to skip, or add the host cell.", s.Builder, s.CandyName)
		}
		return t.execHomeArtifactBuilder(ctx, s, opts)
	}

	image, err := t.resolveBuilderImage(s, opts)
	if err != nil {
		return err
	}
	// Gate on the format's local_pkg.probe (e.g. `command -v pacman`) succeeding
	// on the GUEST — config-driven, not a hardcoded distro/builder-name check.
	if !venueHasPkgManager(ctx, t.Exec, s.LocalPkg, opts) {
		return fmt.Errorf("builder %q (layer=%s) builds %s package files but the guest has no %s package manager (local_pkg.probe %q failed); cannot install the built packages",
			s.Builder, s.CandyName, s.LocalPkg.DepBuilder, s.LocalPkg.DepBuilder, s.LocalPkg.Probe)
	}

	// Build the aur packages on the HOST through the SHARED host-side dep-build
	// helper (R3) — the same primitive the localpkg step uses to build its
	// dependency closure. The builder runs on the host (podman); the guest never
	// needs a container runtime. The package glob comes from the format config.
	matches, err := buildDepPkgsOnHost(ctx, s.LocalPkg, s.BuilderDef, image, extractStringSlice(s.RawStageContext, "packages"), s.CandyDir, t.Cfg, t.ProjectDir, opts)
	if err != nil {
		return fmt.Errorf("VM aur builder: %w", err)
	}
	if opts.DryRun {
		return nil
	}

	// Ship the built packages to the guest and install them via the SHARED,
	// config-driven transfer+install leg (R3) — the same primitive the localpkg
	// step uses. The install command (e.g. `pacman -U`) comes from the format's
	// local_pkg.install_template and is the upgrade form, so a re-run after a
	// partial failure replaces the staging content idempotently.
	return transferAndInstallPkgs(ctx, t.Exec, s.LocalPkg, matches, opts)
}

// resolveBuilderImage picks the builder image ref for a BuilderStep on a VM
// target. Order: --builder-image override → compiled BuilderStep.BuilderImage
// → BuilderImageResolver. The builder always runs on the HOST (podman); the
// guest never needs a container runtime.
func (t *VmDeployTarget) resolveBuilderImage(s *BuilderStep, opts EmitOpts) (string, error) {
	image := opts.BuilderImageOverride
	if image == "" {
		image = s.BuilderImage
	}
	if image == "" && t.BuilderImageResolver != nil {
		image = t.BuilderImageResolver(s.Builder)
	}
	if image == "" {
		return "", fmt.Errorf("no builder image for %s (layer=%s); set --builder-image or define builder.%s in charly.yml",
			s.Builder, s.CandyName, s.Builder)
	}
	return image, nil
}

// execHomeArtifactBuilder runs a user-home builder (npm/pixi/cargo) on the
// HOST into a staging dir that is bind-mounted AS the guest home, then ships
// the produced home subdirs into the guest user's $HOME over SSH.
//
// The critical move is running the builder with HOME = the GUEST home PATH
// (t.guestHome). npm shebangs, cargo binary rpaths, and pixi env activation
// scripts bake the install-prefix path; baking the guest's home means the
// artifacts work unchanged once extracted into the guest's real $HOME. Build
// caches (.cache/) are excluded from the transfer — they're large and the
// guest doesn't need them.
func (t *VmDeployTarget) execHomeArtifactBuilder(ctx context.Context, s *BuilderStep, opts EmitOpts) error {
	image, err := t.resolveBuilderImage(s, opts)
	if err != nil {
		return err
	}
	if t.guestHome == "" && !opts.DryRun {
		return fmt.Errorf("execHomeArtifactBuilder: guest home unresolved (layer=%s)", s.CandyName)
	}
	guestHome := t.guestHome
	if guestHome == "" {
		guestHome = "/home/charly" // dry-run placeholder; never written
	}

	// Host staging dir mounted AS the guest home inside the builder, so the
	// builder writes ~/.npm-global etc. to a host-side dir while baking the
	// guest's home path into shebangs/configs.
	stageHost, err := os.MkdirTemp("", "charly-vm-builder-")
	if err != nil {
		return fmt.Errorf("builder staging mkdir: %w", err)
	}
	RegisterTempCleanup(stageHost)
	defer func() { os.RemoveAll(stageHost); UnregisterTempCleanup(stageHost) }()

	bindMounts := map[string]string{guestHome: stageHost}
	envVars := UserScopeEnv(guestHome)
	script, err := renderBuilderScript(s, guestHome)
	if err != nil {
		return err
	}

	out, err := BuilderRun(opts.ContextOrDefault(), BuilderRunOpts{
		BuilderImage: image,
		CandyDir:     s.CandyDir,
		ScriptBody:   script,
		BindMounts:   bindMounts,
		Env:          envVars,
		HostHome:     guestHome,
		DryRun:       opts.DryRun,
		RunAsRoot:    true,
		// Thread Cfg + ProjectDir so BuilderRun's EnsureImagePresent can resolve
		// a namespace-qualified / short builder ref (e.g. a bed's
		// install_opts.builder_image: charly.arch-builder) to its concrete image —
		// newest-local, or built on-demand from the project — instead of only
		// accepting a full registry ref (which breaks when its tag is pruned or
		// ghcr publishing is paused). Mirrors buildDepPkgsOnHost (localpkg.go), the
		// aur-dep-closure builder path that already threads these. Without them,
		// EnsureImagePresent gets cfg=nil and rejects any short/namespace ref with
		// "requires a project directory with charly.yml".
		Cfg:        t.Cfg,
		ProjectDir: t.ProjectDir,
	})
	if len(out) > 0 {
		os.Stderr.Write(out)
	}
	if err != nil {
		return fmt.Errorf("VM %s builder (layer=%s): %w", s.Builder, s.CandyName, err)
	}
	if opts.DryRun {
		return nil
	}

	// Collect the produced home subdirs, skipping build caches.
	entries, err := os.ReadDir(stageHost)
	if err != nil {
		return fmt.Errorf("reading builder staging dir: %w", err)
	}
	var transferDirs []string
	for _, e := range entries {
		if e.Name() == ".cache" {
			continue
		}
		transferDirs = append(transferDirs, e.Name())
	}
	if len(transferDirs) == 0 {
		return fmt.Errorf("%s builder for layer %q produced no home artifacts in %s; check the builder output above",
			s.Builder, s.CandyName, stageHost)
	}

	// Tar the artifacts into a single tarball on the host.
	tarDir, err := os.MkdirTemp("", "charly-vm-builder-tar-")
	if err != nil {
		return fmt.Errorf("tar staging mkdir: %w", err)
	}
	RegisterTempCleanup(tarDir)
	defer func() { os.RemoveAll(tarDir); UnregisterTempCleanup(tarDir) }()
	tarball := filepath.Join(tarDir, "artifacts.tar.gz")
	tarArgs := append([]string{"-C", stageHost, "-czf", tarball}, transferDirs...)
	tarCmd := exec.CommandContext(ctx, "tar", tarArgs...)
	tarCmd.Stderr = os.Stderr
	if err := tarCmd.Run(); err != nil {
		return fmt.Errorf("tar builder artifacts: %w", err)
	}

	// Ship to the guest and extract into the guest user's $HOME AS the guest
	// user, so ownership + baked paths are correct.
	guestTar := "/tmp/charly-builder-" + s.CandyName + ".tar.gz"
	if err := t.Exec.PutFile(ctx, tarball, guestTar, 0o644, false, opts); err != nil {
		return fmt.Errorf("scp builder artifacts: %w", err)
	}
	// Extract AS THE GUEST USER so the home artifacts (~/.npm-global, ~/.cargo,
	// ~/.pixi) end up owned by the guest user, not root.
	extractScript := fmt.Sprintf("set -e\nmkdir -p \"$HOME\"\ntar -C \"$HOME\" -xzf %s\n", deployShellQuote(guestTar))
	if err := t.Exec.RunUser(ctx, extractScript, opts); err != nil {
		return fmt.Errorf("extracting builder artifacts in guest: %w", err)
	}
	// Remove the tarball AS ROOT: PutFile placed it via `sudo install`, so it is
	// root-owned, and /tmp is sticky (1777) — the guest user can't remove a
	// root-owned file there ("Operation not permitted"). Cleaning up as root
	// avoids leaving a root-owned tarball behind (and previously aborted the
	// deploy under the extract script's `set -e`).
	if err := t.Exec.RunSystem(ctx, fmt.Sprintf("rm -f %s\n", deployShellQuote(guestTar)), opts); err != nil {
		return fmt.Errorf("removing builder tarball in guest: %w", err)
	}
	return nil
}

// (env.d rendering shared with the host path — see renderEnvdBody in
// shell_profile.go; VmDeployTarget.execShellHook calls it directly.)
