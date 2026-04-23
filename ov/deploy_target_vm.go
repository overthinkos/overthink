package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// VmDeployTarget applies an InstallPlan inside a running VM over SSH.
// Uses the same InstallPlan IR that HostDeployTarget consumes — the
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
	// runBuilder step. Same shape as HostDeployTarget's resolver —
	// builders run on the host, artifacts are scp'd into the guest.
	BuilderImageResolver func(builderName string) string

	// Distro mirrors HostDeployTarget.Distro for gating decisions
	// (e.g. aur on non-Arch host). For a VM target, this is the
	// GUEST distro, not the host; resolved via ssh /etc/os-release.
	Distro *HostDistro

	// DryRunWriter receives dry-run output. Nil defaults to os.Stderr.
	DryRunWriter *os.File
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
// Then walks the plans identically to HostDeployTarget, but with
// SSH-wrapped shell execution.
func (t *VmDeployTarget) Emit(plans []*InstallPlan, opts EmitOpts) error {
	if t.Exec == nil {
		return fmt.Errorf("VmDeployTarget: Exec is nil")
	}
	if t.Spec == nil {
		return fmt.Errorf("VmDeployTarget: Spec is nil")
	}

	ctx := opts.ContextOrDefault()

	// 1. Wait for SSH.
	if sshExec, ok := t.Exec.(*SSHExecutor); ok {
		fmt.Fprintf(os.Stderr, "Waiting for sshd on %s:%d...\n", sshExec.Host, sshExec.Port)
		if err := sshExec.WaitForSSH(ctx, 120); err != nil {
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

	// 5. Iterate plans, writing per-layer ledger records to the HOST
	//    (same as container deploys — the ledger is host-side regardless
	//    of where the work executes). DeployRecord is written at the end.
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
			_ = WriteDeployRecord(paths, deployRec)
			return fmt.Errorf("VmDeployTarget: plan %s: %w", plan.Layer, err)
		}
		if err := t.recordLayer(paths, layerRec, plan, opts); err != nil {
			return fmt.Errorf("VmDeployTarget: recording layer %s: %w", plan.Layer, err)
		}
		deployRec.Layers = append(deployRec.Layers, plan.Layer)
		if deployRec.Image == "" && plan.Layer != "" {
			// For pure-add_layers vm deploys the deploy-id's "image" slot
			// stays as the vm: target name so `ov deploy del` can find it.
			deployRec.Image = t.targetName()
		}
	}

	deployRec.AddLayers = append(deployRec.AddLayers, deployRec.Layers...)
	if !opts.DryRun {
		if err := WriteDeployRecord(paths, deployRec); err != nil {
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

// recordLayer writes the per-layer ledger entry on the host. Mirrors
// HostDeployTarget.recordLayer; keeps the VM deploy's ledger writes in
// the same spot as host/container deploys so `ov deploy del` finds them.
func (t *VmDeployTarget) recordLayer(paths *LedgerPaths, rec *LayerRecord, plan *InstallPlan, opts EmitOpts) error {
	if opts.DryRun || plan.DeployID == "" || rec == nil {
		return nil
	}
	return AddLayerDeployment(paths, plan.Layer, plan.DeployID, func(existing *LayerRecord) {
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
// appropriate DeployExecutor method. Mirrors HostDeployTarget.emitPlan's
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

		default:
			fmt.Fprintf(os.Stderr, "VmDeployTarget: skipping unsupported step kind %T\n", step)
		}
	}

	return rec, nil
}

// execSystemPackages runs the distro's package install command on the
// guest. Currently uses the fallback renderer (dnf / apt-get / pacman);
// the structured-template path is shared with HostDeployTarget and
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
func (t *VmDeployTarget) execServiceCustom(ctx context.Context, s *ServiceCustomStep, plan *InstallPlan, opts EmitOpts) error {
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
// available), then scp's the resulting artifacts into the guest. For
// the MVP this is a placeholder — builder-artifact shuttling needs
// per-builder knowledge (pixi, npm, cargo, aur each produce different
// trees) and is Phase 2 of the VM deploy target per plan D11.
func (t *VmDeployTarget) execBuilder(ctx context.Context, s *BuilderStep, plan *InstallPlan, opts EmitOpts) error {
	if opts.SkipIncompatible {
		fmt.Fprintf(os.Stderr, "VmDeployTarget: skipping builder step %q (--skip-incompatible)\n", s.Builder)
		return nil
	}
	return fmt.Errorf("builder steps (%s) in VM deploys are not yet supported (Phase 2); re-run with --skip-incompatible to ignore", s.Builder)
}

// --- Package-install fallback shared with host (mirrors
// HostDeployTarget.renderFallbackPkgCmd). ---

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
		return fmt.Sprintf("DEBIAN_FRONTEND=noninteractive apt-get install -y%s %s", opts, strings.Join(s.Packages, " "))
	case "pac":
		return fmt.Sprintf("pacman -S --noconfirm --needed%s %s", opts, strings.Join(s.Packages, " "))
	}
	return ""
}

// renderVmTaskCommand renders the same task verbs as HostDeployTarget
// (cmd/mkdir/link/setcap/copy/write/download). Kept a separate function
// so VmDeployTarget doesn't depend on HostDeployTarget's receiver, but
// the supported verb set MUST stay in lock-step with the host version —
// any verb the host supports needs a parallel branch here, otherwise
// task steps silently no-op on VMs (observed live 2026-04-22: uv
// layer's `download:` task was silently skipped under VmDeployTarget,
// leaving /usr/local/bin/uv missing).
func renderVmTaskCommand(s *TaskStep) string {
	task := s.Task
	ctxPath := s.CtxPath

	switch {
	case task.Cmd != "":
		body := task.Cmd
		if ctxPath != "" {
			body = strings.ReplaceAll(body, "/ctx/", ctxPath+"/")
		}
		return body
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
		return renderDownloadScript(task)
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
