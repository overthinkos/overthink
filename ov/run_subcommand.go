package main

// Internal helpers for invoking child `ov` processes from within ov
// itself. Used by:
//
//   - UpdateCmd (commands.go) — dispatches to per-target update logic
//     by shelling out to ov image build / ov stop / ov config / ov start
//   - The unified-target Update/Rebuild methods (unified_targets_*.go)
//   - eval_kind_cmd.go — orchestrates per-kind R10 sequences
//   - cycle.go — ov vm cycle / etc.
//
// Extracted from the now-deleted ov/rebuild.go in the 2026-05-09
// rebuild→update cutover. Keeping the helpers in their own file makes
// the ownership explicit (they're internal subprocess plumbing, not
// part of any one verb's implementation) and lets the unified-target
// dispatch keep working without RebuildCmd.

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
)

// runOvSubcommand shells out to `ov <args…>` in the current working
// directory, inheriting stdin/stdout/stderr. Uses the same ov binary
// the caller invoked (via os.Args[0]) so update loops pick up the
// local build-under-test automatically.
func runOvSubcommand(args ...string) error {
	exe := os.Args[0]
	cmd := exec.Command(exe, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// runOvSubcommandCapture is like runOvSubcommand but captures stderr
// into a buffer instead of mirroring it to os.Stderr. The caller
// decides whether the captured text is a real error (print it) or a
// benign signal (suppress). This keeps the update output clean when
// the child's "error" is actually just "already running" or similar.
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

// captureOvStdout captures the child's stdout (instead of stderr).
// Sibling of runOvSubcommandCapture; used when the caller needs to
// parse `ov vm list` / `ov status` table output.
func captureOvStdout(args ...string) (string, error) {
	exe := os.Args[0]
	cmd := exec.Command(exe, args...)
	cmd.Stdin = os.Stdin
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	return buf.String(), err
}

// isBenignAlreadyRunning detects "already running" error text from
// the underlying VM backend. During an update, `ov vm create` may boot
// the VM as part of its libvirt-config-injection sequence (or, for
// QEMU-direct, may auto-start at the end of create); a subsequent
// `ov vm start` then fails. That's the end state we want — treat it
// as success.
//
// Two backend dialects to match:
//   - libvirt: "domain is already running" / "operation is not valid"
//   - qemu-direct: "Cannot lock pid file: Resource temporarily unavailable"
//     (the second qemu-system-x86_64 invocation can't acquire the same
//     pid-file lock the first one holds → effectively "already running")
func isBenignAlreadyRunning(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "already running") ||
		strings.Contains(s, "operation is not valid") ||
		strings.Contains(s, "cannot lock pid file")
}

// vmDisposableFromDeployments returns the disposability + lifecycle
// tag for a kind:vm entity by searching the deployments tree for
// entries with target:vm pointing at vmName via vm_source:. Disposable
// is true iff any matching deployment sets it; lifecycle is the first
// non-empty tag encountered (stable iteration via map access is not
// guaranteed, but for the common one-deploy-per-vm case this is
// unambiguous).
//
// Pre-2026-05-09: lived in rebuild.go and gated `ov rebuild`. Post-cutover:
// retained because vm_classification.go + ov vm cycle still consult it
// to surface lifecycle metadata in operator-facing output. The
// disposability-as-authorization concept itself is gone (ov update is
// non-destructive by design and doesn't need a gate).
func vmDisposableFromDeployments(tree map[string]DeploymentNode, vmName string) (disposable bool, lifecycle string) {
	for _, node := range tree {
		if (node.Target == "vm" || node.Target == "") && node.Vm == vmName {
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
