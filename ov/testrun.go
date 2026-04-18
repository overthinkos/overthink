package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// TestStatus is the outcome of a single Check.
type TestStatus int

const (
	TestPass TestStatus = iota
	TestFail
	TestSkip
)

func (s TestStatus) String() string {
	switch s {
	case TestPass:
		return "pass"
	case TestFail:
		return "fail"
	case TestSkip:
		return "skip"
	}
	return "unknown"
}

// TestResult captures the outcome of running a single Check.
type TestResult struct {
	Check   *Check
	Verb    string
	Status  TestStatus
	Message string
	Elapsed time.Duration
}

// RunMode selects routing rules for a Run() invocation.
//
//   - RunModeTest: ov test — against a running container. In-container
//     probes via Exec; host-side verbs (http/dns/addr) from the ov process.
//   - RunModeImageTest: ov image test — against a disposable container
//     (podman run --rm). All probes via Exec; host-side reachability is
//     not meaningful and those checks are skipped.
type RunMode int

const (
	RunModeTest RunMode = iota
	RunModeImageTest
)

// Executor runs a shell command against some target (running container,
// disposable container, or the host) and returns its output. Every verb
// that needs to inspect target state ultimately goes through Exec so
// tests can swap a fake executor in.
type Executor interface {
	Exec(ctx context.Context, cmd string) (stdout, stderr string, exit int, err error)
	Kind() string // "container", "image", "host" — for reporting
}

// ContainerExecutor runs via `<engine> exec <name> sh -c …` against a
// running container.
type ContainerExecutor struct {
	Engine, ContainerName string
}

func (c *ContainerExecutor) Kind() string { return "container" }

func (c *ContainerExecutor) Exec(ctx context.Context, cmd string) (string, string, int, error) {
	binary := EngineBinary(c.Engine)
	ecmd := exec.CommandContext(ctx, binary, "exec", c.ContainerName, "sh", "-c", cmd)
	return runCapture(ecmd)
}

// ImageExecutor runs via `<engine> run --rm <imageRef> sh -c …`. Each Exec
// call starts a fresh disposable container — slow but semantically clean
// for build-time validation of what the image contains.
type ImageExecutor struct {
	Engine, ImageRef string
}

func (i *ImageExecutor) Kind() string { return "image" }

func (i *ImageExecutor) Exec(ctx context.Context, cmd string) (string, string, int, error) {
	binary := EngineBinary(i.Engine)
	ecmd := exec.CommandContext(ctx, binary, "run", "--rm", "--entrypoint=", i.ImageRef, "sh", "-c", cmd)
	return runCapture(ecmd)
}

// runCapture executes cmd, returning stdout, stderr, exit status, and any
// non-exit error (e.g. the binary could not be started).
func runCapture(cmd *exec.Cmd) (string, string, int, error) {
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exit := 0
	if err != nil {
		var ee *exec.ExitError
		if asExitError(err, &ee) {
			exit = ee.ExitCode()
			return stdout.String(), stderr.String(), exit, nil // exit codes are not errors here
		}
		return stdout.String(), stderr.String(), -1, err
	}
	return stdout.String(), stderr.String(), exit, nil
}

// asExitError is a small helper to avoid an errors.As import dance in
// runCapture while still unwrapping through any wrap layers the stdlib adds.
func asExitError(err error, ee **exec.ExitError) bool {
	for err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			*ee = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// Runner wires together the execution context for one pass of checks.
type Runner struct {
	Exec        Executor
	Resolver    *TestVarResolver
	Mode        RunMode
	HTTPClient  *http.Client
	DialTimeout time.Duration
}

// NewRunner constructs a Runner with sensible defaults. Caller passes an
// Executor appropriate for the mode (ContainerExecutor for RunModeTest,
// ImageExecutor for RunModeImageTest).
func NewRunner(exec Executor, resolver *TestVarResolver, mode RunMode) *Runner {
	return &Runner{
		Exec:        exec,
		Resolver:    resolver,
		Mode:        mode,
		HTTPClient:  &http.Client{Timeout: 10 * time.Second},
		DialTimeout: 3 * time.Second,
	}
}

// Run executes the supplied checks sequentially and returns per-check
// results. Does not short-circuit on failure — the report should show
// every check's outcome for CI ergonomics.
func (r *Runner) Run(ctx context.Context, checks []Check) []TestResult {
	results := make([]TestResult, 0, len(checks))
	for i := range checks {
		results = append(results, r.runOne(ctx, &checks[i]))
	}
	return results
}

// runOne handles all the per-check housekeeping (verb resolution, skip
// handling, variable expansion, routing) and dispatches to a verb handler.
func (r *Runner) runOne(ctx context.Context, c *Check) TestResult {
	start := time.Now()
	kind, err := c.Kind()
	result := TestResult{Check: c, Verb: kind}
	if err != nil {
		result.Status = TestFail
		result.Message = err.Error()
		result.Elapsed = time.Since(start)
		return result
	}
	if c.Skip {
		result.Status = TestSkip
		result.Message = "skip: true"
		result.Elapsed = time.Since(start)
		return result
	}

	// Expand variables in-place on a copy so repeated runs over the same
	// check list don't accumulate substitutions.
	expanded := *c
	missing := expanded.ExpandVars(r.effectiveEnv())
	if len(missing) > 0 {
		result.Status = TestSkip
		result.Message = fmt.Sprintf("unresolved variables: %s", strings.Join(missing, ", "))
		result.Elapsed = time.Since(start)
		return result
	}

	switch kind {
	case "file":
		result = r.runFile(ctx, &expanded)
	case "port":
		result = r.runPort(ctx, &expanded)
	case "command":
		result = r.runCommand(ctx, &expanded)
	case "http":
		result = r.runHTTP(ctx, &expanded)
	case "package":
		result = r.runPackage(ctx, &expanded)
	case "service":
		result = r.runService(ctx, &expanded)
	case "process":
		result = r.runProcess(ctx, &expanded)
	case "dns":
		result = r.runDNS(ctx, &expanded)
	case "user":
		result = r.runUser(ctx, &expanded)
	case "group":
		result = r.runGroup(ctx, &expanded)
	case "interface":
		result = r.runInterface(ctx, &expanded)
	case "kernel-param":
		result = r.runKernelParam(ctx, &expanded)
	case "mount":
		result = r.runMount(ctx, &expanded)
	case "addr":
		result = r.runAddr(ctx, &expanded)
	case "matching":
		result = r.runMatching(ctx, &expanded)
	default:
		result.Status = TestSkip
		result.Message = fmt.Sprintf("unknown verb %q", kind)
	}
	result.Check = c
	result.Verb = kind
	result.Elapsed = time.Since(start)
	return result
}

func (r *Runner) effectiveEnv() map[string]string {
	if r.Resolver == nil {
		return nil
	}
	return r.Resolver.Env
}

// ---------------------------------------------------------------------------
// file verb
// ---------------------------------------------------------------------------

// runFile checks existence, mode, owner, group, filetype, sha256, and
// optional content matchers on a path inside the target.
func (r *Runner) runFile(ctx context.Context, c *Check) TestResult {
	path := c.File
	// Probe script emits a single line: exists|type|mode|owner|group|sha256
	// then (optionally) the file's contents on stdout following a marker.
	// `stat -c` portable fields: %F (type), %a (mode), %U (user), %G (group).
	probe := fmt.Sprintf(
		`if [ -e %[1]s ] || [ -L %[1]s ]; then
  printf "exists=1|"
  stat -c "%%F|%%a|%%U|%%G" %[1]s
else
  printf "exists=0|||||\n"
fi`, shellSingleQuote(path))
	stdout, stderr, exit, err := r.Exec.Exec(ctx, probe)
	if err != nil {
		return failf(c, "probe failed: %v (stderr: %s)", err, stderr)
	}
	if exit != 0 {
		return failf(c, "probe exit %d (stderr: %s)", exit, stderr)
	}
	line := strings.TrimSpace(stdout)
	// Expected: "exists=1|<type>|<mode>|<user>|<group>" OR "exists=0||||"
	parts := strings.SplitN(line, "|", 5)
	if len(parts) < 5 {
		return failf(c, "unexpected probe output: %q", line)
	}
	exists := strings.TrimPrefix(parts[0], "exists=") == "1"
	typeStr, mode, owner, group := parts[1], parts[2], parts[3], parts[4]

	// exists attribute (nil = default true)
	wantExists := true
	if c.Exists != nil {
		wantExists = *c.Exists
	}
	if wantExists != exists {
		return failf(c, "exists=%v, want %v", exists, wantExists)
	}
	if !exists {
		return passf(c, "file absent (as expected)")
	}
	if c.Mode != "" && strings.TrimLeft(mode, "0") != strings.TrimLeft(c.Mode, "0") {
		return failf(c, "mode=%s, want %s", mode, c.Mode)
	}
	if c.Owner != "" && owner != c.Owner {
		return failf(c, "owner=%s, want %s", owner, c.Owner)
	}
	if c.GroupOf != "" && group != c.GroupOf {
		return failf(c, "group=%s, want %s", group, c.GroupOf)
	}
	if c.Filetype != "" {
		ft := normalizeFiletype(typeStr)
		if ft != c.Filetype {
			return failf(c, "filetype=%s, want %s", ft, c.Filetype)
		}
	}

	// Content matchers: pull file contents and evaluate.
	if len(c.Contains) > 0 {
		contents, err := r.readFile(ctx, path)
		if err != nil {
			return failf(c, "read for contains: %v", err)
		}
		if err := matchAll(contents, c.Contains); err != nil {
			return failf(c, "contains: %v", err)
		}
	}
	if c.Sha256 != "" {
		out, _, exit, err := r.Exec.Exec(ctx, fmt.Sprintf("sha256sum %s", shellSingleQuote(path)))
		if err != nil || exit != 0 {
			return failf(c, "sha256 probe exit %d err %v", exit, err)
		}
		sum := strings.Fields(strings.TrimSpace(out))
		if len(sum) == 0 || sum[0] != c.Sha256 {
			return failf(c, "sha256=%s, want %s", sum, c.Sha256)
		}
	}

	return passf(c, "ok")
}

// readFile returns a file's contents from the target via Exec.
func (r *Runner) readFile(ctx context.Context, path string) (string, error) {
	out, stderr, exit, err := r.Exec.Exec(ctx, "cat "+shellSingleQuote(path))
	if err != nil {
		return "", err
	}
	if exit != 0 {
		return "", fmt.Errorf("cat exit %d: %s", exit, stderr)
	}
	return out, nil
}

// normalizeFiletype converts stat's %F verbose string into goss-parity short
// forms ("regular file" → "file", "directory" → "directory", "symbolic link"
// → "symlink").
func normalizeFiletype(s string) string {
	switch {
	case strings.Contains(s, "regular"):
		return "file"
	case strings.Contains(s, "directory"):
		return "directory"
	case strings.Contains(s, "symbolic link"), strings.Contains(s, "symlink"):
		return "symlink"
	case strings.Contains(s, "character"):
		return "character"
	case strings.Contains(s, "block"):
		return "block"
	case strings.Contains(s, "fifo"):
		return "fifo"
	case strings.Contains(s, "socket"):
		return "socket"
	}
	return s
}

// ---------------------------------------------------------------------------
// port verb
// ---------------------------------------------------------------------------

// runPort dispatches between in-container "listening" check and host-side
// reachability check based on attributes + run mode.
//
// Routing rules:
//   - listening: true (default when unset) → probe via Exec (container-internal)
//   - reachable/from-host semantics → dial 127.0.0.1:<HOST_PORT:N> from host
//     (only meaningful in RunModeTest; RunModeImageTest skips with reason)
func (r *Runner) runPort(ctx context.Context, c *Check) TestResult {
	wantListening := true
	if c.Listening != nil {
		wantListening = *c.Listening
	}

	// If the user asked for outside-in reachability (listening:false with a
	// HOST_PORT substitution already performed, or Reachable explicitly set),
	// dial from host.
	if c.Reachable != nil || (c.Listening != nil && !*c.Listening) {
		if r.Mode == RunModeImageTest {
			return skipf(c, "host-side port check not meaningful under ov image test")
		}
		return r.dialPort(c)
	}

	// In-container listening probe: ss first, netstat fallback.
	probe := fmt.Sprintf(
		`(ss -tlnH 2>/dev/null || netstat -tln 2>/dev/null) | awk '{print $4}' | grep -E ':%d$' >/dev/null`,
		c.Port)
	_, stderr, exit, err := r.Exec.Exec(ctx, probe)
	if err != nil {
		return failf(c, "probe failed: %v (%s)", err, stderr)
	}
	listening := exit == 0
	if listening != wantListening {
		return failf(c, "listening=%v, want %v (on port %d)", listening, wantListening, c.Port)
	}
	return passf(c, fmt.Sprintf("port %d listening=%v", c.Port, listening))
}

// dialPort attempts a TCP dial on 127.0.0.1:<port> from the host. Used for
// deploy-scope reachability checks where ${HOST_PORT:N} has been substituted
// into the Port field. If the Port was remapped by deploy.yml, the substituted
// value is what we'll dial.
func (r *Runner) dialPort(c *Check) TestResult {
	addr := fmt.Sprintf("127.0.0.1:%d", c.Port)
	if c.IP != "" {
		addr = fmt.Sprintf("%s:%d", c.IP, c.Port)
	}
	conn, err := net.DialTimeout("tcp", addr, r.DialTimeout)
	wantReachable := true
	if c.Reachable != nil {
		wantReachable = *c.Reachable
	}
	if err != nil {
		if !wantReachable {
			return passf(c, fmt.Sprintf("%s unreachable (as expected)", addr))
		}
		return failf(c, "dial %s: %v", addr, err)
	}
	_ = conn.Close()
	if !wantReachable {
		return failf(c, "%s reachable but wanted unreachable", addr)
	}
	return passf(c, fmt.Sprintf("%s reachable", addr))
}

// ---------------------------------------------------------------------------
// command verb
// ---------------------------------------------------------------------------

// runCommand runs the command (in-container by default, from-host if
// InContainer=false or FromHost=true) and matches against Exit/Stdout/Stderr.
func (r *Runner) runCommand(ctx context.Context, c *Check) TestResult {
	inContainer := true
	if c.InContainer != nil {
		inContainer = *c.InContainer
	}
	if c.FromHost {
		inContainer = false
	}

	var stdout, stderr string
	var exit int
	var err error
	if inContainer {
		stdout, stderr, exit, err = r.Exec.Exec(ctx, c.Command)
	} else {
		if r.Mode == RunModeImageTest {
			return skipf(c, "host-side command not meaningful under ov image test")
		}
		cmd := exec.CommandContext(ctx, "sh", "-c", c.Command)
		stdout, stderr, exit, err = runCapture(cmd)
	}
	if err != nil {
		return failf(c, "execution error: %v", err)
	}

	wantExit := 0
	if c.ExitStatus != nil {
		wantExit = *c.ExitStatus
	}
	if exit != wantExit {
		return failf(c, "exit=%d, want %d (stderr: %s)", exit, wantExit, trimPreview(stderr))
	}
	if err := matchAll(stdout, c.Stdout); err != nil {
		return failf(c, "stdout: %v (got: %s)", err, trimPreview(stdout))
	}
	if err := matchAll(stderr, c.Stderr); err != nil {
		return failf(c, "stderr: %v (got: %s)", err, trimPreview(stderr))
	}
	return passf(c, fmt.Sprintf("exit=%d", exit))
}

// ---------------------------------------------------------------------------
// http verb
// ---------------------------------------------------------------------------

// runHTTP performs an HTTP request against the URL and matches the response
// against Status/Body/Headers.
//
// Under RunModeTest the request goes from the ov process (outside-in
// reachability). Under RunModeImageTest the request is issued from inside
// the disposable container via curl (the container may have no network
// reachability from the host, so host-side is wrong there).
func (r *Runner) runHTTP(ctx context.Context, c *Check) TestResult {
	if r.Mode == RunModeImageTest {
		return r.runHTTPInContainer(ctx, c)
	}
	return r.runHTTPFromHost(ctx, c)
}

func (r *Runner) runHTTPFromHost(ctx context.Context, c *Check) TestResult {
	client, err := httpClientFor(c, r.HTTPClient)
	if err != nil {
		return failf(c, "http client: %v", err)
	}
	method := c.Method
	if method == "" {
		method = "GET"
	}
	var body io.Reader
	if c.RequestBody != "" {
		body = strings.NewReader(c.RequestBody)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.HTTP, body)
	if err != nil {
		return failf(c, "building request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return failf(c, "request: %v", err)
	}
	defer resp.Body.Close()

	if c.Status != 0 && resp.StatusCode != c.Status {
		return failf(c, "status=%d, want %d", resp.StatusCode, c.Status)
	}
	if len(c.Headers) > 0 {
		headerBlob := formatHeaders(resp.Header)
		if err := matchAll(headerBlob, c.Headers); err != nil {
			return failf(c, "headers: %v", err)
		}
	}
	if len(c.Body) > 0 {
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return failf(c, "reading body: %v", err)
		}
		if err := matchAll(string(bodyBytes), c.Body); err != nil {
			return failf(c, "body: %v", err)
		}
	}
	return passf(c, fmt.Sprintf("status=%d", resp.StatusCode))
}

func (r *Runner) runHTTPInContainer(ctx context.Context, c *Check) TestResult {
	// In-container HTTP via curl. We only check status/body here; full
	// header-matching is implemented host-side. For Phase 3 this is
	// sufficient for validating that the service inside the disposable
	// container answers.
	cmd := fmt.Sprintf("curl -sS -o /tmp/.ov-test-body -w '%%{http_code}' %s", shellSingleQuote(c.HTTP))
	if c.AllowInsecure {
		cmd = "curl -sSk -o /tmp/.ov-test-body -w '%{http_code}' " + shellSingleQuote(c.HTTP)
	}
	stdout, stderr, exit, err := r.Exec.Exec(ctx, cmd)
	if err != nil || exit != 0 {
		return failf(c, "curl exit %d err %v (%s)", exit, err, trimPreview(stderr))
	}
	code, convErr := strconv.Atoi(strings.TrimSpace(stdout))
	if convErr != nil {
		return failf(c, "unexpected curl output: %q", stdout)
	}
	if c.Status != 0 && code != c.Status {
		return failf(c, "status=%d, want %d", code, c.Status)
	}
	if len(c.Body) > 0 {
		body, _, exit, err := r.Exec.Exec(ctx, "cat /tmp/.ov-test-body")
		if err != nil || exit != 0 {
			return failf(c, "reading body: exit=%d err=%v", exit, err)
		}
		if err := matchAll(body, c.Body); err != nil {
			return failf(c, "body: %v", err)
		}
	}
	return passf(c, fmt.Sprintf("status=%d", code))
}

// httpClientFor builds a per-check http.Client honoring AllowInsecure,
// NoFollowRedir, CAFile, and Timeout. Derives from the runner's base client
// so concurrent checks don't share TLS state surprises.
func httpClientFor(c *Check, base *http.Client) (*http.Client, error) {
	client := &http.Client{Timeout: base.Timeout}
	if c.Timeout != "" {
		if d, err := time.ParseDuration(c.Timeout); err == nil {
			client.Timeout = d
		}
	}
	tr := &http.Transport{}
	if c.AllowInsecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	if c.CAFile != "" {
		pem, err := os.ReadFile(c.CAFile)
		if err != nil {
			return nil, fmt.Errorf("reading CA file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no certs parsed from %s", c.CAFile)
		}
		if tr.TLSClientConfig == nil {
			tr.TLSClientConfig = &tls.Config{}
		}
		tr.TLSClientConfig.RootCAs = pool
	}
	client.Transport = tr
	if c.NoFollowRedir {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	return client, nil
}

func formatHeaders(h http.Header) string {
	var b strings.Builder
	for k, vs := range h {
		for _, v := range vs {
			b.WriteString(k)
			b.WriteString(": ")
			b.WriteString(v)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Matcher evaluation
// ---------------------------------------------------------------------------

// matchAll returns nil if every matcher succeeds against the value. The first
// failure wins (reports the specific unmet expectation).
func matchAll(value string, matchers MatcherList) error {
	for _, m := range matchers {
		if err := matchOne(value, m); err != nil {
			return err
		}
	}
	return nil
}

// matchOne evaluates a single matcher. The operator set here must stay in
// lockstep with validMatcherOps in validate_tests.go — if the validator
// accepts an op, the runner must handle it.
func matchOne(value string, m Matcher) error {
	switch m.Op {
	case "equals":
		want := matchValueString(m.Value)
		if strings.TrimRight(value, "\r\n") != want {
			return fmt.Errorf("expected exactly %q", want)
		}
	case "not_equals":
		want := matchValueString(m.Value)
		if strings.TrimRight(value, "\r\n") == want {
			return fmt.Errorf("expected NOT to equal %q", want)
		}
	case "contains":
		for _, want := range matchValueStrings(m.Value) {
			if !strings.Contains(value, want) {
				return fmt.Errorf("expected to contain %q", want)
			}
		}
	case "not_contains":
		for _, want := range matchValueStrings(m.Value) {
			if strings.Contains(value, want) {
				return fmt.Errorf("expected NOT to contain %q", want)
			}
		}
	case "matches":
		re, err := regexp.Compile(matchValueString(m.Value))
		if err != nil {
			return fmt.Errorf("bad regex %v: %w", m.Value, err)
		}
		if !re.MatchString(value) {
			return fmt.Errorf("expected to match /%s/", re.String())
		}
	case "not_matches":
		re, err := regexp.Compile(matchValueString(m.Value))
		if err != nil {
			return fmt.Errorf("bad regex %v: %w", m.Value, err)
		}
		if re.MatchString(value) {
			return fmt.Errorf("expected NOT to match /%s/", re.String())
		}
	case "lt", "le", "gt", "ge":
		return matchNumeric(value, m)
	default:
		return fmt.Errorf("unsupported matcher op %q", m.Op)
	}
	return nil
}

// matchNumeric compares both sides as float64. Used for HTTP status codes,
// kernel-param integers, port counts — anywhere an ordering-aware matcher
// makes sense. String values with leading/trailing whitespace (like
// `sysctl -n` output) are trimmed before parsing.
func matchNumeric(value string, m Matcher) error {
	wantStr := matchValueString(m.Value)
	want, err := strconv.ParseFloat(strings.TrimSpace(wantStr), 64)
	if err != nil {
		return fmt.Errorf("%s: operand %q not numeric: %w", m.Op, wantStr, err)
	}
	got, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return fmt.Errorf("%s: observed %q not numeric: %w", m.Op, value, err)
	}
	var ok bool
	switch m.Op {
	case "lt":
		ok = got < want
	case "le":
		ok = got <= want
	case "gt":
		ok = got > want
	case "ge":
		ok = got >= want
	}
	if !ok {
		return fmt.Errorf("expected %s %v (got %v)", m.Op, want, got)
	}
	return nil
}

// matchValueString coerces a matcher's stored Value (any) to a string. For
// numeric types it renders canonically; for everything else it falls back
// to fmt.Sprint.
func matchValueString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	case nil:
		return ""
	}
	return fmt.Sprint(v)
}

// matchValueStrings handles list-valued matchers like {contains: [a, b]}.
// A scalar value becomes a singleton list.
func matchValueStrings(v any) []string {
	if list, ok := v.([]any); ok {
		out := make([]string, 0, len(list))
		for _, e := range list {
			out = append(out, matchValueString(e))
		}
		return out
	}
	return []string{matchValueString(v)}
}

// ---------------------------------------------------------------------------
// Result helpers
// ---------------------------------------------------------------------------

func passf(c *Check, msg string) TestResult {
	return TestResult{Check: c, Status: TestPass, Message: msg}
}

func failf(c *Check, format string, args ...any) TestResult {
	return TestResult{Check: c, Status: TestFail, Message: fmt.Sprintf(format, args...)}
}

func skipf(c *Check, msg string) TestResult {
	return TestResult{Check: c, Status: TestSkip, Message: msg}
}

func trimPreview(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

// ---------------------------------------------------------------------------
// Report rendering (text format — json/tap come in Phase 4)
// ---------------------------------------------------------------------------

// FormatResultsText writes a human-readable summary of results to w and
// returns the number of failures.
func FormatResultsText(w io.Writer, results []TestResult) int {
	passes, fails, skips := 0, 0, 0
	for _, r := range results {
		glyph := "?"
		switch r.Status {
		case TestPass:
			glyph = "✓"
			passes++
		case TestFail:
			glyph = "✗"
			fails++
		case TestSkip:
			glyph = "⚠"
			skips++
		}
		verb := r.Verb
		subject := firstNonEmpty(r.Check.File, r.Check.HTTP, r.Check.Command, r.Check.DNS, r.Check.Addr)
		if r.Check.Port != 0 {
			subject = fmt.Sprintf("%d", r.Check.Port)
		}
		fmt.Fprintf(w, "%s %s %s — %s\n", glyph, verb, subject, r.Message)
		if r.Check.Origin != "" && r.Status == TestFail {
			fmt.Fprintf(w, "  from %s\n", r.Check.Origin)
		}
	}
	fmt.Fprintf(w, "%d passed · %d failed · %d skipped\n", passes, fails, skips)
	return fails
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// FormatResultsJSON emits a structured report suitable for CI consumption.
// Returns the number of failures.
func FormatResultsJSON(w io.Writer, results []TestResult) int {
	type entry struct {
		Verb    string `json:"verb"`
		Status  string `json:"status"`
		Origin  string `json:"origin,omitempty"`
		Subject string `json:"subject,omitempty"`
		Message string `json:"message,omitempty"`
	}
	out := make([]entry, 0, len(results))
	fails := 0
	for _, r := range results {
		subject := firstNonEmpty(r.Check.File, r.Check.HTTP, r.Check.Command, r.Check.DNS, r.Check.Addr)
		if r.Check.Port != 0 {
			subject = fmt.Sprintf("%d", r.Check.Port)
		}
		if r.Status == TestFail {
			fails++
		}
		out = append(out, entry{
			Verb:    r.Verb,
			Status:  r.Status.String(),
			Origin:  r.Check.Origin,
			Subject: subject,
			Message: r.Message,
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
	return fails
}

// FormatResultsTAP emits TAP v13. Returns the number of failures.
func FormatResultsTAP(w io.Writer, results []TestResult) int {
	fails := 0
	fmt.Fprintf(w, "TAP version 13\n1..%d\n", len(results))
	for i, r := range results {
		subject := firstNonEmpty(r.Check.File, r.Check.HTTP, r.Check.Command, r.Check.DNS, r.Check.Addr)
		if r.Check.Port != 0 {
			subject = fmt.Sprintf("%d", r.Check.Port)
		}
		label := fmt.Sprintf("%s %s - %s", r.Verb, subject, r.Message)
		switch r.Status {
		case TestPass:
			fmt.Fprintf(w, "ok %d - %s\n", i+1, label)
		case TestSkip:
			fmt.Fprintf(w, "ok %d - %s # SKIP %s\n", i+1, label, r.Message)
		case TestFail:
			fails++
			fmt.Fprintf(w, "not ok %d - %s\n", i+1, label)
		}
	}
	return fails
}
