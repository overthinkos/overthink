// Command plugin-example-structkind is a reference OUT-OF-TREE charly class:kind plugin (F5):
// it serves the `examplestructkind` STRUCTURAL entity KIND over go-plugin gRPC. Unlike the FLAT
// kind (candy/plugin-example-kind, F4 — body → opaque uf.PluginKinds), a STRUCTURAL kind's
// OpLoad returns a spec.Deploy (BundleNode) MEMBER TREE that the host folds into uf.Bundle — the
// SAME map a builtin structural kind (pod/group/candy) populates in-proc — so the entity
// participates in deploy/check exactly like a builtin. It declares Structural:true in Describe.
// NOT in compiled_plugins (out-of-process only): the witness that a plugin the loader was not
// built with can contribute a uf.Bundle member tree over the wire. The structural companion of
// the flat kind plugin; this is the channel M3 reuses to externalize pod/vm/k8s/local/android/
// group/candy.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"

	pb "github.com/overthinkos/overthink/charly/plugin/proto"
	"github.com/overthinkos/overthink/charly/plugin/sdk"
	"github.com/overthinkos/overthink/charly/spec"
)

//go:embed schema/*.cue
var schemaFS embed.FS

const calver = "2026.181.0001"

func main() { sdk.Serve(&provider{}, &meta{}) }

type provider struct{ pb.UnimplementedProviderServer }

// structkindBody is the authored `examplestructkind:` entity body decoded host→plugin.
type structkindBody struct {
	Marker string `json:"marker"`
}

// Invoke handles OpLoad: decode the authored body and return a STRUCTURAL spec.Deploy whose
// MEMBER is named after the authored marker — proving the authored input round-trips through the
// plugin into a member tree the host folds into uf.Bundle (the F5 structural decode channel).
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != sdk.OpLoad {
		return nil, fmt.Errorf("examplestructkind: unsupported op %q (only %q)", req.GetOp(), sdk.OpLoad)
	}
	var in structkindBody
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
			return nil, fmt.Errorf("examplestructkind: decode entity: %w", err)
		}
	}
	memberName := in.Marker
	if memberName == "" {
		memberName = "examplestructkind-member"
	}
	// A targetless group nesting ONE pod member (Members = peer/sibling). The host folds this
	// whole tree into uf.Bundle[<entity-name>], so the member is addressable like any sibling.
	dep := spec.Deploy{
		Members: map[string]*spec.Deploy{
			memberName: {Target: "pod", Image: "examplebox"},
		},
	}
	out, err := json.Marshal(dep)
	if err != nil {
		return nil, fmt.Errorf("examplestructkind: marshal deploy: %w", err)
	}
	return &pb.InvokeReply{ResultJson: out}, nil
}

type meta struct {
	pb.UnimplementedPluginMetaServer
}

// Describe advertises the STRUCTURAL kind capability (Class "kind", word "examplestructkind",
// Structural:true) — the F5 flag that makes the host fold its OpLoad reply into uf.Bundle.
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities(calver,
		[]sdk.ProvidedCapability{{Class: "kind", Word: "examplestructkind", InputDef: "#ExamplestructkindInput", Structural: true}},
		schemaFS, "schema")
}
