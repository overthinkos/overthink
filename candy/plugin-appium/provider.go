package main

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg" // register JPEG decoder for image.DecodeConfig / image.Decode
	_ "image/png"  // register PNG decoder for image.DecodeConfig / image.Decode
	"os"
	"strconv"
	"strings"

	pb "github.com/overthinkos/overthink/charly/plugin/proto"
	"github.com/overthinkos/overthink/charly/plugin/sdk"
	"github.com/overthinkos/overthink/charly/spec"
)

// provider.go is the out-of-process appium verb provider — charly's host dispatches an
// `appium:` check step to it through the registry (ResolveVerb("appium") → this
// grpcProvider → Provider.Invoke) with the FULL #Op marshaled as params_json and a
// CheckEnv snapshot as env. Because the out-of-process path does NOT run the host's
// runCharlyVerb matcher pipeline, this Invoke OWNS the whole verdict: dispatch the
// method, then evaluate the stdout/stderr/exit_status matchers + artifact validators
// itself (via the shared sdk matcher implementation — R3), and return the wire
// {status,message} the host's invokeVerbProvider decodes.

// checkEnv is the plugin-side decode of charly's CheckEnv (provider_checkenv.go) — the
// serializable invocation context the host ships as Operation.Env. ContainerName is the
// host-authoritative container name (charly-<box>[_<instance>], registry-ref-stripped)
// the plugin uses to reach the running Appium server.
type checkEnv struct {
	Box           string `json:"box"`
	Instance      string `json:"instance"`
	Mode          string `json:"mode"` // "live" | "box"
	ContainerName string `json:"container_name"`
}

// pluginResult is the wire form a verb provider returns (the host's pluginCheckResult).
type pluginResult struct {
	Status  string `json:"status"` // "pass" | "fail" | "skip"
	Message string `json:"message"`
}

func resultJSON(status, msg string) (*pb.InvokeReply, error) {
	j, err := json.Marshal(pluginResult{Status: status, Message: msg})
	if err != nil {
		return nil, err
	}
	return &pb.InvokeReply{ResultJson: j}, nil
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke runs one `appium:` check step. It decodes the full #Op + the check env, skips
// in box mode (these probes need a running container with port mappings), dispatches the
// method, and self-evaluates the matchers + artifact validators.
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	var op spec.Op
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &op); err != nil {
			return resultJSON("fail", "appium: decode op: "+err.Error())
		}
	}
	var env checkEnv
	if len(req.GetEnvJson()) > 0 {
		_ = json.Unmarshal(req.GetEnvJson(), &env)
	}
	method := string(op.Appium)

	// Live-container verb: skip under `charly check box` (no port mappings on a
	// disposable `podman run --rm`) — mirrors runCharlyVerb's RunModeBox skip.
	if env.Mode == "box" {
		return resultJSON("skip", fmt.Sprintf("appium: %s requires a running container (skip under charly check box)", method))
	}
	if env.Box == "" {
		return resultJSON("skip", fmt.Sprintf("appium: %s has no image context", method))
	}

	out, runErr := dispatch(&env, &op)

	// Exit semantics: the in-tree CLI mapped a Run() error → exit 1, success → exit 0;
	// the host compared that against the authored exit_status (default 0). Preserve it.
	exit := 0
	stderr := ""
	if runErr != nil {
		exit = 1
		stderr = runErr.Error()
	}
	wantExit := 0
	if op.ExitStatus != nil {
		wantExit = *op.ExitStatus
	}
	if exit != wantExit {
		return resultJSON("fail", fmt.Sprintf("appium: %s: exit=%d, want %d (stderr: %s)", method, exit, wantExit, preview(stderr)))
	}

	if err := sdk.MatchAll(out, op.Stdout); err != nil {
		return resultJSON("fail", fmt.Sprintf("appium: %s: stdout: %v (got: %s)", method, err, preview(out)))
	}
	if err := sdk.MatchAll(stderr, op.Stderr); err != nil {
		return resultJSON("fail", fmt.Sprintf("appium: %s: stderr: %v (got: %s)", method, err, preview(stderr)))
	}

	// Artifact validators run for the one artifact-producing method (screenshot) —
	// mirrors runCharlyVerb's spec.artifact branch.
	if method == "screenshot" {
		if err := runArtifactValidators(&op); err != nil {
			return resultJSON("fail", fmt.Sprintf("appium: %s: %v", method, err))
		}
	}

	body := out
	if strings.TrimSpace(body) == "" {
		body = stderr
	}
	if strings.TrimSpace(body) == "" {
		body = fmt.Sprintf("appium %s: exit=%d", method, exit)
	}
	return resultJSON("pass", body)
}

// preview trims long output for an error message (mirrors charly's trimPreview).
func preview(s string) string {
	s = strings.TrimSpace(s)
	const max = 400
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// runArtifactValidators is the shared post-validator pipeline (min_bytes /
// min_dimensions / not_uniform), ported from charly/checkrun_charly_verbs.go so an
// out-of-process screenshot check enforces the same artifact-reality guarantees.
func runArtifactValidators(op *spec.Op) error {
	if op.ArtifactMinBytes > 0 {
		info, err := os.Stat(op.Artifact)
		if err != nil {
			return fmt.Errorf("artifact %q not found: %w", op.Artifact, err)
		}
		if info.Size() < int64(op.ArtifactMinBytes) {
			return fmt.Errorf("artifact %q size %d < required min_bytes %d", op.Artifact, info.Size(), op.ArtifactMinBytes)
		}
	}
	if op.ArtifactMinDimensions != "" {
		if err := assertArtifactMinDimensions(op.Artifact, op.ArtifactMinDimensions); err != nil {
			return err
		}
	}
	if op.ArtifactNotUniform {
		if err := assertArtifactNotUniform(op.Artifact); err != nil {
			return err
		}
	}
	return nil
}

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
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return fmt.Errorf("artifact %q decode-config: %w", path, err)
	}
	if cfg.Width < wantW || cfg.Height < wantH {
		return fmt.Errorf("artifact %q dimensions %dx%d < required min %dx%d", path, cfg.Width, cfg.Height, wantW, wantH)
	}
	return nil
}

func assertArtifactNotUniform(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("artifact %q open: %w", path, err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return fmt.Errorf("artifact %q decode: %w", path, err)
	}
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= 0 || h <= 0 {
		return fmt.Errorf("artifact %q has zero-size bounds %dx%d", path, w, h)
	}
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
				return nil
			}
		}
	}
	return fmt.Errorf("artifact %q is uniformly one color (RGBA=%d,%d,%d,%d) — likely a blank/black/white screenshot",
		path, firstR>>8, firstG>>8, firstB>>8, firstA>>8)
}
