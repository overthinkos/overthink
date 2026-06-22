// Command plugin-example-deploy is a reference OUT-OF-TREE charly DEPLOY plugin that
// proves the E3b reverse channel end-to-end: its Invoke calls BACK to the host's
// ExecutorService (via the SDK, over the go-plugin broker) to run a script on the
// host venue — exactly as a real external deploy/step/builder plugin will. charly
// host-builds it and serves it OUT-OF-PROCESS over go-plugin gRPC (LocalTransport),
// the same path candy/plugin-example-external rides for verbs.
package main

import (
	"context"
	"embed"
	"encoding/json"

	pb "github.com/overthinkos/overthink/charly/plugin/proto"
	"github.com/overthinkos/overthink/charly/plugin/sdk"
)

//go:embed schema/*.cue
var schemaFS embed.FS

func main() { sdk.Serve(&provider{}, &meta{}) }

type provider struct{ pb.UnimplementedProviderServer }

// Invoke dials the host's ExecutorService using the broker id the host passed in the
// request and runs a marker script on the host venue — the reverse channel. The
// reply echoes the marker so a test can assert the host's executor received it
// (author → host → broker → plugin → broker → host executor).
func (provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	exec, err := sdk.ExecutorFromInvoke(req.GetExecutorBrokerId())
	if err != nil {
		return nil, err
	}
	const marker = "exampledeploy-reverse-ran"
	if err := exec.RunSystem(ctx, marker, nil); err != nil {
		return nil, err
	}
	j, err := json.Marshal(map[string]string{"status": "ok", "marker": marker})
	if err != nil {
		return nil, err
	}
	return &pb.InvokeReply{ResultJson: j}, nil
}

type meta struct{ pb.UnimplementedPluginMetaServer }

// Describe advertises the deploy:exampledeploy capability + its self-contained CUE
// schema over the same channel a builtin uses; BuildCapabilities compiles the schema
// standalone, failing loudly if broken.
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities("2026.173.0001",
		[]sdk.ProvidedCapability{{Class: "deploy", Word: "exampledeploy", InputDef: "#ExampledeployInput"}},
		schemaFS, "schema")
}
