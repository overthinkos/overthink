package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestExtractMetadataFromLabels(t *testing.T) {
	orig := InspectLabels
	defer func() { InspectLabels = orig }()

	InspectLabels = func(engine, imageRef string) (map[string]string, error) {
		return map[string]string{
			LabelVersion:  "1",
			LabelImage:    "openclaw",
			LabelRegistry: "ghcr.io/overthinkos",
			LabelBootc:    "true",
			LabelUID:      "1000",
			LabelGID:      "1000",
			LabelUser:     "user",
			LabelHome:     "/home/user",
			LabelPorts:    `["18789:18789"]`,
			LabelVolumes:  `[{"name":"data","path":"/home/user/.openclaw"}]`,
			LabelAliases:  `[{"name":"openclaw","command":"openclaw"}]`,
			LabelBindMounts: `[{"name":"config","path":"/home/user/.config","encrypted":true}]`,
			LabelSecurity: `{"privileged":true,"cap_add":["NET_ADMIN"]}`,
			LabelNetwork:  "host",
			LabelTunnel:   `{"provider":"tailscale","public":[18789]}`,
			LabelDNS:     "openclaw.example.com",
			LabelAcmeEmail: "admin@example.com",
			LabelEnv:       `["API_KEY=secret"]`,
			LabelHooks:     `{"post_enable":"echo started","pre_remove":"echo stopping"}`,
			LabelVm:        `{"disk_size":"20 GiB","ram":"8G","cpus":4,"ssh_port":2222}`,
			LabelLibvirt:   `["<devices><channel/></devices>"]`,
			LabelRoutes:    `[{"host":"openclaw.localhost","port":18789}]`,
			LabelInit:                              "supervisord",
			"org.overthinkos.services.supervisord": `["traefik","testapi"]`,
			LabelEnvLayers: `{"CUDA_HOME":"/usr/local/cuda"}`,
			LabelPathAppend: `["/opt/bin"]`,
			LabelSkills:     "https://github.com/overthinkos/overthink-plugins/blob/main/ov-images/skills/openclaw/SKILL.md",
		}, nil
	}

	meta, err := ExtractMetadata("docker", "ghcr.io/overthinkos/openclaw:latest")
	if err != nil {
		t.Fatalf("ExtractMetadata() error = %v", err)
	}
	if meta == nil {
		t.Fatal("ExtractMetadata() returned nil")
	}

	if meta.Image != "openclaw" {
		t.Errorf("Image = %q, want %q", meta.Image, "openclaw")
	}
	if meta.Registry != "ghcr.io/overthinkos" {
		t.Errorf("Registry = %q, want %q", meta.Registry, "ghcr.io/overthinkos")
	}
	if !meta.Bootc {
		t.Error("Bootc = false, want true")
	}
	if meta.UID != 1000 {
		t.Errorf("UID = %d, want 1000", meta.UID)
	}
	if meta.GID != 1000 {
		t.Errorf("GID = %d, want 1000", meta.GID)
	}
	if meta.User != "user" {
		t.Errorf("User = %q, want %q", meta.User, "user")
	}
	if meta.Home != "/home/user" {
		t.Errorf("Home = %q, want %q", meta.Home, "/home/user")
	}
	if meta.Network != "host" {
		t.Errorf("Network = %q, want %q", meta.Network, "host")
	}
	if meta.DNS != "openclaw.example.com" {
		t.Errorf("DNS = %q, want %q", meta.DNS, "openclaw.example.com")
	}
	if meta.AcmeEmail != "admin@example.com" {
		t.Errorf("AcmeEmail = %q, want %q", meta.AcmeEmail, "admin@example.com")
	}

	wantPorts := []string{"18789:18789"}
	if !reflect.DeepEqual(meta.Ports, wantPorts) {
		t.Errorf("Ports = %v, want %v", meta.Ports, wantPorts)
	}

	wantVolumes := []VolumeMount{
		{VolumeName: "ov-openclaw-data", ContainerPath: "/home/user/.openclaw"},
	}
	if !reflect.DeepEqual(meta.Volumes, wantVolumes) {
		t.Errorf("Volumes = %v, want %v", meta.Volumes, wantVolumes)
	}

	wantAliases := []CollectedAlias{
		{Name: "openclaw", Command: "openclaw"},
	}
	if !reflect.DeepEqual(meta.Aliases, wantAliases) {
		t.Errorf("Aliases = %v, want %v", meta.Aliases, wantAliases)
	}

	// Bind mounts
	wantBindMounts := []LabelBindMount{
		{Name: "config", Path: "/home/user/.config", Encrypted: true},
	}
	if !reflect.DeepEqual(meta.BindMounts, wantBindMounts) {
		t.Errorf("BindMounts = %v, want %v", meta.BindMounts, wantBindMounts)
	}

	// Security
	if !meta.Security.Privileged {
		t.Error("Security.Privileged = false, want true")
	}
	wantCapAdd := []string{"NET_ADMIN"}
	if !reflect.DeepEqual(meta.Security.CapAdd, wantCapAdd) {
		t.Errorf("Security.CapAdd = %v, want %v", meta.Security.CapAdd, wantCapAdd)
	}

	// Tunnel
	if meta.Tunnel == nil {
		t.Fatal("Tunnel = nil, want non-nil")
	}
	if meta.Tunnel.Provider != "tailscale" {
		t.Errorf("Tunnel.Provider = %q, want %q", meta.Tunnel.Provider, "tailscale")
	}
	if len(meta.Tunnel.Public.Ports) != 1 || meta.Tunnel.Public.Ports[0] != 18789 {
		t.Errorf("Tunnel.Public.Ports = %v, want [18789]", meta.Tunnel.Public.Ports)
	}

	// Env
	wantEnv := []string{"API_KEY=secret"}
	if !reflect.DeepEqual(meta.Env, wantEnv) {
		t.Errorf("Env = %v, want %v", meta.Env, wantEnv)
	}

	// Hooks
	if meta.Hooks == nil {
		t.Fatal("Hooks = nil, want non-nil")
	}
	if meta.Hooks.PostEnable != "echo started" {
		t.Errorf("Hooks.PostEnable = %q, want %q", meta.Hooks.PostEnable, "echo started")
	}
	if meta.Hooks.PreRemove != "echo stopping" {
		t.Errorf("Hooks.PreRemove = %q, want %q", meta.Hooks.PreRemove, "echo stopping")
	}

	// VM config
	if meta.Vm == nil {
		t.Fatal("Vm = nil, want non-nil")
	}
	if meta.Vm.DiskSize != "20 GiB" {
		t.Errorf("Vm.DiskSize = %q, want %q", meta.Vm.DiskSize, "20 GiB")
	}
	if meta.Vm.Ram != "8G" {
		t.Errorf("Vm.Ram = %q, want %q", meta.Vm.Ram, "8G")
	}
	if meta.Vm.Cpus != 4 {
		t.Errorf("Vm.Cpus = %d, want 4", meta.Vm.Cpus)
	}
	if meta.Vm.SshPort != 2222 {
		t.Errorf("Vm.SshPort = %d, want 2222", meta.Vm.SshPort)
	}

	// Libvirt
	wantLibvirt := []string{"<devices><channel/></devices>"}
	if !reflect.DeepEqual(meta.Libvirt, wantLibvirt) {
		t.Errorf("Libvirt = %v, want %v", meta.Libvirt, wantLibvirt)
	}

	// Routes
	wantRoutes := []LabelRoute{{Host: "openclaw.localhost", Port: 18789}}
	if !reflect.DeepEqual(meta.Routes, wantRoutes) {
		t.Errorf("Routes = %v, want %v", meta.Routes, wantRoutes)
	}

	// Init system
	if meta.Init != "supervisord" {
		t.Errorf("Init = %q, want %q", meta.Init, "supervisord")
	}

	// Services for active init system
	wantServices := []string{"traefik", "testapi"}
	if !reflect.DeepEqual(meta.Services, wantServices) {
		t.Errorf("Services = %v, want %v", meta.Services, wantServices)
	}

	// Layer env vars
	wantEnvLayers := map[string]string{"CUDA_HOME": "/usr/local/cuda"}
	if !reflect.DeepEqual(meta.EnvLayers, wantEnvLayers) {
		t.Errorf("EnvLayers = %v, want %v", meta.EnvLayers, wantEnvLayers)
	}

	// Path append
	wantPathAppend := []string{"/opt/bin"}
	if !reflect.DeepEqual(meta.PathAppend, wantPathAppend) {
		t.Errorf("PathAppend = %v, want %v", meta.PathAppend, wantPathAppend)
	}

	// Skills
	wantSkills := "https://github.com/overthinkos/overthink-plugins/blob/main/ov-images/skills/openclaw/SKILL.md"
	if meta.Skills != wantSkills {
		t.Errorf("Skills = %q, want %q", meta.Skills, wantSkills)
	}
}

func TestExtractMetadataNoLabels(t *testing.T) {
	orig := InspectLabels
	defer func() { InspectLabels = orig }()

	InspectLabels = func(engine, imageRef string) (map[string]string, error) {
		return map[string]string{}, nil
	}

	meta, err := ExtractMetadata("docker", "ubuntu:24.04")
	if err != nil {
		t.Fatalf("ExtractMetadata() error = %v", err)
	}
	if meta != nil {
		t.Errorf("expected nil for image without org.overthinkos labels, got %+v", meta)
	}
}

func TestExtractMetadataOldV1LabelsRejected(t *testing.T) {
	orig := InspectLabels
	defer func() { InspectLabels = orig }()

	// Old org.overthink.* labels should not be recognized
	InspectLabels = func(engine, imageRef string) (map[string]string, error) {
		return map[string]string{
			"org.overthink.version": "1",
			"org.overthink.image":   "oldimage",
			"org.overthink.uid":     "1000",
			"org.overthink.gid":     "1000",
			"org.overthink.user":    "user",
			"org.overthink.home":    "/home/user",
		}, nil
	}

	meta, err := ExtractMetadata("docker", "oldimage:latest")
	if err != nil {
		t.Fatalf("ExtractMetadata() error = %v", err)
	}
	if meta != nil {
		t.Error("expected nil for old org.overthink.* labels, got non-nil — clean break means old labels are ignored")
	}
}

func TestExtractMetadataMinimalLabels(t *testing.T) {
	orig := InspectLabels
	defer func() { InspectLabels = orig }()

	InspectLabels = func(engine, imageRef string) (map[string]string, error) {
		return map[string]string{
			LabelVersion: "1",
			LabelImage:   "fedora",
			LabelUID:     "1000",
			LabelGID:     "1000",
			LabelUser:    "user",
			LabelHome:    "/home/user",
		}, nil
	}

	meta, err := ExtractMetadata("docker", "fedora:latest")
	if err != nil {
		t.Fatalf("ExtractMetadata() error = %v", err)
	}
	if meta == nil {
		t.Fatal("ExtractMetadata() returned nil")
	}

	if meta.Image != "fedora" {
		t.Errorf("Image = %q, want %q", meta.Image, "fedora")
	}
	if meta.Bootc {
		t.Error("Bootc = true, want false")
	}
	if len(meta.Ports) != 0 {
		t.Errorf("Ports = %v, want empty", meta.Ports)
	}
	if len(meta.Volumes) != 0 {
		t.Errorf("Volumes = %v, want empty", meta.Volumes)
	}
	if len(meta.Aliases) != 0 {
		t.Errorf("Aliases = %v, want empty", meta.Aliases)
	}
	if len(meta.BindMounts) != 0 {
		t.Errorf("BindMounts = %v, want empty", meta.BindMounts)
	}
	if meta.Tunnel != nil {
		t.Errorf("Tunnel = %v, want nil", meta.Tunnel)
	}
	if meta.Hooks != nil {
		t.Errorf("Hooks = %v, want nil", meta.Hooks)
	}
	if meta.Vm != nil {
		t.Errorf("Vm = %v, want nil", meta.Vm)
	}
}

func TestExtractMetadataPortRelay(t *testing.T) {
	orig := InspectLabels
	defer func() { InspectLabels = orig }()

	InspectLabels = func(engine, imageRef string) (map[string]string, error) {
		return map[string]string{
			LabelVersion:   "1",
			LabelImage:     "chrome",
			LabelUID:       "1000",
			LabelGID:       "1000",
			LabelUser:      "user",
			LabelHome:      "/home/user",
			LabelPortRelay: `[9222]`,
		}, nil
	}

	meta, err := ExtractMetadata("docker", "chrome:latest")
	if err != nil {
		t.Fatalf("ExtractMetadata() error = %v", err)
	}
	if meta == nil {
		t.Fatal("ExtractMetadata() returned nil")
	}

	wantRelay := []int{9222}
	if !reflect.DeepEqual(meta.PortRelay, wantRelay) {
		t.Errorf("PortRelay = %v, want %v", meta.PortRelay, wantRelay)
	}
}

func TestWriteLabelsPortRelay(t *testing.T) {
	g := &Generator{
		Config: &Config{
			Images: map[string]ImageConfig{
				"chrome-test": {
					Layers: []string{"chrome"},
				},
			},
		},
		Layers: map[string]*Layer{
			"chrome": {
				Name:           "chrome",
				HasUserYml:     true,
				PortRelayPorts: []int{9222},
			},
		},
	}

	img := &ResolvedImage{
		Name: "chrome-test",
		UID:  1000,
		GID:  1000,
		User: "user",
		Home: "/home/user",
	}

	var b strings.Builder
	g.writeLabels(&b, "chrome-test", []string{"chrome"}, img)
	output := b.String()

	// Check port_relay label is emitted
	if !strings.Contains(output, LabelPortRelay) {
		t.Errorf("missing %s label in output:\n%s", LabelPortRelay, output)
	}
	if !strings.Contains(output, "[9222]") {
		t.Errorf("missing port relay value [9222] in output:\n%s", output)
	}
}

func TestWriteLabelsEmitsLabels(t *testing.T) {
	tunnel := &TunnelYAML{Provider: "tailscale", Public: PortScope{Ports: []int{8080}}}
	g := &Generator{
		Config: &Config{
			Images: map[string]ImageConfig{
				"myapp": {
					Layers:     []string{"svc"},
					Ports:      []string{"8080:8080"},
					Tunnel:     tunnel,
					Env:        []string{"KEY=val"},
					BindMounts: []BindMountConfig{{Name: "data", Path: "/data", Encrypted: true}},
				},
			},
		},
		Layers: map[string]*Layer{
			"svc": {
				Name:        "svc",
				HasUserYml:  true,
				InitSystems: map[string]bool{"supervisord": true},
				serviceConf: "[program:svc]\ncommand=svc serve",
				HasVolumes:  true,
				volumes: []VolumeYAML{
					{Name: "data", Path: "/home/user/.myapp"},
				},
				HasAliases: true,
				aliases: []AliasYAML{
					{Name: "myapp-cli", Command: "myapp"},
				},
				HasRoute: true,
				route:    &RouteConfig{Host: "myapp.localhost", Port: "8080"},
				envConfig: &EnvConfig{
					Vars:       map[string]string{"APP_ENV": "prod"},
					PathAppend: []string{"/opt/myapp/bin"},
				},
			},
		},
	}

	img := &ResolvedImage{
		Name:     "myapp",
		Registry: "ghcr.io/test",
		Bootc:    true,
		UID:      1000,
		GID:      1000,
		User:     "user",
		Home:     "/home/user",
		Ports:    []string{"8080:8080"},
		Network:  "host",
		DNS:     "myapp.example.com",
		AcmeEmail: "admin@example.com",
		Vm:       &VmConfig{Ram: "4G", Cpus: 2},
		InitConfig: &InitConfig{
			Inits: map[string]*InitDef{
				"supervisord": {
					LayerFields: []string{"service"},
					LabelKey:    "org.overthinkos.services.supervisord",
				},
			},
		},
	}

	var b strings.Builder
	g.writeLabels(&b, "myapp", []string{"svc"}, img)
	output := b.String()

	// Check all expected labels are present
	checks := []struct {
		label string
		value string
	}{
		{LabelVersion, `"1"`},
		{LabelImage, `"myapp"`},
		{LabelRegistry, `"ghcr.io/test"`},
		{LabelBootc, `"true"`},
		{LabelUID, `"1000"`},
		{LabelGID, `"1000"`},
		{LabelUser, `"user"`},
		{LabelHome, `"/home/user"`},
		{LabelNetwork, `"host"`},
		{LabelDNS, `"myapp.example.com"`},
		{LabelAcmeEmail, `"admin@example.com"`},
	}

	for _, c := range checks {
		want := "LABEL " + c.label + "=" + c.value
		if !strings.Contains(output, want) {
			t.Errorf("missing label: %s\nin output:\n%s", want, output)
		}
	}

	// Check JSON labels
	jsonChecks := []struct {
		label string
		substr string
	}{
		{LabelPorts, `["8080:8080"]`},
		{LabelVolumes, `[{"name":"data","path":"/home/user/.myapp"}]`},
		{LabelAliases, `[{"name":"myapp-cli","command":"myapp"}]`},
		{LabelBindMounts, `[{"name":"data","path":"/data","encrypted":true}]`},
		{LabelTunnel, `"provider":"tailscale"`},
		{LabelEnv, `["KEY=val"]`},
		{LabelRoutes, `[{"host":"myapp.localhost","port":8080}]`},
		{LabelVm, `"ram":"4G"`},
		{LabelEnvLayers, `"APP_ENV":"prod"`},
		{LabelPathAppend, `["/opt/myapp/bin"]`},
		{"org.overthinkos.services.supervisord", `["svc"]`},
	}

	for _, c := range jsonChecks {
		labelLine := "LABEL " + c.label + "='"
		if !strings.Contains(output, labelLine) {
			t.Errorf("missing label %s in output:\n%s", c.label, output)
			continue
		}
		if !strings.Contains(output, c.substr) {
			t.Errorf("label %s missing expected content %q in output:\n%s", c.label, c.substr, output)
		}
	}
}

func TestWriteLabelsOmitsEmptyArrays(t *testing.T) {
	g := &Generator{
		Config: &Config{
			Images: map[string]ImageConfig{
				"minimal": {Layers: []string{"base"}},
			},
		},
		Layers: map[string]*Layer{
			"base": {
				Name:       "base",
				HasRootYml: true,
			},
		},
	}

	img := &ResolvedImage{
		Name: "minimal",
		UID:  1000,
		GID:  1000,
		User: "user",
		Home: "/home/user",
	}

	var b strings.Builder
	g.writeLabels(&b, "minimal", []string{"base"}, img)
	output := b.String()

	// Empty/nil fields should not be emitted
	omitted := []string{
		LabelPorts, LabelVolumes, LabelAliases, LabelRegistry,
		LabelBootc, LabelBindMounts, LabelSecurity, LabelNetwork,
		LabelTunnel, LabelDNS, LabelAcmeEmail, LabelEnv,
		LabelHooks, LabelVm, LabelLibvirt, LabelRoutes,
		LabelInit, LabelEnvLayers, LabelPathAppend,
		LabelPortRelay, LabelSkills,
	}
	for _, label := range omitted {
		if strings.Contains(output, label) {
			t.Errorf("should not emit %s for minimal image, got:\n%s", label, output)
		}
	}
}

func TestLabelRoundTrip(t *testing.T) {
	tunnel := &TunnelYAML{Provider: "cloudflare", Tunnel: "my-tunnel", Public: PortScope{Ports: []int{9090}}}
	g := &Generator{
		Config: &Config{
			Images: map[string]ImageConfig{
				"roundtrip": {
					Layers:  []string{"svc"},
					Ports:   []string{"9090:9090", "8080"},
					Aliases: []AliasConfig{{Name: "extra", Command: "extra-cmd"}},
					Tunnel:  tunnel,
					Env:     []string{"FOO=bar", "BAZ=qux"},
					BindMounts: []BindMountConfig{
						{Name: "secrets", Path: "/run/secrets", Encrypted: true},
					},
				},
			},
		},
		Layers: map[string]*Layer{
			"svc": {
				Name:       "svc",
				HasUserYml: true,
				HasVolumes: true,
				volumes: []VolumeYAML{
					{Name: "cache", Path: "/home/testuser/.cache/myapp"},
					{Name: "data", Path: "/home/testuser/.data"},
				},
				HasAliases: true,
				aliases: []AliasYAML{
					{Name: "svc-cli", Command: "svc-cmd"},
				},
				HasRoute: true,
				route:    &RouteConfig{Host: "svc.localhost", Port: "9090"},
				security: &SecurityConfig{CapAdd: []string{"SYS_PTRACE"}},
				hooks:    &HooksConfig{PostEnable: "echo hello"},
				envConfig: &EnvConfig{
					Vars:       map[string]string{"LANG": "en_US.UTF-8"},
					PathAppend: []string{"/opt/svc/bin"},
				},
				InitSystems:    map[string]bool{"supervisord": true},
				serviceConf:    "[program:svc]\ncommand=svc serve",
				systemServices: []string{"sshd", "docker"},
			},
		},
	}

	img := &ResolvedImage{
		Name:      "roundtrip",
		Registry:  "ghcr.io/test",
		Bootc:     true,
		UID:       1001,
		GID:       1002,
		User:      "testuser",
		Home:      "/home/testuser",
		Ports:     []string{"9090:9090", "8080"},
		Network:   "host",
		DNS:      "roundtrip.example.com",
		AcmeEmail: "test@example.com",
		Vm:        &VmConfig{Ram: "8G", Cpus: 4, SshPort: 2222, DiskSize: "30 GiB"},
		InitConfig: &InitConfig{
			Inits: map[string]*InitDef{
				"supervisord": {
					LayerFields: []string{"service"},
					LabelKey:    "org.overthinkos.services.supervisord",
				},
			},
		},
	}

	var b strings.Builder
	g.writeLabels(&b, "roundtrip", []string{"svc"}, img)
	output := b.String()

	// Parse labels from the generated output
	labels := parseLabelsFromContainerfile(output)

	// Mock InspectLabels to return parsed labels
	orig := InspectLabels
	defer func() { InspectLabels = orig }()
	InspectLabels = func(engine, imageRef string) (map[string]string, error) {
		return labels, nil
	}

	meta, err := ExtractMetadata("docker", "test:latest")
	if err != nil {
		t.Fatalf("ExtractMetadata() error = %v", err)
	}
	if meta == nil {
		t.Fatal("ExtractMetadata() returned nil")
	}

	// Scalar fields
	if meta.Image != "roundtrip" {
		t.Errorf("Image = %q, want %q", meta.Image, "roundtrip")
	}
	if meta.Registry != "ghcr.io/test" {
		t.Errorf("Registry = %q, want %q", meta.Registry, "ghcr.io/test")
	}
	if !meta.Bootc {
		t.Error("Bootc = false, want true")
	}
	if meta.UID != 1001 {
		t.Errorf("UID = %d, want 1001", meta.UID)
	}
	if meta.GID != 1002 {
		t.Errorf("GID = %d, want 1002", meta.GID)
	}
	if meta.Network != "host" {
		t.Errorf("Network = %q, want %q", meta.Network, "host")
	}
	if meta.DNS != "roundtrip.example.com" {
		t.Errorf("DNS = %q, want %q", meta.DNS, "roundtrip.example.com")
	}
	if meta.AcmeEmail != "test@example.com" {
		t.Errorf("AcmeEmail = %q, want %q", meta.AcmeEmail, "test@example.com")
	}

	// Ports
	wantPorts := []string{"9090:9090", "8080"}
	if !reflect.DeepEqual(meta.Ports, wantPorts) {
		t.Errorf("Ports = %v, want %v", meta.Ports, wantPorts)
	}

	// Volumes
	wantVolumes := []VolumeMount{
		{VolumeName: "ov-roundtrip-cache", ContainerPath: "/home/testuser/.cache/myapp"},
		{VolumeName: "ov-roundtrip-data", ContainerPath: "/home/testuser/.data"},
	}
	if !reflect.DeepEqual(meta.Volumes, wantVolumes) {
		t.Errorf("Volumes = %v, want %v", meta.Volumes, wantVolumes)
	}

	// Aliases: layer alias + image-level alias
	if len(meta.Aliases) != 2 {
		t.Fatalf("Aliases count = %d, want 2", len(meta.Aliases))
	}

	// Bind mounts
	wantBindMounts := []LabelBindMount{
		{Name: "secrets", Path: "/run/secrets", Encrypted: true},
	}
	if !reflect.DeepEqual(meta.BindMounts, wantBindMounts) {
		t.Errorf("BindMounts = %v, want %v", meta.BindMounts, wantBindMounts)
	}

	// Security (from layer)
	if !reflect.DeepEqual(meta.Security.CapAdd, []string{"SYS_PTRACE"}) {
		t.Errorf("Security.CapAdd = %v, want [SYS_PTRACE]", meta.Security.CapAdd)
	}

	// Tunnel
	if meta.Tunnel == nil {
		t.Fatal("Tunnel = nil, want non-nil")
	}
	if meta.Tunnel.Provider != "cloudflare" {
		t.Errorf("Tunnel.Provider = %q, want %q", meta.Tunnel.Provider, "cloudflare")
	}
	if len(meta.Tunnel.Public.Ports) != 1 || meta.Tunnel.Public.Ports[0] != 9090 {
		t.Errorf("Tunnel.Public.Ports = %v, want [9090]", meta.Tunnel.Public.Ports)
	}

	// Env
	wantEnv := []string{"FOO=bar", "BAZ=qux"}
	if !reflect.DeepEqual(meta.Env, wantEnv) {
		t.Errorf("Env = %v, want %v", meta.Env, wantEnv)
	}

	// Hooks
	if meta.Hooks == nil {
		t.Fatal("Hooks = nil, want non-nil")
	}
	if meta.Hooks.PostEnable != "echo hello" {
		t.Errorf("Hooks.PostEnable = %q, want %q", meta.Hooks.PostEnable, "echo hello")
	}

	// VM
	if meta.Vm == nil {
		t.Fatal("Vm = nil, want non-nil")
	}
	if meta.Vm.Ram != "8G" {
		t.Errorf("Vm.Ram = %q, want %q", meta.Vm.Ram, "8G")
	}
	if meta.Vm.Cpus != 4 {
		t.Errorf("Vm.Cpus = %d, want 4", meta.Vm.Cpus)
	}

	// Routes
	wantRoutes := []LabelRoute{{Host: "svc.localhost", Port: 9090}}
	if !reflect.DeepEqual(meta.Routes, wantRoutes) {
		t.Errorf("Routes = %v, want %v", meta.Routes, wantRoutes)
	}

	// Init system
	if meta.Init != "supervisord" {
		t.Errorf("Init = %q, want %q", meta.Init, "supervisord")
	}

	// Services for active init system (supervisord)
	wantSvcNames := []string{"svc"}
	if !reflect.DeepEqual(meta.Services, wantSvcNames) {
		t.Errorf("Services = %v, want %v", meta.Services, wantSvcNames)
	}

	// Layer env
	if meta.EnvLayers["LANG"] != "en_US.UTF-8" {
		t.Errorf("EnvLayers[LANG] = %q, want %q", meta.EnvLayers["LANG"], "en_US.UTF-8")
	}

	// Path append
	wantPath := []string{"/opt/svc/bin"}
	if !reflect.DeepEqual(meta.PathAppend, wantPath) {
		t.Errorf("PathAppend = %v, want %v", meta.PathAppend, wantPath)
	}
}

// parseLabelsFromContainerfile extracts LABEL directives from generated Containerfile text
func parseLabelsFromContainerfile(content string) map[string]string {
	labels := make(map[string]string)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "LABEL ") {
			continue
		}
		rest := strings.TrimPrefix(line, "LABEL ")
		eqIdx := strings.Index(rest, "=")
		if eqIdx < 0 {
			continue
		}
		key := rest[:eqIdx]
		value := rest[eqIdx+1:]

		// Remove quoting: "value" or 'value'
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		labels[key] = value
	}
	return labels
}

func TestLabelVolumeJSON(t *testing.T) {
	vol := LabelVolume{Name: "data", Path: "/home/user/.myapp"}
	data, err := json.Marshal(vol)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	want := `{"name":"data","path":"/home/user/.myapp"}`
	if string(data) != want {
		t.Errorf("json.Marshal(LabelVolume) = %s, want %s", data, want)
	}
}

func TestLabelBindMountJSON(t *testing.T) {
	bm := LabelBindMount{Name: "config", Path: "/home/user/.config", Encrypted: true}
	data, err := json.Marshal(bm)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	want := `{"name":"config","path":"/home/user/.config","encrypted":true}`
	if string(data) != want {
		t.Errorf("json.Marshal(LabelBindMount) = %s, want %s", data, want)
	}

	// Non-encrypted omits the field
	bm2 := LabelBindMount{Name: "data", Path: "/data"}
	data2, _ := json.Marshal(bm2)
	want2 := `{"name":"data","path":"/data"}`
	if string(data2) != want2 {
		t.Errorf("json.Marshal(LabelBindMount) = %s, want %s", data2, want2)
	}
}

func TestLabelRouteJSON(t *testing.T) {
	route := LabelRoute{Host: "app.localhost", Port: 8080}
	data, err := json.Marshal(route)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	want := `{"host":"app.localhost","port":8080}`
	if string(data) != want {
		t.Errorf("json.Marshal(LabelRoute) = %s, want %s", data, want)
	}
}
