package main

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// CloudInitRuntimeParams carries the runtime-resolved state needed to
// render cloud-init user-data: the SSH public key to inject, the
// instance-id (stable UUIDv4 persisted in VmDeployState), the hostname,
// and whether cloud-init should inject the SSH key at all (computed
// from D13 auto-defaults + explicit VmKeyInjection overrides).
type CloudInitRuntimeParams struct {
	// SSHPublicKey is the OpenSSH authorized_keys-format public key
	// line (e.g. "ssh-ed25519 AAAA..."). Empty when key injection is
	// disabled or when VmSSH.KeySource == "none".
	SSHPublicKey string

	// InstanceID is the stable UUIDv4 cloud-init instance-id.
	// Pinned at first VM create and persisted in VmDeployState.
	InstanceID string

	// Hostname for the guest. Defaults to the VM name when empty.
	Hostname string

	// InjectKeyViaCloudInit is the resolved D13 key_injection.cloud_init
	// channel state. When false the renderer emits no
	// ssh_authorized_keys entries even if SSHPublicKey is populated.
	InjectKeyViaCloudInit bool

	// OvBinaryURL is the download URL when VmOvInstall.Strategy == "url".
	// Empty for auto/scp/skip strategies.
	OvBinaryURL string

	// OvBinaryChecksum is the expected "sha256:<hex>" of the downloaded
	// ov binary. Empty → no checksum verification runcmd is emitted.
	OvBinaryChecksum string
}

// RenderCloudInit produces the three NoCloud seed ISO payloads from a
// VmSpec plus runtime parameters. Pure function — no filesystem or
// network calls.
//
// - userData   → written to cidata/user-data (prefixed with #cloud-config)
// - metaData   → written to cidata/meta-data (instance-id + hostname)
// - networkCfg → written to cidata/network-config (optional; empty if unset)
//
// Defaults applied automatically (D15):
//  1. VmSSH.User added to users: with sudo + ssh_authorized_keys
//     (if the key-injection channel is enabled AND SSHPublicKey != "")
//  2. Minimum packages: {openssh, curl, tar} unioned with user's Packages
//  3. Minimum runcmd: {systemctl enable --now sshd} prepended
//  4. ov_install.strategy: url → curl runcmd; auto/scp/skip → nothing
//  5. VmCloudInit.Extra: raw cloud-config YAML appended as a second
//     document (separated by ---) if non-empty
func RenderCloudInit(spec *VmSpec, rt CloudInitRuntimeParams) (userData, metaData, networkConfig string, err error) {
	ci := spec.CloudInit
	if ci == nil {
		ci = &VmCloudInit{}
	}

	// --- meta-data ---

	hostname := rt.Hostname
	if ci.Hostname != "" {
		hostname = ci.Hostname
	}
	metaMap := map[string]interface{}{}
	if rt.InstanceID != "" {
		metaMap["instance-id"] = rt.InstanceID
	}
	if hostname != "" {
		metaMap["local-hostname"] = hostname
	}
	metaBytes, err := yaml.Marshal(metaMap)
	if err != nil {
		return "", "", "", fmt.Errorf("render meta-data: %w", err)
	}
	metaData = string(metaBytes)

	// --- network-config ---

	if ci.Network != nil && len(ci.Network.Ethernets) > 0 {
		netMap := map[string]interface{}{
			"version": 2,
		}
		if ci.Network.Version > 0 {
			netMap["version"] = ci.Network.Version
		}
		netMap["ethernets"] = ci.Network.Ethernets
		netBytes, err := yaml.Marshal(netMap)
		if err != nil {
			return "", "", "", fmt.Errorf("render network-config: %w", err)
		}
		networkConfig = string(netBytes)
	}

	// --- user-data ---

	userMap := map[string]interface{}{}

	if hostname != "" {
		userMap["hostname"] = hostname
	}
	if ci.Timezone != "" {
		userMap["timezone"] = ci.Timezone
	}
	if ci.Locale != "" {
		userMap["locale"] = ci.Locale
	}

	userMap["users"] = composeUsers(spec, ci, rt)

	packages := composePackages(ci.Packages)
	if len(packages) > 0 {
		userMap["packages"] = packages
	}

	if len(ci.BootCmd) > 0 {
		userMap["bootcmd"] = ci.BootCmd
	}

	runcmd := composeRunCmd(spec, ci, rt)
	if len(runcmd) > 0 {
		userMap["runcmd"] = runcmd
	}

	if len(ci.WriteFiles) > 0 {
		userMap["write_files"] = composeWriteFiles(ci.WriteFiles)
	}

	if ci.Mirrors != nil && len(ci.Mirrors.APT) > 0 {
		// cloud-init apt config (Ubuntu/Debian guests).
		userMap["apt"] = map[string]interface{}{
			"preserve_sources_list": false,
			"primary": []map[string]interface{}{
				{"arches": []string{"default"}, "uri": ci.Mirrors.APT[0]},
			},
		}
	}

	userBytes, err := yaml.Marshal(userMap)
	if err != nil {
		return "", "", "", fmt.Errorf("render user-data: %w", err)
	}

	var b strings.Builder
	b.WriteString("#cloud-config\n")
	b.Write(userBytes)

	if ci.Extra != "" {
		b.WriteString("---\n")
		extra := strings.TrimPrefix(ci.Extra, "#cloud-config\n")
		b.WriteString(extra)
		if !strings.HasSuffix(extra, "\n") {
			b.WriteString("\n")
		}
	}

	userData = b.String()
	return userData, metaData, networkConfig, nil
}

// composeUsers builds the cloud-init users: list. Always includes
// VmSSH.User with sudo + key (when key injection enabled). User-declared
// entries are appended, and if a user-declared entry's Name matches
// VmSSH.User, the renderer *merges* (giving the key to the user entry)
// instead of emitting two entries.
func composeUsers(spec *VmSpec, ci *VmCloudInit, rt CloudInitRuntimeParams) []map[string]interface{} {
	sshUser := resolveCloudInitSSHUser(spec)

	var out []map[string]interface{}
	mergedSSH := false

	for _, u := range ci.Users {
		entry := userEntryToMap(u)
		if u.Name == sshUser {
			applySSHDefaults(entry, rt)
			mergedSSH = true
		}
		out = append(out, entry)
	}

	if !mergedSSH && sshUser != "" {
		entry := map[string]interface{}{
			"name":   sshUser,
			"sudo":   "ALL=(ALL) NOPASSWD:ALL",
			"groups": []string{"wheel"},
			"shell":  "/bin/bash",
		}
		applySSHDefaults(entry, rt)
		out = append([]map[string]interface{}{entry}, out...)
	}

	return out
}

func resolveCloudInitSSHUser(spec *VmSpec) string {
	if spec.SSH != nil && spec.SSH.User != "" {
		return spec.SSH.User
	}
	// Source-kind-appropriate default.
	if spec.Source.Kind == "bootc" {
		return "root"
	}
	return "ov"
}

func userEntryToMap(u VmCloudInitUser) map[string]interface{} {
	m := map[string]interface{}{"name": u.Name}
	if u.Sudo {
		m["sudo"] = "ALL=(ALL) NOPASSWD:ALL"
	}
	if len(u.Groups) > 0 {
		m["groups"] = u.Groups
	}
	if u.Shell != "" {
		m["shell"] = u.Shell
	}
	if u.LockPasswd != nil {
		m["lock_passwd"] = *u.LockPasswd
	}
	return m
}

// applySSHDefaults adds ssh_authorized_keys to a user entry when
// key injection via cloud-init is enabled and a pubkey exists.
func applySSHDefaults(entry map[string]interface{}, rt CloudInitRuntimeParams) {
	if rt.InjectKeyViaCloudInit && rt.SSHPublicKey != "" {
		existing, _ := entry["ssh_authorized_keys"].([]string)
		entry["ssh_authorized_keys"] = append(existing, rt.SSHPublicKey)
	}
}

// composePackages unions ov's minimum {openssh, curl, tar} with the
// user's declared packages, preserving the user's order for extras.
func composePackages(userPkgs []string) []string {
	minimum := []string{"openssh", "curl", "tar"}
	seen := map[string]bool{}
	var out []string
	for _, p := range minimum {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, p := range userPkgs {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// composeRunCmd prepends ov's minimum boot tasks, appends the user's
// runcmd, and injects the ov_install URL download runcmd when
// strategy == "url".
func composeRunCmd(spec *VmSpec, ci *VmCloudInit, rt CloudInitRuntimeParams) []interface{} {
	var runcmd []interface{}
	runcmd = append(runcmd, "systemctl enable --now sshd")

	for _, cmd := range ci.RunCmd {
		runcmd = append(runcmd, cmd)
	}

	if ci.OvInstall != nil && ci.OvInstall.Strategy == "url" && rt.OvBinaryURL != "" {
		// Download + chmod ov.
		runcmd = append(runcmd,
			fmt.Sprintf("curl -fL %s -o /usr/local/bin/ov", rt.OvBinaryURL),
			"chmod +x /usr/local/bin/ov",
		)
		if rt.OvBinaryChecksum != "" {
			// Verify sha256 before making executable; fail fast if bad.
			runcmd = append(runcmd,
				fmt.Sprintf("echo '%s  /usr/local/bin/ov' | sha256sum --check --status || (rm -f /usr/local/bin/ov; exit 1)",
					strings.TrimPrefix(rt.OvBinaryChecksum, "sha256:")),
			)
		}
	}

	return runcmd
}

// composeWriteFiles turns VmCloudInitFile entries into the
// map-list shape cloud-init expects.
func composeWriteFiles(files []VmCloudInitFile) []map[string]interface{} {
	var out []map[string]interface{}
	for _, f := range files {
		m := map[string]interface{}{
			"path":    f.Path,
			"content": f.Content,
		}
		if f.Owner != "" {
			m["owner"] = f.Owner
		}
		if f.Perms != "" {
			m["permissions"] = f.Perms
		}
		if f.Encoding != "" {
			m["encoding"] = f.Encoding
		}
		out = append(out, m)
	}
	return out
}

// ResolveKeyInjectionChannels applies the D13 auto-defaults and
// explicit-wins merging to produce the effective (smbios, cloudInit)
// toggle state for a VmSpec. Returns the booleans persisted into
// VmDeployState.KeyInjectionResolved.
func ResolveKeyInjectionChannels(spec *VmSpec) (smbios, cloudInit bool) {
	// Defaults per D13:
	//   cloud_image → {smbios: true, cloud_init: true}  (additive)
	//   bootc       → {smbios: true, cloud_init: false}
	if spec.Source.Kind == "bootc" {
		smbios, cloudInit = true, false
	} else {
		smbios, cloudInit = true, true
	}

	if spec.SSH == nil || spec.SSH.KeyInjection == nil {
		return smbios, cloudInit
	}
	kj := spec.SSH.KeyInjection
	switch kj.SMBIOS {
	case "enabled":
		smbios = true
	case "disabled":
		smbios = false
	}
	switch kj.CloudInit {
	case "enabled":
		cloudInit = true
	case "disabled":
		cloudInit = false
	}
	return smbios, cloudInit
}
