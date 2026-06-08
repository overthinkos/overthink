#!/usr/bin/env bash
# Project-level agent-team lifecycle hook. Wired to TaskCreated, TaskCompleted,
# and TeammateIdle in settings.json. Stdout becomes a <system-reminder> for the
# team lead / teammate. SOFT — always exits 0, so it NEVER blocks task creation,
# task completion, or a teammate going idle (exit 2 would block + send feedback;
# we only remind).
#
# DOCTRINE: a LEAN POINTER, not a copy of the rules. The authoritative rule-set
# (R0-R10, the bed-ownership parallel-testing model, the commit gate) lives in
# CLAUDE.md + /charly-internals:agents. Re-stating it here only lets the two drift
# apart. Point, don't duplicate. See /charly-internals:agents "Hooks doctrine".

cat <<'EOF'
OVERTHINK TEAM REMINDER (pointer — the rules live in CLAUDE.md + /charly-internals:agents):

R0 SKILLS FIRST. Each teammate loads the matching skill(s) via the Skill tool
before reading source / running a command / editing a file. Teammates read
CLAUDE.md + project/user skills on spawn; your training is stale, the skill is
current.

OWN A DISJOINT BED. The eval bed is the unit of ownership AND isolation (no
worktree). The lead partitions the kind:eval beds so no two teammates own the
same bed — distinct beds get distinct container/VM/image names; the lead also
gives each disjoint host ports (the loader does NOT check ports — an overlap
fails the second bed at deploy), so they are concurrent-safe.

BED RUNS = THROUGHPUT, PERSISTENT-OWNER-OWNED. `charly eval run --all-beds` is
SEQUENTIAL — parallel speed comes from running beds concurrently, and EVERY full
`charly eval run <bed>` is a `run_in_background` task owned by a PERSISTENT owner that
survives across turns to be notified: the lead's persistent session (one task per
bed), a background agent, or (interactive tmux) a split-pane teammate. An
IN-PROCESS teammate CANNOT own a bed — its bg dies on yield. No 600s/duration
carve-out (600s is a Bash FOREGROUND cap, irrelevant to a backgrounded bed).
Launch longest-pole-first (slow VM/desktop first, cheap pods overlapping). FREEZE
charly/*.go during the bed phase — a Go edit mid-bed-run trips the freshness guard
and aborts everyone's next build/deploy/eval (the lead rebuilds charly ONCE at the
barrier). Detail: /charly-internals:agents "Speed levers".

EDIT YOUR BED, DON'T RUN IT. Your job is your bed's SOURCE (bed-local edits) +
short foreground checks (`charly eval box`, `charly box validate`) — NOT the full `charly
eval run` (the LEAD owns that as a background task). The full live run — build ->
eval image -> deploy -> eval live -> fresh charly update -> teardown — is the lead's;
review/triage/RCA are auxiliary, never a substitute for it.

VERIFY BEFORE YOU CHANGE (Risk Driven Development — proactive twin of R1; rules
in CLAUDE.md). Prove every HIGH-RISK assumption on a live bed BEFORE editing —
never trust a doc or the code for a high-risk call; above all, does this layer
composition at its latest versions build/deploy/run together. Run beds freely
throughout to verify; only on `disposable: true`; no scope-shrinking flags.

THE LEAD OWNS THE COMMIT. One cutover = one phase = ONE atomic commit, owned by
the lead, gated on a full final-code live test (pasted). Teammates NEVER commit
or push independently.
EOF
