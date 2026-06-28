package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/alecthomas/kong"
)

// command.go is the command:preempt leg of this plugin — the externalized `charly preempt …`
// CLI, ported OUT of charly's core (the deleted charly/preempt_cmd.go +
// charly/plugin_command_preempt.go) so the operator-facing preempt command no longer links
// into the core binary. It owns the ENTIRE `charly preempt` grammar verbatim from the former
// core command (status / restore) — the leaf structs parse exactly as before; only each
// leaf's RUN body changed: it no longer constructs the in-core ResourceArbiter directly (that
// 1225-LOC arbiter STAYS core — it is shared by `charly vm create`, `charly vm gpu`, and the
// check-bed runner, so it cannot move, R3). Instead it SHELLS BACK through two NEW HIDDEN core
// commands that expose the in-core arbiter (the SAME `charly __cli-model` /
// `charly __plugin-providers` internal-command pattern, the CLI being the only operational
// interface, R4):
//
//   - `charly preempt status`            → `charly __preempt-status`
//     (newResourceArbiter().Status() + the active-lease table, printed verbatim)
//   - `charly preempt restore [claimant]` → `charly __preempt-restore [claimant]`
//     (reconcileStranded when no claimant; ReleaseClaimant(claimant, true) when given)
//
// Each shell-back syscall.Exec's the SAME charly binary that dispatched this plugin (CHARLY_BIN,
// stamped by the dispatch seam), so the in-core arbiter runs in the re-entered charly process
// and its stdout/stderr/exit-code flow back through this plugin (which IS the operator's
// `charly preempt` process) natively. No core symbol crosses the process boundary.
//
// Dispatch contract (charly/provider_command_external.go dispatchExternalCommand): on
// `charly preempt <args…>`, charly RESOLVES this plugin's binary (host-built from source, or
// baked into /usr/lib/charly/plugins via pkg/arch) and syscall.Exec's it with the pass-through
// tokens after the `preempt` word, in CLI mode (the go-plugin handshake cookie is stripped, so
// sdk.Main runs cliMain instead of serving gRPC) with CHARLY_BIN stamped to charly's own
// executable.

// cliMain is the CLI-mode entry point (sdk.Main calls it when charly fork/exec'd this plugin as
// a command passthrough). It parses the pass-through tokens against PreemptCmd and dispatches
// the selected subcommand via kctx.Run() (each leaf carries its own Run handler). Returns the
// process exit code.
func cliMain(args []string) int {
	var grp PreemptCmd
	parser, err := kong.New(&grp,
		kong.Name("preempt"),
		kong.Description("charly preempt — inspect and recover exclusive-resource preemption leases"),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "plugin-preempt: build kong parser: %v\n", err)
		return 1
	}
	kctx, err := parser.Parse(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "plugin-preempt: parse `charly preempt %v`: %v\n", args, err)
		return 1
	}
	if err := kctx.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "charly preempt: %v\n", err)
		return 1
	}
	return 0
}

// charlyBin returns the host charly binary the dispatch seam stamped into CHARLY_BIN, falling
// back to `charly` on PATH (e.g. if the plugin binary is run directly, off the dispatch path).
func charlyBin() string {
	if b := os.Getenv("CHARLY_BIN"); b != "" {
		return b
	}
	return "charly"
}

// execCharly REPLACES this process with `charly <hiddenCmd> [args…]` via syscall.Exec, so the
// re-entered charly runs the in-core arbiter and its stdout/stderr/exit-code flow back through
// this plugin (which IS the operator's `charly preempt` process) natively. On success this
// never returns; only a PRE-exec failure (binary missing) returns an error.
func execCharly(args ...string) error {
	bin, err := exec.LookPath(charlyBin())
	if err != nil {
		return fmt.Errorf("resolving charly binary %q: %w", charlyBin(), err)
	}
	argv := append([]string{"charly"}, args...)
	if err := syscall.Exec(bin, argv, os.Environ()); err != nil { //nolint:gosec // bin is CHARLY_BIN, args are fixed hidden-command tokens
		return fmt.Errorf("exec %s: %w", bin, err)
	}
	return nil // unreachable: syscall.Exec replaced the process image
}

// PreemptCmd is the operator-facing surface over the in-core resource arbiter
// (charly/preempt.go): inspect active exclusive-resource leases and recover holders left
// stopped by a crashed claim. The leaf structs are byte-identical to the former core command
// (charly/preempt_cmd.go) so `charly preempt …` parses exactly as before.
type PreemptCmd struct {
	Status  PreemptStatusCmd  `cmd:"" help:"Show active resource-arbitration leases (which preemptible holders are stopped for which claimant) and flag stranded ones"`
	Restore PreemptRestoreCmd `cmd:"" help:"Restore preempted holders — no argument reconciles every stranded lease; a claimant argument force-releases that lease and restarts its holders"`
}

// PreemptStatusCmd prints the active leases. A lease is STRANDED when its claimant is no
// longer running (a crashed claim) — its holders are still stopped and want recovering. The
// arbiter read happens in core via the `charly __preempt-status` shell-back.
type PreemptStatusCmd struct{}

func (c *PreemptStatusCmd) Run() error {
	return execCharly("__preempt-status")
}

// PreemptRestoreCmd recovers preempted holders. With no argument it reconciles every stranded
// lease (claimant gone). With a claimant argument it force-releases that specific lease and
// restarts its holders regardless of the `restore:` policy (the operator is explicitly
// recovering). The arbiter mutation happens in core via the `charly __preempt-restore`
// shell-back.
type PreemptRestoreCmd struct {
	Claimant string `arg:"" optional:"" help:"Claimant whose lease to force-restore; omit to reconcile ALL stranded leases"`
}

func (c *PreemptRestoreCmd) Run() error {
	if c.Claimant == "" {
		return execCharly("__preempt-restore")
	}
	return execCharly("__preempt-restore", c.Claimant)
}
