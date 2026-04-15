package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ResolvedBindMount is ready for -v flags.
// Represents a volume backed by a host path (either plain bind or encrypted gocryptfs).
type ResolvedBindMount struct {
	Name      string // e.g. "secrets"
	HostPath  string // effective host path (plain: expanded host, encrypted: plain dir)
	ContPath  string // container path (expanded)
	Encrypted bool   // for status/mount checks
}

// encryptedVolumeName returns the directory name for an encrypted volume: ov-<image>-<name>
func encryptedVolumeName(imageName, name string) string {
	return "ov-" + imageName + "-" + name
}

// encryptedCipherDir returns the cipher directory path for an encrypted bind mount.
func encryptedCipherDir(storagePath, imageName, name string) string {
	return filepath.Join(storagePath, encryptedVolumeName(imageName, name), "cipher")
}

// encryptedPlainDir returns the plain (FUSE mount point) directory path.
func encryptedPlainDir(storagePath, imageName, name string) string {
	return filepath.Join(storagePath, encryptedVolumeName(imageName, name), "plain")
}

// resolveEncVolumeDir returns the volume directory for an encrypted volume.
// If the volume has an explicit Host path, use it directly.
// Otherwise, use the global default: <storagePath>/ov-<image>-<name>.
func resolveEncVolumeDir(vol DeployVolumeConfig, defaultStoragePath, imageName string) string {
	if vol.Host != "" {
		return expandHostHome(vol.Host)
	}
	return filepath.Join(defaultStoragePath, encryptedVolumeName(imageName, vol.Name))
}

// isEncryptedInitialized checks if gocryptfs has been initialized (gocryptfs.conf exists).
func isEncryptedInitialized(cipherDir string) bool {
	_, err := os.Stat(filepath.Join(cipherDir, "gocryptfs.conf"))
	return err == nil
}

// isEncryptedMounted checks if the plain dir is a FUSE mount by reading /proc/mounts.
var isEncryptedMounted = defaultIsEncryptedMounted

func defaultIsEncryptedMounted(plainDir string) bool {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return false
	}
	defer f.Close()

	// Resolve symlinks for comparison
	resolved, err := filepath.EvalSymlinks(plainDir)
	if err != nil {
		resolved = plainDir
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 3 {
			mountPoint, err := filepath.EvalSymlinks(fields[1])
			if err != nil {
				mountPoint = fields[1]
			}
			if mountPoint == resolved && fields[2] == "fuse.gocryptfs" {
				return true
			}
		}
	}
	return false
}

// encExtpassArgs returns gocryptfs -extpass arguments for CLI commands.
// Uses a temp script file because gocryptfs's flag parser normalizes multi-flag
// values (turns -c into --c). The script checks GOCRYPTFS_PASSWORD env var first
// (for testing/CI), then falls back to systemd-ask-password with /dev/tty redirect
// so it works regardless of whether gocryptfs connects stdin to the child process.
// Caller must defer the returned cleanup function.
func encExtpassArgs(imageID string) ([]string, func()) {
	script := "#!/bin/bash\n" +
		"if [ -n \"$GOCRYPTFS_PASSWORD\" ]; then\n" +
		"  printf '%s' \"$GOCRYPTFS_PASSWORD\"\n" +
		"else\n" +
		"  exec 0</dev/tty\n" +
		"  systemd-ask-password --id=" + imageID + " --timeout=0 --echo=masked 'Passphrase for " + imageID + ":'\n" +
		"fi\n"

	f, err := os.CreateTemp("", "ov-extpass-*.sh")
	if err != nil {
		// Fall back to inline systemd-ask-password (won't work headlessly)
		ep := "systemd-ask-password --id=" + imageID + " --timeout=0 --echo=masked Passphrase"
		return []string{"-extpass", ep}, func() {}
	}
	f.WriteString(script)
	f.Chmod(0700)
	f.Close()
	return []string{"-extpass", f.Name()}, func() { os.Remove(f.Name()) }
}

// resolveEncPassphrase resolves the gocryptfs passphrase for an image.
// Resolution order: GOCRYPTFS_PASSWORD env var → credential store (kdbx/keyring/config) → auto-generate or interactive prompt.
func resolveEncPassphrase(imageName string, autoGenerate bool) (string, error) {
	// 1. Test/CI override
	if pw := os.Getenv("GOCRYPTFS_PASSWORD"); pw != "" {
		return pw, nil
	}
	// 2. Credential store (kdbx / keyring / config)
	if val, _ := ResolveCredential("", "ov/enc", imageName, ""); val != "" {
		return val, nil
	}
	// 3. Auto-generate if requested
	if autoGenerate {
		generated := generateRandomHex(32)
		store := DefaultCredentialStore()
		if err := store.Set("ov/enc", imageName, generated); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not persist enc passphrase for %s: %v\n", imageName, err)
		}
		fmt.Fprintf(os.Stderr, "Generated encryption passphrase for %s\n", imageName)
		return generated, nil
	}
	// 4. Interactive prompt
	return askPassword("ov-"+imageName, "Passphrase for ov-"+imageName+":")
}

// encMountDeadline bounds how long resolveEncPassphraseForMount will retry
// transient failures (keyring locked, store unavailable) before giving up.
// Package-level var so tests can shrink it.
var encMountDeadline = 2 * time.Minute

// encMountPollPeriod is the interval between retry attempts within
// encMountDeadline.
var encMountPollPeriod = 5 * time.Second

// resolveEncPassphraseForMount resolves the gocryptfs passphrase with
// backend-aware and failure-aware retry behavior.
//
// Under systemd (INVOCATION_ID set) with a keyring-capable backend:
//   - If the store is temporarily locked ("locked") or unreachable
//     ("unavailable"), retry every encMountPollPeriod until encMountDeadline
//     elapses, then fail with a clear diagnostic.
//   - If the store answered and the credential is NOT stored ("default"),
//     fail immediately with an actionable error — no amount of polling
//     will conjure a credential that was never stored.
//
// Explicit non-keyring backends under systemd: try resolve once, fail fast
// if not found. No polling.
//
// Interactive callers fall back to resolveEncPassphrase which can prompt.
//
// Defect D fix: the previous implementation polled forever on src=="default"
// and had no deadline, so a misconfigured keyring + TimeoutStartSec=0 quadlet
// was unrecoverable without manual intervention. The new behavior caps the
// total wait at encMountDeadline and distinguishes the three failure modes.
func resolveEncPassphraseForMount(imageName string) (string, error) {
	if os.Getenv("INVOCATION_ID") == "" {
		// Interactive: normal resolution (keyring → kdbx → prompt)
		return resolveEncPassphrase(imageName, false)
	}
	backend := resolveSecretBackend()
	resolver := func() (string, string) {
		return ResolveCredential("", "ov/enc", imageName, "")
	}
	return resolveEncPassphraseForMountWithResolver(imageName, backend, resolver, resetDefaultCredentialStore)
}

// resolveEncPassphraseForMountWithResolver is the testable core of
// resolveEncPassphraseForMount. It accepts a resolver closure and a reset
// closure so tests can supply mock implementations without touching global
// state, environment variables, or DBus.
func resolveEncPassphraseForMountWithResolver(
	imageName, backend string,
	resolver func() (value, source string),
	reset func(),
) (string, error) {
	usesWaitingBackend := backend == "" || backend == "auto" || backend == "keyring"

	if !usesWaitingBackend {
		// Explicit non-keyring backend (config, kdbx with explicit opt-in):
		// try once, fail fast. No polling.
		val, src := resolver()
		if val != "" {
			return val, nil
		}
		return "", fmt.Errorf(
			"encryption passphrase not found for ov/enc/%s (backend=%s, source=%s); "+
				"store with `ov secrets set ov/enc %s` or switch backend with `ov settings set secret_backend auto`",
			imageName, backend, src, imageName)
	}

	// Keyring-capable backend: bounded poll loop.
	deadline := time.Now().Add(encMountDeadline)
	attempt := 0
	maxAttempts := int(encMountDeadline / encMountPollPeriod)
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	for {
		attempt++
		val, src := resolver()
		if val != "" {
			return val, nil
		}
		// Only "locked" and "unavailable" are retryable. "default" is
		// terminal — the store worked and the credential is not stored.
		retryable := src == "locked" || src == "unavailable"
		if !retryable || !time.Now().Before(deadline) {
			return "", fmt.Errorf(
				"encryption passphrase not available for ov/enc/%s after %d attempt(s) "+
					"(backend=%s, source=%s, waited up to %v). "+
					"Remediation: run `ov doctor` to check keyring health, "+
					"store with `ov secrets set ov/enc %s`, "+
					"or switch backend with `ov settings set secret_backend config`",
				imageName, attempt, backend, src, encMountDeadline, imageName)
		}
		fmt.Fprintf(os.Stderr,
			"ov: waiting for credential store (ov-enc/%s, source=%s, attempt %d/%d)...\n",
			imageName, src, attempt, maxAttempts)
		time.Sleep(encMountPollPeriod)
		// Reset cached store — keyring may become available on next attempt
		if reset != nil {
			reset()
		}
	}
}

// encInit initializes gocryptfs cipher directories for an image.
// If volume is non-empty, only that volume is initialized.
func encInit(imageName, instance, volume string) error {
	mounts, storagePath, err := loadEncryptedVolumes(imageName, instance)
	if err != nil {
		return err
	}

	passphrase, err := resolveEncPassphrase(imageName, false)
	if err != nil {
		return err
	}
	extpassArgs, cleanup := encExtpassArgs("ov-" + imageName)
	defer cleanup()

	for _, m := range mounts {
		if volume != "" && m.Name != volume {
			continue
		}

		volDir := resolveEncVolumeDir(m, storagePath, imageName)
		cipherDir := filepath.Join(volDir, "cipher")
		plainDir := filepath.Join(volDir, "plain")

		if isEncryptedInitialized(cipherDir) {
			fmt.Fprintf(os.Stderr, "%s: already initialized\n", m.Name)
			continue
		}

		if err := os.MkdirAll(cipherDir, 0700); err != nil {
			return fmt.Errorf("creating cipher dir for %s: %w", m.Name, err)
		}
		if err := os.MkdirAll(plainDir, 0700); err != nil {
			return fmt.Errorf("creating plain dir for %s: %w", m.Name, err)
		}

		args := append([]string{"-init"}, extpassArgs...)
		args = append(args, cipherDir)
		cmd := exec.Command("gocryptfs", args...)
		cmd.Env = append(os.Environ(), "GOCRYPTFS_PASSWORD="+passphrase)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("gocryptfs -init for %s: %w", m.Name, err)
		}
		fmt.Fprintf(os.Stderr, "Initialized %s at %s\n", m.Name, cipherDir)
	}
	return nil
}

// encMount mounts encrypted volumes for an image.
// If volume is non-empty, only that volume is mounted.
// Uses resolveEncPassphraseForMount which waits for keyring unlock under systemd.
//
// Fast path: if every requested volume is already mounted (scope units still
// alive from a previous mount), return nil without querying the credential
// store at all. This makes service restarts resilient to keyring breakage —
// the most common operational case is "restart when everything is still
// mounted", and it has no passphrase dependency.
func encMount(imageName, instance, volume string) error {
	mounts, storagePath, err := loadEncryptedVolumes(imageName, instance)
	if err != nil {
		return err
	}

	// Fast path: count requested volumes and their mount state. If every
	// requested volume is already mounted, skip the passphrase lookup.
	requested := 0
	mounted := 0
	for _, m := range mounts {
		if volume != "" && m.Name != volume {
			continue
		}
		requested++
		volDir := resolveEncVolumeDir(m, storagePath, imageName)
		plainDir := filepath.Join(volDir, "plain")
		if isEncryptedMounted(plainDir) {
			mounted++
		}
	}
	if requested > 0 && mounted == requested {
		fmt.Fprintf(os.Stderr, "All encrypted volumes for %s already mounted (%d/%d)\n", imageName, mounted, requested)
		return nil
	}

	passphrase, err := resolveEncPassphraseForMount(imageName)
	if err != nil {
		return err
	}
	extpassArgs, cleanup := encExtpassArgs("ov-" + imageName)
	defer cleanup()

	for _, m := range mounts {
		if volume != "" && m.Name != volume {
			continue
		}

		volDir := resolveEncVolumeDir(m, storagePath, imageName)
		cipherDir := filepath.Join(volDir, "cipher")
		plainDir := filepath.Join(volDir, "plain")

		if !isEncryptedInitialized(cipherDir) {
			return fmt.Errorf("encrypted volume %q not initialized; run 'ov config %s' first", m.Name, imageName)
		}

		if isEncryptedMounted(plainDir) {
			fmt.Fprintf(os.Stderr, "%s: already mounted\n", m.Name)
			continue
		}

		if err := os.MkdirAll(plainDir, 0700); err != nil {
			return fmt.Errorf("creating plain dir for %s: %w", m.Name, err)
		}

		gcArgs := append(extpassArgs, "-allow_other", cipherDir, plainDir)
		scopeUnit := fmt.Sprintf("ov-enc-%s-%s", imageName, m.Name)
		scopeArgs := append([]string{"--scope", "--user", "--unit=" + scopeUnit, "--", "gocryptfs"}, gcArgs...)
		cmd := exec.Command("systemd-run", scopeArgs...)
		cmd.Env = append(os.Environ(), "GOCRYPTFS_PASSWORD="+passphrase)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			// Stale scope from a previous run — stop it and retry
			if stopErr := exec.Command("systemctl", "--user", "stop", scopeUnit+".scope").Run(); stopErr == nil {
				cmd = exec.Command("systemd-run", scopeArgs...)
				cmd.Env = append(os.Environ(), "GOCRYPTFS_PASSWORD="+passphrase)
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr
				if retryErr := cmd.Run(); retryErr != nil {
					return fmt.Errorf("mounting %s: %w", m.Name, retryErr)
				}
			} else {
				return fmt.Errorf("mounting %s: %w", m.Name, err)
			}
		}
		fmt.Fprintf(os.Stderr, "Mounted %s at %s\n", m.Name, plainDir)
	}
	return nil
}

// encUnmount unmounts encrypted volumes for an image.
// If volume is non-empty, only that volume is unmounted.
func encUnmount(imageName, instance, volume string) error {
	mounts, storagePath, err := loadEncryptedVolumes(imageName, instance)
	if err != nil {
		return err
	}

	for _, m := range mounts {
		if volume != "" && m.Name != volume {
			continue
		}

		volDir := resolveEncVolumeDir(m, storagePath, imageName)
		plainDir := filepath.Join(volDir, "plain")

		if !isEncryptedMounted(plainDir) {
			fmt.Fprintf(os.Stderr, "%s: not mounted\n", m.Name)
			continue
		}

		cmd := exec.Command("fusermount3", "-u", plainDir)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("unmounting %s: %w", m.Name, err)
		}
		// Stop the gocryptfs scope unit (gocryptfs may linger after fusermount)
		scopeUnit := fmt.Sprintf("ov-enc-%s-%s.scope", imageName, m.Name)
		exec.Command("systemctl", "--user", "stop", scopeUnit).Run() // best-effort
		fmt.Fprintf(os.Stderr, "Unmounted %s\n", m.Name)
	}
	return nil
}

// encStatus prints the status of encrypted bind mounts for an image.
func encStatus(imageName, instance string) error {
	mounts, storagePath, err := loadEncryptedVolumes(imageName, instance)
	if err != nil {
		return err
	}

	if len(mounts) == 0 {
		fmt.Println("No encrypted bind mounts configured")
		return nil
	}

	fmt.Printf("%-20s %-12s %-8s %s\n", "NAME", "INITIALIZED", "MOUNTED", "PATH")
	for _, m := range mounts {
		volDir := resolveEncVolumeDir(m, storagePath, imageName)
		cipherDir := filepath.Join(volDir, "cipher")
		plainDir := filepath.Join(volDir, "plain")

		initialized := "no"
		if isEncryptedInitialized(cipherDir) {
			initialized = "yes"
		}
		mounted := "no"
		if isEncryptedMounted(plainDir) {
			mounted = "yes"
		}
		fmt.Printf("%-20s %-12s %-8s %s\n", m.Name, initialized, mounted, plainDir)
	}
	return nil
}

// encPasswd changes the gocryptfs password for all encrypted volumes of an image.
func encPasswd(imageName, instance string) error {
	mounts, storagePath, err := loadEncryptedVolumes(imageName, instance)
	if err != nil {
		return err
	}

	if len(mounts) == 0 {
		return fmt.Errorf("image %q has no encrypted bind mounts", imageName)
	}

	// All volumes must be unmounted before changing password
	for _, m := range mounts {
		volDir := resolveEncVolumeDir(m, storagePath, imageName)
		plainDir := filepath.Join(volDir, "plain")
		if isEncryptedMounted(plainDir) {
			return fmt.Errorf("encrypted volume %q is still mounted; run 'ov config unmount %s' first", m.Name, imageName)
		}
	}

	volID := "ov-" + imageName

	oldPass, err := askPassword(volID+"-old", "Current passphrase:")
	if err != nil {
		return err
	}

	newPass, err := askPassword(volID+"-new", "New passphrase:")
	if err != nil {
		return err
	}

	confirmPass, err := askPassword(volID+"-confirm", "Confirm new passphrase:")
	if err != nil {
		return err
	}

	if newPass != confirmPass {
		return fmt.Errorf("new passphrase and confirmation do not match")
	}

	for _, m := range mounts {
		volDir := resolveEncVolumeDir(m, storagePath, imageName)
		cipherDir := filepath.Join(volDir, "cipher")

		if !isEncryptedInitialized(cipherDir) {
			fmt.Fprintf(os.Stderr, "%s: not initialized, skipping\n", m.Name)
			continue
		}

		// Create temp extpass script that supplies the old password
		oldScript, err := os.CreateTemp("", "ov-oldpass-*.sh")
		if err != nil {
			return fmt.Errorf("creating temp script for %s: %w", m.Name, err)
		}
		oldScript.WriteString("#!/bin/bash\nprintf '%s' '" + strings.ReplaceAll(oldPass, "'", "'\\''") + "'\n")
		oldScript.Chmod(0700)
		oldScript.Close()

		// Pipe new password via stdin to gocryptfs -passwd
		cmd := exec.Command("gocryptfs", "-passwd", "-extpass", oldScript.Name(), cipherDir)
		cmd.Stdin = strings.NewReader(newPass)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		runErr := cmd.Run()
		os.Remove(oldScript.Name())
		if runErr != nil {
			return fmt.Errorf("changing password for %s: %w", m.Name, runErr)
		}
		fmt.Fprintf(os.Stderr, "Password changed for %s\n", m.Name)
	}
	return nil
}

// ensureEncryptedMounts auto-initializes and mounts encrypted volumes as needed.
// Called by ov start to transparently handle encrypted volume setup without
// requiring the user to run ov config init/mount manually first.
// Resolves the enc passphrase once (kdbx → keyring → interactive prompt).
func ensureEncryptedMounts(imageName, instance string, autoGenerate bool) error {
	mounts, storagePath, err := loadEncryptedVolumes(imageName, instance)
	if err != nil || len(mounts) == 0 {
		return nil // no encrypted mounts configured
	}

	anyNotReady := false
	for _, m := range mounts {
		volDir := resolveEncVolumeDir(m, storagePath, imageName)
		cipherDir := filepath.Join(volDir, "cipher")
		plainDir := filepath.Join(volDir, "plain")
		if !isEncryptedInitialized(cipherDir) || !isEncryptedMounted(plainDir) {
			anyNotReady = true
			break
		}
	}
	if !anyNotReady {
		return nil
	}

	passphrase, err := resolveEncPassphrase(imageName, autoGenerate)
	if err != nil {
		return fmt.Errorf("resolving enc passphrase for %s: %w", imageName, err)
	}
	extpassArgs, cleanup := encExtpassArgs("ov-" + imageName)
	defer cleanup()

	for _, m := range mounts {
		volDir := resolveEncVolumeDir(m, storagePath, imageName)
		cipherDir := filepath.Join(volDir, "cipher")
		plainDir := filepath.Join(volDir, "plain")

		if !isEncryptedInitialized(cipherDir) {
			fmt.Fprintf(os.Stderr, "Initializing encrypted volume %s for %s...\n", m.Name, imageName)
			if err := os.MkdirAll(cipherDir, 0700); err != nil {
				return fmt.Errorf("creating cipher dir for %s: %w", m.Name, err)
			}
			if err := os.MkdirAll(plainDir, 0700); err != nil {
				return fmt.Errorf("creating plain dir for %s: %w", m.Name, err)
			}
			args := append([]string{"-init"}, extpassArgs...)
			args = append(args, cipherDir)
			cmd := exec.Command("gocryptfs", args...)
			cmd.Env = append(os.Environ(), "GOCRYPTFS_PASSWORD="+passphrase)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("gocryptfs -init for %s: %w", m.Name, err)
			}
		}
		if !isEncryptedMounted(plainDir) {
			if err := os.MkdirAll(plainDir, 0700); err != nil {
				return fmt.Errorf("creating plain dir for %s: %w", m.Name, err)
			}
			gcArgs := append(extpassArgs, "-allow_other", cipherDir, plainDir)
			scopeUnit := fmt.Sprintf("ov-enc-%s-%s", imageName, m.Name)
			scopeArgs := append([]string{"--scope", "--user", "--unit=" + scopeUnit, "--", "gocryptfs"}, gcArgs...)
			cmd := exec.Command("systemd-run", scopeArgs...)
			cmd.Env = append(os.Environ(), "GOCRYPTFS_PASSWORD="+passphrase)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				if stopErr := exec.Command("systemctl", "--user", "stop", scopeUnit+".scope").Run(); stopErr == nil {
					cmd = exec.Command("systemd-run", scopeArgs...)
					cmd.Env = append(os.Environ(), "GOCRYPTFS_PASSWORD="+passphrase)
					cmd.Stdout = os.Stdout
					cmd.Stderr = os.Stderr
					if retryErr := cmd.Run(); retryErr != nil {
						return fmt.Errorf("mounting encrypted volume %s: %w", m.Name, retryErr)
					}
				} else {
					return fmt.Errorf("mounting encrypted volume %s: %w", m.Name, err)
				}
			}
			fmt.Fprintf(os.Stderr, "Mounted encrypted volume %s\n", m.Name)
		}
	}
	return nil
}

// askPassword prompts for a password using systemd-ask-password.
// id is a unique identifier for kernel keyring caching, prompt is shown to the user.
var askPassword = defaultAskPassword

func defaultAskPassword(id, prompt string) (string, error) {
	cmd := exec.Command("systemd-ask-password",
		"--id="+id, "--timeout=0", "--echo=masked", prompt)
	// Ensure tty access for interactive prompt
	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("systemd-ask-password: %w", err)
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// loadEncryptedVolumes loads encrypted volume configs from deploy.yml for an image.
// Returns the deploy volume configs with type=encrypted and the encrypted storage path.
func loadEncryptedVolumes(imageName, instance string) ([]DeployVolumeConfig, string, error) {
	rt, err := ResolveRuntime()
	if err != nil {
		return nil, "", err
	}

	dc, _ := LoadDeployConfig()
	if dc == nil {
		return nil, rt.EncryptedStoragePath, nil
	}

	overlay, ok := dc.Images[deployKey(imageName, instance)]
	if !ok {
		return nil, rt.EncryptedStoragePath, nil
	}

	var encrypted []DeployVolumeConfig
	for _, dv := range overlay.Volumes {
		if dv.Type == "encrypted" {
			encrypted = append(encrypted, dv)
		}
	}
	return encrypted, rt.EncryptedStoragePath, nil
}

// encServiceFilename returns the systemd service filename for a legacy crypto companion unit.
// Used only for cleanup of stale enc services from older ov versions.
func encServiceFilename(imageName string) string {
	return containerName(imageName) + "-enc.service"
}

// hasEncryptedBindMounts returns true if any bind mount is encrypted.
func hasEncryptedBindMounts(mounts []ResolvedBindMount) bool {
	for _, m := range mounts {
		if m.Encrypted {
			return true
		}
	}
	return false
}

// verifyBindMounts checks that all bind mounts are ready to use:
// - Plain mounts: host directory must exist
// - Encrypted mounts: must be mounted (FUSE)
func verifyBindMounts(mounts []ResolvedBindMount, imageName string) error {
	for _, m := range mounts {
		if m.Encrypted {
			if !isEncryptedMounted(m.HostPath) {
				return fmt.Errorf("encrypted bind mount %q for image %q is not mounted; run 'ov config mount %s' first", m.Name, imageName, imageName)
			}
		} else {
			info, err := os.Stat(m.HostPath)
			if err != nil {
				return fmt.Errorf("bind mount %q: host path %q: %w", m.Name, m.HostPath, err)
			}
			if !info.IsDir() {
				return fmt.Errorf("bind mount %q: host path %q is not a directory", m.Name, m.HostPath)
			}
		}
	}
	return nil
}
