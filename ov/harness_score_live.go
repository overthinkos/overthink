package main

// harness_score_live.go — score the merged scenario set against the
// live deployment the AI created.
//
// Post the 2026-04 kind split, the harness scores `MergedScenarios`
// (the concatenation of every recipe in score.recipes, in order, each
// scenario carrying its SourceRecipe stamp). When merged scenarios are
// non-empty, the harness DOES NOT build + `ov image test` against a
// disposable image-test container; instead:
//
//  1. AI is given the scenarios via ${SCENARIOS} + ${RECIPES} in its
//     prompt
//  2. AI builds + deploys + tests the image themselves
//  3. After AI exits, harness opens a Runner against ov-<deployment>
//     and runs MergedScenarios via RunScenarios
//  4. The result feeds the same 7-way Classify pipeline as today
//
// Image-baked scenarios in the deployment's OCI labels are IGNORED —
// the score's recipe set IS the spec.

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"gopkg.in/yaml.v3"
)

// RunRecipeScenariosLive scores `scenarios` against the running
// deployment named `deployment` (= container `ov-<deployment>`).
// Returns a TestRunResults shaped exactly like ParseOvTestOutput
// would, so the existing scorer (Classify, fingerprints, summary)
// consumes it unchanged.
//
// `scoreName` is the active score name; per-scenario Origin is derived
// from each scenario's SourceRecipe stamp (`recipe:<source-recipe>`).
// Scenarios without SourceRecipe stamps fall back to `score:<scoreName>`.
//
// Function name retained for callsite stability across the cutover.
func RunRecipeScenariosLive(ctx context.Context, deployment, scoreName string, scenarios []Scenario) (*TestRunResults, error) {
	if deployment == "" {
		return nil, fmt.Errorf("score.deployment required for live scoring")
	}
	if len(scenarios) == 0 {
		return &TestRunResults{}, nil
	}
	containerName := "ov-" + deployment
	if err := containerRunningForScoring(ctx, containerName); err != nil {
		return nil, err
	}

	exec := &ContainerExecutor{Engine: "podman", ContainerName: containerName}
	resolver := &TestVarResolver{}
	runner := NewRunner(exec, resolver, RunModeTest)
	runner.Image = deployment

	// Bucket scenarios by SourceRecipe so each LabeledDescription has
	// the right Origin tag for downstream test-output traceability.
	set := &LabelDescriptionSet{
		Layer: bucketScenariosByRecipe(scenarios, scoreName),
	}
	results := RunScenarios(ctx, runner, set, nil, false)

	out := &TestRunResults{
		Image: containerName,
		Mode:  "run",
	}
	for _, sr := range results {
		tr := ScenarioTestResult{
			ID:           sr.ScenarioID,
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
		case "fail":
			out.Summary.Fail++
		case "skip":
			out.Summary.Skip++
		}
	}
	return out, nil
}

// bucketScenariosByRecipe groups scenarios by their SourceRecipe stamp.
// Each non-empty SourceRecipe gets its own LabeledDescription with
// Origin = "recipe:<source-recipe>"; unstamped scenarios share an
// "score:<scoreName>" group.
//
// Order is preserved within each bucket; bucket order matches first
// appearance in the input slice (so the AI sees scenarios in the same
// order they were authored in score.recipes).
func bucketScenariosByRecipe(scenarios []Scenario, scoreName string) []LabeledDescription {
	type bucket struct {
		origin    string
		feature   string
		scenarios []Scenario
	}
	buckets := []*bucket{}
	idx := map[string]*bucket{}
	for _, sc := range scenarios {
		key := sc.SourceRecipe
		if key == "" {
			key = "_score_"
		}
		b, ok := idx[key]
		if !ok {
			b = &bucket{}
			if sc.SourceRecipe != "" {
				b.origin = "recipe:" + sc.SourceRecipe
				b.feature = fmt.Sprintf("Recipe %s scenarios", sc.SourceRecipe)
			} else {
				b.origin = "score:" + scoreName
				b.feature = fmt.Sprintf("Score %s scenarios", scoreName)
			}
			idx[key] = b
			buckets = append(buckets, b)
		}
		b.scenarios = append(b.scenarios, sc)
	}
	out := make([]LabeledDescription, 0, len(buckets))
	for _, b := range buckets {
		out = append(out, LabeledDescription{
			Origin: b.origin,
			Description: Description{
				Feature:  b.feature,
				Scenario: b.scenarios,
			},
		})
	}
	return out
}

// containerRunningForScoring confirms <containerName> is running.
func containerRunningForScoring(ctx context.Context, containerName string) error {
	out, err := exec.CommandContext(ctx, "podman", "inspect", "--format", "{{.State.Running}}", containerName).CombinedOutput()
	if err != nil {
		return fmt.Errorf("score live: container %q not reachable — the AI was supposed to `ov deploy add %s ...` before exiting: %w\n%s",
			containerName, strings.TrimPrefix(containerName, "ov-"), err, string(out))
	}
	if !strings.Contains(string(out), "true") {
		return fmt.Errorf("score live: container %q exists but is not running — AI did not start the deployment", containerName)
	}
	return nil
}

// synthesizeScoreBaseline builds the pre-AI baseline from the merged
// scenario set. Each scenario's Origin tracks its SourceRecipe stamp
// so per-iteration verdicts can be attributed back to the source
// recipe in the result file.
//
// All scenarios are marked status: fail — nothing's been deployed yet.
// Fingerprints are computed from the scenario YAML so post-iteration
// fingerprint comparison works (recipes are immutable; pre == post).
//
// **Per-source-recipe sIdx**: ScenarioIDs MUST match the runtime IDs
// produced by RunScenarios (which iterates each LabeledDescription's
// scenario list independently with sIdx 0..N). So we bucket scenarios
// by SourceRecipe and assign sIdx within each bucket — exactly mirroring
// what bucketScenariosByRecipe + RunScenarios do at scoring time.
// Indexing the flat merged slice would produce IDs that no runtime
// verdict references, mis-classifying every non-first-bucket scenario
// as Tampered and triggering a false solved-all exit.
func synthesizeScoreBaseline(scoreName string, scenarios []Scenario) ([]ScenarioTestResult, map[string]string, map[string]string) {
	var out []ScenarioTestResult
	fps := make(map[string]string)
	tagFps := make(map[string]string)

	// Per-bucket sIdx counter, keyed by origin string.
	bucketIdx := map[string]int{}
	for _, scenario := range scenarios {
		origin := "score:" + scoreName
		if scenario.SourceRecipe != "" {
			origin = "recipe:" + scenario.SourceRecipe
		}
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
