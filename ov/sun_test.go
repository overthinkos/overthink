package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestParseSunPort(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		want    string
		wantErr bool
	}{
		{"standard localhost binding", "127.0.0.1:47990\n", "https://127.0.0.1:47990", false},
		{"all interfaces binding", "0.0.0.0:47990\n", "https://127.0.0.1:47990", false},
		{"random high port", "0.0.0.0:49990\n", "https://127.0.0.1:49990", false},
		{"ipv6 binding", "[::]:47990\n", "https://127.0.0.1:47990", false},
		{"multiple lines", "0.0.0.0:47990\n[::]:47990\n", "https://127.0.0.1:47990", false},
		{"no trailing newline", "127.0.0.1:47990", "https://127.0.0.1:47990", false},
		{"empty output", "", "", true},
		{"only whitespace", "  \n", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSunPort(tt.output)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseSunPort() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("parseSunPort() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSunshineClientBasicAuth(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "admin" || pass != "secret" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":true,"version":"2026.1.0","platform":"linux"}`))
	}))
	defer ts.Close()

	client := NewSunshineClient(ts.URL, "admin", "secret")
	// Use the test server's TLS client
	client.client = ts.Client()

	config, err := client.GetConfig()
	if err != nil {
		t.Fatalf("GetConfig() error: %v", err)
	}
	if config["version"] != "2026.1.0" {
		t.Errorf("version = %v, want 2026.1.0", config["version"])
	}

	// Test auth failure
	badClient := NewSunshineClient(ts.URL, "wrong", "creds")
	badClient.client = ts.Client()
	_, err = badClient.GetConfig()
	if err == nil {
		t.Error("expected auth error, got nil")
	}
}

func TestSunshineClientGetConfig(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/config" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != "GET" {
			t.Errorf("unexpected method: %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status":   true,
			"version":  "2026.321.25052",
			"platform": "linux",
			"encoder":  "nvenc",
			"capture":  "wlroots",
		})
	}))
	defer ts.Close()

	client := NewSunshineClient(ts.URL, "u", "p")
	client.client = ts.Client()

	config, err := client.GetConfig()
	if err != nil {
		t.Fatalf("GetConfig() error: %v", err)
	}
	if config["encoder"] != "nvenc" {
		t.Errorf("encoder = %v, want nvenc", config["encoder"])
	}
	if config["capture"] != "wlroots" {
		t.Errorf("capture = %v, want wlroots", config["capture"])
	}
}

func TestSunshineClientSubmitPIN(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/pin" || r.Method != "POST" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var payload map[string]string
		json.Unmarshal(body, &payload)
		if payload["pin"] != "1234" {
			t.Errorf("pin = %q, want 1234", payload["pin"])
		}
		if payload["name"] != "test-client" {
			t.Errorf("name = %q, want test-client", payload["name"])
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":true}`))
	}))
	defer ts.Close()

	client := NewSunshineClient(ts.URL, "u", "p")
	client.client = ts.Client()

	err := client.SubmitPIN("1234", "test-client")
	if err != nil {
		t.Fatalf("SubmitPIN() error: %v", err)
	}
}

func TestSunshineClientGetClients(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status": true,
			"named_certs": []map[string]string{
				{"uuid": "abc-123", "name": "Moonlight PC"},
				{"uuid": "def-456", "name": "Phone"},
			},
		})
	}))
	defer ts.Close()

	client := NewSunshineClient(ts.URL, "u", "p")
	client.client = ts.Client()

	clients, err := client.GetClients()
	if err != nil {
		t.Fatalf("GetClients() error: %v", err)
	}
	if len(clients) != 2 {
		t.Fatalf("got %d clients, want 2", len(clients))
	}
	if clients[0].UUID != "abc-123" {
		t.Errorf("client[0].UUID = %q, want abc-123", clients[0].UUID)
	}
	if clients[1].Name != "Phone" {
		t.Errorf("client[1].Name = %q, want Phone", clients[1].Name)
	}
}

func TestSunshineClientSetPassword(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/password" || r.Method != "POST" {
			t.Errorf("unexpected: %s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var payload map[string]string
		json.Unmarshal(body, &payload)
		if payload["newUsername"] != "admin" {
			t.Errorf("newUsername = %q, want admin", payload["newUsername"])
		}
		if payload["newPassword"] != "newpass" {
			t.Errorf("newPassword = %q, want newpass", payload["newPassword"])
		}
		if payload["confirmNewPassword"] != "newpass" {
			t.Errorf("confirmNewPassword = %q, want newpass", payload["confirmNewPassword"])
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":true}`))
	}))
	defer ts.Close()

	client := NewSunshineClient(ts.URL, "", "")
	client.client = ts.Client()

	err := client.SetPassword("", "", "admin", "newpass")
	if err != nil {
		t.Fatalf("SetPassword() error: %v", err)
	}
}

func TestPinValidation(t *testing.T) {
	tests := []struct {
		pin   string
		valid bool
	}{
		{"1234", true},
		{"0000", true},
		{"9999", true},
		{"123", false},
		{"12345", false},
		{"abcd", false},
		{"", false},
		{"12 4", false},
	}

	for _, tt := range tests {
		t.Run(tt.pin, func(t *testing.T) {
			got := pinRegex.MatchString(tt.pin)
			if got != tt.valid {
				t.Errorf("pinRegex.MatchString(%q) = %v, want %v", tt.pin, got, tt.valid)
			}
		})
	}
}

func TestSunCredentialResolution(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yml")

	orig := RuntimeConfigPath
	defer func() { RuntimeConfigPath = orig }()
	RuntimeConfigPath = func() (string, error) { return configPath, nil }

	// Use config backend to avoid D-Bus keyring probe hanging in CI/headless
	t.Setenv("OV_SECRET_BACKEND", "config")
	resetDefaultStore()
	defer resetDefaultStore()

	// No credentials set — should error.
	_, _, err := resolveSunCredentials("test-image", "")
	if err == nil {
		t.Error("expected error for missing credentials, got nil")
	}

	// Set credentials via config.
	cfg := &RuntimeConfig{
		SunshineUsers:     map[string]string{"test-image": "admin"},
		SunshinePasswords: map[string]string{"test-image": "secret"},
	}
	if err := SaveRuntimeConfig(cfg); err != nil {
		t.Fatalf("SaveRuntimeConfig() error: %v", err)
	}

	user, pass, err := resolveSunCredentials("test-image", "")
	if err != nil {
		t.Fatalf("resolveSunCredentials() error: %v", err)
	}
	if user != "admin" || pass != "secret" {
		t.Errorf("got user=%q pass=%q, want admin/secret", user, pass)
	}

	// Instance-specific should also fall back to image-level.
	user, pass, err = resolveSunCredentials("test-image", "prod")
	if err != nil {
		t.Fatalf("resolveSunCredentials(prod) error: %v", err)
	}
	if user != "admin" || pass != "secret" {
		t.Errorf("fallback: got user=%q pass=%q, want admin/secret", user, pass)
	}

	// Env vars should override.
	os.Setenv("SUNSHINE_USER", "envuser")
	os.Setenv("SUNSHINE_PASSWORD", "envpass")
	defer os.Unsetenv("SUNSHINE_USER")
	defer os.Unsetenv("SUNSHINE_PASSWORD")

	user, pass, err = resolveSunCredentials("test-image", "")
	if err != nil {
		t.Fatalf("resolveSunCredentials(env) error: %v", err)
	}
	if user != "envuser" || pass != "envpass" {
		t.Errorf("env override: got user=%q pass=%q, want envuser/envpass", user, pass)
	}
}

func TestSunCredentialConfigRoundtrip(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yml")

	orig := RuntimeConfigPath
	defer func() { RuntimeConfigPath = orig }()
	RuntimeConfigPath = func() (string, error) { return configPath, nil }

	// Use config backend to avoid D-Bus keyring probe hanging in CI/headless
	t.Setenv("OV_SECRET_BACKEND", "config")
	resetDefaultStore()
	defer resetDefaultStore()

	// Set via SetConfigValue.
	if err := SetConfigValue("sunshine.user.my-image", "admin"); err != nil {
		t.Fatalf("SetConfigValue(user) error: %v", err)
	}
	if err := SetConfigValue("sunshine.password.my-image", "secret123"); err != nil {
		t.Fatalf("SetConfigValue(password) error: %v", err)
	}

	// Get via GetConfigValue.
	user, err := GetConfigValue("sunshine.user.my-image")
	if err != nil {
		t.Fatalf("GetConfigValue(user) error: %v", err)
	}
	if user != "admin" {
		t.Errorf("user = %q, want admin", user)
	}

	pass, err := GetConfigValue("sunshine.password.my-image")
	if err != nil {
		t.Fatalf("GetConfigValue(password) error: %v", err)
	}
	if pass != "secret123" {
		t.Errorf("password = %q, want secret123", pass)
	}

	// Reset.
	if err := ResetConfigValue("sunshine.user.my-image"); err != nil {
		t.Fatalf("ResetConfigValue(user) error: %v", err)
	}
	user, _ = GetConfigValue("sunshine.user.my-image")
	if user != "" {
		t.Errorf("after reset: user = %q, want empty", user)
	}
}
