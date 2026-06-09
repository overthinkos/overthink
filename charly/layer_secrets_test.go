package main

import (
	"path/filepath"
	"testing"
)

// setupIsolatedConfigStore wires a test-isolated ConfigFileStore-backed
// credential store. Returns a teardown func; defer it. Mirrors the
// pattern in credential_store_test.go but specialised for the new
// layer-secrets tests so each one starts with an empty store.
func setupIsolatedConfigStore(t *testing.T) (cleanup func()) {
	t.Helper()
	dir := t.TempDir()
	RuntimeConfigPath = func() (string, error) {
		return filepath.Join(dir, "config.yml"), nil
	}
	t.Setenv("CHARLY_SECRET_BACKEND", "config")
	resetDefaultStore()
	return func() {
		RuntimeConfigPath = defaultRuntimeConfigPath
		resetDefaultStore()
	}
}

// TestEnsureLayerSecret_PresentInStore verifies that a value already
// stored at (service, key) is returned as-is — no auto-generation,
// no rewrite.
func TestEnsureLayerSecret_PresentInStore(t *testing.T) {
	defer setupIsolatedConfigStore(t)()

	if err := DefaultCredentialStore().Set("charly/secret", "EXISTING_TOKEN", "preset-value"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	dep := EnvDependency{Name: "EXISTING_TOKEN"}
	val, source := ensureLayerSecret(dep, true)

	if val != "preset-value" {
		t.Errorf("expected preset-value, got %q", val)
	}
	if source == "auto-generated" {
		t.Errorf("expected source != auto-generated; got %q (regression: pre-existing values must NOT regenerate)", source)
	}
}

// TestEnsureLayerSecret_RequiredMissingAutoGenerates is the user's
// primary requested behavior: missing + required → 32-byte hex,
// persisted to the active store.
func TestEnsureLayerSecret_RequiredMissingAutoGenerates(t *testing.T) {
	defer setupIsolatedConfigStore(t)()

	dep := EnvDependency{Name: "K3S_CLUSTER_TOKEN"}
	val, source := ensureLayerSecret(dep, true)

	if source != "auto-generated" {
		t.Errorf("expected source=auto-generated, got %q", source)
	}
	// 32 bytes url-safe base64 = 44 chars (Fernet-key compatible).
	// See generateRandomSecretToken in secrets.go for rationale.
	if len(val) != 44 {
		t.Errorf("expected 44-char url-safe base64 token, got %d chars: %q", len(val), val)
	}
	for _, c := range val {
		isUrlSafeB64 := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') || c == '-' || c == '_' || c == '='
		if !isUrlSafeB64 {
			t.Errorf("expected url-safe base64, found invalid char %q in %q", c, val)
			break
		}
	}

	// Persistence: the value must be retrievable via the same store.
	stored, err := DefaultCredentialStore().Get("charly/secret", "K3S_CLUSTER_TOKEN")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored != val {
		t.Errorf("persistence mismatch: returned %q, store has %q", val, stored)
	}
}

// TestEnsureLayerSecret_IdempotentAcrossCalls verifies the race-free
// invariant: a second resolver call (e.g., k3s-agent reading the
// token after k3s-server's first-call auto-gen) returns the SAME
// value, not a fresh regeneration.
func TestEnsureLayerSecret_IdempotentAcrossCalls(t *testing.T) {
	defer setupIsolatedConfigStore(t)()

	dep := EnvDependency{Name: "SHARED_TOKEN"}

	// First call — auto-generates + persists.
	val1, source1 := ensureLayerSecret(dep, true)
	if source1 != "auto-generated" {
		t.Fatalf("first call expected auto-generated, got %q", source1)
	}

	// Second call — must read persisted value.
	val2, source2 := ensureLayerSecret(dep, true)
	if val1 != val2 {
		t.Errorf("idempotency broken: first=%q, second=%q (regression: server+agent would mismatch)", val1, val2)
	}
	if source2 == "auto-generated" {
		t.Errorf("second call regenerated instead of reading persisted (source=%q)", source2)
	}
}

// TestEnsureLayerSecret_OptionalMissingReturnsEmpty verifies that
// non-required deps (secret_accepts) do NOT auto-generate when missing.
// Caller is responsible for falling back to dep.Default.
func TestEnsureLayerSecret_OptionalMissingReturnsEmpty(t *testing.T) {
	defer setupIsolatedConfigStore(t)()

	dep := EnvDependency{Name: "OPTIONAL_KEY"}
	val, source := ensureLayerSecret(dep, false)

	if val != "" {
		t.Errorf("expected empty value for optional+missing, got %q", val)
	}
	if source == "auto-generated" {
		t.Errorf("optional missing must NOT auto-generate; got source=%q", source)
	}

	// Confirm nothing was written to the store either.
	if stored, _ := DefaultCredentialStore().Get("charly/secret", "OPTIONAL_KEY"); stored != "" {
		t.Errorf("optional missing leaked %q to store", stored)
	}
}

// TestEnsureLayerSecret_CustomKeyRoutesToOverride verifies that the
// `key:` override on EnvDependency (e.g., `key: charly/api-key/openrouter`)
// routes the lookup AND the auto-gen persistence to the override
// service/key pair, not the default charly/secret/<name>.
func TestEnsureLayerSecret_CustomKeyRoutesToOverride(t *testing.T) {
	defer setupIsolatedConfigStore(t)()

	dep := EnvDependency{
		Name: "MY_VAR_NAME",
		Key:  "charly/api-key/openrouter",
	}

	val, source := ensureLayerSecret(dep, true)
	if source != "auto-generated" {
		t.Fatalf("expected auto-generated, got %q", source)
	}

	// The auto-gen MUST persist at the override location, not at the default.
	atOverride, _ := DefaultCredentialStore().Get("charly/api-key", "openrouter")
	if atOverride != val {
		t.Errorf("expected persistence at override (charly/api-key, openrouter), got %q (val=%q)", atOverride, val)
	}
	atDefault, _ := DefaultCredentialStore().Get("charly/secret", "MY_VAR_NAME")
	if atDefault != "" {
		t.Errorf("default location should be empty, got %q (key override leaked)", atDefault)
	}
}

// TestResolveLayerSecrets_RequiredAutoGen exercises the wrapper that
// the deploy-add path actually calls: a Layer with secret_requires
// must always resolve (auto-gen guarantees non-empty values).
func TestResolveLayerSecrets_RequiredAutoGen(t *testing.T) {
	defer setupIsolatedConfigStore(t)()

	layer := &Layer{
		secretRequires: []EnvDependency{
			{Name: "K3S_CLUSTER_TOKEN"},
		},
	}
	env := ResolveLayerSecret(layer)
	val, ok := env["K3S_CLUSTER_TOKEN"]
	if !ok || val == "" {
		t.Fatalf("expected K3S_CLUSTER_TOKEN to be resolved (auto-gen), got env=%v", env)
	}
	if len(val) != 44 {
		t.Errorf("expected 44-char url-safe base64 token (Fernet-compatible), got %d chars", len(val))
	}
}

// TestResolveLayerSecrets_OptionalDefaultFallback exercises the
// secret_accepts path with a Default value: missing + optional →
// dep.Default goes into env (not auto-gen).
func TestResolveLayerSecrets_OptionalDefaultFallback(t *testing.T) {
	defer setupIsolatedConfigStore(t)()

	layer := &Layer{
		secretAccepts: []EnvDependency{
			{Name: "OPTIONAL_VAR", Default: "fallback-value"},
		},
	}
	env := ResolveLayerSecret(layer)
	if env["OPTIONAL_VAR"] != "fallback-value" {
		t.Errorf("expected fallback-value, got %q", env["OPTIONAL_VAR"])
	}
}

// TestResolveSecretsForLayers_TwoLayersSameSecret verifies the
// race-free invariant at the wrapper level: two layers (think
// k3s-server + k3s-agent) declaring the same secret_requires
// resolve to the SAME value.
func TestResolveSecretsForLayers_TwoLayersSameSecret(t *testing.T) {
	defer setupIsolatedConfigStore(t)()

	server := &Layer{
		secretRequires: []EnvDependency{{Name: "K3S_CLUSTER_TOKEN"}},
	}
	agent := &Layer{
		secretRequires: []EnvDependency{{Name: "K3S_CLUSTER_TOKEN"}},
	}
	env := ResolveSecretForLayer([]*Layer{server, agent})

	val := env["K3S_CLUSTER_TOKEN"]
	if val == "" || len(val) != 44 {
		t.Fatalf("expected 44-char url-safe base64 token (Fernet-compatible), got %q", val)
	}
	// And the persisted store must have exactly that value.
	stored, _ := DefaultCredentialStore().Get("charly/secret", "K3S_CLUSTER_TOKEN")
	if stored != val {
		t.Errorf("server+agent token mismatch: env=%q stored=%q", val, stored)
	}
}
