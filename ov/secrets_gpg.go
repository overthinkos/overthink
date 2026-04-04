package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// SecretsGpgCmd groups subcommands for managing GPG-encrypted .secrets files.
// These are project-level env files (KEY=VALUE), encrypted with GPG.
type SecretsGpgCmd struct {
	Show         SecretsGpgShowCmd         `cmd:"" help:"Decrypt and print .secrets to stdout"`
	Env          SecretsGpgEnvCmd          `cmd:"" help:"Export decrypted .secrets as shell export statements"`
	Edit         SecretsGpgEditCmd         `cmd:"" help:"Decrypt, edit in $EDITOR, re-encrypt"`
	Encrypt      SecretsGpgEncryptCmd      `cmd:"" help:"Encrypt a plaintext env file to .secrets"`
	Decrypt      SecretsGpgDecryptCmd      `cmd:"" help:"Decrypt .secrets to a plaintext file"`
	Set          SecretsGpgSetCmd          `cmd:"" help:"Set a single KEY=VALUE in .secrets"`
	Unset        SecretsGpgUnsetCmd        `cmd:"" help:"Remove a key from .secrets"`
	AddRecipient SecretsGpgAddRecipientCmd `cmd:"add-recipient" help:"Re-encrypt .secrets with an additional GPG recipient"`
	Recipients   SecretsGpgRecipientsCmd   `cmd:"" help:"List GPG recipients of .secrets file"`
}

// --- show ---

type SecretsGpgShowCmd struct {
	File string `short:"f" long:"file" default:".secrets" help:"Path to encrypted file"`
}

func (c *SecretsGpgShowCmd) Run() error {
	if err := requireGpg(); err != nil {
		return err
	}
	cmd := exec.Command("gpg", "--quiet", "--batch", "--decrypt", c.File)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// --- env ---

type SecretsGpgEnvCmd struct {
	File string `short:"f" long:"file" default:".secrets" help:"Path to encrypted file"`
}

func (c *SecretsGpgEnvCmd) Run() error {
	if err := requireGpg(); err != nil {
		return err
	}

	// Silent skip if file doesn't exist (matches dotenv_gpg_if_exists behavior)
	if _, err := os.Stat(c.File); os.IsNotExist(err) {
		return nil
	}

	plaintext, err := gpgDecryptToBytes(c.File)
	if err != nil {
		return fmt.Errorf("decrypting %s: %w", c.File, err)
	}

	entries, err := ParseEnvBytes(plaintext)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		idx := strings.IndexByte(entry, '=')
		if idx < 0 {
			continue
		}
		key := entry[:idx]
		value := entry[idx+1:]
		fmt.Printf("export %s=%s\n", key, shellQuote(value))
	}

	return nil
}

// --- edit ---

type SecretsGpgEditCmd struct {
	File string `short:"f" long:"file" default:".secrets" help:"Path to encrypted file"`
}

func (c *SecretsGpgEditCmd) Run() error {
	if err := requireGpg(); err != nil {
		return err
	}

	// Decrypt to temp file
	tmp, err := os.CreateTemp("", "ov-secrets-*.env")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer secureDelete(tmpPath)

	decryptCmd := exec.Command("gpg", "--quiet", "--batch", "--decrypt", "--output", tmpPath, c.File)
	decryptCmd.Stderr = os.Stderr
	if err := decryptCmd.Run(); err != nil {
		tmp.Close()
		return fmt.Errorf("decrypting %s: %w", c.File, err)
	}
	tmp.Close()

	// Get recipients before editing
	recipients, err := listRecipients(c.File)
	if err != nil {
		return fmt.Errorf("listing recipients: %w", err)
	}
	if len(recipients) == 0 {
		return fmt.Errorf("could not determine recipients for %s", c.File)
	}

	// Get file stat for change detection
	infoBefore, _ := os.Stat(tmpPath)

	// Open in editor
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	editorCmd := exec.Command(editor, tmpPath)
	editorCmd.Stdin = os.Stdin
	editorCmd.Stdout = os.Stdout
	editorCmd.Stderr = os.Stderr
	if err := editorCmd.Run(); err != nil {
		return fmt.Errorf("editor failed: %w", err)
	}

	// Check if file was modified
	infoAfter, _ := os.Stat(tmpPath)
	if infoBefore != nil && infoAfter != nil && infoBefore.ModTime().Equal(infoAfter.ModTime()) {
		fmt.Fprintln(os.Stderr, "No changes made.")
		return nil
	}

	// Re-encrypt with same recipients
	return gpgEncryptFile(tmpPath, c.File, recipients)
}

// --- encrypt ---

type SecretsGpgEncryptCmd struct {
	Input     string   `short:"i" long:"input" default:".env" help:"Input plaintext file"`
	Output    string   `short:"o" long:"output" default:".secrets" help:"Output encrypted file"`
	Recipient []string `short:"r" long:"recipient" help:"GPG recipient key ID (repeatable)"`
}

func (c *SecretsGpgEncryptCmd) Run() error {
	if err := requireGpg(); err != nil {
		return err
	}
	if len(c.Recipient) == 0 {
		return fmt.Errorf("at least one --recipient (-r) is required")
	}
	return gpgEncryptFile(c.Input, c.Output, c.Recipient)
}

// --- decrypt ---

type SecretsGpgDecryptCmd struct {
	Input  string `short:"i" long:"input" default:".secrets" help:"Input encrypted file"`
	Output string `short:"o" long:"output" help:"Output file (default: stdout)"`
}

func (c *SecretsGpgDecryptCmd) Run() error {
	if err := requireGpg(); err != nil {
		return err
	}
	args := []string{"--quiet", "--batch", "--decrypt"}
	if c.Output != "" {
		args = append(args, "--output", c.Output)
	}
	args = append(args, c.Input)
	cmd := exec.Command("gpg", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// --- set ---

type SecretsGpgSetCmd struct {
	Key       string   `arg:"" help:"Environment variable name"`
	Value     string   `arg:"" help:"Value to set"`
	File      string   `short:"f" long:"file" default:".secrets" help:"Path to encrypted file"`
	Recipient []string `short:"r" long:"recipient" help:"GPG recipient (required if creating new file)"`
}

func (c *SecretsGpgSetCmd) Run() error {
	if err := requireGpg(); err != nil {
		return err
	}
	return modifySecrets(c.File, c.Recipient, func(lines []string) []string {
		return upsertEnvLine(lines, c.Key, c.Value)
	})
}

// --- unset ---

type SecretsGpgUnsetCmd struct {
	Key  string `arg:"" help:"Environment variable name to remove"`
	File string `short:"f" long:"file" default:".secrets" help:"Path to encrypted file"`
}

func (c *SecretsGpgUnsetCmd) Run() error {
	if err := requireGpg(); err != nil {
		return err
	}
	return modifySecrets(c.File, nil, func(lines []string) []string {
		return removeEnvLine(lines, c.Key)
	})
}

// --- add-recipient ---

type SecretsGpgAddRecipientCmd struct {
	KeyID string `arg:"" help:"GPG key ID of the new recipient"`
	File  string `short:"f" long:"file" default:".secrets" help:"Path to encrypted file"`
}

func (c *SecretsGpgAddRecipientCmd) Run() error {
	if err := requireGpg(); err != nil {
		return err
	}

	recipients, err := listRecipients(c.File)
	if err != nil {
		return err
	}

	// Check if already a recipient
	for _, r := range recipients {
		if r == c.KeyID {
			fmt.Fprintf(os.Stderr, "Key %s is already a recipient.\n", c.KeyID)
			return nil
		}
	}

	recipients = append(recipients, c.KeyID)

	// Decrypt and re-encrypt with new recipient list
	plaintext, err := gpgDecryptToBytes(c.File)
	if err != nil {
		return err
	}
	return gpgEncryptBytes(plaintext, c.File, recipients)
}

// --- recipients ---

type SecretsGpgRecipientsCmd struct {
	File string `short:"f" long:"file" default:".secrets" help:"Path to encrypted file"`
}

func (c *SecretsGpgRecipientsCmd) Run() error {
	if err := requireGpg(); err != nil {
		return err
	}
	recipients, err := listRecipients(c.File)
	if err != nil {
		return err
	}
	for _, r := range recipients {
		fmt.Println(r)
	}
	return nil
}

// --- helpers ---

func requireGpg() error {
	if _, err := exec.LookPath("gpg"); err != nil {
		return fmt.Errorf("gpg not found in PATH (install gnupg2)")
	}
	return nil
}

// gpgEncryptFile encrypts inputPath to outputPath for the given recipients.
func gpgEncryptFile(inputPath, outputPath string, recipients []string) error {
	args := []string{"--encrypt", "--armor", "--yes", "--output", outputPath}
	for _, r := range recipients {
		args = append(args, "-r", r)
	}
	args = append(args, inputPath)
	cmd := exec.Command("gpg", args...)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// gpgDecryptToBytes decrypts a file and returns the plaintext.
func gpgDecryptToBytes(path string) ([]byte, error) {
	cmd := exec.Command("gpg", "--quiet", "--batch", "--decrypt", path)
	cmd.Stderr = os.Stderr
	return cmd.Output()
}

// gpgEncryptBytes encrypts plaintext bytes to the given file for the recipients.
func gpgEncryptBytes(plaintext []byte, outputPath string, recipients []string) error {
	args := []string{"--encrypt", "--armor", "--yes", "--output", outputPath}
	for _, r := range recipients {
		args = append(args, "-r", r)
	}
	cmd := exec.Command("gpg", args...)
	cmd.Stdin = strings.NewReader(string(plaintext))
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// listRecipients parses a GPG-encrypted file to extract recipient key IDs.
func listRecipients(path string) ([]string, error) {
	cmd := exec.Command("gpg", "--list-packets", "--batch", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("listing packets for %s: %w\n%s", path, err, string(out))
	}

	var recipients []string
	// Match lines like ":pubkey enc packet: ... keyid 5EA2283B420DE2B3"
	re := regexp.MustCompile(`keyid\s+([0-9A-Fa-f]+)`)
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "pubkey enc packet") {
			if matches := re.FindStringSubmatch(line); len(matches) > 1 {
				recipients = append(recipients, matches[1])
			}
		}
	}
	return recipients, nil
}

// modifySecrets decrypts .secrets, applies a transform function, and re-encrypts.
func modifySecrets(path string, fallbackRecipients []string, transform func([]string) []string) error {
	var lines []string
	var recipients []string

	if _, err := os.Stat(path); err == nil {
		// Existing file: decrypt and get recipients
		plaintext, decErr := gpgDecryptToBytes(path)
		if decErr != nil {
			return fmt.Errorf("decrypting %s: %w", path, decErr)
		}
		lines = strings.Split(string(plaintext), "\n")

		var listErr error
		recipients, listErr = listRecipients(path)
		if listErr != nil {
			return listErr
		}
	} else {
		// New file
		recipients = fallbackRecipients
	}

	if len(recipients) == 0 {
		return fmt.Errorf("no recipients known; specify --recipient (-r) when creating a new file")
	}

	lines = transform(lines)
	content := strings.Join(lines, "\n")
	// Ensure trailing newline
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}

	return gpgEncryptBytes([]byte(content), path, recipients)
}

// upsertEnvLine adds or replaces KEY=VALUE in the lines.
func upsertEnvLine(lines []string, key, value string) []string {
	prefix := key + "="
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, prefix) || trimmed == key {
			lines[i] = key + "=" + value
			return lines
		}
	}
	// Append before any trailing empty lines
	insertIdx := len(lines)
	for insertIdx > 0 && strings.TrimSpace(lines[insertIdx-1]) == "" {
		insertIdx--
	}
	result := make([]string, 0, len(lines)+1)
	result = append(result, lines[:insertIdx]...)
	result = append(result, key+"="+value)
	result = append(result, lines[insertIdx:]...)
	return result
}

// removeEnvLine removes all lines matching KEY= from the lines.
func removeEnvLine(lines []string, key string) []string {
	prefix := key + "="
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, prefix) || trimmed == key {
			continue
		}
		result = append(result, line)
	}
	return result
}

// secureDelete overwrites a file with zeros before deleting.
func secureDelete(path string) {
	if info, err := os.Stat(path); err == nil {
		zeros := make([]byte, info.Size())
		os.WriteFile(path, zeros, 0600)
	}
	os.Remove(path)
}