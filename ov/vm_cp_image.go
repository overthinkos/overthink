package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// VmCpImageCmd loads a locally-built container image into a running VM guest's
// (rootful) podman storage via `podman save | scp | podman load`. This is the
// host→guest delivery path for images that are NOT on a registry — the case a
// disposable GPU eval bed hits: the freshly-built `cachyos.cuda-eval` image
// exists only in the operator's local podman, but the in-guest CUDA container
// must run from the guest's own storage.
//
// Idempotent: skips the transfer when the guest already has the image.
type VmCpImageCmd struct {
	VM    string `arg:"" help:"kind:vm entity name (uses its managed ov-<name> ssh alias)"`
	Image string `arg:"" help:"image ref (short name or full ref) present in host podman storage"`
	As    string `long:"as" help:"after load, tag the image in the guest under this stable ref (e.g. localhost/cuda-eval:latest)"`
}

func (c *VmCpImageCmd) Run() error {
	ref := c.Image
	// Resolve a short name (e.g. "cachyos.cuda-eval") to a concrete local ref.
	if !hostImageExists("podman", ref) {
		if resolved, err := ResolveNewestLocalCalVer("podman", ref); err == nil && resolved != "" {
			ref = resolved
		}
	}
	if !hostImageExists("podman", ref) {
		return fmt.Errorf("image %q not found in host podman storage — build it first (ov image build)", c.Image)
	}
	guest := sshParamsForVm(c.VM)
	return TransferImageToGuest(context.Background(), guest, "podman", ref, c.As, EmitOpts{})
}

// hostImageExists reports whether the host engine has the image locally.
func hostImageExists(engine, ref string) bool {
	cmd := exec.Command(engine, "image", "exists", ref)
	return cmd.Run() == nil
}

// TransferImageToGuest streams a host image into a VM guest's rootful podman
// storage by piping `podman save <ref>` straight into `ssh <guest> sudo podman
// load` — NO intermediate tarball on either side. (A file-based copy fails for
// a multi-GB image because the guest's /tmp is a size-limited tmpfs.) The image
// lands in ROOT podman storage so a `sudo podman run --device
// nvidia.com/gpu=all` in the guest — which needs /dev/nvidia* access — finds it.
//
// VERIFIED transfer: `podman load` can exit 0 on a TRUNCATED stream and
// register an image whose overlay layers are incomplete — a `podman run` then
// fails with `faccessat …/storage/overlay/<hash>: no such file`. So the
// transfer is not trusted on the load exit code alone:
//   - The idempotency skip fires only when the guest already holds the target
//     ref AND that image is verified intact (a name-only check would wrongly
//     skip a present-but-torn image — the case a disposable VM bed hits when
//     `ov update` recreates the domain over the SAME persistent qcow2, so the
//     guest's prior — possibly partial — image survives).
//   - After a fresh load, the image is probed; on the overlay-corruption
//     signature it is dropped and re-streamed ONCE; a second failure is a hard
//     error (surfaced, never silently shipped as a broken image).
//
// Requires an *SSHExecutor (the VM case). The GPU eval bed is the first caller.
func TransferImageToGuest(ctx context.Context, de DeployExecutor, hostEngine, ref, as string, opts EmitOpts) error {
	if de == nil {
		return fmt.Errorf("TransferImageToGuest: nil executor")
	}
	if hostEngine == "" {
		hostEngine = "podman"
	}

	// The stable `as` name (when set) is what the consumer references; else the
	// streamed ref itself.
	probeRef := ref
	if as != "" {
		probeRef = as
	}

	// Verified idempotency.
	if guestHasImage(ctx, de, probeRef) {
		if !guestImageCorrupt(ctx, de, probeRef) {
			fmt.Fprintf(os.Stderr, "cp-image: guest already has %s (verified intact) — skipping transfer\n", probeRef)
			return nil
		}
		fmt.Fprintf(os.Stderr, "cp-image: guest %s is present but corrupt (torn overlay) — re-loading\n", probeRef)
		removeGuestImages(ctx, de, probeRef, ref)
	}

	sshExec, ok := de.(*SSHExecutor)
	if !ok {
		return fmt.Errorf("TransferImageToGuest: requires an SSH executor (got %T)", de)
	}

	if opts.DryRun {
		fmt.Fprintf(os.Stderr, "[dry-run] %s save %s | ssh <guest> sudo podman load\n", hostEngine, ref)
		return nil
	}

	if err := streamLoadAndTag(ctx, sshExec, de, hostEngine, ref, as, opts); err != nil {
		return err
	}
	if guestImageCorrupt(ctx, de, probeRef) {
		fmt.Fprintf(os.Stderr, "cp-image: load produced a corrupt %s — re-streaming once\n", probeRef)
		removeGuestImages(ctx, de, probeRef, ref)
		if err := streamLoadAndTag(ctx, sshExec, de, hostEngine, ref, as, opts); err != nil {
			return err
		}
		if guestImageCorrupt(ctx, de, probeRef) {
			return fmt.Errorf("cp-image: %s is still corrupt in guest storage after a clean re-load — transfer unreliable", probeRef)
		}
	}
	fmt.Fprintf(os.Stderr, "cp-image: %s is now in guest storage (verified intact)\n", probeRef)
	return nil
}

// guestHasImage reports whether the guest engine holds the image by name.
func guestHasImage(ctx context.Context, de DeployExecutor, ref string) bool {
	_, _, code, err := de.RunCapture(ctx, "sudo podman image exists "+deployShellQuote(ref))
	return err == nil && code == 0
}

// guestImageCorrupt reports whether an existing guest image is unusable because
// its overlay storage is torn (a lower layer's diff dir is missing). It mounts
// the image's rootfs via a throwaway `podman run … /usr/bin/true` (no GPU, no
// entrypoint) — a torn layer fails container setup with a
// `…/storage/overlay/<hash>: no such file` error. ONLY that storage signature
// counts as corruption: any other failure (e.g. the probe binary is absent, an
// exotic entrypoint) means the overlay mounted fine, so the image is treated as
// intact — the probe is an integrity check, not an entrypoint test.
func guestImageCorrupt(ctx context.Context, de DeployExecutor, ref string) bool {
	stdout, stderr, code, err := de.RunCapture(ctx,
		"sudo podman run --rm --entrypoint /usr/bin/true "+deployShellQuote(ref))
	if err == nil && code == 0 {
		return false
	}
	return strings.Contains(stdout+stderr, "storage/overlay")
}

// removeGuestImages best-effort removes the given refs (and their now-unused
// layers) from the guest so a subsequent load re-extracts clean overlay dirs.
// `podman rmi -f` on every ref that points at the torn image ID is required —
// dropping only one tag leaves the broken layers in storage, and a re-load that
// shares those layer digests would skip extraction and inherit the corruption.
func removeGuestImages(ctx context.Context, de DeployExecutor, refs ...string) {
	for _, r := range refs {
		if r == "" {
			continue
		}
		_, _, _, _ = de.RunCapture(ctx, "sudo podman rmi -f "+deployShellQuote(r))
	}
}

// streamLoadAndTag streams `hostEngine save <ref>` into the guest's rootful
// podman storage (`ssh … sudo podman load`) with NO intermediate tarball, then
// (when `as` is set) tags the loaded ref under that stable name. Disk-backed on
// the guest (podman extracts into /var/lib/containers), so no tmpfs limit.
func streamLoadAndTag(ctx context.Context, sshExec *SSHExecutor, de DeployExecutor, hostEngine, ref, as string, opts EmitOpts) error {
	fmt.Fprintf(os.Stderr, "cp-image: streaming %s into guest podman (save | ssh load)...\n", ref)
	sshArgs := append(sshExec.sshBaseArgs(), "sudo", "podman", "load")
	save := osExecCommand(ctx, hostEngine, "save", ref)
	load := osExecCommand(ctx, "ssh", sshArgs...)
	pipe, err := save.StdoutPipe()
	if err != nil {
		return fmt.Errorf("cp-image: stdout pipe: %w", err)
	}
	load.Stdin = pipe
	load.Stdout = os.Stderr
	load.Stderr = os.Stderr
	save.Stderr = os.Stderr
	if err := load.Start(); err != nil {
		return fmt.Errorf("cp-image: start ssh load: %w", err)
	}
	if err := save.Start(); err != nil {
		return fmt.Errorf("cp-image: start %s save: %w", hostEngine, err)
	}
	saveErr := save.Wait()
	loadErr := load.Wait()
	if saveErr != nil {
		return fmt.Errorf("cp-image: %s save %s: %w", hostEngine, ref, saveErr)
	}
	if loadErr != nil {
		return fmt.Errorf("cp-image: guest podman load: %w", loadErr)
	}
	if as != "" {
		tagScript := "sudo podman tag " + deployShellQuote(ref) + " " + deployShellQuote(as)
		if err := de.RunSystem(ctx, tagScript, opts); err != nil {
			return fmt.Errorf("cp-image: guest podman tag %s -> %s: %w", ref, as, err)
		}
	}
	return nil
}

// osExecCommand is a tiny indirection so tests can stub host command exec.
var osExecCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}
