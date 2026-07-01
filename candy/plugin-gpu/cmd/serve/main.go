// Command serve is the OUT-OF-PROCESS entrypoint shim for the gpu plugin. gpu is
// compiled-in in practice (charly's in-core Detect* shims Invoke verb:gpu in-proc, and
// MemlockLimitBytes must read charly's OWN process rlimit), so this exists for signature
// symmetry; the SAME provider compiles INTO charly via plugins_generated.go.
package main

import (
	gpu "github.com/overthinkos/overthink/candy/plugin-gpu"
	"github.com/overthinkos/overthink/charly/plugin/sdk"
)

func main() { sdk.Serve(gpu.NewProvider(), gpu.NewMeta()) }
