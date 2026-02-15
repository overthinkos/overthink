package main

import (
	"bufio"
	"os"
	"strings"
)

// EnvConfig represents environment variables from a layer's env file
type EnvConfig struct {
	Vars       map[string]string // KEY=value pairs
	PathAppend []string          // PATH+= entries (without the PATH+=: prefix)
}

// ParseEnvFile reads and parses a layer's env file
func ParseEnvFile(path string) (*EnvConfig, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	cfg := &EnvConfig{
		Vars:       make(map[string]string),
		PathAppend: []string{},
	}

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Handle PATH+= specially
		if strings.HasPrefix(line, "PATH+=") {
			value := strings.TrimPrefix(line, "PATH+=")
			// Remove leading colon if present
			value = strings.TrimPrefix(value, ":")
			if value != "" {
				cfg.PathAppend = append(cfg.PathAppend, value)
			}
			continue
		}

		// Parse KEY=value
		idx := strings.Index(line, "=")
		if idx == -1 {
			continue // Invalid line, skip
		}

		key := strings.TrimSpace(line[:idx])
		value := strings.TrimSpace(line[idx+1:])

		// Remove quotes if present
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}

		cfg.Vars[key] = value
	}

	return cfg, scanner.Err()
}

// ExpandPath expands ~ and $HOME in a path string to the given home directory
func ExpandPath(path string, home string) string {
	// Expand ~ at the start of the path
	if strings.HasPrefix(path, "~/") {
		path = home + path[1:]
	} else if path == "~" {
		path = home
	}

	// Expand $HOME anywhere in the path
	path = strings.ReplaceAll(path, "$HOME", home)

	return path
}

// ExpandEnvConfig expands all ~ and $HOME references in an EnvConfig
func ExpandEnvConfig(cfg *EnvConfig, home string) *EnvConfig {
	expanded := &EnvConfig{
		Vars:       make(map[string]string),
		PathAppend: make([]string, len(cfg.PathAppend)),
	}

	for key, value := range cfg.Vars {
		expanded.Vars[key] = ExpandPath(value, home)
	}

	for i, path := range cfg.PathAppend {
		expanded.PathAppend[i] = ExpandPath(path, home)
	}

	return expanded
}

// MergeEnvConfigs merges multiple env configs, later configs override earlier
func MergeEnvConfigs(configs []*EnvConfig) *EnvConfig {
	merged := &EnvConfig{
		Vars:       make(map[string]string),
		PathAppend: []string{},
	}

	for _, cfg := range configs {
		if cfg == nil {
			continue
		}
		// Merge vars (later overrides earlier)
		for key, value := range cfg.Vars {
			merged.Vars[key] = value
		}
		// Accumulate PATH entries
		merged.PathAppend = append(merged.PathAppend, cfg.PathAppend...)
	}

	return merged
}
