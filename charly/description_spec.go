package main

// The unified `plan:` schema — ONE flat ordered list of intent-typed steps that
// is the candy's complete operational + acceptance spec. See the plan file
// "Unify the entire test/eval/benchmark system into ONE flat plan: vocabulary".
//
// A Step carries exactly one intent keyword (run/check/agent-run/agent-check) OR
// an include: directive, plus an inline Op (the verb + matchers + modifiers):
//   - run:         deterministic state-change (the install timeline; build/deploy/provision)
//   - check:       deterministic idempotent probe (verification; safe to run any time)
//   - agent-run:   agent instruction that MAY mutate (graded)
//   - agent-check: agent read-only assessment (graded)
//   - include:     splice another entity's plan steps (composition; <kind>:<name>)
//
// The keyword's value carries the step's prose; the embedded Op carries the verb,
// matchers, and modifiers (id, tag, context, pod, depends_on, count, ...).

import (
	"fmt"
	"strings"
)

// StepKeyword is the intent discriminator on a Step.
type StepKeyword string

const (
	KwRun        StepKeyword = "run"
	KwCheck      StepKeyword = "check"
	KwAgentRun   StepKeyword = "agent-run"
	KwAgentCheck StepKeyword = "agent-check"
	KwInclude    StepKeyword = "include"
)

// StepKeywords lists the valid step discriminators in document order.
var StepKeywords = []StepKeyword{KwRun, KwCheck, KwAgentRun, KwAgentCheck, KwInclude}

// Step is a single plan step. Exactly one of Run/Check/AgentRun/AgentCheck/Include
// is non-empty (validated by StepKind()). The embedded Op is inline-promoted, so
// authors write:
//
//   - check: the server replies with PONG
//     command: redis-cli ping
//     stdout: PONG
//     context: [runtime]
type Step struct {
	Run        string `yaml:"run,omitempty"         json:"run,omitempty"`
	Check      string `yaml:"check,omitempty"       json:"check,omitempty"`
	AgentRun   string `yaml:"agent-run,omitempty"   json:"agent-run,omitempty"`
	AgentCheck string `yaml:"agent-check,omitempty" json:"agent-check,omitempty"`
	Include    string `yaml:"include,omitempty"     json:"include,omitempty"`

	Op `yaml:",inline"  json:",inline"`
}

// StepKind returns the step's intent keyword and an error if zero or multiple
// keyword discriminators are set. Parallel to Op.Kind().
func (s *Step) StepKind() (StepKeyword, error) {
	set := s.keywordsSet()
	if len(set) == 0 {
		return "", fmt.Errorf("step has no keyword (expected exactly one of: %s)", strings.Join(stepKeywordNames(), ", "))
	}
	if len(set) > 1 {
		names := make([]string, len(set))
		for i, k := range set {
			names[i] = string(k)
		}
		return "", fmt.Errorf("step has multiple keywords (%s); exactly one is required", strings.Join(names, ", "))
	}
	return set[0], nil
}

func stepKeywordNames() []string {
	out := make([]string, len(StepKeywords))
	for i, k := range StepKeywords {
		out[i] = string(k)
	}
	return out
}

// keywordsSet returns the keyword discriminators currently non-zero, in document order.
func (s *Step) keywordsSet() []StepKeyword {
	var set []StepKeyword
	if s.Run != "" {
		set = append(set, KwRun)
	}
	if s.Check != "" {
		set = append(set, KwCheck)
	}
	if s.AgentRun != "" {
		set = append(set, KwAgentRun)
	}
	if s.AgentCheck != "" {
		set = append(set, KwAgentCheck)
	}
	if s.Include != "" {
		set = append(set, KwInclude)
	}
	return set
}

// KeywordText returns the populated keyword's prose regardless of which
// discriminator holds it. For include: it returns the `<kind>:<name>` ref.
func (s *Step) KeywordText() string {
	switch {
	case s.Run != "":
		return s.Run
	case s.Check != "":
		return s.Check
	case s.AgentRun != "":
		return s.AgentRun
	case s.AgentCheck != "":
		return s.AgentCheck
	case s.Include != "":
		return s.Include
	}
	return ""
}

// IsAgent reports whether the step is agent-graded (agent-run / agent-check).
func (s *Step) IsAgent() bool { return s.AgentRun != "" || s.AgentCheck != "" }

// IsInclude reports whether the step is an include: composition directive.
func (s *Step) IsInclude() bool { return s.Include != "" }

// Mutates reports whether the step changes system state (run / agent-run).
// `charly check live` (verify-only mode) skips mutating steps.
func (s *Step) Mutates() bool { return s.Run != "" || s.AgentRun != "" }

// DoMode maps the step keyword to the internal act/assert/instruct dispatch enum.
func (s *Step) DoMode() DoMode {
	switch {
	case s.Run != "":
		return DoAct
	case s.Check != "":
		return DoAssert
	case s.AgentRun != "", s.AgentCheck != "":
		return DoInstruct
	}
	return DoAssert
}

// StepID returns the stable identifier used for plan-overlay merge lookups,
// depends_on references, and ${STEP_ID} substitution. The author-set Op.ID
// wins; otherwise a deterministic id is derived from origin + position.
func StepID(origin string, stepIdx int) string {
	return fmt.Sprintf("plan:%s:%d", origin, stepIdx)
}

// EffectiveStepID returns the step's author id when set, else a derived id.
func EffectiveStepID(s *Step, origin string, stepIdx int) string {
	if s.Op.ID != "" {
		return s.Op.ID
	}
	return StepID(origin, stepIdx)
}

// ---------------------------------------------------------------------------
// Tag-set helpers
// ---------------------------------------------------------------------------

// EffectiveTags normalizes and de-dups a step's tags. (Per-step tags only —
// there is no group-level tag inheritance.)
func EffectiveTags(stepTags []string) []string {
	if len(stepTags) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(stepTags))
	var out []string
	for _, t := range stepTags {
		t = normalizeTag(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// normalizeTag strips a single leading '@' so `@smoke` and `smoke` are
// treated identically — authors commonly write `@smoke` from Gherkin habit
// but the leading sigil is optional in our YAML surface.
func normalizeTag(t string) string {
	t = strings.TrimSpace(t)
	if strings.HasPrefix(t, "@") {
		return t[1:]
	}
	return t
}
