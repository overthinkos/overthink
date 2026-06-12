#!/usr/bin/env bash
# PreToolUse(Bash) deterministic gate. Blocks (exit 2) a `git commit` that:
#   - bypasses the project's git hooks (--no-verify, or its short alias -n
#     incl. bundled forms like -an, as a flag BEFORE the message — so a
#     "--no-verify" mention INSIDE a commit message never false-triggers; or
#     a core.hooksPath override in git's global options, the config spelling
#     of the same bypass), or
#   - carries an AI-attribution tier the CLAUDE.md table forbids on a commit
#     (`theoretical suggestion`, and `syntax check only` — the table pairs it
#     with "do NOT commit"), or any unknown tier (legal-on-commit set:
#     `fully tested and validated`, `analysed on a live system`,
#     `documentation reviewed`), or
#   - carries the `documentation reviewed` tier with a staged diff that is NOT
#     all-documentation (the tier is only honest for `*.md`/CHANGELOG/README/
#     LICENSE/VISION/`*.txt` files, or comment-only code edits), or
#   - uses an inline -m message with NO `Assisted-by: Claude (<tier>)` trailer
#     (every commit Claude is involved in — in ANY way — must attribute; a
#     pure-human hand-commit does not pass through this PreToolUse gate).
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
import json, os, re, subprocess, sys
try:
    cmd = json.loads(sys.argv[1]).get("tool_input", {}).get("command", "")
except Exception:
    sys.exit(0)

LEGAL = {"fully tested and validated", "analysed on a live system", "documentation reviewed"}

def block(msg):
    sys.stderr.write("pre-commit-gate BLOCKED: " + msg + "\n")
    sys.exit(2)

# --- strict gate for the `documentation reviewed` tier ---------------------
# That tier is only honest when the staged diff is all-documentation: every
# staged file is a doc path OR a code file whose staged hunks are full-line
# comments / blanks only. Conservative-safe: it may reject a trailing/block-
# comment-only edit (harmless — use a runtime tier there), but it never lets a
# behavioral change pass as docs. The gate is a discipline backstop, not a
# security boundary (a compound `git add ... && git commit` inspects the
# CURRENT index, like the rest of this gate's command-span scoping).
DOC_PATH = re.compile(r'(?:^|/)(?:CHANGELOG|README|LICENSE|VISION)[^/]*$|\.(?:md|txt)$',
                      re.IGNORECASE)
LINE_COMMENT = {
    '.go': '//', '.js': '//', '.ts': '//', '.c': '//', '.h': '//', '.cc': '//',
    '.cpp': '//', '.hpp': '//', '.rs': '//', '.java': '//', '.kt': '//', '.swift': '//',
    '.sh': '#', '.bash': '#', '.zsh': '#', '.py': '#', '.rb': '#', '.pl': '#',
    '.yml': '#', '.yaml': '#', '.toml': '#', '.cfg': '#', '.ini': '#', '.mk': '#',
}

def _git(args):
    try:
        out = subprocess.run(["git"] + args, capture_output=True, text=True, timeout=10)
    except Exception:
        return None
    if out.returncode != 0:
        return None
    return out.stdout

def changed_lines_all_comments(path):
    ext = os.path.splitext(path)[1].lower()
    marker = LINE_COMMENT.get(ext)
    if marker is None:
        return False  # unknown / binary type — cannot certify comment-only
    diff = _git(["diff", "--cached", "-U0", "--", path])
    if diff is None:
        return False
    if "Binary files" in diff:
        return False
    for line in diff.splitlines():
        if line.startswith('+++') or line.startswith('---'):
            continue
        if line and line[0] in '+-':
            content = line[1:].strip()
            if content == '':
                continue
            if not content.startswith(marker):
                return False
    return True

def assert_docs_only_diff():
    names = _git(["diff", "--cached", "--name-only"])
    if names is None:
        block('the "documentation reviewed" tier requires inspecting the staged diff, but '
              '`git diff --cached` failed. Stage the documentation changes and retry, or use '
              'a runtime tier.')
    files = [f for f in names.splitlines() if f.strip()]
    bad = []
    for f in files:
        if DOC_PATH.search(f):
            continue
        if changed_lines_all_comments(f):
            continue
        bad.append(f)
    if bad:
        block('the "documentation reviewed" tier is only legal for an all-documentation diff '
              '(*.md / CHANGELOG / README / LICENSE / VISION / *.txt, or comment-only code '
              'edits). Non-documentation changes staged: %s. The change touches code/config — '
              'use a runtime tier, or split the docs into their own commit.' % ', '.join(bad))

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
    # A core.hooksPath override is the config spelling of --no-verify. The
    # `-c key=value` form lives in git's GLOBAL options (between `git` and
    # `commit`), so scan ONLY that span — commit's own `-c <commit>`
    # (reuse-message) and a message merely mentioning the key never
    # false-trigger. Env-var config injection is out of scope: the gate is a
    # discipline backstop, not a security boundary.
    glob_opts = cmd[m.start(0):m.start(1)]
    if re.search(r'core\.hookspath', glob_opts, re.IGNORECASE):
        block("`git -c core.hooksPath=...` bypasses the project's git hooks — the config spelling of --no-verify; forbidden (CLAUDE.md: never bypass hooks).")
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
            block('committing at tier "syntax check only" is a CLAUDE.md violation (AI Attribution: this tier pairs with "do NOT commit" — R10 has not run; STOP and ask).')
        if tier not in LEGAL:
            block('illegal AI-attribution tier "%s". Legal on a commit: %s. ("theoretical suggestion" is forbidden for shipped code.)' % (tier, sorted(LEGAL)))
        if tier == "documentation reviewed":
            assert_docs_only_diff()
    if has_inline_msg and not tiers and '$(' not in cmd and '<<' not in cmd:
        block("commit message has no `Assisted-by: Claude (<tier>)` trailer (every commit Claude is involved in must attribute; add it inline with the tier your R10 proof supports — docs-only commits use `documentation reviewed`).")

sys.exit(0)
PY
