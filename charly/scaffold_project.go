package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// scaffold_project.go — project-level authoring helpers used by the
// `charly box new project`, `charly box new box`, `charly box add-candy`, and
// `charly box rm-candy` commands. These exist primarily so the MCP tool
// surface can author a project from scratch over RPC, without the agent
// needing direct filesystem access.
//
// All YAML mutations go through the yaml.v3 *node* API so comments and
// key order are preserved across edits — re-marshalling parsed values
// would scramble human-edited charly.yml files.
//
// Schema v4 cutover (2026-05): every authoring verb defaults to
// charly.yml as the canonical root file. Legacy projects with a
// per-kind box.yml at the project root must run `charly migrate`
// first; the scaffolders error cleanly when charly.yml is missing.

// scaffoldCharlyYAML is the seed charly.yml written into a fresh project. The
// project is immediately usable — the default distro/builder/init/resource build
// vocabulary is embedded in the charly binary (charly/build.yml), so there is no
// build.yml to copy or format_config to wire.
const scaffoldCharlyYAML = `# charly.yml — unified project root: the single file a project needs.
# See https://github.com/overthinkos/overthink for documentation.
#
# Box (image) and candy (layer) definitions are DISCOVERED per name:
#   box/<name>/charly.yml   — one box per directory
#   candy/<name>/charly.yml — one candy per directory
# The default distro/builder/init build vocabulary is EMBEDDED in the charly
# binary; declare distro:/builder:/init:/resource: here only to EXTEND or
# OVERRIDE it.
#
# Cross-kind name reuse is permitted — a single name (e.g. my-app) MAY exist
# simultaneously as a candy, a box, a pod, a vm, a k8s, a local, AND a deploy
# entry. charly verbs disambiguate by command context.

version: __SCHEMA_VERSION__

discover:
  - path: box
    recursive: true
  - path: candy
    recursive: true

defaults:
  registry: ghcr.io/example
  tag: auto
  platforms:
    - linux/amd64
  build: [rpm]
`

// scaffoldGitignore keeps the build artefact dir + common scratch files
// out of git so a fresh project is committable as-is.
const scaffoldGitignore = `# Build artefacts
.build/

# Editor / OS
.DS_Store
*.swp
`

// ScaffoldProject creates an empty charly project at dir. Idempotency: errors
// out if dir already contains an charly.yml so we never silently
// clobber an existing project. The dir itself may exist.
func ScaffoldProject(dir string) error {
	if dir == "" {
		return fmt.Errorf("project directory must be specified")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating project directory: %w", err)
	}
	charlyPath := filepath.Join(dir, UnifiedFileName)
	if _, err := os.Stat(charlyPath); err == nil {
		return fmt.Errorf("charly.yml already exists at %s; refusing to overwrite", charlyPath)
	}
	seed := strings.ReplaceAll(scaffoldCharlyYAML, "__SCHEMA_VERSION__", LatestSchemaVersion().String())
	if err := os.WriteFile(charlyPath, []byte(seed), 0o644); err != nil {
		return fmt.Errorf("writing charly.yml: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, DefaultBoxDir), 0o755); err != nil {
		return fmt.Errorf("creating box/: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, DefaultCandyDir), 0o755); err != nil {
		return fmt.Errorf("creating candy/: %w", err)
	}
	gitignorePath := filepath.Join(dir, ".gitignore")
	if _, err := os.Stat(gitignorePath); os.IsNotExist(err) {
		if err := os.WriteFile(gitignorePath, []byte(scaffoldGitignore), 0o644); err != nil {
			return fmt.Errorf("writing .gitignore: %w", err)
		}
	}
	return nil
}

// AddImage writes a new box to its discovered per-box file box/<name>/charly.yml
// (a kind-keyed `box:` doc). The base argument is the value of the box's `base:`
// field (an external URL or the name of another box). If layers is non-nil it
// populates the box's `candy:` list. Errors if box/<name>/charly.yml exists.
func AddImage(dir, name, base string, layers []string) error {
	if name == "" {
		return fmt.Errorf("box name must be specified")
	}
	dest := filepath.Join(dir, DefaultBoxDir, name, UnifiedFileName)
	if fileExists(dest) {
		return fmt.Errorf("box %q already exists at %s", name, dest)
	}
	inner := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	inner.Content = append(inner.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "name"},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "base"},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: base},
	)
	if len(layers) > 0 {
		layersNode := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, l := range layers {
			layersNode.Content = append(layersNode.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: l},
			)
		}
		inner.Content = append(inner.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "candy"},
			layersNode,
		)
	}
	wrapper := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	wrapper.Content = append(wrapper.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "box"},
		inner,
	)
	doc := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{wrapper}}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("creating box directory: %w", err)
	}
	return saveYAMLNodeFile(dest, doc)
}

// AddLayerToImage appends a layer to an existing image's `candy:` list.
// Idempotent: if the layer is already in the list, this is a no-op. The box is
// resolved across the discovered box/<name>/charly.yml, charly.yml, AND any
// flat-imported per-kind file,
// and the edit is saved to the file where the image actually lives.
func AddLayerToImage(dir, image, layer string) error {
	root, imgNode, path, err := resolveImageNodeFile(dir, image)
	if err != nil {
		return err
	}
	layersNode := mappingChild(imgNode, "candy")
	if layersNode == nil {
		layersNode = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		imgNode.Content = append(imgNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "candy"},
			layersNode,
		)
	}
	for _, n := range layersNode.Content {
		if n.Kind == yaml.ScalarNode && n.Value == layer {
			return nil
		}
	}
	layersNode.Content = append(layersNode.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: layer},
	)
	return saveYAMLNodeFile(path, root)
}

// RemoveLayerFromImage removes the named layer from an image's `candy:`
// list. Errors out if the image does not exist; succeeds silently if the
// layer is not present. The box is resolved across the discovered
// box/<name>/charly.yml, charly.yml, AND any flat-imported per-kind file, and
// the edit is saved to the file where the box actually lives.
func RemoveLayerFromImage(dir, image, layer string) error {
	root, imgNode, path, err := resolveImageNodeFile(dir, image)
	if err != nil {
		return err
	}
	layersNode := mappingChild(imgNode, "candy")
	if layersNode == nil {
		return nil
	}
	out := layersNode.Content[:0]
	for _, n := range layersNode.Content {
		if n.Kind == yaml.ScalarNode && n.Value == layer {
			continue
		}
		out = append(out, n)
	}
	layersNode.Content = out
	return saveYAMLNodeFile(path, root)
}

// ---------------------------------------------------------------------------
// yaml.Node helpers — kept private to this file so the surface is small.

func loadCharlyYAMLNode(dir string) (*yaml.Node, error) {
	path := filepath.Join(dir, UnifiedFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("charly.yml not found in %s; run `charly box new project .` to scaffold or `charly migrate` to convert legacy box.yml", dir)
		}
		return nil, fmt.Errorf("reading charly.yml: %w", err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parsing charly.yml: %w", err)
	}
	return &root, nil
}

func saveCharlyYAMLNode(dir string, root *yaml.Node) error {
	path := filepath.Join(dir, UnifiedFileName)
	data, err := yaml.Marshal(root)
	if err != nil {
		return fmt.Errorf("marshalling charly.yml: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing charly.yml: %w", err)
	}
	return nil
}

// docContent returns the top-level mapping node of a parsed YAML document.
// yaml.Unmarshal returns a DocumentNode whose single Content entry is the
// root mapping — peel that wrapper for callers.
func docContent(root *yaml.Node) *yaml.Node {
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		return root.Content[0]
	}
	return root
}

// mappingChild looks up a key in a mapping node. Returns the value node or
// nil if missing. yaml mapping nodes store [key, value, key, value, …].
func mappingChild(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// imagesMapNode returns the images mapping node from a document and the
// key that addressed it ("image" or "images"). Schema v4 canonical key
// is the singular "image:"; the plural "images:" is accepted for
// legacy projects (it's an alias normalized by LoadUnified). When the
// node is missing, returns ("", nil) so callers can append the
// canonical singular form.
func imagesMapNode(doc *yaml.Node) (string, *yaml.Node) {
	if n := mappingChild(doc, "box"); n != nil {
		return "box", n
	}
	if n := mappingChild(doc, "images"); n != nil {
		return "images", n
	}
	return "", nil
}

// imageNode returns the mapping node for the named image, or nil.
func imageNode(root *yaml.Node, name string) *yaml.Node {
	doc := docContent(root)
	_, imagesNode := imagesMapNode(doc)
	if imagesNode == nil {
		return nil
	}
	return mappingChild(imagesNode, name)
}

// flatLocalImports returns the bare-string `import:` items that are LOCAL file
// refs (same-repo per-kind files such as box.yml) — NOT @github refs and NOT
// namespaced single-key-map imports. The authoring-edit verbs search these for
// an image defined outside charly.yml itself.
func flatLocalImports(root *yaml.Node) []string {
	doc := docContent(root)
	imp := mappingChild(doc, "import")
	if imp == nil || imp.Kind != yaml.SequenceNode {
		return nil
	}
	var out []string
	for _, item := range imp.Content {
		if item.Kind == yaml.ScalarNode {
			ref := strings.TrimSpace(item.Value)
			if ref != "" && !strings.HasPrefix(ref, "@") {
				out = append(out, ref)
			}
		}
	}
	return out
}

// resolveImageNodeFile finds the YAML file that DEFINES box `name` — the
// discovered box/<name>/charly.yml (the canonical location), else charly.yml
// itself, else one of its flat-imported local per-kind files — and returns that
// file's parsed node tree, the box's value node, and the file path. The
// authoring-edit verbs (add-candy/rm-candy) mutate + save that file, so they work
// on boxes wherever they live, not only those inlined in charly.yml.
func resolveImageNodeFile(dir, name string) (*yaml.Node, *yaml.Node, string, error) {
	// Discovered per-box file box/<name>/charly.yml (the canonical location) — a
	// kind-keyed `box:` doc whose value node is the box's inner mapping.
	boxFile := filepath.Join(dir, DefaultBoxDir, name, UnifiedFileName)
	if data, rerr := os.ReadFile(boxFile); rerr == nil {
		var froot yaml.Node
		if yaml.Unmarshal(data, &froot) == nil {
			if rm := docRootMapping(&froot); rm != nil {
				if inner := nodeMapValue(rm, "box"); inner != nil && inner.Kind == yaml.MappingNode {
					return &froot, inner, boxFile, nil
				}
			}
		}
	}
	charlyRoot, err := loadCharlyYAMLNode(dir)
	if err != nil {
		return nil, nil, "", err
	}
	if n := imageNode(charlyRoot, name); n != nil {
		return charlyRoot, n, filepath.Join(dir, UnifiedFileName), nil
	}
	for _, ref := range flatLocalImports(charlyRoot) {
		p := filepath.Join(dir, ref)
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			continue
		}
		var froot yaml.Node
		if yaml.Unmarshal(data, &froot) != nil {
			continue
		}
		if n := imageNode(&froot, name); n != nil {
			return &froot, n, p, nil
		}
	}
	return nil, nil, "", fmt.Errorf("image %q not found in charly.yml or its imported per-kind files", name)
}

// saveYAMLNodeFile marshals a node tree back to an arbitrary file path,
// preserving comments + key order (the yaml.v3 Node round-trip). The generic
// sibling of saveCharlyYAMLNode, used when an edit targets a per-kind import
// file rather than charly.yml itself.
func saveYAMLNodeFile(path string, root *yaml.Node) error {
	data, err := yaml.Marshal(root)
	if err != nil {
		return fmt.Errorf("marshalling %s: %w", filepath.Base(path), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", filepath.Base(path), err)
	}
	return nil
}
