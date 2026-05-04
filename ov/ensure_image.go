package main

// ensure_image.go — helpers backing kind:local `images:` (the
// 2026-05 cutover that lets a local-target deploy declare container
// images that must be present before tests can run).
//
// Three input forms (mirror ImagePullCmd.Run):
//   - Short name ("eval-target") — resolved via cfg.Images
//   - Fully-qualified ref ("ghcr.io/overthinkos/eval-target:tag") — pass-through
//   - Remote project ref ("@github.com/...") — resolves the repo, reads
//     its image.yml, returns the declared registry ref
//
// Pull path: routed through DeployExecutor.RunSystem so SSH-routed
// kind:local deploys (host: user@machine) pull on the REMOTE machine.
//
// Build fallback path: in-process invocation of BuildCmd.Run via a
// minimal command struct. Only viable when the executor is a local
// ShellExecutor (we build LOCALLY and trust the local image is what
// the deploy needs); SSH executors return a friendly error pointing
// the operator at manual `ov image build` then re-run.

import (
	"context"
	"fmt"
)

// resolveImageRefForEnsure converts a user-authored image identifier
// into a fully-qualified registry ref usable for podman pull.
//
// Short names require a *Config (image.yml resolution); fully-qualified
// refs and @github.com/... refs are returned as-is (the latter still
// needs ResolveRemoteImage at pull time, which runImagePull handles).
func resolveImageRefForEnsure(image string, cfg *Config, projectDir string) (string, error) {
	if image == "" {
		return "", fmt.Errorf("resolveImageRefForEnsure: empty image")
	}
	stripped := StripURLScheme(image)
	// Remote ref: caller routes through ResolveRemoteImage at pull time.
	if IsRemoteImageRef(stripped) {
		return image, nil
	}
	// Fully-qualified ref: registry segment present.
	if looksLikeFullRef(image) {
		return image, nil
	}
	// Short name: resolve via cfg.Images.
	if cfg == nil {
		return "", fmt.Errorf("short name %q requires a project directory with image.yml", image)
	}
	resolved, err := cfg.ResolveImage(image, "", projectDir)
	if err != nil {
		return "", fmt.Errorf("resolving %q via image.yml: %w", image, err)
	}
	return resolveShellImageRef(resolved.Registry, resolved.Name, ""), nil
}

// runImagePull pulls a registry ref through the supplied executor. The
// executor pattern lets the same pull logic land on the operator's
// machine (ShellExecutor) or on a remote SSH target (SSHExecutor —
// the pull happens on the REMOTE side, which is what kind:local
// deploys with `host: user@machine` want).
//
// Handles all three input forms by delegating registry resolution
// before the actual pull:
//   - @github.com/... — resolves via ResolveRemoteImage (operator-side
//     repo download), then pulls the resulting registry ref via
//     executor.
//   - looksLikeFullRef — pulled as-is.
//   - Short name — resolved against cfg before pull.
func runImagePull(ctx context.Context, exec DeployExecutor, image string, cfg *Config, projectDir string, opts EmitOpts) error {
	stripped := StripURLScheme(image)
	if IsRemoteImageRef(stripped) {
		// Operator-side repo download to determine the registry ref;
		// then pull on the executor's venue.
		rctx, err := ResolveRemoteImage(stripped, "")
		if err != nil {
			return fmt.Errorf("resolving remote ref %q: %w", image, err)
		}
		return execPodmanPull(ctx, exec, rctx.ImageRef, opts)
	}
	if looksLikeFullRef(image) {
		return execPodmanPull(ctx, exec, image, opts)
	}
	// Short name: use cfg to resolve.
	ref, err := resolveImageRefForEnsure(image, cfg, projectDir)
	if err != nil {
		return err
	}
	return execPodmanPull(ctx, exec, ref, opts)
}

// execPodmanPull issues a single `podman pull <ref>` through the
// executor. Errors propagate verbatim — runImagePull's caller (the
// EnsureImageStep emitter) decides whether to fall back to a local
// build.
func execPodmanPull(ctx context.Context, exec DeployExecutor, ref string, opts EmitOpts) error {
	if exec == nil {
		return fmt.Errorf("execPodmanPull: nil executor")
	}
	// Single-quoted to defend against malicious refs (although
	// resolveImageRefForEnsure should have filtered any).
	script := fmt.Sprintf("podman pull %s", shellSingleQuote(ref))
	return exec.RunUser(ctx, script, opts)
}

// runImageBuild builds a short-name image locally via the same
// BuildCmd code path operators hit on the CLI. ONLY runs when the
// executor is a local ShellExecutor — for SSH executors, we don't
// have a generic "build on remote and don't ship over scp" pattern
// (would require remote project + source presence + remote ov), so
// we return an actionable error.
func runImageBuild(ctx context.Context, exec DeployExecutor, name string, opts EmitOpts) error {
	if exec == nil {
		return fmt.Errorf("runImageBuild: nil executor")
	}
	if _, isLocal := exec.(ShellExecutor); !isLocal {
		return fmt.Errorf("runImageBuild: image %q: pull failed and remote-side local build is not supported via %s — pre-build the image on the target then re-run, or use a fully-qualified ref that's publicly pullable", name, exec.Venue())
	}
	// In-process invocation of the BuildCmd Run path via a fresh struct.
	// This mirrors what `ov image build <name>` does on the CLI; running
	// it from inside another command keeps the same behaviour without
	// shelling out.
	bcmd := &BuildCmd{Images: []string{name}, Jobs: 4}
	return bcmd.Run()
}

// (shellSingleQuote is defined in tasks.go and reused here.)
