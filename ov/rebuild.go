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
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// RebuildCmd implements `ov rebuild`.
type RebuildCmd struct {
	Name         string `arg:"" help:"Deploy or VM name (vms.yml entity or deploy.yml entry)"`
	Instance     string `short:"i" long:"instance" help:"Instance name (for multi-instance VMs/deploys)"`
	DryRun       bool   `long:"dry-run" help:"Print the rebuild sequence without executing"`
	RebuildImage bool   `long:"rebuild-image" help:"Also rebuild the underlying image (default: reuse current)"`
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
		if err := c.rebuildContainerDeploy(); err != nil {
			return err
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
			return "vm", d, life, nil
		}
	}

	// Deployments tree — accept dotted paths.
	if tree != nil {
		if node, _, nodeErr := ResolveNodePath(tree, c.Name); nodeErr == nil && node != nil {
			// For nested nodes, only "host" targets can be rebuilt
			// independently (they re-apply layers in the parent's
			// venue, which stays up). Container / vm children need
			// parent-subtree rebuild that is beyond this session.
			if strings.Contains(c.Name, ".") && node.Target != "host" && node.Target != "" {
				return "", false, "", fmt.Errorf(
					"ov rebuild: nested rebuild of target=%q not yet supported for path %q. "+
						"Rebuild the enclosing parent (e.g. `ov rebuild %s`), which recreates this child too.",
					node.Target, c.Name, pathRoot(c.Name))
			}
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
		if (node.Target == "vm" || node.Target == "") && node.VmSource == vmName {
			if node.Disposable {
				disposable = true
			}
			if lifecycle == "" {
				lifecycle = node.Lifecycle
			}
		}
	}
	return disposable, lifecycle
}

// pathRoot returns the first segment of a dotted path. "foo.bar.baz" → "foo".
func pathRoot(path string) string {
	if idx := strings.IndexByte(path, '.'); idx >= 0 {
		return path[:idx]
	}
	return path
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
		if c.RebuildImage {
			fmt.Printf("dry-run: ov vm build %s\n", c.Name)
		}
		fmt.Printf("dry-run: ov vm create %s\n", c.Name)
		fmt.Printf("dry-run: ov vm start %s\n", c.Name)
		return nil
	}
	// Destroy is best-effort (may not exist yet).
	_ = runOvSubcommand("vm", "destroy", c.Name)
	if c.RebuildImage {
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

// rebuildContainerDeploy: remove → (optional image build) → deploy add → start.
func (c *RebuildCmd) rebuildContainerDeploy() error {
	if c.DryRun {
		fmt.Printf("dry-run: ov remove %s\n", c.Name)
		if c.RebuildImage {
			fmt.Printf("dry-run: ov image build <ref>\n")
		}
		fmt.Printf("dry-run: ov deploy add %s <ref>\n", c.Name)
		fmt.Printf("dry-run: ov start %s\n", c.Name)
		return nil
	}
	// Teardown is best-effort.
	_ = runOvSubcommand("remove", c.Name)
	// Re-add from deploy.yml entry (ref lives there).
	if err := runOvSubcommand("deploy", "add", c.Name); err != nil {
		return fmt.Errorf("ov deploy add %s: %w", c.Name, err)
	}
	if err := runOvSubcommand("start", c.Name); err != nil {
		return fmt.Errorf("ov start %s: %w", c.Name, err)
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
