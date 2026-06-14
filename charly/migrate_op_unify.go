package main

// migrate_op_unify.go — `charly migrate`.
//
// 2026-06 Op-vocabulary unification cutover. task: + eval: + agent: collapse
// into ONE generic Op vocabulary, eval: folds into scenario:, and scenario:
// becomes a top-level sibling of description:. Per candy / box / pod / vm
// entity this step:
//
//   - hoists description.scenario: → a top-level scenario: (merged with any
//     existing top-level scenario:), and leaves description: as {feature,
//     narrative, tag};
//   - folds the flat eval: / deploy_eval: CHECK LISTS into scenario: — each
//     check becomes a one-step scenario, EXCEPT a check that is a structural
//     twin of an existing scenario step (same verb + primary value), which is
//     dropped (the de-duplication the cutover exists to remove); a deploy_eval
//     check carries context: [deploy];
//   - rewrites each task: entry's cmd: → command: and run-as user: → run_as:
//     (user: is now the user VERB; a task's run-as identity moved to run_as:);
//   - rewrites every check/step's scope: <s> → context: [<s>];
//   - deletes the now-empty eval: / deploy_eval: keys.
//
// The ROOT harness `eval:` block (a MAPPING of kind:eval bed name → deploy node)
// is NOT a check list and is left untouched — only a SequenceNode eval: (a
// candy/box/pod/vm check list) is folded. Comment-preserving via the yaml.v3
// node API; idempotent (a migrated entity has no eval:/deploy_eval: and no
// description.scenario left); does NOT recurse into box/<distro> submodules
// (separate repos, migrated on their own).

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// MigrateOpUnify folds eval: into scenario:, hoists description.scenario:, and
// rewrites task cmd/user + check scope across a project's candy/ + box/ dirs
// and root YAML siblings. Returns the rewritten file paths. Idempotent.
func MigrateOpUnify(dir string, dryRun bool) ([]string, error) {
	return runDocMigration(dir, dryRun, opUnifyCandidateFiles, opUnifyDoc)
}

// opUnifyDoc transforms every entity reachable in one doc: the candy/box/pod/
// vm/deploy/k8s/local/android entities AND the kind:eval / kind:recipe /
// kind:score / kind:agent nodes inside the root harness `eval:` MAP. Each
// container key may hold a single kind-keyed entity (a `name:` child) or a map
// of name → entity. The root `eval:` KEY itself is a mapping container and is
// never folded — only a node's OWN SequenceNode `eval:` (a check list) folds.
func opUnifyDoc(doc *yaml.Node) bool {
	root := rootMappingNode(doc)
	if root == nil {
		return false
	}
	changed := false
	for _, k := range []string{"candy", "box", "pod", "vm", "deploy", "k8s", "local", "android", "eval", "recipe", "score"} {
		m := findMappingValue(root, k)
		if m == nil || m.Kind != yaml.MappingNode {
			continue
		}
		if findMappingValue(m, "name") != nil {
			if transformNode(m) { // single kind-keyed entity
				changed = true
			}
			continue
		}
		for i := 0; i+1 < len(m.Content); i += 2 { // map of name → entity
			if transformNode(m.Content[i+1]) {
				changed = true
			}
		}
	}
	return changed
}

// transformNode applies the op-unify rewrites to one entity mapping and
// recurses into its nested: / peer: deployment children.
func transformNode(m *yaml.Node) bool {
	if m == nil || m.Kind != yaml.MappingNode {
		return false
	}
	changed := transformEntity(m)
	// Recipe from: entries lost their scope: / skip_live_only: filters.
	if from := findMappingValue(m, "from"); from != nil && from.Kind == yaml.SequenceNode {
		for _, entry := range from.Content {
			if entry.Kind != yaml.MappingNode {
				continue
			}
			if removeMappingKey(entry, "scope") {
				changed = true
			}
			if removeMappingKey(entry, "skip_live_only") {
				changed = true
			}
		}
	}
	// Descend into nested deployment children (target:pod/local children, peers).
	for _, k := range []string{"nested", "peer"} {
		if child := findMappingValue(m, k); child != nil && child.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(child.Content); i += 2 {
				if transformNode(child.Content[i+1]) {
					changed = true
				}
			}
		}
	}
	return changed
}

// transformEntity applies the op-unify rewrites to one entity mapping (no
// recursion — transformNode handles children).
func transformEntity(m *yaml.Node) bool {
	if m == nil || m.Kind != yaml.MappingNode {
		return false
	}
	changed := false

	// 1. Hoist description.scenario → entity scenario:.
	if desc := findMappingValue(m, "description"); desc != nil && desc.Kind == yaml.MappingNode {
		if sc := findMappingValue(desc, "scenario"); sc != nil && sc.Kind == yaml.SequenceNode {
			appendScenarios(m, sc.Content)
			removeMappingKey(desc, "scenario")
			changed = true
		}
	}

	// 2. task: entries — cmd: → command:, run-as user: → run_as:.
	if task := findMappingValue(m, "task"); task != nil && task.Kind == yaml.SequenceNode {
		for _, entry := range task.Content {
			if entry.Kind != yaml.MappingNode {
				continue
			}
			if renameMappingKey(entry, "cmd", "command") {
				changed = true
			}
			if renameMappingKey(entry, "user", "run_as") {
				changed = true
			}
		}
	}

	// 3. Fold eval: / deploy_eval: CHECK LISTS (sequences only) into scenario:.
	//    Every check folds, preserving its id: as the scenario name — references
	//    to those ids (recipe select:/exclude:, scenario depends_on:, including
	//    cross-repo into the box submodules) stay valid. De-duplicating a check
	//    that twins a description scenario step is deliberately NOT done here: it
	//    would drop a name a reference points at. That twin-coverage cleanup is a
	//    reference-aware per-candy follow-up, not a reference-blind migration.
	for _, ev := range []struct {
		key     string
		context string
	}{{"eval", ""}, {"deploy_eval", "deploy"}} {
		seq := findMappingValue(m, ev.key)
		if seq == nil || seq.Kind != yaml.SequenceNode {
			continue // a MappingNode here is the root harness block — never folded
		}
		var folded []*yaml.Node
		for _, check := range seq.Content {
			if check.Kind != yaml.MappingNode {
				continue
			}
			scopeToContext(check, ev.context)
			folded = append(folded, checkToScenario(check))
		}
		if len(folded) > 0 {
			appendScenarios(m, folded)
		}
		removeMappingKey(m, ev.key)
		changed = true
	}

	// 4. scope: → context: on every (now top-level) scenario step.
	if sc := findMappingValue(m, "scenario"); sc != nil && sc.Kind == yaml.SequenceNode {
		for _, scen := range sc.Content {
			if scen.Kind != yaml.MappingNode {
				continue
			}
			for _, stepKey := range []string{"step", "setup", "teardown", "on_fail"} {
				steps := findMappingValue(scen, stepKey)
				if steps == nil || steps.Kind != yaml.SequenceNode {
					continue
				}
				for _, step := range steps.Content {
					if step.Kind == yaml.MappingNode && scopeToContext(step, "") {
						changed = true
					}
				}
			}
		}
	}
	return changed
}

// appendScenarios appends scenario nodes to the entity's top-level scenario:
// sequence, creating the key when absent.
func appendScenarios(m *yaml.Node, scenarios []*yaml.Node) {
	if len(scenarios) == 0 {
		return
	}
	sc := findMappingValue(m, "scenario")
	if sc == nil {
		key := &yaml.Node{Kind: yaml.ScalarNode, Value: "scenario"}
		sc = &yaml.Node{Kind: yaml.SequenceNode}
		m.Content = append(m.Content, key, sc)
	}
	if sc.Kind != yaml.SequenceNode {
		return
	}
	sc.Content = append(sc.Content, scenarios...)
}

// checkToScenario wraps a flat check map as a one-step scenario node. The
// scenario name is the check's id: (preferred) or a verb-derived fallback; the
// check map itself becomes the single step (it already carries the verb inline).
func checkToScenario(check *yaml.Node) *yaml.Node {
	name := ""
	if id := findMappingValue(check, "id"); id != nil && id.Kind == yaml.ScalarNode {
		name = id.Value
	}
	if name == "" {
		if v := opPrimaryKey(check); v != "" {
			name = strings.NewReplacer("/", "-", " ", "-", ":", "-").Replace(v)
		} else {
			name = "check"
		}
	}
	scen := &yaml.Node{Kind: yaml.MappingNode}
	scen.Content = append(scen.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "name"},
		&yaml.Node{Kind: yaml.ScalarNode, Value: name},
		&yaml.Node{Kind: yaml.ScalarNode, Value: "step"},
		&yaml.Node{Kind: yaml.SequenceNode, Content: []*yaml.Node{check}},
	)
	return scen
}

// scopeToContext rewrites a node's scope: <s> → context: [<mapped>], or — when
// force is non-empty and no scope: is present — injects context: [<mapped>]
// (used to stamp deploy_eval checks). Returns whether it changed.
func scopeToContext(node *yaml.Node, force string) bool {
	if node.Kind != yaml.MappingNode {
		return false
	}
	if sv := findMappingValue(node, "scope"); sv != nil && sv.Kind == yaml.ScalarNode {
		val := sv.Value
		removeMappingKey(node, "scope")
		setContextSeq(node, scopeToCtxValue(val))
		return true
	}
	if force != "" && findMappingValue(node, "context") == nil {
		setContextSeq(node, scopeToCtxValue(force))
		return true
	}
	return false
}

// scopeToCtxValue maps the legacy two-value scope: onto the four-value context:.
// The crux: old `scope: deploy` checks ran via `charly eval live` against the
// RUNNING deployment — which is the `runtime` context (a running target), NOT
// `deploy` (provisioning, where no running container exists to probe). `build`
// maps straight through (the disposable build-container probe).
func scopeToCtxValue(scope string) string {
	if scope == "deploy" {
		return "runtime"
	}
	return scope
}

// setContextSeq sets context: [<val>] on a mapping node (replacing any existing).
func setContextSeq(node *yaml.Node, val string) {
	removeMappingKey(node, "context")
	seq := &yaml.Node{Kind: yaml.SequenceNode, Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Value: val},
	}}
	node.Content = append(node.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "context"}, seq)
}

// opVerbKeys is the verb-discriminator priority used to derive a check's
// primary value for a synthesized scenario name. First match wins.
var opVerbKeys = []string{
	"file", "package", "service", "port", "process", "command", "http", "dns",
	"user", "group", "interface", "kernel-param", "mount", "addr",
	"cdp", "wl", "dbus", "vnc", "mcp", "record", "spice", "libvirt", "k8s",
	"adb", "appium", "mkdir", "copy", "write", "link", "download", "setcap",
}

// opPrimaryKey returns a "<verb>=<value>" structural key for a check/step map,
// or "" when no recognized verb is present.
func opPrimaryKey(node *yaml.Node) string {
	if node == nil || node.Kind != yaml.MappingNode {
		return ""
	}
	for _, verb := range opVerbKeys {
		if v := findMappingValue(node, verb); v != nil && v.Kind == yaml.ScalarNode && v.Value != "" {
			return verb + "=" + v.Value
		}
	}
	return ""
}

// opUnifyCandidateFiles returns the project YAML files that can carry a candy /
// box / pod / vm entity with eval:/task:/description.scenario: candy/<name>/
// charly.yml + box/<name>/charly.yml + root-level YAML siblings. Skips the
// box/<distro> submodules (separate repos) and self-migrates the repo's own
// charly/testdata fixtures. Sorted, deduplicated.
func opUnifyCandidateFiles(dir string) []string {
	seen := map[string]struct{}{}
	addYAMLTree := func(root string) {
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if isGitSubmoduleDir(p, root) {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(p, ".yml") || strings.HasSuffix(p, ".yaml") {
				seen[filepath.Clean(p)] = struct{}{}
			}
			return nil
		})
	}
	addYAMLTree(filepath.Join(dir, "candy"))
	addYAMLTree(filepath.Join(dir, "box"))
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && (strings.HasSuffix(e.Name(), ".yml") || strings.HasSuffix(e.Name(), ".yaml")) {
				seen[filepath.Clean(filepath.Join(dir, e.Name()))] = struct{}{}
			}
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "charly", "go.mod")); err == nil {
		addYAMLTree(filepath.Join(dir, "charly", "testdata"))
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sortStrings(out)
	return out
}
