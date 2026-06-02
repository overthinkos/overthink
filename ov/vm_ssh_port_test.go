package main

import (
	"strings"
	"testing"
)

// TestResolveVmSshPort covers the three resolution paths: the 2222 default, an
// explicit fixed port, and ssh.port_auto auto-allocation (no persisted state →
// a fresh ephemeral host port).
func TestResolveVmSshPort(t *testing.T) {
	// Default: no SSH block → 2222.
	if p, err := resolveVmSshPort(&VmSpec{}, "vm-ssh-port-default-zzz"); err != nil || p != 2222 {
		t.Fatalf("default: got (%d, %v), want (2222, nil)", p, err)
	}
	// Explicit fixed port.
	if p, err := resolveVmSshPort(&VmSpec{SSH: &VmSSH{Port: 2244}}, "vm-ssh-port-fixed-zzz"); err != nil || p != 2244 {
		t.Fatalf("fixed: got (%d, %v), want (2244, nil)", p, err)
	}
	// port_auto with a VM name absent from deploy.yml → allocate a free port.
	// (The ephemeral range is high, so it is never the 2222 default — a default
	// here would mean the port_auto branch silently did nothing.)
	p, err := resolveVmSshPort(&VmSpec{SSH: &VmSSH{PortAuto: true}}, "vm-ssh-port-auto-nonexistent-zzz")
	if err != nil {
		t.Fatalf("port_auto: unexpected error: %v", err)
	}
	if p <= 0 || p > 65535 {
		t.Fatalf("port_auto: allocated port %d out of range 1-65535", p)
	}
	if p == 2222 {
		t.Errorf("port_auto: got the 2222 default instead of an allocated ephemeral port")
	}
}

// TestValidateVmSpec_SshPortAutoMutualExclusion proves ssh.port and
// ssh.port_auto cannot both be set.
func TestValidateVmSpec_SshPortAutoMutualExclusion(t *testing.T) {
	base := func(ssh *VmSSH) *VmSpec {
		return &VmSpec{
			Source: VmSource{Kind: "cloud_image", URL: "https://example/img.qcow2"},
			SSH:    ssh,
		}
	}
	// port + port_auto → rejected.
	bad := &ValidationError{}
	ValidateVmSpec("vm", base(&VmSSH{Port: 2244, PortAuto: true}), bad)
	if !bad.HasErrors() || !strings.Contains(strings.Join(bad.Errors, "\n"), "mutually exclusive") {
		t.Fatalf("expected ssh.port + ssh.port_auto to be rejected, got: %v", bad.Errors)
	}
	// port_auto alone → no mutual-exclusion error.
	ok := &ValidationError{}
	ValidateVmSpec("vm", base(&VmSSH{PortAuto: true}), ok)
	if strings.Contains(strings.Join(ok.Errors, "\n"), "mutually exclusive") {
		t.Errorf("ssh.port_auto alone should be valid, got: %v", ok.Errors)
	}
}
