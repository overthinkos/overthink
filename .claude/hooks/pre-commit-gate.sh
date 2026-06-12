#!/usr/bin/env bash
# PreToolUse(Bash) deterministic gate. Blocks (exit 2) a `git commit` that:
#   - bypasses the project's git hooks (--no-verify, or its short alias -n
#     incl. bundled forms like -an, as a flag BEFORE the message — so a
#     "--no-verify" mention INSIDE a commit message never false-triggers), or
#   - carries an AI-attribution tier the CLAUDE.md table forbids on a commit
#     (`theoretical suggestion`, and `syntax check only` — the table pairs it
#     with "do NOT commit"; docs-only cutovers ship at "fully tested and
#     validated" per the provision), or any unknown tier, or
#   - uses an inline -m message with NO `Assisted-by: Claude (<tier>)` trailer.
# It does NOT judge whether the tier is JUSTIFIED by the proof — that is the
# AI's job (testing-validator + the pasted-proof rule). Hooks gate mechanical
# invariants; agents judge proof. See CLAUDE.md "Agents, Workflows & Teams"
# (Hooks doctrine) + /charly-internals:agents.
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

LEGAL = {"fully tested and validated", "analysed on a live system"}

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
has_inline_msg = False
for m in INVOKE.finditer(cmd):
    found = True
    args = m.group(1) or ''
    # inline-message detection is scoped to THIS commit invocation's arg span,
    # so a foreign -m elsewhere on the line (grep -m 1 ...; git commit -F f)
    # never triggers the absent-trailer check.
    if re.search(r'(?:^|\s)(?:-m|--message)(?:\s|=)', args):
        has_inline_msg = True
    # --no-verify only counts as a FLAG when it appears BEFORE the message
    # provider (-m/-F); a "--no-verify" mention inside the message must not block.
    # -n is git-commit's short alias for --no-verify; match it bundled too
    # (-an, -anm, ...). The bundle charset is git-commit's value-less short
    # options; m may appear only AFTER the n (a bundled m starts the message
    # VALUE, so an n after m is message text, e.g. -amnope = -a -m "nope").
    # A value-carrying token like -uno never false-triggers; long flags (--*)
    # never match a single dash.
    pre_msg = re.split(r'(?:^|\s)(?:-m|--message|-F|--file)(?:\s|=)', args, maxsplit=1)[0]
    if re.search(r'(?:^|\s)(?:--no-verify|-[aiopsvqezS]*n[aiopsvqezSm]*)(?:\s|$)', pre_msg):
        block("`git commit --no-verify` (or its -n short alias) bypasses the project hooks — forbidden (CLAUDE.md: never bypass hooks).")

if found:
    # The Assisted-by trailer is structured; scanning the whole command is correct.
    tiers = re.findall(r'Assisted-by:\s*Claude\s*\(([^)]*)\)', cmd)
    for t in tiers:
        tier = t.strip()
        if tier == "syntax check only":
            block('committing at tier "syntax check only" is a CLAUDE.md violation (AI Attribution: this tier pairs with "do NOT commit" — R10 has not run; STOP and ask). Docs-only cutovers ship at "fully tested and validated" per the provision.')
        if tier not in LEGAL:
            block('illegal AI-attribution tier "%s". Legal on a commit: %s. ("theoretical suggestion" is forbidden for shipped code.)' % (tier, sorted(LEGAL)))
    if has_inline_msg and not tiers and '$(' not in cmd and '<<' not in cmd:
        block("commit message has no `Assisted-by: Claude (<tier>)` trailer (required on every commit; add it inline with the tier your R10 proof supports).")

sys.exit(0)
PY
