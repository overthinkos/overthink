package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
)

// deployNestedPodsInGuest deploys each nested target:pod child of a VM deploy
// as a PERSISTENT in-guest quadlet — the nested-pod-in-VM capability. For each
// child it (1) builds the child image on the host, (2) cp-images it into the
// guest as localhost/ov-<childKey>:latest (offline; preserves nothing but the
// loaded ref), and (3) runs the guest's own project-free
// `ov deploy from-image <ref> <childKey>` over SSH as the guest user — which
// generates + starts a quadlet from the image's baked OCI labels (ports,
// services, GPU device auto-detected in the guest). `loginctl enable-linger`
// makes the --user quadlet start at boot, so the pod survives a guest reboot.
//
// Idempotent: cp-image skips a present image; from-image re-applies cleanly on
// `ov update`. The host never needs the project for the child (the guest reads
// the labels). nil node / no nested pods → no-op. Shared by
// VmUnifiedTarget.Add (auto-deploy after Emit) and the dotted-path dispatch
// `ov deploy add <vm-bed>.<child>`.
func deployNestedPodsInGuest(vmName string, node *DeploymentNode, exec DeployExecutor, opts EmitOpts) error {
	if node == nil || len(node.Nested) == 0 {
		return nil
	}
	// The guest's own `ov` runs the from-image deploy below. syncOvIntoGuest
	// returns the right command: the guest's SYSTEM ov when it is current
	// (>= host by CalVer — never shadowed, never downgraded), or a /tmp copy of
	// the host binary (outside $PATH) when the guest's ov is absent or older. The
	// guest venue is the same for every child, so resolve it once. See
	// syncOvIntoGuest.
	ovCmd, err := syncOvIntoGuest(context.Background(), exec, opts)
	if err != nil {
		return fmt.Errorf("resolving the ov command for guest %s: %w", vmName, err)
	}
	for _, childKey := range sortedNestedKeys(node.Nested) {
		child := node.Nested[childKey]
		if child == nil || child.Image == "" {
			continue
		}
		switch child.Target {
		case "", "pod", "container":
			// in-guest pod child — handled below
		default:
			continue // android / k8s / vm children are not in-guest pods
		}
		asRef := "localhost/ov-" + childKey + ":latest"
		fmt.Fprintf(os.Stderr, "Deploying nested pod %s.%s (%s) as a persistent in-guest quadlet...\n", vmName, childKey, child.Image)
		if err := runOvSubcommand("image", "build", child.Image); err != nil {
			return fmt.Errorf("build nested image %s (%s): %w", childKey, child.Image, err)
		}
		// --rootless: load into the guest USER's podman storage, because the
		// from-image deploy below runs as the guest user (a --user quadlet) and
		// reads the user's storage — a root-loaded image would be invisible to it.
		if err := runOvSubcommand("vm", "cp-image", vmName, child.Image, "--as", asRef, "--rootless"); err != nil {
			return fmt.Errorf("cp-image nested %s -> guest: %w", childKey, err)
		}
		// Run as the guest user (--user quadlet). `sudo` escalates only the
		// linger enable (the guest interactive user has sudo); the from-image
		// deploy itself runs unprivileged so the quadlet lands in the user's
		// systemd, matching how the operator's interactive session runs it.
		// XDG_RUNTIME_DIR must be exported so the `systemctl --user` calls inside
		// `ov deploy from-image` reach the lingering user bus over this non-login
		// SSH session — the same pattern VmDeployTarget uses for user services.
		// ovCmd is "ov" (the guest's own ov is current) or a temp path to a
		// freshly-pushed host copy (the guest's ov was older) — see
		// ensureFreshNestedOv above.
		script := fmt.Sprintf(
			"sudo loginctl enable-linger \"$(id -un)\" >/dev/null 2>&1 || true\n"+
				"export XDG_RUNTIME_DIR=\"/run/user/$(id -u)\"\n"+
				"%s deploy from-image %s %s",
			ovCmd, asRef, childKey)
		if err := exec.RunUser(context.Background(), script, opts); err != nil {
			return fmt.Errorf("deploy nested pod %s in guest: %w", childKey, err)
		}
		fmt.Fprintf(os.Stderr, "Nested pod %s.%s deployed (persistent in-guest quadlet)\n", vmName, childKey)
	}
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

// buildVmReverseRunner constructs a ReverseRunner pointed at the VM
// via its managed ssh-config alias (ov-<vmName>). The deployName arg
// may be the legacy "vm:<name>" form (rewritten by the dispatcher in
// deploy_add_cmd.go) or already the unprefixed form. We strip the
// "vm:" prefix so the alias matches what `ov vm create` wrote
// ("ov-<vmName>") — without this, VmSshAlias("vm:arch") would
// produce "ov-vm:arch" which is invalid SSH-config Host syntax.
// All SSH connection details (User, Port, IdentityFile, host-key
// checking) live in the managed Host stanza written by `ov vm
// create` / runVmAdd; ssh(1) reads them from ~/.ssh/config so we
// need only the alias here.
func buildVmReverseRunner(deployName string) (*sshReverseRunner, error) {
	vmName, err := vmNameFromDeployName(deployName)
	if err != nil {
		// Fallback: if the name doesn't carry the legacy "vm:" prefix,
		// use it directly. Pre-cutover callers passed plain names.
		vmName = deployName
	}
	exec := &SSHExecutor{
		Host:           VmSshAlias(vmName),
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

// vmNameFromDeployName extracts the VM entity name from a deploy-key
// in the legacy "vm:<name>[/<instance>]" form. Callers that hold a
// schema-v4 deploy key (whose entity comes from the node's `vm:` field)
// resolve the entity via vmEntityForAdd instead; this helper handles the
// prefixed form (legacy refs + the "vm:<entity>" key the del path builds
// for ledger/teardown keying). The `instance` suffix is preserved for
// future per-instance addressing but currently unused.
func vmNameFromDeployName(deployName string) (string, error) {
	if !strings.HasPrefix(deployName, "vm:") {
		return "", fmt.Errorf("VM deploy name must start with 'vm:' (got %q)", deployName)
	}
	rest := strings.TrimPrefix(deployName, "vm:")
	if rest == "" {
		return "", fmt.Errorf("VM deploy name missing vm-name portion (got %q)", deployName)
	}
	if idx := strings.IndexByte(rest, '/'); idx >= 0 {
		return rest[:idx], nil
	}
	return rest, nil
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

// resolveVmSshPort picks the host-side SSH port forward.
//
//   - ssh.port_auto: true → reuse the persisted vm_state.ssh_port if one was
//     already allocated (idempotent across rebuilds), else allocate a free host
//     port via the shared pod-path allocator and let the caller persist it.
//   - ssh.port: N        → that fixed port.
//   - neither            → 2222.
func resolveVmSshPort(spec *VmSpec, vmName string) (int, error) {
	if spec.SSH != nil && spec.SSH.PortAuto {
		if entry, ok := loadDeployConfigForRead("ov vm ssh-port").LookupKey("vm:" + vmName); ok && entry.VmState != nil && entry.VmState.SshPort > 0 {
			return entry.VmState.SshPort, nil
		}
		alloc, err := AllocateAutoPorts([]int{22}, nil)
		if err != nil {
			return 0, fmt.Errorf("vm %q: ssh.port_auto allocation failed: %w", vmName, err)
		}
		return alloc[0].Host, nil
	}
	if spec.SSH != nil && spec.SSH.Port > 0 {
		return spec.SSH.Port, nil
	}
	return 2222, nil
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
	if dc.Deploy == nil {
		dc.Deploy = map[string]DeploymentNode{}
	}

	entry, exists := dc.Deploy[deployName]
	if !exists {
		entry = DeploymentNode{}
	}
	entry.Target = "vm"
	// deployName may be a legacy "vm:<entity>" key (from ov vm create) OR a
	// schema-v4 deploy key whose entity comes from the entry's `vm:` field
	// (e.g. eval-k3s-vm → vm: k3s-vm). Derive the entity from the prefix
	// when present; otherwise PRESERVE the existing entry.Vm (a v4 key has
	// no "vm:" prefix to parse, and clobbering it with "" would drop the
	// cross-ref the rest of the deploy code relies on).
	if vmName, perr := vmNameFromDeployName(deployName); perr == nil {
		entry.Vm = vmName
	}
	entry.VmState = state
	dc.Deploy[deployName] = entry

	return SaveDeployConfig(dc)
}

// removeVmDeployEntry strips images.<deployName> from deploy.yml.
func removeVmDeployEntry(deployName string) error {
	dc, err := LoadDeployConfig()
	if err != nil {
		return err
	}
	if dc == nil || dc.Deploy == nil {
		return nil
	}
	entry, ok := dc.Deploy[deployName]
	if !ok {
		return nil
	}
	// Destroying the VM invalidates only the RUNTIME state (vm_state). Clear
	// that, but PRESERVE every operator-authored per-host field (preemptible,
	// env, tunnel, port, security, add_layers, install_opts, …) so a
	// destroy→create cycle — which is exactly what `ov update <vm>` does
	// (VmUnifiedTarget.Rebuild shells `ov vm destroy` then `ov vm create`) —
	// never silently drops local config. (This is the root cause of the lost
	// `preemptible: {holds: [nvidia-gpu]}` on the operator workstation.)
	//
	// If, after clearing vm_state, the entry carries NOTHING operator-authored
	// beyond the fields saveVmDeployState auto-sets (target: vm + vm:), it was a
	// pure auto-created VM-state record — e.g. a disposable eval-bed VM — so
	// delete it entirely (such entries must not accumulate; that's why
	// destroy cleaned up the entry in the first place). Otherwise keep the
	// now-stateless entry so its operator config survives.
	entry.VmState = nil
	if isAutoVmDeployEntry(entry) {
		delete(dc.Deploy, deployName)
	} else {
		dc.Deploy[deployName] = entry
	}
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
