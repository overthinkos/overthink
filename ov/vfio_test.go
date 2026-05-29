package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- A1: scanVFIO against a synthetic sysfs tree ---

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func TestScanVFIO(t *testing.T) {
	sys := t.TempDir()
	cmdline := filepath.Join(sys, "cmdline")
	writeFile(t, cmdline, "BOOT_IMAGE=/vmlinuz root=UUID=x amd_iommu=on iommu=pt rw\n")

	// IOMMU group 13: a GPU (0300) + its audio function (0403).
	gpu := filepath.Join(sys, "bus", "pci", "devices", "0000:01:00.0")
	aud := filepath.Join(sys, "bus", "pci", "devices", "0000:01:00.1")
	writeFile(t, filepath.Join(gpu, "class"), "0x030000\n")
	writeFile(t, filepath.Join(gpu, "vendor"), "0x10de\n")
	writeFile(t, filepath.Join(gpu, "device"), "0x2704\n")
	writeFile(t, filepath.Join(aud, "class"), "0x040300\n")
	writeFile(t, filepath.Join(aud, "vendor"), "0x10de\n")
	writeFile(t, filepath.Join(aud, "device"), "0x22bb\n")

	// driver + iommu_group symlinks (scanVFIO reads basename only).
	symlink(t, "../../../bus/pci/drivers/vfio-pci", filepath.Join(gpu, "driver"))
	symlink(t, "../../../bus/pci/drivers/snd_hda_intel", filepath.Join(aud, "driver"))
	symlink(t, "../../../kernel/iommu_groups/13", filepath.Join(gpu, "iommu_group"))
	symlink(t, "../../../kernel/iommu_groups/13", filepath.Join(aud, "iommu_group"))

	// iommu group membership listing.
	grpDev := filepath.Join(sys, "kernel", "iommu_groups", "13", "devices")
	writeFile(t, filepath.Join(grpDev, "0000:01:00.0"), "")
	writeFile(t, filepath.Join(grpDev, "0000:01:00.1"), "")

	rep := scanVFIO(sys, cmdline)

	if !rep.IOMMUEnabled {
		t.Error("expected IOMMUEnabled=true (iommu_groups populated)")
	}
	if rep.IOMMUKind != "amd" {
		t.Errorf("IOMMUKind = %q, want amd", rep.IOMMUKind)
	}
	if len(rep.GPUs) != 1 {
		t.Fatalf("len(GPUs) = %d, want 1 (only the 0x0300 device)", len(rep.GPUs))
	}
	g := rep.GPUs[0]
	if g.Addr != "0000:01:00.0" || g.VendorID != "0x10de" || g.DeviceID != "0x2704" {
		t.Errorf("GPU id mismatch: %+v", g)
	}
	if g.Driver != "vfio-pci" {
		t.Errorf("GPU driver = %q, want vfio-pci", g.Driver)
	}
	if g.IOMMUGroup != 13 {
		t.Errorf("GPU IOMMUGroup = %d, want 13", g.IOMMUGroup)
	}
	if len(g.GroupMembers) != 2 {
		t.Fatalf("GroupMembers = %d, want 2 (GPU + audio)", len(g.GroupMembers))
	}
	// Members sorted by Addr → GPU first, audio second.
	if g.GroupMembers[0].Addr != "0000:01:00.0" || g.GroupMembers[1].Addr != "0000:01:00.1" {
		t.Errorf("group members not sorted/expected: %+v", g.GroupMembers)
	}
}

func TestScanVFIO_NoIOMMU(t *testing.T) {
	sys := t.TempDir()
	cmdline := filepath.Join(sys, "cmdline")
	writeFile(t, cmdline, "BOOT_IMAGE=/vmlinuz root=UUID=x rw\n") // no iommu flag
	rep := scanVFIO(sys, cmdline)
	if rep.IOMMUEnabled {
		t.Error("expected IOMMUEnabled=false with empty iommu_groups")
	}
	if rep.IOMMUKind != "" {
		t.Errorf("IOMMUKind = %q, want empty", rep.IOMMUKind)
	}
}

// --- A2: PCI-address parsing + hostdevs block rendering ---

func TestParsePCIAddr(t *testing.T) {
	dom, bus, slot, fn, ok := parsePCIAddr("0000:01:00.0")
	if !ok || dom != "0x0000" || bus != "0x01" || slot != "0x00" || fn != "0x0" {
		t.Errorf("parsePCIAddr = %q %q %q %q ok=%v", dom, bus, slot, fn, ok)
	}
	if _, _, _, _, ok := parsePCIAddr("garbage"); ok {
		t.Error("expected parse failure on malformed addr")
	}
}

func TestRenderHostdevsBlock(t *testing.T) {
	members := []VFIOPCIDevice{
		{Addr: "0000:01:00.0"},
		{Addr: "0000:01:00.1"},
	}
	out := renderHostdevsBlock(members)
	for _, want := range []string{
		"hostdevs:", "- type: pci", "managed: \"yes\"",
		"domain: \"0x0000\"", "bus: \"0x01\"", "slot: \"0x00\"", "function: \"0x0\"", "function: \"0x1\"",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("hostdevs block missing %q\n%s", want, out)
		}
	}
}

// --- A4: hostdev ROM/Driver + KVM hidden + HyperV vendor_id render ---

func TestRenderHostdevAndFeaturesXML(t *testing.T) {
	romFile := "off"
	spec := &VmSpec{
		Source:   VmSource{Kind: "cloud_image", URL: "http://x/y.qcow2"},
		Firmware: "uefi-insecure",
		Libvirt: &LibvirtDomain{
			Features: &LibvirtFeatures{
				KVM:    &LibvirtKVM{Hidden: "on"},
				HyperV: &LibvirtHyperV{VendorID: &LibvirtVendorID{State: "on", Value: "ovgpu123456"}},
			},
			Devices: &LibvirtDevices{
				Hostdevs: []LibvirtHostdev{{
					Type:    "pci",
					Managed: "yes",
					Source:  map[string]string{"domain": "0x0000", "bus": "0x01", "slot": "0x00", "function": "0x0"},
					ROM:     map[string]string{"bar": romFile},
					Driver:  map[string]string{"name": "vfio"},
				}},
			},
		},
	}
	rt := VmRuntimeParams{Name: "ov-test", RamMB: 2048, Cpus: 2, HostArch: "x86_64"}
	xmlOut, err := RenderDomainXML(spec, rt)
	if err != nil {
		t.Fatalf("RenderDomainXML: %v", err)
	}
	for _, want := range []string{
		"<hostdev", `managed="yes"`, `type="pci"`,
		`domain="0x0000"`, `bus="0x01"`, `slot="0x00"`, `function="0x0"`,
		`<rom bar="off"`, `<driver name="vfio"`,
		"<kvm>", `<hidden state="on"`, `<vendor_id state="on" value="ovgpu123456"`,
	} {
		if !strings.Contains(xmlOut, want) {
			t.Errorf("domain XML missing %q\n---\n%s", want, xmlOut)
		}
	}
}

// --- A5: hostdev validation ---

func TestValidateLibvirtHostdev(t *testing.T) {
	good := &ValidationError{}
	validateLibvirtHostdev("vm", 0, LibvirtHostdev{
		Type: "pci", Managed: "yes",
		Source: map[string]string{"domain": "0x0000", "bus": "0x01", "slot": "0x00", "function": "0x0"},
	}, good)
	if good.HasErrors() {
		t.Errorf("valid hostdev flagged: %v", good.Errors)
	}

	bad := &ValidationError{}
	validateLibvirtHostdev("vm", 0, LibvirtHostdev{
		Type: "pci", Managed: "maybe",
		Source: map[string]string{"domain": "0x0000", "bus": "zz"}, // missing slot/function, bad bus
	}, bad)
	if !bad.HasErrors() {
		t.Fatal("expected errors for bad managed + missing/invalid pci source")
	}
	joined := strings.Join(bad.Errors, "\n")
	for _, want := range []string{"managed", "source.slot", "source.function", "source.bus"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing expected error mentioning %q:\n%s", want, joined)
		}
	}
}

// --- A6: RebootStep ---

func TestRebootStepInterface(t *testing.T) {
	s := &RebootStep{LayerName: "nvidia-driver"}
	if s.Kind() != StepKindReboot {
		t.Errorf("Kind = %q", s.Kind())
	}
	if s.Scope() != ScopeSystem {
		t.Errorf("Scope = %v", s.Scope())
	}
	if s.Venue() != VenueHostNative {
		t.Errorf("Venue = %v", s.Venue())
	}
	if s.RequiresGate() != GateNone {
		t.Errorf("RequiresGate = %v", s.RequiresGate())
	}
	if len(s.Reverse()) != 0 {
		t.Errorf("Reverse should be empty, got %v", s.Reverse())
	}
}

func TestBuildDeployPlanEmitsReboot(t *testing.T) {
	img := &ResolvedImage{Name: "test-img", Distro: []string{"arch"}}

	// reboot:false → no RebootStep.
	noReboot, err := BuildDeployPlan(&Layer{Name: "x", Version: "2026.1.1"}, img, HostContext{})
	if err != nil {
		t.Fatalf("BuildDeployPlan(no reboot): %v", err)
	}
	for _, s := range noReboot.Steps {
		if _, isReboot := s.(*RebootStep); isReboot {
			t.Fatal("RebootStep emitted for a layer without reboot:true")
		}
	}

	// reboot:true → trailing RebootStep.
	withReboot, err := BuildDeployPlan(&Layer{Name: "nvidia-driver", Version: "2026.1.1", reboot: true}, img, HostContext{})
	if err != nil {
		t.Fatalf("BuildDeployPlan(reboot): %v", err)
	}
	if len(withReboot.Steps) == 0 {
		t.Fatal("no steps emitted")
	}
	last := withReboot.Steps[len(withReboot.Steps)-1]
	rb, isReboot := last.(*RebootStep)
	if !isReboot {
		t.Fatalf("last step = %T, want *RebootStep", last)
	}
	if rb.LayerName != "nvidia-driver" {
		t.Errorf("RebootStep.LayerName = %q, want nvidia-driver", rb.LayerName)
	}
}

// --- A7: host→guest image transfer idempotency ---

// fakeGuestExec implements DeployExecutor. RunCapture reports the image as
// already present; `corrupt` makes the integrity probe report a torn overlay so
// the verified transfer must NOT skip (it rmi's + re-loads instead).
type fakeGuestExec struct {
	putCalled bool
	runCalled bool
	corrupt   bool
	rmiCalled bool
}

func (f *fakeGuestExec) Venue() string { return "ssh://fake" }
func (f *fakeGuestExec) RunSystem(ctx context.Context, script string, opts EmitOpts) error {
	f.runCalled = true
	return nil
}
func (f *fakeGuestExec) RunUser(ctx context.Context, script string, opts EmitOpts) error { return nil }
func (f *fakeGuestExec) RunBuilder(ctx context.Context, opts BuilderRunOpts) ([]byte, error) {
	return nil, nil
}
func (f *fakeGuestExec) PutFile(ctx context.Context, l, r string, m uint32, root bool, o EmitOpts) error {
	f.putCalled = true
	return nil
}
func (f *fakeGuestExec) GetFile(ctx context.Context, p string, root bool, o EmitOpts) ([]byte, error) {
	return nil, nil
}
func (f *fakeGuestExec) RunCapture(ctx context.Context, script string) (string, string, int, error) {
	switch {
	case strings.Contains(script, "image exists"):
		return "", "", 0, nil // present by name
	case strings.Contains(script, "rmi"):
		f.rmiCalled = true
		return "", "", 0, nil
	case strings.Contains(script, "podman run") && strings.Contains(script, "/usr/bin/true"):
		// Integrity probe: torn overlay when corrupt, else clean.
		if f.corrupt {
			return "", "Error: faccessat /var/lib/containers/storage/overlay/abc: no such file or directory", 125,
				fmt.Errorf("exit status 125")
		}
		return "", "", 0, nil
	}
	return "", "", 0, nil
}
func (f *fakeGuestExec) Kind() string { return "vm" }
func (f *fakeGuestExec) ResolveHome(ctx context.Context, user string) (string, error) {
	return "/root", nil
}

func TestTransferImageToGuestIdempotent(t *testing.T) {
	fe := &fakeGuestExec{}
	err := TransferImageToGuest(context.Background(), fe, "podman", "localhost/cuda:latest", "", EmitOpts{})
	if err != nil {
		t.Fatalf("TransferImageToGuest: %v", err)
	}
	if fe.putCalled || fe.runCalled {
		t.Error("transfer should be skipped when guest already has the image, verified intact")
	}
}

// A present-but-torn image must NOT be skipped: the verified transfer rmi's it
// and proceeds to re-load (here it then hits the *SSHExecutor requirement,
// proving it did not short-circuit on the name-exists check).
func TestTransferImageToGuestReloadsCorrupt(t *testing.T) {
	fe := &fakeGuestExec{corrupt: true}
	err := TransferImageToGuest(context.Background(), fe, "podman", "localhost/cuda:latest", "", EmitOpts{})
	if err == nil || !strings.Contains(err.Error(), "SSH executor") {
		t.Fatalf("expected the corrupt image to NOT skip and proceed to re-load (hitting the SSH-executor requirement); got err=%v", err)
	}
	if !fe.rmiCalled {
		t.Error("a present-but-corrupt image must be removed before re-load")
	}
}

// --- ov-cachyos-gpu VM: autostart + virtiofs filesystem validation ---

func TestValidateVmSpec_AutostartRequiresLibvirt(t *testing.T) {
	base := func(backend string) *VmSpec {
		return &VmSpec{
			Source:    VmSource{Kind: "cloud_image", URL: "https://example/img.qcow2"},
			Backend:   backend,
			Autostart: true,
		}
	}
	bad := &ValidationError{}
	ValidateVmSpec("gpu", base("qemu"), bad)
	if !bad.HasErrors() || !strings.Contains(strings.Join(bad.Errors, "\n"), "autostart") {
		t.Fatalf("expected autostart+qemu to be rejected, got: %v", bad.Errors)
	}
	for _, backend := range []string{"libvirt", ""} {
		ok := &ValidationError{}
		ValidateVmSpec("gpu", base(backend), ok)
		if joined := strings.Join(ok.Errors, "\n"); strings.Contains(joined, "autostart") {
			t.Errorf("autostart with backend %q should be allowed, got autostart error: %v", backend, ok.Errors)
		}
	}
}

func TestValidateLibvirtFilesystem(t *testing.T) {
	good := &ValidationError{}
	validateLibvirtFilesystem("vm", 0, LibvirtFilesystem{
		Driver: "virtiofs", AccessMode: "passthrough", Source: "/home/atrawog", Target: "workspace",
	}, good)
	if good.HasErrors() {
		t.Errorf("valid virtiofs filesystem flagged: %v", good.Errors)
	}

	bad := &ValidationError{}
	validateLibvirtFilesystem("vm", 0, LibvirtFilesystem{
		Driver: "nfs", AccessMode: "weird", // bad driver + accessmode, missing source+target
	}, bad)
	if !bad.HasErrors() {
		t.Fatal("expected errors for missing source/target + bad driver/accessmode")
	}
	joined := strings.Join(bad.Errors, "\n")
	for _, want := range []string{"source", "target", "driver", "accessmode"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing expected error mentioning %q:\n%s", want, joined)
		}
	}
}
