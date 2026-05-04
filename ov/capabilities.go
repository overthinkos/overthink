package main

import (
	"fmt"
	"reflect"
)

// -----------------------------------------------------------------------------
// Capabilities — the image's runtime contract. Part G of the refactor plan.
//
// Every field listed here MUST have an OCI label home (labelFields table below).
// This is the "what can this image do, what does it need, what does it provide"
// view baked into OCI labels at build time and read back at deploy time, with
// no dependence on the source repo's overthink.yml. The self-deploy invariant
// (Part F.10: `ov deploy from-image`) depends on this list being complete.
//
// Storage note: today the on-disk representation of capabilities is the existing
// ImageMetadata struct (ov/labels.go). Capabilities is an alias that fixes the
// naming + provides a label-completeness check + a typed helper for loading
// from a pushed OCI image by ref alone. A future schema-level split of
// ImageConfig into image.build: + image.capabilities: (which ov migrate
// unified would emit) reuses this same type.
// -----------------------------------------------------------------------------

// Capabilities names the same data as ImageMetadata — it is the runtime
// contract loaded from OCI labels. Using a type alias keeps every existing
// ImageMetadata consumer unchanged while letting new code (Part F K8s
// generator, ov deploy from-image) use the canonical name.
type Capabilities = ImageMetadata

// CapabilityLabelMap names every OCI label that participates in the
// capabilities contract. Maintained alongside ImageMetadata — adding a field
// to ImageMetadata without adding an entry here trips the completeness check
// below and breaks the build.
var CapabilityLabelMap = map[string]string{
	// Identity
	"Image":         LabelImage,
	"Registry":      LabelRegistry,
	"Bootc":         LabelBootc,
	"Status":        LabelStatus,
	"Info":          LabelInfo,
	"LayerVersions": LabelLayerVersions,

	// Account
	"UID":  LabelUID,
	"GID":  LabelGID,
	"User": LabelUser,
	"Home": LabelHome,

	// Ports / volumes / aliases / routes
	"Ports":      LabelPorts,
	"PortProtos": LabelPortProtos,
	"PortRelay":  LabelPortRelay,
	"Volumes":    LabelVolumes,
	"Aliases":    LabelAliases,
	"Routes":     LabelRoutes,

	// Security
	"Security": LabelSecurity,

	// Networking — image-declared network mode. Tunnel / DNS / AcmeEmail
	// moved to DeploymentNode in schema v4 (deployment choices, no
	// image-declaration meaning).
	"Network": LabelNetwork,

	// Env / vars
	"Env":        LabelEnv,
	"EnvLayers":  LabelEnvLayers,
	"PathAppend": LabelPathAppend,

	// Init — auto-detected from layers (see init_config.go ResolveInitSystem).
	// Engine moved to DeploymentNode in schema v4 (deploy-host choice).
	"Init":         LabelInit,
	"Services":     LabelServices,
	"ServiceNames": LabelInit, // per-init active names; baked alongside the init label

	// Distro + build formats + builder provides
	"Distro":       LabelPlatformDistro,
	"BuildFormats": LabelPlatformFormats,
	"Builder":      LabelBuilderUses,
	"Builds":       LabelBuilderProvides,

	// Hooks
	"Hooks": LabelHooks,
	// Vm / Libvirt removed in the VM hard-cutover (see labels.go).

	// Skills (doc pointer)
	"Skills": LabelSkills,

	// Data seeding
	"DataEntries": LabelDataEntries,
	"DataImage":   LabelDataImage,

	// Env / secret / MCP dependency graph
	"EnvProvides":    LabelEnvProvides,
	"EnvRequires":    LabelEnvRequires,
	"EnvAccepts":     LabelEnvAccepts,
	"SecretAccepts":  LabelSecretAccepts,
	"SecretRequires": LabelSecretRequires,
	"Secrets":        LabelSecrets,
	"MCPProvides":    LabelMCPProvides,
	"MCPRequires":    LabelMCPRequires,
	"MCPAccepts":     LabelMCPAccepts,

	// Declarative tests (image-level invariants + deploy defaults)
	"Eval":  LabelEval,

	// Gherkin-shaped self-description — three-section (layer/image/deploy)
	// LabelDescriptionSet. Replaces the single-scalar Info/Status pair in
	// the BDD cutover; those remain on ImageMetadata during the additive
	// foundation phase and are removed in the hard-cutover commit.
	"Description": LabelDescription,

	// Shell-init manifest — three-section (layer/image/deploy) per-shell
	// rc-snippet contributions. 2026-05 cutover. Read by `ov image
	// inspect`, `ov deploy from-image`, and the deploy.yml `shell:`
	// overlay merge in MergeDeployShell.
	"Shell": LabelShell,
}

// deployOnlyCapabilityFields are ImageMetadata fields that are NOT baked
// as OCI labels by design — they're populated from deploy.yml overlays
// (or deploy-host config) and have no image-declaration meaning. The
// completeness check exempts them from CapabilityLabelMap mapping.
//
// This list codifies the schema v4 migration note on labels.go:33-36:
// "Tunnel / DNS / AcmeEmail / Engine moved to DeploymentNode". The fields
// stay on ImageMetadata because deploy-mode commands still consume them
// after MergeDeployOntoMetadata runs — but they never round-trip through
// OCI labels.
var deployOnlyCapabilityFields = map[string]bool{
	"Tunnel":    true,
	"DNS":       true,
	"AcmeEmail": true,
	"Engine":    true,
}

// checkCapabilityLabelCompleteness returns an error listing any ImageMetadata
// exported field that lacks an entry in CapabilityLabelMap. Called from
// TestCapabilityLabelCompleteness to fail the build when a field is added
// without a label mapping.
func checkCapabilityLabelCompleteness() error {
	rt := reflect.TypeOf(ImageMetadata{})
	var missing []string
	for i := 0; i < rt.NumField(); i++ {
		name := rt.Field(i).Name
		if deployOnlyCapabilityFields[name] {
			continue
		}
		if _, ok := CapabilityLabelMap[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("ImageMetadata fields without CapabilityLabelMap entry: %v", missing)
	}
	return nil
}

// CapabilitiesFromLabels is the source-less loader used by `ov deploy
// from-image` (Part F.10): given only an engine + image ref, pull OCI labels
// via inspect and produce a Capabilities struct. No overthink.yml, no source
// repo access required. Errors propagate ErrImageNotLocal when appropriate
// (caller can wrap with a "run ov image pull" hint).
func CapabilitiesFromLabels(engine, imageRef string) (*Capabilities, error) {
	meta, err := ExtractMetadata(engine, imageRef)
	if err != nil {
		return nil, err
	}
	if meta == nil {
		return nil, fmt.Errorf("image %q has no org.overthinkos labels (not an overthink image?)", imageRef)
	}
	return meta, nil
}
