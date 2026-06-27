package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	pb "github.com/overthinkos/overthink/charly/plugin/proto"
	"github.com/overthinkos/overthink/charly/plugin/sdk"
	"github.com/overthinkos/overthink/charly/spec"
)

// provider.go is the out-of-process record verb provider — charly's host dispatches a
// `record:` check step to it through the registry (ResolveVerb("record") → this
// grpcProvider → invokeVerbProvider) with the FULL #Op marshaled as params_json, a
// CheckEnv snapshot as env, AND — because record is EXEC-based — the host's live
// DeployExecutor attached over the E3b reverse channel (the executorInvoker branch in
// invokeVerbProvider). Because the out-of-process path does NOT run a host-side
// matcher pipeline, this Invoke OWNS the whole verdict: get the venue
// executor (sdk.ExecutorFromInvoke), dispatch the method (RunCapture-driven; `stop` also
// GetFile-pulls the recording to op.Artifact), then evaluate the stdout/stderr/exit_status
// matchers + the artifact validators itself (via the shared sdk implementation — R3), and
// return the wire {status,message} the host decodes.

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

// recordEnv is the plugin-side decode of the CheckEnv the host ships as Operation.Env for
// a `record:` check step (provider_checkenv.go). The fields mirror the shared CheckEnv;
// record reads Mode to skip box-context runs and carries Box/ContainerName/Venue/VenueKind
// for messages — the actual venue work travels over the executor reverse channel, not this
// snapshot (unlike the PORT-based mcp/spice verbs, which carry a pre-resolved endpoint).
type recordEnv struct {
	Box           string `json:"box"`
	Mode          string `json:"mode"` // "live" | "box"
	ContainerName string `json:"container_name"`
	Venue         string `json:"venue"`
	VenueKind     string `json:"venue_kind"`
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke runs one `record:` operation. It decodes the full #Op + the env, skips in box
// mode (no running deployment to record on a disposable `charly check box`), dials back
// the host's live executor over the reverse channel (a missing broker is a HARD FAIL —
// record needs the venue), dispatches the method, and self-evaluates the matchers + the
// artifact validators (`stop`).
func (p provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	var op spec.Op
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &op); err != nil {
			return resultJSON("fail", "record: decode op: "+err.Error())
		}
	}
	var env recordEnv
	if len(req.GetEnvJson()) > 0 {
		_ = json.Unmarshal(req.GetEnvJson(), &env)
	}
	method := string(op.Record)

	// Live-deployment verb: skip under `charly check box` (no running deployment to record
	// in a disposable `podman run --rm`) — mirrors the host's RunModeBox/box-mode skip.
	if env.Mode == "box" {
		return resultJSON("skip", fmt.Sprintf("record: %s requires a running deployment (skip under charly check box)", method))
	}

	// record is EXEC-based: it drives the venue (asciinema/wf-recorder via tmux, the
	// .cast/.mp4 pull) ONLY through the host's live executor over the E3b reverse channel.
	// A missing broker is therefore a HARD FAIL with a clear message, never a silent skip —
	// the verb cannot do its job without the venue.
	exec, err := sdk.ExecutorFromInvoke(req.GetExecutorBrokerId())
	if err != nil {
		return resultJSON("fail", fmt.Sprintf("record: %s has no host executor attached — record needs the live venue (%v)", method, err))
	}

	out, runErr := dispatch(ctx, exec, &op)

	// Exit semantics: the in-tree CLI mapped a Run() error → exit 1, success → exit 0;
	// the host compared that against the authored exit_status (default 0).
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
		return resultJSON("fail", fmt.Sprintf("record: %s: exit=%d, want %d (stderr: %s)", method, exit, wantExit, preview(stderr)))
	}

	if err := sdk.MatchAll(out, op.Stdout); err != nil {
		return resultJSON("fail", fmt.Sprintf("record: %s: stdout: %v (got: %s)", method, err, preview(out)))
	}
	if err := sdk.MatchAll(stderr, op.Stderr); err != nil {
		return resultJSON("fail", fmt.Sprintf("record: %s: stderr: %v (got: %s)", method, err, preview(stderr)))
	}

	// Artifact-producing method (`stop`): the recording was already GetFile-pulled to
	// op.Artifact (the host path) inside dispatch, BEFORE this point, so the validators
	// (min_bytes / min_cast_events / …) read a real file. A no-op for list/start/cmd
	// (op.Artifact empty), mirroring the host's post-run pipeline.
	if op.Artifact != "" {
		if err := sdk.RunArtifactValidators(&op); err != nil {
			return resultJSON("fail", fmt.Sprintf("record: %s: artifact: %v", method, err))
		}
	}

	body := out
	if strings.TrimSpace(body) == "" {
		body = stderr
	}
	if strings.TrimSpace(body) == "" {
		body = fmt.Sprintf("record %s: exit=%d", method, exit)
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
