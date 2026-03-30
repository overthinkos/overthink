package main

import "golang.org/x/sys/unix"

const keyringKeyName = "ov-kdbx-password"

// keyringGet retrieves a cached password from the user keyring.
// Returns ("", nil) if the key is not found or expired.
func keyringGet(name string) (string, error) {
	id, err := unix.KeyctlSearch(unix.KEY_SPEC_USER_KEYRING, "user", name, 0)
	if err != nil {
		return "", nil // not found or expired
	}
	buf := make([]byte, 256)
	n, err := unix.KeyctlBuffer(unix.KEYCTL_READ, id, buf, 0)
	if err != nil {
		return "", err
	}
	if n > len(buf) {
		buf = make([]byte, n)
		n, err = unix.KeyctlBuffer(unix.KEYCTL_READ, id, buf, 0)
		if err != nil {
			return "", err
		}
	}
	return string(buf[:n]), nil
}

// keyringSet stores a password in the user keyring with a TTL in seconds.
// The key is accessible to all processes of the same UID.
func keyringSet(name, value string, timeoutSec int) error {
	id, err := unix.AddKey("user", name, []byte(value), unix.KEY_SPEC_USER_KEYRING)
	if err != nil {
		return err
	}
	if timeoutSec > 0 {
		_, err = unix.KeyctlInt(unix.KEYCTL_SET_TIMEOUT, id, timeoutSec, 0, 0)
		return err
	}
	return nil
}
