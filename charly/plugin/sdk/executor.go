package sdk

import (
	"context"
	"errors"

	plugin "github.com/hashicorp/go-plugin"

	pb "github.com/overthinkos/overthink/charly/plugin/proto"
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
