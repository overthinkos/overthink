package main

// localpkg.go — build a bundled Arch PKGBUILD on the host (makepkg) and install
// the resulting .pkg.tar.zst onto a pac-based deploy target via pacman -U.
//
// This is the execution machinery behind LocalPkgInstallStep (the IR form of a
// layer's `localpkg:` field). Three pieces, each a shared primitive (R3):
//
//   1. resolveLocalPkgDir   — locate the PKGBUILD directory from the author's
//      hint + the layer/project anchors (walk-up search).
//   2. runMakepkgOnHost     — run `makepkg` in that dir on the HOST and return
//      the produced .pkg.tar.zst paths.
//   3. transferAndPacmanInstall — the SHARED transfer+install leg: PutFile each
//      package onto the target venue's filesystem (a local copy for the host
//      ShellExecutor, scp for the SSHExecutor) then `pacman -U` via RunSystem.
//      This is the SAME leg the AUR builder uses — both call this one helper so
//      "ship .pkg.tar.zst to a venue and install it" has exactly one
//      implementation across the aur and localpkg paths.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// pacmanPkgExt is the package-file extension makepkg produces and pacman -U
// installs. Named so the glob isn't a bare magic string scattered across
// callers.
const pacmanPkgExt = "*.pkg.tar.zst"

// localPkgGuestStage is the staging dir on the deploy target where the built
// packages land before `pacman -U`. Shared by the aur and localpkg paths so
// both clean up the same well-known location idempotently.
const localPkgGuestStage = "/tmp/ov-pkgs"

// resolveLocalPkgDir locates the PKGBUILD directory for a layer's `localpkg:`
// hint. Resolution order, returning the first directory that actually contains
// a `PKGBUILD` file:
//
//  1. absolute ref → used verbatim.
//  2. <layerDir>/<ref>     — the PKGBUILD bundled alongside the layer.
//  3. <projectDir>/<ref>   — relative to the deploy project dir (os.Getwd).
//  4. walk UP from projectDir, trying <ancestor>/<ref> at each level — this is
//     the operator path: `ov -C image/cachyos deploy add cachyos-gpu` has a
//     project dir of image/cachyos while pkg/arch lives at the SUPERPROJECT
//     root (../../pkg/arch). The walk finds it without the layer needing to
//     know how deeply the consuming project is nested.
//
// Returns "" when no PKGBUILD is found anywhere — the caller treats that as a
// no-op (the layer's own curl/COPY task is the documented fallback).
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

// runMakepkgOnHost builds the package(s) defined by the PKGBUILD in pkgbuildDir
// on the HOST and returns the produced .pkg.tar.zst paths. The build artifacts
// land in a per-call temp dir (via PKGDEST) so the glob is deterministic and
// the source tree is never polluted, mirroring the AUR builder's host staging.
//
// makepkg flags: `-s` (install missing makedepends — the PKGBUILD's `go`/`git`),
// `-f` (force overwrite an existing built package), `--noconfirm`. NO `-i`
// (install) — installation is the target venue's job via transferAndPacmanInstall,
// and NO `-e` (--noextract) so makepkg clones the PKGBUILD's `git+file://`
// source fresh, producing a CURRENT, from-image-capable, deterministically
// CalVer-stamped package (the calver.sh derives the version from the HEAD commit).
//
// makepkg refuses to run as root, so this assumes the deploy is driven by a
// non-root operator (the standard ov invocation) — the same precondition the
// host AUR builder path relies on.
func runMakepkgOnHost(ctx context.Context, pkgbuildDir string, opts EmitOpts) ([]string, error) {
	pkgDest, err := os.MkdirTemp("", "ov-localpkg-")
	if err != nil {
		return nil, fmt.Errorf("makepkg PKGDEST tempdir: %w", err)
	}
	RegisterTempCleanup(pkgDest)
	// NB: caller is responsible for the package files until install completes;
	// we register the temp dir for sweep but do not defer-remove it here.

	if opts.DryRun {
		fmt.Fprintf(os.Stderr, "[dry-run] makepkg -sf --noconfirm in %s (PKGDEST=%s)\n", pkgbuildDir, pkgDest)
		return nil, nil
	}

	cmd := exec.CommandContext(ctx, "makepkg", "-sf", "--noconfirm")
	cmd.Dir = pkgbuildDir
	cmd.Env = append(os.Environ(), "PKGDEST="+pkgDest)
	cmd.Stdout = os.Stderr // surface build output (operator debugging) without polluting stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("makepkg in %s: %w", pkgbuildDir, err)
	}

	matches, _ := filepath.Glob(filepath.Join(pkgDest, pacmanPkgExt))
	if len(matches) == 0 {
		return nil, fmt.Errorf("makepkg in %s produced no %s in %s", pkgbuildDir, pacmanPkgExt, pkgDest)
	}
	return matches, nil
}

// transferAndPacmanInstall ships built .pkg.tar.zst files onto a deploy target
// and `pacman -U --noconfirm`-installs them. It is venue-agnostic via the
// DeployExecutor: PutFile is a local filesystem copy for the host ShellExecutor
// and an scp for the SSHExecutor, and RunSystem is local sudo vs `ssh sudo`.
// One implementation serves BOTH the localpkg step (LocalDeployTarget /
// VmDeployTarget) AND — once routed through here — the aur builder's transfer
// leg, so "ship packages to a venue and install them" has a single home (R3).
//
// The staging dir is cleared before transfer so a re-run replaces stale content
// idempotently; `pacman -U` is the upgrade form, so re-installing the same or a
// newer build never errors.
func transferAndPacmanInstall(ctx context.Context, exec DeployExecutor, pkgFiles []string, opts EmitOpts) error {
	if len(pkgFiles) == 0 {
		return fmt.Errorf("transferAndPacmanInstall: no package files to install")
	}
	if opts.DryRun {
		fmt.Fprintf(os.Stderr, "[dry-run] transfer %d package(s) to %s and pacman -U on %s\n",
			len(pkgFiles), localPkgGuestStage, exec.Venue())
		return nil
	}

	prep := fmt.Sprintf("set -e\nmkdir -p %[1]s\nrm -f %[1]s/%[2]s 2>/dev/null || true\n",
		localPkgGuestStage, pacmanPkgExt)
	if err := exec.RunUser(ctx, prep, opts); err != nil {
		return fmt.Errorf("preparing package staging dir on %s: %w", exec.Venue(), err)
	}

	for _, f := range pkgFiles {
		dst := filepath.Join(localPkgGuestStage, filepath.Base(f))
		// ownerRoot=false: /tmp staging is user-writable; pacman -U (RunSystem,
		// sudo) reads it. Matches the aur leg's transfer ownership.
		if err := exec.PutFile(ctx, f, dst, 0o644, false, opts); err != nil {
			return fmt.Errorf("transferring package %s to %s: %w", filepath.Base(f), exec.Venue(), err)
		}
	}

	install := fmt.Sprintf("pacman -U --noconfirm %s/%s", localPkgGuestStage, pacmanPkgExt)
	if err := exec.RunSystem(ctx, install, opts); err != nil {
		return fmt.Errorf("pacman -U on %s: %w", exec.Venue(), err)
	}
	return nil
}

// venueHasPacman probes the actual deploy venue for a working pacman — the
// precondition for executing a LocalPkgInstallStep. Probing the VENUE (not the
// host running ov) is what makes the Arch gate correct for a VM deploy: the
// guest may be CachyOS while the operator host is anything, and vice-versa. The
// executor is the venue (ShellExecutor → host, SSHExecutor → guest), so one
// probe through it is venue-accurate for both targets (R3). DryRun assumes true
// so the planner shows the build+install it WOULD do. A probe error or a
// non-`pac` venue returns false: ov never assumes a target can take a package.
func venueHasPacman(ctx context.Context, exec DeployExecutor, opts EmitOpts) bool {
	if opts.DryRun {
		return true
	}
	stdout, _, _, err := exec.RunCapture(ctx, "command -v pacman >/dev/null 2>&1 && echo yes || echo no")
	if err != nil {
		return false
	}
	return strings.TrimSpace(stdout) == "yes"
}

// execLocalPkgInstall is the shared body both LocalDeployTarget and
// VmDeployTarget call for a LocalPkgInstallStep: resolve the PKGBUILD, build it
// on the host, then transfer+install onto the target venue. arch gates whether
// the install leg runs (the target must be pac-based); a non-pac target or a
// missing PKGBUILD is a clean no-op (the layer's own curl/COPY task covers it).
//
// venueName is used only for log lines (e.g. "host", "vm:cachyos-gpu").
func execLocalPkgInstall(ctx context.Context, exec DeployExecutor, s *LocalPkgInstallStep, arch bool, venueName string, opts EmitOpts) error {
	if !arch {
		fmt.Fprintf(os.Stderr, "%s skip: localpkg %s (layer=%s) — target is not Arch/pac-based; the layer's curl/COPY task installs ov instead\n",
			venueName, s.PkgbuildRef, s.LayerName)
		return nil
	}
	pkgDir := resolveLocalPkgDir(s.PkgbuildRef, s.LayerDir, s.ProjectDir)
	if pkgDir == "" {
		fmt.Fprintf(os.Stderr, "%s skip: localpkg %s (layer=%s) — no PKGBUILD found from layer dir %q or project dir %q; the layer's curl/COPY task installs ov instead\n",
			venueName, s.PkgbuildRef, s.LayerName, s.LayerDir, s.ProjectDir)
		return nil
	}
	fmt.Fprintf(os.Stderr, "%s: building %s package from %s (makepkg) for layer %s\n",
		venueName, strings.TrimSuffix(filepath.Base(pkgDir), "/"), pkgDir, s.LayerName)
	pkgFiles, err := runMakepkgOnHost(ctx, pkgDir, opts)
	if err != nil {
		return fmt.Errorf("localpkg %s (layer=%s): %w", s.PkgbuildRef, s.LayerName, err)
	}
	if opts.DryRun {
		return nil
	}
	return transferAndPacmanInstall(ctx, exec, pkgFiles, opts)
}
