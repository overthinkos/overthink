package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// dotenvLoaded tracks which env var names were loaded from .env (for source attribution in ov config list).
var dotenvLoaded = make(map[string]bool)

// DotenvLoaded reports whether a given env var name was loaded from the project .env file.
func DotenvLoaded(name string) bool {
	return dotenvLoaded[name]
}

// resetDotenvLoaded clears the tracking map (for testing).
func resetDotenvLoaded() {
	dotenvLoaded = make(map[string]bool)
}

// LoadProcessDotenv loads .env from dir into the process environment.
// Variables already set in the environment are NOT overwritten (real env wins).
// Silently returns nil if .env does not exist.
func LoadProcessDotenv(dir string) error {
	envPath := filepath.Join(dir, ".env")
	if _, err := os.Stat(envPath); os.IsNotExist(err) {
		return nil
	}

	entries, err := ParseEnvFile(envPath)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		idx := strings.IndexByte(entry, '=')
		if idx < 0 {
			// Bare KEY (no value) — skip, inheriting from host is meaningless here
			continue
		}
		key := entry[:idx]
		value := entry[idx+1:]

		// Only set if NOT already in environment (real env takes precedence)
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, value)
			dotenvLoaded[key] = true
		}
	}

	return nil
}

// ParseEnvFile reads a .env file and returns KEY=VALUE strings.
// Skips comments (#), blank lines, and supports KEY=VALUE and KEY="VALUE" (strips quotes).
// Compatible with docker --env-file format.
func ParseEnvFile(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading env file %s: %w", path, err)
	}
	return ParseEnvBytes(data)
}

// ParseEnvBytes parses KEY=VALUE entries from raw bytes.
// Skips comments (#), blank lines, and strips surrounding quotes from values.
func ParseEnvBytes(data []byte) ([]string, error) {
	var envs []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip blank lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Must contain = for KEY=VALUE format
		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			// KEY without value — pass through as-is (docker behavior: inherits from host)
			envs = append(envs, line)
			continue
		}

		key := line[:idx]
		value := line[idx+1:]

		// Strip surrounding quotes from value
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}

		envs = append(envs, key+"="+value)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("parsing env data: %w", err)
	}

	return envs, nil
}

// LoadWorkspaceEnv loads env vars from a workspace .env file (if it exists).
// Does NOT run direnv — direnv modifies the host env before ov runs.
// Returns nil, nil if no .env file found.
func LoadWorkspaceEnv(workspace string) ([]string, error) {
	envPath := filepath.Join(workspace, ".env")
	if _, err := os.Stat(envPath); os.IsNotExist(err) {
		return nil, nil
	}
	return ParseEnvFile(envPath)
}

// ResolveEnvVars merges env vars from multiple sources.
// Priority (last wins for duplicate keys): global env < deploy config < workspace .env < CLI --env-file < CLI -e flags.
func ResolveEnvVars(globalEnv []string, deployEnv []string, deployEnvFile string, envDir string, cliEnvFile string, cliEnv []string) ([]string, error) {
	var all []string

	// 0. Global env vars from deploy.yml (lowest priority — service discovery)
	all = append(all, globalEnv...)

	// 1. Per-image deploy config env vars
	all = append(all, deployEnv...)

	// 2. Deploy config env file
	if deployEnvFile != "" {
		expanded := expandHostHome(deployEnvFile)
		vars, err := ParseEnvFile(expanded)
		if err != nil {
			return nil, err
		}
		all = append(all, vars...)
	}

	// 3. Workspace .env file
	if envDir != "" {
		vars, err := LoadWorkspaceEnv(envDir)
		if err != nil {
			return nil, err
		}
		all = append(all, vars...)
	}

	// 4. CLI --env-file
	if cliEnvFile != "" {
		vars, err := ParseEnvFile(cliEnvFile)
		if err != nil {
			return nil, err
		}
		all = append(all, vars...)
	}

	// 5. CLI -e flags (highest priority)
	all = append(all, cliEnv...)

	// Deduplicate: last value for each key wins, then normalize NO_PROXY
	return normalizeNoProxy(deduplicateEnv(all)), nil
}

// normalizeNoProxy converts semicolons to commas in NO_PROXY/no_proxy values.
// Semicolons were a workaround for Kong's comma-splitting of []string flags.
// Standard HTTP clients (Python, curl, Go) require comma-separated NO_PROXY.
func normalizeNoProxy(envs []string) []string {
	for i, e := range envs {
		idx := strings.IndexByte(e, '=')
		if idx < 0 {
			continue
		}
		key := e[:idx]
		if key == "NO_PROXY" || key == "no_proxy" {
			val := e[idx+1:]
			envs[i] = key + "=" + strings.ReplaceAll(val, ";", ",")
		}
	}
	return envs
}

// deduplicateEnv deduplicates env vars, keeping the last value for each key.
func deduplicateEnv(envs []string) []string {
	seen := make(map[string]int) // key -> index in result
	var result []string

	for _, e := range envs {
		key := e
		if idx := strings.IndexByte(e, '='); idx >= 0 {
			key = e[:idx]
		}

		if prevIdx, ok := seen[key]; ok {
			// Replace previous entry
			result[prevIdx] = e
		} else {
			seen[key] = len(result)
			result = append(result, e)
		}
	}

	return result
}
