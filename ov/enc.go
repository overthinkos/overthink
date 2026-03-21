package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// BindMountConfig represents a bind mount declaration in images.yml
type BindMountConfig struct {
	Name      string `yaml:"name"`
	Host      string `yaml:"host,omitempty"`      // host path (plain mounts only)
	Path      string `yaml:"path"`                // container path
	Encrypted bool   `yaml:"encrypted,omitempty"` // true = gocryptfs managed
}

// ResolvedBindMount is ready for -v flags
type ResolvedBindMount struct {
	Name      string // e.g. "secrets"
	HostPath  string // effective host path (plain: expanded host, encrypted: plain dir)
	ContPath  string // container path (expanded)
	Encrypted bool   // for status/mount checks
}

// resolveBindMounts resolves bind mount configs into ready-to-use mounts.
// home is the container user's home dir (for expanding container paths).
// storagePath is the encrypted storage base dir.
func resolveBindMounts(imageName string, mounts []BindMountConfig, home, storagePath string) []ResolvedBindMount {
	var resolved []ResolvedBindMount
	for _, m := range mounts {
		rm := ResolvedBindMount{
			Name:      m.Name,
			ContPath:  expandHome(m.Path, home),
			Encrypted: m.Encrypted,
		}
		if m.Encrypted {
			rm.HostPath = encryptedPlainDir(storagePath, imageName, m.Name)
		} else {
			rm.HostPath = expandHostHome(m.Host)
		}
		resolved = append(resolved, rm)
	}
	return resolved
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

// EncCmd groups crypto subcommands
type EncCmd struct {
	Init    EncInitCmd    `cmd:"" help:"Initialize gocryptfs cipher directories"`
	Mount   EncMountCmd   `cmd:"" help:"Mount encrypted volumes (interactive password)"`
	Unmount EncUnmountCmd `cmd:"" help:"Unmount encrypted volumes"`
	Passwd  EncPasswdCmd  `cmd:"" help:"Change encryption password"`
	Status  EncStatusCmd  `cmd:"" help:"Show status of all encrypted bind mounts"`
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

// EncInitCmd initializes gocryptfs cipher directories
type EncInitCmd struct {
	Image  string `arg:"" help:"Image name from images.yml"`
	Volume string `long:"volume" help:"Only initialize this volume (by name)"`
}

func (c *EncInitCmd) Run() error {
	mounts, storagePath, err := loadEncryptedMounts(c.Image)
	if err != nil {
		return err
	}

	volID := "ov-" + c.Image
	extpassArgs, cleanup := encExtpassArgs(volID)
	defer cleanup()

	for _, m := range mounts {
		if c.Volume != "" && m.Name != c.Volume {
			continue
		}

		cipherDir := encryptedCipherDir(storagePath, c.Image, m.Name)
		plainDir := encryptedPlainDir(storagePath, c.Image, m.Name)

		if isEncryptedInitialized(cipherDir) {
			fmt.Fprintf(os.Stderr, "%s: already initialized\n", m.Name)
			continue
		}

		// Create directories
		if err := os.MkdirAll(cipherDir, 0700); err != nil {
			return fmt.Errorf("creating cipher dir for %s: %w", m.Name, err)
		}
		if err := os.MkdirAll(plainDir, 0700); err != nil {
			return fmt.Errorf("creating plain dir for %s: %w", m.Name, err)
		}

		// Run gocryptfs -init with shared password
		args := append([]string{"-init"}, extpassArgs...)
		args = append(args, cipherDir)
		cmd := exec.Command("gocryptfs", args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("gocryptfs -init for %s: %w", m.Name, err)
		}
		fmt.Fprintf(os.Stderr, "Initialized %s at %s\n", m.Name, cipherDir)
	}
	return nil
}

// EncMountCmd mounts encrypted volumes
type EncMountCmd struct {
	Image  string `arg:"" help:"Image name from images.yml"`
	Volume string `long:"volume" help:"Only mount this volume (by name)"`
}

func (c *EncMountCmd) Run() error {
	mounts, storagePath, err := loadEncryptedMounts(c.Image)
	if err != nil {
		return err
	}

	// Use per-image password ID so all volumes share a single prompt
	volID := "ov-" + c.Image
	extpassArgs, cleanup := encExtpassArgs(volID)
	defer cleanup()

	for _, m := range mounts {
		if c.Volume != "" && m.Name != c.Volume {
			continue
		}

		cipherDir := encryptedCipherDir(storagePath, c.Image, m.Name)
		plainDir := encryptedPlainDir(storagePath, c.Image, m.Name)

		if !isEncryptedInitialized(cipherDir) {
			return fmt.Errorf("encrypted volume %q not initialized; run 'ov enc init %s' first", m.Name, c.Image)
		}

		if isEncryptedMounted(plainDir) {
			fmt.Fprintf(os.Stderr, "%s: already mounted\n", m.Name)
			continue
		}

		// Ensure plain dir exists
		if err := os.MkdirAll(plainDir, 0700); err != nil {
			return fmt.Errorf("creating plain dir for %s: %w", m.Name, err)
		}

		args := append(extpassArgs, "-allow_other", cipherDir, plainDir)
		cmd := exec.Command("gocryptfs", args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("mounting %s: %w", m.Name, err)
		}
		fmt.Fprintf(os.Stderr, "Mounted %s at %s\n", m.Name, plainDir)
	}
	return nil
}

// EncUnmountCmd unmounts encrypted volumes
type EncUnmountCmd struct {
	Image  string `arg:"" help:"Image name from images.yml"`
	Volume string `long:"volume" help:"Only unmount this volume (by name)"`
}

func (c *EncUnmountCmd) Run() error {
	mounts, storagePath, err := loadEncryptedMounts(c.Image)
	if err != nil {
		return err
	}

	for _, m := range mounts {
		if c.Volume != "" && m.Name != c.Volume {
			continue
		}

		plainDir := encryptedPlainDir(storagePath, c.Image, m.Name)

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

// EncStatusCmd shows the status of encrypted bind mounts
type EncStatusCmd struct {
	Image string `arg:"" help:"Image name from images.yml"`
}

func (c *EncStatusCmd) Run() error {
	mounts, storagePath, err := loadEncryptedMounts(c.Image)
	if err != nil {
		return err
	}

	if len(mounts) == 0 {
		fmt.Println("No encrypted bind mounts configured")
		return nil
	}

	fmt.Printf("%-20s %-12s %-8s %s\n", "NAME", "INITIALIZED", "MOUNTED", "PATH")
	for _, m := range mounts {
		cipherDir := encryptedCipherDir(storagePath, c.Image, m.Name)
		plainDir := encryptedPlainDir(storagePath, c.Image, m.Name)

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

// EncPasswdCmd changes the encryption password for all encrypted volumes of an image.
type EncPasswdCmd struct {
	Image string `arg:"" help:"Image name from images.yml"`
}

func (c *EncPasswdCmd) Run() error {
	mounts, storagePath, err := loadEncryptedMounts(c.Image)
	if err != nil {
		return err
	}

	if len(mounts) == 0 {
		return fmt.Errorf("image %q has no encrypted bind mounts", c.Image)
	}

	// All volumes must be unmounted before changing password
	for _, m := range mounts {
		plainDir := encryptedPlainDir(storagePath, c.Image, m.Name)
		if isEncryptedMounted(plainDir) {
			return fmt.Errorf("encrypted volume %q is still mounted; run 'ov enc unmount %s' first", m.Name, c.Image)
		}
	}

	volID := "ov-" + c.Image

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
		cipherDir := encryptedCipherDir(storagePath, c.Image, m.Name)

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

// loadEncryptedMounts loads the encrypted bind mounts for an image and returns them
// along with the resolved encrypted storage path.
func loadEncryptedMounts(imageName string) ([]BindMountConfig, string, error) {
	rt, err := ResolveRuntime()
	if err != nil {
		return nil, "", err
	}

	// Try images.yml first
	dir, _ := os.Getwd()
	cfg, cfgErr := LoadConfig(dir)
	if cfgErr == nil {
		img, ok := cfg.Images[imageName]
		if !ok {
			return nil, "", fmt.Errorf("image %q not found in images.yml", imageName)
		}

		var encrypted []BindMountConfig
		for _, m := range img.BindMounts {
			if m.Encrypted {
				encrypted = append(encrypted, m)
			}
		}
		return encrypted, rt.EncryptedStoragePath, nil
	}

	// Label fallback
	engine := ResolveImageEngineForDeploy(imageName, rt.RunEngine)
	imageRef := fmt.Sprintf("%s:latest", imageName)
	meta, metaErr := ExtractMetadata(engine, imageRef)
	if metaErr != nil {
		return nil, "", metaErr
	}
	if meta == nil {
		return nil, "", fmt.Errorf("image %s has no embedded metadata; rebuild with latest ov", imageRef)
	}

	var encrypted []BindMountConfig
	for _, m := range meta.BindMounts {
		if m.Encrypted {
			encrypted = append(encrypted, BindMountConfig{
				Name:      m.Name,
				Path:      m.Path,
				Encrypted: true,
			})
		}
	}
	return encrypted, rt.EncryptedStoragePath, nil
}

// generateEncUnit produces a companion systemd service unit for gocryptfs mounts.
func generateEncUnit(imageName string, mounts []ResolvedBindMount, storagePath string) string {
	// Filter to encrypted mounts only
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

	b.WriteString(fmt.Sprintf("# %s-enc.service (generated by ov enable)\n", name))
	b.WriteString("[Unit]\n")
	b.WriteString(fmt.Sprintf("Description=gocryptfs mounts for %s\n", name))
	b.WriteString("\n[Service]\n")
	b.WriteString("Type=oneshot\n")
	b.WriteString("RemainAfterExit=yes\n")

	// Use per-image password ID so all volumes share a single prompt.
	// Each ExecStart checks if already FUSE-mounted (e.g. via ov enc mount) and skips if so.
	// Quoting: outer double-quotes (systemd), inner single-quotes (bash → gocryptfs extpass).
	volID := "ov-" + imageName
	extpass := fmt.Sprintf("systemd-ask-password --id=%s --timeout=0 --echo=masked Passphrase:", volID)

	for _, m := range encrypted {
		cipherDir := encryptedCipherDir(storagePath, imageName, m.Name)
		plainDir := encryptedPlainDir(storagePath, imageName, m.Name)
		b.WriteString(fmt.Sprintf("ExecStart=/bin/bash -c \"findmnt -n -o FSTYPE %s 2>/dev/null | grep -q fuse.gocryptfs && exit 0; gocryptfs -extpass '%s' -allow_other %s %s\"\n",
			plainDir, extpass, cipherDir, plainDir))
	}
	for _, m := range encrypted {
		plainDir := encryptedPlainDir(storagePath, imageName, m.Name)
		b.WriteString(fmt.Sprintf("ExecStop=-fusermount3 -u %s\n", plainDir))
	}

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
				return fmt.Errorf("encrypted bind mount %q for image %q is not mounted; run 'ov enc mount %s' first", m.Name, imageName, imageName)
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
