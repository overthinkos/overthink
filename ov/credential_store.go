package main

import (
	"fmt"
	"os"
	"sync"
)

// CredentialStore abstracts secret storage backends (keyring, config file, etc.).
type CredentialStore interface {
	// Get retrieves a secret. Returns ("", nil) if not found.
	Get(service, key string) (string, error)
	// Set stores a secret.
	Set(service, key, value string) error
	// Delete removes a secret.
	Delete(service, key string) error
	// List returns all keys for a service.
	List(service string) ([]string, error)
	// Name returns the backend name for source attribution ("keyring" or "config").
	Name() string
}

// Credential service name.
const (
	CredServiceVNC = "ov/vnc"
)

var (
	defaultStoreOnce sync.Once
	defaultStoreVal  CredentialStore
	storeInfoOnce    sync.Once
)

// DefaultCredentialStore returns the active credential store based on the
// secret_backend config key. It probes the keyring on first call when
// backend is "auto" and caches the result.
func DefaultCredentialStore() CredentialStore {
	defaultStoreOnce.Do(func() {
		backend := resolveSecretBackend()
		switch backend {
		case "keyring":
			store := &KeyringStore{}
			if err := store.Probe(); err != nil {
				fmt.Fprintf(os.Stderr, "ERROR: secret_backend is 'keyring' but system keyring is not available: %v\n", err)
				fmt.Fprintf(os.Stderr, "Falling back to config file. Fix the keyring or run: ov config set secret_backend config\n")
				defaultStoreVal = &ConfigFileStore{}
				return
			}
			defaultStoreVal = store
		case "kdbx":
			path, keyFile := resolveKdbxPaths()
			if path == "" {
				fmt.Fprintf(os.Stderr, "ERROR: secret_backend is 'kdbx' but secrets.kdbx_path is not configured.\n")
				fmt.Fprintf(os.Stderr, "Run: ov secrets init  (or: ov config set secrets.kdbx_path /path/to/database.kdbx)\n")
				defaultStoreVal = &ConfigFileStore{}
				return
			}
			defaultStoreVal = &KdbxStore{path: path, keyFile: keyFile}
		case "config":
			defaultStoreVal = &ConfigFileStore{}
		default: // "auto" or ""
			// 1. Try system keyring (silent D-Bus probe)
			store := &KeyringStore{}
			if err := store.Probe(); err == nil {
				defaultStoreVal = store
				return
			}
			// 2. Try kdbx if configured and file exists (no password prompt)
			if path, keyFile := resolveKdbxPaths(); path != "" {
				kdbx := &KdbxStore{path: path, keyFile: keyFile}
				if err := kdbx.Probe(); err == nil {
					defaultStoreVal = kdbx
					return
				}
			}
			// 3. Fall back to config file
			defaultStoreVal = &ConfigFileStore{}
		}
	})
	return defaultStoreVal
}

// PrintStoreInfo prints a one-time informational message about which credential
// backend is active. Call this from commands that store or retrieve credentials.
func PrintStoreInfo() {
	storeInfoOnce.Do(func() {
		store := DefaultCredentialStore()
		switch store.Name() {
		case "keyring":
			fmt.Fprintf(os.Stderr, "Using system keyring for credential storage.\n")
			fmt.Fprintf(os.Stderr, "To force a specific backend: ov config set secret_backend keyring|config\n")
		case "kdbx":
			fmt.Fprintf(os.Stderr, "Using KeePass database for credential storage.\n")
			fmt.Fprintf(os.Stderr, "To force a specific backend: ov config set secret_backend keyring|kdbx|config\n")
		case "config":
			backend := resolveSecretBackend()
			if backend == "config" {
				// User explicitly chose config — no advisory needed
				return
			}
			fmt.Fprintf(os.Stderr, "System keyring not available (no D-Bus session bus).\n")
			fmt.Fprintf(os.Stderr, "Credentials will be stored in ~/.config/ov/config.yml (permissions: 0600).\n")
			fmt.Fprintf(os.Stderr, "To suppress this message: ov config set secret_backend config\n")
			fmt.Fprintf(os.Stderr, "For encrypted storage without a keyring: ov secrets init\n")
		}
	})
}

// ResolveCredential checks env var, then the credential store chain.
// Returns the value and its source ("env", "keyring", "config", or "default").
func ResolveCredential(envVar, service, key, defaultVal string) (value, source string) {
	if envVar != "" {
		if v := os.Getenv(envVar); v != "" {
			return v, "env"
		}
	}

	store := DefaultCredentialStore()
	if v, err := store.Get(service, key); err == nil && v != "" {
		return v, store.Name()
	}

	// If the primary store is keyring or kdbx, also check config file as fallback
	// (for credentials not yet migrated)
	if store.Name() == "keyring" || store.Name() == "kdbx" {
		fallback := &ConfigFileStore{}
		if v, err := fallback.Get(service, key); err == nil && v != "" {
			return v, "config"
		}
	}

	return defaultVal, "default"
}

// resolveSecretBackend reads the secret_backend setting from env or config.
func resolveSecretBackend() string {
	if v := os.Getenv("OV_SECRET_BACKEND"); v != "" {
		return v
	}
	cfg, err := LoadRuntimeConfig()
	if err != nil {
		return "auto"
	}
	if cfg.SecretBackend != "" {
		return cfg.SecretBackend
	}
	return "auto"
}

// resolveKdbxPaths reads the kdbx path and key file from env or config.
func resolveKdbxPaths() (path, keyFile string) {
	if v := os.Getenv("OV_KDBX_PATH"); v != "" {
		path = v
	} else {
		cfg, err := LoadRuntimeConfig()
		if err == nil {
			path = cfg.SecretsKdbxPath
		}
	}
	if v := os.Getenv("OV_KDBX_KEY_FILE"); v != "" {
		keyFile = v
	} else {
		cfg, err := LoadRuntimeConfig()
		if err == nil {
			keyFile = cfg.SecretsKdbxKeyFile
		}
	}
	return
}

// resetDefaultStore resets the cached default store (for testing).
func resetDefaultStore() {
	defaultStoreOnce = sync.Once{}
	defaultStoreVal = nil
	storeInfoOnce = sync.Once{}
}

// ConfigMigrateSecretsCmd migrates plaintext credentials from config.yml to the system keyring.
type ConfigMigrateSecretsCmd struct {
	DryRun bool `long:"dry-run" help:"Show what would be migrated without making changes"`
}

func (c *ConfigMigrateSecretsCmd) Run() error {
	cfg, err := LoadRuntimeConfig()
	if err != nil {
		return err
	}

	entries := PlaintextCredentialEntries(cfg)
	if len(entries) == 0 {
		fmt.Fprintln(os.Stderr, "No plaintext credentials found in config.yml. Nothing to migrate.")
		return nil
	}

	if c.DryRun {
		fmt.Fprintf(os.Stderr, "Found %d plaintext credential(s) in config.yml:\n\n", len(entries))
		for _, e := range entries {
			configKey := credentialConfigKey(e.Service, e.Key)
			fmt.Fprintf(os.Stderr, "  %-45s → would migrate to system keyring\n", configKey)
		}
		fmt.Fprintln(os.Stderr, "\nRun without --dry-run to migrate. A backup of config.yml will be created first.")
		return nil
	}

	// Verify keyring is usable
	keyring := &KeyringStore{}
	if err := keyring.Probe(); err != nil {
		fmt.Fprintln(os.Stderr, "Error: System keyring not available.")
		fmt.Fprintf(os.Stderr, "  Reason: %v\n\n", err)
		fmt.Fprintln(os.Stderr, "Cannot migrate credentials without a running keyring service.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Options:")
		fmt.Fprintln(os.Stderr, "  1. Start a keyring service (e.g., gnome-keyring-daemon --start)")
		fmt.Fprintln(os.Stderr, "  2. Install a Secret Service provider (gnome-keyring, kwalletd, keepassxc)")
		fmt.Fprintln(os.Stderr, "  3. Keep using config file: ov config set secret_backend config")
		return fmt.Errorf("keyring not available")
	}

	// Create backup
	configPath, err := RuntimeConfigPath()
	if err != nil {
		return err
	}
	backupPath := configPath + ".bak"
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("reading config for backup: %w", err)
	}
	if err := os.WriteFile(backupPath, data, 0600); err != nil {
		return fmt.Errorf("creating backup: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Backed up config.yml to %s\n\n", backupPath)

	// Migrate each credential
	fmt.Fprintf(os.Stderr, "Migrating %d credential(s):\n", len(entries))
	migrated := 0
	for _, e := range entries {
		configKey := credentialConfigKey(e.Service, e.Key)
		if err := keyring.Set(e.Service, e.Key, e.Value); err != nil {
			fmt.Fprintf(os.Stderr, "  %-45s → FAILED: %v\n", configKey, err)
			continue
		}
		fmt.Fprintf(os.Stderr, "  %-45s → keyring ✓\n", configKey)
		migrated++
	}

	if migrated == 0 {
		fmt.Fprintln(os.Stderr, "\nNo credentials were migrated. Config unchanged.")
		return nil
	}

	// Clear plaintext credential maps from config
	cfg.VncPasswords = nil
	if err := SaveRuntimeConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "\nWARNING: Failed to clear plaintext credentials from config: %v\n", err)
		fmt.Fprintln(os.Stderr, "Credentials were copied to keyring but plaintext copies remain.")
		return err
	}

	fmt.Fprintln(os.Stderr, "\nRemoved plaintext credentials from config.yml.")
	fmt.Fprintln(os.Stderr, "Migration complete. To verify: ov config list")
	fmt.Fprintf(os.Stderr, "To undo: cp %s %s\n", backupPath, configPath)
	return nil
}

// credentialConfigKey converts a service/key pair to the dot-notation config key.
func credentialConfigKey(service, key string) string {
	switch service {
	case CredServiceVNC:
		return "vnc.password." + key
	default:
		return service + "." + key
	}
}
