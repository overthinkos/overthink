package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// PortSpec represents a port declaration with an optional protocol annotation.
// Supports both plain integer (defaults to "http") and "tcp:5900" string forms.
type PortSpec struct {
	Port     int
	Protocol string // "http" (default) or "tcp"
}

// UnmarshalYAML handles both integer and string forms for port specs.
func (p *PortSpec) UnmarshalYAML(value *yaml.Node) error {
	// Try integer first
	if value.Kind == yaml.ScalarNode {
		// Try as int
		if n, err := strconv.Atoi(value.Value); err == nil {
			p.Port = n
			p.Protocol = "http"
			return nil
		}
		// Try as "proto:port" string
		s := value.Value
		if idx := strings.Index(s, ":"); idx != -1 {
			proto := s[:idx]
			portStr := s[idx+1:]
			n, err := strconv.Atoi(portStr)
			if err != nil {
				return fmt.Errorf("invalid port spec %q: port must be a number", s)
			}
			p.Port = n
			p.Protocol = proto
			return nil
		}
		// Plain number as string
		n, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("invalid port spec %q: must be a number or proto:number", s)
		}
		p.Port = n
		p.Protocol = "http"
		return nil
	}
	return fmt.Errorf("invalid port spec: expected scalar, got %v", value.Kind)
}

// VolumeYAML represents a volume declaration in layer.yml
type VolumeYAML struct {
	Name string `yaml:"name"`
	Path string `yaml:"path"`
}

// AliasYAML represents a command alias declaration in layer.yml
type AliasYAML struct {
	Name    string `yaml:"name"`
	Command string `yaml:"command"`
}

// ExtractYAML represents a file extraction from a Docker image
type ExtractYAML struct {
	Source string `yaml:"source"` // Source image (e.g., "ghcr.io/immich-app/immich-server:v1.106.4")
	Path   string `yaml:"path"`   // Path to extract (e.g., "/usr/src/app")
	Dest   string `yaml:"dest"`   // Destination in target image (e.g., "/opt/immich/server")
}

// LayerYAML represents the parsed layer.yml file.
// Unknown top-level keys are captured as tag-based package sections
// (e.g., "fedora:", "archlinux:", "fedora:43:", "debian,ubuntu:").
type LayerYAML struct {
	Version        string            `yaml:"version,omitempty"`  // CalVer version (YYYY.DDD.HHMM) of this layer definition
	Status         string            `yaml:"status,omitempty"`   // working, testing, broken (default: testing)
	Info           string            `yaml:"info,omitempty"`     // free-form description of what works/doesn't
	Layers         []string          `yaml:"layers,omitempty"`
	Depends        []string          `yaml:"depends,omitempty"`
	Engine         string            `yaml:"engine,omitempty"` // required run engine: "docker" or "" (any)
	Env            map[string]string `yaml:"env,omitempty"`
	PathAppend     []string          `yaml:"path_append,omitempty"`
	Ports          []PortSpec        `yaml:"ports,omitempty"`
	Route          *RouteYAML        `yaml:"route,omitempty"`
	Service        string            `yaml:"service,omitempty"`
	Volumes        []VolumeYAML      `yaml:"volumes,omitempty"`
	Aliases        []AliasYAML       `yaml:"aliases,omitempty"`
	Extract        []ExtractYAML     `yaml:"extract,omitempty"`
	Security       *SecurityConfig   `yaml:"security,omitempty"`
	SystemServices []string          `yaml:"system_services,omitempty"`
	Libvirt        []string          `yaml:"libvirt,omitempty"`
	Hooks          *HooksConfig      `yaml:"hooks,omitempty"`
	PortRelay      []int             `yaml:"port_relay,omitempty"`
	SecretsYAML    []SecretYAML      `yaml:"secrets,omitempty"`

	// Populated by custom UnmarshalYAML:
	FormatSections map[string]*PackageSection `yaml:"-"` // format sections (rpm, deb, pac, aur, etc.)
	TagSections    map[string]*TagPkgConfig   `yaml:"-"` // distro/version tag sections
}

// layerYAMLKnownFields lists non-format top-level keys in layer.yml.
// Unknown keys are routed to FormatSections (if matching a distro.yml format)
// or TagSections (otherwise).
var layerYAMLKnownFields = map[string]bool{
	"description": true, "version": true, "status": true, "info": true,
	"layers": true, "depends": true, "engine": true, "env": true,
	"path_append": true, "ports": true, "route": true, "service": true,
	"volumes": true, "aliases": true, "extract": true, "security": true,
	"system_services": true, "libvirt": true, "hooks": true,
	"port_relay": true, "secrets": true,
}

// layerYAMLFormatNames caches known format names from distro.yml for YAML parsing.
// Must be populated by calling SetFormatNames before scanning layers.
var layerYAMLFormatNames map[string]bool

// SetFormatNames registers format names from a DistroConfig for layer YAML parsing.
// Collects all format names across all distros (including inherited ones).
// Must be called before ScanAllLayersWithConfig to ensure format sections
// (e.g., rpm:, deb:) are correctly distinguished from tag sections.
func SetFormatNames(dc *DistroConfig) {
	layerYAMLFormatNames = make(map[string]bool)
	if dc == nil {
		return
	}
	for _, name := range dc.AllFormatNames() {
		layerYAMLFormatNames[name] = true
	}
}

// PackageSection represents a generic format-specific package config in layer.yml.
// All fields from the YAML section are available in Raw for template rendering.
type PackageSection struct {
	FormatName string                 // "rpm", "deb", "pac", "aur", etc.
	Packages   []string               // extracted from Raw["packages"] for quick access
	Raw        map[string]interface{} // all fields from YAML, passed to templates
}

// TagPkgConfig is a simplified package config for distro/version-specific sections.
// Packages are installed using the primary format's tool (dnf, apt, pacman).
type TagPkgConfig struct {
	Packages []string `yaml:"packages,omitempty"`
}

func (ly *LayerYAML) UnmarshalYAML(value *yaml.Node) error {
	// Use type alias to avoid infinite recursion
	type layerYAMLAlias LayerYAML
	var alias layerYAMLAlias
	if err := value.Decode(&alias); err != nil {
		return err
	}
	*ly = LayerYAML(alias)

	// Capture unknown keys as format sections or tag sections.
	// Keys matching distro.yml format names → FormatSections (parsed as raw maps).
	// All other unknown keys → TagSections (parsed as {packages: [...]}).
	if value.Kind == yaml.MappingNode {
		ly.FormatSections = make(map[string]*PackageSection)
		ly.TagSections = make(map[string]*TagPkgConfig)
		for i := 0; i < len(value.Content)-1; i += 2 {
			key := value.Content[i].Value
			if layerYAMLKnownFields[key] {
				continue // handled by standard YAML decoder
			}

			if layerYAMLFormatNames[key] {
				// Format section: parse as raw map for template rendering
				var raw map[string]interface{}
				if err := value.Content[i+1].Decode(&raw); err != nil {
					continue
				}
				section := &PackageSection{
					FormatName: key,
					Raw:        raw,
				}
				if pkgs, ok := raw["packages"]; ok {
					section.Packages = toStringSlice(pkgs)
				}
				if len(section.Packages) > 0 {
					ly.FormatSections[key] = section
				}
			} else {
				// Tag section: parse as simple {packages: [...]}
				var cfg TagPkgConfig
				if err := value.Content[i+1].Decode(&cfg); err != nil {
					continue
				}
				if len(cfg.Packages) == 0 {
					continue
				}
				// Expand comma-separated keys (e.g., "debian,ubuntu")
				parts := strings.Split(key, ",")
				for _, part := range parts {
					part = strings.TrimSpace(part)
					if part != "" {
						ly.TagSections[part] = &cfg
					}
				}
			}
		}
	}

	return nil
}

// RouteYAML represents a route declaration in layer.yml
type RouteYAML struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

// Format-specific structs (RpmConfig, DebConfig, PacConfig, AurConfig) removed.
// All format sections are now parsed dynamically as PackageSection via distro.yml format names.
// See PackageSection type and LayerYAML.UnmarshalYAML for the generic parsing.

// Layer represents a layer directory and its contents
type Layer struct {
	Name              string
	Path              string
	Version           string // CalVer version from layer.yml
	Status            string // working, testing, broken (empty = testing)
	Info              string // free-form status description
	HasRootYml        bool
	HasPixiToml       bool
	HasPyprojectToml  bool
	HasEnvironmentYml bool
	HasPackageJson    bool
	HasCargoToml      bool
	HasSrcDir         bool
	HasUserYml        bool
	HasEnv            bool
	HasPorts          bool
	HasRoute          bool
	HasVolumes        bool
	HasAliases        bool
	HasPixiLock       bool
	HasExtract        bool
	HasLibvirt         bool
	RootYmlTasks       []string // task names defined in root.yml (e.g., ["all", "rpm", "fedora"])

	// Init system detection (populated by PopulateLayerInitSystems)
	InitSystems    map[string]bool   // set of init system names this layer triggers
	PortRelayPorts []int             // port_relay: field (init-agnostic)
	UserYmlTasks       []string // task names defined in user.yml

	Depends           []string // bare refs (version stripped) for resolution
	RawDepends        []string // original refs with :version for remote ref collection
	IncludedLayers    []string // bare refs from layers: field (version stripped)
	RawIncludedLayers []string // original layers: refs with :version

	// Remote layer metadata
	Remote         bool   // true if from a remote repo
	RepoPath       string // e.g. "github.com/overthinkos/overthink" (empty for local)
	SubPathPrefix  string // e.g. "layers/" — parent directory within the repo for sibling resolution

	// Pre-populated from layer.yml
	formatSections map[string]*PackageSection // generic format sections (rpm, deb, pac, aur, etc.)
	tagSections    map[string]*TagPkgConfig   // distro/version-specific package sections
	ports          []string
	portSpecs      []PortSpec // full PortSpec data with protocol info
	envConfig      *EnvConfig
	route          *RouteConfig
	serviceConf    string   // raw content of service: field (supervisord INI fragment)
	serviceFiles   []string // paths to *.service files in layer dir (systemd user-level)
	systemServices []string // system-level service units to enable (e.g., "sshd")
	volumes        []VolumeYAML
	aliases        []AliasYAML
	extract        []ExtractYAML
	security       *SecurityConfig
	libvirt        []string
	hooks          *HooksConfig
	secrets        []SecretYAML
	engine         string // required run engine from layer.yml ("docker", "podman", or "")
}

// ScanLayers scans the layers/ directory and returns all layers
func ScanLayers(dir string) (map[string]*Layer, error) {
	layersDir := filepath.Join(dir, "layers")
	entries, err := os.ReadDir(layersDir)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]*Layer), nil
		}
		return nil, fmt.Errorf("reading layers directory: %w", err)
	}

	layers := make(map[string]*Layer)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		name := entry.Name()
		layer, err := scanLayer(filepath.Join(layersDir, name), name)
		if err != nil {
			return nil, fmt.Errorf("scanning layer %s: %w", name, err)
		}
		layers[name] = layer
	}

	return layers, nil
}

// parseLayerYAML reads and unmarshals a layer.yml file
func parseLayerYAML(path string) (*LayerYAML, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ly LayerYAML
	if err := yaml.Unmarshal(data, &ly); err != nil {
		return nil, err
	}
	return &ly, nil
}

// scanLayer scans a single layer directory
func scanLayer(path string, name string) (*Layer, error) {
	layer := &Layer{
		Name: name,
		Path: path,
	}

	// Check for install files
	layer.HasRootYml = fileExists(filepath.Join(path, "root.yml"))
	layer.HasPixiToml = fileExists(filepath.Join(path, "pixi.toml"))
	layer.HasPyprojectToml = fileExists(filepath.Join(path, "pyproject.toml"))
	layer.HasEnvironmentYml = fileExists(filepath.Join(path, "environment.yml"))
	layer.HasPackageJson = fileExists(filepath.Join(path, "package.json"))
	layer.HasCargoToml = fileExists(filepath.Join(path, "Cargo.toml"))
	layer.HasSrcDir = dirExists(filepath.Join(path, "src"))
	layer.HasUserYml = fileExists(filepath.Join(path, "user.yml"))
	layer.HasPixiLock = fileExists(filepath.Join(path, "pixi.lock"))

	// Parse task names from root.yml/user.yml for tag-based task dispatch
	if layer.HasRootYml {
		layer.RootYmlTasks = parseTaskfileTaskNames(filepath.Join(path, "root.yml"))
	}
	if layer.HasUserYml {
		layer.UserYmlTasks = parseTaskfileTaskNames(filepath.Join(path, "user.yml"))
	}

	// Scan for systemd service files (init system detection happens in PopulateLayerInitSystems)
	svcFiles, _ := filepath.Glob(filepath.Join(path, "*.service"))
	if len(svcFiles) > 0 {
		layer.serviceFiles = svcFiles
	}

	// Parse layer.yml if present
	yamlPath := filepath.Join(path, "layer.yml")
	if fileExists(yamlPath) {
		ly, err := parseLayerYAML(yamlPath)
		if err != nil {
			return nil, fmt.Errorf("parsing layer.yml: %w", err)
		}

		// Pre-populate version, status, info
		layer.Version = ly.Version
		layer.Status = ly.Status
		layer.Info = ly.Info

		// Keep raw depends for remote ref collection
		layer.RawDepends = ly.Depends
		// Strip :version from remote refs for layer resolution (map keys use bare refs)
		layer.Depends = make([]string, len(ly.Depends))
		for i, dep := range ly.Depends {
			layer.Depends[i] = BareRef(dep)
		}

		// Parse layers: field for layer composition
		layer.RawIncludedLayers = ly.Layers
		layer.IncludedLayers = make([]string, len(ly.Layers))
		for i, ref := range ly.Layers {
			layer.IncludedLayers[i] = BareRef(ref)
		}
		layer.serviceConf = ly.Service
		layer.HasEnv = len(ly.Env) > 0 || len(ly.PathAppend) > 0
		layer.HasPorts = len(ly.Ports) > 0
		layer.HasRoute = ly.Route != nil

		// Package config: format sections and tag sections are populated by
		// the custom UnmarshalYAML on LayerYAML. Format sections are detected
		// by matching top-level keys against distro.yml format names.
		layer.formatSections = ly.FormatSections
		if layer.formatSections == nil {
			layer.formatSections = make(map[string]*PackageSection)
		}
		layer.tagSections = ly.TagSections

		// Pre-populate ports cache
		if layer.HasPorts {
			layer.ports = make([]string, len(ly.Ports))
			layer.portSpecs = make([]PortSpec, len(ly.Ports))
			for i, p := range ly.Ports {
				if p.Protocol == "udp" {
					layer.ports[i] = strconv.Itoa(p.Port) + "/udp"
				} else {
					layer.ports[i] = strconv.Itoa(p.Port)
				}
				layer.portSpecs[i] = p
			}
		}

		// Pre-populate env cache
		if layer.HasEnv {
			env := ly.Env
			if env == nil {
				env = make(map[string]string)
			}
			layer.envConfig = &EnvConfig{
				Vars:       env,
				PathAppend: ly.PathAppend,
			}
		}

		// Pre-populate route cache
		if ly.Route != nil {
			layer.route = &RouteConfig{
				Host: ly.Route.Host,
				Port: strconv.Itoa(ly.Route.Port),
			}
		}

		// Pre-populate volumes
		layer.HasVolumes = len(ly.Volumes) > 0
		layer.volumes = ly.Volumes

		// Pre-populate aliases
		layer.HasAliases = len(ly.Aliases) > 0
		layer.aliases = ly.Aliases

		// Pre-populate extract
		layer.HasExtract = len(ly.Extract) > 0
		layer.extract = ly.Extract

		// Pre-populate security
		layer.security = ly.Security

		// Pre-populate system services
		layer.systemServices = ly.SystemServices

		// Pre-populate libvirt snippets
		if len(ly.Libvirt) > 0 {
			layer.HasLibvirt = true
			layer.libvirt = ly.Libvirt
		}

		// Pre-populate hooks
		layer.hooks = ly.Hooks

		// Pre-populate port relay
		layer.PortRelayPorts = ly.PortRelay

		// Pre-populate secrets
		layer.secrets = ly.SecretsYAML

		// Pre-populate engine requirement
		layer.engine = ly.Engine
	}

	return layer, nil
}

// HasInstallFiles returns true if the layer has at least one install file
func (l *Layer) HasInstallFiles() bool {
	return l.HasFormatPackages() || l.HasRootYml ||
		l.HasPixiToml || l.HasPyprojectToml || l.HasEnvironmentYml ||
		l.HasPackageJson || l.HasCargoToml || l.HasUserYml
}

// HasContent returns true if the layer has install files or any configuration
// that contributes to the Containerfile (env, ports, volumes, etc.)
func (l *Layer) HasContent() bool {
	return l.HasInstallFiles() || l.HasEnv || l.HasPorts || l.HasRoute ||
		l.HasVolumes || l.HasAliases || l.HasExtract || l.HasLibvirt ||
		l.HasAnyInit() || len(l.PortRelayPorts) > 0 ||
		len(l.serviceFiles) > 0 || len(l.systemServices) > 0
}

// PixiManifest returns the filename of the pixi manifest if it exists
func (l *Layer) PixiManifest() string {
	if l.HasPixiToml {
		return "pixi.toml"
	}
	if l.HasPyprojectToml {
		return "pyproject.toml"
	}
	if l.HasEnvironmentYml {
		return "environment.yml"
	}
	return ""
}

// FormatSection returns the generic package section for a format, or nil.
func (l *Layer) FormatSection(name string) *PackageSection {
	if l.formatSections == nil {
		return nil
	}
	return l.formatSections[name]
}

// HasFormatPackages returns true if any format section has packages.
func (l *Layer) HasFormatPackages() bool {
	for _, s := range l.formatSections {
		if len(s.Packages) > 0 {
			return true
		}
	}
	return false
}

// TagSection returns the tag-based package config for the given tag, or nil.
func (l *Layer) TagSection(tag string) *TagPkgConfig {
	if l.tagSections == nil {
		return nil
	}
	return l.tagSections[tag]
}

// EnvConfig returns the environment config (pre-populated from layer.yml)
func (l *Layer) EnvConfig() (*EnvConfig, error) {
	if l.envConfig != nil {
		return l.envConfig, nil
	}
	return nil, nil
}

// Ports returns the ports (pre-populated from layer.yml)
func (l *Layer) Ports() ([]string, error) {
	if l.ports != nil {
		return l.ports, nil
	}
	return nil, nil
}

// PortSpecs returns the port specs with protocol info (pre-populated from layer.yml)
func (l *Layer) PortSpecs() []PortSpec {
	return l.portSpecs
}

// ServiceConf returns the service: field content from layer.yml (supervisord INI fragment)
func (l *Layer) ServiceConf() string {
	return l.serviceConf
}

// ServiceFiles returns detected *.service file paths from the layer directory (systemd user-level)
func (l *Layer) ServiceFiles() []string {
	return l.serviceFiles
}

// SystemServiceUnits returns system-level service units to enable (e.g., "sshd")
func (l *Layer) SystemServiceUnits() []string {
	return l.systemServices
}

// HasAnyInit returns true if this layer triggers any init system.
func (l *Layer) HasAnyInit() bool {
	return len(l.InitSystems) > 0
}

// HasInit returns true if this layer triggers the named init system.
func (l *Layer) HasInit(initName string) bool {
	return l.InitSystems[initName]
}

// PopulateLayerInitSystems sets InitSystems on all layers based on the init config.
// Must be called after scanning layers and loading init config.
func PopulateLayerInitSystems(layers map[string]*Layer, initCfg *InitConfig) {
	if initCfg == nil {
		return
	}
	for _, layer := range layers {
		layer.InitSystems = make(map[string]bool)
		for initName, def := range initCfg.Inits {
			// Check layer_fields
			for _, field := range def.LayerFields {
				switch field {
				case "service":
					if layer.serviceConf != "" {
						layer.InitSystems[initName] = true
					}
				case "system_services":
					if len(layer.systemServices) > 0 {
						layer.InitSystems[initName] = true
					}
				}
			}
			// Check layer_files
			for _, pattern := range def.LayerFiles {
				matches, _ := filepath.Glob(filepath.Join(layer.Path, pattern))
				if len(matches) > 0 {
					layer.InitSystems[initName] = true
				}
			}
		}
	}
}

// RouteConfig represents a route file declaration
type RouteConfig struct {
	Host string
	Port string
}

// Route returns the route config (pre-populated from layer.yml)
func (l *Layer) Route() (*RouteConfig, error) {
	if l.route != nil {
		return l.route, nil
	}
	return nil, nil
}

// RouteLayers returns layers that have a route file
func RouteLayers(layers map[string]*Layer) []*Layer {
	var routes []*Layer
	for _, layer := range layers {
		if layer.HasRoute {
			routes = append(routes, layer)
		}
	}
	return routes
}

// LayerNames returns a sorted list of layer names
func LayerNames(layers map[string]*Layer) []string {
	names := make([]string, 0, len(layers))
	for name := range layers {
		names = append(names, name)
	}
	sortStrings(names)
	return names
}

// Volumes returns the volume declarations (pre-populated from layer.yml)
func (l *Layer) Volumes() []VolumeYAML {
	return l.volumes
}

// Extract returns the extract declarations (pre-populated from layer.yml)
func (l *Layer) Extract() []ExtractYAML {
	return l.extract
}

// Security returns the security config (pre-populated from layer.yml, nil if not set)
func (l *Layer) Security() *SecurityConfig {
	return l.security
}

// Libvirt returns the libvirt XML snippets (pre-populated from layer.yml)
func (l *Layer) Libvirt() []string {
	return l.libvirt
}

// Hooks returns the lifecycle hooks config (pre-populated from layer.yml, nil if not set)
func (l *Layer) Hooks() *HooksConfig {
	return l.hooks
}

// Secrets returns the secret declarations (pre-populated from layer.yml)
func (l *Layer) Secrets() []SecretYAML {
	return l.secrets
}

// Engine returns the required run engine (pre-populated from layer.yml, "" if not set)
func (l *Layer) Engine() string {
	return l.engine
}

// InitLayers returns layers that trigger any init system.
func InitLayers(layers map[string]*Layer) []*Layer {
	var result []*Layer
	for _, layer := range layers {
		if layer.HasAnyInit() || len(layer.PortRelayPorts) > 0 {
			result = append(result, layer)
		}
	}
	return result
}

// VolumeLayers returns layers that have volume declarations
func VolumeLayers(layers map[string]*Layer) []*Layer {
	var vols []*Layer
	for _, layer := range layers {
		if layer.HasVolumes {
			vols = append(vols, layer)
		}
	}
	return vols
}

// Aliases returns the alias declarations (pre-populated from layer.yml)
func (l *Layer) Aliases() []AliasYAML {
	return l.aliases
}

// AliasLayers returns layers that have alias declarations
func AliasLayers(layers map[string]*Layer) []*Layer {
	var result []*Layer
	for _, layer := range layers {
		if layer.HasAliases {
			result = append(result, layer)
		}
	}
	return result
}

// NeedsGit returns true if the pixi manifest contains git-based dependencies
func (l *Layer) NeedsGit() bool {
	manifest := l.PixiManifest()
	if manifest == "" {
		return false
	}
	data, err := os.ReadFile(filepath.Join(l.Path, manifest))
	if err != nil {
		return false
	}
	content := string(data)
	// Check for PyPI git+ format and pixi { git = "..." } format
	return strings.Contains(content, "git+") || strings.Contains(content, "{ git =")
}

// HasPypiDeps returns true if the pixi manifest has PyPI dependencies
func (l *Layer) HasPypiDeps() bool {
	manifest := l.PixiManifest()
	if manifest == "" {
		return false
	}
	data, err := os.ReadFile(filepath.Join(l.Path, manifest))
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "[pypi-dependencies]")
}

// ScanRemoteLayers scans specific layers from a downloaded remote repository.
// Only imports layers whose bare refs are in the wantRefs set.
// Bare refs use the full path format: "github.com/org/repo/layers/name".
func ScanRemoteLayers(repoDir string, repoPath string, wantRefs map[string]bool) (map[string]*Layer, error) {
	layers := make(map[string]*Layer)

	for bareRef := range wantRefs {
		// Extract sub-path from bare ref: "github.com/org/repo/layers/name" -> "layers/name"
		subPath := strings.TrimPrefix(bareRef, repoPath+"/")
		layerDir := filepath.Join(repoDir, subPath)

		// Derive name from last segment
		name := subPath
		if idx := strings.LastIndex(subPath, "/"); idx != -1 {
			name = subPath[idx+1:]
		}

		if _, err := os.Stat(layerDir); os.IsNotExist(err) {
			return nil, fmt.Errorf("remote layer %s not found at %s", bareRef, layerDir)
		}

		layer, err := scanLayer(layerDir, name)
		if err != nil {
			return nil, fmt.Errorf("scanning remote layer %s: %w", bareRef, err)
		}
		layer.Remote = true
		layer.RepoPath = repoPath
		// Compute sub-path prefix for sibling dep resolution (e.g. "layers/")
		if idx := strings.LastIndex(subPath, "/"); idx != -1 {
			layer.SubPathPrefix = subPath[:idx+1]
		}

		layers[bareRef] = layer
	}

	return layers, nil
}

// ScanAllLayers scans local layers and all remote layers, returning a merged map.
// Local layers are keyed by short name, remote layers by fully-qualified path.
// Remote refs are collected from @-prefixed refs in layer.yml and images.yml.
func ScanAllLayers(dir string) (map[string]*Layer, error) {
	return ScanAllLayersWithConfig(dir, nil)
}

// ScanAllLayersWithConfig scans local and remote layers.
// Collects remote refs from @-prefixed layer references and auto-downloads repos.
func ScanAllLayersWithConfig(dir string, cfg *Config) (map[string]*Layer, error) {
	// 1. Scan local layers
	layers, err := ScanLayers(dir)
	if err != nil {
		return nil, err
	}

	// 2. Collect remote refs from @-prefixed layer references
	downloads, err := CollectRemoteRefs(cfg, layers)
	if err != nil {
		return nil, err
	}

	if len(downloads) == 0 {
		return layers, nil
	}

	// 3. Auto-download and scan each required (repo, version) pair
	for _, dl := range downloads {
		cachePath, err := EnsureRepoDownloaded(dl.RepoPath, dl.Version)
		if err != nil {
			return nil, fmt.Errorf("downloading %s:%s: %w", dl.RepoPath, dl.Version, err)
		}

		// Build set of wanted bare refs
		wantRefs := make(map[string]bool)
		for _, ref := range dl.Refs {
			wantRefs[ref] = true
		}

		// Scan only the specific layers referenced
		remoteLayers, err := ScanRemoteLayers(cachePath, dl.RepoPath, wantRefs)
		if err != nil {
			return nil, fmt.Errorf("scanning %s:%s: %w", dl.RepoPath, dl.Version, err)
		}

		// Merge into main map
		for ref, layer := range remoteLayers {
			if existing, ok := layers[ref]; ok && existing.Remote {
				return nil, fmt.Errorf("layer reference conflict: %q provided by both %s and %s", ref, existing.RepoPath, layer.RepoPath)
			}
			if _, ok := layers[layer.Name]; ok {
				fmt.Fprintf(os.Stderr, "Note: local layer %q shadows remote layer %q\n", layer.Name, ref)
			}
			layers[ref] = layer
		}
	}

	return layers, nil
}

// fileExists checks if a file exists
func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// dirExists checks if a directory exists
func dirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// parseTaskfileTaskNames extracts top-level task names from a Taskfile (root.yml/user.yml).
// Returns the list of task names (e.g., ["all", "rpm", "fedora:43"]).
func parseTaskfileTaskNames(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var tf struct {
		Tasks map[string]interface{} `yaml:"tasks"`
	}
	if err := yaml.Unmarshal(data, &tf); err != nil {
		return nil
	}

	names := make([]string, 0, len(tf.Tasks))
	for name := range tf.Tasks {
		names = append(names, name)
	}
	return names
}
