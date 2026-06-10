package main

// pkg_cmd.go — `charly box pkg`: build standalone, downloadable native package
// ARTIFACTS (.pkg.tar.zst / .rpm / .deb) for a layer's localpkg sources.
//
// This is the release-artifact counterpart of the deploy-time localpkg step:
// both build the package the SAME way — through the format's build.yml
// `local_pkg.build_template` rendered by buildLocalPkgOnHost (R3) — so there is
// ONE per-format build definition and ZERO distro-specific Go here. The command
// is format-blind: it looks up each requested format's local_pkg block in
// build.yml and the layer's per-format source dir, builds, and copies the
// produced files into the output dir.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// BoxPkgCmd builds native package artifacts for a layer's localpkg sources.
type BoxPkgCmd struct {
	Format []string `arg:"" optional:"" help:"Package formats to build (pac/rpm/deb). Default: every format the layer declares a localpkg source for."`
	Candy  string   `long:"candy" default:"charly" help:"Layer whose localpkg sources to build."`
	Out    string   `long:"out" default:"dist" help:"Output directory for the built package files."`
}

func (c *BoxPkgCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	// Load the local layers to find the requested layer's per-format localpkg
	// sources (config-driven — no hardcoded pkg/<dir> paths).
	layers, err := ScanLayer(dir)
	if err != nil {
		return fmt.Errorf("scanning layers: %w", err)
	}
	lyr := layers[c.Candy]
	if lyr == nil {
		return fmt.Errorf("layer %q not found in %s/candy", c.Candy, dir)
	}

	formats := c.Format
	if len(formats) == 0 {
		formats = lyr.LocalPkgFormats()
	}
	if len(formats) == 0 {
		return fmt.Errorf("layer %q declares no localpkg sources", c.Candy)
	}

	// Load the build config to resolve each format's local_pkg contract.
	dc, _, _, err := LoadBuildConfigForBox(dir)
	if err != nil {
		return fmt.Errorf("loading build config: %w", err)
	}

	outDir := c.Out
	if !filepath.IsAbs(outDir) {
		outDir = filepath.Join(dir, outDir)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("creating output dir %s: %w", outDir, err)
	}

	ctx := EmitOpts{}.ContextOrDefault()
	for _, format := range formats {
		src := lyr.LocalPkg(format)
		if src == "" {
			return fmt.Errorf("layer %q declares no localpkg source for format %q", c.Candy, format)
		}
		lp := lookupLocalPkgDef(dc, format)
		if lp == nil {
			return fmt.Errorf("no distro in build.yml declares a local_pkg block for format %q", format)
		}
		srcDir := resolveLocalPkgDir(src, lyr.SourceDir, dir, lp.SourceSentinel)
		if srcDir == "" {
			return fmt.Errorf("package source %q for format %q not found (sentinel %q)", src, format, lp.SourceSentinel)
		}
		fmt.Fprintf(os.Stderr, "Building %s package for layer %q from %s\n", format, c.Candy, srcDir)
		files, err := buildLocalPkgOnHost(ctx, lp, srcDir, EmitOpts{})
		if err != nil {
			return fmt.Errorf("building %s package: %w", format, err)
		}
		for _, f := range files {
			dst := filepath.Join(outDir, filepath.Base(f))
			if err := copyFileTo(f, dst); err != nil {
				return fmt.Errorf("copying %s to %s: %w", f, dst, err)
			}
			fmt.Printf("%s\n", dst)
		}
	}
	return nil
}

// lookupLocalPkgDef finds the first distro in the build config that declares a
// local_pkg block for the given package format, returning its contract. The
// per-format build/install/glob/sentinel all come from this config — the only
// distro knowledge lives in build.yml, never here.
func lookupLocalPkgDef(dc *DistroConfig, format string) *LocalPkgDef {
	if dc == nil {
		return nil
	}
	names := make([]string, 0, len(dc.Distro))
	for name := range dc.Distro {
		names = append(names, name)
	}
	sortStrings(names)
	for _, name := range names {
		if fn, lp := dc.Distro[name].LocalPkgFormat(format); lp != nil && fn == format {
			return lp
		}
	}
	return nil
}

// copyFileTo copies a file's contents (mode 0644) to dst.
func copyFileTo(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
