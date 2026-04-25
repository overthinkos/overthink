package main

// harness_substitute.go — ${TOKEN} substitution for the harness.
//
// Substitution precedence (definitive):
//   well-known tokens (PROMPT, ITERATION, MAX_ITERATION, ...) →
//   recipe.env[KEY] → ai.env[KEY] → os.Getenv(KEY) → ""
//
// "Well-known" is a fixed set in lookupHarnessToken below; everything
// else falls through the env chain. The fallback maps are walked in
// order, so a recipe.env entry overrides an ai.env entry overrides
// os.Getenv. Substitution is single-pass (no recursive expansion).

import (
	"fmt"
	"os"
	"regexp"
)

// SubstContext carries every variable Substitute can expand.
//
// Wire order matters: callers populate fields they know about, leave
// the rest zero. Unknown ${X} tokens fall through the EnvChain (recipe
// then ai then os.Getenv).
type SubstContext struct {
	// Run identity
	RunID      string
	RecipeName string
	AIName     string

	// Workspace + target
	WorkspacePath string
	TargetImage   string
	TargetKind    string // "pod" | "vm" | "host"
	TargetName    string // pod or vm name (empty when TargetKind == "host")

	// Iteration loop state
	Iteration        int
	MaxIteration     int
	PlateauIteration int
	PlateauCounter   int
	BestScore        int

	// Prompt + filter
	Prompt     string // rendered prompt text (for ${PROMPT})
	PromptFile string // when PromptVia == "file"
	Tag        string // Gherkin tag filter expression

	// MCP endpoint (drives the canonical ${MCP_ENDPOINT} substitution)
	MCPEndpoint string

	// Persistent NOTES.md content (drives ${NOTES})
	Notes string

	// Recipe-defined scenarios rendered as YAML (drives ${SCENARIOS})
	Scenarios string

	// Deployment name the harness scores against (drives ${DEPLOYMENT})
	Deployment string

	// Timing
	Deadline string // RFC3339 string, or "" when no deadline
	Timeout  string // per-AI resolved timeout string

	// EnvChain is walked in order for any token not in the well-known
	// set. Typical order: recipe.env, ai.env, os env (the os env is
	// implicit — Substitute calls os.Getenv after the chain is empty).
	EnvChain []map[string]string
}

// AppendEnv appends a single env map to the chain. Convenience for the
// loop's prompt-render step.
func (c *SubstContext) AppendEnv(m map[string]string) {
	if len(m) == 0 {
		return
	}
	c.EnvChain = append(c.EnvChain, m)
}

// harnessTokenRe matches ${IDENT} where IDENT follows shell convention
// (leading uppercase letter or underscore, then uppercase / digit /
// underscore).
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
// Returns a new slice; argv is not mutated.
func SubstituteArgv(argv []string, ctx *SubstContext) []string {
	out := make([]string, len(argv))
	for i, a := range argv {
		out[i] = Substitute(a, ctx)
	}
	return out
}

// SubstituteEnv applies Substitute to every value in env.
// Returns a new map; env is not mutated. Keys are not substituted.
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
	// Well-known tokens first — fixed, deterministic.
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
	case "RECIPE_NAME":
		return ctx.RecipeName
	case "AI_NAME":
		return ctx.AIName
	case "ITERATION":
		return intTok(ctx.Iteration)
	case "MAX_ITERATION":
		return intTok(ctx.MaxIteration)
	case "PLATEAU_ITERATION":
		return intTok(ctx.PlateauIteration)
	case "PLATEAU_COUNTER":
		return intTok(ctx.PlateauCounter)
	case "BEST_SCORE":
		return intTok(ctx.BestScore)
	case "MCP_ENDPOINT":
		return ctx.MCPEndpoint
	case "NOTES":
		return ctx.Notes
	case "SCENARIOS":
		return ctx.Scenarios
	case "DEPLOYMENT":
		return ctx.Deployment
	case "TAG":
		return ctx.Tag
	case "DEADLINE":
		return ctx.Deadline
	case "TIMEOUT":
		return ctx.Timeout
	}

	// Env chain — first non-zero wins. (Empty string in a map means
	// "intentionally blank, don't fall through" — we still return it.)
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
