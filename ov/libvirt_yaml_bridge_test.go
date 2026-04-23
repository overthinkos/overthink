package main

// Bridge tests — verify that LibvirtDomain (vms.yml shape) converts
// to libvirtxml.Domain and marshals to XML with the expected
// fragments. Covers:
//   - Top-level domain skeleton (name, memory, vcpu, on_*)
//   - arch's actual libvirt stanza (spice graphics,
//     spicevmc channel, virtio video+rng+memballoon)
//   - Each Rule 5 divergence (video.accel3d scalar, graphics.listen
//     scalar, memballoon.model scalar, rng.backend scalar).
//   - xml_passthrough fragment merge.

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRenderDomainXML_Skeleton(t *testing.T) {
	spec := &VmSpec{
		Firmware: "bios",
		Machine:  "q35",
	}
	rt := VmRuntimeParams{
		Name:     "ov-smoke",
		RamMB:    2048,
		Cpus:     2,
		HostArch: "x86_64",
	}
	out, err := RenderDomainXML(spec, rt)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	want := []string{
		`<domain type="kvm">`,
		`<name>ov-smoke</name>`,
		`<memory unit="MiB">2048</memory>`,
		`<currentMemory unit="MiB">2048</currentMemory>`,
		`<vcpu placement="static">2</vcpu>`,
		`<os>`,
		`<type arch="x86_64" machine="q35">hvm</type>`,
		`<boot dev="hd">`,
		`<features>`,
		`<acpi></acpi>`, // default-on
		`<apic></apic>`, // default-on
		`<clock offset="utc">`,
		`<on_poweroff>destroy</on_poweroff>`,
		`<on_reboot>restart</on_reboot>`,
		`<on_crash>destroy</on_crash>`,
	}
	for _, frag := range want {
		if !strings.Contains(out, frag) {
			t.Errorf("missing fragment: %s\n--- output ---\n%s", frag, out)
		}
	}
}

func TestRenderDomainXML_ArchCloudBase(t *testing.T) {
	// Mirror the vms.yml arch `libvirt:` stanza (post hard
	// cutover to socket-only SPICE — see libvirt_yaml_listen.go).
	yamlStr := `
devices:
  channels:
    - type: spicevmc
      name: com.redhat.spice.0
  graphics:
    - type: spice
      listen:
        - type: socket
  video:
    - model: virtio
      vram: 65536
      heads: 1
      accel3d: false
  rng:
    - model: virtio
      backend: /dev/urandom
  memballoon:
    model: virtio
`
	var lv LibvirtDomain
	if err := yaml.Unmarshal([]byte(yamlStr), &lv); err != nil {
		t.Fatalf("yaml unmarshal: %v", err)
	}
	spec := &VmSpec{
		Firmware: "bios",
		Libvirt:  &lv,
	}
	rt := VmRuntimeParams{
		Name:     "ov-arch",
		RamMB:    8192,
		Cpus:     4,
		HostArch: "x86_64",
	}
	out, err := RenderDomainXML(spec, rt)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	want := []string{
		// Graphics: socket-only listener. virt-manager and
		// `remote-viewer --connect qemu+ssh://…` auto-forward UNIX
		// sockets; no TCP exposure.
		`<graphics type="spice">`,
		`<listen type="socket">`,
		// Channel with spicevmc source + target virtio named.
		`<channel type="spicevmc">`,
		`<target type="virtio" name="com.redhat.spice.0">`,
		// Video: divergence #1 (accel3d scalar → nested <acceleration accel3d=...>)
		`<video>`,
		`<model type="virtio"`,
		`vram="65536"`,
		`heads="1"`,
		`<acceleration accel3d="no">`,
		// RNG: divergence #7 (backend scalar → nested <backend model="random">)
		`<rng model="virtio">`,
		`<backend model="random">/dev/urandom</backend>`,
		// MemBalloon: divergence #6 (model scalar → <memballoon model="...">)
		`<memballoon model="virtio">`,
	}
	for _, frag := range want {
		if !strings.Contains(out, frag) {
			t.Errorf("missing fragment: %s\n--- output ---\n%s", frag, out)
		}
	}
	// Explicit negative: no TCP listener for SPICE.
	if strings.Contains(out, `<listen type="address"`) {
		t.Errorf("expected no TCP <listen> for SPICE, got one in:\n%s", out)
	}
}

// TestRenderDomainXML_GraphicsListen_AllForms covers the three
// accepted YAML shapes for the `listen:` field on a <graphics>
// element: scalar, single mapping, and sequence.
func TestRenderDomainXML_GraphicsListen_AllForms(t *testing.T) {
	cases := []struct {
		name    string
		yamlStr string
		want    []string
		notWant []string
	}{
		{
			name: "scalar address",
			yamlStr: `
devices:
  graphics:
    - type: vnc
      listen: 127.0.0.1
`,
			want: []string{
				`<graphics type="vnc"`,
				`<listen type="address" address="127.0.0.1">`,
			},
		},
		{
			name: "socket-only mapping",
			yamlStr: `
devices:
  graphics:
    - type: spice
      listen:
        type: socket
`,
			want: []string{
				`<graphics type="spice">`,
				`<listen type="socket">`,
			},
			notWant: []string{
				`<listen type="address"`,
			},
		},
		{
			name: "list of socket + address",
			yamlStr: `
devices:
  graphics:
    - type: spice
      listen:
        - type: socket
        - type: address
          address: 127.0.0.1
`,
			want: []string{
				`<graphics type="spice">`,
				`<listen type="socket">`,
				`<listen type="address" address="127.0.0.1">`,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var lv LibvirtDomain
			if err := yaml.Unmarshal([]byte(tc.yamlStr), &lv); err != nil {
				t.Fatalf("yaml unmarshal: %v", err)
			}
			out, err := RenderDomainXML(&VmSpec{Libvirt: &lv},
				VmRuntimeParams{Name: "ov-listen-test", RamMB: 512, Cpus: 1, HostArch: "x86_64"})
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			for _, frag := range tc.want {
				if !strings.Contains(out, frag) {
					t.Errorf("missing fragment %q in:\n%s", frag, out)
				}
			}
			for _, frag := range tc.notWant {
				if strings.Contains(out, frag) {
					t.Errorf("unexpected fragment %q present in:\n%s", frag, out)
				}
			}
		})
	}
}

func TestRenderDomainXML_XMLPassthrough(t *testing.T) {
	// Fragment with both a singleton (launchSecurity) and a device
	// (vsock).
	lv := &LibvirtDomain{
		XMLPassthrough: `
<launchSecurity type="sev-snp">
  <policy>0x30000</policy>
</launchSecurity>
<devices>
  <vsock model="virtio">
    <cid auto="yes"></cid>
  </vsock>
</devices>
`,
	}
	spec := &VmSpec{Libvirt: lv}
	rt := VmRuntimeParams{Name: "ov-passthrough", RamMB: 512, Cpus: 1, HostArch: "x86_64"}
	out, err := RenderDomainXML(spec, rt)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	want := []string{
		`<launchSecurity type="sev-snp">`,
		`<vsock model="virtio">`,
	}
	for _, frag := range want {
		if !strings.Contains(out, frag) {
			t.Errorf("missing passthrough fragment: %s\n--- output ---\n%s", frag, out)
		}
	}
}

func TestRenderDomainXML_AutoSynthesizedDisk(t *testing.T) {
	spec := &VmSpec{}
	rt := VmRuntimeParams{
		Name:        "ov-disk",
		RamMB:       1024,
		Cpus:        1,
		HostArch:    "x86_64",
		QCOW2Path:   "/tmp/ov-disk.qcow2",
		SeedISOPath: "/tmp/ov-disk.iso",
		SshPort:     2224,
	}
	out, err := RenderDomainXML(spec, rt)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// Root disk: vda, virtio, file=/tmp/ov-disk.qcow2.
	for _, frag := range []string{
		`<disk type="file" device="disk">`,
		`<driver name="qemu" type="qcow2">`,
		`<source file="/tmp/ov-disk.qcow2">`,
		`<target dev="vda" bus="virtio">`,
		// Seed ISO.
		`<disk type="file" device="cdrom">`,
		`<source file="/tmp/ov-disk.iso">`,
		`<target dev="sda" bus="sata">`,
		// Default user-mode interface + SSH forward via passt.
		`<interface type="user">`,
		`<backend type="passt">`,
		`<portForward proto="tcp">`,
		`<range start="2224" to="22">`,
		// Auto-synthesized serial + console (emitted with type="pty").
		`<serial type="pty">`,
		`<target port="0">`,
		`<console type="pty">`,
		`<target type="serial" port="0">`,
	} {
		if !strings.Contains(out, frag) {
			t.Errorf("missing fragment: %s\n--- output ---\n%s", frag, out)
		}
	}
}
