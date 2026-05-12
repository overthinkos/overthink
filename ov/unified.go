package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// -----------------------------------------------------------------------------
// Unified YAML Format — Parts B/C/D/E of the refactor plan.
//
// A single `overthink.yml` (+ optional companion files via `includes:`) carries
// everything today's four files carry:
//   - build.yml    → distros: + builders: + inits:
//   - image.yml    → defaults: + images:
//   - deploy.yml   → deployments:
//   - layer.yml    → layers: map entries, or discovered via discover.layers:
//
// Design is described in /home/atrawog/.claude/plans/can-you-make-a-deep-cerf.md.
// Key properties:
//   - plural top-level keys (no kind:/apiVersion: discriminator at root);
//   - includes: for file composition with deep-merge root-wins semantics;
//   - discover: for recursive directory scan of kind-keyed standalone files;
//   - every file is read as a multi-document YAML stream so bundles of
//     kind-keyed entities (`---` separated) work naturally.
// -----------------------------------------------------------------------------

// UnifiedFileName is the canonical root file of the unified format.
const UnifiedFileName = "overthink.yml"

// schemaVersion is the on-disk overthink.yml schema version. Bumped on
// each hard-cutover migration; the LoadUnified gate refuses anything
// older with a hint pointing at `ov migrate schema-v4`.
const schemaVersion = 4

// MaxIncludeDepth caps recursive include resolution. A cycle or excessive depth
// raises a clear error with the offending file path.
const MaxIncludeDepth = 8

// UnifiedFile is the full schema of a single unified-format YAML document.
// Every field is optional — a file with only `distros:` is valid (typical for
// a build.yml-style include); a file with only `deployments:` is valid (typical
// for a deploy.yml-style include); etc.
//
// Schema version 2 consolidates the legacy vms.yml + deploy.yml split into one
// deploy.yml file carrying both `vm:` (singular) and `deployments:` at the
// root. The top-level `vm:` key replaces the legacy `vms:` (plural). See
// `ov migrate merge-vms` for the one-shot migration from v1.
type UnifiedFile struct {
	Version  int                    `yaml:"version,omitempty"`
	Include  []string               `yaml:"include,omitempty"`
	Discover *DiscoverConfig        `yaml:"discover,omitempty"`
	Distro   map[string]*DistroDef  `yaml:"distro,omitempty"`
	Builder  map[string]*BuilderDef `yaml:"builder,omitempty"`
	Init     map[string]*InitDef    `yaml:"init,omitempty"`
	Defaults ImageConfig            `yaml:"defaults,omitempty"`
	// Field-singular cutover (2026-05): legacy plural `Images yaml:"images"`
	// deleted; the singular `Image yaml:"image"` is the canonical surface.
	Image map[string]ImageConfig  `yaml:"image,omitempty"`
	Layer map[string]*InlineLayer `yaml:"layer,omitempty"`
	VM    map[string]*VmSpec      `yaml:"vm,omitempty"`
	// Field-singular cutover: legacy `Deploys *DeploymentsSection
	// yaml:"deployments"` deleted. The flat `Deploy yaml:"deploy"` map is
	// the canonical singular surface; the wrapper's `Provides` migrates
	// to UnifiedFile root (next field).
	Deploy   map[string]DeploymentNode `yaml:"deploy,omitempty"`
	Provides *ProvidesConfig           `yaml:"provides,omitempty"`

	// Schema v4: first-class target template maps (singular keys).
	Pod   map[string]*PodSpec   `yaml:"pod,omitempty"`
	K8s   map[string]*K8sSpec   `yaml:"k8s,omitempty"`
	Local map[string]*LocalSpec `yaml:"local,omitempty"`

	// AI catalog (kind:ai), harness recipes (kind:recipe = pure spec),
	// and harness scores (kind:score = runner config that references
	// recipes). See ai_config.go, harness_recipe.go, harness_score_kind.go,
	// /ov:harness.
	AI     map[string]*AIConfig      `yaml:"ai,omitempty"`
	Recipe map[string]*HarnessRecipe `yaml:"recipe,omitempty"`
	Score  map[string]*HarnessScore  `yaml:"score,omitempty"`

	// Calamares-aligned kinds (2026-05 cutover). `group:` ↔ Calamares
	// netinstall package group; `target:` ↔ Calamares settings.conf
	// install target; `module:` ↔ Calamares module.desc descriptor.
	// Convention files: groups.yml / targets.yml / modules.yml — or
	// inlined in overthink.yml. Importers/emitters are deferred to a
	// follow-up additive PR; this cutover lands the schema.
	Group  map[string]*GroupSpec  `yaml:"group,omitempty"`
	Target map[string]*TargetSpec `yaml:"target,omitempty"`
	Module map[string]*ModuleSpec `yaml:"module,omitempty"`
}

// DiscoverConfig drives filesystem scans for standalone kind-keyed files. Each
// sub-key is independent; a file with only `discover.layer:` is common.
type DiscoverConfig struct {
	Layer    []ScanSpec `yaml:"layer,omitempty"`
	Image    []ScanSpec `yaml:"image,omitempty"`
	Deploy   []ScanSpec `yaml:"deploy,omitempty"`
	Builder  []ScanSpec `yaml:"builder,omitempty"`
	Distro   []ScanSpec `yaml:"distro,omitempty"`
	Init     []ScanSpec `yaml:"init,omitempty"`
	VM       []ScanSpec `yaml:"vm,omitempty"`
	Cluster  []ScanSpec `yaml:"cluster,omitempty"` // reserved for Part F
	// Calamares-aligned kinds.
	Group  []ScanSpec `yaml:"group,omitempty"`
	Target []ScanSpec `yaml:"target,omitempty"`
	Module []ScanSpec `yaml:"module,omitempty"`
}

// ScanSpec describes one discovery root. Accepts string shorthand
// ("layers" → {Path: "layers", Recursive: true}) or the explicit object form
// ({path: X, recursive: false}). Empty Path is invalid.
type ScanSpec struct {
	Path      string `yaml:"path"`
	Recursive bool   `yaml:"recursive"`
}

// UnmarshalYAML accepts the string shorthand where Recursive defaults to true,
// and the object form where Recursive defaults to true when omitted.
func (s *ScanSpec) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		s.Path = node.Value
		s.Recursive = true
		return nil
	}
	// Object form — decode with `recursive` defaulting to true when absent.
	// yaml.v3 has no direct "default true"; we interpret missing as true by
	// looking at the raw node and only clearing Recursive when the field is
	// explicitly set to false.
	var raw struct {
		Path      string `yaml:"path"`
		Recursive *bool  `yaml:"recursive"`
	}
	if err := node.Decode(&raw); err != nil {
		return err
	}
	s.Path = raw.Path
	if raw.Recursive == nil {
		s.Recursive = true
	} else {
		s.Recursive = *raw.Recursive
	}
	return nil
}

// InlineLayer is a layer declared inline in the unified file's `layers:` map.
// Mutually exclusive options: `from:` points at a directory to scan via the
// existing scanLayer (no schema change), OR the inline body defines the layer
// (same fields as layer.yml, flattened via yaml:",inline").
type InlineLayer struct {
	From      string `yaml:"from,omitempty"`
	LayerYAML `yaml:",inline"`
}

// UnmarshalYAML is required because LayerYAML has its own UnmarshalYAML —
// yaml.v3's default ",inline" handling doesn't compose with a custom
// unmarshaler on the embedded type. We read `from:` explicitly, then delegate
// to LayerYAML for the body.
func (il *InlineLayer) UnmarshalYAML(node *yaml.Node) error {
	var own struct {
		From string `yaml:"from"`
	}
	_ = node.Decode(&own)
	il.From = own.From
	if il.From != "" {
		// `from:` entries reference an external directory — no body decode.
		return nil
	}
	return il.LayerYAML.UnmarshalYAML(node)
}

// DeploymentsSection carries repo-shipped deployment defaults plus per-image
// deployment entries. Matches the two-tier deploy model: this block is the
// authored/in-repo defaults; ~/.config/ov/deploy.yml is the per-machine overlay.
// DeploymentsSection — RETIRED by the field-singular cutover (2026-05).
// UnifiedFile.Deploy is now a flat map; UnifiedFile.Provides moved to
// root level. The type definition is kept (not deleted) because
// migrate_unified.go still references it for legacy migration history.
type DeploymentsSection struct {
	Defaults *DeploymentNode           `yaml:"defaults,omitempty"`
	Provides *ProvidesConfig           `yaml:"provides,omitempty"`
	Image    map[string]DeploymentNode `yaml:"image,omitempty"`
}

// -----------------------------------------------------------------------------
// Kind-keyed single-entity document forms (Part E).
//
// A document containing exactly one top-level kind key (`layer:` / `image:` /
// `deployment:` / `builder:` / `distro:` / `init:`) + a `name:` inside the
// body is a self-describing standalone entity. Multiple such documents can be
// concatenated via YAML `---` separators to form a bundle file.
// -----------------------------------------------------------------------------

type kindKeyedDoc struct {
	Layer      *LayerDoc      `yaml:"layer,omitempty"`
	Image      *ImageDoc      `yaml:"image,omitempty"`
	Deploy     *DeployDoc `yaml:"deploy,omitempty"`
	Builder    *BuilderDoc    `yaml:"builder,omitempty"`
	Distro     *DistroDoc     `yaml:"distro,omitempty"`
	Init       *InitDoc       `yaml:"init,omitempty"`
	VM         *VmDoc         `yaml:"vm,omitempty"`
	// Schema v4 first-class target templates.
	Pod   *PodDoc   `yaml:"pod,omitempty"`
	K8s   *K8sDoc   `yaml:"k8s,omitempty"`
	Local *LocalDoc `yaml:"local,omitempty"`
	// 2026-04 harness cutover.
	AI     *AIDoc     `yaml:"ai,omitempty"`
	Recipe *RecipeDoc `yaml:"recipe,omitempty"`
	Score  *ScoreDoc  `yaml:"score,omitempty"`
	// 2026-05 Calamares cutover.
	Group  *GroupDoc  `yaml:"group,omitempty"`
	Target *TargetDoc `yaml:"target,omitempty"`
	Module *ModuleDoc `yaml:"module,omitempty"`
}

// AIDoc wraps a single AIConfig with an explicit Name — the kind:ai
// standalone form. Bundles of `kind: ai` + `name: <name>` documents
// can be concatenated via YAML --- separators in eval.yml.
type AIDoc struct {
	Name     string `yaml:"name"`
	AIConfig `yaml:",inline"`
}

// RecipeDoc wraps a single HarnessRecipe with an explicit Name —
// the kind:recipe standalone form.
type RecipeDoc struct {
	Name          string `yaml:"name"`
	HarnessRecipe `yaml:",inline"`
}

// ScoreDoc wraps a single HarnessScore with an explicit Name —
// the kind:score standalone form.
type ScoreDoc struct {
	Name         string `yaml:"name"`
	HarnessScore `yaml:",inline"`
}

// PodDoc wraps a single PodSpec with an explicit Name — the kind:pod
// standalone form.
type PodDoc struct {
	Name    string `yaml:"name"`
	PodSpec `yaml:",inline"`
}

// K8sDoc wraps a single K8sSpec with an explicit Name — the kind:k8s
// standalone form.
type K8sDoc struct {
	Name    string `yaml:"name"`
	K8sSpec `yaml:",inline"`
}

// LocalDoc wraps a single LocalSpec with an explicit Name — the kind:host
// standalone form.
type LocalDoc struct {
	Name      string `yaml:"name"`
	LocalSpec `yaml:",inline"`
}

// LayerDoc wraps a LayerYAML body with an explicit Name — the standalone form
// authored in `layers/<name>/layer.yml` post-migration.
type LayerDoc struct {
	Name      string `yaml:"name"`
	LayerYAML `yaml:",inline"`
}

// UnmarshalYAML — same rationale as InlineLayer.UnmarshalYAML. The custom
// unmarshaler on the embedded LayerYAML doesn't compose with ",inline", so we
// extract Name ourselves and delegate the body to LayerYAML.
func (ld *LayerDoc) UnmarshalYAML(node *yaml.Node) error {
	var own struct {
		Name string `yaml:"name"`
	}
	_ = node.Decode(&own)
	ld.Name = own.Name
	return ld.LayerYAML.UnmarshalYAML(node)
}

// ImageDoc wraps a single ImageConfig with an explicit Name — the standalone
// form authored in `images/<name>/image.yml` post-migration.
type ImageDoc struct {
	Name        string `yaml:"name"`
	ImageConfig `yaml:",inline"`
}

// DeployDoc wraps a single DeploymentNode.
type DeployDoc struct {
	Name           string `yaml:"name"`
	DeploymentNode `yaml:",inline"`
}

// BuilderDoc wraps a single BuilderDef.
type BuilderDoc struct {
	Name       string `yaml:"name"`
	BuilderDef `yaml:",inline"`
}

// DistroDoc wraps a single DistroDef.
type DistroDoc struct {
	Name      string `yaml:"name"`
	DistroDef `yaml:",inline"`
}

// InitDoc wraps a single InitDef.
type InitDoc struct {
	Name    string `yaml:"name"`
	InitDef `yaml:",inline"`
}

// VmDoc wraps a single VmSpec with an explicit name. The standalone
// form is authored as a top-level `kind: vm` + `name: <name>` document
// (often as a paired entry in the same file as a kind:image entry for
// bootc images — see migrate vm-spec).
type VmDoc struct {
	Name        string       `yaml:"name"`
	Version     string       `yaml:"version,omitempty"`
	Description *Description `yaml:"description,omitempty"` // Gherkin-shaped self-description; replaces retired info:/status:
	Spec        VmSpec       `yaml:"spec"`
}

// -----------------------------------------------------------------------------
// Entity kind table — drives scanner + router + merge path.
// -----------------------------------------------------------------------------

type entityKind struct {
	Key      string // top-level YAML key in kind-keyed form
	Filename string // canonical filename under discovery scan roots
}

var entityKinds = []entityKind{
	{Key: "layer", Filename: "layer.yml"},
	{Key: "image", Filename: "image.yml"},
	{Key: "deploy", Filename: "deploy.yml"},
	{Key: "builder", Filename: "builder.yml"},
	{Key: "distro", Filename: "distro.yml"},
	{Key: "init", Filename: "init.yml"},
	// Schema v4 additions — first-class target template kinds. All
	// singular. All authored in overthink.yml or in their convention
	// files (pod.yml / vm.yml / k8s.yml / local.yml).
	{Key: "pod", Filename: "pod.yml"},
	{Key: "vm", Filename: "vm.yml"},
	{Key: "k8s", Filename: "k8s.yml"},
	{Key: "local", Filename: "local.yml"},
	// 2026-04 harness cutover: `ai:`, `recipe:` (pure spec) and
	// `score:` (runner config referencing recipes) kinds. Convention
	// file: eval.yml (carries all three kinds together).
	{Key: "ai", Filename: "ai.yml"},
	{Key: "recipe", Filename: "recipe.yml"},
	{Key: "score", Filename: "score.yml"},
	// 2026-05 Calamares cutover: `group:` (netinstall group),
	// `target:` (settings.conf), `module:` (module.desc).
	{Key: "group", Filename: "group.yml"},
	{Key: "target", Filename: "target.yml"},
	{Key: "module", Filename: "module.yml"},
}

// -----------------------------------------------------------------------------
// Loader entry point.
// -----------------------------------------------------------------------------

// LoadUnified reads overthink.yml at dir, resolves all `includes:` recursively,
// walks `discover:` roots, and returns the merged UnifiedFile plus a flag
// indicating whether overthink.yml was present. When the file does not exist,
// (nil, false, nil) is returned so callers can fall through to legacy loaders.
//
// Enforces schema version 2: any loaded overthink.yml whose `version:` is
// absent or less than 2 is hard-rejected with a migration hint. v1 configs
// used a separate vms.yml + plural `vms:` root key; `ov migrate merge-vms`
// produces a v2 layout in one shot.
// rejectLegacyLocalSurface refuses to load any project that still
// carries kind:host, target:host, or DeploymentNode `host: <template>`
// references against templates that no longer exist (the new `host:`
// field is destination-only). All three are fixed by
// `ov migrate target-local` in one pass.
// rejectLegacyDeploymentRefs scans every *.yml at the project root for
// residual `deployment:` / `deployments:` / `kind: deployment` references
// retired by the 2026-05 kind-files cutover. Catches data-loss footguns
// where a YAML key would silently fail to bind after the rename.
func rejectLegacyDeploymentRefs(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil // Loader will surface the dir issue elsewhere.
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yml") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		dec := yaml.NewDecoder(bytes.NewReader(data))
		for docIdx := 0; ; docIdx++ {
			var doc yaml.Node
			if err := dec.Decode(&doc); err != nil {
				break
			}
			if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
				continue
			}
			root := doc.Content[0]
			// Case A: root-key `deployment:` (post-v4-pre-cutover singular alias).
			if v := findMappingValue(root, "deployment"); v != nil {
				return fmt.Errorf(
					"%s (doc %d): root-key `deployment:` is retired (2026-05 kind-files cutover).\n  Renamed to `deploy:`. Run: ov migrate kind-files",
					path, docIdx)
			}
			// Case B: root-key `deployments:` (v3 legacy plural).
			if v := findMappingValue(root, "deployments"); v != nil {
				return fmt.Errorf(
					"%s (doc %d): root-key `deployments:` is retired (legacy v3 plural).\n  Run: ov migrate kind-files (which also covers ov migrate schema-v4)",
					path, docIdx)
			}
			// Case C: kind-keyed wrapper `kind: deployment` scalar.
			if v := findMappingValue(root, "kind"); v != nil && v.Kind == yaml.ScalarNode && v.Value == "deployment" {
				return fmt.Errorf(
					"%s (doc %d): `kind: deployment` is retired (2026-05 kind-files cutover).\n  Renamed to `kind: deploy`. Run: ov migrate kind-files",
					path, docIdx)
			}
		}
	}
	return nil
}

func rejectLegacyLocalSurface(root string, merged *UnifiedFile) error {
	if merged == nil {
		return nil
	}
	var walk func(name string, node *DeploymentNode) error
	walk = func(name string, node *DeploymentNode) error {
		if node == nil {
			return nil
		}
		if node.Target == "host" {
			return fmt.Errorf(
				"%s: deployment %q uses legacy `target: host` — schema renamed to `target: local`. Run: ov migrate target-local",
				root, name)
		}
		for childName, child := range node.Nested {
			fullName := name + "." + childName
			if err := walk(fullName, child); err != nil {
				return err
			}
		}
		return nil
	}
	if merged.Deploy != nil {
		for name, node := range merged.Deploy {
			n := node
			if err := walk(name, &n); err != nil {
				return err
			}
		}
	}
	for name, node := range merged.Deploy {
		n := node
		if err := walk(name, &n); err != nil {
			return err
		}
	}
	return nil
}

// rejectLegacyMarimoMl errors out on any residual `marimo-ml` /
// `marimo-ml-pod` reference (image key, deployment key, or `image:`
// cross-ref). The 2026-04 cutover renamed the image + deploy entry to
// `marimo`; the 2026-05 cutover then renamed `marimo` → `versa`. This
// guard ensures users on an outdated personal deploy.yml STILL on
// marimo-ml see a remediation hint pointing at the current canonical
// (`versa`) rather than silently picking up the wrong image at
// `ov update` time.
func rejectLegacyMarimoMl(root string, merged *UnifiedFile) error {
	if merged == nil {
		return nil
	}
	if _, ok := merged.Image["marimo-ml"]; ok {
		return fmt.Errorf(
			"%s: image entry %q is retired (2026-04 marimo-rename cutover, 2026-05 versa-rename cutover).\n  Renamed to `versa` (cross-kind name reuse). Run: ov migrate marimo-rename",
			root, "marimo-ml")
	}
	var walk func(name string, node *DeploymentNode) error
	walk = func(name string, node *DeploymentNode) error {
		if node == nil {
			return nil
		}
		if name == "marimo-ml-pod" {
			return fmt.Errorf(
				"%s: deployment %q is retired (2026-04 marimo-rename cutover, 2026-05 versa-rename cutover).\n  Renamed to `versa` (cross-kind name reuse). Run: ov migrate marimo-rename",
				root, name)
		}
		if node.Image == "marimo-ml" {
			return fmt.Errorf(
				"%s: deployment %q references retired image %q (2026-04 marimo-rename cutover, 2026-05 versa-rename cutover).\n  Renamed to `versa`. Run: ov migrate marimo-rename",
				root, name, "marimo-ml")
		}
		for childName, child := range node.Nested {
			fullName := name + "." + childName
			if err := walk(fullName, child); err != nil {
				return err
			}
		}
		return nil
	}
	for name, node := range merged.Deploy {
		n := node
		if err := walk(name, &n); err != nil {
			return err
		}
	}
	return nil
}

func LoadUnified(dir string) (*UnifiedFile, bool, error) {
	root := filepath.Join(dir, UnifiedFileName)
	if !fileExists(root) {
		return nil, false, nil
	}
	// 2026-05 kind-files cutover: hard-reject residual `deployment:` /
	// `deployments:` / `kind: deployment` in any project YAML. These
	// were renamed to `deploy:` / `kind: deploy`. The migration command
	// rewrites them in-place.
	if err := rejectLegacyDeploymentRefs(dir); err != nil {
		return nil, true, err
	}
	// Field-singular cutover (2026-05): hard-reject any residual plural
	// top-level keys (images:/layers:/distros:/... ) in overthink.yml.
	// `ov migrate field-singular` rewrites them in-place.
	if rootData, err := os.ReadFile(root); err == nil {
		if err := RejectLegacyPluralKeys(root, rootData); err != nil {
			return nil, true, err
		}
	}
	merged := &UnifiedFile{}
	visited := map[string]bool{}
	if err := loadUnifiedInto(root, merged, visited, 0); err != nil {
		return nil, true, err
	}
	normalizeV4Aliases(merged)
	if merged.Version < schemaVersion {
		return nil, true, fmt.Errorf(
			"%s: schema v%d is required (found v%d). Run: ov migrate schema-v4",
			root, schemaVersion, merged.Version,
		)
	}
	// Reject any residual legacy local/host or status/info surface.
	// `ov migrate target-local` fixes all of these in one shot.
	if err := rejectLegacyLocalSurface(root, merged); err != nil {
		return nil, true, err
	}
	if err := rejectLegacyMarimoMl(root, merged); err != nil {
		return nil, true, err
	}
	if err := validateDeploymentTree(merged.Deploy); err != nil {
		return nil, true, fmt.Errorf("%s: %w", root, err)
	}
	// Hard load-time error for the retired `local.cachyos-dx` key.
	// Pairs with the deployment-side checks in validateDeploymentTree.
	// All three retired keys (deployment.qc, deployment.cachyos-dx,
	// local.cachyos-dx) point at the consolidated migration command.
	if _, present := merged.Local["cachyos-dx"]; present {
		return nil, true, fmt.Errorf(
			"%s: kind:local key \"cachyos-dx\" is retired (2026-05 init-system-polymorphism cutover).\n  Run: ov migrate ov-cachyos",
			root,
		)
	}
	if err := expandRecipeFromIfNeeded(merged, dir); err != nil {
		return nil, true, fmt.Errorf("%s: %w", root, err)
	}
	if err := validateHarnessSemantics(merged); err != nil {
		return nil, true, fmt.Errorf("%s: %w", root, err)
	}
	return merged, true, nil
}

// expandRecipeFromIfNeeded resolves every recipe's `from:` directives
// into synthetic scenarios via ExpandRecipeFrom. Runs only when at least
// one recipe declares `from:` — otherwise we skip the discover/project
// pass entirely, preserving zero-cost loading for non-composing projects.
//
// Hooked between validateDeploymentTree and validateHarnessSemantics so
// the latter sees the synthesized scenarios and can apply its existing
// pod-required + depends_on intra-recipe rules to the post-expansion
// flat scenario list uniformly.
//
// Also enforces the cross-recipe-score rule: a recipe with any
// `kind: vm` import can only be referenced by scores that target that
// VM via `vm:` (per-scenario execution against a VM uses the score's
// SSH path, not `podman exec ov-<pod>`).
func expandRecipeFromIfNeeded(merged *UnifiedFile, dir string) error {
	needed := false
	for _, r := range merged.Recipe {
		if r != nil && len(r.From) > 0 {
			needed = true
			break
		}
	}
	if !needed {
		return nil
	}
	if err := merged.ApplyDiscover(dir); err != nil {
		return fmt.Errorf("apply discover (for from: expansion): %w", err)
	}
	layers, err := merged.ProjectLayers(dir)
	if err != nil {
		return fmt.Errorf("project layers (for from: expansion): %w", err)
	}
	// recipe-name → set of vm names imported into that recipe.
	vmImportsByRecipe := map[string]map[string]bool{}
	for name, recipe := range merged.Recipe {
		if recipe == nil {
			continue
		}
		for _, from := range recipe.From {
			if from.Kind == "vm" && from.Name != "" {
				if vmImportsByRecipe[name] == nil {
					vmImportsByRecipe[name] = map[string]bool{}
				}
				vmImportsByRecipe[name][from.Name] = true
			}
		}
		if err := ExpandRecipeFrom(merged, layers, name, recipe); err != nil {
			return err
		}
	}
	// Cross-validate: any score referencing a vm-importing recipe MUST
	// target the same VM via `vm:`. Pod- or host-targeted scores can't
	// reach VM-side tests through `podman exec`.
	for scoreName, score := range merged.Score {
		if score == nil {
			continue
		}
		for _, recipeName := range score.Recipe {
			vmSet := vmImportsByRecipe[recipeName]
			if len(vmSet) == 0 {
				continue
			}
			if score.VM == "" {
				return fmt.Errorf("score %q references recipe %q which imports tests from kind:vm (%s) — the score MUST target the VM via `vm: <name>` (current target: pod=%q host=%v); per-scenario execution against a VM uses the score's SSH path, not podman exec",
					scoreName, recipeName, joinStringSet(vmSet), score.Pod, score.Host)
			}
			if !vmSet[score.VM] {
				return fmt.Errorf("score %q targets vm %q but references recipe %q which imports tests from a different vm (%s) — vm-import recipes can only be scored against the same VM they import from",
					scoreName, score.VM, recipeName, joinStringSet(vmSet))
			}
		}
	}
	return nil
}

// joinStringSet returns a sorted comma-joined string of map keys, for
// human-readable error messages.
func joinStringSet(m map[string]bool) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

// validateDeploymentTree enforces structural invariants on the
// deployments tree that can't be expressed in the YAML struct tags:
//
//   - Map keys at every level MUST NOT contain "." (dots are reserved
//     for dotted-path CLI addressing like `ov deploy add a.b.c`).
//   - The reserved name `arch` is no longer valid — schema
//     v2 renamed it to `arch`. This catches stale user configs that
//     sneaked past the merge-vms migration.
//
// Errors include the offending path so the user sees exactly which
// entry needs to be fixed.
func validateDeploymentTree(deploy map[string]DeploymentNode) error {
	if deploy == nil {
		return nil
	}
	for name, node := range deploy {
		if err := validateDeploymentName(name, ""); err != nil {
			return err
		}
		if err := validateDeploymentChildren(name, &node); err != nil {
			return err
		}
	}
	// Hard load-time errors for the retired CachyOS-deployment keys.
	// The qc → cachyos-dx → ov-cachyos rename chain (2026-05) collapsed
	// in the second cutover to ONE canonical name `ov-cachyos` shared
	// by the kind:local template and the kind:deployment entry that
	// applies it. Any residual `qc:` or `cachyos-dx:` deployment key
	// (or `cachyos-dx:` kind:local key) needs a one-shot migration.
	if _, present := deploy["qc"]; present {
		return fmt.Errorf(
			"deployment key \"qc\" is retired (2026-05 cross-kind name reuse cutover).\n  Run: ov migrate ov-cachyos",
		)
	}
	if _, present := deploy["cachyos-dx"]; present {
		return fmt.Errorf(
			"deployment key \"cachyos-dx\" is retired (2026-05 init-system-polymorphism cutover).\n  Run: ov migrate ov-cachyos",
		)
	}
	if err := validateDeployRequiresImage(deploy); err != nil {
		return err
	}
	return nil
}

// validateDeployRequiresImage enforces the 2026-05-12 schema rule:
// every `target: pod` deploy entry MUST declare its `image:` field.
// Pre-cutover the eval runner silently fell back to inspecting the
// running container's image ref via `containerImageRef`, which read
// stale OCI labels off volume-pinned containers and dropped any
// probes added after the seed image. The hard-required field forces
// operator intent to be explicit; the eval runner now resolves the
// ref ONLY from this field.
//
// Scope: target: pod (or empty — pod is the default). target: vm
// uses `vm:`, target: local is layer-driven, target: k8s
// CLUSTER definitions live in the `k8s:` section (not deploy:).
//
// Remediation: `ov migrate require-image` (idempotent) walks every
// affected deploy and injects the field, inferring the value from
// the deploy key (`<base>` for `<base>/<instance>` keys; the key
// itself otherwise).
func validateDeployRequiresImage(deploy map[string]DeploymentNode) error {
	for name, node := range deploy {
		target := node.Target
		if target != "" && target != "pod" {
			continue
		}
		if node.Image == "" {
			return fmt.Errorf(
				"deploy entry %q lacks required `image:` field (2026-05-12 schema cutover — pod-target deploys must declare `image:` explicitly so the eval runner reads the operator's declared intent, not the running container's stale label).\n  Remediation: run `ov migrate require-image` (one-shot, idempotent).",
				name,
			)
		}
	}
	return nil
}

func validateDeploymentChildren(path string, node *DeploymentNode) error {
	if node == nil || len(node.Nested) == 0 {
		return nil
	}
	for childName, child := range node.Nested {
		childPath := childName
		if path != "" {
			childPath = path + "." + childName
		}
		if err := validateDeploymentName(childName, path); err != nil {
			return err
		}
		if err := validateDeploymentChildren(childPath, child); err != nil {
			return err
		}
	}
	return nil
}

func validateDeploymentName(name, parentPath string) error {
	full := name
	if parentPath != "" {
		full = parentPath + "." + name
	}
	if strings.Contains(name, ".") {
		return fmt.Errorf(
			"deployment key %q contains '.' — the character is reserved for dotted-path addressing (ov deploy add a.b.c). Rename this entry in deploy.yml",
			full,
		)
	}
	return nil
}

// loadUnifiedInto reads one file, merges every one of its documents into merged,
// then recurses into any `includes:` it declared. Cycle-safe via the visited set.
func loadUnifiedInto(path string, merged *UnifiedFile, visited map[string]bool, depth int) error {
	if depth > MaxIncludeDepth {
		return fmt.Errorf("include depth exceeded %d at %s", MaxIncludeDepth, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolving %s: %w", path, err)
	}
	if visited[abs] {
		return fmt.Errorf("include cycle: %s already visited", abs)
	}
	visited[abs] = true

	data, err := os.ReadFile(abs)
	if err != nil {
		return fmt.Errorf("reading %s: %w", abs, err)
	}

	// Parse as a multi-document YAML stream.
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	docIdx := 0
	var includesQueue []string
	for {
		var node yaml.Node
		if err := decoder.Decode(&node); err != nil {
			if err.Error() == "EOF" {
				break
			}
			return fmt.Errorf("%s:doc%d: %w", abs, docIdx, err)
		}
		shape, err := classifyDoc(&node)
		if err != nil {
			return fmt.Errorf("%s:doc%d: %w", abs, docIdx, err)
		}
		if err := validateHarnessYAMLNode(&node, fmt.Sprintf("%s:doc%d", abs, docIdx)); err != nil {
			return err
		}
		switch shape {
		case docShapeRoot:
			var uf UnifiedFile
			if err := node.Decode(&uf); err != nil {
				return fmt.Errorf("%s:doc%d: decoding root-shape document: %w", abs, docIdx, err)
			}
			// Queue includes for recursion after current-file merging.
			for _, inc := range uf.Include {
				includesQueue = append(includesQueue, inc)
			}
			// Clear includes before merging so they don't leak into merged struct.
			uf.Include = nil
			// Queue discovery roots (resolved relative to this file).
			// Discovery runs only AFTER all includes are fully merged, so
			// collect them on merged.Discover directly and process at end.
			normalizeV4Aliases(&uf)
			mergeUnified(merged, &uf, filepath.Dir(abs))
		case docShapeKind:
			var kd kindKeyedDoc
			if err := node.Decode(&kd); err != nil {
				return fmt.Errorf("%s:doc%d: decoding kind-keyed document: %w", abs, docIdx, err)
			}
			if err := mergeKindDoc(merged, &kd, filepath.Dir(abs)); err != nil {
				return fmt.Errorf("%s:doc%d: %w", abs, docIdx, err)
			}
		case docShapeEmpty:
			// Skip empty docs (YAML streams commonly end with "---\n").
		}
		docIdx++
	}

	// Recurse into includes relative to this file's directory.
	base := filepath.Dir(abs)
	for _, inc := range includesQueue {
		incPath := inc
		if strings.HasPrefix(incPath, "@") {
			// Remote ref (@host/org/repo/sub/path:version) — parse, download,
			// resolve SubPath. Flow through the existing cycle-detect map on
			// the resolved absolute path (like any local include).
			parsed := ParseRemoteRef(incPath)
			version := parsed.Version
			if version == "" {
				repoURL := RepoGitURL(parsed.RepoPath)
				branch, err := GitDefaultBranch(repoURL)
				if err != nil {
					return fmt.Errorf("%s: resolving default branch for %s: %w", abs, parsed.RepoPath, err)
				}
				version = branch
			}
			cachePath, err := EnsureRepoDownloaded(parsed.RepoPath, version)
			if err != nil {
				return fmt.Errorf("%s: downloading remote include %q: %w", abs, incPath, err)
			}
			incPath = filepath.Join(cachePath, parsed.SubPath)
		} else if !filepath.IsAbs(incPath) {
			incPath = filepath.Join(base, incPath)
		}
		// Includes merge UNDER the root file — root wins. In our implementation,
		// we already merged the root's fields above; now we merge includes on
		// top but the merge function below preserves existing (root) values.
		if err := loadUnifiedInto(incPath, merged, visited, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// -----------------------------------------------------------------------------
// Document-shape classifier.
// -----------------------------------------------------------------------------

type docShape int

const (
	docShapeEmpty docShape = iota
	docShapeRoot
	docShapeKind
)

// rootShapeKeys are the top-level keys that mark a doc as root-shaped.
// Schema v4 uses singular keys uniformly: image/pod/vm/k8s/host/deployment.
// Plural spellings (images:/vms:) are legacy; classifyDoc rejects them
// with a migration hint.
var rootShapeKeys = map[string]bool{
	"version": true, "include": true, "discover": true, "defaults": true,
	"provides": true,
	// Field-singular cutover (2026-05): plurals collapsed.
	"distro": true, "builder": true, "init": true,
	"layer": true,
	"image": true, "pod": true, "vm": true, "k8s": true, "local": true,
	"deploy": true,
	// 2026-04 harness cutover: `ai:` and `recipe:` are recognized as
	// root-shape collection-map keys (in addition to being valid
	// kind-keyed forms). Mirrors how image/pod/vm work.
	"ai": true, "recipe": true, "score": true,
	// Calamares-aligned kinds (also used as DiscoverConfig field names).
	"group": true, "target": true, "module": true,
}

// kindKeysSet mirrors entityKinds for O(1) lookup.
var kindKeysSet = func() map[string]bool {
	m := make(map[string]bool, len(entityKinds))
	for _, k := range entityKinds {
		m[k.Key] = true
	}
	return m
}()

// classifyDoc inspects a document's top-level keys and returns its shape. A
// doc with any root key + any kind key is ambiguous and errors out. Empty
// documents (scalar null, empty mapping) return docShapeEmpty.
func classifyDoc(node *yaml.Node) (docShape, error) {
	if node == nil || node.Kind == 0 {
		return docShapeEmpty, nil
	}
	// yaml.NewDecoder wraps content in a DocumentNode.
	inner := node
	if node.Kind == yaml.DocumentNode {
		if len(node.Content) == 0 {
			return docShapeEmpty, nil
		}
		inner = node.Content[0]
	}
	if inner.Kind == yaml.ScalarNode && inner.Tag == "!!null" {
		return docShapeEmpty, nil
	}
	if inner.Kind != yaml.MappingNode {
		return 0, fmt.Errorf("top-level must be a mapping, got kind=%v", inner.Kind)
	}
	if len(inner.Content) == 0 {
		return docShapeEmpty, nil
	}

	hasRoot, hasKind := false, false
	var keys []string
	hasLegacyBenchmarkKey := false
	for i := 0; i < len(inner.Content); i += 2 {
		k := inner.Content[i].Value
		keys = append(keys, k)
		if k == "benchmark" {
			hasLegacyBenchmarkKey = true
		}
		// Schema v4: the target-template kind keys (image/pod/vm/k8s/host/
		// deployment) overlap with root-shape map keys. Disambiguate by
		// value inspection — kind-keyed single-entity form has a `name:`
		// scalar child; root-shape map form has child keys that are names
		// themselves (no `name:` key at the first level of the value).
		val := inner.Content[i+1]
		if rootShapeKeys[k] && kindKeysSet[k] {
			if mapHasKey(val, "name") {
				hasKind = true
			} else {
				hasRoot = true
			}
		} else if rootShapeKeys[k] {
			hasRoot = true
		} else if kindKeysSet[k] {
			hasKind = true
		}
	}
	// Legacy `benchmark:` root key — predates two cutovers. The eval
	// migrator is forward-only (harness → eval); pre-April-2026 projects
	// must run `ov migrate harness` from a pre-April-2026 ov release
	// first, then upgrade and run `ov migrate eval`.
	if hasLegacyBenchmarkKey {
		return 0, fmt.Errorf(
			"the `benchmark:` root key is no longer accepted (this project predates two cutovers). Run `ov migrate harness` from a pre-April-2026 ov release first, then upgrade and run: ov migrate eval",
		)
	}
	switch {
	case hasRoot && hasKind:
		return 0, fmt.Errorf("ambiguous document: contains both root-shape and kind-keyed top-level keys %v", keys)
	case hasRoot:
		return docShapeRoot, nil
	case hasKind:
		return docShapeKind, nil
	default:
		return 0, fmt.Errorf("document has no recognized top-level keys (got %v)", keys)
	}
}

// -----------------------------------------------------------------------------
// Harness kind-split validation (post-classify, pre-decode).
// -----------------------------------------------------------------------------

// validateHarnessYAMLNode rejects, with hard errors pointing at
// `ov migrate harness`, two legacy shapes that the slim post-cutover
// HarnessRecipe / HarnessScore struct decoders would otherwise silently
// drop:
//
//  1. **Fat-recipe shape**: a `recipe:` entry whose body carries any
//     key other than {description, scenario}. Pre-cutover recipes
//     mixed runner config (host, ai, plateau_iteration, prompt, …)
//     with spec; post-cutover those move to `score:`.
//
//  2. **Residual `max_iteration:`** anywhere on `recipe:` or `score:`
//     bodies. The post-cutover loop bound is plateau-only.
//
// Walks both the root-shape (recipe: { name: { ... }, name2: { ... } })
// and the kind-keyed standalone form (top-level `recipe: { name: X,
// description: ..., scenario: ... }`). Empty or absent keys are no-ops.
func validateHarnessYAMLNode(node *yaml.Node, source string) error {
	inner := node
	if node.Kind == yaml.DocumentNode {
		if len(node.Content) == 0 {
			return nil
		}
		inner = node.Content[0]
	}
	if inner == nil || inner.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(inner.Content); i += 2 {
		k := inner.Content[i]
		v := inner.Content[i+1]
		if k.Kind != yaml.ScalarNode {
			continue
		}
		switch k.Value {
		case "recipe":
			if err := validateRecipeNode(v, source); err != nil {
				return err
			}
		case "score":
			if err := validateScoreNode(v, source); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateRecipeNode walks either:
//   - root-shape: a mapping of name -> body (each body validated)
//   - kind-keyed: a mapping with `name:` + body fields at the same level
func validateRecipeNode(v *yaml.Node, source string) error {
	if v == nil || v.Kind != yaml.MappingNode {
		return nil
	}
	if mapHasKey(v, "name") {
		// Kind-keyed standalone form: this node IS the body (with
		// an extra `name:` key alongside description/scenario).
		name := ""
		for i := 0; i+1 < len(v.Content); i += 2 {
			if v.Content[i].Value == "name" {
				name = v.Content[i+1].Value
				break
			}
		}
		return validateRecipeBody(v, name, source, true)
	}
	// Root-shape map: name -> body
	for i := 0; i+1 < len(v.Content); i += 2 {
		nameNode := v.Content[i]
		body := v.Content[i+1]
		if err := validateRecipeBody(body, nameNode.Value, source, false); err != nil {
			return err
		}
	}
	return nil
}

// validateRecipeBody enforces the slim recipe shape and rejects
// max_iteration. allowName=true permits a `name:` key (kind-keyed form).
func validateRecipeBody(body *yaml.Node, name, source string, allowName bool) error {
	if body == nil || body.Kind != yaml.MappingNode {
		return nil
	}
	allowed := map[string]bool{"description": true, "scenario": true, "from": true}
	if allowName {
		allowed["name"] = true
	}
	for i := 0; i+1 < len(body.Content); i += 2 {
		k := body.Content[i].Value
		if k == "max_iteration" {
			return fmt.Errorf(
				"%s: recipe %q carries `max_iteration:` — the field was removed in the 2026-04 harness cutover. Loop bound is now plateau-only via score.plateau_iteration. Run: ov migrate harness",
				source, name,
			)
		}
		if !allowed[k] {
			return fmt.Errorf(
				"%s: recipe %q carries forbidden runner field %q. Recipes are pure spec (description + scenario only); runner fields (host/pod/vm, ai, plateau_iteration, prompt, deployment, target_image, mcp_endpoint, env, notes, recipes) live on a `kind: score` entry. Run: ov migrate harness",
				source, name, k,
			)
		}
	}
	return nil
}

// validateScoreNode walks either:
//   - root-shape: a mapping of name -> body
//   - kind-keyed: a mapping with `name:` + body at the same level
//
// Rejects residual `max_iteration:` and a few obvious mis-spellings.
func validateScoreNode(v *yaml.Node, source string) error {
	if v == nil || v.Kind != yaml.MappingNode {
		return nil
	}
	if mapHasKey(v, "name") {
		name := ""
		for i := 0; i+1 < len(v.Content); i += 2 {
			if v.Content[i].Value == "name" {
				name = v.Content[i+1].Value
				break
			}
		}
		return validateScoreBody(v, name, source)
	}
	for i := 0; i+1 < len(v.Content); i += 2 {
		nameNode := v.Content[i]
		body := v.Content[i+1]
		if err := validateScoreBody(body, nameNode.Value, source); err != nil {
			return err
		}
	}
	return nil
}

// validateScoreBody rejects max_iteration on a score body. Other fields
// are decoded permissively (HarnessScore decode will catch unknown).
func validateScoreBody(body *yaml.Node, name, source string) error {
	if body == nil || body.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(body.Content); i += 2 {
		k := body.Content[i].Value
		if k == "max_iteration" {
			return fmt.Errorf(
				"%s: score %q carries `max_iteration:` — the field was removed in the 2026-04 harness cutover. Loop bound is now plateau-only via score.plateau_iteration. Run: ov migrate harness",
				source, name,
			)
		}
	}
	return nil
}

// validateHarnessSemantics runs cross-reference validation on the merged
// UnifiedFile after every include has been folded in. Catches:
//   - score.recipes must be non-empty
//   - score.recipes entries must not duplicate
//   - score.recipes entries must resolve to defined recipes
//   - score target xor (exactly one of pod/vm/host)
//   - host: true requires disposable: true on the score
//   - every scenario in every recipe MUST set `pod:` (the scoring target)
//   - scenario.depends_on entries must reference scenarios within the
//     same recipe (intra-recipe scope) and form an acyclic graph
func validateHarnessSemantics(u *UnifiedFile) error {
	// Every kind:recipe scenario must declare `pod:`, have a unique name,
	// and resolve depends_on intra-recipe without cycles. All four rules
	// run through the shared ValidateScenarios (scenario_validate.go,
	// 2026-04 cleanup cutover) so layer/image descriptions get the same
	// enforcement when description loading calls it with RequirePod=false.
	for name, recipe := range u.Recipe {
		if recipe == nil {
			continue
		}
		ctx := ScenarioValidationContext{
			OwnerLabel: fmt.Sprintf("recipe %q", name),
			RequirePod: true,
		}
		if err := ValidateScenarios(recipe.Scenario, ctx); err != nil {
			return err
		}
	}

	for name, ai := range u.AI {
		if ai == nil {
			continue
		}
		switch ai.OutputFormat {
		case AIOutputFormatPlain, AIOutputFormatStreamJSON:
			// ok
		default:
			return fmt.Errorf("ai %q: output_format: %q is not a legal value (allowed: %q, %q)",
				name, ai.OutputFormat, AIOutputFormatPlain, AIOutputFormatStreamJSON)
		}
	}

	for name, score := range u.Score {
		if score == nil {
			continue
		}
		if len(score.Recipe) == 0 {
			return fmt.Errorf("score %q: recipes: must reference at least one recipe (got empty list)", name)
		}
		seen := make(map[string]bool, len(score.Recipe))
		for _, r := range score.Recipe {
			if seen[r] {
				return fmt.Errorf("score %q: recipes: duplicate recipe name %q", name, r)
			}
			seen[r] = true
			if _, ok := u.Recipe[r]; !ok {
				avail := SortedRecipeNames(u.Recipe)
				return fmt.Errorf("score %q: recipes: %q does not resolve to a defined recipe (available: %s)",
					name, r, strings.Join(avail, ", "))
			}
		}
		if _, _, err := ResolveScoreTarget(score); err != nil {
			return fmt.Errorf("score %q: %w", name, err)
		}
	}
	return nil
}

// validateRecipeScenarioDependencies was deleted in the 2026-04
// BDD/test/harness surface-cleanup cutover. Its rules (name uniqueness,
// depends_on resolution, cycle detection) are now part of the shared
// ValidateScenarios in scenario_validate.go, which validateHarnessSemantics
// invokes above with RequirePod=true. Layer/image description loading
// invokes the same validator with RequirePod=false.

// -----------------------------------------------------------------------------
// Merge helpers.
// -----------------------------------------------------------------------------

// mapHasKey reports whether a yaml mapping node contains a top-level key.
// Used by classifyDoc to disambiguate kind-keyed (has `name:`) vs
// root-shape (value is a map of named entries) forms.
func mapHasKey(node *yaml.Node, key string) bool {
	if node == nil || node.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i < len(node.Content)-1; i += 2 {
		if node.Content[i].Kind == yaml.ScalarNode && node.Content[i].Value == key {
			return true
		}
	}
	return false
}

// normalizeV4Aliases — RETIRED by the field-singular cutover (2026-05).
// Dual `Images`/`ImageSingular` and `Deploys`/`DeploySingular` fields
// collapsed into single canonical singular fields with matching yaml
// tags. Function kept as a no-op so external callers don't break;
// remove on next refactor pass.
func normalizeV4Aliases(u *UnifiedFile) {
	_ = u
}

// mergeUnified merges src into dst such that dst's existing values WIN on
// conflict at the same leaf (root-wins). This means when loadUnifiedInto is
// called with the root file first and then includes, the root file's values
// are already present before any include's fields are considered, so root wins.
//
// For included files: the same mergeUnified is called but dst already contains
// the root's values, so those fields stay untouched. src's fields that aren't
// present in dst get copied over. That's the desired semantics.
func mergeUnified(dst, src *UnifiedFile, srcDir string) {
	if src.Version != 0 && dst.Version == 0 {
		dst.Version = src.Version
	}
	// Discover entries concatenate (not overwrite).
	if src.Discover != nil {
		if dst.Discover == nil {
			dst.Discover = &DiscoverConfig{}
		}
		dst.Discover.Layer = append(dst.Discover.Layer, src.Discover.Layer...)
		dst.Discover.Image = append(dst.Discover.Image, src.Discover.Image...)
		dst.Discover.Deploy = append(dst.Discover.Deploy, src.Discover.Deploy...)
		dst.Discover.Builder = append(dst.Discover.Builder, src.Discover.Builder...)
		dst.Discover.Distro = append(dst.Discover.Distro, src.Discover.Distro...)
		dst.Discover.Init = append(dst.Discover.Init, src.Discover.Init...)
		dst.Discover.VM = append(dst.Discover.VM, src.Discover.VM...)
		dst.Discover.Cluster = append(dst.Discover.Cluster, src.Discover.Cluster...)
	}
	mergeDistroMap(&dst.Distro, src.Distro)
	mergeBuilderMap(&dst.Builder, src.Builder)
	mergeInitMap(&dst.Init, src.Init)
	mergeImageMap(&dst.Image, src.Image)
	mergeLayerMap(&dst.Layer, src.Layer)
	mergeVmMap(&dst.VM, src.VM)
	mergePodMap(&dst.Pod, src.Pod)
	mergeK8sMap(&dst.K8s, src.K8s)
	mergeLocalMap(&dst.Local, src.Local)
	mergeAIMap(&dst.AI, src.AI)
	mergeRecipeMap(&dst.Recipe, src.Recipe)
	mergeScoreMap(&dst.Score, src.Score)
	mergeGroupMap(&dst.Group, src.Group)
	mergeTargetMap(&dst.Target, src.Target)
	mergeModuleMap(&dst.Module, src.Module)
	mergeDeployMaps(&dst.Deploy, src.Deploy)
	if dst.Provides == nil && src.Provides != nil {
		dst.Provides = src.Provides
	}
	// Defaults: dst wins per-field if set.
	mergeImageConfig(&dst.Defaults, &src.Defaults)
	_ = srcDir
}

func mergeDistroMap(dst *map[string]*DistroDef, src map[string]*DistroDef) {
	if len(src) == 0 {
		return
	}
	if *dst == nil {
		*dst = make(map[string]*DistroDef)
	}
	for k, v := range src {
		if _, exists := (*dst)[k]; !exists {
			(*dst)[k] = v
		}
	}
}

// mergeAIMap merges AI-catalog entries (kind:ai). Root-wins: existing dst
// keys are preserved; src keys are only added if dst doesn't have them.
func mergeAIMap(dst *map[string]*AIConfig, src map[string]*AIConfig) {
	if len(src) == 0 {
		return
	}
	if *dst == nil {
		*dst = make(map[string]*AIConfig)
	}
	for k, v := range src {
		if _, exists := (*dst)[k]; !exists {
			(*dst)[k] = v
		}
	}
}

// mergeRecipeMap merges harness-recipe entries (kind:recipe). Root-wins.
func mergeRecipeMap(dst *map[string]*HarnessRecipe, src map[string]*HarnessRecipe) {
	if len(src) == 0 {
		return
	}
	if *dst == nil {
		*dst = make(map[string]*HarnessRecipe)
	}
	for k, v := range src {
		if _, exists := (*dst)[k]; !exists {
			(*dst)[k] = v
		}
	}
}

// mergeScoreMap merges harness-score entries (kind:score). Root-wins.
func mergeScoreMap(dst *map[string]*HarnessScore, src map[string]*HarnessScore) {
	if len(src) == 0 {
		return
	}
	if *dst == nil {
		*dst = make(map[string]*HarnessScore)
	}
	for k, v := range src {
		if _, exists := (*dst)[k]; !exists {
			(*dst)[k] = v
		}
	}
}

func mergeBuilderMap(dst *map[string]*BuilderDef, src map[string]*BuilderDef) {
	if len(src) == 0 {
		return
	}
	if *dst == nil {
		*dst = make(map[string]*BuilderDef)
	}
	for k, v := range src {
		if _, exists := (*dst)[k]; !exists {
			(*dst)[k] = v
		}
	}
}

func mergeInitMap(dst *map[string]*InitDef, src map[string]*InitDef) {
	if len(src) == 0 {
		return
	}
	if *dst == nil {
		*dst = make(map[string]*InitDef)
	}
	for k, v := range src {
		if _, exists := (*dst)[k]; !exists {
			(*dst)[k] = v
		}
	}
}

func mergeImageMap(dst *map[string]ImageConfig, src map[string]ImageConfig) {
	if len(src) == 0 {
		return
	}
	if *dst == nil {
		*dst = make(map[string]ImageConfig)
	}
	for k, v := range src {
		if _, exists := (*dst)[k]; !exists {
			(*dst)[k] = v
		}
	}
}

func mergeLayerMap(dst *map[string]*InlineLayer, src map[string]*InlineLayer) {
	if len(src) == 0 {
		return
	}
	if *dst == nil {
		*dst = make(map[string]*InlineLayer)
	}
	for k, v := range src {
		if _, exists := (*dst)[k]; !exists {
			(*dst)[k] = v
		}
	}
}

func mergeVmMap(dst *map[string]*VmSpec, src map[string]*VmSpec) {
	if len(src) == 0 {
		return
	}
	if *dst == nil {
		*dst = make(map[string]*VmSpec)
	}
	for k, v := range src {
		if _, exists := (*dst)[k]; !exists {
			(*dst)[k] = v
		}
	}
}

// Schema v4 target-template merge helpers. Same root-wins semantics as
// mergeVmMap: existing entries survive; included-file entries fill gaps.
func mergePodMap(dst *map[string]*PodSpec, src map[string]*PodSpec) {
	if len(src) == 0 {
		return
	}
	if *dst == nil {
		*dst = make(map[string]*PodSpec)
	}
	for k, v := range src {
		if _, exists := (*dst)[k]; !exists {
			(*dst)[k] = v
		}
	}
}

func mergeK8sMap(dst *map[string]*K8sSpec, src map[string]*K8sSpec) {
	if len(src) == 0 {
		return
	}
	if *dst == nil {
		*dst = make(map[string]*K8sSpec)
	}
	for k, v := range src {
		if _, exists := (*dst)[k]; !exists {
			(*dst)[k] = v
		}
	}
}

func mergeLocalMap(dst *map[string]*LocalSpec, src map[string]*LocalSpec) {
	if len(src) == 0 {
		return
	}
	if *dst == nil {
		*dst = make(map[string]*LocalSpec)
	}
	for k, v := range src {
		if _, exists := (*dst)[k]; !exists {
			(*dst)[k] = v
		}
	}
}

// Calamares-aligned merge helpers (root-wins, same shape as the rest).
func mergeGroupMap(dst *map[string]*GroupSpec, src map[string]*GroupSpec) {
	if len(src) == 0 {
		return
	}
	if *dst == nil {
		*dst = make(map[string]*GroupSpec)
	}
	for k, v := range src {
		if _, exists := (*dst)[k]; !exists {
			(*dst)[k] = v
		}
	}
}

func mergeTargetMap(dst *map[string]*TargetSpec, src map[string]*TargetSpec) {
	if len(src) == 0 {
		return
	}
	if *dst == nil {
		*dst = make(map[string]*TargetSpec)
	}
	for k, v := range src {
		if _, exists := (*dst)[k]; !exists {
			(*dst)[k] = v
		}
	}
}

func mergeModuleMap(dst *map[string]*ModuleSpec, src map[string]*ModuleSpec) {
	if len(src) == 0 {
		return
	}
	if *dst == nil {
		*dst = make(map[string]*ModuleSpec)
	}
	for k, v := range src {
		if _, exists := (*dst)[k]; !exists {
			(*dst)[k] = v
		}
	}
}

// mergeDeployMaps merges src into dst, dst-wins on name collisions.
// Field-singular cutover: replaces the legacy mergeDeployments which
// took *DeploymentsSection wrappers. Provides now lives at UnifiedFile
// root and is merged separately by mergeUnified.
func mergeDeployMaps(dst *map[string]DeploymentNode, src map[string]DeploymentNode) {
	if len(src) == 0 {
		return
	}
	if *dst == nil {
		*dst = make(map[string]DeploymentNode)
	}
	for k, v := range src {
		if _, exists := (*dst)[k]; !exists {
			(*dst)[k] = v
		}
	}
}

// mergeImageConfig preserves dst's already-set fields and fills only the
// zero-valued ones from src. Used for merging Defaults blocks from includes.
func mergeImageConfig(dst, src *ImageConfig) {
	if src == nil || dst == nil {
		return
	}
	if dst.Base == "" {
		dst.Base = src.Base
	}
	if dst.Tag == "" {
		dst.Tag = src.Tag
	}
	if dst.Registry == "" {
		dst.Registry = src.Registry
	}
	if len(dst.Platforms) == 0 {
		dst.Platforms = src.Platforms
	}
	if len(dst.Distro) == 0 {
		dst.Distro = src.Distro
	}
	if len(dst.Build) == 0 {
		dst.Build = src.Build
	}
	if len(dst.Layer) == 0 {
		dst.Layer = src.Layer
	}
	if dst.User == "" {
		dst.User = src.User
	}
	if dst.UID == nil {
		dst.UID = src.UID
	}
	if dst.GID == nil {
		dst.GID = src.GID
	}
	if dst.UserPolicy == "" {
		dst.UserPolicy = src.UserPolicy
	}
	if dst.Merge == nil {
		dst.Merge = src.Merge
	}
	if len(dst.Builder) == 0 {
		dst.Builder = src.Builder
	}
	if dst.Init == "" {
		dst.Init = src.Init
	}
}

// mergeKindDoc routes a kind-keyed single-entity document into the correct
// map on merged. Explicit map entries always win over discovered entries
// (same rule as the discover scanner), so the check is "register unless
// already present."
func mergeKindDoc(merged *UnifiedFile, kd *kindKeyedDoc, srcDir string) error {
	count := 0
	if kd.Layer != nil {
		count++
	}
	if kd.Image != nil {
		count++
	}
	if kd.Deploy != nil {
		count++
	}
	if kd.Builder != nil {
		count++
	}
	if kd.Distro != nil {
		count++
	}
	if kd.Init != nil {
		count++
	}
	if kd.VM != nil {
		count++
	}
	if kd.Pod != nil {
		count++
	}
	if kd.K8s != nil {
		count++
	}
	if kd.Local != nil {
		count++
	}
	if kd.AI != nil {
		count++
	}
	if kd.Recipe != nil {
		count++
	}
	if kd.Score != nil {
		count++
	}
	if kd.Group != nil {
		count++
	}
	if kd.Target != nil {
		count++
	}
	if kd.Module != nil {
		count++
	}
	if count > 1 {
		return fmt.Errorf("ambiguous kind-keyed document: multiple kind wrappers set")
	}
	if count == 0 {
		return nil
	}
	switch {
	case kd.Layer != nil:
		if kd.Layer.Name == "" {
			return fmt.Errorf("layer: missing name")
		}
		if merged.Layer == nil {
			merged.Layer = map[string]*InlineLayer{}
		}
		if _, exists := merged.Layer[kd.Layer.Name]; !exists {
			merged.Layer[kd.Layer.Name] = &InlineLayer{LayerYAML: kd.Layer.LayerYAML}
		}
	case kd.Image != nil:
		if kd.Image.Name == "" {
			return fmt.Errorf("image: missing name")
		}
		if merged.Image == nil {
			merged.Image = map[string]ImageConfig{}
		}
		if _, exists := merged.Image[kd.Image.Name]; !exists {
			merged.Image[kd.Image.Name] = kd.Image.ImageConfig
		}
	case kd.Deploy != nil:
		if kd.Deploy.Name == "" {
			return fmt.Errorf("deployment: missing name")
		}
		if merged.Deploy == nil {
			merged.Deploy = map[string]DeploymentNode{}
		}
		if _, exists := merged.Deploy[kd.Deploy.Name]; !exists {
			merged.Deploy[kd.Deploy.Name] = kd.Deploy.DeploymentNode
		}
	case kd.Builder != nil:
		if kd.Builder.Name == "" {
			return fmt.Errorf("builder: missing name")
		}
		if merged.Builder == nil {
			merged.Builder = map[string]*BuilderDef{}
		}
		if _, exists := merged.Builder[kd.Builder.Name]; !exists {
			builder := kd.Builder.BuilderDef
			merged.Builder[kd.Builder.Name] = &builder
		}
	case kd.Distro != nil:
		if kd.Distro.Name == "" {
			return fmt.Errorf("distro: missing name")
		}
		if merged.Distro == nil {
			merged.Distro = map[string]*DistroDef{}
		}
		if _, exists := merged.Distro[kd.Distro.Name]; !exists {
			distro := kd.Distro.DistroDef
			merged.Distro[kd.Distro.Name] = &distro
		}
	case kd.Init != nil:
		if kd.Init.Name == "" {
			return fmt.Errorf("init: missing name")
		}
		if merged.Init == nil {
			merged.Init = map[string]*InitDef{}
		}
		if _, exists := merged.Init[kd.Init.Name]; !exists {
			initDef := kd.Init.InitDef
			merged.Init[kd.Init.Name] = &initDef
		}
	case kd.VM != nil:
		if kd.VM.Name == "" {
			return fmt.Errorf("vm: missing name")
		}
		if merged.VM == nil {
			merged.VM = map[string]*VmSpec{}
		}
		if _, exists := merged.VM[kd.VM.Name]; !exists {
			spec := kd.VM.Spec
			merged.VM[kd.VM.Name] = &spec
		}
	case kd.Pod != nil:
		if kd.Pod.Name == "" {
			return fmt.Errorf("pod: missing name")
		}
		if merged.Pod == nil {
			merged.Pod = map[string]*PodSpec{}
		}
		if _, exists := merged.Pod[kd.Pod.Name]; !exists {
			spec := kd.Pod.PodSpec
			merged.Pod[kd.Pod.Name] = &spec
		}
	case kd.K8s != nil:
		if kd.K8s.Name == "" {
			return fmt.Errorf("k8s: missing name")
		}
		if merged.K8s == nil {
			merged.K8s = map[string]*K8sSpec{}
		}
		if _, exists := merged.K8s[kd.K8s.Name]; !exists {
			spec := kd.K8s.K8sSpec
			merged.K8s[kd.K8s.Name] = &spec
		}
	case kd.Local != nil:
		if kd.Local.Name == "" {
			return fmt.Errorf("host: missing name")
		}
		if merged.Local == nil {
			merged.Local = map[string]*LocalSpec{}
		}
		if _, exists := merged.Local[kd.Local.Name]; !exists {
			spec := kd.Local.LocalSpec
			merged.Local[kd.Local.Name] = &spec
		}
	case kd.AI != nil:
		if kd.AI.Name == "" {
			return fmt.Errorf("ai: missing name")
		}
		if merged.AI == nil {
			merged.AI = map[string]*AIConfig{}
		}
		if _, exists := merged.AI[kd.AI.Name]; !exists {
			spec := kd.AI.AIConfig
			merged.AI[kd.AI.Name] = &spec
		}
	case kd.Recipe != nil:
		if kd.Recipe.Name == "" {
			return fmt.Errorf("recipe: missing name")
		}
		if merged.Recipe == nil {
			merged.Recipe = map[string]*HarnessRecipe{}
		}
		if _, exists := merged.Recipe[kd.Recipe.Name]; !exists {
			spec := kd.Recipe.HarnessRecipe
			merged.Recipe[kd.Recipe.Name] = &spec
		}
	case kd.Score != nil:
		if kd.Score.Name == "" {
			return fmt.Errorf("score: missing name")
		}
		if merged.Score == nil {
			merged.Score = map[string]*HarnessScore{}
		}
		if _, exists := merged.Score[kd.Score.Name]; !exists {
			spec := kd.Score.HarnessScore
			merged.Score[kd.Score.Name] = &spec
		}
	case kd.Group != nil:
		if kd.Group.Name == "" {
			return fmt.Errorf("group: missing name")
		}
		if merged.Group == nil {
			merged.Group = map[string]*GroupSpec{}
		}
		if _, exists := merged.Group[kd.Group.Name]; !exists {
			spec := kd.Group.GroupSpec
			merged.Group[kd.Group.Name] = &spec
		}
	case kd.Target != nil:
		if kd.Target.Name == "" {
			return fmt.Errorf("target: missing name")
		}
		if merged.Target == nil {
			merged.Target = map[string]*TargetSpec{}
		}
		if _, exists := merged.Target[kd.Target.Name]; !exists {
			spec := kd.Target.TargetSpec
			merged.Target[kd.Target.Name] = &spec
		}
	case kd.Module != nil:
		if kd.Module.Name == "" {
			return fmt.Errorf("module: missing name")
		}
		if merged.Module == nil {
			merged.Module = map[string]*ModuleSpec{}
		}
		if _, exists := merged.Module[kd.Module.Name]; !exists {
			spec := kd.Module.ModuleSpec
			merged.Module[kd.Module.Name] = &spec
		}
	}
	_ = srcDir
	return nil
}

// -----------------------------------------------------------------------------
// Discovery scanner (Part D).
// -----------------------------------------------------------------------------

// ApplyDiscover walks every scan root on uf.Discover and registers any entity
// files found, honoring the conflict rule "explicit map entries win over
// discovered entries." scanRoot resolution is relative to rootDir (the dir
// containing overthink.yml).
func (uf *UnifiedFile) ApplyDiscover(rootDir string) error {
	if uf.Discover == nil {
		return nil
	}
	cfg := uf.Discover
	// Layers come first because downstream scans (images, deployments) don't
	// interact with them here — this is purely deterministic order for error
	// messages.
	if err := applyScanSpecsLayers(cfg.Layer, rootDir, uf); err != nil {
		return err
	}
	if err := applyScanSpecsImages(cfg.Image, rootDir, uf); err != nil {
		return err
	}
	if err := applyScanSpecsDeploys(cfg.Deploy, rootDir, uf); err != nil {
		return err
	}
	if err := applyScanSpecsBuilders(cfg.Builder, rootDir, uf); err != nil {
		return err
	}
	if err := applyScanSpecsDistros(cfg.Distro, rootDir, uf); err != nil {
		return err
	}
	if err := applyScanSpecsInits(cfg.Init, rootDir, uf); err != nil {
		return err
	}
	return nil
}

// findEntityDirs walks a scan root and returns every directory that contains
// the given canonical filename. When recursive is false, only the immediate
// children of path are considered.
func findEntityDirs(path, filename string, recursive bool) ([]string, error) {
	if !dirExists(path) {
		return nil, fmt.Errorf("discover path %q does not exist", path)
	}
	var out []string
	if !recursive {
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			target := filepath.Join(path, e.Name(), filename)
			if fileExists(target) {
				out = append(out, filepath.Join(path, e.Name()))
			}
		}
		sort.Strings(out)
		return out, nil
	}
	err := filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if info.Name() == filename {
			out = append(out, filepath.Dir(p))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

func applyScanSpecsLayers(specs []ScanSpec, rootDir string, uf *UnifiedFile) error {
	for _, s := range specs {
		scanPath := s.Path
		if !filepath.IsAbs(scanPath) {
			scanPath = filepath.Join(rootDir, scanPath)
		}
		dirs, err := findEntityDirs(scanPath, "layer.yml", s.Recursive)
		if err != nil {
			return fmt.Errorf("discover.layers %q: %w", s.Path, err)
		}
		if uf.Layer == nil {
			uf.Layer = map[string]*InlineLayer{}
		}
		for _, d := range dirs {
			name := filepath.Base(d)
			if _, exists := uf.Layer[name]; exists {
				continue // explicit entry wins
			}
			// Represent as a `from:` entry pointing at the discovered dir.
			// Downstream loader calls scanLayer(d, name) to populate a *Layer.
			rel, err := filepath.Rel(rootDir, d)
			if err != nil {
				rel = d
			}
			uf.Layer[name] = &InlineLayer{From: rel}
		}
	}
	return nil
}

func applyScanSpecsImages(specs []ScanSpec, rootDir string, uf *UnifiedFile) error {
	return applyScanSpecsKindKeyed(specs, rootDir, "image.yml", func(doc *kindKeyedDoc, srcDir string) error {
		if doc.Image == nil {
			return fmt.Errorf("expected image: wrapper")
		}
		if doc.Image.Name == "" {
			doc.Image.Name = filepath.Base(srcDir)
		}
		return mergeKindDoc(uf, doc, srcDir)
	})
}

func applyScanSpecsDeploys(specs []ScanSpec, rootDir string, uf *UnifiedFile) error {
	return applyScanSpecsKindKeyed(specs, rootDir, "deploy.yml", func(doc *kindKeyedDoc, srcDir string) error {
		if doc.Deploy == nil {
			return fmt.Errorf("expected deployment: wrapper")
		}
		if doc.Deploy.Name == "" {
			doc.Deploy.Name = filepath.Base(srcDir)
		}
		return mergeKindDoc(uf, doc, srcDir)
	})
}

func applyScanSpecsBuilders(specs []ScanSpec, rootDir string, uf *UnifiedFile) error {
	return applyScanSpecsKindKeyed(specs, rootDir, "builder.yml", func(doc *kindKeyedDoc, srcDir string) error {
		if doc.Builder == nil {
			return fmt.Errorf("expected builder: wrapper")
		}
		if doc.Builder.Name == "" {
			doc.Builder.Name = filepath.Base(srcDir)
		}
		return mergeKindDoc(uf, doc, srcDir)
	})
}

func applyScanSpecsDistros(specs []ScanSpec, rootDir string, uf *UnifiedFile) error {
	return applyScanSpecsKindKeyed(specs, rootDir, "distro.yml", func(doc *kindKeyedDoc, srcDir string) error {
		if doc.Distro == nil {
			return fmt.Errorf("expected distro: wrapper")
		}
		if doc.Distro.Name == "" {
			doc.Distro.Name = filepath.Base(srcDir)
		}
		return mergeKindDoc(uf, doc, srcDir)
	})
}

func applyScanSpecsInits(specs []ScanSpec, rootDir string, uf *UnifiedFile) error {
	return applyScanSpecsKindKeyed(specs, rootDir, "init.yml", func(doc *kindKeyedDoc, srcDir string) error {
		if doc.Init == nil {
			return fmt.Errorf("expected init: wrapper")
		}
		if doc.Init.Name == "" {
			doc.Init.Name = filepath.Base(srcDir)
		}
		return mergeKindDoc(uf, doc, srcDir)
	})
}

// applyScanSpecsKindKeyed is the generic body for images/deployments/builders/
// distros/inits. Layers use applyScanSpecsLayers because the file format
// currently differs (flat LayerYAML — the kind-keyed wrapping is introduced
// by the migration in Part G).
func applyScanSpecsKindKeyed(specs []ScanSpec, rootDir, filename string, perDir func(*kindKeyedDoc, string) error) error {
	for _, s := range specs {
		scanPath := s.Path
		if !filepath.IsAbs(scanPath) {
			scanPath = filepath.Join(rootDir, scanPath)
		}
		dirs, err := findEntityDirs(scanPath, filename, s.Recursive)
		if err != nil {
			return fmt.Errorf("discover %q: %w", s.Path, err)
		}
		for _, d := range dirs {
			target := filepath.Join(d, filename)
			data, err := os.ReadFile(target)
			if err != nil {
				return fmt.Errorf("reading %s: %w", target, err)
			}
			decoder := yaml.NewDecoder(strings.NewReader(string(data)))
			for {
				var kd kindKeyedDoc
				if err := decoder.Decode(&kd); err != nil {
					if err.Error() == "EOF" {
						break
					}
					return fmt.Errorf("%s: %w", target, err)
				}
				if err := perDir(&kd, d); err != nil {
					return fmt.Errorf("%s: %w", target, err)
				}
			}
		}
	}
	return nil
}

// -----------------------------------------------------------------------------
// Projections — extract the existing concrete types from UnifiedFile so the
// existing loaders can become thin wrappers.
// -----------------------------------------------------------------------------

// ProjectConfig returns the *Config equivalent of uf (image.yml view).
func (uf *UnifiedFile) ProjectConfig() *Config {
	images := uf.Image
	if images == nil {
		images = map[string]ImageConfig{}
	}
	return &Config{
		Defaults: uf.Defaults,
		Image:    images,
	}
}

// ProjectDistroConfig returns the *DistroConfig equivalent (distros: section).
func (uf *UnifiedFile) ProjectDistroConfig() *DistroConfig {
	if len(uf.Distro) == 0 {
		return nil
	}
	return &DistroConfig{Distro: uf.Distro}
}

// ProjectBuilderConfig returns the *BuilderConfig equivalent (builders: section).
func (uf *UnifiedFile) ProjectBuilderConfig() *BuilderConfig {
	if len(uf.Builder) == 0 {
		return nil
	}
	return &BuilderConfig{Builder: uf.Builder}
}

// ProjectInitConfig returns the *InitConfig equivalent (inits: section).
func (uf *UnifiedFile) ProjectInitConfig() *InitConfig {
	if len(uf.Init) == 0 {
		return nil
	}
	return &InitConfig{Init: uf.Init}
}

// ProjectDeployConfig returns the *DeployConfig equivalent (deployments: section
// of the authored file, independent of any per-machine ~/.config/ov/deploy.yml
// which remains loaded separately by LoadDeployConfig).
func (uf *UnifiedFile) ProjectDeployConfig() *DeployConfig {
	if uf == nil || (len(uf.Deploy) == 0 && uf.Provides == nil) {
		return nil
	}
	return &DeployConfig{
		Provides: uf.Provides,
		Deploy:   uf.Deploy,
	}
}

// ProjectLayers scans or synthesizes a *Layer per entry in uf.Layer. Entries
// with `from:` go through the existing scanLayer so directory-based layers
// behave identically to today. Inline entries synthesize a *Layer from the
// embedded LayerYAML (Part A's `directory:` field still applies).
func (uf *UnifiedFile) ProjectLayers(rootDir string) (map[string]*Layer, error) {
	out := map[string]*Layer{}
	for name, il := range uf.Layer {
		if il == nil {
			continue
		}
		if il.From != "" {
			// Directory-based layer — reuse existing scanner.
			p := il.From
			if !filepath.IsAbs(p) {
				p = filepath.Join(rootDir, p)
			}
			layer, err := scanLayer(p, name)
			if err != nil {
				return nil, fmt.Errorf("layer %q from %q: %w", name, il.From, err)
			}
			out[name] = layer
			continue
		}
		// Inline layer — synthesize.
		layer, err := synthesizeInlineLayer(name, il, rootDir)
		if err != nil {
			return nil, fmt.Errorf("inline layer %q: %w", name, err)
		}
		out[name] = layer
	}
	return out, nil
}

// synthesizeInlineLayer produces a *Layer from an inline declaration in the
// unified file. The effective Path is rootDir (the overthink.yml's dir);
// SourceDir always equals Path (the `directory:` field was deleted in the
// 2026-05 Calamares cutover).
func synthesizeInlineLayer(name string, il *InlineLayer, rootDir string) (*Layer, error) {
	// Use inline layer body as if it were a parsed layer.yml at rootDir.
	layer := &Layer{
		Name: name,
		Path: rootDir,
	}
	layer.SourceDir = rootDir
	// Populate fields the same way scanLayer does post-parse. We reuse the
	// logic by duplicating the minimal set a test would notice; the full set
	// can be factored out alongside Part G's refactor.
	populateLayerFromYAML(layer, &il.LayerYAML)
	// Install-file detection against SourceDir.
	layer.HasPixiToml = fileExists(filepath.Join(layer.SourceDir, "pixi.toml"))
	layer.HasPyprojectToml = fileExists(filepath.Join(layer.SourceDir, "pyproject.toml"))
	layer.HasEnvironmentYml = fileExists(filepath.Join(layer.SourceDir, "environment.yml"))
	layer.HasPackageJson = fileExists(filepath.Join(layer.SourceDir, "package.json"))
	layer.HasCargoToml = fileExists(filepath.Join(layer.SourceDir, "Cargo.toml"))
	layer.HasSrcDir = dirExists(filepath.Join(layer.SourceDir, "src"))
	layer.HasPixiLock = fileExists(filepath.Join(layer.SourceDir, "pixi.lock"))
	svcFiles, _ := filepath.Glob(filepath.Join(layer.SourceDir, "*.service"))
	if len(svcFiles) > 0 {
		layer.serviceFiles = svcFiles
	}
	return layer, nil
}

// populateLayerFromYAML copies fields from a LayerYAML into the legacy Layer
// struct, mirroring scanLayer's post-parse block without the install-file
// detection (which synthesizeInlineLayer does separately against SourceDir).
func populateLayerFromYAML(layer *Layer, ly *LayerYAML) {
	layer.Version = ly.Version
	layer.Description = ly.Description
	layer.Status = descriptionStatus(ly.Description)
	layer.Info = descriptionInfo(ly.Description)

	layer.RawRequire = ly.Require
	layer.Require = make([]string, len(ly.Require))
	for i, dep := range ly.Require {
		layer.Require[i] = BareRef(dep)
	}
	layer.RawIncludedLayer = ly.Layer
	layer.IncludedLayer = make([]string, len(ly.Layer))
	for i, ref := range ly.Layer {
		layer.IncludedLayer[i] = BareRef(ref)
	}
	layer.service = ly.Service
	layer.HasEnv = len(ly.Env) > 0 || len(ly.PathAppend) > 0
	layer.HasPorts = len(ly.Port) > 0
	layer.HasRoute = ly.Route != nil
	layer.formatSections = ly.FormatSections
	if layer.formatSections == nil {
		layer.formatSections = make(map[string]*PackageSection)
	}
	layer.tagSections = ly.TagSections
	// 2026-05 Calamares cutover: derive format/tag sections from packages: + distros:.
	if len(ly.Package) > 0 || len(ly.Distro) > 0 {
		derivePackageSectionsFromCalamares(layer, ly)
	}
	if layer.HasPorts {
		layer.ports = make([]string, len(ly.Port))
		layer.portSpecs = make([]PortSpec, len(ly.Port))
		for i, p := range ly.Port {
			if p.Protocol == "udp" {
				layer.ports[i] = fmt.Sprintf("%d/udp", p.Port)
			} else {
				layer.ports[i] = fmt.Sprintf("%d", p.Port)
			}
			layer.portSpecs[i] = p
		}
	}
	if layer.HasEnv {
		env := ly.Env
		if env == nil {
			env = make(map[string]string)
		}
		layer.envConfig = &EnvConfig{Vars: env, PathAppend: ly.PathAppend}
	}
	if ly.Route != nil {
		layer.route = &RouteConfig{Host: ly.Route.Host, Port: fmt.Sprintf("%d", ly.Route.Port)}
	}
	layer.HasVolumes = len(ly.Volume) > 0
	layer.volumes = ly.Volume
	layer.HasAliases = len(ly.Alias) > 0
	layer.aliases = ly.Alias
	layer.HasExtract = len(ly.Extract) > 0
	layer.extract = ly.Extract
	layer.HasData = len(ly.Data) > 0
	layer.data = ly.Data
	layer.security = ly.Security
	if len(ly.Libvirt) > 0 {
		layer.HasLibvirt = true
		layer.libvirt = ly.Libvirt
	}
	layer.hooks = ly.Hooks
	layer.tests = ly.Eval
	layer.PortRelayPorts = ly.PortRelay
	layer.secrets = ly.SecretYAML
	if len(ly.EnvProvides) > 0 {
		layer.HasEnvProvides = true
		layer.envProvides = ly.EnvProvides
	}
	if len(ly.EnvRequire) > 0 {
		layer.HasEnvRequires = true
		layer.envRequires = ly.EnvRequire
	}
	if len(ly.EnvAccept) > 0 {
		layer.HasEnvAccepts = true
		layer.envAccepts = ly.EnvAccept
	}
	if len(ly.SecretAccept) > 0 {
		layer.HasSecretAccepts = true
		layer.secretAccepts = ly.SecretAccept
	}
	if len(ly.SecretRequire) > 0 {
		layer.HasSecretRequires = true
		layer.secretRequires = ly.SecretRequire
	}
	if len(ly.MCPProvide) > 0 {
		layer.HasMCPProvides = true
		layer.mcpProvides = ly.MCPProvide
	}
	if len(ly.MCPRequire) > 0 {
		layer.HasMCPRequires = true
		layer.mcpRequires = ly.MCPRequire
	}
	if len(ly.MCPAccept) > 0 {
		layer.HasMCPAccepts = true
		layer.mcpAccepts = ly.MCPAccept
	}
	layer.engine = ly.Engine
	layer.vars = ly.Vars
	layer.tasks = ly.Task
	layer.HasTasks = len(ly.Task) > 0
}
