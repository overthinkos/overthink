package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestExternalDeployPlugin_ReverseChannelEndToEnd proves E3b END-TO-END on real code:
// the reference external DEPLOY plugin (candy/plugin-example-deploy, its own Go
// module) is host-built and served OUT-OF-PROCESS over go-plugin gRPC (LocalTransport,
// which carries the GRPCBroker), then invoked via grpcProvider.InvokeWithExecutor —
// the host stands up its ExecutorService on the broker, the plugin dials back through
// the SDK (ExecutorFromInvoke) and runs a marker script, and that script reaches the
// host's DeployExecutor. This is the full reverse channel an external deploy/step/
// builder plugin uses to run ops on the venue it cannot hold across the process
// boundary. Builds + execs a real binary, so it is gated behind -short exactly like
// TestExternalPluginEndToEnd.
func TestExternalDeployPlugin_ReverseChannelEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("builds + execs the external plugin binary (slow)")
	}
	ctx := context.Background()

	srcDir, err := filepath.Abs("../candy/plugin-example-deploy")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(srcDir, "go.mod")); err != nil {
		t.Fatalf("external deploy plugin module not found at %s: %v", srcDir, err)
	}

	// 1. Host-build the provider binary (the loader's buildPluginBinary step).
	bin, err := buildPluginBinary(ctx, srcDir, "plugin-example-deploy-test")
	if err != nil {
		t.Fatalf("buildPluginBinary: %v", err)
	}
	// 2. Connect OUT-OF-PROCESS via LocalTransport — the connection carries the broker.
	unit, closer, err := (&LocalTransport{BinPath: bin}).Connect(ctx)
	if err != nil {
		t.Fatalf("LocalTransport.Connect: %v", err)
	}
	defer func() { _ = closer.Close() }()

	if len(unit.Providers) != 1 || unit.Providers[0].Class() != ClassDeployTarget || unit.Providers[0].Reserved() != "exampledeploy" {
		t.Fatalf("providers = %+v, want exactly one deploy:exampledeploy", unit.Providers)
	}
	gp, ok := unit.Providers[0].(*grpcProvider)
	if !ok {
		t.Fatalf("provider is %T, want *grpcProvider (the broker-carrying out-of-proc peer)", unit.Providers[0])
	}

	// 3. Invoke WITH the reverse channel. The plugin dials back and runs a marker
	//    script; the recording executor must receive it.
	fake := &reverseFakeExec{}
	res, err := gp.InvokeWithExecutor(ctx, &Operation{Reserved: "exampledeploy", Op: OpExecute}, fake)
	if err != nil {
		t.Fatalf("InvokeWithExecutor: %v", err)
	}
	if fake.lastSystem != "exampledeploy-reverse-ran" {
		t.Fatalf("reverse channel FAILED: host executor did not receive the plugin's script, got %q", fake.lastSystem)
	}
	if res == nil || len(res.JSON) == 0 {
		t.Fatal("plugin returned no result over the wire")
	}

	// E3-deploy consumer: register the external provider, route it through ResolveTarget
	// to the externalDeployTarget adapter, and confirm the adapter's Add drives the SAME
	// reverse channel through the deploy flow (not just a direct InvokeWithExecutor).
	if err := providerRegistry.RegisterPluginProviders(unit.Providers, "e3deploy-test", closer); err != nil {
		t.Fatalf("RegisterPluginProviders: %v", err)
	}
	tgt, err := ResolveTarget(&BundleNode{Target: "exampledeploy"}, "e3deploy")
	if err != nil {
		t.Fatalf("ResolveTarget(external deploy): %v", err)
	}
	if _, ok := tgt.(*externalDeployTarget); !ok {
		t.Fatalf("ResolveTarget routed external deploy to %T, want *externalDeployTarget", tgt)
	}
	addFake := &reverseFakeExec{}
	if err := (&externalDeployTarget{name: "e3deploy", prov: gp, exec: addFake}).Add(ctx, nil, nil, EmitOpts{}); err != nil {
		t.Fatalf("externalDeployTarget.Add: %v", err)
	}
	if addFake.lastSystem != "exampledeploy-reverse-ran" {
		t.Fatalf("E3-deploy: adapter.Add did not drive the reverse channel, got %q", addFake.lastSystem)
	}
}
