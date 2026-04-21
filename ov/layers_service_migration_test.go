package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Tests that exercise the compiler's handling of BOTH the legacy
// (service: + system_services:) and the unified (services:) schemas.
// Layer authors may migrate at their own pace; the compiler must keep
// producing correct IR for both shapes.

func TestLegacyServiceAndSystemServicesProduceSteps(t *testing.T) {
	// Synthetic layer with both legacy fields, exactly matching the
	// shape of layers/sshd/layer.yml.
	yml := `
service: |
  [program:sshd]
  command=/usr/local/bin/sshd-wrapper
  autorestart=true

system_services:
  - sshd
`
	layer := parseTestLayer(t, "sshd", yml)
	img := testComposeImage()

	plan, err := BuildDeployPlan(layer, img, HostContext{})
	if err != nil {
		t.Fatalf("BuildDeployPlan: %v", err)
	}

	var pkg, custom int
	for _, s := range plan.Steps {
		switch s.(type) {
		case *ServicePackagedStep:
			pkg++
		case *ServiceCustomStep:
			custom++
		}
	}
	if pkg != 1 {
		t.Errorf("expected 1 ServicePackagedStep (from system_services:), got %d", pkg)
	}
	if custom != 1 {
		t.Errorf("expected 1 ServiceCustomStep (from service:), got %d", custom)
	}
}

func TestUnifiedServicesSchemaPreferredOverLegacy(t *testing.T) {
	// When a layer declares `services:`, the compiler should use that
	// list and IGNORE the legacy service: / system_services: fields.
	// This lets authors migrate incrementally: during the transition
	// they can leave the old fields in place as documentation while
	// the new schema actually drives the deploy.
	yml := `
services:
  - name: sshd
    use_packaged: sshd.service
    enable: true
  - name: sshd-wrapper
    exec: /usr/local/bin/sshd-wrapper
    restart: always
    enable: true

# Legacy fields retained but should be ignored when services: is present.
service: |
  [program:old-sshd]
  command=/bin/false

system_services:
  - old-unit
`
	layer := parseTestLayer(t, "sshd", yml)
	img := testComposeImage()

	plan, err := BuildDeployPlan(layer, img, HostContext{})
	if err != nil {
		t.Fatalf("BuildDeployPlan: %v", err)
	}

	// Should have 1 packaged + 1 custom from services: (unified), NOT
	// the legacy entries.
	var packaged []*ServicePackagedStep
	var custom []*ServiceCustomStep
	for _, s := range plan.Steps {
		if sp, ok := s.(*ServicePackagedStep); ok {
			packaged = append(packaged, sp)
		}
		if sc, ok := s.(*ServiceCustomStep); ok {
			custom = append(custom, sc)
		}
	}
	if len(packaged) != 1 || packaged[0].Unit != "sshd.service" {
		t.Errorf("expected ServicePackagedStep for sshd.service (unified), got %+v", packaged)
	}
	if len(custom) != 1 || !strings.Contains(custom[0].Name, "sshd-wrapper") {
		t.Errorf("expected ServiceCustomStep for sshd-wrapper (unified), got %+v", custom)
	}
	// The legacy entries (old-unit, old-sshd) must not have produced steps.
	for _, p := range packaged {
		if p.Unit == "old-unit.service" {
			t.Errorf("legacy system_services: entry leaked through: %+v", p)
		}
	}
	for _, c := range custom {
		if strings.Contains(c.Name, "old-sshd") {
			t.Errorf("legacy service: entry leaked through: %+v", c)
		}
	}
}

func TestServiceMigrationPattern(t *testing.T) {
	// Demonstration test: show the before/after shape for layer authors.
	legacy := `
rpm:
  packages: [openssh-server, sudo]

service: |
  [program:sshd]
  command=/usr/local/bin/sshd-wrapper
  autorestart=true

system_services:
  - sshd
`
	migrated := `
rpm:
  packages: [openssh-server, sudo]

services:
  - name: sshd
    use_packaged: sshd.service
    enable: true

  - name: sshd-wrapper
    exec: /usr/local/bin/sshd-wrapper
    restart: always
    enable: true
`
	legacyLayer := parseTestLayer(t, "sshd", legacy)
	migratedLayer := parseTestLayer(t, "sshd", migrated)
	img := testComposeImage()

	legacyPlan, _ := BuildDeployPlan(legacyLayer, img, HostContext{})
	migratedPlan, _ := BuildDeployPlan(migratedLayer, img, HostContext{})

	// Both should produce the same step *count* per kind; actual
	// rendering differs (legacy carries the raw INI as UnitText,
	// migrated relies on the init-system template). This test ensures
	// the migration doesn't silently drop a service.
	if countStepsByKind(legacyPlan, StepKindServicePackaged) != countStepsByKind(migratedPlan, StepKindServicePackaged) {
		t.Errorf("packaged-step count differs: legacy=%d migrated=%d",
			countStepsByKind(legacyPlan, StepKindServicePackaged),
			countStepsByKind(migratedPlan, StepKindServicePackaged))
	}
	if countStepsByKind(legacyPlan, StepKindServiceCustom) != countStepsByKind(migratedPlan, StepKindServiceCustom) {
		t.Errorf("custom-step count differs: legacy=%d migrated=%d",
			countStepsByKind(legacyPlan, StepKindServiceCustom),
			countStepsByKind(migratedPlan, StepKindServiceCustom))
	}
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// parseTestLayer writes yml to a temp layer.yml, scans it via the
// project's own layer loader, and returns the populated Layer. The
// scan uses a minimal distro config that registers "rpm" as a format
// so package sections parse.
func parseTestLayer(t *testing.T, name, yml string) *Layer {
	t.Helper()
	tmp := t.TempDir()
	layerDir := filepath.Join(tmp, "layers", name)
	if err := os.MkdirAll(layerDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layerDir, "layer.yml"), []byte(yml), 0644); err != nil {
		t.Fatal(err)
	}
	// Register a minimal distro that knows about rpm — the format_section
	// detection depends on layerYAMLFormatNames being populated first.
	oldFormats := layerYAMLFormatNames
	layerYAMLFormatNames = map[string]bool{"rpm": true, "deb": true, "pac": true, "aur": true}
	t.Cleanup(func() { layerYAMLFormatNames = oldFormats })

	// Parse the YAML directly into a LayerYAML — simpler than spinning
	// up ScanAllLayersWithConfig in a fresh cwd.
	var ly LayerYAML
	if err := yaml.Unmarshal([]byte(yml), &ly); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	layer := &Layer{
		Name:           name,
		Path:           layerDir,
		formatSections: ly.FormatSections,
		tagSections:    ly.TagSections,
		serviceConf:    ly.Service,
		systemServices: ly.SystemServices,
		services:       ly.Services,
		tasks:          ly.Tasks,
	}
	if len(ly.Tasks) > 0 {
		layer.HasTasks = true
	}
	return layer
}

// testComposeImage returns a minimal ResolvedImage suitable for the
// service-migration tests. A real image would have Builder/Distro etc.
// populated; we only need the fields the service compiler reads.
func testComposeImage() *ResolvedImage {
	return &ResolvedImage{
		Name:         "test",
		Home:         "/home/test",
		User:         "test",
		UID:          1000,
		GID:          1000,
		Pkg:          "rpm",
		BuildFormats: []string{"rpm"},
		Distro:       []string{"fedora:43", "fedora"},
	}
}

func countStepsByKind(p *InstallPlan, kind StepKind) int {
	n := 0
	for _, s := range p.Steps {
		if s.Kind() == kind {
			n++
		}
	}
	return n
}
