package main

// migrate_runtime_config_test.go — the CORE runtime-config loader's kdbx-residual
// rejection gate (relocated from the migrate chain in C13a; LoadRuntimeConfig +
// RuntimeConfigPath are package-main, so these tests stay in core). The migrator
// that strips the residuals (MigrateDropKdbx) lives in candy/plugin-migrate and is
// tested there.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRuntimeConfig_RejectsKdbxResiduals(t *testing.T) {
	cases := map[string]string{
		"secret_backend kdbx": "secret_backend: kdbx\n",
		"secrets_kdbx_path":   "secrets_kdbx_path: /x.kdbx\n",
		"kdbx_cache":          "kdbx_cache: true\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			RuntimeConfigPath = func() (string, error) { return filepath.Join(dir, "config.yml"), nil }
			defer func() { RuntimeConfigPath = defaultRuntimeConfigPath }()
			if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadRuntimeConfig()
			if err == nil {
				t.Fatalf("expected hard error for residual %q, got nil", name)
			}
			if !strings.Contains(err.Error(), "charly migrate") {
				t.Errorf("error should point at the migration command, got: %v", err)
			}
		})
	}
}

func TestLoadRuntimeConfig_CleanConfigOK(t *testing.T) {
	dir := t.TempDir()
	RuntimeConfigPath = func() (string, error) { return filepath.Join(dir, "config.yml"), nil }
	defer func() { RuntimeConfigPath = defaultRuntimeConfigPath }()
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte("secret_backend: keyring\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRuntimeConfig(); err != nil {
		t.Fatalf("clean config should load, got: %v", err)
	}
}
