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
		if len(fields) >= 2 {
			mountPoint, err := filepath.EvalSymlinks(fields[1])
			if err != nil {
				mountPoint = fields[1]
			}
			if mountPoint == resolved {
				return true
			}
		}
	}
	return false
}

// CryptoCmd groups crypto subcommands
type CryptoCmd struct {
	Init    CryptoInitCmd    `cmd:"" help:"Initialize gocryptfs cipher directories"`
	Mount   CryptoMountCmd   `cmd:"" help:"Mount encrypted volumes (interactive password)"`
	Unmount CryptoUnmountCmd `cmd:"" help:"Unmount encrypted volumes"`
	Status  CryptoStatusCmd  `cmd:"" help:"Show status of all encrypted bind mounts"`
}

// CryptoInitCmd initializes gocryptfs cipher directories
type CryptoInitCmd struct {
	Image  string `arg:"" help:"Image name from images.yml"`
	Volume string `long:"volume" help:"Only initialize this volume (by name)"`
}

func (c *CryptoInitCmd) Run() error {
	mounts, storagePath, err := loadEncryptedMounts(c.Image)
	if err != nil {
		return err
	}

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

		// Run gocryptfs -init
		cmd := exec.Command("gocryptfs", "-init", cipherDir)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("gocryptfs -init for %s: %w", m.Name, err)
		}
		fmt.Fprintf(os.Stderr, "Initialized %s at %s\n", m.Name, cipherDir)
	}
	return nil
}

// CryptoMountCmd mounts encrypted volumes
type CryptoMountCmd struct {
	Image  string `arg:"" help:"Image name from images.yml"`
	Volume string `long:"volume" help:"Only mount this volume (by name)"`
}

func (c *CryptoMountCmd) Run() error {
	mounts, storagePath, err := loadEncryptedMounts(c.Image)
	if err != nil {
		return err
	}

	for _, m := range mounts {
		if c.Volume != "" && m.Name != c.Volume {
			continue
		}

		cipherDir := encryptedCipherDir(storagePath, c.Image, m.Name)
		plainDir := encryptedPlainDir(storagePath, c.Image, m.Name)

		if !isEncryptedInitialized(cipherDir) {
			return fmt.Errorf("encrypted volume %q not initialized; run 'ov crypto init %s' first", m.Name, c.Image)
		}

		if isEncryptedMounted(plainDir) {
			fmt.Fprintf(os.Stderr, "%s: already mounted\n", m.Name)
			continue
		}

		// Ensure plain dir exists
		if err := os.MkdirAll(plainDir, 0700); err != nil {
			return fmt.Errorf("creating plain dir for %s: %w", m.Name, err)
		}

		volName := encryptedVolumeName(c.Image, m.Name)
		extpass := fmt.Sprintf("systemd-ask-password --timeout=0 --echo=masked 'Passphrase for %s:'", volName)
		cmd := exec.Command("gocryptfs", "-extpass", extpass, "-allow_other", cipherDir, plainDir)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("mounting %s: %w", m.Name, err)
		}
		fmt.Fprintf(os.Stderr, "Mounted %s at %s\n", m.Name, plainDir)
	}
	return nil
}

// CryptoUnmountCmd unmounts encrypted volumes
type CryptoUnmountCmd struct {
	Image  string `arg:"" help:"Image name from images.yml"`
	Volume string `long:"volume" help:"Only unmount this volume (by name)"`
}

func (c *CryptoUnmountCmd) Run() error {
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

// CryptoStatusCmd shows the status of encrypted bind mounts
type CryptoStatusCmd struct {
	Image string `arg:"" help:"Image name from images.yml"`
}

func (c *CryptoStatusCmd) Run() error {
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

// loadEncryptedMounts loads the encrypted bind mounts for an image and returns them
// along with the resolved encrypted storage path.
func loadEncryptedMounts(imageName string) ([]BindMountConfig, string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, "", err
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		return nil, "", err
	}

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

	rt, err := ResolveRuntime()
	if err != nil {
		return nil, "", err
	}

	return encrypted, rt.EncryptedStoragePath, nil
}

// generateCryptoUnit produces a companion systemd service unit for gocryptfs mounts.
func generateCryptoUnit(imageName string, mounts []ResolvedBindMount, storagePath string) string {
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

	b.WriteString(fmt.Sprintf("# %s-crypto.service (generated by ov enable)\n", name))
	b.WriteString("[Unit]\n")
	b.WriteString(fmt.Sprintf("Description=gocryptfs mounts for %s\n", name))
	b.WriteString("\n[Service]\n")
	b.WriteString("Type=oneshot\n")
	b.WriteString("RemainAfterExit=yes\n")

	for _, m := range encrypted {
		volName := encryptedVolumeName(imageName, m.Name)
		cipherDir := encryptedCipherDir(storagePath, imageName, m.Name)
		plainDir := encryptedPlainDir(storagePath, imageName, m.Name)
		extpass := fmt.Sprintf("systemd-ask-password --timeout=0 --echo=masked 'Passphrase for %s:'", volName)
		b.WriteString(fmt.Sprintf("ExecStart=gocryptfs -extpass \"%s\" -allow_other %s %s\n", extpass, cipherDir, plainDir))
	}
	for _, m := range encrypted {
		plainDir := encryptedPlainDir(storagePath, imageName, m.Name)
		b.WriteString(fmt.Sprintf("ExecStop=-fusermount3 -u %s\n", plainDir))
	}

	b.WriteString("\n[Install]\n")
	b.WriteString("WantedBy=default.target\n")

	return b.String()
}

// cryptoServiceFilename returns the systemd service filename for a crypto companion unit.
func cryptoServiceFilename(imageName string) string {
	return containerName(imageName) + "-crypto.service"
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
				return fmt.Errorf("encrypted bind mount %q for image %q is not mounted; run 'ov crypto mount %s' first", m.Name, imageName, imageName)
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
