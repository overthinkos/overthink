package main

import (
	"fmt"
	"os"
	"strings"
)

// FeatureCmd groups the `ov feature` authoring + inspection verbs.
// Additional run-verbs live on EvalCmd / ImageCmd as Feature children
// so that `ov eval feature run <deployment>` and `ov image feature run
// <image>` fit the existing test-command hierarchy.
type FeatureCmd struct {
	List     FeatureListCmd     `cmd:"list"     help:"Enumerate every kind: entity and the scenarios declared on its description: block"`
	Pending  FeaturePendingCmd  `cmd:"pending"  help:"List steps with no bound Check (authoring gaps)"`
	Validate FeatureValidateCmd `cmd:"validate" help:"Parse + binding consistency check for description: blocks (called by ov image validate)"`
}

// FeatureListCmd: `ov feature list [<kind>]`. Walks the resolved
// project config and prints each entity's description summary.
type FeatureListCmd struct {
	Kind string `arg:"" optional:"" help:"Restrict to one kind (layer|image|pod|vm|k8s|host|deployment). Default: all."`
}

// Run executes `ov feature list`.
func (c *FeatureListCmd) Run() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	cfg, err := LoadConfig(cwd)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	layers, err := ScanLayer(cwd)
	if err != nil {
		return fmt.Errorf("scanning layers: %w", err)
	}

	filter := strings.ToLower(strings.TrimSpace(c.Kind))

	if filter == "" || filter == "layer" {
		for name, layer := range layers {
			if layer == nil {
				continue
			}
			summarizeDesc("layer", name, layer.description)
		}
	}
	if filter == "" || filter == "image" {
		for name, img := range cfg.Image {
			if img.Description != nil {
				summarizeDesc("image", name, img.Description)
			}
		}
	}
	return nil
}

func summarizeDesc(kind, name string, d *Description) {
	if d == nil {
		fmt.Printf("%s %s: (no description)\n", kind, name)
		return
	}
	feature := d.Feature
	if feature == "" {
		feature = "(empty)"
	}
	nScenarios := len(d.Scenario)
	nSkeleton := 0
	for _, sc := range d.Scenario {
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

// FeaturePendingCmd: `ov feature pending <entity>`. Lists steps with
// no bound verb (pending) so authors see outstanding work.
type FeaturePendingCmd struct {
	Entity   string `arg:"" optional:"" help:"Entity identifier (e.g. layer:redis); default: all"`
	Skeleton bool   `long:"skeleton" help:"Also list scenarios tagged @skeleton (migration placeholders)"`
}

// Run executes `ov feature pending`.
func (c *FeaturePendingCmd) Run() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	cfg, err := LoadConfig(cwd)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	layers, err := ScanLayer(cwd)
	if err != nil {
		return fmt.Errorf("scanning layers: %w", err)
	}

	filter := strings.ToLower(strings.TrimSpace(c.Entity))

	scan := func(kind, name string, d *Description) {
		if d == nil {
			return
		}
		eid := kind + ":" + name
		if filter != "" && filter != eid && filter != kind {
			return
		}
		for _, sc := range d.Scenario {
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
			scan("layer", name, layer.description)
		}
	}
	for name, img := range cfg.Image {
		scan("image", name, img.Description)
	}
	return nil
}

// FeatureValidateCmd: `ov feature validate [<entity>]`. Parses every
// description: block and reports issues. Called automatically by
// `ov image validate` as of the cutover.
type FeatureValidateCmd struct {
	Entity string `arg:"" optional:"" help:"Entity identifier (e.g. layer:redis); default: all"`
}

// Run executes `ov feature validate`.
func (c *FeatureValidateCmd) Run() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	cfg, err := LoadConfig(cwd)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	layers, err := ScanLayer(cwd)
	if err != nil {
		return fmt.Errorf("scanning layers: %w", err)
	}

	filter := strings.ToLower(strings.TrimSpace(c.Entity))
	var errs []string

	validate := func(kind, name string, d *Description) {
		if d == nil {
			return
		}
		eid := kind + ":" + name
		if filter != "" && filter != eid && filter != kind {
			return
		}
		issues := ValidateDescription(d, eid)
		errs = append(errs, issues...)
	}

	for name, layer := range layers {
		if layer != nil {
			validate("layer", name, layer.description)
		}
	}
	for name, img := range cfg.Image {
		validate("image", name, img.Description)
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

// ValidateDescription runs static checks against a description:
//
//   - feature: non-empty
//   - every step has exactly one given/when/then/and/but
//   - every step Check passes Check.Kind() (0 or 1 verb; 0 = pending)
//   - scenario Examples rows cover every <placeholder> used in step
//     text or Check string fields
//
// Returns a list of human-readable error strings (empty if OK).
func ValidateDescription(d *Description, eid string) []string {
	if d == nil {
		return nil
	}
	var errs []string
	if strings.TrimSpace(d.Feature) == "" {
		errs = append(errs, fmt.Sprintf("%s: description.feature is empty", eid))
	}
	for sIdx, sc := range d.Scenario {
		if strings.TrimSpace(sc.Name) == "" {
			errs = append(errs, fmt.Sprintf("%s: scenario %d has empty name", eid, sIdx))
		}
		for stepIdx, step := range sc.Step {
			if _, err := step.StepKeyword(); err != nil {
				errs = append(errs, fmt.Sprintf("%s: scenario %q step %d: %v", eid, sc.Name, stepIdx, err))
			}
			if !step.IsPending() {
				if _, err := step.Check.Kind(); err != nil {
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
		for _, p := range step.Check.StringFields() {
			scan(*p)
		}
	}
	return set
}
