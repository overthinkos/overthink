package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// LoadBenchmarkConfig
// ---------------------------------------------------------------------------

// writeBenchmarkYAML is a helper that writes overthink.yml to a tempdir and
// returns the dir path.
func writeBenchmarkYAML(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "overthink.yml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	return dir
}

func TestLoadBenchmarkConfig_AbsentFile(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadBenchmarkConfig(dir)
	if err != nil {
		t.Fatalf("absent overthink.yml should not error: %v", err)
	}
	if cfg != nil {
		t.Errorf("absent file should yield nil config, got %+v", cfg)
	}
}

func TestLoadBenchmarkConfig_AbsentKey(t *testing.T) {
	dir := writeBenchmarkYAML(t, `version: 1
images: {}
`)
	cfg, err := LoadBenchmarkConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg != nil {
		t.Errorf("absent benchmark: key should yield nil, got %+v", cfg)
	}
}

func TestLoadBenchmarkConfig_FullShape(t *testing.T) {
	dir := writeBenchmarkYAML(t, `version: 1
benchmark:
  runners:
    - name: claude
      command: [claude, -p, "${PROMPT}"]
      prompt_via: argv
      env:
        ANTHROPIC_API_KEY: "${ANTHROPIC_API_KEY}"
      timeout: 20m
      credentials:
        - src: ~/.claude/.credentials.json
          dst: ~/.claude/.credentials.json
    - name: codex
      command: [codex, exec, "${PROMPT}"]
  prompt: |
    Solve the scenarios. Exit when done.
`)
	cfg, err := LoadBenchmarkConfig(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg == nil {
		t.Fatal("want non-nil config")
	}
	if len(cfg.Runners) != 2 {
		t.Fatalf("want 2 runners, got %d", len(cfg.Runners))
	}
	if cfg.Runners[0].Name != "claude" {
		t.Errorf("runner[0].Name: %q", cfg.Runners[0].Name)
	}
	if cfg.Runners[0].Timeout != "20m" {
		t.Errorf("runner[0].Timeout: %q", cfg.Runners[0].Timeout)
	}
	if len(cfg.Runners[0].Credentials) != 1 {
		t.Fatalf("want 1 credential, got %d", len(cfg.Runners[0].Credentials))
	}
	if cfg.Runners[0].Credentials[0].Src != "~/.claude/.credentials.json" {
		t.Errorf("credential src: %q", cfg.Runners[0].Credentials[0].Src)
	}
	if cfg.Runners[0].Env["ANTHROPIC_API_KEY"] != "${ANTHROPIC_API_KEY}" {
		t.Errorf("env passthrough: %+v", cfg.Runners[0].Env)
	}
	if !strings.Contains(cfg.Prompt, "Solve the scenarios") {
		t.Errorf("prompt text mangled: %q", cfg.Prompt)
	}
}

// ---------------------------------------------------------------------------
// ResolveRunner + defaults
// ---------------------------------------------------------------------------

func TestResolveRunner_EmptyConfig(t *testing.T) {
	_, err := ResolveRunner(nil, "claude")
	if !errors.Is(err, ErrNoRunners) {
		t.Errorf("nil config should yield ErrNoRunners, got %v", err)
	}
	_, err = ResolveRunner(&BenchmarkConfig{}, "claude")
	if !errors.Is(err, ErrNoRunners) {
		t.Errorf("empty config should yield ErrNoRunners, got %v", err)
	}
}

func TestResolveRunner_SoleRunnerImplicit(t *testing.T) {
	cfg := &BenchmarkConfig{
		Runners: []BenchmarkRunner{
			{Name: "only", Command: []string{"echo"}},
		},
	}
	r, err := ResolveRunner(cfg, "")
	if err != nil {
		t.Fatalf("sole runner should resolve with empty name: %v", err)
	}
	if r.Name != "only" {
		t.Errorf("want only, got %q", r.Name)
	}
}

func TestResolveRunner_MultipleRequiresName(t *testing.T) {
	cfg := &BenchmarkConfig{
		Runners: []BenchmarkRunner{
			{Name: "a"}, {Name: "b"},
		},
	}
	_, err := ResolveRunner(cfg, "")
	if err == nil {
		t.Fatal("want ambiguity error; got nil")
	}
	if !strings.Contains(err.Error(), "multiple runners") {
		t.Errorf("error should mention multiple: %v", err)
	}
}

func TestResolveRunner_NotFound(t *testing.T) {
	cfg := &BenchmarkConfig{Runners: []BenchmarkRunner{{Name: "claude"}}}
	_, err := ResolveRunner(cfg, "codex")
	if !errors.Is(err, ErrRunnerNotFound) {
		t.Errorf("want ErrRunnerNotFound, got %v", err)
	}
}

func TestResolveRunner_DefaultsApplied(t *testing.T) {
	cfg := &BenchmarkConfig{
		Runners: []BenchmarkRunner{
			{Name: "claude", Command: []string{"claude"}}, // Timeout empty
		},
	}
	r, err := ResolveRunner(cfg, "claude")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if r.Timeout != "30m" {
		t.Errorf("Timeout should default to 30m, got %q", r.Timeout)
	}
	if r.PromptVia != "argv" {
		t.Errorf("PromptVia should default to argv, got %q", r.PromptVia)
	}
}

func TestResolveRunner_ExplicitOverridesDefault(t *testing.T) {
	cfg := &BenchmarkConfig{
		Runners: []BenchmarkRunner{
			{Name: "x", Timeout: "5m", PromptVia: "file"},
		},
	}
	r, err := ResolveRunner(cfg, "x")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if r.Timeout != "5m" || r.PromptVia != "file" {
		t.Errorf("explicit values not preserved: %+v", r)
	}
}

func TestResolveRunner_ReturnsCopyNotPointerIntoConfig(t *testing.T) {
	cfg := &BenchmarkConfig{Runners: []BenchmarkRunner{{Name: "x"}}}
	r, err := ResolveRunner(cfg, "x")
	if err != nil {
		t.Fatal(err)
	}
	// Mutate the returned runner; the config must be unchanged.
	r.Timeout = "99h"
	if cfg.Runners[0].Timeout == "99h" {
		t.Error("caller mutation leaked into config — must return a copy")
	}
}

// ---------------------------------------------------------------------------
// ParseRunnerTimeout
// ---------------------------------------------------------------------------

func TestParseRunnerTimeout_DefaultsOnEmpty(t *testing.T) {
	d, err := ParseRunnerTimeout("")
	if err != nil {
		t.Fatalf("empty should parse via default: %v", err)
	}
	if d != 30*time.Minute {
		t.Errorf("want 30m, got %v", d)
	}
}

func TestParseRunnerTimeout_Explicit(t *testing.T) {
	d, err := ParseRunnerTimeout("5m30s")
	if err != nil {
		t.Fatal(err)
	}
	if d != 5*time.Minute+30*time.Second {
		t.Errorf("want 5m30s, got %v", d)
	}
}

func TestParseRunnerTimeout_Invalid(t *testing.T) {
	_, err := ParseRunnerTimeout("not a duration")
	if err == nil {
		t.Error("expected parse error")
	}
}

// ---------------------------------------------------------------------------
// Substitute
// ---------------------------------------------------------------------------

func TestSubstitute_WellKnownTokens(t *testing.T) {
	ctx := &SubstContext{
		RunID:             "run-abc",
		WorkspacePath:     "/workspace/.benchmark/run-abc/worktree",
		TargetImage:       "fedora-ov",
		TargetDeployment:  "hermes-disposable",
		Iteration:         3,
		MaxIterations:     50,
		PlateauIterations: 3,
		PlateauCounter:    1,
		BestScore:         4,
		MCPEndpoint:       "http://localhost:18765/mcp",
		Tags:              "@skeleton",
		Prompt:            "<rendered>",
		PromptFile:        "/tmp/p.md",
		Deadline:          "2026-04-24T22:35:00Z",
		Timeout:           "30m",
	}
	cases := []struct{ in, want string }{
		{"${RUN_ID}", "run-abc"},
		{"${WORKSPACE}", "/workspace/.benchmark/run-abc/worktree"},
		{"${TARGET_IMAGE}", "fedora-ov"},
		{"${TARGET_DEPLOYMENT}", "hermes-disposable"},
		{"${ITERATION}", "3"},
		{"${MAX_ITERATIONS}", "50"},
		{"${PLATEAU_ITERATIONS}", "3"},
		{"${PLATEAU_COUNTER}", "1"},
		{"${BEST_SCORE}", "4"},
		{"${MCP_ENDPOINT}", "http://localhost:18765/mcp"},
		{"${TAGS}", "@skeleton"},
		{"${PROMPT}", "<rendered>"},
		{"${PROMPT_FILE}", "/tmp/p.md"},
		{"${DEADLINE}", "2026-04-24T22:35:00Z"},
		{"${TIMEOUT}", "30m"},
	}
	for _, c := range cases {
		got := Substitute(c.in, ctx)
		if got != c.want {
			t.Errorf("Substitute(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestSubstitute_ExtraEnvOverridesOsEnv(t *testing.T) {
	t.Setenv("MY_CUSTOM_VAR", "from-os")
	ctx := &SubstContext{ExtraEnv: map[string]string{"MY_CUSTOM_VAR": "from-extra"}}
	got := Substitute("${MY_CUSTOM_VAR}", ctx)
	if got != "from-extra" {
		t.Errorf("ExtraEnv must override os.Getenv; got %q", got)
	}
}

func TestSubstitute_OsEnvFallback(t *testing.T) {
	t.Setenv("SOME_OS_VAR", "from-os-fallback")
	got := Substitute("${SOME_OS_VAR}", &SubstContext{})
	if got != "from-os-fallback" {
		t.Errorf("want os.Getenv fallback; got %q", got)
	}
}

func TestSubstitute_UnresolvedEmpty(t *testing.T) {
	got := Substitute("prefix:${NONEXISTENT_TOKEN_XYZ}:suffix", &SubstContext{})
	if got != "prefix::suffix" {
		t.Errorf("unresolved should expand to empty; got %q", got)
	}
}

func TestSubstitute_SinglePass(t *testing.T) {
	// ExtraEnv value contains ${OTHER} — must NOT be re-expanded.
	ctx := &SubstContext{
		ExtraEnv: map[string]string{
			"OUTER": "${INNER}",
			"INNER": "should-not-appear",
		},
	}
	got := Substitute("${OUTER}", ctx)
	if got != "${INNER}" {
		t.Errorf("expansion must be single-pass; got %q", got)
	}
}

func TestSubstitute_NilContext(t *testing.T) {
	// Nil ctx should not panic; unresolved tokens expand to "".
	got := Substitute("plain ${X}", nil)
	if got != "plain " {
		t.Errorf("nil ctx: %q", got)
	}
}

func TestSubstitute_TokenRegexBoundary(t *testing.T) {
	// Must NOT match $X, ${x} (lowercase), ${}, or ${1ABC} (leading digit).
	ctx := &SubstContext{ExtraEnv: map[string]string{
		"X": "should-not-trigger-for-$X",
	}}
	cases := []struct{ in, want string }{
		{"$X", "$X"},          // no braces → no match
		{"${x}", "${x}"},      // lowercase → no match
		{"${}", "${}"},        // empty → no match
		{"${1ABC}", "${1ABC}"},// leading digit → no match
	}
	for _, c := range cases {
		got := Substitute(c.in, ctx)
		if got != c.want {
			t.Errorf("Substitute(%q) = %q; want %q (no match expected)", c.in, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// SubstituteArgv + SubstituteEnv
// ---------------------------------------------------------------------------

func TestSubstituteArgv(t *testing.T) {
	ctx := &SubstContext{Prompt: "go"}
	argv := []string{"claude", "-p", "${PROMPT}", "--max=${MAX_ITERATIONS}"}
	ctx.MaxIterations = 50
	out := SubstituteArgv(argv, ctx)
	want := []string{"claude", "-p", "go", "--max=50"}
	for i := range want {
		if out[i] != want[i] {
			t.Errorf("argv[%d] = %q; want %q", i, out[i], want[i])
		}
	}
	// Source argv unchanged.
	if argv[2] != "${PROMPT}" {
		t.Error("source argv was mutated")
	}
}

func TestSubstituteEnv_NilAndValues(t *testing.T) {
	if SubstituteEnv(nil, &SubstContext{}) != nil {
		t.Error("nil env should return nil")
	}
	in := map[string]string{"K1": "${RUN_ID}", "K2": "literal"}
	ctx := &SubstContext{RunID: "abc"}
	out := SubstituteEnv(in, ctx)
	if out["K1"] != "abc" || out["K2"] != "literal" {
		t.Errorf("env substitution: %+v", out)
	}
	if in["K1"] != "${RUN_ID}" {
		t.Error("source env was mutated")
	}
}

// ---------------------------------------------------------------------------
// PrintRunners + SortedRunnerNames
// ---------------------------------------------------------------------------

func TestPrintRunners_EmptyAndPopulated(t *testing.T) {
	var buf bytes.Buffer
	PrintRunners(&buf, nil)
	if !strings.Contains(buf.String(), "No runners") {
		t.Errorf("empty config should say so: %q", buf.String())
	}

	buf.Reset()
	cfg := &BenchmarkConfig{
		Runners: []BenchmarkRunner{
			{Name: "claude", Command: []string{"claude", "-p", "${PROMPT}"}},
			{Name: "codex", Command: []string{"codex"}, Timeout: "15m"},
		},
	}
	PrintRunners(&buf, cfg)
	out := buf.String()
	if !strings.Contains(out, "claude") || !strings.Contains(out, "codex") {
		t.Errorf("print missing rows: %q", out)
	}
	if !strings.Contains(out, "30m (default)") {
		t.Errorf("claude row should show the default timeout label: %q", out)
	}
	if !strings.Contains(out, "15m") {
		t.Errorf("codex row should show explicit 15m: %q", out)
	}
}

func TestSortedRunnerNames(t *testing.T) {
	cfg := &BenchmarkConfig{Runners: []BenchmarkRunner{
		{Name: "zed"}, {Name: "alpha"}, {Name: "mid"},
	}}
	got := SortedRunnerNames(cfg)
	want := []string{"alpha", "mid", "zed"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sorted[%d] = %q; want %q", i, got[i], want[i])
		}
	}
}
