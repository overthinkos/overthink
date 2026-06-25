package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg" // register JPEG decoder for image.DecodeConfig / image.Decode
	_ "image/png"  // register PNG decoder for image.DecodeConfig / image.Decode
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/overthinkos/overthink/charly/plugin/kit"
	"github.com/overthinkos/overthink/charly/plugin/sdk"
)

// artifactValidatableMethods lists the verb/method pairs that
// `validate_ai_artifacts: true` swaps to AI-artifact validation. ALL
// OTHER methods always re-run via the harness's own subprocess — the
// harness is authoritative for non-state-dependent probes (status,
// checkuation, listing, info, file/process/package/port/service/etc.).
//
// The justification for each entry is "re-running this probe a few
// seconds later against the same logically-correct state can yield
// different bytes" — chrome paints at vsync, wayland frame timing,
// VNC/RFB framebuffer at re-capture moment, libvirt display
// surfaces, terminal recordings (asciinema cast files become final
// once `record stop` finalizes them). The `spice` capture methods
// (screenshot/cursor) left this allowlist with the verb when it became
// an EXTERNAL-CHARLY-VERB (candy/plugin-spice) — the out-of-process
// plugin always re-runs and self-validates via sdk.RunArtifactValidators.
//
// Anti-deception properties around this allowlist:
//
//   - The set of `spec.Artifact == true` methods must be the SAME as
//     this allowlist. Drift is caught at compile/test time by
//     TestArtifactValidatableMethods_MatchesArtifactProducingMethodSpecs.
//
//   - When validate_ai_artifacts is true AND the method is in this
//     allowlist, runCharlyVerb skips subprocess execution and runs the
//     post-run validators (artifact_min_bytes / artifact_min_dimensions
//     / artifact_not_uniform / artifact_min_cast_events) against the
//     existing file at the plan-declared `artifact:` path.
//
//   - The freshness mtime gate (artifact mtime ≥ Runner.IterStartTime)
//     prevents pre-staged or stale files from passing.
var artifactValidatableMethods = map[string]bool{
	"cdp/screenshot":     true,
	"wl/screenshot":      true,
	"vnc/screenshot":     true,
	"libvirt/screenshot": true,
	"record/stop":        true,
}

// checkrun_charly_verbs.go is the SHARED live-container-verb dispatch HOST: the
// artifactValidatableMethods allowlist, the runCharlyVerb dispatcher, the artifact
// validators, and resolveCheckApk — all reused by every live verb (R3). The verb
// CONTRACT types (kit.MethodSpec, the kit.PosX positional-arg builder library, and the
// kit.LiveVerbProvider interface) live in charly/plugin/kit/liveverb.go so a live-verb
// CANDY can author its allowlist without importing package main; runCharlyVerb consumes
// that kit.MethodSpec map directly. Each live verb is a thin wrapper around the corresponding `charly check
// <verb> <method>` CLI path — the test framework spawns a subprocess for each check,
// captures stdout/stderr/exit, and feeds the output through the existing matcher pipeline
// (Stdout/Stderr/ExitStatus + artifact size via ArtifactMinBytes). The per-verb providers
// + their <verb>Methods allowlists + run<Verb> dispatchers live in dedicated
// plugin_verb_<verb>.go files (cdp/vnc/wl/dbus/mcp/record/libvirt). NO dep-shedder
// remains here — kube/adb/appium/spice are all extracted as external-charly-verbs
// (candy/plugin-kube, candy/plugin-adb, candy/plugin-appium, candy/plugin-spice),
// dispatching via invokeVerbProvider, never through this subprocess library.
//
// Architectural notes:
//   - Host-side only: the test runner invokes the host `charly` binary, which
//     internally connects to the container (CDP over TCP, WL via exec,
//     D-Bus via delegation, VNC over TCP). No container-side test runner.
//   - RunModeBox short-circuits with a skip: these verbs need a live
//     container with port mappings, which a disposable `podman run --rm`
//     container doesn't expose the same way.
//   - Method allowlists are hand-enumerated (each in its verb's file) so authoring
//     errors surface at `charly box validate` time, not at test-run time. Drift between
//     the CLI and the allowlist is a documentation issue — see /charly-internals:go for
//     the maintenance rule.

// resolveCheckApk resolves a relative committed-APK path (the external adb / appium plugin's
// install / install-app `apk: ./tests/data/...`) against the AUTHORING candy's
// source tree, so a check resolves its fixture whether the candy is local OR
// fetched via @github (the SAME walk-up the deploy path uses, R3). The check's
// Origin is "candy:<key>" where <key> is the candy MAP KEY (a bare name for a
// local candy, the bare @github ref for a fetched one) — CandyDirs is keyed by
// that same key (candySourceDirs), so the single lookup matches in both cases.
//
// It FAILS HARD (returns an error) on every condition where the fixture cannot
// be anchored — a non-candy origin (the step's Origin was lost upstream), an
// absent CandyDirs entry (the candy scan failed or did not see this candy), or a
// file missing under the candy tree. There is NO fallback and NO silent
// cwd-relative pass-through: a wrong CandyDirs must surface here, not be patched
// over into a misleading downstream "no such file".
func (r *Runner) resolveCheckApk(apk, origin string) (string, error) {
	if apk == "" || filepath.IsAbs(apk) {
		return apk, nil
	}
	key, ok := strings.CutPrefix(origin, "candy:")
	if !ok {
		return "", fmt.Errorf("committed APK %q has origin %q, not a candy origin — cannot anchor it to a candy source tree (the step's candy Origin was not propagated)", apk, origin)
	}
	dir := r.CandyDirs[key]
	if dir == "" {
		if r.CandyScanErr != nil {
			return "", fmt.Errorf("committed APK %q (candy %q): candy source-dir scan failed: %w", apk, key, r.CandyScanErr)
		}
		return "", fmt.Errorf("committed APK %q: candy %q is absent from the source scan (%d candies scanned) — cannot anchor the fixture", apk, key, len(r.CandyDirs))
	}
	return resolveApkPath(apk, dir)
}

// The appium positional-arg builders (the caps/selector/session/gesture/execute/raw
// flag-form argv helpers) were removed in the appium → external-plugin dep-shed: appium
// no longer dispatches via runCharlyVerb (there is no `charly check appium` subprocess),
// so its argv builders have no caller. The W3C method dispatch now lives in
// candy/plugin-appium. The adb argv builders left the SAME way (adb → candy/plugin-adb).

// ---------------------------------------------------------------------------
// Verb dispatchers
// ---------------------------------------------------------------------------

// run<Verb> for cdp/vnc/wl/dbus/mcp/record/libvirt lives in each verb's dedicated
// plugin_verb_<verb>.go file (Phase 1 live-container-verb relocation) — alongside its
// provider + method allowlist. NO dep-shedder dispatcher remains here.

// The kube + adb + appium + spice runCharlyVerb dispatchers were removed in their
// external-plugin dep-sheds — none dispatches through a `charly check <verb>` subprocess
// anymore; each grpcProvider (candy/plugin-kube, candy/plugin-adb, candy/plugin-appium,
// candy/plugin-spice) is invoked via invokeVerbProvider with the full #Op.

// runCharlyVerb is the shared dispatch path: skip checks, method lookup,
// argv building, subprocess exec, matcher pipeline, optional artifact size
// assertion. Returns the CheckResult directly.
// noVmDisplayDeviceErr is the substring the VM-target resolver (charly/vm_target.go)
// emits when a VM declares no graphics device of the requested kind ("VM <name> has
// no SPICE/VNC graphics device declared in vm.yml") — the signal for a legitimate N/A
// SKIP, not a check failure. Shared by the in-proc vnc verb (vmDisplayDeviceAbsent
// below) AND the host-side spice endpoint pre-resolver (preresolveSpiceEndpoint), so
// the skip wording is anchored to ONE string (R3).
const noVmDisplayDeviceErr = "graphics device declared in vm.yml"

// vmDisplayDeviceAbsent reports whether the in-proc `vnc` VM-display verb failed
// because the target VM declares no VNC display device — a legitimate N/A SKIP, NOT a
// check failure. The cachyos-gpu operator drops the virtio display head (the
// passed-through RTX heads ARE the display), so a SHARED desktop check skips on the
// operator while still asserting on the disposable check bed (which keeps a virtio
// head) — one shared candy, no operator/bed split (R3). `spice` enforces the SAME rule
// HOST-side now (it is an EXTERNAL-CHARLY-VERB — candy/plugin-spice): the host's
// preresolveSpiceEndpoint detects the no-SPICE-device resolver error and returns the
// skip before dispatch, so spice no longer flows through this subprocess path.
func vmDisplayDeviceAbsent(verb, stderr string) bool {
	return verb == "vnc" && strings.Contains(stderr, noVmDisplayDeviceErr)
}

//nolint:gocyclo // verb dispatch with bifurcated artifact validation (ai-artifact vs exec mode) sharing post-validation; cohesive
func (r *Runner) runCharlyVerb(ctx context.Context, c *Op, verb, method string, allowlist map[string]kit.MethodSpec) CheckResult {
	if r.Mode == RunModeBox {
		return skipf(c, fmt.Sprintf("%s: %s requires a running container (skip under charly check box)", verb, method))
	}
	if r.Box == "" {
		return skipf(c, fmt.Sprintf("%s: %s runner has no image context (should not happen under charly check)", verb, method))
	}

	spec, ok := allowlist[method]
	if !ok {
		return failf(c, "%s: unknown method %q (see /charly:test for the allowlist)", verb, method)
	}

	// Required-modifier check mirrors validate_tests.go but guards against
	// runs where validation was bypassed (e.g. tests loaded directly from a
	// label without re-validating).
	if err := checkRequiredFields(c, spec.Required); err != nil {
		return failf(c, "%s: %s: %v", verb, method, err)
	}

	// Branch: AI-artifact validation mode for state-dependent capture
	// probes ONLY. Activated when score.validate_ai_artifacts is set
	// AND the verb/method is in the narrow artifactValidatableMethods
	// allowlist. The harness scorer skips the subprocess re-execution
	// (which would overwrite the AI's iteration artifact and capture a
	// different chrome/wayland/etc. moment) and instead validates the
	// AI-produced file at the plan-declared `artifact:` path.
	//
	// The freshness mtime gate enforces that the file was written
	// during the current iteration — pre-staged or stale files are
	// rejected with a clear actionable error. This is the load-bearing
	// anti-deception mechanism.
	//
	// stdout/stderr/exit_status matchers are incompatible with this
	// mode: without re-running the command there is no captured
	// output to match against. Authors hitting this combination need
	// to either remove the matchers or split into separate steps.
	key := verb + "/" + method
	if r.ValidateAiArtifacts && artifactValidatableMethods[key] {
		if c.Stdout != nil || c.Stderr != nil || c.ExitStatus != nil {
			return failf(c,
				"%s: %s: validate_ai_artifacts skips command execution; "+
					"stdout/stderr/exit_status matchers cannot be evaluated — "+
					"remove them or split into a separate step", verb, method)
		}
		info, err := os.Stat(c.Artifact)
		if err != nil {
			return failf(c,
				"%s: %s: validate_ai_artifacts requires the AI to have produced %q "+
					"during its iteration (e.g. via `charly check self-evaluate`); "+
					"file not found: %v", verb, method, c.Artifact, err)
		}
		if !r.IterStartTime.IsZero() && info.ModTime().Before(r.IterStartTime) {
			return failf(c,
				"%s: %s: artifact %q is stale (mtime %s, iter started %s) — "+
					"the AI must produce this artifact during the current iteration; "+
					"pre-staged or carried-forward files are not accepted",
				verb, method, c.Artifact,
				info.ModTime().UTC().Format(time.RFC3339),
				r.IterStartTime.UTC().Format(time.RFC3339))
		}
		// Run the artifact validators against the existing AI-produced
		// file. Identical pipeline to the post-execution branch below;
		// validators inspect the binary content and dimensions
		// independently of who wrote the file.
		if err := runArtifactValidators(c); err != nil {
			return failf(c, "%s: %s: %v", verb, method, err)
		}
		return passf(c, fmt.Sprintf("%s %s: validated AI-produced artifact at %s (mtime %s)",
			verb, method, c.Artifact, info.ModTime().UTC().Format(time.RFC3339)))
	}

	// Resolve a relative committed-APK path (adb: install / appium: install-app,
	// `apk: ./tests/data/...`) against the ORIGINATING candy's source tree, so a
	// check authored on a candy resolves to that candy's copy — local OR fetched
	// via @github — instead of the check cwd. Reuses the deploy walk-up (R3).
	if c.Apk != "" {
		resolved, err := r.resolveCheckApk(c.Apk, c.Origin)
		if err != nil {
			return failf(c, "%s: %s: %v", verb, method, err)
		}
		if resolved != c.Apk {
			cc := *c
			cc.Apk = resolved
			c = &cc
		}
	}

	// Build argv: ["check"] + spec.Path + [image?] + spec.PosArgs(c) + ["-i", instance]
	// spec.SkipBox=true elides the image/deploy-name positional (used by
	// kube verbs that operate against a cluster instead of an image).
	argv := append([]string{"check"}, spec.Path...)
	if !spec.SkipBox {
		argv = append(argv, r.Box)
	}
	if spec.PosArgs != nil {
		argv = append(argv, spec.PosArgs(c)...)
	}
	if r.Instance != "" && !spec.SkipBox {
		argv = append(argv, "-i", r.Instance)
	}

	charlyBinary, err := findCharlyBinary()
	if err != nil {
		return failf(c, "%s: %s: %v", verb, method, err)
	}
	cmd := exec.CommandContext(ctx, charlyBinary, argv...)
	stdout, stderr, exit, execErr := runCaptureCmd(cmd)
	if execErr != nil {
		return failf(c, "%s: %s: execution error: %v", verb, method, execErr)
	}
	// Precondition-not-met gate: a VM-display verb run against a deployment that
	// declares no such display device is N/A, not a failure (the SPICE-less
	// cachyos-gpu operator vs the SPICE-having check bed). See vmDisplayDeviceAbsent.
	if vmDisplayDeviceAbsent(verb, stderr) {
		return skipf(c, fmt.Sprintf("%s %s — N/A: deployment has no %s graphics device (SPICE-less GPU desktop)",
			verb, method, strings.ToUpper(verb)))
	}

	wantExit := 0
	if c.ExitStatus != nil {
		wantExit = *c.ExitStatus
	}
	if exit != wantExit {
		return failf(c, "%s: %s: exit=%d, want %d (stderr: %s)", verb, method, exit, wantExit, trimPreview(stderr))
	}

	if err := sdk.MatchAll(stdout, c.Stdout); err != nil {
		return failf(c, "%s: %s: stdout: %v (got: %s)", verb, method, err, trimPreview(stdout))
	}
	if err := sdk.MatchAll(stderr, c.Stderr); err != nil {
		return failf(c, "%s: %s: stderr: %v (got: %s)", verb, method, err, trimPreview(stderr))
	}

	if spec.Artifact {
		if err := runArtifactValidators(c); err != nil {
			return failf(c, "%s: %s: %v", verb, method, err)
		}
	}

	// On PASS, return the captured stdout as the Message (or stderr if
	// stdout is empty — some verbs print to stderr per /charly-build:check
	// "Know which stream a --version-style command writes to"). This
	// makes the captured subprocess output available to downstream
	// `capture: <name>` / `capture_extract:` chains; the docstring on
	// CaptureFromResult promises this and runCommand already does it.
	// Falls back to the exit summary when both streams are empty so
	// the report still has something human-readable.
	body := stdout
	if strings.TrimSpace(body) == "" {
		body = stderr
	}
	if strings.TrimSpace(body) == "" {
		body = fmt.Sprintf("%s %s: exit=%d", verb, method, exit)
	}
	return passf(c, body)
}

// runArtifactValidators is the shared post-validator pipeline used by
// both code paths in runCharlyVerb: (a) after the harness's own subprocess
// exec produced the file, and (b) after the freshness mtime gate
// confirmed the AI's file is fresh in validate_ai_artifacts mode.
// Returns nil on all-pass or the first validator's error.
func runArtifactValidators(c *Op) error {
	if c.ArtifactMinBytes > 0 {
		info, err := os.Stat(c.Artifact)
		if err != nil {
			return fmt.Errorf("artifact %q not found: %w", c.Artifact, err)
		}
		if info.Size() < int64(c.ArtifactMinBytes) {
			return fmt.Errorf("artifact %q size %d < required min_bytes %d",
				c.Artifact, info.Size(), c.ArtifactMinBytes)
		}
	}
	if c.ArtifactMinDimensions != "" {
		if err := assertArtifactMinDimensions(c.Artifact, c.ArtifactMinDimensions); err != nil {
			return err
		}
	}
	if c.ArtifactNotUniform {
		if err := assertArtifactNotUniform(c.Artifact); err != nil {
			return err
		}
	}
	if c.ArtifactMinCastEvents > 0 {
		if err := assertArtifactMinCastEvents(c.Artifact, c.ArtifactMinCastEvents); err != nil {
			return err
		}
	}
	return nil
}

// assertArtifactMinDimensions decodes the artifact's image header (PNG/JPEG)
// and fails if width or height is below the "WxH" requirement. Cheap — uses
// image.DecodeConfig which reads only the header, not the full pixel data.
func assertArtifactMinDimensions(path, wxh string) error {
	parts := strings.SplitN(wxh, "x", 2)
	if len(parts) != 2 {
		return fmt.Errorf("artifact_min_dimensions: bad format %q (want WxH)", wxh)
	}
	wantW, err := strconv.Atoi(parts[0])
	if err != nil || wantW <= 0 {
		return fmt.Errorf("artifact_min_dimensions: bad width %q", parts[0])
	}
	wantH, err := strconv.Atoi(parts[1])
	if err != nil || wantH <= 0 {
		return fmt.Errorf("artifact_min_dimensions: bad height %q", parts[1])
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("artifact %q open: %w", path, err)
	}
	defer f.Close() //nolint:errcheck
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return fmt.Errorf("artifact %q decode-config: %w", path, err)
	}
	if cfg.Width < wantW || cfg.Height < wantH {
		return fmt.Errorf("artifact %q dimensions %dx%d < required min %dx%d",
			path, cfg.Width, cfg.Height, wantW, wantH)
	}
	return nil
}

// assertArtifactNotUniform decodes the full image and samples pixels at 100
// deterministic positions; fails if every sampled pixel shares the same RGBA.
// Catches all-black / all-white / blank-canvas screenshot failures that
// artifact_min_bytes alone would pass (a 100KB all-black PNG has the same
// byte profile as a real screenshot of similar dimensions).
func assertArtifactNotUniform(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("artifact %q open: %w", path, err)
	}
	defer f.Close() //nolint:errcheck
	img, _, err := image.Decode(f)
	if err != nil {
		return fmt.Errorf("artifact %q decode: %w", path, err)
	}
	bounds := img.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()
	if w <= 0 || h <= 0 {
		return fmt.Errorf("artifact %q has zero-size bounds %dx%d", path, w, h)
	}
	// Sample 100 pixels on a 10x10 stride. For very small images this still
	// covers every pixel because step rounds up via max(1, dim/10).
	stepX := max(w/10, 1)
	stepY := max(h/10, 1)
	var firstR, firstG, firstB, firstA uint32
	first := true
	for py := bounds.Min.Y; py < bounds.Max.Y; py += stepY {
		for px := bounds.Min.X; px < bounds.Max.X; px += stepX {
			r, g, b, a := img.At(px, py).RGBA()
			if first {
				firstR, firstG, firstB, firstA = r, g, b, a
				first = false
				continue
			}
			if r != firstR || g != firstG || b != firstB || a != firstA {
				return nil // found a varying pixel — not uniform
			}
		}
	}
	return fmt.Errorf("artifact %q is uniformly one color (RGBA=%d,%d,%d,%d) — likely a blank/black/white screenshot",
		path, firstR>>8, firstG>>8, firstB>>8, firstA>>8)
}

// assertArtifactMinCastEvents validates an asciinema .cast file as having
// at least the requested number of event lines. The cast format is one
// JSON object per line: line 1 is a header object {"version":2, "width":..,
// "height":.., ...}, subsequent non-empty lines are event arrays
// [time_offset, "o"|"i", payload]. Fails if header is missing/malformed
// or fewer than minEvents non-empty event lines follow.
func assertArtifactMinCastEvents(path string, minEvents int) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("artifact %q open: %w", path, err)
	}
	defer f.Close() //nolint:errcheck
	scan := bufio.NewScanner(f)
	// asciinema events can be long; bump the buffer so a 1MB single line
	// does not silently truncate the count.
	scan.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	if !scan.Scan() {
		return fmt.Errorf("artifact %q is empty (expected asciinema cast header on line 1)", path)
	}
	var header map[string]any
	if err := json.Unmarshal(scan.Bytes(), &header); err != nil {
		return fmt.Errorf("artifact %q line 1: not a JSON object (asciinema header expected): %w", path, err)
	}
	if _, ok := header["version"]; !ok {
		return fmt.Errorf("artifact %q line 1: JSON object missing %q field (not an asciinema cast header)", path, "version")
	}
	events := 0
	for scan.Scan() {
		if len(strings.TrimSpace(scan.Text())) == 0 {
			continue
		}
		events++
		if events >= minEvents {
			return nil // reached the required count; stop reading
		}
	}
	if err := scan.Err(); err != nil {
		return fmt.Errorf("artifact %q scan: %w", path, err)
	}
	return fmt.Errorf("artifact %q has %d events, want >= %d", path, events, minEvents)
}

// checkRequiredFields returns an error naming any required field that is
// zero-valued on the Check. Mirrors the validate_tests.go precondition so
// runtime-only callers (e.g. tests loaded from an OCI label into an
// un-validated runner) still surface authoring errors rather than silent
// wrong behavior.
func checkRequiredFields(c *Op, required []string) error {
	var missing []string
	for _, f := range required {
		if isZeroField(c, f) {
			missing = append(missing, strings.ToLower(f))
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("missing required modifier(s): %s", strings.Join(missing, ", "))
}

// isZeroField checks whether the named Check field is at its zero value.
// Enumerates the fields the new verbs use — grep-able at the allowlist site
// so adding a new modifier means adding a case here too.
//
//nolint:gocyclo // canonical field-enumeration switch; grep-able required-field enforcement; extraction fragments validation
func isZeroField(c *Op, name string) bool {
	switch name {
	case "Tab":
		return c.Tab == ""
	case "Expression":
		return c.Expression == ""
	case "URL":
		return c.URL == ""
	case "Selector":
		return c.Selector == ""
	case "Dest":
		return c.Dest == ""
	case "Path":
		return c.Path == ""
	case "Method":
		return c.Method == ""
	case "Artifact":
		return c.Artifact == ""
	case "X":
		return c.X == 0
	case "Y":
		return c.Y == 0
	case "X2":
		return c.X2 == 0
	case "Y2":
		return c.Y2 == 0
	case "Button":
		return c.Button == ""
	case "Text":
		return c.Text == ""
	case "KeyName":
		return c.KeyName == ""
	case "Combo":
		return c.Combo == ""
	case "Direction":
		return c.Direction == ""
	case "Amount":
		return c.Amount == 0
	case "Target":
		return c.Target == ""
	case "Action":
		return c.Action == ""
	case "Query":
		return c.Query == ""
	case "Command":
		return c.Command == ""
	case "Tool":
		return c.Tool == ""
	case "URI":
		return c.URI == ""
	case "Input":
		return c.Input == ""
	case "McpName":
		return c.McpName == ""
	case "Record":
		return c.Record == ""
	case "RecordName":
		return c.RecordName == ""
	case "Libvirt":
		return c.Libvirt == ""
		// The kube required-field cases (Kube/Name/Namespace/Label/Cluster/Manifest/Kind/
		// KubeKind/KubeContext/Kubeconfig/KubeCount/KubeResource/KubeGroup/KubeVersion) were
		// DELETED in the kube → external-plugin dep-shed: kube's required-modifier checks now
		// run inside candy/plugin-kube (methods.go's checkRequiredModifiers), so no remaining
		// in-proc verb's required: list names them. The adb required-field cases
		// (Args/Apk/Property/AppId, plus the "Adb" discriminator) left the SAME way
		// (candy/plugin-adb), as did the appium required-field cases (candy/plugin-appium).
		// The spice "Spice" discriminator case left the SAME way (candy/plugin-spice); its
		// X/Y/Text/KeyName/Artifact modifier cases STAY — shared with the in-proc
		// vnc/wl/libvirt verbs.
	}
	// Unknown field name is a programming error: treat as "not zero" so
	// authoring errors surface elsewhere instead of spurious skips.
	return false
}

// findCharlyBinary returns the absolute path to the `charly` binary the test runner
// should spawn. Prefers /proc/self/exe (the currently-running binary so tests
// invoke the same build that collected them), falling back to $PATH lookup.
// Testability var for mocks.
var findCharlyBinary = defaultFindCharlyBinary

func defaultFindCharlyBinary() (string, error) {
	if p, err := os.Executable(); err == nil && p != "" {
		return p, nil
	}
	return exec.LookPath("charly")
}
