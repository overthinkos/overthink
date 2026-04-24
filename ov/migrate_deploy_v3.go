package main

// migrate_deploy_v3.go — `ov migrate deploy-schema-v3`.
//
// Converts a legacy (schema v1 / v2) deploy.yml or overthink.yml to
// schema v3:
//
//   - Rename deployment keys matching `vm:<name>` → `<name>-vm`.
//   - Set explicit `target:` on every deployment entry that lacks one
//     (inferred from old form: `vm:` prefix → `vm`, bare `host` → `host`,
//     everything else → `pod` / legacy `container`).
//   - Rename legacy target values: `container` → `pod`,
//     `kubernetes` → `k8s`.
//   - Rename `vm_source:` → `vm:` (cleaner schema v3 cross-ref name).
//   - Move `disposable:`/`lifecycle:` from any top-level `vm.<name>`
//     entry onto the matching `deployments.<name>-vm` entry. Leave
//     the value on the vm entity as-is for backward compat; the merge
//     in MergeDeployConfigs now reads the deploy-level value correctly.
//   - Bump `version: 2` → `3`.
//
// Idempotency: a second invocation on an already-v3 file is a no-op
// (nothing in the rewrite set matches). A `--dry-run` mode lists the
// transformations that WOULD be applied without touching the file.
//
// Preserves YAML comments by operating on yaml.Node trees (same
// pattern as migrate_merge_vms.go).

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// MigrateDeploySchemaV3Cmd is `ov migrate deploy-schema-v3`.
type MigrateDeploySchemaV3Cmd struct {
	Path   string `arg:"" optional:"" help:"Path to deploy.yml or overthink.yml (default: <cwd>/deploy.yml, falling back to overthink.yml)"`
	DryRun bool   `long:"dry-run" help:"Print transformations that would be applied, don't touch the file"`
}

// Run executes the migration against the given file.
func (c *MigrateDeploySchemaV3Cmd) Run() error {
	path, err := c.resolvePath()
	if err != nil {
		return err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}

	result := MigrateDeploySchemaV3(&doc)
	if result.NoChanges {
		fmt.Fprintf(os.Stderr, "%s: already at schema v3 (no changes)\n", path)
		return nil
	}

	// Print the per-transformation log.
	for _, t := range result.Transforms {
		fmt.Fprintln(os.Stderr, t)
	}

	if c.DryRun {
		fmt.Fprintf(os.Stderr, "[dry-run] would rewrite %s\n", path)
		return nil
	}

	// Marshal back preserving comments.
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(4)
	if err := enc.Encode(&doc); err != nil {
		return fmt.Errorf("encoding migrated document: %w", err)
	}
	enc.Close()

	if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	fmt.Fprintf(os.Stderr, "wrote %s (schema v3)\n", path)
	return nil
}

// resolvePath picks deploy.yml or overthink.yml from cwd when no path
// is given.
func (c *MigrateDeploySchemaV3Cmd) resolvePath() (string, error) {
	if c.Path != "" {
		return c.Path, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for _, candidate := range []string{"deploy.yml", "overthink.yml"} {
		p := filepath.Join(cwd, candidate)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("no deploy.yml or overthink.yml in %s; pass a path explicitly", cwd)
}

// MigrateResult summarizes what the migration did. Mutations are the
// structural changes that require writing the file; Notes are
// informational preserved-as-is observations that don't flip the
// no-changes flag.
type MigrateResult struct {
	NoChanges  bool
	Transforms []string // visible log lines — both mutations and notes
	Mutations  int      // strict mutation count; zero → idempotent no-op
}

// MigrateDeploySchemaV3 applies the schema-v3 transformations to doc
// in place. Pure function for testability — no I/O. Callers decide
// whether to write the result back.
func MigrateDeploySchemaV3(doc *yaml.Node) MigrateResult {
	var r MigrateResult
	if doc == nil || doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		r.NoChanges = true
		return r
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		r.NoChanges = true
		return r
	}

	// --- version: bump v<2 → 3 -----------------------------------------
	versionNode := findMapValue(root, "version")
	if versionNode != nil && versionNode.Kind == yaml.ScalarNode {
		if versionNode.Value != "3" {
			r.Transforms = append(r.Transforms, fmt.Sprintf("version: %s → 3", versionNode.Value))
			r.Mutations++
			versionNode.Value = "3"
			versionNode.Tag = "!!int"
		}
	} else {
		// No version key; add one.
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "version", Tag: "!!str"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: "3", Tag: "!!int"},
		)
		r.Transforms = append(r.Transforms, "added version: 3")
		r.Mutations++
	}

	// --- deployments.images: rewrite keys + normalize target: ---------
	deploymentsNode := findMapValue(root, "deployments")
	if deploymentsNode != nil && deploymentsNode.Kind == yaml.MappingNode {
		imagesNode := findMapValue(deploymentsNode, "images")
		if imagesNode != nil && imagesNode.Kind == yaml.MappingNode {
			rewriteDeploymentImages(imagesNode, &r)
		}
	}

	// --- vm.<name>.disposable / lifecycle migration -------------------
	// Leave the values on the vm entity (MergeDeployConfigs reads deploy
	// level), but emit a transform log line if we detected one so the
	// operator can confirm the flag is still authoritative.
	vmSection := findMapValue(root, "vm")
	if vmSection != nil && vmSection.Kind == yaml.MappingNode {
		for i := 0; i < len(vmSection.Content); i += 2 {
			name := vmSection.Content[i].Value
			entry := vmSection.Content[i+1]
			if entry.Kind != yaml.MappingNode {
				continue
			}
			if d := findMapValue(entry, "disposable"); d != nil && d.Value == "true" {
				r.Transforms = append(r.Transforms, fmt.Sprintf("vm.%s.disposable=true preserved (also live on matching deployment entry)", name))
			}
		}
	}

	if r.Mutations == 0 {
		r.NoChanges = true
	}
	return r
}

// rewriteDeploymentImages applies per-entry transforms:
//   - vm:X → X-vm
//   - target: container → pod; kubernetes → k8s
//   - vm_source: → vm:
//   - ensure explicit target: on every entry
func rewriteDeploymentImages(imagesNode *yaml.Node, r *MigrateResult) {
	// Rename keys first: `vm:X` → `X-vm`. Walk once, collect renames,
	// then apply to avoid mutating during iteration.
	type rename struct{ from, to string }
	var renames []rename
	for i := 0; i < len(imagesNode.Content); i += 2 {
		keyNode := imagesNode.Content[i]
		if strings.HasPrefix(keyNode.Value, "vm:") {
			newKey := strings.TrimPrefix(keyNode.Value, "vm:") + "-vm"
			renames = append(renames, rename{from: keyNode.Value, to: newKey})
		}
	}
	for _, rn := range renames {
		for i := 0; i < len(imagesNode.Content); i += 2 {
			if imagesNode.Content[i].Value == rn.from {
				imagesNode.Content[i].Value = rn.to
				r.Transforms = append(r.Transforms, fmt.Sprintf("deployments.images: %s → %s", rn.from, rn.to))
		r.Mutations++
				break
			}
		}
	}

	// Per-entry rewrites.
	for i := 0; i < len(imagesNode.Content); i += 2 {
		keyNode := imagesNode.Content[i]
		entryNode := imagesNode.Content[i+1]
		if entryNode.Kind != yaml.MappingNode {
			continue
		}
		deployName := keyNode.Value

		// target: value normalization. Also infer an explicit target
		// when missing.
		targetNode := findMapValue(entryNode, "target")
		switch {
		case targetNode == nil:
			inferred := inferTargetFromName(deployName)
			// Add target: <inferred> after the first child (convention:
			// near the top of the entry).
			keyN := &yaml.Node{Kind: yaml.ScalarNode, Value: "target", Tag: "!!str"}
			valN := &yaml.Node{Kind: yaml.ScalarNode, Value: inferred, Tag: "!!str"}
			entryNode.Content = append([]*yaml.Node{keyN, valN}, entryNode.Content...)
			r.Transforms = append(r.Transforms, fmt.Sprintf("deployments.%s: added target: %s", deployName, inferred))
			r.Mutations++
		case targetNode.Value == "container":
			targetNode.Value = "pod"
			r.Transforms = append(r.Transforms, fmt.Sprintf("deployments.%s: target: container → pod", deployName))
			r.Mutations++
		case targetNode.Value == "kubernetes":
			targetNode.Value = "k8s"
			r.Transforms = append(r.Transforms, fmt.Sprintf("deployments.%s: target: kubernetes → k8s", deployName))
			r.Mutations++
		}

		// vm_source: → vm: (cross-ref rename).
		if vmSourceNode := findMapKey(entryNode, "vm_source"); vmSourceNode != nil {
			vmSourceNode.Value = "vm"
			r.Transforms = append(r.Transforms, fmt.Sprintf("deployments.%s: vm_source: → vm:", deployName))
			r.Mutations++
		}
	}
}

// inferTargetFromName picks a target: value from a deployment name
// that predates the explicit field. Legacy conventions:
//   - literal "host" → host
//   - "vm:..." or "*-vm" → vm
//   - "*-k8s" or name contains "kubernetes" → k8s
//   - anything else → pod
func inferTargetFromName(name string) string {
	switch {
	case name == "host":
		return "host"
	case strings.HasPrefix(name, "vm:") || strings.HasSuffix(name, "-vm"):
		return "vm"
	case strings.HasSuffix(name, "-k8s") || strings.Contains(name, "kubernetes"):
		return "k8s"
	default:
		return "pod"
	}
}

// findMapValue returns the value node for `key` in a YAML mapping, or
// nil when absent. `node` must be a MappingNode.
func findMapValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

// findMapKey returns the KEY node (not value) for `key` — used when
// the migration wants to rename the key in place.
func findMapKey(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i]
		}
	}
	return nil
}
