package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

// SecretsCmdGroup groups ov secrets subcommands for KeePass .kdbx management.
type SecretsCmdGroup struct {
	Init   SecretsInitCmd   `cmd:"" help:"Create a new .kdbx database for ov"`
	List   SecretsListCmd   `cmd:"" help:"List all ov entries in kdbx"`
	Get    SecretsGetCmd    `cmd:"" help:"Get a credential value"`
	Set    SecretsSetCmd    `cmd:"" help:"Set a credential"`
	Delete SecretsDeleteCmd `cmd:"" help:"Delete an entry"`
	Import SecretsImportCmd `cmd:"" help:"Import credentials from config/keyring into kdbx"`
	Export SecretsExportCmd `cmd:"" help:"Export kdbx entries to stdout"`
	Path   SecretsPathCmd   `cmd:"" help:"Print kdbx file path"`
}

// --- Init ---

// SecretsInitCmd creates a new .kdbx database for ov.
type SecretsInitCmd struct {
	DbPath string `arg:"" optional:"" help:"Path for new .kdbx file (default: ~/.config/ov/secrets.kdbx)"`
	Force  bool   `long:"force" help:"Overwrite existing database"`
}

func (c *SecretsInitCmd) Run() error {
	path := c.DbPath
	if path == "" {
		configDir, err := os.UserConfigDir()
		if err != nil {
			return fmt.Errorf("determining config directory: %w", err)
		}
		path = filepath.Join(configDir, "ov", "secrets.kdbx")
	}

	if _, err := os.Stat(path); err == nil && !c.Force {
		return fmt.Errorf("database already exists at %s (use --force to overwrite)", path)
	}

	// Prompt for password with confirmation
	pw1, err := promptPassword("New KeePass database password: ")
	if err != nil {
		return err
	}
	pw2, err := promptPassword("Confirm password: ")
	if err != nil {
		return err
	}
	if pw1 != pw2 {
		return fmt.Errorf("passwords do not match")
	}
	if pw1 == "" {
		return fmt.Errorf("password cannot be empty")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}

	if err := CreateKdbxDatabase(path, pw1); err != nil {
		return fmt.Errorf("creating database: %w", err)
	}

	// Auto-set the config key
	if err := SetConfigValue("secrets.kdbx_path", path); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not save kdbx path to config: %v\n", err)
	}

	fmt.Fprintf(os.Stderr, "Created KeePass database at %s\n", path)
	fmt.Fprintf(os.Stderr, "To activate: ov config set secret_backend kdbx\n")
	fmt.Fprintf(os.Stderr, "Or it will auto-activate when keyring is unavailable.\n")
	return nil
}

// --- List ---

// SecretsListCmd lists entries in the kdbx database.
type SecretsListCmd struct {
	Service string `arg:"" optional:"" help:"Service filter (e.g., ov/vnc)"`
}

func (c *SecretsListCmd) Run() error {
	path, keyFile, err := resolveAndValidateKdbxPath()
	if err != nil {
		return err
	}
	pw, err := kdbxAskPassword()
	if err != nil {
		return err
	}

	prefix := "ov"
	if c.Service != "" {
		prefix = c.Service
	}

	entries, err := ListAllKdbxEntries(path, pw, keyFile, prefix)
	if err != nil {
		return err
	}

	if len(entries) == 0 {
		fmt.Fprintln(os.Stderr, "No entries found.")
		return nil
	}

	for _, e := range entries {
		fmt.Printf("%s/%s\n", e.Service, e.Key)
	}
	return nil
}

// --- Get ---

// SecretsGetCmd gets a credential value from the kdbx database.
type SecretsGetCmd struct {
	Service string `arg:"" help:"Service name (e.g., ov/vnc)"`
	Key     string `arg:"" help:"Entry key (e.g., my-image)"`
}

func (c *SecretsGetCmd) Run() error {
	path, keyFile, err := resolveAndValidateKdbxPath()
	if err != nil {
		return err
	}
	pw, err := kdbxAskPassword()
	if err != nil {
		return err
	}

	store := &KdbxStore{path: path, keyFile: keyFile, cachedPass: pw}
	// Skip the prompt since we already have the password
	store.passOnce.Do(func() {})

	val, err := store.Get(c.Service, c.Key)
	if err != nil {
		return err
	}
	if val == "" {
		return fmt.Errorf("no entry found for %s/%s", c.Service, c.Key)
	}
	fmt.Println(val)
	return nil
}

// --- Set ---

// SecretsSetCmd sets a credential in the kdbx database.
type SecretsSetCmd struct {
	Service  string `arg:"" help:"Service name (e.g., ov/vnc)"`
	Key      string `arg:"" help:"Entry key (e.g., my-image)"`
	Value    string `arg:"" optional:"" help:"Value to set (omit to prompt securely)"`
	Generate bool   `long:"generate" help:"Generate random value and print to stdout"`
}

func (c *SecretsSetCmd) Run() error {
	path, keyFile, err := resolveAndValidateKdbxPath()
	if err != nil {
		return err
	}
	pw, err := kdbxAskPassword()
	if err != nil {
		return err
	}

	var value string
	if c.Generate {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			return fmt.Errorf("generating random value: %w", err)
		}
		value = hex.EncodeToString(b)
		fmt.Println(value)
	} else if c.Value != "" {
		value = c.Value
	} else {
		value, err = promptPassword("Secret value: ")
		if err != nil {
			return err
		}
		if value == "" {
			return fmt.Errorf("value cannot be empty")
		}
	}

	store := &KdbxStore{path: path, keyFile: keyFile, cachedPass: pw}
	store.passOnce.Do(func() {})

	if err := store.Set(c.Service, c.Key, value); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Stored %s/%s in %s\n", c.Service, c.Key, path)
	return nil
}

// --- Delete ---

// SecretsDeleteCmd deletes an entry from the kdbx database.
type SecretsDeleteCmd struct {
	Service string `arg:"" help:"Service name (e.g., ov/vnc)"`
	Key     string `arg:"" help:"Entry key"`
}

func (c *SecretsDeleteCmd) Run() error {
	path, keyFile, err := resolveAndValidateKdbxPath()
	if err != nil {
		return err
	}
	pw, err := kdbxAskPassword()
	if err != nil {
		return err
	}

	store := &KdbxStore{path: path, keyFile: keyFile, cachedPass: pw}
	store.passOnce.Do(func() {})

	if err := store.Delete(c.Service, c.Key); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Deleted %s/%s from %s\n", c.Service, c.Key, path)
	return nil
}

// --- Import ---

// SecretsImportCmd imports credentials from config.yml and keyring into the kdbx database.
type SecretsImportCmd struct {
	DryRun bool `long:"dry-run" help:"Show what would be imported without making changes"`
}

func (c *SecretsImportCmd) Run() error {
	// Collect credentials from config file
	cfg, err := LoadRuntimeConfig()
	if err != nil {
		return err
	}
	configEntries := PlaintextCredentialEntries(cfg)

	// Collect credentials from keyring (if available)
	var keyringEntries []struct{ Service, Key, Value string }
	keyringStore := &KeyringStore{}
	if keyringStore.Probe() == nil {
		for _, indexEntry := range cfg.KeyringKeys {
			parts := strings.SplitN(indexEntry, "/", 2)
			if len(parts) != 2 {
				continue
			}
			// Reconstruct service from index entry: "ov/vnc/my-image" → service="ov/vnc", key="my-image"
			lastSlash := strings.LastIndex(indexEntry, "/")
			if lastSlash < 0 {
				continue
			}
			service := indexEntry[:lastSlash]
			key := indexEntry[lastSlash+1:]
			val, err := keyringStore.Get(service, key)
			if err == nil && val != "" {
				keyringEntries = append(keyringEntries, struct{ Service, Key, Value string }{service, key, val})
			}
		}
	}

	total := len(configEntries) + len(keyringEntries)
	if total == 0 {
		fmt.Fprintln(os.Stderr, "No credentials found to import.")
		return nil
	}

	if c.DryRun {
		fmt.Fprintf(os.Stderr, "Found %d credential(s) to import:\n\n", total)
		for _, e := range configEntries {
			fmt.Fprintf(os.Stderr, "  %-45s (from config.yml)\n", e.Service+"/"+e.Key)
		}
		for _, e := range keyringEntries {
			fmt.Fprintf(os.Stderr, "  %-45s (from keyring)\n", e.Service+"/"+e.Key)
		}
		fmt.Fprintln(os.Stderr, "\nRun without --dry-run to import.")
		return nil
	}

	path, keyFile, err := resolveAndValidateKdbxPath()
	if err != nil {
		return err
	}
	pw, err := kdbxAskPassword()
	if err != nil {
		return err
	}

	store := &KdbxStore{path: path, keyFile: keyFile, cachedPass: pw}
	store.passOnce.Do(func() {})

	imported := 0
	fmt.Fprintf(os.Stderr, "Importing %d credential(s):\n", total)
	for _, e := range configEntries {
		if err := store.Set(e.Service, e.Key, e.Value); err != nil {
			fmt.Fprintf(os.Stderr, "  %-45s → FAILED: %v\n", e.Service+"/"+e.Key, err)
			continue
		}
		fmt.Fprintf(os.Stderr, "  %-45s → kdbx ✓ (from config)\n", e.Service+"/"+e.Key)
		imported++
	}
	for _, e := range keyringEntries {
		if err := store.Set(e.Service, e.Key, e.Value); err != nil {
			fmt.Fprintf(os.Stderr, "  %-45s → FAILED: %v\n", e.Service+"/"+e.Key, err)
			continue
		}
		fmt.Fprintf(os.Stderr, "  %-45s → kdbx ✓ (from keyring)\n", e.Service+"/"+e.Key)
		imported++
	}

	fmt.Fprintf(os.Stderr, "\nImported %d credential(s) into %s\n", imported, path)
	return nil
}

// --- Export ---

// SecretsExportCmd exports all entries from the kdbx database.
type SecretsExportCmd struct {
	Format string `long:"format" default:"yaml" enum:"yaml,json" help:"Output format (yaml, json)"`
}

func (c *SecretsExportCmd) Run() error {
	path, keyFile, err := resolveAndValidateKdbxPath()
	if err != nil {
		return err
	}
	pw, err := kdbxAskPassword()
	if err != nil {
		return err
	}

	fmt.Fprintln(os.Stderr, "WARNING: This exports plaintext credentials. Handle with care.")

	entries, err := ListAllKdbxEntries(path, pw, keyFile, "ov")
	if err != nil {
		return err
	}

	// Build nested map: service -> key -> value
	data := make(map[string]map[string]string)
	for _, e := range entries {
		if data[e.Service] == nil {
			data[e.Service] = make(map[string]string)
		}
		data[e.Service][e.Key] = e.Value
	}

	switch c.Format {
	case "json":
		out, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(out))
	default:
		out, err := yaml.Marshal(data)
		if err != nil {
			return err
		}
		fmt.Print(string(out))
	}
	return nil
}

// --- Path ---

// SecretsPathCmd prints the resolved kdbx file path.
type SecretsPathCmd struct{}

func (c *SecretsPathCmd) Run() error {
	path, _ := resolveKdbxPaths()
	if path == "" {
		configDir, err := os.UserConfigDir()
		if err != nil {
			return err
		}
		path = filepath.Join(configDir, "ov", "secrets.kdbx") + " (default, not yet created)"
	}
	fmt.Println(path)
	return nil
}

// --- Helpers ---

// resolveAndValidateKdbxPath resolves the kdbx path and validates it exists.
func resolveAndValidateKdbxPath() (path, keyFile string, err error) {
	path, keyFile = resolveKdbxPaths()
	if path == "" {
		return "", "", fmt.Errorf("no kdbx database configured.\nRun: ov secrets init  (or: ov config set secrets.kdbx_path /path/to/database.kdbx)")
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "", "", fmt.Errorf("kdbx database not found at %s.\nRun: ov secrets init %s", path, path)
	}
	return path, keyFile, nil
}

// promptPassword reads a password from the terminal without echo.
func promptPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	pw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("reading password: %w", err)
	}
	return string(pw), nil
}
