package main

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// twoFnGPU is the canonical multifunction passthrough GPU under test: a VGA
// display function + its sibling HDMI-audio function in one IOMMU group (matches
// this host's RTX 4080 SUPER, group 13 = 01:00.0 + 01:00.1). DetectVFIO always
// populates GroupMembers (the GPU itself at minimum), so the group-aware switch
// never sees an empty member list in production.
func twoFnGPU() VFIOGpu {
	return VFIOGpu{
		VFIOPCIDevice: VFIOPCIDevice{Addr: "0000:01:00.0", VendorID: "0x10de", DeviceID: "0x2702", Class: "0x0300", Driver: "vfio-pci", IOMMUGroup: 13},
		GroupMembers: []VFIOPCIDevice{
			{Addr: "0000:01:00.0", Class: "0x0300", ClassLabel: "VGA controller", Driver: "vfio-pci"},
			{Addr: "0000:01:00.1", Class: "0x0403", ClassLabel: "Audio device", Driver: "vfio-pci"},
		},
	}
}

func TestGpuModeFromDriver(t *testing.T) {
	cases := map[string]string{
		"nvidia":   gpuModeNvidia,
		"vfio-pci": gpuModeVfio,
		"":         gpuModeVfio, // unbound is the vfio/default side
		"nouveau":  gpuModeVfio,
	}
	for driver, want := range cases {
		if got := gpuModeFromDriver(driver); got != want {
			t.Errorf("gpuModeFromDriver(%q) = %q, want %q", driver, got, want)
		}
	}
}

func TestCurrentGPUMode(t *testing.T) {
	orig := gpuDisplayDriver
	defer func() { gpuDisplayDriver = orig }()
	gpuDisplayDriver = func(addr string) string { return "nvidia" }
	gpu := VFIOGpu{VFIOPCIDevice: VFIOPCIDevice{Addr: "0000:01:00.0"}}
	if got := currentGPUMode(gpu); got != gpuModeNvidia {
		t.Errorf("currentGPUMode = %q, want nvidia", got)
	}
}

func TestHostDriverForFunction(t *testing.T) {
	cases := []struct{ class, mode, want string }{
		{"0x0300", gpuModeNvidia, hostDriverDisplay}, // VGA -> nvidia
		{"0x0302", gpuModeNvidia, hostDriverDisplay}, // 3D controller -> nvidia
		{"0x0403", gpuModeNvidia, hostDriverAudio},   // HDMI audio -> snd_hda_intel
		{"0x0c03", gpuModeNvidia, hostDriverVfio},    // unknown sibling -> vfio (safe)
		{"0x0300", gpuModeVfio, hostDriverVfio},      // passthrough: everything vfio
		{"0x0403", gpuModeVfio, hostDriverVfio},      // passthrough: audio vfio too (group viability)
	}
	for _, c := range cases {
		if got := hostDriverForFunction(c.class, c.mode); got != c.want {
			t.Errorf("hostDriverForFunction(%q,%q)=%q want %q", c.class, c.mode, got, c.want)
		}
	}
}

func TestSwitchScripts_GroupAware(t *testing.T) {
	gpu := twoFnGPU()

	nv := switchScriptToNvidia(gpu)
	for _, addr := range []string{"0000:01:00.0", "0000:01:00.1"} {
		if !strings.Contains(nv, addr) {
			t.Errorf("nvidia switch script must cover group member %s", addr)
		}
	}
	if !strings.Contains(nv, "nvidia") || !strings.Contains(nv, "snd_hda_intel") {
		t.Error("nvidia switch must bind display->nvidia AND audio->snd_hda_intel")
	}
	if !strings.Contains(nv, "nvidia-modprobe") {
		t.Error("nvidia switch must create /dev/nvidia* nodes")
	}

	vf := switchScriptToVfio(gpu)
	for _, addr := range []string{"0000:01:00.0", "0000:01:00.1"} {
		if !strings.Contains(vf, addr) {
			t.Errorf("vfio switch script must cover group member %s", addr)
		}
	}
	// THE root-cause guarantee: detach nvidia via the refcount-guarded module
	// unload (EBUSY fast-fail), NEVER a sysfs-unbind of nvidia (the device_lock wedge).
	if !strings.Contains(vf, "modprobe -r nvidia") {
		t.Error("vfio switch MUST detach nvidia via `modprobe -r` (the safe EBUSY gate)")
	}
	if strings.Contains(vf, "drivers/nvidia/unbind") {
		t.Error("vfio switch MUST NEVER sysfs-unbind nvidia — that is the reboot-only device_lock wedge")
	}
	if !strings.Contains(vf, "exit 3") {
		t.Error("vfio switch must refuse (exit 3) when nvidia is still in use rather than force-unbind")
	}
	if !strings.Contains(vf, "nv_pci_remove") {
		t.Error("vfio switch must self-detect a wedge (nv_pci_remove D-state) and exit 4")
	}
}

func TestGroupInMode_GroupAware(t *testing.T) {
	gpu := twoFnGPU()
	orig := gpuDisplayDriver
	defer func() { gpuDisplayDriver = orig }()

	// display on nvidia but audio still vfio => NOT fully in nvidia mode.
	gpuDisplayDriver = func(addr string) string {
		if addr == "0000:01:00.0" {
			return "nvidia"
		}
		return "vfio-pci"
	}
	if groupInMode(gpu, gpuModeNvidia) {
		t.Error("half-switched group (audio still vfio) must NOT read as in nvidia mode")
	}
	// both on their nvidia-mode drivers => in nvidia mode.
	gpuDisplayDriver = func(addr string) string {
		if addr == "0000:01:00.0" {
			return "nvidia"
		}
		return "snd_hda_intel"
	}
	if !groupInMode(gpu, gpuModeNvidia) {
		t.Error("display=nvidia + audio=snd_hda_intel must read as in nvidia mode")
	}
	// both vfio => in vfio mode.
	gpuDisplayDriver = func(string) string { return "vfio-pci" }
	if !groupInMode(gpu, gpuModeVfio) {
		t.Error("both functions vfio-pci must read as in vfio mode")
	}
}

func TestSwitchGPUDriverMode_IdempotentNoOp(t *testing.T) {
	origDrv, origRun := gpuDisplayDriver, runGPUSwitchScript
	defer func() { gpuDisplayDriver, runGPUSwitchScript = origDrv, origRun }()

	gpuDisplayDriver = func(string) string { return "vfio-pci" } // whole group already vfio
	called := false
	runGPUSwitchScript = func(string) ([]byte, error) { called = true; return nil, nil }

	if err := switchGPUDriverMode(twoFnGPU(), gpuModeVfio); err != nil {
		t.Fatalf("switch: %v", err)
	}
	if called {
		t.Fatal("idempotent no-op must NOT invoke the sudo sysfs script when already in mode")
	}
}

func TestSwitchGPUDriverMode_ToNvidiaScript(t *testing.T) {
	origDrv, origRun := gpuDisplayDriver, runGPUSwitchScript
	defer func() { gpuDisplayDriver, runGPUSwitchScript = origDrv, origRun }()

	gpuDisplayDriver = func(string) string { return "vfio-pci" } // flip needed
	var gotScript string
	runGPUSwitchScript = func(script string) ([]byte, error) { gotScript = script; return nil, nil }

	if err := switchGPUDriverMode(twoFnGPU(), gpuModeNvidia); err != nil {
		t.Fatalf("switch: %v", err)
	}
	for _, want := range []string{"driver_override", "nvidia", "snd_hda_intel", "nvidia-modprobe", "0000:01:00.0", "0000:01:00.1"} {
		if !strings.Contains(gotScript, want) {
			t.Errorf("to-nvidia script missing %q:\n%s", want, gotScript)
		}
	}
}

func TestSwitchGPUDriverMode_ToVfioScript(t *testing.T) {
	origDrv, origRun := gpuDisplayDriver, runGPUSwitchScript
	defer func() { gpuDisplayDriver, runGPUSwitchScript = origDrv, origRun }()

	gpuDisplayDriver = func(string) string { return "nvidia" } // flip needed
	var gotScript string
	runGPUSwitchScript = func(script string) ([]byte, error) { gotScript = script; return nil, nil }

	if err := switchGPUDriverMode(twoFnGPU(), gpuModeVfio); err != nil {
		t.Fatalf("switch: %v", err)
	}
	// The RDD-proven recipe: SAFE module-unload detach + driver_override bind.
	for _, want := range []string{"modprobe -r nvidia", "modprobe vfio-pci", "driver_override", "drivers_probe"} {
		if !strings.Contains(gotScript, want) {
			t.Errorf("to-vfio script missing %q:\n%s", want, gotScript)
		}
	}
}

func TestSwitchGPUDriverMode_WedgeDetection(t *testing.T) {
	gpu := twoFnGPU()
	origD, origR := gpuDisplayDriver, runGPUSwitchScript
	defer func() { gpuDisplayDriver, runGPUSwitchScript = origD, origR }()
	gpuDisplayDriver = func(string) string { return "nvidia" } // not in vfio mode => will switch

	// (a) deadline timeout surfaced as errGPUSwitchWedged by runGPUSwitchScript.
	runGPUSwitchScript = func(string) ([]byte, error) { return []byte("timed out"), errGPUSwitchWedged }
	if err := switchGPUDriverMode(gpu, gpuModeVfio); !errors.Is(err, errGPUSwitchWedged) {
		t.Errorf("deadline wedge must surface errGPUSwitchWedged, got %v", err)
	}
	// (b) script self-detected wedge (WEDGED in output, generic exit error).
	runGPUSwitchScript = func(string) ([]byte, error) {
		return []byte("switch-to-vfio WEDGED: 0000:01:00.0 ..."), errors.New("exit status 4")
	}
	if err := switchGPUDriverMode(gpu, gpuModeVfio); !errors.Is(err, errGPUSwitchWedged) {
		t.Errorf("self-detected WEDGED output must surface errGPUSwitchWedged, got %v", err)
	}
	// (c) an ordinary failure is NOT a wedge.
	runGPUSwitchScript = func(string) ([]byte, error) {
		return []byte("switch-to-vfio FAILED: 0000:01:00.0 driver=unbound"), errors.New("exit status 1")
	}
	if err := switchGPUDriverMode(gpu, gpuModeVfio); err == nil || errors.Is(err, errGPUSwitchWedged) {
		t.Errorf("ordinary failure must error but NOT be a wedge, got %v", err)
	}
}

func TestSwitchGPUDriverMode_UnknownMode(t *testing.T) {
	origDrv := gpuDisplayDriver
	defer func() { gpuDisplayDriver = origDrv }()
	gpuDisplayDriver = func(string) string { return "vfio-pci" }
	if err := switchGPUDriverMode(twoFnGPU(), "bogus"); err == nil {
		t.Fatal("expected error for unknown mode")
	}
}

func TestGpuSwitchModeTolerant_AbsentCardIsNoError(t *testing.T) {
	origDetect, origRun := DetectVFIO, runGPUSwitchScript
	defer func() { DetectVFIO, runGPUSwitchScript = origDetect, origRun }()

	DetectVFIO = func() VFIOReport { return VFIOReport{} } // no GPUs
	called := false
	runGPUSwitchScript = func(string) ([]byte, error) { called = true; return nil, nil }

	if err := gpuSwitchModeTolerant("0x10de", gpuModeVfio); err != nil {
		t.Fatalf("absent card must be tolerated, got %v", err)
	}
	if called {
		t.Fatal("must not run the switch script when no matching card exists")
	}
}

func TestDeployNodeSharesGPU(t *testing.T) {
	resources := map[string]*ResourceDef{
		"nvidia-gpu": {Gpu: &GpuSelector{Vendor: "0x10de"}},
		"abstract":   {}, // no gpu selector
	}
	cases := []struct {
		name string
		node BundleNode
		want bool
	}{
		{"gpu-backed shared token", BundleNode{RequiresShared: []string{"nvidia-gpu"}}, true},
		{"selector-less shared token", BundleNode{RequiresShared: []string{"abstract"}}, false},
		{"no shared claim", BundleNode{}, false},
		{"exclusive is not shared", BundleNode{RequiresExclusive: []string{"nvidia-gpu"}}, false},
	}
	for _, tc := range cases {
		if got := deployNodeSharesGPU(tc.node, resources); got != tc.want {
			t.Errorf("%s: deployNodeSharesGPU = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// --- resource poisoning (device_lock wedge cascade containment) ------------

func TestArbiter_PoisonRoundTripAndStale(t *testing.T) {
	if bootID() == "" {
		t.Skip("no /proc/sys/kernel/random/boot_id on this host")
	}
	a := newTestArbiter(t, nil, &fakeWorld{running: map[string]bool{}})

	a.poisonResource("nvidia-gpu")
	if !a.resourcePoisoned("nvidia-gpu") {
		t.Fatal("token must read poisoned right after poisonResource (same boot)")
	}
	// a marker from a prior boot is stale => not poisoned, and removed.
	if err := os.WriteFile(a.poisonPath("nvidia-gpu"), []byte("some-old-boot-id\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if a.resourcePoisoned("nvidia-gpu") {
		t.Error("a prior-boot marker must read NOT poisoned (the reboot cleared the wedge)")
	}
	if _, err := os.Stat(a.poisonPath("nvidia-gpu")); !os.IsNotExist(err) {
		t.Error("a stale marker must be removed on read")
	}
	a.poisonResource("nvidia-gpu")
	a.clearPoison("nvidia-gpu")
	if a.resourcePoisoned("nvidia-gpu") {
		t.Error("clearPoison must remove the marker")
	}
}

func TestApplyMode_WedgePoisonsToken(t *testing.T) {
	if bootID() == "" {
		t.Skip("no boot_id")
	}
	w := &fakeWorld{resources: map[string]*ResourceDef{"nvidia-gpu": {Gpu: &GpuSelector{Vendor: "0x10de"}}}}
	a := newTestArbiter(t, nil, w)
	a.switchMode = func(string, string) error { return errGPUSwitchWedged }

	if err := a.applyMode([]string{"nvidia-gpu"}, gpuModeVfio); !errors.Is(err, errGPUSwitchWedged) {
		t.Fatalf("applyMode must surface the wedge, got %v", err)
	}
	if !a.resourcePoisoned("nvidia-gpu") {
		t.Error("a wedge during applyMode must POISON the token (cascade containment)")
	}
	// a subsequent applyMode refuses the poisoned token WITHOUT calling switchMode.
	switched := false
	a.switchMode = func(string, string) error { switched = true; return nil }
	if err := a.applyMode([]string{"nvidia-gpu"}, gpuModeVfio); !errors.Is(err, errGPUSwitchWedged) {
		t.Errorf("poisoned token must keep being refused, got %v", err)
	}
	if switched {
		t.Error("a poisoned token must NOT reach switchMode (would re-wedge)")
	}
}

func TestArbiter_PoisonedTokenRefusesAcquire(t *testing.T) {
	if bootID() == "" {
		t.Skip("no boot_id")
	}
	w := &fakeWorld{running: map[string]bool{}, resources: map[string]*ResourceDef{"nvidia-gpu": {Gpu: &GpuSelector{Vendor: "0x10de"}}}}
	a := newTestArbiter(t, map[string]BundleNode{}, w)
	a.poisonResource("nvidia-gpu")

	if _, err := a.AcquireShared("gpu-bed", BundleNode{RequiresShared: []string{"nvidia-gpu"}}, true); err == nil || !strings.Contains(err.Error(), "reboot") {
		t.Fatalf("AcquireShared on a poisoned token must refuse with a reboot-required error, got %v", err)
	}
	if _, err := a.AcquireExclusive("gpu-vm", BundleNode{RequiresExclusive: []string{"nvidia-gpu"}}, true); err == nil || !strings.Contains(err.Error(), "reboot") {
		t.Fatalf("AcquireExclusive on a poisoned token must refuse with a reboot-required error, got %v", err)
	}
}

// H1. The pure formatter: empty holders → a generic clause; one/many → the
// listed "external process(es): comm (pid N), …" form. No /proc, no shell-out.
func TestFormatNvidiaHolders(t *testing.T) {
	if got := formatNvidiaHolders(nil); !strings.Contains(got, "could not be identified") || strings.Contains(got, "pid") {
		t.Fatalf("empty holders must render the generic clause, got %q", got)
	}
	one := formatNvidiaHolders([]NvidiaHolder{{PID: 237390, Comm: "btop"}})
	if one != "external process(es): btop (pid 237390)" {
		t.Fatalf("single holder format wrong, got %q", one)
	}
	many := formatNvidiaHolders([]NvidiaHolder{{PID: 237390, Comm: "btop"}, {PID: 41, Comm: "nvidia-smi"}})
	if many != "external process(es): btop (pid 237390), nvidia-smi (pid 41)" {
		t.Fatalf("multi holder format wrong, got %q", many)
	}
}

// H2. The refusal error names the external holders, points at the
// auto-preempt-and-close-yours remediation, and KEEPS the no-force wording.
func TestNvidiaInUseRefusal_Message(t *testing.T) {
	err := nvidiaInUseRefusal([]NvidiaHolder{{PID: 237390, Comm: "btop"}})
	msg := err.Error()
	for _, want := range []string{"REFUSED", "btop (pid 237390)", "auto-preempts", "close these external GPU clients", "refusing to force-unbind"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal message missing %q:\n%s", want, msg)
		}
	}
	// Empty discovery still produces an actionable (generic) refusal.
	if g := nvidiaInUseRefusal(nil).Error(); !strings.Contains(g, "could not be identified") || !strings.Contains(g, "force-unbind") {
		t.Errorf("empty-holder refusal must stay actionable, got %q", g)
	}
}

// H3. Wiring: an EBUSY (exit-3) vfio refusal is enriched with the discovered
// external holder(s); it stays a plain refusal (NOT errGPUSwitchWedged) and
// never forces. Discovery is injected (no /proc read).
func TestSwitchGPUDriverMode_VfioRefusalNamesHolders(t *testing.T) {
	origDrv, origRun, origDisc := gpuDisplayDriver, runGPUSwitchScript, discoverNvidiaHolders
	defer func() { gpuDisplayDriver, runGPUSwitchScript, discoverNvidiaHolders = origDrv, origRun, origDisc }()

	gpuDisplayDriver = func(string) string { return "nvidia" } // group not in vfio → flip attempted
	runGPUSwitchScript = func(string) ([]byte, error) {
		return []byte("switch-to-vfio REFUSED: " + nvidiaInUseMarker + " (a GPU client holds the card) — refusing to force-unbind (would wedge the device_lock)"), errors.New("exit status 3")
	}
	discoverNvidiaHolders = func() []NvidiaHolder { return []NvidiaHolder{{PID: 237390, Comm: "btop"}} }

	err := switchGPUDriverMode(twoFnGPU(), gpuModeVfio)
	if err == nil {
		t.Fatal("an EBUSY refusal must surface an error")
	}
	if errors.Is(err, errGPUSwitchWedged) {
		t.Fatalf("an external-holder refusal is NOT a wedge, got %v", err)
	}
	if !strings.Contains(err.Error(), "btop (pid 237390)") {
		t.Fatalf("refusal must name the external holder, got %q", err.Error())
	}
}
