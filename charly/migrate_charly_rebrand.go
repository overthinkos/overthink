package main

// migrate_charly_rebrand.go — `charly migrate` step for the 2026-06 ov→charly /
// overthink→opencharly rebrand.
//
// The CLI binary `ov` became `charly` and the project/OS name `overthink`
// became `opencharly`. The `overthinkos` GitHub org + `ghcr.io/overthinkos`
// registry + repo names are KEPT (infrastructure), so this step does NOT touch
// any `github.com/overthinkos/overthink` ref, registry path, or the module
// path — only the brand surface a user authored.
//
// This step, in a project tree (and, host-gated, in the per-host config dir):
//   - renames the project root config `overthink.yml` → `charly.yml`.
//   - rewrites SCALAR values in every project YAML:
//       * `@github…/candy/ov[-mcp]:vTAG` layer-ref path segment → `/candy/charly[-mcp]`.
//       * `org.overthinkos.*` label strings → `ai.opencharly.*`.
//       * qualified import-namespace refs `ov.<member>` → `charly.<member>`.
//   - renames the import-namespace ALIAS key `ov:` → `charly:` (the alias every
//     submodule mounts the main repo under).
//   - host-gated (the full runner passes ~/.config/charly paths; the project-only /
//     remote-cache runner leaves them empty): relocates the per-host state dirs
//     `~/.config/ov`→`~/.config/charly`, `~/.cache/ov`→`~/.cache/charly`,
//     `~/.config/overthink`→`~/.config/opencharly`, `~/.local/share/ov`→
//     `~/.local/share/charly`, rewrites `OV_*`→`CH_*` env keys + `.config/ov`
//     path strings inside the relocated deploy.yml/config.yml, and MUTATES the
//     ctx pointers so the always-last calver-schema stamp lands on the relocated
//     file.
//
// Comment-preserving via the yaml.v3 node API; idempotent (a fully-migrated tree
// is a no-op — both the pre- and post-rename filenames are processed, and every
// rename helper no-ops on a missing source / pre-existing destination);
// per-file .bak.<unix-ts> backups on content rewrites. TouchesHost is false so
// remote-cache auto-migration applies the project-side rewrites to fetched
// repos too (its host paths are empty, so the relocation block is skipped).
// See CHANGELOG.md.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// charlyEnvSentinels are the EXACT OV_-prefixed tokens that are heredoc/script
// delimiters in generated shell bodies, NOT env-contract variables. They must
// survive the OV_→CH_ env-prefix rewrite verbatim (an unbalanced heredoc
// delimiter breaks the script). Matched exactly, so the env var CH_REPO_CACHE is
// still renamed while the OV_REPO heredoc sentinel is preserved.
var charlyEnvSentinels = map[string]bool{
	"OV_ROOT": true, "OV_USER": true, "OV_DROPIN": true, "OV_REPO": true,
	"OV_WRITE": true, "OV_UNIT": true, "OV_SNIPPET": true,
	"OV_NESTED_SCRIPT_EOF": true, "OV_LEDGER_EOF": true,
}

var charlyEnvTokenRe = regexp.MustCompile(`OV_[A-Z][A-Z0-9_]*`)

// charlyRewriteEnvPrefix renames every OV_<NAME> env-contract token to CH_<NAME>,
// preserving the exact heredoc sentinels in charlyEnvSentinels. Shared by the
// host-file migration (here) and the Group-C source rewrite.
func charlyRewriteEnvPrefix(s string) string {
	return charlyEnvTokenRe.ReplaceAllStringFunc(s, func(tok string) string {
		if charlyEnvSentinels[tok] {
			return tok
		}
		return "CH_" + tok[len("OV_"):]
	})
}

// MigrateCharlyRebrand applies the ov→charly / overthink→opencharly rebrand to a
// project tree. ctx carries the project dir + (when non-empty) the per-host
// paths to relocate; the ctx pointers are mutated in place so a later step
// (calver-schema) stamps the relocated host files.
func MigrateCharlyRebrand(ctx *MigrateContext) ([]string, error) {
	var changed []string
	dir := ctx.Dir

	// Phase A — content rewrites in every project YAML (both pre- and
	// post-rename root filename, for idempotency).
	rootFiles := []string{
		"overthink.yml", "charly.yml", "box.yml", "base.yml", "build.yml",
		"vm.yml", "pod.yml", "k8s.yml", "local.yml", "android.yml",
		"deploy.yml", "eval.yml",
	}
	for _, f := range rootFiles {
		mod, err := rewriteCharlyRebrandFile(filepath.Join(dir, f), ctx.DryRun)
		if err != nil {
			return changed, err
		}
		if mod {
			changed = append(changed, f)
		}
	}
	// candy/<name>/candy.yml — layer refs (require:) + label strings.
	candyDir := filepath.Join(dir, DefaultCandyDir)
	if entries, err := os.ReadDir(candyDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			p := filepath.Join(candyDir, e.Name(), "candy.yml")
			mod, err := rewriteCharlyRebrandFile(p, ctx.DryRun)
			if err != nil {
				return changed, err
			}
			if mod {
				changed = append(changed, filepath.Join(DefaultCandyDir, e.Name(), "candy.yml"))
			}
		}
	}

	// Phase B — rename the project root config file.
	if mod, err := renameProjectPath(filepath.Join(dir, "overthink.yml"), filepath.Join(dir, "charly.yml"), ctx.DryRun); err != nil {
		return changed, err
	} else if mod {
		changed = append(changed, "charly.yml")
	}

	// Phase C — host-gated per-host state relocation.
	if ctx.HostDeployPath != "" {
		host, err := relocateHostStateForCharly(ctx)
		if err != nil {
			return changed, err
		}
		changed = append(changed, host...)
	}

	return changed, nil
}

// rewriteCharlyRebrandFile rewrites one YAML file in place (yaml.v3 node API,
// comment-preserving) with a .bak backup. Returns false (no error) when the
// file is absent or already fully migrated.
func rewriteCharlyRebrandFile(path string, dryRun bool) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, nil // absent — nothing to do
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return false, fmt.Errorf("parse %s: %w", path, err)
	}
	if !rewriteCharlyRebrandNode(&root, false) {
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

// rewriteCharlyRebrandNode walks a yaml node tree, rewriting the import-namespace
// alias KEY `ov`→`charly` and every brand SCALAR value. inImport marks the
// `import:` list so a single-key `{ov: ref}` alias map is renamed only there.
func rewriteCharlyRebrandNode(n *yaml.Node, inImport bool) bool {
	changed := false
	switch n.Kind {
	case yaml.DocumentNode:
		for _, c := range n.Content {
			if rewriteCharlyRebrandNode(c, false) {
				changed = true
			}
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			key, val := n.Content[i], n.Content[i+1]
			// Rename the import-namespace alias key `ov:` → `charly:` (only as
			// a single-key alias map inside the import: list).
			if inImport && key.Kind == yaml.ScalarNode && key.Value == "ov" {
				key.Value = "charly"
				changed = true
			}
			childInImport := inImport || (key.Kind == yaml.ScalarNode && key.Value == "import")
			if rewriteCharlyRebrandNode(val, childInImport) {
				changed = true
			}
		}
	case yaml.SequenceNode:
		for _, c := range n.Content {
			if rewriteCharlyRebrandNode(c, inImport) {
				changed = true
			}
		}
	case yaml.ScalarNode:
		if nv := rewriteCharlyBrandScalar(n.Value); nv != n.Value {
			n.Value = nv
			changed = true
		}
	}
	return changed
}

// rewriteCharlyBrandScalar applies the brand string transforms to one scalar
// value. overthinkos infrastructure (org/registry/repo/module) is preserved by
// shape: the rewrites target only `/candy/ov`, the `org.overthinkos.` label
// prefix, and a leading `ov.` namespace qualifier — none of which appear inside
// `github.com/overthinkos/overthink`, `ghcr.io/overthinkos/…`, or `overthinkos`.
func rewriteCharlyBrandScalar(v string) string {
	// Layer-ref path segment: …/candy/ov-mcp:vTAG and …/candy/ov:vTAG.
	v = strings.ReplaceAll(v, "/candy/ov-mcp", "/candy/charly-mcp")
	v = strings.ReplaceAll(v, "/candy/ov:", "/candy/charly:")
	if strings.HasSuffix(v, "/candy/ov") {
		v = strings.TrimSuffix(v, "/candy/ov") + "/candy/charly"
	}
	// OCI label namespace.
	v = strings.ReplaceAll(v, "org.overthinkos.", "ai.opencharly.")
	// Qualified import-namespace ref: `ov.<member>` (e.g. ov.arch-builder).
	if strings.HasPrefix(v, "ov.") {
		v = "charly." + v[len("ov."):]
	}
	return v
}

// relocateHostStateForCharly relocates the per-host state directories and
// rewrites the relocated deploy.yml/config.yml content, then mutates ctx so the
// trailing calver-schema stamp lands on the new paths.
func relocateHostStateForCharly(ctx *MigrateContext) ([]string, error) {
	var changed []string
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	cacheDir := filepath.Join(home, ".cache")
	shareDir := filepath.Join(home, ".local", "share")

	// dir relocations: (from, to) — ov→charly (CLI-named) + overthink→opencharly (project-named).
	relocs := [][2]string{
		{filepath.Join(cfgDir, "ov"), filepath.Join(cfgDir, "charly")},
		{filepath.Join(cfgDir, "overthink"), filepath.Join(cfgDir, "opencharly")},
		{filepath.Join(cacheDir, "ov"), filepath.Join(cacheDir, "charly")},
		{filepath.Join(shareDir, "ov"), filepath.Join(shareDir, "charly")},
	}
	for _, r := range relocs {
		mod, err := renameProjectPath(r[0], r[1], ctx.DryRun)
		if err != nil {
			return changed, err
		}
		if mod {
			changed = append(changed, r[1])
		}
	}

	// New host file locations after the ~/.config/charly → ~/.config/charly move.
	newDeploy := filepath.Join(cfgDir, "charly", "deploy.yml")
	newConfig := filepath.Join(cfgDir, "charly", "config.yml")

	// Rewrite OV_* env keys + .config/charly path strings inside the relocated files.
	for _, p := range []string{newDeploy, newConfig} {
		if mod, err := rewriteHostFileForCharly(p, ctx.DryRun); err != nil {
			return changed, err
		} else if mod {
			changed = append(changed, p)
		}
	}

	// Mutate ctx so calver-schema (always last) stamps the relocated files.
	if !ctx.DryRun {
		ctx.HostDeployPath = newDeploy
		ctx.HostConfigPath = newConfig
	}
	return changed, nil
}

// rewriteHostFileForCharly rewrites OV_* → CH_* env keys and .config/charly path
// strings in a relocated host file (plain text — these files carry no
// brand-named YAML keys, only values + env maps).
func rewriteHostFileForCharly(path string, dryRun bool) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, nil
	}
	s := string(data)
	out := charlyHostTextRewrite(s)
	if out == s {
		return false, nil
	}
	if dryRun {
		return true, nil
	}
	bak := fmt.Sprintf("%s.bak.%d", path, time.Now().Unix())
	if err := os.WriteFile(bak, data, 0o644); err != nil {
		return false, err
	}
	return true, os.WriteFile(path, []byte(out), 0o644)
}

// charlyHostTextRewrite applies the host-file string transforms (env-prefix +
// path component). Split out for unit testing.
func charlyHostTextRewrite(s string) string {
	s = strings.ReplaceAll(s, ".config/ov/", ".config/charly/")
	s = strings.ReplaceAll(s, ".cache/ov/", ".cache/charly/")
	s = strings.ReplaceAll(s, ".config/overthink/", ".config/opencharly/")
	s = charlyRewriteEnvPrefix(s)
	return s
}
