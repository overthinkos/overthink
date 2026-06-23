package main

import (
	"context"
	"fmt"

	packageplugin "github.com/overthinkos/overthink/charly/plugin/builtins/package"
	"github.com/overthinkos/overthink/charly/plugin/builtins/package/params"
)

// packageVerb is the BUILT-IN `package` plugin: the SECOND typed-step state-provision
// verb (after service), but SIMPLER — no PriorEnabled teardown-restore state. It is
// THREE-NATURED:
//
//   - CheckVerbProvider (do:assert) — RunVerb dispatches the rpm/dpkg/pacman presence
//     probe IN-PROCESS via the live *Runner (r.runPackage), which cannot cross the wire.
//     Authored as `check: … / plugin: package / plugin_input: {package, installed,
//     version, package_map}`, dispatched via runPluginVerb after the host validates
//     plugin_input against the served #PackageInput.
//   - TypedStepProvider (do:act, build/deploy install timeline) — like service (and unlike
//     the RenderProvisionScript verbs user/unix_group/kernel-param/mount), package's act
//     lowers into a TYPED SystemPackagesStep whose Reverse() records the LOAD-BEARING
//     reversals (ReverseOpPackageRemove + ReverseOpCoprDisable). A RenderProvisionScript
//     shell string would DROP those, so compileActOp resolves this provider and returns
//     ConstructStep — the typed step flows through the SAME SystemPackagesStep.Emit{OCI,
//     Local,VM} + Reverse() as the now-removed VerbCatalog["package"].LowersTo lowering did.
//   - ProvisionActor (do:act, runtime/opt-in) — RenderProvisionScript renders the
//     dnf/apt/pacman install for a `run: {plugin: package}` step the check Runner executes
//     LIVE (runProvisionAct → resolveProvisionScript) AND the box-build emitTasks `case
//     "plugin"` seam. This is NOT the build/deploy install path (that is the typed step
//     above) — it is the runtime/box-build act.
//
// The verb left the closed #Op/spec.OpVerbs; `package`/`installed`/`version`/`package_map`
// (read ONLY by the `package` verb) MOVED out of #Op into #PackageInput. The SHARED
// `exclude_distro` modifier (read by the generic runOne skip filter for EVERY verb) STAYS
// on #Op. Every half decodes the typed plugin_input (params.PackageInput, generated from
// the unit's schema/package.cue) — never a hand-parsed map, never the removed
// Op.Package/Op.Installed/Op.Versions/Op.PackageMap fields.
//
// It embeds builtinVerbBase, which supplies Class()=ClassVerb and the in-proc-only Invoke
// stub (an in-proc verb never serves itself over the wire — Invoke errors loudly rather
// than silently dropping the *Runner).
type packageVerb struct{ builtinVerbBase }

func (packageVerb) Reserved() string { return "package" }

// RunVerb (the do:assert half) decodes plugin_input and runs the rpm/dpkg/pacman presence
// probe via the live *Runner; the impl stays in r.runPackage (checkrun_verbs.go).
func (packageVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	var in params.PackageInput
	decodePluginInput(op.PluginInput, &in)
	return r.runPackage(ctx, op, in.Package, in.PackageMap, in.Installed, in.Versions)
}

// LowersTo names the InstallPlan step kind package's act lowers into — the role of the
// now-removed VerbCatalog["package"].LowersTo field, now owned by the provider.
func (packageVerb) LowersTo() StepKind { return StepKindSystemPackages }

// ConstructStep (the do:act build/deploy half) decodes plugin_input and builds the
// SystemPackagesStep EXACTLY as compileActOp built it before the extraction — install the
// (cross-distro-resolved) package via the image's primary format at PhaseInstall;
// SystemPackagesStep.Reverse() then records ReverseOpPackageRemove (+ ReverseOpCoprDisable
// for a copr repo). Repos/Copr/Options come from the top-level package cascade
// (compileSystemPackageSteps), NOT a per-op `run: {package}` step — so they are not set
// here, matching the pre-extraction lowering.
func (packageVerb) ConstructStep(op *Op, _ *Candy, img *ResolvedBox) InstallStep {
	var in params.PackageInput
	decodePluginInput(op.PluginInput, &in)
	return &SystemPackagesStep{
		Format:   img.Pkg,
		Phase:    PhaseInstall,
		Packages: []string{resolvePackageName(in.Package, in.PackageMap, img.Tags)},
	}
}

// RenderProvisionScript (the do:act runtime/box-build half) decodes plugin_input and
// renders the install of the package via whichever package manager the LIVE target
// carries; the name is cross-distro-resolved exactly as the assert path resolves it. ok is
// always true — a package act always has an install form. This is the runtime/box-build
// act path (runProvisionAct → resolveProvisionScript, and the box-build emitTasks `case
// "plugin"` seam); the build/deploy install timeline uses the typed ConstructStep above,
// not this shell string.
func (packageVerb) RenderProvisionScript(op *Op, distros []string) (string, bool) {
	var in params.PackageInput
	decodePluginInput(op.PluginInput, &in)
	name := shellSingleQuote(resolvePackageName(in.Package, in.PackageMap, distros))
	return fmt.Sprintf(`if command -v dnf >/dev/null 2>&1; then dnf install -y %[1]s; `+
		`elif command -v apt-get >/dev/null 2>&1; then apt-get update && apt-get install -y %[1]s; `+
		`elif command -v pacman >/dev/null 2>&1; then pacman -S --noconfirm %[1]s; `+
		`else echo "no supported package manager" >&2; exit 1; fi`, name), true
}

func init() {
	RegisterBuiltinPluginUnit(PluginUnit{
		Providers: []Provider{packageVerb{}},
		Schema:    PluginSchema{CueSource: packageplugin.Schema(), InputDefs: packageplugin.InputDefs},
	})
}
