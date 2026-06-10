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
			RawInstallContext: map[string]interface{}{
				"package": []interface{}{"ripgrep"},
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
			RawInstallContext: map[string]interface{}{
				"package": []interface{}{"foo"},
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
