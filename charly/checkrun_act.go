package main

import (
	"context"
	"fmt"
	"strings"
)

// checkrun_act.go — the runtime do:act execution path.
//
// runOne dispatches by the op's resolved do-mode (Op.EffectiveDo, stamped from
// the enclosing Step keyword):
//
//   - do: assert (check:) → the run<Verb> probe handlers (the default).
//   - do: act    (run:)   → for a STATE-PROVISION verb (file/package/service/
//                   user/group/kernel-param/mount) render the create/configure
//                   command and run it via the executor. ACTION verbs
//                   (command/http/dbus/cdp/wl/vnc/mcp/k8s/adb/appium/spice/
//                   libvirt/record/kill) already perform their side-effect in
//                   their own handler, so do:act there reuses that handler.
//
// The state-provision renderers are the do:act half of each verb provider — a
// ProvisionActor method (verb_builtins.go types). runProvisionAct resolves the
// verb through the registry and type-asserts ProvisionActor; the per-verb switch
// is gone (C1b).
//
// Agent steps (agent-run:/agent-check:) never reach runOne — they route to the
// grader in runUnit (description_run.go). Runtime act ops are NOT auto-reversed
// (no ledger entry) — the author reverses them with a teardown run: step.

// resolveProvisionScript resolves an op's state-provision verb to its ProvisionActor
// and renders the act shell — the SINGLE Op→act-shell seam shared by the runtime act
// path (runProvisionAct) AND every install-emit path: emitTasks' `case "plugin"` (the
// box build via writeCandySteps→emitTasks, and the pod overlay via OCITarget.emitOp,
// which delegates to emitTasks) AND renderOpCommand (the local/vm deploy targets) — the
// act-emit enabler, so a state-provision verb provisions identically whether run live,
// baked into an image, or applied at deploy (R3).
//
// It threads the plugin indirection: when the op's verb is the generic `plugin:`
// discriminator, the ProvisionActor is the plugin word's provider (op.Plugin), NOT the
// pluginVerb dispatcher. ok=false when the resolved provider is not a ProvisionActor (an
// action verb whose handler already acts, a pure observe verb, or a non-act plugin) — the
// runtime caller then falls through to the normal dispatch; an emit caller turns it into a
// hard error (a run: step naming a non-act verb has no build/deploy install path).
func resolveProvisionScript(op *Op, distros []string) (string, bool) {
	word, err := op.Kind()
	if err != nil {
		return "", false
	}
	if word == "plugin" {
		word = op.Plugin
	}
	prov, ok := providerRegistry.ResolveVerb(word)
	if !ok {
		return "", false
	}
	actor, ok := prov.(ProvisionActor)
	if !ok {
		return "", false
	}
	return actor.RenderProvisionScript(op, distros)
}

// runProvisionAct executes a state-provision verb's create/configure command
// and reports pass on a zero exit. Returns ok=false when the verb has no
// provision renderer (an action verb whose handler already acts, or a pure
// observe verb) so the caller falls through to the normal dispatch. Resolution
// (incl. the `plugin:` indirection) is the shared resolveProvisionScript.
func (r *Runner) runProvisionAct(ctx context.Context, c *Op, verb string) (CheckResult, bool) {
	script, ok := resolveProvisionScript(c, r.Distros)
	if !ok {
		return CheckResult{}, false
	}
	if r.Mode == RunModeBox {
		return skipf(c, "do: act not meaningful under charly check box (no running target)"), true
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

// The do:act renderers — the ProvisionActor half of each state-provision verb
// provider. Each renders the shell that performs the verb's side-effect on the
// live target; ok=false for an input that cannot act (e.g. kernel-param with no
// desired value).

func (fileVerb) RenderProvisionScript(c *Op, _ []string) (string, bool) {
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
}

func (packageVerb) RenderProvisionScript(c *Op, distros []string) (string, bool) {
	// Install via whichever package manager the target carries; the name
	// is cross-distro-resolved exactly as the assert path resolves it.
	name := shellSingleQuote(resolvePackageName(c, distros))
	return fmt.Sprintf(`if command -v dnf >/dev/null 2>&1; then dnf install -y %[1]s; `+
		`elif command -v apt-get >/dev/null 2>&1; then apt-get update && apt-get install -y %[1]s; `+
		`elif command -v pacman >/dev/null 2>&1; then pacman -S --noconfirm %[1]s; `+
		`else echo "no supported package manager" >&2; exit 1; fi`, name), true
}

func (serviceVerb) RenderProvisionScript(c *Op, _ []string) (string, bool) {
	// Enable + start the unit under whichever init the target runs.
	svc := shellSingleQuote(c.Service)
	return fmt.Sprintf(`if command -v systemctl >/dev/null 2>&1; then systemctl enable --now %[1]s; `+
		`elif command -v supervisorctl >/dev/null 2>&1; then supervisorctl start %[1]s; `+
		`else echo "no service manager" >&2; exit 1; fi`, svc), true
}

func (userVerb) RenderProvisionScript(c *Op, _ []string) (string, bool) {
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
}

// unix_group's RenderProvisionScript (the do:act half) lives with its dedicated plugin
// unit (plugin_unix_group.go) — it decodes plugin_input rather than the removed
// Op.UnixGroup/Op.GID fields.

func (kernelParamVerb) RenderProvisionScript(c *Op, _ []string) (string, bool) {
	if v, ok := firstMatcherScalar(c.Value); ok {
		return fmt.Sprintf("sysctl -w %s=%s", shellSingleQuote(c.KernelParam), shellSingleQuote(v)), true
	}
	return "", false // act with no desired value is meaningless
}

func (mountVerb) RenderProvisionScript(c *Op, _ []string) (string, bool) {
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
