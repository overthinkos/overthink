package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/zalando/go-keyring"
)

// KeyringStore implements CredentialStore using the system keyring
// (freedesktop Secret Service on Linux, Keychain on macOS).
type KeyringStore struct{}

const keyringProbeService = "ov/probe"
const keyringProbeKey = "__ov_keyring_probe__"

// Probe tests whether the system keyring is usable by performing a
// write/read/delete cycle with a test entry.
func (k *KeyringStore) Probe() error {
	testVal := "probe"
	if err := keyring.Set(keyringProbeService, keyringProbeKey, testVal); err != nil {
		return fmt.Errorf("keyring write: %w", err)
	}
	got, err := keyring.Get(keyringProbeService, keyringProbeKey)
	if err != nil {
		return fmt.Errorf("keyring read: %w", err)
	}
	if got != testVal {
		return fmt.Errorf("keyring roundtrip mismatch: wrote %q, got %q", testVal, got)
	}
	_ = keyring.Delete(keyringProbeService, keyringProbeKey)
	return nil
}

func (k *KeyringStore) Get(service, key string) (string, error) {
	val, err := keyring.Get(service, key)
	if err != nil {
		if isKeyringNotFound(err) {
			return "", nil
		}
		return "", fmt.Errorf("keyring get %s/%s: %w", service, key, err)
	}
	return val, nil
}

func (k *KeyringStore) Set(service, key, value string) error {
	if err := keyring.Set(service, key, value); err != nil {
		return fmt.Errorf("keyring set %s/%s: %w", service, key, err)
	}
	// Update shadow index in config.yml (key names only, no values)
	return addKeyringIndex(service, key)
}

func (k *KeyringStore) Delete(service, key string) error {
	err := keyring.Delete(service, key)
	if err != nil && !isKeyringNotFound(err) {
		return fmt.Errorf("keyring delete %s/%s: %w", service, key, err)
	}
	// Remove from shadow index
	return removeKeyringIndex(service, key)
}

// List returns all keys for a service from the shadow index.
// The go-keyring library does not support listing, so we maintain
// a key-name-only index in config.yml (no secret values stored).
func (k *KeyringStore) List(service string) ([]string, error) {
	cfg, err := LoadRuntimeConfig()
	if err != nil {
		return nil, err
	}
	prefix := service + "/"
	var keys []string
	for _, entry := range cfg.KeyringKeys {
		if strings.HasPrefix(entry, prefix) {
			keys = append(keys, strings.TrimPrefix(entry, prefix))
		}
	}
	return keys, nil
}

func (k *KeyringStore) Name() string {
	return "keyring"
}

// isKeyringNotFound checks whether the error indicates the key was not found.
func isKeyringNotFound(err error) bool {
	if err == nil {
		return false
	}
	// go-keyring returns keyring.ErrNotFound for missing entries
	return err == keyring.ErrNotFound || strings.Contains(err.Error(), "secret not found")
}

// addKeyringIndex adds a service/key entry to the shadow index in config.yml.
func addKeyringIndex(service, key string) error {
	cfg, err := LoadRuntimeConfig()
	if err != nil {
		return err
	}
	entry := service + "/" + key
	if slices.Contains(cfg.KeyringKeys, entry) {
		return nil
	}
	cfg.KeyringKeys = append(cfg.KeyringKeys, entry)
	slices.Sort(cfg.KeyringKeys)
	return SaveRuntimeConfig(cfg)
}

// removeKeyringIndex removes a service/key entry from the shadow index.
func removeKeyringIndex(service, key string) error {
	cfg, err := LoadRuntimeConfig()
	if err != nil {
		return err
	}
	entry := service + "/" + key
	idx := slices.Index(cfg.KeyringKeys, entry)
	if idx < 0 {
		return nil
	}
	cfg.KeyringKeys = slices.Delete(cfg.KeyringKeys, idx, idx+1)
	return SaveRuntimeConfig(cfg)
}
