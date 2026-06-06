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
// `ov box new project <dir>`

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
	fmt.Fprintln(os.Stderr, "  ov -C "+c.Dir+" box set defaults.format_config build.yml")
	fmt.Fprintln(os.Stderr, "  # Add an image, a layer, and build:")
	fmt.Fprintln(os.Stderr, "  ov -C "+c.Dir+" box new box my-image --base quay.io/fedora/fedora:43 --candy my-layer")
	fmt.Fprintln(os.Stderr, "  ov -C "+c.Dir+" box new candy my-layer")
	fmt.Fprintln(os.Stderr, "  ov -C "+c.Dir+" candy add-rpm my-layer curl jq")
	fmt.Fprintln(os.Stderr, "  ov -C "+c.Dir+" box build my-image")
	return nil
}

// ---------------------------------------------------------------------------
// `ov box new box <name>`

type NewBoxCmd struct {
	Name   string   `arg:"" help:"Name for the new image entry"`
	Base   string   `long:"base" required:"" help:"Base image (URL like quay.io/... or another image name)"`
	Layers []string `long:"candy" sep:"," help:"Comma-separated list of layer names to include"`
}

func (c *NewBoxCmd) Run() error {
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
// `ov box set <dotpath> <value>`

type BoxSetCmd struct {
	Path  string `arg:"" help:"Dot-path into overthink.yml (e.g. defaults.tag, image.foo.layers)"`
	Value string `arg:"" help:"Value (parsed as YAML; use [a,b] for lists, {x: y} for maps)"`
}

func (c *BoxSetCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	target := filepath.Join(dir, "overthink.yml")
	if _, err := os.Stat(target); os.IsNotExist(err) {
		return fmt.Errorf("overthink.yml not found in %s; run `ov box new project .` to scaffold or `ov migrate` to convert a legacy box.yml", dir)
	}
	return SetByDotPath(target, c.Path, c.Value)
}

// ---------------------------------------------------------------------------
// `ov box add-candy <image> <layer>`

type BoxAddCandyCmd struct {
	Image string `arg:"" help:"Name of the image in overthink.yml"`
	Layer string `arg:"" help:"Name of the layer to append"`
}

func (c *BoxAddCandyCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	return AddLayerToImage(dir, c.Image, c.Layer)
}

// ---------------------------------------------------------------------------
// `ov box rm-candy <image> <layer>`

type BoxRmCandyCmd struct {
	Image string `arg:"" help:"Name of the image in overthink.yml"`
	Layer string `arg:"" help:"Name of the layer to remove"`
}

func (c *BoxRmCandyCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	return RemoveLayerFromImage(dir, c.Image, c.Layer)
}

// ---------------------------------------------------------------------------
// `ov box fetch [<spec>]` and `ov box refresh [<spec>]`

type BoxFetchCmd struct {
	Spec string `arg:"" optional:"" help:"Repo spec (default: 'default' → overthinkos/overthink)"`
}

func (c *BoxFetchCmd) Run() error {
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

type BoxRefreshCmd struct {
	Spec string `arg:"" optional:"" help:"Repo spec (default: 'default' → overthinkos/overthink)"`
}

func (c *BoxRefreshCmd) Run() error {
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
// `ov box write <rel-path>` and `ov box cat <rel-path>`

type BoxWriteCmd struct {
	Path    string `arg:"" help:"Path under the project root (relative; .. is rejected)"`
	Content string `long:"content" help:"File content (mutually exclusive with --from-stdin)"`
	FromIn  bool   `long:"from-stdin" help:"Read file content from stdin"`
}

func (c *BoxWriteCmd) Run() error {
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

type BoxCatCmd struct {
	Path string `arg:"" help:"Path under the project root (relative; .. is rejected)"`
}

func (c *BoxCatCmd) Run() error {
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
// `ov box write` / `ov box cat` escape hatch — every path passes
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
// `ov candy …` — top-level group for editing candy manifest files

type CandyCmd struct {
	Set         CandySetCmd         `cmd:"" help:"Set a value in a candy manifest by dot-path"`
	AddRpm      CandyAddPkgCmd      `cmd:"add-rpm" help:"Append packages to a layer's rpm.packages list"`
	AddDeb      CandyAddPkgCmd      `cmd:"add-deb" help:"Append packages to a layer's deb.packages list"`
	AddPac      CandyAddPkgCmd      `cmd:"add-pac" help:"Append packages to a layer's pac.packages list"`
	AddAur      CandyAddPkgCmd      `cmd:"add-aur" help:"Append packages to a layer's aur.packages list"`
	AddScenario CandyAddScenarioCmd `cmd:"add-scenario" help:"Append a Gherkin acceptance scenario to a layer's description (idempotent; Agent Driven Development)"`
}

type CandySetCmd struct {
	Name  string `arg:"" help:"Layer name (under candy/)"`
	Path  string `arg:"" help:"Dot-path into the candy manifest (e.g. service.name, env.MY_VAR)"`
	Value string `arg:"" help:"Value (parsed as YAML)"`
}

func (c *CandySetCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	layerYml := filepath.Join(dir, DefaultCandyDir, c.Name, DefaultManifest)
	if _, err := os.Stat(layerYml); err != nil {
		return fmt.Errorf("candy %q not found at %s", c.Name, layerYml)
	}
	// Candy manifests are kind-keyed under `candy:` (the layer kind key), so a
	// body-relative dot-path like `version` or `env.X` must descend into the
	// `candy:` wrapper. Without this, SetByDotPath appends a stray top-level
	// key (e.g. a second `version:`) and the loader then rejects the file as
	// ambiguous.
	path := c.Path
	if path != "candy" && !strings.HasPrefix(path, "candy.") {
		path = "candy." + path
	}
	return SetByDotPath(layerYml, path, c.Value)
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
type CandyAddPkgCmd struct {
	Name     string   `arg:"" help:"Layer name (under candy/)"`
	Packages []string `arg:"" help:"Package names to append"`
	// section is set by the parent group via aliases; default to rpm if
	// somehow invoked directly.
	section string `kong:"-"`
}

func (c *CandyAddPkgCmd) Run() error {
	// Kong doesn't fill section based on which alias was used, so derive
	// it from os.Args. This is a small runtime indirection but lets us
	// share one struct across four nearly-identical commands.
	section := detectPkgSection(os.Args)
	return appendLayerPackages(c.Name, section, c.Packages)
}

// detectPkgSection looks at os.Args for "add-rpm" / "add-deb" / etc. and
// returns the matching candy manifest section name. Defaults to "rpm" if none
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

// appendLayerPackages reads the candy manifest, appends packages to
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
	layerYml := filepath.Join(dir, DefaultCandyDir, name, DefaultManifest)
	data, err := os.ReadFile(layerYml)
	if err != nil {
		return fmt.Errorf("reading %s: %w", layerYml, err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("parsing %s: %w", layerYml, err)
	}
	// Candy manifests are kind-keyed under `candy:` — package sections
	// (rpm/deb/pac/aur) live INSIDE that wrapper, not at the document root.
	candy, err := candyBodyNode(&root)
	if err != nil {
		return fmt.Errorf("%s: %w", layerYml, err)
	}
	sectionNode := mappingChild(candy, section)
	if sectionNode == nil {
		sectionNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		candy.Content = append(candy.Content,
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
		existing[p] = true // dedupe within this call too, not just vs pre-existing
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
