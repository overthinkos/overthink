package main

import (
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
	if !ok || uf.VMs == nil {
		return fmt.Errorf("deploy %q: no overthink.yml or no kind:vm entities declared", c.Name)
	}
	spec, ok := uf.VMs[vmName]
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

	// Build SSHExecutor.
	exec := &SSHExecutor{
		User:           sshUser,
		Host:           "127.0.0.1",
		Port:           sshPort,
		KeyPath:        sshKeyPath,
		ConnectTimeout: 10,
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

// runVmDel handles `ov deploy del vm:<vm-name>`. Currently a minimal
// implementation that removes the deploy.yml entry. A future
// enhancement will execute ReverseOps against the guest ledger over
// SSH (mirroring runHostDel but via SSHExecutor).
func (c *DeployDelCmd) runVmDel(paths *LedgerPaths) error {
	if c.DryRun {
		fmt.Printf("[dry-run] would tear down VM deploy %s (guest-side reverse ops pending Task-follow-up)\n", c.Name)
		return nil
	}

	// Remove the deploy.yml entry.
	if err := removeVmDeployEntry(c.Name); err != nil {
		return fmt.Errorf("removing deploy.yml entry: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Removed VM deploy %s (guest-side layer teardown not yet implemented)\n", c.Name)
	_ = paths
	return nil
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

// resolveVmSshUser picks the SSH user for a spec. cloud_image sources
// default to "ov"; bootc sources default to "root".
func resolveVmSshUser(spec *VmSpec) string {
	if spec.SSH != nil && spec.SSH.User != "" {
		return spec.SSH.User
	}
	if spec.Source.Kind == "bootc" {
		return "root"
	}
	return "ov"
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
		dc.Images = map[string]DeployImageConfig{}
	}

	entry, exists := dc.Images[deployName]
	if !exists {
		entry = DeployImageConfig{}
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
