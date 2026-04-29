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

// KeyToUserTmpfilesD generates a systemd tmpfiles.d config that drops a
// per-VM SSH key into the named user's ~/.ssh/authorized_keys. The user
// account itself must already exist in the rootfs (created at build time
// by the bootloader install template OR by cloud-init); this function
// only delivers the *key*, never bakes one into the image.
//
// The home path defaults to /home/<user> when empty.
func KeyToUserTmpfilesD(user, home, pubkey string) string {
	if home == "" {
		home = fmt.Sprintf("/home/%s", user)
	}
	b64Key := base64.StdEncoding.EncodeToString([]byte(pubkey))
	// Use `~` mode/owner placeholders so systemd-tmpfiles inherits the
	// existing user/group on the home directory (the user must exist).
	return fmt.Sprintf(
		"d %s/.ssh 0700 %s %s -\nf+~ %s/.ssh/authorized_keys 0600 %s %s - %s\n",
		home, user, user, home, user, user, b64Key,
	)
}

// SmbiosCredForSSH generates the SMBIOS type 11 credential string that
// delivers a per-VM SSH key to the named user via systemd-tmpfiles. When
// user == "" or user == "root", the legacy /root/.ssh path is used.
// Returns: "io.systemd.credential.binary:tmpfiles.extra=<base64>"
func SmbiosCredForSSH(user, home, pubkey string) string {
	var tmpfiles string
	if user == "" || user == "root" {
		tmpfiles = KeyToRootTmpfilesD(pubkey)
	} else {
		tmpfiles = KeyToUserTmpfilesD(user, home, pubkey)
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(tmpfiles))
	return fmt.Sprintf("io.systemd.credential.binary:tmpfiles.extra=%s", encoded)
}

// SmbiosCredForRootSSH is preserved as a compatibility wrapper for the
// existing call sites in vm.go (legacy bootc paths).
func SmbiosCredForRootSSH(pubkey string) string {
	return SmbiosCredForSSH("root", "", pubkey)
}
