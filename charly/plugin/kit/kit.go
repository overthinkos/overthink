// Package kit is the importable contract a HOST-COUPLED plugin candy implements to
// run IN-PROCESS against charly's live check engine — the seam that lets a check
// verb whose logic needs the running *Runner (exec-in-container, host TCP dial, the
// HTTP client) live in its own candy module instead of charly's module.
//
// A host-coupled verb candy implements CheckVerbProvider; charly wraps it in an
// in-proc adapter that passes its *Runner as a CheckContext. These candies are
// COMPILED-IN-ONLY for now — RunVerb takes a LIVE CheckContext that cannot cross a
// process boundary (the out-of-process kit, serving CheckContext over a reverse
// channel like the deploy executor, is a later cutover). This package imports only
// the stdlib + charly/spec (the generated param/Op types), so a candy module can
// import it without pulling charly's package main.
package kit

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/overthinkos/overthink/charly/spec"
)

// RunMode mirrors charly's RunMode: the mode a check runs under.
type RunMode int

const (
	// ModeLive — `charly check live`, against a running container/VM (in-container probes).
	ModeLive RunMode = iota
	// ModeBox — `charly check box`, against a disposable build container.
	ModeBox
)

// Executor is the subset of charly's DeployExecutor a check verb needs: run one
// command/script on the venue and capture stdout/stderr/exit separately. charly's
// DeployExecutor satisfies this structurally (RunCapture + Kind have identical
// signatures), so *Runner.Exec is passed straight through.
type Executor interface {
	// RunCapture runs a shell command/script on the venue, returning stdout,
	// stderr, the exit code, and any execution error (NOT a non-zero exit — that
	// is reported via the exit code). No root escalation; callers add sudo.
	RunCapture(ctx context.Context, script string) (stdout, stderr string, exit int, err error)
	// Kind classifies the venue: "host" | "container" | "image" | "vm".
	Kind() string
}

// CheckContext is the live check-engine surface a host-coupled verb's RunVerb
// consumes. charly's *Runner implements it; a candy reaches the running deployment
// through it without importing charly's package main.
type CheckContext interface {
	// Exec runs commands on the venue (in-container under ModeLive, in a disposable
	// container under ModeBox, or host-side depending on the executor).
	Exec() Executor
	// Mode is the run mode (Live vs Box).
	Mode() RunMode
	// HTTPClient is the engine's shared HTTP client (timeouts, redirect policy).
	HTTPClient() *http.Client
	// DialTimeout is the per-dial ceiling for host-side TCP reachability probes.
	DialTimeout() time.Duration
	// Box / Instance are the deployment's image + instance names (empty under ModeBox).
	Box() string
	Instance() string
	// Distros is the image's distro tag list (e.g. ["fedora:43","fedora"]) for
	// distro-specific package-name resolution.
	Distros() []string
}

// Status is a check verdict (mirrors charly's CheckStatus ordering).
type Status int

const (
	StatusPass Status = iota
	StatusFail
	StatusSkip
)

// Result is a host-coupled verb's verdict. charly converts it to its internal
// CheckResult (stamping the Op/Verb/timing) at the dispatch boundary.
type Result struct {
	Status        Status
	Message       string
	CapturedValue string // value stashed under `capture:` (recorded only on PASS)
}

// Pass / Fail / Skip are the verdict constructors a verb returns; the *f variants
// take a printf format (mirror charly's passf/failf/skipf).
func Pass(msg string) Result { return Result{Status: StatusPass, Message: msg} }
func Fail(msg string) Result { return Result{Status: StatusFail, Message: msg} }
func Skip(msg string) Result { return Result{Status: StatusSkip, Message: msg} }

func Passf(format string, a ...any) Result { return Pass(fmt.Sprintf(format, a...)) }
func Failf(format string, a ...any) Result { return Fail(fmt.Sprintf(format, a...)) }
func Skipf(format string, a ...any) Result { return Skip(fmt.Sprintf(format, a...)) }

// ShellQuote wraps s in single quotes for safe interpolation into a shell command
// (the importable analogue of charly's shellSingleQuote). Embedded single quotes are
// escaped as '\''.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// DecodeInput decodes an Op's plugin_input (map[string]any) into a candy's
// CUE-generated typed params struct via a JSON round-trip — the importable analogue
// of charly's decodePluginInput. A nil/empty input leaves out at its zero value;
// the host has already validated the input against the served schema.
func DecodeInput(in map[string]any, out any) {
	if len(in) == 0 {
		return
	}
	b, err := json.Marshal(in)
	if err != nil {
		return
	}
	_ = json.Unmarshal(b, out)
}

// CheckVerbProvider is the typed in-process contract a host-coupled check-verb candy
// implements. Reserved() is the verb word; RunVerb runs the probe against the live
// CheckContext and returns a Result. The authored plugin_input rides op.PluginInput
// (decode it into the candy's CUE-generated params struct).
type CheckVerbProvider interface {
	Reserved() string
	RunVerb(ctx context.Context, cc CheckContext, op *spec.Op) Result
}
