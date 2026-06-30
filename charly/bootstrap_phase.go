package main

import (
	"context"
	"encoding/json"

	"github.com/overthinkos/overthink/charly/plugin/sdk"
)

// runBootstrapPhase invokes every PhaseBootstrap provider's OpBootstrap on the raw project config
// bytes (F9), BEFORE config validation/migration, threading each provider's returned (possibly
// transformed) bytes to the next — the migrate (M15) + egress (M16) enabler: a bootstrap-phase
// plugin runs before the schema gate accepts the config (migrate rewrites a stale config's raw
// bytes here; the kernel never needs a validated config to migrate it). Bootstrap providers are
// COMPILED-IN (in-proc — no validated config exists yet to discover an out-of-process source), so
// this NEVER re-enters the validated-config load (the F4 connect-cycle hazard avoided by
// construction). A no-op bootstrap plugin returns the bytes unchanged. Returns the bytes after all
// bootstrap transforms (the input unchanged when no bootstrap plugin is registered).
func runBootstrapPhase(data []byte) []byte {
	return runBootstrapPhaseWith(data, providerRegistry.providersInPhase(sdk.PhaseBootstrap))
}

// runBootstrapPhaseWith is the injectable core of runBootstrapPhase — it invokes each given
// bootstrap provider's OpBootstrap on data, threading transforms. Split out so a test can drive a
// fixed provider list WITHOUT registering into the global registry (which would make the hot-path
// runBootstrapPhase transform every other test's config).
func runBootstrapPhaseWith(data []byte, providers []Provider) []byte {
	for _, p := range providers {
		params, err := marshalJSON(map[string]string{"config": string(data)})
		if err != nil {
			continue
		}
		res, err := p.Invoke(context.Background(), &Operation{Reserved: p.Reserved(), Op: sdk.OpBootstrap, Params: params})
		if err != nil || res == nil || len(res.JSON) == 0 {
			continue
		}
		var reply struct {
			Config string `json:"config"`
		}
		if json.Unmarshal(res.JSON, &reply) == nil && reply.Config != "" {
			data = []byte(reply.Config)
		}
	}
	return data
}
