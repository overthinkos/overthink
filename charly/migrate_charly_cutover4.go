package main

// migrate_charly_cutover4.go — `charly migrate` step for "Cutover 4", the
// finishing pass of the ov→charly / overthink→opencharly rebrand. The earlier
// `charly-rebrand` step (it renamed the ov→charly namespace, candy paths, OCI
// labels, and OV_→CH_ env keys) deliberately KEPT three surfaces that this step
// now completes:
//
//   - the interim env prefix `CH_` is unified to the full word `CHARLY_`
//     (every `CH_<VAR>` env key in project/host YAML).
//   - the credential-store service prefix `ov/` (secret / enc / api-key / vnc /
//     probe) → `charly/` — both the `key:` overrides authored in YAML AND the
//     persisted entries in the OS keyring / per-host config (re-keyed in place).
//   - the inconsistent power-user image names `arch-charly` / `fedora-charly`
//     (distro-first) → `charly-arch` / `charly-fedora` (charly-first, matching
//     the `charly-cachyos` the cachyos step already produces).
//   - the fish shell-init `~/.config/fish/conf.d/overthink.fish` →
//     `opencharly.fish`; the pre-rebrand `# overthink:begin/end` managed block
//     is STRIPPED from every managed shell-init file (.bashrc / .zshenv /
//     .zshrc / .profile / the fish file) — the rebranded binary already writes
//     its own `# opencharly:` block, so rewriting the markers would duplicate
//     it; the stale block also sourced the relocated ~/.config/overthink/env.d
//     that no longer exists; the brand header of already-written
//     ~/.config/opencharly/env.d/*.env files is corrected in place.
//
// overthinkos infrastructure (GitHub org / ghcr registry / repo / Go module) is
// preserved — none of the rewrites below touch `github.com/overthinkos`,
// `ghcr.io/overthinkos`, or the module path.
//
// Comment-preserving via the yaml.v3 node API; idempotent (a fully-migrated tree
// is a no-op — every rewrite no-ops on absent source / pre-existing dest);
// per-file .bak.<unix-ts> backups on content rewrites. The registry entry is
// TouchesHost=false so the Phase A project-YAML rewrites ALSO run in the
// project-only / remote-cache runner; Phase B (keyring re-key + shell-init /
// env.d cleanup) is gated INTERNALLY on ctx.HostDeployPath, so a remote fetch
// never mutates per-host state. See CHANGELOG.md.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// charly4EnvTokenRe matches the interim `CH_`-prefixed env keys the rebrand step
// produced. `\bCH_` would also match the tail of words like BRANCH_/MATCH_, so
// the rewrite is applied only to whole tokens at a word boundary.
var charly4EnvTokenRe = regexp.MustCompile(`\bCH_[A-Z][A-Z0-9_]*`)

// charly4CredServicePrefixes are the credential-store service namespaces the
// rebrand left on `ov/`. Each is re-keyed to `charly/<tail>`.
var charly4CredServicePrefixes = []string{"secret", "enc", "api-key", "vnc", "probe"}

// charly4EntityRenames are the charly-first power-user image-name fixes applied
// as exact whole-token replacements in scalar values and mapping keys.
var charly4EntityRenames = [][2]string{
	{"arch-charly", "charly-arch"},
	{"fedora-charly", "charly-fedora"},
}

// MigrateCharlyCutover4 applies the cutover-4 finishing transforms.
func MigrateCharlyCutover4(ctx *MigrateContext) ([]string, error) {
	var changed []string
	dir := ctx.Dir

	// Phase A — content rewrites in every project YAML + candy.yml.
	rootFiles := []string{
		"charly.yml", "overthink.yml", "box.yml", "base.yml", "build.yml",
		"vm.yml", "pod.yml", "k8s.yml", "local.yml", "android.yml",
		"deploy.yml", "eval.yml",
	}
	for _, f := range rootFiles {
		mod, err := rewriteCutover4File(filepath.Join(dir, f), ctx.DryRun)
		if err != nil {
			return changed, err
		}
		if mod {
			changed = append(changed, f)
		}
	}
	candyDir := filepath.Join(dir, DefaultCandyDir)
	if entries, err := os.ReadDir(candyDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			p := filepath.Join(candyDir, e.Name(), "candy.yml")
			if mod, err := rewriteCutover4File(p, ctx.DryRun); err != nil {
				return changed, err
			} else if mod {
				changed = append(changed, filepath.Join(DefaultCandyDir, e.Name(), "candy.yml"))
			}
		}
	}

	// Phase B — host-gated: re-key the credential store + relocate the shell
	// init file. Skipped by the project-only / remote-cache runner.
	if ctx.HostDeployPath != "" {
		host, err := cutover4HostState(ctx)
		if err != nil {
			return changed, err
		}
		changed = append(changed, host...)
	}

	return changed, nil
}

// rewriteCutover4File rewrites one YAML file in place (comment-preserving) with
// a .bak backup. Absent / already-migrated files are a no-op.
func rewriteCutover4File(path string, dryRun bool) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, nil
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return false, fmt.Errorf("parse %s: %w", path, err)
	}
	if !rewriteCutover4Node(&root) {
		return false, nil
	}
	if dryRun {
		return true, nil
	}
	bak := fmt.Sprintf("%s.bak.%d", path, time.Now().Unix())
	if err := os.WriteFile(bak, data, 0o644); err != nil {
		return false, fmt.Errorf("backup %s: %w", path, err)
	}
	out, err := yaml.Marshal(&root)
	if err != nil {
		return false, err
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// rewriteCutover4Node walks a yaml node tree, rewriting both mapping KEYS and
// scalar VALUES via cutover4Scalar.
func rewriteCutover4Node(n *yaml.Node) bool {
	changed := false
	switch n.Kind {
	case yaml.DocumentNode:
		for _, c := range n.Content {
			if rewriteCutover4Node(c) {
				changed = true
			}
		}
	case yaml.MappingNode:
		// Content alternates key,value,key,value… — rewrite both.
		for _, c := range n.Content {
			if c.Kind == yaml.ScalarNode {
				if nv := cutover4Scalar(c.Value); nv != c.Value {
					c.Value = nv
					changed = true
				}
			} else if rewriteCutover4Node(c) {
				changed = true
			}
		}
	case yaml.SequenceNode:
		for _, c := range n.Content {
			if rewriteCutover4Node(c) {
				changed = true
			}
		}
	case yaml.ScalarNode:
		if nv := cutover4Scalar(n.Value); nv != n.Value {
			n.Value = nv
			changed = true
		}
	}
	return changed
}

// cutover4Scalar applies the cutover-4 string transforms to one scalar.
func cutover4Scalar(v string) string {
	// CH_<VAR> → CHARLY_<VAR> (whole-token; spares BRANCH_/MATCH_ tails).
	v = charly4EnvTokenRe.ReplaceAllStringFunc(v, func(tok string) string {
		return "CHARLY_" + strings.TrimPrefix(tok, "CH_")
	})
	// Credential service prefix `ov/<svc>` → `charly/<svc>` in `key:` overrides.
	for _, svc := range charly4CredServicePrefixes {
		v = strings.ReplaceAll(v, "ov/"+svc, "charly/"+svc)
	}
	// VM cloud-init schema field `ov_install:` → `charly_install:` (and the
	// persisted `ov_install_strategy:` in deploy.yml — covered by the same
	// substring rewrite). The Go struct tag was renamed to charly_install in the
	// rebrand; legacy configs still authoring ov_install migrate here.
	v = strings.ReplaceAll(v, "ov_install", "charly_install")
	// Charly-first power-user image names.
	for _, r := range charly4EntityRenames {
		v = strings.ReplaceAll(v, r[0], r[1])
	}
	return v
}

// cutover4HostState re-keys the credential store from `ov/*` to `charly/*` and
// relocates the fish shell-init file + managed-block markers.
func cutover4HostState(ctx *MigrateContext) ([]string, error) {
	var changed []string

	// Re-key the credential store: for each ov/<svc>, move every entry to
	// charly/<svc>. List/Get/Set/Delete on the default store; a backend that
	// cannot enumerate (returns an error) is skipped, not fatal.
	store := DefaultCredentialStore()
	for _, svc := range charly4CredServicePrefixes {
		oldSvc, newSvc := "ov/"+svc, "charly/"+svc
		keys, err := store.List(oldSvc)
		if err != nil {
			continue
		}
		for _, k := range keys {
			changed = append(changed, fmt.Sprintf("credential %s/%s → %s/%s", oldSvc, k, newSvc, k))
			if ctx.DryRun {
				continue // preview only — never mutate the keyring in dry-run
			}
			val, err := store.Get(oldSvc, k)
			if err != nil {
				continue
			}
			if _, gerr := store.Get(newSvc, k); gerr != nil {
				if err := store.Set(newSvc, k, val); err != nil {
					return changed, fmt.Errorf("re-key %s/%s → %s/%s: %w", oldSvc, k, newSvc, k, err)
				}
			}
			_ = store.Delete(oldSvc, k)
		}
	}

	if home, err := os.UserHomeDir(); err == nil {
		// Relocate the legacy fish shell-init file overthink.fish → opencharly.fish.
		oldFish := filepath.Join(home, ".config", "fish", "conf.d", "overthink.fish")
		newFish := filepath.Join(home, ".config", "fish", "conf.d", "opencharly.fish")
		if mod, err := renameProjectPath(oldFish, newFish, ctx.DryRun); err == nil && mod {
			changed = append(changed, newFish)
		}
		// Strip the pre-rebrand `# overthink:` managed block from every shell
		// init file charly manages — zsh's is .zshenv (NOT .zshrc), bash's is
		// .bashrc, sh's is .profile, fish's is the relocated opencharly.fish.
		// The rebranded binary already wrote its own `# opencharly:` block; the
		// legacy block only sources the relocated ~/.config/overthink/env.d that
		// no longer exists, so it errors on every shell startup. Rewriting the
		// markers in place (the prior approach) would DUPLICATE the opencharly
		// block whenever charly had already written one — stripping is the clean
		// cutover. Uses the same stripLegacyOverthinkBlocks helper charly
		// self-heals with on deploy (R3).
		for _, rc := range []string{
			filepath.Join(home, ".bashrc"),
			filepath.Join(home, ".zshenv"),
			filepath.Join(home, ".zshrc"),
			filepath.Join(home, ".profile"),
			newFish,
		} {
			if mod, err := cutover4StripLegacyBlocks(rc, ctx.DryRun); err == nil && mod {
				changed = append(changed, rc)
			}
		}
		// Rewrite the stale brand header in every already-written env.d file
		// (`# overthink env for layer … managed by ov`). The rebranded generator
		// now emits the correct header; this fixes files the old binary wrote.
		envdDir := EnvdDir(home)
		if entries, derr := os.ReadDir(envdDir); derr == nil {
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".env") {
					continue
				}
				p := filepath.Join(envdDir, e.Name())
				if mod, err := cutover4RewriteEnvdHeader(p, ctx.DryRun); err == nil && mod {
					changed = append(changed, p)
				}
			}
		}
	}

	return changed, nil
}

// cutover4StripLegacyBlocks removes the pre-rebrand `# overthink:` managed
// block(s) from one shell-init file via the shared stripLegacyOverthinkBlocks
// helper (R3 — the same logic charly's managed-block writer self-heals with).
// The rebranded binary's own `# opencharly:` block supersedes them. No-op (and
// no .bak written) when the file is absent or carries no legacy block.
func cutover4StripLegacyBlocks(path string, dryRun bool) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, nil
	}
	out := stripLegacyOverthinkBlocks(string(data))
	if out == string(data) {
		return false, nil
	}
	if dryRun {
		return true, nil
	}
	bak := fmt.Sprintf("%s.bak.%d", path, time.Now().Unix())
	if err := os.WriteFile(bak, data, 0o644); err != nil {
		return false, fmt.Errorf("backup %s: %w", path, err)
	}
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// cutover4RewriteEnvdHeader rewrites the stale brand header in one generated
// env.d file: `# overthink env for layer …` → `# opencharly env for layer …`
// and `managed by ov;` → `managed by charly;`. These files were emitted by the
// pre-rebrand binary; the rebranded generator now emits the correct header, so
// this is a one-time cleanup of already-written files. No-op when current.
func cutover4RewriteEnvdHeader(path string, dryRun bool) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, nil
	}
	s := string(data)
	out := strings.ReplaceAll(s, "# overthink env for layer", "# opencharly env for layer")
	out = strings.ReplaceAll(out, "managed by ov;", "managed by charly;")
	if out == s {
		return false, nil
	}
	if dryRun {
		return true, nil
	}
	bak := fmt.Sprintf("%s.bak.%d", path, time.Now().Unix())
	if err := os.WriteFile(bak, data, 0o644); err != nil {
		return false, fmt.Errorf("backup %s: %w", path, err)
	}
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		return false, err
	}
	return true, nil
}
