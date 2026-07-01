// Command serve is the OUT-OF-PROCESS entrypoint for the substrate structural kind plugin: a
// thin shim serving the importable provider over go-plugin gRPC (the SAME provider compiles
// INTO charly in-process via plugins_generated.go, its default compiled-in placement).
package main

import (
	substratekind "github.com/overthinkos/overthink/candy/plugin-substrate"
	"github.com/overthinkos/overthink/charly/plugin/sdk"
)

func main() { sdk.Serve(substratekind.NewProvider(), substratekind.NewMeta()) }
