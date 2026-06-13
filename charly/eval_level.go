package main

// eval_level.go — the per-box acceptance-depth ladder.
//
// EvalLevel controls how deep `charly check run <bed>` drives a box's
// acceptance, gated by the do:/context: axes of its Op steps:
//
//	none    — skip acceptance entirely
//	build   — build-context ops only (charly check box)
//	noagent — build + deploy + runtime act/assert, NO do: instruct (default)
//	agent   — also run do: instruct steps through the agent grader
//
// Authored as BoxConfig.EvalLevel; baked into the ai.opencharly.eval_level
// capability label so the bed runner reads the rung from the built image
// without the source repo.
const (
	EvalLevelNone    = "none"
	EvalLevelBuild   = "build"
	EvalLevelNoAgent = "noagent"
	EvalLevelAgent   = "agent"
)

// DefaultEvalLevel is the rung applied when a box declares no eval_level.
const DefaultEvalLevel = EvalLevelNoAgent

// evalLevelRank orders the ladder so a runner can ask "does this rung reach
// build/deploy/runtime/agent depth?" by numeric comparison instead of a
// scattered string switch (R3 — one source of truth for the ordering).
var evalLevelRank = map[string]int{
	EvalLevelNone:    0,
	EvalLevelBuild:   1,
	EvalLevelNoAgent: 2,
	EvalLevelAgent:   3,
}

// EvalLevels lists the canonical rungs in ladder order (for error messages).
var EvalLevels = []string{EvalLevelNone, EvalLevelBuild, EvalLevelNoAgent, EvalLevelAgent}

// ResolveEvalLevel normalizes an authored eval_level to a canonical rung,
// applying the default for the empty value. An unknown value is returned
// verbatim so the validator flags it — never silently defaulted.
func ResolveEvalLevel(level string) string {
	if level == "" {
		return DefaultEvalLevel
	}
	return level
}

// IsValidEvalLevel reports whether level is one of the four canonical rungs.
func IsValidEvalLevel(level string) bool {
	_, ok := evalLevelRank[level]
	return ok
}

// EvalLevelReaches reports whether a box resolved to `have` runs at least as
// deep as `want` — e.g. EvalLevelReaches(boxLevel, EvalLevelNoAgent) gates the
// runtime live pass, EvalLevelReaches(boxLevel, EvalLevelAgent) gates the agent
// grader. Both operands are normalized through ResolveEvalLevel first.
func EvalLevelReaches(have, want string) bool {
	return evalLevelRank[ResolveEvalLevel(have)] >= evalLevelRank[ResolveEvalLevel(want)]
}
