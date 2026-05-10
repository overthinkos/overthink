package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestHasLegacyImagesKey(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want bool
	}{
		{
			name: "legacy images-only",
			yaml: "images:\n  immich:\n    bind_mounts: []\n",
			want: true,
		},
		{
			name: "modern deploy-only",
			yaml: "version: 4\ndeploy:\n  immich:\n    target: pod\n",
			want: false,
		},
		{
			name: "post-migration with provides + deploy",
			yaml: "provides:\n  env: []\ndeploy:\n  immich:\n    target: pod\n",
			want: false,
		},
		{
			name: "both keys present (mid-migration; treat as already-modern)",
			yaml: "images:\n  old: {}\ndeploy:\n  new:\n    target: pod\n",
			want: false,
		},
		{
			name: "empty file",
			yaml: "",
			want: false,
		},
		{
			name: "comment-only file",
			yaml: "# nothing here\n",
			want: false,
		},
		{
			name: "ignored nested images key inside deploy entry",
			yaml: "deploy:\n  foo:\n    target: pod\n    eval:\n      - {images: irrelevant}\n",
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasLegacyImagesKey([]byte(tc.yaml)); got != tc.want {
				t.Errorf("hasLegacyImagesKey(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// TestMigrateLocalDeploy_FullExample exercises the full transformation
// against a realistic legacy deploy.yml — the actual shape on disk before
// the May-2026 cutover. Asserts every field-level transform.
func TestMigrateLocalDeploy_FullExample(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	legacy := `images:
    immich:
        workspace: /home/user/project
        tunnel:
            provider: cloudflare
            public:
                2283: im.example.org
        dns: im.example.org
        bind_mounts:
            - name: library
              path: ~/.immich/library
              encrypted: true
            - name: cache
              path: ~/.immich/cache
              encrypted: true
            - name: pgdata
              path: ~/.postgresql/data
              encrypted: true
            - name: shared
              path: /opt/shared
        port:
            - 2283:2283
        env_file: /home/user/project/.env
        security:
            devices:
                - /dev/kvm
        network: ov
`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatalf("writing legacy fixture: %v", err)
	}

	changed, summary, err := MigrateLocalDeploy(path, false)
	if err != nil {
		t.Fatalf("MigrateLocalDeploy: %v", err)
	}
	if !changed {
		t.Fatal("changed=false on legacy file")
	}
	if len(summary) == 0 {
		t.Error("summary unexpectedly empty")
	}

	// Decode the rewritten file and assert structure.
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading rewritten file: %v", err)
	}
	var got map[string]any
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatalf("decoding rewritten yaml: %v", err)
	}

	if v, ok := got["version"].(int); !ok || v != 4 {
		t.Errorf("version: got %v, want 4", got["version"])
	}
	if _, hasImages := got["images"]; hasImages {
		t.Error("rewritten file still has top-level `images:` key")
	}
	deploy, ok := got["deploy"].(map[string]any)
	if !ok {
		t.Fatalf("rewritten file has no top-level `deploy:` map: got %T", got["deploy"])
	}
	immich, ok := deploy["immich"].(map[string]any)
	if !ok {
		t.Fatalf("deploy.immich missing or wrong type: got %T", deploy["immich"])
	}

	if immich["target"] != "pod" {
		t.Errorf("target: got %v, want pod", immich["target"])
	}
	if _, has := immich["bind_mounts"]; has {
		t.Error("entry still has `bind_mounts:` after migration")
	}
	if _, has := immich["workspace"]; has {
		t.Error("entry still has `workspace:` after migration")
	}
	volumes, ok := immich["volumes"].([]any)
	if !ok {
		t.Fatalf("volumes missing or wrong type: got %T", immich["volumes"])
	}
	// Expect 3 encrypted + 1 bind + 1 workspace = 5
	if len(volumes) != 5 {
		t.Errorf("volumes len = %d, want 5; volumes = %v", len(volumes), volumes)
	}

	// Index volumes by name for assertion clarity.
	byName := map[string]map[string]any{}
	for _, v := range volumes {
		vm, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("volume entry wrong type: got %T", v)
		}
		byName[vm["name"].(string)] = vm
	}

	for _, name := range []string{"library", "cache", "pgdata"} {
		v := byName[name]
		if v == nil {
			t.Errorf("volume %q missing", name)
			continue
		}
		if v["type"] != "encrypted" {
			t.Errorf("volume %q type = %v, want encrypted", name, v["type"])
		}
		if _, has := v["path"]; has {
			t.Errorf("volume %q (encrypted) should not retain path:; got %v", name, v["path"])
		}
	}

	shared := byName["shared"]
	if shared == nil {
		t.Error("plain bind_mount `shared` missing after migration")
	} else {
		if shared["type"] != "bind" {
			t.Errorf("shared.type = %v, want bind", shared["type"])
		}
		if shared["path"] != "/opt/shared" {
			t.Errorf("shared.path = %v, want /opt/shared", shared["path"])
		}
	}

	ws := byName["workspace"]
	if ws == nil {
		t.Error("workspace volume missing after migration")
	} else {
		if ws["type"] != "bind" {
			t.Errorf("workspace.type = %v, want bind", ws["type"])
		}
		if ws["host"] != "/home/user/project" {
			t.Errorf("workspace.host = %v, want /home/user/project", ws["host"])
		}
		if ws["path"] != "/workspace" {
			t.Errorf("workspace.path = %v, want /workspace", ws["path"])
		}
	}

	// Pass-through fields preserved.
	if immich["dns"] != "im.example.org" {
		t.Errorf("dns lost: got %v", immich["dns"])
	}
	if immich["network"] != "ov" {
		t.Errorf("network lost: got %v", immich["network"])
	}
	if immich["env_file"] != "/home/user/project/.env" {
		t.Errorf("env_file lost: got %v", immich["env_file"])
	}
	if _, ok := immich["tunnel"].(map[string]any); !ok {
		t.Errorf("tunnel block lost")
	}
	if _, ok := immich["security"].(map[string]any); !ok {
		t.Errorf("security block lost")
	}

	// Backup file exists.
	matches, _ := filepath.Glob(path + ".bak.*")
	if len(matches) != 1 {
		t.Errorf("expected exactly 1 backup file, got %d", len(matches))
	}

	// Rewritten file must now LOAD via LoadDeployConfig without error.
	// Use a per-test XDG_CONFIG_HOME so we don't read the real user file.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg"))
	if err := os.MkdirAll(filepath.Join(dir, "xdg", "ov"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, filepath.Join(dir, "xdg", "ov", "deploy.yml")); err != nil {
		t.Fatal(err)
	}
	dc, err := LoadDeployConfig()
	if err != nil {
		t.Fatalf("LoadDeployConfig on migrated file: %v", err)
	}
	if dc == nil || dc.Deploy["immich"].Target != "pod" {
		t.Errorf("LoadDeployConfig returned wrong shape: %+v", dc)
	}
}

// TestMigrateLocalDeploy_Idempotent asserts that a v4 file is left
// untouched (changed=false, no backup).
func TestMigrateLocalDeploy_Idempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	modern := `version: 4
deploy:
    immich:
        target: pod
        volume:
            - name: library
              type: encrypted
`
	if err := os.WriteFile(path, []byte(modern), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, _, err := MigrateLocalDeploy(path, false)
	if err != nil {
		t.Fatalf("MigrateLocalDeploy on v4 file: %v", err)
	}
	if changed {
		t.Error("changed=true on v4 file; want false")
	}
	matches, _ := filepath.Glob(path + ".bak.*")
	if len(matches) != 0 {
		t.Errorf("backup file unexpectedly created on v4 file: %v", matches)
	}
}

// TestMigrateLocalDeploy_DryRun exercises --dry-run: returns changed=true
// and a summary, but the file is untouched and no backup is written.
func TestMigrateLocalDeploy_DryRun(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	legacy := "images:\n  foo:\n    bind_mounts:\n      - {name: data, encrypted: true}\n"
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)

	changed, summary, err := MigrateLocalDeploy(path, true)
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if !changed {
		t.Error("changed=false on dry-run of legacy file")
	}
	if len(summary) == 0 {
		t.Error("dry-run summary empty")
	}

	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Error("dry-run modified the file on disk")
	}
	matches, _ := filepath.Glob(path + ".bak.*")
	if len(matches) != 0 {
		t.Errorf("dry-run created a backup: %v", matches)
	}
}

// TestLoadDeployConfig_LegacySchemaErrors exercises the load-time guard:
// a deploy.yml with `images:` at top level errors with a remediation hint
// pointing at `ov migrate local-deploy`.
func TestLoadDeployConfig_LegacySchemaErrors(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "xdg")
	if err := os.MkdirAll(filepath.Join(configDir, "ov"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", configDir)
	deployPath := filepath.Join(configDir, "ov", "deploy.yml")
	legacy := "images:\n  immich:\n    bind_mounts: []\n"
	if err := os.WriteFile(deployPath, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadDeployConfig()
	if err == nil {
		t.Fatal("LoadDeployConfig accepted legacy schema; want error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "legacy top-level `images:`") {
		t.Errorf("error message missing legacy-key hint: %s", msg)
	}
	if !strings.Contains(msg, "ov migrate local-deploy") {
		t.Errorf("error message missing remediation command: %s", msg)
	}
}
