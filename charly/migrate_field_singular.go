package main

// migrate_field_singular.go — `charly migrate`.
//
// Hard cutover that singularizes every plural YAML field name in the
// project schema. Rewrites every reachable .yml under the project root
// (overthink.yml siblings + includes:/include: + discover: + ov/testdata
// when self-running). Idempotent: a second run reports zero changes.
//
// Modeled after rewriteServiceKeys in migrate_unified.go:397 — line-level
// rewriter preserving comments and indentation. Longest-match-first key
// ordering ensures `requires_capabilities:` is rewritten before the
// shorter `requires:` rule fires on the same file.
//
// The `pluralToSingularYAMLKeys` map is the single source of truth for
// (a) the migrator's substitution table, AND (b) the loader-rejection
// helper `RejectLegacyPluralKeys` used by parseLayerYAML / LoadUnified
// / LoadDeployConfig (R3 no-duplication).

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// pluralToSingularYAMLKeys is the canonical plural → singular mapping
// applied by the migrator and rejected by the loaders. Every entry is a
// top-level YAML mapping key; nested keys with the same spelling are
// rewritten too because the algorithm is purely lexical.
//
// Three categories: list-plurals (sequence values), map/namespace plurals
// (mapping values), and compound plurals. See the field-singular plan
// §A.2 for the canonical table; the literal here is its codified form.
var pluralToSingularYAMLKeys = map[string]string{
	// §A.2.a — list-plurals
	"includes": "include",
	"layers":   "layer",
	"ports":    "port",
	"volumes":  "volume",
	"secrets":  "secret",
	"aliases":  "alias",
	// builds: → produce: is a SEMANTIC rename, not a pluralization
	// removal. The naive singular `build:` would collide with the
	// existing `build:` yaml tag in BoxConfig (BuildFormats). The
	// downstream consumer assigns img.Produce to BuilderCapabilities, so
	// `produce:` is the semantic fit.
	"builds":    "produce",
	"requires":  "require",
	"tasks":     "task",
	"artifacts": "artifact",
	"packages":  "package",
	"sidecars":  "sidecar",
	// 2026-06 singular-label cutover: the layer parser now hard-rejects
	// these as unknown keys (the OCI label contract + the LayerYAML fields
	// went singular). hooks: / capabilities: are layer-level fields; tags:
	// is the eval-scenario field. requires_capabilities (below) already
	// singularizes longest-first, so `capabilities` is safe to add here.
	"hooks":        "hook",
	"capabilities": "capability",
	"tags":         "tag",

	// §A.2.b — map/namespace plurals
	"images":      "image",
	"distros":     "distro",
	"builders":    "builder",
	"inits":       "init",
	"deployments": "deploy",
	"deploys":     "deploy",
	"clusters":    "cluster",
	// "groups": "group" — CARVE-OUT: the eval Check struct already has a
	// verb-level `group:` scalar (the group-membership check), so renaming a
	// Check's `groups:` list to `group:` would collide. cloud-init's `groups:`
	// is also kept plural for the same global-key reason. (Same class of
	// semantic carve-out as addr/addrs below.)
	"targets": "target",
	"modules": "module",

	// §A.2.c — compound plurals
	"env_requires":          "env_require",
	"env_accepts":           "env_accept",
	"secret_requires":       "secret_require",
	"secret_accepts":        "secret_accept",
	"mcp_provides":          "mcp_provide",
	"mcp_requires":          "mcp_require",
	"mcp_accepts":           "mcp_accept",
	"requires_capabilities": "requires_capability",
	"add_layers":            "add_layer",
	"exit_codes":            "exit_code",
	"system_services":       "system_service",
	"cap_adds":              "cap_add",
	"with_services":         "with_service",

	// §A.2.d — domain plurals (overthink-native authoring keys)
	"events":    "event",
	"replicas":  "replica",
	"ssh_args":  "ssh_arg",
	"mounts":    "mount",
	"snapshots": "snapshot",
	"repos":     "repo",
	"subgroups": "subgroup",
	// "addrs": "addr" — REVERTED: collides with existing addr: scalar field in evalspec.go (semantic carve-out)
	"phases":             "phase",
	"steps":              "step",
	"metrics":            "metric",
	"notes":              "note",
	"examples":           "example",
	"replaces":           "replace",
	"recipes":            "recipe",
	"scenarios":          "scenario",
	"versions":           "version",
	"formats":            "format",
	"start_retries":      "start_retry",
	"start_secs":         "start_sec",
	"wait_seconds":       "wait_second",
	"solved_ids":         "solved_id",
	"over_ids":           "over_id",
	"newly_solved_ids":   "newly_solved_id",
	"oci_labels":         "oci_label",
	"extra_repos":        "extra_repo",
	"pod_defaults":       "pod_default",
	"env_defaults":       "env_default",
	"path_contributions": "path_contribution",

	// §A.2.e — field-singular completion batch (2026-05). Only overthink-NATIVE
	// authoring keys are singularized here. EXTERNAL-SCHEMA keys are deliberately
	// ABSENT and kept PLURAL so the authoring key matches the external output:
	// overthink renders cloud-init / Kubernetes / libvirt config with the
	// external schema's own plural keys (`users:`, `labels:`, `resources:`,
	// `<devices>`, `<topology sockets= cores= threads=>`, …), so authoring those
	// fields in the SAME plural spelling keeps a 1:1 mapping. The kept-plural set
	// (defined by the keys living in cloud_init_types.go / k8s_spec.go /
	// k8s_config.go / libvirt_yaml.go) includes: users, groups, mirrors,
	// write_files, ethernets, hostnames, labels, annotations, tolerations,
	// pull_secrets, resources, limits, requests, devices, channels, cores,
	// sockets, threads, dies, disks, cpus, filesystems, interfaces, inputs,
	// hostdevs, memnodes, snippets, graphics, hugepages, iothreads, timers,
	// frequencies, retries, oem_strings, port_forwards, spinlocks, features,
	// heads, shares, patches, kernel_hashes. Other carve-outs: groups/addrs
	// (verb collisions, above); config.yml-only keys in runtime_config.go (not
	// field-singular-walked).
	"platforms":           "platform",
	"instances":           "instance",
	"options":             "option",
	"opts":                "opt",
	"headers":             "header",
	"args":                "arg",
	"workarounds":         "workaround",
	"vars":                "var",
	"install_commands":    "install_command",
	"management_commands": "management_command",
	"detect_files":        "detect_file",
	"layer_files":         "layer_file",
	"layer_fields":        "layer_field",
	"section_fields":      "section_field",
	"base_packages":       "base_package",
	"include_packages":    "include_package",
	"copy_artifacts":      "copy_artifact",
	"exclude_distros":     "exclude_distro",
	"env_provides":        "env_provide",
}

// orderedPluralKeys returns the keys of pluralToSingularYAMLKeys sorted
// by length descending. Longest-match-first is required so that
// `requires_capabilities:` matches its full key before the shorter
// `requires:` rule would otherwise fire (and corrupt the longer key
// into `require_capabilities:`).
func orderedPluralKeys() []string {
	keys := make([]string, 0, len(pluralToSingularYAMLKeys))
	for k := range pluralToSingularYAMLKeys {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(keys[i]) != len(keys[j]) {
			return len(keys[i]) > len(keys[j])
		}
		return keys[i] < keys[j]
	})
	return keys
}

// MigrateFieldSingularOpts configures MigrateFieldSingular.
type MigrateFieldSingularOpts struct {
	Dir    string
	DryRun bool
	// BackupSuffix overrides the default `.bak.<unix-ts>` suffix; used by
	// tests to make output deterministic. Empty means generate from
	// time.Now().
	BackupSuffix string
}

// FileRewrite is one file's outcome.
type FileRewrite struct {
	Path string   // absolute path to the rewritten file
	Keys []string // singularized keys observed in the file (one entry per distinct key, sorted)
}

// MigrateFieldSingularResult reports the outcome of a migrator invocation.
type MigrateFieldSingularResult struct {
	Rewritten    []FileRewrite
	BackupSuffix string
}

// MigrateFieldSingular walks the project rooted at opts.Dir and applies
// the plural→singular rename to every reachable .yml. Idempotent: a
// second invocation on the same tree returns Rewritten==nil.
func MigrateFieldSingular(opts MigrateFieldSingularOpts) (MigrateFieldSingularResult, error) {
	var res MigrateFieldSingularResult

	files, err := discoverProjectYAMLs(opts.Dir)
	if err != nil {
		return res, err
	}

	suffix := opts.BackupSuffix
	if suffix == "" {
		suffix = fmt.Sprintf(".bak.%d", time.Now().Unix())
	}
	res.BackupSuffix = suffix

	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			// Skip unreadable files silently (e.g. dangling symlinks);
			// don't abort the whole migration on one bad sibling.
			continue
		}
		newData, touched := rewriteFieldSingular(data)
		if len(touched) == 0 {
			continue
		}
		// Idempotency invariant — after rewrite, no plural keys should
		// remain. If any do, surface the bug rather than silently shipping
		// a half-migrated file.
		_, residual := rewriteFieldSingular(newData)
		if len(residual) != 0 {
			return res, fmt.Errorf("%s: post-rewrite residual plural keys %v — this is a migrator bug", path, residual)
		}
		if !opts.DryRun {
			backupPath := path + suffix
			if _, err := os.Stat(backupPath); err == nil {
				return res, fmt.Errorf("%s: backup file already exists; refusing to overwrite (delete it or wait one second and re-run)", backupPath)
			}
			if err := os.WriteFile(backupPath, data, 0644); err != nil {
				return res, fmt.Errorf("writing backup %s: %w", backupPath, err)
			}
			if err := os.WriteFile(path, newData, 0644); err != nil {
				return res, fmt.Errorf("writing %s: %w", path, err)
			}
		}
		sort.Strings(touched)
		res.Rewritten = append(res.Rewritten, FileRewrite{Path: path, Keys: touched})
	}

	return res, nil
}

// rewriteFieldSingular performs the line-level rewrite on raw YAML
// bytes. Returns the rewritten bytes and the sorted unique list of
// plural keys that were touched.
//
// Algorithm (mirrors migrate_unified.go:rewriteServiceKeys):
//
//   - Split into lines.
//   - For each line, strip leading whitespace to find the indent.
//   - For each plural key in longest-first order, check whether the
//     trimmed line is `<plural>:` followed by `:`-end-of-line OR by a
//     space (block-mapping shapes) OR by inline-mapping content (`{...}`,
//     value, comment). If matched, rewrite the key in-place and record.
//   - Reassemble; return.
//
// The algorithm is purely lexical — quoted string values containing the
// word `layers:` mid-content are NOT rewritten because the trim+prefix
// check requires the key to start at column-after-indent.
func rewriteFieldSingular(data []byte) ([]byte, []string) {
	lines := strings.Split(string(data), "\n")
	keys := orderedPluralKeys()
	touched := map[string]struct{}{}

	for i, ln := range lines {
		trimmed := strings.TrimLeft(ln, " \t")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := ln[:len(ln)-len(trimmed)]
		for _, plural := range keys {
			prefix := plural + ":"
			if !strings.HasPrefix(trimmed, prefix) {
				continue
			}
			// Boundary check: the character after the colon must be the
			// end of the line, a space, or a tab. This prevents
			// `layers_unmodified:` (hypothetical) from matching the
			// `layers:` rule.
			rest := trimmed[len(prefix):]
			if rest != "" && rest[0] != ' ' && rest[0] != '\t' && rest[0] != '\r' {
				continue
			}
			singular := pluralToSingularYAMLKeys[plural]
			lines[i] = indent + singular + ":" + rest
			touched[plural] = struct{}{}
			break // longest-first matched; don't fire shorter rules on the same line
		}
	}

	out := make([]string, 0, len(touched))
	for k := range touched {
		out = append(out, k)
	}
	sort.Strings(out)
	return []byte(strings.Join(lines, "\n")), out
}

// discoverProjectYAMLs returns the absolute paths of every .yml file
// reachable from a project rooted at dir. Reads overthink.yml raw (NOT
// via LoadUnified) so the walker can run on a tree that still has
// legacy plural keys.
//
// Sources:
//   - overthink.yml itself
//   - top-level includes: / include: entries
//   - discover: scan paths (recursive *.yml under each)
//   - project-root .yml siblings (image.yml, deploy.yml, pod.yml,
//     k8s.yml, vm.yml, eval.yml, local.yml) — fallback regardless of
//     whether they appear in includes:
//   - ov/testdata/**/*.yml when the cwd looks like the overthink repo
//     itself (self-migration mode)
//
// Returns sorted, deduplicated absolute paths.
func discoverProjectYAMLs(dir string) ([]string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	add := func(p string) {
		if !filepath.IsAbs(p) {
			p = filepath.Join(abs, p)
		}
		clean := filepath.Clean(p)
		seen[clean] = struct{}{}
	}

	// Project-root .yml siblings — always include if present.
	for _, name := range []string{
		"overthink.yml", "image.yml", "deploy.yml", "pod.yml",
		"k8s.yml", "vm.yml", "eval.yml", "local.yml",
	} {
		p := filepath.Join(abs, name)
		if _, err := os.Stat(p); err == nil {
			add(p)
		}
	}

	// Standard project layout: walk layers/<name>/layer.yml. Every
	// OpenCharly project has these by convention; we don't require an
	// explicit discover: block.
	if td := filepath.Join(abs, "layers"); statIsDir(td) {
		if err := walkYAMLs(td, seen); err != nil {
			return nil, err
		}
	}

	// Read overthink.yml raw to extract includes:/include:/discover:
	rootPath := filepath.Join(abs, "overthink.yml")
	if data, err := os.ReadFile(rootPath); err == nil {
		var doc yaml.Node
		if err := yaml.Unmarshal(data, &doc); err == nil && len(doc.Content) > 0 && doc.Content[0].Kind == yaml.MappingNode {
			rootMap := doc.Content[0]
			// import: (canonical) / legacy includes: / include:
			for _, key := range []string{"import", "includes", "include"} {
				if seq := lookupMapNode(rootMap, key); seq != nil && seq.Kind == yaml.SequenceNode {
					for _, item := range seq.Content {
						if item.Kind == yaml.ScalarNode && item.Value != "" {
							add(item.Value)
						}
					}
				}
			}
			// discover: recursive scan
			if discNode := lookupMapNode(rootMap, "discover"); discNode != nil && discNode.Kind == yaml.MappingNode {
				for i := 0; i+1 < len(discNode.Content); i += 2 {
					valNode := discNode.Content[i+1]
					switch valNode.Kind {
					case yaml.SequenceNode:
						for _, item := range valNode.Content {
							var p string
							switch item.Kind {
							case yaml.ScalarNode:
								p = item.Value
							case yaml.MappingNode:
								if pn := lookupMapNode(item, "path"); pn != nil {
									p = pn.Value
								}
							}
							if p == "" {
								continue
							}
							if err := walkYAMLs(filepath.Join(abs, p), seen); err != nil {
								return nil, err
							}
						}
					case yaml.ScalarNode:
						if valNode.Value != "" {
							if err := walkYAMLs(filepath.Join(abs, valNode.Value), seen); err != nil {
								return nil, err
							}
						}
					}
				}
			}
		}
	}

	// Self-migration mode: when running from the overthink repo root
	// (detected via the ov/go.mod sentinel), also include
	// ov/testdata/**/*.yml. These fixtures are loaded by Go tests and
	// must stay in lockstep with the schema.
	if _, err := os.Stat(filepath.Join(abs, "ov", "go.mod")); err == nil {
		td := filepath.Join(abs, "ov", "testdata")
		if statIsDir(td) {
			if err := walkYAMLs(td, seen); err != nil {
				return nil, err
			}
		}
	}

	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

func statIsDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// walkYAMLs recursively adds every *.yml under root to the seen set.
// Non-existent roots are silently skipped.
func walkYAMLs(root string, seen map[string]struct{}) error {
	info, err := os.Stat(root)
	if err != nil {
		return nil
	}
	if !info.IsDir() {
		if strings.HasSuffix(root, ".yml") || strings.HasSuffix(root, ".yaml") {
			seen[filepath.Clean(root)] = struct{}{}
		}
		return nil
	}
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".yml") && !strings.HasSuffix(path, ".yaml") {
			return nil
		}
		seen[filepath.Clean(path)] = struct{}{}
		return nil
	})
}

// lookupMapNode returns the value node for the given key in a YAML
// mapping node, or nil if absent. Identical to the helper used in
// migrate_kind_files.go but local-scoped to avoid coupling.
func lookupMapNode(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// RejectLegacyPluralKeys is the single rejection point used by every
// YAML loader. Walks the top-level mapping of the document and returns
// an error if any legacy plural field name is present, with a remediation
// hint pointing at `charly migrate`. (R3 no-duplication: this
// helper and pluralToSingularYAMLKeys live together so the loader
// rejection and the migrator rewrite share ONE source of truth.)
//
// Returns nil for documents that are already singular OR that can't be
// parsed as a top-level mapping (caller will surface the parse error
// itself with full context).
func RejectLegacyPluralKeys(path string, data []byte) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil // let the caller's own decode produce the parse error
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	root := doc.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		k := root.Content[i].Value
		if singular, ok := pluralToSingularYAMLKeys[k]; ok {
			return fmt.Errorf("%s: legacy plural field %q rejected; the project moved to singular field names. Run `charly migrate` to rewrite this file (key %q → %q)", path, k, k, singular)
		}
	}
	return nil
}
