#!/usr/bin/env bash
# PreToolUse(Bash) deterministic gate. Blocks (exit 2) a `git push` that
# force-pushes (forbidden on EVERY branch in EVERY repo — main only
# fast-forwards, tags are add-only; CLAUDE.md / git-workflow) or bypasses
# hooks. It checks ONLY the push invocation's OWN argument span (up to the
# next shell separator), so a `git branch -f` or other `-f` elsewhere in the
# same command line never false-triggers. Recognizes git in command position
# at start, after a separator, or after a shell keyword.
#
# Fast path: only a git-push-mentioning command reaches the analyzer.

INPUT=$(cat)
case "$INPUT" in
  *git*push*) ;;
  *) exit 0 ;;
esac

python3 - "$INPUT" <<'PY'
import json, re, sys
try:
    cmd = json.loads(sys.argv[1]).get("tool_input", {}).get("command", "")
except Exception:
    sys.exit(0)

def block(msg):
    sys.stderr.write("pre-push-gate BLOCKED: " + msg + "\n")
    sys.exit(2)

# git in command position (start / after ;&| / after a shell keyword),
# optional global opts (`-C path`, `-c k=v`, ...), then `push`, then capture
# the push invocation's arg span up to the next shell separator.
INVOKE = re.compile(
    r'(?:^\s*|[\n;&|]\s*|(?:^|\s)(?:if|then|elif|else|do|while|until)\s+)'
    r'git(?:\s+-{1,2}[A-Za-z][^\s]*(?:\s+[^\s-][^\s]*)?)*\s+push((?:\s+[^\s;&|]+)*)')

for m in INVOKE.finditer(cmd):
    args = m.group(1) or ''
    if re.search(r'(?:^|\s)(?:--force|--force-with-lease|-f)(?:\s|=|$)', args):
        block("force-push is forbidden on every branch in every repo (CLAUDE.md: main only fast-forwards, tags are add-only). Remove --force / --force-with-lease / -f.")
    if re.search(r'(?:^|\s)--no-verify(?:\s|$)', args):
        block("`git push --no-verify` bypasses hooks — forbidden.")

sys.exit(0)
PY
