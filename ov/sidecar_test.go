package main

import (
	"testing"
)

func TestLoadEmbeddedSidecarConfig(t *testing.T) {
	cfg, err := LoadEmbeddedSidecarConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}

	ts, ok := cfg.Sidecars["tailscale"]
	if !ok {
		t.Fatal("expected tailscale sidecar in embedded config")
	}
	if ts.Image != "ghcr.io/tailscale/tailscale:latest" {
		t.Errorf("image = %q, want ghcr.io/tailscale/tailscale:latest", ts.Image)
	}
	if ts.Env["TS_USERSPACE"] != "false" {
		t.Errorf("TS_USERSPACE = %q, want false", ts.Env["TS_USERSPACE"])
	}
	if ts.Env["TS_DEBUG_FIREWALL_MODE"] != "nftables" {
		t.Errorf("TS_DEBUG_FIREWALL_MODE = %q, want nftables", ts.Env["TS_DEBUG_FIREWALL_MODE"])
	}
	if len(ts.Volumes) != 1 || ts.Volumes[0].Name != "state" {
		t.Errorf("volumes = %v, want [{state /var/lib/tailscale}]", ts.Volumes)
	}
	if len(ts.Security.CapAdd) != 2 {
		t.Errorf("cap_add = %v, want [NET_ADMIN SYS_MODULE]", ts.Security.CapAdd)
	}
	if len(ts.Secrets) != 1 || ts.Secrets[0].Env != "TS_AUTHKEY" {
		t.Errorf("secrets = %v, want [{ts-authkey TS_AUTHKEY}]", ts.Secrets)
	}
}

func TestMergeSidecars_EnvMerge(t *testing.T) {
	base := map[string]SidecarDef{
		"tailscale": {
			Image: "tailscale:base",
			Env: map[string]string{
				"TS_STATE_DIR":  "/var/lib/tailscale",
				"TS_USERSPACE":  "false",
				"TS_ACCEPT_DNS": "true",
			},
		},
	}
	overlay := map[string]SidecarDef{
		"tailscale": {
			Env: map[string]string{
				"TS_HOSTNAME":  "my-app",
				"TS_USERSPACE": "true",
			},
		},
	}

	result := MergeSidecars(base, overlay)
	ts := result["tailscale"]

	if ts.Image != "tailscale:base" {
		t.Errorf("image = %q, want tailscale:base", ts.Image)
	}
	if ts.Env["TS_STATE_DIR"] != "/var/lib/tailscale" {
		t.Error("TS_STATE_DIR should be preserved from base")
	}
	if ts.Env["TS_HOSTNAME"] != "my-app" {
		t.Error("TS_HOSTNAME should be added from overlay")
	}
	if ts.Env["TS_USERSPACE"] != "true" {
		t.Error("TS_USERSPACE should be overridden by overlay")
	}
	if ts.Env["TS_ACCEPT_DNS"] != "true" {
		t.Error("TS_ACCEPT_DNS should be preserved from base")
	}
}

func TestMergeSidecars_NilInputs(t *testing.T) {
	if result := MergeSidecars(nil, nil); result != nil {
		t.Error("nil+nil should return nil")
	}
	result := MergeSidecars(nil, map[string]SidecarDef{"a": {Image: "x"}})
	if result["a"].Image != "x" {
		t.Error("nil base + overlay should return overlay")
	}
	result = MergeSidecars(map[string]SidecarDef{"a": {Image: "x"}}, nil)
	if result["a"].Image != "x" {
		t.Error("base + nil overlay should return copy of base")
	}
}

func TestResolveSidecars(t *testing.T) {
	defs := map[string]SidecarDef{
		"tailscale": {
			Image: "ts:latest",
			Env:   map[string]string{"TS_HOSTNAME": "test"},
			Volumes: []SidecarVolume{
				{Name: "state", Path: "/var/lib/tailscale"},
			},
			Secrets: []SidecarSecret{
				{Name: "ts-authkey", Env: "TS_AUTHKEY"},
			},
			Security: &SecurityConfig{
				CapAdd: []string{"NET_ADMIN"},
			},
		},
	}

	resolved := ResolveSidecars(defs, "my-app", "")
	if len(resolved) != 1 {
		t.Fatalf("expected 1 sidecar, got %d", len(resolved))
	}

	sc := resolved[0]
	if sc.Volumes[0].VolumeName != "ov-my-app-tailscale-state" {
		t.Errorf("volume name = %q, want ov-my-app-tailscale-state", sc.Volumes[0].VolumeName)
	}
	if sc.Secrets[0].Name != "ov-my-app-tailscale-ts-authkey" {
		t.Errorf("secret name = %q, want ov-my-app-tailscale-ts-authkey", sc.Secrets[0].Name)
	}
}

func TestResolveSidecarsForConfig(t *testing.T) {
	deploySidecars := map[string]SidecarDef{
		"tailscale": {
			Env: map[string]string{
				"TS_HOSTNAME": "my-app",
			},
		},
	}

	result, err := ResolveSidecarsForConfig(deploySidecars)
	if err != nil {
		t.Fatal(err)
	}

	ts, ok := result["tailscale"]
	if !ok {
		t.Fatal("tailscale should be resolved from embedded template")
	}
	if ts.Image != "ghcr.io/tailscale/tailscale:latest" {
		t.Errorf("image = %q, should come from embedded template", ts.Image)
	}
	if ts.Env["TS_HOSTNAME"] != "my-app" {
		t.Error("TS_HOSTNAME should be from deploy override")
	}
	if ts.Env["TS_STATE_DIR"] != "/var/lib/tailscale" {
		t.Error("TS_STATE_DIR should be from embedded template")
	}
}

func TestResolveSidecarsForConfig_Empty(t *testing.T) {
	result, err := ResolveSidecarsForConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Error("nil input should return nil")
	}
}

func TestSidecarEnvKeys(t *testing.T) {
	sidecars := map[string]SidecarDef{
		"tailscale": {
			Env: map[string]string{
				"TS_STATE_DIR": "/var/lib/tailscale",
			},
			Secrets: []SidecarSecret{
				{Name: "ts-authkey", Env: "TS_AUTHKEY"},
			},
		},
	}
	keys := SidecarEnvKeys(sidecars)
	if keys["TS_STATE_DIR"] != "tailscale" {
		t.Error("TS_STATE_DIR should map to tailscale")
	}
	if keys["TS_AUTHKEY"] != "tailscale" {
		t.Error("TS_AUTHKEY should map to tailscale")
	}
	if keys["TS_HOSTNAME"] != "tailscale" {
		t.Error("TS_HOSTNAME should map to tailscale (well-known TS_ prefix)")
	}
	if keys["TS_EXTRA_ARGS"] != "tailscale" {
		t.Error("TS_EXTRA_ARGS should map to tailscale (well-known)")
	}
}

func TestSortedSidecarEnv(t *testing.T) {
	env := map[string]string{
		"TS_USERSPACE":  "false",
		"TS_ACCEPT_DNS": "true",
		"TS_STATE_DIR":  "/var/lib/tailscale",
	}
	sorted := SortedSidecarEnv(env)
	if len(sorted) != 3 {
		t.Fatalf("expected 3, got %d", len(sorted))
	}
	if sorted[0] != "TS_ACCEPT_DNS=true" {
		t.Errorf("sorted[0] = %q, want TS_ACCEPT_DNS=true", sorted[0])
	}
}

func TestHasTailscaleSidecar(t *testing.T) {
	if HasTailscaleSidecar(nil) {
		t.Error("nil should return false")
	}
	if !HasTailscaleSidecar(map[string]SidecarDef{"tailscale": {}}) {
		t.Error("tailscale should return true")
	}
}
