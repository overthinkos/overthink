package main

import (
	"context"
	"fmt"
	"io"
	"os/exec"

	plugin "github.com/hashicorp/go-plugin"

	"github.com/overthinkos/overthink/charly/plugin/sdk"
)

// PluginTransport connects to a plugin and returns its self-contained unit (its
// Providers AND its CUE schema, lifted from the Describe channel) plus a closer
// that tears the connection down. The loader gates the unit's schema and registers
// the providers into providerRegistry; the closer is held by the registry and run
// at Close(). The host code above this interface NEVER branches on builtin vs
// external — that is the whole point of the unit (the zero-distinction seam).
//
// C0 ships LocalTransport (same-host subprocess) + InProcTransport (the builtin's
// in-proc Describe channel). ExecutorTransport (deliver charly into a venue →
// __plugin serve → ssh-`L` forward → reattach) and BridgeTransport (pod↔pod TCP +
// manual mTLS) land in the out-of-proc-on-a-bed follow-up cutover, where they are
// proven end-to-end on a disposable bed (the RDD spikes already proved the
// mechanism).
type PluginTransport interface {
	Connect(ctx context.Context) (*PluginUnit, io.Closer, error)
}

// InProcTransport is a built-in plugin's "Describe channel": no socket, no
// subprocess — the in-tree unit (its providers + its embedded schema) is handed
// straight back. It exists so the host's load/gate/validate path is byte-identical
// for an in-proc builtin and an out-of-proc external (directive: the host must not
// special-case builtin vs external).
type InProcTransport struct{ Unit *PluginUnit }

//nolint:unparam // the (error) result is required by the PluginTransport interface (LocalTransport's Connect genuinely errors); an in-proc handoff cannot fail, hence always nil.
func (t *InProcTransport) Connect(context.Context) (*PluginUnit, io.Closer, error) {
	return t.Unit, io.NopCloser(nil), nil
}

// LocalTransport runs a plugin provider binary as a same-host subprocess — the
// standard go-plugin exec model, the one path where AutoMTLS applies (charly
// execs the child, so the mutual cert exchange happens).
type LocalTransport struct {
	BinPath string   // the plugin provider binary
	Args    []string // serve args; default ["__plugin","serve"]
}

func (t *LocalTransport) Connect(ctx context.Context) (*PluginUnit, io.Closer, error) {
	args := t.Args
	if len(args) == 0 {
		args = []string{"__plugin", "serve"}
	}
	client := plugin.NewClient(&plugin.ClientConfig{
		HandshakeConfig:  sdk.Handshake,
		Plugins:          sdk.PluginMap(nil, nil),
		Cmd:              exec.Command(t.BinPath, args...),
		AllowedProtocols: []plugin.Protocol{plugin.ProtocolGRPC},
		AutoMTLS:         true,
	})
	unit, err := connectAndDescribe(ctx, client)
	if err != nil {
		return nil, nil, err
	}
	return unit, &clientCloser{client}, nil
}

// connectAndDescribe dispenses the uniform plugin, reads its capability manifest
// (providers + schema, both over the Describe channel), and builds the unit. On
// any failure it kills the client so no subprocess leaks.
func connectAndDescribe(ctx context.Context, client *plugin.Client) (*PluginUnit, error) {
	rpc, err := client.Client()
	if err != nil {
		client.Kill()
		return nil, fmt.Errorf("plugin client: %w", err)
	}
	raw, err := rpc.Dispense(sdk.DispenseKey)
	if err != nil {
		client.Kill()
		return nil, fmt.Errorf("plugin dispense: %w", err)
	}
	conn, ok := raw.(*sdk.Conn)
	if !ok {
		client.Kill()
		return nil, fmt.Errorf("plugin: unexpected dispensed type %T", raw)
	}
	caps, err := describe(ctx, conn)
	if err != nil {
		client.Kill()
		return nil, fmt.Errorf("plugin describe: %w", err)
	}
	// buildUnit applies the protocol-version gate (a readable refusal, not a later
	// wire panic) before lifting caps → unit.
	unit, err := buildUnit(conn, caps)
	if err != nil {
		client.Kill()
		return nil, err
	}
	return unit, nil
}

// clientCloser adapts a go-plugin client to io.Closer. Kill() disconnects; the
// in-venue server auto-exits when the host disconnects (clean teardown).
type clientCloser struct{ c *plugin.Client }

func (cc *clientCloser) Close() error { cc.c.Kill(); return nil }
