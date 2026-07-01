package main

import (
	"strings"
	"testing"
)

// Tests for build_target_oci.go.
//
// We feed OCITarget synthetic InstallPlans and verify it emits the
// expected directive shapes. These are unit tests over the IR → Dockerfile
// translation; they don't cover the BuildDeployPlan compiler side (which
// has its own tests).

func TestOCITargetEmitShellHook(t *testing.T) {
	tgt := &OCITarget{}
	plan := &InstallPlan{Candy: "uv", Steps: []InstallStep{
		&ShellHookStep{
			CandyName: "uv",
			EnvVars: map[string]string{
				"UV_INSTALL_DIR": "/usr/local/bin",
			},
			PathAdd: []string{"$HOME/.cargo/bin"},
		},
	}}
	if err := tgt.Emit([]*InstallPlan{plan}, EmitOpts{}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	got := tgt.String()
	if !strings.Contains(got, `ENV UV_INSTALL_DIR="/usr/local/bin"`) {
		t.Errorf("missing ENV var: %s", got)
	}
	if !strings.Contains(got, "ENV PATH=$HOME/.cargo/bin:$PATH") {
		t.Errorf("missing PATH prepend: %s", got)
	}
	if !strings.Contains(got, "# Layer: uv") {
		t.Errorf("missing layer header: %s", got)
	}
}

func TestOCITargetEmitSystemPackagesWithLegacyTemplate(t *testing.T) {
	// Legacy InstallTemplate set; PhaseTemplate returns it for (install, container).
	distro := &DistroDef{
		Format: map[string]*FormatDef{
			"rpm": {
				InstallTemplate: "RUN dnf install -y {{join .Packages \" \"}}\n",
			},
		},
	}
	tgt := &OCITarget{DistroDef: distro}
	plan := &InstallPlan{Candy: "ripgrep", Steps: []InstallStep{
		&SystemPackagesStep{
			Format:   "rpm",
			Phase:    PhaseInstall,
			Packages: []string{"ripgrep"},
			RawInstallContext: map[string]any{
				"package": []any{"ripgrep"},
			},
		},
	}}
	if err := tgt.Emit([]*InstallPlan{plan}, EmitOpts{}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	got := tgt.String()
	if !strings.Contains(got, "dnf install -y ripgrep") {
		t.Errorf("legacy template not rendered: %s", got)
	}
}

func TestOCITargetEmitSystemPackagesPrefersNewPhases(t *testing.T) {
	// Both legacy and new path set; new path must win.
	distro := &DistroDef{
		Format: map[string]*FormatDef{
			"rpm": {
				InstallTemplate: "RUN legacy-install\n",
				Phases: &PhaseSet{
					Install: &PhaseTemplates{
						Container: "RUN new-install {{join .Packages \" \"}}\n",
					},
				},
			},
		},
	}
	tgt := &OCITarget{DistroDef: distro}
	plan := &InstallPlan{Candy: "foo", Steps: []InstallStep{
		&SystemPackagesStep{
			Format:   "rpm",
			Phase:    PhaseInstall,
			Packages: []string{"foo"},
			RawInstallContext: map[string]any{
				"package": []any{"foo"},
			},
		},
	}}
	if err := tgt.Emit([]*InstallPlan{plan}, EmitOpts{}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	got := tgt.String()
	if !strings.Contains(got, "new-install foo") {
		t.Errorf("expected new phase template to win, got: %s", got)
	}
	if strings.Contains(got, "legacy-install") {
		t.Errorf("legacy template leaked despite new phases path: %s", got)
	}
}

// TestOCITargetEmitBuilderInlineViaPlugin drives the FULL real chain the C1.3 externalization
// introduces for an INLINE (cargo) builder: BuilderStep → OCITarget.Emit → emitStep →
// pluginEmitStepWords[Builder]="builder" → spliceClassStepEmit("builder") → the compiled-in
// candy/plugin-installstep OpEmit → emitViaHostBuild → HostBuild("step-emit",{Word:"builder"}) →
// stepEmitBuilder (the in-core host build engine on the in-proc reverse channel) → inline render.
// It asserts the byte-equivalent `cargo install` output the former in-proc OCITarget builder
// build-emit produced — the test FAILS without this change (there is no in-proc Builder StepProvider;
// the plugin must serve step:builder and the host must render via the step-emit seam). This is the exact
// in-proc chain a pod overlay with an inline-builder add_candy runs host-side.
func TestOCITargetEmitBuilderInlineViaPlugin(t *testing.T) {
	bc := &BuilderConfig{Builder: map[string]*BuilderDef{
		"cargo": {Inline: true, InstallTemplate: "RUN cargo install --path .\n"},
	}}
	gen := &Generator{Candies: map[string]*Candy{"mytool": {Name: "mytool"}}}
	tgt := &OCITarget{
		BuilderConfig: bc,
		Box:           &ResolvedBox{UID: 1000, GID: 1000},
		Generator:     gen,
	}
	plan := &InstallPlan{Candy: "mytool", Steps: []InstallStep{
		&BuilderStep{Builder: "cargo", CandyName: "mytool", Phase: PhaseInstall},
	}}
	if err := tgt.Emit([]*InstallPlan{plan}, EmitOpts{}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	got := tgt.String()
	if !strings.Contains(got, "USER 1000") {
		t.Errorf("inline builder must switch USER to the image user via the plugin chain: %s", got)
	}
	if !strings.Contains(got, "cargo install --path .") {
		t.Errorf("inline builder template not rendered via the step:builder plugin chain: %s", got)
	}
}

// TestOCITargetEmitBuilderMultiStageViaPlugin drives the FULL real chain for a MULTI-STAGE
// (pixi/npm/aur) builder — the richer render that goes through Generator.buildStageContext +
// StageTemplate. Same dispatch path as the inline test (through the compiled-in plugin's OpEmit and
// the in-proc HostBuild), proving stepEmitBuilder reaches the threaded Generator/BuilderConfig/Box
// build engine (buildEngineContext) and emits the `FROM <builder> AS <stage>` stage the pod-overlay
// build needs.
func TestOCITargetEmitBuilderMultiStageViaPlugin(t *testing.T) {
	bc := &BuilderConfig{Builder: map[string]*BuilderDef{
		"pixi": {StageTemplate: "FROM {{.BuilderRef}} AS {{.StageName}}\nRUN pixi install\n"},
	}}
	gen := &Generator{Candies: map[string]*Candy{"mytool": {Name: "mytool"}}}
	tgt := &OCITarget{
		BuilderConfig: bc,
		Box:           &ResolvedBox{UID: 1000, GID: 1000, Builder: map[string]string{"pixi": "ghcr.io/x/builder:latest"}},
		Generator:     gen,
	}
	plan := &InstallPlan{Candy: "mytool", Steps: []InstallStep{
		&BuilderStep{Builder: "pixi", CandyName: "mytool", Phase: PhaseInstall},
	}}
	if err := tgt.Emit([]*InstallPlan{plan}, EmitOpts{}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	got := tgt.String()
	if !strings.Contains(got, "FROM ghcr.io/x/builder:latest AS mytool-pixi-build") {
		t.Errorf("multi-stage builder FROM stage not rendered via the step:builder plugin chain: %s", got)
	}
	if !strings.Contains(got, "RUN pixi install") {
		t.Errorf("multi-stage builder body not rendered via the step:builder plugin chain: %s", got)
	}
}

func TestOCITargetSkipsVenueSkip(t *testing.T) {
	// A step with VenueSkip should be elided entirely.
	tgt := &OCITarget{}
	plan := &InstallPlan{Candy: "x", Steps: []InstallStep{
		&fakeSkipStep{},
	}}
	if err := tgt.Emit([]*InstallPlan{plan}, EmitOpts{}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	got := tgt.String()
	if strings.Contains(got, "FAKE") {
		t.Errorf("skip step was rendered: %s", got)
	}
}

func TestOCITargetEmitRepoChange(t *testing.T) {
	tgt := &OCITarget{}
	plan := &InstallPlan{Candy: "rpmfusion", Steps: []InstallStep{
		&RepoChangeStep{
			Format:  "rpm",
			File:    "/etc/yum.repos.d/rpmfusion-free.repo",
			Content: "[rpmfusion-free]\nname=test",
		},
	}}
	if err := tgt.Emit([]*InstallPlan{plan}, EmitOpts{}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	got := tgt.String()
	if !strings.Contains(got, "/etc/yum.repos.d/rpmfusion-free.repo") {
		t.Errorf("missing repo file path: %s", got)
	}
	if !strings.Contains(got, "[rpmfusion-free]") {
		t.Errorf("missing repo content: %s", got)
	}
}

// fakeSkipStep is a synthetic InstallStep used to verify VenueSkip
// elision. Returns Venue=VenueSkip and marker content in its Kind.
type fakeSkipStep struct{}

func (f *fakeSkipStep) Kind() StepKind       { return "FAKE" }
func (f *fakeSkipStep) Scope() Scope         { return ScopeUser }
func (f *fakeSkipStep) Venue() Venue         { return VenueSkip }
func (f *fakeSkipStep) RequiresGate() Gate   { return GateNone }
func (f *fakeSkipStep) Reverse() []ReverseOp { return nil }

// TestOCITargetLookupCandyRemoteQualifiedKey guards the add_candy-on-pod overlay
// build: a REMOTE add_candy candy (fetched via ResolveOpts.ExtraCandyRefs) is keyed
// in Generator.Candies under its fully-qualified ref, while the compiled plan step's
// CandyName is the candy's bare intrinsic name. lookupCandy must resolve the bare
// name to the qualified-key candy, or OCITarget.Emit fails with
// `task emit: candy "<name>" not found`. Regression for the add_candy-on-pod-overlay
// "candy not found" build failure.
func TestOCITargetLookupCandyRemoteQualifiedKey(t *testing.T) {
	gen := &Generator{Candies: map[string]*Candy{
		"github.com/org/repo/candy/marker": {Name: "marker"},
		"local-layer":                      {Name: "local-layer"},
	}}
	tgt := &OCITarget{Generator: gen}

	// Exact (local) key — bare == .Name — still resolves directly.
	if c := tgt.lookupCandy("local-layer"); c == nil || c.Name != "local-layer" {
		t.Fatalf("local-layer: got %v, want .Name=local-layer", c)
	}
	// Bare name resolves the qualified-key remote candy (the regression this fix closes).
	if c := tgt.lookupCandy("marker"); c == nil || c.Name != "marker" {
		t.Fatalf("marker bare-name lookup returned %v; qualified-key .Name fallback is broken", c)
	}
	// An unknown name is still nil (no accidental match).
	if c := tgt.lookupCandy("nonexistent"); c != nil {
		t.Fatalf("nonexistent: want nil, got %v", c)
	}
}
