package main

// migrate_marimo_rename.go — `ov migrate marimo-rename`.
//
// One-shot migration that renames the legacy `marimo-ml` image and the
// `marimo-ml-pod` deployment straight to the post-2026-05 canonical
// `versa` (cross-kind name reuse — see CLAUDE.md "Cross-kind name
// reuse is permitted and encouraged"). Skips the intermediate `marimo`
// state that briefly existed between the 2026-04 marimo-ml → marimo
// cutover and the 2026-05 marimo → versa cutover. Walks both:
//
//   - the in-repo overthink.yml + deploy.yml + image.yml (project)
//   - the per-machine ~/.config/ov/deploy.yml (user overlay)
//
// Idempotent. Running twice is a no-op (modulo printing "nothing to
// migrate"). The rewrite is textual, longest-first — `marimo-ml-pod`
// is rewritten BEFORE `marimo-ml` so the substring overlap collapses
// cleanly to a single canonical name.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// MigrateMarimoRenameCmd is `ov migrate marimo-rename`.
type MigrateMarimoRenameCmd struct {
	DryRun bool `long:"dry-run" help:"Print files that would be modified, don't touch the filesystem"`
}

func (c *MigrateMarimoRenameCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	changed, err := MigrateMarimoRename(dir, c.DryRun)
	if err != nil {
		return err
	}
	prefix := "modified "
	if c.DryRun {
		prefix = "[dry-run] would modify "
	}
	if len(changed) == 0 {
		fmt.Println("ov migrate marimo-rename: nothing to migrate (already renamed)")
		return nil
	}
	for _, p := range changed {
		fmt.Println(prefix + p)
	}
	return nil
}

// MigrateMarimoRename walks both the in-repo project files and the
// per-machine ~/.config/ov/deploy.yml, applying the marimo-ml(-pod) →
// versa rename. Also cleans up stale systemd quadlets + running
// containers under the OLD deploy names so a follow-up `ov update`
// generates the new quadlet cleanly. Returns the list of touched
// files (quadlet deletions and unit stops are reported separately
// via stdout from cleanupRenamedDeploy).
func MigrateMarimoRename(dir string, dryRun bool) ([]string, error) {
	var changed []string

	projectFiles := []string{
		filepath.Join(dir, "overthink.yml"),
		filepath.Join(dir, "deploy.yml"),
		filepath.Join(dir, "image.yml"),
	}
	for _, p := range projectFiles {
		modified, err := rewriteMarimoRenameFile(p, dryRun)
		if err != nil {
			return changed, err
		}
		if modified {
			changed = append(changed, p)
		}
	}

	// Per-machine deploy.yml. Errors silently fall through — most
	// users won't have one, and the per-repo edit alone is sufficient.
	if home, err := os.UserHomeDir(); err == nil {
		userFile := filepath.Join(home, ".config", "ov", "deploy.yml")
		if modified, err := rewriteMarimoRenameFile(userFile, dryRun); err == nil && modified {
			changed = append(changed, userFile)
		}
	}

	// Stale-state cleanup: the previous container name was
	// ov-marimo-ml-pod (or ov-marimo-ml-<instance> for instances like
	// ecovoyage). The systemd quadlets and running containers under
	// those names still reference the retired image tag and ContainerName;
	// `ov update <new>` would fail trying to rewrite a quadlet at the
	// new path. Stop + remove both surfaces so the next `ov config` /
	// `ov update` regenerates them cleanly. Best-effort: each step
	// silently no-ops if the resource doesn't exist (idempotent).
	if !dryRun {
		cleanupRenamedDeploy("ov-marimo-ml-pod")
		// Instances follow the same naming. Discover them by scanning
		// the user's quadlet dir for ov-marimo-ml-* (excluding the base
		// pod handled above).
		if home, err := os.UserHomeDir(); err == nil {
			quadletDir := filepath.Join(home, ".config", "containers", "systemd")
			if entries, err := os.ReadDir(quadletDir); err == nil {
				for _, e := range entries {
					name := strings.TrimSuffix(e.Name(), ".container")
					if name == e.Name() {
						continue
					}
					if name == "ov-marimo-ml-pod" {
						continue
					}
					if strings.HasPrefix(name, "ov-marimo-ml-") {
						cleanupRenamedDeploy(name)
					}
				}
			}
		}
	}

	return changed, nil
}

// cleanupRenamedDeploy removes the systemd + podman state that the
// pre-cutover deploy name left behind. Idempotent — each step is a
// best-effort no-op when the corresponding resource does not exist.
//
// Sequence (mirrors what `ov remove` does, minus the deploy.yml edit
// because the YAML rewrite step already handled that):
//
//  1. systemctl --user stop <name>.service
//  2. podman rm -f <name> (covers stopped + running container forms)
//  3. delete the quadlet file at ~/.config/containers/systemd/<name>.container
//  4. systemctl --user daemon-reload (so systemd forgets the unit)
//
// Status messages go to stdout so users see what the migration cleaned
// up — silent cleanup would surprise operators who track quadlet
// state by hand.
func cleanupRenamedDeploy(name string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	quadlet := filepath.Join(home, ".config", "containers", "systemd", name+".container")
	if _, err := os.Stat(quadlet); err != nil {
		// Nothing to clean — no quadlet under this name.
		return
	}
	// 1. Stop the user-mode systemd unit. Best-effort.
	exec.Command("systemctl", "--user", "stop", name+".service").Run()
	// 2. Force-remove the container (covers running + stopped).
	exec.Command("podman", "rm", "-f", name).Run()
	// 3. Delete the now-stale quadlet.
	os.Remove(quadlet)
	// 4. Reload systemd so it forgets the unit; otherwise enabling the
	//    fresh quadlet emits a "Unit was already loaded from disk"
	//    transient warning that confuses bug reports.
	exec.Command("systemctl", "--user", "daemon-reload").Run()
	fmt.Printf("  cleaned up stale deploy state: %s (quadlet + systemd unit + container)\n", name)
}

// rewriteMarimoRenameFile applies the rename to a single YAML file.
// Returns (modified, error). Missing files are NOT errors — the
// migration is opportunistic per file.
func rewriteMarimoRenameFile(path string, dryRun bool) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("reading %s: %w", path, err)
	}
	updated := applyMarimoRenameRewrites(string(data))
	if updated == string(data) {
		return false, nil
	}
	if dryRun {
		return true, nil
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return false, fmt.Errorf("writing %s: %w", path, err)
	}
	return true, nil
}

// applyMarimoRenameRewrites returns src with all rename rewrites
// applied. Order is load-bearing: the longer substring (`marimo-ml-pod`)
// must rewrite first so that the residual `marimo-ml` rewrite never
// re-touches an already-canonical name.
func applyMarimoRenameRewrites(src string) string {
	out := strings.ReplaceAll(src, "marimo-ml-pod", "versa")
	out = strings.ReplaceAll(out, "marimo-ml", "versa")
	return out
}
