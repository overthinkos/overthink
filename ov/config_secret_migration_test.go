package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// withDeployConfigTempPath redirects DeployConfigPath to a file inside a
// temp directory and returns the resolved path. Combine with
// withIsolatedCredentialStore (from secrets_test.go) to fully isolate both
// the credential store and the deploy.yml file.
func withDeployConfigTempPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	orig := DeployConfigPath
	t.Cleanup(func() { DeployConfigPath = orig })
	DeployConfigPath = func() (string, error) {
		return path, nil
	}
	return path
}

// seedDeployConfig writes an initial deploy.yml with a single image entry
// containing the given env list. Returns the resolved path so tests can
// verify backup and rewrite behavior.
func seedDeployConfig(t *testing.T, image, instance string, env []string) {
	t.Helper()
	dc := &DeployConfig{
		Images: map[string]DeployImageConfig{
			deployKey(image, instance): {
				Env: env,
			},
		},
	}
	if err := SaveDeployConfig(dc); err != nil {
		t.Fatalf("seedDeployConfig: %v", err)
	}
}

// TestMigratePlaintextEnvSecretsNoOpOnCleanDeploy — the helper must be a
// zero-mutation no-op when deploy.yml has no entries matching the image's
// secret declarations. Nothing happens, no backup file is written.
func TestMigratePlaintextEnvSecretsNoOpOnCleanDeploy(t *testing.T) {
	deployPath := withDeployConfigTempPath(t)
	withIsolatedCredentialStore(t)

	seedDeployConfig(t, "openwebui", "", []string{"TEST_OV_CFG_URL=http://example"})

	dc, err := LoadDeployConfig()
	if err != nil {
		t.Fatalf("LoadDeployConfig: %v", err)
	}
	meta := &ImageMetadata{
		SecretAccepts: []EnvDependency{
			{Name: "TEST_OV_CRED_ROUTEA", Description: "not in deploy.yml"},
		},
	}

	migrated, err := MigratePlaintextEnvSecrets(dc, meta, "openwebui", "")
	if err != nil {
		t.Fatalf("MigratePlaintextEnvSecrets: %v", err)
	}
	if migrated != 0 {
		t.Errorf("migrated = %d, want 0", migrated)
	}

	// No backup file should exist after a no-op.
	matches, _ := filepath.Glob(deployPath + ".bak.*")
	if len(matches) != 0 {
		t.Errorf("unexpected backup files after no-op: %v", matches)
	}

	// deploy.yml should be unchanged.
	dc2, _ := LoadDeployConfig()
	entry := dc2.Images[deployKey("openwebui", "")]
	if !reflect.DeepEqual(entry.Env, []string{"TEST_OV_CFG_URL=http://example"}) {
		t.Errorf("deploy.yml env mutated after no-op: %+v", entry.Env)
	}
}

// TestMigratePlaintextEnvSecretsHappyPath — a deploy.yml entry containing a
// plaintext credential whose name is now declared on the image gets moved
// into the credential store. The original entry is removed from dc.Env,
// unrelated plaintext entries stay put, a backup file is written, and the
// cleaned deploy.yml is persisted.
func TestMigratePlaintextEnvSecretsHappyPath(t *testing.T) {
	deployPath := withDeployConfigTempPath(t)
	store := withIsolatedCredentialStore(t)

	seedDeployConfig(t, "openwebui", "", []string{
		"TEST_OV_CFG_URL=http://example",   // plaintext config, must stay
		"TEST_OV_CRED_ROUTEA=legacy-value", // credential, must migrate
		"TEST_OV_CFG_MODE=debug",           // plaintext config, must stay
	})

	dc, _ := LoadDeployConfig()
	meta := &ImageMetadata{
		SecretAccepts: []EnvDependency{
			{Name: "TEST_OV_CRED_ROUTEA", Description: "credential-backed", Key: "ov/api-key/routea"},
		},
	}

	migrated, err := MigratePlaintextEnvSecrets(dc, meta, "openwebui", "")
	if err != nil {
		t.Fatalf("MigratePlaintextEnvSecrets: %v", err)
	}
	if migrated != 1 {
		t.Errorf("migrated = %d, want 1", migrated)
	}

	// The credential store should now hold the migrated value at the
	// layer-declared (service, key) path.
	got, err := store.Get("ov/api-key", "routea")
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if got != "legacy-value" {
		t.Errorf("migrated value mismatch (source=store)")
	}

	// deploy.yml should have the two plaintext entries but not the
	// credential entry.
	dc2, _ := LoadDeployConfig()
	entry := dc2.Images[deployKey("openwebui", "")]
	want := []string{"TEST_OV_CFG_URL=http://example", "TEST_OV_CFG_MODE=debug"}
	if !reflect.DeepEqual(entry.Env, want) {
		t.Errorf("cleaned env = %+v\nwant %+v", entry.Env, want)
	}

	// A backup file should exist at deploy.yml.bak.<ts> and contain the
	// pre-migration content (the credential line is still there).
	matches, _ := filepath.Glob(deployPath + ".bak.*")
	if len(matches) != 1 {
		t.Fatalf("expected exactly 1 backup, got %d: %v", len(matches), matches)
	}
	backupBytes, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("reading backup: %v", err)
	}
	if !strings.Contains(string(backupBytes), "TEST_OV_CRED_ROUTEA=legacy-value") {
		t.Errorf("backup does not contain the pre-migration credential line")
	}
}

// TestMigratePlaintextEnvSecretsIdempotent — running the migration twice
// against the same initial state migrates exactly once; the second call is a
// clean no-op and does not write a second backup.
func TestMigratePlaintextEnvSecretsIdempotent(t *testing.T) {
	deployPath := withDeployConfigTempPath(t)
	withIsolatedCredentialStore(t)

	seedDeployConfig(t, "openwebui", "", []string{"TEST_OV_CRED_ROUTEA=once"})

	meta := &ImageMetadata{
		SecretAccepts: []EnvDependency{
			{Name: "TEST_OV_CRED_ROUTEA", Description: "credential"},
		},
	}

	dc, _ := LoadDeployConfig()
	if n, err := MigratePlaintextEnvSecrets(dc, meta, "openwebui", ""); err != nil || n != 1 {
		t.Fatalf("first call: n=%d err=%v, want n=1 nil", n, err)
	}

	dc, _ = LoadDeployConfig()
	if n, err := MigratePlaintextEnvSecrets(dc, meta, "openwebui", ""); err != nil || n != 0 {
		t.Errorf("second call: n=%d err=%v, want n=0 nil (idempotent)", n, err)
	}

	matches, _ := filepath.Glob(deployPath + ".bak.*")
	if len(matches) != 1 {
		t.Errorf("idempotent run should leave exactly 1 backup, got %d", len(matches))
	}
}

// TestMigratePlaintextEnvSecretsNilDeploy — safe on nil DeployConfig.
func TestMigratePlaintextEnvSecretsNilDeploy(t *testing.T) {
	withDeployConfigTempPath(t)
	withIsolatedCredentialStore(t)

	meta := &ImageMetadata{
		SecretAccepts: []EnvDependency{{Name: "TEST_OV_CRED_ROUTEA", Description: "x"}},
	}
	n, err := MigratePlaintextEnvSecrets(nil, meta, "openwebui", "")
	if err != nil || n != 0 {
		t.Errorf("nil dc: n=%d err=%v, want n=0 nil", n, err)
	}
}

// TestMigratePlaintextEnvSecretsNilMeta — safe on nil metadata.
func TestMigratePlaintextEnvSecretsNilMeta(t *testing.T) {
	withDeployConfigTempPath(t)
	withIsolatedCredentialStore(t)
	seedDeployConfig(t, "openwebui", "", []string{"X=1"})
	dc, _ := LoadDeployConfig()

	n, err := MigratePlaintextEnvSecrets(dc, nil, "openwebui", "")
	if err != nil || n != 0 {
		t.Errorf("nil meta: n=%d err=%v, want n=0 nil", n, err)
	}
}

// TestMigratePlaintextEnvSecretsInstanceScoped — migration applies only to
// the specified instance's deploy entry; other instances of the same base
// image are untouched.
func TestMigratePlaintextEnvSecretsInstanceScoped(t *testing.T) {
	withDeployConfigTempPath(t)
	withIsolatedCredentialStore(t)

	dc := &DeployConfig{Images: map[string]DeployImageConfig{
		"openwebui":      {Env: []string{"TEST_OV_CRED_ROUTEA=base-value"}},
		"openwebui/test": {Env: []string{"TEST_OV_CRED_ROUTEA=test-value"}},
	}}
	if err := SaveDeployConfig(dc); err != nil {
		t.Fatalf("save seed: %v", err)
	}

	dc, _ = LoadDeployConfig()
	meta := &ImageMetadata{
		SecretAccepts: []EnvDependency{{Name: "TEST_OV_CRED_ROUTEA", Description: "cred"}},
	}
	n, err := MigratePlaintextEnvSecrets(dc, meta, "openwebui", "test")
	if err != nil || n != 1 {
		t.Fatalf("migrate test instance: n=%d err=%v", n, err)
	}

	dc2, _ := LoadDeployConfig()

	if got := dc2.Images["openwebui/test"].Env; len(got) != 0 {
		t.Errorf("test instance env = %+v, want empty", got)
	}
	base := dc2.Images["openwebui"]
	if !reflect.DeepEqual(base.Env, []string{"TEST_OV_CRED_ROUTEA=base-value"}) {
		t.Errorf("base instance env mutated: %+v", base.Env)
	}
}

// TestScrubSecretCLIEnvHappyPath — a CLI -e NAME=VAL where NAME is declared
// as secret_accepts is moved to the credential store and removed from the
// returned slice. Plain env_accepts -e entries pass through unchanged.
func TestScrubSecretCLIEnvHappyPath(t *testing.T) {
	store := withIsolatedCredentialStore(t)

	meta := &ImageMetadata{
		SecretAccepts: []EnvDependency{
			{Name: "TEST_OV_CRED_ROUTEA", Description: "cred", Key: "ov/api-key/routea"},
		},
	}
	cliEnv := []string{
		"TEST_OV_CFG_URL=http://example",     // plaintext, passes through
		"TEST_OV_CRED_ROUTEA=imported-via-e", // credential, gets stripped
		"TEST_OV_CFG_MODE=debug",             // plaintext, passes through
	}

	cleaned, imported, err := scrubSecretCLIEnv(cliEnv, meta)
	if err != nil {
		t.Fatalf("scrubSecretCLIEnv: %v", err)
	}
	if imported != 1 {
		t.Errorf("imported = %d, want 1", imported)
	}

	want := []string{"TEST_OV_CFG_URL=http://example", "TEST_OV_CFG_MODE=debug"}
	if !reflect.DeepEqual(cleaned, want) {
		t.Errorf("cleaned = %+v\nwant %+v", cleaned, want)
	}

	got, err := store.Get("ov/api-key", "routea")
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if got != "imported-via-e" {
		t.Errorf("credential store value was not set correctly")
	}
}

// TestScrubSecretCLIEnvNoMatches — when no -e flags match the image's
// declared secrets, the input slice passes through unchanged and imported is
// zero.
func TestScrubSecretCLIEnvNoMatches(t *testing.T) {
	withIsolatedCredentialStore(t)

	meta := &ImageMetadata{
		SecretAccepts: []EnvDependency{
			{Name: "TEST_OV_CRED_ROUTEA", Description: "cred"},
		},
	}
	cliEnv := []string{"UNRELATED=value", "ALSO_UNRELATED=other"}

	cleaned, imported, err := scrubSecretCLIEnv(cliEnv, meta)
	if err != nil {
		t.Fatalf("scrubSecretCLIEnv: %v", err)
	}
	if imported != 0 {
		t.Errorf("imported = %d, want 0", imported)
	}
	if !reflect.DeepEqual(cleaned, cliEnv) {
		t.Errorf("cleaned should equal input for no-match case")
	}
}

// TestScrubSecretCLIEnvEmpty — safe on empty input and nil metadata.
func TestScrubSecretCLIEnvEmpty(t *testing.T) {
	withIsolatedCredentialStore(t)

	cleaned, imported, err := scrubSecretCLIEnv(nil, nil)
	if err != nil || imported != 0 || cleaned != nil {
		t.Errorf("empty case: cleaned=%+v imported=%d err=%v", cleaned, imported, err)
	}

	cleaned, imported, err = scrubSecretCLIEnv([]string{"A=1"}, nil)
	if err != nil || imported != 0 || !reflect.DeepEqual(cleaned, []string{"A=1"}) {
		t.Errorf("nil meta: cleaned=%+v imported=%d err=%v", cleaned, imported, err)
	}
}

// TestScrubSecretCLIEnvMalformedEntry — a -e entry with no '=' is passed
// through unchanged (caller's responsibility to validate shape). Ensures
// scrubSecretCLIEnv does not drop invalid input.
func TestScrubSecretCLIEnvMalformedEntry(t *testing.T) {
	withIsolatedCredentialStore(t)
	meta := &ImageMetadata{
		SecretAccepts: []EnvDependency{{Name: "TEST_OV_CRED_ROUTEA", Description: "cred"}},
	}
	in := []string{"BARE_NAME_NO_EQUALS", "TEST_OV_CRED_ROUTEA=val"}
	out, imported, err := scrubSecretCLIEnv(in, meta)
	if err != nil {
		t.Fatal(err)
	}
	if imported != 1 {
		t.Errorf("imported = %d, want 1", imported)
	}
	if !reflect.DeepEqual(out, []string{"BARE_NAME_NO_EQUALS"}) {
		t.Errorf("out = %+v, want bare entry preserved", out)
	}
}

// TestSecretKeyForDepDefault / TestSecretKeyForDepExplicit — unit tests for
// the (service, key) split helper.
func TestSecretKeyForDepDefault(t *testing.T) {
	dep := EnvDependency{Name: "TEST_OV_CRED_ROUTEA"}
	svc, key := secretKeyForDep(dep)
	if svc != "ov/secret" || key != "TEST_OV_CRED_ROUTEA" {
		t.Errorf("default = (%q, %q), want (ov/secret, TEST_OV_CRED_ROUTEA)", svc, key)
	}
}

func TestSecretKeyForDepExplicit(t *testing.T) {
	cases := map[string][2]string{
		"ov/api-key/routea": {"ov/api-key", "routea"},
		"ov/secret/admin":   {"ov/secret", "admin"},
	}
	for in, want := range cases {
		dep := EnvDependency{Name: "X", Key: in}
		svc, key := secretKeyForDep(dep)
		if svc != want[0] || key != want[1] {
			t.Errorf("secretKeyForDep(%q) = (%q, %q), want (%q, %q)", in, svc, key, want[0], want[1])
		}
	}
}

// TestStripSecretEnvNames — defense-in-depth scrub called by saveDeployState.
// Verifies that KEY=VAL entries whose KEY matches the blocked list are
// removed, unrelated entries are preserved in order, and empty/nil inputs
// are handled cleanly.
func TestStripSecretEnvNames(t *testing.T) {
	cases := []struct {
		name    string
		env     []string
		blocked []string
		want    []string
	}{
		{
			name:    "nil env",
			env:     nil,
			blocked: []string{"X"},
			want:    nil,
		},
		{
			name:    "nil blocked",
			env:     []string{"A=1", "B=2"},
			blocked: nil,
			want:    []string{"A=1", "B=2"},
		},
		{
			name:    "no matches",
			env:     []string{"A=1", "B=2"},
			blocked: []string{"X", "Y"},
			want:    []string{"A=1", "B=2"},
		},
		{
			name:    "single match removed",
			env:     []string{"A=1", "SECRET=xxx", "B=2"},
			blocked: []string{"SECRET"},
			want:    []string{"A=1", "B=2"},
		},
		{
			name:    "multiple matches removed, order preserved",
			env:     []string{"A=1", "K1=x", "B=2", "K2=y", "C=3"},
			blocked: []string{"K1", "K2"},
			want:    []string{"A=1", "B=2", "C=3"},
		},
		{
			name:    "malformed entry without = is preserved when not blocked",
			env:     []string{"A=1", "BARE"},
			blocked: []string{"X"},
			want:    []string{"A=1", "BARE"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stripSecretEnvNames(tc.env, tc.blocked)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("stripSecretEnvNames(%v, %v) = %v, want %v", tc.env, tc.blocked, got, tc.want)
			}
		})
	}
}

// TestSecretDepNames — the Run() call site helper that flattens
// meta.SecretAccepts + meta.SecretRequires into a single []string for
// SaveDeployStateInput.SecretNames.
func TestSecretDepNames(t *testing.T) {
	if got := secretDepNames(nil); got != nil {
		t.Errorf("nil meta: got %+v, want nil", got)
	}
	if got := secretDepNames(&ImageMetadata{}); got != nil {
		t.Errorf("empty meta: got %+v, want nil", got)
	}
	meta := &ImageMetadata{
		SecretRequires: []EnvDependency{{Name: "REQ1"}, {Name: "REQ2"}},
		SecretAccepts:  []EnvDependency{{Name: "ACC1"}},
	}
	got := secretDepNames(meta)
	want := []string{"REQ1", "REQ2", "ACC1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("secretDepNames = %+v, want %+v", got, want)
	}
}
