// Command plugin-example-stepkind is a reference OUT-OF-TREE charly class:step plugin that
// proves the F3 EXTERNAL STEP-KIND contract: a plugin CONTRIBUTES a first-class install-step
// KIND ("external:examplestepkind") whose Scope/Venue/Gate it DECLARES in its Describe
// StepContract, carried OPAQUELY through the IR (InstallStepView.Payload) and dispatched by
// the host's OPEN DEFAULT ARM (no compiled-in case) to this plugin's OpExecute over the E3b
// reverse channel — where it writes a venue marker and returns a teardown ReverseOp the host
// records + replays (record-and-replay). charly host-builds it + serves it OUT-OF-PROCESS over
// go-plugin gRPC (LocalTransport), the same path the deploy/verb example plugins ride.
package main

import (
	"context"
	"encoding/json"
	"embed"
	"fmt"

	"github.com/overthinkos/overthink/charly/plugin/kit"
	pb "github.com/overthinkos/overthink/charly/plugin/proto"
	"github.com/overthinkos/overthink/charly/plugin/sdk"
	"github.com/overthinkos/overthink/charly/spec"
)

//go:embed schema/*.cue
var schemaFS embed.FS

const calver = "2026.181.0001"

// markerDir is the disposable venue scratch dir the step writes into — under /tmp, user-owned,
// removed by the recorded teardown op (zero operator side-effect).
const markerDir = "/tmp/charly-examplestepkind"
const markerPath = markerDir + "/marker"

func main() { sdk.Serve(&provider{}, &meta{}) }

type provider struct{ pb.UnimplementedProviderServer }

// stepInput is the OPAQUE Payload (the external step's plugin_input) decoded host→plugin.
type stepInput struct {
	Marker string `json:"marker"`
}

// Invoke runs the external step's OpExecute: decode the opaque payload, write the marker on the
// venue over the reverse channel (proving the external step kind executed + its payload
// round-tripped), and return a teardown ReverseOp removing the scratch dir.
func (provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	exec, err := sdk.ExecutorFromInvoke(req.GetExecutorBrokerId())
	if err != nil {
		return nil, err
	}
	var in stepInput
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
			return nil, fmt.Errorf("plugin-example-stepkind: decode payload: %w", err)
		}
	}
	if in.Marker == "" {
		in.Marker = "EXAMPLE-STEPKIND-OK"
	}
	script := fmt.Sprintf("mkdir -p %s && printf '%%s\\n' %s > %s", markerDir, kit.ShellQuote(in.Marker), markerPath)
	if err := exec.RunUser(ctx, script, nil); err != nil {
		return nil, fmt.Errorf("plugin-example-stepkind: write marker: %w", err)
	}
	reverseOps := []spec.ReverseOp{sdk.PluginScriptReverseOp(spec.ScopeUser, "rm -rf "+markerDir)}
	return sdk.BuildDeployReply(reverseOps, "plugin-example-stepkind", calver)
}

type meta struct{ pb.UnimplementedPluginMetaServer }

// Describe advertises the step:examplestepkind capability WITH its DECLARED StepContract
// (Scope user, Venue host-native (0), no gate) — the F3 plugin-declared install-step contract
// the host carries through the IR and applies via the open default arm.
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities(calver,
		[]sdk.ProvidedCapability{{
			Class:        "step",
			Word:         "examplestepkind",
			InputDef:     "#ExamplestepkindInput",
			StepContract: &sdk.StepContract{Scope: "user", Venue: 0, Gate: ""},
		}},
		schemaFS, "schema")
}
