package main

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	pb "github.com/overthinkos/overthink/charly/plugin/proto"
)

// reverseFakeExec is a fake DeployExecutor that records the scripts it is asked to run
// — enough to prove the reverse-channel server forwards a plugin's RunSystem/RunUser/
// Venue calls to the live executor. The embedded nil DeployExecutor satisfies the
// methods executorReverseServer never calls.
type reverseFakeExec struct {
	DeployExecutor
	lastSystem, lastUser string
}

func (r *reverseFakeExec) Venue() string { return "fake-venue" }
func (r *reverseFakeExec) RunSystem(_ context.Context, s string, _ EmitOpts) error {
	r.lastSystem = s
	return nil
}
func (r *reverseFakeExec) RunUser(_ context.Context, s string, _ EmitOpts) error {
	r.lastUser = s
	return nil
}

// TestExecutorReverseServer_DelegatesToExecutor proves the HOST side of the E3b
// reverse channel: executorReverseServer, served over gRPC (here on an in-memory
// bufconn, exactly as it is served on the go-plugin broker in production), forwards a
// caller's Venue/RunSystem/RunUser to the live DeployExecutor — so an out-of-process
// deploy/step/builder plugin's call-backs reach the real venue. Fails if the server
// does not delegate.
func TestExecutorReverseServer_DelegatesToExecutor(t *testing.T) {
	lis := bufconn.Listen(1 << 20)
	fake := &reverseFakeExec{}
	srv := grpc.NewServer()
	pb.RegisterExecutorServiceServer(srv, &executorReverseServer{exec: fake})
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	conn, err := grpc.Dial("bufnet", //nolint:staticcheck // grpc.Dial is fine for an in-memory bufconn test
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	client := pb.NewExecutorServiceClient(conn)
	ctx := context.Background()

	if _, err := client.RunSystem(ctx, &pb.RunRequest{Script: "echo system"}); err != nil {
		t.Fatalf("RunSystem: %v", err)
	}
	if fake.lastSystem != "echo system" {
		t.Fatalf("RunSystem did not reach the executor, got %q", fake.lastSystem)
	}
	if _, err := client.RunUser(ctx, &pb.RunRequest{Script: "echo user"}); err != nil {
		t.Fatalf("RunUser: %v", err)
	}
	if fake.lastUser != "echo user" {
		t.Fatalf("RunUser did not reach the executor, got %q", fake.lastUser)
	}
	v, err := client.Venue(ctx, &pb.Empty{})
	if err != nil || v.GetVenue() != "fake-venue" {
		t.Fatalf("Venue: %q err=%v", v.GetVenue(), err)
	}
}
