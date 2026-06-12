#!/usr/bin/env bash
# Project-level UserPromptSubmit hook. Fires on every user prompt. Stdout
# becomes a <system-reminder> at the start of Claude's next response.
#
# DOCTRINE: this hook is a LEAN POINTER, not a copy of the rules. The
# authoritative rule-set (R0-R10, cutover policy, AI attribution, landing) is
# CLAUDE.md — the single current source. Re-stating it here only lets the two
# drift apart (this hook previously duplicated CLAUDE.md and kept naming
# commands long after they were renamed). Keep this short; point, don't
# duplicate. The
# deterministic enforcement lives in the PreToolUse gates (pre-commit-gate.sh,
# pre-push-gate.sh), not in walls of text here. See /charly-internals:agents
# "Hooks doctrine".

cat <<'EOF'
OPENCHARLY OPERATING REMINDER (pointer — the rules live in CLAUDE.md):

R0 SKILLS FIRST. Before you read source / grep / run a command / edit a file
/ launch an Agent, invoke the matching skill(s) via the Skill tool — ALL of
them in one message when several apply. Consult order: skills > CLAUDE.md >
memory > exploration — that orders where you LOOK FIRST, not what is TRUE:
for a HIGH-RISK claim a live disposable bed outranks every doc (RDD below).
Your training is stale; the skill is current. Consult the Skill Dispatcher
table in CLAUDE.md (R0) for the trigger -> skill map.

R10 = THE GATE FOR YOUR CHANGE CLASS ACTUALLY RAN (CLAUDE.md R10 "gate by
change class"). Code/config changes: a real `charly eval run <bed>` /
`charly eval live` against a fresh rebuild of a `disposable: true` target with
the output PASTED — a `--dry-run`, green `go test`, `charly box validate`, or
bare rebuild is NOT that gate. Hook/workflow script edits: execute the
changed script live (a bed only when workflow control flow changed).
Docs/comments-only changes: the non-runtime
standards, NO bed run (beds cannot fail on prose). Inflating the
attribution tier above what the pasted proof supports is fraud; a known rule
violation forbids commit at ANY tier (fix in-tree or escalate — never
"downgrade and ship"). The 2026-04-26 incident (dry-run-as-R10 + tier
inflation + task deletion) is recorded in CHANGELOG.md; do not repeat it.

RDD — RISK DRIVEN DEVELOPMENT (proactive twin of R1; rules in CLAUDE.md). ALWAYS
prove a HIGH-RISK assumption on a `disposable: true` bed — never accept the
skills, CLAUDE.md, or current code as automatically correct (they drift). Load
the skill first for intent (R0), but confirm high-risk claims — above all
whether a layer composition at its latest versions builds/deploys/runs TOGETHER
— on a real bed EARLY. "The docs say so" / "the code does X" / "it probably
composes" are confessions for a high-risk call. See /charly-internals:strict-policy.

An approved plan runs end-to-end through R10 in ONE phase. The only valid
mid-plan stops are CLAUDE.md's two: (a) an error you cannot resolve without
user input; (b) the plan contradicts itself, CLAUDE.md, or a loaded skill —
never context/scope/time/"handoff".

Drive the existing `charly eval` beds to test/verify (eval-bed-runner +
/verify-beds; deploy-verifier + /audit-deploy-configs). The COMMIT is gated on
a full final-code bed test (pasted); run beds freely throughout to verify —
only on `disposable: true`, no scope-shrinking flags. See /charly-internals:agents.
EOF
