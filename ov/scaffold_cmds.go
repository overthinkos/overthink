package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// scaffold_cmds.go — Kong command structs for the authoring + remote-repo
// surface. Each command auto-becomes an MCP tool via mcp_server.go's Kong
// reflection, so adding one here adds it to both the CLI and the MCP
// server in lockstep.

// ---------------------------------------------------------------------------
// `ov image new project <dir>`

type NewProjectCmd struct {
	Dir string `arg:"" help:"Directory to scaffold the project in (created if missing)"`
}

func (c *NewProjectCmd) Run() error {
	if err := ScaffoldProject(c.Dir); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Scaffolded project at %s\n", c.Dir)
	fmt.Fprintln(os.Stderr, "Next steps:")
	fmt.Fprintln(os.Stderr, "  # Wire a build.yml — copy from upstream, or reference a published release:")
	fmt.Fprintln(os.Stderr, "  cp /path/to/overthink/build.yml "+c.Dir+"/")
	fmt.Fprintln(os.Stderr, "  ov -C "+c.Dir+" image set defaults.format_config build.yml")
	fmt.Fprintln(os.Stderr, "  # Add an image, a layer, and build:")
	fmt.Fprintln(os.Stderr, "  ov -C "+c.Dir+" image new image my-image --base quay.io/fedora/fedora:43 --layers my-layer")
	fmt.Fprintln(os.Stderr, "  ov -C "+c.Dir+" image new layer my-layer")
	fmt.Fprintln(os.Stderr, "  ov -C "+c.Dir+" layer add-rpm my-layer curl jq")
	fmt.Fprintln(os.Stderr, "  ov -C "+c.Dir+" image build my-image")
	return nil
}

// ---------------------------------------------------------------------------
// `ov image new image <name>`

type NewImageCmd struct {
	Name   string   `arg:"" help:"Name for the new image entry"`
	Base   string   `long:"base" required:"" help:"Base image (URL like quay.io/... or another image name)"`
	Layers []string `long:"layers" sep:"," help:"Comma-separated list of layer names to include"`
}

func (c *NewImageCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := AddImage(dir, c.Name, c.Base, c.Layers); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Added image %s to overthink.yml\n", c.Name)
	return nil
}

// ---------------------------------------------------------------------------
// `ov image set <dotpath> <value>`

type ImageSetCmd struct {
	Path  string `arg:"" help:"Dot-path into overthink.yml (e.g. defaults.tag, image.foo.layers)"`
	Value string `arg:"" help:"Value (parsed as YAML; use [a,b] for lists, {x: y} for maps)"`
}

func (c *ImageSetCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	target := filepath.Join(dir, "overthink.yml")
	if _, err := os.Stat(target); os.IsNotExist(err) {
		return fmt.Errorf("overthink.yml not found in %s; run `ov image new project .` to scaffold or `ov migrate` to convert a legacy image.yml", dir)
	}
	return SetByDotPath(target, c.Path, c.Value)
}

// ---------------------------------------------------------------------------
// `ov image add-layer <image> <layer>`

type ImageAddLayerCmd struct {
	Image string `arg:"" help:"Name of the image in image.yml"`
	Layer string `arg:"" help:"Name of the layer to append"`
}

func (c *ImageAddLayerCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	return AddLayerToImage(dir, c.Image, c.Layer)
}

// ---------------------------------------------------------------------------
// `ov image rm-layer <image> <layer>`

type ImageRmLayerCmd struct {
	Image string `arg:"" help:"Name of the image in image.yml"`
	Layer string `arg:"" help:"Name of the layer to remove"`
}

func (c *ImageRmLayerCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	return RemoveLayerFromImage(dir, c.Image, c.Layer)
}

// ---------------------------------------------------------------------------
// `ov image fetch [<spec>]` and `ov image refresh [<spec>]`

type ImageFetchCmd struct {
	Spec string `arg:"" optional:"" help:"Repo spec (default: 'default' → overthinkos/overthink)"`
}

func (c *ImageFetchCmd) Run() error {
	spec := c.Spec
	if spec == "" {
		spec = "default"
	}
	path, err := ResolveProjectRepo(spec)
	if err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}

type ImageRefreshCmd struct {
	Spec string `arg:"" optional:"" help:"Repo spec (default: 'default' → overthinkos/overthink)"`
}

func (c *ImageRefreshCmd) Run() error {
	spec := c.Spec
	if spec == "" {
		spec = "default"
	}
	repoPath, version := normalizeRepoSpec(spec)
	if version == "" {
		branch, err := GitDefaultBranch(RepoGitURL(repoPath))
		if err != nil {
			return fmt.Errorf("resolving default branch for %s: %w", repoPath, err)
		}
		version = branch
	}
	cachePath, err := RepoCachePath(repoPath, version)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(cachePath); err != nil {
		return fmt.Errorf("removing cache %s: %w", cachePath, err)
	}
	path, err := ResolveProjectRepo(spec)
	if err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}

// ---------------------------------------------------------------------------
// `ov image write <rel-path>` and `ov image cat <rel-path>`

type ImageWriteCmd struct {
	Path    string `arg:"" help:"Path under the project root (relative; .. is rejected)"`
	Content string `long:"content" help:"File content (mutually exclusive with --from-stdin)"`
	FromIn  bool   `long:"from-stdin" help:"Read file content from stdin"`
}

func (c *ImageWriteCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	abs, err := resolveProjectFile(dir, c.Path)
	if err != nil {
		return err
	}
	var data []byte
	switch {
	case c.FromIn && c.Content != "":
		return fmt.Errorf("--content and --from-stdin are mutually exclusive")
	case c.FromIn:
		data, err = io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("reading stdin: %w", err)
		}
	default:
		data = []byte(c.Content)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fmt.Errorf("creating parent dir: %w", err)
	}
	if err := os.WriteFile(abs, data, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", abs, err)
	}
	fmt.Fprintf(os.Stderr, "Wrote %d bytes to %s\n", len(data), abs)
	return nil
}

type ImageCatCmd struct {
	Path string `arg:"" help:"Path under the project root (relative; .. is rejected)"`
}

func (c *ImageCatCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	abs, err := resolveProjectFile(dir, c.Path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(data)
	return err
}

// resolveProjectFile turns a user-supplied relative path into an absolute
// path under projectDir, rejecting absolute paths and any traversal that
// would escape the project root. This is the one safety boundary for the
// `ov image write` / `ov image cat` escape hatch — every path passes
// through here.
func resolveProjectFile(projectDir, relPath string) (string, error) {
	if relPath == "" {
		return "", fmt.Errorf("path must be specified")
	}
	if filepath.IsAbs(relPath) {
		return "", fmt.Errorf("path must be relative to project root, got absolute %q", relPath)
	}
	abs := filepath.Clean(filepath.Join(projectDir, relPath))
	rel, err := filepath.Rel(projectDir, abs)
	if err != nil {
		return "", fmt.Errorf("computing relative path: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes the project root", relPath)
	}
	return abs, nil
}

// ---------------------------------------------------------------------------
// `ov layer …` — top-level group for editing layer.yml files

type LayerCmd struct {
	Set    LayerSetCmd    `cmd:"" help:"Set a value in layers/<name>/layer.yml by dot-path"`
	AddRpm LayerAddPkgCmd `cmd:"add-rpm" help:"Append packages to a layer's rpm.packages list"`
	AddDeb LayerAddPkgCmd `cmd:"add-deb" help:"Append packages to a layer's deb.packages list"`
	AddPac LayerAddPkgCmd `cmd:"add-pac" help:"Append packages to a layer's pac.packages list"`
	AddAur LayerAddPkgCmd `cmd:"add-aur" help:"Append packages to a layer's aur.packages list"`
}

type LayerSetCmd struct {
	Name  string `arg:"" help:"Layer name (under layers/)"`
	Path  string `arg:"" help:"Dot-path into layer.yml (e.g. service.name, env.MY_VAR)"`
	Value string `arg:"" help:"Value (parsed as YAML)"`
}

func (c *LayerSetCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	layerYml := filepath.Join(dir, "layers", c.Name, "layer.yml")
	if _, err := os.Stat(layerYml); err != nil {
		return fmt.Errorf("layer %q not found at %s", c.Name, layerYml)
	}
	return SetByDotPath(layerYml, c.Path, c.Value)
}

// LayerAddPkgCmd is shared between add-rpm/add-deb/add-pac/add-aur. The
// section name is derived from the Kong command name at runtime. Since
// Kong dispatches to the *same* struct type for all four, we determine
// "which section" via a back-channel: each command instance is its own
// receiver, so we pass the section name as part of the command kong tag
// string. To keep this simple we use the same struct and look at the
// Kong context's selected command path.
//
// Implementation choice: instead of plumbing Kong context, we instantiate
// four distinct concrete types so the section is hard-wired per type.
type LayerAddPkgCmd struct {
	Name     string   `arg:"" help:"Layer name (under layers/)"`
	Packages []string `arg:"" help:"Package names to append"`
	// section is set by the parent group via aliases; default to rpm if
	// somehow invoked directly.
	section string `kong:"-"`
}

func (c *LayerAddPkgCmd) Run() error {
	// Kong doesn't fill section based on which alias was used, so derive
	// it from os.Args. This is a small runtime indirection but lets us
	// share one struct across four nearly-identical commands.
	section := detectPkgSection(os.Args)
	return appendLayerPackages(c.Name, section, c.Packages)
}

// detectPkgSection looks at os.Args for "add-rpm" / "add-deb" / etc. and
// returns the matching layer.yml section name. Defaults to "rpm" if none
// is found (defensive — Kong should always have routed via one of them).
func detectPkgSection(args []string) string {
	for _, a := range args {
		switch a {
		case "add-rpm":
			return "rpm"
		case "add-deb":
			return "deb"
		case "add-pac":
			return "pac"
		case "add-aur":
			return "aur"
		}
	}
	return "rpm"
}

// appendLayerPackages reads layers/<name>/layer.yml, appends packages to
// <section>.packages (creating the parent mappings as needed), and writes
// back — preserving comments via the yaml.Node API.
func appendLayerPackages(name, section string, pkgs []string) error {
	if len(pkgs) == 0 {
		return fmt.Errorf("no packages specified")
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	layerYml := filepath.Join(dir, "layers", name, "layer.yml")
	data, err := os.ReadFile(layerYml)
	if err != nil {
		return fmt.Errorf("reading %s: %w", layerYml, err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("parsing %s: %w", layerYml, err)
	}
	doc := &root
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		doc = doc.Content[0]
	}
	if doc.Kind != yaml.MappingNode {
		// Empty file or scalar root — synthesise a mapping.
		doc.Kind = yaml.MappingNode
		doc.Tag = "!!map"
		doc.Content = nil
	}
	sectionNode := mappingChild(doc, section)
	if sectionNode == nil {
		sectionNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		doc.Content = append(doc.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: section},
			sectionNode,
		)
	}
	pkgsNode := mappingChild(sectionNode, "packages")
	if pkgsNode == nil {
		pkgsNode = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		sectionNode.Content = append(sectionNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "packages"},
			pkgsNode,
		)
	} else if pkgsNode.Kind != yaml.SequenceNode {
		// Upgrade scaffold's `packages:` null/scalar to a real sequence in
		// place, so the existing key+comment association is preserved by
		// yaml.Marshal.
		pkgsNode.Kind = yaml.SequenceNode
		pkgsNode.Tag = "!!seq"
		pkgsNode.Value = ""
		pkgsNode.Content = nil
	}
	// Idempotent append: skip packages already present.
	existing := make(map[string]bool, len(pkgsNode.Content))
	for _, n := range pkgsNode.Content {
		if n.Kind == yaml.ScalarNode {
			existing[n.Value] = true
		}
	}
	for _, p := range pkgs {
		if existing[p] {
			continue
		}
		pkgsNode.Content = append(pkgsNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: p},
		)
	}
	out, err := yaml.Marshal(&root)
	if err != nil {
		return fmt.Errorf("marshalling %s: %w", layerYml, err)
	}
	return os.WriteFile(layerYml, out, 0o644)
}
