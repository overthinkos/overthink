package main

import (
	"reflect"
	"testing"
)

func TestScanLayers(t *testing.T) {
	layers, err := ScanLayers("testdata")
	if err != nil {
		t.Fatalf("ScanLayers() error = %v", err)
	}

	expectedLayers := []string{"pixi", "python", "nodejs", "cargo-tool", "webservice", "pixi-locked"}
	for _, name := range expectedLayers {
		if _, ok := layers[name]; !ok {
			t.Errorf("missing layer %q", name)
		}
	}
}

func TestLayerPixi(t *testing.T) {
	layers, err := ScanLayers("testdata")
	if err != nil {
		t.Fatalf("ScanLayers() error = %v", err)
	}

	pixi := layers["pixi"]
	if pixi == nil {
		t.Fatal("pixi layer not found")
	}

	if !pixi.HasRootYml {
		t.Error("pixi should have root.yml")
	}
	if !pixi.HasUserYml {
		t.Error("pixi should have user.yml")
	}
	if pixi.RpmConfig() != nil {
		t.Error("pixi should not have rpm config")
	}
	if pixi.HasPixiToml {
		t.Error("pixi should not have pixi.toml")
	}
	if len(pixi.Depends) != 0 {
		t.Errorf("pixi should have no depends, got %v", pixi.Depends)
	}
}

func TestLayerPython(t *testing.T) {
	layers, err := ScanLayers("testdata")
	if err != nil {
		t.Fatalf("ScanLayers() error = %v", err)
	}

	python := layers["python"]
	if python == nil {
		t.Fatal("python layer not found")
	}

	if !python.HasPixiToml {
		t.Error("python should have pixi.toml")
	}
	if !reflect.DeepEqual(python.Depends, []string{"pixi"}) {
		t.Errorf("python.Depends = %v, want [pixi]", python.Depends)
	}
}

func TestLayerNodejs(t *testing.T) {
	layers, err := ScanLayers("testdata")
	if err != nil {
		t.Fatalf("ScanLayers() error = %v", err)
	}

	nodejs := layers["nodejs"]
	if nodejs == nil {
		t.Fatal("nodejs layer not found")
	}

	// Test package config from layer.yml
	rpm := nodejs.RpmConfig()
	if rpm == nil {
		t.Fatal("nodejs should have rpm config")
	}
	if !reflect.DeepEqual(rpm.Packages, []string{"nodejs", "npm"}) {
		t.Errorf("RpmConfig().Packages = %v, want [nodejs npm]", rpm.Packages)
	}

	deb := nodejs.DebConfig()
	if deb == nil {
		t.Fatal("nodejs should have deb config")
	}
	if !reflect.DeepEqual(deb.Packages, []string{"nodejs", "npm"}) {
		t.Errorf("DebConfig().Packages = %v, want [nodejs npm]", deb.Packages)
	}

	pac := nodejs.PacConfig()
	if pac == nil {
		t.Fatal("nodejs should have pac config")
	}
	if !reflect.DeepEqual(pac.Packages, []string{"nodejs", "npm"}) {
		t.Errorf("PacConfig().Packages = %v, want [nodejs npm]", pac.Packages)
	}
}

func TestLayerPacTool(t *testing.T) {
	layers, err := ScanLayers("testdata")
	if err != nil {
		t.Fatalf("ScanLayers() error = %v", err)
	}

	pacTool := layers["pac-tool"]
	if pacTool == nil {
		t.Fatal("pac-tool layer not found")
	}

	// Test pac config
	pac := pacTool.PacConfig()
	if pac == nil {
		t.Fatal("pac-tool should have pac config")
	}
	if !reflect.DeepEqual(pac.Packages, []string{"neovim", "ripgrep"}) {
		t.Errorf("PacConfig().Packages = %v, want [neovim ripgrep]", pac.Packages)
	}
	if len(pac.Repos) != 1 || pac.Repos[0].Name != "custom-repo" {
		t.Errorf("PacConfig().Repos = %v, want [{custom-repo ...}]", pac.Repos)
	}
	if pac.Repos[0].Server != "https://example.com/repo/$arch" {
		t.Errorf("PacConfig().Repos[0].Server = %v, want https://example.com/repo/$arch", pac.Repos[0].Server)
	}
	if pac.Repos[0].SigLevel != "Optional TrustAll" {
		t.Errorf("PacConfig().Repos[0].SigLevel = %v, want 'Optional TrustAll'", pac.Repos[0].SigLevel)
	}
	if !reflect.DeepEqual(pac.Options, []string{"--needed"}) {
		t.Errorf("PacConfig().Options = %v, want [--needed]", pac.Options)
	}

	// Test aur config
	aur := pacTool.AurConfig()
	if aur == nil {
		t.Fatal("pac-tool should have aur config")
	}
	if !reflect.DeepEqual(aur.Packages, []string{"yay-bin", "neovim-nightly-bin"}) {
		t.Errorf("AurConfig().Packages = %v, want [yay-bin neovim-nightly-bin]", aur.Packages)
	}

	// Test HasAur flag
	if !pacTool.HasAur {
		t.Error("pac-tool should have HasAur=true")
	}

	// Test HasInstallFiles
	if !pacTool.HasInstallFiles() {
		t.Error("pac-tool should have install files")
	}
}

func TestLayerCargoTool(t *testing.T) {
	layers, err := ScanLayers("testdata")
	if err != nil {
		t.Fatalf("ScanLayers() error = %v", err)
	}

	cargoTool := layers["cargo-tool"]
	if cargoTool == nil {
		t.Fatal("cargo-tool layer not found")
	}

	if !cargoTool.HasCargoToml {
		t.Error("cargo-tool should have Cargo.toml")
	}
	if !cargoTool.HasSrcDir {
		t.Error("cargo-tool should have src/ directory")
	}
}

func TestHasInstallFiles(t *testing.T) {
	layers, err := ScanLayers("testdata")
	if err != nil {
		t.Fatalf("ScanLayers() error = %v", err)
	}

	for name, layer := range layers {
		if !layer.HasInstallFiles() {
			t.Errorf("layer %q should have install files", name)
		}
	}
}

func TestLayerNames(t *testing.T) {
	layers, err := ScanLayers("testdata")
	if err != nil {
		t.Fatalf("ScanLayers() error = %v", err)
	}

	names := LayerNames(layers)
	if len(names) != 7 {
		t.Errorf("LayerNames() returned %d names, want 7", len(names))
	}

	// Should be sorted
	for i := 0; i < len(names)-1; i++ {
		if names[i] > names[i+1] {
			t.Errorf("LayerNames() not sorted: %v", names)
			break
		}
	}
}

func TestLayerPorts(t *testing.T) {
	layers, err := ScanLayers("testdata")
	if err != nil {
		t.Fatalf("ScanLayers() error = %v", err)
	}

	ws := layers["webservice"]
	if ws == nil {
		t.Fatal("webservice layer not found")
	}

	if !ws.HasPorts {
		t.Error("webservice should have ports")
	}

	ports, err := ws.Ports()
	if err != nil {
		t.Fatalf("Ports() error = %v", err)
	}
	if !reflect.DeepEqual(ports, []string{"8080", "9090"}) {
		t.Errorf("Ports() = %v, want [8080 9090]", ports)
	}

	// Test caching
	ports2, err := ws.Ports()
	if err != nil {
		t.Fatalf("Ports() second call error = %v", err)
	}
	if !reflect.DeepEqual(ports, ports2) {
		t.Error("Ports() should return cached result")
	}
}

func TestLayerPortsNone(t *testing.T) {
	layers, err := ScanLayers("testdata")
	if err != nil {
		t.Fatalf("ScanLayers() error = %v", err)
	}

	pixi := layers["pixi"]
	if pixi.HasPorts {
		t.Error("pixi should not have ports")
	}

	ports, err := pixi.Ports()
	if err != nil {
		t.Fatalf("Ports() error = %v", err)
	}
	if ports != nil {
		t.Errorf("Ports() = %v, want nil", ports)
	}
}

func TestLayerRoute(t *testing.T) {
	layers, err := ScanLayers("testdata")
	if err != nil {
		t.Fatalf("ScanLayers() error = %v", err)
	}

	ws := layers["webservice"]
	if ws == nil {
		t.Fatal("webservice layer not found")
	}

	if !ws.HasRoute {
		t.Error("webservice should have route")
	}

	route, err := ws.Route()
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if route.Host != "webservice.localhost" {
		t.Errorf("Route().Host = %q, want %q", route.Host, "webservice.localhost")
	}
	if route.Port != "8080" {
		t.Errorf("Route().Port = %q, want %q", route.Port, "8080")
	}

	// Test caching
	route2, err := ws.Route()
	if err != nil {
		t.Fatalf("Route() second call error = %v", err)
	}
	if route != route2 {
		t.Error("Route() should return cached result")
	}
}

func TestLayerRouteNone(t *testing.T) {
	layers, err := ScanLayers("testdata")
	if err != nil {
		t.Fatalf("ScanLayers() error = %v", err)
	}

	pixi := layers["pixi"]
	if pixi.HasRoute {
		t.Error("pixi should not have route")
	}

	route, err := pixi.Route()
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if route != nil {
		t.Errorf("Route() = %v, want nil", route)
	}
}

func TestLayerPixiLocked(t *testing.T) {
	layers, err := ScanLayers("testdata")
	if err != nil {
		t.Fatalf("ScanLayers() error = %v", err)
	}

	locked := layers["pixi-locked"]
	if locked == nil {
		t.Fatal("pixi-locked layer not found")
	}

	if !locked.HasPixiToml {
		t.Error("pixi-locked should have pixi.toml")
	}
	if !locked.HasPixiLock {
		t.Error("pixi-locked should have pixi.lock")
	}
	if locked.PixiManifest() != "pixi.toml" {
		t.Errorf("pixi-locked.PixiManifest() = %q, want %q", locked.PixiManifest(), "pixi.toml")
	}
	if !reflect.DeepEqual(locked.Depends, []string{"pixi"}) {
		t.Errorf("pixi-locked.Depends = %v, want [pixi]", locked.Depends)
	}
}

func TestLayerPixiNoLock(t *testing.T) {
	layers, err := ScanLayers("testdata")
	if err != nil {
		t.Fatalf("ScanLayers() error = %v", err)
	}

	python := layers["python"]
	if python == nil {
		t.Fatal("python layer not found")
	}

	if !python.HasPixiToml {
		t.Error("python should have pixi.toml")
	}
	if python.HasPixiLock {
		t.Error("python should not have pixi.lock")
	}
}

func TestLayerVolumes(t *testing.T) {
	layers, err := ScanLayers("testdata")
	if err != nil {
		t.Fatalf("ScanLayers() error = %v", err)
	}

	ws := layers["webservice"]
	if ws == nil {
		t.Fatal("webservice layer not found")
	}

	if !ws.HasVolumes {
		t.Error("webservice should have volumes")
	}

	vols := ws.Volumes()
	if len(vols) != 1 {
		t.Fatalf("Volumes() returned %d volumes, want 1", len(vols))
	}
	if vols[0].Name != "data" {
		t.Errorf("Volumes()[0].Name = %q, want %q", vols[0].Name, "data")
	}
	if vols[0].Path != "~/.webservice" {
		t.Errorf("Volumes()[0].Path = %q, want %q", vols[0].Path, "~/.webservice")
	}
}

func TestLayerVolumesNone(t *testing.T) {
	layers, err := ScanLayers("testdata")
	if err != nil {
		t.Fatalf("ScanLayers() error = %v", err)
	}

	pixi := layers["pixi"]
	if pixi.HasVolumes {
		t.Error("pixi should not have volumes")
	}
	if len(pixi.Volumes()) != 0 {
		t.Errorf("Volumes() = %v, want nil/empty", pixi.Volumes())
	}
}

func TestVolumeLayers(t *testing.T) {
	layers, err := ScanLayers("testdata")
	if err != nil {
		t.Fatalf("ScanLayers() error = %v", err)
	}

	vols := VolumeLayers(layers)
	if len(vols) != 1 {
		t.Errorf("VolumeLayers() returned %d layers, want 1", len(vols))
	}
	if len(vols) > 0 && vols[0].Name != "webservice" {
		t.Errorf("VolumeLayers()[0].Name = %q, want %q", vols[0].Name, "webservice")
	}
}

func TestLayerPortRelayFromYAML(t *testing.T) {
	layers, err := ScanLayers("testdata")
	if err != nil {
		t.Fatalf("ScanLayers() error = %v", err)
	}

	ws := layers["webservice"]
	if ws == nil {
		t.Fatal("webservice layer not found")
	}

	if !ws.HasPortRelay {
		t.Error("webservice should have port_relay")
	}

	relay := ws.PortRelay()
	if len(relay) != 1 || relay[0] != 8080 {
		t.Errorf("PortRelay() = %v, want [8080]", relay)
	}
}

func TestLayerPortRelay(t *testing.T) {
	// Test direct struct construction (no testdata file needed)
	layer := &Layer{
		Name:         "chrome",
		HasUserYml:   true,
		HasPortRelay: true,
		portRelay:    []int{9222},
		HasPorts:     true,
		ports:        []string{"9222"},
		portSpecs:    []PortSpec{{Port: 9222, Protocol: "http"}},
	}

	if !layer.HasPortRelay {
		t.Error("layer should have port_relay")
	}
	relay := layer.PortRelay()
	if len(relay) != 1 || relay[0] != 9222 {
		t.Errorf("PortRelay() = %v, want [9222]", relay)
	}
}

func TestLayerPortRelayNone(t *testing.T) {
	layer := &Layer{
		Name:       "basic",
		HasRootYml: true,
	}

	if layer.HasPortRelay {
		t.Error("basic layer should not have port_relay")
	}
	if len(layer.PortRelay()) != 0 {
		t.Errorf("PortRelay() = %v, want nil/empty", layer.PortRelay())
	}
}

func TestLayerPortRelayMultiple(t *testing.T) {
	layer := &Layer{
		Name:         "multi",
		HasUserYml:   true,
		HasPortRelay: true,
		portRelay:    []int{9222, 5900},
		HasPorts:     true,
		ports:        []string{"9222", "5900"},
		portSpecs:    []PortSpec{{Port: 9222, Protocol: "http"}, {Port: 5900, Protocol: "tcp"}},
	}

	relay := layer.PortRelay()
	if len(relay) != 2 {
		t.Fatalf("PortRelay() returned %d ports, want 2", len(relay))
	}
	if relay[0] != 9222 || relay[1] != 5900 {
		t.Errorf("PortRelay() = %v, want [9222 5900]", relay)
	}
}

func TestRouteLayers(t *testing.T) {
	layers, err := ScanLayers("testdata")
	if err != nil {
		t.Fatalf("ScanLayers() error = %v", err)
	}

	routes := RouteLayers(layers)
	if len(routes) != 1 {
		t.Errorf("RouteLayers() returned %d layers, want 1", len(routes))
	}
	if len(routes) > 0 && routes[0].Name != "webservice" {
		t.Errorf("RouteLayers()[0].Name = %q, want %q", routes[0].Name, "webservice")
	}
}

