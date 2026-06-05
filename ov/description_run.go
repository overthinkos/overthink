package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"syscall"
)

// stepGroupResult bundles a step's StepResult with the per-step Pending
// counter so runStepGroup can hand back both to the scenario aggregator.
type stepGroupResult struct {
	Result  StepResult
	Pending int
}

// runStepGroup executes one or more sibling steps. When parallel is true
// AND there are >=2 steps to run, they execute concurrently as goroutines
// joined via WaitGroup; otherwise they run sequentially in slice order.
// Count-expanded steps (Check.Count > 0) are fanned out into N pseudo-
// steps with id="<orig>-<i>" and an INDEX env var; if the step is also
// in a parallel group, all N expansions run concurrently.
//
// blocked=true means an earlier failure in the scenario has flipped this
// run into skip-everything mode; we still emit StepResults so reporters
// see the full step list, but no work is done.
func runStepGroup(
	ctx context.Context,
	r *Runner,
	steps []Step,
	origin string,
	scenarioIdx, stepStartIdx, rowIdx int,
	scenarioCtx *ScenarioContext,
	blocked bool,
	strict bool,
	parallel bool,
) []stepGroupResult {
	// Expand each step into one-or-more (orig-index, sub-index) pairs
	// honoring Check.Count. Sub-index of -1 means no count (single step).
	type stepUnit struct {
		idxInGroup int
		subIdx     int  // -1 if no count expansion
		step       Step // copy with Check potentially mutated for INDEX
		stepID     string
	}
	var units []stepUnit
	for i, s := range steps {
		count := s.Check.Count
		if count <= 0 {
			units = append(units, stepUnit{
				idxInGroup: i,
				subIdx:     -1,
				step:       s,
				stepID:     StepID(origin, scenarioIdx, stepStartIdx+i, rowIdx),
			})
			continue
		}
		for j := 0; j < count; j++ {
			ss := s // copy step
			// Mutate the step ID + capture template by appending -<j>.
			ss.Check.ID = appendIndex(ss.Check.ID, j)
			ss.Check.Capture = appendIndex(ss.Check.Capture, j)
			units = append(units, stepUnit{
				idxInGroup: i,
				subIdx:     j,
				step:       ss,
				stepID:     fmt.Sprintf("%s-%d", StepID(origin, scenarioIdx, stepStartIdx+i, rowIdx), j),
			})
		}
	}

	results := make([]stepGroupResult, len(units))

	runUnit := func(u stepUnit, slot int) {
		var pending int
		sr := StepResult{
			Keyword: keywordOf(&u.step),
			Text:    u.step.KeywordText(),
			StepID:  u.stepID,
		}

		if blocked {
			sr.Result = EvalResult{
				Status:  TestSkip,
				Message: "skipped — blocked by earlier fail in scenario",
				Verb:    verbOf(&u.step),
			}
			results[slot] = stepGroupResult{Result: sr, Pending: 0}
			return
		}

		if u.step.IsPending() {
			// Agent Driven Development binding: a prose-only step (no
			// embedded check verb) binds to the agent grader when one is
			// set (`ov eval feature run` against a live deployment). The
			// grader probes the target and returns a real pass/fail
			// verdict with evidence — NOT "pending". Without a grader the
			// step stays advisory: skip by default, fail under --strict.
			if r.Grader != nil {
				sr.Result = r.Grader.Grade(ctx, GraderRequest{
					Feature:   r.GraderFeature,
					Narrative: r.GraderNarrative,
					Scenario:  r.GraderScenario,
					Keyword:   keywordOf(&u.step),
					Text:      u.step.KeywordText(),
				})
				results[slot] = stepGroupResult{Result: sr, Pending: 0}
				return
			}
			status := TestSkip
			msg := "pending (no verb bound)"
			if strict {
				status = TestFail
				msg = "pending (no verb bound) — strict mode"
			}
			sr.Result = EvalResult{Status: status, Message: msg}
			pending = 1
			results[slot] = stepGroupResult{Result: sr, Pending: pending}
			return
		}

		// Set per-iteration INDEX env var if the step is count-expanded.
		// Restored after the call (best-effort; in parallel mode a shared
		// process env can't preserve per-goroutine values, so we substitute
		// into the Check fields directly via string replacement instead).
		if u.subIdx >= 0 {
			indexVar := u.step.Check.IndexVar
			if indexVar == "" {
				indexVar = "INDEX"
			}
			// Substitute ${INDEX} (or the named var) directly into the
			// check's expandable string fields BEFORE running. This avoids
			// the global os.Setenv race in concurrent goroutines.
			expanded := substituteIndex(&u.step.Check, indexVar, u.subIdx)
			u.step.Check = *expanded
		}

		// runOne writes scenarioCtx.Captures on PASS. Mutex-protected
		// inside ScenarioContext.Capture (added in Ext 1).
		// CurrentStepID can race in parallel groups; reporters consult
		// individual StepResult.StepID fields, so this assignment is
		// only useful for sequential failures — best-effort.
		scenarioCtx.CurrentStepID = u.stepID
		evalRes := r.runOne(ctx, &u.step.Check)
		sr.Result = evalRes
		// Record for summarize: aggregation. Thread-safe.
		scenarioCtx.RecordResult(u.stepID, evalRes)
		results[slot] = stepGroupResult{Result: sr, Pending: 0}
	}

	if parallel && len(units) > 1 && !blocked {
		var wg sync.WaitGroup
		for i, u := range units {
			wg.Add(1)
			i, u := i, u
			go func() {
				defer wg.Done()
				runUnit(u, i)
			}()
		}
		wg.Wait()
	} else {
		for i, u := range units {
			runUnit(u, i)
			// In sequential mode, if this unit failed AND this is not the
			// last unit, mark blocked=true for subsequent units.
			if results[i].Result.Result.Status == TestFail {
				blocked = true
			}
		}
	}

	return results
}

// appendIndex appends "-<idx>" to the input if non-empty. Used to make
// IDs and capture names unique across count-expanded iterations.
func appendIndex(s string, idx int) string {
	if s == "" {
		return ""
	}
	return fmt.Sprintf("%s-%d", s, idx)
}

// substituteIndex returns a copy of c with ${<indexVar>} replaced by
// idx in every string field that the runner subsequently expands. We
// can't use os.Setenv in parallel goroutines (global), so we do
// in-place template substitution instead. This is a narrow special-
// case before the runner's general ${VAR} resolver runs.
func substituteIndex(c *Check, indexVar string, idx int) *Check {
	out := *c // shallow copy
	idxStr := fmt.Sprintf("%d", idx)
	tok := "${" + indexVar + "}"
	replace := func(s string) string {
		if !strings.Contains(s, tok) {
			return s
		}
		return strings.ReplaceAll(s, tok, idxStr)
	}
	out.ID = replace(out.ID)
	out.Capture = replace(out.Capture)
	out.Command = replace(out.Command)
	out.Tab = replace(out.Tab)
	out.Selector = replace(out.Selector)
	out.Text = replace(out.Text)
	out.URL = replace(out.URL)
	out.Expression = replace(out.Expression)
	out.HTTP = replace(out.HTTP)
	// Tool input is a JSON string — substitute directly.
	out.Input = replace(out.Input)
	return &out
}

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
// Scenario ordering: by default document order, but `depends_on:` between
// scenarios in the same description triggers a topological sort (post-
// 2026-04 BDD/test/harness surface-cleanup cutover — scenario_topo.go's
// topoSortByDeclarationOrder is now the shared sorter for both BDD
// descriptions and harness recipes). Tie-breaking by declaration index
// preserves the document-order intuition unless a dep forces reordering.
// Cycle errors are recorded as a fail verdict on every cyclic scenario
// in the affected description, then the non-cyclic remainder runs
// normally — matches the harness's "score the non-cyclic subset" policy
// from RunEvalLive.
//
// Features are iterated in LabelDescriptionSet section order
// (layer → image → deploy). Outline scenarios fan out to one
// ScenarioResult per Examples row. Outline expansion happens AFTER the
// topo-sort (sort orders the parent scenarios; outline rows inherit
// their parent's position).
func RunScenarios(ctx context.Context, r *Runner, set *LabelDescriptionSet, filter *TagExpr, strict bool) []ScenarioResult {
	if set == nil {
		return nil
	}
	var out []ScenarioResult
	for _, sec := range [][]LabeledDescription{set.Layer, set.Image, set.Deploy} {
		for _, ld := range sec {
			// Carry this description's goal (feature/narrative) into the
			// agent grader for any prose-only step in its scenarios (ADD).
			// No-op when r.Grader is nil (the common path).
			r.GraderFeature = ld.Description.Feature
			r.GraderNarrative = ld.Description.Narrative
			ordered, cyclicByName := topoSortDescription(ld.Description.Scenario)
			for sIdx, scenario := range ordered {
				if cyclicByName[scenario.Name] {
					// Cyclic scenario — emit a fail verdict so reporters
					// surface it, then skip its actual execution.
					out = append(out, cyclicScenarioResult(ld.Origin, sIdx, scenario))
					continue
				}
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

// topoSortDescription sorts a single description's scenarios via
// topoSortByDeclarationOrder (scenario_topo.go). On cycle, returns the
// declaration-order slice unchanged AND a name set of the scenarios
// involved in the cycle so the caller can emit fail verdicts for them
// without aborting the whole description.
//
// Pre-2026-04 BDD descriptions had no depends_on semantics; this is
// additive for any description that doesn't author depends_on (the
// topo-sort then degenerates to declaration order, identical output).
func topoSortDescription(scenarios []Scenario) ([]Scenario, map[string]bool) {
	if len(scenarios) == 0 {
		return scenarios, nil
	}
	sorted, err := topoSortByDeclarationOrder(scenarios)
	if err == nil {
		return sorted, nil
	}
	// Cycle detected — fall back to declaration order, mark cyclic
	// scenarios so the caller skips their execution with a fail verdict.
	cyclicByName := map[string]bool{}
	if ce, ok := err.(*CycleError); ok {
		for _, n := range ce.Cycle {
			cyclicByName[n] = true
		}
	}
	return scenarios, cyclicByName
}

// cyclicScenarioResult emits a single fail-verdict ScenarioResult for a
// scenario rejected by cycle detection. No steps run; the name appears
// in reports so the user can fix the depends_on graph.
func cyclicScenarioResult(origin string, sIdx int, sc Scenario) ScenarioResult {
	return ScenarioResult{
		Origin:     origin,
		ScenarioID: ScenarioID(origin, sIdx, -1),
		Name:       sc.Name,
		Tag:        append([]string(nil), sc.Tag...),
		Status:     TestFail,
		Step: []StepResult{{
			Result: EvalResult{
				Status:  TestFail,
				Message: "scenario participates in a depends_on cycle — see validation output",
			},
		}},
	}
}

// ScenarioResult is the summary of one scenario's execution, including
// every step's individual EvalResult. Reporters transform this into
// text/json/tap/junit output.
type ScenarioResult struct {
	Origin     string       `json:"origin"`      // "layer:redis" etc.
	ScenarioID string       `json:"scenario_id"` // ScenarioID(origin, idx[, row])
	Name       string       `json:"name"`        // post-substitution scenario name
	Tag        []string     `json:"tag,omitempty"`
	Setup      []StepResult `json:"setup,omitempty"` // setup steps (Ext 5)
	Step       []StepResult `json:"step"`
	Teardown   []StepResult `json:"teardown,omitempty"` // teardown steps (Ext 5; always run)
	OnFail     []StepResult `json:"on_fail,omitempty"`
	Status     EvalStatus   `json:"status"` // overall (fail if any step failed)
	Pending    int          `json:"pending,omitempty"`
}

// StepResult pairs a EvalResult with the step's narrative keyword/text.
type StepResult struct {
	Keyword string     `json:"keyword"`
	Text    string     `json:"text"`
	StepID  string     `json:"step_id"`
	Result  EvalResult `json:"result"`
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
	return filter.Match(es.Tag)
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
	origGraderScenario := r.GraderScenario
	r.GraderScenario = es.Name
	defer func() { r.Scenario = orig; r.GraderScenario = origGraderScenario }()

	res := ScenarioResult{
		Origin:     origin,
		ScenarioID: scenarioID,
		Name:       es.Name,
		Tag:        append([]string(nil), es.Tag...),
		Status:     TestPass,
	}

	// Setup steps run first. A Setup failure aborts the main Steps loop
	// (sets failed=true) but Teardown still runs.
	failed := false
	if len(es.Setup) > 0 {
		setupResults := runStepGroup(ctx, r, es.Setup, origin,
			scenarioIdx, -1000, es.RowIndex, scenarioCtx, false, strict, false)
		for _, sr := range setupResults {
			res.Setup = append(res.Setup, sr.Result)
			if sr.Result.Result.Status == TestFail {
				failed = true
				res.Status = TestFail
			}
		}
	}

	// Teardown reaper — always runs, even on setup or steps failure.
	defer func() {
		if len(es.Teardown) > 0 {
			tdResults := runStepGroup(ctx, r, es.Teardown, origin,
				scenarioIdx, -2000, es.RowIndex, scenarioCtx, false, strict, false)
			for _, sr := range tdResults {
				// Teardown failures DON'T escalate the scenario verdict.
				res.Teardown = append(res.Teardown, sr.Result)
			}
		}
		// Reap host-side background processes.
		for _, pid := range scenarioCtx.SnapshotBackgrounds() {
			_ = sendSIGTERM(pid)
		}
	}()

	// Execute steps in document order. Stop on first FAIL; remaining
	// steps report as skipped-blocked.
	//
	// Parallel groups: consecutive steps with the same non-empty
	// step.Parallel value execute as goroutines awaited via WaitGroup
	// before the runner advances past the last step in the group. The
	// scenarioCtx mutex serializes Capture/Results/Backgrounds writes.
	// Count-expanded steps within a parallel group are also fanned out.
	stepIdx := 0
	for stepIdx < len(es.Step) {
		// Detect a parallel group at the current position.
		groupID := es.Step[stepIdx].Check.Parallel
		groupEnd := stepIdx + 1
		if groupID != "" {
			for groupEnd < len(es.Step) && es.Step[groupEnd].Check.Parallel == groupID {
				groupEnd++
			}
		}

		// Group runs all steps from stepIdx to groupEnd-1 concurrently
		// (single-step groups still go through the same path so the
		// failure-handling logic stays uniform).
		isParallel := groupID != "" && groupEnd-stepIdx > 1
		groupResults := runStepGroup(ctx, r, es.Step[stepIdx:groupEnd], origin,
			scenarioIdx, stepIdx, es.RowIndex, scenarioCtx, failed, strict, isParallel)
		for _, sr := range groupResults {
			if sr.Pending > 0 {
				res.Pending += sr.Pending
			}
			res.Step = append(res.Step, sr.Result)
			if sr.Result.Result.Status == TestFail {
				failed = true
				res.Status = TestFail
			}
		}
		stepIdx = groupEnd
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
				sr.Result = EvalResult{Status: TestSkip, Message: "on_fail step has no verb (advisory)"}
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

// sendSIGTERM sends SIGTERM to a host-side PID. Used by the scenario
// teardown reaper to clean up `command: { background: true }` processes.
// Best-effort: process-not-found errors are swallowed (the process may
// have already exited on its own), but other errors are returned for
// logging.
func sendSIGTERM(pid int) error {
	if pid <= 0 {
		return nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil
	}
	// On unix, FindProcess never errors and Signal returns "process already
	// finished" if so — we treat both as success.
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		// Unwrap "no such process" / "already finished" — best-effort.
		if strings.Contains(err.Error(), "process already finished") ||
			strings.Contains(err.Error(), "no such process") {
			return nil
		}
		return err
	}
	return nil
}

// sendSIGKILL is the SIGKILL sibling of sendSIGTERM. Used by the
// `kill:` step verb when Signal: KILL is requested. Same best-effort
// semantics as sendSIGTERM — process-not-found is treated as success.
func sendSIGKILL(pid int) error {
	if pid <= 0 {
		return nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil
	}
	if err := proc.Signal(syscall.SIGKILL); err != nil {
		if strings.Contains(err.Error(), "process already finished") ||
			strings.Contains(err.Error(), "no such process") {
			return nil
		}
		return err
	}
	return nil
}
