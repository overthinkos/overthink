package main

// harness_substitute.go — ${TOKEN} substitution for the harness.
//
// Substitution precedence (definitive):
//   well-known tokens (PROMPT, ITERATION, SCORE_DELTA, ...) →
//   score.env[KEY] → ai.env[KEY] → os.Getenv(KEY) → ""
//
// "Well-known" is a fixed set in lookupHarnessToken below; everything
// else falls through the env chain. The fallback maps are walked in
// order, so a score.env entry overrides an ai.env entry overrides
// os.Getenv. Substitution is single-pass (no recursive expansion).

import (
	"fmt"
	"os"
	"regexp"
)

// SubstContext carries every variable Substitute can expand.
//
// Wire order matters: callers populate fields they know about, leave
// the rest zero. Unknown ${X} tokens fall through the EnvChain (score
// then ai then os.Getenv).
type SubstContext struct {
	// Run identity
	RunID     string
	ScoreName string
	AIName    string

	// Workspace + target
	WorkspacePath string
	TargetImage   string
	TargetKind    string // "pod" | "vm" | "host"
	TargetName    string // pod or vm name (empty when TargetKind == "host")

	// Iteration loop state
	Iteration        int
	PlateauIteration int
	PlateauCounter   int
	BestScore        int
	ScoreDelta       int
	AttemptsLeft     int

	// Prompt + filter
	Prompt     string // rendered prompt text (for ${PROMPT})
	PromptFile string // when PromptVia == "file"
	Tag        string // Gherkin tag filter expression

	// MCP endpoint (drives the canonical ${MCP_ENDPOINT} substitution)
	MCPEndpoint string

	// Persistent NOTES.md content (drives ${NOTES})
	Notes string

	// Score-merged scenarios rendered as YAML (drives ${SCENARIOS})
	Scenarios string

	// Per-recipe-grouped block (drives ${RECIPES})
	Recipes string

	// Deployment name the harness scores against (drives ${DEPLOYMENT})
	Deployment string

	// Timing
	Deadline string // RFC3339 string, or "" when no deadline
	Timeout  string // per-AI resolved timeout string

	// EnvChain is walked in order for any token not in the well-known
	// set. Typical order: score.env, ai.env, os env (the os env is
	// implicit — Substitute calls os.Getenv after the chain is empty).
	EnvChain []map[string]string
}

// AppendEnv appends a single env map to the chain.
func (c *SubstContext) AppendEnv(m map[string]string) {
	if len(m) == 0 {
		return
	}
	c.EnvChain = append(c.EnvChain, m)
}

// harnessTokenRe matches ${IDENT} where IDENT follows shell convention.
var harnessTokenRe = regexp.MustCompile(`\$\{([A-Z_][A-Z0-9_]*)\}`)

// Substitute replaces every ${TOKEN} in `in` using ctx.
// Single-pass — no recursive expansion. Unresolved tokens become "".
func Substitute(in string, ctx *SubstContext) string {
	if ctx == nil {
		ctx = &SubstContext{}
	}
	return harnessTokenRe.ReplaceAllStringFunc(in, func(match string) string {
		return lookupHarnessToken(match[2:len(match)-1], ctx)
	})
}

// SubstituteArgv applies Substitute to every element of argv.
func SubstituteArgv(argv []string, ctx *SubstContext) []string {
	out := make([]string, len(argv))
	for i, a := range argv {
		out[i] = Substitute(a, ctx)
	}
	return out
}

// SubstituteEnv applies Substitute to every value in env.
func SubstituteEnv(env map[string]string, ctx *SubstContext) map[string]string {
	if env == nil {
		return nil
	}
	out := make(map[string]string, len(env))
	for k, v := range env {
		out[k] = Substitute(v, ctx)
	}
	return out
}

// lookupHarnessToken resolves one token name. Order:
//  1. Well-known token table
//  2. Each map in EnvChain (in order — first match wins)
//  3. os.Getenv
//  4. ""
func lookupHarnessToken(name string, ctx *SubstContext) string {
	// Well-known tokens — fixed, deterministic. Post the 2026-04 kind
	// split: ${RECIPE_NAME} and ${MAX_ITERATION} are removed; new
	// ${SCORE_NAME}, ${SCORE_DELTA}, ${ATTEMPTS_LEFT}, ${RECIPES} added.
	switch name {
	case "PROMPT":
		return ctx.Prompt
	case "PROMPT_FILE":
		return ctx.PromptFile
	case "WORKSPACE":
		return ctx.WorkspacePath
	case "TARGET_IMAGE":
		return ctx.TargetImage
	case "TARGET_KIND":
		return ctx.TargetKind
	case "TARGET_NAME":
		return ctx.TargetName
	case "RUN_ID":
		return ctx.RunID
	case "SCORE_NAME":
		return ctx.ScoreName
	case "AI_NAME":
		return ctx.AIName
	case "ITERATION":
		return intTok(ctx.Iteration)
	case "PLATEAU_ITERATION":
		return intTok(ctx.PlateauIteration)
	case "PLATEAU_COUNTER":
		return intTok(ctx.PlateauCounter)
	case "BEST_SCORE":
		return intTok(ctx.BestScore)
	case "SCORE_DELTA":
		return intTok(ctx.ScoreDelta)
	case "ATTEMPTS_LEFT":
		return intTok(ctx.AttemptsLeft)
	case "MCP_ENDPOINT":
		return ctx.MCPEndpoint
	case "NOTES":
		return ctx.Notes
	case "SCENARIOS":
		return ctx.Scenarios
	case "RECIPES":
		return ctx.Recipes
	case "DEPLOYMENT":
		return ctx.Deployment
	case "TAG":
		return ctx.Tag
	case "DEADLINE":
		return ctx.Deadline
	case "TIMEOUT":
		return ctx.Timeout
	}

	// Env chain — first non-zero wins.
	for _, m := range ctx.EnvChain {
		if v, ok := m[name]; ok {
			return v
		}
	}

	// Last resort: os.Getenv
	return os.Getenv(name)
}

// intTok stringifies an int for substitution.
func intTok(n int) string {
	return fmt.Sprintf("%d", n)
}
