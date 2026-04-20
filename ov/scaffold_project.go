package main

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// scaffold_project.go — project-level authoring helpers used by the
// `ov image new project`, `ov image new image`, `ov image add-layer`, and
// `ov image rm-layer` commands. These exist primarily so the MCP tool
// surface can author a project from scratch over RPC, without the agent
// needing direct filesystem access.
//
// All YAML mutations go through the yaml.v3 *node* API so comments and
// key order are preserved across edits — re-marshalling parsed values
// would scramble human-edited image.yml files.

// scaffoldImageYAML is the seed image.yml written into a fresh project.
// Uses the upstream build.yml via format_config remote ref so the new
// project doesn't have to copy the canonical 1k-line build.yml.
const scaffoldImageYAML = `# image.yml — image definitions for this project.
# See https://github.com/overthinkos/overthink for documentation.
#
# Before building you must wire format_config to a build.yml — either:
#   1. Copy build.yml from the overthinkos/overthink repo and point at it:
#        format_config: build.yml
#   2. Reference a published release remotely:
#        format_config: "@github.com/overthinkos/overthink/build.yml:<tag>"

defaults:
  registry: ghcr.io/example
  tag: auto
  platforms:
    - linux/amd64
  build: [rpm]
  # format_config: build.yml          # ← uncomment after you've placed build.yml here

images: {}
`

// scaffoldGitignore keeps the build artefact dir + common scratch files
// out of git so a fresh project is committable as-is.
const scaffoldGitignore = `# Build artefacts
.build/

# Editor / OS
.DS_Store
*.swp
`

// ScaffoldProject creates an empty ov project at dir. Idempotency: errors
// out if dir already contains an image.yml so we never silently clobber an
// existing project. The dir itself may exist.
func ScaffoldProject(dir string) error {
	if dir == "" {
		return fmt.Errorf("project directory must be specified")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating project directory: %w", err)
	}
	imagePath := filepath.Join(dir, "image.yml")
	if _, err := os.Stat(imagePath); err == nil {
		return fmt.Errorf("image.yml already exists at %s; refusing to overwrite", imagePath)
	}
	if err := os.WriteFile(imagePath, []byte(scaffoldImageYAML), 0o644); err != nil {
		return fmt.Errorf("writing image.yml: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "layers"), 0o755); err != nil {
		return fmt.Errorf("creating layers/: %w", err)
	}
	gitignorePath := filepath.Join(dir, ".gitignore")
	if _, err := os.Stat(gitignorePath); os.IsNotExist(err) {
		if err := os.WriteFile(gitignorePath, []byte(scaffoldGitignore), 0o644); err != nil {
			return fmt.Errorf("writing .gitignore: %w", err)
		}
	}
	return nil
}

// AddImage appends a new image entry to image.yml under the given dir.
// Existing entries are not touched. The base argument is the value of
// the image's `base:` field (either an external URL or the name of
// another image in image.yml). If layers is non-nil it populates the
// image's `layers:` list.
func AddImage(dir, name, base string, layers []string) error {
	if name == "" {
		return fmt.Errorf("image name must be specified")
	}
	root, err := loadImageYAMLNode(dir)
	if err != nil {
		return err
	}
	doc := docContent(root)
	imagesNode := mappingChild(doc, "images")
	if imagesNode == nil {
		// `images:` is missing entirely — append it. Use a flow node so the
		// canonical scaffold's `images: {}` is replaced by a real mapping.
		imagesNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		doc.Content = append(doc.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "images"},
			imagesNode,
		)
	}
	// Reset to block style if the existing node was `{}` flow.
	imagesNode.Style = 0
	if mappingChild(imagesNode, name) != nil {
		return fmt.Errorf("image %q already exists in image.yml", name)
	}
	imgValue := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	imgValue.Content = append(imgValue.Content,
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
		imgValue.Content = append(imgValue.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "layers"},
			layersNode,
		)
	}
	imagesNode.Content = append(imagesNode.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name},
		imgValue,
	)
	return saveImageYAMLNode(dir, root)
}

// AddLayerToImage appends a layer to an existing image's `layers:` list.
// Idempotent: if the layer is already in the list, this is a no-op.
func AddLayerToImage(dir, image, layer string) error {
	root, err := loadImageYAMLNode(dir)
	if err != nil {
		return err
	}
	imgNode := imageNode(root, image)
	if imgNode == nil {
		return fmt.Errorf("image %q not found in image.yml", image)
	}
	layersNode := mappingChild(imgNode, "layers")
	if layersNode == nil {
		layersNode = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		imgNode.Content = append(imgNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "layers"},
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
	return saveImageYAMLNode(dir, root)
}

// RemoveLayerFromImage removes the named layer from an image's `layers:`
// list. Errors out if the image does not exist; succeeds silently if the
// layer is not present.
func RemoveLayerFromImage(dir, image, layer string) error {
	root, err := loadImageYAMLNode(dir)
	if err != nil {
		return err
	}
	imgNode := imageNode(root, image)
	if imgNode == nil {
		return fmt.Errorf("image %q not found in image.yml", image)
	}
	layersNode := mappingChild(imgNode, "layers")
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
	return saveImageYAMLNode(dir, root)
}

// ---------------------------------------------------------------------------
// yaml.Node helpers — kept private to this file so the surface is small.

func loadImageYAMLNode(dir string) (*yaml.Node, error) {
	path := filepath.Join(dir, "image.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading image.yml: %w", err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parsing image.yml: %w", err)
	}
	return &root, nil
}

func saveImageYAMLNode(dir string, root *yaml.Node) error {
	path := filepath.Join(dir, "image.yml")
	data, err := yaml.Marshal(root)
	if err != nil {
		return fmt.Errorf("marshalling image.yml: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing image.yml: %w", err)
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

// imageNode returns the mapping node for the named image, or nil.
func imageNode(root *yaml.Node, name string) *yaml.Node {
	doc := docContent(root)
	imagesNode := mappingChild(doc, "images")
	if imagesNode == nil {
		return nil
	}
	return mappingChild(imagesNode, name)
}
