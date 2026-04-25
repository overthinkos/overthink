package main

// harness_score_live.go — score recipe-defined scenarios against the
// live running deployment the AI created.
//
// When recipe.Scenario is non-empty, the harness DOES NOT build +
// `ov image test` against a disposable image-test container. Instead:
//
//  1. AI is given the scenarios via ${SCENARIOS} in its prompt
//  2. AI builds + deploys + tests the image themselves
//  3. After AI exits, harness opens a Runner against ov-<deployment>
//     and runs recipe.Scenario via RunScenarios from description_run.go
//  4. The result feeds the same 7-way Classify pipeline as today
//
// Image-baked scenarios in the deployment's OCI labels are IGNORED —
// the recipe's scenarios are the spec.

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"gopkg.in/yaml.v3"
)

// RunRecipeScenariosLive scores recipe.Scenario against the running
// deployment named `deployment` (= container `ov-<deployment>`).
// Returns a TestRunResults shaped exactly like ParseOvTestOutput
// would, so the existing scorer (Classify, fingerprints, summary)
// consumes it unchanged.
func RunRecipeScenariosLive(ctx context.Context, deployment, recipeName string, scenarios []Scenario) (*TestRunResults, error) {
	if deployment == "" {
		return nil, fmt.Errorf("recipe.deployment required for live scoring")
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

	// Wrap recipe scenarios in a synthetic LabelDescriptionSet so we
	// can reuse RunScenarios from description_run.go.
	set := &LabelDescriptionSet{
		Layer: []LabeledDescription{{
			Origin: "recipe:" + recipeName,
			Description: Description{
				Feature:  fmt.Sprintf("Recipe %s scenarios", recipeName),
				Scenario: scenarios,
			},
		}},
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

// containerRunningForScoring confirms <containerName> is in the
// running state. The named function avoids a collision with the
// existing podRunning helper in harness_synccreds_cmd.go.
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

// synthesizeRecipeBaseline builds the pre-AI baseline directly from
// recipe.Scenario (instead of from the project clone's layer.yml
// description blocks). All scenarios are marked status: fail —
// nothing's been deployed yet. Fingerprints are computed from the
// scenario YAML so post-iteration fingerprint comparison works
// (the AI doesn't get to mutate recipe scenarios; pre == post).
func synthesizeRecipeBaseline(recipeName string, scenarios []Scenario) ([]ScenarioTestResult, map[string]string, map[string]string) {
	var out []ScenarioTestResult
	fps := make(map[string]string)
	tagFps := make(map[string]string)
	origin := "recipe:" + recipeName
	for sIdx, scenario := range scenarios {
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

// RenderRecipeScenariosYAML returns recipe.Scenario as a YAML block,
// suitable for ${SCENARIOS} substitution in the prompt. The AI sees
// scenarios in the same shape they're authored in (Gherkin-style
// Given/When/Then with embedded Check verbs).
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
