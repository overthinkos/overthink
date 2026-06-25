package main

import (
	"context"
	"io/fs"
	"net/http"
	"time"

	"github.com/overthinkos/overthink/charly/internal/schemaconcat"
	"github.com/overthinkos/overthink/charly/plugin/kit"
)

// runnerCheckContext adapts the live *Runner to kit.CheckContext — the surface a
// HOST-COUPLED verb candy consumes. It is a wrapper (NOT methods on *Runner) because
// *Runner already has fields named Exec/Mode/HTTPClient/DialTimeout/Box/Instance/
// Distros; a method of the same name would collide. DeployExecutor satisfies
// kit.Executor structurally (identical RunCapture + Kind signatures), so Exec()
// returns r.Exec straight through.
type runnerCheckContext struct{ r *Runner }

func (c runnerCheckContext) Exec() kit.Executor         { return c.r.Exec }
func (c runnerCheckContext) HTTPClient() *http.Client   { return c.r.HTTPClient }
func (c runnerCheckContext) DialTimeout() time.Duration { return c.r.DialTimeout }
func (c runnerCheckContext) Box() string                { return c.r.Box }
func (c runnerCheckContext) Instance() string           { return c.r.Instance }
func (c runnerCheckContext) Distros() []string          { return c.r.Distros }
func (c runnerCheckContext) Mode() kit.RunMode {
	if c.r.Mode == RunModeBox {
		return kit.ModeBox
	}
	return kit.ModeLive
}

// kitVerbAdapter wraps a COMPILED-IN host-coupled verb candy's kit.CheckVerbProvider
// as a package-main CheckVerbProvider, so runOne dispatches it through the SAME
// providerRegistry path as an in-charly-module verb. It passes the live *Runner as a
// kit.CheckContext and converts the returned kit.Result back to a CheckResult
// (stamping Op + Verb). It embeds builtinVerbBase for Class()=ClassVerb + the
// in-proc-only Invoke stub — a kit verb is in-process only (RunVerb needs the live
// *Runner, which cannot cross a process boundary).
type kitVerbAdapter struct {
	builtinVerbBase
	kv kit.CheckVerbProvider
}

func (a kitVerbAdapter) Reserved() string { return a.kv.Reserved() }

func (a kitVerbAdapter) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	res := a.kv.RunVerb(ctx, runnerCheckContext{r: r}, op)
	return CheckResult{
		Op:            op,
		Verb:          a.kv.Reserved(),
		Status:        kitStatusToCheck(res.Status),
		Message:       res.Message,
		CapturedValue: res.CapturedValue,
	}
}

func kitStatusToCheck(s kit.Status) CheckStatus {
	switch s {
	case kit.StatusFail:
		return TestFail
	case kit.StatusSkip:
		return TestSkip
	default:
		return TestPass
	}
}

// registerCompiledCheckVerb registers a COMPILED-IN host-coupled verb candy: it wraps
// the candy's kit.CheckVerbProvider in a kitVerbAdapter and registers it (with the
// candy's CUE schema) through the SAME RegisterBuiltinPluginUnit gate an
// in-charly-module verb uses (schema gated at process start, origin "builtin", so the
// coexist switch treats it like any compiled-in plugin). Called from the generated
// plugins_generated.go for a kit-shape candy named in charly.yml compiled_plugins.
// Distinct from registerCompiledPlugin (the pb/dual-placement path) because a kit verb
// is in-proc-only. The candy passes its RAW schema embed.FS + dir + InputDefs; charly
// concatenates here via schemaconcat (the candy cannot import internal/schemaconcat) —
// the SAME concat contract a builtin/external schema goes through (R3). A read/concat
// failure is a build-time invariant violation (panic, like loadBuiltinPluginUnits).
func registerCompiledCheckVerb(kv kit.CheckVerbProvider, schemaFS fs.FS, schemaDir string, inputDefs map[string]string) {
	cueSource, _, err := schemaconcat.ConcatSchema(schemaFS, schemaDir, nil)
	if err != nil {
		panic("registerCompiledCheckVerb " + kv.Reserved() + ": concat schema: " + err.Error())
	}
	RegisterBuiltinPluginUnit(PluginUnit{
		Providers: []Provider{kitVerbAdapter{kv: kv}},
		Schema:    PluginSchema{CueSource: cueSource, InputDefs: inputDefs},
	})
}
