package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// MigrateTailscaleSecretsCmd implements `ov migrate tailscale-secrets`.
//
// Background: the 2026-05-12 incident surfaced that the tailscale sidecar's
// `secret: [{name: ts-authkey, env: TS_AUTHKEY}]` uses a flat env var name,
// so a host with TWO tailnets can only store ONE auth-key — silently picking
// the wrong tailnet for any deploy. The fix (2026-05-13) introduces a
// per-tailnet env var convention `TS_AUTHKEY_<TAILNET-NORMALIZED>` where
// TAILNET-NORMALIZED is the MagicDNS suffix uppercased with non-alphanumeric
// chars replaced by '_'. The sidecar template's `env_from:` field renders
// against `parameter.tailnet` to pick the right env var per deploy.
//
// This command does the one-shot migration:
//  1. Read `.secrets` (via `ov secrets gpg show`) for the legacy `TS_AUTHKEY` entry.
//  2. Prompt for the tailnet suffix this key belongs to (skipped with --tailnet).
//  3. Write the value to `TS_AUTHKEY_<normalized>` via `ov secrets gpg set`.
//  4. Optionally delete the legacy `TS_AUTHKEY` entry (--delete-legacy).
//  5. Scan `~/.config/ov/deploy.yml` for `sidecars.tailscale:` entries lacking
//     `parameter.tailnet:` and emit a per-entry warning (does NOT auto-write
//     the parameter — the operator must choose which tailnet each deploy
//     uses).
//
// Idempotent: re-running after migration is a no-op (legacy entry already
// renamed; warnings repeat).
type MigrateTailscaleSecretsCmd struct {
	Tailnet       string `long:"tailnet" help:"MagicDNS suffix the legacy TS_AUTHKEY belongs to (e.g. armadillo-quail.ts.net). Empty = prompt interactively."`
	DeleteLegacy  bool   `long:"delete-legacy" help:"After migration, delete the legacy TS_AUTHKEY entry from .secrets. Default: preserve."`
	SecretsFile   string `long:"secrets-file" default:".secrets" help:"Path to GPG-encrypted .secrets file"`
	DryRun        bool   `long:"dry-run" help:"Print what would change without writing"`
}

func (c *MigrateTailscaleSecretsCmd) Run() error {
	// Step 1: read legacy TS_AUTHKEY from .secrets.
	legacy, err := readSecretsEnv(c.SecretsFile, "TS_AUTHKEY")
	if err != nil {
		return fmt.Errorf("reading legacy TS_AUTHKEY from %s: %w", c.SecretsFile, err)
	}
	if legacy == "" {
		fmt.Fprintf(os.Stderr, "No legacy TS_AUTHKEY entry found in %s — nothing to migrate.\n", c.SecretsFile)
		return c.scanDeployYAML()
	}

	// Step 2: determine tailnet suffix.
	suffix := strings.TrimSpace(c.Tailnet)
	if suffix == "" {
		// Try to auto-detect from a currently-deployed tailscale sidecar.
		if detected, ok := detectTailnetFromRunningSidecar(); ok {
			fmt.Fprintf(os.Stderr, "Auto-detected tailnet from running sidecar: %s\n", detected)
			fmt.Fprintf(os.Stderr, "Press Enter to accept, or type a different MagicDNS suffix: ")
			reader := bufio.NewReader(os.Stdin)
			line, _ := reader.ReadString('\n')
			line = strings.TrimSpace(line)
			if line == "" {
				suffix = detected
			} else {
				suffix = line
			}
		} else {
			fmt.Fprintf(os.Stderr, "Tailnet MagicDNS suffix for the legacy TS_AUTHKEY (e.g. armadillo-quail.ts.net): ")
			reader := bufio.NewReader(os.Stdin)
			line, _ := reader.ReadString('\n')
			suffix = strings.TrimSpace(line)
		}
	}
	if suffix == "" {
		return fmt.Errorf("tailnet suffix is required (pass --tailnet or answer the prompt)")
	}

	// Step 3: compute the new env var name + write it.
	newName := "TS_AUTHKEY_" + normalizeTailnetSuffix(suffix)
	if newName == "TS_AUTHKEY_" {
		return fmt.Errorf("tailnet suffix %q normalizes to an empty fragment; refusing", suffix)
	}

	existing, err := readSecretsEnv(c.SecretsFile, newName)
	if err != nil {
		return fmt.Errorf("reading existing %s from %s: %w", newName, c.SecretsFile, err)
	}
	if existing != "" {
		if existing == legacy {
			fmt.Fprintf(os.Stderr, "%s already set to the legacy TS_AUTHKEY value — no rename needed.\n", newName)
		} else {
			return fmt.Errorf("%s already exists in %s with a DIFFERENT value than legacy TS_AUTHKEY; refusing to overwrite. Resolve manually with `ov secrets gpg edit %s`", newName, c.SecretsFile, c.SecretsFile)
		}
	} else {
		if c.DryRun {
			fmt.Fprintf(os.Stderr, "DRY-RUN: would set %s in %s\n", newName, c.SecretsFile)
		} else {
			if err := setSecretsEnv(c.SecretsFile, newName, legacy); err != nil {
				return fmt.Errorf("setting %s in %s: %w", newName, c.SecretsFile, err)
			}
			fmt.Fprintf(os.Stderr, "Wrote %s to %s\n", newName, c.SecretsFile)
		}
	}

	// Step 4: optionally delete the legacy entry.
	if c.DeleteLegacy {
		if c.DryRun {
			fmt.Fprintf(os.Stderr, "DRY-RUN: would delete TS_AUTHKEY from %s\n", c.SecretsFile)
		} else {
			if err := unsetSecretsEnv(c.SecretsFile, "TS_AUTHKEY"); err != nil {
				return fmt.Errorf("removing legacy TS_AUTHKEY from %s: %w", c.SecretsFile, err)
			}
			fmt.Fprintf(os.Stderr, "Removed legacy TS_AUTHKEY from %s\n", c.SecretsFile)
		}
	} else {
		fmt.Fprintf(os.Stderr, "Legacy TS_AUTHKEY preserved in %s (pass --delete-legacy to remove).\n", c.SecretsFile)
	}

	// Step 5: scan deploy.yml for entries that need parameter.tailnet.
	return c.scanDeployYAML()
}

// scanDeployYAML walks ~/.config/ov/deploy.yml for sidecars.tailscale entries
// lacking parameter.tailnet and emits one warning per entry. Does NOT auto-
// write (the operator chooses which tailnet per deploy).
func (c *MigrateTailscaleSecretsCmd) scanDeployYAML() error {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil // best-effort
	}
	path := filepath.Join(configDir, "ov", "deploy.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil // file not present is fine
	}
	var doc struct {
		Deploy map[string]struct {
			Sidecar map[string]struct {
				Parameter map[string]string `yaml:"parameter"`
			} `yaml:"sidecar"`
			Sidecars map[string]struct {
				Parameter map[string]string `yaml:"parameter"`
			} `yaml:"sidecars"`
		} `yaml:"deploy"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil // tolerant — schema warnings live elsewhere
	}
	var warnings []string
	for deployName, entry := range doc.Deploy {
		// `sidecar:` is the canonical YAML key per 2026-05 field-singular cutover.
		// Also check `sidecars:` for legacy entries.
		for _, sm := range []map[string]struct {
			Parameter map[string]string `yaml:"parameter"`
		}{entry.Sidecar, entry.Sidecars} {
			if ts, ok := sm["tailscale"]; ok {
				if v := strings.TrimSpace(ts.Parameter["tailnet"]); v == "" {
					warnings = append(warnings, deployName)
				}
			}
		}
	}
	if len(warnings) == 0 {
		return nil
	}
	fmt.Fprintln(os.Stderr, "\nDeploy.yml entries with tailscale sidecar but no parameter.tailnet:")
	for _, name := range warnings {
		fmt.Fprintf(os.Stderr, "  - %s\n", name)
	}
	fmt.Fprintln(os.Stderr, "\nNext step: add `parameter: { tailnet: <suffix> }` under each entry's `sidecar.tailscale:` block")
	fmt.Fprintln(os.Stderr, "before running `ov config <deploy>`. Without it, the sidecar resolution will")
	fmt.Fprintln(os.Stderr, "error out with a clear message naming the missing parameter.")
	return nil
}

// normalizeTailnetSuffix mirrors the `tailnetEnvSuffix` template func in
// sidecar.go — uppercases, replaces every non-alphanumeric character with '_'.
// Used by the migration to produce the new env-var name from a tailnet suffix.
func normalizeTailnetSuffix(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - 32)
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// readSecretsEnv shells out to `ov secrets gpg env -f <path>` and parses
// `export KEY='value'` lines to find the requested name. Returns empty
// string if the file doesn't exist or the key isn't present.
func readSecretsEnv(path, name string) (string, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "", nil
	}
	cmd := exec.Command("ov", "secrets", "gpg", "env", "-f", path)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	prefix := "export " + name + "="
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		v := strings.TrimPrefix(line, prefix)
		v = strings.TrimSpace(v)
		// Strip surrounding single quotes (ov secrets gpg env single-quotes values)
		if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
			v = v[1 : len(v)-1]
		}
		return v, nil
	}
	return "", nil
}

// setSecretsEnv calls `ov secrets gpg set <name> <value> -f <path>`.
func setSecretsEnv(path, name, value string) error {
	cmd := exec.Command("ov", "secrets", "gpg", "set", name, value, "-f", path)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// unsetSecretsEnv calls `ov secrets gpg unset <name> -f <path>`.
func unsetSecretsEnv(path, name string) error {
	cmd := exec.Command("ov", "secrets", "gpg", "unset", name, "-f", path)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// detectTailnetFromRunningSidecar tries to read the MagicDNS suffix from a
// currently-running tailscale sidecar via `podman exec ... tailscale status
// --json`. Returns (suffix, true) on success, ("", false) otherwise.
func detectTailnetFromRunningSidecar() (string, bool) {
	// List running containers matching the *-tailscale name pattern.
	cmd := exec.Command("podman", "ps", "--format", "{{.Names}}")
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	var sidecarCtr string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.HasSuffix(line, "-tailscale") {
			sidecarCtr = line
			break
		}
	}
	if sidecarCtr == "" {
		return "", false
	}
	statusCmd := exec.Command("podman", "exec", sidecarCtr, "tailscale", "status", "--json")
	statusOut, err := statusCmd.Output()
	if err != nil {
		return "", false
	}
	// Cheap parse — look for "MagicDNSSuffix":"..."
	const marker = `"MagicDNSSuffix":"`
	i := strings.Index(string(statusOut), marker)
	if i < 0 {
		return "", false
	}
	start := i + len(marker)
	end := strings.Index(string(statusOut)[start:], `"`)
	if end < 0 {
		return "", false
	}
	return string(statusOut)[start : start+end], true
}
