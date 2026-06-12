package main

// eval_feature_grader.go — the Agent Driven Evaluation (ADE) agent grader.
//
// ADE binds each Gherkin scenario step to a verifier BY SHAPE: a step that
// embeds a check verb (file/http/cdp/mcp/command/…) is graded
// DETERMINISTICALLY by the runner; a prose-only step (a given/when/then with
// no verb) binds to an AGENT — this grader. The grader spawns the configured
// `kind: agent` CLI on the host, hands it the scenario goal + the step's prose +
// the live target handle, lets it probe the running deployment with the full
// `charly eval` surface, and parses back a structured pass/fail verdict.
//
// Bounded by construction: ONE agent invocation per prose step (never the
// plateau loop), wall-clock-capped, and an unparseable / failed / timed-out
// grader is a FAIL with evidence — never a silent pass. The grader is wired
// only by `charly eval feature run <deployment>` (a live deployment the agent can
// reach); `charly box feature run` (disposable, no stable target) leaves it nil so
// prose steps stay advisory-skip, and the harness loop / `charly eval recipe`
// never set it.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// GraderDefaultTimeout bounds a single grader invocation when neither the
// `charly eval feature run --timeout` flag nor the AI entry's own `timeout:` is
// set. Unlike the plateau-bounded harness loop, a grader call MUST be
// wall-clock-bounded so one stuck prose step can't hang an acceptance run.
const GraderDefaultTimeout = 5 * time.Minute

// GraderRequest is the context handed to a StepGrader for one prose step.
// Feature/Narrative are the scenario's description goal; Scenario is the
// scenario name; Keyword/Text are the step's Gherkin keyword and prose.
type GraderRequest struct {
	Feature   string
	Narrative string
	Scenario  string
	Keyword   string
	Text      string
}

// StepGrader judges a prose-only scenario step (one with no embedded check
// verb). Implementations return an EvalResult with Status TestPass/TestFail
// and a Message carrying the grader's evidence. A grader that cannot reach a
// verdict (launch failure, timeout, unparseable output) returns TestFail —
// never a silent pass.
type StepGrader interface {
	Grade(ctx context.Context, req GraderRequest) EvalResult
}

// AgentGrader is the production StepGrader: it drives the configured
// `kind: agent` CLI against a live deployment.
type AgentGrader struct {
	Agent    *AgentConfig // the resolved kind:agent entry (how to launch the CLI)
	Target   string       // the deployment name the agent probes (e.g. "eval-pod")
	Instance string       // optional deploy instance
	Timeout  string       // optional Go-duration override (from --timeout)
}

// Grade builds the grader prompt, runs the AI once, and parses its verdict.
func (g *AgentGrader) Grade(ctx context.Context, req GraderRequest) EvalResult {
	if g == nil || g.Agent == nil {
		return EvalResult{Status: TestFail, Verb: "agent", Message: "agent grader misconfigured (no ai)"}
	}
	prompt := buildGraderPrompt(req, g.Target, g.Instance)

	timeout := GraderDefaultTimeout
	src := strings.TrimSpace(g.Timeout)
	if src == "" {
		src = strings.TrimSpace(g.Agent.Timeout)
	}
	if src != "" {
		if d, err := time.ParseDuration(src); err == nil && d > 0 {
			timeout = d
		}
	}

	started := time.Now()
	stdout, stderr, err := RunAgentOnce(ctx, g.Agent, prompt, timeout)
	elapsed := time.Since(started)
	if err != nil {
		msg := fmt.Sprintf("agent grader launch failed: %v", err)
		if s := strings.TrimSpace(stderr); s != "" {
			msg += " — " + lastLines(s, 2)
		}
		return EvalResult{Status: TestFail, Verb: "agent", Message: msg, Elapsed: elapsed}
	}

	pass, evidence, ok := parseVerdict(stdout)
	if !ok {
		return EvalResult{
			Status:  TestFail,
			Verb:    "agent",
			Message: "agent grader returned no parseable verdict: " + lastLines(stdout, 2),
			Elapsed: elapsed,
		}
	}
	status := TestFail
	if pass {
		status = TestPass
	}
	return EvalResult{
		Status:  status,
		Verb:    "agent",
		Message: "agent: " + evidence,
		Elapsed: elapsed,
	}
}

// buildGraderPrompt renders the instruction handed to the grading agent.
// It states the goal, the exact behaviour to verify, the live target to
// probe, the tools available, and the strict single-line JSON verdict
// contract the parser expects back.
func buildGraderPrompt(req GraderRequest, target, instance string) string {
	var b strings.Builder
	b.WriteString("You are an acceptance-test grader for Agent Driven Evaluation. ")
	b.WriteString("Decide, from real evidence, whether ONE behaviour holds on a running deployment.\n\n")
	if strings.TrimSpace(req.Feature) != "" {
		b.WriteString("Feature (the goal): " + req.Feature + "\n")
	}
	if strings.TrimSpace(req.Narrative) != "" {
		b.WriteString("Narrative:\n" + req.Narrative + "\n")
	}
	if strings.TrimSpace(req.Scenario) != "" {
		b.WriteString("Scenario: " + req.Scenario + "\n")
	}
	kw := strings.TrimSpace(req.Keyword)
	if kw == "" {
		kw = "Then"
	}
	b.WriteString("\nBehaviour to verify — " + kw + ": " + req.Text + "\n\n")
	b.WriteString("The deployment under test is named '" + target + "'")
	if strings.TrimSpace(instance) != "" {
		b.WriteString(" (instance '" + instance + "')")
	}
	b.WriteString(". You MAY gather evidence by running probes against it, e.g.:\n")
	b.WriteString("  charly cmd " + target + " '<shell>'            # run a shell command inside the deployment\n")
	b.WriteString("  charly eval mcp list-tools " + target + "      # probe an MCP server it advertises\n")
	b.WriteString("  charly eval cdp status " + target + "          # Chrome DevTools (if it runs a browser)\n")
	b.WriteString("  charly eval wl screenshot " + target + " --artifact /tmp/s.png   # desktop screenshot\n")
	b.WriteString("  charly status " + target + "                   # deployment status\n")
	b.WriteString("Use only what is relevant. Do NOT modify the deployment.\n\n")
	b.WriteString("Decide pass ONLY if the evidence positively confirms the behaviour; otherwise fail. ")
	b.WriteString("If you cannot gather evidence, fail.\n\n")
	b.WriteString("Output discipline: your FINAL line MUST be exactly one JSON object and nothing after it:\n")
	b.WriteString(`{"verdict":"pass","evidence":"<one sentence citing the concrete evidence>"}` + "\n")
	b.WriteString(`or {"verdict":"fail","evidence":"<why it does not hold>"}` + "\n")
	return b.String()
}

// RunAgentOnce launches the configured AI CLI exactly once with the given
// prompt and returns its stdout/stderr. It is the bounded, single-shot
// sibling of the harness loop's iteration launcher (eval_loop.go) and of
// LocalCaptureVersion (agent_config.go) — same host-exec shape, no iteration
// directories, no plateau state. ${PROMPT} in the AI's command argv (and a
// PromptVia: file temp file) is substituted with the prompt text.
func RunAgentOnce(ctx context.Context, ai *AgentConfig, prompt string, timeout time.Duration) (string, string, error) {
	if ai == nil || len(ai.Command) == 0 {
		return "", "", fmt.Errorf("ai entry has no command")
	}

	argv := append([]string(nil), ai.Command...)
	if ai.PromptVia == "file" {
		f, err := os.CreateTemp("", "charly-grader-prompt-*.md")
		if err != nil {
			return "", "", fmt.Errorf("writing grader prompt file: %w", err)
		}
		defer os.Remove(f.Name())
		if _, err := f.WriteString(prompt); err != nil {
			_ = f.Close()
			return "", "", err
		}
		_ = f.Close()
		for i := range argv {
			argv[i] = strings.ReplaceAll(argv[i], "${PROMPT_FILE}", f.Name())
		}
	} else {
		for i := range argv {
			argv[i] = strings.ReplaceAll(argv[i], "${PROMPT}", prompt)
		}
	}

	if timeout <= 0 {
		timeout = GraderDefaultTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, argv[0], argv[1:]...)
	if len(ai.Env) > 0 {
		env := os.Environ()
		for k, v := range ai.Env {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if cctx.Err() == context.DeadlineExceeded {
		return stdout.String(), stderr.String(), fmt.Errorf("grader timed out after %s", timeout)
	}
	return stdout.String(), stderr.String(), err
}

// graderVerdict is the JSON shape the grading agent emits.
type graderVerdict struct {
	Verdict  string `json:"verdict"`
	Evidence string `json:"evidence"`
}

// parseVerdict extracts the grader's pass/fail verdict from the AI's output.
// It tolerates two shapes: plain output whose final line is the verdict JSON,
// and `--output-format stream-json` NDJSON whose final {"type":"result"}
// event carries the agent's text in its "result" field. Returns ok=false
// when no `{"verdict":…}` object can be found (caller fails the step).
func parseVerdict(out string) (pass bool, evidence string, ok bool) {
	// First, harvest any stream-json result text so the verdict embedded in
	// the agent's final message is searchable even under NDJSON.
	text := out
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var ev map[string]any
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		if ev["type"] == "result" {
			if r, isStr := ev["result"].(string); isStr {
				text += "\n" + r
			}
		}
	}

	// Scan for the LAST balanced JSON object that contains "verdict".
	if v, found := lastVerdictObject(text); found {
		var gv graderVerdict
		if json.Unmarshal([]byte(v), &gv) == nil && gv.Verdict != "" {
			return strings.EqualFold(strings.TrimSpace(gv.Verdict), "pass"), gv.Evidence, true
		}
	}
	return false, "", false
}

// lastVerdictObject returns the brace-balanced {...} substring around the LAST
// occurrence of the "verdict" token, so a verdict that follows the agent's
// reasoning (or an earlier illustrative example) wins. Falls back to earlier
// "verdict" occurrences if the nearest one isn't a balanced object.
func lastVerdictObject(s string) (string, bool) {
	const tok = "\"verdict\""
	idx := strings.LastIndex(s, tok)
	for idx >= 0 {
		open := strings.LastIndex(s[:idx], "{")
		if open >= 0 {
			depth := 0
			for j := open; j < len(s); j++ {
				switch s[j] {
				case '{':
					depth++
				case '}':
					depth--
					if depth == 0 {
						return s[open : j+1], true
					}
				}
			}
		}
		// Nearest "verdict" wasn't a balanced object — try an earlier one.
		idx = strings.LastIndex(s[:idx], tok)
	}
	return "", false
}

// lastLines returns the last n non-empty lines of s, joined by " | ", for
// compact one-line error/evidence messages.
func lastLines(s string, n int) string {
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(l); t != "" {
			lines = append(lines, t)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}
