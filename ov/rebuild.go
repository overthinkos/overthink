package main

// `ov rebuild <name> [-i <instance>]` — autonomous destroy + rebuild
// + restart of a resource explicitly marked `disposable: true`.
// Refuses any target not carrying the flag, with a clear remediation
// hint. See /ov-dev:disposable for the schema and CLAUDE.md R10 for
// the verification-loop rules this command is designed to support.
//
// Resolution order:
//   1. vms.yml kind:vm entity (name matches a VMs entry)
//   2. deploy.yml deploys entry (for container deploys)
//   3. neither — error
//
// For VMs: destroys the domain (disk preserved), runs `ov vm build`
// if --rebuild-image, then `ov vm create` + `ov vm start`. Final
// state must be `running (booted)`.
//
// For container deploys: runs `ov remove` to tear down the quadlet,
// optionally `ov image build` when --rebuild-image is set, then
// `ov deploy add` + `ov start`.
//
// The entire flow is idempotent: re-running `ov rebuild` on a clean
// disposable target is expected to succeed without side effects.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// RebuildCmd implements `ov rebuild`.
//
// By default, `ov rebuild` actually rebuilds the underlying image (not
// just the runtime artifacts). This matches the command's name: a
// rebuild that silently reused a stale image would be a refresh, not a
// rebuild. For the faster "reuse existing image" path, pass
// `--reuse-image`.
type RebuildCmd struct {
	Name       string `arg:"" help:"Deploy or VM name (vms.yml entity or deploy.yml entry)"`
	Instance   string `short:"i" long:"instance" help:"Instance name (for multi-instance VMs/deploys)"`
	DryRun     bool   `long:"dry-run" help:"Print the rebuild sequence without executing"`
	ReuseImage bool   `long:"reuse-image" help:"Skip the underlying image build and reuse the currently-tagged one (faster; risks running on a stale image)"`
}

// Run executes the rebuild orchestration.
func (c *RebuildCmd) Run() error {
	// Resolve the target kind (VM vs container deploy).
	kind, disposable, lifecycle, err := c.resolve()
	if err != nil {
		return err
	}

	// Enforce the disposable gate. This is the ONE authorization
	// check — no derivation, no fallback, no hostname heuristic.
	if !disposable {
		return c.refuseMessage(kind, lifecycle)
	}

	start := time.Now()
	switch kind {
	case "vm":
		if err := c.rebuildVm(); err != nil {
			return err
		}
	case "deploy":
		target := c.deployTarget()
		switch target {
		case "vm":
			if err := c.rebuildVmDeploy(); err != nil {
				return err
			}
		case "host":
			if err := c.rebuildHostDeploy(); err != nil {
				return err
			}
		default: // "pod", "container", "" (legacy), "k8s"
			if err := c.rebuildContainerDeploy(); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unreachable: kind=%q", kind)
	}

	if c.DryRun {
		fmt.Printf("dry-run: would rebuild %s\n", c.Name)
	} else {
		fmt.Printf("rebuilt %s in %s — state: running\n", c.Name, time.Since(start).Round(time.Second))
	}
	return nil
}

// resolve looks up the target in overthink.yml's `vm:` section
// (kind=vm) or the deployments tree (kind=deploy). Returns the
// effective classification fields so the caller can gate on disposable.
//
// Accepts a dotted-path Name for nested deployments
// (`stack.web.db`). Disposability is checked on the targeted node
// ONLY — a parent's disposable: true does NOT cascade (per CLAUDE.md
// R10). This lets operators mark an inner leaf as rebuildable while
// the parent container/VM remains protected, and vice versa.
//
// Rebuild of a non-root node is currently only fully supported when
// the node's target is "host" (applying layers in parent's venue).
// For container/vm children a clear error points at the equivalent
// workflow ("destroy + recreate the parent + redeploy the whole
// subtree").
func (c *RebuildCmd) resolve() (kind string, disposable bool, lifecycle string, err error) {
	dir, derr := os.Getwd()
	if derr != nil {
		return "", false, "", fmt.Errorf("getwd: %w", derr)
	}

	uf, ok, _ := LoadUnified(dir)
	tree, _ := resolveTreeRoot(dir)

	// VM-entity lookup — `ov rebuild arch` names a kind:vm entity.
	// Per /ov-dev:disposable, disposability is a DEPLOY property, NOT
	// an image/spec property. Authorization reads from the
	// DeploymentNode(s) that reference this VM entity via `vm_source:`,
	// NOT from VmSpec itself. When multiple deployments reference the
	// same VM, disposability is authorized iff at least one of them
	// carries disposable: true (the presence of ANY disposable-flagged
	// deployment makes the VM rebuildable on that operator's behalf).
	if ok && uf != nil && uf.VM != nil {
		if _, present := uf.VM[c.Name]; present {
			d, life := vmDisposableFromDeployments(tree, c.Name)
			// Per-instance override (2026-05): lets an operator
			// flip disposable/lifecycle for their specific libvirt
			// domain without editing deploy.yml. Domain name is
			// "ov-<vmEntityName>" (matches what ov vm create writes
			// to ~/.local/share/ov/vm/<domain>/). Silent miss →
			// keep upstream classification.
			if ov, _ := LoadVmInstanceOverride("ov-" + c.Name); ov != nil {
				d, life = ov.ApplyToVmClassification(d, life)
			}
			return "vm", d, life, nil
		}
	}

	// Deployments tree — accept dotted paths.
	if tree != nil {
		if node, _, nodeErr := ResolveNodePath(tree, c.Name); nodeErr == nil && node != nil {
			// 2026-05: dropped the early-fail for nested container/vm
			// rebuilds. Nested HOST already worked via fall-through
			// (rebuildHostDeploy invokes `ov deploy add <dotted-name>`,
			// which is dotted-path-aware). Nested CONTAINER/VM children
			// fall through to rebuildContainerDeploy / rebuildVmDeploy
			// — those paths may not yet be fully nested-aware, but
			// returning the dispatcher's downstream error is more
			// actionable than the prior preemptive "not yet supported"
			// rejection. Operators who want a clean parent-rebuild can
			// still invoke `ov rebuild <parent>` for the same effect.
			return "deploy", node.IsDisposable(), node.LifecycleTag(), nil
		}
	}

	return "", false, "", fmt.Errorf("ov rebuild: %q is neither a kind:vm entity nor a deployments entry in this project", c.Name)
}

// vmDisposableFromDeployments returns the disposability + lifecycle
// tag for a kind:vm entity by searching the deployments tree for
// entries with target:vm pointing at vmName via vm_source:. Disposable
// is true iff any matching deployment sets it; lifecycle is the first
// non-empty tag encountered (stable iteration via map access is not
// guaranteed, but for the common one-deploy-per-vm case this is
// unambiguous).
func vmDisposableFromDeployments(tree map[string]DeploymentNode, vmName string) (disposable bool, lifecycle string) {
	for _, node := range tree {
		if (node.Target == "vm" || node.Target == "") && node.Vm == vmName {
			// IsDisposable() honors the load-bearing implication
			// `ephemeral: ... ⇒ disposable: true` so an ephemeral
			// deploy authorizes rebuild even without explicit
			// `disposable: true`.
			if node.IsDisposable() {
				disposable = true
			}
			if lifecycle == "" {
				lifecycle = node.Lifecycle
			}
		}
	}
	return disposable, lifecycle
}

// refuseMessage returns the explicit refusal error with remediation.
// Cites the current lifecycle (if any) purely as context — lifecycle
// has no effect on disposability, so the remediation is always the
// same: set `disposable: true` explicitly.
func (c *RebuildCmd) refuseMessage(kind, lifecycle string) error {
	tag := lifecycle
	if tag == "" {
		tag = "(unset)"
	}
	switch kind {
	case "vm":
		return fmt.Errorf(
			"ov rebuild: %q is not marked `disposable: true` in vms.yml (current lifecycle: %s).\n"+
				"  `ov rebuild` only acts on explicitly disposable targets — lifecycle tags\n"+
				"  alone do NOT authorize autonomous destroy.\n"+
				"  To opt in: edit vms.yml and set `disposable: true` on the %q entry.",
			c.Name, tag, c.Name)
	case "deploy":
		return fmt.Errorf(
			"ov rebuild: %q is not marked `disposable: true` in deploy.yml (current lifecycle: %s).\n"+
				"  `ov rebuild` only acts on explicitly disposable deploys — lifecycle tags\n"+
				"  alone do NOT authorize autonomous destroy.\n"+
				"  To opt in: edit deploy.yml and add `disposable: true` to the entry,\n"+
				"  or run: ov deploy add %s <ref> --disposable",
			c.Name, tag, c.Name)
	}
	return fmt.Errorf("ov rebuild: %q is not disposable", c.Name)
}

// rebuildVm: destroy → (optional build) → create → start.
func (c *RebuildCmd) rebuildVm() error {
	if c.DryRun {
		fmt.Printf("dry-run: ov vm destroy %s\n", c.Name)
		if !c.ReuseImage {
			fmt.Printf("dry-run: ov vm build %s\n", c.Name)
		}
		fmt.Printf("dry-run: ov vm create %s\n", c.Name)
		fmt.Printf("dry-run: ov vm start %s\n", c.Name)
		return nil
	}
	// Destroy is best-effort (may not exist yet).
	_ = runOvSubcommand("vm", "destroy", c.Name)
	if !c.ReuseImage {
		if err := runOvSubcommand("vm", "build", c.Name); err != nil {
			return fmt.Errorf("ov vm build %s: %w", c.Name, err)
		}
	}
	if err := runOvSubcommand("vm", "create", c.Name); err != nil {
		return fmt.Errorf("ov vm create %s: %w", c.Name, err)
	}
	// `ov vm create` may auto-start the VM via libvirt-config-injection
	// post-create. If so, `ov vm start` will fail with "already
	// running" — that's a success signal for us, not an error.
	// runOvSubcommandCapture captures stderr so we can pattern-match
	// AND suppress the output when it's benign (no scary error line
	// in the user-visible rebuild log).
	stderr, err := runOvSubcommandCapture("vm", "start", c.Name)
	if err != nil {
		if isBenignAlreadyRunning(stderr) {
			// VM was booted by the create path — that's the desired
			// end state for rebuild. Silently accept.
			return nil
		}
		// Real error: print the captured stderr then return.
		fmt.Fprint(os.Stderr, stderr)
		return fmt.Errorf("ov vm start %s: %w", c.Name, err)
	}
	// Non-error path: mirror captured stderr (if any) so diagnostic
	// messages aren't lost.
	if stderr != "" {
		fmt.Fprint(os.Stderr, stderr)
	}
	return nil
}

// rebuildContainerDeploy follows a build → build-test → stop → start
// cycle so the running container only gets disrupted AFTER the new
// artifact is known good. If any earlier step fails, the previous
// container keeps running untouched.
//
// Sequence:
//  1. (unless --reuse-image) ov image build <base>   [build]
//  2. (unless --reuse-image) ov eval image <base>    [build-scope eval: disposable container]
//  3. ov deploy add <name>                           [compile overlay if add_layers; non-destructive]
//  4. ov stop <name>                                 [disruption window starts]
//  5. ov config <name>                               [regenerate quadlet]
//  6. ov start <name>                                [start with new image]
//
// Deploy-scope eval is NOT part of rebuild — operators run
// `ov eval live <name>` separately against the running service. That
// keeps rebuild focused (build the artifact, start it) and eval
// distinct (build-scope during rebuild; deploy-scope on demand).
//
// Uses `ov stop` — NOT `ov remove`. `ov remove` wipes the deploy.yml
// entry (ports/tunnel/volumes/env); rebuild must preserve operator
// configuration. `ov stop` only runs `systemctl --user stop` and
// leaves everything else in place.
func (c *RebuildCmd) rebuildContainerDeploy() error {
	baseRef := c.deployBaseImageRef()
	if baseRef == "" {
		// Fall back to using the deploy name itself as the ref — some
		// deploys are named after their image.
		baseRef = c.Name
	}

	if !c.DryRun {
		if err := c.precheckPortConflicts(); err != nil {
			return err
		}
	}

	if c.DryRun {
		if !c.ReuseImage {
			fmt.Printf("dry-run: ov image build %s\n", baseRef)
			fmt.Printf("dry-run: ov eval image %s\n", baseRef)
		}
		fmt.Printf("dry-run: ov deploy add %s\n", c.Name)
		fmt.Printf("dry-run: ov stop %s\n", c.Name)
		fmt.Printf("dry-run: ov config %s\n", c.Name)
		fmt.Printf("dry-run: ov start %s\n", c.Name)
		return nil
	}

	// 1-2. Rebuild the base image and run build-scope tests on it.
	// Fail fast before touching anything that might disrupt the
	// running container.
	if !c.ReuseImage {
		if err := runOvSubcommand("image", "build", baseRef); err != nil {
			return fmt.Errorf("ov image build %s: %w", baseRef, err)
		}
		if err := runOvSubcommand("eval", "image", baseRef); err != nil {
			return fmt.Errorf("ov eval image %s: %w", baseRef, err)
		}
	}

	// 3. Re-apply the deploy. For pod targets with add_layers this
	//    builds the overlay image; failures here leave the running
	//    container untouched.
	if err := runOvSubcommand("deploy", "add", c.Name); err != nil {
		return fmt.Errorf("ov deploy add %s: %w", c.Name, err)
	}

	// 4. Stop the running container (systemctl --user stop only).
	_ = runOvSubcommand("stop", c.Name)

	// 5. Regenerate the quadlet so systemd picks up any changed image
	//    or deploy.yml field on the next start.
	if err := runOvSubcommand("config", c.Name); err != nil {
		return fmt.Errorf("ov config %s: %w", c.Name, err)
	}

	// 6. Start with the new image.
	if err := runOvSubcommand("start", c.Name); err != nil {
		return fmt.Errorf("ov start %s: %w", c.Name, err)
	}
	return nil
}

// deployTarget re-reads the deploy node to return its Target field.
// Called after resolve() has already approved the rebuild; used by
// Run() to dispatch among target=vm / target=host / target=pod.
// Returns "" when the node can't be resolved (falls through to
// container path, matching legacy behaviour).
func (c *RebuildCmd) deployTarget() string {
	dir, derr := os.Getwd()
	if derr != nil {
		return ""
	}
	tree, _ := resolveTreeRoot(dir)
	if tree == nil {
		return ""
	}
	node, _, err := ResolveNodePath(tree, c.Name)
	if err != nil || node == nil {
		return ""
	}
	return node.Target
}

// deployBaseImageRef returns the image name declared on the deploy
// node (DeploymentNode.Image). For pod/k8s targets this is the base
// image the deploy runs or overlays on top of. Returns "" if the
// node has no image field set — caller must handle that case.
func (c *RebuildCmd) deployBaseImageRef() string {
	dir, derr := os.Getwd()
	if derr != nil {
		return ""
	}
	tree, _ := resolveTreeRoot(dir)
	if tree == nil {
		return ""
	}
	node, _, err := ResolveNodePath(tree, c.Name)
	if err != nil || node == nil {
		return ""
	}
	return node.Image
}

// rebuildVmDeploy handles `ov rebuild <deploy-name>` for deploys with
// target: vm. The deploy's `vm:` field points at a kind:vm entity;
// we destroy that entity's domain, optionally rebuild its image,
// recreate + start the VM, and then run `ov deploy add <deploy-name>`
// to apply the deploy's add_layers over SSH. No `ov start <deploy>`
// step — there's no quadlet to start; the SSH layer apply is the
// final state.
func (c *RebuildCmd) rebuildVmDeploy() error {
	dir, derr := os.Getwd()
	if derr != nil {
		return fmt.Errorf("getwd: %w", derr)
	}
	tree, _ := resolveTreeRoot(dir)
	node, _, err := ResolveNodePath(tree, c.Name)
	if err != nil || node == nil {
		return fmt.Errorf("ov rebuild: can't re-resolve deploy %q", c.Name)
	}
	vmName := node.Vm
	if vmName == "" {
		return fmt.Errorf("ov rebuild: deploy %q has target=vm but no `vm:` field set", c.Name)
	}

	if c.DryRun {
		fmt.Printf("dry-run: ov vm destroy %s\n", vmName)
		if !c.ReuseImage {
			fmt.Printf("dry-run: ov vm build %s\n", vmName)
		}
		fmt.Printf("dry-run: ov vm create %s\n", vmName)
		fmt.Printf("dry-run: ov vm start %s\n", vmName)
		fmt.Printf("dry-run: ov deploy add %s\n", c.Name)
		return nil
	}
	// Destroy is best-effort (may not exist yet).
	_ = runOvSubcommand("vm", "destroy", vmName)
	if !c.ReuseImage {
		if err := runOvSubcommand("vm", "build", vmName); err != nil {
			return fmt.Errorf("ov vm build %s: %w", vmName, err)
		}
	}
	if err := runOvSubcommand("vm", "create", vmName); err != nil {
		return fmt.Errorf("ov vm create %s: %w", vmName, err)
	}
	// `ov vm create` may auto-start the VM; ov vm start then reports
	// "already running", which we accept as the desired end state.
	stderr, startErr := runOvSubcommandCapture("vm", "start", vmName)
	if startErr != nil && !isBenignAlreadyRunning(stderr) {
		fmt.Fprint(os.Stderr, stderr)
		return fmt.Errorf("ov vm start %s: %w", vmName, startErr)
	}
	if stderr != "" && startErr == nil {
		fmt.Fprint(os.Stderr, stderr)
	}
	// Apply the deploy's add_layers (in-guest, over SSH).
	if err := runOvSubcommand("deploy", "add", c.Name); err != nil {
		return fmt.Errorf("ov deploy add %s: %w", c.Name, err)
	}
	return nil
}

// rebuildHostDeploy handles `ov rebuild <deploy-name>` for deploys
// with target: host (including nested dotted-path host deploys like
// `arch-vm.arch-host`). Applies layers via HostDeployTarget to the
// local FS or the nested-executor venue.
//
// `ov deploy add` is idempotent on host targets — it re-applies
// against the existing ledger without needing an explicit teardown.
// We do NOT call `ov deploy del` here: deletion would reverse repo
// changes, disable services, and strip env.d files, which the
// operator explicitly opted into. Refresh, don't destroy.
func (c *RebuildCmd) rebuildHostDeploy() error {
	target := &HostUnifiedTarget{NodeName: c.Name}
	if err := target.Rebuild(context.Background(), RebuildOpts{
		DryRun:       c.DryRun,
		RebuildImage: false,
	}); err != nil {
		return fmt.Errorf("ov deploy add %s: %w", c.Name, err)
	}
	return nil
}

// runOvSubcommand shells out to `ov <args…>` in the current working
// directory, inheriting stdin/stdout/stderr. Uses the same ov binary
// the caller invoked (via os.Args[0]) so rebuild loops pick up the
// local build-under-test automatically.
func runOvSubcommand(args ...string) error {
	exe := os.Args[0]
	cmd := exec.Command(exe, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// runOvSubcommandCapture is like runOvSubcommand but captures
// stderr into a buffer instead of mirroring it to os.Stderr. The
// caller decides whether the captured text is a real error
// (print it) or a benign signal (suppress). This keeps the
// rebuild output clean when the child's "error" is actually just
// "already running".
func runOvSubcommandCapture(args ...string) (string, error) {
	exe := os.Args[0]
	cmd := exec.Command(exe, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	var buf bytes.Buffer
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

// isBenignAlreadyRunning detects the libvirt "domain is already
// running" error text. During a rebuild, `ov vm create` may boot
// the VM as part of its libvirt-config-injection sequence; a
// subsequent `ov vm start` then fails with this exact message.
// That's the end state we want — treat it as success.
func isBenignAlreadyRunning(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "already running") ||
		strings.Contains(s, "operation is not valid")
}

// precheckPortConflicts inspects the deploy's published host ports and
// errors out before any disruption if a different container is already
// publishing the same host port. Saves the operator from a confusing
// `bind: address already in use` mid-rebuild — the destroy/rebuild has
// already happened by the time podman tries to start, leaving the
// system in a weirder state than where it started.
//
// A container with the same name as this deploy is treated as
// non-conflicting (it will be stopped at step 4 of rebuild).
func (c *RebuildCmd) precheckPortConflicts() error {
	dir, err := os.Getwd()
	if err != nil {
		return nil
	}
	tree, _ := resolveTreeRoot(dir)
	if tree == nil {
		return nil
	}
	node, _, err := ResolveNodePath(tree, c.Name)
	if err != nil || node == nil || len(node.Ports) == 0 {
		return nil
	}

	hostPorts := make(map[string]struct{}, len(node.Ports))
	for _, p := range node.Ports {
		hp := hostPortOf(p)
		if hp != "" {
			hostPorts[hp] = struct{}{}
		}
	}
	if len(hostPorts) == 0 {
		return nil
	}

	out, err := exec.Command("podman", "ps", "--format", "{{.Names}}\t{{.Ports}}").Output()
	if err != nil {
		return nil
	}

	selfNames := map[string]struct{}{
		c.Name:         {},
		"ov-" + c.Name: {},
	}

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		name, ports := parts[0], parts[1]
		if _, self := selfNames[name]; self {
			continue
		}
		for hp := range hostPorts {
			if portsListContainsHostPort(ports, hp) {
				return fmt.Errorf("port %s already published by container %q\n  Fix: ov stop %s   (or remap %s in deploy.yml)", hp, name, name, c.Name)
			}
		}
	}
	return nil
}

// hostPortOf parses a deploy.yml port entry such as "2222:22",
// "127.0.0.1:2222:22", or "5900:5900/tcp" and returns the host port.
// Returns "" for entries that don't follow the host:container shape.
func hostPortOf(spec string) string {
	s := spec
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ":")
	switch len(parts) {
	case 2:
		return parts[0]
	case 3:
		return parts[1]
	}
	return ""
}

// portsListContainsHostPort returns true when podman's ports column
// (e.g. "0.0.0.0:2222->22/tcp, 0.0.0.0:9222->9222/tcp") publishes the
// given host port.
func portsListContainsHostPort(podmanPorts, hostPort string) bool {
	needle := ":" + hostPort + "->"
	return strings.Contains(podmanPorts, needle)
}
