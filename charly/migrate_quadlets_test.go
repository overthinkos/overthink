package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
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

// TestDetectStaleEncryptedQuadlets exercises the full detector against a
// scratch deploy.yml + scratch quadlet directory. Builds three deploys —
// one that's stale, one that's already migrated, and one with no encrypted
// volumes (must be ignored regardless of quadlet content).
func TestDetectStaleEncryptedQuadlets(t *testing.T) {
	dir := t.TempDir()
	xdg := filepath.Join(dir, "xdg")
	if err := os.MkdirAll(filepath.Join(xdg, "charly"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", xdg)

	deployYAML := `version: 2026.172.0006
immich:
    pod:
        image: immich
    immich-volume:
        volume:
            - {name: data, type: encrypted}
jupyter:
    pod:
        image: jupyter
    jupyter-volume:
        volume:
            - {name: data, type: encrypted}
webapp:
    pod:
        image: webapp
    webapp-volume:
        volume:
            - {name: data, type: bind, host: /tmp}
`
	if err := os.WriteFile(filepath.Join(xdg, "charly", "charly.yml"), []byte(deployYAML), 0o600); err != nil {
		t.Fatal(err)
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
	// webapp: no encrypted volumes — quadlet content irrelevant, must
	// be ignored even with no ExecStartPre.
	if err := os.WriteFile(filepath.Join(quadletDir, "ov-webapp.container"), []byte("[Service]\nRestart=always\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	stale, err := DetectStaleEncryptedQuadlets(quadletDir)
	if err != nil {
		t.Fatalf("DetectStaleEncryptedQuadlets: %v", err)
	}
	want := []string{"immich"}
	if !reflect.DeepEqual(stale, want) {
		t.Errorf("stale = %v, want %v", stale, want)
	}
}

// TestDetectStaleEncryptedQuadlets_NoQuadletOnDisk asserts an encrypted
// deploy without a corresponding quadlet (i.e., user has the deploy entry
// but never ran `charly config <name>`) is silently skipped — there's nothing
// to migrate, and we don't want to noise the user with a "missing"
// warning.
func TestDetectStaleEncryptedQuadlets_NoQuadletOnDisk(t *testing.T) {
	dir := t.TempDir()
	xdg := filepath.Join(dir, "xdg")
	if err := os.MkdirAll(filepath.Join(xdg, "charly"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if err := os.WriteFile(filepath.Join(xdg, "charly", "charly.yml"), []byte("version: 2026.172.0006\nghost:\n    pod:\n        image: ghost\n    ghost-volume:\n        volume:\n            - {name: data, type: encrypted}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stale, err := DetectStaleEncryptedQuadlets(filepath.Join(dir, "quadlets-empty"))
	if err != nil {
		t.Fatalf("DetectStaleEncryptedQuadlets: %v", err)
	}
	if len(stale) != 0 {
		t.Errorf("stale = %v, want empty (no quadlet on disk → skipped)", stale)
	}
}

// TestCipherPopulatedPlainEmpty covers the verifyBindMounts hardening
// helper that discriminates the dangerous-state pre-start case from a
// fresh setup with no harm yet.
func TestCipherPopulatedPlainEmpty(t *testing.T) {
	mk := func(t *testing.T, cipherFiles, plainFiles []string) (cipher, plain string) {
		t.Helper()
		dir := t.TempDir()
		cipher = filepath.Join(dir, "cipher")
		plain = filepath.Join(dir, "plain")
		if err := os.MkdirAll(cipher, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(plain, 0o700); err != nil {
			t.Fatal(err)
		}
		for _, f := range cipherFiles {
			if err := os.WriteFile(filepath.Join(cipher, f), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		for _, f := range plainFiles {
			if err := os.WriteFile(filepath.Join(plain, f), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return cipher, plain
	}

	t.Run("dangerous: cipher populated, plain empty", func(t *testing.T) {
		cipher, plain := mk(t,
			[]string{"gocryptfs.conf", "gocryptfs.diriv", "AbCdEfGh", "QrStUvWx"},
			nil,
		)
		if !cipherPopulatedPlainEmpty(cipher, plain) {
			t.Error("expected true (cipher has user data, plain empty)")
		}
	})

	t.Run("benign: cipher metadata-only, plain empty (fresh init)", func(t *testing.T) {
		cipher, plain := mk(t,
			[]string{"gocryptfs.conf", "gocryptfs.diriv"},
			nil,
		)
		if cipherPopulatedPlainEmpty(cipher, plain) {
			t.Error("expected false (cipher only has metadata files)")
		}
	})

	t.Run("benign: plain non-empty (FUSE was mounted then containerwrote, OR plain has stale plaintext drift)", func(t *testing.T) {
		cipher, plain := mk(t,
			[]string{"gocryptfs.conf", "AbCdEfGh"},
			[]string{"some-file"},
		)
		if cipherPopulatedPlainEmpty(cipher, plain) {
			t.Error("expected false (plain not empty — different failure class)")
		}
	})

	t.Run("missing cipher dir", func(t *testing.T) {
		dir := t.TempDir()
		plain := filepath.Join(dir, "plain")
		if err := os.MkdirAll(plain, 0o700); err != nil {
			t.Fatal(err)
		}
		if cipherPopulatedPlainEmpty(filepath.Join(dir, "missing-cipher"), plain) {
			t.Error("expected false (cipher dir does not exist)")
		}
	})

	t.Run("missing plain dir", func(t *testing.T) {
		dir := t.TempDir()
		cipher := filepath.Join(dir, "cipher")
		if err := os.MkdirAll(cipher, 0o700); err != nil {
			t.Fatal(err)
		}
		if cipherPopulatedPlainEmpty(cipher, filepath.Join(dir, "missing-plain")) {
			t.Error("expected false (plain dir does not exist)")
		}
	})
}
