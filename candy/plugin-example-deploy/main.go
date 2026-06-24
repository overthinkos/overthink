// Command plugin-example-deploy is a reference OUT-OF-TREE charly DEPLOY plugin that
// proves the FULL external deploy lifecycle over the E3b reverse channel: its Invoke
// decodes the host's InstallPlan views + venue descriptor, applies a marker on the
// host VENUE by calling BACK to the host's ExecutorService (via the SDK, over the
// go-plugin broker), and RETURNS a structured DeployReply carrying a plugin-script
// reverse op the host RECORDS in the ledger and REPLAYS at `charly bundle del`. charly
// host-builds it and serves it OUT-OF-PROCESS over go-plugin gRPC (LocalTransport),
// the same path candy/plugin-example-external rides for verbs.
package main

import (
	"context"
	"embed"
	"fmt"
	"path"
	"strings"

	pb "github.com/overthinkos/overthink/charly/plugin/proto"
	"github.com/overthinkos/overthink/charly/plugin/sdk"
	"github.com/overthinkos/overthink/charly/spec"
)

//go:embed schema/*.cue
var schemaFS embed.FS

func main() { sdk.Serve(&provider{}, &meta{}) }

type provider struct{ pb.UnimplementedProviderServer }

// markerDir derives the deploy's disposable scratch dir DETERMINISTICALLY from the
// deploy name, so Add and Update (whose unified signature carries no node env) agree
// on one path. Under /tmp → zero operator side-effect; Del's recorded reverse op
// removes it. User-owned, so the apply + teardown need no sudo.
func markerDir(deployName string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, deployName)
	if safe == "" {
		safe = "default"
	}
	return path.Join("/tmp/charly-exampledeploy", safe)
}

// Invoke applies the deployment on the host venue via the E3b reverse channel and
// returns the teardown ops + ledger record. It decodes the host-marshalled plans +
// venue (proving the wire contract carries them author -> host -> wire -> external
// process), then writes TWO markers via the SDK Executor (the reverse channel): the
// `applied` marker proves the apply ran; the `probe` marker is what the bed's Test
// check (file=<probe>) inspects. Both ride a single plugin-script reverse op.
func (provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	exec, err := sdk.ExecutorFromInvoke(req.GetExecutorBrokerId())
	if err != nil {
		return nil, err
	}
	if _, err := sdk.DecodeInstallPlans(req.GetParamsJson()); err != nil {
		return nil, fmt.Errorf("plugin-example-deploy: decode plans: %w", err)
	}
	venue, err := sdk.DecodeDeployVenue(req.GetEnvJson())
	if err != nil {
		return nil, fmt.Errorf("plugin-example-deploy: decode venue: %w", err)
	}
	dir := markerDir(venue.DeployName)
	applied := dir + "/applied"
	probe := dir + "/probe"
	// Apply both markers on the host VENUE over the reverse channel (user scope, no
	// sudo — the markers live under /tmp). Idempotent: re-running (Update) re-creates
	// the same files.
	apply := "mkdir -p " + dir + " && : > " + applied + " && : > " + probe
	if err := exec.RunUser(ctx, apply, nil); err != nil {
		return nil, err
	}
	// ONE generic plugin-script reverse op removing the whole scratch dir (both
	// markers) at teardown; the host records it in the ledger and replays it.
	reverse := sdk.PluginScriptReverseOp(spec.ScopeUser, "rm -rf "+dir)
	return sdk.BuildDeployReply([]spec.ReverseOp{reverse}, "plugin-example-deploy", "2026.175.0001")
}

type meta struct{ pb.UnimplementedPluginMetaServer }

// Describe advertises the deploy:exampledeploy capability + its self-contained CUE
// schema over the same channel a builtin uses; BuildCapabilities compiles the schema
// standalone, failing loudly if broken.
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities("2026.175.0001",
		[]sdk.ProvidedCapability{{Class: "deploy", Word: "exampledeploy", InputDef: "#ExampledeployInput"}},
		schemaFS, "schema")
}
