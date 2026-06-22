package main

import (
	"context"
	"fmt"
)

// externalDeployTarget is the UnifiedDeployTarget adapter for an OUT-OF-PROCESS deploy
// provider (a grpcProvider whose class is deploy but which is Invoke-only, not a typed
// DeployTargetProvider). The E3-deploy consumer of the E3b reverse channel: Add Invokes
// the external provider with the host's executor served on the go-plugin broker
// (grpcProvider.InvokeWithExecutor), so the plugin runs the deployment's shell/SSH ops
// on the real venue it cannot hold across the process boundary. Built-in deploy targets
// (local/vm/pod/k8s/android) use their typed ResolveTarget path instead.
//
// The example provider (candy/plugin-example-deploy) supports only the apply step, so
// Del/Test/Update are minimal here; the full external deploy lifecycle (reverse-op
// recording, live checks, plan-diff update over the wire) rides with the Phase-3
// deploy-target extraction that promotes a real built-in target to an external plugin.
type externalDeployTarget struct {
	name string
	prov *grpcProvider
	exec DeployExecutor
}

func (t *externalDeployTarget) Name() string             { return t.name }
func (t *externalDeployTarget) Kind() string             { return "host" } // ops run on the host venue via the reverse channel
func (t *externalDeployTarget) Executor() DeployExecutor { return t.exec }

// Add Invokes the external provider over the wire with the host executor served on the
// broker; the plugin dials back to run the deployment's ops on the venue.
func (t *externalDeployTarget) Add(ctx context.Context, _ *DeployContext, _ []*InstallPlan, _ EmitOpts) error {
	_, err := t.prov.InvokeWithExecutor(ctx, &Operation{Reserved: t.prov.word, Op: OpExecute}, t.exec)
	return err
}

func (t *externalDeployTarget) Del(context.Context, DelOpts) error { return nil }
func (t *externalDeployTarget) Test(_ context.Context, checks []Op, _ TestOpts) error {
	if len(checks) > 0 {
		return fmt.Errorf("external deploy target %q: live checks ride with the Phase-3 external deploy lifecycle", t.name)
	}
	return nil
}
func (t *externalDeployTarget) Update(context.Context, []*InstallPlan, UpdateOpts) error { return nil }
