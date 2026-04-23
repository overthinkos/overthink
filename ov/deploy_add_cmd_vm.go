package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// runVM handles `ov deploy add vm:<vm-name>[/<instance>] [<ref>]`.
// Looks up the kind:vm entity, resolves SSH connection details from
// deploy.yml VmDeployState (or initializes new state), constructs a
// VmDeployTarget with an SSHExecutor wired to the guest, and emits
// the compiled plans.
//
// Pre-flight: the VM must already be booted. Callers typically run
// `ov vm create <vm-name>` first. A future enhancement (Task 19's
// vm.go integration) will auto-boot on first apply.
func (c *DeployAddCmd) runVM(plans []*InstallPlan, dir string, opts EmitOpts) error {
	vmName, instance, err := parseVmDeployName(c.Name)
	if err != nil {
		return err
	}
	_ = instance // reserved for -i instance support; not used in MVP dispatch

	// Load the kind:vm entity from overthink.yml.
	uf, ok, err := LoadUnified(dir)
	if err != nil {
		return fmt.Errorf("loading overthink.yml: %w", err)
	}
	if !ok || uf.VM == nil {
		return fmt.Errorf("deploy %q: no overthink.yml or no kind:vm entities declared", c.Name)
	}
	spec, ok := uf.VM[vmName]
	if !ok {
		return fmt.Errorf("deploy %q: no kind:vm entity named %q in overthink.yml", c.Name, vmName)
	}

	// Load existing VmDeployState from deploy.yml if any.
	dc, _ := LoadDeployConfig()
	var state *VmDeployState
	if dc != nil {
		if entry, exists := dc.Images[c.Name]; exists && entry.VmState != nil {
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
	sshPort := resolveVmSshPort(spec)
	sshKeyPath := state.SshKeyPath
	if sshKeyPath == "" {
		sshKeyPath = filepath.Join(stateDir, "id_ed25519")
	}

	// Resolve key-injection state (persisted into VmDeployState for audit).
	smbiosOn, cloudInitOn := ResolveKeyInjectionChannels(spec)

	// Build the DeployExecutor. At the root of a tree (no parent),
	// this is a direct SSHExecutor from the invoking host to the VM
	// over the forwarded port — today's behavior. When this VM is a
	// child of a container (vm-in-container), the tree walker passes
	// opts.ParentExec — we compose a NestedExecutor so SSH runs
	// through the parent's venue.
	var exec DeployExecutor = &SSHExecutor{
		User:           sshUser,
		Host:           "127.0.0.1",
		Port:           sshPort,
		KeyPath:        sshKeyPath,
		ConnectTimeout: 10,
	}
	if opts.ParentExec != nil {
		// Nested VM: the parent's executor runs commands in its venue,
		// and from there we ssh into the guest.
		sshTarget := fmt.Sprintf("%s@%s:%d", sshUser, "127.0.0.1", sshPort)
		exec = &NestedExecutor{
			Parent: opts.ParentExec,
			Jump: NestedJump{
				Kind:       JumpSSH,
				Target:     sshTarget,
				SSHKeyPath: sshKeyPath,
			},
		}
	}

	// Build VmDeployTarget.
	target := &VmDeployTarget{
		Name:   c.Name,
		VMName: vmName,
		Spec:   spec,
		State:  state,
		Exec:   exec,
	}

	// Emit plans.
	if err := target.Emit(plans, opts); err != nil {
		return fmt.Errorf("VmDeployTarget.Emit: %w", err)
	}

	// Write back updated VmDeployState to deploy.yml.
	state.SshUser = sshUser
	state.SshPort = sshPort
	state.SshKeyPath = sshKeyPath
	if state.Backend == "" {
		// Default to "auto" — vm.go refactor will populate this once
		// ResolveRuntime() is wired into the VM deploy code path.
		state.Backend = "auto"
	}
	state.KeyInjectionResolved = &VmKeyInjectionResolved{
		SMBIOS:    smbiosOn,
		CloudInit: cloudInitOn,
	}
	state.OvInstallStrategy = string(ResolveOvInstallStrategy(spec))

	if err := saveVmDeployState(c.Name, state, spec); err != nil {
		return fmt.Errorf("persisting VmDeployState: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Deployed %s (ssh: %s@127.0.0.1:%d)\n", c.Name, sshUser, sshPort)
	return nil
}

// runVmDel handles `ov deploy del vm:<vm-name>`. Mirrors runHostDel
// 1:1 — reads the host ledger to find the deploy record, iterates each
// layer's ReverseOps and executes them on the GUEST via
// sshReverseRunner (which wraps the same SSHExecutor that applied the
// deploy). At the end, removes the ledger entries and the deploy.yml
// vm: entry.
func (c *DeployDelCmd) runVmDel(paths *LedgerPaths) error {
	// Locate the VM's deploy record in the host ledger. VM deploys are
	// Target="vm:<name>"; there's typically a single record per name.
	rec, err := findVmDeployRecord(paths, c.Name)
	if err != nil {
		return err
	}
	if rec == nil {
		// No ledger record → nothing to reverse on the guest. Still
		// clean up the deploy.yml entry if present.
		if entryErr := removeVmDeployEntry(c.Name); entryErr != nil {
			fmt.Fprintf(os.Stderr, "note: deploy.yml cleanup: %v\n", entryErr)
		}
		fmt.Fprintf(os.Stderr, "No VM deploy ledger entry for %s (already torn down?)\n", c.Name)
		return nil
	}

	if c.DryRun {
		fmt.Printf("[dry-run] would tear down VM deploy %s (deploy_id=%s, %d layers)\n",
			c.Name, rec.DeployID, len(rec.Layers))
		for _, layer := range rec.Layers {
			layerRec, err := ReadLayerRecord(paths, layer)
			if err != nil || layerRec == nil {
				continue
			}
			for _, op := range layerRec.ReverseOps {
				fmt.Printf("  - %s %v\n", op.Kind, op.Targets)
			}
		}
		return nil
	}

	// Build an SSH-backed ReverseRunner from the VmDeployState recorded
	// in the per-machine deploy.yml (same fields VmDeployTarget used at
	// apply time, so we talk to the same guest over the same SSH key).
	runner, err := buildVmReverseRunner(c.Name)
	if err != nil {
		return fmt.Errorf("building VM reverse runner: %w", err)
	}
	c.Runner = runner

	// Tear down each layer record in reverse order (last-installed,
	// first-removed). Mirrors tearDownDeploy but with guest-side exec.
	for _, layer := range rec.Layers {
		layerRec, shouldRemove, err := RemoveLayerDeployment(paths, layer, rec.DeployID)
		if err != nil {
			return fmt.Errorf("removing layer deployment %s: %w", layer, err)
		}
		if !shouldRemove {
			continue
		}
		if err := runReverseOps(layerRec.ReverseOps, c); err != nil {
			return fmt.Errorf("reversing layer %s: %w", layer, err)
		}
		// env.d file cleanup on the guest. Use double-quoted form so $HOME
		// expands — shellQuoteSimple would single-quote the whole string
		// and leave the literal "$HOME" in place.
		_ = runner.RunUser(fmt.Sprintf(`rm -f "$HOME/.config/overthink/env.d/%s.env"`, layer))
		if err := DeleteLayerRecord(paths, layer); err != nil {
			return fmt.Errorf("deleting layer record %s: %w", layer, err)
		}
	}

	if err := DeleteDeployRecord(paths, rec.DeployID); err != nil {
		return fmt.Errorf("deleting deploy record: %w", err)
	}

	// Best-effort deploy.yml cleanup.
	if err := removeVmDeployEntry(c.Name); err != nil {
		fmt.Fprintf(os.Stderr, "note: deploy.yml cleanup: %v\n", err)
	}

	fmt.Fprintf(os.Stderr, "Removed VM deploy %s (%d layers torn down on guest)\n", c.Name, len(rec.Layers))
	return nil
}

// findVmDeployRecord scans paths.Deploys for a record whose Target
// matches c.Name. Returns the first match (VMs typically have a single
// record per name; multiple only arise if a user manually force-applied
// multiple deploy-ids, which is a pathological state we tolerate but
// don't optimise for).
func findVmDeployRecord(paths *LedgerPaths, vmName string) (*DeployRecord, error) {
	entries, err := os.ReadDir(paths.Deploys)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		rec, err := ReadDeployRecord(paths, strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			continue
		}
		if rec != nil && rec.Target == vmName {
			return rec, nil
		}
	}
	return nil, nil
}

// buildVmReverseRunner resolves the VM's SSH connection info (from
// vms.yml spec defaults + VmDeployState overlay in the local
// deploy.yml) and wraps the resulting SSHExecutor in a ReverseRunner.
func buildVmReverseRunner(deployName string) (*sshReverseRunner, error) {
	vmName, _, err := parseVmDeployName(deployName)
	if err != nil {
		return nil, err
	}
	dir, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	uf, ok, err := LoadUnified(dir)
	if err != nil {
		return nil, err
	}
	if !ok || uf.VM == nil {
		return nil, fmt.Errorf("no vms.yml entity %q", vmName)
	}
	spec, ok := uf.VM[vmName]
	if !ok {
		return nil, fmt.Errorf("no vms.yml entity %q", vmName)
	}

	user := resolveVmSshUser(spec)
	port := resolveVmSshPort(spec)
	home, _ := os.UserHomeDir()
	keyPath := filepath.Join(home, ".local", "share", "ov", "vm", "ov-"+vmName, "id_ed25519")

	if dc, _ := LoadDeployConfig(); dc != nil {
		if entry, ok := dc.Images[deployName]; ok && entry.VmState != nil {
			if entry.VmState.SshUser != "" {
				user = entry.VmState.SshUser
			}
			if entry.VmState.SshPort > 0 {
				port = entry.VmState.SshPort
			}
			if entry.VmState.SshKeyPath != "" {
				keyPath = entry.VmState.SshKeyPath
			}
		}
	}
	if user == "" || port == 0 || keyPath == "" {
		return nil, fmt.Errorf("VM %s has incomplete SSH config (user=%q port=%d key=%q)",
			deployName, user, port, keyPath)
	}
	exec := &SSHExecutor{
		User:           user,
		Host:           "127.0.0.1",
		Port:           port,
		KeyPath:        keyPath,
		ConnectTimeout: 10,
	}
	return &sshReverseRunner{exec: exec}, nil
}

// sshReverseRunner adapts SSHExecutor to the ReverseRunner interface so
// reverse_ops.go handlers can run tear-down commands inside the VM
// without knowing about SSH.
type sshReverseRunner struct {
	exec *SSHExecutor
}

func (r *sshReverseRunner) RunSystem(script string) error {
	return r.exec.RunSystem(context.Background(), script, EmitOpts{})
}

func (r *sshReverseRunner) RunUser(script string) error {
	return r.exec.RunUser(context.Background(), script, EmitOpts{})
}

// parseVmDeployName splits "vm:<name>[/<instance>]" into vmName +
// instance. Returns an error if the prefix is missing or the name
// portion is empty.
func parseVmDeployName(deployName string) (vmName, instance string, err error) {
	if !strings.HasPrefix(deployName, "vm:") {
		return "", "", fmt.Errorf("VM deploy name must start with 'vm:' (got %q)", deployName)
	}
	rest := strings.TrimPrefix(deployName, "vm:")
	if rest == "" {
		return "", "", fmt.Errorf("VM deploy name missing vm-name portion (got %q)", deployName)
	}
	if idx := strings.IndexByte(rest, '/'); idx >= 0 {
		return rest[:idx], rest[idx+1:], nil
	}
	return rest, "", nil
}

// resolveVmSshUser picks the SSH user for a spec. Precedence mirrors
// resolveCloudInitSSHUser: explicit spec.ssh.user → spec.source.base_user
// (adopt path for cloud images) → source-kind default ("root" for bootc).
// cloud_image sources with no base_user declared have no sensible
// default — callers treat "" as "user must supply --ssh-key none and
// manage identity out-of-band, or declare base_user in the spec".
func resolveVmSshUser(spec *VmSpec) string {
	if spec.SSH != nil && spec.SSH.User != "" {
		return spec.SSH.User
	}
	if spec.Source.BaseUser != "" {
		return spec.Source.BaseUser
	}
	if spec.Source.Kind == "bootc" {
		return "root"
	}
	return ""
}

// resolveVmSshPort picks the host-side SSH port forward. Default 2222.
func resolveVmSshPort(spec *VmSpec) int {
	if spec.SSH != nil && spec.SSH.Port > 0 {
		return spec.SSH.Port
	}
	return 2222
}

// saveVmDeployState writes the updated VmDeployState into
// ~/.config/ov/deploy.yml for the given deploy name. Idempotent —
// overwrites the images.<name>.vm_state block.
func saveVmDeployState(deployName string, state *VmDeployState, spec *VmSpec) error {
	// Load existing deploy.yml (or start fresh).
	dc, err := LoadDeployConfig()
	if err != nil {
		return fmt.Errorf("loading deploy.yml: %w", err)
	}
	if dc == nil {
		dc = &DeployConfig{}
	}
	if dc.Images == nil {
		dc.Images = map[string]DeploymentNode{}
	}

	entry, exists := dc.Images[deployName]
	if !exists {
		entry = DeploymentNode{}
	}
	entry.Target = "vm"
	vmName, _, _ := parseVmDeployName(deployName)
	entry.VmSource = vmName
	entry.VmState = state
	dc.Images[deployName] = entry

	return SaveDeployConfig(dc)
}

// removeVmDeployEntry strips images.<deployName> from deploy.yml.
func removeVmDeployEntry(deployName string) error {
	dc, err := LoadDeployConfig()
	if err != nil {
		return err
	}
	if dc == nil || dc.Images == nil {
		return nil
	}
	delete(dc.Images, deployName)
	return SaveDeployConfig(dc)
}

// hostArchRuntime returns runtime.GOARCH translated to the libvirt/
// QEMU canonical form (amd64 → x86_64, arm64 → aarch64).
func hostArchRuntime() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	default:
		return runtime.GOARCH
	}
}
