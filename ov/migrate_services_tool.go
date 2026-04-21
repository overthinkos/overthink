package main

// migrate_services_tool.go — migration logic that converts legacy
// `service:` (raw supervisord INI) and `system_services:` (list of
// unit names) into the unified `services:` schema introduced in Task 6.
//
// The migration is invoked via an explicit Go test
// (migrate_services_test.go) that's guarded by the OV_RUN_MIGRATION
// env var so it doesn't run in normal test runs. After all layers are
// migrated the tool can be deleted — the compiler only needs to
// understand the unified schema going forward (legacy-compat path
// stays in the compiler to keep `go test` green without migration).

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MigrateServicesDir walks dir for layer.yml files and rewrites each
// legacy service:/system_services: block into the unified services:
// schema. Returns the count of files modified.
func MigrateServicesDir(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("reading %s: %w", dir, err)
	}
	migrated := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name(), "layer.yml")
		changed, err := migrateLayerFile(path)
		if err != nil {
			return migrated, fmt.Errorf("%s: %w", path, err)
		}
		if changed {
			migrated++
		}
	}
	return migrated, nil
}

// migrateLayerFile reads path, rewrites service:/system_services: into
// the unified services: list, and writes back. Returns true when the
// file was modified.
func migrateLayerFile(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	original := string(data)
	// Cheap early-out when there's nothing legacy to migrate.
	hasLegacy := strings.Contains(original, "\nservice:") ||
		strings.Contains(original, "\nsystem_services:") ||
		strings.HasPrefix(original, "service:") ||
		strings.HasPrefix(original, "system_services:")
	if !hasLegacy {
		return false, nil
	}
	// Already migrated? Skip if services: present.
	if strings.Contains(original, "\nservices:") || strings.HasPrefix(original, "services:") {
		return false, nil
	}

	service := extractBlock(original, "service:")
	systemServices := extractListBlock(original, "system_services:")

	entries := buildServicesEntries(service, systemServices)
	if len(entries) == 0 {
		return false, nil
	}

	updated := removeBlock(original, "service:")
	updated = removeBlock(updated, "system_services:")
	servicesYAML := renderServicesYAML(entries)
	updated = strings.TrimRight(updated, "\n") + "\n\n" + servicesYAML + "\n"

	if updated == original {
		return false, nil
	}
	return true, os.WriteFile(path, []byte(updated), 0644)
}

// extractBlock returns the content of a top-level scalar block like
//
//	service: |
//	  [program:foo]
//	  command=/bin/foo
//
// Returns "" when key is not present.
func extractBlock(src, key string) string {
	lines := strings.Split(src, "\n")
	var blockLines []string
	inBlock := false
	indent := ""
	for _, line := range lines {
		if !inBlock {
			if line == key+" |" || strings.HasPrefix(line, key+" |") {
				inBlock = true
				indent = ""
				continue
			}
			continue
		}
		if line == "" {
			blockLines = append(blockLines, "")
			continue
		}
		if indent == "" {
			trimmed := strings.TrimLeft(line, " \t")
			indent = line[:len(line)-len(trimmed)]
		}
		if strings.HasPrefix(line, indent) {
			blockLines = append(blockLines, strings.TrimPrefix(line, indent))
			continue
		}
		break
	}
	return strings.TrimRight(strings.Join(blockLines, "\n"), "\n")
}

// extractListBlock returns the items of a top-level sequence.
func extractListBlock(src, key string) []string {
	lines := strings.Split(src, "\n")
	var items []string
	inBlock := false
	for _, line := range lines {
		if !inBlock {
			if line == key || strings.HasPrefix(line, key+"\n") ||
				strings.HasPrefix(line, key) && strings.HasSuffix(strings.TrimSpace(line), ":") {
				if strings.HasPrefix(line, key) && (len(line) == len(key) || strings.TrimSpace(line[len(key):]) == "") {
					inBlock = true
					continue
				}
			}
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- ") {
			items = append(items, strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))
			continue
		}
		if trimmed != "" {
			break
		}
	}
	return items
}

// removeBlock strips the lines of a top-level block from src.
func removeBlock(src, key string) string {
	lines := strings.Split(src, "\n")
	out := make([]string, 0, len(lines))
	inBlock := false
	for _, line := range lines {
		if !inBlock {
			// Match the start of the block — either scalar "key: |" or
			// "key:" followed by an indented list/scalar.
			if line == key+" |" || strings.HasPrefix(line, key+" |") {
				inBlock = true
				continue
			}
			if line == key || strings.HasPrefix(line, key+":") && !strings.Contains(line, ": |") &&
				strings.TrimSpace(strings.TrimPrefix(line, key)) == "" {
				inBlock = true
				continue
			}
			out = append(out, line)
			continue
		}
		if line == "" {
			out = append(out, line)
			continue
		}
		// Indented continuation.
		if strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "\t") {
			continue
		}
		// List element at top level (for system_services: where items
		// appear with `-` at column 0 after 2-space indent isn't common,
		// but safeguard).
		if strings.HasPrefix(strings.TrimSpace(line), "- ") {
			continue
		}
		// Back to top level — resume normal output.
		inBlock = false
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// buildServicesEntries parses legacy blocks into structured entries.
func buildServicesEntries(serviceINI string, systemUnits []string) []map[string]interface{} {
	var out []map[string]interface{}

	for _, unit := range systemUnits {
		name := strings.TrimSuffix(unit, ".service")
		out = append(out, map[string]interface{}{
			"name":         name,
			"use_packaged": appendServiceSuffix(unit),
			"enable":       true,
			"scope":        "system",
		})
	}

	for _, program := range parseSupervisordPrograms(serviceINI) {
		entry := map[string]interface{}{
			"name":   program.Name,
			"enable": true,
			"scope":  "system",
		}
		if program.Command != "" {
			entry["exec"] = program.Command
		}
		if len(program.Environment) > 0 {
			env := map[string]string{}
			for k, v := range program.Environment {
				env[k] = v
			}
			entry["env"] = env
		}
		switch program.AutoRestart {
		case "true":
			entry["restart"] = "always"
		case "unexpected":
			entry["restart"] = "on-failure"
		case "false":
			entry["restart"] = "no"
		}
		if program.User != "" {
			entry["user"] = program.User
		}
		if program.Directory != "" {
			entry["working_directory"] = program.Directory
		}
		if program.Priority != "" {
			entry["priority"] = program.Priority
		}
		out = append(out, entry)
	}
	return out
}

type supervisordProgram struct {
	Name        string
	Command     string
	AutoRestart string
	User        string
	Directory   string
	Priority    string
	Environment map[string]string
}

func parseSupervisordPrograms(ini string) []supervisordProgram {
	var programs []supervisordProgram
	var cur *supervisordProgram
	scanner := bufio.NewScanner(strings.NewReader(ini))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[program:") {
			if cur != nil {
				programs = append(programs, *cur)
			}
			name := strings.TrimSuffix(strings.TrimPrefix(line, "[program:"), "]")
			cur = &supervisordProgram{Name: name, Environment: map[string]string{}}
			continue
		}
		if cur == nil {
			continue
		}
		eq := strings.Index(line, "=")
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		switch key {
		case "command":
			cur.Command = val
		case "autorestart":
			cur.AutoRestart = val
		case "user":
			cur.User = val
		case "directory":
			cur.Directory = val
		case "priority":
			cur.Priority = val
		case "environment":
			for _, e := range parseSupervisordEnv(val) {
				cur.Environment[e.Key] = e.Val
			}
		}
	}
	if cur != nil {
		programs = append(programs, *cur)
	}
	return programs
}

type envKV struct{ Key, Val string }

func parseSupervisordEnv(s string) []envKV {
	var out []envKV
	var cur strings.Builder
	inQuote := false
	tokens := []string{}
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '"' {
			inQuote = !inQuote
			continue
		}
		if ch == ',' && !inQuote {
			tokens = append(tokens, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteByte(ch)
	}
	if cur.Len() > 0 {
		tokens = append(tokens, cur.String())
	}
	for _, t := range tokens {
		t = strings.TrimSpace(t)
		if eq := strings.Index(t, "="); eq > 0 {
			out = append(out, envKV{Key: strings.TrimSpace(t[:eq]), Val: strings.TrimSpace(t[eq+1:])})
		}
	}
	return out
}

func renderServicesYAML(entries []map[string]interface{}) string {
	var b strings.Builder
	b.WriteString("services:\n")
	for i, e := range entries {
		b.WriteString("  - name: " + yamlScalar(e["name"].(string)) + "\n")
		if up, ok := e["use_packaged"].(string); ok {
			b.WriteString("    use_packaged: " + up + "\n")
		}
		if ex, ok := e["exec"].(string); ok {
			b.WriteString("    exec: " + yamlScalar(ex) + "\n")
		}
		if envMap, ok := e["env"].(map[string]string); ok && len(envMap) > 0 {
			b.WriteString("    env:\n")
			keys := make([]string, 0, len(envMap))
			for k := range envMap {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				b.WriteString("      " + k + ": " + yamlScalar(envMap[k]) + "\n")
			}
		}
		if r, ok := e["restart"].(string); ok {
			b.WriteString("    restart: " + r + "\n")
		}
		if u, ok := e["user"].(string); ok {
			b.WriteString("    user: " + yamlScalar(u) + "\n")
		}
		if wd, ok := e["working_directory"].(string); ok {
			b.WriteString("    working_directory: " + yamlScalar(wd) + "\n")
		}
		if p, ok := e["priority"].(string); ok {
			b.WriteString("    priority: " + p + "\n")
		}
		if en, ok := e["enable"].(bool); ok && en {
			b.WriteString("    enable: true\n")
		}
		if sc, ok := e["scope"].(string); ok {
			b.WriteString("    scope: " + sc + "\n")
		}
		if i < len(entries)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func yamlScalar(v string) string {
	if v == "" {
		return `""`
	}
	// YAML requires quoting for:
	//   - values containing flow-style / indicator characters
	//   - values starting with indicator characters (-, ?, :, @, `, %, !, &, *)
	//   - values starting with a tab/space (rare)
	needsQuote := strings.ContainsAny(v, ": #'\"\n\t[]{},&*!>|")
	if len(v) > 0 {
		switch v[0] {
		case '-', '?', ':', '@', '`', '%', '!', '&', '*', ' ', '\t':
			needsQuote = true
		}
	}
	if needsQuote {
		return `"` + strings.ReplaceAll(v, `"`, `\"`) + `"`
	}
	return v
}

func appendServiceSuffix(u string) string {
	for _, s := range []string{".service", ".timer", ".socket", ".path", ".target", ".mount", ".slice"} {
		if strings.HasSuffix(u, s) {
			return u
		}
	}
	return u + ".service"
}
