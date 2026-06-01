#!/usr/bin/env bash
# Project-level agent-team lifecycle hook. Wired to TaskCreated, TaskCompleted,
# and TeammateIdle in settings.json. Stdout becomes a <system-reminder> for the
# team lead / teammate. SOFT — always exits 0, so it NEVER blocks task creation,
# task completion, or a teammate going idle (exit 2 would block + send feedback;
# we only remind).
#
# DOCTRINE: a LEAN POINTER, not a copy of the rules. The authoritative rule-set
# (R0-R10, the bed-ownership parallel-testing model, the commit gate) lives in
# CLAUDE.md + /ov-internals:agents. Re-stating it here only lets the two drift
# apart. Point, don't duplicate. See /ov-internals:agents "Hooks doctrine".

cat <<'EOF'
OVERTHINK TEAM REMINDER (pointer — the rules live in CLAUDE.md + /ov-internals:agents):

R0 SKILLS FIRST. Each teammate loads the matching skill(s) via the Skill tool
before reading source / running a command / editing a file. Teammates read
CLAUDE.md + project/user skills on spawn; your training is stale, the skill is
current.

OWN A DISJOINT BED. The eval bed is the unit of ownership AND isolation (no
worktree). The lead partitions the kind:eval beds so no two teammates own the
same bed — distinct beds have disjoint container/VM/image names + ports
(validateEvalBeds guarantees it), so they are concurrent-safe.

RUN A REAL DEPLOYMENT. Your job is your bed's full `ov eval run <bed>` on a live
deployment — build -> eval image -> deploy -> eval live -> fresh ov update ->
teardown. Review/triage/RCA are auxiliary, NEVER a substitute for the live run.

VERIFY BEFORE YOU CHANGE (Risk Driven Development — proactive twin of R1; rules
in CLAUDE.md). Prove every HIGH-RISK assumption on a live bed BEFORE editing —
never trust a doc or the code for a high-risk call; above all, does this layer
composition at its latest versions build/deploy/run together. Run beds freely
throughout to verify; only on `disposable: true`; no scope-shrinking flags.

THE LEAD OWNS THE COMMIT. One cutover = one phase = ONE atomic commit, owned by
the lead, gated on a full final-code live test (pasted). Teammates NEVER commit
or push independently.
EOF
