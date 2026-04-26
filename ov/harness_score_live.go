package main

// harness_score_live.go — score the merged scenario set against the
// live deployments inside the bench-pod's nested podman.
//
// Post the 2026-04 pod-cutover, EVERY recipe scenario carries a
// `pod:` field naming the container its steps probe. The harness:
//
//  1. AI sees the scenarios via ${SCENARIOS}/${RECIPES} in its prompt.
//  2. AI builds whatever images each pod needs and `ov deploy add`s
//     each pod by name inside bench-pod's nested podman.
//  3. After the AI exits, this code groups scenarios by their `Pod`
//     field and runs each group's scenarios against `ov-<pod>` via
//     a fresh ContainerExecutor.
//  4. The result feeds the same 7-way Classify pipeline as today.
//
// One field, one container per scenario. No defaults, no fallbacks.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"gopkg.in/yaml.v3"
)

// RunRecipeScenariosLive scores `scenarios` against the live containers
// they target via their `Pod` field. Returns a TestRunResults shaped
// like ParseOvTestOutput's, so the existing scorer (Classify,
// fingerprints, summary) consumes it unchanged.
//
// Scenarios are grouped by their `Pod` field; each group runs against
// a separate `ContainerExecutor{ContainerName: "ov-" + pod}`. If a
// pod's container isn't reachable, every scenario in that bucket
// returns a "not reachable" verdict (status: fail, no steps); the
// remaining buckets are still scored.
//
// `scoreName` and `deployment` parameters are kept on the signature
// for callsite stability but only `scoreName` is used (for narrative
// labelling). The legacy `deployment` argument is now ignored —
// scoring follows scenario.Pod, never score.deployment.
func RunRecipeScenariosLive(ctx context.Context, deployment, scoreName string, scenarios []Scenario) (*TestRunResults, error) {
	_ = deployment // legacy parameter, no longer consulted

	if len(scenarios) == 0 {
		return &TestRunResults{}, nil
	}

	// Defensive pod check (validator catches this earlier; double-tap).
	for _, sc := range scenarios {
		if sc.Pod == "" {
			return nil, fmt.Errorf("scenario %q has empty pod field — validator should have rejected this; refusing to score", sc.Name)
		}
	}

	// Topologically sort by depends_on, tie-break by declaration order,
	// then group consecutive same-pod runs into execution buckets. This
	// preserves the one-`podman exec`-per-bucket efficiency in the
	// common case (no cross-pod deps) while honoring cross-pod
	// dependencies when they exist (a single pod may legitimately span
	// multiple buckets).
	sorted, err := topoSortByDeclarationOrder(scenarios)
	if err != nil {
		return nil, fmt.Errorf("scoring scenarios: %w", err)
	}
	buckets := groupConsecutiveByPod(sorted)

	// IDs used by ScenarioTestResult must match the IDs that
	// synthesizeScoreBaseline emits — those use a per-pod sIdx counter
	// indexed against scenario declaration order in the recipe (NOT
	// topo-sort order). Compute a stable name -> ID map up front so
	// the runtime path can attach the matching ID regardless of the
	// post-topo execution order.
	idByName := stableScenarioIDsByName(scenarios)

	out := &TestRunResults{
		Image: "score:" + scoreName,
		Mode:  "run",
	}

	// verdictByName tracks the live status of each scenario as buckets
	// run, so cross-bucket depends_on cascades work. Values: "pass" |
	// "fail" | "skipped". Anything other than "pass" blocks dependents.
	verdictByName := make(map[string]string, len(scenarios))

	for _, bucket := range buckets {
		// Filter the bucket: any scenario whose depends_on contains a
		// non-passing entry is recorded as skipped without probing.
		// firstUnmetDep treats unknown-name as blocked — defensive
		// against topo-sort/validator misbehavior. validateHarness-
		// Semantics catches dangling refs at load time so this branch
		// shouldn't fire in well-formed configs.
		var probe []Scenario
		for _, sc := range bucket {
			blocked := firstUnmetDep(sc, verdictByName)
			if blocked != "" {
				out.Scenario = append(out.Scenario, skippedResult(sc, idByName[sc.Name], blocked))
				out.Summary.Total++
				out.Summary.Skip++
				verdictByName[sc.Name] = "skipped"
				continue
			}
			probe = append(probe, sc)
		}

		if len(probe) == 0 {
			continue
		}

		pod := probe[0].Pod
		containerName := "ov-" + pod

		if err := containerRunningForScoring(ctx, containerName); err != nil {
			// Container missing → record per-scenario "not reachable"
			// fail-status verdicts with no steps; continue to next bucket.
			for _, sc := range probe {
				expanded := ExpandScenario(sc)
				for _, es := range expanded {
					tr := ScenarioTestResult{
						ID:           idByName[sc.Name],
						Origin:       "pod:" + pod,
						Name:         es.Name,
						Tag:          append([]string(nil), es.Tag...),
						Status:       "fail",
						PendingSteps: 0,
					}
					out.Scenario = append(out.Scenario, tr)
					out.Summary.Total++
					out.Summary.Fail++
				}
				verdictByName[sc.Name] = "fail"
			}
			fmt.Fprintf(os.Stderr, "score live: pod %q unreachable: %v\n", pod, err)
			continue
		}

		exec := &ContainerExecutor{Engine: "podman", ContainerName: containerName}
		resolver := &TestVarResolver{}
		runner := NewRunner(exec, resolver, RunModeTest)
		runner.Image = pod

		set := &LabelDescriptionSet{
			Layer: []LabeledDescription{{
				Origin: "pod:" + pod,
				Description: Description{
					Feature:  fmt.Sprintf("Score scenarios for pod %s", pod),
					Scenario: probe,
				},
			}},
		}
		results := RunScenarios(ctx, runner, set, nil, false)

		// Map runner results back to scenarios by name (the runner
		// preserves declaration order within `probe`; results align by
		// scenario index inside the LabelDescriptionSet — but to be
		// robust we match on Name).
		resultByName := make(map[string]ScenarioResult, len(results))
		for _, sr := range results {
			resultByName[sr.Name] = sr
		}
		for _, sc := range probe {
			sr, ok := resultByName[sc.Name]
			if !ok {
				// Runner skipped this scenario somehow — shouldn't happen
				// for a well-formed recipe; surface as fail.
				tr := ScenarioTestResult{
					ID:     idByName[sc.Name],
					Origin: "pod:" + pod,
					Name:   sc.Name,
					Status: "fail",
				}
				out.Scenario = append(out.Scenario, tr)
				out.Summary.Total++
				out.Summary.Fail++
				verdictByName[sc.Name] = "fail"
				continue
			}
			tr := ScenarioTestResult{
				ID:           idByName[sc.Name],
				Origin:       sr.Origin,
				Name:         sr.Name,
				Tag:          append([]string(nil), sr.Tag...),
				Status:       sr.Status.String(),
				PendingSteps: sr.Pending,
			}
			for _, sp := range sr.Steps {
				step := StepTestResult{
					Keyword: sp.Keyword,
					Text:    sp.Text,
					StepID:  sp.StepID,
					Status:  sp.Result.Status.String(),
					Verb:    sp.Result.Verb,
				}
				if sp.Result.Verb == "" {
					step.Pending = true
				}
				tr.Steps = append(tr.Steps, step)
			}
			out.Scenario = append(out.Scenario, tr)
			out.Summary.Total++
			switch tr.Status {
			case "pass":
				out.Summary.Pass++
				verdictByName[sc.Name] = "pass"
			case "fail":
				out.Summary.Fail++
				verdictByName[sc.Name] = "fail"
			case "skip":
				out.Summary.Skip++
				verdictByName[sc.Name] = "fail" // skipped-by-runner ≠ depends-skipped
			}
		}
	}
	return out, nil
}

// skippedResult builds a depends-on-cascade skip result for one
// scenario. Status "skipped" (distinct from runner-side "skip") is
// recognized by Classify and surfaces as VerdictSkipped.
func skippedResult(sc Scenario, id, blockedBy string) ScenarioTestResult {
	return ScenarioTestResult{
		ID:            id,
		Origin:        "pod:" + sc.Pod,
		Name:          sc.Name,
		Tag:           append([]string(nil), sc.Tag...),
		Status:        "skipped",
		PendingSteps:  0,
		SkippedReason: "dep-unmet: " + blockedBy,
	}
}

// stableScenarioIDsByName returns a name -> scenario-ID map computed
// from the scenarios in declaration order, using the SAME bucketing
// scheme as synthesizeScoreBaseline (per-pod sIdx counter + ExpandScenario
// for row index). Topo-sort and bucket-grouping reorder execution but
// MUST NOT shift IDs — otherwise baseline IDs (computed pre-AI) would
// not match runtime IDs and every scenario would mis-classify as
// Tampered (pre.Present=true, post.Present=false).
//
// For non-outline scenarios, ExpandScenario returns a single entry
// with RowIndex=-1 → ScenarioID produces "desc:<origin>:<sIdx>" (no
// :row<n> suffix), which is what synthesizeScoreBaseline emits.
// Outline scenarios produce one ID per expanded row.
func stableScenarioIDsByName(scenarios []Scenario) map[string]string {
	out := make(map[string]string, len(scenarios))
	bucketIdx := map[string]int{}
	for _, sc := range scenarios {
		origin := "pod:" + sc.Pod
		sIdx := bucketIdx[origin]
		bucketIdx[origin]++
		expanded := ExpandScenario(sc)
		if len(expanded) == 0 {
			// Non-expandable scenario; use RowIndex=-1 to match the
			// non-outline path in synthesizeScoreBaseline.
			out[sc.Name] = ScenarioID(origin, sIdx, -1)
			continue
		}
		// First-row ID (sufficient for non-outline scenarios where
		// expanded[0].RowIndex == -1; outline scenarios emit only the
		// first row's ID under the scenario name — outline expansions
		// are handled within RunScenarios at probe time and matched by
		// scenario Name).
		out[sc.Name] = ScenarioID(origin, sIdx, expanded[0].RowIndex)
	}
	return out
}

// containerRunningForScoring confirms <containerName> is running.
func containerRunningForScoring(ctx context.Context, containerName string) error {
	out, err := exec.CommandContext(ctx, "podman", "inspect", "--format", "{{.State.Running}}", containerName).CombinedOutput()
	if err != nil {
		return fmt.Errorf("container %q not reachable: %w\n%s",
			containerName, err, string(out))
	}
	if !strings.Contains(string(out), "true") {
		return fmt.Errorf("container %q exists but is not running", containerName)
	}
	return nil
}

// synthesizeScoreBaseline builds the pre-AI baseline from the merged
// scenario set, bucketing scenarios by their `Pod` field. Each scenario
// is marked status: fail at baseline (nothing's deployed yet); IDs use
// per-pod sIdx so they match the runtime IDs produced by RunScenarios
// inside RunRecipeScenariosLive (which also buckets by Pod).
//
// Without per-pod bucketing the baseline IDs would index against the
// flat merged list (sIdx 0..N) and never match runtime IDs (sIdx
// 0..M per pod) — every non-first-bucket scenario would mis-classify
// as Tampered (pre.Present=true, post.Present=false), triggering a
// false solved-all exit.
func synthesizeScoreBaseline(scoreName string, scenarios []Scenario) ([]ScenarioTestResult, map[string]string, map[string]string) {
	_ = scoreName // legacy parameter, kept for callsite stability

	var out []ScenarioTestResult
	fps := make(map[string]string)
	tagFps := make(map[string]string)

	// Per-pod sIdx counter — must match the bucketing in
	// RunRecipeScenariosLive so baseline IDs == runtime IDs.
	bucketIdx := map[string]int{}
	for _, scenario := range scenarios {
		if scenario.Pod == "" {
			// Validator should reject; defensive skip.
			continue
		}
		origin := "pod:" + scenario.Pod
		sIdx := bucketIdx[origin]
		bucketIdx[origin]++

		expanded := ExpandScenario(scenario)
		for _, es := range expanded {
			id := ScenarioID(origin, sIdx, es.RowIndex)
			pending := 0
			for _, step := range es.Steps {
				if step.IsPending() {
					pending++
				}
			}
			out = append(out, ScenarioTestResult{
				ID:           id,
				Origin:       origin,
				Name:         es.Name,
				Tag:          append([]string(nil), es.Tag...),
				Status:       "fail",
				PendingSteps: pending,
			})
			fps[id] = FingerprintScenario(es.Scenario)
			tagFps[id] = FingerprintTags(es.Tag)
		}
	}
	return out, fps, tagFps
}

// RenderRecipeScenariosYAML returns the merged scenario list as a
// YAML block, suitable for ${SCENARIOS} substitution in the prompt.
// (Function name retained for callsite stability.)
func RenderRecipeScenariosYAML(scenarios []Scenario) string {
	if len(scenarios) == 0 {
		return ""
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(scenarios); err != nil {
		return fmt.Sprintf("# error rendering scenarios: %v", err)
	}
	_ = enc.Close()
	return buf.String()
}
