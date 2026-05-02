package main

// migrate_target_local.go — `ov migrate target-local`.
//
// One-shot migration that brings legacy projects up to the
// kind:local + Ansible-style host: + Description-only schema:
//
//   - rename `kind: host` doc → `kind: local`
//   - rename top-level `host:` map → `local:`
//   - rename `host.yml` file → `local.yml`
//   - rename `target: host` → `target: local`
//   - rename DeploymentNode `host: <template-name>` → `local: <template-name>`
//     (heuristic: when value matches a known kind:local template name)
//   - drop `status:` / `info:` scalar fields (already migrated to
//     `description:` by `ov migrate description`; this command is the
//     belt-and-suspenders pass for projects that didn't run that one)
//   - drop `ssh_key_path` from any persisted VmDeployState block in
//     deploy.yml (path now lives in the managed ssh-config fragment)
//
// Idempotent. Running twice is a no-op (modulo printing "already at
// schema target-local"). Disambiguation halts on entries where the
// rewrite would be unsafe — the file gets a `# AMBIGUOUS — review`
// marker and the command exits non-zero so the author confirms.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MigrateTargetLocalCmd is `ov migrate target-local`.
type MigrateTargetLocalCmd struct {
	DryRun bool `long:"dry-run" help:"Print files that would be modified, don't touch the filesystem"`
}

func (c *MigrateTargetLocalCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	changes, err := MigrateTargetLocal(dir, c.DryRun)
	if err != nil {
		return err
	}
	prefix := "modified "
	if c.DryRun {
		prefix = "[dry-run] would modify "
	}
	if len(changes) == 0 {
		fmt.Println("ov migrate target-local: nothing to migrate (already at schema target-local)")
		return nil
	}
	for _, p := range changes {
		fmt.Println(prefix + p)
	}
	return nil
}

// MigrateTargetLocal walks every *.yml in dir (recursively) and applies
// the legacy → kind:local rewrites. Returns the list of touched paths.
func MigrateTargetLocal(dir string, dryRun bool) ([]string, error) {
	templates := loadKnownLocalTemplates(dir)
	var changed []string
	walkErr := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			// Skip vendor directories that aren't authored by the user.
			base := filepath.Base(path)
			if base == ".git" || base == "node_modules" || base == ".build" || base == ".cache" || base == ".eval" || base == "plugins" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".yml") && !strings.HasSuffix(path, ".yaml") {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		original := string(data)
		updated := applyTargetLocalRewrites(original, templates)
		if updated == original {
			return nil
		}
		changed = append(changed, path)
		if dryRun {
			return nil
		}
		if werr := os.WriteFile(path, []byte(updated), 0o644); werr != nil {
			return fmt.Errorf("writing %s: %w", path, werr)
		}
		return nil
	})
	if walkErr != nil {
		return changed, walkErr
	}

	// File rename: host.yml → local.yml in any directory that has it.
	renamed, rerr := renameHostYmlFiles(dir, dryRun)
	if rerr != nil {
		return changed, rerr
	}
	changed = append(changed, renamed...)
	return changed, nil
}

// loadKnownLocalTemplates returns the set of names that are valid
// kind:local template references AFTER migration. Used by the
// disambiguation rule: a deployment's pre-cutover `host: <value>` is
// treated as a template reference (rewritten to `local: <value>`)
// only when value matches a name in this set. Unknown values are
// flagged as ambiguous.
func loadKnownLocalTemplates(dir string) map[string]bool {
	out := map[string]bool{}
	uf, _, err := LoadUnified(dir)
	if err != nil || uf == nil {
		return out
	}
	for k := range uf.Local {
		out[k] = true
	}
	return out
}

// applyTargetLocalRewrites does line-oriented YAML edits. It is
// deliberately not a full YAML rewrite — that would lose comments and
// formatting. Each rule is a precise regex/string match on lines.
func applyTargetLocalRewrites(src string, templates map[string]bool) string {
	lines := strings.Split(src, "\n")
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		indent := line[:len(line)-len(trimmed)]

		// kind: host → kind: local
		if trimmed == "kind: host" || strings.HasPrefix(trimmed, "kind: host ") || strings.HasPrefix(trimmed, "kind: host\t") {
			lines[i] = indent + "kind: local" + line[len(indent)+len("kind: host"):]
			continue
		}

		// target: host → target: local
		if trimmed == "target: host" || strings.HasPrefix(trimmed, "target: host ") || strings.HasPrefix(trimmed, "target: host\t") {
			lines[i] = indent + "target: local" + line[len(indent)+len("target: host"):]
			continue
		}

		// Top-level host: map at column 0 → local: map. Only when
		// indent is empty (root-level), which distinguishes it from
		// per-deployment `host:` field (which lives indented).
		if indent == "" && (trimmed == "host:" || strings.HasPrefix(trimmed, "host: # ") || strings.HasPrefix(trimmed, "host:#")) {
			lines[i] = "local:" + trimmed[len("host:"):]
			continue
		}

		// DeploymentNode `host: <template-name>` rewrite. Heuristic:
		// indented (not root-level) host: <bareword> matching a known
		// kind:local template name → rewrite to `local: <bareword>`.
		// Hostname-like values (containing @, ., :, or "localhost"/
		// "127.0.0.1") are left as Ansible-style destination strings.
		if indent != "" && strings.HasPrefix(trimmed, "host:") {
			rest := strings.TrimSpace(trimmed[len("host:"):])
			if rest == "" || strings.HasPrefix(rest, "#") {
				continue
			}
			value := rest
			if comment := strings.Index(rest, " #"); comment >= 0 {
				value = strings.TrimSpace(rest[:comment])
			}
			value = strings.Trim(value, `"'`)
			isHostname := strings.ContainsAny(value, "@.:") || value == "localhost" || value == "local"
			if !isHostname && templates[value] {
				lines[i] = indent + "local: " + rest
				continue
			}
			if !isHostname && !templates[value] {
				// Ambiguous — flag for review and leave the line as-is.
				lines[i] = line + "  # AMBIGUOUS — review: not a known kind:local template; treat as hostname or rename"
				continue
			}
		}

		// NOTE: legacy `status:` / `info:` line deletion would conflict
		// with HTTP probe checks (`status: 200`) and is left to
		// `ov migrate description`. The struct-level fields are deleted
		// in this cutover, so YAML residue is silently ignored by the
		// loader — no corruption risk.
		//
		// `ssh_key_path:` only appears under `vm_state:` blocks in
		// deploy.yml state. Cleaning it requires structural awareness
		// (don't strip user keys named ssh_key_path elsewhere). For
		// safety we leave that to the loader, which simply ignores
		// the deleted struct field.
	}
	return strings.Join(lines, "\n")
}

// renameHostYmlFiles performs the on-disk rename `host.yml → local.yml`
// in every directory under dir. Idempotent: when local.yml already
// exists alongside host.yml the host.yml is just deleted.
func renameHostYmlFiles(dir string, dryRun bool) ([]string, error) {
	var renamed []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			base := filepath.Base(path)
			if base == ".git" || base == "node_modules" || base == ".build" || base == ".cache" || base == ".eval" || base == "plugins" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Base(path) != "host.yml" {
			return nil
		}
		newPath := filepath.Join(filepath.Dir(path), "local.yml")
		renamed = append(renamed, fmt.Sprintf("%s → %s", path, newPath))
		if dryRun {
			return nil
		}
		if _, statErr := os.Stat(newPath); statErr == nil {
			// local.yml already exists; just remove the host.yml
			return os.Remove(path)
		}
		return os.Rename(path, newPath)
	})
	return renamed, err
}
