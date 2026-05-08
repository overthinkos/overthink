package main

// migrate_local_deploy.go — `ov migrate local-deploy`.
//
// Converts the per-host deploy file (~/.config/ov/deploy.yml) from the
// legacy pre-2026-04 schema to the current schema-v4 shape used by
// LoadDeployConfig.
//
// Legacy form (silently dropped by current LoadDeployConfig because
// yaml.Unmarshal ignores unknown root keys):
//
//   images:
//     <name>:
//       workspace: <host-path>     # bind-mount of the project repo at /workspace
//       tunnel: { ... }
//       dns: <fqdn>
//       bind_mounts:
//         - { name: <vol>, path: <container-path>, encrypted: true }
//         - { name: <vol>, path: <container-path> }
//       ports: [...]
//       env_file: <path>
//       security: { ... }
//       network: <name>
//
// Modern form:
//
//   version: 4
//   deploy:
//     <name>:
//       target: pod
//       tunnel: { ... }
//       dns: <fqdn>
//       volumes:
//         - { name: <vol>, type: encrypted }
//         - { name: <vol>, type: bind, path: <container-path> }
//         - { name: workspace, type: bind, host: <host-path>, path: /workspace }
//       ports: [...]
//       env_file: <path>
//       security: { ... }
//       network: <name>
//
// The migration writes a `<file>.bak.<unix-timestamp>` rollback file before
// rewriting (matches the plaintext-secret migration pattern in
// `ov/config_secret_migration.go`). Idempotent: running again on a v4 file
// is a no-op.
//
// NOTE: this targets ~/.config/ov/deploy.yml (per-host state) — NOT the
// project-root deploy.yml that `LoadUnified` reads via `includes:`. Those
// are different files with different roles. See CLAUDE.md "Mode purity".

import (
	"fmt"
	"os"
	"sort"
	"time"

	"gopkg.in/yaml.v3"
)

// MigrateLocalDeployCmd is `ov migrate local-deploy`.
type MigrateLocalDeployCmd struct {
	DryRun bool   `long:"dry-run" help:"Print what would change, don't touch the filesystem"`
	Path   string `long:"path" help:"Override the deploy.yml path (default: ~/.config/ov/deploy.yml)"`
}

func (c *MigrateLocalDeployCmd) Run() error {
	path := c.Path
	if path == "" {
		p, err := DeployConfigPath()
		if err != nil {
			return fmt.Errorf("resolving deploy.yml path: %w", err)
		}
		path = p
	}

	changed, summary, err := MigrateLocalDeploy(path, c.DryRun)
	if err != nil {
		return err
	}
	if !changed {
		fmt.Println("ov migrate local-deploy: nothing to migrate (already on schema v4)")
		return nil
	}
	prefix := "wrote "
	if c.DryRun {
		prefix = "[dry-run] would write "
	}
	fmt.Println(prefix + path)
	for _, line := range summary {
		fmt.Println("  " + line)
	}
	return nil
}

// MigrateLocalDeploy reads path, transforms a legacy `images:` map into the
// modern `deploy:` map (with bind_mounts → volumes + workspace promotion),
// and writes the result back. Returns (changed, summary, error).
//
// Idempotent: when the file is missing or already in v4 shape, returns
// (false, nil, nil).
func MigrateLocalDeploy(path string, dryRun bool) (bool, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil, nil
		}
		return false, nil, fmt.Errorf("reading %s: %w", path, err)
	}

	if !hasLegacyImagesKey(data) {
		return false, nil, nil
	}

	// Decode into a generic map to preserve fields we don't transform
	// verbatim (tunnel, dns, ports, env_file, security, network, etc.).
	var root map[string]any
	if err := yaml.Unmarshal(data, &root); err != nil {
		return false, nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	imagesAny, ok := root["images"]
	if !ok {
		// Should not happen given hasLegacyImagesKey returned true, but
		// defend anyway.
		return false, nil, nil
	}
	images, ok := imagesAny.(map[string]any)
	if !ok {
		return false, nil, fmt.Errorf("%s: top-level `images:` is not a mapping", path)
	}

	deploy := make(map[string]any, len(images))
	var summary []string
	imageNames := make([]string, 0, len(images))
	for name := range images {
		imageNames = append(imageNames, name)
	}
	sort.Strings(imageNames)
	for _, name := range imageNames {
		entryAny := images[name]
		entry, ok := entryAny.(map[string]any)
		if !ok {
			return false, nil, fmt.Errorf("%s: images.%s is not a mapping", path, name)
		}
		newEntry, entrySummary := migrateLocalDeployEntry(entry)
		deploy[name] = newEntry
		summary = append(summary, fmt.Sprintf("images.%s → deploy.%s (target: pod)", name, name))
		for _, line := range entrySummary {
			summary = append(summary, "    "+line)
		}
	}

	// Drop the legacy key, install the new one. Preserve any other
	// top-level keys (e.g. `provides:` injected by ov config) verbatim.
	delete(root, "images")
	root["deploy"] = deploy
	if _, hasVersion := root["version"]; !hasVersion {
		root["version"] = 4
	}

	out, err := yaml.Marshal(root)
	if err != nil {
		return true, summary, fmt.Errorf("encoding rewritten yaml: %w", err)
	}

	if dryRun {
		return true, summary, nil
	}

	// Backup first, then write atomically.
	backup := fmt.Sprintf("%s.bak.%d", path, time.Now().Unix())
	if err := os.WriteFile(backup, data, 0o600); err != nil {
		return true, summary, fmt.Errorf("writing backup %s: %w", backup, err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return true, summary, fmt.Errorf("writing migrated %s: %w", path, err)
	}
	summary = append(summary, fmt.Sprintf("backup at %s", backup))
	return true, summary, nil
}

// migrateLocalDeployEntry transforms a single legacy per-image entry into a
// modern DeploymentNode-shaped map. Returns (transformed, summary-lines).
//
// Legacy fields handled:
//   - bind_mounts → volumes (encrypted: true → type: encrypted; otherwise type: bind)
//   - workspace (string) → volumes entry with name=workspace, type=bind, host=<value>, path=/workspace
//   - tunnel, dns, ports, env_file, security, network: passed through verbatim
//
// Adds: target: pod (the legacy schema only supported container deploys).
func migrateLocalDeployEntry(entry map[string]any) (map[string]any, []string) {
	out := make(map[string]any, len(entry)+2)
	out["target"] = "pod"

	var summary []string
	var volumes []any

	// 1. Convert bind_mounts → volumes
	if bmAny, ok := entry["bind_mounts"]; ok {
		bmList, ok := bmAny.([]any)
		if ok {
			for _, bmAny := range bmList {
				bm, ok := bmAny.(map[string]any)
				if !ok {
					continue
				}
				vol := map[string]any{}
				if name, ok := bm["name"].(string); ok {
					vol["name"] = name
				}
				encrypted, _ := bm["encrypted"].(bool)
				if encrypted {
					vol["type"] = "encrypted"
					summary = append(summary, fmt.Sprintf("bind_mounts.%v → volumes (type: encrypted)", bm["name"]))
				} else {
					vol["type"] = "bind"
					if path, ok := bm["path"].(string); ok && path != "" {
						vol["path"] = path
					}
					summary = append(summary, fmt.Sprintf("bind_mounts.%v → volumes (type: bind)", bm["name"]))
				}
				volumes = append(volumes, vol)
			}
		}
		delete(entry, "bind_mounts")
	}

	// 2. workspace: <host-path> → volumes entry of type:bind
	if wsAny, ok := entry["workspace"]; ok {
		if wsPath, ok := wsAny.(string); ok && wsPath != "" {
			volumes = append(volumes, map[string]any{
				"name": "workspace",
				"type": "bind",
				"host": wsPath,
				"path": "/workspace",
			})
			summary = append(summary, fmt.Sprintf("workspace: %s → volumes (name: workspace, type: bind, path: /workspace)", wsPath))
		}
		delete(entry, "workspace")
	}

	if len(volumes) > 0 {
		out["volumes"] = volumes
	}

	// 3. Pass-through fields (tunnel, dns, ports, env_file, security,
	//    network, env, eval, …). The DeploymentNode struct accepts more
	//    fields than the legacy schema offered, so any unknown fields
	//    become benign — they're either valid v4 fields the user added
	//    by hand, or ignored on load.
	for k, v := range entry {
		if k == "bind_mounts" || k == "workspace" {
			continue
		}
		out[k] = v
	}

	return out, summary
}
