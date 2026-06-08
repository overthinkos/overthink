package main

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
)

// PreemptCmd is the operator-facing surface over the resource arbiter
// (ov/preempt.go): inspect active exclusive-resource leases and recover
// holders left stopped by a crashed claim.
type PreemptCmd struct {
	Status  PreemptStatusCmd  `cmd:"" help:"Show active resource-arbitration leases (which preemptible holders are stopped for which claimant) and flag stranded ones"`
	Restore PreemptRestoreCmd `cmd:"" help:"Restore preempted holders — no argument reconciles every stranded lease; a claimant argument force-releases that lease and restarts its holders"`
}

// PreemptStatusCmd prints the active leases. A lease is STRANDED when its
// claimant is no longer running (a crashed claim) — its holders are still
// stopped and want recovering.
type PreemptStatusCmd struct{}

func (c *PreemptStatusCmd) Run() error {
	a := newResourceArbiter()
	ledger, stranded, err := a.Status()
	if err != nil {
		return err
	}
	if len(ledger.Leases) == 0 {
		fmt.Println("No active preemption leases.")
		return nil
	}
	strandedSet := map[string]bool{}
	for _, s := range stranded {
		strandedSet[s] = true
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
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

// PreemptRestoreCmd recovers preempted holders. With no argument it reconciles
// every stranded lease (claimant gone). With a claimant argument it
// force-releases that specific lease and restarts its holders regardless of
// the `restore:` policy (the operator is explicitly recovering).
type PreemptRestoreCmd struct {
	Claimant string `arg:"" optional:"" help:"Claimant whose lease to force-restore; omit to reconcile ALL stranded leases"`
}

func (c *PreemptRestoreCmd) Run() error {
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
