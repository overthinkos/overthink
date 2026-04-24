package main

// deploy_ref.go — unified image/layer reference resolver for
// `ov deploy add <name> <ref>`, `--add-layer <ref>`, and deploy.yml
// image:/add_layers: fields.
//
// <ref> accepts four forms, auto-detected:
//
//   1. Local image name         "fedora-coder"
//      Matches a top-level entry in image.yml.
//
//   2. Local layer name         "pre-commit"
//      Matches a directory in layers/.
//
//   3. Local YAML path          "./my-image.yml" | "/abs/path/layer.yml"
//      Starts with "./" or "/"; ends with ".yml" or ".yaml". The file's
//      top-level keys tell us whether it's an image or layer declaration.
//
//   4. Remote repo ref          "github.com/owner/repo[/images/<n>|/layers/<n>][@ref]"
//      Matches "{host}/{org}/{repo}[/sub][@ref]" with a known host. The
//      existing refs.go `@`-prefixed form is also accepted for backward
//      compat with image.yml depends:/layers: already in the tree.
//
// Disambiguation rules:
//   - Any ref containing "/layers/" resolves to a layer.
//   - Any ref containing "/images/" resolves to an image.
//   - A local name found in BOTH image.yml and layers/ is a hard error
//     (authoring bug).
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
func ResolveDeployRef(ref, projectDir string) (*DeployRef, error) {
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

	// Forms 1 + 2: local name. Check image.yml then layers/; error if ambiguous.
	return resolveLocalName(ref, projectDir)
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

// resolveRemoteRef parses and classifies an @-prefixed remote ref.
func resolveRemoteRef(ref string) (*DeployRef, error) {
	parsed := ParseRemoteRef(ref)
	kind := RefKindLayer
	switch {
	case strings.Contains(parsed.SubPath, "/layers/") || strings.HasPrefix(parsed.SubPath, "layers/"):
		kind = RefKindLayer
	case strings.Contains(parsed.SubPath, "/images/") || strings.HasPrefix(parsed.SubPath, "images/"):
		kind = RefKindImage
	default:
		// A bare repo ref (no /layers/ or /images/) defaults to the
		// project's image.yml, which is image-shaped. Existing tooling
		// treats such refs as project imports; we follow suit.
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
		// A directory ref points at a layer directory (layers/<name>/).
		path = filepath.Join(path, "layer.yml")
		if _, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("ResolveDeployRef: directory %s has no layer.yml", ref)
		}
	}
	kind, err := classifyYAMLFile(path)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if kind == RefKindLayer && filepath.Base(path) == "layer.yml" {
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
	for _, k := range []string{"images", "base", "defaults"} {
		if _, ok := top[k]; ok {
			return RefKindImage, nil
		}
	}
	// Layer-shaped: has any layer marker. This list roughly matches
	// layer.yml's documented top-level fields; a YAML that has none of
	// these keys is an error (we don't try to guess).
	for _, k := range []string{"rpm", "deb", "pac", "aur", "tasks", "services", "service", "system_services", "layers", "depends", "env", "path_append", "description"} {
		if _, ok := top[k]; ok {
			return RefKindLayer, nil
		}
	}
	return "", fmt.Errorf("ResolveDeployRef: %s has no recognized image or layer keys", path)
}

// resolveLocalName checks image.yml, images.yml (unified), then layers/
// for a matching name. Ambiguity is a hard error per the plan's
// disambiguation rule.
func resolveLocalName(name, projectDir string) (*DeployRef, error) {
	imgYml := filepath.Join(projectDir, "image.yml")
	imagesYml := filepath.Join(projectDir, "images.yml")
	layersDir := filepath.Join(projectDir, "layers", name)

	inImageYml := false
	resolvedImgPath := imgYml
	// Schema v4: only overthink.yml is the entry point. Resolve image
	// names through the unified loader (which pulls in includes like
	// image.yml / images.yml transparently). No direct file reads here.
	if uf, ok, err := LoadUnified(projectDir); err == nil && ok && uf != nil {
		if _, present := uf.Images[name]; present {
			inImageYml = true
			resolvedImgPath = imgYml
		}
	}
	_ = imagesYml

	inLayers := false
	layerYML := filepath.Join(layersDir, "layer.yml")
	if info, err := os.Stat(layerYML); err == nil && !info.IsDir() {
		inLayers = true
	}

	switch {
	case inImageYml && inLayers:
		return nil, fmt.Errorf("ResolveDeployRef: name %q is both an image and a layer — use an explicit path or remote ref to disambiguate", name)
	case inImageYml:
		return &DeployRef{
			Raw:    name,
			Kind:   RefKindImage,
			Source: RefSourceLocalName,
			Name:   name,
			Path:   resolvedImgPath,
		}, nil
	case inLayers:
		return &DeployRef{
			Raw:    name,
			Kind:   RefKindLayer,
			Source: RefSourceLocalName,
			Name:   name,
			Path:   layerYML,
		}, nil
	}

	return nil, fmt.Errorf("ResolveDeployRef: %q not found as image in %s or %s or layer in %s",
		name, imgYml, imagesYml, layersDir)
}
