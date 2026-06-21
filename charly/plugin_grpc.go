package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	plugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	pb "github.com/overthinkos/overthink/charly/plugin/proto"
)

// pluginProtocolVersion is the go-plugin/proto contract version — a thin
// secondary gate. CalVer (version.go) is the authority; if CalVer matches between
// two charly instances, the protocol matches by construction.
const pluginProtocolVersion = 1

// charlyHandshake is the go-plugin magic-cookie handshake. A plugin server refuses
// to serve unless launched with CHARLY_PLUGIN set (so `charly __plugin serve` run
// by hand prints the "not meant to be executed directly" notice instead of hanging).
var charlyHandshake = plugin.HandshakeConfig{
	ProtocolVersion:  pluginProtocolVersion,
	MagicCookieKey:   "CHARLY_PLUGIN",
	MagicCookieValue: "charly-plugin-v1",
}

// pluginDispenseKey is the single go-plugin plugin name; charly serves ONE
// gRPC plugin exposing the uniform Provider + PluginMeta services.
const pluginDispenseKey = "charly"

// servedSet is the provider set a `__plugin serve` process exposes over gRPC.
type servedSet struct {
	calver   string
	byKey    map[string]Provider // class:word → provider
	provided []string            // sorted "class:word" capability list
}

func newServedSet(calver string, providers []Provider) *servedSet {
	s := &servedSet{calver: calver, byKey: map[string]Provider{}}
	for _, p := range providers {
		k := provKey(p.Class(), p.Reserved())
		s.byKey[k] = p
		s.provided = append(s.provided, k)
	}
	sort.Strings(s.provided)
	return s
}

// --- server side ---

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
	// C0: single-frame. A genuinely-streaming provider (record/logcat) is a
	// follow-up (StreamingProvider) — non-streaming providers send exactly one frame.
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
	return &pb.Capabilities{
		Calver:          m.set.calver,
		ProtocolVersion: pluginProtocolVersion,
		Provided:        m.set.provided,
	}, nil
}

// --- go-plugin glue ---

type charlyGRPCPlugin struct {
	plugin.NetRPCUnsupportedPlugin
	set *servedSet // server side; nil on the client
}

func (p *charlyGRPCPlugin) GRPCServer(_ *plugin.GRPCBroker, s *grpc.Server) error { //nolint:unparam // go-plugin GRPCPlugin interface mandates the error return
	pb.RegisterProviderServer(s, &providerGRPCServer{set: p.set})
	pb.RegisterPluginMetaServer(s, &metaGRPCServer{set: p.set})
	return nil
}

func (p *charlyGRPCPlugin) GRPCClient(_ context.Context, _ *plugin.GRPCBroker, c *grpc.ClientConn) (interface{}, error) { //nolint:unparam // go-plugin GRPCPlugin interface mandates the (any,error) return
	return &pluginConn{provider: pb.NewProviderClient(c), meta: pb.NewPluginMetaClient(c)}, nil
}

func charlyPluginMap(set *servedSet) plugin.PluginSet {
	return plugin.PluginSet{pluginDispenseKey: &charlyGRPCPlugin{set: set}}
}

// --- client side ---

type pluginConn struct {
	provider pb.ProviderClient
	meta     pb.PluginMetaClient
}

func (pc *pluginConn) describe(ctx context.Context) (*pb.Capabilities, error) {
	return pc.meta.Describe(ctx, &pb.Empty{})
}

// grpcProvider is a Provider backed by a remote plugin over gRPC — the
// out-of-process peer of a built-in. Call sites never distinguish the two.
type grpcProvider struct {
	conn  *pluginConn
	class ProviderClass
	word  string
}

func (g *grpcProvider) Reserved() string     { return g.word }
func (g *grpcProvider) Class() ProviderClass { return g.class }
func (g *grpcProvider) Invoke(ctx context.Context, op *Operation) (*Result, error) {
	rep, err := g.conn.provider.Invoke(ctx, &pb.InvokeRequest{
		Reserved: op.Reserved, Op: op.Op, ParamsJson: op.Params, EnvJson: op.Env, Class: string(g.class),
	})
	if err != nil {
		return nil, err
	}
	return &Result{JSON: rep.GetResultJson()}, nil
}

// buildProviders turns a connected plugin's advertised capability list into the
// gRPC-backed Providers the registry indexes.
func (pc *pluginConn) buildProviders(provided []string) ([]Provider, error) {
	out := make([]Provider, 0, len(provided))
	for _, capStr := range provided {
		class, word, ok := splitCapability(capStr)
		if !ok {
			return nil, fmt.Errorf("plugin advertised malformed capability %q", capStr)
		}
		out = append(out, &grpcProvider{conn: pc, class: class, word: word})
	}
	return out, nil
}

// splitCapability parses a "<class>:<word>" capability advertised by a plugin.
func splitCapability(s string) (ProviderClass, string, bool) {
	i := strings.IndexByte(s, ':')
	if i <= 0 || i == len(s)-1 {
		return "", "", false
	}
	c := ProviderClass(s[:i])
	if !providerClasses[c] {
		return "", "", false
	}
	return c, s[i+1:], true
}
