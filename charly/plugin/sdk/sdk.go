// Package sdk is the importable surface an out-of-tree charly plugin builds
// against. An external plugin implements the proto Provider + PluginMeta services
// (github.com/overthinkos/overthink/charly/plugin/proto) and calls sdk.Serve from
// its main; charly connects to it through the SAME handshake + dispense key. The
// handshake/glue live here (NOT in charly's package main) so both charly and an
// external plugin share ONE definition — no drift, no duplication (R3).
package sdk

import (
	"context"

	plugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	pb "github.com/overthinkos/overthink/charly/plugin/proto"
)

// ProtocolVersion is the go-plugin/proto contract version — a thin secondary gate.
// CalVer (charly's version.go) is the authority; matching CalVer ⇒ matching proto.
const ProtocolVersion = 1

// DispenseKey is the single go-plugin plugin name; charly serves/dispenses ONE
// gRPC plugin exposing the uniform Provider + PluginMeta services.
const DispenseKey = "charly"

// Handshake is the magic-cookie handshake charly and every plugin MUST share. A
// plugin server refuses to serve unless launched with CHARLY_PLUGIN set, so a
// plugin binary run by hand prints the "not meant to be executed directly" notice
// instead of hanging.
var Handshake = plugin.HandshakeConfig{
	ProtocolVersion:  ProtocolVersion,
	MagicCookieKey:   "CHARLY_PLUGIN",
	MagicCookieValue: "charly-plugin-v1",
}

// Serve exposes a plugin's Provider + PluginMeta services over go-plugin gRPC and
// blocks until the host disconnects (then auto-exits — clean teardown, no orphan).
// The single entry point an external plugin's main calls:
//
//	func main() { sdk.Serve(&myProvider{}, &myMeta{}) }
func Serve(providerSrv pb.ProviderServer, metaSrv pb.PluginMetaServer) {
	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: Handshake,
		Plugins:         PluginMap(providerSrv, metaSrv),
		GRPCServer:      plugin.DefaultGRPCServer,
	})
}

// PluginMap builds the go-plugin PluginSet for the dispense key. Server side passes
// the two service impls; the client side (charly connecting) passes nil,nil and
// receives a *Conn from the dispense.
func PluginMap(providerSrv pb.ProviderServer, metaSrv pb.PluginMetaServer) plugin.PluginSet {
	return plugin.PluginSet{DispenseKey: &grpcPlugin{providerSrv: providerSrv, metaSrv: metaSrv}}
}

type grpcPlugin struct {
	plugin.NetRPCUnsupportedPlugin
	providerSrv pb.ProviderServer
	metaSrv     pb.PluginMetaServer
}

func (p *grpcPlugin) GRPCServer(_ *plugin.GRPCBroker, s *grpc.Server) error { //nolint:unparam // go-plugin GRPCPlugin mandates the error return
	pb.RegisterProviderServer(s, p.providerSrv)
	pb.RegisterPluginMetaServer(s, p.metaSrv)
	return nil
}

func (p *grpcPlugin) GRPCClient(_ context.Context, _ *plugin.GRPCBroker, c *grpc.ClientConn) (interface{}, error) { //nolint:unparam // go-plugin GRPCPlugin mandates the (any,error) return
	return &Conn{Provider: pb.NewProviderClient(c), Meta: pb.NewPluginMetaClient(c)}, nil
}

// Conn is the dispensed client handle — charly's side of a connected plugin.
type Conn struct {
	Provider pb.ProviderClient
	Meta     pb.PluginMetaClient
}
