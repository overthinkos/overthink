package migrate

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

// TestMigrateGossVerbsToPlugin_SecondWave proves the second extraction wave
// (http/interface/addr): a deterministic `check:` step with an inline verb + companion
// Op fields is CONVERTED to plugin: <verb> + plugin_input:, moving only the verb-EXCLUSIVE
// companions; the SHARED method/request_body and the GENERAL timeout STAY at step level
// (read off the step Op by the runner). A non-check step has the vestigial keys STRIPPED.
// Comment-preserving + idempotent.
func TestMigrateGossVerbsToPlugin_SecondWave(t *testing.T) {
	dir := t.TempDir()
	rootYML := "" +
		"version: 2026.172.0006\n" +
		"sample:\n" +
		"    candy: {}\n" +
		"    plan:\n" +
		"        - check: \"the api answers\"\n" +
		"          http: \"http://127.0.0.1:8080/health\"  # keep this comment\n" +
		"          status: 200\n" +
		"          body: [\"ok\"]\n" +
		"          header: [\"application/json\"]\n" +
		"          allow_insecure: true\n" +
		"          no_follow_redirects: true\n" +
		"          ca_file: \"/etc/ssl/ca.pem\"\n" +
		"          method: \"POST\"\n" +
		"          request_body: \"{}\"\n" +
		"          timeout: \"5s\"\n" +
		"        - check: \"eth0 exists\"\n" +
		"          interface: \"eth0\"\n" +
		"          mtu: 1500\n" +
		"          addrs: [\"10.0.0.1\"]\n" +
		"        - check: \"redis port reachable\"\n" +
		"          addr: \"127.0.0.1:6379\"\n" +
		"          reachable: true\n" +
		"        - agent-check: \"agent assesses the endpoint\"\n" +
		"          http: \"http://vestigial\"\n" +
		"          status: 500\n"
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
		t.Errorf("comment on http: was lost:\n%s", out)
	}

	var doc map[string]any
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("re-parse migrated YAML: %v", err)
	}
	plan, ok := doc["sample"].(map[string]any)["plan"].([]any)
	if !ok || len(plan) != 4 {
		t.Fatalf("plan shape wrong (len=%d): %v", len(plan), doc["sample"])
	}

	// (a) http check step → plugin: http + plugin_input{http, status, body, header,
	//     allow_insecure, no_follow_redirects, ca_file}; method/request_body/timeout STAY
	//     at step level (the runner reads them off the step Op).
	httpStep := plan[0].(map[string]any)
	if httpStep["plugin"] != "http" {
		t.Errorf("step 0: plugin: http not added, got %v", httpStep["plugin"])
	}
	if _, has := httpStep["http"]; has {
		t.Errorf("step 0: bare http: not removed: %v", httpStep)
	}
	httpPI := httpStep["plugin_input"].(map[string]any)
	for _, k := range []string{"http", "status", "body", "header", "allow_insecure", "no_follow_redirects", "ca_file"} {
		if _, has := httpPI[k]; !has {
			t.Errorf("step 0: plugin_input missing %q: %v", k, httpPI)
		}
	}
	if httpPI["http"] != "http://127.0.0.1:8080/health" || httpPI["status"] != 200 {
		t.Errorf("step 0: plugin_input = %v, want http+status moved", httpPI)
	}
	// SHARED/GENERAL modifiers must remain at step level, NOT in plugin_input.
	for _, k := range []string{"method", "request_body", "timeout"} {
		if _, has := httpStep[k]; !has {
			t.Errorf("step 0: shared modifier %q must stay at step level: %v", k, httpStep)
		}
		if _, has := httpPI[k]; has {
			t.Errorf("step 0: shared modifier %q must NOT move into plugin_input: %v", k, httpPI)
		}
	}

	// (b) interface check step → plugin: interface + plugin_input{interface, mtu, addrs}.
	ifaceStep := plan[1].(map[string]any)
	if ifaceStep["plugin"] != "interface" {
		t.Errorf("step 1: plugin: interface not added, got %v", ifaceStep["plugin"])
	}
	ifacePI := ifaceStep["plugin_input"].(map[string]any)
	if ifacePI["interface"] != "eth0" || ifacePI["mtu"] != 1500 {
		t.Errorf("step 1: plugin_input = %v, want {interface: eth0, mtu: 1500, addrs}", ifacePI)
	}
	if addrs, ok := ifacePI["addrs"].([]any); !ok || len(addrs) != 1 || addrs[0] != "10.0.0.1" {
		t.Errorf("step 1: plugin_input.addrs = %v, want [10.0.0.1]", ifacePI["addrs"])
	}

	// (c) addr check step → plugin: addr + plugin_input{addr, reachable}.
	addrStep := plan[2].(map[string]any)
	if addrStep["plugin"] != "addr" {
		t.Errorf("step 2: plugin: addr not added, got %v", addrStep["plugin"])
	}
	addrPI := addrStep["plugin_input"].(map[string]any)
	if addrPI["addr"] != "127.0.0.1:6379" || addrPI["reachable"] != true {
		t.Errorf("step 2: plugin_input = %v, want {addr: 127.0.0.1:6379, reachable: true}", addrPI)
	}

	// (d) the agent-check http step has http:/status: stripped, no plugin added.
	agentStep := plan[3].(map[string]any)
	if _, has := agentStep["http"]; has {
		t.Errorf("step 3: vestigial http: not stripped: %v", agentStep)
	}
	if _, has := agentStep["status"]; has {
		t.Errorf("step 3: vestigial status: not stripped: %v", agentStep)
	}
	if _, has := agentStep["plugin"]; has {
		t.Errorf("step 3: plugin: wrongly added to a non-check step: %v", agentStep)
	}

	// (e) idempotent — a second pass changes nothing.
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
