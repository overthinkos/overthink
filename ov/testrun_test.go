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

func (f *fakeExecutor) Exec(ctx context.Context, cmd string) (string, string, int, error) {
	f.calls = append(f.calls, cmd)
	for _, r := range f.responses {
		if strings.HasPrefix(cmd, r.matchPrefix) || strings.Contains(cmd, r.matchPrefix) {
			return r.stdout, r.stderr, r.exit, r.err
		}
	}
	return "", "no fake response registered for: " + cmd, 127, nil
}

func (f *fakeExecutor) Kind() string { return "fake" }

func newFakeRunner(t *testing.T, mode RunMode) (*Runner, *fakeExecutor) {
	t.Helper()
	fake := &fakeExecutor{}
	r := NewRunner(fake, &TestVarResolver{Env: map[string]string{}}, mode)
	return r, fake
}

// file verb — exists pass, mode mismatch, filetype check, missing file.
func TestRunner_FileVerb(t *testing.T) {
	t.Run("exists true, mode ok", func(t *testing.T) {
		r, fake := newFakeRunner(t, RunModeTest)
		fake.responses = []fakeResponse{
			{matchPrefix: "if [ -e", stdout: "exists=1|regular file|755|root|root\n"},
		}
		results := r.Run(context.Background(), []Check{
			{File: "/usr/bin/redis-server", Exists: ptrBool(true), Mode: "0755", Filetype: "file"},
		})
		if len(results) != 1 || results[0].Status != TestPass {
			t.Errorf("expected pass, got %+v", results[0])
		}
	})

	t.Run("mode mismatch", func(t *testing.T) {
		r, fake := newFakeRunner(t, RunModeTest)
		fake.responses = []fakeResponse{
			{matchPrefix: "if [ -e", stdout: "exists=1|regular file|755|root|root\n"},
		}
		results := r.Run(context.Background(), []Check{
			{File: "/x", Mode: "0644"},
		})
		if results[0].Status != TestFail || !strings.Contains(results[0].Message, "mode") {
			t.Errorf("expected mode failure, got %+v", results[0])
		}
	})

	t.Run("absent as expected", func(t *testing.T) {
		r, fake := newFakeRunner(t, RunModeTest)
		fake.responses = []fakeResponse{
			{matchPrefix: "if [ -e", stdout: "exists=0||||\n"},
		}
		results := r.Run(context.Background(), []Check{
			{File: "/nope", Exists: ptrBool(false)},
		})
		if results[0].Status != TestPass {
			t.Errorf("expected pass for absent-as-expected, got %+v", results[0])
		}
	})

	t.Run("exists false but file present", func(t *testing.T) {
		r, fake := newFakeRunner(t, RunModeTest)
		fake.responses = []fakeResponse{
			{matchPrefix: "if [ -e", stdout: "exists=1|regular file|644|root|root\n"},
		}
		results := r.Run(context.Background(), []Check{
			{File: "/x", Exists: ptrBool(false)},
		})
		if results[0].Status != TestFail {
			t.Errorf("expected fail, got %+v", results[0])
		}
	})
}

// port verb — listening via fake ss output, unreachable via dial timeout,
// host-side dial skip under image-test mode.
func TestRunner_PortVerb_Listening(t *testing.T) {
	r, fake := newFakeRunner(t, RunModeTest)
	fake.responses = []fakeResponse{
		{matchPrefix: "(ss -tlnH", exit: 0},
	}
	res := r.Run(context.Background(), []Check{{Port: 6379, Listening: ptrBool(true)}})
	if res[0].Status != TestPass {
		t.Errorf("expected pass, got %+v", res[0])
	}
}

func TestRunner_PortVerb_NotListening(t *testing.T) {
	r, fake := newFakeRunner(t, RunModeTest)
	fake.responses = []fakeResponse{
		{matchPrefix: "(ss -tlnH", exit: 1},
	}
	res := r.Run(context.Background(), []Check{{Port: 6379, Listening: ptrBool(true)}})
	if res[0].Status != TestFail {
		t.Errorf("expected fail, got %+v", res[0])
	}
}

func TestRunner_PortVerb_ReachableSkipUnderImageTest(t *testing.T) {
	r, _ := newFakeRunner(t, RunModeImageTest)
	// Reachable attribute triggers host-side dial → skipped under image test.
	res := r.Run(context.Background(), []Check{{Port: 6379, Reachable: ptrBool(true)}})
	if res[0].Status != TestSkip {
		t.Errorf("expected skip, got %+v", res[0])
	}
}

// command verb — exit/stdout/stderr matchers.
func TestRunner_CommandVerb(t *testing.T) {
	t.Run("exit ok stdout equals", func(t *testing.T) {
		r, fake := newFakeRunner(t, RunModeTest)
		fake.responses = []fakeResponse{
			{matchPrefix: "redis-cli ping", stdout: "PONG\n", exit: 0},
		}
		res := r.Run(context.Background(), []Check{
			{Command: "redis-cli ping", Stdout: MatcherList{{Op: "equals", Value: "PONG"}}},
		})
		if res[0].Status != TestPass {
			t.Errorf("expected pass, got %+v", res[0])
		}
	})

	t.Run("stdout contains list", func(t *testing.T) {
		r, fake := newFakeRunner(t, RunModeTest)
		fake.responses = []fakeResponse{
			{matchPrefix: "status", stdout: "ready ok running", exit: 0},
		}
		res := r.Run(context.Background(), []Check{
			{Command: "status", Stdout: MatcherList{{Op: "contains", Value: []any{"ready", "ok"}}}},
		})
		if res[0].Status != TestPass {
			t.Errorf("expected pass, got %+v", res[0])
		}
	})

	t.Run("exit mismatch", func(t *testing.T) {
		r, fake := newFakeRunner(t, RunModeTest)
		fake.responses = []fakeResponse{
			{matchPrefix: "fail-cmd", exit: 2},
		}
		res := r.Run(context.Background(), []Check{{Command: "fail-cmd"}})
		if res[0].Status != TestFail || !strings.Contains(res[0].Message, "exit=2") {
			t.Errorf("expected exit failure, got %+v", res[0])
		}
	})

	t.Run("matches regex", func(t *testing.T) {
		r, fake := newFakeRunner(t, RunModeTest)
		fake.responses = []fakeResponse{
			{matchPrefix: "uptime", stdout: "load average: 0.12 0.34 0.56\n", exit: 0},
		}
		res := r.Run(context.Background(), []Check{
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
		r, _ := newFakeRunner(t, RunModeTest)
		res := r.Run(context.Background(), []Check{
			{HTTP: srv.URL + "/ok", Status: 200, Body: MatcherList{{Op: "contains", Value: "ready"}}},
		})
		if res[0].Status != TestPass {
			t.Errorf("expected pass, got %+v", res[0])
		}
	})

	t.Run("status mismatch", func(t *testing.T) {
		r, _ := newFakeRunner(t, RunModeTest)
		res := r.Run(context.Background(), []Check{
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
		r, _ := newFakeRunner(t, RunModeTest)
		res := r.Run(context.Background(), []Check{
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
		r := NewRunner(fake, &TestVarResolver{Env: map[string]string{"HOST_PORT:6379": "16379"}}, RunModeTest)
		res := r.Run(context.Background(), []Check{
			{Command: "redis-cli -p ${HOST_PORT:6379}", Stdout: MatcherList{{Op: "equals", Value: "PONG"}}},
		})
		if res[0].Status != TestPass {
			t.Errorf("expected pass, got %+v. fake calls: %v", res[0], fake.calls)
		}
	})

	t.Run("unresolved → skip", func(t *testing.T) {
		r, _ := newFakeRunner(t, RunModeTest)
		res := r.Run(context.Background(), []Check{
			{Command: "redis-cli -p ${HOST_PORT:6379}"},
		})
		if res[0].Status != TestSkip || !strings.Contains(res[0].Message, "unresolved") {
			t.Errorf("expected skip with unresolved, got %+v", res[0])
		}
	})
}

// Skip:true short-circuits before any execution.
func TestRunner_SkipFlag(t *testing.T) {
	r, _ := newFakeRunner(t, RunModeTest)
	res := r.Run(context.Background(), []Check{{Command: "anything", Skip: true}})
	if res[0].Status != TestSkip {
		t.Errorf("expected skip, got %+v", res[0])
	}
}

// Zero-verb check fails with a clear error at runOne.
func TestRunner_EmptyCheck(t *testing.T) {
	r, _ := newFakeRunner(t, RunModeTest)
	res := r.Run(context.Background(), []Check{{}})
	if res[0].Status != TestFail || !strings.Contains(res[0].Message, "no verb") {
		t.Errorf("expected fail with 'no verb', got %+v", res[0])
	}
}

// Text formatter emits one line per result and a summary footer, returns
// the number of failures.
func TestFormatResultsText(t *testing.T) {
	results := []TestResult{
		{Check: &Check{File: "/x"}, Verb: "file", Status: TestPass, Message: "ok"},
		{Check: &Check{Port: 6379}, Verb: "port", Status: TestFail, Message: "not listening", Elapsed: 5 * time.Millisecond},
		{Check: &Check{Command: "a"}, Verb: "command", Status: TestSkip, Message: "skipped"},
	}
	var buf bytes.Buffer
	fails := FormatResultsText(&buf, results)
	if fails != 1 {
		t.Errorf("fails = %d, want 1", fails)
	}
	out := buf.String()
	for _, want := range []string{"✓ file /x", "✗ port 6379", "⚠ command a", "1 passed", "1 failed", "1 skipped"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n--\n%s", want, out)
		}
	}
}

func ptrBool(b bool) *bool { return &b }

// Sync guard: every operator listed as valid by the validator must be
// implemented by matchOne. A new allow-listed op without a runner branch
// would crash at runtime; this test fails at compile time of the allowlist
// instead.
func TestMatcher_AllowlistRunnerSync(t *testing.T) {
	for op := range validMatcherOps {
		err := matchOne("x", Matcher{Op: op, Value: "x"})
		// Either a clean result or a domain-specific error is fine; an
		// "unsupported matcher op" error means matchOne is missing a case.
		if err != nil && strings.Contains(err.Error(), "unsupported matcher op") {
			t.Errorf("validator allows op %q but runner has no implementation", op)
		}
	}
}

// Verifies every matcher operator in validMatcherOps has a runner path —
// guards against the earlier regression where lt/le/gt/ge and not_equals
// were declared valid by the validator but crashed at runtime.
func TestMatcher_AllOperators(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		matcher Matcher
		wantErr bool
	}{
		{"equals pass", "hello", Matcher{Op: "equals", Value: "hello"}, false},
		{"equals fail", "hello", Matcher{Op: "equals", Value: "world"}, true},
		{"not_equals pass", "hello", Matcher{Op: "not_equals", Value: "world"}, false},
		{"not_equals fail", "hello", Matcher{Op: "not_equals", Value: "hello"}, true},
		{"contains pass", "hello world", Matcher{Op: "contains", Value: "world"}, false},
		{"contains fail", "hello world", Matcher{Op: "contains", Value: "xyz"}, true},
		{"not_contains pass", "hello", Matcher{Op: "not_contains", Value: "xyz"}, false},
		{"not_contains fail", "hello", Matcher{Op: "not_contains", Value: "ell"}, true},
		{"matches pass", "abc123", Matcher{Op: "matches", Value: `\d+`}, false},
		{"matches fail", "abc", Matcher{Op: "matches", Value: `\d+`}, true},
		{"not_matches pass", "abc", Matcher{Op: "not_matches", Value: `\d+`}, false},
		{"not_matches fail", "abc123", Matcher{Op: "not_matches", Value: `\d+`}, true},
		{"lt pass", "5", Matcher{Op: "lt", Value: "10"}, false},
		{"lt fail", "10", Matcher{Op: "lt", Value: "5"}, true},
		{"le pass equal", "10", Matcher{Op: "le", Value: "10"}, false},
		{"gt pass", "10", Matcher{Op: "gt", Value: "5"}, false},
		{"ge pass equal", "5", Matcher{Op: "ge", Value: "5"}, false},
		{"lt non-numeric observed", "x", Matcher{Op: "lt", Value: "10"}, true},
		{"lt non-numeric want", "5", Matcher{Op: "lt", Value: "nope"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := matchOne(tc.value, tc.matcher)
			if tc.wantErr && err == nil {
				t.Errorf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
