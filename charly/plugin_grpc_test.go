package main

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	pb "github.com/overthinkos/overthink/charly/plugin/proto"
)

// testVerbProvider is an in-proc Provider used to exercise the gRPC contract.
type testVerbProvider struct{ word string }

func (p testVerbProvider) Reserved() string     { return p.word }
func (p testVerbProvider) Class() ProviderClass { return ClassVerb }
func (p testVerbProvider) Invoke(_ context.Context, op *Operation) (*Result, error) {
	j, err := json.Marshal(pluginCheckResult{Status: "pass", Message: "testprobe-ok reserved=" + op.Reserved})
	if err != nil {
		return nil, err
	}
	return &Result{JSON: j}, nil
}

// TestPluginGRPCRoundTrip proves the uniform Provider envelope survives the gRPC
// boundary: a served in-proc provider (providerGRPCServer + metaGRPCServer) is
// reached by the client-side grpcProvider through Describe + buildProviders +
// Invoke, with the pass verdict + marker round-tripping intact. This is the wire
// contract LocalTransport / ExecutorTransport ride on (the go-plugin exec/reattach
// envelope itself is proven by the RDD spikes).
func TestPluginGRPCRoundTrip(t *testing.T) {
	set := newServedSet("2026.0.0", []Provider{testVerbProvider{word: "testprobe"}})

	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	pb.RegisterProviderServer(srv, &providerGRPCServer{set: set})
	pb.RegisterPluginMetaServer(srv, &metaGRPCServer{set: set})
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	conn, err := grpc.DialContext(context.Background(), "bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	pc := &pluginConn{provider: pb.NewProviderClient(conn), meta: pb.NewPluginMetaClient(conn)}

	caps, err := pc.describe(context.Background())
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if caps.GetCalver() != "2026.0.0" {
		t.Fatalf("describe calver = %q, want 2026.0.0", caps.GetCalver())
	}
	wantCap := "verb:testprobe"
	found := false
	for _, c := range caps.GetProvided() {
		if c == wantCap {
			found = true
		}
	}
	if !found {
		t.Fatalf("describe provided = %v, want to contain %q", caps.GetProvided(), wantCap)
	}

	provs, err := pc.buildProviders(caps.GetProvided())
	if err != nil {
		t.Fatalf("buildProviders: %v", err)
	}
	if len(provs) != 1 || provs[0].Class() != ClassVerb || provs[0].Reserved() != "testprobe" {
		t.Fatalf("buildProviders = %+v, want one verb:testprobe", provs)
	}

	out, err := provs[0].Invoke(context.Background(), &Operation{
		Reserved: "testprobe", Op: OpRun, Params: []byte(`{"plugin":"testprobe"}`), Env: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	var pr pluginCheckResult
	if err := json.Unmarshal(out.JSON, &pr); err != nil {
		t.Fatalf("decode result: %v (%s)", err, out.JSON)
	}
	if pr.Status != "pass" || !strings.Contains(pr.Message, "testprobe-ok") {
		t.Fatalf("result = %+v, want pass + testprobe-ok", pr)
	}
}

// TestProviderRegistryDispatch proves the registry resolves a verb provider and
// the (class, word) keying keeps a kind and a verb of the SAME word distinct.
func TestProviderRegistryDispatch(t *testing.T) {
	r := newRegistry()
	if err := r.register(testVerbProvider{word: "dup"}, "builtin"); err != nil {
		t.Fatalf("register verb: %v", err)
	}
	// Same word, different class — must NOT collide.
	if err := r.register(testKindProvider{word: "dup"}, "builtin"); err != nil {
		t.Fatalf("register kind (same word, diff class) should not collide: %v", err)
	}
	// Duplicate (class, word) — must be refused.
	if err := r.register(testVerbProvider{word: "dup"}, "builtin"); err == nil {
		t.Fatalf("duplicate verb:dup should be refused")
	}
	if p, ok := r.ResolveVerb("dup"); !ok || p.Class() != ClassVerb {
		t.Fatalf("ResolveVerb(dup) = %v,%v want verb", p, ok)
	}
	if p, ok := r.ResolveKind("dup"); !ok || p.Class() != ClassKind {
		t.Fatalf("ResolveKind(dup) = %v,%v want kind", p, ok)
	}
}

type testKindProvider struct{ word string }

func (p testKindProvider) Reserved() string     { return p.word }
func (p testKindProvider) Class() ProviderClass { return ClassKind }
func (p testKindProvider) Invoke(_ context.Context, _ *Operation) (*Result, error) {
	return &Result{JSON: []byte("{}")}, nil
}
