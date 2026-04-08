package main

import (
	"encoding/json"
	"strings"
)

// EnvProvidesEntry is a resolved env_provides entry in deploy.yml.
type EnvProvidesEntry struct {
	Name   string `yaml:"name" json:"name"`
	Value  string `yaml:"value" json:"value"`
	Source string `yaml:"source" json:"source"`
}

// MCPProvidesEntry is a resolved mcp_provides entry in deploy.yml.
type MCPProvidesEntry struct {
	Name      string `yaml:"name" json:"name"`
	URL       string `yaml:"url" json:"url"`
	Transport string `yaml:"transport,omitempty" json:"transport,omitempty"`
	Source    string `yaml:"source" json:"source"`
}

// ProvidesConfig holds all resolved provides entries in deploy.yml.
type ProvidesConfig struct {
	Env []EnvProvidesEntry `yaml:"env,omitempty" json:"env,omitempty"`
	MCP []MCPProvidesEntry `yaml:"mcp,omitempty" json:"mcp,omitempty"`
}

// Named is the interface for provides entries (shared pipeline logic).
type Named interface {
	GetName() string
	GetSource() string
}

func (e EnvProvidesEntry) GetName() string   { return e.Name }
func (e EnvProvidesEntry) GetSource() string { return e.Source }
func (e MCPProvidesEntry) GetName() string   { return e.Name }
func (e MCPProvidesEntry) GetSource() string { return e.Source }

// filterOwnProvides removes entries injected by the given image (self-exclusion).
// NOTE: No longer used in GlobalEnvForImage (replaced by podAwareEnvProvides).
// Kept for removeBySource and other callers that need strict exclusion.
func filterOwnProvides[T Named](entries []T, imageName string) []T {
	if imageName == "" {
		return entries
	}
	var result []T
	for _, e := range entries {
		if e.GetSource() != imageName {
			result = append(result, e)
		}
	}
	return result
}

// podAwareEnvProvides resolves env entries for a specific consumer image.
// Same-image entries get container hostname rewritten to localhost (pod: co-located services).
// Cross-image entries keep their container hostname URLs.
// If both local and remote share a name, local wins.
func podAwareEnvProvides(entries []EnvProvidesEntry, imageName, ctrName string) []EnvProvidesEntry {
	var result []EnvProvidesEntry
	seen := map[string]bool{} // name → true if local entry added
	// First pass: add same-image entries with localhost
	for _, e := range entries {
		if isSameBaseImage(e.Source, imageName) {
			local := e
			local.Value = strings.ReplaceAll(e.Value, ctrName, "localhost")
			result = append(result, local)
			seen[e.Name] = true
		}
	}
	// Second pass: add cross-image entries (skip if local exists with same name)
	for _, e := range entries {
		if e.Source != imageName && !seen[e.Name] {
			result = append(result, e)
		}
	}
	return result
}

// removeBySource removes all entries injected by the given image.
// Returns the filtered list and whether anything was removed.
func removeBySource[T Named](entries []T, imageName string) ([]T, bool) {
	var result []T
	removed := false
	for _, e := range entries {
		if isSameBaseImage(e.GetSource(), imageName) {
			removed = true
		} else {
			result = append(result, e)
		}
	}
	return result, removed
}

// removeByExactSource removes entries whose source matches the exact deploy key.
// Unlike removeBySource, this does not match other instances of the same base image.
func removeByExactSource[T Named](entries []T, source string) ([]T, bool) {
	var result []T
	removed := false
	for _, e := range entries {
		if e.GetSource() == source {
			removed = true
		} else {
			result = append(result, e)
		}
	}
	return result, removed
}

// podAwareMCPProvides resolves MCP entries for a specific consumer image.
// Same-image entries get localhost URLs (pod: co-located services).
// Cross-image entries keep their container hostname URLs.
// If both local and remote share a name, local wins.
func podAwareMCPProvides(entries []MCPProvidesEntry, imageName, ctrName string) []MCPProvidesEntry {
	var result []MCPProvidesEntry
	seen := map[string]bool{} // name → true if local entry added
	// First pass: add same-image entries with localhost
	for _, e := range entries {
		if isSameBaseImage(e.Source, imageName) {
			local := e
			local.URL = strings.ReplaceAll(e.URL, ctrName, "localhost")
			result = append(result, local)
			seen[e.Name] = true
		}
	}
	// Second pass: add cross-image entries (skip if local exists with same name)
	for _, e := range entries {
		if e.Source != imageName && !seen[e.Name] {
			result = append(result, e)
		}
	}
	return result
}

// GlobalEnvForImage returns the global env vars from provides for a specific consumer image.
// Both env and MCP provides are pod-aware: same-image entries resolve to localhost,
// cross-image entries keep their container hostname. If both share a name, local wins.
// Returns flat env var slice ready for ResolveEnvVars.
func (dc *DeployConfig) GlobalEnvForImage(imageName, ctrName string) []string {
	if dc == nil || dc.Provides == nil {
		return nil
	}
	var result []string

	// Env provides: pod-aware (same-image entries resolve to localhost)
	for _, entry := range podAwareEnvProvides(dc.Provides.Env, imageName, ctrName) {
		result = appendOrReplaceEnv(result, entry.Name+"="+entry.Value)
	}

	// MCP provides: pod-aware (same-image entries resolve to localhost)
	if len(dc.Provides.MCP) > 0 {
		mcpEntries := podAwareMCPProvides(dc.Provides.MCP, imageName, ctrName)
		if len(mcpEntries) > 0 {
			mcpJSON, _ := json.Marshal(mcpEntries)
			result = append(result, "OV_MCP_SERVERS="+string(mcpJSON))
		}
	}

	return result
}

// resolveTemplate replaces {{.ContainerName}} in a string.
func resolveTemplate(tmpl, containerName string) string {
	return strings.ReplaceAll(tmpl, "{{.ContainerName}}", containerName)
}

// validateProvidesTemplate checks that only {{.ContainerName}} is used.
func validateProvidesTemplate(tmpl string) bool {
	stripped := strings.ReplaceAll(tmpl, "{{.ContainerName}}", "")
	return !strings.Contains(stripped, "{{") && !strings.Contains(stripped, "}}")
}
