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

// TestRenderHostdevAndFeaturesXML moved to candy/plugin-vm/vfio_render_test.go alongside the
// RenderDomainXML renderer (the go-libvirt + libvirtxml shed).

// --- A5: hostdev validation ---

// --- A6: RebootStep ---

func TestRebootStepInterface(t *testing.T) {
	s := &RebootStep{CandyName: "nvidia-driver"}
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
	img := &ResolvedBox{Name: "test-img", Distro: []string{"arch"}}

	// reboot:false → no RebootStep.
	noReboot, err := BuildDeployPlan(&Candy{Name: "x", Version: "2026.001.0001"}, img, HostContext{})
	if err != nil {
		t.Fatalf("BuildDeployPlan(no reboot): %v", err)
	}
	for _, s := range noReboot.Steps {
		if _, isReboot := s.(*RebootStep); isReboot {
			t.Fatal("RebootStep emitted for a candy without reboot:true")
		}
	}

	// reboot:true → trailing RebootStep.
	withReboot, err := BuildDeployPlan(&Candy{Name: "nvidia-driver", Version: "2026.001.0001", reboot: true}, img, HostContext{})
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
	if rb.CandyName != "nvidia-driver" {
		t.Errorf("RebootStep.CandyName = %q, want nvidia-driver", rb.CandyName)
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
	err := TransferImageToGuest(context.Background(), fe, "podman", "localhost/cuda:latest", "", false, EmitOpts{})
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
	err := TransferImageToGuest(context.Background(), fe, "podman", "localhost/cuda:latest", "", false, EmitOpts{})
	if err == nil || !strings.Contains(err.Error(), "SSH executor") {
		t.Fatalf("expected the corrupt image to NOT skip and proceed to re-load (hitting the SSH-executor requirement); got err=%v", err)
	}
	if !fe.rmiCalled {
		t.Error("a present-but-corrupt image must be removed before re-load")
	}
}

// --- Render consolidation: VM + local share ONE render path per functionality ---

// The in-proc VM Op-step execution (copy stages via PutFile; a non-copy verb renders via
// the shared renderOpCommand) moved into the out-of-process kit.WalkPlans (kit.walkOp) when
// target:vm externalized — the in-proc VM-target Op execution is gone, so its two unit tests retired
// here. kit owns walkOp's copy-vs-render split (the SAME renderOpCommand, exercised below by
// TestSharedRenderersConsolidated) and the check-arch-vm bed proves it end-to-end in a guest.

// renderTaskCommand is the ONE shared task renderer; copy: is explicitly NOT
// handled here (it must be staged via PutFile in execTask), and pac package
// installs carry options: through (the divergence the consolidation fixed) —
// now via the config-driven host install renderer reading build.yml.
func TestSharedRenderersConsolidated(t *testing.T) {
	if _, err := renderOpCommand(&OpStep{Op: &Op{Copy: "f"}}); err == nil {
		t.Error("renderTaskCommand must reject copy: (staged via PutFile, not rendered)")
	}
	dc, _, _, err := LoadBuildConfigForBox(repoRootDir(t))
	if err != nil {
		t.Fatalf("LoadBuildConfigForBox: %v", err)
	}
	got, err := renderHostPackageCommand(dc, &SystemPackagesStep{
		Format:            "pac",
		Phase:             PhaseInstall,
		Packages:          []string{"libyuv"},
		Options:           []string{"--overwrite", "*"},
		RawInstallContext: map[string]any{"package": []string{"libyuv"}, "options": []string{"--overwrite", "*"}},
	})
	if err != nil || got != "pacman -Sy --noconfirm --needed --overwrite * libyuv" {
		t.Errorf("pac options not applied by shared host renderer: %q (err %v)", got, err)
	}
}
