package main

import (
	"context"
	"fmt"

	pb "github.com/overthinkos/overthink/charly/plugin/proto"
	"github.com/overthinkos/overthink/charly/plugin/sdk"
)

// inprocProvider is a Provider backed by a COMPILED-IN plugin candy's
// pb.ProviderServer, called IN-PROCESS — the in-proc twin of grpcProvider (which
// calls the SAME pb.ProviderServer methods over gRPC). Call sites never distinguish
// the two: placement (compiled-in vs out-of-process) is invisible above the
// registry. A plugin candy serves ONE provider that works in BOTH placements; this
// type is how the in-proc placement reaches it without a socket.
type inprocProvider struct {
	srv   pb.ProviderServer
	class ProviderClass
	word  string
}

func (p *inprocProvider) Reserved() string     { return p.word }
func (p *inprocProvider) Class() ProviderClass { return p.class }

func (p *inprocProvider) Invoke(ctx context.Context, op *Operation) (*Result, error) {
	rep, err := p.srv.Invoke(ctx, &pb.InvokeRequest{
		Reserved: op.Reserved, Op: op.Op, ParamsJson: op.Params, EnvJson: op.Env, Class: string(p.class),
	})
	if err != nil {
		return nil, err
	}
	return &Result{JSON: rep.GetResultJson()}, nil
}

// buildUnitInProc lifts a compiled-in plugin's (meta, provider) pair into a
// *PluginUnit by calling Describe IN-PROCESS and wrapping each advertised
// capability in an inprocProvider — the in-proc analogue of buildUnit
// (plugin_grpc.go), applying the SAME protocol-version gate and the SAME capability
// validation (R3: one capability-lifting contract, two transports). The candy's
// Describe is the single schema source for both placements, so the host's
// load/gate/validate path is byte-identical whether the plugin is compiled in or
// served out-of-process.
func buildUnitInProc(meta pb.PluginMetaServer, srv pb.ProviderServer) (*PluginUnit, error) {
	caps, err := meta.Describe(context.Background(), &pb.Empty{})
	if err != nil {
		return nil, fmt.Errorf("compiled-in plugin describe: %w", err)
	}
	if caps.GetProtocolVersion() != sdk.ProtocolVersion {
		return nil, fmt.Errorf("compiled-in plugin protocol version mismatch: plugin advertises protocol %d (CalVer %q), host requires protocol %d",
			caps.GetProtocolVersion(), caps.GetCalver(), sdk.ProtocolVersion)
	}
	provided := caps.GetProvided()
	providers := make([]Provider, 0, len(provided))
	inputDefs := make(map[string]string, len(provided))
	for _, c := range provided {
		class := ProviderClass(c.GetClass())
		if !providerClasses[class] || c.GetWord() == "" {
			return nil, fmt.Errorf("compiled-in plugin advertised malformed capability %q:%q", c.GetClass(), c.GetWord())
		}
		providers = append(providers, &inprocProvider{srv: srv, class: class, word: c.GetWord()})
		if c.GetInputDef() != "" {
			inputDefs[provKey(class, c.GetWord())] = c.GetInputDef()
		}
	}
	return &PluginUnit{
		Providers: providers,
		Schema:    PluginSchema{CueSource: caps.GetSchemaCue(), InputDefs: inputDefs},
	}, nil
}

// registerCompiledPlugin registers a COMPILED-IN plugin candy's provider in-process.
// Called from the generated plugins_generated.go init() for each candy in the
// charly.yml `compiled_plugins:` selection. It reuses RegisterBuiltinPluginUnit, so
// the compiled-in candy enters the SAME builtinPluginUnits gate (schema gated at
// process start) and registers with origin "builtin" — the fact the coexist switch
// in loadProjectPlugins keys on to skip the host go-build + out-of-process connect
// for an already-compiled-in word. A Describe/schema failure here is a build-time
// invariant violation (the candy is compiled into THIS binary), so it panics,
// mirroring loadBuiltinPluginUnits' fail-loud-at-startup contract.
func registerCompiledPlugin(srv pb.ProviderServer, meta pb.PluginMetaServer) {
	unit, err := buildUnitInProc(meta, srv)
	if err != nil {
		panic("registerCompiledPlugin: " + err.Error())
	}
	RegisterBuiltinPluginUnit(*unit)
}
