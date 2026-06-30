package kit

// step_keyword.go — the plan step-keyword constants, shared by charly core
// (description_spec.go) AND the compiled-in candy/plugin-migrate's plan-unify
// migrator (R3 — ONE copy across the module boundary). The StepKeyword TYPE lives
// in spec (union_types.go); these are the wire-keyword constant values. Core +
// candy each alias them.

import "github.com/overthinkos/overthink/charly/spec"

const (
	KwRun        spec.StepKeyword = "run"
	KwCheck      spec.StepKeyword = "check"
	KwAgentRun   spec.StepKeyword = "agent-run"
	KwAgentCheck spec.StepKeyword = "agent-check"
	KwInclude    spec.StepKeyword = "include"
)
