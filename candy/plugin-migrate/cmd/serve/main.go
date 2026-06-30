// Command serve is the OUT-OF-PROCESS entrypoint shim for the migrate plugin.
// migrate is compiled-in in practice (the `charly migrate` command + the
// remote-cache auto-migration call the in-core shim, which Invokes verb:migrate
// in-proc), so this exists for signature symmetry; the SAME provider compiles
// INTO charly via plugins_generated.go (C13a).
package main

import (
	migrate "github.com/overthinkos/overthink/candy/plugin-migrate"
	"github.com/overthinkos/overthink/charly/plugin/sdk"
)

func main() { sdk.Serve(migrate.NewProvider(), migrate.NewMeta()) }
