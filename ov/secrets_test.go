package main

import (
	"strings"
	"testing"
)

func TestCollectSecretsFromLabels(t *testing.T) {
	labelSecrets := []LabelSecret{
		{Name: "api-key", Target: "/run/secrets/api_key", Env: "API_KEY"},
		{Name: "vnc-password", Target: "/run/secrets/vnc_password"},
	}

	secrets := CollectSecretsFromLabels("my-image", labelSecrets)
	if len(secrets) != 2 {
		t.Fatalf("expected 2 secrets, got %d", len(secrets))
	}

	if secrets[0].Name != "ov-my-image-api-key" {
		t.Errorf("secret[0].Name = %q, want %q", secrets[0].Name, "ov-my-image-api-key")
	}
	if secrets[0].Target != "/run/secrets/api_key" {
		t.Errorf("secret[0].Target = %q", secrets[0].Target)
	}
	if secrets[0].Env != "API_KEY" {
		t.Errorf("secret[0].Env = %q", secrets[0].Env)
	}
	if secrets[0].SecretName != "api-key" {
		t.Errorf("secret[0].SecretName = %q", secrets[0].SecretName)
	}

	if secrets[1].Name != "ov-my-image-vnc-password" {
		t.Errorf("secret[1].Name = %q", secrets[1].Name)
	}
}

func TestSecretArgs(t *testing.T) {
	secrets := []CollectedSecret{
		{Name: "ov-img-pass", Target: "/run/secrets/pass"},
		{Name: "ov-img-user", Target: "/run/secrets/user"},
	}
	args := SecretArgs(secrets)
	if len(args) != 4 {
		t.Fatalf("expected 4 args, got %d: %v", len(args), args)
	}
	if args[0] != "--secret" || args[1] != "ov-img-pass,target=/run/secrets/pass" {
		t.Errorf("args[0:2] = %v", args[0:2])
	}
	if args[2] != "--secret" || args[3] != "ov-img-user,target=/run/secrets/user" {
		t.Errorf("args[2:4] = %v", args[2:4])
	}
}

func TestQuadletSecretDirectives(t *testing.T) {
	cfg := QuadletConfig{
		ImageName: "test-img",
		ImageRef:  "ghcr.io/test/test-img:latest",
		Workspace: "/tmp/workspace",
		Secrets: []CollectedSecret{
			{Name: "ov-test-img-api-key", Target: "/run/secrets/api_key"},
			{Name: "ov-test-img-db-pass", Target: "/run/secrets/db_pass"},
		},
	}

	content := generateQuadlet(cfg)
	if !strings.Contains(content, "Secret=ov-test-img-api-key,target=/run/secrets/api_key") {
		t.Error("missing Secret= directive for api-key")
	}
	if !strings.Contains(content, "Secret=ov-test-img-db-pass,target=/run/secrets/db_pass") {
		t.Error("missing Secret= directive for db-pass")
	}
}

func TestCredServiceForSecret(t *testing.T) {
	tests := []struct {
		envVar string
		want   string
	}{
		{"VNC_PASSWORD", CredServiceVNC},
		{"CUSTOM_SECRET", "ov/secret"},
	}
	for _, tt := range tests {
		got := credServiceForSecret(tt.envVar)
		if got != tt.want {
			t.Errorf("credServiceForSecret(%q) = %q, want %q", tt.envVar, got, tt.want)
		}
	}
}

func TestCredKeyForSecret(t *testing.T) {
	if got := credKeyForSecret("my-image", ""); got != "my-image" {
		t.Errorf("credKeyForSecret(my-image, '') = %q", got)
	}
	if got := credKeyForSecret("my-image", "work"); got != "my-image-work" {
		t.Errorf("credKeyForSecret(my-image, work) = %q", got)
	}
}
