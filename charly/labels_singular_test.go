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
// `ai.opencharly.*` keys into the renamed BoxMetadata fields. The keys are
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
			"ai.opencharly.version":     "2026.155.1801",
			"ai.opencharly.image":       "demo",
			"ai.opencharly.port":        `["8080:8080"]`,
			"ai.opencharly.service":     string(svcBlob),
			"ai.opencharly.env_provide": string(envBlob),
		}, nil
	}

	meta, err := ExtractMetadata("podman", "ghcr.io/overthinkos/demo:test")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(meta.Port) != 1 || meta.Port[0] != "8080:8080" {
		t.Errorf("singular ai.opencharly.port → meta.Port: got %+v", meta.Port)
	}
	if len(meta.Service) != 1 || meta.Service[0].Name != "web" {
		t.Errorf("singular ai.opencharly.service → meta.Service: got %+v", meta.Service)
	}
	if meta.EnvProvide["OLLAMA_HOST"] == "" {
		t.Errorf("singular ai.opencharly.env_provide → meta.EnvProvide: got %+v", meta.EnvProvide)
	}
}

// TestLabelConstantsAreSingular pins every renamed label const to its singular
// wire string. A regression to plural fails the suite — the contract guard.
func TestLabelConstantsAreSingular(t *testing.T) {
	pairs := []struct{ got, want string }{
		{LabelPort, "ai.opencharly.port"},
		{LabelVolume, "ai.opencharly.volume"},
		{LabelAlias, "ai.opencharly.alias"},
		{LabelHook, "ai.opencharly.hook"},
		{LabelRoute, "ai.opencharly.route"},
		{LabelSecret, "ai.opencharly.secret"},
		{LabelService, "ai.opencharly.service"},
		{LabelSkill, "ai.opencharly.skill"},
		{LabelEnvCandy, "ai.opencharly.env_candy"},
		{LabelPortProto, "ai.opencharly.port_proto"},
		{LabelCandyVersion, "ai.opencharly.candy_version"},
		{LabelPlatformFormat, "ai.opencharly.platform.format"},
		{LabelBuilderUse, "ai.opencharly.builder.use"},
		{LabelBuilderProvide, "ai.opencharly.builder.provide"},
		{LabelEnvProvide, "ai.opencharly.env_provide"},
		{LabelEnvRequire, "ai.opencharly.env_require"},
		{LabelEnvAccept, "ai.opencharly.env_accept"},
		{LabelSecretAccept, "ai.opencharly.secret_accept"},
		{LabelSecretRequire, "ai.opencharly.secret_require"},
		{LabelMCPProvide, "ai.opencharly.mcp_provide"},
		{LabelMCPRequire, "ai.opencharly.mcp_require"},
		{LabelMCPAccept, "ai.opencharly.mcp_accept"},
	}
	for _, p := range pairs {
		if p.got != p.want {
			t.Errorf("label const = %q, want singular %q", p.got, p.want)
		}
	}
}

// TestMigrateSingularLabel covers the build.yml `label_key` rewrite
// (ai.opencharly.services.<init> → service.<init>) and idempotency.
func TestMigrateSingularLabel(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "overthink.yml"),
		[]byte("version: 2026.174.0300\nimport:\n  - build.yml\n"), 0o644); err != nil {
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
