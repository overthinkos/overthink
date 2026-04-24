package main

import (
	"context"
)

// RunScenarios executes every scenario in `descriptions` (already
// collected into a LabelDescriptionSet and merged with deploy.yml
// overlays) against the supplied runner, returning scenario-level
// results for reporting.
//
// The runner owns the base executor + resolver. When a scenario step
// carries `on: <target>`, the runner's TargetResolver (if set by the
// caller — typically the CLI layer) provides a target-specific
// resolver+executor pair. Classical `tests:` runs leave TargetResolver
// nil and never hit the multi-target path.
//
// Scenario ordering is document order; features are iterated in
// LabelDescriptionSet section order (layer → image → deploy). Outline
// scenarios fan out to one ScenarioResult per Examples row.
func RunScenarios(ctx context.Context, r *Runner, set *LabelDescriptionSet, filter *TagExpr, strict bool) []ScenarioResult {
	if set == nil {
		return nil
	}
	var out []ScenarioResult
	for _, sec := range [][]LabeledDescription{set.Layer, set.Image, set.Deploy} {
		for _, ld := range sec {
			for sIdx, scenario := range ld.Description.Scenarios {
				expanded := ExpandScenario(scenario)
				for _, es := range expanded {
					if filter != nil && !matchScenario(es, filter) {
						continue
					}
					res := runOneScenario(ctx, r, ld.Origin, sIdx, es, strict)
					out = append(out, res)
				}
			}
		}
	}
	return out
}

// ScenarioResult is the summary of one scenario's execution, including
// every step's individual TestResult. Reporters transform this into
// text/json/tap/junit output.
type ScenarioResult struct {
	Origin     string       `json:"origin"`      // "layer:redis" etc.
	ScenarioID string       `json:"scenario_id"` // ScenarioID(origin, idx[, row])
	Name       string       `json:"name"`        // post-substitution scenario name
	Tags       []string     `json:"tags,omitempty"`
	Steps      []StepResult `json:"steps"`
	OnFail     []StepResult `json:"on_fail,omitempty"`
	Status     TestStatus   `json:"status"` // overall (fail if any step failed)
	Pending    int          `json:"pending,omitempty"`
}

// StepResult pairs a TestResult with the step's narrative keyword/text.
type StepResult struct {
	Keyword string     `json:"keyword"`
	Text    string     `json:"text"`
	StepID  string     `json:"step_id"`
	Result  TestResult `json:"result"`
}

// matchScenario returns true when the filter matches the scenario's
// tag set. Implementation is simple because scenario tags already
// live on the Scenario struct — no inheritance propagation needed at
// this level; step-level tag inheritance happens inside filter application
// when the CLI passes `--filter <verb>` style narrowing.
func matchScenario(es ExpandedScenario, filter *TagExpr) bool {
	if filter == nil {
		return true
	}
	return filter.Match(es.Tags)
}

// runOneScenario executes one expanded scenario: sets up a fresh
// ScenarioContext, runs steps in order (stop on first fail), then
// runs OnFail steps if a failure occurred.
func runOneScenario(ctx context.Context, r *Runner, origin string, scenarioIdx int, es ExpandedScenario, strict bool) ScenarioResult {
	scenarioID := ScenarioID(origin, scenarioIdx, es.RowIndex)
	scenarioCtx := NewScenarioContext(scenarioID)

	// Swap in the scenario context for the duration of this scenario.
	// Classical tests: runs always have Runner.Scenario == nil — we save
	// and restore to be robust against reuse of the same Runner across
	// scenario and non-scenario runs.
	orig := r.Scenario
	r.Scenario = scenarioCtx
	defer func() { r.Scenario = orig }()

	res := ScenarioResult{
		Origin:     origin,
		ScenarioID: scenarioID,
		Name:       es.Name,
		Tags:       append([]string(nil), es.Tags...),
		Status:     TestPass,
	}

	// Execute steps in document order. Stop on first FAIL; remaining
	// steps report as skipped-blocked.
	failed := false
	for stepIdx := range es.Steps {
		step := es.Steps[stepIdx]
		sid := StepID(origin, scenarioIdx, stepIdx, es.RowIndex)
		scenarioCtx.CurrentStepID = sid

		sr := StepResult{
			Keyword: keywordOf(&step),
			Text:    step.KeywordText(),
			StepID:  sid,
		}

		if failed {
			sr.Result = TestResult{
				Status:  TestSkip,
				Message: "skipped — blocked by earlier fail in scenario",
				Verb:    verbOf(&step),
			}
			res.Steps = append(res.Steps, sr)
			continue
		}

		if step.IsPending() {
			status := TestSkip
			msg := "pending (no verb bound)"
			if strict {
				status = TestFail
				msg = "pending (no verb bound) — strict mode"
			}
			sr.Result = TestResult{Status: status, Message: msg}
			res.Steps = append(res.Steps, sr)
			res.Pending++
			if strict {
				failed = true
				res.Status = TestFail
			}
			continue
		}

		sr.Result = r.runOne(ctx, &step.Check)
		res.Steps = append(res.Steps, sr)
		if sr.Result.Status == TestFail {
			failed = true
			res.Status = TestFail
		}
	}

	// OnFail hook — runs once when any step failed. Each OnFail step
	// is itself a Step with an embedded Check; we reuse the runner
	// machinery so `on:` / `eventually:` / captures inside on_fail
	// work the same as in the main scenario steps.
	if failed && len(es.OnFail) > 0 {
		for idx := range es.OnFail {
			onfailStep := es.OnFail[idx]
			sid := StepID(origin, scenarioIdx, 10_000+idx, es.RowIndex) // 10_000+ suffix disambiguates from main steps
			scenarioCtx.CurrentStepID = sid

			sr := StepResult{
				Keyword: keywordOf(&onfailStep),
				Text:    onfailStep.KeywordText(),
				StepID:  sid,
			}

			if onfailStep.IsPending() {
				sr.Result = TestResult{Status: TestSkip, Message: "on_fail step has no verb (advisory)"}
				res.OnFail = append(res.OnFail, sr)
				continue
			}

			sr.Result = r.runOne(ctx, &onfailStep.Check)
			// OnFail step failures DON'T re-escalate the scenario — the
			// scenario is already failed. We record them so reporters
			// surface them, but they don't flip status.
			res.OnFail = append(res.OnFail, sr)
		}
	}

	return res
}

// keywordOf returns the populated keyword name for a step, or ""
// when the step has no keyword set (invalid but not fatal — reporters
// can render the empty keyword).
func keywordOf(s *Step) string {
	switch {
	case s.Given != "":
		return "Given"
	case s.When != "":
		return "When"
	case s.Then != "":
		return "Then"
	case s.And != "":
		return "And"
	case s.But != "":
		return "But"
	}
	return ""
}

// verbOf returns the verb name bound to a step's embedded Check, or
// "" for pending steps. Used for reporting when the step skips because
// a prior step failed.
func verbOf(s *Step) string {
	if kind, err := s.Check.Kind(); err == nil {
		return kind
	}
	return ""
}
