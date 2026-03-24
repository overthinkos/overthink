package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	gokeepasslib "github.com/tobischo/gokeepasslib/v3"
	w "github.com/tobischo/gokeepasslib/v3/wrappers"
	"golang.org/x/term"
)

// KdbxStore implements CredentialStore using a KeePassXC .kdbx database file.
// This is the middle-ground backend for systems without a D-Bus Secret Service
// provider (headless servers, SSH sessions, containers).
type KdbxStore struct {
	path       string
	keyFile    string
	cachedPass string
	passOnce   sync.Once
	passErr    error
}

// kdbxAskPassword prompts for the .kdbx database password.
// Override in tests.
var kdbxAskPassword = defaultKdbxAskPassword

func defaultKdbxAskPassword() (string, error) {
	// 1. Environment variable (CI/automation)
	if pw := os.Getenv("OV_KDBX_PASSWORD"); pw != "" {
		return pw, nil
	}
	// 2. systemd-ask-password with kernel keyring caching
	if _, err := exec.LookPath("systemd-ask-password"); err == nil {
		return askPassword("ov-kdbx", "KeePass database password:")
	}
	// 3. Terminal fallback
	fmt.Fprint(os.Stderr, "KeePass database password: ")
	pw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("reading password: %w", err)
	}
	return string(pw), nil
}

// password returns the cached database password, prompting at most once per process.
func (k *KdbxStore) password() (string, error) {
	k.passOnce.Do(func() {
		k.cachedPass, k.passErr = kdbxAskPassword()
	})
	return k.cachedPass, k.passErr
}

// Probe checks that the .kdbx file exists. Does NOT prompt for password
// (used for auto-detection where we must not trigger interactive prompts).
func (k *KdbxStore) Probe() error {
	if k.path == "" {
		return fmt.Errorf("no kdbx path configured")
	}
	if _, err := os.Stat(k.path); err != nil {
		return fmt.Errorf("kdbx file: %w", err)
	}
	return nil
}

func (k *KdbxStore) Get(service, key string) (string, error) {
	pw, err := k.password()
	if err != nil {
		return "", err
	}
	db, err := openKdbx(k.path, pw, k.keyFile)
	if err != nil {
		return "", err
	}
	db.UnlockProtectedEntries()

	group := navigateToGroup(db.Content.Root, serviceToGroupPath(service))
	if group == nil {
		return "", nil
	}
	entry, _ := findKdbxEntry(group, key)
	if entry == nil {
		return "", nil
	}
	return entry.GetPassword(), nil
}

func (k *KdbxStore) Set(service, key, value string) error {
	pw, err := k.password()
	if err != nil {
		return err
	}
	db, err := openKdbx(k.path, pw, k.keyFile)
	if err != nil {
		return err
	}
	db.UnlockProtectedEntries()

	parts := serviceToGroupPath(service)
	group := findOrCreateKdbxGroup(db.Content.Root, parts)

	entry, idx := findKdbxEntry(group, key)
	if entry != nil {
		// Update existing entry password
		for i, v := range group.Entries[idx].Values {
			if v.Key == "Password" {
				group.Entries[idx].Values[i].Value.Content = value
				break
			}
		}
	} else {
		// Create new entry
		group.Entries = append(group.Entries, newKdbxEntry(key, value))
	}

	db.LockProtectedEntries()
	return saveKdbx(db, k.path)
}

func (k *KdbxStore) Delete(service, key string) error {
	pw, err := k.password()
	if err != nil {
		return err
	}
	db, err := openKdbx(k.path, pw, k.keyFile)
	if err != nil {
		return err
	}
	db.UnlockProtectedEntries()

	group := navigateToGroup(db.Content.Root, serviceToGroupPath(service))
	if group == nil {
		return nil
	}
	_, idx := findKdbxEntry(group, key)
	if idx < 0 {
		return nil
	}
	group.Entries = slices.Delete(group.Entries, idx, idx+1)

	db.LockProtectedEntries()
	return saveKdbx(db, k.path)
}

func (k *KdbxStore) List(service string) ([]string, error) {
	pw, err := k.password()
	if err != nil {
		return nil, err
	}
	db, err := openKdbx(k.path, pw, k.keyFile)
	if err != nil {
		return nil, err
	}
	db.UnlockProtectedEntries()

	group := navigateToGroup(db.Content.Root, serviceToGroupPath(service))
	if group == nil {
		return nil, nil
	}

	var keys []string
	for _, e := range group.Entries {
		keys = append(keys, e.GetTitle())
	}
	slices.Sort(keys)
	return keys, nil
}

func (k *KdbxStore) Name() string {
	return "kdbx"
}

// --- Helper functions ---

// serviceToGroupPath splits a service like "ov/vnc" into ["ov", "vnc"].
func serviceToGroupPath(service string) []string {
	return strings.Split(service, "/")
}

// openKdbx opens and decodes a .kdbx database file.
func openKdbx(path, password, keyFile string) (*gokeepasslib.Database, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening kdbx: %w", err)
	}
	defer f.Close()

	db := gokeepasslib.NewDatabase()
	if keyFile != "" {
		db.Credentials, err = gokeepasslib.NewPasswordAndKeyCredentials(password, keyFile)
		if err != nil {
			return nil, fmt.Errorf("creating kdbx credentials with key file: %w", err)
		}
	} else {
		db.Credentials = gokeepasslib.NewPasswordCredentials(password)
	}

	if err := gokeepasslib.NewDecoder(f).Decode(db); err != nil {
		return nil, fmt.Errorf("decoding kdbx: %w", err)
	}
	return db, nil
}

// saveKdbx writes the database to a temp file and atomically renames it.
func saveKdbx(db *gokeepasslib.Database, path string) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".ov-kdbx-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if err := gokeepasslib.NewEncoder(tmp).Encode(db); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("encoding kdbx: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("closing temp file: %w", err)
	}

	if err := os.Chmod(tmpPath, 0600); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("setting permissions: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("renaming temp file: %w", err)
	}
	return nil
}

// navigateToGroup traverses the group tree to find a group at the given path.
// Returns nil if any part of the path doesn't exist.
func navigateToGroup(root *gokeepasslib.RootData, parts []string) *gokeepasslib.Group {
	if root == nil || len(root.Groups) == 0 {
		return nil
	}
	current := &root.Groups[0] // KeePass root group
	for _, part := range parts {
		found := false
		for i := range current.Groups {
			if current.Groups[i].Name == part {
				current = &current.Groups[i]
				found = true
				break
			}
		}
		if !found {
			return nil
		}
	}
	return current
}

// findOrCreateKdbxGroup navigates to a group, creating missing groups along the way.
func findOrCreateKdbxGroup(root *gokeepasslib.RootData, parts []string) *gokeepasslib.Group {
	if root == nil {
		return nil
	}
	if len(root.Groups) == 0 {
		root.Groups = append(root.Groups, gokeepasslib.NewGroup())
		root.Groups[0].Name = "Root"
	}
	current := &root.Groups[0]
	for _, part := range parts {
		found := false
		for i := range current.Groups {
			if current.Groups[i].Name == part {
				current = &current.Groups[i]
				found = true
				break
			}
		}
		if !found {
			newGroup := gokeepasslib.NewGroup()
			newGroup.Name = part
			current.Groups = append(current.Groups, newGroup)
			current = &current.Groups[len(current.Groups)-1]
		}
	}
	return current
}

// findKdbxEntry finds an entry by title in a group. Returns nil, -1 if not found.
func findKdbxEntry(group *gokeepasslib.Group, title string) (*gokeepasslib.Entry, int) {
	for i := range group.Entries {
		if group.Entries[i].GetTitle() == title {
			return &group.Entries[i], i
		}
	}
	return nil, -1
}

// newKdbxEntry creates a new KeePass entry with the given title and password.
func newKdbxEntry(title, password string) gokeepasslib.Entry {
	entry := gokeepasslib.NewEntry()
	entry.Values = append(entry.Values,
		gokeepasslib.ValueData{Key: "Title", Value: gokeepasslib.V{Content: title}},
		gokeepasslib.ValueData{Key: "Password", Value: gokeepasslib.V{Content: password, Protected: w.NewBoolWrapper(true)}},
	)
	return entry
}

// CreateKdbxDatabase creates a new KDBX 4 database file with the given password.
func CreateKdbxDatabase(path, password string) error {
	db := gokeepasslib.NewDatabase()
	db.Credentials = gokeepasslib.NewPasswordCredentials(password)

	// Create root structure with ov service groups
	rootGroup := gokeepasslib.NewGroup()
	rootGroup.Name = "Root"

	ovGroup := gokeepasslib.NewGroup()
	ovGroup.Name = "ov"

	for _, svc := range []string{"vnc", "sunshine-user", "sunshine-password", "secret"} {
		g := gokeepasslib.NewGroup()
		g.Name = svc
		ovGroup.Groups = append(ovGroup.Groups, g)
	}
	rootGroup.Groups = append(rootGroup.Groups, ovGroup)
	db.Content.Root.Groups = []gokeepasslib.Group{rootGroup}

	db.LockProtectedEntries()
	return saveKdbx(db, path)
}

// ListAllKdbxEntries recursively lists all entries under a path prefix.
func ListAllKdbxEntries(path, password, keyFile, prefix string) ([]struct{ Service, Key, Value string }, error) {
	db, err := openKdbx(path, password, keyFile)
	if err != nil {
		return nil, err
	}
	db.UnlockProtectedEntries()

	var entries []struct{ Service, Key, Value string }
	if len(db.Content.Root.Groups) == 0 {
		return nil, nil
	}

	var walk func(group *gokeepasslib.Group, path string)
	walk = func(group *gokeepasslib.Group, groupPath string) {
		for _, e := range group.Entries {
			title := e.GetTitle()
			if title != "" {
				entries = append(entries, struct{ Service, Key, Value string }{
					Service: groupPath,
					Key:     title,
					Value:   e.GetPassword(),
				})
			}
		}
		for i := range group.Groups {
			childPath := group.Groups[i].Name
			if groupPath != "" {
				childPath = groupPath + "/" + group.Groups[i].Name
			}
			walk(&group.Groups[i], childPath)
		}
	}

	// Start from the root group's children (skip "Root" itself)
	root := &db.Content.Root.Groups[0]
	if prefix != "" {
		// Navigate to prefix first
		parts := strings.Split(prefix, "/")
		target := navigateToGroup(db.Content.Root, parts)
		if target == nil {
			return nil, nil
		}
		walk(target, prefix)
	} else {
		for i := range root.Groups {
			walk(&root.Groups[i], root.Groups[i].Name)
		}
	}
	return entries, nil
}
