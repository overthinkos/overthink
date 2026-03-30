package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// encInit initializes gocryptfs cipher directories for an image.
// If volume is non-empty, only that volume is initialized.
func encInit(imageName, volume string) error {
	mounts, storagePath, err := loadEncryptedVolumes(imageName)
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

		cipherDir := encryptedCipherDir(storagePath, imageName, m.Name)
		plainDir := encryptedPlainDir(storagePath, imageName, m.Name)

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
func encMount(imageName, volume string) error {
	mounts, storagePath, err := loadEncryptedVolumes(imageName)
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

		cipherDir := encryptedCipherDir(storagePath, imageName, m.Name)
		plainDir := encryptedPlainDir(storagePath, imageName, m.Name)

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

		args := append(extpassArgs, "-allow_other", cipherDir, plainDir)
		cmd := exec.Command("gocryptfs", args...)
		cmd.Env = append(os.Environ(), "GOCRYPTFS_PASSWORD="+passphrase)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("mounting %s: %w", m.Name, err)
		}
		fmt.Fprintf(os.Stderr, "Mounted %s at %s\n", m.Name, plainDir)
	}
	return nil
}

// encUnmount unmounts encrypted volumes for an image.
// If volume is non-empty, only that volume is unmounted.
func encUnmount(imageName, volume string) error {
	mounts, storagePath, err := loadEncryptedVolumes(imageName)
	if err != nil {
		return err
	}

	for _, m := range mounts {
		if volume != "" && m.Name != volume {
			continue
		}

		plainDir := encryptedPlainDir(storagePath, imageName, m.Name)

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
		fmt.Fprintf(os.Stderr, "Unmounted %s\n", m.Name)
	}
	return nil
}

// encStatus prints the status of encrypted bind mounts for an image.
func encStatus(imageName string) error {
	mounts, storagePath, err := loadEncryptedVolumes(imageName)
	if err != nil {
		return err
	}

	if len(mounts) == 0 {
		fmt.Println("No encrypted bind mounts configured")
		return nil
	}

	fmt.Printf("%-20s %-12s %-8s %s\n", "NAME", "INITIALIZED", "MOUNTED", "PATH")
	for _, m := range mounts {
		cipherDir := encryptedCipherDir(storagePath, imageName, m.Name)
		plainDir := encryptedPlainDir(storagePath, imageName, m.Name)

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
func encPasswd(imageName string) error {
	mounts, storagePath, err := loadEncryptedVolumes(imageName)
	if err != nil {
		return err
	}

	if len(mounts) == 0 {
		return fmt.Errorf("image %q has no encrypted bind mounts", imageName)
	}

	// All volumes must be unmounted before changing password
	for _, m := range mounts {
		plainDir := encryptedPlainDir(storagePath, imageName, m.Name)
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
		cipherDir := encryptedCipherDir(storagePath, imageName, m.Name)

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
func ensureEncryptedMounts(imageName string, autoGenerate bool) error {
	mounts, storagePath, err := loadEncryptedVolumes(imageName)
	if err != nil || len(mounts) == 0 {
		return nil // no encrypted mounts configured
	}

	anyNotReady := false
	for _, m := range mounts {
		cipherDir := encryptedCipherDir(storagePath, imageName, m.Name)
		plainDir := encryptedPlainDir(storagePath, imageName, m.Name)
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
		cipherDir := encryptedCipherDir(storagePath, imageName, m.Name)
		plainDir := encryptedPlainDir(storagePath, imageName, m.Name)

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
			args := append(extpassArgs, "-allow_other", cipherDir, plainDir)
			cmd := exec.Command("gocryptfs", args...)
			cmd.Env = append(os.Environ(), "GOCRYPTFS_PASSWORD="+passphrase)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("mounting encrypted volume %s: %w", m.Name, err)
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
func loadEncryptedVolumes(imageName string) ([]DeployVolumeConfig, string, error) {
	rt, err := ResolveRuntime()
	if err != nil {
		return nil, "", err
	}

	dc, _ := LoadDeployConfig()
	if dc == nil {
		return nil, rt.EncryptedStoragePath, nil
	}

	overlay, ok := dc.Images[imageName]
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

// generateEncUnit produces a companion systemd service unit for gocryptfs mounts.
// It delegates to `ov config mount`/`ov config unmount` which have full credential-store
// integration (kdbx → keyring → interactive prompt via systemd-ask-password).
func generateEncUnit(imageName string, mounts []ResolvedBindMount, ovBin string) string {
	var encrypted []ResolvedBindMount
	for _, m := range mounts {
		if m.Encrypted {
			encrypted = append(encrypted, m)
		}
	}
	if len(encrypted) == 0 {
		return ""
	}

	name := containerName(imageName)
	var b strings.Builder

	b.WriteString(fmt.Sprintf("# %s-enc.service (generated by ov config)\n", name))
	b.WriteString("[Unit]\n")
	b.WriteString(fmt.Sprintf("Description=gocryptfs mounts for %s\n", name))
	b.WriteString("\n[Service]\n")
	b.WriteString("Type=oneshot\n")
	b.WriteString("RemainAfterExit=yes\n")
	b.WriteString(fmt.Sprintf("ExecStart=%s config mount %s\n", ovBin, imageName))
	b.WriteString(fmt.Sprintf("ExecStop=%s config unmount %s\n", ovBin, imageName))
	b.WriteString("\n[Install]\n")
	b.WriteString("WantedBy=default.target\n")

	return b.String()
}

// encServiceFilename returns the systemd service filename for a crypto companion unit.
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
