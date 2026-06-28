package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
)

// preempt_internal_cmd.go implements the two HIDDEN core commands that expose the in-core
// resource arbiter (preempt.go, ResourceArbiter — which STAYS core: it is shared by
// `charly vm create`, `charly vm gpu`, and the check-bed runner, so it cannot move, R3) to the
// externalized `charly preempt …` COMMAND plugin (candy/plugin-preempt). The plugin
// re-expresses each operator-facing `charly preempt` leaf as a shell-back through these
// sanctioned hidden verbs — the SAME `charly __cli-model` / `charly __plugin-providers`
// internal-command pattern — so the `charly preempt status` / `charly preempt restore` CLI is
// unchanged while the command implementation moved OUT of the core binary. The arbiter logic is
// NOT duplicated: it stays in preempt.go and is invoked ONLY here.

// PreemptStatusInternalCmd: `charly __preempt-status` (hidden machinery). Prints the active
// resource-arbitration leases exactly as the former `charly preempt status` did.
type PreemptStatusInternalCmd struct{}

func (PreemptStatusInternalCmd) Run() error {
	return renderPreemptStatus(newResourceArbiter(), os.Stdout)
}

// renderPreemptStatus loads the arbiter's lease ledger, flags stranded leases (claimant gone),
// and prints the active-lease table to out. Split from the command Run so a unit test can drive
// it with a temp-ledger arbiter (the arbiter read is the only side effect).
func renderPreemptStatus(a *ResourceArbiter, out io.Writer) error {
	ledger, stranded, err := a.Status()
	if err != nil {
		return err
	}
	if len(ledger.Leases) == 0 {
		fmt.Fprintln(out, "No active preemption leases.")
		return nil
	}
	strandedSet := map[string]bool{}
	for _, s := range stranded {
		strandedSet[s] = true
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "CLAIMANT\tTOKENS\tTRANSIENT\tPREEMPTED HOLDERS\tCREATED\tSTATE")
	for _, lz := range ledger.Leases {
		holders := make([]string, 0, len(lz.Preempted))
		for _, ph := range lz.Preempted {
			holders = append(holders, ph.Addr.Name)
		}
		hs := strings.Join(holders, ",")
		if hs == "" {
			hs = "-"
		}
		state := "active"
		if strandedSet[lz.Claimant] {
			state = "STRANDED — run `charly preempt restore`"
		}
		fmt.Fprintf(tw, "%s\t%s\t%t\t%s\t%s\t%s\n",
			lz.Claimant, strings.Join(lz.Tokens, ","), lz.Transient, hs, lz.Created, state)
	}
	return tw.Flush()
}

// PreemptRestoreInternalCmd: `charly __preempt-restore [claimant]` (hidden machinery). Recovers
// preempted holders exactly as the former `charly preempt restore` did. With no argument it
// reconciles every stranded lease (claimant gone); with a claimant argument it force-releases
// that lease and restarts its holders regardless of the `restore:` policy.
type PreemptRestoreInternalCmd struct {
	Claimant string `arg:"" optional:"" help:"Claimant whose lease to force-restore; omit to reconcile ALL stranded leases"`
}

func (c *PreemptRestoreInternalCmd) Run() error {
	a := newResourceArbiter()
	if c.Claimant == "" {
		if err := a.reconcileStranded(); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "preempt: reconciled stranded leases — holders for any departed claimant restored.")
		return nil
	}
	if err := a.ReleaseClaimant(c.Claimant, true); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "preempt: released lease for %q and restored its holders.\n", c.Claimant)
	return nil
}
