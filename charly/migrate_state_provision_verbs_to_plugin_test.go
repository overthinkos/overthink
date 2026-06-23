package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestMigrateStateProvisionVerbsToPlugin proves the FIRST state-provision-verb extraction
// (unix_group). Because a state-provision verb is DUAL-NATURED, BOTH a `check:` (assert)
// step AND a `run:` (act) step authoring it are CONVERTED to the generic plugin step
// (plugin: unix_group + plugin_input{unix_group, gid}) — the distinguishing behaviour
// versus the OBSERVE-only goss migrator, which strips a run: step's vestigial keys. A
// verb-less step kind (agent-check/include) has the vestigial keys STRIPPED. The shared
// `gid` companion MOVES into plugin_input on the unix_group step but STAYS untouched on a
// `user:` step (the non-extracted verb that still reads it). Comment-preserving + idempotent.
func TestMigrateStateProvisionVerbsToPlugin(t *testing.T) {
	dir := t.TempDir()
	rootYML := "" +
		"version: 2026.174.0100\n" +
		"sample:\n" +
		"    candy: {}\n" +
		"    plan:\n" +
		"        - check: \"the wheel group exists\"\n" +
		"          unix_group: \"wheel\"  # keep this comment\n" +
		"          gid: 10\n" +
		"        - run: \"create the build group\"\n" +
		"          unix_group: \"builders\"\n" +
		"          gid: 4242\n" +
		"        - agent-check: \"agent assesses the group\"\n" +
		"          unix_group: \"vestigial\"\n" +
		"          gid: 99\n" +
		"        - check: \"the deploy user is present\"\n" +
		"          user: \"deploy\"\n" +
		"          gid: 1000\n" +
		"        - run: \"a plain step\"\n" +
		"          command: \"echo hi\"\n"
	rootPath := filepath.Join(dir, "charly.yml")
	if err := os.WriteFile(rootPath, []byte(rootYML), 0o644); err != nil {
		t.Fatal(err)
	}

	rewritten, err := MigrateStateProvisionVerbsToPlugin(dir, false)
	if err != nil {
		t.Fatalf("MigrateStateProvisionVerbsToPlugin() error = %v", err)
	}
	if len(rewritten) != 1 {
		t.Fatalf("rewrote %v, want the single root charly.yml", rewritten)
	}

	out, _ := os.ReadFile(rootPath)
	if !strings.Contains(string(out), "keep this comment") {
		t.Errorf("comment on unix_group: was lost:\n%s", out)
	}

	var doc map[string]any
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("re-parse migrated YAML: %v", err)
	}
	plan, ok := doc["sample"].(map[string]any)["plan"].([]any)
	if !ok || len(plan) != 5 {
		t.Fatalf("plan shape wrong (len=%d): %v", len(plan), doc["sample"])
	}

	// (a) a check: unix_group step → plugin: unix_group + plugin_input{unix_group, gid}.
	checkStep := plan[0].(map[string]any)
	if checkStep["plugin"] != "unix_group" {
		t.Errorf("step 0: plugin: unix_group not added, got %v", checkStep["plugin"])
	}
	if _, has := checkStep["unix_group"]; has {
		t.Errorf("step 0: bare unix_group: not removed: %v", checkStep)
	}
	if _, has := checkStep["gid"]; has {
		t.Errorf("step 0: bare gid: not removed (must move into plugin_input): %v", checkStep)
	}
	checkPI := checkStep["plugin_input"].(map[string]any)
	if checkPI["unix_group"] != "wheel" || checkPI["gid"] != 10 {
		t.Errorf("step 0: plugin_input = %v, want {unix_group: wheel, gid: 10}", checkPI)
	}

	// (b) a RUN: unix_group step is ALSO converted (the act timeline) — the key distinction
	//     from the observe-only goss migrator, which would strip a run: step.
	runStep := plan[1].(map[string]any)
	if runStep["plugin"] != "unix_group" {
		t.Errorf("step 1: a run: unix_group step must CONVERT, got plugin=%v: %v", runStep["plugin"], runStep)
	}
	if runStep["run"] != "create the build group" {
		t.Errorf("step 1: the run: keyword must be preserved: %v", runStep)
	}
	runPI := runStep["plugin_input"].(map[string]any)
	if runPI["unix_group"] != "builders" || runPI["gid"] != 4242 {
		t.Errorf("step 1: plugin_input = %v, want {unix_group: builders, gid: 4242}", runPI)
	}

	// (c) a verb-less step kind (agent-check) has the vestigial unix_group:/gid: stripped,
	//     no plugin added.
	agentStep := plan[2].(map[string]any)
	if _, has := agentStep["unix_group"]; has {
		t.Errorf("step 2: vestigial unix_group: not stripped: %v", agentStep)
	}
	if _, has := agentStep["gid"]; has {
		t.Errorf("step 2: vestigial gid: not stripped: %v", agentStep)
	}
	if _, has := agentStep["plugin"]; has {
		t.Errorf("step 2: plugin: wrongly added to a verb-less step: %v", agentStep)
	}

	// (d) a user: step (the non-extracted verb that still reads gid) is UNTOUCHED — gid
	//     STAYS at step level, no plugin added.
	userStep := plan[3].(map[string]any)
	if userStep["user"] != "deploy" || userStep["gid"] != 1000 {
		t.Errorf("step 3: user step wrongly modified (gid must stay for the user verb): %v", userStep)
	}
	if _, has := userStep["plugin"]; has {
		t.Errorf("step 3: plugin: wrongly added to a user step: %v", userStep)
	}

	// (e) the plain run: command step is untouched.
	plainStep := plan[4].(map[string]any)
	if plainStep["command"] != "echo hi" {
		t.Errorf("step 4: plain run step mangled: %v", plainStep)
	}

	// (f) idempotent — a second pass changes nothing (the nested plugin_input keys are not
	//     step nodes, so they are never re-processed).
	again, err := MigrateStateProvisionVerbsToPlugin(dir, false)
	if err != nil {
		t.Fatalf("second pass error = %v", err)
	}
	if len(again) != 0 {
		t.Errorf("migration not idempotent — second pass rewrote %v", again)
	}
}

// TestMigrateStateProvisionVerbsToPlugin_NoVerbUntouched proves a config with no
// unix_group Op anywhere is left byte-for-byte unchanged — even when it carries a `user:`
// step that uses the shared gid companion.
func TestMigrateStateProvisionVerbsToPlugin_NoVerbUntouched(t *testing.T) {
	dir := t.TempDir()
	rootYML := "" +
		"version: 2026.174.0100\n" +
		"sample:\n" +
		"    candy: {}\n" +
		"    plan:\n" +
		"        - check: \"the deploy user is present\"\n" +
		"          user: \"deploy\"\n" +
		"          gid: 1000\n" +
		"        - run: \"do a thing\"\n" +
		"          command: \"echo hi\"\n"
	rootPath := filepath.Join(dir, "charly.yml")
	if err := os.WriteFile(rootPath, []byte(rootYML), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(rootPath)

	rewritten, err := MigrateStateProvisionVerbsToPlugin(dir, false)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if len(rewritten) != 0 {
		t.Errorf("a config with no unix_group verb was rewritten: %v", rewritten)
	}
	after, _ := os.ReadFile(rootPath)
	if string(before) != string(after) {
		t.Errorf("file modified despite no unix_group keys:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}
