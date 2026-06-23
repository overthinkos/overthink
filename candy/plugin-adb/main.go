// Command plugin-adb is the OUT-OF-TREE charly plugin serving the `adb`
// Android-Debug-Bridge check verb AND the goadb-backed Android app-install /
// device-probe operations the `target: android` deploy + `charly status`
// collector need (a standalone Go module, its own go.mod). It exists to keep
// github.com/zach-klippenstein/goadb OUT of charly's core go.mod: the host
// go-builds this binary and serves it OUT-OF-PROCESS over go-plugin gRPC via the
// charly plugin SDK, so the `adb:` verb dispatches through the provider registry
// exactly like a built-in — with the verb keeping its `adb:` discriminator +
// every modifier on charly's core #Op (authoring unchanged). The second external
// dep-shed (after candy/plugin-appium); the FULL adb/goadb dependency lives HERE.
package main

import (
	"context"
	"embed"

	pb "github.com/overthinkos/overthink/charly/plugin/proto"
	"github.com/overthinkos/overthink/charly/plugin/sdk"
)

//go:embed schema/*.cue
var schemaFS embed.FS

func main() { sdk.Serve(&provider{}, &meta{}) }

type meta struct {
	pb.UnimplementedPluginMetaServer
}

// Describe ships the plugin's capability (verb:adb) AND its self-contained CUE
// schema over the wire via sdk.BuildCapabilities. adb keeps its entire authoring
// contract (the #AdbMethod enum + every modifier) on charly's core #Op — like
// cdp/vnc, it has NO plugin_input — so the advertised capability carries an EMPTY
// InputDef and the served schema (adb.cue) exists only to satisfy the host's
// non-empty-schema load gate. The SDK compiles the schema standalone here,
// failing loudly before serving if it is broken.
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities("2026.174.0900",
		[]sdk.ProvidedCapability{{Class: "verb", Word: "adb", InputDef: ""}},
		schemaFS, "schema")
}
