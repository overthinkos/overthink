// Command plugin-kube is the OUT-OF-TREE charly plugin serving the `kube`
// Kubernetes cluster-probe check verb AND the k3s kubeconfig-merge the
// `target: k8s` / k3s-server deploy seam needs (a standalone Go module, its own
// go.mod). It exists to keep the heavy k8s.io/client-go + k8s.io/apimachinery
// stack OUT of charly's core go.mod: the host go-builds this binary and serves
// it OUT-OF-PROCESS over go-plugin gRPC via the charly plugin SDK, so the `kube:`
// verb dispatches through the provider registry exactly like a built-in — with
// the verb keeping its `kube:` discriminator + every modifier (#KubeMethod) on
// charly's core #Op (authoring unchanged). The goadb-analog of candy/plugin-adb:
// the FULL client-go/clientcmd/dynamic dependency lives HERE.
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

// Describe ships the plugin's capability (verb:kube) AND its self-contained CUE
// schema over the wire via sdk.BuildCapabilities. kube keeps its entire authoring
// contract (the #KubeMethod enum + every modifier) on charly's core #Op — like
// cdp/vnc, it has NO plugin_input — so the advertised capability carries an EMPTY
// InputDef and the served schema (kube.cue) exists only to satisfy the host's
// non-empty-schema load gate. The SDK compiles the schema standalone here,
// failing loudly before serving if it is broken.
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	return sdk.BuildCapabilities("2026.174.1200",
		[]sdk.ProvidedCapability{{Class: "verb", Word: "kube", InputDef: ""}},
		schemaFS, "schema")
}
