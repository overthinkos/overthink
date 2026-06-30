package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// mergeHostStateDir recovers the empty-destination orphan: an EMPTY `to`
// receives the whole real config and `from` disappears; a `to` that already
// holds config is never clobbered and `from` survives.
func TestMergeHostStateDir(t *testing.T) {
	t.Run("from absent → no-op", func(t *testing.T) {
		base := t.TempDir()
		changed, err := mergeHostStateDir(filepath.Join(base, "missing"), filepath.Join(base, "to"), false)
		if err != nil || changed {
			t.Fatalf("absent from: changed=%v err=%v, want false/nil", changed, err)
		}
	})

	t.Run("to absent → whole-tree rename", func(t *testing.T) {
		base := t.TempDir()
		from := filepath.Join(base, "ov")
		writeTreeFile(t, filepath.Join(from, "deploy.yml"), "real\n")
		writeTreeFile(t, filepath.Join(from, "env.d", "x.env"), "X=1\n") // sub-dir preserved
		to := filepath.Join(base, "charly")
		changed, err := mergeHostStateDir(from, to, false)
		if err != nil || !changed {
			t.Fatalf("changed=%v err=%v, want true/nil", changed, err)
		}
		if !fileExists(filepath.Join(to, "deploy.yml")) || !fileExists(filepath.Join(to, "env.d", "x.env")) {
			t.Errorf("whole tree not moved into %s", to)
		}
		if fileExists(from) {
			t.Errorf("source %s should be gone after rename", from)
		}
	})

	t.Run("to EMPTY → real config moved in, from removed", func(t *testing.T) {
		base := t.TempDir()
		from := filepath.Join(base, "ov")
		writeTreeFile(t, filepath.Join(from, "deploy.yml"), "real-deploy\n")
		writeTreeFile(t, filepath.Join(from, "config.yml"), "real-config\n")
		to := filepath.Join(base, "charly")
		if err := os.MkdirAll(to, 0o755); err != nil { // pre-exists EMPTY (the orphan trigger)
			t.Fatal(err)
		}
		changed, err := mergeHostStateDir(from, to, false)
		if err != nil || !changed {
			t.Fatalf("changed=%v err=%v, want true/nil", changed, err)
		}
		if b, _ := os.ReadFile(filepath.Join(to, "deploy.yml")); string(b) != "real-deploy\n" {
			t.Errorf("real deploy.yml not recovered into %s: %q", to, b)
		}
		if fileExists(from) {
			t.Errorf("emptied source %s should be removed", from)
		}
	})

	t.Run("to NON-EMPTY → conflicting kept, non-conflicting merged, from survives", func(t *testing.T) {
		base := t.TempDir()
		from := filepath.Join(base, "ov")
		writeTreeFile(t, filepath.Join(from, "deploy.yml"), "OV-deploy\n") // conflicts
		writeTreeFile(t, filepath.Join(from, "config.yml"), "OV-config\n") // unique
		to := filepath.Join(base, "charly")
		writeTreeFile(t, filepath.Join(to, "deploy.yml"), "CHARLY-deploy\n") // pre-existing real config

		changed, err := mergeHostStateDir(from, to, false)
		if err != nil || !changed {
			t.Fatalf("changed=%v err=%v, want true/nil", changed, err)
		}
		// The pre-existing charly deploy.yml is NEVER clobbered.
		if b, _ := os.ReadFile(filepath.Join(to, "deploy.yml")); string(b) != "CHARLY-deploy\n" {
			t.Errorf("pre-existing deploy.yml was clobbered: %q", b)
		}
		// The non-conflicting config.yml moved in.
		if b, _ := os.ReadFile(filepath.Join(to, "config.yml")); string(b) != "OV-config\n" {
			t.Errorf("non-conflicting config.yml not merged: %q", b)
		}
		// from survives carrying ONLY the conflicting entry (a stale backup).
		if !fileExists(filepath.Join(from, "deploy.yml")) {
			t.Errorf("conflicting source deploy.yml should remain as a stale backup")
		}
		if fileExists(filepath.Join(from, "config.yml")) {
			t.Errorf("merged config.yml should have left the source")
		}
	})
}

// End-to-end: ~/.config/ov holds the real (legacy) overlay AND ~/.config/charly
// pre-exists EMPTY. After the host-relocation + host-charly-yml rename +
// node-form conversion + the install-strategy-key rename, the real config lands
// at ~/.config/charly/charly.yml in node-form (no top-level `deploy:` map, the
// vm_state install strategy carried over as charly_install_strategy) and
// ~/.config/ov is gone — `charly migrate` recovers the host with NO manual dir
// removal (the orphan that previously left the chain operating on a phantom path).
func TestRelocateRecoversEmptyCharlyDirOrphan(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	ovDir := filepath.Join(home, ".config", "ov")
	charlyDir := filepath.Join(home, ".config", "charly")
	// The real legacy overlay at ~/.config/ov/deploy.yml.
	writeTreeFile(t, filepath.Join(ovDir, "deploy.yml"), `deploy:
    vm:arch:
        vm: arch
        vm_state:
            ssh_port: 2224
            ov_install_strategy: auto
`)
	// ~/.config/charly pre-exists EMPTY — the bug trigger (rename was skipped).
	if err := os.MkdirAll(charlyDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := &MigrateContext{Dir: t.TempDir(), HostDeployPath: filepath.Join(ovDir, "deploy.yml")}

	// Run the relevant host-overlay chain steps in registry order.
	if _, err := relocateHostStateForCharly(ctx); err != nil {
		t.Fatalf("relocate: %v", err)
	}
	if _, err := MigrateHostCharlyYml(ctx); err != nil {
		t.Fatalf("host-charly-yml: %v", err)
	}
	if _, err := migrateHostOverlayDoc(ctx, migrateUnifiedNodeDoc); err != nil {
		t.Fatalf("unified-node host overlay: %v", err)
	}
	if _, err := MigrateInstallStrategyKey(ctx); err != nil {
		t.Fatalf("install-strategy-key: %v", err)
	}

	finalPath := filepath.Join(charlyDir, "charly.yml")
	if !fileExists(finalPath) {
		t.Fatalf("real config not landed at runtime path %s", finalPath)
	}
	if fileExists(ovDir) {
		t.Errorf("orphan source dir %s should be gone (emptied)", ovDir)
	}
	if ctx.HostDeployPath != finalPath {
		t.Errorf("ctx.HostDeployPath = %q, want the runtime path %q", ctx.HostDeployPath, finalPath)
	}

	data, _ := os.ReadFile(finalPath)
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("final overlay unparseable: %v\n%s", err, data)
	}
	root := rootMappingNode(&doc)
	if findMappingValue(root, "deploy") != nil {
		t.Errorf("legacy top-level deploy: map should be converted to node-form:\n%s", data)
	}
	if findMappingValue(root, "vm:arch") == nil {
		t.Errorf("entity node lost in conversion:\n%s", data)
	}
	body := string(data)
	if strings.Contains(body, "ov_install_strategy") {
		t.Errorf("legacy vm_state key survived:\n%s", body)
	}
	if !strings.Contains(body, "charly_install_strategy") {
		t.Errorf("install strategy not carried over as charly_install_strategy:\n%s", body)
	}
}

// writeTreeFile writes body to path, creating parent dirs on demand (the
// package's existing mustWrite does not mkdir).
func writeTreeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
