package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zalando/go-keyring"
)

// KeyringState represents the detected state of the system keyring.
type KeyringState int

const (
	KeyringUnchecked   KeyringState = iota // Not yet probed
	KeyringAvailable                       // Unlocked and functional
	KeyringLocked                          // Present but locked (requires unlock prompt)
	KeyringUnavailable                     // Not present or broken
)

// String returns a human-readable keyring state.
func (s KeyringState) String() string {
	switch s {
	case KeyringAvailable:
		return "available"
	case KeyringLocked:
		return "locked"
	case KeyringUnavailable:
		return "unavailable"
	default:
		return "unchecked"
	}
}

var (
	keyringStateMu  sync.Mutex
	keyringStateVal KeyringState
)

// GetKeyringState returns the last detected keyring state.
func GetKeyringState() KeyringState {
	keyringStateMu.Lock()
	defer keyringStateMu.Unlock()
	return keyringStateVal
}

func setKeyringState(state KeyringState) {
	keyringStateMu.Lock()
	defer keyringStateMu.Unlock()
	keyringStateVal = state
}

// resetKeyringState resets state (for testing).
func resetKeyringState() {
	setKeyringState(KeyringUnchecked)
}

// KeyringStore implements CredentialStore using the system keyring
// (freedesktop Secret Service on Linux, Keychain on macOS).
type KeyringStore struct{}

const keyringProbeService = "ov/probe"
const keyringProbeKey = "__ov_keyring_probe__"
const keyringTimeout = 3 * time.Second

// Probe tests whether the system keyring is usable and unlocked.
// Uses a read-only check with a timeout to avoid hanging when the
// Secret Service requires an unlock prompt or is unresponsive.
func (k *KeyringStore) Probe() error {
	ch := make(chan error, 1)
	go func() {
		// Read-only probe: try to read a non-existent key.
		// ErrNotFound → keyring is unlocked and working.
		// Other error → keyring is broken/unavailable.
		// Hang → keyring is locked, waiting for unlock prompt.
		_, err := keyring.Get(keyringProbeService, keyringProbeKey)
		if err != nil && isKeyringNotFound(err) {
			ch <- nil
			return
		}
		if err != nil {
			ch <- err
			return
		}
		// Key unexpectedly exists (leftover), clean up.
		_ = keyring.Delete(keyringProbeService, keyringProbeKey)
		ch <- nil
	}()

	select {
	case err := <-ch:
		if err != nil {
			setKeyringState(KeyringUnavailable)
			return fmt.Errorf("keyring unavailable: %w", err)
		}
		setKeyringState(KeyringAvailable)
		return nil
	case <-time.After(keyringTimeout):
		setKeyringState(KeyringLocked)
		return fmt.Errorf("keyring is locked — unlock your keyring or run: ov config set secret_backend config")
	}
}

func (k *KeyringStore) Get(service, key string) (string, error) {
	if GetKeyringState() == KeyringLocked {
		return "", &KeyringLockedError{op: "get", service: service, key: key}
	}
	type result struct {
		val string
		err error
	}
	ch := make(chan result, 1)
	go func() {
		v, e := keyring.Get(service, key)
		ch <- result{v, e}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			if isKeyringNotFound(r.err) {
				return "", nil
			}
			return "", fmt.Errorf("keyring get %s/%s: %w", service, key, r.err)
		}
		return r.val, nil
	case <-time.After(keyringTimeout):
		setKeyringState(KeyringLocked)
		return "", &KeyringLockedError{op: "get", service: service, key: key}
	}
}

func (k *KeyringStore) Set(service, key, value string) error {
	if GetKeyringState() == KeyringLocked {
		return &KeyringLockedError{op: "set", service: service, key: key}
	}
	ch := make(chan error, 1)
	go func() {
		ch <- keyring.Set(service, key, value)
	}()
	select {
	case err := <-ch:
		if err != nil {
			return fmt.Errorf("keyring set %s/%s: %w", service, key, err)
		}
		return addKeyringIndex(service, key)
	case <-time.After(keyringTimeout):
		setKeyringState(KeyringLocked)
		return &KeyringLockedError{op: "set", service: service, key: key}
	}
}

func (k *KeyringStore) Delete(service, key string) error {
	if GetKeyringState() == KeyringLocked {
		return &KeyringLockedError{op: "delete", service: service, key: key}
	}
	ch := make(chan error, 1)
	go func() {
		ch <- keyring.Delete(service, key)
	}()
	select {
	case err := <-ch:
		if err != nil && !isKeyringNotFound(err) {
			return fmt.Errorf("keyring delete %s/%s: %w", service, key, err)
		}
		return removeKeyringIndex(service, key)
	case <-time.After(keyringTimeout):
		setKeyringState(KeyringLocked)
		return &KeyringLockedError{op: "delete", service: service, key: key}
	}
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
	if GetKeyringState() == KeyringLocked {
		return "keyring (locked)"
	}
	return "keyring"
}

// KeyringLockedError is returned when a keyring operation cannot proceed
// because the keyring is locked. Callers can check for this with
// errors.As to provide context-specific messages.
type KeyringLockedError struct {
	op      string // "get", "set", "delete"
	service string
	key     string
}

func (e *KeyringLockedError) Error() string {
	return fmt.Sprintf("keyring is locked (cannot %s %s/%s) — unlock your keyring or run: ov config set secret_backend config", e.op, e.service, e.key)
}

// IsKeyringLocked returns true if the error is a KeyringLockedError.
func IsKeyringLocked(err error) bool {
	var locked *KeyringLockedError
	return err != nil && errors.As(err, &locked)
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
