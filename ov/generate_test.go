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
				HasRootYml: true,
			},
			"svc": {
				Name:     "svc",
				HasRoute: true,
				HasUserYml: true,
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
				HasUserYml: true,
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

func TestGenerateSupervisordFragments(t *testing.T) {
	tmpDir := t.TempDir()

	g := &Generator{
		BuildDir: tmpDir,
		Layers: map[string]*Layer{
			"python": {
				Name:       "python",
				HasRootYml: true,
			},
			"svc": {
				Name:           "svc",
				HasSupervisord: true,
				HasUserYml:     true,
				serviceConf:    "[program:svc]\ncommand=svc serve\nautostart=true\n",
			},
			"other": {
				Name:           "other",
				HasSupervisord: true,
				HasUserYml:     true,
				serviceConf:    "[program:other]\ncommand=other run",
			},
		},
	}

	err := g.generateSupervisordFragments("test-image", []string{"python", "svc", "other"})
	if err != nil {
		t.Fatalf("generateSupervisordFragments() error = %v", err)
	}

	// svc fragment should be at position 02 (index 1 + 1)
	data, err := os.ReadFile(tmpDir + "/test-image/supervisor/02-svc.conf")
	if err != nil {
		t.Fatalf("reading svc supervisor config: %v", err)
	}
	if !strings.Contains(string(data), "[program:svc]") {
		t.Error("svc fragment should contain [program:svc]")
	}
	if !strings.HasSuffix(string(data), "\n") {
		t.Error("supervisor config should end with newline")
	}

	// other supervisor config should be at position 03
	data, err = os.ReadFile(tmpDir + "/test-image/supervisor/03-other.conf")
	if err != nil {
		t.Fatalf("reading other supervisor config: %v", err)
	}
	if !strings.Contains(string(data), "[program:other]") {
		t.Error("other fragment should contain [program:other]")
	}
	if !strings.HasSuffix(string(data), "\n") {
		t.Error("supervisor config without trailing newline should get one added")
	}

	// python has no supervisord, should not have a supervisor config
	_, err = os.ReadFile(tmpDir + "/test-image/supervisor/01-python.conf")
	if err == nil {
		t.Error("python should not have a supervisor config")
	}
}

func TestGenerateRelaySupervisordFragments(t *testing.T) {
	tmpDir := t.TempDir()

	g := &Generator{
		BuildDir: tmpDir,
		Layers: map[string]*Layer{
			"socat": {
				Name:       "socat",
				HasRootYml: true,
			},
			"chrome": {
				Name:         "chrome",
				HasUserYml:   true,
				HasPortRelay: true,
				portRelay:    []int{9222},
				HasSupervisord: true,
				serviceConf:   "[program:chrome]\ncommand=chrome\nautostart=true\n",
			},
		},
	}

	err := g.generateSupervisordFragments("test-image", []string{"socat", "chrome"})
	if err != nil {
		t.Fatalf("generateSupervisordFragments() error = %v", err)
	}

	// Regular service config should exist
	data, err := os.ReadFile(tmpDir + "/test-image/supervisor/02-chrome.conf")
	if err != nil {
		t.Fatalf("reading chrome supervisor config: %v", err)
	}
	if !strings.Contains(string(data), "[program:chrome]") {
		t.Error("chrome fragment should contain [program:chrome]")
	}

	// Relay config should also exist
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

func TestGenerateRelayConf(t *testing.T) {
	conf := generateRelayConf(9222)

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

func TestWriteDnfInstallWithModules(t *testing.T) {
	g := &Generator{}
	var b strings.Builder
	g.writeDnfInstall(&b, &RpmConfig{
		Modules:  []string{"valkey:remi-9.0"},
		Packages: []string{"valkey"},
	})
	out := b.String()

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

func TestWriteDnfInstallWithReleaseRpm(t *testing.T) {
	g := &Generator{}
	var b strings.Builder
	g.writeDnfInstall(&b, &RpmConfig{
		Repos:    []RpmRepo{{Name: "remi", RPM: "https://example.com/remi-release.rpm"}},
		Packages: []string{"valkey"},
	})
	out := b.String()

	if !strings.Contains(out, `dnf install -y "https://example.com/remi-release.rpm"`) {
		t.Errorf("should contain release RPM install, got:\n%s", out)
	}
	// rpm-type repos should NOT emit --enable-repo
	if strings.Contains(out, "--enable-repo") {
		t.Error("rpm-type repos should not emit --enable-repo")
	}
}

func TestWriteDnfInstallWithReleaseRpmAndModules(t *testing.T) {
	g := &Generator{}
	var b strings.Builder
	g.writeDnfInstall(&b, &RpmConfig{
		Repos:    []RpmRepo{{Name: "remi", RPM: "https://example.com/remi-release.rpm"}},
		Modules:  []string{"valkey:remi-9.0"},
		Packages: []string{"valkey"},
	})
	out := b.String()

	// Verify order: release RPM → module enable → package install
	rpmIdx := strings.Index(out, "remi-release.rpm")
	resetIdx := strings.Index(out, "dnf module reset")
	enableIdx := strings.Index(out, "dnf module enable")
	installIdx := strings.LastIndex(out, "dnf install -y")

	if rpmIdx < 0 || resetIdx < 0 || enableIdx < 0 || installIdx < 0 {
		t.Fatalf("missing expected commands in output:\n%s", out)
	}
	if rpmIdx > resetIdx {
		t.Error("release RPM install should come before module reset")
	}
	if resetIdx > enableIdx {
		t.Error("module reset should come before module enable")
	}
	if enableIdx > installIdx {
		t.Error("module enable should come before package install")
	}
}

func TestWritePacmanInstallBasic(t *testing.T) {
	g := &Generator{}
	var b strings.Builder
	g.writePacmanInstall(&b, &PacConfig{
		Packages: []string{"neovim", "ripgrep"},
	})
	out := b.String()

	if !strings.Contains(out, "pacman -Syu --noconfirm") {
		t.Error("should contain pacman -Syu --noconfirm")
	}
	if !strings.Contains(out, "neovim") {
		t.Error("should contain neovim")
	}
	if !strings.Contains(out, "ripgrep") {
		t.Error("should contain ripgrep")
	}
	if !strings.Contains(out, "/var/cache/pacman/pkg") {
		t.Error("should use pacman cache mount")
	}
}

func TestWritePacmanInstallWithRepos(t *testing.T) {
	g := &Generator{}
	var b strings.Builder
	g.writePacmanInstall(&b, &PacConfig{
		Repos: []PacRepo{{
			Name:     "custom",
			Server:   "https://example.com/repo/$arch",
			SigLevel: "Optional TrustAll",
		}},
		Packages: []string{"custom-pkg"},
	})
	out := b.String()

	if !strings.Contains(out, "[custom]") {
		t.Error("should contain repo name in brackets")
	}
	if !strings.Contains(out, "https://example.com/repo/$arch") {
		t.Error("should contain repo server URL")
	}
	if !strings.Contains(out, "Optional TrustAll") {
		t.Error("should contain SigLevel")
	}
	if !strings.Contains(out, "/etc/pacman.conf") {
		t.Error("should append to pacman.conf")
	}
}

func TestWritePacmanInstallWithOptions(t *testing.T) {
	g := &Generator{}
	var b strings.Builder
	g.writePacmanInstall(&b, &PacConfig{
		Options:  []string{"--needed"},
		Packages: []string{"base-devel"},
	})
	out := b.String()

	if !strings.Contains(out, "--needed") {
		t.Error("should contain --needed option")
	}
}

func TestWriteAurBuildStage(t *testing.T) {
	g := &Generator{}
	var b strings.Builder
	g.writeAurBuildStage(&b, "my-tool", &AurConfig{
		Packages: []string{"yay-bin", "neovim-nightly-bin"},
	}, &ResolvedImage{UID: 1000, Home: "/home/user"}, "ghcr.io/overthinkos/archlinux-builder:latest")
	out := b.String()

	if !strings.Contains(out, "FROM ghcr.io/overthinkos/archlinux-builder:latest AS my-tool-aur-build") {
		t.Error("should have correct FROM line")
	}
	if !strings.Contains(out, "USER 1000") {
		t.Error("should switch to non-root user")
	}
	if !strings.Contains(out, "yay -S --noconfirm --needed") {
		t.Error("should use yay to install")
	}
	if !strings.Contains(out, "yay-bin") {
		t.Error("should contain yay-bin package")
	}
	if !strings.Contains(out, "neovim-nightly-bin") {
		t.Error("should contain neovim-nightly-bin package")
	}
	if !strings.Contains(out, "--builddir /tmp/aur-build") {
		t.Error("should use --builddir")
	}
	if !strings.Contains(out, "/tmp/aur-pkgs") {
		t.Error("should copy built packages to /tmp/aur-pkgs")
	}
}

func TestWriteAurInstallStep(t *testing.T) {
	g := &Generator{}
	var b strings.Builder
	g.writeAurInstallStep(&b, "my-tool")
	out := b.String()

	if !strings.Contains(out, "COPY --from=my-tool-aur-build /tmp/aur-pkgs/ /tmp/aur-pkgs/") {
		t.Error("should COPY from AUR build stage")
	}
	if !strings.Contains(out, "pacman -U --noconfirm /tmp/aur-pkgs/*.pkg.tar.zst") {
		t.Error("should install with pacman -U")
	}
	if !strings.Contains(out, "rm -rf /tmp/aur-pkgs") {
		t.Error("should clean up aur-pkgs")
	}
}

func TestWriteRootYmlPac(t *testing.T) {
	g := &Generator{}
	var b strings.Builder
	layer := &Layer{
		Name:         "test-layer",
		HasRootYml:   true,
		RootYmlTasks: []string{"all"},
	}
	img := &ResolvedImage{Pkg: "pac", Tags: []string{"all", "pac"}}
	g.writeRootYml(&b, "test-layer", layer, img)
	out := b.String()

	if !strings.Contains(out, "/var/cache/pacman/pkg") {
		t.Error("should use pacman cache mount for pac")
	}
	if strings.Contains(out, "libdnf5") {
		t.Error("should not use dnf cache for pac")
	}
	if strings.Contains(out, "/var/cache/apt") {
		t.Error("should not use apt cache for pac")
	}
	if !strings.Contains(out, "task -t root.yml all") {
		t.Error("should call tag-based tasks")
	}
}

func TestBuilderRefForFormat(t *testing.T) {
	g := &Generator{
		Images: map[string]*ResolvedImage{
			"arch-img": {
				Builders: BuildersMap{"aur": "archlinux-builder", "pixi": "archlinux-builder"},
			},
			"archlinux-builder": {
				FullTag: "ghcr.io/overthinkos/archlinux-builder:2026.84.1200",
			},
			"no-aur-img": {
				Builders: BuildersMap{},
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
