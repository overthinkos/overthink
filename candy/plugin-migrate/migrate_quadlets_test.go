package migrate

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/overthinkos/overthink/charly/plugin/kit"
)

func TestQuadletHasMountHook(t *testing.T) {
	cases := []struct {
		name string
		body string
		ref  string
		want bool
	}{
		{
			name: "modern hook present",
			body: "[Service]\nExecStartPre=/usr/bin/charly config mount immich\nTimeoutStartSec=0\n",
			ref:  "immich",
			want: true,
		},
		{
			name: "bare charly binary path",
			body: "[Service]\nExecStartPre=charly config mount immich\n",
			ref:  "immich",
			want: true,
		},
		{
			name: "tolerant to leading whitespace",
			body: "[Service]\n  ExecStartPre=/home/u/.local/bin/charly config mount immich\n",
			ref:  "immich",
			want: true,
		},
		{
			name: "trailing flag tolerated",
			body: "ExecStartPre=/usr/bin/charly config mount immich --quiet\n",
			ref:  "immich",
			want: true,
		},
		{
			name: "no hook at all (the immich-2026-04 incident shape)",
			body: "[Service]\nRestart=always\nTimeoutStartSec=900\n",
			ref:  "immich",
			want: false,
		},
		{
			name: "hook for a different image (prefix collision)",
			body: "ExecStartPre=/usr/bin/charly config mount immich-ml\n",
			ref:  "immich",
			want: false,
		},
		{
			name: "hook is for the LONGER image name when looking up the shorter",
			body: "ExecStartPre=/usr/bin/charly config mount foo-bar\n",
			ref:  "foo",
			want: false,
		},
		{
			name: "non-mount ExecStartPre (e.g., a custom user hook)",
			body: "ExecStartPre=/usr/bin/echo about to start\n",
			ref:  "immich",
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := quadletHasMountHook(tc.body, tc.ref); got != tc.want {
				t.Errorf("quadletHasMountHook(ref=%q) = %v, want %v\n  body=%q", tc.ref, got, tc.want, tc.body)
			}
		})
	}
}

// TestDetectStaleEncryptedQuadlets exercises the detector against a scratch quadlet
// directory. The per-deploy encrypted-volume summary is HOST-PRELIFTED by charly
// core (from LoadBundleConfig) since C13a, so the test feeds the summary directly
// (it no longer writes a host config): one stale deploy, one already migrated, one
// with no encrypted volumes.
func TestDetectStaleEncryptedQuadlets(t *testing.T) {
	dir := t.TempDir()
	summary := []kit.MigrateBundleVolume{
		{Name: "immich", Target: "pod", HasEncrypted: true},
		{Name: "jupyter", Target: "pod", HasEncrypted: true},
		{Name: "webapp", Target: "pod", HasEncrypted: false},
	}

	quadletDir := filepath.Join(dir, "quadlets")
	if err := os.MkdirAll(quadletDir, 0o700); err != nil {
		t.Fatal(err)
	}

	// immich: stale (no ExecStartPre)
	staleBody := `[Container]
Image=ghcr.io/x/immich:1
[Service]
Restart=always
TimeoutStartSec=900
[Install]
WantedBy=default.target
`
	if err := os.WriteFile(filepath.Join(quadletDir, "ov-immich.container"), []byte(staleBody), 0o600); err != nil {
		t.Fatal(err)
	}
	// jupyter: already migrated
	freshBody := `[Container]
Image=ghcr.io/x/jupyter:1
[Service]
Restart=always
ExecStartPre=/usr/bin/charly config mount jupyter
TimeoutStartSec=0
[Install]
WantedBy=default.target
`
	if err := os.WriteFile(filepath.Join(quadletDir, "ov-jupyter.container"), []byte(freshBody), 0o600); err != nil {
		t.Fatal(err)
	}
	// webapp: no encrypted volumes — quadlet content irrelevant, must be ignored.
	if err := os.WriteFile(filepath.Join(quadletDir, "ov-webapp.container"), []byte("[Service]\nRestart=always\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	stale := DetectStaleEncryptedQuadlets(quadletDir, summary)
	want := []string{"immich"}
	if !reflect.DeepEqual(stale, want) {
		t.Errorf("stale = %v, want %v", stale, want)
	}
}

// TestDetectStaleEncryptedQuadlets_NoQuadletOnDisk asserts an encrypted deploy with
// no corresponding quadlet on disk is silently skipped.
func TestDetectStaleEncryptedQuadlets_NoQuadletOnDisk(t *testing.T) {
	dir := t.TempDir()
	summary := []kit.MigrateBundleVolume{{Name: "ghost", Target: "pod", HasEncrypted: true}}
	stale := DetectStaleEncryptedQuadlets(filepath.Join(dir, "quadlets-empty"), summary)
	if len(stale) != 0 {
		t.Errorf("stale = %v, want empty (no quadlet on disk → skipped)", stale)
	}
}

// TestCipherPopulatedPlainEmpty moved to charly core (charly/enc_cipher_test.go) —
// it exercises cipherPopulatedPlainEmpty (charly/enc.go, package-main), not this
// migrator (C13a).
