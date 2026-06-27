package main

// migrate_agent_kind_rename.go — `charly migrate` step renaming the reusable
// agent-CLI catalog kind `kind: ai` → `kind: agent`. Three wire surfaces move:
//   - the top-level catalog map key `ai:` → `agent:` (UnifiedFile.Agent)
//   - the kind:score eligible-agent selector key `ai:` → `agent:` (HarnessScore.Agent)
//   - the standalone-doc discriminator value `kind: ai` → `kind: agent`
// The Go loader now reads `agent`/AgentConfig, so a config carrying the old
// `ai:` key silently loses the catalog/selector; this step rewrites them.
//
// `ai` is the catalog + kind:score selector key EXCLUSIVELY in the processed
// files (project charly.yml / eval.yml + the per-host overlay — candy var/env
// maps live in candy/<name>/charly.yml, which this step does not touch), so an
// every-depth key rename is unambiguous (mirrors the candy/box rename's
// every-depth key rewrite). (The independent iterate `validate_ai_artifacts`
// flag this step did NOT rename was later retired by the
// drop-validate-ai-artifacts step.)
//
// Comment-preserving (yaml.v3 node API); idempotent (a config already on
// `agent:` is a no-op); per-file .bak.<unix-ts>. TouchesHost false so the
// project-file rewrites run under remote-cache auto-migration; the per-host
// agent catalog (the AI-CLI overlay that never ships with the repo) is
// processed when HostDeployPath is set. See CHANGELOG/.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// MigrateAgentKindRename rewrites the `ai`→`agent` catalog/selector key and the
// `kind: ai`→`kind: agent` discriminator value in a project tree (charly.yml +
// legacy eval.yml) and, when hostDeployPath is set, the per-host overlay.
// Returns the list of changed files.
func MigrateAgentKindRename(dir, hostDeployPath string, dryRun bool) ([]string, error) {
	var changed []string
	for _, f := range []string{UnifiedFileName, "eval.yml"} {
		mod, err := rewriteAgentKindFile(filepath.Join(dir, f), dryRun)
		if err != nil {
			return changed, err
		}
		if mod {
			changed = append(changed, f)
		}
	}
	if hostDeployPath != "" {
		mod, err := rewriteAgentKindFile(hostDeployPath, dryRun)
		if err != nil {
			return changed, err
		}
		if mod {
			changed = append(changed, hostDeployPath)
		}
	}
	return changed, nil
}

func rewriteAgentKindFile(path string, dryRun bool) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, nil
	}
	// Multi-document stream (eval.yml bundles kind-keyed docs via --- separators).
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var docs []*yaml.Node
	changed := false
	for {
		var doc yaml.Node
		if err := dec.Decode(&doc); err != nil {
			break
		}
		if rewriteAgentKindNode(&doc) {
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

// rewriteAgentKindNode renames the catalog/selector key `ai`→`agent` and the
// standalone-doc `kind: ai` value→`agent` at every depth. Returns whether it
// changed anything (idempotent: a config already on `agent:` is a no-op).
func rewriteAgentKindNode(n *yaml.Node) bool {
	changed := false
	switch n.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, c := range n.Content {
			if rewriteAgentKindNode(c) {
				changed = true
			}
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := n.Content[i]
			val := n.Content[i+1]
			if key.Value == "ai" {
				key.Value = "agent"
				changed = true
			}
			if key.Value == "kind" && val.Kind == yaml.ScalarNode && val.Value == "ai" {
				val.Value = "agent"
				changed = true
			}
			if rewriteAgentKindNode(val) {
				changed = true
			}
		}
	}
	return changed
}
