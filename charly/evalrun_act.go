package main

import (
	"context"
	"fmt"
	"strings"
)

// evalrun_act.go — the runtime do:act + do:instruct execution paths.
//
// runOne dispatches by the op's resolved do-mode (Op.EffectiveDo):
//
//   - do: assert  → the run<Verb> probe handlers (the default; unchanged).
//   - do: act     → for a STATE-PROVISION verb (file/package/service/user/
//                   group/kernel-param/mount) render the create/configure
//                   command and run it via the executor. ACTION verbs
//                   (command/http/dbus/cdp/wl/vnc/mcp/k8s/adb/appium/spice/
//                   libvirt/record/kill) already perform their side-effect in
//                   their own handler, so do:act there reuses that handler.
//   - do: instruct → hand the free-form text to the agent grader.
//
// Runtime act ops are NOT auto-reversed (no ledger entry) — the author reverses
// them with a scenario `teardown:` step (CLAUDE.md / the plan: "live-verb
// do:act … reversed via scenario teardown, never the ledger").

// gradeInstruct routes a do:instruct op (the `agent:` verb, or any op marked
// do: instruct) to the agent grader. With no grader bound it is an advisory
// skip — mirroring a prose-only step under `--no-agent`.
func (r *Runner) gradeInstruct(ctx context.Context, c *Op) EvalResult {
	text := c.Agent
	if text == "" {
		// A non-agent verb marked do:instruct: fall back to the command/file
		// path text so the grader still has something to act on.
		text = firstNonEmpty(c.Command, c.File, c.HTTP)
	}
	if r.Grader == nil {
		return skipf(c, "instruct: no agent grader bound (advisory; run without --no-agent to grade)")
	}
	res := r.Grader.Grade(ctx, GraderRequest{
		Feature:   r.GraderFeature,
		Narrative: r.GraderNarrative,
		Scenario:  r.GraderScenario,
		Text:      text,
	})
	res.Op = c
	return res
}

// runProvisionAct executes a state-provision verb's create/configure command
// and reports pass on a zero exit. Returns ok=false when the verb has no
// provision renderer (an action verb whose handler already acts, or a pure
// observe verb) so the caller falls through to the normal dispatch.
func (r *Runner) runProvisionAct(ctx context.Context, c *Op, verb string) (EvalResult, bool) {
	script, ok := renderProvisionScript(c, verb, r.Distros)
	if !ok {
		return EvalResult{}, false
	}
	if r.Mode == RunModeBox {
		return skipf(c, "do: act not meaningful under charly eval box (no running target)"), true
	}
	_, stderr, exit, err := r.Exec.RunCapture(ctx, wrapContainerCommand(script))
	if err != nil {
		return failf(c, "act %s: execution error: %v", verb, err), true
	}
	if exit != 0 {
		return failf(c, "act %s: exit %d: %s", verb, exit, strings.TrimSpace(stderr)), true
	}
	return passf(c, fmt.Sprintf("act %s: applied", verb)), true
}

// renderProvisionScript renders the shell that performs a state-provision
// verb's side-effect on the live target. ok=false for verbs without an act
// form (their handler observes or already acts).
func renderProvisionScript(c *Op, verb string, distros []string) (string, bool) {
	switch verb {
	case "file":
		// Ensure the file exists with the given content + mode.
		path := shellSingleQuote(c.File)
		var b strings.Builder
		if c.Content != "" {
			// Heredoc with a collision-resistant marker; content is verbatim.
			fmt.Fprintf(&b, "mkdir -p \"$(dirname %s)\" && cat > %s <<'CHARLY_ACT_EOF'\n%s\nCHARLY_ACT_EOF", path, path, c.Content)
		} else {
			fmt.Fprintf(&b, "mkdir -p \"$(dirname %s)\" && touch %s", path, path)
		}
		if c.Mode != "" {
			fmt.Fprintf(&b, " && chmod %s %s", shellSingleQuote(c.Mode), path)
		}
		return b.String(), true

	case "package":
		// Install via whichever package manager the target carries; the name
		// is cross-distro-resolved exactly as the assert path resolves it.
		name := shellSingleQuote(resolvePackageName(c, distros))
		return fmt.Sprintf(`if command -v dnf >/dev/null 2>&1; then dnf install -y %[1]s; `+
			`elif command -v apt-get >/dev/null 2>&1; then apt-get update && apt-get install -y %[1]s; `+
			`elif command -v pacman >/dev/null 2>&1; then pacman -S --noconfirm %[1]s; `+
			`else echo "no supported package manager" >&2; exit 1; fi`, name), true

	case "service":
		// Enable + start the unit under whichever init the target runs.
		svc := shellSingleQuote(c.Service)
		return fmt.Sprintf(`if command -v systemctl >/dev/null 2>&1; then systemctl enable --now %[1]s; `+
			`elif command -v supervisorctl >/dev/null 2>&1; then supervisorctl start %[1]s; `+
			`else echo "no service manager" >&2; exit 1; fi`, svc), true

	case "user":
		flags := ""
		if c.UID != nil {
			flags += fmt.Sprintf(" -u %d", *c.UID)
		}
		if c.Home != "" {
			flags += " -m -d " + shellSingleQuote(c.Home)
		}
		if c.Shell != "" {
			flags += " -s " + shellSingleQuote(c.Shell)
		}
		name := shellSingleQuote(c.User)
		return fmt.Sprintf("id %[1]s >/dev/null 2>&1 || useradd%[2]s %[1]s", name, flags), true

	case "group":
		flags := ""
		if c.GID != nil {
			flags += fmt.Sprintf(" -g %d", *c.GID)
		}
		name := shellSingleQuote(c.Group)
		return fmt.Sprintf("getent group %[1]s >/dev/null 2>&1 || groupadd%[2]s %[1]s", name, flags), true

	case "kernel-param":
		if v, ok := firstMatcherScalar(c.Value); ok {
			return fmt.Sprintf("sysctl -w %s=%s", shellSingleQuote(c.KernelParam), shellSingleQuote(v)), true
		}
		return "", false // act with no desired value is meaningless

	case "mount":
		var args []string
		if c.Filesystem != "" {
			args = append(args, "-t "+shellSingleQuote(c.Filesystem))
		}
		if v, ok := firstMatcherScalar(c.Opts); ok && v != "" {
			args = append(args, "-o "+shellSingleQuote(v))
		}
		if c.MountSource == "" {
			return "", false // need a source to mount
		}
		return fmt.Sprintf("findmnt %[1]s >/dev/null 2>&1 || mount %[2]s %[3]s %[1]s",
			shellSingleQuote(c.Mount), strings.Join(args, " "), shellSingleQuote(c.MountSource)), true
	}
	return "", false
}

// firstMatcherScalar returns the first matcher's value rendered as a string,
// used by the act renderers to read a desired scalar (sysctl value, mount
// opts) out of a field that carries assertion matchers in do:assert mode.
func firstMatcherScalar(ml MatcherList) (string, bool) {
	for _, m := range ml {
		if m.Value == nil {
			continue
		}
		return fmt.Sprintf("%v", m.Value), true
	}
	return "", false
}
