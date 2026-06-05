package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ParsedRef represents a parsed remote reference with version.
// Works for both layer refs and image refs.
// Format: @host/org/repo/sub/path:version
type ParsedRef struct {
	Raw      string // original string, e.g. "@github.com/org/repo/layers/name:v1.0.0"
	RepoPath string // e.g. "github.com/org/repo"
	SubPath  string // e.g. "layers/name" (path within repo)
	Name     string // e.g. "name" (last segment)
	Version  string // e.g. "v1.0.0"
}

// StripVersion removes the :version suffix from a remote ref.
// For non-remote refs (no @ prefix), returns (ref, "").
// e.g. "@github.com/org/repo/name:v1.0.0" -> ("@github.com/org/repo/name", "v1.0.0")
func StripVersion(ref string) (string, string) {
	if !strings.HasPrefix(ref, "@") {
		return ref, ""
	}
	if idx := strings.LastIndex(ref, ":"); idx != -1 {
		return ref[:idx], ref[idx+1:]
	}
	return ref, ""
}

// IsRemoteLayerRef returns true if a layer reference is a remote ref (starts with @)
func IsRemoteLayerRef(ref string) bool {
	return strings.HasPrefix(ref, "@")
}

// IsRemoteImageRef returns true if a ref looks like a remote image reference (starts with @)
func IsRemoteImageRef(ref string) bool {
	return strings.HasPrefix(ref, "@")
}

// ParseRemoteRef parses a remote reference into repo path, sub-path, name, and version.
// e.g. "@github.com/org/repo/layers/name:v1.0.0" -> ParsedRef{RepoPath: "github.com/org/repo", SubPath: "layers/name", Name: "name", Version: "v1.0.0"}
func ParseRemoteRef(ref string) *ParsedRef {
	raw := ref

	// Strip @ prefix
	ref = strings.TrimPrefix(ref, "@")

	// Split version
	version := ""
	if idx := strings.LastIndex(ref, ":"); idx != -1 {
		version = ref[idx+1:]
		ref = ref[:idx]
	}

	// Split into repo path (first 3 segments) and sub-path (rest)
	repoPath, subPath, name := splitRepoAndSubPath(ref)

	return &ParsedRef{
		Raw:      raw,
		RepoPath: repoPath,
		SubPath:  subPath,
		Name:     name,
		Version:  version,
	}
}

// splitRepoAndSubPath splits a ref into repo path (host/org/repo), sub-path, and name.
// e.g. "github.com/org/repo/layers/name" -> ("github.com/org/repo", "layers/name", "name")
// For short refs like "pixi", returns ("", "", "pixi").
func splitRepoAndSubPath(ref string) (repoPath, subPath, name string) {
	parts := strings.SplitN(ref, "/", 4) // [host, org, repo, sub/path]
	if len(parts) < 4 {
		// Not enough segments for a remote ref — treat as local name
		name = parts[len(parts)-1]
		if len(parts) <= 1 {
			return "", "", name
		}
		return strings.Join(parts, "/"), "", name
	}
	repoPath = strings.Join(parts[:3], "/")
	subPath = parts[3]
	if idx := strings.LastIndex(subPath, "/"); idx != -1 {
		name = subPath[idx+1:]
	} else {
		name = subPath
	}
	return repoPath, subPath, name
}

// BareRef returns the layer map key for a remote ref (without @ prefix and without :version).
// e.g. "@github.com/org/repo/name:v1.0.0" -> "github.com/org/repo/name"
func BareRef(ref string) string {
	bare, _ := StripVersion(ref)
	return strings.TrimPrefix(bare, "@")
}

// LayerRef is a single layer reference as authored in the candy manifest `require:` /
// `layer:` (or overthink.yml `layer:`). It carries the ORIGINAL ref string — with
// any `@repo` prefix and `:version` suffix — as the single source of truth; the
// bare map-key form (.Bare()) and the pinned version (.Version()) are DERIVED on
// demand, so a ref's identity and its version can never drift apart. The
// transitive fetch keys on .Raw; the dependency graph keys on .Bare().
type CandyRef struct {
	Raw string // original ref, e.g. "@github.com/org/repo/layers/x:v1" or bare "x"
	// resolved is the qualified layer-map key assigned when a freshly-fetched
	// remote layer's plain-name sibling deps are qualified to
	// "<repo>/<subpathprefix><name>" (qualifyRemoteSiblingDeps). Empty for every
	// other ref, where the map key derives from Raw. Keeping Raw immutable while
	// resolution lands in a separate slot lets ONE list serve both the graph
	// (which keys on .Bare()) and the transitive fetch (which keys on .Raw).
	resolved string
}

// Bare returns the layer-map key (no @ prefix, no :version) — the form used for
// dependency resolution and graph keying. After remote sibling-qualification it
// is the qualified key; otherwise it derives from the original ref.
func (r CandyRef) Bare() string {
	if r.resolved != "" {
		return r.resolved
	}
	return BareRef(r.Raw)
}

// Version returns the pinned version (the ":vX" suffix), or "" for an unpinned
// remote ref or a local (bare-name) ref.
func (r CandyRef) Version() string { _, v := StripVersion(r.Raw); return v }

// IsRemote reports whether this is an @-prefixed remote ref.
func (r CandyRef) IsRemote() bool { return IsRemoteLayerRef(r.Raw) }

// toLayerRefs wraps raw ref strings (as parsed from the candy manifest) into LayerRef
// values. Returns nil for a nil/empty input so an absent list stays absent.
func toLayerRefs(raw []string) []CandyRef {
	if len(raw) == 0 {
		return nil
	}
	out := make([]CandyRef, len(raw))
	for i, s := range raw {
		out[i] = CandyRef{Raw: s}
	}
	return out
}

// bareRefs returns the bare map-key form of each ref — for the consumers that
// resolve a layer list against the layer map.
func bareRefs(refs []CandyRef) []string {
	if len(refs) == 0 {
		return nil
	}
	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = r.Bare()
	}
	return out
}

// Layer-version resolution is per-entity, not per-git-tag: the `@github…:vTAG`
// suffix is ONLY the FETCH coordinate (which commit to clone). The authority is
// the layer's own `version:` field, read AFTER fetch and arbitrated by
// pickLayerVersion in ScanAllLayerWithConfigOpts (layers.go). So a repo re-tag
// that doesn't change a layer emits no warning. CollectRemoteRefsOpts below
// therefore collects EVERY distinct (repo, git-tag) a ref is referenced at;
// the per-entity dedup + warn happens once, after fetch.

// RepoCacheDir returns the cache directory for remote repos.
// Uses $OV_REPO_CACHE env var if set, otherwise ~/.cache/ov/repos/
func RepoCacheDir() (string, error) {
	if envDir := os.Getenv("OV_REPO_CACHE"); envDir != "" {
		return envDir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("getting home directory: %w", err)
	}
	return filepath.Join(home, ".cache", "ov", "repos"), nil
}

// RepoCachePath returns the cache path for a specific repo version.
// e.g. ~/.cache/ov/repos/github.com/org/repo@v1.0.0/
func RepoCachePath(repoPath, version string) (string, error) {
	cacheDir, err := RepoCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cacheDir, repoPath+"@"+version), nil
}

// IsRepoCached checks if a repo version is already in the cache
func IsRepoCached(repoPath, version string) (bool, error) {
	cachePath, err := RepoCachePath(repoPath, version)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(cachePath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// EnsureRepoDownloaded downloads the repo if not already cached.
// Returns the cache path. Newly-downloaded caches are auto-migrated via
// `MigrateUnified --rewrite-layers` so any legacy-form layer.yml files in
// the remote are normalized to the canonical `layer: {name, ...}` form
// before the runtime parses them. Already-cached repos are NOT re-migrated
// (they were migrated on first download).
func EnsureRepoDownloaded(repoPath, version string) (string, error) {
	cached, err := IsRepoCached(repoPath, version)
	if err != nil {
		return "", err
	}
	if cached {
		path, err := RepoCachePath(repoPath, version)
		if err != nil {
			return "", err
		}
		return path, nil
	}
	path, err := DownloadRepo(repoPath, version)
	if err != nil {
		return "", err
	}
	// One-shot migration of the freshly-cloned cache to the latest schema
	// CalVer. Runs the project-only subset of the chain (RunProjectMigrations)
	// so a remote fetch never mutates the user's per-host state; HostDeployPath
	// is left empty so even the calver-schema stamp touches only the cache's
	// project files. Idempotent: an already-current remote is a no-op.
	ctx := &MigrateContext{Dir: path, Out: io.Discard}
	if _, err := RunProjectMigrations(ctx); err != nil {
		return path, fmt.Errorf("auto-migrating remote cache %s: %w", path, err)
	}
	return path, nil
}

// RemoteDownload represents a unique (repo, version) pair to download,
// along with the specific bare refs needed from it.
type RemoteDownload struct {
	RepoPath string
	Version  string
	Refs     []string // bare refs to import (e.g. "github.com/org/repo/layers/name")
}

// CollectRemoteRefs is the default-opts wrapper (enabled images only) around
// CollectRemoteRefsOpts. The overwhelming majority of call sites want
// enabled-only collection, so they keep this two-arg form.
func CollectRemoteRefs(cfg *Config, layers map[string]*Layer) ([]RemoteDownload, error) {
	return CollectRemoteRefsOpts(cfg, layers, ResolveOpts{})
}

// CollectRemoteRefsOpts collects all unique remote refs from overthink.yml layer
// lists and candy manifest depends/layers fields. Different layers from the same repo
// can use different versions. Only the same bare ref at conflicting versions is
// an error. Returns a list of RemoteDownload grouped by (repoPath, version).
//
// opts gates the disabled-image walk: a disabled image's layer refs are
// collected when opts.shouldIncludeDisabled(name) is true (i.e. a
// `--include-disabled <name>` build). This keeps the remote-ref FETCH set in
// lockstep with the RESOLVE set walked by ResolveAllImage / GlobalLayerOrder —
// the same shouldIncludeDisabled predicate gates both. Without it, a disabled
// named image lands in the build working set but its remote layers are never
// fetched/registered, surfacing as "unknown layer" while computing global layer
// order.
func CollectRemoteRefsOpts(cfg *Config, layers map[string]*Layer, opts ResolveOpts) ([]RemoteDownload, error) {
	// Collect EVERY distinct (repo, git-tag) a ref is referenced at. The git tag
	// is only the FETCH coordinate — per-entity-version arbitration (and any
	// warning) happens AFTER fetch in ScanAllLayerWithConfigOpts, so a re-tag of
	// an unchanged layer no longer warns here. `source` is unused now (kept for
	// call-site stability + future diagnostics).
	type repoVer struct{ repo, ver string }
	pairs := make(map[repoVer]map[string]bool) // (repo, git-tag) -> set of bare refs
	// Track resolved default branches per repo (to avoid duplicate git queries)
	defaultBranches := make(map[string]string)

	addRef := func(ref, source string) error {
		_ = source
		if !IsRemoteLayerRef(ref) {
			return nil
		}
		parsed := ParseRemoteRef(ref)
		bareRef := BareRef(ref)
		version := parsed.Version
		if version == "" {
			// No version specified -- resolve to default branch
			if branch, ok := defaultBranches[parsed.RepoPath]; ok {
				version = branch
			} else {
				repoURL := RepoGitURL(parsed.RepoPath)
				branch, err := GitDefaultBranch(repoURL)
				if err != nil {
					return fmt.Errorf("%s: cannot resolve default branch for %s: %w", source, parsed.RepoPath, err)
				}
				version = branch
				defaultBranches[parsed.RepoPath] = branch
				fmt.Fprintf(os.Stderr, "Resolved @%s -> %s (default branch)\n", parsed.RepoPath, version)
			}
		}
		key := repoVer{parsed.RepoPath, version}
		if pairs[key] == nil {
			pairs[key] = make(map[string]bool)
		}
		pairs[key][bareRef] = true
		return nil
	}

	// format_config: has been removed. Remote build-config refs now live in
	// overthink.yml's `includes:` mechanism (see unified.go).

	// Collect layer refs from the ROOT project's own build/deploy targets (every
	// enabled image + every kind:local template), then follow base/builder edges
	// into imported namespaces, collecting ONLY the namespaced images actually
	// reachable as a base or builder. A namespace is imported to provide
	// bases/builders; its UNREFERENCED images and its kind:local templates (which
	// can never be a base/builder of the importing project) are not build inputs
	// here and must not be collected. Over-collecting them pulled unrelated
	// layers pinned at a different ecosystem tag, which the one-layer-one-version
	// invariant (tracker) then correctly — but spuriously — rejected. The
	// per-(Config,name) `collected` set also breaks the main<->cachyos cycle.
	collected := map[*Config]map[string]bool{}
	var collectImage func(c *Config, name string) error
	collectImage = func(c *Config, name string) error {
		seen := collected[c]
		if seen == nil {
			seen = map[string]bool{}
			collected[c] = seen
		}
		if seen[name] {
			return nil
		}
		seen[name] = true
		img, ok := c.Image[name]
		if !ok {
			return nil // external OCI base or unknown name — no layers to collect
		}
		for _, layerRef := range img.Layer {
			if err := addRef(layerRef, fmt.Sprintf("image %s", name)); err != nil {
				return err
			}
		}
		// Follow the base edge, plus builder edges when this image actually builds
		// (a layerless base needs no builder). A namespaced builder (e.g.
		// ov.fedora-builder) is BUILT as an intermediate in the consumer's graph,
		// so its layers (rpmfusion, yay, …) must be fetched here — dropping the
		// builder edge under-collects them ("unknown layer"). The builder edge
		// follows the EFFECTIVE builder (effectiveBuilderForImage → the canonical
		// resolveEffectiveBuilder), NOT the raw per-image img.Builder: an image
		// whose builder comes from defaults.builder / the distro-keyed default
		// (e.g. bazzite/aurora -> ov.fedora-builder, with no per-image builder:
		// block) has an EMPTY raw img.Builder, so reading it skipped the builder
		// edge and under-collected its layers — the exact fetch/resolve lockstep
		// break this walk exists to prevent. Qualified refs descend into the
		// imported namespace; bare refs resolve within c; an external-URL/unknown
		// base resolves to ok=false and is skipped.
		edges := []string{}
		if img.Base != "" {
			edges = append(edges, img.Base)
		}
		if len(img.Layer) > 0 {
			edges = append(edges, c.effectiveBuilderForImage(name, img).AllBuilder()...)
		}
		for _, ref := range edges {
			if _, tc, ok := c.resolveImageRef(ref); ok {
				if err := collectImage(tc, leafName(ref)); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if cfg != nil {
		for imgName, img := range cfg.Image {
			if !img.IsEnabled() && !opts.shouldIncludeDisabled(imgName) {
				continue
			}
			if err := collectImage(cfg, imgName); err != nil {
				return nil, err
			}
		}
		for tplName, spec := range cfg.Local {
			if spec == nil {
				continue
			}
			for _, layerRef := range spec.Layer {
				if err := addRef(layerRef, fmt.Sprintf("kind:local %s", tplName)); err != nil {
					return nil, err
				}
			}
		}
	}

	// Scan the candy manifest require: and layer: fields
	for layerName, layer := range layers {
		for _, dep := range layer.Require {
			if err := addRef(dep.Raw, fmt.Sprintf("layer %s require", layerName)); err != nil {
				return nil, err
			}
		}
		for _, ref := range layer.IncludedLayer {
			if err := addRef(ref.Raw, fmt.Sprintf("layer %s layer", layerName)); err != nil {
				return nil, err
			}
		}
	}

	// Emit one RemoteDownload per distinct (repo, git-tag). A bare ref pinned at
	// two git tags yields two downloads (both fetched); the post-fetch
	// arbitration keeps one materialization per bare ref.
	var result []RemoteDownload
	for key, refs := range pairs {
		refList := make([]string, 0, len(refs))
		for ref := range refs {
			refList = append(refList, ref)
		}
		result = append(result, RemoteDownload{
			RepoPath: key.repo,
			Version:  key.ver,
			Refs:     refList,
		})
	}
	return result, nil
}
