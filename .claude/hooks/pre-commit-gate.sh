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
#     LICENSE/VISION/`*.txt` files, comment-only code edits, or a submodule
#     pointer bump whose own old..new diff is itself all-documentation), or
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
# comments / blanks only OR a submodule pointer bump whose own old..new diff is
# itself all-documentation (recursed one level — a bump that integrates submodule
# code is rejected). Conservative-safe: it may reject a trailing/block-comment-
# only edit (harmless — use a runtime tier there), but it never lets a behavioral
# change pass as docs. The gate is a discipline backstop, not a security boundary
# (a compound `git add ... && git commit` inspects the CURRENT index, like the
# rest of this gate's command-span scoping).
DOC_PATH = re.compile(r'(?:^|/)(?:CHANGELOG|README|LICENSE|VISION)[^/]*$|\.(?:md|txt)$',
                      re.IGNORECASE)
LINE_COMMENT = {
    '.go': '//', '.js': '//', '.ts': '//', '.c': '//', '.h': '//', '.cc': '//',
    '.cpp': '//', '.hpp': '//', '.rs': '//', '.java': '//', '.kt': '//', '.swift': '//',
    '.sh': '#', '.bash': '#', '.zsh': '#', '.py': '#', '.rb': '#', '.pl': '#',
    '.yml': '#', '.yaml': '#', '.toml': '#', '.cfg': '#', '.ini': '#', '.mk': '#',
}

def _git(args, cwd=None):
    base = ["git"] + (["-C", cwd] if cwd else [])
    try:
        out = subprocess.run(base + args, capture_output=True, text=True, timeout=10)
    except Exception:
        return None
    if out.returncode != 0:
        return None
    return out.stdout

def changed_lines_all_comments(path, repo=None, rangespec=None):
    ext = os.path.splitext(path)[1].lower()
    marker = LINE_COMMENT.get(ext)
    if marker is None:
        return False  # unknown / binary type — cannot certify comment-only
    diffargs = (["diff", "-U0", rangespec, "--", path] if rangespec
                else ["diff", "--cached", "-U0", "--", path])
    diff = _git(diffargs, cwd=repo)
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

def _is_doc(path, repo=None, rangespec=None):
    if DOC_PATH.search(path):
        return True
    return changed_lines_all_comments(path, repo=repo, rangespec=rangespec)

ZERO = re.compile(r'^0+$')

def submodule_bad_files(sub, old, new, repo=None):
    # A staged submodule pointer bump is documentation IFF the submodule's own
    # old..new diff is itself all-documentation. Returns the non-doc file list
    # (empty == all docs), or None when the bump cannot be certified — objects
    # absent locally, or a submodule add/remove (all-zero old/new sha).
    if ZERO.match(old) or ZERO.match(new):
        return None
    subrepo = os.path.join(repo, sub) if repo else sub
    rangespec = old + ".." + new
    names = _git(["diff", "--name-only", rangespec], cwd=subrepo)
    if names is None:
        return None
    bad = []
    for f in (x for x in names.splitlines() if x.strip()):
        if _is_doc(f, repo=subrepo, rangespec=rangespec):
            continue
        bad.append(f)
    return bad

def assert_docs_only_diff(repo=None):
    # The `documentation reviewed` tier is honest only when EVERY staged entry is
    # documentation: a doc path, a comment-only code edit, OR a submodule pointer
    # bump whose own old..new diff is itself all-documentation (recursed one
    # level). `--raw` exposes the gitlink mode (160000) + the old/new SHAs needed
    # to inspect the bumped submodule commit.
    raw = _git(["diff", "--cached", "--raw"], cwd=repo)
    if raw is None:
        block('the "documentation reviewed" tier requires inspecting the staged diff, but '
              '`git diff --cached --raw` failed. Stage the documentation changes and retry, or use '
              'a runtime tier.')
    bad = []
    for line in raw.splitlines():
        if not line.startswith(':'):
            continue
        meta, _tab, rest = line.partition('\t')
        fields = meta[1:].split()
        path = rest.strip()
        if len(fields) < 4:
            bad.append(path or meta)
            continue
        modeA, modeB, shaA, shaB = fields[0], fields[1], fields[2], fields[3]
        if modeA == '160000' or modeB == '160000':
            sub_bad = submodule_bad_files(path, shaA, shaB, repo=repo)
            if sub_bad is None:
                block('the "documentation reviewed" tier cannot certify the submodule pointer bump '
                      '"%s" as documentation: its objects are not present locally, or it adds/removes '
                      'a submodule. Fetch the submodule and retry, or use a runtime tier.' % path)
            bad.extend('%s -> %s' % (path, b) for b in sub_bad)
            continue
        if _is_doc(path, repo=repo):
            continue
        bad.append(path)
    if bad:
        block('the "documentation reviewed" tier is only legal for an all-documentation diff '
              '(*.md / CHANGELOG / README / LICENSE / VISION / *.txt, comment-only code edits, or a '
              'submodule pointer bump to an all-documentation submodule commit). Non-documentation '
              'changes staged: %s. The change touches code/config — use a runtime tier, or split the '
              'docs into their own commit.' % ', '.join(bad))

# git in command position (start / after ;&| / after a shell keyword),
# optional global opts, then `commit`, then capture the invocation's arg span
# up to the next shell separator.
INVOKE = re.compile(
    r'(?:^\s*|[\n;&|]\s*|(?:^|\s)(?:if|then|elif|else|do|while|until)\s+)'
    r'git(?:\s+-{1,2}[A-Za-z][^\s]*(?:\s+[^\s-][^\s]*)?)*\s+commit((?:\s+[^\s;&|]+)*)')

found = False
has_inline_msg = False
commit_cwd = None
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
    # A `-C <dir>` in the commit invocation's global options retargets the repo
    # whose index this commit writes; scope the docs-tier diff inspection there
    # (default: the hook's CWD) so a `git -C <sub> commit` is judged against the
    # submodule's index, not the superproject's.
    mC = re.search(r'(?:^|\s)-C\s+(\S+)', glob_opts)
    if mC:
        commit_cwd = mC.group(1)
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
            assert_docs_only_diff(commit_cwd)
    if has_inline_msg and not tiers and '$(' not in cmd and '<<' not in cmd:
        block("commit message has no `Assisted-by: Claude (<tier>)` trailer (every commit Claude is involved in must attribute; add it inline with the tier your R10 proof supports — docs-only commits use `documentation reviewed`).")

sys.exit(0)
PY
