package main

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestIsDisposableFields — the one-liner invariant. lifecycle never
// influences the result. This test is the wall against anyone later
// "helpfully" adding a derivation shortcut.
func TestIsDisposableFields(t *testing.T) {
	cases := []struct {
		name       string
		disposable bool
		lifecycle  string
		want       bool
	}{
		{"default zero", false, "", false},
		{"explicit true, no lifecycle", true, "", true},
		{"explicit true with prod lifecycle", true, "prod", true},
		{"explicit false, dev lifecycle — NOT disposable", false, "dev", false},
		{"explicit false, scratch lifecycle — NOT disposable", false, "scratch", false},
		{"explicit false, test lifecycle — NOT disposable", false, "test", false},
		{"explicit false, custom tag — NOT disposable", false, "demo", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IsDisposableFields(tc.disposable, tc.lifecycle)
			if got != tc.want {
				t.Errorf("IsDisposableFields(%v, %q) = %v, want %v",
					tc.disposable, tc.lifecycle, got, tc.want)
			}
		})
	}
}

// TestVmSpecRoundTrip — verify the fields round-trip through
// yaml.v3, and that the IsDisposable / LifecycleTag methods read
// the literal values.
func TestVmSpec_DisposableRoundTrip(t *testing.T) {
	yamlStr := `
source:
  kind: cloud_image
disposable: true
lifecycle: dev
`
	var s VmSpec
	if err := yaml.Unmarshal([]byte(yamlStr), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !s.IsDisposable() {
		t.Error("VmSpec.IsDisposable() = false; want true (explicit)")
	}
	if got := s.LifecycleTag(); got != "dev" {
		t.Errorf("VmSpec.LifecycleTag() = %q; want %q", got, "dev")
	}

	// Re-marshal and confirm both fields survive.
	out, err := yaml.Marshal(&s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	outStr := string(out)
	for _, need := range []string{"disposable: true", "lifecycle: dev"} {
		if !strings.Contains(outStr, need) {
			t.Errorf("round-trip dropped %q; output:\n%s", need, out)
		}
	}
}

// TestVmSpec_LifecycleAloneDoesNotAuthorize — the critical
// anti-derivation regression test. `lifecycle: dev` alone must
// leave IsDisposable() false. This is the test that breaks if
// someone reintroduces derivation.
func TestVmSpec_LifecycleAloneDoesNotAuthorize(t *testing.T) {
	yamlStr := `
source:
  kind: cloud_image
lifecycle: dev
`
	var s VmSpec
	if err := yaml.Unmarshal([]byte(yamlStr), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if s.IsDisposable() {
		t.Fatal("VmSpec{Lifecycle: dev}.IsDisposable() = true; want false. " +
			"Lifecycle must NEVER authorize disposability on its own. " +
			"If this test fails, someone reintroduced derivation logic — revert that.")
	}
}

// TestDeployImageConfig_DisposableRoundTrip — same invariants for
// the container-deploy side.
func TestDeployImageConfig_DisposableRoundTrip(t *testing.T) {
	yamlStr := `
disposable: true
lifecycle: dev
`
	var c DeployImageConfig
	if err := yaml.Unmarshal([]byte(yamlStr), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !c.IsDisposable() {
		t.Error("DeployImageConfig.IsDisposable() = false; want true")
	}
	if got := c.LifecycleTag(); got != "dev" {
		t.Errorf("LifecycleTag = %q; want dev", got)
	}
}

// TestDeployImageConfig_LifecycleAloneDoesNotAuthorize — container
// mirror of the critical anti-derivation test.
func TestDeployImageConfig_LifecycleAloneDoesNotAuthorize(t *testing.T) {
	yamlStr := `lifecycle: dev`
	var c DeployImageConfig
	if err := yaml.Unmarshal([]byte(yamlStr), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if c.IsDisposable() {
		t.Fatal("DeployImageConfig{Lifecycle: dev}.IsDisposable() = true; want false.")
	}
}

// TestMultipleInstances_IndependentFlags — two deploy entries for
// the same image with different disposable values must behave
// independently (the multi-instance requirement).
func TestMultipleInstances_IndependentFlags(t *testing.T) {
	yamlStr := `
provides:
  registry: localhost
images:
  fedora-coder:
    lifecycle: prod
  fedora-coder-dev:
    lifecycle: dev
  fedora-coder-qa:
    lifecycle: qa
    disposable: true
  fedora-coder-scratch:
    disposable: true
`
	var cfg DeployConfig
	if err := yaml.Unmarshal([]byte(yamlStr), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	tests := []struct {
		key  string
		want bool
	}{
		{"fedora-coder", false},         // lifecycle: prod, no disposable → false
		{"fedora-coder-dev", false},     // lifecycle: dev, no disposable → STILL false
		{"fedora-coder-qa", true},       // explicit disposable: true
		{"fedora-coder-scratch", true},  // explicit disposable: true
	}
	for _, tc := range tests {
		e, ok := cfg.Images[tc.key]
		if !ok {
			t.Fatalf("image %q missing", tc.key)
		}
		if got := e.IsDisposable(); got != tc.want {
			t.Errorf("%s.IsDisposable() = %v; want %v", tc.key, got, tc.want)
		}
	}
}

