package main

// localpkg.go — build a bundled package SOURCE dir on the host and install the
// resulting package FILE onto a deploy target, fully driven by the package
// format's `local_pkg:` config (build.yml `distro.<name>.format.<fmt>.local_pkg`).
//
// This is the execution machinery behind LocalPkgInstallStep (the IR form of a
// layer's `localpkg:` field). NOTHING here hardcodes a package-format command:
// the build command, install command, package-file glob, foreign-deps query,
// probe command, dependency-constraint operators, and dependency-builder name
// all come from the resolved *LocalPkgDef (LocalPkgInstallStep.LocalPkg /
// BuilderStep.LocalPkg), rendered through the EXISTING RenderTemplate engine
// (format_template.go) — the same machinery the rest of the build pipeline uses.
//
// Pieces, each a shared primitive (R3):
//
//   1. resolveLocalPkgDir   — locate the package SOURCE directory from the
//      author's hint + the layer/project anchors (walk-up search).
//   2. buildLocalPkgOnHost  — render LocalPkgDef.BuildTemplate and run it on the
//      HOST, returning the produced package-file paths (globbed via PkgGlob).
//   3. transferAndInstallPkgs — the SHARED transfer+install leg: PutFile each
//      package onto the target venue's filesystem (a local copy for the host
//      ShellExecutor, scp for the SSHExecutor) then render+run
//      LocalPkgDef.InstallTemplate via RunSystem. The SAME leg the aur builder
//      uses (BuilderStep.LocalPkg) — both call this one helper.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// localPkgGuestStage is the staging dir on the deploy target where the built
// packages land before the format's install command runs. Shared by the
// builder and localpkg paths so both clean up the same well-known location
// idempotently. (A staging PATH, not a package-format string — venue-agnostic.)
const localPkgGuestStage = "/tmp/ov-pkgs"

// localPkgBuildContext is the template context for LocalPkgDef.BuildTemplate.
type localPkgBuildContext struct {
	SrcDir  string // resolved package source directory (the PKGBUILD dir for pac)
	PkgDest string // per-build output dir the build writes package files into
}

// localPkgInstallContext is the template context for LocalPkgDef.InstallTemplate.
type localPkgInstallContext struct {
	StageDir string // on-target staging dir holding the transferred package files
	Glob     string // LocalPkgDef.PkgGlob (e.g. "*.pkg.tar.zst")
}

// resolveLocalPkgDir locates the package SOURCE directory for a layer's
// `localpkg:` hint. Resolution order, returning the first directory that
// actually contains a `PKGBUILD` file:
//
//  1. absolute ref → used verbatim.
//  2. <layerDir>/<ref>     — the source bundled alongside the layer.
//  3. <projectDir>/<ref>   — relative to the deploy project dir (os.Getwd).
//  4. walk UP from projectDir, trying <ancestor>/<ref> at each level — this is
//     the operator path: `ov -C image/cachyos deploy add cachyos-gpu` has a
//     project dir of image/cachyos while pkg/arch lives at the SUPERPROJECT
//     root (../../pkg/arch). The walk finds it without the layer needing to
//     know how deeply the consuming project is nested.
//
// Returns "" when no PKGBUILD is found anywhere — the caller treats that as a
// no-op (the layer's own curl/COPY task is the documented fallback).
//
// NB: the PKGBUILD sentinel is the pac source-dir marker; it is the one
// format-specific filename retained here because it identifies the SOURCE dir
// shape, not a package-manager command. (rpm/deb localpkg would key off their
// own spec-file marker when wired; today only pac is wired.)
func resolveLocalPkgDir(ref, layerDir, projectDir string) string {
	if ref == "" {
		return ""
	}
	hasPkgbuild := func(dir string) bool {
		if dir == "" {
			return false
		}
		info, err := os.Stat(filepath.Join(dir, "PKGBUILD"))
		return err == nil && !info.IsDir()
	}

	if filepath.IsAbs(ref) {
		if hasPkgbuild(ref) {
			return ref
		}
		return ""
	}
	// Layer-relative, then project-relative.
	for _, base := range []string{layerDir, projectDir} {
		if base == "" {
			continue
		}
		if cand := filepath.Join(base, ref); hasPkgbuild(cand) {
			return cand
		}
	}
	// Walk up from the project dir. filepath.Dir is idempotent at the root
	// ("/" → "/"), so cap the loop to terminate even on an unrooted relative
	// projectDir.
	dir := projectDir
	for i := 0; dir != "" && i < 64; i++ {
		if cand := filepath.Join(dir, ref); hasPkgbuild(cand) {
			return cand
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// buildLocalPkgOnHost builds the package(s) defined by the source dir on the
// HOST by rendering LocalPkgDef.BuildTemplate and returns the produced
// package-file paths (globbed via LocalPkgDef.PkgGlob). The build output lands
// in a per-call temp dir (passed as {{.PkgDest}}) so the glob is deterministic
// and the source tree is never polluted.
//
// The build command (e.g. makepkg) comes ENTIRELY from config — this function
// renders LocalPkgDef.BuildTemplate via the existing RenderTemplate engine and
// runs it under `bash -c`, so there is no hardcoded makepkg/pacman literal here.
//
// The temp dir is registered for sweep but deliberately NOT defer-removed: the
// caller owns the package files until install completes.
func buildLocalPkgOnHost(ctx context.Context, lp *LocalPkgDef, srcDir string, opts EmitOpts) ([]string, error) {
	if lp == nil {
		return nil, fmt.Errorf("buildLocalPkgOnHost: nil LocalPkgDef")
	}
	pkgDest, err := os.MkdirTemp("", "ov-localpkg-")
	if err != nil {
		return nil, fmt.Errorf("localpkg build output tempdir: %w", err)
	}
	RegisterTempCleanup(pkgDest)

	buildCmd, err := RenderTemplate("localpkg-build", lp.BuildTemplate, localPkgBuildContext{
		SrcDir:  srcDir,
		PkgDest: pkgDest,
	})
	if err != nil {
		return nil, fmt.Errorf("rendering localpkg build template: %w", err)
	}
	buildCmd = strings.TrimSpace(buildCmd)
	if buildCmd == "" {
		return nil, fmt.Errorf("localpkg build template rendered empty (format config missing build_template?)")
	}

	if opts.DryRun {
		fmt.Fprintf(os.Stderr, "[dry-run] localpkg build (PKGDEST=%s): %s\n", pkgDest, buildCmd)
		return nil, nil
	}

	cmd := exec.CommandContext(ctx, "bash", "-c", buildCmd)
	cmd.Stdout = os.Stderr // surface build output (operator debugging) without polluting stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("localpkg build in %s: %w", srcDir, err)
	}

	matches, _ := filepath.Glob(filepath.Join(pkgDest, lp.PkgGlob))
	if len(matches) == 0 {
		return nil, fmt.Errorf("localpkg build in %s produced no %s in %s", srcDir, lp.PkgGlob, pkgDest)
	}
	return matches, nil
}

// buildDepPkgsOnHost builds an arbitrary set of dependency packages into
// package files ON THE HOST (where podman is available) through the EXISTING
// builder named by LocalPkgDef.DepBuilder (the `aur` builder for pac) and
// returns the produced package paths. It is the BUILD half of the VM target's
// aur `execBuilder` path factored out (R3): execBuilder now calls this and then
// transferAndInstallPkgs, and the localpkg step calls it to build the package's
// dependency closure. There is exactly ONE host-side dep-builder implementation
// across the layer-aur path and the localpkg-dep-closure path.
//
// It synthesizes a BuilderStep{Builder:lp.DepBuilder, …} carrying the package
// names in RawStageContext["packages"], renders the SAME renderBuilderScript the
// container/local/VM builder paths use, wraps it with the same root
// backstop-find + chown-to-0:0 (so the bind-mount surface is host-readable under
// rootless podman), runs it via BuilderRun(RunAsRoot:true), surfaces output to
// stderr, and globs the staging dir for LocalPkgDef.PkgGlob.
//
// Empty packages → (nil, nil): a no-op, never an error. On DryRun it logs the
// plan and returns nil (no artifacts).
//
// The staging tmpdir is registered for sweep but deliberately NOT defer-removed:
// the caller owns the returned package files until install completes.
func buildDepPkgsOnHost(ctx context.Context, lp *LocalPkgDef, bDef *BuilderDef, builderImage string, packages []string, layerDir string, cfg *Config, projectDir string, opts EmitOpts) ([]string, error) {
	if len(packages) == 0 {
		return nil, nil
	}
	if lp == nil {
		return nil, fmt.Errorf("buildDepPkgsOnHost: nil LocalPkgDef")
	}
	if builderImage == "" {
		return nil, fmt.Errorf("buildDepPkgsOnHost: no %s builder image for packages %v", lp.DepBuilder, packages)
	}
	if bDef == nil {
		return nil, fmt.Errorf("buildDepPkgsOnHost: no %s builder definition for packages %v", lp.DepBuilder, packages)
	}

	if opts.DryRun {
		fmt.Fprintf(os.Stderr, "[dry-run] build %d dependency package(s) %v via %s builder %s\n",
			len(packages), packages, lp.DepBuilder, builderImage)
		return nil, nil
	}

	// Synthetic BuilderStep — the SAME shape compileBuilderSteps produces, so
	// renderBuilderScript renders the identical build flow for this builder from
	// its phase.install.host cell (config-driven).
	step := &BuilderStep{
		Builder:         lp.DepBuilder,
		BuilderImage:    builderImage,
		BuilderDef:      bDef,
		LayerDir:        layerDir,
		Phase:           PhaseInstall,
		RawStageContext: map[string]interface{}{"packages": packages},
	}

	// Host staging dir bind-mounted as /tmp/aur-pkgs — the builder writes the
	// package files here; we then glob them. RegisterTempCleanup sweeps it on
	// exit; no defer-remove (caller owns the files until install completes).
	hostStage, err := os.MkdirTemp("", "ov-pkgdep-")
	if err != nil {
		return nil, fmt.Errorf("dependency staging mkdir: %w", err)
	}
	RegisterTempCleanup(hostStage)

	hostHome, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("UserHomeDir: %w", err)
	}
	bindMounts, err := UserScopeBindMounts(hostHome)
	if err != nil {
		return nil, err
	}
	bindMounts["/tmp/aur-pkgs"] = hostStage
	envVars := UserScopeEnv(hostHome)

	// renderBuilderScript runs AS ROOT inside the builder (RunAsRoot=true): for
	// aur it writes the NOPASSWD-wheel sudoers, adds `user` to wheel, then
	// `sudo -u user`s the build. Run it directly as root — do NOT pre-drop.
	innerScript, err := renderBuilderScript(step, hostHome)
	if err != nil {
		return nil, err
	}
	wrappedScript := "set -e\n" +
		innerScript + "\n" +
		"# Backstop find: the builder installs the package and cleans up its\n" +
		"# build tree, so the inner script's find may run after the tree is\n" +
		"# already wiped. Broaden the search if /tmp/aur-pkgs is still empty.\n" +
		"if [ -z \"$(ls -A /tmp/aur-pkgs 2>/dev/null)\" ]; then\n" +
		"  find / -name " + shQuoteArg(lp.PkgGlob) + " 2>/dev/null -exec cp {} /tmp/aur-pkgs/ \\;\n" +
		"fi\n" +
		"# Rootless-podman userns fix: files created by container user\n" +
		"# 1000 land in the host's subuid range and become unreadable to\n" +
		"# the operator. chown to 0:0 — root in container maps to the\n" +
		"# host user under rootless podman — so the bind-mount surface is\n" +
		"# host-readable for the subsequent transfer+install leg.\n" +
		"chown -R 0:0 /tmp/aur-pkgs/\n"

	out, err := BuilderRun(opts.ContextOrDefault(), BuilderRunOpts{
		BuilderImage: builderImage,
		LayerDir:     step.LayerDir,
		ScriptBody:   wrappedScript,
		BindMounts:   bindMounts,
		Env:          envVars,
		HostHome:     hostHome,
		DryRun:       opts.DryRun,
		RunAsRoot:    true,
		// Cfg + ProjectDir let BuilderRun's EnsureImagePresent run the
		// namespace-aware ResolveImage, so a namespace-qualified builder ref
		// (e.g. the cachyos project's aur builder `ov.arch-builder`) resolves to
		// its concrete image — matching the aur-LAYER path (deploy_target_local.go).
		Cfg:        cfg,
		ProjectDir: projectDir,
	})
	// Always surface the builder's stdout/stderr — the operator needs to see
	// compile output to debug build failures, not just the bare exit status.
	if len(out) > 0 {
		os.Stderr.Write(out)
	}
	if err != nil {
		return nil, fmt.Errorf("%s builder: %w", lp.DepBuilder, err)
	}

	matches, _ := filepath.Glob(filepath.Join(hostStage, lp.PkgGlob))
	if len(matches) == 0 {
		return nil, fmt.Errorf("%s builder produced no %s in %s for packages %v", lp.DepBuilder, lp.PkgGlob, hostStage, packages)
	}
	return matches, nil
}

// pkgInfoDepends parses the bare runtime-dependency names from a built package
// file's embedded metadata (`bsdtar -xOqf <pkg> .PKGINFO` for pacman packages).
// Each `depend = <name>[<op><version>]` line yields one name; the version
// constraint is stripped to the bare name using the format's
// LocalPkgDef.DepConstraintOps. The package file is local (just built), so this
// runs on the host. The bsdtar invocation is injected via the pkgInfoReader
// package var so the pure parsing logic (parsePkgInfoDepends) is unit-testable
// without shelling out.
func pkgInfoDepends(pkgFile string, ops []string) ([]string, error) {
	data, err := pkgInfoReader(pkgFile)
	if err != nil {
		return nil, fmt.Errorf("reading package metadata from %s: %w", filepath.Base(pkgFile), err)
	}
	return parsePkgInfoDepends(data, ops), nil
}

// pkgInfoReader extracts the dependency-carrying metadata bytes from a built
// package file. Package var so tests inject canned content instead of running
// bsdtar. (The .PKGINFO member name is the pacman package layout; rpm/deb
// localpkg would swap this reader when wired — only pac is wired today.)
var pkgInfoReader = func(pkgFile string) ([]byte, error) {
	cmd := exec.Command("bsdtar", "-xOqf", pkgFile, ".PKGINFO")
	return cmd.Output()
}

// parsePkgInfoDepends is the pure parser over raw metadata bytes — the
// version-constraint-stripping logic, unit-testable in isolation. Returns the
// bare dependency names in file order, de-duplicated. `ops` is the format's
// LocalPkgDef.DepConstraintOps (version-constraint operators), so even the
// constraint-stripping is config-driven.
func parsePkgInfoDepends(pkginfo []byte, ops []string) []string {
	var deps []string
	seen := map[string]bool{}
	for _, line := range strings.Split(string(pkginfo), "\n") {
		line = strings.TrimSpace(line)
		const prefix = "depend"
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		rest := strings.TrimSpace(line[len(prefix):])
		if !strings.HasPrefix(rest, "=") {
			continue // not a `depend = …` line
		}
		val := strings.TrimSpace(rest[1:])
		if val == "" {
			continue
		}
		name := stripDependConstraint(val, ops)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		deps = append(deps, name)
	}
	return deps
}

// stripDependConstraint reduces a `name[op version]` dependency spec to its
// bare package name by cutting at the first version-constraint operator from
// `ops`. `ops` must be ordered longest-first (so `>=` matches before `>`).
func stripDependConstraint(spec string, ops []string) string {
	idx := -1
	for _, op := range ops {
		if op == "" {
			continue
		}
		if i := strings.Index(spec, op); i >= 0 && (idx < 0 || i < idx) {
			idx = i
		}
	}
	if idx < 0 {
		return strings.TrimSpace(spec)
	}
	return strings.TrimSpace(spec[:idx])
}

// hostForeignPkgs returns the set of FOREIGN package names on the HOST — the
// packages no sync repo provides (the dep-closure discriminator). The query
// command comes from LocalPkgDef.ForeignQuery (e.g. `pacman -Qmq`), so this is
// config-driven, not a hardcoded pacman literal. The command runner is injected
// via the foreignPkgRunner package var so the intersection logic is
// unit-testable.
func hostForeignPkgs(query string) (map[string]bool, error) {
	if strings.TrimSpace(query) == "" {
		return map[string]bool{}, nil
	}
	out, err := foreignPkgRunner(query)
	if err != nil {
		return nil, fmt.Errorf("foreign-package query %q: %w", query, err)
	}
	set := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			set[name] = true
		}
	}
	return set, nil
}

// foreignPkgRunner runs the format's foreign-package query under `bash -c`.
// Package var so tests inject a canned foreign-package list instead of querying
// a real package DB.
var foreignPkgRunner = func(query string) ([]byte, error) {
	return exec.Command("bash", "-c", query).Output()
}

// builderOnlyDeps returns the bare dependency names that the dependency builder
// must build — the intersection of a built package's `depends` with the host's
// FOREIGN-package set (a source-built package's builder-only deps are
// foreign-installed on the build host by definition, while repo deps are not).
// Pure (no I/O) so it is directly unit-testable; the callers feed it the two
// probed inputs. Order follows `depends` (deterministic), de-duplicated by
// pkgInfoDepends already.
func builderOnlyDeps(depends []string, foreign map[string]bool) []string {
	var out []string
	for _, d := range depends {
		if foreign[d] {
			out = append(out, d)
		}
	}
	return out
}

// transferAndInstallPkgs ships built package files onto a deploy target and
// installs them by rendering LocalPkgDef.InstallTemplate. It is venue-agnostic
// via the DeployExecutor: PutFile is a local filesystem copy for the host
// ShellExecutor and an scp for the SSHExecutor, and RunSystem is local sudo vs
// `ssh sudo`. One implementation serves BOTH the localpkg step (LocalDeployTarget
// / VmDeployTarget) AND the builder's install leg (BuilderStep.LocalPkg), so
// "ship packages to a venue and install them" has a single config-driven home
// (R3).
//
// The staging dir is cleared before transfer so a re-run replaces stale content
// idempotently; the format's install command (e.g. `pacman -U`) is expected to
// be the upgrade form, so re-installing the same or a newer build never errors.
func transferAndInstallPkgs(ctx context.Context, exec DeployExecutor, lp *LocalPkgDef, pkgFiles []string, opts EmitOpts) error {
	if lp == nil {
		return fmt.Errorf("transferAndInstallPkgs: nil LocalPkgDef")
	}
	if len(pkgFiles) == 0 {
		return fmt.Errorf("transferAndInstallPkgs: no package files to install")
	}

	install, err := RenderTemplate("localpkg-install", lp.InstallTemplate, localPkgInstallContext{
		StageDir: localPkgGuestStage,
		Glob:     lp.PkgGlob,
	})
	if err != nil {
		return fmt.Errorf("rendering localpkg install template: %w", err)
	}
	install = strings.TrimSpace(install)
	if install == "" {
		return fmt.Errorf("localpkg install template rendered empty (format config missing install_template?)")
	}

	if opts.DryRun {
		fmt.Fprintf(os.Stderr, "[dry-run] transfer %d package(s) to %s and install on %s: %s\n",
			len(pkgFiles), localPkgGuestStage, exec.Venue(), install)
		return nil
	}

	prep := fmt.Sprintf("set -e\nmkdir -p %[1]s\nrm -f %[1]s/%[2]s 2>/dev/null || true\n",
		localPkgGuestStage, lp.PkgGlob)
	if err := exec.RunUser(ctx, prep, opts); err != nil {
		return fmt.Errorf("preparing package staging dir on %s: %w", exec.Venue(), err)
	}

	for _, f := range pkgFiles {
		dst := filepath.Join(localPkgGuestStage, filepath.Base(f))
		// ownerRoot=false: /tmp staging is user-writable; the install command
		// (RunSystem, sudo) reads it.
		if err := exec.PutFile(ctx, f, dst, 0o644, false, opts); err != nil {
			return fmt.Errorf("transferring package %s to %s: %w", filepath.Base(f), exec.Venue(), err)
		}
	}

	if err := exec.RunSystem(ctx, install, opts); err != nil {
		return fmt.Errorf("installing packages on %s: %w", exec.Venue(), err)
	}
	return nil
}

// venueHasPkgManager probes the actual deploy venue for the package format's
// manager — the precondition for executing a LocalPkgInstallStep. The probe
// command comes from LocalPkgDef.Probe (e.g. `command -v pacman`), so this is
// config-driven, not a hardcoded pacman literal. Probing the VENUE (not the host
// running ov) is what makes the gate correct for a VM deploy: the guest may be a
// different distro than the operator host, and vice-versa. The executor is the
// venue (ShellExecutor → host, SSHExecutor → guest), so one probe through it is
// venue-accurate for both targets (R3). DryRun assumes true so the planner shows
// the build+install it WOULD do. A nil LocalPkgDef, empty probe, probe error, or
// non-matching venue returns false: ov never assumes a target can take a package.
func venueHasPkgManager(ctx context.Context, exec DeployExecutor, lp *LocalPkgDef, opts EmitOpts) bool {
	if lp == nil || strings.TrimSpace(lp.Probe) == "" {
		return false
	}
	if opts.DryRun {
		return true
	}
	probe := fmt.Sprintf("%s >/dev/null 2>&1 && echo yes || echo no", lp.Probe)
	stdout, _, _, err := exec.RunCapture(ctx, probe)
	if err != nil {
		return false
	}
	return strings.TrimSpace(stdout) == "yes"
}

// execLocalPkgInstall is the shared body both LocalDeployTarget and
// VmDeployTarget call for a LocalPkgInstallStep: resolve the package source dir,
// build it on the host, then transfer+install onto the target venue. `supported`
// gates whether the install leg runs (the venue's package manager must match the
// step's format); an unsupported target or a missing source dir is a clean no-op
// (the layer's own curl/COPY task covers it).
//
// venueName is used only for log lines (e.g. "host", "vm:cachyos-gpu").
func execLocalPkgInstall(ctx context.Context, exec DeployExecutor, s *LocalPkgInstallStep, supported bool, venueName string, cfg *Config, opts EmitOpts) error {
	if s.LocalPkg == nil {
		fmt.Fprintf(os.Stderr, "%s skip: localpkg %s (layer=%s) — target distro declares no localpkg-capable package format; the layer's curl/COPY task installs it instead\n",
			venueName, s.PkgbuildRef, s.LayerName)
		return nil
	}
	if !supported {
		fmt.Fprintf(os.Stderr, "%s skip: localpkg %s (layer=%s) — target has no %s package manager; the layer's curl/COPY task installs it instead\n",
			venueName, s.PkgbuildRef, s.LayerName, s.Format)
		return nil
	}
	pkgDir := resolveLocalPkgDir(s.PkgbuildRef, s.LayerDir, s.ProjectDir)
	if pkgDir == "" {
		fmt.Fprintf(os.Stderr, "%s skip: localpkg %s (layer=%s) — no package source found from layer dir %q or project dir %q; the layer's curl/COPY task installs it instead\n",
			venueName, s.PkgbuildRef, s.LayerName, s.LayerDir, s.ProjectDir)
		return nil
	}
	fmt.Fprintf(os.Stderr, "%s: building %s package (%s) from %s for layer %s\n",
		venueName, strings.TrimSuffix(filepath.Base(pkgDir), "/"), s.Format, pkgDir, s.LayerName)
	pkgFiles, err := buildLocalPkgOnHost(ctx, s.LocalPkg, pkgDir, opts)
	if err != nil {
		return fmt.Errorf("localpkg %s (layer=%s): %w", s.PkgbuildRef, s.LayerName, err)
	}
	if opts.DryRun {
		return nil
	}

	// Resolve + build the built package's builder-resolvable dependency closure.
	// The package's `depends=` may name packages that no sync repo can satisfy
	// under the install command (e.g. AUR packages under `pacman -U`), so the
	// install would fail "unable to satisfy dependency". Derive the closure
	// GENERICALLY from the built package's metadata ∩ the host's foreign packages
	// (NO hardcoded names), build it through the SAME builder (R3), and install
	// the WHOLE closure in one install command so the deps satisfy the package's
	// depends.
	depPkgs, err := resolveLocalPkgDeps(ctx, s, pkgFiles, pkgDir, venueName, cfg, opts)
	if err != nil {
		return fmt.Errorf("localpkg %s (layer=%s): resolving dependency closure: %w", s.PkgbuildRef, s.LayerName, err)
	}

	// Install deps FIRST in the same install command so they satisfy the
	// package's depends. append into a fresh slice — never mutate depPkgs.
	installSet := make([]string, 0, len(depPkgs)+len(pkgFiles))
	installSet = append(installSet, depPkgs...)
	installSet = append(installSet, pkgFiles...)
	return transferAndInstallPkgs(ctx, exec, s.LocalPkg, installSet, opts)
}

// resolveLocalPkgDeps computes a built package's builder-resolvable dependency
// closure and builds it through the format's dependency builder, returning the
// produced dep package paths (empty when there are no builder-only deps). It is
// the dependency-resolution leg of execLocalPkgInstall, kept separate so the
// build-vs-install ordering in the caller stays legible.
//
// The closure is derived GENERICALLY (no hardcoded package names): parse the
// built package's `depends` from its metadata (using the format's
// DepConstraintOps), intersect with the host's foreign packages (queried via
// the format's ForeignQuery). Deps with no resolvable dep builder
// (s.BuilderImage == "") are logged by name and NOT silently dropped (the
// curl/COPY fallback still covers non-localpkg targets).
func resolveLocalPkgDeps(ctx context.Context, s *LocalPkgInstallStep, pkgFiles []string, pkgDir, venueName string, cfg *Config, opts EmitOpts) ([]string, error) {
	lp := s.LocalPkg
	// Union the depends across every built package (a split package may emit
	// several), de-duplicated.
	var depends []string
	seen := map[string]bool{}
	for _, pf := range pkgFiles {
		ds, err := pkgInfoDepends(pf, lp.DepConstraintOps)
		if err != nil {
			return nil, err
		}
		for _, d := range ds {
			if !seen[d] {
				seen[d] = true
				depends = append(depends, d)
			}
		}
	}
	if len(depends) == 0 {
		return nil, nil
	}

	foreign, err := hostForeignPkgs(lp.ForeignQuery)
	if err != nil {
		return nil, err
	}
	deps := builderOnlyDeps(depends, foreign)
	if len(deps) == 0 {
		return nil, nil
	}

	if s.BuilderImage == "" || s.DepBuilderDef == nil {
		fmt.Fprintf(os.Stderr, "%s warn: localpkg %s (layer=%s) has %s-builder dependencies %v but no %s builder resolved — they will NOT be built; the install will fail unless they are already present on the target. Define builder.%s in image.yml to build them.\n",
			venueName, s.PkgbuildRef, s.LayerName, lp.DepBuilder, deps, lp.DepBuilder, lp.DepBuilder)
		return nil, nil
	}

	fmt.Fprintf(os.Stderr, "%s: building %d %s dependency package(s) %v for localpkg %s (layer=%s) via builder %s\n",
		venueName, len(deps), lp.DepBuilder, deps, s.PkgbuildRef, s.LayerName, s.BuilderImage)
	return buildDepPkgsOnHost(ctx, lp, s.DepBuilderDef, s.BuilderImage, deps, pkgDir, cfg, s.ProjectDir, opts)
}
