package migrate

// migrate_drop_validate_ai_artifacts.go — `charly migrate` step stripping the
// retired `validate_ai_artifacts` key from every `iterate:` block.
//
// The `validate_ai_artifacts` AI-iteration flag was removed when the in-proc
// live-verb runtime it depended on was deleted: its only reader was the
// compiled-in live-verb subprocess dispatcher, unreachable once every
// live-container verb externalized, so the flag was dead — set but never acted
// on. Artifact validation is now always-on in the out-of-process verb plugins
// (sdk.RunArtifactValidators). This step removes the now-meaningless key.
//
// It is an intra-HEAD CLEANUP, not a format cutover: the loader TOLERATES a
// residual `validate_ai_artifacts` key (the iterate node is not closed-validated
// by #NodeDoc), so a config carrying it still loads — the key is simply ignored.
// This step removes it for cleanliness; `charly migrate` running the whole chain
// (incl. remote-cache auto-migration) absorbs it. The CalVer therefore slots
// BELOW HEAD and does NOT raise LatestSchemaVersion().
//
// `validate_ai_artifacts` is exclusively the iterate-block key in the processed
// files, so an every-depth key strip is unambiguous. Comment-preserving
// (yaml.v3 node API); idempotent (a config already without it is a no-op);
// per-file .bak.<unix-ts>. TouchesHost false so the project-file rewrite runs
// under remote-cache auto-migration; the per-host overlay is processed when
// HostDeployPath is set. See CHANGELOG/.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// MigrateDropValidateAiArtifacts strips the `validate_ai_artifacts` key from a
// project tree (charly.yml) and, when hostDeployPath is set, the per-host
// overlay. Returns the list of changed files.
func MigrateDropValidateAiArtifacts(dir, hostDeployPath string, dryRun bool) ([]string, error) {
	var changed []string
	mod, err := dropValidateAiArtifactsFile(filepath.Join(dir, UnifiedFileName), dryRun)
	if err != nil {
		return changed, err
	}
	if mod {
		changed = append(changed, UnifiedFileName)
	}
	if hostDeployPath != "" {
		mod, err := dropValidateAiArtifactsFile(hostDeployPath, dryRun)
		if err != nil {
			return changed, err
		}
		if mod {
			changed = append(changed, hostDeployPath)
		}
	}
	return changed, nil
}

func dropValidateAiArtifactsFile(path string, dryRun bool) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, nil
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var docs []*yaml.Node
	changed := false
	for {
		var doc yaml.Node
		if err := dec.Decode(&doc); err != nil {
			break
		}
		if dropValidateAiArtifactsNode(&doc) {
			changed = true
		}
		d := doc
		docs = append(docs, &d)
	}
	if !changed {
		return false, nil
	}
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(4)
	for _, d := range docs {
		if err := enc.Encode(d); err != nil {
			return false, err
		}
	}
	_ = enc.Close()
	if dryRun {
		return true, nil
	}
	bak := fmt.Sprintf("%s.bak.%d", path, time.Now().Unix())
	_ = os.WriteFile(bak, data, 0644)
	if err := os.WriteFile(path, out.Bytes(), 0644); err != nil {
		return false, err
	}
	return true, nil
}

// dropValidateAiArtifactsNode removes the `validate_ai_artifacts` key (and its
// value) from every mapping node at any depth. Returns whether it changed
// anything (idempotent: a config already without it is a no-op).
func dropValidateAiArtifactsNode(n *yaml.Node) bool {
	changed := false
	switch n.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, c := range n.Content {
			if dropValidateAiArtifactsNode(c) {
				changed = true
			}
		}
	case yaml.MappingNode:
		filtered := n.Content[:0:0]
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := n.Content[i]
			val := n.Content[i+1]
			if key.Value == "validate_ai_artifacts" {
				changed = true
				continue // drop both key and value
			}
			if dropValidateAiArtifactsNode(val) {
				changed = true
			}
			filtered = append(filtered, key, val)
		}
		if changed {
			n.Content = filtered
		}
	}
	return changed
}
