package main

// migrate_arch_rename.go — `charly migrate`.
//
// Renames the `archlinux` distro tag and the `archlinux` / `archlinux-builder`
// / `archlinux-pacstrap*` image identifiers to `arch` / `arch-builder` /
// `arch-pacstrap*` everywhere in a project's YAML — EXCEPT external Arch
// strings that must stay verbatim:
//
//   - ANY external registry ref whose path contains archlinux, matched by SHAPE
//     (a registry host + path), e.g. quay.io/archlinux/archlinux (the official
//     quay mirror), docker.io/library/archlinux, ghcr.io/<ns>/archlinux-* —
//     see archRenameExternalRefRe
//   - pacman-key --populate archlinux  (the literal keyring name)
//   - archlinux.org  (mirrorlist URLs)
//   - archlinux-keyring  (the keyring package name)
//
// Because every internal identifier (`archlinux-builder`, `archlinux-pacstrap`)
// starts with `archlinux`, a single `archlinux`→`arch` substitution collapses
// all of them at once. Covers:
//
//   - overthink.yml + per-kind siblings + build.yml + base.yml (project)
//   - every layers/<name>/layer.yml (distros: archlinux: sections, archlinux:
//     package-map keys)
//   - the per-machine ~/.config/ov/deploy.yml (host overlay)
//
// Idempotent: an already-renamed config has no non-external `archlinux` left,
// so a re-run is a no-op. Per-file backups follow the <file>.bak.<unix-ts>
// convention.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// archRenameExternalRefRe matches an EXTERNAL registry reference whose path
// contains "archlinux" — a registry-host segment (a token carrying a "." or ":"
// before the first "/") followed by a path component containing archlinux. This
// protects the quay.io/archlinux/archlinux mirror, docker.io/library/archlinux,
// ghcr.io/<ns>/archlinux-*, registry.example:5000/.../archlinux, etc. by SHAPE —
// no per-registry enumeration. Internal identifiers (the `archlinux` distro tag,
// archlinux-builder, archlinux-pacstrap*) have no registry-host segment and are
// therefore still renamed.
var archRenameExternalRefRe = regexp.MustCompile(`[A-Za-z0-9._-]+[.:][A-Za-z0-9._:-]*/[A-Za-z0-9._/-]*archlinux[A-Za-z0-9._/-]*`)

// archRenameExternals are the NON-registry substrings that legitimately contain
// "archlinux" and must survive the rename: a CLI argument, a bare mirrorlist
// hostname, and a package name. (Registry refs are handled by the regex above.)
// Each is swapped for a NUL-delimited placeholder before the global replace,
// then restored after — so the embedded "archlinux" is never touched.
var archRenameExternals = []struct{ token, placeholder string }{
	{"populate archlinux", "\x00OVX_POP\x00"},
	{"archlinux.org", "\x00OVX_ORG\x00"},
	{"archlinux-keyring", "\x00OVX_KR\x00"},
}

// archRenameText performs the protected archlinux→arch substitution. External
// registry refs (by shape) AND the non-registry literals are stashed behind
// placeholders before the global replace, then restored — so only genuine
// internal identifiers are renamed.
func archRenameText(s string) string {
	// 1. Stash external registry refs (shape-based; one placeholder per match).
	var protected []string
	s = archRenameExternalRefRe.ReplaceAllStringFunc(s, func(m string) string {
		ph := fmt.Sprintf("\x00OVX_RE%d\x00", len(protected))
		protected = append(protected, m)
		return ph
	})
	// 2. Stash the non-registry literals.
	for _, e := range archRenameExternals {
		s = strings.ReplaceAll(s, e.token, e.placeholder)
	}
	// 3. Rename every remaining (internal) archlinux identifier.
	s = strings.ReplaceAll(s, "archlinux", "arch")
	// 4. Restore literals, then the regex-protected refs (reverse order).
	for _, e := range archRenameExternals {
		s = strings.ReplaceAll(s, e.placeholder, e.token)
	}
	for i, p := range slices.Backward(protected) {
		s = strings.ReplaceAll(s, fmt.Sprintf("\x00OVX_RE%d\x00", i), p)
	}
	return s
}

// rewriteArchRenameFile applies archRenameText to one file. Missing files are
// skipped silently (most projects won't have every per-kind sibling).
func rewriteArchRenameFile(path string, dryRun bool) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, nil
	}
	out := archRenameText(string(data))
	if out == string(data) {
		return false, nil
	}
	if !dryRun {
		bak := fmt.Sprintf("%s.bak.%d", path, time.Now().Unix())
		_ = os.WriteFile(bak, data, 0644)
		if err := os.WriteFile(path, []byte(out), 0644); err != nil {
			return false, err
		}
	}
	return true, nil
}

// MigrateArchRename walks the in-repo project files (every layer.yml included)
// and the per-machine host deploy.yml, applying the archlinux→arch rename.
func MigrateArchRename(dir, hostDeployPath string, dryRun bool) ([]string, error) {
	var changed []string

	for _, f := range []string{
		"overthink.yml", "image.yml", "vm.yml", "deploy.yml", "eval.yml",
		"local.yml", "pod.yml", "k8s.yml", "build.yml", "base.yml",
	} {
		mod, err := rewriteArchRenameFile(filepath.Join(dir, f), dryRun)
		if err != nil {
			return changed, err
		}
		if mod {
			changed = append(changed, f)
		}
	}

	// Every layers/<name>/layer.yml — distro tag sections + package-map keys.
	if entries, err := os.ReadDir(filepath.Join(dir, "layers")); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			p := filepath.Join(dir, "layers", e.Name(), "layer.yml")
			if mod, err := rewriteArchRenameFile(p, dryRun); err == nil && mod {
				changed = append(changed, p)
			}
		}
	}

	// Per-machine deploy.yml (host overlay). Empty in project-only mode.
	if hostDeployPath != "" {
		if mod, err := rewriteArchRenameFile(hostDeployPath, dryRun); err == nil && mod {
			changed = append(changed, hostDeployPath)
		}
	}

	return changed, nil
}
