package migrate

import "testing"

// TestArchRenameText is the coverage whose absence let the quay-mirror
// corruption ship: external registry refs containing "archlinux" (the quay
// mirror, the docker.io base, a ghcr ns ref) MUST stay verbatim, while genuine
// internal identifiers (the distro tag, archlinux-builder, archlinux-pacstrap*,
// package-map keys) MUST rename to arch.
func TestArchRenameText(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		// External registry refs — protected by SHAPE (regex), unchanged.
		{"quay mirror", "base: quay.io/archlinux/archlinux:latest", "base: quay.io/archlinux/archlinux:latest"},
		{"docker base", "base: docker.io/library/archlinux:latest", "base: docker.io/library/archlinux:latest"},
		{"ghcr ns ref", "base: ghcr.io/foo/archlinux-base:1", "base: ghcr.io/foo/archlinux-base:1"},
		{"host:port ref", "base: registry.example:5000/team/archlinux:edge", "base: registry.example:5000/team/archlinux:edge"},
		// Non-registry literals — protected, unchanged.
		{"keyring populate", "pacman-key --populate archlinux", "pacman-key --populate archlinux"},
		{"mirror host", "Server = https://archlinux.org/$repo", "Server = https://archlinux.org/$repo"},
		{"keyring pkg", "  - archlinux-keyring", "  - archlinux-keyring"},
		// Internal identifiers — renamed.
		{"distro tag", "distro: archlinux", "distro: arch"},
		{"image name", "image: archlinux-builder", "image: arch-builder"},
		{"pacstrap", "  - archlinux-pacstrap", "  - arch-pacstrap"},
		{"base internal", "base: archlinux", "base: arch"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := archRenameText(tt.in); got != tt.want {
				t.Errorf("archRenameText(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}

	// Idempotence: a second pass leaves a renamed string untouched.
	once := archRenameText("distro: archlinux\nbase: quay.io/archlinux/archlinux:latest\n")
	if twice := archRenameText(once); twice != once {
		t.Errorf("not idempotent:\n once=%q\ntwice=%q", once, twice)
	}
}
