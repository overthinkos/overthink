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

// installHints maps binary names to per-distro package names, read from the install_hints
// directive in the embedded charly.yml (Phase 4: data moved out of Go) via the shared
// minimal decoder. Panics if the directive is empty/malformed (a build-time invariant).
var installHints = parseEmbeddedInstallHints()

// parseEmbeddedInstallHints reads the install_hints map from the embedded charly.yml.
func parseEmbeddedInstallHints() map[string]map[string]string {
	var doc struct {
		InstallHints map[string]map[string]string `yaml:"install_hints"`
	}
	unmarshalEmbeddedDefaults(&doc)
	if len(doc.InstallHints) == 0 {
		panic("distro: embedded charly.yml has no install_hints: directive")
	}
	return doc.InstallHints
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
