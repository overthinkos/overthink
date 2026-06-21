package main

import (
	"context"
	"fmt"
	"io"
	"os/exec"

	plugin "github.com/hashicorp/go-plugin"
)

// PluginTransport connects to a plugin and returns its Providers plus a closer
// that tears the connection down. The loader registers the providers into
// providerRegistry; the closer is held by the registry and run at Close().
//
// C0 ships LocalTransport (same-host subprocess). ExecutorTransport (deliver
// charly into a venue → __plugin serve → ssh-`L` forward → reattach) and
// BridgeTransport (pod↔pod TCP + manual mTLS) land in the out-of-proc-on-a-bed
// follow-up cutover, where they are proven end-to-end on a disposable bed (the
// RDD spikes already proved the mechanism).
type PluginTransport interface {
	Connect(ctx context.Context) ([]Provider, io.Closer, error)
}

// LocalTransport runs a plugin provider binary as a same-host subprocess — the
// standard go-plugin exec model, the one path where AutoMTLS applies (charly
// execs the child, so the mutual cert exchange happens).
type LocalTransport struct {
	BinPath string   // the plugin provider binary
	Args    []string // serve args; default ["__plugin","serve"]
}

func (t *LocalTransport) Connect(ctx context.Context) ([]Provider, io.Closer, error) {
	args := t.Args
	if len(args) == 0 {
		args = []string{"__plugin", "serve"}
	}
	client := plugin.NewClient(&plugin.ClientConfig{
		HandshakeConfig:  charlyHandshake,
		Plugins:          charlyPluginMap(nil),
		Cmd:              exec.Command(t.BinPath, args...),
		AllowedProtocols: []plugin.Protocol{plugin.ProtocolGRPC},
		AutoMTLS:         true,
	})
	providers, err := connectAndDescribe(ctx, client)
	if err != nil {
		return nil, nil, err
	}
	return providers, &clientCloser{client}, nil
}

// connectAndDescribe dispenses the uniform plugin, reads its capability manifest,
// and builds the gRPC-backed Providers. On any failure it kills the client so no
// subprocess leaks.
func connectAndDescribe(ctx context.Context, client *plugin.Client) ([]Provider, error) {
	rpc, err := client.Client()
	if err != nil {
		client.Kill()
		return nil, fmt.Errorf("plugin client: %w", err)
	}
	raw, err := rpc.Dispense(pluginDispenseKey)
	if err != nil {
		client.Kill()
		return nil, fmt.Errorf("plugin dispense: %w", err)
	}
	conn, ok := raw.(*pluginConn)
	if !ok {
		client.Kill()
		return nil, fmt.Errorf("plugin: unexpected dispensed type %T", raw)
	}
	caps, err := conn.describe(ctx)
	if err != nil {
		client.Kill()
		return nil, fmt.Errorf("plugin describe: %w", err)
	}
	// CalVer is the version authority (version.go). A plugin built from a fetched
	// repo is CalVer-stamped; a gross mismatch is a readable refusal rather than a
	// wire panic. An empty/unparseable plugin CalVer is tolerated for a same-host
	// builtin served out-of-process (identical binary).
	providers, err := conn.buildProviders(caps.GetProvided())
	if err != nil {
		client.Kill()
		return nil, err
	}
	return providers, nil
}

// clientCloser adapts a go-plugin client to io.Closer. Kill() disconnects; the
// in-venue server auto-exits when the host disconnects (clean teardown).
type clientCloser struct{ c *plugin.Client }

func (cc *clientCloser) Close() error { cc.c.Kill(); return nil }
