package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestMigrateGossVerbsToPlugin proves the observe-only goss-verb extraction across all
// three extracted verbs (process/port/dns): a deterministic `check:` step with an inline
// verb + companion Op fields is CONVERTED to the generic plugin step (plugin: <verb> +
// plugin_input:), while a non-check step has the vestigial keys STRIPPED. The
// state-provision verbs `service:`/`package:` are NOT extracted, so their steps (and the
// shared `running:`/`enabled:` they carry) are left UNTOUCHED. Comment-preserving +
// idempotent.
func TestMigrateGossVerbsToPlugin(t *testing.T) {
	dir := t.TempDir()
	rootYML := "" +
		"version: 2026.172.0006\n" +
		"sample:\n" +
		"    candy: {}\n" +
		"    plan:\n" +
		"        - check: \"redis-server is running\"\n" +
		"          process: \"redis-server\"  # keep this comment\n" +
		"          running: true\n" +
		"        - check: \"port 6379 listens\"\n" +
		"          port: 6379\n" +
		"          listening: true\n" +
		"          reachable: false\n" +
		"        - check: \"localhost resolves\"\n" +
		"          dns: \"localhost\"\n" +
		"          addrs: [\"127.0.0.1\"]\n" +
		"        - agent-check: \"agent assesses the process\"\n" +
		"          process: \"vestigial\"\n" +
		"          running: false\n" +
		"        - check: \"a service is up\"\n" +
		"          service: \"jupyter\"\n" +
		"          running: true\n" +
		"        - check: \"the redis package is installed\"\n" +
		"          package: \"valkey-compat-redis\"\n" +
		"          installed: true\n" +
		"        - run: \"a plain step\"\n" +
		"          command: \"echo hi\"\n"
	rootPath := filepath.Join(dir, "charly.yml")
	if err := os.WriteFile(rootPath, []byte(rootYML), 0o644); err != nil {
		t.Fatal(err)
	}

	rewritten, err := MigrateGossVerbsToPlugin(dir, false)
	if err != nil {
		t.Fatalf("MigrateGossVerbsToPlugin() error = %v", err)
	}
	if len(rewritten) != 1 {
		t.Fatalf("rewrote %v, want the single root charly.yml", rewritten)
	}

	out, _ := os.ReadFile(rootPath)
	if !strings.Contains(string(out), "keep this comment") {
		t.Errorf("comment on process: was lost:\n%s", out)
	}

	var doc map[string]any
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("re-parse migrated YAML: %v", err)
	}
	plan, ok := doc["sample"].(map[string]any)["plan"].([]any)
	if !ok || len(plan) != 7 {
		t.Fatalf("plan shape wrong (len=%d): %v", len(plan), doc["sample"])
	}

	// (a) process check step → plugin: process + plugin_input{process, running}.
	procStep := plan[0].(map[string]any)
	if procStep["plugin"] != "process" {
		t.Errorf("step 0: plugin: process not added, got %v", procStep["plugin"])
	}
	if _, has := procStep["process"]; has {
		t.Errorf("step 0: bare process: not removed: %v", procStep)
	}
	procPI := procStep["plugin_input"].(map[string]any)
	if procPI["process"] != "redis-server" || procPI["running"] != true {
		t.Errorf("step 0: plugin_input = %v, want {process: redis-server, running: true}", procPI)
	}

	// (b) port check step → plugin: port + plugin_input{port, listening, reachable}.
	portStep := plan[1].(map[string]any)
	if portStep["plugin"] != "port" {
		t.Errorf("step 1: plugin: port not added, got %v", portStep["plugin"])
	}
	portPI := portStep["plugin_input"].(map[string]any)
	if portPI["port"] != 6379 || portPI["listening"] != true || portPI["reachable"] != false {
		t.Errorf("step 1: plugin_input = %v, want {port: 6379, listening: true, reachable: false}", portPI)
	}

	// (c) dns check step → plugin: dns + plugin_input{dns, addrs}.
	dnsStep := plan[2].(map[string]any)
	if dnsStep["plugin"] != "dns" {
		t.Errorf("step 2: plugin: dns not added, got %v", dnsStep["plugin"])
	}
	dnsPI := dnsStep["plugin_input"].(map[string]any)
	if dnsPI["dns"] != "localhost" {
		t.Errorf("step 2: plugin_input.dns = %v, want localhost", dnsPI["dns"])
	}
	if addrs, ok := dnsPI["addrs"].([]any); !ok || len(addrs) != 1 || addrs[0] != "127.0.0.1" {
		t.Errorf("step 2: plugin_input.addrs = %v, want [127.0.0.1]", dnsPI["addrs"])
	}

	// (d) the agent-check process step has process:/running: stripped, no plugin added.
	agentStep := plan[3].(map[string]any)
	if _, has := agentStep["process"]; has {
		t.Errorf("step 3: vestigial process: not stripped: %v", agentStep)
	}
	if _, has := agentStep["running"]; has {
		t.Errorf("step 3: vestigial running: not stripped: %v", agentStep)
	}
	if _, has := agentStep["plugin"]; has {
		t.Errorf("step 3: plugin: wrongly added to a non-check step: %v", agentStep)
	}

	// (e) the service: step (NOT extracted) is untouched — its shared running: stays.
	svcStep := plan[4].(map[string]any)
	if svcStep["service"] != "jupyter" || svcStep["running"] != true {
		t.Errorf("step 4: service step wrongly modified: %v", svcStep)
	}
	if _, has := svcStep["plugin"]; has {
		t.Errorf("step 4: plugin: wrongly added to a service step: %v", svcStep)
	}

	// (f) the package: step (NOT extracted) is untouched.
	pkgStep := plan[5].(map[string]any)
	if pkgStep["package"] != "valkey-compat-redis" || pkgStep["installed"] != true {
		t.Errorf("step 5: package step wrongly modified: %v", pkgStep)
	}
	if _, has := pkgStep["plugin"]; has {
		t.Errorf("step 5: plugin: wrongly added to a package step: %v", pkgStep)
	}

	// (g) the plain run: step is untouched.
	runStep := plan[6].(map[string]any)
	if runStep["command"] != "echo hi" {
		t.Errorf("step 6: plain run step mangled: %v", runStep)
	}

	// (h) idempotent — a second pass changes nothing (the nested plugin_input keys are
	// not step nodes, so they are never re-processed).
	again, err := MigrateGossVerbsToPlugin(dir, false)
	if err != nil {
		t.Fatalf("second pass error = %v", err)
	}
	if len(again) != 0 {
		t.Errorf("migration not idempotent — second pass rewrote %v", again)
	}
}

// TestMigrateGossVerbsToPlugin_NoVerbUntouched proves a config with no extracted
// goss-verb Op anywhere is left byte-for-byte unchanged — even when it carries a
// `service:`/`package:` step (the non-extracted verbs, whose fields must survive) and a
// box-level published `port:` field (a non-step `port:` that must NOT be rewritten).
func TestMigrateGossVerbsToPlugin_NoVerbUntouched(t *testing.T) {
	dir := t.TempDir()
	rootYML := "" +
		"version: 2026.172.0006\n" +
		"web:\n" +
		"    pod:\n" +
		"        image: web\n" +
		"    port: [\"8080:8080\"]\n" +
		"    plan:\n" +
		"        - check: \"a service is up\"\n" +
		"          service: \"jupyter\"\n" +
		"          running: true\n" +
		"        - run: \"do a thing\"\n" +
		"          command: \"echo hi\"\n"
	rootPath := filepath.Join(dir, "charly.yml")
	if err := os.WriteFile(rootPath, []byte(rootYML), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(rootPath)

	rewritten, err := MigrateGossVerbsToPlugin(dir, false)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if len(rewritten) != 0 {
		t.Errorf("a config with no extracted goss verb was rewritten: %v", rewritten)
	}
	after, _ := os.ReadFile(rootPath)
	if string(before) != string(after) {
		t.Errorf("file modified despite no extracted goss-verb keys:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}
