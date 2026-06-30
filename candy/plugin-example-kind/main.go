// Command plugin-example-kind is a reference OUT-OF-TREE charly class:kind plugin (F4): it
// serves the `examplekind` entity KIND over go-plugin gRPC. It proves PARSE-TIME kind prescan +
// pre-load connect — a `kind: examplekind` entity whose plugin is NOT compiled in is RECOGNIZED
// at parse, the plugin is CONNECTED before decode, and runPluginKind dispatches Invoke(OpLoad)
// so the authored body lands in uf.PluginKinds["examplekind"][<name>]. The kind-class companion
// of the verb/deploy/step example plugins. NOT listed in compiled_plugins — OUT-OF-PROCESS only,
// so it is the witness that the loader can recognize + connect a kind plugin it was not built with.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"

	pb "github.com/overthinkos/overthink/charly/plugin/proto"
	"github.com/overthinkos/overthink/charly/plugin/sdk"
)

//go:embed schema/*.cue
var schemaFS embed.FS

const calver = "2026.181.0001"

func main() { sdk.Serve(&provider{}, &meta{}) }

type provider struct{ pb.UnimplementedProviderServer }

// examplekindBody is the authored `examplekind:` entity body the host assembles + validates
// against #ExamplekindInput before OpLoad.
type examplekindBody struct {
	Marker string `json:"marker"`
}

// Invoke handles OpLoad: decode the authored entity body and return it as canonical JSON (the
// host lands it in uf.PluginKinds["examplekind"][<name>]). Proves the flat external-kind decode
// channel works for a plugin CONNECTED AT PARSE by the F4 prescan, not compiled in.
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != sdk.OpLoad {
		return nil, fmt.Errorf("examplekind: unsupported op %q (only %q)", req.GetOp(), sdk.OpLoad)
	}
	var in examplekindBody
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
			return nil, fmt.Errorf("examplekind: decode entity: %w", err)
		}
	}
	out, err := json.Marshal(in)
	if err != nil {
		return nil, fmt.Errorf("examplekind: marshal entity: %w", err)
	}
	return &pb.InvokeReply{ResultJson: out}, nil
}

type meta struct{ pb.UnimplementedPluginMetaServer }

// Describe ships the kind's capability (Class "kind", word "examplekind") + its self-contained
// CUE schema over the SAME Describe channel a compiled-in kind uses.
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities(calver,
		[]sdk.ProvidedCapability{{Class: "kind", Word: "examplekind", InputDef: "#ExamplekindInput"}},
		schemaFS, "schema")
}
