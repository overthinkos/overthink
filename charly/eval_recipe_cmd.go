package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// EvalRecipeCmd implements `charly eval recipe <name>` — runs all scenarios
// in the named recipe ONCE against the live deployments declared via
// scenario.pod, without the AI iteration loop. Differs from `charly eval run`
// (which drives an AI through plateau-bounded iterations against a score)
// in that this verb is purely a deterministic scenario evaluator: load,
// run, report.
//
// Each scenario's `pod:` field selects the container the steps probe.
// Flat pod names like `jupyter-concurrency-test` resolve to the container
// `charly-jupyter-concurrency-test` via ContainerChain (the same fallback
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
			for _, step := range s.Step {
				if step.Status == "fail" {
					fmt.Printf("        FAIL: %s (%s)\n", step.Text, step.Verb)
				}
			}
		}
	}
	fmt.Printf("\n%d passed  %d failed  %d skipped  (total %d)\n",
		pass, fail, skip, len(sorted))
}

// printRecipeTAP emits TAP v13 output for CI consumption. Each scenario
// is one top-level test; failures carry a YAML diagnostic block listing
// the failed step text + verb so `prove` / `tap-parser` consumers see
// exactly which step regressed without re-running the recipe.
func printRecipeTAP(res *EvalRunResults) {
	fmt.Println("TAP version 13")
	if res == nil {
		fmt.Println("1..0")
		return
	}
	fmt.Printf("1..%d\n", len(res.Scenario))
	for i, s := range res.Scenario {
		num := i + 1
		switch s.Status {
		case "pass":
			fmt.Printf("ok %d - %s\n", num, s.Name)
		case "skip", "skipped":
			fmt.Printf("ok %d - %s # SKIP\n", num, s.Name)
		case "fail":
			fmt.Printf("not ok %d - %s\n", num, s.Name)
			// YAML diagnostic block — TAP v13 spec.
			fmt.Println("  ---")
			fmt.Printf("  origin: %q\n", s.Origin)
			if len(s.Step) > 0 {
				fmt.Println("  failed_steps:")
				for _, step := range s.Step {
					if step.Status == "fail" {
						fmt.Printf("    - text: %q\n", step.Text)
						fmt.Printf("      verb: %q\n", step.Verb)
						fmt.Printf("      step_id: %q\n", step.StepID)
					}
				}
			}
			fmt.Println("  ...")
		default:
			// Unknown status — emit not ok with the raw status as a directive.
			fmt.Printf("not ok %d - %s # %s\n", num, s.Name, s.Status)
		}
	}
}
