package main

import (
	"context"
	"fmt"

	serviceplugin "github.com/overthinkos/overthink/charly/plugin/builtins/service"
	"github.com/overthinkos/overthink/charly/plugin/builtins/service/params"
)

// serviceVerb is the BUILT-IN `service` plugin: the TYPED-STEP-OUTLIER state-provision
// verb. It is THREE-NATURED:
//
//   - CheckVerbProvider (do:assert) — RunVerb dispatches the supervisorctl/systemctl
//     probe IN-PROCESS via the live *Runner (r.runService), which cannot cross the wire.
//     Authored as `check: … / plugin: service / plugin_input: {service, running, enabled}`,
//     dispatched via runPluginVerb after the host validates plugin_input against the
//     served #ServiceInput.
//   - TypedStepProvider (do:act, build/deploy install timeline) — unlike the
//     RenderProvisionScript verbs (user/unix_group/kernel-param/mount), service's act
//     lowers into a TYPED ServicePackagedStep whose Reverse() records the LOAD-BEARING
//     reversals (ReverseOpServiceDisable / RestoreEnabled / RemoveDropin). A
//     RenderProvisionScript shell string would DROP those, so compileActOp resolves this
//     provider and returns ConstructStep — the typed step flows through the SAME
//     ServicePackagedStep.Emit{OCI,Local,VM} + Reverse() as before the extraction.
//   - ProvisionActor (do:act, runtime/opt-in) — RenderProvisionScript renders the
//     systemctl/supervisorctl enable for a `run: {plugin: service}` step the check Runner
//     executes LIVE (runProvisionAct → resolveProvisionScript). This is NOT the
//     build/deploy install path (that is the typed step above) — it is the runtime act.
//
// The verb left the closed #Op/spec.OpVerbs; `service`/`running`/`enabled` (read ONLY by
// the `service` verb) MOVED out of #Op into #ServiceInput. Every half decodes the typed
// plugin_input (params.ServiceInput, generated from the unit's schema/service.cue) —
// never a hand-parsed map, never the removed Op.Service/Op.Running/Op.Enabled fields.
//
// It embeds builtinVerbBase, which supplies Class()=ClassVerb and the in-proc-only Invoke
// stub (an in-proc verb never serves itself over the wire — Invoke errors loudly rather
// than silently dropping the *Runner).
type serviceVerb struct{ builtinVerbBase }

func (serviceVerb) Reserved() string { return "service" }

// RunVerb (the do:assert half) decodes plugin_input and runs the supervisorctl/systemctl
// probe via the live *Runner; the impl stays in r.runService (checkrun_verbs.go).
func (serviceVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	var in params.ServiceInput
	decodePluginInput(op.PluginInput, &in)
	return r.runService(ctx, op, in.Service, in.Running, in.Enabled)
}

// LowersTo names the InstallPlan step kind service's act lowers into — the former
// VerbCatalog["service"].LowersTo, now owned by the provider.
func (serviceVerb) LowersTo() StepKind { return StepKindServicePackaged }

// ConstructStep (the do:act build/deploy half) decodes plugin_input and builds the
// ServicePackagedStep EXACTLY as compileActOp built it before the extraction — enable the
// named packaged unit at the op's resolved scope; ServicePackagedStep.Reverse() then
// records the load-bearing reversals (ReverseOpServiceDisable / RestoreEnabled /
// RemoveDropin). userDir is the op's resolved run-as (RunAs stays an #Op modifier).
func (serviceVerb) ConstructStep(op *Op, layer *Candy, img *ResolvedBox) InstallStep {
	var in params.ServiceInput
	decodePluginInput(op.PluginInput, &in)
	userDir, _ := resolveUserSpec(op.RunAs, img)
	return &ServicePackagedStep{
		Unit:        in.Service,
		TargetScope: opStepScope(userDir),
		Enable:      true,
		CandyName:   layer.Name,
	}
}

// RenderProvisionScript (the do:act runtime/opt-in half) decodes plugin_input and renders
// the enable + start of the unit under whichever init the LIVE target runs. ok is always
// true — a service act always has an enable form. This is the runtime act path
// (runProvisionAct → resolveProvisionScript); the build/deploy install timeline uses the
// typed ConstructStep above, not this shell string.
func (serviceVerb) RenderProvisionScript(op *Op, _ []string) (string, bool) {
	var in params.ServiceInput
	decodePluginInput(op.PluginInput, &in)
	svc := shellSingleQuote(in.Service)
	return fmt.Sprintf(`if command -v systemctl >/dev/null 2>&1; then systemctl enable --now %[1]s; `+
		`elif command -v supervisorctl >/dev/null 2>&1; then supervisorctl start %[1]s; `+
		`else echo "no service manager" >&2; exit 1; fi`, svc), true
}

func init() {
	RegisterBuiltinPluginUnit(PluginUnit{
		Providers: []Provider{serviceVerb{}},
		Schema:    PluginSchema{CueSource: serviceplugin.Schema(), InputDefs: serviceplugin.InputDefs},
	})
}
