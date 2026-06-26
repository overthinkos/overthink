// Package httpverb is the importable, COMPILED-IN host-coupled `http` check verb: an
// HTTP request matched against status / body / headers — issued from the charly process
// (outside-in) under charly check live, or from inside the disposable container via
// `curl` under charly check box. It implements kit.CheckVerbProvider — RunVerb runs the
// request via the live kit.CheckContext (HTTPClient under live, Exec under box).
// Relocated out of charly's module (formerly charly/plugin/builtins/http +
// charly/plugin_http.go) onto the charly/plugin/kit contract; COMPILED-IN-ONLY. The
// matcher evaluation reuses the importable sdk.MatchAll + spec.MatcherList (R3).
package httpverb

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/overthinkos/overthink/candy/plugin-http/params"
	"github.com/overthinkos/overthink/charly/plugin/kit"
	"github.com/overthinkos/overthink/charly/plugin/sdk"
	"github.com/overthinkos/overthink/charly/spec"
)

//go:embed schema/*.cue
var SchemaFS embed.FS

// SchemaDir is the embedded schema directory; charly concatenates SchemaFS/SchemaDir.
const SchemaDir = "schema"

// InputDefs maps the provided capability to its CUE def for plugin_input validation.
var InputDefs = map[string]string{"verb:http": "#HttpInput"}

// NewCheckVerb returns the http verb as a kit.CheckVerbProvider for compiled-in registration.
func NewCheckVerb() kit.CheckVerbProvider { return verb{} }

type verb struct{}

func (verb) Reserved() string { return "http" }

// RunVerb decodes the typed plugin_input (params.HttpInput) and runs the request via the
// live CheckContext. gengotypes degrades the body/header matcher disjunction to `any`, so
// each is re-decoded through the shared matcher codec (spec.MatcherList.UnmarshalJSON) into
// the typed list sdk.MatchAll consumes. The SHARED method/request_body modifiers + the
// general timeout stay base #Op fields, read off op directly (mirrors the former r.runHTTP).
func (verb) RunVerb(ctx context.Context, cc kit.CheckContext, op *spec.Op) kit.Result {
	var in params.HttpInput
	kit.DecodeInput(op.PluginInput, &in)
	h := httpCheck{
		URL:           in.HTTP,
		Status:        in.Status,
		Body:          decodeMatcherList(in.Body),
		Headers:       decodeMatcherList(in.Headers),
		AllowInsecure: in.AllowInsecure,
		NoFollowRedir: in.NoFollowRedir,
		CAFile:        in.CAFile,
	}
	if cc.Mode() == kit.ModeBox {
		return runHTTPInContainer(ctx, cc, op, h)
	}
	return runHTTPFromHost(ctx, cc, op, h)
}

// httpCheck carries the http verb's plugin_input-decoded fields. The SHARED
// method/request_body modifiers and the general timeout stay base #Op fields, read off op.
type httpCheck struct {
	URL           string
	Status        int
	Body          spec.MatcherList
	Headers       spec.MatcherList
	AllowInsecure bool
	NoFollowRedir bool
	CAFile        string
}

func runHTTPFromHost(ctx context.Context, cc kit.CheckContext, op *spec.Op, h httpCheck) kit.Result {
	client, err := httpClientFor(op, h, cc.HTTPClient())
	if err != nil {
		return kit.Failf("http client: %v", err)
	}
	method := op.Method
	if method == "" {
		method = "GET"
	}
	var body io.Reader
	if op.RequestBody != "" {
		body = strings.NewReader(op.RequestBody)
	}
	req, err := http.NewRequestWithContext(ctx, method, h.URL, body)
	if err != nil {
		return kit.Failf("building request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return kit.Failf("request: %v", err)
	}
	defer resp.Body.Close()

	if h.Status != 0 && resp.StatusCode != h.Status {
		return kit.Failf("status=%d, want %d", resp.StatusCode, h.Status)
	}
	if len(h.Headers) > 0 {
		headerBlob := formatHeaders(resp.Header)
		if err := sdk.MatchAll(headerBlob, h.Headers); err != nil {
			return kit.Failf("headers: %v", err)
		}
	}
	if len(h.Body) > 0 {
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return kit.Failf("reading body: %v", err)
		}
		if err := sdk.MatchAll(string(bodyBytes), h.Body); err != nil {
			return kit.Failf("body: %v", err)
		}
	}
	return kit.Passf("status=%d", resp.StatusCode)
}

func runHTTPInContainer(ctx context.Context, cc kit.CheckContext, _ *spec.Op, h httpCheck) kit.Result {
	// In-container HTTP via curl. We only check status/body here; full header-matching
	// is host-side. Sufficient to validate the service inside the disposable container
	// answers.
	cmd := fmt.Sprintf("curl -sS -o /tmp/.charly-test-body -w '%%{http_code}' %s", kit.ShellQuote(h.URL))
	if h.AllowInsecure {
		cmd = "curl -sSk -o /tmp/.charly-test-body -w '%{http_code}' " + kit.ShellQuote(h.URL)
	}
	stdout, stderr, exit, err := cc.Exec().RunCapture(ctx, cmd)
	if err != nil || exit != 0 {
		return kit.Failf("curl exit %d err %v (%s)", exit, err, trimPreview(stderr))
	}
	code, convErr := strconv.Atoi(strings.TrimSpace(stdout))
	if convErr != nil {
		return kit.Failf("unexpected curl output: %q", stdout)
	}
	if h.Status != 0 && code != h.Status {
		return kit.Failf("status=%d, want %d", code, h.Status)
	}
	if len(h.Body) > 0 {
		body, _, exit, err := cc.Exec().RunCapture(ctx, "cat /tmp/.charly-test-body")
		if err != nil || exit != 0 {
			return kit.Failf("reading body: exit=%d err=%v", exit, err)
		}
		if err := sdk.MatchAll(body, h.Body); err != nil {
			return kit.Failf("body: %v", err)
		}
	}
	return kit.Passf("status=%d", code)
}

// httpClientFor builds a per-check http.Client honoring AllowInsecure, NoFollowRedir,
// CAFile (from the plugin_input, h) and Timeout (the general #Op step modifier, off op).
// Derives from the engine's base client so concurrent checks don't share TLS surprises.
func httpClientFor(op *spec.Op, h httpCheck, base *http.Client) (*http.Client, error) {
	client := &http.Client{Timeout: base.Timeout}
	if op.Timeout != "" {
		if d, err := time.ParseDuration(op.Timeout); err == nil {
			client.Timeout = d
		}
	}
	tr := &http.Transport{}
	if h.AllowInsecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	if h.CAFile != "" {
		pem, err := os.ReadFile(h.CAFile)
		if err != nil {
			return nil, fmt.Errorf("reading CA file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no certs parsed from %s", h.CAFile)
		}
		if tr.TLSClientConfig == nil {
			tr.TLSClientConfig = &tls.Config{}
		}
		tr.TLSClientConfig.RootCAs = pool
	}
	client.Transport = tr
	if h.NoFollowRedir {
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

// decodeMatcherList re-decodes a gengotypes-degraded matcher value (`any`) through the
// shared spec.MatcherList JSON codec. A nil / unparseable value yields a nil list.
func decodeMatcherList(v any) spec.MatcherList {
	if v == nil {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var ml spec.MatcherList
	if err := json.Unmarshal(raw, &ml); err != nil {
		return nil
	}
	return ml
}

// trimPreview is the shared kit helper (FU-11 — formerly duplicated in core + plugins).
var trimPreview = kit.TrimPreview
