package main

// unified_targets_vm.go — VmUnifiedTarget's Add + lifecycle and
// management methods.
//
//   - Add: constructs the live VmDeployTarget (ssh-config stanza +
//     auto-boot + SSHExecutor), emits the plans, and deploys nested
//     target:pod children from the merged dctx.Node.Nested.
//   - Del: walks the host ledger, runs guest-side ReverseOps over SSH,
//     removes the deploy.yml vm: entry + managed ssh-config stanza.
//   - Lifecycle methods that have a clean ov subcommand surface
//     (Start, Stop, Shell, Logs) shell out via runOvSubcommand. The
//     spawned child uses the same binary on $PATH, so a developer
//     install picks up the local build automatically.
//   - Test: a Runner over the target's SSHExecutor walks the checks.
//   - Status reads ov vm list output for the specific VM.
//
// Unlike Local, every lifecycle method makes sense on a VM target:
// there's a real VM to start/stop/console-into. So no ErrNotSupportedOnVM
// sentinel — every method has a meaningful body.

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Del tears down a VM deploy: walks the host ledger, runs guest-side
// ReverseOps over SSH (via sshReverseRunner), removes the deploy.yml
// vm: entry.
//
// The SSHExecutor used for ReverseOps comes from buildVmReverseRunner
// against the VM's persisted deploy state (deploy.yml's vm_state block).
// The dispatcher (DeployDelCmd.Run) may pre-build a ReverseRunner and
// supply it via VmUnifiedTarget.RevRunner; when nil, Del builds it itself.
func (t *VmUnifiedTarget) Del(ctx context.Context, opts DelOpts) error {
	paths, err := DefaultLedgerPaths()
	if err != nil {
		return err
	}
	rec, err := findVmDeployRecord(paths, t.NodeName)
	if err != nil {
		return err
	}
	if rec == nil {
		// No ledger record → nothing to reverse on the guest. Still
		// clean up the deploy.yml entry if present.
		if entryErr := removeVmDeployEntry(t.NodeName); entryErr != nil {
			fmt.Fprintf(os.Stderr, "note: deploy.yml cleanup: %v\n", entryErr)
		}
		fmt.Fprintf(os.Stderr, "No VM deploy ledger entry for %s (already torn down?)\n", t.NodeName)
		return nil
	}
	if opts.DryRun {
		fmt.Printf("[dry-run] would tear down VM deploy %s (deploy_id=%s, %d layers)\n",
			t.NodeName, rec.DeployID, len(rec.Layer))
		for _, layer := range rec.Layer {
			layerRec, lerr := ReadLayerRecord(paths, layer)
			if lerr != nil || layerRec == nil {
				continue
			}
			for _, op := range layerRec.ReverseOps {
				fmt.Printf("  - %s %v\n", op.Kind, op.Targets)
			}
		}
		return nil
	}

	if t.RevRunner == nil {
		// Caller didn't pre-build the runner — build it ourselves from
		// the persisted deploy state.
		runner, rerr := buildVmReverseRunner(t.NodeName)
		if rerr != nil {
			return fmt.Errorf("building VM reverse runner: %w", rerr)
		}
		t.RevRunner = runner
	}
	re := &vmReverseExec{
		DryRun:          opts.DryRun,
		KeepRepoChanges: t.KeepRepoChanges,
		KeepServices:    t.KeepServices,
		Runner:          t.RevRunner,
	}

	for _, layer := range rec.Layer {
		layerRec, shouldRemove, lerr := RemoveLayerDeployment(paths, layer, rec.DeployID)
		if lerr != nil {
			return fmt.Errorf("removing layer deployment %s: %w", layer, lerr)
		}
		if !shouldRemove {
			continue
		}
		if rerr := runReverseOps(layerRec.ReverseOps, re); rerr != nil {
			return fmt.Errorf("reversing layer %s: %w", layer, rerr)
		}
		_ = t.RevRunner.RunUser(fmt.Sprintf(`rm -f "$HOME/.config/overthink/env.d/%s.env"`, layer))
		if derr := DeleteLayerRecord(paths, layer); derr != nil {
			return fmt.Errorf("deleting layer record %s: %w", layer, derr)
		}
	}

	if derr := DeleteDeployRecord(paths, rec.DeployID); derr != nil {
		return fmt.Errorf("deleting deploy record: %w", derr)
	}

	// Ephemeral lifecycle teardown.
	if node, ok := loadDeployConfigForRead("vm target ephemeral-teardown").LookupKey(t.NodeName); ok && node.IsEphemeral() {
		if tdErr := TeardownEphemeralLifecycle(&node, t.NodeName); tdErr != nil {
			fmt.Fprintf(os.Stderr, "warning: ephemeral lifecycle teardown: %v\n", tdErr)
		}
	}

	if rerr := removeVmDeployEntry(t.NodeName); rerr != nil {
		fmt.Fprintf(os.Stderr, "note: deploy.yml cleanup: %v\n", rerr)
	}

	// Remove the VM's managed ssh-config Host stanza. When this was the
	// last managed alias, also strip the Include line from ~/.ssh/config.
	if home, herr := os.UserHomeDir(); herr == nil {
		remaining, rerr := RemoveVmSshStanza(home, VmSshAlias(t.NodeName))
		if rerr != nil {
			fmt.Fprintf(os.Stderr, "note: ssh-config stanza cleanup: %v\n", rerr)
		}
		if remaining == 0 {
			if rerr := RemoveSshConfigInclude(home); rerr != nil {
				fmt.Fprintf(os.Stderr, "note: ssh-config include cleanup: %v\n", rerr)
			}
		}
	}
	if !opts.DryRun {
		fmt.Fprintf(os.Stderr, "Removed VM deploy %s\n", t.NodeName)
	}
	return nil
}

// vmReverseExec is the VM equivalent of hostReverseExec — combines the
// target's gate flags with per-call DryRun. Identical shape; kept
// separate so a future divergence (VM-specific gates) doesn't ripple
// into Host code.
type vmReverseExec struct {
	DryRun          bool
	KeepRepoChanges bool
	KeepServices    bool
	Runner          ReverseRunner
}

func (e *vmReverseExec) reverseDryRun() bool          { return e.DryRun }
func (e *vmReverseExec) reverseKeepRepoChanges() bool { return e.KeepRepoChanges }
func (e *vmReverseExec) reverseKeepServices() bool    { return e.KeepServices }
func (e *vmReverseExec) reverseRunner() ReverseRunner { return e.Runner }

// Test runs deploy-scope checks against the live VM via its SSHExecutor.
// Mirrors LocalUnifiedTarget.Test — only the executor differs.
func (t *VmUnifiedTarget) Test(ctx context.Context, checks []Check, opts TestOpts) error {
	onlyIDs := make(map[string]bool, len(opts.OnlyIDs))
	for _, id := range opts.OnlyIDs {
		onlyIDs[id] = true
	}
	filtered := checks
	if len(onlyIDs) > 0 {
		filtered = filtered[:0]
		for _, c := range checks {
			if onlyIDs[c.ID] {
				filtered = append(filtered, c)
			}
		}
	}
	exec := t.Executor()
	if exec == nil {
		return fmt.Errorf("vm %q: no SSHExecutor configured", t.NodeName)
	}
	runner := NewRunner(exec, nil, RunModeLive)
	results := runner.Run(ctx, filtered)
	failed := 0
	for _, r := range results {
		if r.Status == TestFail {
			failed++
			id := ""
			if r.Check != nil {
				id = r.Check.ID
			}
			fmt.Fprintf(os.Stderr, "FAIL %s: %s\n", id, r.Message)
			if opts.StopOnFail {
				return fmt.Errorf("test stopped at first failure: %s", id)
			}
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d vm check(s) failed", failed)
	}
	return nil
}

// Update re-applies plans against the VM via SSH. Idempotent re-apply
// over the existing guest ledger — equivalent to a fresh Add. Future
// "diff and only apply changed steps" mode would live behind an
// UpdateOpts flag.
func (t *VmUnifiedTarget) Update(ctx context.Context, plans []*InstallPlan, opts UpdateOpts) error {
	if t.VmDeployTarget == nil {
		return fmt.Errorf("vm %q: VmDeployTarget is nil", t.NodeName)
	}
	return t.VmDeployTarget.Emit(plans, EmitOpts{
		DryRun:           opts.DryRun,
		AllowRepoChanges: opts.AllowRepoChanges,
		AllowRootTasks:   opts.AllowRootTasks,
		WithServices:     opts.WithServices,
		AssumeYes:        opts.AssumeYes,
	})
}

// Start boots the VM via the existing `ov vm start` subcommand. Sub-
// process spawn matches the rebuildVm pattern; the spawned child uses
// the same binary the parent was invoked from.
func (t *VmUnifiedTarget) Start(ctx context.Context) error {
	return runOvSubcommand("vm", "start", t.vmEntityName())
}

// Stop graceful-shutdowns the VM via `ov vm stop`.
func (t *VmUnifiedTarget) Stop(ctx context.Context) error {
	return runOvSubcommand("vm", "stop", t.vmEntityName())
}

// Status reads `ov vm list` output and walks for this target's domain
// name. Returns a typed StatusInfo regardless of which backend
// (libvirt/qemu) is in use — the list output normalizes them. Falls
// back to "stopped" when the VM isn't in the list (no domain created
// yet). Returns "unknown" + the captured error on a real CLI failure.
func (t *VmUnifiedTarget) Status(ctx context.Context) (StatusInfo, error) {
	want := t.vmDomainName()
	out, err := captureOvStdout("vm", "list")
	if err != nil {
		return StatusInfo{State: "unknown"}, err
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		if fields[0] != want {
			continue
		}
		state := fields[len(fields)-1]
		return StatusInfo{
			State:   state,
			Healthy: state == "running",
			Details: map[string]string{
				"backend": fields[1],
				"domain":  fields[0],
			},
		}, nil
	}
	return StatusInfo{State: "stopped", Healthy: false}, nil
}

// Logs streams the VM's serial console via `ov vm console`. Follow=true
// keeps streaming; follow=false captures whatever is in the console
// buffer and returns. Tail is currently ignored — console buffers
// don't expose a per-line tail.
func (t *VmUnifiedTarget) Logs(ctx context.Context, opts LogsOpts) error {
	return runOvSubcommand("vm", "console", t.vmEntityName())
}

// Shell sshes into the VM via `ov vm ssh`. With cmd, runs it non-
// interactively and returns. Without cmd, opens an interactive session.
func (t *VmUnifiedTarget) Shell(ctx context.Context, cmd []string) error {
	args := []string{"vm", "ssh", t.vmEntityName()}
	args = append(args, cmd...)
	return runOvSubcommand(args...)
}

// Rebuild destroys + (optionally) rebuilds the disk image + recreates +
// starts the VM. This is the VM rebuild path. The
// disposable check is the caller's responsibility (the disposable
// classification); this method does not re-validate.
func (t *VmUnifiedTarget) Rebuild(ctx context.Context, opts RebuildOpts) error {
	name := t.vmEntityName()
	if opts.DryRun {
		fmt.Printf("dry-run: ov vm destroy %s\n", name)
		if opts.RebuildImage {
			fmt.Printf("dry-run: ov vm build %s\n", name)
		}
		fmt.Printf("dry-run: ov vm create %s\n", name)
		fmt.Printf("dry-run: ov vm start %s\n", name)
		return nil
	}
	// Destroy is best-effort — the VM may not exist yet on a first build.
	_ = runOvSubcommand("vm", "destroy", name)
	if opts.RebuildImage {
		if err := runOvSubcommand("vm", "build", name); err != nil {
			return fmt.Errorf("ov vm build %s: %w", name, err)
		}
	}
	if err := runOvSubcommand("vm", "create", name); err != nil {
		return fmt.Errorf("ov vm create %s: %w", name, err)
	}
	stderr, startErr := runOvSubcommandCapture("vm", "start", name)
	if startErr != nil {
		if isBenignAlreadyRunning(stderr) {
			return nil
		}
		fmt.Fprint(os.Stderr, stderr)
		return fmt.Errorf("ov vm start %s: %w", name, startErr)
	}
	if stderr != "" {
		fmt.Fprint(os.Stderr, stderr)
	}
	return nil
}

// vmEntityName returns the name to pass to `ov vm <verb>` — the
// kind:vm entity name. Defaults to NodeName; when VmDeployTarget is
// embedded with a populated VMName, prefers that (the deploy.yml
// node's `vm:` cross-ref is the canonical mapping).
func (t *VmUnifiedTarget) vmEntityName() string {
	if t.VmDeployTarget != nil && t.VMName != "" {
		return t.VMName
	}
	// NodeName is the DEPLOY key, which is NOT the vm entity name when they
	// differ (e.g. bed eval-k3s-vm -> vm: k3s-vm). Resolve the deploy's
	// `vm:` cross-ref via the shared resolver so `ov update <bed>` runs
	// `ov vm create <vm-entity>`, not `ov vm create <deploy-key>`. Fall back
	// to NodeName only for legacy vm:<name> deploy keys that declare no `vm:`.
	if vm := vmEntityForDeploy(t.NodeName); vm != "" {
		return vm
	}
	return t.NodeName
}

// vmDomainName returns the libvirt/qemu domain name. Convention:
// "ov-<entity>" with optional "-<instance>" suffix. Mirrors the
// vmName() helper used by VmStartCmd et al.
func (t *VmUnifiedTarget) vmDomainName() string {
	return "ov-" + t.vmEntityName()
}

// vmEntityForAdd resolves the kind:vm entity name for an add. Prefers the
// merged node's `vm:` cross-ref (the canonical mapping for a schema-v4
// deploy where the key != entity, e.g. eval-k3s-vm → vm: k3s-vm); falls
// back to stripping a legacy "vm:<name>" deploy-key prefix, then to the
// leaf of a nested dotted path (stack.myvm → myvm).
func vmEntityForAdd(node *DeploymentNode, name string) (string, error) {
	if node != nil && node.Vm != "" {
		return node.Vm, nil
	}
	if strings.HasPrefix(name, "vm:") {
		return vmNameFromDeployName(name)
	}
	if strings.Contains(name, ".") {
		return pathLeaf(name), nil
	}
	return "", fmt.Errorf("vm deploy %q: no `vm:` cross-ref and key is not a legacy vm:<name> form", name)
}

// Add brings a target:vm deployment online: resolves the kind:vm entity,
// publishes the managed ssh-config stanza, builds an SSHExecutor (or
// NestedExecutor under a parent), auto-boots the VM if unreachable,
// constructs the live VmDeployTarget, emits the plans, retrieves layer
// artifacts, deploys nested target:pod children IN the guest from the
// MERGED dctx.Node.Nested, and writes back VmDeployState.
//
// THE CRUX: nested pods come from dctx.Node — the dispatch-merged node
// (project+operator field merge from resolveTreeRoot). A whole-node
// re-read of the operator deploy.yml would drop a project-declared
// `nested:` under an operator overlay that omits it; consuming the merged
// node is the one source of truth (R3).
func (t *VmUnifiedTarget) Add(ctx context.Context, dctx *DeployContext, plans []*InstallPlan, opts EmitOpts) error {
	node := dctx.Node
	dir := dctx.Dir
	deployName := dctx.Name

	vmName, err := vmEntityForAdd(node, deployName)
	if err != nil {
		return err
	}

	// Load the kind:vm entity from overthink.yml.
	uf, ok, err := LoadUnified(dir)
	if err != nil {
		return fmt.Errorf("loading overthink.yml: %w", err)
	}
	if !ok || uf.VM == nil {
		return fmt.Errorf("deploy %q: no overthink.yml or no kind:vm entities declared", deployName)
	}
	spec, ok := uf.VM[vmName]
	if !ok {
		return fmt.Errorf("deploy %q: no kind:vm entity named %q in overthink.yml", deployName, vmName)
	}

	// Ephemeral lifecycle hook (FIRST action — panic-safe TTL ordering).
	// Consumes the MERGED node (never a deploy.yml re-read).
	registerEphemeralIfMarked(node, deployName)

	// Load existing VmDeployState (RUNTIME state: instance-id, ssh_port,
	// disk path) from deploy.yml. This is persistence written back by THIS
	// path — not a node-field re-read — so it legitimately reads the
	// operator deploy.yml entry keyed by the deploy name.
	var state *VmDeployState
	if dc := loadDeployConfigForRead("ov deploy add vm"); dc != nil {
		if entry, exists := dc.Deploy[deployName]; exists && entry.VmState != nil {
			state = entry.VmState
		}
	}
	if state == nil {
		state = &VmDeployState{}
	}

	// Resolve VM state dir (for SSH keys, NVRAM, persistent sockets).
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolving home dir: %w", err)
	}
	stateDir := filepath.Join(home, ".local", "share", "ov", "vm", "ov-"+vmName)

	// Resolve SSH details.
	sshUser := resolveVmSshUser(spec)
	sshPort, err := resolveVmSshPort(spec, vmName)
	if err != nil {
		return err
	}
	sshKeyPath := filepath.Join(stateDir, "id_ed25519")
	knownHostsPath := filepath.Join(stateDir, "known_hosts")

	// Publish (or refresh) the managed ssh-config Host stanza for this VM
	// and ensure the Include line is present in ~/.ssh/config. The alias
	// keys off the unprefixed VM template name (vmName).
	if err := WriteVmSshStanza(home, VmSshStanza{
		Alias:          VmSshAlias(vmName),
		Hostname:       "127.0.0.1",
		Port:           sshPort,
		User:           sshUser,
		IdentityFile:   sshKeyPath,
		KnownHostsFile: knownHostsPath,
	}); err != nil {
		return fmt.Errorf("publishing ssh-config stanza: %w", err)
	}
	if err := EnsureSshConfigInclude(home); err != nil {
		return fmt.Errorf("ensuring ssh-config include: %w", err)
	}

	// Resolve key-injection state (persisted into VmDeployState for audit).
	smbiosOn, cloudInitOn := ResolveKeyInjectionChannels(spec)

	// Build the DeployExecutor against the managed ssh-config alias. For a
	// nested VM, the same alias works inside the parent's venue.
	alias := VmSshAlias(vmName)
	var exec DeployExecutor = &SSHExecutor{
		Host:           alias,
		ConnectTimeout: 10,
	}
	if opts.ParentExec != nil {
		exec = &NestedExecutor{
			Parent: opts.ParentExec,
			Jump:   NestedJump{Kind: JumpSSH, Target: alias},
		}
	}

	// Build VmDeployTarget.
	target := &VmDeployTarget{
		Name:       "vm:" + vmName,
		VMName:     vmName,
		Spec:       spec,
		State:      state,
		Exec:       exec,
		DistroCfg:  dctx.DistroCfg,
		Cfg:        dctx.Cfg,
		ProjectDir: dctx.Dir,
	}

	// Resolve layer secrets + inject them into TaskSteps BEFORE emission
	// (R3 shared helper).
	layerList, secretEnv, err := prepareLayerSecrets(plans, dir)
	if err != nil {
		return fmt.Errorf("loading layers for secret resolution: %w", err)
	}

	// artifactEnv = secretEnv overlaid with the MERGED node's env: lines
	// (R3 shared helper) — so rewrite rules like ${K3S_KUBECONFIG_SERVER}
	// resolve to the declared value. This consumes dctx.Node directly,
	// replacing the former mergeNodeEnv(pdc)/mergeNodeEnv(dc) re-read.
	artifactEnv := buildArtifactEnv(secretEnv, node)

	// Auto-boot integration: if the VM isn't reachable on its SSH port
	// yet, `ov vm build` + `ov vm create` to boot it. TCP probe — fast.
	// Skipped in DryRun, when nested, and when OV_DEPLOY_NO_AUTOBOOT is set.
	if !opts.DryRun && opts.ParentExec == nil && os.Getenv("OV_DEPLOY_NO_AUTOBOOT") == "" {
		sshAddr := fmt.Sprintf("127.0.0.1:%d", sshPort)
		conn, dialErr := net.DialTimeout("tcp", sshAddr, 2*time.Second)
		if dialErr != nil {
			fmt.Fprintf(os.Stderr,
				"VM %q not reachable on %s — auto-booting via `ov vm build %s` + `ov vm create %s` (set OV_DEPLOY_NO_AUTOBOOT=1 to skip)...\n",
				vmName, sshAddr, vmName, vmName)
			if bErr := runOvSubcommand("vm", "build", vmName); bErr != nil {
				return fmt.Errorf("auto-boot: ov vm build %s: %w", vmName, bErr)
			}
			if cErr := runOvSubcommand("vm", "create", vmName); cErr != nil {
				return fmt.Errorf("auto-boot: ov vm create %s: %w", vmName, cErr)
			}
		} else {
			_ = conn.Close()
		}
	}

	// Emit plans.
	if err := target.Emit(plans, opts); err != nil {
		return fmt.Errorf("VmDeployTarget.Emit: %w", err)
	}

	// Retrieve layer artifacts + k3s post-hook (R3 shared helper). Keyed by the
	// VM-ENTITY name ("vm:<entity>"), NOT the deploy key: a k3s cluster hosted in
	// a VM is identified by that VM (one cluster per VM, possibly reached by
	// several beds/deploys), so its ClusterProfile + artifact cache must land
	// under "vm-<entity>" — the name `cluster:` refs use (e.g. the eval-k3s-vm
	// bed's `cluster: "vm-k3s-vm"`). Passing the deploy key here wrote the fresh
	// kubeconfig under the wrong profile name, leaving the probe on a stale CA.
	if err := retrieveArtifactsAndK3s(ctx, exec, layerList, "vm:"+vmName, artifactEnv, opts); err != nil {
		return fmt.Errorf("retrieving layer artifacts: %w", err)
	}

	// Deploy nested target:pod children as persistent in-guest quadlets.
	// Runs AFTER Emit (so the VM's own layers + any kernel-driver reboot
	// are applied). Skipped on dry-run, nested VMs, and --node-only.
	//
	// The children come from the dispatch-merged dctx.Node — THE source
	// of truth (R3).
	if !opts.DryRun && opts.ParentExec == nil && !t.NodeOnly && node != nil && len(node.Nested) > 0 {
		if err := deployNestedPodsInGuest(vmName, node, exec, opts); err != nil {
			return fmt.Errorf("deploying nested pods in guest: %w", err)
		}
	}

	// Write back updated VmDeployState to deploy.yml.
	state.SshUser = sshUser
	state.SshPort = sshPort
	if state.Backend == "" {
		state.Backend = "auto"
	}
	state.KeyInjectionResolved = &VmKeyInjectionResolved{SMBIOS: smbiosOn, CloudInit: cloudInitOn}
	state.OvInstallStrategy = string(ResolveOvInstallStrategy(spec))

	if err := saveVmDeployState(deployName, state, spec); err != nil {
		return fmt.Errorf("persisting VmDeployState: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Deployed %s (ssh: %s@127.0.0.1:%d)\n", deployName, sshUser, sshPort)
	return nil
}
