// Command serve is the OUT-OF-PROCESS entrypoint for the package-group kind plugin: a thin shim
// serving the importable provider over go-plugin gRPC (the SAME provider compiles INTO
// charly in-process via plugins_generated.go).
package main

import (
	packagegroupkind "github.com/overthinkos/overthink/candy/plugin-package-group"
	"github.com/overthinkos/overthink/charly/plugin/sdk"
)

func main() { sdk.Serve(packagegroupkind.NewProvider(), packagegroupkind.NewMeta()) }
