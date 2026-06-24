// Command plugin-example-step is a reference OUT-OF-TREE charly plugin that proves
// BUILD-TIME plugin execution: `charly box build`/`generate` connects it
// out-of-process and Invokes its OpEmit during image generation, then splices the
// returned Containerfile FRAGMENT verbatim into the .build/<image>/Containerfile —
// so the RUN bakes a marker file into the image. Authored as a candy `run:` step
// (`plugin: examplestep`, a verb) in BUILD context, it is the build-context
// counterpart of the deploy-time candy/plugin-example-deploy.
//
// The operator-authorized build-time plugin-execution MECHANISM lives in charly
// (the generate.go NewGenerator connect seam + tasks.go emitPluginFragment); this
// module is only the reference PAYLOAD that mechanism builds + executes.
package main

import (
	"context"
	"embed"
	"encoding/json"

	pb "github.com/overthinkos/overthink/charly/plugin/proto"
	"github.com/overthinkos/overthink/charly/plugin/sdk"
	"github.com/overthinkos/overthink/charly/spec"
)

//go:embed schema/*.cue
var schemaFS embed.FS

func main() { sdk.Serve(&provider{}, &meta{}) }

type provider struct{ pb.UnimplementedProviderServer }

// opEmit mirrors charly's build-time emit op selector (package main's OpEmit =
// "emit"). An external plugin can't import that constant, so it is named here; the
// host sends it on the BUILD-context Invoke (deploy/check ops use other selectors).
const opEmit = "emit"

// Invoke handles the build-time OpEmit call: it returns a spec.EmitReply whose
// Fragment is a Containerfile RUN the host splices verbatim into the generated
// Containerfile, baking an empty /opt/examplestep-baked marker into the image (proof
// the plugin executed at build). The host marshals the authored plugin_input as
// op.Params and a spec.BuildEnv as op.Env — a real plugin tailors its fragment per
// spec.BuildEnv.Distros; this example emits a static, deterministic RUN. Any
// non-OpEmit op returns a benign empty result (this plugin contributes nothing at
// deploy/check time).
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != opEmit {
		return &pb.InvokeReply{ResultJson: []byte("{}")}, nil
	}
	j, err := json.Marshal(spec.EmitReply{Fragment: "RUN mkdir -p /opt && : > /opt/examplestep-baked\n"})
	if err != nil {
		return nil, err
	}
	return &pb.InvokeReply{ResultJson: j}, nil
}

type meta struct{ pb.UnimplementedPluginMetaServer }

// Describe advertises the verb:examplestep capability + its self-contained CUE
// schema over the same channel a builtin uses; BuildCapabilities compiles the schema
// standalone, failing loudly if broken.
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities("2026.175.0001",
		[]sdk.ProvidedCapability{{Class: "verb", Word: "examplestep", InputDef: "#ExamplestepInput"}},
		schemaFS, "schema")
}
