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
type UnifiedFile struct {
	Version     int                      `yaml:"version,omitempty"`
	Includes    []string                 `yaml:"includes,omitempty"`
	Discover    *DiscoverConfig          `yaml:"discover,omitempty"`
	Distros     map[string]*DistroDef    `yaml:"distros,omitempty"`
	Builders    map[string]*BuilderDef   `yaml:"builders,omitempty"`
	Inits       map[string]*InitDef      `yaml:"inits,omitempty"`
	Defaults    ImageConfig              `yaml:"defaults,omitempty"`
	Images      map[string]ImageConfig   `yaml:"images,omitempty"`
	Layers      map[string]*InlineLayer  `yaml:"layers,omitempty"`
	VMs         map[string]*VmSpec       `yaml:"vms,omitempty"`
	Deployments *DeploymentsSection      `yaml:"deployments,omitempty"`
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
	VMs         []ScanSpec `yaml:"vms,omitempty"`
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
	Defaults *DeployImageConfig           `yaml:"defaults,omitempty"`
	Provides *ProvidesConfig              `yaml:"provides,omitempty"`
	Images   map[string]DeployImageConfig `yaml:"images,omitempty"`
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

// DeploymentDoc wraps a single DeployImageConfig.
type DeploymentDoc struct {
	Name              string `yaml:"name"`
	DeployImageConfig `yaml:",inline"`
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
	Name    string  `yaml:"name"`
	Version string  `yaml:"version,omitempty"`
	Status  string  `yaml:"status,omitempty"`
	Info    string  `yaml:"info,omitempty"`
	Spec    VmSpec  `yaml:"spec"`
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
	{Key: "vm", Filename: "vm.yml"},
}

// -----------------------------------------------------------------------------
// Loader entry point.
// -----------------------------------------------------------------------------

// LoadUnified reads overthink.yml at dir, resolves all `includes:` recursively,
// walks `discover:` roots, and returns the merged UnifiedFile plus a flag
// indicating whether overthink.yml was present. When the file does not exist,
// (nil, false, nil) is returned so callers can fall through to legacy loaders.
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
	return merged, true, nil
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
var rootShapeKeys = map[string]bool{
	"version": true, "includes": true, "discover": true, "defaults": true,
	"distros": true, "builders": true, "inits": true,
	"images": true, "layers": true, "deployments": true,
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
	for i := 0; i < len(inner.Content); i += 2 {
		k := inner.Content[i].Value
		keys = append(keys, k)
		if rootShapeKeys[k] {
			hasRoot = true
		}
		if kindKeysSet[k] {
			hasKind = true
		}
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
// Merge helpers.
// -----------------------------------------------------------------------------

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
		dst.Discover.VMs = append(dst.Discover.VMs, src.Discover.VMs...)
		dst.Discover.Clusters = append(dst.Discover.Clusters, src.Discover.Clusters...)
	}
	mergeDistroMap(&dst.Distros, src.Distros)
	mergeBuilderMap(&dst.Builders, src.Builders)
	mergeInitMap(&dst.Inits, src.Inits)
	mergeImageMap(&dst.Images, src.Images)
	mergeLayerMap(&dst.Layers, src.Layers)
	mergeVmMap(&dst.VMs, src.VMs)
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
			d.Images = make(map[string]DeployImageConfig)
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
			merged.Deployments.Images = map[string]DeployImageConfig{}
		}
		if _, exists := merged.Deployments.Images[kd.Deployment.Name]; !exists {
			merged.Deployments.Images[kd.Deployment.Name] = kd.Deployment.DeployImageConfig
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
		if merged.VMs == nil {
			merged.VMs = map[string]*VmSpec{}
		}
		if _, exists := merged.VMs[kd.VM.Name]; !exists {
			spec := kd.VM.Spec
			merged.VMs[kd.VM.Name] = &spec
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
		Provides: uf.Deployments.Provides,
		Images:   uf.Deployments.Images,
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
