package sdk

import (
	"context"
	"encoding/json"
	"errors"

	plugin "github.com/hashicorp/go-plugin"

	pb "github.com/overthinkos/overthink/charly/plugin/proto"
	"github.com/overthinkos/overthink/charly/spec"
)

// servedBroker is the go-plugin GRPCBroker captured when this plugin's gRPC server
// starts (grpcPlugin.GRPCServer in sdk.go). A deploy/step/builder plugin dials the
// host's E3b reverse-channel ExecutorService through it. One broker per plugin
// process (go-plugin's model), so a package var is the natural home.
var servedBroker *plugin.GRPCBroker

// Executor is the plugin-side handle to the host's live DeployExecutor over the E3b
// reverse channel. An out-of-process deploy/step/builder plugin runs shell/SSH ops on
// the real venue by calling these; the host executes them with the executor it stood
// up on the broker for this Invoke. The plugin never holds the (unmarshallable)
// executor itself.
type Executor struct {
	client pb.ExecutorServiceClient
}

// ExecutorFromInvoke dials the host's ExecutorService using the broker id the host
// passed in InvokeRequest.executor_broker_id. Errors if this plugin was not served
// over go-plugin (no broker) or the id is 0 (no executor attached — a verb/kind op,
// or a deploy op the host ran in-proc).
func ExecutorFromInvoke(brokerID uint32) (*Executor, error) {
	if servedBroker == nil {
		return nil, errors.New("sdk: no go-plugin broker (plugin not served over go-plugin)")
	}
	if brokerID == 0 {
		return nil, errors.New("sdk: no host executor attached (executor_broker_id=0)")
	}
	conn, err := servedBroker.Dial(brokerID)
	if err != nil {
		return nil, err
	}
	return &Executor{client: pb.NewExecutorServiceClient(conn)}, nil
}

// Venue returns the host executor's stable venue identifier.
func (e *Executor) Venue(ctx context.Context) (string, error) {
	r, err := e.client.Venue(ctx, &pb.Empty{})
	if err != nil {
		return "", err
	}
	return r.GetVenue(), nil
}

// RunSystem runs a root (sudo) script on the venue; optsJSON is a marshalled EmitOpts
// (nil for none). A non-empty reply error is the script's failure on the venue.
func (e *Executor) RunSystem(ctx context.Context, script string, optsJSON []byte) error {
	return runErr(e.client.RunSystem(ctx, &pb.RunRequest{Script: script, OptsJson: optsJSON}))
}

// RunUser runs an unprivileged script on the venue (see RunSystem).
func (e *Executor) RunUser(ctx context.Context, script string, optsJSON []byte) error {
	return runErr(e.client.RunUser(ctx, &pb.RunRequest{Script: script, OptsJson: optsJSON}))
}

func runErr(r *pb.RunReply, err error) error {
	if err != nil {
		return err
	}
	if r.GetError() != "" {
		return errors.New(r.GetError())
	}
	return nil
}

// PutFile places file content at a path on the venue — the deploy/step file-PLACEMENT
// leg. An out-of-process deploy/step plugin that EXECUTES an InstallPlan's steps ships
// the bytes (a service unit, an env.d file, the charly binary, a builder artifact);
// the host materializes them and delegates to the live DeployExecutor.PutFile.
// ownerRoot == true installs the file as root:root (root-owned system paths); mode is
// the octal permission bits. Binary-safe (proto bytes). A non-empty reply error is the
// placement failure on the venue.
func (e *Executor) PutFile(ctx context.Context, remotePath string, content []byte, mode uint32, ownerRoot bool) error {
	r, err := e.client.PutFile(ctx, &pb.PutFileRequest{
		Path:      remotePath,
		Content:   content,
		Mode:      mode,
		OwnerRoot: ownerRoot,
	})
	if err != nil {
		return err
	}
	if r.GetError() != "" {
		return errors.New(r.GetError())
	}
	return nil
}

// RunCapture runs a command on the venue and returns stdout/stderr/exit separately —
// the check-verb capture leg (an out-of-process exec-based check verb probing the live
// container). A non-empty reply error is an EXECUTION failure, NOT a non-zero exit
// (which rides the returned exit code). Mirrors kit.Executor.RunCapture over the wire.
func (e *Executor) RunCapture(ctx context.Context, script string) (stdout, stderr string, exit int, err error) {
	r, callErr := e.client.RunCapture(ctx, &pb.RunRequest{Script: script})
	if callErr != nil {
		return "", "", 0, callErr
	}
	if r.GetError() != "" {
		return r.GetStdout(), r.GetStderr(), int(r.GetExitCode()), errors.New(r.GetError())
	}
	return r.GetStdout(), r.GetStderr(), int(r.GetExitCode()), nil
}

// GetFile reads a venue file back to the host (asRoot reads via sudo) — the check-verb
// artifact-pull leg (a screenshot / recording produced on the venue).
func (e *Executor) GetFile(ctx context.Context, path string, asRoot bool) ([]byte, error) {
	r, callErr := e.client.GetFile(ctx, &pb.GetFileRequest{Path: path, AsRoot: asRoot})
	if callErr != nil {
		return nil, callErr
	}
	if r.GetError() != "" {
		return nil, errors.New(r.GetError())
	}
	return r.GetContent(), nil
}

// RunHostStep is the HOST-ENGINE channel leg (the generalization of the former F3 build channel): a
// deploy/step plugin walking an InstallPlan that hits one of the five step kinds it cannot
// execute itself — BuilderStep (podman / makepkg / EnsureImagePresent), LocalPkgInstallStep,
// SystemPackagesStep (the DistroConfig package-template render), an act-verb OpStep (a
// builtin ProvisionActor that needs the in-proc registry), or an ExternalPluginStep (a verb
// served by ANOTHER out-of-process plugin, dispatched over a nested reverse channel) — drives
// this. The host reconstructs the step, runs the existing in-core machinery on the host,
// applies the effect onto the venue, and returns the step's recorded reverse ops. The plugin
// folds them into its DeployReply (sdk.BuildDeployReply) so `charly bundle del` replays them
// (record-and-replay teardown). The plugin owns the plan WALK; the host owns the host ENGINE.
// A non-nil error is a host-engine/apply FAILURE on the venue.
func (e *Executor) RunHostStep(ctx context.Context, step spec.InstallStepView, optsJSON []byte) ([]spec.ReverseOp, error) {
	stepJSON, err := json.Marshal(step)
	if err != nil {
		return nil, err
	}
	r, callErr := e.client.RunHostStep(ctx, &pb.HostStepRequest{StepJson: stepJSON, OptsJson: optsJSON})
	if callErr != nil {
		return nil, callErr
	}
	if r.GetError() != "" {
		return nil, errors.New(r.GetError())
	}
	var ops []spec.ReverseOp
	if len(r.GetReverseOpsJson()) > 0 {
		if err := json.Unmarshal(r.GetReverseOpsJson(), &ops); err != nil {
			return nil, err
		}
	}
	return ops, nil
}
