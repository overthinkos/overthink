package main

import (
	"fmt"
	"os"
	"sync"
)

// readiness_config.go — charly core's readiness ENTRY. The config→resolved resolver AND the
// CHARLY_READINESS_* field table live ONCE in charly/vmshared (ResolveReadiness + the PluginEnv
// emitter, aliased here as readinessResolve via vmshared_aliases.go), shared with the
// out-of-process plugins. loadedReadiness feeds the resolver the PROJECT's defaults.readiness —
// which the plugins cannot LoadUnified to read; the host threads its resolved bounds to them via
// ResolvedReadiness.PluginEnv (see readinessPluginEnv + LocalTransport.Connect).

// loadedReadiness resolves the project's readiness bounds ONCE (the project defaults.readiness
// block + CHARLY_READINESS_* env + the named fallbacks, validated). A site deep in the executors
// with no threaded ResolvedReadiness calls this. On absence/error it falls back to the built-in
// defaults (always safe + never-hang) with a logged warning — a bad config block degrades to the
// built-in defaults rather than breaking every deploy.
var (
	readinessOnce   sync.Once
	readinessCached ResolvedReadiness
)

func loadedReadiness() ResolvedReadiness {
	readinessOnce.Do(func() {
		var def *ReadinessConfig
		if uf, ok, err := LoadUnified("."); err == nil && ok && uf != nil {
			def = uf.Defaults.Readiness
		}
		rr, err := readinessResolve(def)
		if err != nil {
			fmt.Fprintf(os.Stderr, "readiness config invalid (%v) — using built-in defaults\n", err)
			rr, _ = readinessResolve(nil)
		}
		readinessCached = rr
	})
	return readinessCached
}

// readinessPluginEnv emits the host's RESOLVED readiness as CHARLY_READINESS_* env entries, for
// threading into an out-of-process plugin's spawn env (LocalTransport.Connect) — the plugin's own
// readinessResolve re-reads them, so it honors the project's defaults.readiness without a project
// loader. The emitter (ResolvedReadiness.PluginEnv) lives in vmshared, beside the resolver.
func readinessPluginEnv() []string {
	return loadedReadiness().PluginEnv()
}
