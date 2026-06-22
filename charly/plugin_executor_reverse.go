package main

import (
	"context"
	"encoding/json"

	pb "github.com/overthinkos/overthink/charly/plugin/proto"
)

// executorReverseServer is the HOST side of the E3b reverse channel: it serves the
// proto ExecutorService by delegating to a live DeployExecutor (ShellExecutor /
// SSHExecutor / NestedExecutor). A deploy/step/builder provider's executor holds live
// OS resources (shell FDs, SSH connections) that cannot cross a process boundary, so
// an OUT-OF-PROCESS plugin never holds it — instead the host stands one of these up on
// the go-plugin GRPCBroker per deploy Invoke, passes its broker id in
// InvokeRequest.executor_broker_id, and the plugin dials back to run ops on the real
// venue. Built-in providers use the typed DeployExecutor directly (no wire).
type executorReverseServer struct {
	pb.UnimplementedExecutorServiceServer
	exec DeployExecutor
}

func (s *executorReverseServer) Venue(context.Context, *pb.Empty) (*pb.VenueReply, error) {
	return &pb.VenueReply{Venue: s.exec.Venue()}, nil
}

func (s *executorReverseServer) RunSystem(ctx context.Context, req *pb.RunRequest) (*pb.RunReply, error) {
	return runReply(s.exec.RunSystem(ctx, req.GetScript(), decodeReverseEmitOpts(req.GetOptsJson())))
}

func (s *executorReverseServer) RunUser(ctx context.Context, req *pb.RunRequest) (*pb.RunReply, error) {
	return runReply(s.exec.RunUser(ctx, req.GetScript(), decodeReverseEmitOpts(req.GetOptsJson())))
}

// decodeReverseEmitOpts decodes the JSON EmitOpts carried in a RunRequest; an empty
// payload yields the zero EmitOpts (the common "no options" call).
func decodeReverseEmitOpts(b []byte) EmitOpts {
	var o EmitOpts
	if len(b) > 0 {
		_ = json.Unmarshal(b, &o)
	}
	return o
}

// runReply maps a DeployExecutor error onto a RunReply — the gRPC call itself
// succeeds; the script's error (if any) travels in the reply so the plugin sees the
// same string the in-proc executor would have returned.
func runReply(err error) (*pb.RunReply, error) {
	if err != nil {
		return &pb.RunReply{Error: err.Error()}, nil
	}
	return &pb.RunReply{}, nil
}
