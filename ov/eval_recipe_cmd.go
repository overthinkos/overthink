package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// EvalRecipeCmd implements `ov eval recipe <name>` — runs all scenarios
// in the named recipe ONCE against the live deployments declared via
// scenario.pod, without the AI iteration loop. Differs from `ov eval run`
// (which drives an AI through plateau-bounded iterations against a score)
// in that this verb is purely a deterministic scenario evaluator: load,
// run, report.
//
// Each scenario's `pod:` field selects the container the steps probe.
// Flat pod names like `jupyter-concurrency-test` resolve to the container
// `ov-jupyter-concurrency-test` via ContainerChain (the same fallback
// path RunEvalLive uses for non-dotted names). Slash- or dot-form pod
// names (e.g. `jupyter/concurrency-test`) walk the deploy tree.
type EvalRecipeCmd struct {
	Name   string `arg:"" help:"Recipe name (from eval.yml recipe: map)"`
	Format string `long:"format" default:"text" help:"Output format: text, json, tap"`
	Strict bool   `long:"strict" help:"Fail the run if any step is pending (no verb bound)"`
}

func (c *EvalRecipeCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	uf, _, err := LoadUnified(dir)
	if err != nil {
		return err
	}
	if uf == nil || len(uf.Recipe) == 0 {
		return fmt.Errorf("eval recipe: no recipes configured (add a `recipe:` map to eval.yml)")
	}

	recipe, err := ResolveRecipe(uf.Recipe, c.Name)
	if err != nil {
		return err
	}

	// Recipes can declare `from:` imports that materialize into synthetic
	// scenarios. ExpandRecipeFrom mutates the recipe in place; for recipes
	// without `from:` (the common case for hand-authored concurrency
	// recipes) it's a no-op and the layers map is unused.
	if err := ExpandRecipeFrom(uf, nil, c.Name, recipe); err != nil {
		return fmt.Errorf("eval recipe: expanding from imports: %w", err)
	}
	scenarios := recipe.Scenario
	if len(scenarios) == 0 {
		return fmt.Errorf("eval recipe %q: no scenarios", c.Name)
	}

	// Dispatch via RunEvalLive — the scenario-by-scenario probe driver.
	res, err := RunEvalLive(context.Background(), "", c.Name, scenarios, RunScoringOpts{})
	if err != nil {
		return fmt.Errorf("eval recipe %q: %w", c.Name, err)
	}

	// Format output.
	switch c.Format {
	case "json":
		out, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(out))
	case "tap":
		printRecipeTAP(res)
	default:
		printRecipeText(res)
	}

	// Exit non-zero if any scenario failed.
	for _, s := range res.Scenario {
		if s.Status == "fail" {
			os.Exit(1)
		}
	}
	return nil
}

// printRecipeText renders a per-scenario summary suitable for a terminal.
func printRecipeText(res *EvalRunResults) {
	if res == nil {
		return
	}
	// Sort scenario results for stable output.
	sorted := make([]ScenarioEvalResult, len(res.Scenario))
	copy(sorted, res.Scenario)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Origin != sorted[j].Origin {
			return sorted[i].Origin < sorted[j].Origin
		}
		return sorted[i].Name < sorted[j].Name
	})

	pass, fail, skip := 0, 0, 0
	for _, s := range sorted {
		marker := "✓"
		switch s.Status {
		case "fail":
			marker = "✗"
			fail++
		case "skip", "skipped":
			marker = "·"
			skip++
		default:
			pass++
		}
		fmt.Printf("  %s %-40s origin=%s\n", marker, s.Name, s.Origin)
		// Surface failed step text (StepEvalResult doesn't carry message).
		if s.Status == "fail" {
			for _, step := range s.Steps {
				if step.Status == "fail" {
					fmt.Printf("        FAIL: %s (%s)\n", step.Text, step.Verb)
				}
			}
		}
	}
	fmt.Printf("\n%d passed  %d failed  %d skipped  (total %d)\n",
		pass, fail, skip, len(sorted))
}

// printRecipeTAP emits TAP12-compatible output for CI consumption.
func printRecipeTAP(res *EvalRunResults) {
	if res == nil {
		fmt.Println("1..0")
		return
	}
	fmt.Printf("1..%d\n", len(res.Scenario))
	for i, s := range res.Scenario {
		marker := "ok"
		if s.Status == "fail" {
			marker = "not ok"
		}
		fmt.Printf("%s %d - %s\n", marker, i+1, s.Name)
	}
}
