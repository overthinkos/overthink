package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testPacLocalPkgDef returns a LocalPkgDef mirroring build.yml's `pac.local_pkg`
// block — the config that drives the localpkg mechanism. Tests use it so they
// exercise the SAME config-driven path the loader produces, without parsing YAML.
func testPacLocalPkgDef() *LocalPkgDef {
	return &LocalPkgDef{
		PkgGlob:          "*.pkg.tar.zst",
		BuildTemplate:    "cd {{.SrcDir}} && PKGDEST={{.PkgDest}} makepkg -sf --noconfirm",
		InstallTemplate:  "pacman -U --noconfirm {{.StageDir}}/{{.Glob}}",
		ForeignQuery:     "pacman -Qmq",
		Probe:            "command -v pacman",
		DepConstraintOps: []string{">=", "<=", "=", ">", "<"},
		DepBuilder:       "aur",
	}
}

// testPacDistroDef returns a DistroDef whose `pac` format carries the localpkg
// contract — so compileLocalPkgStep resolves it the way it would from build.yml.
func testPacDistroDef() *DistroDef {
	return &DistroDef{
		Format: map[string]*FormatDef{
			"pac": {LocalPkg: testPacLocalPkgDef()},
		},
	}
}

// TestCompileLocalPkgStep verifies a layer's `localpkg:` field compiles into a
// single LocalPkgInstallStep carrying the ref + anchors, and the config-driven
// format/builder resolution; a layer with no localpkg: compiles to nothing.
func TestCompileLocalPkgStep(t *testing.T) {
	img := &ResolvedImage{
		Name:      "ov-host",
		Pkg:       "pac",
		DistroDef: testPacDistroDef(),
		Builder:   map[string]string{"aur": "ghcr.io/overthinkos/arch-builder:latest"},
	}
	hostCtx := HostContext{Target: "host", Distro: "arch"}
	if step := compileLocalPkgStep(&Layer{Name: "no-pkg"}, img, hostCtx); step != nil {
		t.Errorf("layer with no localpkg: should compile to nil, got %T", step)
	}

	l := &Layer{Name: "ov", SourceDir: "/layers/ov", localpkg: "pkg/arch"}
	step := compileLocalPkgStep(l, img, hostCtx)
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
	// Format + LocalPkg config resolved from the distro's pac format (config-driven).
	if pkg.Format != "pac" || pkg.LocalPkg == nil || pkg.LocalPkg.PkgGlob != "*.pkg.tar.zst" {
		t.Errorf("LocalPkg config not resolved from the pac format: Format=%q LocalPkg=%#v", pkg.Format, pkg.LocalPkg)
	}
	// BuilderImage is resolved for LocalPkg.DepBuilder (aur) via the image builder map.
	if pkg.BuilderImage != "ghcr.io/overthinkos/arch-builder:latest" {
		t.Errorf("BuilderImage = %q, want the image's aur builder", pkg.BuilderImage)
	}
	// No aur builder → BuilderImage left "" (dep-build skipped with a clear log).
	noBuilder := compileLocalPkgStep(l, &ResolvedImage{Name: "ov-host", Pkg: "pac", DistroDef: testPacDistroDef()}, hostCtx)
	if pkg, ok := noBuilder.(*LocalPkgInstallStep); !ok || pkg.BuilderImage != "" || pkg.LocalPkg == nil {
		t.Errorf("no aur builder should leave BuilderImage empty but keep LocalPkg, got %#v", noBuilder)
	}
	// Distro with no localpkg-capable format → LocalPkg nil (executor skips).
	noFmt := compileLocalPkgStep(l, &ResolvedImage{Name: "ov-host", Pkg: "rpm", DistroDef: &DistroDef{Format: map[string]*FormatDef{"rpm": {}}}}, hostCtx)
	if pkg, ok := noFmt.(*LocalPkgInstallStep); !ok || pkg.LocalPkg != nil || pkg.Format != "" {
		t.Errorf("distro without localpkg format should leave LocalPkg nil, got %#v", noFmt)
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
// tests can assert the transfer+install leg without a real venue.
type localPkgRecExec struct {
	systemScripts []string
	userScripts   []string
	putDests      []string
	probeYes      bool // canned answer for the config-driven package-manager probe
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
	// The probe script echoes "yes"/"no"; mirror that contract.
	if e.probeYes {
		return "yes", "", 0, nil
	}
	return "no", "", 0, nil
}
func (e *localPkgRecExec) Kind() string { return "localpkg-rec" }
func (e *localPkgRecExec) ResolveHome(context.Context, string) (string, error) {
	return "/home/guest", nil
}

// TestVenueHasPkgManager confirms the gate runs the format's config-driven probe
// (LocalPkgDef.Probe), treating only an exact "yes" as supported; DryRun assumes
// true; a nil LocalPkgDef gates false (never assume a target can take a package).
func TestVenueHasPkgManager(t *testing.T) {
	lp := testPacLocalPkgDef()
	yes := &localPkgRecExec{probeYes: true}
	if !venueHasPkgManager(context.Background(), yes, lp, EmitOpts{}) {
		t.Error("venue reporting the package manager present should gate true")
	}
	no := &localPkgRecExec{probeYes: false}
	if venueHasPkgManager(context.Background(), no, lp, EmitOpts{}) {
		t.Error("venue without the package manager should gate false")
	}
	// DryRun assumes true regardless of the probe (planner shows what it WOULD do).
	if !venueHasPkgManager(context.Background(), no, lp, EmitOpts{DryRun: true}) {
		t.Error("DryRun should assume the package manager present")
	}
	// Nil LocalPkgDef → false even on DryRun (no format config = nothing to do).
	if venueHasPkgManager(context.Background(), yes, nil, EmitOpts{DryRun: true}) {
		t.Error("nil LocalPkgDef should gate false")
	}
}

// TestExecLocalPkgInstall_SkipsUnsupported proves an unsupported venue is a
// clean no-op: no build, no transfer, no install — the layer's curl/COPY task
// installs it instead. `supported=false` is what the config-driven probe returns
// on a non-pac venue.
func TestExecLocalPkgInstall_SkipsUnsupported(t *testing.T) {
	exec := &localPkgRecExec{}
	s := &LocalPkgInstallStep{PkgbuildRef: "pkg/arch", LayerName: "ov", ProjectDir: t.TempDir(), Format: "pac", LocalPkg: testPacLocalPkgDef()}
	if err := execLocalPkgInstall(context.Background(), exec, s, false /* supported */, "host", nil, EmitOpts{}); err != nil {
		t.Fatalf("unsupported venue should be a clean no-op, got %v", err)
	}
	if len(exec.systemScripts) != 0 || len(exec.putDests) != 0 {
		t.Errorf("unsupported venue must not install anything: systemScripts=%v putDests=%v", exec.systemScripts, exec.putDests)
	}
}

// TestExecLocalPkgInstall_SkipsNilLocalPkg proves a step with no resolved
// LocalPkg config (target distro declares no localpkg-capable format) is a clean
// no-op even when the venue is reported supported.
func TestExecLocalPkgInstall_SkipsNilLocalPkg(t *testing.T) {
	exec := &localPkgRecExec{}
	s := &LocalPkgInstallStep{PkgbuildRef: "pkg/arch", LayerName: "ov", ProjectDir: t.TempDir()} // LocalPkg nil
	if err := execLocalPkgInstall(context.Background(), exec, s, true, "host", nil, EmitOpts{}); err != nil {
		t.Fatalf("nil LocalPkg should be a clean no-op, got %v", err)
	}
	if len(exec.systemScripts) != 0 || len(exec.putDests) != 0 {
		t.Errorf("nil LocalPkg must not install anything: systemScripts=%v putDests=%v", exec.systemScripts, exec.putDests)
	}
}

// TestExecLocalPkgInstall_SkipsMissingSource proves a missing source dir on a
// supported venue is ALSO a clean no-op (fallback to the layer's curl/COPY
// task) — not an error that aborts the deploy.
func TestExecLocalPkgInstall_SkipsMissingSource(t *testing.T) {
	exec := &localPkgRecExec{}
	s := &LocalPkgInstallStep{PkgbuildRef: "no/such/source", LayerName: "ov", ProjectDir: t.TempDir(), Format: "pac", LocalPkg: testPacLocalPkgDef()}
	if err := execLocalPkgInstall(context.Background(), exec, s, true /* supported */, "host", nil, EmitOpts{}); err != nil {
		t.Fatalf("missing source should be a clean no-op, got %v", err)
	}
	if len(exec.systemScripts) != 0 || len(exec.putDests) != 0 {
		t.Errorf("missing source must not install anything: systemScripts=%v putDests=%v", exec.systemScripts, exec.putDests)
	}
}

// TestTransferAndInstallPkgs proves the shared transfer+install leg stages the
// dir, PutFiles each package, and renders the format's CONFIG-DRIVEN install
// command (LocalPkgDef.InstallTemplate) against the staging glob — venue-agnostic.
func TestTransferAndInstallPkgs(t *testing.T) {
	exec := &localPkgRecExec{}
	lp := testPacLocalPkgDef()
	pkgs := []string{"/tmp/build/overthink-git-2026.155.0001-1-x86_64.pkg.tar.zst"}
	if err := transferAndInstallPkgs(context.Background(), exec, lp, pkgs, EmitOpts{}); err != nil {
		t.Fatalf("transferAndInstallPkgs: %v", err)
	}
	if len(exec.putDests) != 1 || !strings.HasPrefix(exec.putDests[0], localPkgGuestStage) {
		t.Errorf("package not staged under %s: %v", localPkgGuestStage, exec.putDests)
	}
	// The install command is rendered from the config template, not hardcoded.
	wantCmd := "pacman -U --noconfirm " + localPkgGuestStage + "/" + lp.PkgGlob
	if len(exec.systemScripts) != 1 || strings.TrimSpace(exec.systemScripts[0]) != wantCmd {
		t.Errorf("install command = %v, want rendered %q", exec.systemScripts, wantCmd)
	}
	// No packages → error (caller bug, never a silent skip).
	if err := transferAndInstallPkgs(context.Background(), exec, lp, nil, EmitOpts{}); err == nil {
		t.Error("transferAndInstallPkgs(nil pkgs) should error")
	}
	// Nil LocalPkgDef → error.
	if err := transferAndInstallPkgs(context.Background(), exec, nil, pkgs, EmitOpts{}); err == nil {
		t.Error("transferAndInstallPkgs(nil LocalPkgDef) should error")
	}
}

// TestStripDependConstraint covers the version-constraint-stripping cases the
// package-metadata parser relies on, using the format's config-driven operator
// set: each operator, bare name, and longest-op-wins (>= not >). NO hardcoded
// overthink-specific names — pure string logic.
func TestStripDependConstraint(t *testing.T) {
	ops := testPacLocalPkgDef().DepConstraintOps
	cases := []struct{ in, want string }{
		{"cloudflared-bin", "cloudflared-bin"},   // bare name
		{"glibc>=2.39", "glibc"},                 // >= stripped, not just >
		{"foo<=1.0", "foo"},                      // <= stripped, not just <
		{"bar=2.0", "bar"},                       // exact-version =
		{"baz>1", "baz"},                         // >
		{"qux<3", "qux"},                         // <
		{"  spaced >= 1 ", "spaced"},             // surrounding + interior whitespace
		{"gvisor-tap-vsock", "gvisor-tap-vsock"}, // hyphenated bare name
	}
	for _, c := range cases {
		if got := stripDependConstraint(c.in, ops); got != c.want {
			t.Errorf("stripDependConstraint(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// Empty ops set → no stripping (bare name returned as-is, whitespace trimmed).
	if got := stripDependConstraint("glibc>=2.39", nil); got != "glibc>=2.39" {
		t.Errorf("empty ops should not strip: got %q", got)
	}
}

// TestParsePkgInfoDepends proves the metadata parser extracts every `depend =`
// line's bare name, strips version constraints (via the config-driven op set),
// de-duplicates, ignores non-depend lines (pkgname, makedepend, optdepend,
// blank), and preserves order.
func TestParsePkgInfoDepends(t *testing.T) {
	ops := testPacLocalPkgDef().DepConstraintOps
	pkginfo := []byte(strings.Join([]string{
		"# Generated by makepkg",
		"pkgname = overthink-git",
		"pkgver = 2026.155.0001-1",
		"depend = cloudflared-bin",
		"depend = gvisor-tap-vsock>=0.7",
		"depend = git",
		"depend = cloudflared-bin", // duplicate — must collapse
		"makedepend = go",
		"optdepend = something: only optional",
		"",
	}, "\n"))
	got := parsePkgInfoDepends(pkginfo, ops)
	want := []string{"cloudflared-bin", "gvisor-tap-vsock", "git"}
	if len(got) != len(want) {
		t.Fatalf("parsePkgInfoDepends = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("dep[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

// TestPkgInfoDepends_InjectedReader proves pkgInfoDepends routes through the
// swappable pkgInfoReader var (no real bsdtar) and surfaces a reader error.
func TestPkgInfoDepends_InjectedReader(t *testing.T) {
	orig := pkgInfoReader
	defer func() { pkgInfoReader = orig }()
	ops := testPacLocalPkgDef().DepConstraintOps

	pkgInfoReader = func(string) ([]byte, error) {
		return []byte("depend = cloudflared-bin\ndepend = gvisor-tap-vsock=0.7\n"), nil
	}
	got, err := pkgInfoDepends("/tmp/overthink-git.pkg.tar.zst", ops)
	if err != nil {
		t.Fatalf("pkgInfoDepends: %v", err)
	}
	if len(got) != 2 || got[0] != "cloudflared-bin" || got[1] != "gvisor-tap-vsock" {
		t.Errorf("pkgInfoDepends = %v, want [cloudflared-bin gvisor-tap-vsock]", got)
	}

	pkgInfoReader = func(string) ([]byte, error) { return nil, os.ErrPermission }
	if _, err := pkgInfoDepends("/tmp/x.pkg.tar.zst", ops); err == nil {
		t.Error("pkgInfoDepends should surface the reader error")
	}
}

// TestBuilderOnlyDeps proves the builder-only intersection: a built package's
// depends ∩ the host's foreign (builder-installed) packages. Repo deps (not in
// the foreign set) are excluded; builder deps are kept in depends-order. The
// discriminator is purely the foreign set — NO hardcoded package names.
func TestBuilderOnlyDeps(t *testing.T) {
	depends := []string{"cloudflared-bin", "git", "gvisor-tap-vsock", "glibc"}
	foreign := map[string]bool{
		"cloudflared-bin":  true, // AUR (foreign)
		"gvisor-tap-vsock": true, // AUR (foreign)
		"yay":              true, // foreign but not a dep of this pkg
		// git + glibc are repo packages — absent from the foreign set.
	}
	got := builderOnlyDeps(depends, foreign)
	want := []string{"cloudflared-bin", "gvisor-tap-vsock"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("builderOnlyDeps = %v, want %v (order = depends order, repo deps excluded)", got, want)
	}

	// No depends → nil; no foreign overlap → nil.
	if got := builderOnlyDeps(nil, foreign); got != nil {
		t.Errorf("builderOnlyDeps(nil, …) = %v, want nil", got)
	}
	if got := builderOnlyDeps([]string{"git", "glibc"}, foreign); got != nil {
		t.Errorf("builderOnlyDeps(repo-only) = %v, want nil", got)
	}
}

// TestHostForeignPkgs_InjectedRunner proves hostForeignPkgs runs the
// config-driven foreign-package query (LocalPkgDef.ForeignQuery) via the
// swappable foreignPkgRunner var (no real pacman), trims blanks, surfaces a
// runner error, and treats an empty query as the empty set.
func TestHostForeignPkgs_InjectedRunner(t *testing.T) {
	orig := foreignPkgRunner
	defer func() { foreignPkgRunner = orig }()

	var gotQuery string
	foreignPkgRunner = func(query string) ([]byte, error) {
		gotQuery = query
		return []byte("cloudflared-bin\ngvisor-tap-vsock\n\n  yay  \n"), nil
	}
	set, err := hostForeignPkgs("pacman -Qmq")
	if err != nil {
		t.Fatalf("hostForeignPkgs: %v", err)
	}
	if gotQuery != "pacman -Qmq" {
		t.Errorf("foreign query = %q, want the config-driven command", gotQuery)
	}
	for _, want := range []string{"cloudflared-bin", "gvisor-tap-vsock", "yay"} {
		if !set[want] {
			t.Errorf("foreign set missing %q: %v", want, set)
		}
	}
	if set[""] {
		t.Error("foreign set must not contain an empty-string entry from a blank line")
	}

	// Empty query → empty set, no error, no runner call.
	called := false
	foreignPkgRunner = func(string) ([]byte, error) { called = true; return nil, nil }
	if s, err := hostForeignPkgs(""); err != nil || len(s) != 0 || called {
		t.Errorf("empty query should yield empty set with no runner call: set=%v err=%v called=%v", s, err, called)
	}

	foreignPkgRunner = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	if _, err := hostForeignPkgs("pacman -Qmq"); err == nil {
		t.Error("hostForeignPkgs should surface the runner error")
	}
}

// TestBuildLocalPkgOnHost_DryRunAndEmpty proves the build leg renders the
// CONFIG-DRIVEN build template (no hardcoded makepkg) and honors DryRun (no
// shell-out), and that a nil/empty config errors rather than silently building.
func TestBuildLocalPkgOnHost_DryRunAndEmpty(t *testing.T) {
	lp := testPacLocalPkgDef()
	// DryRun: renders the template (proving config-driven) but never runs it.
	if pkgs, err := buildLocalPkgOnHost(context.Background(), lp, "/src/pkg/arch", EmitOpts{DryRun: true}); err != nil || pkgs != nil {
		t.Errorf("dry-run = (%v, %v), want (nil, nil)", pkgs, err)
	}
	// Nil LocalPkgDef → error.
	if _, err := buildLocalPkgOnHost(context.Background(), nil, "/src", EmitOpts{DryRun: true}); err == nil {
		t.Error("buildLocalPkgOnHost(nil) should error")
	}
	// Empty build template → error (config missing build_template).
	empty := &LocalPkgDef{PkgGlob: "*.pkg.tar.zst"}
	if _, err := buildLocalPkgOnHost(context.Background(), empty, "/src", EmitOpts{DryRun: true}); err == nil {
		t.Error("empty build_template should error")
	}
}

// TestBuildDepPkgsOnHost_EmptyAndDryRun proves the no-op contracts: empty
// packages → (nil, nil) with no build; DryRun → (nil, nil) logging the plan; an
// empty builder image (or nil builder def) with packages → error (never a
// silent drop).
func TestBuildDepPkgsOnHost_EmptyAndDryRun(t *testing.T) {
	lp := testPacLocalPkgDef()
	_, bc, _, err := LoadBuildConfigForImage(repoRootDir(t))
	if err != nil {
		t.Fatalf("LoadBuildConfigForImage: %v", err)
	}
	aurDef := bc.Builder["aur"]
	if aurDef == nil {
		t.Fatal("aur builder not defined in build.yml")
	}
	// Empty packages: pure no-op regardless of builder/dryrun — never shells out.
	if pkgs, err := buildDepPkgsOnHost(context.Background(), lp, aurDef, "", nil, "", nil, "", EmitOpts{}); err != nil || pkgs != nil {
		t.Errorf("empty packages = (%v, %v), want (nil, nil)", pkgs, err)
	}
	// DryRun with packages + builder + def: no build, no error.
	if pkgs, err := buildDepPkgsOnHost(context.Background(), lp, aurDef, "arch-builder:latest", []string{"cloudflared-bin"}, "", nil, "", EmitOpts{DryRun: true}); err != nil || pkgs != nil {
		t.Errorf("dry-run = (%v, %v), want (nil, nil)", pkgs, err)
	}
	// Packages but no builder image (live): hard error, never a silent drop.
	if _, err := buildDepPkgsOnHost(context.Background(), lp, aurDef, "", []string{"cloudflared-bin"}, "", nil, "", EmitOpts{}); err == nil {
		t.Error("buildDepPkgsOnHost with packages but no builder image should error")
	}
	// Packages + image but nil builder def: hard error.
	if _, err := buildDepPkgsOnHost(context.Background(), lp, nil, "arch-builder:latest", []string{"cloudflared-bin"}, "", nil, "", EmitOpts{}); err == nil {
		t.Error("buildDepPkgsOnHost with nil builder def should error")
	}
}

// TestLocalPkgDef_RoundTripFromBuildYML proves the pac format in the repo's
// build.yml actually carries the local_pkg block this code reads — guarding the
// config-driven contract end to end (the build.yml field names and the Go struct
// stay in lockstep). Loads the real build.yml via LoadBuildConfigForImage.
func TestLocalPkgDef_RoundTripFromBuildYML(t *testing.T) {
	dc, _, _, err := LoadBuildConfigForImage(repoRootDir(t))
	if err != nil {
		t.Fatalf("LoadBuildConfigForImage: %v", err)
	}
	arch := dc.ResolveDistro([]string{"arch"})
	if arch == nil {
		t.Fatal("arch distro not found in build.yml")
	}
	fmtName, lp := arch.LocalPkgFormat("pac")
	if fmtName != "pac" || lp == nil {
		t.Fatalf("pac format has no local_pkg block: fmt=%q lp=%#v", fmtName, lp)
	}
	if lp.PkgGlob == "" || lp.BuildTemplate == "" || lp.InstallTemplate == "" ||
		lp.ForeignQuery == "" || lp.Probe == "" || lp.DepBuilder == "" || len(lp.DepConstraintOps) == 0 {
		t.Errorf("build.yml pac.local_pkg is incomplete: %#v", lp)
	}
	// cachyos inherits arch's pac format → must resolve the same localpkg block.
	cachy := dc.ResolveDistro([]string{"cachyos"})
	if cachy == nil {
		t.Skip("cachyos distro not present; arch-only check already passed")
	}
	if _, clp := cachy.LocalPkgFormat("pac"); clp == nil {
		t.Error("cachyos (inherits arch) should resolve the pac local_pkg block")
	}
}

// repoRootDir walks up from the test's working dir to the directory containing
// build.yml (the project root), so the round-trip test finds the real config
// regardless of the package-test cwd.
func repoRootDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 16; i++ {
		if _, err := os.Stat(filepath.Join(dir, "build.yml")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Skip("build.yml not found walking up from test cwd; skipping round-trip")
	return ""
}
