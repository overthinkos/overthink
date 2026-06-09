package main

// deploy_ref.go — unified image/layer reference resolver for
// `charly deploy add <name> <ref>`, `--add-layer <ref>`, and deploy.yml
// image:/add_layers: fields.
//
// <ref> accepts four forms, auto-detected:
//
//   1. Local image name         "fedora-coder"
//      Matches a top-level entry in charly.yml.
//
//   2. Local layer name         "pre-commit"
//      Matches a directory in candy/.
//
//   3. Local YAML path          "./my-box.yml" | "/abs/path/candy.yml"
//      Starts with "./" or "/"; ends with ".yml" or ".yaml". The file's
//      top-level keys tell us whether it's an image or layer declaration.
//
//   4. Remote repo ref          "github.com/owner/repo[/box/<n>|/candy/<n>][@ref]"
//      Matches "{host}/{org}/{repo}[/sub][@ref]" with a known host. The
//      existing refs.go `@`-prefixed form is also accepted for backward
//      compat with charly.yml import:/candy: already in the tree.
//
// Disambiguation rules (post 2026-06 candy/box rebrand):
//   - Any ref with a "candy/<n>" subpath resolves to a layer ("candy/" legacy).
//   - Any ref with a "box/<n>" subpath resolves to an image ("images/" legacy).
//   - A local name found in BOTH charly.yml and candy/ is permitted —
//     each kind has its own namespace. Precedence is decided by the
//     CALLER's context: ResolveDeployRef defaults to image-first
//     (the primary `<ref>` positional almost always means "deploy
//     this image"); ResolveDeployRefAsLayer prefers layers (used by
//     `--add-layer <ref>` where the user explicitly asked for a layer).
//   - A YAML file with a top-level `base:` or `images:` key is an image;
//     one with `rpm:`/`deb:`/`pac:`/`aur:`/`tasks:`/`services:` is a layer.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// RefKind classifies a DeployRef.
type RefKind string

const (
	RefKindImage RefKind = "image"
	RefKindLayer RefKind = "layer"
)

// RefSource classifies where the ref's content lives.
type RefSource string

const (
	RefSourceLocalName RefSource = "local-name"
	RefSourceLocalPath RefSource = "local-path"
	RefSourceRemote    RefSource = "remote"
)

// DeployRef is a parsed `<image-or-layer-ref>` ready to be loaded.
type DeployRef struct {
	Raw    string     // original input
	Kind   RefKind    // image or layer
	Source RefSource  // local-name | local-path | remote
	Name   string     // resolved short name (ripgrep, fedora-coder, etc.)
	Path   string     // absolute path to the relevant YAML; populated for local-name + local-path
	Remote *ParsedRef // populated for remote refs
}

// ResolveDeployRef parses ref using projectDir as the current project
// root for local-name resolution. Returns a DeployRef ready for
// downstream loading (LoadLayerFromFile / LoadImageFromFile / remote
// download). The function does not fetch remote repos — that happens
// at Emit time when the plan actually needs the content.
//
// When the same name exists as BOTH an image and a layer (cross-kind
// name reuse — permitted since 2026-05), this entry point prefers
// image. For the `--add-layer` context where the user asked for a
// layer specifically, use ResolveDeployRefAsLayer instead.
func ResolveDeployRef(ref, projectDir string) (*DeployRef, error) {
	return resolveDeployRefWithPref(ref, projectDir, RefKindImage)
}

// ResolveDeployRefAsLayer is the layer-preferring sibling of
// ResolveDeployRef. Used for `--add-layer <ref>` resolution where the
// user has explicitly asked for a layer overlay; if the same name
// exists as both an image and a layer, layer wins.
func ResolveDeployRefAsLayer(ref, projectDir string) (*DeployRef, error) {
	return resolveDeployRefWithPref(ref, projectDir, RefKindLayer)
}

// resolveDeployRefWithPref is the shared implementation; preferKind
// only affects the local-name codepath when a name resolves to both
// an image and a layer. Remote refs and explicit local paths classify
// themselves unambiguously.
func resolveDeployRefWithPref(ref, projectDir string, preferKind RefKind) (*DeployRef, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("ResolveDeployRef: empty ref")
	}

	// Form 4a (legacy): @host/org/repo/path:version — existing refs.go syntax.
	if strings.HasPrefix(ref, "@") {
		return resolveRemoteRef(ref)
	}

	// Form 4b: host/org/repo/path[@ref] — new syntax, no leading @.
	if looksLikeRemoteRef(ref) {
		// Normalize to the @-prefixed form so ParseRemoteRef handles it.
		normalized := "@" + translateAtVersion(ref)
		return resolveRemoteRef(normalized)
	}

	// Form 3: local YAML path.
	if strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "/") || strings.HasPrefix(ref, "../") ||
		strings.HasSuffix(ref, ".yml") || strings.HasSuffix(ref, ".yaml") {
		return resolveLocalPath(ref, projectDir)
	}

	// Forms 1 + 2: local name. Check charly.yml AND candy/; cross-kind
	// reuse permitted, preferKind decides which wins.
	return resolveLocalName(ref, projectDir, preferKind)
}

// looksLikeRemoteRef returns true if ref resembles a remote repo ref:
// "{host}/{org}/{repo}..." with a recognized host. We match a known set
// of hosts conservatively — adding new hosts is a one-line change.
var knownRemoteHosts = regexp.MustCompile(
	`^(github\.com|gitlab\.com|codeberg\.org|bitbucket\.org)(/|$)`,
)

func looksLikeRemoteRef(ref string) bool {
	return knownRemoteHosts.MatchString(ref)
}

// translateAtVersion rewrites "path@ref" into "path:ref" so ParseRemoteRef
// (which uses the colon delimiter) recognizes version pins in the new
// syntax. If ref has no @, returns it unchanged.
func translateAtVersion(ref string) string {
	idx := strings.LastIndex(ref, "@")
	if idx < 0 {
		return ref
	}
	// Preserve chars up to idx; replace @ with : after.
	return ref[:idx] + ":" + ref[idx+1:]
}

// refSubPathHas reports whether a remote-ref subpath contains a path segment
// either as the leading component ("candy/charly") or an interior one ("x/candy/charly").
func refSubPathHas(subPath, segment string) bool {
	return strings.Contains(subPath, "/"+segment+"/") || strings.HasPrefix(subPath, segment+"/")
}

// resolveRemoteRef parses and classifies an @-prefixed remote ref.
func resolveRemoteRef(ref string) (*DeployRef, error) {
	parsed := ParseRemoteRef(ref)
	kind := RefKindLayer
	switch {
	case refSubPathHas(parsed.SubPath, "candy") || refSubPathHas(parsed.SubPath, "layers"):
		// `candy/<n>` is the post-rebrand layer subpath; `candy/<n>` is the
		// legacy form kept for back-compat with old pins.
		kind = RefKindLayer
	case refSubPathHas(parsed.SubPath, "box") || refSubPathHas(parsed.SubPath, "images"):
		// `box/<n>` is the post-rebrand image subpath; `images/<n>` is legacy.
		kind = RefKindImage
	default:
		// A bare repo ref (no candy//box/ subpath) defaults to the project's
		// charly.yml, which is image-shaped. Existing tooling treats such
		// refs as project imports; we follow suit.
		kind = RefKindImage
	}
	return &DeployRef{
		Raw:    ref,
		Kind:   kind,
		Source: RefSourceRemote,
		Name:   parsed.Name,
		Remote: parsed,
	}, nil
}

// resolveLocalPath handles `./path.yml`, `/abs/path.yaml`, etc. Reads
// the file's top-level keys to classify as image vs layer.
func resolveLocalPath(ref, projectDir string) (*DeployRef, error) {
	path := ref
	if !filepath.IsAbs(path) {
		path = filepath.Join(projectDir, ref)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("ResolveDeployRef: cannot stat %s: %w", path, err)
	}
	if info.IsDir() {
		// A directory ref points at a layer directory (candy/<name>/).
		path = filepath.Join(path, "candy.yml")
		if _, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("ResolveDeployRef: directory %s has no candy.yml", ref)
		}
	}
	kind, err := classifyYAMLFile(path)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if kind == RefKindLayer && filepath.Base(path) == "candy.yml" {
		name = filepath.Base(filepath.Dir(path))
	}
	return &DeployRef{
		Raw:    ref,
		Kind:   kind,
		Source: RefSourceLocalPath,
		Name:   name,
		Path:   path,
	}, nil
}

// classifyYAMLFile reads the file's top-level keys and decides whether
// it declares an image or a layer.
func classifyYAMLFile(path string) (RefKind, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	var top map[string]interface{}
	if err := yaml.Unmarshal(data, &top); err != nil {
		return "", fmt.Errorf("parsing %s: %w", path, err)
	}
	// Image-shaped: has `images:` (top-level), `base:`, or `defaults:` block.
	for _, k := range []string{"box", "base", "defaults"} {
		if _, ok := top[k]; ok {
			return RefKindImage, nil
		}
	}
	// Layer-shaped: has any layer marker. This list roughly matches
	// the candy manifest's documented top-level fields; a YAML that has none of
	// these keys is an error (we don't try to guess).
	for _, k := range []string{"rpm", "deb", "pac", "aur", "tasks", "services", "service", "system_services", "candy", "depends", "env", "path_append", "description"} {
		if _, ok := top[k]; ok {
			return RefKindLayer, nil
		}
	}
	return "", fmt.Errorf("ResolveDeployRef: %s has no recognized image or layer keys", path)
}

// resolveLocalName checks the unified loader's images and candy/ for
// a matching name. Cross-kind name reuse is permitted (since 2026-05);
// preferKind decides precedence when the name exists in both kinds.
func resolveLocalName(name, projectDir string, preferKind RefKind) (*DeployRef, error) {
	imgYml := filepath.Join(projectDir, "box.yml")
	imagesYml := filepath.Join(projectDir, "images.yml")
	layersDir := filepath.Join(projectDir, DefaultCandyDir, name)

	inImageYml := false
	resolvedImgPath := imgYml
	// Schema v4: only charly.yml is the entry point. Resolve image
	// names through the unified loader (which pulls in sibling per-kind
	// files transparently). No direct file reads here.
	if uf, ok, err := LoadUnified(projectDir); err == nil && ok && uf != nil {
		// Namespace-aware presence check via the single resolver, so a qualified
		// deploy ref (`charly deploy add charly.<image>`) resolves the same way every
		// other command resolves an image name. Bare names hit the root map
		// exactly as the previous flat `uf.Image[name]` did.
		if _, _, present := uf.ProjectConfig().resolveImageRef(name); present {
			inImageYml = true
			resolvedImgPath = imgYml
		}
	}
	_ = imagesYml

	inLayers := false
	layerYML := filepath.Join(layersDir, "candy.yml")
	if info, err := os.Stat(layerYML); err == nil && !info.IsDir() {
		inLayers = true
	}

	imageRef := func() *DeployRef {
		return &DeployRef{
			Raw:    name,
			Kind:   RefKindImage,
			Source: RefSourceLocalName,
			Name:   name,
			Path:   resolvedImgPath,
		}
	}
	layerRef := func() *DeployRef {
		return &DeployRef{
			Raw:    name,
			Kind:   RefKindLayer,
			Source: RefSourceLocalName,
			Name:   name,
			Path:   layerYML,
		}
	}

	switch {
	case inImageYml && inLayers:
		// Cross-kind name reuse — preferKind decides. Both kinds
		// remain reachable via explicit paths (./candy/<name>/ or
		// the box config file with a #<name> fragment) or via ResolveDeployRefAsLayer.
		if preferKind == RefKindLayer {
			return layerRef(), nil
		}
		return imageRef(), nil
	case inImageYml:
		return imageRef(), nil
	case inLayers:
		return layerRef(), nil
	}

	return nil, fmt.Errorf("ResolveDeployRef: %q not found as image in %s or %s or layer in %s",
		name, imgYml, imagesYml, layersDir)
}
