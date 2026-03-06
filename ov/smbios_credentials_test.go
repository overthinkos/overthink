package main

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestKeyToRootTmpfilesD(t *testing.T) {
	pubkey := "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQC..."
	result := KeyToRootTmpfilesD(pubkey)

	// Must create /root/.ssh directory
	if !strings.Contains(result, "d /root/.ssh 0750 - - -") {
		t.Errorf("missing /root/.ssh directory line, got: %s", result)
	}

	// Must create authorized_keys with base64-encoded key
	b64Key := base64.StdEncoding.EncodeToString([]byte(pubkey))
	expected := "f+~ /root/.ssh/authorized_keys 700 - - - " + b64Key
	if !strings.Contains(result, expected) {
		t.Errorf("missing authorized_keys line\nwant: %s\ngot:  %s", expected, result)
	}
}

func TestSmbiosCredForRootSSH(t *testing.T) {
	pubkey := "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQC..."
	result := SmbiosCredForRootSSH(pubkey)

	// Must start with the credential prefix
	prefix := "io.systemd.credential.binary:tmpfiles.extra="
	if !strings.HasPrefix(result, prefix) {
		t.Errorf("wrong prefix, got: %s", result)
	}

	// Decode and verify the tmpfiles.d content round-trips
	b64Part := strings.TrimPrefix(result, prefix)
	decoded, err := base64.StdEncoding.DecodeString(b64Part)
	if err != nil {
		t.Fatalf("invalid base64: %v", err)
	}

	expected := KeyToRootTmpfilesD(pubkey)
	if string(decoded) != expected {
		t.Errorf("round-trip mismatch\nwant: %q\ngot:  %q", expected, string(decoded))
	}
}
