package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCompileLocalPkgStep verifies a layer's `localpkg:` field compiles into a
// single LocalPkgInstallStep carrying the ref + anchors, and that a layer with
// no localpkg: compiles to nothing.
func TestCompileLocalPkgStep(t *testing.T) {
	if step := compileLocalPkgStep(&Layer{Name: "no-pkg"}); step != nil {
		t.Errorf("layer with no localpkg: should compile to nil, got %T", step)
	}

	l := &Layer{Name: "ov", SourceDir: "/layers/ov", localpkg: "pkg/arch"}
	step := compileLocalPkgStep(l)
	if step == nil {
		t.Fatal("compileLocalPkgStep returned nil for a layer with localpkg:")
	}
	pkg, ok := step.(*LocalPkgInstallStep)
	if !ok {
		t.Fatalf("compileLocalPkgStep returned %T, want *LocalPkgInstallStep", step)
	}
	if pkg.PkgbuildRef != "pkg/arch" || pkg.LayerName != "ov" || pkg.LayerDir != "/layers/ov" {
		t.Errorf("step fields = %+v", pkg)
	}
	if pkg.ProjectDir == "" {
		t.Error("ProjectDir should be set from os.Getwd()")
	}
}

// TestLocalPkgInstallStepIR exercises the IR contract: kind, scope (system),
// venue (host-native), gate (none), reverse (no ledger ops — like apk).
func TestLocalPkgInstallStepIR(t *testing.T) {
	s := &LocalPkgInstallStep{PkgbuildRef: "pkg/arch", LayerName: "ov"}
	if s.Kind() != StepKindLocalPkgInstall {
		t.Errorf("Kind() = %q, want %q", s.Kind(), StepKindLocalPkgInstall)
	}
	if s.Scope() != ScopeSystem {
		t.Errorf("Scope() = %v, want ScopeSystem", s.Scope())
	}
	if s.Venue() != VenueHostNative {
		t.Errorf("Venue() = %v, want VenueHostNative", s.Venue())
	}
	if s.RequiresGate() != GateNone {
		t.Errorf("RequiresGate() = %v, want GateNone", s.RequiresGate())
	}
	if s.Reverse() != nil {
		t.Errorf("Reverse() = %v, want nil (pacman package is the substrate's own, not ledger-reversed)", s.Reverse())
	}
}

// TestBuildDeployPlanLocalPkgOrdering proves the localpkg step is emitted BEFORE
// the layer's task steps in the compiled plan — load-bearing so the ov layer's
// pacman-aware cmd: gate sees overthink-git already installed and does nothing
// (instead of curling a /usr/local/bin/ov that shadows /usr/bin/ov).
func TestBuildDeployPlanLocalPkgOrdering(t *testing.T) {
	l := &Layer{
		Name:     "ov",
		localpkg: "pkg/arch",
		tasks: []Task{
			{Cmd: "echo install ov", User: "root"},
		},
	}
	img := &ResolvedImage{Name: "host-adhoc", Home: "/root", User: "root"}
	plan, err := BuildDeployPlan(l, img, HostContext{Target: "host", Distro: "arch"})
	if err != nil {
		t.Fatalf("BuildDeployPlan: %v", err)
	}
	pkgIdx, taskIdx := -1, -1
	for i, step := range plan.Steps {
		switch step.(type) {
		case *LocalPkgInstallStep:
			if pkgIdx < 0 {
				pkgIdx = i
			}
		case *TaskStep:
			if taskIdx < 0 {
				taskIdx = i
			}
		}
	}
	if pkgIdx < 0 {
		t.Fatal("no LocalPkgInstallStep in the compiled plan")
	}
	if taskIdx < 0 {
		t.Fatal("no TaskStep in the compiled plan")
	}
	if pkgIdx > taskIdx {
		t.Errorf("localpkg step (idx %d) must precede the layer's task steps (idx %d) so the cmd: gate sees the installed package", pkgIdx, taskIdx)
	}
}

// TestOCITargetSkipsLocalPkg proves the localpkg step is SKIPPED at image build
// (no makepkg in a container) — emitStep returns nil and emits nothing.
func TestOCITargetSkipsLocalPkg(t *testing.T) {
	tgt := &OCITarget{}
	step := &LocalPkgInstallStep{PkgbuildRef: "pkg/arch", LayerName: "ov"}
	if err := tgt.emitStep(step, &InstallPlan{}); err != nil {
		t.Fatalf("OCITarget.emitStep(LocalPkgInstallStep) = %v, want nil (skip)", err)
	}
	if tgt.buf.Len() != 0 {
		t.Errorf("OCITarget emitted %q for a localpkg step; should emit nothing", tgt.buf.String())
	}
}

// TestResolveLocalPkgDir covers PKGBUILD-location resolution across all four
// branches: absolute, layer-relative, project-relative, and the walk-up search
// (the operator path where `ov -C image/cachyos deploy add …` finds pkg/arch at
// the superproject root ../../pkg/arch). A missing PKGBUILD returns "".
func TestResolveLocalPkgDir(t *testing.T) {
	root := t.TempDir()
	// Lay out: <root>/pkg/arch/PKGBUILD  (superproject) and a nested project dir
	// <root>/image/cachyos that does NOT contain pkg/arch directly.
	pkgArch := filepath.Join(root, "pkg", "arch")
	if err := os.MkdirAll(pkgArch, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgArch, "PKGBUILD"), []byte("pkgname=overthink-git\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nestedProject := filepath.Join(root, "image", "cachyos")
	if err := os.MkdirAll(nestedProject, 0o755); err != nil {
		t.Fatal(err)
	}
	// A layer dir that bundles its OWN PKGBUILD (layer-relative branch).
	layerWithPkg := filepath.Join(root, "layers", "mytool")
	if err := os.MkdirAll(filepath.Join(layerWithPkg, "arch"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layerWithPkg, "arch", "PKGBUILD"), []byte("pkgname=mytool\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. Absolute ref.
	if got := resolveLocalPkgDir(pkgArch, "", ""); got != pkgArch {
		t.Errorf("absolute ref = %q, want %q", got, pkgArch)
	}
	// 2. Layer-relative.
	if got := resolveLocalPkgDir("arch", layerWithPkg, root); got != filepath.Join(layerWithPkg, "arch") {
		t.Errorf("layer-relative = %q, want %q", got, filepath.Join(layerWithPkg, "arch"))
	}
	// 3. Project-relative (project dir == superproject root).
	if got := resolveLocalPkgDir("pkg/arch", "/no/such/layer", root); got != pkgArch {
		t.Errorf("project-relative = %q, want %q", got, pkgArch)
	}
	// 4. Walk-up: project dir is the nested image/cachyos; pkg/arch is two levels up.
	if got := resolveLocalPkgDir("pkg/arch", "/no/such/layer", nestedProject); got != pkgArch {
		t.Errorf("walk-up = %q, want %q (must find the superproject pkg/arch from a nested project dir)", got, pkgArch)
	}
	// 5. Missing PKGBUILD → "".
	if got := resolveLocalPkgDir("does/not/exist", "/no/such/layer", nestedProject); got != "" {
		t.Errorf("missing PKGBUILD = %q, want empty (no-op fallback)", got)
	}
	// 6. Empty ref → "".
	if got := resolveLocalPkgDir("", layerWithPkg, root); got != "" {
		t.Errorf("empty ref = %q, want empty", got)
	}
}

// localPkgRecExec records RunSystem scripts + PutFile dests so the install-body
// tests can assert the transfer+pacman leg without a real venue.
type localPkgRecExec struct {
	systemScripts []string
	userScripts   []string
	putDests      []string
	pacman        string // canned `command -v pacman` probe answer ("yes"/"no")
}

func (e *localPkgRecExec) Venue() string { return "localpkg-rec://test" }
func (e *localPkgRecExec) RunSystem(_ context.Context, script string, _ EmitOpts) error {
	e.systemScripts = append(e.systemScripts, script)
	return nil
}
func (e *localPkgRecExec) RunUser(_ context.Context, script string, _ EmitOpts) error {
	e.userScripts = append(e.userScripts, script)
	return nil
}
func (e *localPkgRecExec) RunBuilder(context.Context, BuilderRunOpts) ([]byte, error) {
	return nil, nil
}
func (e *localPkgRecExec) PutFile(_ context.Context, _, remotePath string, _ uint32, _ bool, _ EmitOpts) error {
	e.putDests = append(e.putDests, remotePath)
	return nil
}
func (e *localPkgRecExec) GetFile(context.Context, string, bool, EmitOpts) ([]byte, error) {
	return nil, nil
}
func (e *localPkgRecExec) RunCapture(_ context.Context, _ string) (string, string, int, error) {
	if e.pacman == "" {
		return "no", "", 0, nil
	}
	return e.pacman, "", 0, nil
}
func (e *localPkgRecExec) Kind() string { return "localpkg-rec" }
func (e *localPkgRecExec) ResolveHome(context.Context, string) (string, error) {
	return "/home/guest", nil
}

// TestVenueHasPacman confirms the Arch gate probes the VENUE via command -v
// pacman, treating only an exact "yes" as pac-capable; DryRun assumes true.
func TestVenueHasPacman(t *testing.T) {
	yes := &localPkgRecExec{pacman: "yes"}
	if !venueHasPacman(context.Background(), yes, EmitOpts{}) {
		t.Error("venue reporting pacman present should gate true")
	}
	no := &localPkgRecExec{pacman: "no"}
	if venueHasPacman(context.Background(), no, EmitOpts{}) {
		t.Error("venue without pacman should gate false")
	}
	// DryRun assumes true regardless of the probe (planner shows what it WOULD do).
	if !venueHasPacman(context.Background(), no, EmitOpts{DryRun: true}) {
		t.Error("DryRun should assume pacman present")
	}
}

// TestExecLocalPkgInstall_SkipsNonArch proves a non-pac venue is a clean no-op:
// no makepkg, no transfer, no pacman — the layer's curl/COPY task installs ov.
func TestExecLocalPkgInstall_SkipsNonArch(t *testing.T) {
	exec := &localPkgRecExec{}
	s := &LocalPkgInstallStep{PkgbuildRef: "pkg/arch", LayerName: "ov", ProjectDir: t.TempDir()}
	if err := execLocalPkgInstall(context.Background(), exec, s, false /* arch */, "host", EmitOpts{}); err != nil {
		t.Fatalf("non-Arch should be a clean no-op, got %v", err)
	}
	if len(exec.systemScripts) != 0 || len(exec.putDests) != 0 {
		t.Errorf("non-Arch venue must not install anything: systemScripts=%v putDests=%v", exec.systemScripts, exec.putDests)
	}
}

// TestExecLocalPkgInstall_SkipsMissingPkgbuild proves a missing PKGBUILD on an
// Arch venue is ALSO a clean no-op (fallback to the layer's curl/COPY task) —
// not an error that aborts the deploy.
func TestExecLocalPkgInstall_SkipsMissingPkgbuild(t *testing.T) {
	exec := &localPkgRecExec{pacman: "yes"}
	s := &LocalPkgInstallStep{PkgbuildRef: "no/such/pkgbuild", LayerName: "ov", ProjectDir: t.TempDir()}
	if err := execLocalPkgInstall(context.Background(), exec, s, true /* arch */, "host", EmitOpts{}); err != nil {
		t.Fatalf("missing PKGBUILD should be a clean no-op, got %v", err)
	}
	if len(exec.systemScripts) != 0 || len(exec.putDests) != 0 {
		t.Errorf("missing PKGBUILD must not install anything: systemScripts=%v putDests=%v", exec.systemScripts, exec.putDests)
	}
}

// TestTransferAndPacmanInstall proves the shared transfer+install leg stages the
// dir, PutFiles each package, and pacman -U's the staging glob — venue-agnostic.
func TestTransferAndPacmanInstall(t *testing.T) {
	exec := &localPkgRecExec{}
	pkgs := []string{"/tmp/build/overthink-git-2026.155.0001-1-x86_64.pkg.tar.zst"}
	if err := transferAndPacmanInstall(context.Background(), exec, pkgs, EmitOpts{}); err != nil {
		t.Fatalf("transferAndPacmanInstall: %v", err)
	}
	if len(exec.putDests) != 1 || !strings.HasPrefix(exec.putDests[0], localPkgGuestStage) {
		t.Errorf("package not staged under %s: %v", localPkgGuestStage, exec.putDests)
	}
	if len(exec.systemScripts) != 1 || !strings.Contains(exec.systemScripts[0], "pacman -U --noconfirm "+localPkgGuestStage) {
		t.Errorf("missing pacman -U on the staging glob: %v", exec.systemScripts)
	}
	// No packages → error (caller bug, never a silent skip).
	if err := transferAndPacmanInstall(context.Background(), exec, nil, EmitOpts{}); err == nil {
		t.Error("transferAndPacmanInstall(nil) should error")
	}
}
