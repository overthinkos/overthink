package main

import (
	"encoding/base64"
	"fmt"
)

// KeyToRootTmpfilesD converts an SSH public key to a systemd tmpfiles.d config
// that creates /root/.ssh/authorized_keys. Matches bcvk's key_to_root_tmpfiles_d.
func KeyToRootTmpfilesD(pubkey string) string {
	b64Key := base64.StdEncoding.EncodeToString([]byte(pubkey))
	return fmt.Sprintf("d /root/.ssh 0750 - - -\nf+~ /root/.ssh/authorized_keys 700 - - - %s\n", b64Key)
}

// SmbiosCredForRootSSH generates the SMBIOS type 11 credential string for SSH access.
// The credential uses systemd's tmpfiles.extra to create authorized_keys on boot.
// Returns: "io.systemd.credential.binary:tmpfiles.extra=<base64>"
func SmbiosCredForRootSSH(pubkey string) string {
	tmpfiles := KeyToRootTmpfilesD(pubkey)
	encoded := base64.StdEncoding.EncodeToString([]byte(tmpfiles))
	return fmt.Sprintf("io.systemd.credential.binary:tmpfiles.extra=%s", encoded)
}
