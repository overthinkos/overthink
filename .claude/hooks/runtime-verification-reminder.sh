#!/usr/bin/env bash
# Project-level UserPromptSubmit hook. Fires on every user prompt. Stdout
# becomes a <system-reminder> at the start of Claude's next response.
#
# DOCTRINE: an ULTRA-LEAN POINTER — section-name pointers plus at most ONE
# behavioral anchor, restating no rule bodies. The authoritative rule-set
# (R0-R10, cutover policy, AI attribution, landing) is CLAUDE.md — the single
# current source; restating it here is how this hook previously drifted.
# Deterministic enforcement lives in the PreToolUse gates (pre-commit-gate.sh,
# pre-push-gate.sh). See /charly-internals:agents "Hooks doctrine".

cat <<'EOF'
OPENCHARLY OPERATING REMINDER (pointers — every rule lives in CLAUDE.md):
- R0 SKILLS FIRST: load ALL matching skills (CLAUDE.md R0 "Skill Dispatcher")
  before any read/grep/command/edit/Agent; the consult order (skills >
  CLAUDE.md > memory > exploration) orders where you LOOK, not what is TRUE.
- RDD: prove every HIGH-RISK claim on a `disposable: true` bed EARLY — there
  the live bed outranks every doc (CLAUDE.md "Risk Driven Development (RDD)";
  detail /charly-internals:strict-policy).
- R10: run the gate for YOUR change class, output PASTED; tier == proof
  (CLAUDE.md R10 "gate by change class" + "AI Attribution").
- ONE PHASE through R10 (CLAUDE.md "Hard Cutover by Default"); beds per /charly-internals:agents.
EOF
