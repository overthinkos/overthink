package main

import (
	"context"
	"encoding/json"
	"os"

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

// PutFile is the deploy/step file-PLACEMENT leg: an OUT-OF-PROCESS deploy/step plugin
// that EXECUTES an InstallPlan's steps pushes file content (a service unit, an env.d
// file, the charly binary, a builder artifact) onto the venue. The plugin holds no
// venue filesystem across the process boundary, so it ships the bytes; the host
// materializes them to a private temp file and delegates to the live
// DeployExecutor.PutFile (a plain os.WriteFile for ShellExecutor, scp+install for
// SSHExecutor), preserving the owner_root → root:root semantics. The gRPC call itself
// succeeds; a placement failure travels in PutFileReply.Error (the runReply convention).
func (s *executorReverseServer) PutFile(ctx context.Context, req *pb.PutFileRequest) (*pb.PutFileReply, error) {
	tmp, err := os.CreateTemp("", "charly-putfile-*")
	if err != nil {
		return &pb.PutFileReply{Error: err.Error()}, nil
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) //nolint:errcheck
	if _, err := tmp.Write(req.GetContent()); err != nil {
		_ = tmp.Close()
		return &pb.PutFileReply{Error: err.Error()}, nil
	}
	if err := tmp.Close(); err != nil {
		return &pb.PutFileReply{Error: err.Error()}, nil
	}
	err = s.exec.PutFile(ctx, tmpPath, req.GetPath(), req.GetMode(), req.GetOwnerRoot(), decodeReverseEmitOpts(req.GetOptsJson()))
	return &pb.PutFileReply{Error: errString(err)}, nil
}

// RunCapture is the CHECK-VERB capture leg: an out-of-process exec-based check verb
// (record/dbus — and wl when it externalizes) probes the live venue by capturing
// stdout/stderr/exit. No root
// escalation — the verb's script adds sudo if it needs it. The gRPC call itself
// succeeds; an execution failure (not a non-zero exit) travels in CaptureReply.Error.
func (s *executorReverseServer) RunCapture(ctx context.Context, req *pb.RunRequest) (*pb.CaptureReply, error) {
	stdout, stderr, exit, err := s.exec.RunCapture(ctx, req.GetScript())
	return &pb.CaptureReply{Stdout: stdout, Stderr: stderr, ExitCode: int32(exit), Error: errString(err)}, nil
}

// GetFile is the CHECK-VERB artifact-pull leg: a verb that produces a file on the venue
// (a record .cast / a screenshot) reads it back to the host. asRoot reads via sudo.
func (s *executorReverseServer) GetFile(ctx context.Context, req *pb.GetFileRequest) (*pb.GetFileReply, error) {
	content, err := s.exec.GetFile(ctx, req.GetPath(), req.GetAsRoot(), decodeReverseEmitOpts(req.GetOptsJson()))
	return &pb.GetFileReply{Content: content, Error: errString(err)}, nil
}

// errString is err.Error() or "" — the reverse-channel convention (the RPC succeeds; the
// venue-op failure rides the reply's error field, like runReply).
func errString(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
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
