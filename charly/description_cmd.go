package main

import (
	"fmt"
	"os"
	"strings"
)

// FeatureCmd groups the `charly feature` authoring + inspection verbs.
// Additional run-verbs live on CheckCmd / ImageCmd as Feature children
// so that `charly check feature run <deployment>` and `charly box feature run
// <image>` fit the existing test-command hierarchy.
type FeatureCmd struct {
	List     FeatureListCmd     `cmd:"list"     help:"Enumerate every kind: entity and its plan: steps"`
	Pending  FeaturePendingCmd  `cmd:"pending"  help:"List agent-graded plan steps (agent-run:/agent-check:)"`
	Validate FeatureValidateCmd `cmd:"validate" help:"Parse + binding consistency check for plan: blocks (called by charly box validate)"`
}

// FeatureListCmd: `charly feature list [<kind>]`. Walks the resolved
// project config and prints each entity's description summary.
type FeatureListCmd struct {
	Kind string `arg:"" optional:"" help:"Restrict to one kind (candy|box). Default: all."`
}

// Run executes `charly feature list`.
func (c *FeatureListCmd) Run() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	cfg, err := LoadConfig(cwd)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	layers, err := ScanCandy(cwd)
	if err != nil {
		return fmt.Errorf("scanning candies: %w", err)
	}

	filter := strings.ToLower(strings.TrimSpace(c.Kind))

	if filter == "" || filter == "candy" {
		for name, layer := range layers {
			if layer == nil {
				continue
			}
			summarizeDesc("candy", name, layer.Description, layer.plan)
		}
	}
	if filter == "" || filter == "box" {
		for name, img := range cfg.Box {
			if img.Description != "" || len(img.Plan) > 0 {
				summarizeDesc("box", name, img.Description, img.Plan)
			}
		}
	}
	return nil
}

func summarizeDesc(kind, name string, desc string, plan []Step) {
	if desc == "" && len(plan) == 0 {
		fmt.Printf("%s %s: (no description)\n", kind, name)
		return
	}
	summary := "(empty)"
	if s := descriptionInfo(desc); s != "" {
		summary = s
	}
	nChecks := 0
	for _, st := range plan {
		if st.Check != "" || st.AgentCheck != "" {
			nChecks++
		}
	}
	fmt.Printf("%s %s: %q (%d step%s, %d check%s)\n",
		kind, name, summary, len(plan), plural(len(plan)), nChecks, plural(nChecks))
}

// FeaturePendingCmd: `charly feature pending <entity>`. Lists agent-graded
// plan steps (the non-deterministic ones), so authors see what relies on an
// agent grader rather than a deterministic check.
type FeaturePendingCmd struct {
	Entity string `arg:"" optional:"" help:"Entity identifier (e.g. candy:redis); default: all"`
}

// Run executes `charly feature pending`.
func (c *FeaturePendingCmd) Run() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	cfg, err := LoadConfig(cwd)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	layers, err := ScanCandy(cwd)
	if err != nil {
		return fmt.Errorf("scanning candies: %w", err)
	}

	filter := strings.ToLower(strings.TrimSpace(c.Entity))

	scan := func(kind, name string, desc string, plan []Step) {
		if desc == "" && len(plan) == 0 {
			return
		}
		eid := kind + ":" + name
		if filter != "" && filter != eid && filter != kind {
			return
		}
		for i := range plan {
			step := plan[i]
			if step.IsAgent() {
				fmt.Printf("%s — step %d: %s %q (agent-graded)\n", eid, i, keywordOf(&step), step.KeywordText())
			}
		}
	}

	for name, layer := range layers {
		if layer != nil {
			scan("candy", name, layer.Description, layer.plan)
		}
	}
	for name, img := range cfg.Box {
		scan("box", name, img.Description, img.Plan)
	}
	return nil
}

// FeatureValidateCmd: `charly feature validate [<entity>]`. Parses every
// plan: block and reports issues. Called automatically by `charly box validate`.
type FeatureValidateCmd struct {
	Entity string `arg:"" optional:"" help:"Entity identifier (e.g. candy:redis); default: all"`
}

// Run executes `charly feature validate`.
func (c *FeatureValidateCmd) Run() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	cfg, err := LoadConfig(cwd)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	layers, err := ScanCandy(cwd)
	if err != nil {
		return fmt.Errorf("scanning candies: %w", err)
	}

	filter := strings.ToLower(strings.TrimSpace(c.Entity))
	var errs []string

	validate := func(kind, name string, desc string, plan []Step) {
		if desc == "" && len(plan) == 0 {
			return
		}
		eid := kind + ":" + name
		if filter != "" && filter != eid && filter != kind {
			return
		}
		errs = append(errs, validatePlanSteps(desc, plan, eid)...)
	}

	for name, layer := range layers {
		if layer != nil {
			validate("candy", name, layer.Description, layer.plan)
		}
	}
	for name, img := range cfg.Box {
		validate("box", name, img.Description, img.Plan)
	}

	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, e)
		}
		return fmt.Errorf("%d validation error(s)", len(errs))
	}
	fmt.Println("All plan blocks validated successfully.")
	return nil
}

// validatePlanSteps runs static checks against a description + its plan steps
// (complementary to ValidatePlan in step_validate.go, which validates list
// structure / depends_on):
//
//   - description non-empty
//   - every step has exactly one keyword (StepKind())
//   - run/check steps carry exactly one Op verb; agent-* steps carry none
//
// Returns a list of human-readable error strings (empty if OK).
func validatePlanSteps(desc string, plan []Step, eid string) []string {
	var errs []string
	if strings.TrimSpace(desc) == "" {
		errs = append(errs, fmt.Sprintf("%s: description is empty", eid))
	}
	for i := range plan {
		step := plan[i]
		kw, err := step.StepKind()
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: step %d: %v", eid, i, err))
			continue
		}
		switch kw {
		case KwRun, KwCheck:
			if _, verbErr := step.Op.Kind(); verbErr != nil {
				errs = append(errs, fmt.Sprintf("%s: step %d (%s): %v", eid, i, kw, verbErr))
			}
		case KwAgentRun, KwAgentCheck:
			if _, verbErr := step.Op.Kind(); verbErr == nil {
				errs = append(errs, fmt.Sprintf("%s: step %d (%s): agent steps must not carry an Op verb", eid, i, kw))
			}
		}
	}
	return errs
}
