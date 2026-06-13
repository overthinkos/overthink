package main

import (
	"fmt"
	"os"
	"strings"
)

// FeatureCmd groups the `charly feature` authoring + inspection verbs.
// Additional run-verbs live on EvalCmd / ImageCmd as Feature children
// so that `charly eval feature run <deployment>` and `charly box feature run
// <image>` fit the existing test-command hierarchy.
type FeatureCmd struct {
	List     FeatureListCmd     `cmd:"list"     help:"Enumerate every kind: entity and the scenarios declared on its description: block"`
	Pending  FeaturePendingCmd  `cmd:"pending"  help:"List steps with no bound Check (authoring gaps)"`
	Validate FeatureValidateCmd `cmd:"validate" help:"Parse + binding consistency check for description: blocks (called by charly box validate)"`
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
			summarizeDesc("candy", name, layer.Description, layer.scenario)
		}
	}
	if filter == "" || filter == "box" {
		for name, img := range cfg.Box {
			if img.Description != nil || len(img.Scenario) > 0 {
				summarizeDesc("box", name, img.Description, img.Scenario)
			}
		}
	}
	return nil
}

func summarizeDesc(kind, name string, d *Description, scenarios []Scenario) {
	if d == nil && len(scenarios) == 0 {
		fmt.Printf("%s %s: (no description)\n", kind, name)
		return
	}
	feature := "(empty)"
	if d != nil && d.Feature != "" {
		feature = d.Feature
	}
	nScenarios := len(scenarios)
	nSkeleton := 0
	for _, sc := range scenarios {
		for _, t := range sc.Tag {
			if normalizeTag(t) == "skeleton" {
				nSkeleton++
				break
			}
		}
	}
	skel := ""
	if nSkeleton > 0 {
		skel = fmt.Sprintf(" [%d skeleton]", nSkeleton)
	}
	fmt.Printf("%s %s: %q (%d scenario%s%s)\n",
		kind, name, feature, nScenarios, plural(nScenarios), skel)
}

// FeaturePendingCmd: `charly feature pending <entity>`. Lists steps with
// no bound verb (pending) so authors see outstanding work.
type FeaturePendingCmd struct {
	Entity   string `arg:"" optional:"" help:"Entity identifier (e.g. candy:redis); default: all"`
	Skeleton bool   `long:"skeleton" help:"Also list scenarios tagged @skeleton (migration placeholders)"`
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

	scan := func(kind, name string, d *Description, scenarios []Scenario) {
		if d == nil && len(scenarios) == 0 {
			return
		}
		eid := kind + ":" + name
		if filter != "" && filter != eid && filter != kind {
			return
		}
		for _, sc := range scenarios {
			isSkel := false
			for _, t := range sc.Tag {
				if normalizeTag(t) == "skeleton" {
					isSkel = true
					break
				}
			}
			if isSkel && !c.Skeleton {
				continue
			}
			var pendingSteps []int
			for i, step := range sc.Step {
				if step.IsPending() {
					pendingSteps = append(pendingSteps, i)
				}
			}
			if isSkel || len(pendingSteps) > 0 {
				tag := ""
				if isSkel {
					tag = " [@skeleton]"
				}
				fmt.Printf("%s — scenario %q%s\n", eid, sc.Name, tag)
				for _, i := range pendingSteps {
					step := sc.Step[i]
					fmt.Printf("    step %d: %s %q — pending (no verb bound)\n", i, keywordOf(&step), step.KeywordText())
				}
			}
		}
	}

	for name, layer := range layers {
		if layer != nil {
			scan("candy", name, layer.Description, layer.scenario)
		}
	}
	for name, img := range cfg.Box {
		scan("box", name, img.Description, img.Scenario)
	}
	return nil
}

// FeatureValidateCmd: `charly feature validate [<entity>]`. Parses every
// description: block and reports issues. Called automatically by
// `charly box validate` as of the cutover.
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

	validate := func(kind, name string, d *Description, scenarios []Scenario) {
		if d == nil && len(scenarios) == 0 {
			return
		}
		eid := kind + ":" + name
		if filter != "" && filter != eid && filter != kind {
			return
		}
		issues := validateDescriptionSteps(d, scenarios, eid)
		errs = append(errs, issues...)
	}

	for name, layer := range layers {
		if layer != nil {
			validate("candy", name, layer.Description, layer.scenario)
		}
	}
	for name, img := range cfg.Box {
		validate("box", name, img.Description, img.Scenario)
	}

	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, e)
		}
		return fmt.Errorf("%d validation error(s)", len(errs))
	}
	fmt.Println("All description blocks validated successfully.")
	return nil
}

// validateDescriptionSteps runs static checks against a description + its
// top-level scenario list (complementary to ValidateScenarios in
// scenario_validate.go, which validates list structure / depends_on):
//
//   - feature: non-empty
//   - every step binds a keyword OR carries an Op verb
//   - every step's Op passes Op.Kind() (0 or 1 verb; 0 = narrative)
//   - scenario Examples rows cover every <placeholder> used in step
//     text or Op string fields
//
// Returns a list of human-readable error strings (empty if OK).
func validateDescriptionSteps(d *Description, scenarios []Scenario, eid string) []string {
	var errs []string
	if d == nil || strings.TrimSpace(d.Feature) == "" {
		errs = append(errs, fmt.Sprintf("%s: description.feature is empty", eid))
	}
	for sIdx, sc := range scenarios {
		if strings.TrimSpace(sc.Name) == "" {
			errs = append(errs, fmt.Sprintf("%s: scenario %d has empty name", eid, sIdx))
		}
		for stepIdx, step := range sc.Step {
			// A step is one Op: it is valid with a Gherkin keyword (prose, for
			// narrative / agent-grading) OR a verb (a bare deterministic Op step,
			// per the scenario schema) — a keyword is NOT required when a verb is
			// present. Only a step with NEITHER (an empty step) is invalid, and
			// MULTIPLE keywords are always invalid.
			_, kwErr := step.StepKeyword()
			hasVerb := !step.IsPending()
			switch {
			case kwErr != nil && strings.Contains(kwErr.Error(), "multiple"):
				errs = append(errs, fmt.Sprintf("%s: scenario %q step %d: %v", eid, sc.Name, stepIdx, kwErr))
			case kwErr != nil && !hasVerb:
				errs = append(errs, fmt.Sprintf("%s: scenario %q step %d: empty step — needs a Gherkin keyword (given/when/then/and/but) or a verb", eid, sc.Name, stepIdx))
			}
			if hasVerb {
				if _, err := step.Op.Kind(); err != nil {
					errs = append(errs, fmt.Sprintf("%s: scenario %q step %d: %v", eid, sc.Name, stepIdx, err))
				}
			}
		}
		if len(sc.Example) > 0 {
			placeholders := collectPlaceholders(sc)
			for _, row := range sc.Example {
				for ph := range placeholders {
					if _, ok := row[ph]; !ok {
						errs = append(errs, fmt.Sprintf("%s: scenario %q outline row missing placeholder <%s>", eid, sc.Name, ph))
					}
				}
			}
		}
	}
	return errs
}

// collectPlaceholders returns the set of <name> tokens referenced
// anywhere in a scenario's step text or Check string fields.
func collectPlaceholders(sc Scenario) map[string]bool {
	set := map[string]bool{}
	scan := func(s string) {
		for {
			i := strings.IndexByte(s, '<')
			if i < 0 {
				return
			}
			j := strings.IndexByte(s[i+1:], '>')
			if j < 0 {
				return
			}
			name := s[i+1 : i+1+j]
			if name != "" && !strings.ContainsAny(name, " \t") {
				set[name] = true
			}
			s = s[i+1+j+1:]
		}
	}
	for _, step := range sc.Step {
		scan(step.Given)
		scan(step.When)
		scan(step.Then)
		scan(step.And)
		scan(step.But)
		for _, p := range step.Op.StringFields() {
			scan(*p)
		}
	}
	return set
}
