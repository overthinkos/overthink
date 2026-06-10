package main

// scaffold_scenario.go — `charly candy add-scenario`: idempotently append a
// Gherkin scenario to a candy's `description.scenario` list.
//
// This is the SPECIFY-stage authoring affordance of Agent Driven Evaluation:
// it lets a human or an agent (over MCP, via the auto-reflected
// candy.add-scenario tool) add an acceptance scenario without hand-editing
// YAML or clobbering the existing list — the scenario sibling of
// `charly candy add-rpm` (idempotent append, comment-preserving yaml.Node API).
//
// Scenarios belong on the CANDY that provides the behaviour: a scenario
// authored here bakes into the ai.opencharly.description label of EVERY
// box that composes the candy (CollectDescriptions walks the candy chain),
// so one scenario covers all consumers (R3) — no per-box duplication. The
// helper authors PROSE steps (the agent-graded path); deterministic check
// verbs are added afterward by editing the step, surfaced by
// `charly feature pending`.

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// CandyAddScenarioCmd: `charly candy add-scenario <candy> <name> [--given … --when … --then …]`.
type CandyAddScenarioCmd struct {
	Name     string   `arg:"" help:"Candy name (under candy/)"`
	Scenario string   `arg:"" name:"scenario" help:"Scenario name (idempotent: a no-op if a scenario with this name already exists)"`
	Given    []string `long:"given" help:"Given step text (repeatable; the scenario's preconditions)"`
	When     []string `long:"when" help:"When step text (repeatable; the action)"`
	Then     []string `long:"then" help:"Then step text (repeatable; the expected behaviour)"`
	Pod      string   `long:"pod" help:"Container the scenario probes (recipe scenarios only)"`
	Tag      []string `long:"tag" help:"Scenario tag (repeatable)"`
}

func (c *CandyAddScenarioCmd) Run() error {
	if len(c.Given) == 0 && len(c.When) == 0 && len(c.Then) == 0 {
		return fmt.Errorf("a scenario needs at least one --given/--when/--then step")
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	candyYml := filepath.Join(dir, DefaultCandyDir, c.Name, UnifiedFileName)
	added, err := appendCandyScenario(candyYml, c.Scenario, c.Given, c.When, c.Then, c.Tag, c.Pod)
	if err != nil {
		return err
	}
	if !added {
		fmt.Fprintf(os.Stderr, "scenario %q already present in candy %q — no change\n", c.Scenario, c.Name)
		return nil
	}
	fmt.Fprintf(os.Stderr, "added scenario %q to candy %q\n", c.Scenario, c.Name)
	return nil
}

// appendCandyScenario appends one scenario to <candyYml>'s
// candy.description.scenario list (creating the description / scenario nodes
// as needed) and writes the file back, preserving comments via the yaml.Node
// API. Returns added=false (no write) when a scenario of the same name is
// already present (idempotent).
func appendCandyScenario(candyYml, name string, given, when, then, tags []string, pod string) (bool, error) {
	if _, err := os.Stat(candyYml); err != nil {
		return false, fmt.Errorf("candy manifest not found at %s", candyYml)
	}
	data, err := os.ReadFile(candyYml)
	if err != nil {
		return false, fmt.Errorf("reading %s: %w", candyYml, err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return false, fmt.Errorf("parsing %s: %w", candyYml, err)
	}
	candyNode, err := candyBodyNode(&root)
	if err != nil {
		return false, fmt.Errorf("%s: %w", candyYml, err)
	}
	descNode := ensureMappingChild(candyNode, "description")
	scenarioSeq := mappingChild(descNode, "scenario")
	if scenarioSeq == nil {
		scenarioSeq = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		descNode.Content = append(descNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "scenario"},
			scenarioSeq,
		)
	} else if scenarioSeq.Kind != yaml.SequenceNode {
		scenarioSeq.Kind = yaml.SequenceNode
		scenarioSeq.Tag = "!!seq"
		scenarioSeq.Value = ""
		scenarioSeq.Content = nil
	}

	// Idempotency: skip if a scenario with this name already exists.
	for _, sc := range scenarioSeq.Content {
		if sc.Kind == yaml.MappingNode {
			if n := mappingChild(sc, "name"); n != nil && n.Value == name {
				return false, nil
			}
		}
	}

	scenarioSeq.Content = append(scenarioSeq.Content, buildScenarioNode(name, given, when, then, tags, pod))

	out, err := yaml.Marshal(&root)
	if err != nil {
		return false, fmt.Errorf("marshalling %s: %w", candyYml, err)
	}
	return true, os.WriteFile(candyYml, out, 0o644)
}

// candyBodyNode returns the `candy:` kind-wrapper mapping from a parsed candy
// manifest root, descending through the document node. Candy manifests are
// kind-keyed under `candy:` (the candy kind key); every body-relative edit —
// package sections, scenarios, scalar fields — lives INSIDE that wrapper, not
// at the document root. Shared by the candy authoring helpers (add-scenario,
// add-rpm/deb/pac/aur) so they agree on where the body lives (R3).
func candyBodyNode(root *yaml.Node) (*yaml.Node, error) {
	doc := root
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		doc = doc.Content[0]
	}
	if doc.Kind != yaml.MappingNode {
		// Empty file or scalar root — synthesise the candy wrapper.
		doc.Kind = yaml.MappingNode
		doc.Tag = "!!map"
		doc.Content = nil
	}
	candy := mappingChild(doc, "candy")
	if candy == nil {
		return nil, fmt.Errorf("not a kind-keyed candy manifest (no `candy:`)")
	}
	return candy, nil
}

// ensureMappingChild returns the named child mapping of m, creating an empty
// mapping (with key) when absent.
func ensureMappingChild(m *yaml.Node, key string) *yaml.Node {
	if child := mappingChild(m, key); child != nil {
		return child
	}
	child := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		child,
	)
	return child
}

// buildScenarioNode constructs the yaml.Node for one scenario.
func buildScenarioNode(name string, given, when, then, tags []string, pod string) *yaml.Node {
	scalar := func(v string) *yaml.Node {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
	}
	sc := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	sc.Content = append(sc.Content, scalar("name"), scalar(name))
	if len(tags) > 0 {
		seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, t := range tags {
			seq.Content = append(seq.Content, scalar(t))
		}
		sc.Content = append(sc.Content, scalar("tag"), seq)
	}
	if pod != "" {
		sc.Content = append(sc.Content, scalar("pod"), scalar(pod))
	}
	stepSeq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	addStep := func(keyword, text string) {
		step := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		step.Content = append(step.Content, scalar(keyword), scalar(text))
		stepSeq.Content = append(stepSeq.Content, step)
	}
	for _, g := range given {
		addStep("given", g)
	}
	for _, w := range when {
		addStep("when", w)
	}
	for _, t := range then {
		addStep("then", t)
	}
	sc.Content = append(sc.Content, scalar("step"), stepSeq)
	return sc
}
