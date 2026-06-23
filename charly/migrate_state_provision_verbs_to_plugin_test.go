package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestMigrateStateProvisionVerbsToPlugin proves the state-provision-verb extraction across
// all four extracted verbs (unix_group, user, kernel-param, mount). Because a
// state-provision verb is DUAL-NATURED, BOTH a `check:` (assert) step AND a `run:` (act)
// step authoring it are CONVERTED to the generic plugin step (plugin: <verb> +
// plugin_input{<verb>, <companions>}) — the distinguishing behaviour versus the OBSERVE-only
// goss migrator, which strips a run: step's vestigial keys. A verb-less step kind
// (agent-check/include) has the vestigial keys STRIPPED. Each verb's companion fields MOVE
// into plugin_input. Comment-preserving + idempotent.
func TestMigrateStateProvisionVerbsToPlugin(t *testing.T) {
	dir := t.TempDir()
	rootYML := "" +
		"version: 2026.174.0300\n" +
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
		"          uid: 1000\n" +
		"          gid: 1000\n" +
		"          home: \"/home/deploy\"\n" +
		"          shell: \"/bin/bash\"\n" +
		"        - check: \"ip_forward is enabled\"\n" +
		"          kernel-param: \"net.ipv4.ip_forward\"\n" +
		"          value: \"1\"\n" +
		"        - run: \"mount the data volume\"\n" +
		"          mount: \"/mnt/data\"\n" +
		"          mount_source: \"/dev/sdb1\"\n" +
		"          filesystem: \"ext4\"\n" +
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
	if !ok || len(plan) != 7 {
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

	// (d) a user: step now CONVERTS (user is an extracted state-provision verb) — its
	//     uid/gid/home/shell companions ALL move into plugin_input.
	userStep := plan[3].(map[string]any)
	if userStep["plugin"] != "user" {
		t.Errorf("step 3: plugin: user not added, got %v: %v", userStep["plugin"], userStep)
	}
	for _, k := range []string{"user", "uid", "gid", "home", "shell"} {
		if _, has := userStep[k]; has {
			t.Errorf("step 3: bare %s: not removed (must move into plugin_input): %v", k, userStep)
		}
	}
	userPI := userStep["plugin_input"].(map[string]any)
	if userPI["user"] != "deploy" || userPI["uid"] != 1000 || userPI["gid"] != 1000 ||
		userPI["home"] != "/home/deploy" || userPI["shell"] != "/bin/bash" {
		t.Errorf("step 3: plugin_input = %v, want {user: deploy, uid: 1000, gid: 1000, home: /home/deploy, shell: /bin/bash}", userPI)
	}

	// (e) a kernel-param: step CONVERTS — value moves into plugin_input under the (hyphenated)
	//     kernel-param key.
	kpStep := plan[4].(map[string]any)
	if kpStep["plugin"] != "kernel-param" {
		t.Errorf("step 4: plugin: kernel-param not added, got %v: %v", kpStep["plugin"], kpStep)
	}
	if _, has := kpStep["kernel-param"]; has {
		t.Errorf("step 4: bare kernel-param: not removed: %v", kpStep)
	}
	kpPI := kpStep["plugin_input"].(map[string]any)
	if kpPI["kernel-param"] != "net.ipv4.ip_forward" || kpPI["value"] != "1" {
		t.Errorf("step 4: plugin_input = %v, want {kernel-param: net.ipv4.ip_forward, value: 1}", kpPI)
	}

	// (f) a run: mount: step CONVERTS — mount_source/filesystem move into plugin_input, the
	//     run: keyword preserved.
	mountStep := plan[5].(map[string]any)
	if mountStep["plugin"] != "mount" {
		t.Errorf("step 5: plugin: mount not added, got %v: %v", mountStep["plugin"], mountStep)
	}
	if mountStep["run"] != "mount the data volume" {
		t.Errorf("step 5: the run: keyword must be preserved: %v", mountStep)
	}
	for _, k := range []string{"mount", "mount_source", "filesystem"} {
		if _, has := mountStep[k]; has {
			t.Errorf("step 5: bare %s: not removed: %v", k, mountStep)
		}
	}
	mountPI := mountStep["plugin_input"].(map[string]any)
	if mountPI["mount"] != "/mnt/data" || mountPI["mount_source"] != "/dev/sdb1" || mountPI["filesystem"] != "ext4" {
		t.Errorf("step 5: plugin_input = %v, want {mount: /mnt/data, mount_source: /dev/sdb1, filesystem: ext4}", mountPI)
	}

	// (g) the plain run: command step is untouched.
	plainStep := plan[6].(map[string]any)
	if plainStep["command"] != "echo hi" {
		t.Errorf("step 6: plain run step mangled: %v", plainStep)
	}

	// (h) idempotent — a second pass changes nothing (the nested plugin_input keys are not
	//     step nodes, so they are never re-processed).
	again, err := MigrateStateProvisionVerbsToPlugin(dir, false)
	if err != nil {
		t.Fatalf("second pass error = %v", err)
	}
	if len(again) != 0 {
		t.Errorf("migration not idempotent — second pass rewrote %v", again)
	}
}

// TestMigrateStateProvisionVerbsToPlugin_NoVerbUntouched proves a config with no extracted
// state-provision-verb Op anywhere is left byte-for-byte unchanged — even when it carries a
// non-step `user:` field (an SSH deploy user) that shares a key name with the extracted verb.
func TestMigrateStateProvisionVerbsToPlugin_NoVerbUntouched(t *testing.T) {
	dir := t.TempDir()
	rootYML := "" +
		"version: 2026.174.0300\n" +
		"via-bastion:\n" +
		"    local:\n" +
		"        from: dev-workstation\n" +
		"        host: target.internal\n" +
		"        user: ops\n" + // a deploy field named user:, NOT a plan-step verb
		"sample:\n" +
		"    candy: {}\n" +
		"    plan:\n" +
		"        - check: \"the marker file exists\"\n" +
		"          file: \"/etc/marker\"\n" +
		"          exists: true\n" +
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
		t.Errorf("a config with no extracted state-provision verb was rewritten: %v", rewritten)
	}
	after, _ := os.ReadFile(rootPath)
	if string(before) != string(after) {
		t.Errorf("file modified despite no state-provision verb keys:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}
