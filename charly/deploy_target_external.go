package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/overthinkos/overthink/charly/spec"
)

// externalDeployTarget is the UnifiedDeployTarget adapter for an OUT-OF-PROCESS deploy
// provider (a grpcProvider whose class is deploy but which is Invoke-only, not a typed
// DeployTargetProvider). It is the E3-deploy consumer of the E3b reverse channel: the
// full external deploy LIFECYCLE rides over the already-built ExecutorService reverse
// channel —
//
//   - Add Invokes the provider (OpExecute) with the deployment's InstallPlans in
//     op.Params and a venue descriptor in op.Env, the host's executor served on the
//     go-plugin broker (grpcProvider.InvokeWithExecutor) so the plugin runs the
//     deployment's shell/SSH ops on the real venue it cannot hold across the process
//     boundary, then DECODES the structured DeployReply and writes its ReverseOps +
//     record into the ledger via the SAME install_ledger.go path a built-in Add uses;
//   - Test runs the deploy-scope checks HOST-SIDE (the plugin is not involved — the
//     checks are in-proc CheckVerbProviders run against the executor);
//   - Update re-Invokes OpExecute with fresh plans (idempotent re-Add);
//   - Del replays the RECORDED ReverseOps from the ledger (no plugin call) — the
//     record-and-replay invariant: only recorded ops are reversed, never recomputed.
//
// Built-in deploy targets (local/vm/pod/k8s/android) use their typed ResolveTarget
// path instead.
type externalDeployTarget struct {
	name string
	prov *grpcProvider
	exec DeployExecutor

	// paths is the ledger root for this deploy's records. nil →
	// DefaultLedgerPaths() (the operator ledger). Tests redirect it to a temp dir.
	paths *LedgerPaths

	// revRunner is the ReverseRunner Del hands to runReverseOps. nil → reverse_ops
	// falls back to local exec.Command (sudo bash / bash) — the host-teardown path,
	// matching the executor (ShellExecutor) ResolveTarget gives an external deploy.
	// Tests inject a no-sudo runner.
	revRunner ReverseRunner

	// KeepRepoChanges / KeepServices are the `charly bundle del --keep-…` teardown gates,
	// set by the del-command dispatcher (bundle_add_cmd.go) for the externalized local
	// substrate, then handed to teardownHostDeploy's ReverseExecutor in Del.
	KeepRepoChanges bool
	KeepServices    bool

	// build is the host-ENGINE context (project Config + dir + DistroCfg) the RunHostStep
	// reverse leg needs when the plugin walks a plan carrying a host-engine step kind
	// (Builder / LocalPkgInstall resolve a short / namespace-qualified builder image and
	// fall back to a local `charly box build`; SystemPackages renders the format's
	// phase.install.host template from DistroCfg). Populated by Add from the DeployContext;
	// the zero value (no project context) is fine for a deploy whose plan has no host-engine step.
	build buildEngineContext
}

func (t *externalDeployTarget) Name() string             { return t.name }
func (t *externalDeployTarget) Kind() string             { return "host" } // ops run on the host venue via the reverse channel
func (t *externalDeployTarget) Executor() DeployExecutor { return t.exec }

func (t *externalDeployTarget) ledgerPaths() (*LedgerPaths, error) {
	if t.paths != nil {
		return t.paths, nil
	}
	return DefaultLedgerPaths()
}

// deployID is the deterministic ledger key for this external deploy — derived
// from the deploy name (no image / candy set), so Add and Del agree on which
// record to write / read without scanning the ledger by Target (the external
// target's Kind()=="host" would otherwise collide with a real host deploy in
// the local deploy target.Del's `Target=="host"` scan).
func (t *externalDeployTarget) deployID() string {
	return computeDeployID(t.name, nil, nil)
}

// Add applies the deployment via the out-of-process provider, then records the returned
// teardown ops + provenance into the ledger. For a deployment carrying candies that declare
// secrets / artifacts (now reached by ANY external substrate — local most of all), it does
// the host-side secret injection BEFORE the wire payload is projected, and the candy-artifact
// retrieval + --verify AFTER the plugin applied — the SAME shared helpers the in-proc
// local/vm targets use (R3). All are no-ops for a deployment whose candies declare neither
// (the android/k8s/example substrates), so the path stays generic.
func (t *externalDeployTarget) Add(ctx context.Context, dctx *DeployContext, plans []*InstallPlan, opts EmitOpts) error {
	var node *BundleNode
	var dir string
	if dctx != nil {
		node = dctx.Node
		dir = dctx.Dir
		// Capture the host-engine context so the RunHostStep leg can resolve a builder
		// image + run the host build, and render a SystemPackages step's host install
		// template from DistroCfg, when the plugin walks a host-engine step.
		t.build = buildEngineContext{Cfg: dctx.Cfg, ProjectDir: dctx.Dir, DistroCfg: dctx.DistroCfg}
	}

	// Host-side secret injection BEFORE wireView (Part 3): resolve candy secret_requires /
	// secret_accepts and inject them into each OpStep's env so the projected views carry the
	// resolved values (the plugin runs the steps with secrets already present). The SAME
	// prepareCandySecrets the in-proc local/vm Add uses (R3). candyList feeds artifact
	// retrieval below; secretEnv feeds artifact-path substitution.
	var candyList []*Candy
	var secretEnv map[string]string
	if dir != "" {
		var serr error
		candyList, secretEnv, serr = prepareCandySecrets(plans, dir)
		if serr != nil {
			return fmt.Errorf("external deploy %q: loading candies for secret resolution: %w", t.name, serr)
		}
	}

	if err := t.apply(ctx, node, dir, plans, opts.DryRun); err != nil {
		return err
	}
	if opts.DryRun {
		return nil
	}

	// Retrieve candy artifacts (+ k3s post-hook) host-side — guarded no-op when no candy
	// declares an artifact: block. artifactEnv = secretEnv overlaid with the node's env:.
	artifactEnv := buildArtifactEnv(secretEnv, node)
	if err := retrieveArtifactsAndK3s(ctx, t.exec, candyList, t.name, artifactEnv, opts); err != nil {
		return fmt.Errorf("external deploy %q: retrieving candy artifacts: %w", t.name, err)
	}

	// --verify: run the deployment's deploy-scope check probes on the venue we just deployed
	// to. Default (Verify=false) is a no-op. Reuses checkLocalDeployScope so external local
	// `--verify` sources + runs probes identically to `charly check live` (R3).
	if opts.Verify {
		fails, verr := checkLocalDeployScope(dir, node, t.name, "", "", nil, t.exec, "text")
		if verr != nil {
			return fmt.Errorf("external deploy %q: --verify: %w", t.name, verr)
		}
		if fails > 0 {
			return fmt.Errorf("external deploy %q: --verify: %d deploy-scope check(s) failed", t.name, fails)
		}
	}
	return nil
}

// Update re-applies the deployment over the wire — an idempotent re-Add (mirrors
// the local deploy target.Update's re-Emit). The unified Update signature carries no
// DeployContext, so the venue descriptor carries only the deploy name; a substrate
// preresolver (if any) re-resolves the node from the tree by name.
func (t *externalDeployTarget) Update(ctx context.Context, plans []*InstallPlan, opts UpdateOpts) error {
	return t.apply(ctx, nil, "", plans, opts.DryRun)
}

// apply is the shared Add/Update body: marshal the plans + venue (with any
// substrate-specific preresolved payload), Invoke the provider with the host
// executor on the broker, decode the reply, and (unless DryRun) persist the
// teardown ops + record to the ledger.
func (t *externalDeployTarget) apply(ctx context.Context, node *BundleNode, dir string, plans []*InstallPlan, dryRun bool) error {
	// Fork-A host-side pre-pass (live only): resolve {{.Home}} against the venue home and
	// capture the deploy-time-stateful reverse state (ShellHook.EnvFile, ServicePackaged
	// .PriorEnabled) on the LIVE venue BEFORE projecting the views, so each step's
	// host-computed Reverse() (carried in InstallStepView.ReverseOps) is faithful. The
	// plugin ECHOES those ops; the Reverse() rule stays ONCE in package main (R3). A
	// dry-run has no live venue, so it skips this (no teardown is recorded on a dry-run).
	if !dryRun {
		if err := t.prepareReverseState(ctx, plans); err != nil {
			return fmt.Errorf("external deploy %q: %w", t.name, err)
		}
	}
	views := make([]spec.InstallPlanView, 0, len(plans))
	for _, p := range plans {
		if p != nil {
			views = append(views, p.wireView())
		}
	}
	params, err := json.Marshal(views)
	if err != nil {
		return fmt.Errorf("external deploy %q: marshal plans: %w", t.name, err)
	}
	// Venue descriptor: the deploy name + the merged deploy-node env (reusing the
	// shared buildArtifactEnv flattener, R3). The plugin reads it to locate where
	// to apply its effects on the venue.
	venue := spec.DeployVenue{DeployName: t.name, Env: buildArtifactEnv(nil, node)}
	// Substrate preresolution (F1): a registered host-side preresolver (e.g. the
	// android one — resolve the live device endpoint + collect the apk install specs)
	// produces the substrate-specific payload the plugin needs but cannot resolve
	// itself. Skipped on a dry-run (it requires a LIVE venue — engine inspect on the
	// running pod). The generic target never branches on the substrate; only the
	// registered preresolver body is substrate-specific.
	if !dryRun {
		if pre, ok := deployPreresolverFor(t.prov.word); ok {
			payload, perr := pre(t.name, dir, node, plans)
			if perr != nil {
				return fmt.Errorf("external deploy %q: preresolve substrate %q: %w", t.name, t.prov.word, perr)
			}
			venue.Substrate = payload
		}
	}
	envJSON, err := json.Marshal(venue)
	if err != nil {
		return fmt.Errorf("external deploy %q: marshal venue: %w", t.name, err)
	}
	if dryRun {
		// A dry-run does NOT Invoke the provider — Invoke IS the apply (the plugin
		// runs ops on the venue via the reverse channel), so a no-side-effect
		// dry-run stops here after validating the wire payload marshalled.
		fmt.Printf("[dry-run] external deploy %s (target=%s): would apply %d plan(s) via the reverse channel\n",
			t.name, t.prov.word, len(views))
		return nil
	}
	res, err := t.prov.InvokeWithExecutor(ctx,
		&Operation{Reserved: t.prov.word, Op: OpExecute, Params: params, Env: envJSON}, t.exec, t.build)
	if err != nil {
		return err
	}
	var reply spec.DeployReply
	if res != nil && len(res.JSON) > 0 {
		if err := json.Unmarshal(res.JSON, &reply); err != nil {
			return fmt.Errorf("external deploy %q: decode reply: %w", t.name, err)
		}
	}
	return t.recordDeploy(reply)
}

// prepareReverseState is the Fork-A host-side pre-pass: it resolves the venue home and
// captures the two deploy-time-stateful reverse inputs on the LIVE venue so the
// host-computed step.Reverse() (carried in InstallStepView.ReverseOps for the plugin to
// echo) is faithful:
//
//   - ShellHookStep.EnvFile: the env.d path the plugin will write into, derived from the
//     resolved venue home — so the recorded teardown op (ReverseOpRemoveEnvdFile) targets
//     the right file. plan.ResolveHome also expands {{.Home}} in EnvVars / PathAdd /
//     ShellSnippet Destination / FileStep.Dest so the plugin receives ABSOLUTE paths.
//   - ServicePackagedStep.PriorEnabled: probed via `systemctl is-enabled` on the venue, so
//     teardown re-enables a unit that was already enabled before the deploy.
//
// Idempotent + harmless for substrates whose plans carry no such steps (android/k8s):
// ResolveHome is a no-op without {{.Home}} tokens and the switch matches nothing.
func (t *externalDeployTarget) prepareReverseState(ctx context.Context, plans []*InstallPlan) error {
	home, err := t.exec.ResolveHome(ctx, "")
	if err != nil {
		return fmt.Errorf("resolve venue home: %w", err)
	}
	for _, p := range plans {
		if p == nil {
			continue
		}
		if home != "" {
			p.ResolveHome(home)
		}
		for _, step := range p.Steps {
			switch s := step.(type) {
			case *ShellHookStep:
				if s.EnvFile == "" && home != "" {
					s.EnvFile = EnvdFilePath(home, s.CandyName)
				}
			case *ServicePackagedStep:
				s.PriorEnabled = venueUnitEnabled(ctx, t.exec, s.Unit, s.TargetScope)
			}
		}
	}
	return nil
}

// venueUnitEnabled reports whether a systemd unit is enabled on the venue — the
// executor-backed analogue of systemctlIsEnabled (which uses local exec.Command). Used by
// prepareReverseState to capture ServicePackaged.PriorEnabled before the plugin enables the
// unit, so teardown restores the prior state. A probe failure (executor error / non-zero
// exit) reports "not enabled" (the safe default — teardown then disables, never spuriously
// re-enables).
func venueUnitEnabled(ctx context.Context, exec DeployExecutor, unit string, scope Scope) bool {
	cmd := "systemctl is-enabled --quiet " + shQuoteArg(unit)
	if scope == ScopeUser {
		cmd = "systemctl --user is-enabled --quiet " + shQuoteArg(unit)
	}
	_, _, exit, err := exec.RunCapture(ctx, cmd)
	return err == nil && exit == 0
}

// recordDeploy persists the external deploy's teardown ops + provenance into the
// ledger via the SAME install_ledger.go path a built-in Add uses: one CandyRecord
// carrying the ReverseOps, plus a DeployRecord keyed on deployID() that names the
// candy. Idempotent across re-applies (Update): the candy's ReverseOps are
// REPLACED, not appended, so a re-apply never doubles the teardown.
func (t *externalDeployTarget) recordDeploy(reply spec.DeployReply) error {
	paths, err := t.ledgerPaths()
	if err != nil {
		return err
	}
	candy := reply.Record.Candy
	if candy == "" {
		candy = t.name // fall back to the deploy name so teardown always has a key
	}
	id := t.deployID()
	reverseOps := reply.ReverseOps
	if err := AddCandyDeployment(paths, candy, id, func(rec *CandyRecord) {
		rec.Version = reply.Record.Version
		rec.ReverseOps = append([]ReverseOp(nil), reverseOps...) // replace (idempotent)
	}); err != nil {
		return fmt.Errorf("external deploy %q: record candy: %w", t.name, err)
	}
	return WriteDeployRecord(paths, &DeployRecord{
		DeployID:   id,
		Image:      t.name,
		Target:     t.prov.word, // the deploy WORD (e.g. "exampledeploy") — NOT "host"
		Candy:      []string{candy},
		DeployedAt: time.Now().UTC().Format(time.RFC3339),
	})
}

// Test runs the deploy-scope checks against the host venue. The plugin is NOT
// involved — the checks are in-proc CheckVerbProviders run against t.exec, the
// SAME runUnifiedTargetChecks path the host/pod/vm targets use (R3).
func (t *externalDeployTarget) Test(ctx context.Context, checks []Op, opts TestOpts) error {
	return runUnifiedTargetChecks(ctx, t.exec, t.Kind(), t.name, checks, opts)
}

// Del replays the RECORDED ReverseOps for this deploy (no plugin call): reads the
// ledger record by deployID() and reverses it via teardownHostDeploy — the SAME
// host-teardown helper the local deploy target.Del uses (R3). Only recorded ops are
// replayed, never recomputed from a manifest.
func (t *externalDeployTarget) Del(ctx context.Context, opts DelOpts) error {
	paths, err := t.ledgerPaths()
	if err != nil {
		return err
	}
	rec, err := ReadDeployRecord(paths, t.deployID())
	if err != nil {
		return err
	}
	if rec == nil {
		return nil // nothing recorded — idempotent teardown
	}
	if opts.DryRun {
		fmt.Printf("[dry-run] would tear down external deploy %s (target=%s, %d candies)\n",
			rec.DeployID, rec.Target, len(rec.Candy))
		return nil
	}
	re := &hostReverseExec{
		DryRun:          opts.DryRun,
		KeepRepoChanges: t.KeepRepoChanges,
		KeepServices:    t.KeepServices,
		Runner:          t.revRunner,
	}
	if err := teardownHostDeploy(paths, rec, os.Getenv("HOME"), re); err != nil {
		return err
	}
	fmt.Printf("Removed external deploy %s (%s)\n", rec.DeployID, rec.Target)
	return nil
}

// ErrNotSupportedOnExternal is returned by lifecycle methods that have no meaning
// for an external (out-of-process) deploy target. Like the host target it runs on
// the host venue with no separate runtime to start/stop or journal to stream;
// mirrors ErrNotSupportedOnHost.
var ErrNotSupportedOnExternal = errors.New("lifecycle operation not supported on external deploy target")

// Rebuild re-applies the external deployment (refresh semantics, mirroring
// the local deploy target.Rebuild): it re-runs `charly bundle add <name>` as a
// subprocess, which reconnects the plugin and re-Invokes the provider — an
// idempotent re-apply. This is the path `charly update <name>` (the bed's
// fresh-rebuild R10 gate) takes. The Disposable gate is checked by the caller's
// classification logic, so this method does not re-validate.
func (t *externalDeployTarget) Rebuild(ctx context.Context, opts RebuildOpts) error {
	if opts.DryRun {
		fmt.Printf("dry-run: charly bundle add %s\n", t.name)
		return nil
	}
	return runCharlySubcommand("bundle", "add", t.name)
}

// Status reports the external deploy's presence from the ledger: "running" when a
// deploy record exists, "stopped" otherwise (the host venue itself is always up;
// charly-managed presence is the signal). Mirrors the local deploy target.Status.
func (t *externalDeployTarget) Status(ctx context.Context) (StatusInfo, error) {
	paths, err := t.ledgerPaths()
	if err != nil {
		return StatusInfo{}, err
	}
	rec, err := ReadDeployRecord(paths, t.deployID())
	if err != nil || rec == nil {
		return StatusInfo{State: "stopped", Healthy: false}, nil
	}
	return StatusInfo{
		State:   "running",
		Healthy: true,
		Details: map[string]string{"target": rec.Target, "candies": fmt.Sprintf("%d", len(rec.Candy))},
	}, nil
}

// Start, Stop, Logs, Shell: not applicable to an external deploy target (host
// venue, no separate runtime / journal we own). Mirror the host-target pattern.
func (t *externalDeployTarget) Start(ctx context.Context) error {
	return fmt.Errorf("external deploy %q: %w", t.name, ErrNotSupportedOnExternal)
}
func (t *externalDeployTarget) Stop(ctx context.Context) error {
	return fmt.Errorf("external deploy %q: %w", t.name, ErrNotSupportedOnExternal)
}
func (t *externalDeployTarget) Logs(ctx context.Context, opts LogsOpts) error {
	return fmt.Errorf("external deploy %q: %w", t.name, ErrNotSupportedOnExternal)
}
func (t *externalDeployTarget) Shell(ctx context.Context, cmd []string) error {
	return fmt.Errorf("external deploy %q: %w", t.name, ErrNotSupportedOnExternal)
}
