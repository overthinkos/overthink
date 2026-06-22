package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeExecutor records calls and returns canned results by command-prefix.
// Keeps tests hermetic — no actual podman/docker exec happens.
type fakeExecutor struct {
	responses []fakeResponse
	calls     []string
}

type fakeResponse struct {
	matchPrefix string
	stdout      string
	stderr      string
	exit        int
	err         error
}

// fakeExecutor implements DeployExecutor (post-2026-04 cutover). Only
// RunCapture is exercised by the tests below; the other interface methods
// return zero values so the type still satisfies the interface contract.
func (f *fakeExecutor) RunCapture(ctx context.Context, cmd string) (string, string, int, error) {
	f.calls = append(f.calls, cmd)
	for _, r := range f.responses {
		if strings.HasPrefix(cmd, r.matchPrefix) || strings.Contains(cmd, r.matchPrefix) {
			return r.stdout, r.stderr, r.exit, r.err
		}
	}
	return "", "no fake response registered for: " + cmd, 127, nil
}

func (f *fakeExecutor) Kind() string  { return "fake" }
func (f *fakeExecutor) Venue() string { return "fake" }
func (f *fakeExecutor) ResolveHome(_ context.Context, _ string) (string, error) {
	return "/home/fake", nil
}

// Stubs satisfying the rest of DeployExecutor — never called by these tests.
func (f *fakeExecutor) RunSystem(_ context.Context, _ string, _ EmitOpts) error { return nil }
func (f *fakeExecutor) RunUser(_ context.Context, _ string, _ EmitOpts) error   { return nil }
func (f *fakeExecutor) RunBuilder(_ context.Context, _ BuilderRunOpts) ([]byte, error) {
	return nil, nil
}
func (f *fakeExecutor) PutFile(_ context.Context, _, _ string, _ uint32, _ bool, _ EmitOpts) error {
	return nil
}
func (f *fakeExecutor) GetFile(_ context.Context, _ string, _ bool, _ EmitOpts) ([]byte, error) {
	return nil, nil
}

func newFakeRunner(t *testing.T, mode RunMode) (*Runner, *fakeExecutor) {
	t.Helper()
	fake := &fakeExecutor{}
	r := NewRunner(fake, &CheckVarResolver{Env: map[string]string{}}, mode)
	return r, fake
}

// file verb — exists pass, mode mismatch, filetype check, missing file.
func TestRunner_FileVerb(t *testing.T) {
	t.Run("exists true, mode ok", func(t *testing.T) {
		r, fake := newFakeRunner(t, RunModeLive)
		fake.responses = []fakeResponse{
			{matchPrefix: "if [ -e", stdout: "exists=1|regular file|755|root|root\n"},
		}
		results := r.Run(context.Background(), []Op{
			{File: "/usr/bin/redis-server", Exists: new(true), Mode: "0755", Filetype: "file"},
		})
		if len(results) != 1 || results[0].Status != TestPass {
			t.Errorf("expected pass, got %+v", results[0])
		}
	})

	t.Run("mode mismatch", func(t *testing.T) {
		r, fake := newFakeRunner(t, RunModeLive)
		fake.responses = []fakeResponse{
			{matchPrefix: "if [ -e", stdout: "exists=1|regular file|755|root|root\n"},
		}
		results := r.Run(context.Background(), []Op{
			{File: "/x", Mode: "0644"},
		})
		if results[0].Status != TestFail || !strings.Contains(results[0].Message, "mode") {
			t.Errorf("expected mode failure, got %+v", results[0])
		}
	})

	t.Run("absent as expected", func(t *testing.T) {
		r, fake := newFakeRunner(t, RunModeLive)
		fake.responses = []fakeResponse{
			{matchPrefix: "if [ -e", stdout: "exists=0||||\n"},
		}
		results := r.Run(context.Background(), []Op{
			{File: "/nope", Exists: new(false)},
		})
		if results[0].Status != TestPass {
			t.Errorf("expected pass for absent-as-expected, got %+v", results[0])
		}
	})

	t.Run("exists false but file present", func(t *testing.T) {
		r, fake := newFakeRunner(t, RunModeLive)
		fake.responses = []fakeResponse{
			{matchPrefix: "if [ -e", stdout: "exists=1|regular file|644|root|root\n"},
		}
		results := r.Run(context.Background(), []Op{
			{File: "/x", Exists: new(false)},
		})
		if results[0].Status != TestFail {
			t.Errorf("expected fail, got %+v", results[0])
		}
	})
}

// port plugin verb — listening via fake ss output, unreachable via dial timeout,
// host-side dial skip under box-test mode. Now a dedicated built-in plugin unit
// dispatched IN-PROCESS via the CheckVerbProvider RunVerb path (TestMain loads its
// schema); authored as plugin: port + plugin_input.
func TestRunner_PortPlugin_Listening(t *testing.T) {
	r, fake := newFakeRunner(t, RunModeLive)
	fake.responses = []fakeResponse{
		{matchPrefix: "(ss -tlnH", exit: 0},
	}
	res := r.Run(context.Background(), []Op{
		{Plugin: "port", PluginInput: map[string]any{"port": 6379, "listening": true}},
	})
	if res[0].Status != TestPass {
		t.Errorf("expected pass, got %+v", res[0])
	}
}

func TestRunner_PortPlugin_NotListening(t *testing.T) {
	r, fake := newFakeRunner(t, RunModeLive)
	fake.responses = []fakeResponse{
		{matchPrefix: "(ss -tlnH", exit: 1},
	}
	res := r.Run(context.Background(), []Op{
		{Plugin: "port", PluginInput: map[string]any{"port": 6379, "listening": true}},
	})
	if res[0].Status != TestFail {
		t.Errorf("expected fail, got %+v", res[0])
	}
}

func TestRunner_PortPlugin_ReachableSkipUnderImageTest(t *testing.T) {
	r, _ := newFakeRunner(t, RunModeBox)
	// Reachable attribute triggers host-side dial → skipped under box test.
	res := r.Run(context.Background(), []Op{
		{Plugin: "port", PluginInput: map[string]any{"port": 6379, "reachable": true}},
	})
	if res[0].Status != TestSkip {
		t.Errorf("expected skip, got %+v", res[0])
	}
}

// command verb — exit/stdout/stderr matchers.
func TestRunner_CommandVerb(t *testing.T) {
	t.Run("exit ok stdout equals", func(t *testing.T) {
		r, fake := newFakeRunner(t, RunModeLive)
		fake.responses = []fakeResponse{
			{matchPrefix: "redis-cli ping", stdout: "PONG\n", exit: 0},
		}
		res := r.Run(context.Background(), []Op{
			{Command: "redis-cli ping", Stdout: MatcherList{{Op: "equals", Value: "PONG"}}},
		})
		if res[0].Status != TestPass {
			t.Errorf("expected pass, got %+v", res[0])
		}
	})

	t.Run("stdout contains list", func(t *testing.T) {
		r, fake := newFakeRunner(t, RunModeLive)
		fake.responses = []fakeResponse{
			{matchPrefix: "status", stdout: "ready ok running", exit: 0},
		}
		res := r.Run(context.Background(), []Op{
			{Command: "status", Stdout: MatcherList{{Op: "contains", Value: []any{"ready", "ok"}}}},
		})
		if res[0].Status != TestPass {
			t.Errorf("expected pass, got %+v", res[0])
		}
	})

	t.Run("exit mismatch", func(t *testing.T) {
		r, fake := newFakeRunner(t, RunModeLive)
		fake.responses = []fakeResponse{
			{matchPrefix: "fail-cmd", exit: 2},
		}
		res := r.Run(context.Background(), []Op{{Command: "fail-cmd"}})
		if res[0].Status != TestFail || !strings.Contains(res[0].Message, "exit=2") {
			t.Errorf("expected exit failure, got %+v", res[0])
		}
	})

	t.Run("matches regex", func(t *testing.T) {
		r, fake := newFakeRunner(t, RunModeLive)
		fake.responses = []fakeResponse{
			{matchPrefix: "uptime", stdout: "load average: 0.12 0.34 0.56\n", exit: 0},
		}
		res := r.Run(context.Background(), []Op{
			{Command: "uptime", Stdout: MatcherList{{Op: "matches", Value: `load average: [\d.]+`}}},
		})
		if res[0].Status != TestPass {
			t.Errorf("expected pass, got %+v", res[0])
		}
	})
}

// http verb — status match, body contains, insecure flag propagation.
func TestRunner_HTTPVerb(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/ok":
			w.WriteHeader(200)
			fmt.Fprintln(w, "service is ready")
		case "/boom":
			w.WriteHeader(500)
		}
	}))
	defer srv.Close()

	t.Run("status + body contains", func(t *testing.T) {
		r, _ := newFakeRunner(t, RunModeLive)
		res := r.Run(context.Background(), []Op{
			{HTTP: srv.URL + "/ok", Status: 200, Body: MatcherList{{Op: "contains", Value: "ready"}}},
		})
		if res[0].Status != TestPass {
			t.Errorf("expected pass, got %+v", res[0])
		}
	})

	t.Run("status mismatch", func(t *testing.T) {
		r, _ := newFakeRunner(t, RunModeLive)
		res := r.Run(context.Background(), []Op{
			{HTTP: srv.URL + "/boom", Status: 200},
		})
		if res[0].Status != TestFail {
			t.Errorf("expected fail, got %+v", res[0])
		}
	})

	t.Run("custom timeout", func(t *testing.T) {
		slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			time.Sleep(100 * time.Millisecond)
			w.WriteHeader(200)
		}))
		defer slow.Close()
		r, _ := newFakeRunner(t, RunModeLive)
		res := r.Run(context.Background(), []Op{
			{HTTP: slow.URL, Status: 200, Timeout: "10ms"},
		})
		if res[0].Status != TestFail {
			t.Errorf("expected timeout failure, got %+v", res[0])
		}
	})
}

// Verifies the runner performs ${VAR} expansion before executing, and
// reports unresolved refs as skip.
func TestRunner_VariableExpansion(t *testing.T) {
	t.Run("expanded", func(t *testing.T) {
		fake := &fakeExecutor{}
		fake.responses = []fakeResponse{
			{matchPrefix: "redis-cli -p 16379", stdout: "PONG\n", exit: 0},
		}
		r := NewRunner(fake, &CheckVarResolver{Env: map[string]string{"HOST_PORT:6379": "16379"}}, RunModeLive)
		res := r.Run(context.Background(), []Op{
			{Command: "redis-cli -p ${HOST_PORT:6379}", Stdout: MatcherList{{Op: "equals", Value: "PONG"}}},
		})
		if res[0].Status != TestPass {
			t.Errorf("expected pass, got %+v. fake calls: %v", res[0], fake.calls)
		}
	})

	t.Run("unresolved → skip", func(t *testing.T) {
		r, _ := newFakeRunner(t, RunModeLive)
		res := r.Run(context.Background(), []Op{
			{Command: "redis-cli -p ${HOST_PORT:6379}"},
		})
		if res[0].Status != TestSkip || !strings.Contains(res[0].Message, "unresolved") {
			t.Errorf("expected skip with unresolved, got %+v", res[0])
		}
	})
}

// Skip:true short-circuits before any execution.
func TestRunner_SkipFlag(t *testing.T) {
	r, _ := newFakeRunner(t, RunModeLive)
	res := r.Run(context.Background(), []Op{{Command: "anything", Skip: true}})
	if res[0].Status != TestSkip {
		t.Errorf("expected skip, got %+v", res[0])
	}
}

// Zero-verb check fails with a clear error at runOne.
func TestRunner_EmptyCheck(t *testing.T) {
	r, _ := newFakeRunner(t, RunModeLive)
	res := r.Run(context.Background(), []Op{{}})
	if res[0].Status != TestFail || !strings.Contains(res[0].Message, "no verb") {
		t.Errorf("expected fail with 'no verb', got %+v", res[0])
	}
}

// Text formatter emits one line per result and a summary footer, returns
// the number of failures.
func TestFormatResultsText(t *testing.T) {
	results := []CheckResult{
		{Op: &Op{File: "/x"}, Verb: "file", Status: TestPass, Message: "ok"},
		{Op: &Op{Addr: "127.0.0.1:6379"}, Verb: "addr", Status: TestFail, Message: "not reachable", Elapsed: 5 * time.Millisecond},
		{Op: &Op{Command: "a"}, Verb: "command", Status: TestSkip, Message: "skipped"},
	}
	var buf bytes.Buffer
	fails := FormatResultsText(&buf, results)
	if fails != 1 {
		t.Errorf("fails = %d, want 1", fails)
	}
	out := buf.String()
	for _, want := range []string{"✓ file /x", "✗ addr 127.0.0.1:6379", "⚠ command a", "1 passed", "1 failed", "1 skipped"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n--\n%s", want, out)
		}
	}
}
