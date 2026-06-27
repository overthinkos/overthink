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

// provider.go is the out-of-process kube verb provider — charly's host dispatches
// a `kube:` check step to it through the registry (ResolveVerb("kube") → this
// grpcProvider → Provider.Invoke) with the FULL #Op marshaled as params_json and a
// CheckEnv snapshot as env. The SAME provider also serves the k3s kubeconfig-merge
// the deploy seam needs: that caller (k8s_plugin.go's invokeKubePlugin) builds a
// synthetic #Op (kube: merge-kubeconfig + the retrieved kubeconfig path + context)
// and reads the result's Message. Because the out-of-process path does NOT run the
// host-side matcher pipeline, this Invoke OWNS the whole verdict:
// dispatch the method, then evaluate the stdout/stderr/exit_status matchers itself
// (via the shared sdk implementation — R3), and return the wire {status,message}
// the host decodes.

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

// kubeEnv is the plugin-side decode of the CheckEnv the host ships as
// Operation.Env for a `kube:` check step (provider_checkenv.go) — only Mode/Box
// matter here (kube probes a cluster, not a container, so it needs no container
// resolution). The merge-kubeconfig deploy seam ships no env (the plugin reads the
// kubeconfig path + context off the Op and uses os.UserHomeDir itself).
type kubeEnv struct {
	Box  string `json:"box"`
	Mode string `json:"mode"` // "live" | "box"
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke runs one `kube:` operation. It decodes the full #Op + the env, handles
// the merge-kubeconfig deploy seam first, skips in box mode (cluster probes need a
// reachable cluster, never a disposable `charly check box`), dispatches the method,
// and self-evaluates the matchers.
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	var op spec.Op
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &op); err != nil {
			return resultJSON("fail", "kube: decode op: "+err.Error())
		}
	}
	var env kubeEnv
	if len(req.GetEnvJson()) > 0 {
		_ = json.Unmarshal(req.GetEnvJson(), &env)
	}
	method := string(op.Kube)

	// merge-kubeconfig is the k3s deploy seam (NOT an authored check method): it
	// merges the retrieved kubeconfig into ~/.kube/config and returns a short
	// success message the host's invokeKubePlugin reads. No matcher pipeline.
	if method == "merge-kubeconfig" {
		msg, err := mergeKubeconfig(op.Kubeconfig, op.KubeContext)
		if err != nil {
			return resultJSON("fail", "kube: merge-kubeconfig: "+err.Error())
		}
		return resultJSON("pass", msg)
	}

	// Cluster-probe verb: skip under `charly check box` — there is no cluster to
	// reach on a disposable `podman run --rm` (mirrors the host's RunModeBox/box-mode skip).
	if env.Mode == "box" {
		return resultJSON("skip", fmt.Sprintf("kube: %s requires a running cluster (skip under charly check box)", method))
	}

	conn := connFromOp(&op)
	out, runErr := dispatch(conn, &op)

	// Exit semantics: the in-tree CLI mapped a Run() error → exit 1, success →
	// exit 0; the host compared that against the authored exit_status (default 0).
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
		return resultJSON("fail", fmt.Sprintf("kube: %s: exit=%d, want %d (stderr: %s)", method, exit, wantExit, preview(stderr)))
	}

	if err := sdk.MatchAll(out, op.Stdout); err != nil {
		return resultJSON("fail", fmt.Sprintf("kube: %s: stdout: %v (got: %s)", method, err, preview(out)))
	}
	if err := sdk.MatchAll(stderr, op.Stderr); err != nil {
		return resultJSON("fail", fmt.Sprintf("kube: %s: stderr: %v (got: %s)", method, err, preview(stderr)))
	}

	body := out
	if strings.TrimSpace(body) == "" {
		body = stderr
	}
	if strings.TrimSpace(body) == "" {
		body = fmt.Sprintf("kube %s: exit=%d", method, exit)
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
