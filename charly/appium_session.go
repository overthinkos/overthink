package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// appium_session.go manages the persistent session-file pattern that lets
// multi-step Appium tests share one WebDriver session across separate `charly
// eval appium …` invocations. Each session-create writes a JSON file at
// ~/.cache/charly/appium/sessions/<image>[_<instance>].json; later
// find/click/install-app/screenshot/etc. load it to discover the session
// id + base URL; session-delete removes the file (and best-effort closes
// the remote session).
//
// Location rationale: XDG cache (not in-project, not ~/.local/share). The
// session id is host-local ephemeral state — different hosts run different
// containers with different ids, so Syncthing-syncing it would actively
// corrupt cross-host setups. Per the feedback_syncthing_state_in_project
// memory, ~/.local/share is the SPECIFIC anti-pattern, so we use ~/.cache.

// AppiumSession is the on-disk shape persisted between leaf invocations.
type AppiumSession struct {
	SessionID string                 `json:"session_id"`
	BaseURL   string                 `json:"base_url"`
	CreatedAt time.Time              `json:"created_at"`
	Image     string                 `json:"image"`
	Instance  string                 `json:"instance,omitempty"`
	Caps      map[string]interface{} `json:"caps,omitempty"`
}

// appiumSessionsDir returns ~/.cache/charly/appium/sessions, creating it on
// demand. Honours XDG_CACHE_HOME when set (per the XDG Base Directory
// Specification — tests can isolate by exporting it).
func appiumSessionsDir() (string, error) {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolving home dir for session cache: %w", err)
		}
		base = filepath.Join(home, ".cache")
	}
	dir := filepath.Join(base, "charly", "appium", "sessions")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("creating session cache dir %s: %w", dir, err)
	}
	return dir, nil
}

// appiumSessionPath returns the per-deploy session-file path. Instance
// suffix is "_<instance>" so the filename matches deploy-key conventions
// (filesystem-safe; no slashes).
func appiumSessionPath(image, instance string) (string, error) {
	dir, err := appiumSessionsDir()
	if err != nil {
		return "", err
	}
	name := image
	if instance != "" {
		name = image + "_" + instance
	}
	return filepath.Join(dir, name+".json"), nil
}

// loadAppiumSession reads the on-disk session for an image+instance.
// Returns (nil, nil) when the file doesn't exist — callers translate that
// to a "no session — run session-create first" error message at the call
// site for actionable context.
func loadAppiumSession(image, instance string) (*AppiumSession, error) {
	path, err := appiumSessionPath(image, instance)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading session file %s: %w", path, err)
	}
	var sess AppiumSession
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, fmt.Errorf("parsing session file %s: %w", path, err)
	}
	return &sess, nil
}

// saveAppiumSession writes (or overwrites) the session file for an
// image+instance with 0600 permissions (the session id is a bearer token
// for the running Appium server's API; not a passwordless credential, but
// still worth protecting against other-user read on shared hosts).
func saveAppiumSession(sess *AppiumSession) error {
	if sess == nil {
		return fmt.Errorf("saveAppiumSession: nil session")
	}
	path, err := appiumSessionPath(sess.Image, sess.Instance)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("writing session file %s: %w", path, err)
	}
	return nil
}

// deleteAppiumSession removes the session file (no error if absent).
func deleteAppiumSession(image, instance string) error {
	path, err := appiumSessionPath(image, instance)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing session file %s: %w", path, err)
	}
	return nil
}
