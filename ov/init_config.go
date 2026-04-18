package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

// --- Init Config ---

// InitConfig represents the `init:` section of build.yml.
// Each init system defines how to detect, build, assemble, and manage services.
type InitConfig struct {
	Init map[string]*InitDef `yaml:"init"`
}

// InitDef defines an init system (supervisord, systemd, s6, etc.).
type InitDef struct {
	// Detection: which layer.yml fields and file patterns trigger this init system
	LayerFields   []string `yaml:"layer_fields,omitempty"`
	LayerFiles    []string `yaml:"layer_files,omitempty"`   // glob patterns (e.g., "*.service")
	DependsLayer  string   `yaml:"depends_layer,omitempty"` // layer name required in dependency chain
	RequiresBootc bool     `yaml:"requires_bootc,omitempty"`

	// Build model: "fragment_assembly" or "file_copy"
	Model string `yaml:"model"`

	// Fragment assembly model
	HeaderFile       string `yaml:"header_file,omitempty"`
	FragmentDir      string `yaml:"fragment_dir,omitempty"` // subdir under .build/<image>/
	FragmentTemplate string `yaml:"fragment_template,omitempty"`
	RelayTemplate    string `yaml:"relay_template,omitempty"`

	// Containerfile stage
	StageName         string `yaml:"stage_name,omitempty"`
	StageHeaderCopy   string `yaml:"stage_header_copy,omitempty"`
	StageFragmentCopy string `yaml:"stage_fragment_copy,omitempty"` // Go template

	// Containerfile assembly
	AssemblyTemplate     string `yaml:"assembly_template,omitempty"`
	SystemEnableTemplate string `yaml:"system_enable_template,omitempty"`
	PostAssemblyTemplate string `yaml:"post_assembly_template,omitempty"`

	// Runtime
	Entrypoint         []string `yaml:"entrypoint,omitempty"`
	FallbackEntrypoint []string `yaml:"fallback_entrypoint,omitempty"`

	// Service management (ov service commands)
	ManagementTool     string            `yaml:"management_tool,omitempty"`
	ManagementCommands map[string]string  `yaml:"management_commands,omitempty"`

	// OCI label key for service list
	LabelKey string `yaml:"label_key,omitempty"`
}

// FragmentContext is the template context for fragment_template rendering.
type FragmentContext struct {
	Content   string
	LayerName string
	Index     int
}

// RelayContext is the template context for relay_template rendering.
type RelayContext struct {
	Port      int
	LayerName string
	Index     int
}

// StageFragmentContext is the template context for stage_fragment_copy rendering.
type StageFragmentContext struct {
	ImageName   string
	FragmentDir string
	FileName    string
}

// SystemEnableContext is the template context for system_enable_template rendering.
type SystemEnableContext struct {
	Units []string
}

// ServiceCommandContext is the template context for management_commands rendering.
type ServiceCommandContext struct {
	Service string
}

// DetectLayerInits returns which init system names a layer triggers,
// based on its layer.yml fields and file patterns.
func (ic *InitConfig) DetectLayerInits(ly *LayerYAML, layerPath string) []string {
	if ic == nil {
		return nil
	}
	var result []string
	for initName, def := range ic.Init {
		if detectsInit(def, ly, layerPath) {
			result = append(result, initName)
		}
	}
	sortStrings(result)
	return result
}

// detectsInit checks if a layer matches an init system's detection criteria.
func detectsInit(def *InitDef, ly *LayerYAML, layerPath string) bool {
	// Check layer_fields: does the layer.yml have this field set?
	for _, field := range def.LayerFields {
		switch field {
		case "service":
			if ly != nil && ly.Service != "" {
				return true
			}
		case "system_services":
			if ly != nil && len(ly.SystemServices) > 0 {
				return true
			}
		}
	}

	// Check layer_files: does the layer directory contain matching files?
	for _, pattern := range def.LayerFiles {
		matches, _ := filepath.Glob(filepath.Join(layerPath, pattern))
		if len(matches) > 0 {
			return true
		}
	}

	return false
}

// ResolveInitSystem determines the active init system for an image.
// Priority: explicit override → auto-detect from layers.
// Returns ("", nil) if no init system is needed.
func (ic *InitConfig) ResolveInitSystem(layers map[string]*Layer, layerOrder []string, isBootc bool, explicit string) (string, *InitDef) {
	if ic == nil {
		return "", nil
	}

	// Explicit override
	if explicit != "" {
		if def, ok := ic.Init[explicit]; ok {
			return explicit, def
		}
	}

	// Auto-detect: find the init system that layers trigger
	// For bootc images, prefer systemd over supervisord when systemd services exist
	initHits := make(map[string]bool)
	for _, layerName := range layerOrder {
		layer, ok := layers[layerName]
		if !ok {
			continue
		}
		for initName := range layer.InitSystems {
			initHits[initName] = true
		}
		// port_relay triggers the init system with a relay_template
		if len(layer.PortRelayPorts) > 0 {
			for initName, def := range ic.Init {
				if def.RelayTemplate != "" {
					initHits[initName] = true
				}
			}
		}
	}

	// Filter by bootc requirement
	for initName := range initHits {
		def := ic.Init[initName]
		if def.RequiresBootc && !isBootc {
			delete(initHits, initName)
		}
	}

	// For bootc images with systemd, use systemd (skip supervisord)
	if isBootc && initHits["systemd"] {
		return "systemd", ic.Init["systemd"]
	}

	// For container images, prefer supervisord
	if initHits["supervisord"] {
		return "supervisord", ic.Init["supervisord"]
	}

	// Return first remaining init system
	for initName := range initHits {
		return initName, ic.Init[initName]
	}

	return "", nil
}

// ActiveInits returns all init systems that are active for the given image.
// An image can have multiple active inits (e.g., supervisord + systemd on bootc).
func (ic *InitConfig) ActiveInits(layers map[string]*Layer, layerOrder []string, isBootc bool) map[string]*InitDef {
	if ic == nil {
		return nil
	}

	result := make(map[string]*InitDef)
	for _, layerName := range layerOrder {
		layer, ok := layers[layerName]
		if !ok {
			continue
		}
		for initName := range layer.InitSystems {
			if def, ok := ic.Init[initName]; ok {
				if def.RequiresBootc && !isBootc {
					continue
				}
				result[initName] = def
			}
		}
		// port_relay triggers init systems with relay_template
		if len(layer.PortRelayPorts) > 0 {
			for initName, def := range ic.Init {
				if def.RelayTemplate != "" && (!def.RequiresBootc || isBootc) {
					result[initName] = def
				}
			}
		}
	}

	return result
}

// HasRelayTemplate returns true if this init definition has a relay template.
func (def *InitDef) HasRelayTemplate() bool {
	return def.RelayTemplate != ""
}

// EntrypointArgs returns the entrypoint command as a string slice.
// Returns fallback_entrypoint if the init has no entrypoint (e.g., systemd).
func (def *InitDef) EntrypointArgs() []string {
	if len(def.Entrypoint) > 0 {
		return def.Entrypoint
	}
	return def.FallbackEntrypoint
}

// RenderManagementCommand renders a management command template with the given service name.
func (def *InitDef) RenderManagementCommand(operation, serviceName string) (string, error) {
	tmplStr, ok := def.ManagementCommands[operation]
	if !ok {
		return "", fmt.Errorf("init system %q has no management command for %q", def.ManagementTool, operation)
	}
	ctx := ServiceCommandContext{Service: serviceName}
	return RenderTemplate("mgmt-"+operation, tmplStr, ctx)
}

// --- Loading ---
// Init config is loaded as part of LoadBuildConfigForImage in format_config.go.
// The `init:` section of build.yml is optional — absent/empty means no init system.

// InitNames returns a sorted list of all init system names.
func (ic *InitConfig) InitNames() []string {
	if ic == nil {
		return nil
	}
	names := make([]string, 0, len(ic.Init))
	for name := range ic.Init {
		names = append(names, name)
	}
	sortStrings(names)
	return names
}

// RenderStageFragmentCopy renders the stage_fragment_copy template.
func (def *InitDef) RenderStageFragmentCopy(imageName, fileName string) (string, error) {
	if def.StageFragmentCopy == "" {
		return "", nil
	}
	ctx := StageFragmentContext{
		ImageName:   imageName,
		FragmentDir: def.FragmentDir,
		FileName:    fileName,
	}
	return RenderTemplate("stage-fragment-copy", def.StageFragmentCopy, ctx)
}

// RenderFragmentTemplate renders the fragment_template for a layer's service content.
func (def *InitDef) RenderFragmentTemplate(content, layerName string, index int) (string, error) {
	if def.FragmentTemplate == "" {
		return content, nil
	}
	ctx := FragmentContext{
		Content:   content,
		LayerName: layerName,
		Index:     index,
	}
	result, err := RenderTemplate("fragment", def.FragmentTemplate, ctx)
	if err != nil {
		return "", err
	}
	// Ensure trailing newline
	if !strings.HasSuffix(result, "\n") {
		result += "\n"
	}
	return result, nil
}

// RenderRelayTemplate renders the relay_template for a port relay.
func (def *InitDef) RenderRelayTemplate(port int, layerName string, index int) (string, error) {
	if def.RelayTemplate == "" {
		return "", fmt.Errorf("init system has no relay_template")
	}
	ctx := RelayContext{
		Port:      port,
		LayerName: layerName,
		Index:     index,
	}
	result, err := RenderTemplate("relay", def.RelayTemplate, ctx)
	if err != nil {
		return "", err
	}
	if !strings.HasSuffix(result, "\n") {
		result += "\n"
	}
	return result, nil
}

// RenderAssemblyTemplate renders the assembly_template.
func (def *InitDef) RenderAssemblyTemplate() (string, error) {
	if def.AssemblyTemplate == "" {
		return "", nil
	}
	return RenderTemplate("assembly", def.AssemblyTemplate, nil)
}

// RenderSystemEnableTemplate renders the system_enable_template.
func (def *InitDef) RenderSystemEnableTemplate(units []string) (string, error) {
	if def.SystemEnableTemplate == "" || len(units) == 0 {
		return "", nil
	}
	ctx := SystemEnableContext{Units: units}
	return RenderTemplate("system-enable", def.SystemEnableTemplate, ctx)
}

// RenderPostAssemblyTemplate renders the post_assembly_template.
func (def *InitDef) RenderPostAssemblyTemplate() (string, error) {
	if def.PostAssemblyTemplate == "" {
		return "", nil
	}
	return RenderTemplate("post-assembly", def.PostAssemblyTemplate, nil)
}
