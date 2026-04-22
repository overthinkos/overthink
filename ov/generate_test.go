package main

import (
	"os"
	"strings"
	"testing"
)

func TestResolveBaseImage_InternalUseCalVer(t *testing.T) {
	g := &Generator{
		Images: map[string]*ResolvedImage{
			"fedora": {
				Name:           "fedora",
				Base:           "quay.io/fedora/fedora:43",
				IsExternalBase: true,
				Registry:       "ghcr.io/overthinkos",
				Tag:            "2026.46.1415",
				FullTag:        "ghcr.io/overthinkos/fedora:2026.46.1415",
			},
			"fedora-test": {
				Name:           "fedora-test",
				Base:           "fedora",
				IsExternalBase: false,
				Registry:       "ghcr.io/overthinkos",
				Tag:            "2026.46.1415",
				FullTag:        "ghcr.io/overthinkos/fedora-test:2026.46.1415",
			},
		},
	}

	// External base should return the base as-is
	got := g.resolveBaseImage(g.Images["fedora"])
	if got != "quay.io/fedora/fedora:43" {
		t.Errorf("resolveBaseImage(fedora) = %q, want external base", got)
	}

	// Internal base should return the parent's full CalVer tag
	got = g.resolveBaseImage(g.Images["fedora-test"])
	want := "ghcr.io/overthinkos/fedora:2026.46.1415"
	if got != want {
		t.Errorf("resolveBaseImage(fedora-test) = %q, want %q", got, want)
	}
}

func TestGenerateTraefikRoutes(t *testing.T) {
	tmpDir := t.TempDir()

	g := &Generator{
		BuildDir: tmpDir,
		Layers: map[string]*Layer{
			"traefik": {
				Name:       "traefik",
				HasTasks: true,
			},
			"svc": {
				Name:     "svc",
				HasRoute: true,
				HasTasks: true,
				route:    &RouteConfig{Host: "svc.localhost", Port: "9090"},
			},
		},
	}

	err := g.generateTraefikRoutes("test-image", []string{"traefik", "svc"}, &ResolvedImage{})
	if err != nil {
		t.Fatalf("generateTraefikRoutes() error = %v", err)
	}

	data, err := os.ReadFile(tmpDir + "/test-image/traefik-routes.yml")
	if err != nil {
		t.Fatalf("reading generated routes YAML: %v", err)
	}
	yaml := string(data)

	// Check structure
	if !strings.Contains(yaml, "http:") {
		t.Error("missing http: key")
	}
	if !strings.Contains(yaml, "routers:") {
		t.Error("missing routers: key")
	}
	if !strings.Contains(yaml, "services:") {
		t.Error("missing services: key")
	}

	// Check route entry
	if !strings.Contains(yaml, "svc:") {
		t.Error("missing svc router/service entry")
	}
	if !strings.Contains(yaml, `Host(`+"`"+`svc.localhost`+"`"+`)`) {
		t.Error("missing Host rule")
	}
	if !strings.Contains(yaml, "http://127.0.0.1:9090") {
		t.Error("missing backend URL")
	}
	if !strings.Contains(yaml, "- web") {
		t.Error("missing entryPoints web")
	}
}

func TestGenerateRouteWithoutTraefik_NoTraefikRoutes(t *testing.T) {
	// When an image has route layers but no traefik layer,
	// traefik-routes.yml should NOT be generated
	tmpDir := t.TempDir()

	g := &Generator{
		BuildDir: tmpDir,
		Config:   &Config{},
		Layers: map[string]*Layer{
			"svc": {
				Name:       "svc",
				HasRoute:   true,
				HasTasks: true,
				route:      &RouteConfig{Host: "svc.localhost", Port: "9090"},
			},
		},
		Images: map[string]*ResolvedImage{
			"test-image": {
				Name:           "test-image",
				Base:           "quay.io/fedora/fedora:43",
				IsExternalBase: true,
				Registry:       "ghcr.io/test",
				Tag:            "latest",
				FullTag:        "ghcr.io/test/test-image:latest",
				Layers:         []string{"svc"},
				Pkg:            "rpm",
				BuildFormats:   []string{"rpm"},
				Tags:           []string{"all", "rpm"},
				User:           "user",
				UID:            1000,
				GID:            1000,
				Home:           "/home/user",
			},
		},
		Containerfiles: make(map[string]string),
	}

	err := g.generateContainerfile("test-image")
	if err != nil {
		t.Fatalf("generateContainerfile() error = %v", err)
	}

	// traefik-routes.yml should NOT exist
	_, err = os.ReadFile(tmpDir + "/test-image/traefik-routes.yml")
	if err == nil {
		t.Error("traefik-routes.yml should NOT be generated when traefik layer is absent")
	}

	// Containerfile should NOT reference traefik-routes
	content := g.Containerfiles["test-image"]
	if strings.Contains(content, "traefik-routes") {
		t.Error("Containerfile should not reference traefik-routes when traefik layer is absent")
	}
}

func TestGenerateInitFragments(t *testing.T) {
	tmpDir := t.TempDir()

	// Schema-driven: each layer's service: list contains structured entries.
	// generateInitFragments iterates them and calls RenderService per entry.
	g := &Generator{
		BuildDir: tmpDir,
		Layers: map[string]*Layer{
			"python": {
				Name:     "python",
				HasTasks: true,
			},
			"svc": {
				Name:        "svc",
				InitSystems: map[string]bool{"supervisord": true},
				HasTasks:    true,
				service: []ServiceEntry{
					{Name: "svc", Exec: "svc serve"},
				},
			},
			"other": {
				Name:        "other",
				InitSystems: map[string]bool{"supervisord": true},
				HasTasks:    true,
				service: []ServiceEntry{
					{Name: "other", Exec: "other run"},
				},
			},
		},
	}

	// Minimal supervisord-like template that renders a [program:NAME] block.
	supervisordDef := &InitDef{
		Model:       "fragment_assembly",
		FragmentDir: "supervisor",
		ServiceSchema: &ServiceSchemaDef{
			SupportsPackaged: false,
			ServiceTemplate:  "[program:{{.Name}}]\ncommand={{.Exec}}\n",
		},
	}

	err := g.generateInitFragments("test-image", "supervisord", supervisordDef, []string{"python", "svc", "other"})
	if err != nil {
		t.Fatalf("generateInitFragments() error = %v", err)
	}

	// Layer ordering: python=1, svc=2, other=3. Each layer with service entries
	// gets ONE fragment file named <NN>-<layer>.conf containing all its entries.
	data, err := os.ReadFile(tmpDir + "/test-image/supervisor/02-svc.conf")
	if err != nil {
		t.Fatalf("reading svc supervisor fragment: %v", err)
	}
	if !strings.Contains(string(data), "[program:svc]") {
		t.Errorf("svc fragment missing [program:svc]; got: %q", string(data))
	}
	if !strings.Contains(string(data), "command=svc serve") {
		t.Errorf("svc fragment missing exec command; got: %q", string(data))
	}

	data, err = os.ReadFile(tmpDir + "/test-image/supervisor/03-other.conf")
	if err != nil {
		t.Fatalf("reading other supervisor fragment: %v", err)
	}
	if !strings.Contains(string(data), "[program:other]") {
		t.Errorf("other fragment missing [program:other]; got: %q", string(data))
	}

	// python has no service: entry → no fragment file.
	if _, err := os.Stat(tmpDir + "/test-image/supervisor/01-python.conf"); err == nil {
		t.Error("python should not produce a fragment")
	}
}

func TestGenerateRelayInitFragments(t *testing.T) {
	tmpDir := t.TempDir()

	relayTmpl := "[program:relay-{{.Port}}]\ncommand=/usr/local/bin/relay-wrapper {{.Port}}\nautostart=true\nautorestart=true\npriority=1\nstartsecs=0\nstdout_logfile=/dev/fd/1\nstdout_logfile_maxbytes=0\nredirect_stderr=true\n"

	g := &Generator{
		BuildDir: tmpDir,
		Layers: map[string]*Layer{
			"socat": {
				Name:       "socat",
				HasTasks: true,
			},
			"chrome": {
				Name:           "chrome",
				HasTasks:       true,
				PortRelayPorts: []int{9222},
				InitSystems:    map[string]bool{"supervisord": true},
				service: []ServiceEntry{
					{Name: "chrome", Exec: "chrome"},
				},
			},
		},
	}

	supervisordDef := &InitDef{
		Model:       "fragment_assembly",
		FragmentDir: "supervisor",
		ServiceSchema: &ServiceSchemaDef{
			SupportsPackaged: false,
			ServiceTemplate:  "[program:{{.Name}}]\ncommand={{.Exec}}\n",
		},
		RelayTemplate: relayTmpl,
	}

	err := g.generateInitFragments("test-image", "supervisord", supervisordDef, []string{"socat", "chrome"})
	if err != nil {
		t.Fatalf("generateInitFragments() error = %v", err)
	}

	// Layer ordering: socat=1, chrome=2. chrome has both a service: entry
	// and a port_relay, producing 02-chrome.conf + 02-relay-9222.conf.
	data, err := os.ReadFile(tmpDir + "/test-image/supervisor/02-chrome.conf")
	if err != nil {
		t.Fatalf("reading chrome supervisor config: %v", err)
	}
	if !strings.Contains(string(data), "[program:chrome]") {
		t.Error("chrome fragment should contain [program:chrome]")
	}

	data, err = os.ReadFile(tmpDir + "/test-image/supervisor/02-relay-9222.conf")
	if err != nil {
		t.Fatalf("reading relay supervisor config: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "[program:relay-9222]") {
		t.Error("relay fragment should contain [program:relay-9222]")
	}
	if !strings.Contains(content, "relay-wrapper 9222") {
		t.Error("relay fragment should contain relay-wrapper 9222 command")
	}
	if !strings.Contains(content, "autostart=true") {
		t.Error("relay fragment should have autostart=true")
	}
	if !strings.Contains(content, "priority=1") {
		t.Error("relay fragment should have priority=1")
	}

	// socat has no supervisord or port_relay, should not have a config
	_, err = os.ReadFile(tmpDir + "/test-image/supervisor/01-socat.conf")
	if err == nil {
		t.Error("socat should not have a supervisor config")
	}
}

func TestRenderRelayTemplate(t *testing.T) {
	relayTmpl := "[program:relay-{{.Port}}]\ncommand=/usr/local/bin/relay-wrapper {{.Port}}\nautostart=true\nautorestart=true\npriority=1\nstartsecs=0\nstdout_logfile=/dev/fd/1\nstdout_logfile_maxbytes=0\nredirect_stderr=true\n"
	def := &InitDef{
		RelayTemplate: relayTmpl,
	}

	conf, err := def.RenderRelayTemplate(9222, "chrome", 1)
	if err != nil {
		t.Fatalf("RenderRelayTemplate() error = %v", err)
	}

	if !strings.Contains(conf, "[program:relay-9222]") {
		t.Error("should contain [program:relay-9222]")
	}
	if !strings.Contains(conf, "command=/usr/local/bin/relay-wrapper 9222") {
		t.Error("should contain relay-wrapper command")
	}
	if !strings.Contains(conf, "autostart=true") {
		t.Error("should contain autostart=true")
	}
	if !strings.Contains(conf, "autorestart=true") {
		t.Error("should contain autorestart=true")
	}
	if !strings.Contains(conf, "priority=1") {
		t.Error("should contain priority=1")
	}
	if !strings.HasSuffix(conf, "\n") {
		t.Error("should end with newline")
	}
}

func TestRpmTemplateWithModules(t *testing.T) {
	fedora := testDistroDef("fedora")
	rpm := fedora.Formats["rpm"]
	ctx := &InstallContext{
		CacheMounts: rpm.CacheMounts,
		Packages:    []string{"valkey"},
		Modules:     []string{"valkey:remi-9.0"},
	}
	out, err := RenderTemplate("rpm-test", rpm.InstallTemplate, ctx)
	if err != nil {
		t.Fatalf("render error: %v", err)
	}

	if !strings.Contains(out, "dnf module reset -y valkey") {
		t.Error("should contain dnf module reset")
	}
	if !strings.Contains(out, "dnf module enable -y valkey:remi-9.0") {
		t.Error("should contain dnf module enable")
	}
	if !strings.Contains(out, "dnf install -y") {
		t.Error("should contain dnf install")
	}
	if !strings.Contains(out, "valkey") {
		t.Error("should contain package name")
	}
}

func TestPacTemplateBasic(t *testing.T) {
	arch := testDistroDef("archlinux")
	pac := arch.Formats["pac"]
	ctx := &InstallContext{
		CacheMounts: pac.CacheMounts,
		Packages:    []string{"neovim", "ripgrep"},
	}
	out, err := RenderTemplate("pac-test", pac.InstallTemplate, ctx)
	if err != nil {
		t.Fatalf("render error: %v", err)
	}
	if !strings.Contains(out, "pacman -Syu --noconfirm") {
		t.Error("should contain pacman -Syu --noconfirm")
	}
	if !strings.Contains(out, "neovim") {
		t.Error("should contain neovim")
	}
	if !strings.Contains(out, "/var/cache/pacman/pkg") {
		t.Error("should use pacman cache mount")
	}
}

func TestAurBuilderStageTemplate(t *testing.T) {
	builderCfg := testBuilderCfg()
	aurBuilder := builderCfg.Builder["aur"]
	ctx := &BuildStageContext{
		BuilderRef:  "ghcr.io/overthinkos/archlinux-builder:latest",
		StageName:   "my-tool-aur-build",
		UID:         1000,
		Home:        "/home/user",
		User:        "user",
		CacheMounts: aurBuilder.CacheMounts,
		Packages:    []string{"yay-bin", "neovim-nightly-bin"},
	}
	out, err := RenderTemplate("aur-stage-test", aurBuilder.StageTemplate, ctx)
	if err != nil {
		t.Fatalf("render error: %v", err)
	}
	if !strings.Contains(out, "FROM ghcr.io/overthinkos/archlinux-builder:latest AS my-tool-aur-build") {
		t.Error("should have correct FROM line")
	}
	if !strings.Contains(out, "yay -S --noconfirm --needed") {
		t.Error("should use yay to install")
	}
	if !strings.Contains(out, "yay-bin") {
		t.Error("should contain yay-bin package")
	}
}

func TestAurInstallTemplate(t *testing.T) {
	arch := testDistroDef("archlinux")
	aur := arch.Formats["aur"]
	ctx := &InstallContext{
		CacheMounts: aur.CacheMounts,
		StageName:   "my-tool-aur-build",
	}
	out, err := RenderTemplate("aur-install-test", aur.InstallTemplate, ctx)
	if err != nil {
		t.Fatalf("render error: %v", err)
	}
	if !strings.Contains(out, "COPY --from=my-tool-aur-build /tmp/aur-pkgs/") {
		t.Error("should COPY from AUR build stage")
	}
	if !strings.Contains(out, "pacman -U --noconfirm") {
		t.Error("should install with pacman -U")
	}
}

func TestBuilderRefForFormat(t *testing.T) {
	g := &Generator{
		Images: map[string]*ResolvedImage{
			"arch-img": {
				Builder: BuilderMap{"aur": "archlinux-builder", "pixi": "archlinux-builder"},
			},
			"archlinux-builder": {
				FullTag: "ghcr.io/overthinkos/archlinux-builder:2026.84.1200",
			},
			"no-aur-img": {
				Builder: BuilderMap{},
			},
		},
	}

	ref := g.builderRefForFormat("arch-img", "aur")
	if ref != "ghcr.io/overthinkos/archlinux-builder:2026.84.1200" {
		t.Errorf("builderRefForFormat(aur) = %q, want full tag", ref)
	}

	ref = g.builderRefForFormat("arch-img", "pixi")
	if ref != "ghcr.io/overthinkos/archlinux-builder:2026.84.1200" {
		t.Errorf("builderRefForFormat(pixi) = %q, want full tag", ref)
	}

	ref = g.builderRefForFormat("no-aur-img", "aur")
	if ref != "" {
		t.Errorf("builderRefForFormat(aur) = %q, want empty", ref)
	}
}
