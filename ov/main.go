package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/alecthomas/kong"
)

// CLI defines the command-line interface structure
type CLI struct {
	// Host enables "run this command on a remote machine" semantics.
	// When set, ov re-execs itself over SSH on the target host:
	//
	//   ov --host o.atrawog.org status        # runs `ov status` on o.atrawog.org
	//   ov --host o vm list                   # alias lookup via `ov settings set hosts.o …`
	//
	// Commands marked LocalOnly (settings, version, ssh tunnel) are
	// not re-execed — they always run on the local machine. See
	// ov/host_exec.go for the exec dispatch.
	Host string `long:"host" env:"OV_HOST" help:"Remote host (alias or user@host[:port]) to run this command on via SSH"`

	// Dir is the project directory that every build-mode command resolves
	// image.yml / layers/ / build.yml relative to. Default is the process
	// cwd. Useful for MCP servers and remote agents that run outside a
	// project checkout — set OV_PROJECT_DIR or pass -C / --dir to point at
	// a mounted project root. Build-mode commands call os.Getwd()
	// unconditionally; when this flag is set, main() chdirs before Kong's
	// ctx.Run() so every existing call site picks up the change.
	Dir string `short:"C" long:"dir" env:"OV_PROJECT_DIR" help:"Project directory containing image.yml (default: cwd)" type:"path"`

	// Repo points ov at a remote git repo as the project source instead
	// of cwd / --dir. Spec is OWNER/REPO[@REF] (auto-prefixed with
	// github.com/) or HOST/OWNER/REPO[@REF]. The literal "default" expands
	// to overthinkos/overthink. main() resolves this to a local cache path
	// (~/.cache/ov/repos/...) and falls through into the existing --dir
	// chdir block, so every os.Getwd() site Just Works. Mutually exclusive
	// with --dir.
	Repo string `long:"repo" env:"OV_PROJECT_REPO" placeholder:"OWNER/REPO[@REF]" help:"Read image.yml from a remote git repo (e.g. overthinkos/overthink). Use 'default' for overthinkos/overthink."`

	Alias       AliasCmd        `cmd:"" help:"Manage command aliases for container images"`
	Clean       CleanCmd        `cmd:"" help:"Prune reusable build artifacts to defaults: retention (images, eval runs) + sweep one-time makepkg leftovers"`
	Cmd         CmdCmd          `cmd:"" help:"Run a command in a running container (with notification)"`
	Config      ImageConfigCmd  `cmd:"" help:"Configure image deployment (setup, secrets, encrypted volumes)"`
	Deploy      DeployCmd       `cmd:"" help:"Manage deploy.yml deployment overrides"`
	Doctor      DoctorCmd       `cmd:"" help:"Show host dependency status"`
	Image       ImageCmd        `cmd:"" help:"Build, generate, inspect, and pull container images (reads image.yml)"`
	Layer       LayerCmd        `cmd:"" help:"Edit layer.yml files in the project's layers/ directory"`
	Logs        LogsCmd         `cmd:"" help:"Show service container logs"`
	Mcp         McpCmdGroup     `cmd:"" help:"Run an MCP server exposing the ov CLI as tools"`
	Migrate     MigrateCmd      `cmd:"" help:"Migrate any overthink config up to the latest schema CalVer (single idempotent chain — no sub-verbs)"`
	Preempt     PreemptCmd      `cmd:"" help:"Inspect and recover exclusive-resource preemption leases (preemptible holders stopped to free a resource for a claimant)"`
	ReapOrphans ReapOrphansCmd  `cmd:"reap-orphans" help:"Find ephemeral deployments whose underlying resource is gone and clean them up"`
	Remove      RemoveCmd       `cmd:"" help:"Remove service container"`
	Restart     RestartCmd      `cmd:"" help:"Restart a service container atomically (systemctl --user restart)"`
	Secrets     SecretsCmdGroup `cmd:"" help:"Manage credentials (Secret Service / config) and GPG-encrypted .secrets files"`
	Service     ServiceCmd      `cmd:"" help:"Manage supervisord services inside a running container"`
	Settings    SettingsCmd     `cmd:"" help:"Manage runtime configuration (get/set/list)"`
	Shell       ShellCmd        `cmd:"" help:"Start a bash shell in a container image"`
	Ssh         SshCmd          `cmd:"" help:"SSH helpers (tunnel SPICE/VNC/unix sockets from a remote libvirt host to the local machine)"`
	Start       StartCmd        `cmd:"" help:"Start a container as a background service"`
	Status      StatusCmd       `cmd:"" help:"Show service status (all if no image given)"`
	Stop        StopCmd         `cmd:"" help:"Stop a running service container"`
	Eval        EvalCmd         `cmd:"" help:"Evaluate images and deployments — pure-image (disposable), live (running deployment), AI-driven iteration, and live-container probe verbs (cdp/wl/dbus/vnc/mcp/spice/libvirt/record/k8s)"`
	Feature     FeatureCmd      `cmd:"" help:"Gherkin-shaped description authoring: list/pending/validate"`
	Tmux        TmuxCmd         `cmd:"" help:"Manage tmux sessions inside running containers"`
	Udev        UdevCmd         `cmd:"" help:"Manage udev rules for GPU device access in containers"`
	Update      UpdateCmd       `cmd:"" help:"Update image and restart if active"`
	Version     VersionCmd      `cmd:"" help:"Print computed CalVer tag"`
	Vm          VmCmd           `cmd:"" help:"Manage virtual machines from bootc images"`
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

	gen, err := NewGenerator(dir, c.Tag, ResolveOpts{})
	if err != nil {
		return err
	}

	return gen.Generate()
}

// ValidateCmd validates image.yml and layers
type ValidateCmd struct {
	IncludeDisabled bool `long:"include-disabled" help:"Include images with enabled: false in validation (does not modify image.yml)"`
}

func (c *ValidateCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		return err
	}

	// Load default build config for SetFormatNames + init detection before layer scanning.
	var defaultInitCfg *InitConfig
	{
		distroCfg, _, initCfg, err := LoadDefaultBuildConfig(dir)
		if err != nil {
			return fmt.Errorf("loading default build config: %w", err)
		}
		SetFormatNames(distroCfg)
		defaultInitCfg = initCfg
	}

	layers, err := ScanAllLayerWithConfig(dir, cfg)
	if err != nil {
		return err
	}

	// Populate init systems on layers from build.yml config
	PopulateLayerInitSystem(layers, defaultInitCfg)

	return Validate(cfg, layers, dir, ResolveOpts{IncludeDisabled: c.IncludeDisabled})
}

// InspectCmd prints resolved config for an image
type InspectCmd struct {
	Image           string `arg:"" help:"Image name"`
	Format          string `long:"format" help:"Output a single field instead of full JSON"`
	Instance        string `short:"i" long:"instance" help:"Instance name"`
	IncludeDisabled bool   `long:"include-disabled" help:"Operate on images with enabled: false (does not modify image.yml)"`
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
	resolved, err := cfg.ResolveImage(c.Image, calverTag, dir, ResolveOpts{IncludeDisabled: c.IncludeDisabled})
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
			for _, l := range resolved.Layer {
				fmt.Println(l)
			}
		case "ports":
			for _, p := range resolved.Port {
				fmt.Println(p)
			}
		case "volumes":
			layers, err := ScanAllLayerWithConfig(dir, cfg)
			if err != nil {
				return err
			}
			volumes, err := CollectImageVolume(cfg, layers, c.Image, resolved.Home, nil)
			if err != nil {
				return err
			}
			for _, vol := range volumes {
				fmt.Printf("%s\t%s\n", vol.VolumeName, vol.ContainerPath)
			}
		case "aliases":
			layers, err := ScanAllLayerWithConfig(dir, cfg)
			if err != nil {
				return err
			}
			aliases, err := CollectImageAlias(cfg, layers, c.Image)
			if err != nil {
				return err
			}
			for _, a := range aliases {
				fmt.Printf("%s\t%s\n", a.Name, a.Command)
			}
		case "tunnel":
			// Schema v4: Tunnel moved off ImageConfig/ResolvedImage —
			// deploy-only. Resolve from DeploymentNode.Tunnel via deploy.yml.
			if overlay, ok := loadDeployConfigForRead("ov image inspect tunnel").Lookup(c.Image, c.Instance); ok && overlay.Tunnel != nil {
				layers, err := ScanAllLayerWithConfig(dir, cfg)
				if err == nil {
					portProtos := make(map[int]string)
					tc := ResolveTunnelConfig(overlay.Tunnel, c.Image, "", layers, resolved.Layer, portProtos, resolved.Port)
					if tc != nil && len(tc.Ports) > 0 {
						fmt.Println("PORT\tACCESS\tPROTOCOL\tHOSTNAME")
						for _, tp := range tc.Ports {
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
				}
			}
		case "network":
			fmt.Println(resolved.Network)
		case "engine":
			layers, err := ScanAllLayerWithConfig(dir, cfg)
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
			if overlay, ok := loadDeployConfigForRead("ov image inspect bind_mounts").Lookup(c.Image, c.Instance); ok {
				for _, dv := range overlay.Volume {
					fmt.Printf("%s\t%s\t%s\t%s\n", dv.Name, dv.Host, dv.Path, dv.Type)
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
			layers, err := ScanAllLayerWithConfig(dir, cfg)
			if err != nil {
				return err
			}
			set := CollectEval(cfg, layers, c.Image)
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
		img := cfg.Image[name]
		status := descriptionStatus(img.Description)
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

	layers, err := ScanAllLayerWithConfig(dir, cfg)
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
	images, err := cfg.ResolveAllImage(calverTag, dir, ResolveOpts{})
	if err != nil {
		return err
	}

	layers, err := ScanAllLayerWithConfig(dir, cfg)
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

	layers, err := ScanAllLayerWithConfig(dir, cfg)
	if err != nil {
		return err
	}

	services := InitLayer(layers)
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

	layers, err := ScanAllLayerWithConfig(dir, cfg)
	if err != nil {
		return err
	}

	routes := RouteLayer(layers)
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

	layers, err := ScanAllLayerWithConfig(dir, cfg)
	if err != nil {
		return err
	}

	vols := VolumeLayer(layers)
	// Sort by name for deterministic output
	names := make([]string, 0, len(vols))
	for _, layer := range vols {
		names = append(names, layer.Name)
	}
	sortStrings(names)

	for _, name := range names {
		layer := layers[name]
		for _, vol := range layer.Volume() {
			fmt.Printf("%s\t%s\t%s\n", name, vol.Name, vol.Path)
		}
	}
	return nil
}

// NewCmd groups scaffolding subcommands
type NewCmd struct {
	Layer   NewLayerCmd   `cmd:"" help:"Scaffold a layer directory"`
	Project NewProjectCmd `cmd:"" help:"Scaffold a fresh ov project (image.yml + build.yml ref + layers/)"`
	Image   NewImageCmd   `cmd:"" help:"Add a new image entry to image.yml"`
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
	// Fall back to GetConfigValue for dynamic keys (hosts.<alias>,
	// vnc.password.<image>) that don't appear in ListConfigValues
	// unless they're set.
	if strings.HasPrefix(c.Key, "hosts.") || strings.HasPrefix(c.Key, "vnc.password.") {
		v, err := GetConfigValue(c.Key)
		if err != nil {
			return err
		}
		fmt.Println(v)
		return nil
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
	fmt.Println(ComputeCalVer())
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

	// --host: re-exec over SSH (unless we're running a LocalOnly
	// command like `settings`, `version`, or `ssh tunnel`). Doing
	// this AFTER Kong parse ensures --help / invalid-flag cases print
	// locally; doing it BEFORE ctx.Run() ensures no local state is
	// touched when we're about to forward the command.
	if shouldReexecForHost(&cli, ctx.Command()) {
		os.Exit(ReexecOverSSH(&cli))
	}

	// Resolve --repo before --dir. Both end up driving the same chdir
	// intervention below. Mutually exclusive: --repo would race with --dir.
	if cli.Repo != "" {
		if cli.Dir != "" {
			fmt.Fprintln(os.Stderr, "ov: --repo and --dir are mutually exclusive")
			os.Exit(1)
		}
		path, err := ResolveProjectRepo(cli.Repo)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ov: cannot resolve --repo %q: %v\n", cli.Repo, err)
			os.Exit(1)
		}
		cli.Dir = path
	}

	// Honour -C / --dir / OV_PROJECT_DIR (and --repo, after the resolver
	// above) before dispatch. Chdir is the single-point intervention:
	// every build-mode command reaches project files through os.Getwd(),
	// so one chdir here propagates to all of them without touching 10+
	// call sites.
	if cli.Dir != "" {
		if err := os.Chdir(cli.Dir); err != nil {
			fmt.Fprintf(os.Stderr, "ov: cannot chdir to --dir %q: %v\n", cli.Dir, err)
			os.Exit(1)
		}
	}

	// Stale-binary guardrail: if cwd is inside an overthink source tree
	// AND the source tree has .go files newer than this binary, abort
	// with a clear error pointing at `task build:ov`. See
	// CheckBinaryFreshness for the full rationale (CLAUDE.md R9 +
	// the 2026-05-09 cuda-cudnn cache-mount incident).
	CheckBinaryFreshness(ctx.Command())

	// Cleanup hygiene: install a global signal handler so that registered
	// temp-file paths are removed on SIGTERM/SIGINT/SIGHUP, and sweep any
	// /tmp/ov-* leftovers from prior SIGKILL'd ov invocations. See
	// cleanup.go for the full design.
	InstallSignalHandler()
	SweepStaleTemps()

	err := ctx.Run()
	// `ov eval` distinguishes "the thing under test is broken" from "the
	// command/usage/infra errored" via a distinct exit code: 0 = pass,
	// 1 = command error (Kong's FatalIfErrorf default), 2 = eval checks
	// failed. See EvalFailedError / EvalCheckFailExitCode in eval_cmd.go.
	if err != nil {
		var evalFail *EvalFailedError
		if errors.As(err, &evalFail) {
			fmt.Fprintln(os.Stderr, FormatCLIError(err))
			os.Exit(EvalCheckFailExitCode)
		}
	}
	ctx.FatalIfErrorf(FormatCLIError(err))
}
