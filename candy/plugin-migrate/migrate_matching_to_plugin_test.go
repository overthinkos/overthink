package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestMigrateMatchingToPlugin proves the matching-verb extraction: a deterministic
// `check:` step with an inline `matching:`/`contains:` Op is CONVERTED to the
// generic plugin step (plugin: matching + plugin_input:), while a non-check step
// (agent-check) has the vestigial matching:/contains: STRIPPED with no plugin
// added. Comment-preserving + idempotent.
func TestMigrateMatchingToPlugin(t *testing.T) {
	dir := t.TempDir()
	rootYML := "" +
		"version: 2026.172.0006\n" +
		"sample:\n" +
		"    candy: {}\n" +
		"    plan:\n" +
		"        - check: \"value matches\"\n" +
		"          matching: \"v\"  # keep this comment\n" +
		"          contains:\n" +
		"              contains: \"x\"\n" +
		"        - agent-check: \"agent assesses the value\"\n" +
		"          matching: \"v\"\n" +
		"          contains:\n" +
		"              contains: \"x\"\n" +
		"        - run: \"a plain step\"\n" +
		"          command: \"echo hi\"\n"
	rootPath := filepath.Join(dir, "charly.yml")
	if err := os.WriteFile(rootPath, []byte(rootYML), 0o644); err != nil {
		t.Fatal(err)
	}

	rewritten, err := MigrateMatchingToPlugin(dir, false)
	if err != nil {
		t.Fatalf("MigrateMatchingToPlugin() error = %v", err)
	}
	if len(rewritten) != 1 {
		t.Fatalf("rewrote %v, want the single root charly.yml", rewritten)
	}

	out, _ := os.ReadFile(rootPath)
	// Comment preservation: the moved matching: key keeps its inline comment.
	if !strings.Contains(string(out), "keep this comment") {
		t.Errorf("comment on matching: was lost:\n%s", out)
	}

	var doc map[string]any
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("re-parse migrated YAML: %v", err)
	}
	plan, ok := doc["sample"].(map[string]any)["plan"].([]any)
	if !ok || len(plan) != 3 {
		t.Fatalf("plan shape wrong: %v", doc["sample"])
	}

	// (a) the check: step is converted to plugin: matching + plugin_input.
	checkStep := plan[0].(map[string]any)
	if checkStep["check"] != "value matches" {
		t.Errorf("check intent lost: %v", checkStep["check"])
	}
	if _, has := checkStep["matching"]; has {
		t.Errorf("bare step-level matching: not removed: %v", checkStep)
	}
	if checkStep["plugin"] != "matching" {
		t.Errorf("plugin: matching not added, got %v", checkStep["plugin"])
	}
	pi, ok := checkStep["plugin_input"].(map[string]any)
	if !ok {
		t.Fatalf("plugin_input missing or not a map: %v", checkStep["plugin_input"])
	}
	if pi["matching"] != "v" {
		t.Errorf("plugin_input.matching = %v, want v", pi["matching"])
	}
	cont, ok := pi["contains"].(map[string]any)
	if !ok || cont["contains"] != "x" {
		t.Errorf("plugin_input.contains = %v, want {contains: x}", pi["contains"])
	}

	// (b) the agent-check: step has matching:/contains: stripped, no plugin added.
	agentStep := plan[1].(map[string]any)
	if agentStep["agent-check"] != "agent assesses the value" {
		t.Errorf("agent-check intent lost: %v", agentStep["agent-check"])
	}
	if _, has := agentStep["matching"]; has {
		t.Errorf("vestigial matching: not stripped from agent step: %v", agentStep)
	}
	if _, has := agentStep["contains"]; has {
		t.Errorf("vestigial contains: not stripped from agent step: %v", agentStep)
	}
	if _, has := agentStep["plugin"]; has {
		t.Errorf("plugin: wrongly added to a non-check step: %v", agentStep)
	}

	// The plain run: step (no matching:) is untouched.
	runStep := plan[2].(map[string]any)
	if runStep["command"] != "echo hi" {
		t.Errorf("plain run step mangled: %v", runStep)
	}
	if _, has := runStep["plugin"]; has {
		t.Errorf("plugin: wrongly added to a plain step: %v", runStep)
	}

	// (c) idempotent — a second pass changes nothing (the nested plugin_input
	// matching: is not a step node, so it is never re-processed).
	again, err := MigrateMatchingToPlugin(dir, false)
	if err != nil {
		t.Fatalf("second pass error = %v", err)
	}
	if len(again) != 0 {
		t.Errorf("migration not idempotent — second pass rewrote %v", again)
	}
}

// TestMigrateMatchingToPlugin_NoMatchingUntouched proves a config with no
// `matching:` Op anywhere is left byte-for-byte unchanged (and not reported as
// rewritten).
func TestMigrateMatchingToPlugin_NoMatchingUntouched(t *testing.T) {
	dir := t.TempDir()
	rootYML := "" +
		"version: 2026.172.0006\n" +
		"sample:\n" +
		"    candy: {}\n" +
		"    plan:\n" +
		"        - check: \"a file exists\"\n" +
		"          file: /etc/hostname\n" +
		"        - run: \"do a thing\"\n" +
		"          command: \"echo hi\"\n"
	rootPath := filepath.Join(dir, "charly.yml")
	if err := os.WriteFile(rootPath, []byte(rootYML), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(rootPath)

	rewritten, err := MigrateMatchingToPlugin(dir, false)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if len(rewritten) != 0 {
		t.Errorf("a config with no matching: was rewritten: %v", rewritten)
	}
	after, _ := os.ReadFile(rootPath)
	if string(before) != string(after) {
		t.Errorf("file modified despite no matching: keys:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}
