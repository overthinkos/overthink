package main

// shell_profile.go — host-side shell profile integration.
//
// On `ov deploy add host`, each installed layer contributes a set of
// env vars and PATH additions (from layer.yml's env: + path_append:).
// We materialize them as `~/.config/overthink/env.d/<layer>.env` files
// and insert a managed block in the user's shell init so those files
// get sourced at login.
//
// Shell detection:
//   - bash → write both ~/.bashrc (interactive) and ~/.profile (login).
//     We insert the managed block into ~/.profile only (bash auto-
//     sources that on login), but the user may still need to run
//     `. ~/.profile` in an already-open shell.
//   - zsh  → ~/.zshenv (sourced for every zsh invocation type).
//   - fish → ~/.config/fish/conf.d/overthink.fish (conf.d is idiomatic).
//
// Managed-block fence:
//
//   # overthink:begin (managed by ov; do not edit inside this block)
//   <sourcing loop>
//   # overthink:end
//
// On `ov deploy del host`, if no layers remain deployed the managed
// block is removed from the shell init file.

import (
	"bufio"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
)

// ShellKind classifies the user's login shell.
type ShellKind string

const (
	ShellBash ShellKind = "bash"
	ShellZsh  ShellKind = "zsh"
	ShellFish ShellKind = "fish"
)

// DetectLoginShell inspects $SHELL (or /etc/passwd as fallback) and
// returns the detected shell. Unknown shells default to bash — that's
// the POSIX-safest choice.
func DetectLoginShell() ShellKind {
	shellPath := os.Getenv("SHELL")
	if shellPath == "" {
		if u, err := user.Current(); err == nil {
			shellPath = getShellFromPasswd(u.Username)
		}
	}
	base := filepath.Base(shellPath)
	switch base {
	case "zsh":
		return ShellZsh
	case "fish":
		return ShellFish
	case "bash", "sh", "":
		return ShellBash
	}
	return ShellBash
}

// getShellFromPasswd is a stub that can be upgraded to parse
// /etc/passwd directly. The os/user package doesn't expose the shell
// field cross-platform, so we rely on $SHELL as the primary source.
func getShellFromPasswd(_ string) string { return "" }

// ---------------------------------------------------------------------------
// env.d file writing.
// ---------------------------------------------------------------------------

// EnvdDir returns the directory where per-layer env files live.
func EnvdDir(hostHome string) string {
	return filepath.Join(hostHome, ".config", "overthink", "env.d")
}

// EnvdFilePath returns the env file path for a given layer.
func EnvdFilePath(hostHome, layerName string) string {
	return filepath.Join(EnvdDir(hostHome), layerName+".env")
}

// WriteEnvdFile creates (or overwrites) the env.d entry for a layer.
// Content is rendered from the ShellHookStep's EnvVars + PathAdd.
func WriteEnvdFile(hostHome, layerName string, envVars map[string]string, pathAdd []string) (string, error) {
	dir := EnvdDir(hostHome)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("WriteEnvdFile mkdir: %w", err)
	}
	path := EnvdFilePath(hostHome, layerName)
	body := renderEnvdBody(layerName, envVars, pathAdd)
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		return "", fmt.Errorf("WriteEnvdFile %s: %w", path, err)
	}
	return path, nil
}

// RemoveEnvdFile deletes an env.d entry. Silently succeeds when absent.
func RemoveEnvdFile(hostHome, layerName string) error {
	err := os.Remove(EnvdFilePath(hostHome, layerName))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// renderEnvdBody produces a deterministic, shell-agnostic POSIX-sh
// fragment. Sorted keys + path entries guarantee stable output across
// runs (tests compare directly).
func renderEnvdBody(layerName string, envVars map[string]string, pathAdd []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# overthink env for layer %s — managed by ov; do not edit\n", layerName)
	keys := make([]string, 0, len(envVars))
	for k := range envVars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "export %s=%s\n", k, shQuoteEnv(envVars[k]))
	}
	if len(pathAdd) > 0 {
		// Build PATH prepend entries. Double-quote the value so $PATH
		// EXPANDS at sourcing time — single-quoting (shQuoteEnv) makes
		// each layer's env.d set PATH to the literal string
		// "/some/dir:$PATH", which the next layer's env.d then clobbers
		// with its own literal "/other/dir:$PATH", losing every prior
		// layer's PATH entries. Layer-supplied paths are absolute, so
		// double-quoting is safe; we only escape characters with
		// special meaning inside double quotes.
		parts := append([]string(nil), pathAdd...)
		parts = append(parts, `$PATH`)
		fmt.Fprintf(&b, "export PATH=\"%s\"\n", shDoubleQuotePath(strings.Join(parts, ":")))
	}
	return b.String()
}

// shDoubleQuotePath escapes a PATH-list value for use INSIDE double
// quotes. Only the four chars with special meaning inside POSIX-sh
// double quotes need escaping: $ ` " \. We DON'T escape $ — the
// caller wants $PATH to expand. So escape just the others. PATH
// entries from layer.yml are absolute paths and won't contain those
// characters in practice; this is purely defensive.
func shDoubleQuotePath(v string) string {
	r := strings.NewReplacer(`\`, `\\`, "`", "\\`", `"`, `\"`)
	return r.Replace(v)
}

// shQuoteEnv single-quotes a value for POSIX sh. Inside single quotes
// nothing needs escaping except the single quote itself.
func shQuoteEnv(v string) string {
	if v == "" {
		return `''`
	}
	if !strings.ContainsAny(v, " \t\n\"'$*?[](){}<>|&;`\\!") {
		return v
	}
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}

// ---------------------------------------------------------------------------
// Managed block insertion / removal.
// ---------------------------------------------------------------------------

const (
	managedBlockBegin = "# overthink:begin (managed by ov; do not edit inside this block)"
	managedBlockEnd   = "# overthink:end"
)

// ManagedBlockBody returns the shell-specific loop that sources the
// env.d directory. POSIX-sh for bash/zsh, fish syntax for fish.
func ManagedBlockBody(shell ShellKind, hostHome string) string {
	envdGlob := filepath.Join(EnvdDir(hostHome), "*.env")
	switch shell {
	case ShellFish:
		return fmt.Sprintf(`for f in %s
  if test -r $f
    source $f
  end
end`, envdGlob)
	default: // bash/zsh POSIX
		// Glob is intentionally UNQUOTED so the shell expands *.env.
		// Quoting (`"%s"`) suppresses expansion — the loop then iterates
		// once over the literal pattern, the `[ -r ]` test fails, and
		// no env files get sourced. The path is ov-controlled so there
		// are no word-splitting concerns from spaces.
		return fmt.Sprintf(`for f in %s; do [ -r "$f" ] && . "$f"; done`, envdGlob)
	}
}

// ShellInitFilePath returns the path to the init file we write the
// managed block into, for each detected shell.
func ShellInitFilePath(shell ShellKind, hostHome string) string {
	switch shell {
	case ShellZsh:
		return filepath.Join(hostHome, ".zshenv")
	case ShellFish:
		return filepath.Join(hostHome, ".config", "fish", "conf.d", "overthink.fish")
	default:
		return filepath.Join(hostHome, ".profile")
	}
}

// EnsureManagedBlock inserts (or updates) the managed block in the
// shell init file. Creates the file (and its parent dirs) if missing.
// Returns the file path written.
func EnsureManagedBlock(shell ShellKind, hostHome string) (string, error) {
	path := ShellInitFilePath(shell, hostHome)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", fmt.Errorf("EnsureManagedBlock mkdir: %w", err)
	}
	existing, _ := os.ReadFile(path) // may not exist yet; ignore error
	body := ManagedBlockBody(shell, hostHome)
	updated := replaceOrAppendManagedBlock(string(existing), body)
	if err := os.WriteFile(path, []byte(updated), 0644); err != nil {
		return "", fmt.Errorf("EnsureManagedBlock write %s: %w", path, err)
	}
	return path, nil
}

// RemoveManagedBlock strips the managed block from the shell init
// file. Used at full-teardown when no layers remain deployed.
func RemoveManagedBlock(shell ShellKind, hostHome string) error {
	path := ShellInitFilePath(shell, hostHome)
	existing, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("RemoveManagedBlock read %s: %w", path, err)
	}
	stripped := stripManagedBlock(string(existing))
	// Only write if something changed — preserves file mtime when
	// there's nothing to remove.
	if stripped == string(existing) {
		return nil
	}
	return os.WriteFile(path, []byte(stripped), 0644)
}

// replaceOrAppendManagedBlock finds `# overthink:begin ... # overthink:end`
// in `existing` and replaces its body; if the markers are absent, it
// appends a fresh block at end-of-file.
func replaceOrAppendManagedBlock(existing, body string) string {
	if strings.Contains(existing, managedBlockBegin) {
		scanner := bufio.NewScanner(strings.NewReader(existing))
		var out strings.Builder
		inBlock := false
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, managedBlockBegin) {
				inBlock = true
				out.WriteString(managedBlockBegin + "\n")
				out.WriteString(body + "\n")
				continue
			}
			if inBlock && strings.Contains(line, managedBlockEnd) {
				inBlock = false
				out.WriteString(managedBlockEnd + "\n")
				continue
			}
			if !inBlock {
				out.WriteString(line + "\n")
			}
		}
		return strings.TrimRight(out.String(), "\n") + "\n"
	}
	// Not found — append.
	prefix := existing
	if prefix != "" && !strings.HasSuffix(prefix, "\n") {
		prefix += "\n"
	}
	if prefix != "" {
		prefix += "\n"
	}
	return prefix + managedBlockBegin + "\n" + body + "\n" + managedBlockEnd + "\n"
}

// stripManagedBlock removes the `# overthink:begin ... # overthink:end`
// block (including its body) from existing.
func stripManagedBlock(existing string) string {
	if !strings.Contains(existing, managedBlockBegin) {
		return existing
	}
	var out strings.Builder
	scanner := bufio.NewScanner(strings.NewReader(existing))
	inBlock := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, managedBlockBegin) {
			inBlock = true
			continue
		}
		if inBlock && strings.Contains(line, managedBlockEnd) {
			inBlock = false
			continue
		}
		if !inBlock {
			out.WriteString(line + "\n")
		}
	}
	return strings.TrimRight(out.String(), "\n") + "\n"
}
