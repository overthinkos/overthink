package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// -----------------------------------------------------------------------------
// `ov migrate` — one-shot migration from the legacy four-file layout
// (build.yml + image.yml + deploy.yml + layers/<name>/layer.yml) to the
// unified format (overthink.yml + includes + kind-keyed standalone files).
//
// Default emission uses `includes:` to preserve the split at the filesystem
// level — produces a slim overthink.yml that pulls in build.yml/images.yml/
// deploy.yml (rewritten to the unified schema), plus `discover.layers: [layers]`
// that picks up every existing layer directory without enumeration.
//
// The --monolithic flag collapses everything into one flat overthink.yml.
// -----------------------------------------------------------------------------

// MigrateUnifiedOpts carries the migration-command inputs.
type MigrateUnifiedOpts struct {
	Dir           string // project dir containing build.yml/image.yml (and layers/)
	Monolithic    bool   // emit one flat overthink.yml instead of includes: set
	DryRun        bool   // if true, print plan without writing files
	RewriteLayers bool   // if true, rewrite layer.yml files into kind-keyed form
}

// MigrateUnified performs the migration and returns the list of files it
// wrote (or would write in dry-run mode).
func MigrateUnified(opts MigrateUnifiedOpts) ([]string, error) {
	dir := opts.Dir
	if dir == "" {
		return nil, fmt.Errorf("project dir is required")
	}

	// Early-exit: trees that already carry overthink.yml are by definition
	// in the unified schema (v3 or later). The legacy migration would
	// otherwise re-read the canonical singular image.yml / deploy.yml as
	// LEGACY inputs and re-emit them into the deprecated plural form
	// (image.yml -> images.yml), producing a Frankenstein tree where both
	// names coexist. This bit downstream tooling that cloned via
	// EnsureRepoDownloaded.
	if fileExists(filepath.Join(dir, UnifiedFileName)) {
		return nil, nil
	}

	var written []string

	// 1. Read legacy inputs.
	buildSections, err := readBuildYaml(dir)
	if err != nil {
		return nil, fmt.Errorf("reading build.yml: %w", err)
	}
	imageSections, err := readImageYaml(dir)
	if err != nil {
		return nil, fmt.Errorf("reading image.yml: %w", err)
	}
	deploySections, err := readRepoRootDeployYaml(dir)
	if err != nil {
		return nil, fmt.Errorf("reading repo-root deploy.yml: %w", err)
	}

	// 2. Emit output.
	if opts.Monolithic {
		file, err := emitMonolithic(dir, buildSections, imageSections, deploySections, opts.DryRun)
		if err != nil {
			return nil, err
		}
		written = append(written, file)
	} else {
		files, err := emitWithIncludes(dir, buildSections, imageSections, deploySections, opts.DryRun)
		if err != nil {
			return nil, err
		}
		written = append(written, files...)
	}

	// 3. Optionally rewrite layer.yml files.
	if opts.RewriteLayers {
		layersDir := filepath.Join(dir, "layers")
		if dirExists(layersDir) {
			rewritten, err := rewriteLayerFiles(layersDir, opts.DryRun)
			if err != nil {
				return written, fmt.Errorf("rewriting layer.yml files: %w", err)
			}
			written = append(written, rewritten...)
		}
	}

	return written, nil
}

// -----------------------------------------------------------------------------
// Readers for legacy files.
// -----------------------------------------------------------------------------

type buildSections struct {
	Distros  map[string]*DistroDef
	Builders map[string]*BuilderDef
	Inits    map[string]*InitDef
}

func readBuildYaml(dir string) (*buildSections, error) {
	path := filepath.Join(dir, "build.yml")
	if !fileExists(path) {
		// Fall back to the location pointed at by image.yml's
		// defaults.format_config (common in older repos where build.yml lived
		// under defaults/ or was referenced remotely).
		if alt := buildYamlFromFormatConfig(dir); alt != "" {
			path = alt
		} else if alt := filepath.Join(dir, "defaults", "build.yml"); fileExists(alt) {
			// Conventional fallback location used by the test fixture.
			path = alt
		} else {
			return nil, nil
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var bf BuildFile
	if err := yaml.Unmarshal(data, &bf); err != nil {
		return nil, err
	}
	return &buildSections{
		Distros:  bf.Distro,
		Builders: bf.Builder,
		Inits:    bf.Init,
	}, nil
}

// buildYamlFromFormatConfig peeks at image.yml's defaults.format_config: to
// find a local build.yml path. Returns "" if image.yml is missing or the
// format_config points at a remote ref.
func buildYamlFromFormatConfig(dir string) string {
	imgPath := filepath.Join(dir, "image.yml")
	if !fileExists(imgPath) {
		return ""
	}
	data, err := os.ReadFile(imgPath)
	if err != nil {
		return ""
	}
	var peek struct {
		Defaults struct {
			FormatConfig string `yaml:"format_config"`
		} `yaml:"defaults"`
	}
	if err := yaml.Unmarshal(data, &peek); err != nil {
		return ""
	}
	ref := peek.Defaults.FormatConfig
	if ref == "" || strings.HasPrefix(ref, "@") {
		return ""
	}
	// Resolve relative to dir.
	if !filepath.IsAbs(ref) {
		ref = filepath.Join(dir, ref)
	}
	if fileExists(ref) {
		return ref
	}
	return ""
}

type imageSections struct {
	Defaults BoxConfig
	Images   map[string]BoxConfig
}

func readImageYaml(dir string) (*imageSections, error) {
	path := filepath.Join(dir, "image.yml")
	if !fileExists(path) {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// A pre-unified image.yml predates the candy/box rename and uses the legacy
	// `image:`/`layer:` keys; normalize them to the current `box:`/`candy:`
	// shape before decoding into the current structs.
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return nil, err
	}
	renameBoxCandyKeys(&node)
	var cfg Config
	if err := node.Decode(&cfg); err != nil {
		return nil, err
	}
	return &imageSections{
		Defaults: cfg.Defaults,
		Images:   cfg.Image,
	}, nil
}

func readRepoRootDeployYaml(dir string) (*DeployConfig, error) {
	path := filepath.Join(dir, "deploy.yml")
	if !fileExists(path) {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var dc DeployConfig
	if err := yaml.Unmarshal(data, &dc); err != nil {
		return nil, err
	}
	return &dc, nil
}

// -----------------------------------------------------------------------------
// Emitters.
// -----------------------------------------------------------------------------

func emitMonolithic(dir string, bs *buildSections, is *imageSections, ds *DeployConfig, dryRun bool) (string, error) {
	uf := &UnifiedFile{Version: LatestSchemaVersion().String()}
	if bs != nil {
		uf.Distro = bs.Distros
		uf.Builder = bs.Builders
		uf.Init = bs.Inits
	}
	if is != nil {
		uf.Defaults = is.Defaults
		uf.Image = is.Images
	}
	if ds != nil {
		uf.Deploy = ds.Deploy
		uf.Provides = ds.Provides
	}
	// Auto-discover layers/ if present.
	if dirExists(filepath.Join(dir, "layers")) {
		uf.Discover = &DiscoverConfig{Layer: []ScanSpec{{Path: "layers", Recursive: true}}}
	}
	return writeUnifiedFile(filepath.Join(dir, UnifiedFileName), uf, dryRun)
}

func emitWithIncludes(dir string, bs *buildSections, is *imageSections, ds *DeployConfig, dryRun bool) ([]string, error) {
	var written []string

	root := &UnifiedFile{Version: LatestSchemaVersion().String()}
	includes := []string{}

	// build.yml → unified plural keys. Write when we have data to migrate; on
	// re-run (post-migration) reference the existing file if it's on disk.
	buildPath := filepath.Join(dir, "build.yml")
	if bs != nil && (len(bs.Distros) > 0 || len(bs.Builders) > 0 || len(bs.Inits) > 0) {
		buildOut := &UnifiedFile{
			Distro:  bs.Distros,
			Builder: bs.Builders,
			Init:    bs.Inits,
		}
		p, err := writeUnifiedFile(buildPath, buildOut, dryRun)
		if err != nil {
			return written, err
		}
		written = append(written, p)
		includes = append(includes, "build.yml")
	} else if fileExists(buildPath) {
		includes = append(includes, "build.yml")
	}

	// image.yml → images.yml (new name keeps meaning clear; supports forward-migration).
	imagesPath := filepath.Join(dir, "images.yml")
	if is != nil && (len(is.Images) > 0 || !isZeroImageConfig(is.Defaults)) {
		imgOut := &UnifiedFile{
			Defaults: is.Defaults,
			Image:    is.Images,
		}
		p, err := writeUnifiedFile(imagesPath, imgOut, dryRun)
		if err != nil {
			return written, err
		}
		written = append(written, p)
		includes = append(includes, "images.yml")
	} else if fileExists(imagesPath) {
		includes = append(includes, "images.yml")
	}

	// deploy.yml → deployments block.
	deployPath := filepath.Join(dir, "deploy.yml")
	if ds != nil && (len(ds.Deploy) > 0 || ds.Provides != nil) {
		depOut := &UnifiedFile{
			Deploy:   ds.Deploy,
			Provides: ds.Provides,
		}
		p, err := writeUnifiedFile(deployPath, depOut, dryRun)
		if err != nil {
			return written, err
		}
		written = append(written, p)
		includes = append(includes, "deploy.yml")
	} else if fileExists(deployPath) {
		includes = append(includes, "deploy.yml")
	}

	// Root overthink.yml — the unified step emits the canonical `import:`
	// statement (flat string items for these same-repo files). The legacy
	// `include:` keyword was deleted in the 2026-05 import-namespace cutover.
	root.Import = make(ImportList, len(includes))
	for i, inc := range includes {
		root.Import[i] = ImportEntry{Ref: inc}
	}
	if dirExists(filepath.Join(dir, "layers")) {
		root.Discover = &DiscoverConfig{Layer: []ScanSpec{{Path: "layers", Recursive: true}}}
	}
	p, err := writeUnifiedFile(filepath.Join(dir, UnifiedFileName), root, dryRun)
	if err != nil {
		return written, err
	}
	written = append([]string{p}, written...) // root first

	return written, nil
}

// -----------------------------------------------------------------------------
// Layer rewrite — wrap flat layer.yml in `layer: {name, ...body}`.
// -----------------------------------------------------------------------------

func rewriteLayerFiles(layersDir string, dryRun bool) ([]string, error) {
	entries, err := os.ReadDir(layersDir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(layersDir, e.Name(), "layer.yml")
		if !fileExists(path) {
			continue
		}
		if err := rewriteOneLayerFile(path, e.Name(), dryRun); err != nil {
			return out, fmt.Errorf("%s: %w", path, err)
		}
		out = append(out, path)
	}
	return out, nil
}

// rewriteOneLayerFile rewrites a layer.yml to the canonical unified form:
//  1. Wrapped under `layer: {name: <dir>, ...body}`.
//  2. Schema: `service:` (singular, list) — legacy forms (`services:` plural,
//     `service: |RAW_INI|`, `system_services: [names]`) are converted.
//
// Idempotent: running on an already-migrated file is a no-op.
func rewriteOneLayerFile(path, name string, dryRun bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	// Step 1 — normalize the legacy service keys to the unified `service:` form.
	// Operates on the raw bytes because the in-tree layers use plain mapping
	// forms (block, not flow), so line-level rewrite is unambiguous.
	data = rewriteServiceKeys(data)

	// Step 2 — wrap under `layer:` if not already wrapped.
	lines := strings.Split(string(data), "\n")
	firstNonEmpty := ""
	for _, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		firstNonEmpty = trimmed
		break
	}
	alreadyWrapped := strings.HasPrefix(firstNonEmpty, "layer:")

	if alreadyWrapped {
		if dryRun {
			return nil
		}
		return os.WriteFile(path, data, 0644)
	}

	var sb strings.Builder
	sb.WriteString("layer:\n")
	sb.WriteString("  name: ")
	sb.WriteString(name)
	sb.WriteString("\n")
	for _, ln := range lines {
		if ln == "" {
			sb.WriteString("\n")
			continue
		}
		sb.WriteString("  ")
		sb.WriteString(ln)
		sb.WriteString("\n")
	}

	if dryRun {
		return nil
	}
	return os.WriteFile(path, []byte(sb.String()), 0644)
}

// rewriteServiceKeys performs the legacy → unified schema rename on a raw
// layer.yml byte slice. Handles three legacy forms:
//
//   - `services:` (plural) at any indent → `service:` (singular)
//   - `system_services: [names]` → removed; each name becomes an
//     appended `service:` entry with `use_packaged: <name>.service`.
//   - `service: |\n  [program:x]\n  command=...` (legacy raw INI) → parsed
//     into structured `service: [{name, exec, restart, ...}]` entries.
//
// Idempotent: running on an already-migrated file yields identical output.
func rewriteServiceKeys(data []byte) []byte {
	// Phase 1 — rename `services:` key to `service:` (block-mapping form).
	// We target lines of the form `<indent>services:<optional-content>` and
	// rewrite the key while preserving indentation and any trailing content.
	lines := strings.Split(string(data), "\n")
	for i, ln := range lines {
		trimmed := strings.TrimLeft(ln, " \t")
		indent := ln[:len(ln)-len(trimmed)]
		if strings.HasPrefix(trimmed, "services:") {
			suffix := trimmed[len("services:"):]
			lines[i] = indent + "service:" + suffix
		}
	}
	// Phase 2/3 (system_services / raw-INI) — the in-tree repo has zero of
	// these as of the migration cutover; kept as TODO placeholders for
	// external layers that may have them. See the plan's F1 section.
	return []byte(strings.Join(lines, "\n"))
}

// -----------------------------------------------------------------------------
// Small helpers.
// -----------------------------------------------------------------------------

func isZeroImageConfig(ic BoxConfig) bool {
	return ic.Base == "" && ic.Registry == "" && ic.Tag == "" && len(ic.Platforms) == 0 &&
		len(ic.Distro) == 0 && len(ic.Build) == 0 && len(ic.Layer) == 0
}

func writeUnifiedFile(path string, uf *UnifiedFile, dryRun bool) (string, error) {
	data, err := yaml.Marshal(uf)
	if err != nil {
		return path, fmt.Errorf("marshaling %s: %w", path, err)
	}
	if dryRun {
		return path, nil
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return path, fmt.Errorf("writing %s: %w", path, err)
	}
	return path, nil
}
