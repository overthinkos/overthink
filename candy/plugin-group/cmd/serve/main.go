// Command serve is the OUT-OF-PROCESS entrypoint for the group structural kind plugin: a thin shim
// serving the importable provider over go-plugin gRPC (the SAME provider compiles INTO charly
// in-process via plugins_generated.go, its default compiled-in placement).
package main

import (
	groupkind "github.com/overthinkos/overthink/candy/plugin-group"
	"github.com/overthinkos/overthink/charly/plugin/sdk"
)

func main() { sdk.Serve(groupkind.NewProvider(), groupkind.NewMeta()) }
