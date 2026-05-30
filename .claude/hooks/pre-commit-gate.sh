#!/usr/bin/env bash
# PreToolUse(Bash) deterministic gate. Blocks (exit 2) a `git commit` that:
#   - bypasses the project's git hooks (--no-verify, as a flag BEFORE the
#     message — so a "--no-verify" mention INSIDE a commit message never
#     false-triggers), or
#   - carries an AI-attribution tier outside the legal set (incl. the
#     forbidden `theoretical suggestion`), or
#   - uses an inline -m message with NO `Assisted-by: Claude (<tier>)` trailer.
# It does NOT judge whether the tier is JUSTIFIED by the proof — that is the
# AI's job (testing-validator + the pasted-proof rule). Hooks gate mechanical
# invariants; agents judge proof. See CLAUDE.md "Agents, Workflows & Teams"
# (Hooks doctrine) + /ov-internals:agents.
#
# Fast path: only a git-commit-mentioning command reaches the analyzer.

INPUT=$(cat)
case "$INPUT" in
  *git*commit*) ;;
  *) exit 0 ;;
esac

python3 - "$INPUT" <<'PY'
import json, re, sys
try:
    cmd = json.loads(sys.argv[1]).get("tool_input", {}).get("command", "")
except Exception:
    sys.exit(0)

LEGAL = {"fully tested and validated", "analysed on a live system", "syntax check only"}

def block(msg):
    sys.stderr.write("pre-commit-gate BLOCKED: " + msg + "\n")
    sys.exit(2)

# git in command position (start / after ;&| / after a shell keyword),
# optional global opts, then `commit`, then capture the invocation's arg span
# up to the next shell separator.
INVOKE = re.compile(
    r'(?:^\s*|[\n;&|]\s*|(?:^|\s)(?:if|then|elif|else|do|while|until)\s+)'
    r'git(?:\s+-{1,2}[A-Za-z][^\s]*(?:\s+[^\s-][^\s]*)?)*\s+commit((?:\s+[^\s;&|]+)*)')

found = False
for m in INVOKE.finditer(cmd):
    found = True
    args = m.group(1) or ''
    # --no-verify only counts as a FLAG when it appears BEFORE the message
    # provider (-m/-F); a "--no-verify" mention inside the message must not block.
    pre_msg = re.split(r'(?:^|\s)(?:-m|--message|-F|--file)(?:\s|=)', args, maxsplit=1)[0]
    if re.search(r'(?:^|\s)--no-verify(?:\s|$)', pre_msg):
        block("`git commit --no-verify` bypasses the project hooks — forbidden (CLAUDE.md: never bypass hooks).")

if found:
    # The Assisted-by trailer is structured; scanning the whole command is correct.
    tiers = re.findall(r'Assisted-by:\s*Claude\s*\(([^)]*)\)', cmd)
    for t in tiers:
        if t.strip() not in LEGAL:
            block('illegal AI-attribution tier "%s". Legal: %s. ("theoretical suggestion" is forbidden for shipped code.)' % (t.strip(), sorted(LEGAL)))
    inline = re.search(r'(?:^|\s)(?:-m|--message)(?:\s|=)', cmd)
    if inline and not tiers and '$(' not in cmd and '<<' not in cmd:
        block("commit message has no `Assisted-by: Claude (<tier>)` trailer (required on every commit; add it inline with the tier your R10 proof supports).")

sys.exit(0)
PY
