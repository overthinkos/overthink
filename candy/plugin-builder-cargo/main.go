// Command plugin-builder-cargo is the OUT-OF-TREE charly plugin serving the `cargo` builder's
// DEPLOY-TIME IR shim. A builder's BUILD-TIME multi-stage stays the CORE embedded vocabulary
// (charly's generate.go emitBuilderStages); this plugin carries the per-builder deploy-time legs:
//
//   - OpCollectContext → the per-candy stage-context keys the host records on a BuilderStep
//     (cargo records none — binaries are read from Cargo.toml [[bin]] host-side at install time); and
//   - OpReverse → that step's teardown ops (cargo → cargo-uninstall when binaries are known).
//
// The host invokes both in its build PRE-PASS (BEFORE the pure BuildDeployPlan compile), keeping the
// compiler pure. The per-builder LOGIC is the shared charly/plugin/kit (R3); this module is only the
// composable selection point + serve shim.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"

	"github.com/overthinkos/overthink/charly/plugin/kit"
	pb "github.com/overthinkos/overthink/charly/plugin/proto"
	"github.com/overthinkos/overthink/charly/plugin/sdk"
	"github.com/overthinkos/overthink/charly/spec"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// builderWord is the reserved builder word this plugin serves.
const builderWord = "cargo"

func main() { sdk.Serve(&provider{}, &meta{}) }

type provider struct{ pb.UnimplementedProviderServer }

// Invoke dispatches the two deploy-time builder ops to the shared kit logic. Any other op is a loud
// error (a builder serves neither OpResolve here — its build-time multi-stage is the core vocabulary
// — nor any deploy/check op).
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	switch req.GetOp() {
	case sdk.OpCollectContext:
		var in spec.BuilderCollectInput
		if len(req.GetParamsJson()) > 0 {
			if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
				return nil, fmt.Errorf("builder %q: decode collect-context input: %w", builderWord, err)
			}
		}
		j, err := json.Marshal(spec.BuilderCollectReply{Context: kit.BuilderCollectContext(builderWord, in)})
		if err != nil {
			return nil, err
		}
		return &pb.InvokeReply{ResultJson: j}, nil
	case sdk.OpReverse:
		var in spec.BuilderReverseInput
		if len(req.GetParamsJson()) > 0 {
			if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
				return nil, fmt.Errorf("builder %q: decode reverse input: %w", builderWord, err)
			}
		}
		j, err := json.Marshal(spec.BuilderReverseReply{ReverseOps: kit.BuilderReverse(builderWord, in)})
		if err != nil {
			return nil, err
		}
		return &pb.InvokeReply{ResultJson: j}, nil
	}
	return nil, fmt.Errorf("builder %q: unsupported op %q (serves only %q + %q)", builderWord, req.GetOp(), sdk.OpCollectContext, sdk.OpReverse)
}

type meta struct{ pb.UnimplementedPluginMetaServer }

// Describe advertises the builder:cargo capability + its self-contained CUE schema over the same
// channel a builtin uses; BuildCapabilities compiles the schema standalone, failing loudly if broken.
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities("2026.181.0300",
		[]sdk.ProvidedCapability{{Class: "builder", Word: builderWord, InputDef: "#CargoBuilderInput"}},
		schemaFS, "schema")
}
