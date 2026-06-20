package main

import (
	"fmt"
	"os"
	"strings"
)

// osReleasePath is the path to the os-release file, overridable for testing.
var osReleasePath = "/etc/os-release"

// Distro is the HOST distribution detected at runtime (from /etc/os-release).
// Distinct from the build-vocabulary distro definition (spec.DistroDef / the
// CUE #Distro) — this one describes the machine charly runs ON, not an image's
// package format.
type Distro struct {
	ID      string // "arch", "fedora", "debian", "ubuntu", etc.
	Name    string // "Arch Linux", "Fedora Linux", etc.
	Manager string // "pacman -S", "sudo dnf install", "sudo apt-get install"
}

// detectDistro reads /etc/os-release and returns the detected distribution.
func detectDistro() Distro {
	data, err := os.ReadFile(osReleasePath)
	if err != nil {
		return Distro{ID: "unknown", Name: "Unknown", Manager: ""}
	}
	return parseOsRelease(string(data))
}

// parseOsRelease parses os-release content and returns a Distro.
func parseOsRelease(content string) Distro {
	d := Distro{ID: "unknown", Name: "Unknown"}
	for line := range strings.SplitSeq(content, "\n") {
		if after, ok := strings.CutPrefix(line, "ID="); ok {
			d.ID = strings.Trim(after, "\"")
		}
		if after, ok := strings.CutPrefix(line, "NAME="); ok {
			d.Name = strings.Trim(after, "\"")
		}
	}
	switch d.ID {
	case "arch":
		d.Manager = "pacman -S"
	case "fedora", "rhel", "centos", "rocky", "almalinux":
		d.Manager = "sudo dnf install"
	case "debian", "ubuntu", "pop", "linuxmint":
		d.Manager = "sudo apt-get install"
	case "opensuse-tumbleweed", "opensuse-leap":
		d.Manager = "sudo zypper install"
	}
	return d
}

// installHints maps binary names to per-distro package names.
var installHints = map[string]map[string]string{
	"docker":               {"arch": "docker", "fedora": "docker-ce", "debian": "docker-ce"},
	"podman":               {"arch": "podman", "fedora": "podman", "debian": "podman"},
	"git":                  {"arch": "git", "fedora": "git", "debian": "git"},
	"skopeo":               {"arch": "skopeo", "fedora": "skopeo", "debian": "skopeo"},
	"gocryptfs":            {"arch": "gocryptfs", "fedora": "gocryptfs", "debian": "gocryptfs"},
	"fusermount3":          {"arch": "fuse3", "fedora": "fuse3", "debian": "fuse3"},
	"systemd-ask-password": {"arch": "systemd", "fedora": "systemd", "debian": "systemd"},
	"qemu-system-x86_64":   {"arch": "qemu-full", "fedora": "qemu-kvm", "debian": "qemu-system-x86"},
	"qemu-system-aarch64":  {"arch": "qemu-full", "fedora": "qemu-kvm", "debian": "qemu-system-arm"},
	"qemu-img":             {"arch": "qemu-img", "fedora": "qemu-img", "debian": "qemu-utils"},
	"virtiofsd":            {"arch": "virtiofsd", "fedora": "virtiofsd", "debian": "virtiofsd"},
	"virsh":                {"arch": "libvirt", "fedora": "libvirt-client", "debian": "libvirt-clients"},
	"ssh":                  {"arch": "openssh", "fedora": "openssh-clients", "debian": "openssh-client"},
	"script":               {"arch": "util-linux", "fedora": "util-linux", "debian": "bsdutils"},
	"systemctl":            {"arch": "systemd", "fedora": "systemd", "debian": "systemd"},
	"tailscale":            {"arch": "tailscale", "fedora": "tailscale", "debian": "tailscale"},
	"cloudflared":          {"arch": "AUR: yay -S cloudflared-bin", "fedora": "cloudflared", "debian": "cloudflared"},
	"nvidia-smi":           {"arch": "nvidia-utils", "fedora": "nvidia-driver", "debian": "nvidia-utils"},
	"gvproxy":              {"arch": "AUR: yay -S gvisor-tap-vsock", "fedora": "gvisor-tap-vsock", "debian": "golang-github-containers-gvisor-tap-vsock"},
}

// InstallHint returns a distro-appropriate install command for the given binary.
// Returns an empty string if no hint is available.
func InstallHint(binary string) string {
	distro := detectDistro()
	return distro.installHint(binary)
}

func (d Distro) installHint(binary string) string {
	if d.Manager == "" {
		return binary
	}
	if pkgMap, ok := installHints[binary]; ok {
		// Try exact distro ID first
		if pkg, ok := pkgMap[d.ID]; ok {
			// AUR packages include their own install command
			if strings.Contains(pkg, "AUR:") {
				return strings.TrimSpace(pkg[strings.Index(pkg, "AUR:")+4:])
			}
			return fmt.Sprintf("%s %s", d.Manager, pkg)
		}
		// Try distro family
		family := distroFamily(d.ID)
		if pkg, ok := pkgMap[family]; ok {
			if strings.Contains(pkg, "AUR:") {
				return strings.TrimSpace(pkg[strings.Index(pkg, "AUR:")+4:])
			}
			return fmt.Sprintf("%s %s", d.Manager, pkg)
		}
	}
	return fmt.Sprintf("%s %s", d.Manager, binary)
}

// distroFamily maps distro IDs to their base family for install hint lookup.
func distroFamily(id string) string {
	switch id {
	case "ubuntu", "pop", "linuxmint":
		return "debian"
	case "rhel", "centos", "rocky", "almalinux":
		return "fedora"
	case "opensuse-tumbleweed", "opensuse-leap":
		return "fedora" // similar package names
	default:
		return id
	}
}
