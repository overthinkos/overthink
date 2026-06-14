package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// MigrateTailscaleSecretsAuto is the non-interactive, chain-callable form of
// the tailscale-secrets migration used by the unified `charly migrate` runner.
// Unlike the interactive command it NEVER prompts: it renames the legacy flat
// TS_AUTHKEY to TS_AUTHKEY_<TAILNET-NORMALIZED> only when the tailnet suffix
// can be auto-detected from a running tailscale sidecar; otherwise it leaves
// the legacy key in place, prints a one-line hint, and proceeds to the
// (read-only) deploy.yml scan. It never deletes the legacy entry. Returns the
// list of human-readable changes (or, under dryRun, would-be changes).
func MigrateTailscaleSecretsAuto(secretsFile string, dryRun bool) ([]string, error) {
	var changes []string
	legacy, err := readSecretsEnv(secretsFile, "TS_AUTHKEY")
	if err != nil {
		// .secrets may be absent or gpg unavailable on this host — that is
		// not a migration failure; fall through to the scan.
		scanTailscaleDeployYAML()
		return changes, nil
	}
	if legacy == "" {
		scanTailscaleDeployYAML()
		return changes, nil
	}
	suffix, ok := detectTailnetFromRunningSidecar()
	if !ok || strings.TrimSpace(suffix) == "" {
		fmt.Fprintf(os.Stderr, "tailscale-secrets: legacy TS_AUTHKEY found in %s but the tailnet could not be auto-detected;\n", secretsFile)
		fmt.Fprintln(os.Stderr, "  run `charly secrets gpg set TS_AUTHKEY_<TAILNET> <value>` manually to rename it (left in place for now).")
		scanTailscaleDeployYAML()
		return changes, nil
	}
	newName := "TS_AUTHKEY_" + normalizeTailnetSuffix(suffix)
	if newName == "TS_AUTHKEY_" {
		scanTailscaleDeployYAML()
		return changes, nil
	}
	existing, err := readSecretsEnv(secretsFile, newName)
	if err != nil {
		scanTailscaleDeployYAML()
		return changes, nil
	}
	if existing != "" {
		// Already migrated (or a conflicting value the operator must resolve
		// — non-interactive mode leaves conflicts untouched).
		scanTailscaleDeployYAML()
		return changes, nil
	}
	if dryRun {
		changes = append(changes, fmt.Sprintf("would set %s in %s (tailnet %s)", newName, secretsFile, suffix))
		scanTailscaleDeployYAML()
		return changes, nil
	}
	if err := setSecretsEnv(secretsFile, newName, legacy); err != nil {
		return changes, fmt.Errorf("setting %s in %s: %w", newName, secretsFile, err)
	}
	changes = append(changes, fmt.Sprintf("set %s in %s (tailnet %s; legacy TS_AUTHKEY preserved)", newName, secretsFile, suffix))
	scanTailscaleDeployYAML()
	return changes, nil
}

// scanTailscaleDeployYAML walks ~/.config/ov/deploy.yml for sidecars.tailscale
// entries lacking parameter.tailnet and emits one warning per entry. Does NOT
// auto-write (the operator chooses which tailnet per deploy). Used by
// MigrateTailscaleSecretsAuto.
func scanTailscaleDeployYAML() {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return // best-effort
	}
	path := filepath.Join(configDir, "ov", "deploy.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		return // file not present is fine
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
		return // tolerant — schema warnings live elsewhere
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
		return
	}
	fmt.Fprintln(os.Stderr, "\nDeploy.yml entries with tailscale sidecar but no parameter.tailnet:")
	for _, name := range warnings {
		fmt.Fprintf(os.Stderr, "  - %s\n", name)
	}
	fmt.Fprintln(os.Stderr, "\nNext step: add `parameter: { tailnet: <suffix> }` under each entry's `sidecar.tailscale:` block")
	fmt.Fprintln(os.Stderr, "before running `charly config <deploy>`. Without it, the sidecar resolution will")
	fmt.Fprintln(os.Stderr, "error out with a clear message naming the missing parameter.")
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

// readSecretsEnv shells out to `charly secrets gpg env -f <path>` and parses
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
		// Strip surrounding single quotes (charly secrets gpg env single-quotes values)
		if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
			v = v[1 : len(v)-1]
		}
		return v, nil
	}
	return "", nil
}

// setSecretsEnv calls `charly secrets gpg set <name> <value> -f <path>`.
func setSecretsEnv(path, name, value string) error {
	cmd := exec.Command("ov", "secrets", "gpg", "set", name, value, "-f", path)
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
