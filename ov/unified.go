package main

import (
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
	Includes []string               `yaml:"includes,omitempty"`
	Discover *DiscoverConfig        `yaml:"discover,omitempty"`
	Distros  map[string]*DistroDef  `yaml:"distros,omitempty"`
	Builders map[string]*BuilderDef `yaml:"builders,omitempty"`
	Inits    map[string]*InitDef    `yaml:"inits,omitempty"`
	Defaults ImageConfig            `yaml:"defaults,omitempty"`
	Images   map[string]ImageConfig `yaml:"images,omitempty"`
	// Schema v4 singular alias. Populated by ImageSingular during YAML
	// unmarshal; merged into Images post-load via normalizeAliases.
	ImageSingular map[string]ImageConfig  `yaml:"image,omitempty"`
	Layers        map[string]*InlineLayer `yaml:"layers,omitempty"`
	VM            map[string]*VmSpec      `yaml:"vm,omitempty"`
	Deployments   *DeploymentsSection     `yaml:"deployments,omitempty"`
	// Schema v4 singular alias for Deployments. Populated via
	// DeploymentSingular; merged post-load.
	DeploymentSingular map[string]DeploymentNode `yaml:"deployment,omitempty"`

	// Schema v4: first-class target template maps (singular keys).
	Pod  map[string]*PodSpec  `yaml:"pod,omitempty"`
	K8s  map[string]*K8sSpec  `yaml:"k8s,omitempty"`
	Host map[string]*HostSpec `yaml:"host,omitempty"`

	// AI catalog (kind:ai), harness recipes (kind:recipe = pure spec),
	// and harness scores (kind:score = runner config that references
	// recipes). See ai_config.go, harness_recipe.go, harness_score_kind.go,
	// /ov:harness.
	AI     map[string]*AIConfig      `yaml:"ai,omitempty"`
	Recipe map[string]*HarnessRecipe `yaml:"recipe,omitempty"`
	Score  map[string]*HarnessScore  `yaml:"score,omitempty"`
}

// DiscoverConfig drives filesystem scans for standalone kind-keyed files. Each
// sub-key is independent; a file with only `discover.layers:` is common.
type DiscoverConfig struct {
	Layers      []ScanSpec `yaml:"layers,omitempty"`
	Images      []ScanSpec `yaml:"images,omitempty"`
	Deployments []ScanSpec `yaml:"deployments,omitempty"`
	Builders    []ScanSpec `yaml:"builders,omitempty"`
	Distros     []ScanSpec `yaml:"distros,omitempty"`
	Inits       []ScanSpec `yaml:"inits,omitempty"`
	VM          []ScanSpec `yaml:"vm,omitempty"`
	Clusters    []ScanSpec `yaml:"clusters,omitempty"` // reserved for Part F
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
type DeploymentsSection struct {
	Defaults *DeploymentNode           `yaml:"defaults,omitempty"`
	Provides *ProvidesConfig           `yaml:"provides,omitempty"`
	Images   map[string]DeploymentNode `yaml:"images,omitempty"`
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
	Deployment *DeploymentDoc `yaml:"deployment,omitempty"`
	Builder    *BuilderDoc    `yaml:"builder,omitempty"`
	Distro     *DistroDoc     `yaml:"distro,omitempty"`
	Init       *InitDoc       `yaml:"init,omitempty"`
	VM         *VmDoc         `yaml:"vm,omitempty"`
	// Schema v4 first-class target templates.
	Pod  *PodDoc  `yaml:"pod,omitempty"`
	K8s  *K8sDoc  `yaml:"k8s,omitempty"`
	Host *HostDoc `yaml:"host,omitempty"`
	// 2026-04 harness cutover.
	AI     *AIDoc     `yaml:"ai,omitempty"`
	Recipe *RecipeDoc `yaml:"recipe,omitempty"`
	Score  *ScoreDoc  `yaml:"score,omitempty"`
}

// AIDoc wraps a single AIConfig with an explicit Name — the kind:ai
// standalone form. Bundles of `kind: ai` + `name: <name>` documents
// can be concatenated via YAML --- separators in harness.yml.
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

// HostDoc wraps a single HostSpec with an explicit Name — the kind:host
// standalone form.
type HostDoc struct {
	Name     string `yaml:"name"`
	HostSpec `yaml:",inline"`
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

// DeploymentDoc wraps a single DeploymentNode.
type DeploymentDoc struct {
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
	Status      string       `yaml:"status,omitempty"`      // [DEPRECATED - migrate to description.tags]
	Info        string       `yaml:"info,omitempty"`        // [DEPRECATED - migrate to description.feature+narrative]
	Description *Description `yaml:"description,omitempty"` // Gherkin-shaped self-description; replaces Info/Status
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
	{Key: "deployment", Filename: "deploy.yml"},
	{Key: "builder", Filename: "builder.yml"},
	{Key: "distro", Filename: "distro.yml"},
	{Key: "init", Filename: "init.yml"},
	// Schema v4 additions — first-class target template kinds. All
	// singular. All authored in overthink.yml or in their convention
	// files (pod.yml / vm.yml / k8s.yml / host.yml).
	{Key: "pod", Filename: "pod.yml"},
	{Key: "vm", Filename: "vm.yml"},
	{Key: "k8s", Filename: "k8s.yml"},
	{Key: "host", Filename: "host.yml"},
	// 2026-04 harness cutover: `ai:`, `recipe:` (pure spec) and
	// `score:` (runner config referencing recipes) kinds. Convention
	// file: harness.yml (carries all three kinds together).
	{Key: "ai", Filename: "ai.yml"},
	{Key: "recipe", Filename: "recipe.yml"},
	{Key: "score", Filename: "score.yml"},
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
func LoadUnified(dir string) (*UnifiedFile, bool, error) {
	root := filepath.Join(dir, UnifiedFileName)
	if !fileExists(root) {
		return nil, false, nil
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
	if err := validateDeploymentTree(merged.Deployments); err != nil {
		return nil, true, fmt.Errorf("%s: %w", root, err)
	}
	if err := validateHarnessSemantics(merged); err != nil {
		return nil, true, fmt.Errorf("%s: %w", root, err)
	}
	return merged, true, nil
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
func validateDeploymentTree(section *DeploymentsSection) error {
	if section == nil {
		return nil
	}
	for name, node := range section.Images {
		if err := validateDeploymentName(name, ""); err != nil {
			return err
		}
		if err := validateDeploymentChildren(name, &node); err != nil {
			return err
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
	if strings.Contains(name, legacyVmEntityName) {
		return fmt.Errorf(
			"deployment key %q references the legacy entity name %q which was renamed to %q in schema v2. Run: ov migrate merge-vms",
			full, legacyVmEntityName, currentVmEntityName,
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
	// Hard-reject the legacy vms.yml layout (v1). The file-level check
	// catches both `includes:` users and any rogue discover path. Mirror
	// of the `vms:` key-level check in classifyDoc.
	if filepath.Base(abs) == legacyVmFilename {
		return fmt.Errorf(
			"%s: vms.yml is no longer accepted. VM entities now live under the `vm:` key in deploy.yml. Run: ov migrate merge-vms",
			abs,
		)
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
			for _, inc := range uf.Includes {
				includesQueue = append(includesQueue, inc)
			}
			// Clear includes before merging so they don't leak into merged struct.
			uf.Includes = nil
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
	"version": true, "includes": true, "discover": true, "defaults": true,
	"distros": true, "builders": true, "inits": true,
	"layers": true,
	// v4 singular kind maps:
	"image": true, "pod": true, "vm": true, "k8s": true, "host": true,
	"deployment": true,
	// legacy aliases accepted for transitional compatibility; migration
	// rewrites them:
	"images": true, "deployments": true,
	// 2026-04 harness cutover: `ai:` and `recipe:` are recognized as
	// root-shape collection-map keys (in addition to being valid
	// kind-keyed forms). Mirrors how image/pod/vm work.
	"ai": true, "recipe": true, "score": true,
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
	hasLegacyVmsKey := false
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
		if k == legacyVmRootKey {
			hasLegacyVmsKey = true
		}
	}
	// Legacy `vms:` root key (v1 schema) — hard-rejected before the
	// generic ambiguity / unrecognized-keys branches so the user gets
	// a pointer straight at the migration command.
	if hasLegacyVmsKey {
		return 0, fmt.Errorf(
			"the `vms:` root key was renamed to `vm:` (singular) in schema v2. Run: ov migrate merge-vms",
		)
	}
	// Legacy `benchmark:` root key — replaced by kind:ai + kind:recipe
	// in the 2026-04 harness cutover.
	if hasLegacyBenchmarkKey {
		return 0, fmt.Errorf(
			"the `benchmark:` root key is no longer accepted. AI catalog and harness recipes are now kind-entities (kind:ai, kind:recipe) living in harness.yml. Run: ov migrate harness",
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
//   1. **Fat-recipe shape**: a `recipe:` entry whose body carries any
//      key other than {description, scenario}. Pre-cutover recipes
//      mixed runner config (host, ai, plateau_iteration, prompt, …)
//      with spec; post-cutover those move to `score:`.
//
//   2. **Residual `max_iteration:`** anywhere on `recipe:` or `score:`
//      bodies. The post-cutover loop bound is plateau-only.
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
	allowed := map[string]bool{"description": true, "scenario": true}
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
	// Every kind:recipe scenario must declare `pod:`. The harness scoring
	// code does `containerName := "ov-" + scenario.Pod`; without `pod:`,
	// we have no scoring target. Layer- and image-baked scenarios are NOT
	// subject to this rule (they live outside the recipe: map).
	for name, recipe := range u.Recipe {
		if recipe == nil {
			continue
		}
		for i, sc := range recipe.Scenario {
			if sc.Pod == "" {
				identifier := sc.Name
				if identifier == "" {
					identifier = fmt.Sprintf("scenario[%d]", i)
				}
				return fmt.Errorf("recipe %q: scenario %q: missing required `pod:` field — every scenario in a recipe must declare the container name its steps probe (the harness has no default scoring target)", name, identifier)
			}
		}
		if err := validateRecipeScenarioDependencies(name, recipe); err != nil {
			return err
		}
	}

	for name, score := range u.Score {
		if score == nil {
			continue
		}
		if len(score.Recipes) == 0 {
			return fmt.Errorf("score %q: recipes: must reference at least one recipe (got empty list)", name)
		}
		seen := make(map[string]bool, len(score.Recipes))
		for _, r := range score.Recipes {
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

// validateRecipeScenarioDependencies enforces that every entry in
// scenario.depends_on resolves to another scenario within the SAME
// recipe (intra-recipe scope) and that the resulting dependency graph
// has no cycles. Surfaces fuzzy "did you mean" hints for typos and
// the cycle path for cycles.
func validateRecipeScenarioDependencies(recipeName string, recipe *HarnessRecipe) error {
	if recipe == nil || len(recipe.Scenario) == 0 {
		return nil
	}
	// Build the in-recipe scenario name set + duplicate check.
	names := make([]string, 0, len(recipe.Scenario))
	known := make(map[string]bool, len(recipe.Scenario))
	for i, sc := range recipe.Scenario {
		if sc.Name == "" {
			return fmt.Errorf("recipe %q: scenario[%d]: missing required `name:` field (depends_on resolution requires unique scenario names)", recipeName, i)
		}
		if known[sc.Name] {
			return fmt.Errorf("recipe %q: duplicate scenario name %q (each scenario name must be unique within a recipe for depends_on resolution)", recipeName, sc.Name)
		}
		known[sc.Name] = true
		names = append(names, sc.Name)
	}
	// Validate every depends_on entry resolves intra-recipe.
	for _, sc := range recipe.Scenario {
		for _, dep := range sc.DependsOn {
			if dep == sc.Name {
				return fmt.Errorf("recipe %q: scenario %q: depends_on cannot reference itself (%q)", recipeName, sc.Name, dep)
			}
			if !known[dep] {
				suggestion := findSimilarName(dep, names)
				if suggestion != "" {
					return fmt.Errorf("recipe %q: scenario %q: depends_on: unknown scenario %q (did you mean %q?). Cross-recipe depends_on is not supported — the referenced scenario must live in the same recipe", recipeName, sc.Name, dep, suggestion)
				}
				return fmt.Errorf("recipe %q: scenario %q: depends_on: unknown scenario %q. Cross-recipe depends_on is not supported — the referenced scenario must live in the same recipe (available scenarios in this recipe: %s)", recipeName, sc.Name, dep, strings.Join(names, ", "))
			}
		}
	}
	// Cycle detection — defer to topoSortByDeclarationOrder which
	// returns *CycleError on cycle. We don't need the sorted output
	// here, just the error.
	if _, err := topoSortByDeclarationOrder(recipe.Scenario); err != nil {
		if cycleErr, ok := err.(*CycleError); ok {
			return fmt.Errorf("recipe %q: scenario depends_on cycle: %s", recipeName, strings.Join(cycleErr.Cycle, " -> "))
		}
		return fmt.Errorf("recipe %q: depends_on resolution failed: %w", recipeName, err)
	}
	return nil
}

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

// normalizeV4Aliases folds the schema-v4 singular root-shape keys (image:,
// deployment:) into the canonical plural fields used by the rest of the
// codebase (Images, Deployments.Images). Makes the loader accept both
// forms interchangeably during the v4 migration window.
func normalizeV4Aliases(u *UnifiedFile) {
	if u == nil {
		return
	}
	if len(u.ImageSingular) > 0 {
		if u.Images == nil {
			u.Images = make(map[string]ImageConfig)
		}
		for k, v := range u.ImageSingular {
			if _, ok := u.Images[k]; !ok {
				u.Images[k] = v
			}
		}
		u.ImageSingular = nil
	}
	if len(u.DeploymentSingular) > 0 {
		if u.Deployments == nil {
			u.Deployments = &DeploymentsSection{}
		}
		if u.Deployments.Images == nil {
			u.Deployments.Images = make(map[string]DeploymentNode)
		}
		for k, v := range u.DeploymentSingular {
			if _, ok := u.Deployments.Images[k]; !ok {
				u.Deployments.Images[k] = v
			}
		}
		u.DeploymentSingular = nil
	}
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
		dst.Discover.Layers = append(dst.Discover.Layers, src.Discover.Layers...)
		dst.Discover.Images = append(dst.Discover.Images, src.Discover.Images...)
		dst.Discover.Deployments = append(dst.Discover.Deployments, src.Discover.Deployments...)
		dst.Discover.Builders = append(dst.Discover.Builders, src.Discover.Builders...)
		dst.Discover.Distros = append(dst.Discover.Distros, src.Discover.Distros...)
		dst.Discover.Inits = append(dst.Discover.Inits, src.Discover.Inits...)
		dst.Discover.VM = append(dst.Discover.VM, src.Discover.VM...)
		dst.Discover.Clusters = append(dst.Discover.Clusters, src.Discover.Clusters...)
	}
	mergeDistroMap(&dst.Distros, src.Distros)
	mergeBuilderMap(&dst.Builders, src.Builders)
	mergeInitMap(&dst.Inits, src.Inits)
	mergeImageMap(&dst.Images, src.Images)
	mergeLayerMap(&dst.Layers, src.Layers)
	mergeVmMap(&dst.VM, src.VM)
	mergePodMap(&dst.Pod, src.Pod)
	mergeK8sMap(&dst.K8s, src.K8s)
	mergeHostMap(&dst.Host, src.Host)
	mergeAIMap(&dst.AI, src.AI)
	mergeRecipeMap(&dst.Recipe, src.Recipe)
	mergeScoreMap(&dst.Score, src.Score)
	mergeDeployments(&dst.Deployments, src.Deployments)
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

func mergeHostMap(dst *map[string]*HostSpec, src map[string]*HostSpec) {
	if len(src) == 0 {
		return
	}
	if *dst == nil {
		*dst = make(map[string]*HostSpec)
	}
	for k, v := range src {
		if _, exists := (*dst)[k]; !exists {
			(*dst)[k] = v
		}
	}
}

func mergeDeployments(dst **DeploymentsSection, src *DeploymentsSection) {
	if src == nil {
		return
	}
	if *dst == nil {
		*dst = &DeploymentsSection{}
	}
	d := *dst
	if d.Defaults == nil && src.Defaults != nil {
		d.Defaults = src.Defaults
	}
	if d.Provides == nil && src.Provides != nil {
		d.Provides = src.Provides
	}
	if len(src.Images) > 0 {
		if d.Images == nil {
			d.Images = make(map[string]DeploymentNode)
		}
		for k, v := range src.Images {
			if _, exists := d.Images[k]; !exists {
				d.Images[k] = v
			}
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
	if len(dst.Layers) == 0 {
		dst.Layers = src.Layers
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
	if kd.Deployment != nil {
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
	if kd.Host != nil {
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
		if merged.Layers == nil {
			merged.Layers = map[string]*InlineLayer{}
		}
		if _, exists := merged.Layers[kd.Layer.Name]; !exists {
			merged.Layers[kd.Layer.Name] = &InlineLayer{LayerYAML: kd.Layer.LayerYAML}
		}
	case kd.Image != nil:
		if kd.Image.Name == "" {
			return fmt.Errorf("image: missing name")
		}
		if merged.Images == nil {
			merged.Images = map[string]ImageConfig{}
		}
		if _, exists := merged.Images[kd.Image.Name]; !exists {
			merged.Images[kd.Image.Name] = kd.Image.ImageConfig
		}
	case kd.Deployment != nil:
		if kd.Deployment.Name == "" {
			return fmt.Errorf("deployment: missing name")
		}
		if merged.Deployments == nil {
			merged.Deployments = &DeploymentsSection{}
		}
		if merged.Deployments.Images == nil {
			merged.Deployments.Images = map[string]DeploymentNode{}
		}
		if _, exists := merged.Deployments.Images[kd.Deployment.Name]; !exists {
			merged.Deployments.Images[kd.Deployment.Name] = kd.Deployment.DeploymentNode
		}
	case kd.Builder != nil:
		if kd.Builder.Name == "" {
			return fmt.Errorf("builder: missing name")
		}
		if merged.Builders == nil {
			merged.Builders = map[string]*BuilderDef{}
		}
		if _, exists := merged.Builders[kd.Builder.Name]; !exists {
			builder := kd.Builder.BuilderDef
			merged.Builders[kd.Builder.Name] = &builder
		}
	case kd.Distro != nil:
		if kd.Distro.Name == "" {
			return fmt.Errorf("distro: missing name")
		}
		if merged.Distros == nil {
			merged.Distros = map[string]*DistroDef{}
		}
		if _, exists := merged.Distros[kd.Distro.Name]; !exists {
			distro := kd.Distro.DistroDef
			merged.Distros[kd.Distro.Name] = &distro
		}
	case kd.Init != nil:
		if kd.Init.Name == "" {
			return fmt.Errorf("init: missing name")
		}
		if merged.Inits == nil {
			merged.Inits = map[string]*InitDef{}
		}
		if _, exists := merged.Inits[kd.Init.Name]; !exists {
			initDef := kd.Init.InitDef
			merged.Inits[kd.Init.Name] = &initDef
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
	case kd.Host != nil:
		if kd.Host.Name == "" {
			return fmt.Errorf("host: missing name")
		}
		if merged.Host == nil {
			merged.Host = map[string]*HostSpec{}
		}
		if _, exists := merged.Host[kd.Host.Name]; !exists {
			spec := kd.Host.HostSpec
			merged.Host[kd.Host.Name] = &spec
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
	if err := applyScanSpecsLayers(cfg.Layers, rootDir, uf); err != nil {
		return err
	}
	if err := applyScanSpecsImages(cfg.Images, rootDir, uf); err != nil {
		return err
	}
	if err := applyScanSpecsDeployments(cfg.Deployments, rootDir, uf); err != nil {
		return err
	}
	if err := applyScanSpecsBuilders(cfg.Builders, rootDir, uf); err != nil {
		return err
	}
	if err := applyScanSpecsDistros(cfg.Distros, rootDir, uf); err != nil {
		return err
	}
	if err := applyScanSpecsInits(cfg.Inits, rootDir, uf); err != nil {
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
		if uf.Layers == nil {
			uf.Layers = map[string]*InlineLayer{}
		}
		for _, d := range dirs {
			name := filepath.Base(d)
			if _, exists := uf.Layers[name]; exists {
				continue // explicit entry wins
			}
			// Represent as a `from:` entry pointing at the discovered dir.
			// Downstream loader calls scanLayer(d, name) to populate a *Layer.
			rel, err := filepath.Rel(rootDir, d)
			if err != nil {
				rel = d
			}
			uf.Layers[name] = &InlineLayer{From: rel}
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

func applyScanSpecsDeployments(specs []ScanSpec, rootDir string, uf *UnifiedFile) error {
	return applyScanSpecsKindKeyed(specs, rootDir, "deploy.yml", func(doc *kindKeyedDoc, srcDir string) error {
		if doc.Deployment == nil {
			return fmt.Errorf("expected deployment: wrapper")
		}
		if doc.Deployment.Name == "" {
			doc.Deployment.Name = filepath.Base(srcDir)
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
	images := uf.Images
	if images == nil {
		images = map[string]ImageConfig{}
	}
	return &Config{
		Defaults: uf.Defaults,
		Images:   images,
	}
}

// ProjectDistroConfig returns the *DistroConfig equivalent (distros: section).
func (uf *UnifiedFile) ProjectDistroConfig() *DistroConfig {
	if len(uf.Distros) == 0 {
		return nil
	}
	return &DistroConfig{Distro: uf.Distros}
}

// ProjectBuilderConfig returns the *BuilderConfig equivalent (builders: section).
func (uf *UnifiedFile) ProjectBuilderConfig() *BuilderConfig {
	if len(uf.Builders) == 0 {
		return nil
	}
	return &BuilderConfig{Builder: uf.Builders}
}

// ProjectInitConfig returns the *InitConfig equivalent (inits: section).
func (uf *UnifiedFile) ProjectInitConfig() *InitConfig {
	if len(uf.Inits) == 0 {
		return nil
	}
	return &InitConfig{Init: uf.Inits}
}

// ProjectDeployConfig returns the *DeployConfig equivalent (deployments: section
// of the authored file, independent of any per-machine ~/.config/ov/deploy.yml
// which remains loaded separately by LoadDeployConfig).
func (uf *UnifiedFile) ProjectDeployConfig() *DeployConfig {
	if uf.Deployments == nil {
		return nil
	}
	return &DeployConfig{
		Provides:   uf.Deployments.Provides,
		Deployment: uf.Deployments.Images,
	}
}

// ProjectLayers scans or synthesizes a *Layer per entry in uf.Layers. Entries
// with `from:` go through the existing scanLayer so directory-based layers
// behave identically to today. Inline entries synthesize a *Layer from the
// embedded LayerYAML (Part A's `directory:` field still applies).
func (uf *UnifiedFile) ProjectLayers(rootDir string) (map[string]*Layer, error) {
	out := map[string]*Layer{}
	for name, il := range uf.Layers {
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
// unified file. The effective Path is rootDir (the overthink.yml's dir), and
// SourceDir honors the layer's `directory:` field via resolveLayerSourceDir.
func synthesizeInlineLayer(name string, il *InlineLayer, rootDir string) (*Layer, error) {
	// Use inline layer body as if it were a parsed layer.yml at rootDir.
	layer := &Layer{
		Name: name,
		Path: rootDir,
	}
	layer.SourceDir = resolveLayerSourceDir(rootDir, il.LayerYAML.Directory)
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
	layer.Status = ly.Status
	layer.Info = ly.Info

	layer.RawDepends = ly.Depends
	layer.Depends = make([]string, len(ly.Depends))
	for i, dep := range ly.Depends {
		layer.Depends[i] = BareRef(dep)
	}
	layer.RawIncludedLayers = ly.Layers
	layer.IncludedLayers = make([]string, len(ly.Layers))
	for i, ref := range ly.Layers {
		layer.IncludedLayers[i] = BareRef(ref)
	}
	layer.service = ly.Service
	layer.HasEnv = len(ly.Env) > 0 || len(ly.PathAppend) > 0
	layer.HasPorts = len(ly.Ports) > 0
	layer.HasRoute = ly.Route != nil
	layer.formatSections = ly.FormatSections
	if layer.formatSections == nil {
		layer.formatSections = make(map[string]*PackageSection)
	}
	layer.tagSections = ly.TagSections
	if layer.HasPorts {
		layer.ports = make([]string, len(ly.Ports))
		layer.portSpecs = make([]PortSpec, len(ly.Ports))
		for i, p := range ly.Ports {
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
	layer.HasVolumes = len(ly.Volumes) > 0
	layer.volumes = ly.Volumes
	layer.HasAliases = len(ly.Aliases) > 0
	layer.aliases = ly.Aliases
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
	layer.tests = ly.Tests
	layer.PortRelayPorts = ly.PortRelay
	layer.secrets = ly.SecretsYAML
	if len(ly.EnvProvides) > 0 {
		layer.HasEnvProvides = true
		layer.envProvides = ly.EnvProvides
	}
	if len(ly.EnvRequires) > 0 {
		layer.HasEnvRequires = true
		layer.envRequires = ly.EnvRequires
	}
	if len(ly.EnvAccepts) > 0 {
		layer.HasEnvAccepts = true
		layer.envAccepts = ly.EnvAccepts
	}
	if len(ly.SecretAccepts) > 0 {
		layer.HasSecretAccepts = true
		layer.secretAccepts = ly.SecretAccepts
	}
	if len(ly.SecretRequires) > 0 {
		layer.HasSecretRequires = true
		layer.secretRequires = ly.SecretRequires
	}
	if len(ly.MCPProvides) > 0 {
		layer.HasMCPProvides = true
		layer.mcpProvides = ly.MCPProvides
	}
	if len(ly.MCPRequires) > 0 {
		layer.HasMCPRequires = true
		layer.mcpRequires = ly.MCPRequires
	}
	if len(ly.MCPAccepts) > 0 {
		layer.HasMCPAccepts = true
		layer.mcpAccepts = ly.MCPAccepts
	}
	layer.engine = ly.Engine
	layer.vars = ly.Vars
	layer.tasks = ly.Tasks
	layer.HasTasks = len(ly.Tasks) > 0
}
