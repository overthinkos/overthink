package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// namespaceAliasRe constrains an `import:` namespace alias to a bare
// lowercase-hyphenated identifier — no dots, since `.` is the
// qualified-reference separator (`alias.entry`).
var namespaceAliasRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// -----------------------------------------------------------------------------
// Unified YAML Format — Parts B/C/D/E of the refactor plan.
//
// A single `charly.yml` (+ optional companion files via `includes:`) carries
// everything today's four files carry:
//   - build.yml    → distros: + builders: + inits:
//   - box.yml    → defaults: + images:
//   - deploy.yml   → deployments:
//   - candy.yml    → layers: map entries, or discovered via discover.layers:
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
const UnifiedFileName = "charly.yml"

// The on-disk charly.yml schema version is a CalVer string (e.g.
// 2026.141.1530) — the same scheme as image tags. LatestSchemaVersion()
// (migrate_registry.go) is the curated HEAD value; the LoadUnified gate
// refuses anything older with a hint pointing at `charly migrate`.

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
// `charly migrate` for the one-shot migration from v1.
type UnifiedFile struct {
	Version string `yaml:"version,omitempty"`
	// Repo is this project's canonical repo identity (e.g.
	// "github.com/overthinkos/overthink"). Optional; only meaningful on the ROOT
	// file. It lets the import-namespace loader break mutual-import cycles by
	// repo identity: a transitive import of THIS repo (at ANY pinned version)
	// resolves to the local working tree instead of fetching a divergent pinned
	// snapshot, so the root's namespace pins win. When unset, the loader falls
	// back to `git remote origin` inference (see ns_identity.go).
	Repo string `yaml:"repo,omitempty"`
	// Import is the SINGLE composition statement (the legacy `include:` key
	// was deleted in the 2026-05 import-namespace cutover). A list whose
	// items are either a bare string (flat import into THIS root namespace —
	// same-repo file splits + shared build.yml vocabulary) or a single-key
	// map `alias: ref` (a namespaced child import — cross-repo entity
	// cherry-pick, referenced qualified as `alias.entry`). See ImportList.
	Import   ImportList             `yaml:"import,omitempty"`
	Discover DiscoverConfig         `yaml:"discover,omitempty"`
	Distro   map[string]*DistroDef  `yaml:"distro,omitempty"`
	Builder  map[string]*BuilderDef `yaml:"builder,omitempty"`
	Init     map[string]*InitDef    `yaml:"init,omitempty"`
	Defaults BoxConfig              `yaml:"defaults,omitempty"`
	// Field-singular cutover (2026-05): legacy plural `Images yaml:"images"`
	// deleted; the singular `Image yaml:"box"` is the canonical surface.
	Image map[string]BoxConfig    `yaml:"box,omitempty"`
	Layer map[string]*InlineCandy `yaml:"candy,omitempty"`
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

	// Android (kind:android) — Android device substrates (an in-pod emulator
	// or a remote/physical adb endpoint) onto which `apk:` packages install
	// via a `target: android` deploy. Modeled on K8s (the device is the
	// substrate; the apps ride in on the deploy's layers). See android_spec.go.
	Android map[string]*AndroidSpec `yaml:"android,omitempty"`

	// AI catalog (kind:ai), harness recipes (kind:recipe = pure spec),
	// and harness scores (kind:score = runner config that references
	// recipes). See ai_config.go, harness_recipe.go, harness_score_kind.go,
	// /charly:harness.
	AI     map[string]*AIConfig      `yaml:"ai,omitempty"`
	Recipe map[string]*HarnessRecipe `yaml:"recipe,omitempty"`
	Score  map[string]*HarnessScore  `yaml:"score,omitempty"`

	// Eval (kind:eval) — disposable R10 test beds. A deploy-shaped map
	// (bed-name → DeploymentNode) authored in eval.yml alongside the
	// recipe/score framework. foldEvalBeds() copies each entry into the
	// Deploy map (EvalBed=true) at load time so every deploy verb resolves
	// a bed by name through the SAME path as any deploy; `charly eval run <bed>`
	// drives the full R10 sequence. EvalBeds() enumerates them.
	Eval map[string]DeploymentNode `yaml:"eval,omitempty"`

	// Calamares-aligned kinds (2026-05 cutover). `group:` ↔ Calamares
	// netinstall package group; `target:` ↔ Calamares settings.conf
	// install target; `module:` ↔ Calamares module.desc descriptor.
	// Convention files: groups.yml / targets.yml / modules.yml — or
	// inlined in charly.yml. Importers/emitters are deferred to a
	// follow-up additive PR; this cutover lands the schema.
	Group  map[string]*GroupSpec  `yaml:"group,omitempty"`
	Target map[string]*TargetSpec `yaml:"target,omitempty"`
	Module map[string]*ModuleSpec `yaml:"module,omitempty"`

	// Resource (kind:resource) — exclusive host-resource definitions: a token
	// name (matching requires_exclusive: / preemptible.holds:) → an optional
	// hardware selector (e.g. gpu.vendor) that drives GPU auto-allocation at
	// `charly vm create`. Build-vocab VALUE map, flat-imported from build.yml.
	Resource map[string]*ResourceDef `yaml:"resource,omitempty"`

	// Namespaces holds child namespaces mounted by namespaced `import:`
	// entries (alias → fully-resolved isolated UnifiedFile). NOT authored
	// directly and NOT flat-merged into the root maps — populated at load
	// time by loadUnifiedInto. Entries are referenced qualified, e.g.
	// `base: cachyos.cachyos` resolves `cachyos` in Namespaces, then its
	// Image["cachyos"]. Bare refs inside a namespace resolve within that
	// namespace first (Go package-member semantics). See charly/namespace.go.
	Namespaces map[string]*UnifiedFile `yaml:"-"`
}

// ImportEntry is one parsed `import:` list item. A flat entry (Namespace == "")
// merges the referenced file into the current root namespace; a namespaced
// entry mounts the referenced project under Namespace.
type ImportEntry struct {
	Namespace string // "" = flat import into the current root namespace
	Ref       string // local path or `@host/org/repo[/sub/path]:version`
}

// ImportList is the `import:` field type. Custom YAML decoding accepts a list
// whose items are either a bare string (flat) or a single-key mapping
// `alias: ref` (namespaced child import).
type ImportList []ImportEntry

// UnmarshalYAML decodes the mixed-shape import list.
func (il *ImportList) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.SequenceNode {
		return fmt.Errorf("import: must be a list (got kind=%v)", node.Kind)
	}
	out := make(ImportList, 0, len(node.Content))
	for i, item := range node.Content {
		switch item.Kind {
		case yaml.ScalarNode:
			if item.Value == "" {
				return fmt.Errorf("import[%d]: empty ref", i)
			}
			out = append(out, ImportEntry{Ref: item.Value})
		case yaml.MappingNode:
			if len(item.Content) != 2 {
				return fmt.Errorf("import[%d]: a namespaced entry must be a single-key map `alias: ref`", i)
			}
			alias := item.Content[0].Value
			ref := item.Content[1].Value
			if alias == "" || ref == "" {
				return fmt.Errorf("import[%d]: namespaced entry needs both an alias and a ref", i)
			}
			out = append(out, ImportEntry{Namespace: alias, Ref: ref})
		default:
			return fmt.Errorf("import[%d]: each item must be a string ref or a single-key `alias: ref` map (got kind=%v)", i, item.Kind)
		}
	}
	*il = out
	return nil
}

// MarshalYAML emits each entry compactly: a flat entry as a scalar string, a
// namespaced entry as a single-key `alias: ref` map — the same shapes
// UnmarshalYAML accepts (round-trip safe; used by migrators that write configs).
func (il ImportList) MarshalYAML() (interface{}, error) {
	seq := &yaml.Node{Kind: yaml.SequenceNode}
	for _, e := range il {
		if e.Namespace == "" {
			seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: e.Ref})
			continue
		}
		seq.Content = append(seq.Content, &yaml.Node{
			Kind: yaml.MappingNode,
			Content: []*yaml.Node{
				{Kind: yaml.ScalarNode, Value: e.Namespace},
				{Kind: yaml.ScalarNode, Value: e.Ref},
			},
		})
	}
	return seq, nil
}

// DiscoverConfig is a FLAT list of generic scan specs. Each spec scans a path
// for directories containing its manifest; every discovered manifest is parsed
// as a multi-document stream and routed by SHAPE (the kind-key it carries), so
// one discover root can surface candies, boxes, deploys — any kind. There is no
// kind dimension and no hardcoded path/filename: discovery is fully configured
// in charly.yml.
type DiscoverConfig []ScanSpec

// ScanSpec describes one discovery root. Accepts string shorthand
// ("layers" → {Path: "layers", Recursive: true}) or the explicit object form
// ({path: X, recursive: false}). Empty Path is invalid.
type ScanSpec struct {
	Path      string `yaml:"path"`
	Recursive bool   `yaml:"recursive"`
	// Manifest is the per-directory manifest filename to look for. Empty
	// defaults to UnifiedFileName; configurable per spec in charly.yml.
	Manifest string `yaml:"manifest,omitempty"`
}

// UnmarshalYAML accepts the string shorthand where Recursive defaults to true,
// and the object form where Recursive defaults to true when omitted.
func (s *ScanSpec) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		s.Path = node.Value
		s.Recursive = true
		s.Manifest = UnifiedFileName
		return nil
	}
	// Object form — decode with `recursive` defaulting to true when absent.
	// yaml.v3 has no direct "default true"; we interpret missing as true by
	// looking at the raw node and only clearing Recursive when the field is
	// explicitly set to false.
	var raw struct {
		Path      string `yaml:"path"`
		Recursive *bool  `yaml:"recursive"`
		Manifest  string `yaml:"manifest"`
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
	s.Manifest = raw.Manifest
	if s.Manifest == "" {
		s.Manifest = UnifiedFileName
	}
	return nil
}

// InlineLayer is a layer declared inline in the unified file's `layers:` map.
// Mutually exclusive options: `from:` points at a directory to scan via the
// existing scanLayer (no schema change), OR the inline body defines the layer
// (same fields as the candy manifest, flattened via yaml:",inline").
type InlineCandy struct {
	From      string `yaml:"from,omitempty"`
	CandyYAML `yaml:",inline"`
	// manifest carries the discovery manifest filename for a `From:` directory
	// so ProjectLayers→scanLayer reads the right file. Not serialized.
	manifest string
}

// UnmarshalYAML is required because LayerYAML has its own UnmarshalYAML —
// yaml.v3's default ",inline" handling doesn't compose with a custom
// unmarshaler on the embedded type. We read `from:` explicitly, then delegate
// to LayerYAML for the body.
func (il *InlineCandy) UnmarshalYAML(node *yaml.Node) error {
	var own struct {
		From string `yaml:"from"`
	}
	_ = node.Decode(&own)
	il.From = own.From
	if il.From != "" {
		// `from:` entries reference an external directory — no body decode.
		return nil
	}
	return il.CandyYAML.UnmarshalYAML(node)
}

// DeploymentsSection carries repo-shipped deployment defaults plus per-image
// deployment entries. Matches the two-tier deploy model: this block is the
// authored/in-repo defaults; ~/.config/charly/deploy.yml is the per-machine overlay.
// DeploymentsSection — RETIRED by the field-singular cutover (2026-05).
// UnifiedFile.Deploy is now a flat map; UnifiedFile.Provides moved to
// root level. The type definition is kept (not deleted) because
// migrate_unified.go still references it for legacy migration history.
type DeploymentsSection struct {
	Defaults *DeploymentNode           `yaml:"defaults,omitempty"`
	Provides *ProvidesConfig           `yaml:"provides,omitempty"`
	Image    map[string]DeploymentNode `yaml:"box,omitempty"`
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
	Layer   *CandyDoc   `yaml:"candy,omitempty"`
	Image   *BoxDoc     `yaml:"box,omitempty"`
	Deploy  *DeployDoc  `yaml:"deploy,omitempty"`
	Builder *BuilderDoc `yaml:"builder,omitempty"`
	Distro  *DistroDoc  `yaml:"distro,omitempty"`
	Init    *InitDoc    `yaml:"init,omitempty"`
	VM      *VmDoc      `yaml:"vm,omitempty"`
	// Schema v4 first-class target templates.
	Pod     *PodDoc     `yaml:"pod,omitempty"`
	K8s     *K8sDoc     `yaml:"k8s,omitempty"`
	Local   *LocalDoc   `yaml:"local,omitempty"`
	Android *AndroidDoc `yaml:"android,omitempty"`
	// 2026-04 harness cutover.
	AI     *AIDoc     `yaml:"ai,omitempty"`
	Recipe *RecipeDoc `yaml:"recipe,omitempty"`
	Score  *ScoreDoc  `yaml:"score,omitempty"`
	// 2026-05 Calamares cutover.
	Group  *GroupDoc  `yaml:"group,omitempty"`
	Target *TargetDoc `yaml:"target,omitempty"`
	Module *ModuleDoc `yaml:"module,omitempty"`
	// Exclusive host-resource definition (GPU auto-allocation vocabulary).
	Resource *ResourceDoc `yaml:"resource,omitempty"`
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

// AndroidDoc wraps a single AndroidSpec with an explicit Name — the
// kind:android standalone form (authored in android.yml or inline).
type AndroidDoc struct {
	Name        string `yaml:"name"`
	AndroidSpec `yaml:",inline"`
}

// LayerDoc wraps a LayerYAML body with an explicit Name — the standalone form
// authored as a standalone candy manifest post-migration.
type CandyDoc struct {
	Name      string `yaml:"name"`
	CandyYAML `yaml:",inline"`
}

// UnmarshalYAML — same rationale as InlineLayer.UnmarshalYAML. The custom
// unmarshaler on the embedded LayerYAML doesn't compose with ",inline", so we
// extract Name ourselves and delegate the body to LayerYAML.
func (ld *CandyDoc) UnmarshalYAML(node *yaml.Node) error {
	var own struct {
		Name string `yaml:"name"`
	}
	_ = node.Decode(&own)
	ld.Name = own.Name
	return ld.CandyYAML.UnmarshalYAML(node)
}

// ImageDoc wraps a single ImageConfig with an explicit Name — the standalone
// form authored as a standalone box doc post-migration.
type BoxDoc struct {
	Name      string `yaml:"name"`
	BoxConfig `yaml:",inline"`
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

// kindKeys is the schema's kind vocabulary — the top-level keys that mark a
// document as kind-keyed (vs root-shape). It exists ONLY for shape
// classification (kindKeysSet). Files are generic kind-containers routed by
// shape; there is no per-kind filename — discovery + every per-kind filename
// are configured in charly.yml, never baked into the code.
var kindKeys = []string{
	"candy", "box", "deploy", "builder", "distro", "init",
	"pod", "vm", "k8s", "local", "android",
	"ai", "recipe", "score",
	"group", "target", "module",
	"resource",
}

// -----------------------------------------------------------------------------
// Loader entry point.
// -----------------------------------------------------------------------------

// LoadUnified reads charly.yml at dir, resolves all `includes:` recursively,
// walks `discover:` roots, and returns the merged UnifiedFile plus a flag
// indicating whether charly.yml was present. When the file does not exist,
// (nil, false, nil) is returned so callers can fall through to legacy loaders.
//
// Enforces schema version 2: any loaded charly.yml whose `version:` is
// absent or less than 2 is hard-rejected with a migration hint. v1 configs
// used a separate vms.yml + plural `vms:` root key; `charly migrate`
// produces a v2 layout in one shot.
// rejectLegacyLocalSurface refuses to load any project that still
// carries kind:host, target:host, or DeploymentNode `host: <template>`
// references against templates that no longer exist (the new `host:`
// field is destination-only). All three are fixed by
// `charly migrate` in one pass.
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
					"%s (doc %d): root-key `deployment:` is retired (2026-05 kind-files cutover).\n  Renamed to `deploy:`. Run: charly migrate",
					path, docIdx)
			}
			// Case B: root-key `deployments:` (v3 legacy plural).
			if v := findMappingValue(root, "deployments"); v != nil {
				return fmt.Errorf(
					"%s (doc %d): root-key `deployments:` is retired (legacy v3 plural).\n  Run: charly migrate",
					path, docIdx)
			}
			// Case C: kind-keyed wrapper `kind: deployment` scalar.
			if v := findMappingValue(root, "kind"); v != nil && v.Kind == yaml.ScalarNode && v.Value == "deployment" {
				return fmt.Errorf(
					"%s (doc %d): `kind: deployment` is retired (2026-05 kind-files cutover).\n  Renamed to `kind: deploy`. Run: charly migrate",
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
				"%s: deployment %q uses legacy `target: host` — schema renamed to `target: local`. Run: charly migrate",
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
// `charly update` time.
func rejectLegacyMarimoMl(root string, merged *UnifiedFile) error {
	if merged == nil {
		return nil
	}
	if _, ok := merged.Image["marimo-ml"]; ok {
		return fmt.Errorf(
			"%s: image entry %q is retired (2026-04 marimo-rename cutover, 2026-05 versa-rename cutover).\n  Renamed to `versa` (cross-kind name reuse). Run: charly migrate",
			root, "marimo-ml")
	}
	var walk func(name string, node *DeploymentNode) error
	walk = func(name string, node *DeploymentNode) error {
		if node == nil {
			return nil
		}
		if name == "marimo-ml-pod" {
			return fmt.Errorf(
				"%s: deployment %q is retired (2026-04 marimo-rename cutover, 2026-05 versa-rename cutover).\n  Renamed to `versa` (cross-kind name reuse). Run: charly migrate",
				root, name)
		}
		if node.Image == "marimo-ml" {
			return fmt.Errorf(
				"%s: deployment %q references retired image %q (2026-04 marimo-rename cutover, 2026-05 versa-rename cutover).\n  Renamed to `versa`. Run: charly migrate",
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
	// top-level keys (images:/layers:/distros:/... ) in charly.yml.
	// `charly migrate` rewrites them in-place.
	if rootData, err := os.ReadFile(root); err == nil {
		if err := RejectLegacyPluralKeys(root, rootData); err != nil {
			return nil, true, err
		}
	}
	merged := &UnifiedFile{}
	visited := map[string]bool{}
	nsCache := map[string]*UnifiedFile{}
	// Register the local root under its repo identity so a transitive import of
	// THIS repo (at any pinned version) cycle-breaks to the working tree (root's
	// namespace pins win — see ns_identity.go). Seeded BEFORE the load and never
	// popped, so it matches anywhere in the import graph. "" (no `repo:`, no git
	// origin) → no registration → version-keyed behavior, as before.
	loadingRepos := map[string]*UnifiedFile{}
	if rootID := rootRepoIdentity(dir); rootID != "" {
		loadingRepos[rootID] = merged
	}
	if err := loadUnifiedInto(root, merged, visited, 0, nsCache, loadingRepos); err != nil {
		return nil, true, err
	}
	normalizeV4Aliases(merged)
	fileVer, verOK := ParseCalVer(merged.Version)
	switch {
	case verOK && LatestSchemaVersion().Less(fileVer):
		// The config was written for a NEWER schema than this charly binary
		// understands. `charly migrate` cannot help — migration only moves
		// configs forward to THIS binary's HEAD, and the file is already
		// past it. The binary itself is behind, so the only fix is to
		// update charly. Hard-fail with that advice instead of letting a
		// newer field shape silently mis-parse.
		return nil, true, fmt.Errorf(
			"%s: config schema %s is newer than this charly supports (max %s). Update charly (reinstall the latest opencharly package, or run 'task build:charly' from a fresh checkout)",
			root, merged.Version, LatestSchemaVersion(),
		)
	case !verOK || fileVer.Less(LatestSchemaVersion()):
		return nil, true, fmt.Errorf(
			"%s: schema %s is required (found %q). Run: charly migrate",
			root, LatestSchemaVersion(), merged.Version,
		)
	}
	// Reject any residual legacy local/host or status/info surface.
	// `charly migrate` fixes all of these in one shot.
	if err := rejectLegacyLocalSurface(root, merged); err != nil {
		return nil, true, err
	}
	if err := rejectLegacyMarimoMl(root, merged); err != nil {
		return nil, true, err
	}
	// Fold kind:eval beds into the Deploy map (EvalBed=true) so every
	// deploy verb resolves them by name through the same path as any
	// deploy. Disjoint-name guard inside. Runs BEFORE validateDeploymentTree
	// so folded beds get the same deploy validation (name shape, required
	// image: on pod targets).
	if err := foldEvalBeds(merged); err != nil {
		return nil, true, fmt.Errorf("%s: %w", root, err)
	}
	// Fold sibling peers (companion deployments) into the Deploy map as
	// addressable top-level entries (inheriting the owner's disposability) so
	// the SAME deploy verbs bring them up/down. Runs AFTER foldEvalBeds (so a
	// bed's peers fold too) and BEFORE validateDeploymentTree (so folded peers
	// get the same deploy validation).
	if err := foldPeers(merged); err != nil {
		return nil, true, fmt.Errorf("%s: %w", root, err)
	}
	if err := validateDeploymentTree(merged.Deploy); err != nil {
		return nil, true, fmt.Errorf("%s: %w", root, err)
	}
	if err := validateEvalBeds(merged); err != nil {
		return nil, true, fmt.Errorf("%s: %w", root, err)
	}
	if err := validatePeers(merged); err != nil {
		return nil, true, fmt.Errorf("%s: %w", root, err)
	}
	if err := validatePreemptibleUnified(merged); err != nil {
		return nil, true, fmt.Errorf("%s: %w", root, err)
	}
	// Hard load-time error for the retired `local.cachyos-dx` key.
	// Pairs with the deployment-side checks in validateDeploymentTree.
	// All three retired keys (deployment.qc, deployment.cachyos-dx,
	// local.cachyos-dx) point at the consolidated migration command.
	if _, present := merged.Local["cachyos-dx"]; present {
		return nil, true, fmt.Errorf(
			"%s: kind:local key \"cachyos-dx\" is retired (2026-05 init-system-polymorphism cutover).\n  Run: charly migrate",
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
// SSH path, not `podman exec charly-<pod>`).
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
//     for dotted-path CLI addressing like `charly deploy add a.b.c`).
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
	// The qc → cachyos-dx → charly-cachyos rename chain (2026-05) collapsed
	// in the second cutover to ONE canonical name `charly-cachyos` shared
	// by the kind:local template and the kind:deployment entry that
	// applies it. Any residual `qc:` or `cachyos-dx:` deployment key
	// (or `cachyos-dx:` kind:local key) needs a one-shot migration.
	if _, present := deploy["qc"]; present {
		return fmt.Errorf(
			"deployment key \"qc\" is retired (2026-05 cross-kind name reuse cutover).\n  Run: charly migrate",
		)
	}
	if _, present := deploy["cachyos-dx"]; present {
		return fmt.Errorf(
			"deployment key \"cachyos-dx\" is retired (2026-05 init-system-polymorphism cutover).\n  Run: charly migrate",
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
// Remediation: `charly migrate` (idempotent) walks every
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
				"deploy entry %q lacks required `box:` field (2026-05-12 schema cutover — pod-target deploys must declare `box:` explicitly so the eval runner reads the operator's declared intent, not the running container's stale label).\n  Remediation: run `charly migrate` (one-shot, idempotent).",
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
			"deployment key %q contains '.' — the character is reserved for dotted-path addressing (charly deploy add a.b.c). Rename this entry in deploy.yml",
			full,
		)
	}
	return nil
}

// loadUnifiedInto reads one file, merges every one of its documents into merged,
// then processes any `import:` it declared. Flat imports recurse into the SAME
// merged/visited (root namespace); namespaced imports mount an isolated child
// UnifiedFile under merged.Namespaces via the shared nsCache (cycle-broken).
// Cycle-safe within a namespace via the visited set; across namespaces via nsCache.
func loadUnifiedInto(path string, merged *UnifiedFile, visited map[string]bool, depth int, nsCache, loadingRepos map[string]*UnifiedFile) error {
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
	var importQueue ImportList
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
		// Surface key misalignments (silently-dropped unknown keys) as
		// non-fatal warnings before the lenient decode below.
		warnUnknownYAMLKeys(&node, shape, fmt.Sprintf("%s:doc%d", abs, docIdx))
		switch shape {
		case docShapeRoot:
			var uf UnifiedFile
			if err := node.Decode(&uf); err != nil {
				return fmt.Errorf("%s:doc%d: decoding root-shape document: %w", abs, docIdx, err)
			}
			// Queue imports for processing after current-file merging.
			importQueue = append(importQueue, uf.Import...)
			// Clear before merge so they don't leak into the merged struct.
			uf.Import = nil
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

	// Process imports relative to this file's directory.
	base := filepath.Dir(abs)
	for _, imp := range importQueue {
		if imp.Namespace == "" {
			// Flat import — merge UNDER the root file (root wins). We already
			// merged the root's fields above; the merge function preserves
			// existing (root) values. Shares merged + visited.
			_, incPath, err := canonicalRef(imp.Ref, base)
			if err != nil {
				return fmt.Errorf("%s: import %q: %w", abs, imp.Ref, err)
			}
			if err := loadUnifiedInto(incPath, merged, visited, depth+1, nsCache, loadingRepos); err != nil {
				return err
			}
			continue
		}
		// Namespaced import — mount an isolated child UnifiedFile.
		if err := validateNamespaceAlias(imp.Namespace); err != nil {
			return fmt.Errorf("%s: import %q: %w", abs, imp.Ref, err)
		}
		sub, err := loadNamespaceCached(imp.Ref, base, nsCache, loadingRepos)
		if err != nil {
			return fmt.Errorf("%s: import %s (%q): %w", abs, imp.Namespace, imp.Ref, err)
		}
		if merged.Namespaces == nil {
			merged.Namespaces = map[string]*UnifiedFile{}
		}
		if existing, ok := merged.Namespaces[imp.Namespace]; ok && existing != sub {
			return fmt.Errorf("%s: import namespace %q bound to two different refs", abs, imp.Namespace)
		}
		merged.Namespaces[imp.Namespace] = sub
	}
	// At a project boundary (depth 0 = the root file OR a namespace root) every
	// import is now merged, so run discovery here — the SINGLE site for ALL
	// consumers (box config, layers, deploy). discover: scans each spec and
	// registers discovered entities by SHAPE (candies AND boxes AND any other
	// kind). Historically only the layer-loading path called ApplyDiscover, so a
	// discovered `box:` dir never reached ProjectConfig/Config.Image; routing
	// it through the loader fixes box-via-discover uniformly.
	if depth == 0 {
		if err := merged.ApplyDiscover(base); err != nil {
			return fmt.Errorf("%s: %w", abs, err)
		}
		// Fill any distro/builder/init/resource vocabulary the project did NOT
		// declare from the binary-embedded default build.yml (project-wins; see
		// applyEmbeddedBuildDefaults). Runs for the root AND every namespace, so
		// a project needs no build.yml of its own.
		if err := applyEmbeddedBuildDefaults(merged); err != nil {
			return fmt.Errorf("%s: %w", abs, err)
		}
	}
	return nil
}

// canonicalRef resolves an import ref (local path or
// `@host/org/repo[/sub/path]:version`) to a concrete on-disk path AND a stable
// cache key. Remote refs are downloaded into the shared repo cache (and
// auto-migrated). The key dedups identical refs across the whole load so a
// diamond — or the intentional main<->cachyos cycle — of namespaced imports
// resolves exactly once.
func canonicalRef(ref, baseDir string) (key, path string, err error) {
	if strings.HasPrefix(ref, "@") {
		parsed := ParseRemoteRef(ref)
		version := parsed.Version
		if version == "" {
			branch, e := GitDefaultBranch(RepoGitURL(parsed.RepoPath))
			if e != nil {
				return "", "", fmt.Errorf("resolving default branch for %s: %w", parsed.RepoPath, e)
			}
			version = branch
		}
		cachePath, e := EnsureRepoDownloaded(parsed.RepoPath, version)
		if e != nil {
			return "", "", fmt.Errorf("downloading remote ref %q: %w", ref, e)
		}
		return parsed.RepoPath + "@" + version + "/" + parsed.SubPath,
			filepath.Join(cachePath, parsed.SubPath), nil
	}
	p := ref
	if !filepath.IsAbs(p) {
		p = filepath.Join(baseDir, ref)
	}
	abs, e := filepath.Abs(p)
	if e != nil {
		return "", "", fmt.Errorf("resolving %s: %w", ref, e)
	}
	return abs, abs, nil
}

// loadNamespaceCached loads a namespaced import target as a fully-resolved,
// isolated UnifiedFile — its OWN files (flat imports for vocabulary, its own
// entities) plus its OWN namespaced imports. A fresh `visited` set isolates its
// file-cycle detection; the shared nsCache breaks cross-namespace cycles
// (including the intentional main<->cachyos mutual import) by recording an
// in-progress node BEFORE recursing. A whole-repo ref (empty sub-path) resolves
// to its charly.yml.
func loadNamespaceCached(ref, baseDir string, nsCache, loadingRepos map[string]*UnifiedFile) (*UnifiedFile, error) {
	// Cycle-break by REPO IDENTITY (not pinned version), BEFORE any fetch: if
	// this ref targets a repo already being loaded up the stack (the root or an
	// ancestor namespace), resolve to that in-progress node. This terminates the
	// intentional mutual import (main <-> cachyos) even when the loop's pins
	// diverge — a transitive back-reference to an in-progress repo at a DIFFERENT
	// pinned version resolves to the in-progress node instead of fetching a
	// divergent (possibly stale-schema) snapshot. See ns_identity.go.
	repoID := nsRepoIdentity(ref, baseDir)
	if repoID != "" {
		if existing, ok := loadingRepos[repoID]; ok {
			return existing, nil
		}
	}
	key, path, err := canonicalRef(ref, baseDir)
	if err != nil {
		return nil, err
	}
	if existing, ok := nsCache[key]; ok {
		return existing, nil // version-keyed diamond memo (dedup identical refs)
	}
	if info, statErr := os.Stat(path); statErr == nil && info.IsDir() {
		path = filepath.Join(path, UnifiedFileName)
	}
	sub := &UnifiedFile{}
	nsCache[key] = sub // version-keyed memo entry (persists across the whole load)
	if repoID != "" {
		// Stack-scoped in-progress (ancestor) marker for the identity cycle-break
		// above: pushed before recursing, popped after, so two SIBLING imports of
		// the same repo at different versions still each load — only a genuine
		// back-edge (an ancestor still on the stack) short-circuits.
		loadingRepos[repoID] = sub
		defer delete(loadingRepos, repoID)
	}
	if err := loadUnifiedInto(path, sub, map[string]bool{}, 0, nsCache, loadingRepos); err != nil {
		return nil, err
	}
	return sub, nil
}

// validateNamespaceAlias enforces a bare lowercase-hyphenated alias (no dots).
func validateNamespaceAlias(alias string) error {
	if !namespaceAliasRe.MatchString(alias) {
		return fmt.Errorf("import namespace alias %q must match %s", alias, namespaceAliasRe.String())
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
	"version": true, "repo": true, "import": true, "discover": true, "defaults": true,
	"provides": true,
	// Field-singular cutover (2026-05): plurals collapsed.
	"distro": true, "builder": true, "init": true,
	"candy": true,
	"box":   true, "pod": true, "vm": true, "k8s": true, "local": true,
	"android": true,
	"deploy":  true,
	// 2026-04 harness cutover: `ai:` and `recipe:` are recognized as
	// root-shape collection-map keys (in addition to being valid
	// kind-keyed forms). Mirrors how image/pod/vm work.
	"ai": true, "recipe": true, "score": true,
	// kind:eval disposable R10 beds — root-shape collection map
	// (bed-name → DeploymentNode) authored in eval.yml. The nested `eval:`
	// PROBE-LIST field never appears as a top-level document key, so this
	// only ever matches the bed collection.
	"eval": true,
	// Calamares-aligned kinds.
	"group": true, "target": true, "module": true,
	// Exclusive host-resource vocabulary (token -> hardware selector) driving
	// GPU auto-allocation. Build-vocab VALUE map, like distro:; flat-imported
	// from build.yml into the root namespace.
	"resource": true,
}

// kindKeysSet mirrors kindKeys for O(1) lookup.
var kindKeysSet = func() map[string]bool {
	m := make(map[string]bool, len(kindKeys))
	for _, k := range kindKeys {
		m[k] = true
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
	hasLegacyIncludeKey := false
	for i := 0; i < len(inner.Content); i += 2 {
		k := inner.Content[i].Value
		keys = append(keys, k)
		if k == "benchmark" {
			hasLegacyBenchmarkKey = true
		}
		if k == "include" {
			hasLegacyIncludeKey = true
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
	// Legacy `benchmark:` root key — predates the 2026-04 harness→eval
	// cutover, whose forward-only migrator has since been removed. There is
	// no automated path in the current binary; the block must be rewritten
	// by hand as a `kind: score` + `kind: recipe` pair under `eval:`.
	if hasLegacyBenchmarkKey {
		return 0, fmt.Errorf(
			"the `benchmark:` root key is no longer accepted — it predates the 2026-04 harness→eval cutover, whose migrator has since been removed. Rewrite the block by hand as a `kind: score` + `kind: recipe` pair under `eval:` (see /charly-eval:eval)",
		)
	}
	// 2026-05 import-namespace cutover: `include:` was deleted in favor of
	// the single `import:` statement (flat + namespaced child imports).
	if hasLegacyIncludeKey {
		return 0, fmt.Errorf(
			"the `include:` key is no longer accepted — it was replaced by `import:` (flat string items + namespaced `alias: ref` items) in the 2026-05 import-namespace cutover. Run: charly migrate",
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
// `charly migrate`, two legacy shapes that the slim post-cutover
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
				"%s: recipe %q carries `max_iteration:` — the field was removed in the 2026-04 harness cutover. Loop bound is now plateau-only via score.plateau_iteration. Remove the field.",
				source, name,
			)
		}
		if !allowed[k] {
			return fmt.Errorf(
				"%s: recipe %q carries forbidden runner field %q. Recipes are pure spec (description + scenario only); runner fields (host/pod/vm, ai, plateau_iteration, prompt, deployment, target_image, mcp_endpoint, env, notes, recipes) live on a `kind: score` entry. Move them there.",
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
				"%s: score %q carries `max_iteration:` — the field was removed in the 2026-04 harness cutover. Loop bound is now plateau-only via score.plateau_iteration. Remove the field.",
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
	if src.Version != "" && dst.Version == "" {
		dst.Version = src.Version
	}
	// Root-wins: the root file (merged first) defines the project's repo
	// identity; a flat import declaring `repo:` never overrides it.
	if src.Repo != "" && dst.Repo == "" {
		dst.Repo = src.Repo
	}
	// Discover entries concatenate (not overwrite). Resolve relative
	// paths to absolute against srcDir so an included file's discover
	// roots remain anchored to the included file's directory rather
	// than to the eventual root file's directory. Without this, a
	// downstream workspace that `include:`-s an upstream charly.yml
	// would look for upstream's `candy/` inside the workspace tree.
	if len(src.Discover) > 0 {
		dst.Discover = append(dst.Discover, anchorScanSpecs(src.Discover, srcDir)...)
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
	mergeAndroidMap(&dst.Android, src.Android)
	mergeAIMap(&dst.AI, src.AI)
	mergeRecipeMap(&dst.Recipe, src.Recipe)
	mergeScoreMap(&dst.Score, src.Score)
	mergeGroupMap(&dst.Group, src.Group)
	mergeTargetMap(&dst.Target, src.Target)
	mergeModuleMap(&dst.Module, src.Module)
	mergeResourceMap(&dst.Resource, src.Resource)
	mergeDeployMaps(&dst.Deploy, src.Deploy)
	mergeDeployMaps(&dst.Eval, src.Eval)
	if dst.Provides == nil && src.Provides != nil {
		dst.Provides = src.Provides
	}
	// Defaults: dst wins per-field if set.
	mergeImageConfig(&dst.Defaults, &src.Defaults)
}

// anchorScanSpecs returns a copy of `specs` with every relative Path
// resolved to an absolute path against `srcDir`. Absolute paths are
// kept verbatim. Empty srcDir leaves specs unchanged so the
// root-file merge (called with rootDir == workspace) is a no-op.
func anchorScanSpecs(specs []ScanSpec, srcDir string) []ScanSpec {
	if srcDir == "" || len(specs) == 0 {
		return specs
	}
	out := make([]ScanSpec, len(specs))
	for i, s := range specs {
		out[i] = s
		if s.Path != "" && !filepath.IsAbs(s.Path) {
			out[i].Path = filepath.Join(srcDir, s.Path)
		}
	}
	return out
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

// mergeResourceMap merges exclusive host-resource definitions (kind:resource).
// Root-wins, like the other build-vocab maps.
func mergeResourceMap(dst *map[string]*ResourceDef, src map[string]*ResourceDef) {
	if len(src) == 0 {
		return
	}
	if *dst == nil {
		*dst = make(map[string]*ResourceDef)
	}
	for k, v := range src {
		if _, exists := (*dst)[k]; !exists {
			(*dst)[k] = v
		}
	}
}

func mergeImageMap(dst *map[string]BoxConfig, src map[string]BoxConfig) {
	if len(src) == 0 {
		return
	}
	if *dst == nil {
		*dst = make(map[string]BoxConfig)
	}
	for k, v := range src {
		if _, exists := (*dst)[k]; !exists {
			(*dst)[k] = v
		}
	}
}

func mergeLayerMap(dst *map[string]*InlineCandy, src map[string]*InlineCandy) {
	if len(src) == 0 {
		return
	}
	if *dst == nil {
		*dst = make(map[string]*InlineCandy)
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

func mergeAndroidMap(dst *map[string]*AndroidSpec, src map[string]*AndroidSpec) {
	if len(src) == 0 {
		return
	}
	if *dst == nil {
		*dst = make(map[string]*AndroidSpec)
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

// foldEvalBeds copies every kind:eval bed (uf.Eval) into the Deploy map
// with EvalBed=true so that every deploy verb (`charly deploy add`, `charly config`,
// `charly start`, `charly eval live`, `charly update`, `charly remove`) resolves a bed by
// name through the SAME Deploy-map path it already uses — no per-verb
// special case. uf.Eval is retained as the authoritative bed set for
// EvalBeds() enumeration. A name present as BOTH a kind:eval bed and a
// kind:deploy entry is a hard error (disjoint namespaces by construction).
func foldEvalBeds(uf *UnifiedFile) error {
	if len(uf.Eval) == 0 {
		return nil
	}
	if uf.Deploy == nil {
		uf.Deploy = make(map[string]DeploymentNode, len(uf.Eval))
	}
	for name, node := range uf.Eval {
		if _, clash := uf.Deploy[name]; clash {
			return fmt.Errorf(
				"name %q is declared as both a kind:eval bed and a kind:deploy entry — names must be unique across the two kinds; rename one",
				name,
			)
		}
		node.EvalBed = true
		uf.Deploy[name] = node
		uf.Eval[name] = node // keep the marker on the retained bed set too
	}
	return nil
}

// EvalBeds returns the kind:eval disposable R10 beds keyed by name. It is
// the single enumeration source for `charly eval run <bed>` / `--all-beds`;
// every other consumer reads the folded entries from the Deploy map.
func (uf *UnifiedFile) EvalBeds() map[string]DeploymentNode {
	if uf == nil {
		return nil
	}
	return uf.Eval
}

// validateEvalBeds enforces the kind:eval bed-specific invariants beyond the
// generic deploy validation (which already runs on the folded beds via
// validateDeploymentTree → validateDeployRequiresImage, covering the pod
// `image:` requirement). Runs at LOAD time so EVERY command that resolves a
// bed (charly eval run, charly deploy add, charly config, charly box validate, …) sees the
// same friendly error — not just `charly box validate`.
func validateEvalBeds(uf *UnifiedFile) error {
	for name, node := range uf.Eval {
		// Disposable is the sole authorization for the destroy+rebuild the
		// R10 sequence drives; a non-disposable bed can't be rebuilt
		// unattended (see /charly-internals:disposable).
		if !node.IsDisposable() {
			return fmt.Errorf(
				"kind:eval bed %q must set `disposable: true` — `charly eval run` destroys + rebuilds it unattended (R10 acceptance gate)",
				name)
		}
		switch node.Target {
		case "pod":
			// image: presence enforced by validateDeployRequiresImage on the
			// folded Deploy entry — no duplicate check here.
		case "vm":
			if node.Vm == "" {
				return fmt.Errorf("kind:eval bed %q (target: vm) must set `vm: <entity>`", name)
			}
			if _, ok := uf.VM[node.Vm]; !ok {
				return fmt.Errorf("kind:eval bed %q references vm entity %q which is not defined", name, node.Vm)
			}
		case "local":
			if node.Local == "" {
				return fmt.Errorf("kind:eval bed %q (target: local) must set `local: <template>`", name)
			}
			if _, ok := uf.Local[node.Local]; !ok {
				return fmt.Errorf("kind:eval bed %q references local template %q which is not defined", name, node.Local)
			}
		case "android":
			if node.Android == "" {
				return fmt.Errorf("kind:eval bed %q (target: android) must set `android: <device>`", name)
			}
			if _, ok := uf.Android[node.Android]; !ok {
				return fmt.Errorf("kind:eval bed %q references android device %q which is not defined", name, node.Android)
			}
		default:
			return fmt.Errorf("kind:eval bed %q has unsupported target %q (must be pod, vm, local, or android)", name, node.Target)
		}
	}
	return nil
}

// mergeImageConfig preserves dst's already-set fields and fills only the
// zero-valued ones from src. Used for merging Defaults blocks from includes.
func mergeImageConfig(dst, src *BoxConfig) {
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
	// Build-speed tunables (defaults: block) — carried through the same
	// per-field "dst wins if set" merge as the rest of ImageConfig.
	if dst.Jobs == nil {
		dst.Jobs = src.Jobs
	}
	if dst.PodmanJobs == nil {
		dst.PodmanJobs = src.PodmanJobs
	}
	if dst.PodmanJobsCap == nil {
		dst.PodmanJobsCap = src.PodmanJobsCap
	}
	if len(dst.ContextIgnore) == 0 {
		dst.ContextIgnore = src.ContextIgnore
	}
	if dst.Cache == "" {
		dst.Cache = src.Cache
	}
	if dst.KeepImages == nil {
		dst.KeepImages = src.KeepImages
	}
	if dst.KeepEvalRuns == nil {
		dst.KeepEvalRuns = src.KeepEvalRuns
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
	if kd.Android != nil {
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
			return fmt.Errorf("candy: missing name")
		}
		if merged.Layer == nil {
			merged.Layer = map[string]*InlineCandy{}
		}
		if _, exists := merged.Layer[kd.Layer.Name]; !exists {
			merged.Layer[kd.Layer.Name] = &InlineCandy{CandyYAML: kd.Layer.CandyYAML}
		}
	case kd.Image != nil:
		if kd.Image.Name == "" {
			return fmt.Errorf("box: missing name")
		}
		if merged.Image == nil {
			merged.Image = map[string]BoxConfig{}
		}
		if _, exists := merged.Image[kd.Image.Name]; !exists {
			merged.Image[kd.Image.Name] = kd.Image.BoxConfig
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
	case kd.Resource != nil:
		if kd.Resource.Name == "" {
			return fmt.Errorf("resource: missing name")
		}
		if merged.Resource == nil {
			merged.Resource = map[string]*ResourceDef{}
		}
		if _, exists := merged.Resource[kd.Resource.Name]; !exists {
			resDef := kd.Resource.ResourceDef
			merged.Resource[kd.Resource.Name] = &resDef
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
	case kd.Android != nil:
		if kd.Android.Name == "" {
			return fmt.Errorf("android: missing name")
		}
		if merged.Android == nil {
			merged.Android = map[string]*AndroidSpec{}
		}
		if _, exists := merged.Android[kd.Android.Name]; !exists {
			spec := kd.Android.AndroidSpec
			merged.Android[kd.Android.Name] = &spec
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

// ApplyDiscover walks every flat scan spec on uf.Discover and registers any
// entity found. Each spec scans its path for directories containing the spec's
// manifest; every discovered manifest is routed by SHAPE. Conflict rule:
// explicit map entries win over discovered entries. scanRoot resolution is
// relative to rootDir (the dir containing charly.yml).
func (uf *UnifiedFile) ApplyDiscover(rootDir string) error {
	for _, s := range uf.Discover {
		manifest := s.Manifest
		if manifest == "" {
			manifest = UnifiedFileName
		}
		scanPath := s.Path
		if !filepath.IsAbs(scanPath) {
			scanPath = filepath.Join(rootDir, scanPath)
		}
		dirs, err := findEntityDirs(scanPath, manifest, s.Recursive)
		if err != nil {
			return fmt.Errorf("discover %q: %w", s.Path, err)
		}
		for _, d := range dirs {
			if err := uf.applyDiscoveredManifest(d, manifest, rootDir); err != nil {
				return err
			}
		}
	}
	return nil
}

// findEntityDirs walks a scan root and returns every directory that contains
// the given canonical filename. When recursive is false, only the immediate
// children of path are considered.
func findEntityDirs(path, filename string, recursive bool) ([]string, error) {
	if !dirExists(path) {
		// A discover path that doesn't exist yields zero entities — NOT an
		// error. discover: is universally applied at load now (not just on the
		// layer path), and a project may legitimately declare a uniform
		// `discover: [box, candy]` while carrying only one of the directories
		// (e.g. a distro submodule with boxes but no candy/ of its own).
		return nil, nil
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

// applyDiscoveredManifest loads one discovered manifest and routes every
// document it contains by SHAPE — determined from the document's top-level
// kind-key WITHOUT parsing the body. A candy-shaped doc registers a lazy
// `From:` directory reference (scanLayer parses + validates the manifest and
// resolves the layer's assets relative to its dir); every other shape decodes
// and merges inline via mergeKindDoc. The conflict rule "explicit entry wins"
// applies to discovered candies.
func (uf *UnifiedFile) applyDiscoveredManifest(dir, manifest, rootDir string) error {
	target := filepath.Join(dir, manifest)
	data, err := os.ReadFile(target)
	if err != nil {
		return fmt.Errorf("reading %s: %w", target, err)
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	for {
		var node yaml.Node
		if err := decoder.Decode(&node); err != nil {
			if err.Error() == "EOF" {
				break
			}
			return fmt.Errorf("%s: %w", target, err)
		}
		switch firstKindKey(&node) {
		case "":
			continue // empty / non-kind document — nothing to register
		case "candy":
			// Candy: register a lazy directory reference (name = dir base, as
			// the legacy scanner did). scanLayer does the real parse later.
			name := filepath.Base(dir)
			if uf.Layer == nil {
				uf.Layer = map[string]*InlineCandy{}
			}
			if _, exists := uf.Layer[name]; exists {
				continue // explicit entry wins
			}
			rel, relErr := filepath.Rel(rootDir, dir)
			if relErr != nil {
				rel = dir
			}
			uf.Layer[name] = &InlineCandy{From: rel, manifest: manifest}
		default:
			// Any other kind: decode + merge inline, defaulting an empty entity
			// name to the discovered directory's base.
			var kd kindKeyedDoc
			if err := node.Decode(&kd); err != nil {
				return fmt.Errorf("%s: %w", target, err)
			}
			defaultKindDocName(&kd, filepath.Base(dir))
			if err := mergeKindDoc(uf, &kd, dir); err != nil {
				return fmt.Errorf("%s: %w", target, err)
			}
		}
	}
	return nil
}

// firstKindKey returns the first top-level kind-key present in a document
// (candy / box / deploy / …), or "" if none. It inspects keys only — the body
// is never parsed — so discovery routes by shape without validating content.
func firstKindKey(node *yaml.Node) string {
	inner := node
	if node.Kind == yaml.DocumentNode {
		if len(node.Content) == 0 {
			return ""
		}
		inner = node.Content[0]
	}
	if inner.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i+1 < len(inner.Content); i += 2 {
		if kindKeysSet[inner.Content[i].Value] {
			return inner.Content[i].Value
		}
	}
	return ""
}

// defaultKindDocName fills an empty Name on whichever kind a discovered doc
// carries, defaulting it to the discovered directory's base name (parity with
// the legacy per-kind discovery scanners).
func defaultKindDocName(kd *kindKeyedDoc, name string) {
	switch {
	case kd.Image != nil && kd.Image.Name == "":
		kd.Image.Name = name
	case kd.Deploy != nil && kd.Deploy.Name == "":
		kd.Deploy.Name = name
	case kd.Builder != nil && kd.Builder.Name == "":
		kd.Builder.Name = name
	case kd.Distro != nil && kd.Distro.Name == "":
		kd.Distro.Name = name
	case kd.Init != nil && kd.Init.Name == "":
		kd.Init.Name = name
	case kd.Pod != nil && kd.Pod.Name == "":
		kd.Pod.Name = name
	case kd.K8s != nil && kd.K8s.Name == "":
		kd.K8s.Name = name
	case kd.Local != nil && kd.Local.Name == "":
		kd.Local.Name = name
	}
}

// -----------------------------------------------------------------------------
// Projections — extract the existing concrete types from UnifiedFile so the
// existing loaders can become thin wrappers.
// -----------------------------------------------------------------------------

// ProjectConfig returns the *Config equivalent of uf (the box config view).
func (uf *UnifiedFile) ProjectConfig() *Config {
	return uf.projectConfigCached(map[*UnifiedFile]*Config{})
}

// projectConfigCached projects uf (and its import namespaces, recursively) into
// a *Config. The pointer-keyed cache breaks the intentional main<->cachyos
// import cycle (the shared UnifiedFile node is projected exactly once).
func (uf *UnifiedFile) projectConfigCached(cache map[*UnifiedFile]*Config) *Config {
	if c, ok := cache[uf]; ok {
		return c
	}
	images := uf.Image
	if images == nil {
		images = map[string]BoxConfig{}
	}
	c := &Config{
		Defaults: uf.Defaults,
		Image:    images,
		Local:    uf.Local,
	}
	cache[uf] = c // cache BEFORE recursing (cycle break)
	if len(uf.Namespaces) > 0 {
		c.Namespaces = make(map[string]*Config, len(uf.Namespaces))
		for ns, sub := range uf.Namespaces {
			c.Namespaces[ns] = sub.projectConfigCached(cache)
		}
	}
	return c
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
// of the authored file, independent of any per-machine ~/.config/charly/deploy.yml
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
			manifest := il.manifest
			if manifest == "" {
				manifest = UnifiedFileName
			}
			layer, err := scanLayer(p, name, manifest)
			if err != nil {
				return nil, fmt.Errorf("layer %q from %q: %w", name, il.From, err)
			}
			// Layers discovered via `include:` of a remote charly.yml
			// live OUTSIDE the workspace's project tree (typically in
			// the github cache under ~/.cache/charly/repos/). Mark them as
			// Remote so the generator's createRemoteLayerCopies stages
			// them into .build/_layers/ and the emitted Containerfile
			// COPY paths resolve correctly.
			if absRoot, err := filepath.Abs(rootDir); err == nil {
				if absLayer, err := filepath.Abs(p); err == nil {
					if rel, err := filepath.Rel(absRoot, absLayer); err == nil && strings.HasPrefix(rel, "..") {
						layer.Remote = true
					}
				}
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
// unified file. The effective Path is rootDir (the charly.yml's dir);
// SourceDir always equals Path (the `directory:` field was deleted in the
// 2026-05 Calamares cutover).
func synthesizeInlineLayer(name string, il *InlineCandy, rootDir string) (*Layer, error) {
	// Use inline layer body as if it were a parsed candy manifest at rootDir.
	layer := &Layer{
		Name: name,
		Path: rootDir,
	}
	layer.SourceDir = rootDir
	// Populate fields the same way scanLayer does post-parse. We reuse the
	// logic by duplicating the minimal set a test would notice; the full set
	// can be factored out alongside Part G's refactor.
	populateLayerFromYAML(layer, &il.CandyYAML)
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

// populateLayerFromYAML copies every field from a parsed LayerYAML into the
// runtime Layer. It is the SINGLE post-parse populator: BOTH scanLayer (the
// discovered-layer-dir path) and synthesizeInlineLayer (the charly.yml
// inline path) call it, so the two can never drift. (They previously did — the
// inline path silently dropped artifacts/capabilities/requiresCapabilities/
// shell and the unexported description.) The caller is responsible for the
// install-file filesystem probes (HasPixiToml etc.) against SourceDir.
func populateLayerFromYAML(layer *Layer, ly *CandyYAML) {
	layer.Version = ly.Version
	layer.Description = ly.Description
	layer.Status = descriptionStatus(ly.Description)
	layer.Info = descriptionInfo(ly.Description)

	layer.Require = toLayerRefs(ly.Require)
	layer.IncludedLayer = toLayerRefs(ly.Layer)

	layer.service = ly.Service
	// derivePackageSectionsFromCalamares is the SOLE populator of the package
	// surface (layer.tagSections + layer.topPackages, plus the arch `aur` format
	// section) from package: + distro:. There is no top-level format/tag-key
	// parse path anymore — the `distro:` map is the only package surface.
	if len(ly.Package) > 0 || len(ly.Distro) > 0 {
		derivePackageSectionsFromCalamares(layer, ly)
	}
	if len(ly.Port) > 0 {
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
	if len(ly.Env) > 0 || len(ly.PathAppend) > 0 {
		env := ly.Env
		if env == nil {
			env = make(map[string]string)
		}
		layer.envConfig = &EnvConfig{Vars: env, PathAppend: ly.PathAppend}
	}
	if ly.Route != nil {
		layer.route = &RouteConfig{Host: ly.Route.Host, Port: fmt.Sprintf("%d", ly.Route.Port)}
	}
	layer.volumes = ly.Volume
	layer.aliases = ly.Alias
	layer.extract = ly.Extract
	layer.data = ly.Data
	layer.security = ly.Security
	layer.libvirt = ly.Libvirt
	layer.hooks = ly.Hook
	layer.tests = ly.Eval
	layer.artifacts = ly.Artifact
	layer.capabilities = ly.Capability
	layer.requiresCapabilities = ly.RequiresCapability
	layer.PortRelayPorts = ly.PortRelay
	layer.secrets = ly.SecretYAML
	layer.envProvides = ly.EnvProvides
	layer.envRequires = ly.EnvRequire
	layer.envAccepts = ly.EnvAccept
	layer.secretAccepts = ly.SecretAccept
	layer.secretRequires = ly.SecretRequire
	layer.mcpProvides = ly.MCPProvide
	layer.mcpRequires = ly.MCPRequire
	layer.mcpAccepts = ly.MCPAccept
	layer.engine = ly.Engine
	layer.vars = ly.Vars
	layer.tasks = ly.Task
	layer.apk = ly.Apk
	layer.localpkg = ly.LocalPkg
	layer.reboot = ly.Reboot
	layer.shell = ly.Shell
}
