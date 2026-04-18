package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/alecthomas/kong"
)

// CLI defines the command-line interface structure
type CLI struct {
	Kdbx string `long:"kdbx" help:"Path to KeePass .kdbx database" type:"path"`

	Alias    AliasCmd       `cmd:"" help:"Manage command aliases for container images"`
	Cdp      CdpCmd         `cmd:"" help:"Chrome DevTools Protocol (open, list, click, eval)"`
	Cmd      CmdCmd         `cmd:"" help:"Run a command in a running container (with notification)"`
	Config   ImageConfigCmd `cmd:"" help:"Configure image deployment (setup, secrets, encrypted volumes)"`
	Dbus     DbusCmd        `cmd:"" help:"Interact with D-Bus services inside containers"`
	Deploy   DeployCmd      `cmd:"" help:"Manage deploy.yml deployment overrides"`
	Doctor   DoctorCmd      `cmd:"" help:"Show host dependency status"`
	Image    ImageCmd       `cmd:"" help:"Build, generate, inspect, and pull container images (reads image.yml)"`
	Logs     LogsCmd        `cmd:"" help:"Show service container logs"`
	Record   RecordCmd      `cmd:"" help:"Record terminal sessions or desktop video"`
	Remove   RemoveCmd      `cmd:"" help:"Remove service container"`
	Secrets  SecretsCmdGroup `cmd:"" help:"Manage credentials in KeePass (.kdbx) database"`
	Service  ServiceCmd     `cmd:"" help:"Manage supervisord services inside a running container"`
	Settings SettingsCmd    `cmd:"" help:"Manage runtime configuration (get/set/list)"`
	Shell    ShellCmd       `cmd:"" help:"Start a bash shell in a container image"`
	Start    StartCmd       `cmd:"" help:"Start a container as a background service"`
	Status   StatusCmd      `cmd:"" help:"Show service status (all if no image given)"`
	Stop     StopCmd        `cmd:"" help:"Stop a running service container"`
	Test     TestCmd        `cmd:"" help:"Run declarative tests against a running service"`
	Tmux     TmuxCmd        `cmd:"" help:"Manage tmux sessions inside running containers"`
	Udev     UdevCmd        `cmd:"" help:"Manage udev rules for GPU device access in containers"`
	Update   UpdateCmd      `cmd:"" help:"Update image and restart if active"`
	Version  VersionCmd     `cmd:"" help:"Print computed CalVer tag"`
	Vm       VmCmd          `cmd:"" help:"Manage virtual machines from bootc images"`
	Vnc      VncCmd         `cmd:"" help:"Control VNC desktop in running containers"`
	Wl       WlCmd          `cmd:"" help:"Desktop automation (input, windows, screenshots, sway IPC)"`
}

// GenerateCmd generates Containerfiles
type GenerateCmd struct {
	Tag string `long:"tag" help:"Override tag (default: CalVer)"`
}

func (c *GenerateCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	gen, err := NewGenerator(dir, c.Tag)
	if err != nil {
		return err
	}

	return gen.Generate()
}

// ValidateCmd validates image.yml and layers
type ValidateCmd struct{}

func (c *ValidateCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		return err
	}

	// Load default build config for SetFormatNames + init detection before layer scanning
	var defaultInitCfg *InitConfig
	if cfg.Defaults.FormatConfig != "" {
		distroCfg, _, initCfg, err := LoadDefaultBuildConfig(cfg.Defaults.FormatConfig, dir)
		if err != nil {
			return fmt.Errorf("loading default build config: %w", err)
		}
		SetFormatNames(distroCfg)
		defaultInitCfg = initCfg
	}

	layers, err := ScanAllLayersWithConfig(dir, cfg)
	if err != nil {
		return err
	}

	// Populate init systems on layers from build.yml config
	PopulateLayerInitSystems(layers, defaultInitCfg)

	return Validate(cfg, layers, dir)
}

// InspectCmd prints resolved config for an image
type InspectCmd struct {
	Image    string `arg:"" help:"Image name"`
	Format   string `long:"format" help:"Output a single field instead of full JSON"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *InspectCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		return err
	}
	return c.runFromConfig(cfg, dir)
}

func (c *InspectCmd) runFromConfig(cfg *Config, dir string) error {
	calverTag := ComputeCalVer()
	resolved, err := cfg.ResolveImage(c.Image, calverTag, dir)
	if err != nil {
		return err
	}

	if c.Format != "" {
		switch c.Format {
		case "tag":
			fmt.Println(resolved.FullTag)
		case "base":
			fmt.Println(resolved.Base)
		case "builder":
			for typ, builder := range resolved.Builder {
				fmt.Printf("%s: %s\n", typ, builder)
			}
		case "builds":
			for _, b := range resolved.BuilderCapabilities {
				fmt.Println(b)
			}
		case "build":
			for _, b := range resolved.BuildFormats {
				fmt.Println(b)
			}
		case "distro":
			for _, d := range resolved.Distro {
				fmt.Println(d)
			}
		case "pkg":
			fmt.Println(resolved.Pkg)
		case "registry":
			fmt.Println(resolved.Registry)
		case "platforms":
			for _, p := range resolved.Platforms {
				fmt.Println(p)
			}
		case "layers":
			for _, l := range resolved.Layers {
				fmt.Println(l)
			}
		case "ports":
			for _, p := range resolved.Ports {
				fmt.Println(p)
			}
		case "volumes":
			layers, err := ScanAllLayersWithConfig(dir, cfg)
			if err != nil {
				return err
			}
			volumes, err := CollectImageVolumes(cfg, layers, c.Image, resolved.Home, nil)
			if err != nil {
				return err
			}
			for _, vol := range volumes {
				fmt.Printf("%s\t%s\n", vol.VolumeName, vol.ContainerPath)
			}
		case "aliases":
			layers, err := ScanAllLayersWithConfig(dir, cfg)
			if err != nil {
				return err
			}
			aliases, err := CollectImageAliases(cfg, layers, c.Image)
			if err != nil {
				return err
			}
			for _, a := range aliases {
				fmt.Printf("%s\t%s\n", a.Name, a.Command)
			}
		case "tunnel":
			if resolved.Tunnel != nil && len(resolved.Tunnel.Ports) > 0 {
				fmt.Println("PORT\tACCESS\tPROTOCOL\tHOSTNAME")
				for _, tp := range resolved.Tunnel.Ports {
					access := "private"
					if tp.Public {
						access = "public"
					}
					hostname := tp.Hostname
					if hostname == "" {
						hostname = "-"
					}
					fmt.Printf("%d\t%s\t%s\t%s\n", tp.Port, access, tp.Protocol, hostname)
				}
			}
		case "network":
			fmt.Println(resolved.Network)
		case "engine":
			layers, err := ScanAllLayersWithConfig(dir, cfg)
			if err != nil {
				return err
			}
			engine := ResolveImageEngine(cfg, layers, c.Image, "")
			if engine == "" {
				engine = "(global default)"
			}
			fmt.Println(engine)
		case "bind_mounts":
			// bind_mounts are now deploy-time only; show deploy.yml volume config
			dc, _ := LoadDeployConfig()
			if dc != nil {
				if overlay, ok := dc.Images[deployKey(c.Image, c.Instance)]; ok {
					for _, dv := range overlay.Volumes {
						fmt.Printf("%s\t%s\t%s\t%s\n", dv.Name, dv.Host, dv.Path, dv.Type)
					}
				}
			}
		case "version":
			fmt.Println(resolved.Version)
		case "status":
			fmt.Println(resolveStatus(resolved.Status))
		case "info":
			fmt.Println(resolved.Info)
		case "tests":
			// Emit the effective three-section test manifest as JSON.
			// Mirrors what will land in the org.overthinkos.tests OCI label.
			layers, err := ScanAllLayersWithConfig(dir, cfg)
			if err != nil {
				return err
			}
			set := CollectTests(cfg, layers, c.Image)
			if set == nil {
				fmt.Println("{}")
				return nil
			}
			data, err := json.MarshalIndent(set, "", "  ")
			if err != nil {
				return err
			}
			fmt.Println(string(data))
		default:
			return fmt.Errorf("unknown format field: %s", c.Format)
		}
		return nil
	}

	data, err := json.MarshalIndent(resolved, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

// ListCmd groups list subcommands
type ListCmd struct {
	Aliases  ListAliasesCmd  `cmd:"" help:"List layers that declare aliases"`
	Images   ListImagesCmd   `cmd:"" help:"List images from image.yml"`
	Layers   ListLayersCmd   `cmd:"" help:"List layers from the filesystem"`
	Routes   ListRoutesCmd   `cmd:"" help:"List layers that declare a route"`
	Services ListServicesCmd `cmd:"" help:"List layers that declare a service"`
	Targets  ListTargetsCmd  `cmd:"" help:"List build targets in dependency order"`
	Volumes  ListVolumesCmd  `cmd:"" help:"List layers that declare volumes"`
}

// ListImagesCmd lists images from image.yml
type ListImagesCmd struct{}

func (c *ListImagesCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		return err
	}

	for _, name := range cfg.ImageNames() {
		img := cfg.Images[name]
		status := resolveStatus(img.Status)
		if status != "working" {
			fmt.Printf("%s [%s]\n", name, status)
		} else {
			fmt.Println(name)
		}
	}
	return nil
}

// ListLayersCmd lists layers from filesystem
type ListLayersCmd struct{}

func (c *ListLayersCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		return err
	}

	layers, err := ScanAllLayersWithConfig(dir, cfg)
	if err != nil {
		return err
	}

	for _, name := range LayerNames(layers) {
		layer := layers[name]
		status := resolveStatus(layer.Status)
		var tags []string
		if layer.Remote {
			tags = append(tags, layer.RepoPath)
		}
		if status != "working" {
			tags = append(tags, status)
		}
		if len(tags) > 0 {
			fmt.Printf("%s [%s]\n", name, strings.Join(tags, ", "))
		} else {
			fmt.Println(name)
		}
	}
	return nil
}

// ListTargetsCmd lists build targets in dependency order
type ListTargetsCmd struct{}

func (c *ListTargetsCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		return err
	}

	calverTag := ComputeCalVer()
	images, err := cfg.ResolveAllImages(calverTag, dir)
	if err != nil {
		return err
	}

	layers, err := ScanAllLayersWithConfig(dir, cfg)
	if err != nil {
		return err
	}

	// Compute intermediates to get full build order
	images, err = ComputeIntermediates(images, layers, cfg, calverTag)
	if err != nil {
		return err
	}

	order, err := ResolveImageOrder(images, layers)
	if err != nil {
		return err
	}

	for _, name := range order {
		img := images[name]
		if img.Auto {
			fmt.Printf("%s [auto]\n", name)
		} else {
			fmt.Println(name)
		}
	}
	return nil
}

// ListServicesCmd lists layers that trigger any init system
type ListServicesCmd struct{}

func (c *ListServicesCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		return err
	}

	layers, err := ScanAllLayersWithConfig(dir, cfg)
	if err != nil {
		return err
	}

	services := InitLayers(layers)
	for _, layer := range services {
		fmt.Println(layer.Name)
	}
	return nil
}

// ListRoutesCmd lists layers with route files
type ListRoutesCmd struct{}

func (c *ListRoutesCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		return err
	}

	layers, err := ScanAllLayersWithConfig(dir, cfg)
	if err != nil {
		return err
	}

	routes := RouteLayers(layers)
	// Sort by name for deterministic output
	names := make([]string, 0, len(routes))
	for _, layer := range routes {
		names = append(names, layer.Name)
	}
	sortStrings(names)

	for _, name := range names {
		layer := layers[name]
		route, err := layer.Route()
		if err != nil {
			return err
		}
		fmt.Printf("%s\thost=%s\tport=%s\n", name, route.Host, route.Port)
	}
	return nil
}

// ListVolumesCmd lists layers with volume declarations
type ListVolumesCmd struct{}

func (c *ListVolumesCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		return err
	}

	layers, err := ScanAllLayersWithConfig(dir, cfg)
	if err != nil {
		return err
	}

	vols := VolumeLayers(layers)
	// Sort by name for deterministic output
	names := make([]string, 0, len(vols))
	for _, layer := range vols {
		names = append(names, layer.Name)
	}
	sortStrings(names)

	for _, name := range names {
		layer := layers[name]
		for _, vol := range layer.Volumes() {
			fmt.Printf("%s\t%s\t%s\n", name, vol.Name, vol.Path)
		}
	}
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
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	return ScaffoldLayer(dir, c.Name)
}

// SettingsCmd groups settings subcommands (renamed from ConfigCmd to free `ov config` for image configuration).
type SettingsCmd struct {
	Get            SettingsGetCmd          `cmd:"" help:"Print resolved value for a config key"`
	List           SettingsListCmd         `cmd:"" help:"Show all settings with source"`
	MigrateSecrets ConfigMigrateSecretsCmd `cmd:"migrate-secrets" help:"Migrate plaintext credentials from config.yml to system keyring"`
	Path           SettingsPathCmd         `cmd:"" help:"Print config file path"`
	Reset          SettingsResetCmd        `cmd:"" help:"Remove a key from config (revert to default)"`
	Set            SettingsSetCmd          `cmd:"" help:"Set a config value"`
}

// SettingsGetCmd prints the resolved value for a key
type SettingsGetCmd struct {
	Key string `arg:"" help:"Config key"`
}

func (c *SettingsGetCmd) Run() error {
	// vnc.password.* keys use their own resolution path
	if strings.HasPrefix(c.Key, "vnc.password.") {
		val, err := GetConfigValue(c.Key)
		if err != nil {
			return err
		}
		fmt.Println(val)
		return nil
	}

	// For engine keys, try to resolve the actual engine (shows "podman" instead of "auto")
	switch c.Key {
	case "engine.build", "engine.run", "engine.rootful":
		rt, err := ResolveRuntime()
		if err == nil {
			switch c.Key {
			case "engine.build":
				fmt.Println(rt.BuildEngine)
			case "engine.run":
				fmt.Println(rt.RunEngine)
			case "engine.rootful":
				fmt.Println(rt.Rootful)
			}
			return nil
		}
		// Fall through to ListConfigValues if engine detection fails
	case "secret_backend":
		// Show the resolved backend, not just the config value
		store := DefaultCredentialStore()
		fmt.Println(store.Name())
		return nil
	}

	// All keys: use ListConfigValues (no engine detection needed)
	vals, err := ListConfigValues()
	if err != nil {
		return err
	}
	for _, v := range vals {
		if v.Key == c.Key {
			fmt.Println(v.Value)
			return nil
		}
	}
	return fmt.Errorf("unknown config key %q (run 'ov settings list' to see valid keys)", c.Key)
}

// SettingsSetCmd sets a config value
type SettingsSetCmd struct {
	Key   string `arg:"" help:"Config key"`
	Value string `arg:"" help:"Config value"`
}

func (c *SettingsSetCmd) Run() error {
	if err := SetConfigValue(c.Key, c.Value); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Set %s = %s\n", c.Key, c.Value)
	return nil
}

// SettingsListCmd shows all settings
type SettingsListCmd struct{}

func (c *SettingsListCmd) Run() error {
	vals, err := ListConfigValues()
	if err != nil {
		return err
	}
	for _, v := range vals {
		fmt.Printf("%-15s %-10s (%s)\n", v.Key, v.Value, v.Source)
	}
	return nil
}

// SettingsResetCmd removes a key from config
type SettingsResetCmd struct {
	Key string `arg:"" optional:"" help:"Config key to reset (omit to reset all)"`
}

func (c *SettingsResetCmd) Run() error {
	if err := ResetConfigValue(c.Key); err != nil {
		return err
	}
	if c.Key == "" {
		fmt.Fprintln(os.Stderr, "Reset all config to defaults")
	} else {
		fmt.Fprintf(os.Stderr, "Reset %s to default\n", c.Key)
	}
	return nil
}

// SettingsPathCmd prints the config file path
type SettingsPathCmd struct{}

func (c *SettingsPathCmd) Run() error {
	path, err := RuntimeConfigPath()
	if err != nil {
		return err
	}
	fmt.Println(path)
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
	// Load project .env into process environment before any config resolution.
	// Real env vars take precedence over .env values.
	if dir, err := os.Getwd(); err == nil {
		if err := LoadProcessDotenv(dir); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: loading .env: %v\n", err)
		}
	}

	var cli CLI
	ctx := kong.Parse(&cli,
		kong.Name("ov"),
		kong.Description("Overthink - the container management experience for you and your AI"),
		kong.UsageOnError(),
	)

	// Set global --kdbx flag into env so resolveKdbxPaths() picks it up everywhere
	if cli.Kdbx != "" {
		os.Setenv("OV_KDBX_PATH", cli.Kdbx)
	}

	err := ctx.Run()
	ctx.FatalIfErrorf(FormatCLIError(err))
}
