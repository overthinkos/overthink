package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
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
func (c *DeployAddCmd) runVM(deployName string, plans []*InstallPlan, dir string, opts EmitOpts) error {
	vmName, err := vmNameFromDeployName(c.Name)
	if err != nil {
		return err
	}

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
	dc := loadDeployConfigForRead("ov deploy add vm")

	// Ephemeral lifecycle hook (FIRST action — panic-safe TTL ordering).
	// When this deploy is marked ephemeral, register the systemd
	// transient timer + parent-detection + snapshot refcount BEFORE
	// any libvirt or qemu-img call. The handle is ephemeral-runtime
	// metadata persisted into deploy.yml; teardown reads it from there.
	if dc != nil {
		if node, ok := dc.Deploy[c.Name]; ok && node.IsEphemeral() {
			if _, regErr := RegisterEphemeralLifecycle(&node, c.Name); regErr != nil {
				fmt.Fprintf(os.Stderr, "warning: ephemeral lifecycle registration: %v\n", regErr)
			}
		}
	}
	var state *VmDeployState
	if dc != nil {
		if entry, exists := dc.Deploy[c.Name]; exists && entry.VmState != nil {
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
	sshKeyPath := filepath.Join(stateDir, "id_ed25519")
	knownHostsPath := filepath.Join(stateDir, "known_hosts")

	// Publish (or refresh) the managed ssh-config Host stanza for this
	// VM and ensure the Include line is present in ~/.ssh/config. After
	// these two calls, `ssh ov-<vmname>` works from any terminal and
	// our SSHExecutor needs only the alias as Host. The alias keys off
	// the unprefixed VM template name (vmName) — c.Name carries the
	// legacy "vm:<name>" sentinel which would produce a malformed
	// "ov-vm:<name>" alias (colons aren't valid SSH config Host names).
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

	// Build the DeployExecutor. The VM publishes a managed ssh-config
	// Host stanza (ov-<vmname>) via WriteVmSshStanza after `ov vm
	// create`; from then on `ssh ov-<vmname>` works from any terminal
	// and SSHExecutor needs nothing but the alias as Host. The user's
	// ~/.ssh/config Includes ~/.config/ov/ssh_config (managed by
	// EnsureSshConfigInclude). For nested VMs, the same alias works
	// inside the parent's venue.
	alias := VmSshAlias(vmName)
	var exec DeployExecutor = &SSHExecutor{
		Host:           alias,
		ConnectTimeout: 10,
	}
	if opts.ParentExec != nil {
		// Nested VM: the parent's executor runs commands in its venue,
		// and from there we ssh into the guest.
		exec = &NestedExecutor{
			Parent: opts.ParentExec,
			Jump: NestedJump{
				Kind:   JumpSSH,
				Target: alias,
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

	// Resolve layer secret_requires / secret_accepts and inject them
	// into the TaskSteps BEFORE emission. Missing `secret_requires:`
	// auto-generate a 32-byte hex token via ensureLayerSecret.
	layerList, err := LayerForPlan(plans, dir, nil)
	if err != nil {
		return fmt.Errorf("loading layers for secret resolution: %w", err)
	}
	secretEnv := ResolveSecretForLayer(layerList)
	InjectSecretsIntoPlans(plans, secretEnv)

	// Collect env for artifact substitution — merges resolved secrets +
	// any deploy.yml env: entries on this node. Needed so rewrite rules
	// like "${K3S_SERVER_HOSTNAME}" resolve to the declared hostname
	// rather than the literal placeholder.
	artifactEnv := map[string]string{}
	for k, v := range secretEnv {
		artifactEnv[k] = v
	}
	// Merge the deploy entry's env: entries so artifact rewrite rules like
	// "${K3S_KUBECONFIG_SERVER}" resolve to the declared value instead of
	// the literal placeholder / shell default. The deploy entry is keyed by
	// the DEPLOY NAME (the bed key, e.g. "eval-k3s-vm"), NOT the vm entity
	// name — resolve it via the shared findVmDeployNode(deployName, vmName).
	// Pre-fix this keyed off c.Name (rewritten upstream to "vm:<entity>"),
	// which silently missed any bed whose key differs from its vm entity
	// name (the eval-k3s-vm -> vm: k3s-vm case), defaulting the kubeconfig
	// server rewrite to :6443. Operator overlay (dc) merges over project
	// (pdc): later wins.
	mergeNodeEnv := func(deploys map[string]DeploymentNode) {
		node, ok := findVmDeployNode(deploys, deployName, vmName)
		if !ok {
			return
		}
		for _, line := range node.Env {
			if idx := strings.Index(line, "="); idx > 0 {
				artifactEnv[line[:idx]] = line[idx+1:]
			}
		}
	}
	if uf != nil {
		if pdc := uf.ProjectDeployConfig(); pdc != nil {
			mergeNodeEnv(pdc.Deploy)
		}
	}
	if dc != nil {
		mergeNodeEnv(dc.Deploy)
	}

	// Auto-boot integration (formerly Task 19, deferred at
	// deploy_add_cmd_vm.go's runVM-doc): if the VM isn't reachable
	// on its SSH port yet, invoke `ov vm build <vmName>` (idempotent;
	// fetches/builds the disk if missing) followed by `ov vm create
	// <vmName>` to boot it. Probe via TCP DialTimeout — fast (10ms
	// happy path; ~2s connection-refused). Idempotent: when the VM
	// is already up, this short-circuits and Emit's own WaitForSSH
	// gates SSH-readiness. Skipped in DryRun mode and when the
	// deploy is nested (parent's executor handles the
	// guest-into-guest path differently).
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

	// Retrieve layer artifacts (files the layer publishes back to the
	// operator after successful setup, e.g. kubeconfig from a k3s-server
	// layer). Uses the same SSH executor that applied the deploy.
	if !opts.DryRun {
		if aErr := RetrieveLayerArtifacts(context.Background(), exec, layerList, sanitizeDeployName(c.Name), artifactEnv, opts); aErr != nil {
			return fmt.Errorf("retrieving layer artifacts: %w", aErr)
		}
		// k3s-server post-hook: merge retrieved kubeconfig into
		// ~/.kube/config and write a ClusterProfile so the new cluster
		// is immediately usable via `ov eval k8s --cluster <name>` and
		// `ov deploy add <app> --target kubernetes --kubernetes-cluster <name>`.
		if deployHasLayer(layerList, "k3s-server") {
			if pErr := K3sPostProvision(c.Name); pErr != nil {
				return fmt.Errorf("k3s post-provision: %w", pErr)
			}
		}
	}

	// Write back updated VmDeployState to deploy.yml.
	state.SshUser = sshUser
	state.SshPort = sshPort
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

// runVmDel is a thin wrapper that constructs a VmUnifiedTarget with
// this cmd's gate flags and delegates teardown to the unified target's
// Del method (see unified_targets_vm.go). The body lives on
// VmUnifiedTarget.Del so future schema-v3 dispatchers can call into the
// same logic without going through DeployDelCmd.
func (c *DeployDelCmd) runVmDel(paths *LedgerPaths) error {
	target := &VmUnifiedTarget{
		NodeName:        c.Name,
		KeepRepoChanges: c.KeepRepoChanges,
		KeepServices:    c.KeepServices,
	}
	err := target.Del(context.Background(), DelOpts{
		DryRun:    c.DryRun,
		AssumeYes: c.AssumeYes,
	})
	if err == nil && !c.DryRun {
		// Match legacy log line for `ov deploy del vm:` — the unified
		// Del path doesn't print this because it doesn't know how many
		// layers were torn down at the call site (the runner does).
		fmt.Fprintf(os.Stderr, "Removed VM deploy %s\n", c.Name)
	}
	return err
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
// in the legacy "vm:<name>[/<instance>]" form. This form is the
// internal shape that deploy_add_cmd.go's dispatch rewrites before
// calling runVM/runVmDel — schema-v3 entries with plain identifiers
// and explicit `vm_source:` are rewritten upstream so this helper
// always receives the prefixed form. The `instance` suffix is
// preserved for future per-instance addressing but currently unused.
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
	if dc.Deploy == nil {
		dc.Deploy = map[string]DeploymentNode{}
	}

	entry, exists := dc.Deploy[deployName]
	if !exists {
		entry = DeploymentNode{}
	}
	entry.Target = "vm"
	vmName, _ := vmNameFromDeployName(deployName)
	entry.Vm = vmName
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
	delete(dc.Deploy, deployName)
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
