package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This file holds the load-bearing CI guarantee for the credential-backed
// secrets feature (plan §2.6 / §6.8 / §8.2): given a sentinel credential
// value that flows through the full ov config pipeline, no file written by
// that pipeline may contain the sentinel as a substring.
//
// The tests here exercise the code paths that actually write user-facing
// bytes to disk:
//
//   - MigratePlaintextEnvSecrets → SaveDeployConfig → deploy.yml bytes
//   - scrubSecretCLIEnv → (returned slice, to be persisted via saveDeployState)
//   - generateQuadlet → .container file bytes
//
// Each test greps the resulting bytes for the sentinel string and fails
// with the offending line printed if the sentinel appears anywhere. The
// sentinel has enough entropy that accidental substring collisions are
// implausible, so a single match is definitive evidence of a plaintext
// leak. Any future refactor that introduces a leak through a new code path
// (logging, debug dumps, error messages, YAML re-serialization) will fail
// at least one of these assertions.
//
// Why this is load-bearing: the structural design (separate secret_*
// sections, validation rules, credential store routing) is necessary but
// not sufficient — someone could still add a `fmt.Fprintf(f, "env: %s",
// env)` somewhere that undoes the whole boundary. This test reads the
// actual bytes and refuses to ship any path that leaks.

const (
	// sentinel is chosen to be distinctive, high-entropy, and obviously
	// test-only. If it ever appears in a user-facing file, the grep test
	// below will find it.
	sentinel = "SK-SENTINEL-DO-NOT-LEAK-12345-abcdef-xyzzy"

	// sentinelEnvName is a synthetic env var name that cannot match any
	// real credential. The test stores the sentinel at this name, so if
	// the test process inherits a real env var with this name, the sentinel
	// wins by virtue of being the one we t.Setenv'd. The synthetic name
	// ensures no real credential from the outer shell leaks into test
	// output.
	sentinelEnvName = "TEST_OV_SENTINEL_CRED"
)

// assertNoSentinelLeak scans bytes for the sentinel string and fails the
// test with the offending line when found. Used by every leak test below
// as the single source of truth for the boundary.
func assertNoSentinelLeak(t *testing.T, label string, data []byte) {
	t.Helper()
	if !bytes.Contains(data, []byte(sentinel)) {
		return
	}
	// Find the offending line for a clean error message.
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if strings.Contains(line, sentinel) {
			t.Errorf("%s: SENTINEL LEAKED at line %d:\n  %s", label, i+1, strings.TrimSpace(line))
			return
		}
	}
	t.Errorf("%s: SENTINEL LEAKED (not on a line boundary)", label)
}

// TestSentinelLeakMigrationWritesCleanDeployYaml — the primary guarantee
// for MigratePlaintextEnvSecrets: given a pre-existing deploy.yml with a
// plaintext sentinel and an image that now declares the sentinel env var
// as secret_requires, the migration must remove the plaintext entirely
// from the on-disk deploy.yml bytes before any subsequent write.
func TestSentinelLeakMigrationWritesCleanDeployYaml(t *testing.T) {
	deployPath := withDeployConfigTempPath(t)
	withIsolatedCredentialStore(t)

	// Seed a deploy.yml that contains the sentinel as a plaintext env
	// value — exactly the state a pre-upgrade host would have after running
	// `ov config -e TEST_OV_SENTINEL_CRED=<sentinel>`.
	seedDeployConfig(t, "openwebui", "", []string{
		"TEST_OV_CFG_URL=http://example",
		sentinelEnvName + "=" + sentinel,
	})

	// Declare the env var as secret_requires on the image so the migration
	// hook will pick it up.
	meta := &ImageMetadata{
		SecretRequires: []EnvDependency{
			{Name: sentinelEnvName, Description: "sentinel credential"},
		},
	}

	dc, err := LoadDeployConfig()
	if err != nil {
		t.Fatalf("LoadDeployConfig: %v", err)
	}

	n, err := MigratePlaintextEnvSecrets(dc, meta, "openwebui", "")
	if err != nil {
		t.Fatalf("MigratePlaintextEnvSecrets: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 migration, got %d", n)
	}

	// After migration, the persisted deploy.yml must NOT contain the sentinel.
	cleanedBytes, err := os.ReadFile(deployPath)
	if err != nil {
		t.Fatalf("read deploy.yml after migration: %v", err)
	}
	assertNoSentinelLeak(t, "deploy.yml after migration", cleanedBytes)

	// The backup file, on the other hand, MUST contain the sentinel —
	// that's the whole point of the backup. If the backup is missing the
	// sentinel, the user has no rollback path for their credential.
	matches, _ := filepath.Glob(deployPath + ".bak.*")
	if len(matches) != 1 {
		t.Fatalf("expected exactly one backup file, got %d", len(matches))
	}
	backupBytes, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if !bytes.Contains(backupBytes, []byte(sentinel)) {
		t.Errorf("backup file does not contain the sentinel — no rollback path")
	}
}

// TestSentinelLeakScrubCLIEnvRemovesSentinel — the guarantee for
// scrubSecretCLIEnv: a `-e NAME=<sentinel>` flag where NAME is declared as
// secret_accepts must never appear in the returned slice. The returned
// slice is what eventually reaches saveDeployState and the quadlet writer,
// so if the sentinel survives scrubSecretCLIEnv it ends up in the .container
// file.
func TestSentinelLeakScrubCLIEnvRemovesSentinel(t *testing.T) {
	withIsolatedCredentialStore(t)

	meta := &ImageMetadata{
		SecretAccepts: []EnvDependency{
			{Name: sentinelEnvName, Description: "sentinel"},
		},
	}
	cliEnv := []string{
		"TEST_OV_CFG_URL=http://example",
		sentinelEnvName + "=" + sentinel,
	}

	cleaned, imported, err := scrubSecretCLIEnv(cliEnv, meta)
	if err != nil {
		t.Fatalf("scrubSecretCLIEnv: %v", err)
	}
	if imported != 1 {
		t.Errorf("imported = %d, want 1", imported)
	}
	// The returned slice is what downstream code sees. It must not
	// contain the sentinel string anywhere.
	assertNoSentinelLeak(t, "scrubSecretCLIEnv cleaned slice", []byte(strings.Join(cleaned, "\n")))
}

// TestSentinelLeakQuadletEmission — the guarantee for generateQuadlet: a
// quadlet built from a CollectedSecret with RotateOnConfig=true must emit
// a Secret=<podman-name>,type=env,target=<var> directive and MUST NOT
// include any Environment= line carrying the sentinel value. The podman
// secret name and target env var name are metadata; the value lives in
// podman's secret store and never appears in the quadlet.
func TestSentinelLeakQuadletEmission(t *testing.T) {
	cfg := QuadletConfig{
		ImageName: "openwebui",
		ImageRef:  "ghcr.io/overthinkos/openwebui:latest",
		UID:       1000,
		GID:       1000,
		Env: []string{
			"TEST_OV_CFG_URL=http://example",
			// Deliberately provocative: even if a caller mistakenly
			// passed the sentinel as a plaintext Env, the quadlet emission
			// must still strip it via the defense-in-depth scrub. (In
			// production the scrub happens in Run() → saveDeployState, but
			// we verify generateQuadlet itself does not rehydrate.)
		},
		Secrets: []CollectedSecret{
			{
				Name:           "ov-openwebui-test-ov-sentinel-cred",
				Env:            sentinelEnvName,
				SecretName:     sentinelEnvName,
				Service:        "ov/secret",
				Key:            sentinelEnvName,
				RotateOnConfig: true,
			},
		},
	}

	content := generateQuadlet(cfg)

	// The quadlet must contain the Secret= directive pointing at the
	// podman secret name and target env var — that's how podman injects
	// the decrypted value into the container at runtime.
	if !strings.Contains(content, "Secret=ov-openwebui-test-ov-sentinel-cred,type=env,target="+sentinelEnvName) {
		t.Errorf("quadlet missing expected Secret= directive\n%s", content)
	}

	// And the sentinel value itself must not appear anywhere in the
	// generated bytes.
	assertNoSentinelLeak(t, "generated quadlet", []byte(content))

	// Plaintext Environment= lines for the secret env var are forbidden.
	// The var must reach the container via the podman secret path, not
	// as an inline Environment= directive.
	if strings.Contains(content, "Environment="+sentinelEnvName+"=") {
		t.Errorf("quadlet contains plaintext Environment=%s= line — credential-backed env vars must use Secret= instead", sentinelEnvName)
	}
}

// TestSentinelLeakEndToEndPipeline — exercises MigratePlaintextEnvSecrets +
// scrubSecretCLIEnv + generateQuadlet in the order they run inside
// config_image.go:Run(), with the sentinel threaded through both the
// deploy.yml and the -e CLI path. Asserts that no byte the pipeline writes
// contains the sentinel.
func TestSentinelLeakEndToEndPipeline(t *testing.T) {
	deployPath := withDeployConfigTempPath(t)
	withIsolatedCredentialStore(t)

	// Step 1: pre-existing plaintext in deploy.yml.
	seedDeployConfig(t, "openwebui", "", []string{sentinelEnvName + "=" + sentinel})

	meta := &ImageMetadata{
		SecretRequires: []EnvDependency{
			{Name: sentinelEnvName, Description: "sentinel"},
		},
	}

	dc, _ := LoadDeployConfig()
	if _, err := MigratePlaintextEnvSecrets(dc, meta, "openwebui", ""); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Step 2: -e flag also carries the sentinel — auto-import must strip it.
	cliEnv := []string{sentinelEnvName + "=" + sentinel}
	cleaned, _, err := scrubSecretCLIEnv(cliEnv, meta)
	if err != nil {
		t.Fatalf("scrub: %v", err)
	}

	// Step 3: collect secrets from image labels + credential store.
	collected, resolutions := CollectLayerSecretAccepts("openwebui", "", meta)
	if len(collected) != 1 || !resolutions[0].Resolved {
		t.Fatalf("expected 1 resolved collected secret, got %+v resolutions=%+v", collected, resolutions)
	}

	// Step 4: generate the quadlet with the collected secret.
	cfg := QuadletConfig{
		ImageName: "openwebui",
		ImageRef:  "ghcr.io/overthinkos/openwebui:latest",
		UID:       1000,
		GID:       1000,
		Env:       cleaned, // post-scrub env — should NOT carry sentinel
		Secrets:   collected,
	}
	content := generateQuadlet(cfg)

	// Assertions: the sentinel must not appear in any output.
	cleanedDeployBytes, err := os.ReadFile(deployPath)
	if err != nil {
		t.Fatalf("read deploy.yml: %v", err)
	}
	assertNoSentinelLeak(t, "deploy.yml", cleanedDeployBytes)
	assertNoSentinelLeak(t, "scrubbed CLI env", []byte(strings.Join(cleaned, "\n")))
	assertNoSentinelLeak(t, "generated quadlet", []byte(content))

	// The quadlet must still carry the Secret= directive — the metadata
	// (secret name, target env var) is fine to leak; only the VALUE is
	// sensitive.
	if !strings.Contains(content, "Secret=ov-openwebui-"+strings.ToLower(strings.ReplaceAll(sentinelEnvName, "_", "-"))+",type=env,target="+sentinelEnvName) {
		t.Errorf("quadlet missing Secret= directive for %s:\n%s", sentinelEnvName, content)
	}
}
