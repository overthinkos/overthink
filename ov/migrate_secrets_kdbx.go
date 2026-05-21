package main

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// kdbxResidualKeys are the top-level config.yml keys removed unconditionally.
var kdbxResidualKeys = map[string]bool{
	"secrets_kdbx_path":     true,
	"secrets_kdbx_key_file": true,
	"kdbx_cache":            true,
	"kdbx_cache_timeout":    true,
}

// MigrateDropKdbx strips residual KeePass .kdbx backend keys from the runtime
// config at path (default ~/.config/ov/config.yml when empty). It is the
// chain-callable form used by the unified `ov migrate` runner. Returns the
// list of removed keys (or, under dryRun, the keys that would be removed). A
// missing file or already-clean file yields an empty list, no error. Writes a
// <path>.bak.<unix-ts> rollback before any rewrite.
func MigrateDropKdbx(path string, dryRun bool) ([]string, error) {
	if path == "" {
		p, err := RuntimeConfigPath()
		if err != nil {
			return nil, err
		}
		path = p
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	root := rootMappingNode(&doc)
	if root == nil {
		return nil, nil
	}
	removed := dropKdbxKeys(root)
	if len(removed) == 0 || dryRun {
		return removed, nil
	}
	backup := fmt.Sprintf("%s.bak.%d", path, time.Now().Unix())
	if err := os.WriteFile(backup, data, 0600); err != nil {
		return removed, fmt.Errorf("writing backup %s: %w", backup, err)
	}
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return removed, fmt.Errorf("marshaling %s: %w", path, err)
	}
	if err := os.WriteFile(path, out, 0600); err != nil {
		return removed, fmt.Errorf("writing %s: %w", path, err)
	}
	return removed, nil
}

// rootMappingNode returns the top-level mapping node of a parsed YAML document,
// or nil when the document is empty / not a mapping.
func rootMappingNode(doc *yaml.Node) *yaml.Node {
	if doc.Kind == yaml.DocumentNode {
		if len(doc.Content) == 0 {
			return nil
		}
		doc = doc.Content[0]
	}
	if doc.Kind != yaml.MappingNode {
		return nil
	}
	return doc
}

// dropKdbxKeys removes the kdbx residual keys from a mapping node in place and
// returns the names of the keys it removed. A `secret_backend` key is removed
// only when its value is exactly "kdbx" (any other value is a live setting).
func dropKdbxKeys(m *yaml.Node) []string {
	var removed []string
	kept := make([]*yaml.Node, 0, len(m.Content))
	for i := 0; i+1 < len(m.Content); i += 2 {
		key := m.Content[i]
		val := m.Content[i+1]
		switch {
		case kdbxResidualKeys[key.Value]:
			removed = append(removed, key.Value)
		case key.Value == "secret_backend" && val.Value == "kdbx":
			removed = append(removed, "secret_backend: kdbx")
		default:
			kept = append(kept, key, val)
		}
	}
	m.Content = kept
	return removed
}
