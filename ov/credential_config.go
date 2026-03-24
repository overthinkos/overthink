package main

import (
	"fmt"
	"slices"
)

// ConfigFileStore implements CredentialStore using the existing plaintext
// credential maps in ~/.config/ov/config.yml. This is the fallback backend
// for headless environments without a system keyring.
type ConfigFileStore struct{}

func (c *ConfigFileStore) Get(service, key string) (string, error) {
	cfg, err := LoadRuntimeConfig()
	if err != nil {
		return "", err
	}
	return lookupConfigCredential(cfg, service, key), nil
}

func (c *ConfigFileStore) Set(service, key, value string) error {
	cfg, err := LoadRuntimeConfig()
	if err != nil {
		return err
	}
	setConfigCredential(cfg, service, key, value)
	return SaveRuntimeConfig(cfg)
}

func (c *ConfigFileStore) Delete(service, key string) error {
	cfg, err := LoadRuntimeConfig()
	if err != nil {
		return err
	}
	deleteConfigCredential(cfg, service, key)
	return SaveRuntimeConfig(cfg)
}

func (c *ConfigFileStore) List(service string) ([]string, error) {
	cfg, err := LoadRuntimeConfig()
	if err != nil {
		return nil, err
	}
	m := configCredentialMap(cfg, service)
	if m == nil {
		return nil, nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys, nil
}

func (c *ConfigFileStore) Name() string {
	return "config"
}

// lookupConfigCredential reads a credential from the appropriate config map.
func lookupConfigCredential(cfg *RuntimeConfig, service, key string) string {
	m := configCredentialMap(cfg, service)
	if m == nil {
		return ""
	}
	return m[key]
}

// setConfigCredential writes a credential to the appropriate config map.
func setConfigCredential(cfg *RuntimeConfig, service, key, value string) {
	switch service {
	case CredServiceVNC:
		if cfg.VncPasswords == nil {
			cfg.VncPasswords = make(map[string]string)
		}
		cfg.VncPasswords[key] = value
	case CredServiceSunshineUser:
		if cfg.SunshineUsers == nil {
			cfg.SunshineUsers = make(map[string]string)
		}
		cfg.SunshineUsers[key] = value
	case CredServiceSunshinePassword:
		if cfg.SunshinePasswords == nil {
			cfg.SunshinePasswords = make(map[string]string)
		}
		cfg.SunshinePasswords[key] = value
	default:
		// Unknown service — store in VncPasswords as a generic fallback.
		// This shouldn't happen in practice.
		if cfg.VncPasswords == nil {
			cfg.VncPasswords = make(map[string]string)
		}
		cfg.VncPasswords[fmt.Sprintf("%s/%s", service, key)] = value
	}
}

// deleteConfigCredential removes a credential from the appropriate config map.
func deleteConfigCredential(cfg *RuntimeConfig, service, key string) {
	m := configCredentialMap(cfg, service)
	if m != nil {
		delete(m, key)
	}
}

// configCredentialMap returns the config map for a given service.
func configCredentialMap(cfg *RuntimeConfig, service string) map[string]string {
	switch service {
	case CredServiceVNC:
		return cfg.VncPasswords
	case CredServiceSunshineUser:
		return cfg.SunshineUsers
	case CredServiceSunshinePassword:
		return cfg.SunshinePasswords
	default:
		return nil
	}
}

// HasPlaintextCredentials returns the number of plaintext credentials
// currently stored in config.yml credential maps.
func HasPlaintextCredentials(cfg *RuntimeConfig) int {
	return len(cfg.VncPasswords) + len(cfg.SunshineUsers) + len(cfg.SunshinePasswords)
}

// PlaintextCredentialEntries returns all plaintext credential entries as
// service/key pairs for migration or audit purposes.
func PlaintextCredentialEntries(cfg *RuntimeConfig) []struct{ Service, Key, Value string } {
	var entries []struct{ Service, Key, Value string }
	for k, v := range cfg.VncPasswords {
		entries = append(entries, struct{ Service, Key, Value string }{CredServiceVNC, k, v})
	}
	for k, v := range cfg.SunshineUsers {
		entries = append(entries, struct{ Service, Key, Value string }{CredServiceSunshineUser, k, v})
	}
	for k, v := range cfg.SunshinePasswords {
		entries = append(entries, struct{ Service, Key, Value string }{CredServiceSunshinePassword, k, v})
	}
	return entries
}
