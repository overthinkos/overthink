package migrate

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// MigrateShellSchema walks dir/layers/ and rewrites every layer.yml carrying
// a legacy shell-rc heredoc cmd: task into the structured shell: schema.
// Returns the list of files changed (or that would change under dryRun).
// This is the chain-callable form used by the unified `charly migrate` runner.
func MigrateShellSchema(dir string, dryRun bool) ([]string, error) {
	candiesDir := filepath.Join(dir, "layers")
	entries, err := os.ReadDir(candiesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var changed []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candyYAML := filepath.Join(candiesDir, entry.Name(), "layer.yml")
		if _, err := os.Stat(candyYAML); err != nil {
			continue
		}
		did, err := migrateCandyShellSchema(candyYAML, dryRun)
		if err != nil {
			return changed, fmt.Errorf("%s: %w", candyYAML, err)
		}
		if did {
			changed = append(changed, candyYAML)
		}
	}
	return changed, nil
}

// migrateCandyShellSchema parses one layer.yml, scans for legacy
// heredoc cmd: tasks, and rewrites the file with a shell: schema.
// Returns (changed, err) — changed is true when the file's content
// would actually differ.
func migrateCandyShellSchema(path string, dryRun bool) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return false, err
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return false, nil
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return false, nil
	}
	// Locate the layer: wrapper if present, else the doc itself.
	body := doc
	for i := 0; i < len(doc.Content)-1; i += 2 {
		if doc.Content[i].Value == "layer" && doc.Content[i+1].Kind == yaml.MappingNode {
			body = doc.Content[i+1]
			break
		}
	}
	tasksNode := findChildNode(body, "tasks")
	if tasksNode == nil || tasksNode.Kind != yaml.SequenceNode {
		return false, nil
	}
	// Walk tasks looking for heredoc cmd: bodies that match the
	// canonical fence patterns. Collect (index, parsed shell entry)
	// pairs; remove the matched tasks; build a synthesized shell:
	// node from the parsed entries.
	var legacyIndices []int
	bashSnippet := ""
	zshSnippet := ""
	fishSnippet := ""
	for i, taskNode := range tasksNode.Content {
		if taskNode.Kind != yaml.MappingNode {
			continue
		}
		cmdNode := findChildNode(taskNode, "cmd")
		if cmdNode == nil {
			continue
		}
		body := cmdNode.Value
		if !looksLikeLegacyShellRcHeredoc(body) {
			continue
		}
		legacyIndices = append(legacyIndices, i)
		bash, zsh, fish := extractLegacyShellBodies(body)
		if bash != "" {
			bashSnippet = bash
		}
		if zsh != "" {
			zshSnippet = zsh
		}
		if fish != "" {
			fishSnippet = fish
		}
	}
	if len(legacyIndices) == 0 {
		return false, nil
	}
	// Remove matched tasks (in reverse order so indices stay valid).
	for _, idx := range slices.Backward(legacyIndices) {

		tasksNode.Content = append(tasksNode.Content[:idx], tasksNode.Content[idx+1:]...)
	}
	// Build a shell: node and merge into body.
	shellNode := buildShellNodeFromLegacy(bashSnippet, zshSnippet, fishSnippet)
	mergeChildNode(body, "shell", shellNode)
	if dryRun {
		return true, nil
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&root); err != nil {
		return false, err
	}
	_ = enc.Close()
	return true, os.WriteFile(path, buf.Bytes(), 0644)
}

// findChildNode returns the value node for the named key in a mapping
// node, or nil if absent.
func findChildNode(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(m.Content)-1; i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// mergeChildNode upserts a key-value pair into a mapping node. If the
// key exists, its value is replaced; otherwise the pair is appended.
func mergeChildNode(m *yaml.Node, key string, value *yaml.Node) {
	if m == nil || m.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i < len(m.Content)-1; i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = value
			return
		}
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: key, Tag: "!!str"}
	m.Content = append(m.Content, keyNode, value)
}

// looksLikeLegacyShellRcHeredoc returns true when a cmd: body contains
// the canonical fence pattern AND the heredoc-creation signature
// (`cat >`, `cat >>`, or a heredoc `EOF` delimiter). This distinguishes
// a legacy install task that WRITES a fenced block from a cleanup task
// that STRIPS one (uses `sed -i '/.../d'` instead of `cat`).
func looksLikeLegacyShellRcHeredoc(body string) bool {
	fences := []string{
		"# overthink:begin direnv-hook",
		"# overthink:begin ssh-auth-sock",
	}
	hasFence := false
	for _, p := range fences {
		if strings.Contains(body, p) {
			hasFence = true
			break
		}
	}
	if !hasFence {
		return false
	}
	// Strip-tasks use sed; install-tasks use cat. Distinguishing on the
	// presence of a write-redirect (`>` or `>>`) keeps the migrator
	// from rewriting cleanup tasks that legitimately reference the
	// fence string for removal.
	hasWrite := strings.Contains(body, "cat >") || strings.Contains(body, "cat >>")
	hasSed := strings.Contains(body, "sed -i")
	return hasWrite && !hasSed
}

// extractLegacyShellBodies parses the per-shell snippet bodies out of
// a legacy heredoc cmd: body. Returns (bashBody, zshBody, fishBody).
// Empty strings indicate the body wasn't recognised for that shell.
func extractLegacyShellBodies(body string) (bash, zsh, fish string) {
	scanner := bufio.NewScanner(strings.NewReader(body))
	currentShell := ""
	var collected []string
	flush := func() {
		if currentShell == "" || len(collected) == 0 {
			return
		}
		s := strings.Join(collected, "\n") + "\n"
		switch currentShell {
		case "bash":
			bash = s
		case "zsh":
			zsh = s
		case "fish":
			fish = s
		}
		currentShell = ""
		collected = nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "BASHRC=") || strings.Contains(line, ".bashrc") {
			flush()
			currentShell = "bash"
			continue
		}
		if strings.Contains(line, "ZSHRC=") || strings.Contains(line, ".zshrc") {
			flush()
			currentShell = "zsh"
			continue
		}
		if strings.Contains(line, "fish/conf.d/direnv.fish") {
			flush()
			currentShell = "fish"
			continue
		}
		// Heuristic: extract the actual hook line.
		l := strings.TrimSpace(line)
		if strings.Contains(l, "direnv hook") {
			collected = append(collected, l)
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: legacy shell body scan error: %v\n", err)
	}
	flush()
	return bash, zsh, fish
}

// buildShellNodeFromLegacy constructs a yaml.Node tree representing a
// `shell:` block built from the extracted per-shell bodies.
func buildShellNodeFromLegacy(bash, zsh, fish string) *yaml.Node {
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	addShell := func(name, body string) {
		if body == "" {
			return
		}
		key := &yaml.Node{Kind: yaml.ScalarNode, Value: name, Tag: "!!str"}
		val := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		val.Content = append(val.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "init", Tag: "!!str"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: body, Style: yaml.LiteralStyle, Tag: "!!str"},
		)
		node.Content = append(node.Content, key, val)
	}
	addShell("bash", bash)
	addShell("zsh", zsh)
	addShell("fish", fish)
	return node
}
