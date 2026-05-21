package main

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// MigrateDropKdbxCmd is `ov migrate drop-kdbx`. It removes every residual
// artefact of the dropped direct KeePass .kdbx credential backend from the
// runtime config (~/.config/ov/config.yml):
//
//   - secret_backend: kdbx          → key deleted (reverts to "auto")
//   - secrets_kdbx_path             → deleted
//   - secrets_kdbx_key_file         → deleted
//   - kdbx_cache                    → deleted
//   - kdbx_cache_timeout            → deleted
//
// It reads/writes the file via the yaml.Node API directly (preserving key
// order and comments) and never calls LoadRuntimeConfig, so it is immune to
// the hard load-time guard (validateNoKdbxResiduals) that those same residuals
// trip on every other command. Idempotent: re-running on a clean file is a
// no-op. A <path>.bak.<unix-ts> rollback is written before any rewrite.
//
// Existing secrets stored in a .kdbx file remain available without data
// migration by exposing the database through KeePassXC's FdoSecrets plugin —
// that surfaces them on the Secret Service bus, which ov reads via the
// unaffected keyring backend.
type MigrateDropKdbxCmd struct {
	DryRun bool   `long:"dry-run" help:"Print what would change without modifying the file"`
	Path   string `long:"path" help:"Override the config file path (default: ~/.config/ov/config.yml)"`
}

// kdbxResidualKeys are the top-level config.yml keys removed unconditionally.
var kdbxResidualKeys = map[string]bool{
	"secrets_kdbx_path":     true,
	"secrets_kdbx_key_file": true,
	"kdbx_cache":            true,
	"kdbx_cache_timeout":    true,
}

func (c *MigrateDropKdbxCmd) Run() error {
	path := c.Path
	if path == "" {
		p, err := RuntimeConfigPath()
		if err != nil {
			return err
		}
		path = p
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("nothing to migrate (no config file at " + path + ")")
			return nil
		}
		return fmt.Errorf("reading %s: %w", path, err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}

	root := rootMappingNode(&doc)
	if root == nil {
		fmt.Println("nothing to migrate (already on the post-kdbx schema)")
		return nil
	}

	removed := dropKdbxKeys(root)
	if len(removed) == 0 {
		fmt.Println("nothing to migrate (already on the post-kdbx schema)")
		return nil
	}

	if c.DryRun {
		for _, k := range removed {
			fmt.Printf("[dry-run] would remove %s from %s\n", k, path)
		}
		return nil
	}

	backup := fmt.Sprintf("%s.bak.%d", path, time.Now().Unix())
	if err := os.WriteFile(backup, data, 0600); err != nil {
		return fmt.Errorf("writing backup %s: %w", backup, err)
	}

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return fmt.Errorf("marshaling %s: %w", path, err)
	}
	if err := os.WriteFile(path, out, 0600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	for _, k := range removed {
		fmt.Printf("removed %s from %s\n", k, path)
	}
	fmt.Printf("backup written to %s\n", backup)
	fmt.Println("The direct KeePass .kdbx backend is gone. To keep using an existing .kdbx,")
	fmt.Println("open it in KeePassXC and enable the FdoSecrets integration (Settings → Secret")
	fmt.Println("Service Integration) so its entries are served over the Secret Service bus.")
	return nil
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
