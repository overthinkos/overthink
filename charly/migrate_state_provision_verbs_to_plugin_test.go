package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestMigrateStateProvisionVerbsToPlugin proves the state-provision-verb extraction across
// all five extracted verbs (unix_group, user, kernel-param, mount, command). Because a
// state-provision verb carries both a check and an act, BOTH a `check:` (assert) step AND a
// `run:` (act) step authoring it are CONVERTED to the generic plugin step (plugin: <verb> +
// plugin_input{<verb>, <companions>}) — the distinguishing behaviour versus the OBSERVE-only
// goss migrator, which strips a run: step's vestigial keys. A verb-less step kind
// (agent-check/include) has the vestigial keys STRIPPED. Each verb's companion fields MOVE
// into plugin_input — except `command`, the FIELD-SPLIT case (only the EXCLUSIVE
// background/from_host/in_container move; the matchers exit_status/stdout/stderr STAY at
// step level), which converts ONLY when no charly-verb is present (else `command` is a
// wl/libvirt argv modifier). Comment-preserving + idempotent.
func TestMigrateStateProvisionVerbsToPlugin(t *testing.T) {
	dir := t.TempDir()
	rootYML := "" +
		"version: 2026.174.0500\n" +
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
		"        - run: \"install the package\"\n" +
		"          command: \"dnf install -y foo\"\n" +
		"          from_host: false\n" +
		"        - check: \"redis answers ping\"\n" +
		"          command: \"redis-cli ping\"  # field-split: matchers stay\n" +
		"          in_container: false\n" +
		"          stdout: PONG\n" +
		"          exit_status: 0\n" +
		"        - check: \"the guest reports its kernel\"\n" +
		"          libvirt: \"guest/exec\"\n" +
		"          command: \"uname -r\"\n"
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
	if !ok || len(plan) != 9 {
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

	// (g) a run: command step CONVERTS — command + the EXCLUSIVE from_host move into
	//     plugin_input (the install-task act timeline).
	cmdRun := plan[6].(map[string]any)
	if cmdRun["plugin"] != "command" {
		t.Errorf("step 6: plugin: command not added, got %v: %v", cmdRun["plugin"], cmdRun)
	}
	if cmdRun["run"] != "install the package" {
		t.Errorf("step 6: the run: keyword must be preserved: %v", cmdRun)
	}
	for _, k := range []string{"command", "from_host"} {
		if _, has := cmdRun[k]; has {
			t.Errorf("step 6: bare %s: not removed (must move into plugin_input): %v", k, cmdRun)
		}
	}
	cmdRunPI := cmdRun["plugin_input"].(map[string]any)
	if cmdRunPI["command"] != "dnf install -y foo" || cmdRunPI["from_host"] != false {
		t.Errorf("step 6: plugin_input = %v, want {command: dnf install -y foo, from_host: false}", cmdRunPI)
	}

	// (h) a check: command step is the FIELD-SPLIT case — command + the EXCLUSIVE
	//     in_container move into plugin_input, but the MATCHERS stdout/exit_status STAY at
	//     step level (#Op, shared via matchAll). Comment on the command line is preserved.
	cmdCheck := plan[7].(map[string]any)
	if cmdCheck["plugin"] != "command" {
		t.Errorf("step 7: plugin: command not added, got %v: %v", cmdCheck["plugin"], cmdCheck)
	}
	cmdCheckPI := cmdCheck["plugin_input"].(map[string]any)
	if cmdCheckPI["command"] != "redis-cli ping" || cmdCheckPI["in_container"] != false {
		t.Errorf("step 7: plugin_input = %v, want {command: redis-cli ping, in_container: false}", cmdCheckPI)
	}
	if _, has := cmdCheckPI["stdout"]; has {
		t.Errorf("step 7: stdout MUST stay at step level (matcher), not move into plugin_input: %v", cmdCheckPI)
	}
	if cmdCheck["stdout"] != "PONG" {
		t.Errorf("step 7: stdout matcher must stay at step level, got %v", cmdCheck["stdout"])
	}
	if cmdCheck["exit_status"] != 0 {
		t.Errorf("step 7: exit_status matcher must stay at step level, got %v", cmdCheck["exit_status"])
	}

	// (i) a libvirt: guest/exec step with a command MODIFIER is NOT converted — `command`
	//     is the libvirt argv (a charly-verb is present), so it stays in place.
	libvirtStep := plan[8].(map[string]any)
	if _, has := libvirtStep["plugin"]; has {
		t.Errorf("step 8: a libvirt step with a command modifier must NOT convert: %v", libvirtStep)
	}
	if libvirtStep["libvirt"] != "guest/exec" || libvirtStep["command"] != "uname -r" {
		t.Errorf("step 8: libvirt verb + command modifier must be untouched: %v", libvirtStep)
	}
	if !strings.Contains(string(out), "field-split: matchers stay") {
		t.Errorf("comment on the command check step was lost:\n%s", out)
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
		"version: 2026.174.0500\n" +
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
		"        - run: \"make a dir\"\n" + // mkdir is NOT an extracted verb → no rewrite
		"          mkdir: \"/opt/app\"\n"
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
