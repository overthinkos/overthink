package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestExtractMetadata_SingularLabels proves the 2026-06 singular-label
// contract end-to-end on the read path: ExtractMetadata reads the SINGULAR
// `org.overthinkos.*` keys into the renamed ImageMetadata fields. The keys are
// written as LITERAL strings (not via the consts), so if any label const ever
// regresses to plural, ExtractMetadata — which reads via the const — won't find
// the literal key and the field stays empty, failing this test.
func TestExtractMetadata_SingularLabels(t *testing.T) {
	orig := InspectLabels
	defer func() { InspectLabels = orig }()

	svcBlob, _ := json.Marshal([]CapabilityService{{Name: "web", Init: "supervisord"}})
	envBlob, _ := json.Marshal(map[string]string{"OLLAMA_HOST": "http://{{.ContainerName}}:11434"})

	InspectLabels = func(engine, imageRef string) (map[string]string, error) {
		return map[string]string{
			"org.overthinkos.version":     "2026.155.1801",
			"org.overthinkos.image":       "demo",
			"org.overthinkos.port":        `["8080:8080"]`,
			"org.overthinkos.service":     string(svcBlob),
			"org.overthinkos.env_provide": string(envBlob),
		}, nil
	}

	meta, err := ExtractMetadata("podman", "ghcr.io/overthinkos/demo:test")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(meta.Port) != 1 || meta.Port[0] != "8080:8080" {
		t.Errorf("singular org.overthinkos.port → meta.Port: got %+v", meta.Port)
	}
	if len(meta.Service) != 1 || meta.Service[0].Name != "web" {
		t.Errorf("singular org.overthinkos.service → meta.Service: got %+v", meta.Service)
	}
	if meta.EnvProvide["OLLAMA_HOST"] == "" {
		t.Errorf("singular org.overthinkos.env_provide → meta.EnvProvide: got %+v", meta.EnvProvide)
	}
}

// TestLabelConstantsAreSingular pins every renamed label const to its singular
// wire string. A regression to plural fails the suite — the contract guard.
func TestLabelConstantsAreSingular(t *testing.T) {
	pairs := []struct{ got, want string }{
		{LabelPort, "org.overthinkos.port"},
		{LabelVolume, "org.overthinkos.volume"},
		{LabelAlias, "org.overthinkos.alias"},
		{LabelHook, "org.overthinkos.hook"},
		{LabelRoute, "org.overthinkos.route"},
		{LabelSecret, "org.overthinkos.secret"},
		{LabelService, "org.overthinkos.service"},
		{LabelSkill, "org.overthinkos.skill"},
		{LabelEnvCandy, "org.overthinkos.env_candy"},
		{LabelPortProto, "org.overthinkos.port_proto"},
		{LabelCandyVersion, "org.overthinkos.candy_version"},
		{LabelPlatformFormat, "org.overthinkos.platform.format"},
		{LabelBuilderUse, "org.overthinkos.builder.use"},
		{LabelBuilderProvide, "org.overthinkos.builder.provide"},
		{LabelEnvProvide, "org.overthinkos.env_provide"},
		{LabelEnvRequire, "org.overthinkos.env_require"},
		{LabelEnvAccept, "org.overthinkos.env_accept"},
		{LabelSecretAccept, "org.overthinkos.secret_accept"},
		{LabelSecretRequire, "org.overthinkos.secret_require"},
		{LabelMCPProvide, "org.overthinkos.mcp_provide"},
		{LabelMCPRequire, "org.overthinkos.mcp_require"},
		{LabelMCPAccept, "org.overthinkos.mcp_accept"},
	}
	for _, p := range pairs {
		if p.got != p.want {
			t.Errorf("label const = %q, want singular %q", p.got, p.want)
		}
	}
}

// TestMigrateSingularLabel covers the build.yml `label_key` rewrite
// (org.overthinkos.services.<init> → service.<init>) and idempotency.
func TestMigrateSingularLabel(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "overthink.yml"),
		[]byte("version: 2026.156.1041\nimport:\n  - build.yml\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bp := filepath.Join(dir, "build.yml")
	if err := os.WriteFile(bp,
		[]byte("init:\n  supervisord:\n    label_key: org.overthinkos.services.supervisord\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rewritten, err := MigrateSingularLabel(dir, false)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(rewritten) != 1 {
		t.Fatalf("expected 1 file rewritten, got %d (%v)", len(rewritten), rewritten)
	}
	out, _ := os.ReadFile(bp)
	if !strings.Contains(string(out), "org.overthinkos.service.supervisord") {
		t.Errorf("label_key not singularized: %s", out)
	}
	if strings.Contains(string(out), "org.overthinkos.services.") {
		t.Errorf("plural label string survived: %s", out)
	}

	// Idempotency: a second run rewrites nothing.
	rewritten2, err := MigrateSingularLabel(dir, false)
	if err != nil {
		t.Fatalf("migrate (2nd): %v", err)
	}
	if len(rewritten2) != 0 {
		t.Errorf("not idempotent: %d files rewritten on 2nd run", len(rewritten2))
	}
}
