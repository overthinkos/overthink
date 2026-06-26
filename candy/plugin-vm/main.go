// Command plugin-vm is the OUT-OF-TREE charly plugin housing charly's VM-subsystem IMPL: it serves
// the `charly check libvirt` probe verb plus the internal VM ops — domain-state / list-domains /
// resolve-spice / resolve-vnc / create / start / stop / destroy / snapshot-internal / qemu-shutdown
// / domain-xml / list-all-domains — that core's `charly vm` command tree + the spice/vnc/ssh/
// status/preempt consumers + the vm deploy target RPC. A standalone Go module (its own go.mod)
// keeping the go-libvirt + kata-containers/govmm + libvirt.org/go/libvirtxml stack OUT of charly's
// core go.mod: the host go-builds this binary and serves it OUT-OF-PROCESS over go-plugin gRPC. The
// `charly vm` command tree, kind:vm parsing, and the deploy:vm target (pure SSH) stay in core (they
// own no go-libvirt); the host resolves the VmSpec and passes it in the RPC, since this
// out-of-process plugin cannot reach core's project loader.
package main

import (
	"context"
	"embed"

	pb "github.com/overthinkos/overthink/charly/plugin/proto"
	"github.com/overthinkos/overthink/charly/plugin/sdk"
)

//go:embed schema/*.cue
var schemaFS embed.FS

func main() { sdk.Serve(&vmProvider{}, &vmMeta{}) }

type vmMeta struct {
	pb.UnimplementedPluginMetaServer
}

// Describe advertises the ONE user-facing capability: the libvirt check verb (nested under
// `charly check` at runtime like kube/adb/appium). The internal VM ops (resolution / lifecycle /
// snapshot / create / qemu-shutdown) ride Invoke via special VmOp words and are NOT Describe
// classes — core's `charly vm` command tree + the display/status/preempt consumers RPC them.
// libvirt keeps its modifiers on charly's core #Op (a schema-less verb, empty InputDef).
func (vmMeta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities("2026.177.0300",
		[]sdk.ProvidedCapability{
			{Class: "verb", Word: "libvirt", InputDef: ""},
		},
		schemaFS, "schema")
}

// vmProvider is the out-of-process provider. Its Invoke dispatches the libvirt verb (the in-process
// LibvirtCmd Kong tree) plus the internal VmOp-keyed ops (resolution / lifecycle / snapshot /
// create / qemu-shutdown) that core RPCs.
type vmProvider struct {
	pb.UnimplementedProviderServer
}
