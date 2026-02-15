package main

import (
	"github.com/alecthomas/kong"
)

// CLI defines the command-line interface structure
type CLI struct {
	Generate GenerateCmd `cmd:"" help:"Write .build/ (Containerfiles + HCL)"`
	Validate ValidateCmd `cmd:"" help:"Check build.json + layers, exit 0 or 1"`
	Inspect  InspectCmd  `cmd:"" help:"Print resolved config for an image (JSON)"`
	List     ListCmd     `cmd:"" help:"List components"`
	New      NewCmd      `cmd:"" help:"Scaffold new components"`
	Version  VersionCmd  `cmd:"" help:"Print computed CalVer tag"`
}

// GenerateCmd generates Containerfiles and docker-bake.hcl
type GenerateCmd struct {
	Tag string `long:"tag" help:"Override tag (default: CalVer)"`
}

func (c *GenerateCmd) Run() error {
	// TODO: implement
	return nil
}

// ValidateCmd validates build.json and layers
type ValidateCmd struct{}

func (c *ValidateCmd) Run() error {
	// TODO: implement
	return nil
}

// InspectCmd prints resolved config for an image
type InspectCmd struct {
	Image  string `arg:"" help:"Image name"`
	Format string `long:"format" help:"Output a single field instead of full JSON"`
}

func (c *InspectCmd) Run() error {
	// TODO: implement
	return nil
}

// ListCmd groups list subcommands
type ListCmd struct {
	Images   ListImagesCmd   `cmd:"" help:"Images from build.json"`
	Layers   ListLayersCmd   `cmd:"" help:"Layers from filesystem"`
	Targets  ListTargetsCmd  `cmd:"" help:"Bake targets from generated HCL"`
	Services ListServicesCmd `cmd:"" help:"Layers with supervisord.conf"`
}

// ListImagesCmd lists images from build.json
type ListImagesCmd struct{}

func (c *ListImagesCmd) Run() error {
	// TODO: implement
	return nil
}

// ListLayersCmd lists layers from filesystem
type ListLayersCmd struct{}

func (c *ListLayersCmd) Run() error {
	// TODO: implement
	return nil
}

// ListTargetsCmd lists bake targets
type ListTargetsCmd struct{}

func (c *ListTargetsCmd) Run() error {
	// TODO: implement
	return nil
}

// ListServicesCmd lists layers with supervisord.conf
type ListServicesCmd struct{}

func (c *ListServicesCmd) Run() error {
	// TODO: implement
	return nil
}

// NewCmd groups scaffolding subcommands
type NewCmd struct {
	Layer NewLayerCmd `cmd:"" help:"Scaffold a layer directory"`
}

// NewLayerCmd scaffolds a new layer
type NewLayerCmd struct {
	Name string `arg:"" help:"Layer name"`
}

func (c *NewLayerCmd) Run() error {
	// TODO: implement
	return nil
}

// VersionCmd prints the computed CalVer tag
type VersionCmd struct{}

func (c *VersionCmd) Run() error {
	version := ComputeCalVer()
	println(version)
	return nil
}

func main() {
	var cli CLI
	ctx := kong.Parse(&cli,
		kong.Name("ov"),
		kong.Description("Overthink build system - composable container images"),
		kong.UsageOnError(),
	)
	err := ctx.Run()
	ctx.FatalIfErrorf(err)
}
