package main

import (
	"os"
	"strings"
	"testing"
)

func TestSubstitute_WellKnownTokens(t *testing.T) {
	ctx := &SubstContext{
		RunID:            "run-1",
		RecipeName:       "bench-claude",
		AIName:           "claude",
		WorkspacePath:    "/workspace/repo",
		TargetImage:      "fedora-coder",
		TargetKind:       "pod",
		TargetName:       "bench-pod",
		Iteration:        2,
		MaxIteration:     50,
		PlateauIteration: 3,
		PlateauCounter:   1,
		BestScore:        4,
		MCPEndpoint:      "http://mcp.example/",
		Notes:            "remember this",
		Tag:              "@smoke",
	}
	cases := map[string]string{
		"${RUN_ID}":            "run-1",
		"${RECIPE_NAME}":       "bench-claude",
		"${AI_NAME}":           "claude",
		"${WORKSPACE}":         "/workspace/repo",
		"${TARGET_IMAGE}":      "fedora-coder",
		"${TARGET_KIND}":       "pod",
		"${TARGET_NAME}":       "bench-pod",
		"${ITERATION}":         "2",
		"${MAX_ITERATION}":     "50",
		"${PLATEAU_ITERATION}": "3",
		"${PLATEAU_COUNTER}":   "1",
		"${BEST_SCORE}":        "4",
		"${MCP_ENDPOINT}":      "http://mcp.example/",
		"${NOTES}":             "remember this",
		"${TAG}":               "@smoke",
	}
	for in, want := range cases {
		if got := Substitute(in, ctx); got != want {
			t.Errorf("Substitute(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSubstitute_PrecedenceChain(t *testing.T) {
	t.Setenv("MY_VAR", "from-env")
	recipeEnv := map[string]string{"MY_VAR": "from-recipe"}
	aiEnv := map[string]string{"MY_VAR": "from-ai"}

	// recipe.env wins over ai.env wins over os.Getenv
	ctx := &SubstContext{}
	ctx.AppendEnv(recipeEnv)
	ctx.AppendEnv(aiEnv)
	if got := Substitute("${MY_VAR}", ctx); got != "from-recipe" {
		t.Errorf("recipe.env should win, got %q", got)
	}

	// Drop recipe.env: ai.env wins
	ctx2 := &SubstContext{}
	ctx2.AppendEnv(aiEnv)
	if got := Substitute("${MY_VAR}", ctx2); got != "from-ai" {
		t.Errorf("ai.env should win when recipe absent, got %q", got)
	}

	// Drop both: os.Getenv wins
	ctx3 := &SubstContext{}
	if got := Substitute("${MY_VAR}", ctx3); got != "from-env" {
		t.Errorf("os.Getenv should be last resort, got %q", got)
	}

	// Unresolved → empty
	if got := Substitute("${UNSET_TOKEN_XYZ}", ctx3); got != "" {
		t.Errorf("unresolved token should be empty, got %q", got)
	}
}

func TestSubstitute_SinglePass(t *testing.T) {
	// Token expansion containing another ${X} should NOT recurse.
	ctx := &SubstContext{}
	ctx.AppendEnv(map[string]string{"OUTER": "${INNER}", "INNER": "leaf"})
	if got := Substitute("${OUTER}", ctx); got != "${INNER}" {
		t.Errorf("expected single-pass substitution; got %q (recursion happened)", got)
	}
}

func TestSubstituteArgv_NoMutation(t *testing.T) {
	ctx := &SubstContext{Prompt: "hello"}
	argv := []string{"echo", "${PROMPT}"}
	out := SubstituteArgv(argv, ctx)
	if out[1] != "hello" {
		t.Errorf("expected substitution, got %q", out[1])
	}
	if argv[1] != "${PROMPT}" {
		t.Error("input argv was mutated")
	}
}

func TestSubstitute_OSEnvFallthrough(t *testing.T) {
	// Sanity: a token that's nowhere falls back to os.Getenv.
	got := os.Getenv("PATH")
	if got == "" {
		t.Skip("no PATH in env")
	}
	ctx := &SubstContext{}
	if want := strings.Split(got, ":"); len(want) > 0 {
		// Just confirm we get something
		if Substitute("${PATH}", ctx) == "" {
			t.Error("PATH fallback failed")
		}
	}
}
