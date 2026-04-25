package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateHarness_LegacyToHarnessYml(t *testing.T) {
	dir := t.TempDir()

	// Write a minimal legacy overthink.yml carrying a benchmark: block.
	legacy := `version: 4
includes:
  - build.yml
benchmark:
  runners:
    - name: claude
      command: [claude, -p, "${PROMPT}"]
      credentials:
        - {src: ~/.claude/.credentials.json, dst: ~/.claude/.credentials.json}
    - name: codex
      command: [codex, exec, "${PROMPT}"]
  prompt: |
    Iteration ${ITERATION} of ${MAX_ITERATIONS}, plateau ${PLATEAU_COUNTER}/${PLATEAU_ITERATIONS}.
    Use ov benchmark scope to read scope.
`
	if err := os.WriteFile(filepath.Join(dir, "overthink.yml"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	written, err := MigrateHarness(MigrateHarnessOpts{Dir: dir})
	if err != nil {
		t.Fatalf("MigrateHarness failed: %v", err)
	}
	if len(written) == 0 {
		t.Fatal("expected migrator to write files")
	}

	harnessPath := filepath.Join(dir, "harness.yml")
	body, err := os.ReadFile(harnessPath)
	if err != nil {
		t.Fatalf("harness.yml not produced: %v", err)
	}
	bodyStr := string(body)
	// AI catalog entries.
	if !strings.Contains(bodyStr, "ai:") || !strings.Contains(bodyStr, "claude:") {
		t.Errorf("ai catalog missing claude:\n%s", bodyStr)
	}
	if !strings.Contains(bodyStr, "version_command: [claude, --version]") {
		t.Errorf("synthesized version_command missing:\n%s", bodyStr)
	}
	if !strings.Contains(bodyStr, "credential:") || strings.Contains(bodyStr, "credentials:") {
		t.Errorf("credential field should be singular:\n%s", bodyStr)
	}
	// Recipe section.
	if !strings.Contains(bodyStr, "recipe:") || !strings.Contains(bodyStr, "default:") {
		t.Errorf("recipe.default missing:\n%s", bodyStr)
	}
	// Token rename in prompt.
	if !strings.Contains(bodyStr, "${MAX_ITERATION}") || strings.Contains(bodyStr, "${MAX_ITERATIONS}") {
		t.Errorf("MAX_ITERATIONS should be renamed to MAX_ITERATION:\n%s", bodyStr)
	}
	if !strings.Contains(bodyStr, "${PLATEAU_ITERATION}") || strings.Contains(bodyStr, "${PLATEAU_ITERATIONS}") {
		t.Errorf("PLATEAU_ITERATIONS should be renamed:\n%s", bodyStr)
	}
	// Memory block prepended.
	if !strings.Contains(bodyStr, "== Memory ==") {
		t.Errorf("Memory block should be prepended:\n%s", bodyStr)
	}
	// Command path rewrite.
	if strings.Contains(bodyStr, "ov benchmark scope") {
		t.Errorf("ov benchmark should be rewritten to ov harness:\n%s", bodyStr)
	}
	if !strings.Contains(bodyStr, "ov harness scope") {
		t.Errorf("ov harness scope should appear:\n%s", bodyStr)
	}
	// Empty pod field with comment.
	if !strings.Contains(bodyStr, `pod: ""`) {
		t.Errorf("pod should be empty placeholder:\n%s", bodyStr)
	}

	// overthink.yml should no longer have a benchmark: block.
	otkBody, err := os.ReadFile(filepath.Join(dir, "overthink.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(otkBody), "benchmark:") {
		t.Errorf("benchmark: block should be removed from overthink.yml:\n%s", otkBody)
	}
	if !strings.Contains(string(otkBody), "harness.yml") {
		t.Errorf("harness.yml should be in includes:\n%s", otkBody)
	}
}

func TestMigrateHarness_Idempotent(t *testing.T) {
	dir := t.TempDir()
	// No benchmark: block — already migrated.
	if err := os.WriteFile(filepath.Join(dir, "overthink.yml"),
		[]byte("version: 4\nincludes:\n  - harness.yml\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	written, err := MigrateHarness(MigrateHarnessOpts{Dir: dir})
	if err != nil {
		t.Fatalf("idempotent run failed: %v", err)
	}
	// Should write nothing (no .gitignore, no .benchmark/, etc.).
	for _, p := range written {
		if !strings.Contains(p, ".gitignore") && !strings.Contains(p, ".benchmark") {
			t.Errorf("idempotent run wrote unexpected file: %s", p)
		}
	}
}

func TestMigrateHarness_NoOverthinkYml(t *testing.T) {
	dir := t.TempDir()
	if _, err := MigrateHarness(MigrateHarnessOpts{Dir: dir}); err == nil {
		t.Error("expected error when overthink.yml missing")
	}
}

func TestRewriteDescriptionPlurals(t *testing.T) {
	in := `layer:
  name: foo
  description:
    feature: Foo
    tags: [a, b]
    scenarios:
      - name: bar
        tags: [c]
        steps:
          - then: do thing
`
	got := rewriteDescriptionPlurals(in)
	if strings.Contains(got, "scenarios:") {
		t.Errorf("scenarios: should be renamed to scenario:\n%s", got)
	}
	if strings.Contains(got, "    tags:") {
		t.Errorf("description.tags: should be renamed to tag:\n%s", got)
	}
	if !strings.Contains(got, "scenario:") || !strings.Contains(got, "    tag:") {
		t.Errorf("rename incomplete:\n%s", got)
	}
}
