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
	// When set, charly re-execs itself over SSH on the target host:
	//
	//   charly --host o.atrawog.org status        # runs `charly status` on o.atrawog.org
	//   charly --host o vm list                   # alias lookup via `charly settings set hosts.o …`
	//
	// Commands marked LocalOnly (settings, version, ssh tunnel) are
	// not re-execed — they always run on the local machine. See
	// charly/host_exec.go for the exec dispatch.
	Host string `long:"host" env:"CHARLY_HOST" help:"Remote host (alias or user@host[:port]) to run this command on via SSH"`

	// Dir is the project directory that every build-mode command resolves
	// charly.yml / candy/ / build.yml relative to. Default is the process
	// cwd. Useful for MCP servers and remote agents that run outside a
	// project checkout — set CHARLY_PROJECT_DIR or pass -C / --dir to point at
	// a mounted project root. Build-mode commands call os.Getwd()
	// unconditionally; when this flag is set, main() chdirs before Kong's
	// ctx.Run() so every existing call site picks up the change.
	Dir string `short:"C" long:"dir" env:"CHARLY_PROJECT_DIR" help:"Project directory containing charly.yml (default: cwd)" type:"path"`

	// Repo points charly at a remote git repo as the project source instead
	// of cwd / --dir. Spec is OWNER/REPO[@REF] (auto-prefixed with
	// github.com/) or HOST/OWNER/REPO[@REF]. The literal "default" expands
	// to overthinkos/overthink. main() resolves this to a local cache path
	// (~/.cache/charly/repos/...) and falls through into the existing --dir
	// chdir block, so every os.Getwd() site Just Works. Mutually exclusive
	// with --dir.
	Repo string `long:"repo" env:"CHARLY_PROJECT_REPO" placeholder:"OWNER/REPO[@REF]" help:"Read charly.yml from a remote git repo (e.g. overthinkos/overthink). Use 'default' for overthinkos/overthink."`

	Alias       AliasCmd        `cmd:"" help:"Manage command aliases for container images"`
	Clean       CleanCmd        `cmd:"" help:"Prune reusable build artifacts to defaults: retention (images, check runs) + sweep one-time makepkg leftovers"`
	Cmd         CmdCmd          `cmd:"" help:"Run a command in a running container (with notification)"`
	Config      BoxConfigCmd    `cmd:"" help:"Configure box deployment (setup, secrets, encrypted volumes)"`
	Deploy      DeployCmd       `cmd:"" help:"Manage charly.yml deployment overrides"`
	Doctor      DoctorCmd       `cmd:"" help:"Show host dependency status"`
	Box         BoxCmd          `cmd:"" name:"box" help:"Build, generate, inspect, and pull container boxes (reads charly.yml)"`
	Candy       CandyCmd        `cmd:"" name:"candy" help:"Edit candy.yml files in the project's candy/ directory"`
	Logs        LogsCmd         `cmd:"" help:"Show service container logs"`
	Volume      VolumeCmd       `cmd:"" name:"volume" help:"List or reset a deployment's charly-managed named volumes"`
	Cp          CpCmd           `cmd:"" name:"cp" help:"Copy a file between the host and a running container (':' prefix marks the container side)"`
	Mcp         McpCmdGroup     `cmd:"" help:"Run an MCP server exposing the charly CLI as tools"`
	Migrate     MigrateCmd      `cmd:"" help:"Migrate any opencharly config up to the latest schema CalVer (single idempotent chain — no sub-verbs)"`
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
	Status      StatusCmd       `cmd:"" help:"Show service status (all if no box given)"`
	Stop        StopCmd         `cmd:"" help:"Stop a running service container"`
	Check       CheckCmd        `cmd:"" help:"Evaluate boxes and deployments — pure-box (disposable), live (running deployment), AI-driven iteration, and live-container probe verbs (cdp/wl/dbus/vnc/mcp/spice/libvirt/record/k8s)"`
	Feature     FeatureCmd      `cmd:"" help:"plan-shaped description authoring: list/pending/validate"`
	Tmux        TmuxCmd         `cmd:"" help:"Manage tmux sessions inside running containers"`
	Udev        UdevCmd         `cmd:"" help:"Manage udev rules for GPU device access in containers"`
	Update      UpdateCmd       `cmd:"" help:"Update box and restart if active"`
	Version     VersionCmd      `cmd:"" help:"Print computed CalVer tag"`
	Vm          VmCmd           `cmd:"" help:"Manage virtual machines from bootc images"`
}

// GenerateCmd generates Containerfiles
type GenerateCmd struct {
	Boxes           []string `arg:"" optional:"" help:"Boxes to generate (default: all enabled). The sentinel 'all' is equivalent to passing no argument."`
	Tag             string   `long:"tag" help:"Override tag (default: CalVer)"`
	IncludeDisabled bool     `long:"include-disabled" help:"Generate boxes with enabled: false in charly.yml (does not modify the file). Scoped to the named boxes when any are given."`
}

func (c *GenerateCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	// Share the box-selection rule with `charly box build`: the `all` sentinel
	// collapses to "every enabled box", and a named selection scopes the
	// resolved set (and, with --include-disabled, relaxes the gate for exactly
	// those names).
	boxes := normalizeBoxArgs(c.Boxes)
	gen, err := NewGenerator(dir, c.Tag, boxResolveOpts(boxes, c.IncludeDisabled))
	if err != nil {
		return err
	}

	// Serialize against any concurrent generate/build sharing this dir's
	// .build/_layers staging tree (see acquireBuildLock).
	buildUnlock, err := acquireBuildLock(gen.BuildDir)
	if err != nil {
		return fmt.Errorf("acquiring build lock: %w", err)
	}
	defer func() { _ = buildUnlock() }()

	return gen.Generate()
}

// ValidateCmd validates charly.yml and candies
type ValidateCmd struct {
	IncludeDisabled bool `long:"include-disabled" help:"Include boxes with enabled: false in validation (does not modify charly.yml)"`
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

	// Load default build config for RegisterBuildVocabulary + init detection before candy scanning.
	var defaultInitCfg *InitConfig
	{
		distroCfg, _, initCfg, err := LoadDefaultBuildConfig(dir)
		if err != nil {
			return fmt.Errorf("loading default build config: %w", err)
		}
		RegisterBuildVocabulary(distroCfg)
		defaultInitCfg = initCfg
	}

	layers, err := ScanAllCandyWithConfig(dir, cfg)
	if err != nil {
		return err
	}

	// Populate init systems on candies from build.yml config
	PopulateCandyInitSystem(layers, defaultInitCfg)

	return Validate(cfg, layers, dir, ResolveOpts{IncludeDisabled: c.IncludeDisabled})
}

// InspectCmd prints resolved config for an image
type InspectCmd struct {
	Box             string `arg:"" help:"Box name"`
	Format          string `long:"format" help:"Output a single field instead of full JSON"`
	Instance        string `short:"i" long:"instance" help:"Instance name"`
	IncludeDisabled bool   `long:"include-disabled" help:"Operate on boxes with enabled: false (does not modify charly.yml)"`
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
	resolved, err := cfg.ResolveBox(c.Box, calverTag, dir, ResolveOpts{IncludeDisabled: c.IncludeDisabled})
	if err != nil {
		return err
	}

	if c.Format != "" {
		switch c.Format {
		case "tag":
			formatTag(resolved)
		case "base":
			formatBase(resolved)
		case "builder":
			formatBuilder(resolved)
		case "builds":
			formatBuilds(resolved)
		case "build":
			formatBuild(resolved)
		case "distro":
			formatDistro(resolved)
		case "pkg":
			formatPkg(resolved)
		case "registry":
			formatRegistry(resolved)
		case "platforms":
			formatPlatforms(resolved)
		case "candy":
			formatCandy(resolved)
		case "ports":
			return c.formatPorts(cfg, dir)
		case "volumes":
			return c.formatVolumes(cfg, dir, resolved)
		case "aliases":
			return c.formatAliases(cfg, dir)
		case "tunnel":
			c.formatTunnel(cfg, dir, resolved)
		case "network":
			fmt.Println(resolved.Network)
		case "engine":
			layers, err := ScanAllCandyWithConfig(dir, cfg)
			if err != nil {
				return err
			}
			engine := ResolveBoxEngine(cfg, layers, c.Box, "")
			if engine == "" {
				engine = "(global default)"
			}
			fmt.Println(engine)
		case "bind_mounts":
			// bind_mounts are now deploy-time only; show charly.yml volume config
			if overlay, ok := loadDeployConfigForRead("charly box inspect bind_mounts").Lookup(c.Box, c.Instance); ok {
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

// formatTag prints the resolved box's full tag.
func formatTag(resolved *ResolvedBox) { fmt.Println(resolved.FullTag) }

// formatBase prints the resolved box's base image.
func formatBase(resolved *ResolvedBox) { fmt.Println(resolved.Base) }

// formatBuilder prints the resolved builder map (type: image).
func formatBuilder(resolved *ResolvedBox) {
	for typ, builder := range resolved.Builder {
		fmt.Printf("%s: %s\n", typ, builder)
	}
}

// formatBuilds prints the resolved builder capabilities.
func formatBuilds(resolved *ResolvedBox) {
	for _, b := range resolved.BuilderCapabilities {
		fmt.Println(b)
	}
}

// formatBuild prints the resolved build formats.
func formatBuild(resolved *ResolvedBox) {
	for _, b := range resolved.BuildFormats {
		fmt.Println(b)
	}
}

// formatDistro prints the resolved distro chain.
func formatDistro(resolved *ResolvedBox) {
	for _, d := range resolved.Distro {
		fmt.Println(d)
	}
}

// formatPkg prints the resolved package manager.
func formatPkg(resolved *ResolvedBox) { fmt.Println(resolved.Pkg) }

// formatRegistry prints the resolved registry.
func formatRegistry(resolved *ResolvedBox) { fmt.Println(resolved.Registry) }

// formatPlatforms prints the resolved platforms.
func formatPlatforms(resolved *ResolvedBox) {
	for _, p := range resolved.Platforms {
		fmt.Println(p)
	}
}

// formatCandy prints the resolved candy list.
func formatCandy(resolved *ResolvedBox) {
	for _, l := range resolved.Candy {
		fmt.Println(l)
	}
}

// formatPorts prints the collected box ports.
func (c *InspectCmd) formatPorts(cfg *Config, dir string) error {
	layers, err := ScanAllCandyWithConfig(dir, cfg)
	if err != nil {
		return err
	}
	ports, err := CollectBoxPorts(cfg, layers, c.Box)
	if err != nil {
		return err
	}
	for _, p := range ports {
		fmt.Println(p)
	}
	return nil
}

// formatVolumes prints the collected box volumes.
func (c *InspectCmd) formatVolumes(cfg *Config, dir string, resolved *ResolvedBox) error {
	layers, err := ScanAllCandyWithConfig(dir, cfg)
	if err != nil {
		return err
	}
	volumes, err := CollectBoxVolume(cfg, layers, c.Box, resolved.Home, nil)
	if err != nil {
		return err
	}
	for _, vol := range volumes {
		fmt.Printf("%s\t%s\n", vol.VolumeName, vol.ContainerPath)
	}
	return nil
}

// formatAliases prints the collected box aliases.
func (c *InspectCmd) formatAliases(cfg *Config, dir string) error {
	layers, err := ScanAllCandyWithConfig(dir, cfg)
	if err != nil {
		return err
	}
	aliases, err := CollectBoxAlias(cfg, layers, c.Box)
	if err != nil {
		return err
	}
	for _, a := range aliases {
		fmt.Printf("%s\t%s\n", a.Name, a.Command)
	}
	return nil
}

// formatTunnel prints the deploy-time tunnel config for the box. Schema v4:
// Tunnel moved off BoxConfig/ResolvedBox — deploy-only. Resolve from
// DeploymentNode.Tunnel via charly.yml. Any resolution failure is silently
// skipped (no tunnel output), matching the prior inline behaviour.
func (c *InspectCmd) formatTunnel(cfg *Config, dir string, resolved *ResolvedBox) {
	overlay, ok := loadDeployConfigForRead("charly box inspect tunnel").Lookup(c.Box, c.Instance)
	if !ok || overlay.Tunnel == nil {
		return
	}
	layers, err := ScanAllCandyWithConfig(dir, cfg)
	if err != nil {
		return
	}
	portProtos := make(map[int]string)
	boxPorts, _ := CollectBoxPorts(cfg, layers, c.Box)
	tc := ResolveTunnelConfig(overlay.Tunnel, c.Box, "", layers, resolved.Candy, portProtos, boxPorts)
	if tc == nil || len(tc.Ports) == 0 {
		return
	}
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

// ListCmd groups list subcommands
type ListCmd struct {
	Aliases  ListAliasesCmd  `cmd:"" help:"List candies that declare aliases"`
	Boxes    ListBoxesCmd    `cmd:"" name:"boxes" help:"List boxes from charly.yml"`
	Candies  ListCandiesCmd  `cmd:"" name:"candies" help:"List candies from the filesystem"`
	Routes   ListRoutesCmd   `cmd:"" help:"List candies that declare a route"`
	Services ListServicesCmd `cmd:"" help:"List candies that declare a service"`
	Targets  ListTargetsCmd  `cmd:"" help:"List build targets in dependency order"`
	Volumes  ListVolumesCmd  `cmd:"" help:"List candies that declare volumes"`
	Tags     ListTagsCmd     `cmd:"" help:"List locally stored CalVer tags of charly-built images, newest first (rollback discovery for charly update --tag)"`
}

// ListBoxesCmd lists boxes from charly.yml
type ListBoxesCmd struct{}

func (c *ListBoxesCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		return err
	}

	for _, name := range cfg.BoxNames() {
		_ = cfg.Box[name]
		// Boxes author no status; the effective rung (worst of the candy chain)
		// is computed for the ai.opencharly.status label at generate time.
		status := resolveStatus("")
		if status != "working" {
			fmt.Printf("%s [%s]\n", name, status)
		} else {
			fmt.Println(name)
		}
	}
	return nil
}

// ListCandiesCmd lists candies from filesystem
type ListCandiesCmd struct{}

func (c *ListCandiesCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		return err
	}

	layers, err := ScanAllCandyWithConfig(dir, cfg)
	if err != nil {
		return err
	}

	for _, name := range CandyNames(layers) {
		layer := layers[name]
		status := candyStatus(layer)
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
	images, err := cfg.ResolveAllBox(calverTag, dir, ResolveOpts{})
	if err != nil {
		return err
	}

	layers, err := ScanAllCandyWithConfig(dir, cfg)
	if err != nil {
		return err
	}

	// Compute intermediates to get full build order
	images, err = ComputeIntermediates(images, layers, cfg, calverTag)
	if err != nil {
		return err
	}

	order, err := ResolveBoxOrder(images, layers)
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

// ListServicesCmd lists candies that trigger any init system
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

	layers, err := ScanAllCandyWithConfig(dir, cfg)
	if err != nil {
		return err
	}

	services := InitCandy(layers)
	for _, layer := range services {
		fmt.Println(layer.Name)
	}
	return nil
}

// ListRoutesCmd lists candies with route files
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

	layers, err := ScanAllCandyWithConfig(dir, cfg)
	if err != nil {
		return err
	}

	routes := RouteCandy(layers)
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

// ListVolumesCmd lists candies with volume declarations
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

	layers, err := ScanAllCandyWithConfig(dir, cfg)
	if err != nil {
		return err
	}

	vols := VolumeCandy(layers)
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
	Candy   NewCandyCmd   `cmd:"" name:"candy" help:"Scaffold a candy directory"`
	Project NewProjectCmd `cmd:"" help:"Scaffold a fresh charly project (charly.yml + build.yml ref + candy/)"`
	Box     NewBoxCmd     `cmd:"" name:"box" help:"Add a new box entry to charly.yml"`
}

// NewCandyCmd scaffolds a new candy
type NewCandyCmd struct {
	Name string `arg:"" help:"Candy name"`
}

func (c *NewCandyCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	return ScaffoldCandy(dir, c.Name)
}

// SettingsCmd groups settings subcommands (renamed from ConfigCmd to free `charly config` for image configuration).
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
	return fmt.Errorf("unknown config key %q (run 'charly settings list' to see valid keys)", c.Key)
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

func (c *VersionCmd) Run() error { //nolint:unparam // error return kept for interface/API stability
	// The BINARY's identity (stamped at build time), NOT the wall clock.
	fmt.Println(CharlyVersion())
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
		kong.Name("charly"),
		kong.Description("OpenCharly - the container management experience for you and your agents"),
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
			fmt.Fprintln(os.Stderr, "charly: --repo and --dir are mutually exclusive")
			os.Exit(1)
		}
		path, err := ResolveProjectRepo(cli.Repo)
		if err != nil {
			fmt.Fprintf(os.Stderr, "charly: cannot resolve --repo %q: %v\n", cli.Repo, err)
			os.Exit(1)
		}
		cli.Dir = path
	}

	// Honour -C / --dir / CHARLY_PROJECT_DIR (and --repo, after the resolver
	// above) before dispatch. Chdir is the single-point intervention:
	// every build-mode command reaches project files through os.Getwd(),
	// so one chdir here propagates to all of them without touching 10+
	// call sites.
	if cli.Dir != "" {
		if err := os.Chdir(cli.Dir); err != nil {
			fmt.Fprintf(os.Stderr, "charly: cannot chdir to --dir %q: %v\n", cli.Dir, err)
			os.Exit(1)
		}
	}

	// Stale-binary guardrail: if cwd is inside an opencharly source tree
	// AND the source tree has .go files newer than this binary, abort
	// with a clear error pointing at `task build:charly`. See
	// CheckBinaryFreshness for the full rationale (CLAUDE.md R9 +
	// the 2026-05-09 cuda-cudnn cache-mount incident).
	CheckBinaryFreshness(ctx.Command())

	// Cleanup hygiene: install a global signal handler so that registered
	// temp-file paths are removed on SIGTERM/SIGINT/SIGHUP, and sweep any
	// /tmp/charly-* leftovers from prior SIGKILL'd charly invocations. See
	// cleanup.go for the full design.
	InstallSignalHandler()
	SweepStaleTemps()

	err := ctx.Run()
	// `charly check` distinguishes "the thing under test is broken" from "the
	// command/usage/infra errored" via a distinct exit code: 0 = pass,
	// 1 = command error (Kong's FatalIfErrorf default), 2 = check checks
	// failed. See CheckFailedError / CheckFailExitCode in check_cmd.go.
	if err != nil {
		if _, ok := errors.AsType[*CheckFailedError](err); ok {
			fmt.Fprintln(os.Stderr, FormatCLIError(err))
			os.Exit(CheckFailExitCode)
		}
	}
	ctx.FatalIfErrorf(FormatCLIError(err))
}
