package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"google.golang.org/grpc"

	pb "github.com/overthinkos/overthink/charly/plugin/proto"
	"github.com/overthinkos/overthink/charly/plugin/sdk"
)

// This file is charly's side of the plugin wire: the server wrappers that expose
// charly's in-proc providerRegistry over gRPC (`__plugin serve`), and the client
// wrappers that turn a connected plugin's advertised capabilities into Providers.
// The handshake, dispense key, and go-plugin glue are shared with out-of-tree
// plugins via the importable plugin/sdk package (R3) — charly serves charly's
// Provider abstraction here; an external plugin serves the proto services directly
// via sdk.Serve.

// ProvidedCap is one served capability plus the CUE def that validates its
// plugin_input — the structured form of the proto ProvidedCapability, carried on
// the servedSet and lifted onto a connected unit's schema.
type ProvidedCap struct {
	Class    ProviderClass
	Word     string
	InputDef string
}

// servedSet is the set of plugin UNITS a `__plugin serve` process exposes over
// gRPC: their providers, the union of their structured capabilities, and the
// concatenation of their self-contained CUE schemas (shipped over Describe so the
// host validates plugin_input against base ++ served — identical to an external).
type servedSet struct {
	calver    string
	byKey     map[string]Provider // class:word → provider
	provided  []ProvidedCap       // sorted structured capability list
	schemaCUE string              // \n-joined concatenation of every unit's schema source
}

func newServedSet(calver string, units []PluginUnit) *servedSet {
	s := &servedSet{calver: calver, byKey: map[string]Provider{}}
	var schemas []string
	for _, u := range units {
		if u.Schema.CueSource != "" {
			schemas = append(schemas, u.Schema.CueSource)
		}
		for _, p := range u.Providers {
			k := provKey(p.Class(), p.Reserved())
			s.byKey[k] = p
			s.provided = append(s.provided, ProvidedCap{Class: p.Class(), Word: p.Reserved(), InputDef: u.Schema.InputDefs[k]})
		}
	}
	sort.Slice(s.provided, func(i, j int) bool {
		return provKey(s.provided[i].Class, s.provided[i].Word) < provKey(s.provided[j].Class, s.provided[j].Word)
	})
	s.schemaCUE = strings.Join(schemas, "\n")
	return s
}

// --- server side: charly's Provider registry over the proto services ---

type providerGRPCServer struct {
	pb.UnimplementedProviderServer
	set *servedSet
}

func (s *providerGRPCServer) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	p, ok := s.set.byKey[req.GetClass()+":"+req.GetReserved()]
	if !ok {
		return nil, fmt.Errorf("plugin serve: no provider %s:%s", req.GetClass(), req.GetReserved())
	}
	out, err := p.Invoke(ctx, &Operation{
		Reserved: req.GetReserved(), Op: req.GetOp(),
		Params: req.GetParamsJson(), Env: req.GetEnvJson(),
	})
	if err != nil {
		return nil, err
	}
	return &pb.InvokeReply{ResultJson: out.JSON}, nil
}

func (s *providerGRPCServer) InvokeStream(req *pb.InvokeRequest, srv pb.Provider_InvokeStreamServer) error {
	// Single-frame: a genuinely-streaming provider (record/logcat) is a follow-up
	// (StreamingProvider) — non-streaming providers send exactly one frame.
	rep, err := s.Invoke(srv.Context(), req)
	if err != nil {
		return err
	}
	return srv.Send(&pb.Frame{ResultJson: rep.GetResultJson()})
}

type metaGRPCServer struct {
	pb.UnimplementedPluginMetaServer
	set *servedSet
}

func (m *metaGRPCServer) Describe(_ context.Context, _ *pb.Empty) (*pb.Capabilities, error) {
	provided := make([]*pb.ProvidedCapability, 0, len(m.set.provided))
	for _, c := range m.set.provided {
		provided = append(provided, &pb.ProvidedCapability{Class: string(c.Class), Word: c.Word, InputDef: c.InputDef})
	}
	return &pb.Capabilities{
		Calver:          m.set.calver,
		ProtocolVersion: sdk.ProtocolVersion,
		Provided:        provided,
		SchemaCue:       m.set.schemaCUE,
	}, nil
}

// --- client side: a connected plugin → charly Providers ---

// describe reads a connected plugin's capability manifest.
func describe(ctx context.Context, conn *sdk.Conn) (*pb.Capabilities, error) {
	return conn.Meta.Describe(ctx, &pb.Empty{})
}

// grpcProvider is a Provider backed by a remote plugin over gRPC — the
// out-of-process peer of a built-in. Call sites never distinguish the two.
type grpcProvider struct {
	conn  *sdk.Conn
	class ProviderClass
	word  string
}

func (g *grpcProvider) Reserved() string     { return g.word }
func (g *grpcProvider) Class() ProviderClass { return g.class }
func (g *grpcProvider) Invoke(ctx context.Context, op *Operation) (*Result, error) {
	rep, err := g.conn.Provider.Invoke(ctx, &pb.InvokeRequest{
		Reserved: op.Reserved, Op: op.Op, ParamsJson: op.Params, EnvJson: op.Env, Class: string(g.class),
	})
	if err != nil {
		return nil, err
	}
	return &Result{JSON: rep.GetResultJson()}, nil
}

// InvokeWithExecutor invokes a deploy/step/builder op WITH the E3b reverse channel: it
// stands up the host's ExecutorService (delegating to exec) on this connection's
// go-plugin broker, passes the broker id in the request, and the out-of-process plugin
// dials back to run shell/SSH ops on exec's real venue. The reverse server lives for
// the duration of the (synchronous) Invoke. `build` is the host-engine context the
// RunHostStep leg needs (the project Config + dir + DistroCfg for a Builder/SystemPackages
// host step); the zero value is fine for a deploy/step with no host-engine step to drive. Falls
// back to a plain Invoke (broker id 0) when the connection has no broker (an in-proc
// transport) or no executor is given.
func (g *grpcProvider) InvokeWithExecutor(ctx context.Context, op *Operation, exec DeployExecutor, build buildEngineContext) (*Result, error) {
	var brokerID uint32
	if g.conn.Broker != nil && exec != nil {
		id := g.conn.Broker.NextId()
		var srv *grpc.Server
		go g.conn.Broker.AcceptAndServe(id, func(opts []grpc.ServerOption) *grpc.Server {
			srv = grpc.NewServer(opts...)
			pb.RegisterExecutorServiceServer(srv, &executorReverseServer{exec: exec, build: build})
			return srv
		})
		defer func() {
			if srv != nil {
				srv.Stop()
			}
		}()
		brokerID = id
	}
	rep, err := g.conn.Provider.Invoke(ctx, &pb.InvokeRequest{
		Reserved: op.Reserved, Op: op.Op, ParamsJson: op.Params, EnvJson: op.Env,
		Class: string(g.class), ExecutorBrokerId: brokerID,
	})
	if err != nil {
		return nil, err
	}
	return &Result{JSON: rep.GetResultJson()}, nil
}

// buildUnit lifts a connected plugin's Describe reply into a *PluginUnit: the
// gRPC-backed Providers AND the served CUE schema (source + per-capability input
// defs). This is THE client-side construction — identical for an external plugin
// and a builtin served out-of-process; the host never reads a candy schema/ dir.
func buildUnit(conn *sdk.Conn, caps *pb.Capabilities) (*PluginUnit, error) {
	// Version gate — a readable refusal here, never a later wire panic.
	// ProtocolVersion is the ENFORCED wire-compatibility gate: a plugin built
	// against a different charly proto/SDK speaks a different contract and is
	// refused before any Invoke. CalVer is the plugin's advisory version stamp,
	// surfaced in the refusal for the operator but NOT an equality gate — plugins
	// are independent repos at independent CalVers, and a same-host builtin served
	// out-of-process may advertise an empty/unstamped CalVer (identical binary).
	if caps.GetProtocolVersion() != sdk.ProtocolVersion {
		return nil, fmt.Errorf("plugin protocol version mismatch: plugin advertises protocol %d (CalVer %q), host requires protocol %d — rebuild the plugin against this charly",
			caps.GetProtocolVersion(), caps.GetCalver(), sdk.ProtocolVersion)
	}
	provided := caps.GetProvided()
	providers := make([]Provider, 0, len(provided))
	inputDefs := make(map[string]string, len(provided))
	for _, c := range provided {
		class := ProviderClass(c.GetClass())
		if !providerClasses[class] || c.GetWord() == "" {
			return nil, fmt.Errorf("plugin advertised malformed capability %q:%q", c.GetClass(), c.GetWord())
		}
		providers = append(providers, &grpcProvider{conn: conn, class: class, word: c.GetWord()})
		if c.GetInputDef() != "" {
			inputDefs[provKey(class, c.GetWord())] = c.GetInputDef()
		}
	}
	return &PluginUnit{
		Providers: providers,
		Schema:    PluginSchema{CueSource: caps.GetSchemaCue(), InputDefs: inputDefs},
	}, nil
}
