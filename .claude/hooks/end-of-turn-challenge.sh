#!/usr/bin/env bash
# Project-level Stop hook. Fires at end-of-turn. Stdout becomes a
# <system-reminder> appended to Claude's final response context. SOFT — never
# emits blocking JSON; a trivial-reply turn still completes normally.
#
# DOCTRINE: a SECOND-PASS COMPLIANCE REMINDER — it points at CLAUDE.md
# "Acceptance checklist" + "Post-Execution Policies" and prompts a re-audit of
# THIS turn's changes against them; it carries pointer-shaped behavioral anchors
# (re-audit, CHANGELOG entry, keep-working) but restates no rule bodies
# (CLAUDE.md is the single source). Deterministic enforcement (force-push, bad
# tier, --no-verify, the per-repo CHANGELOG-entry gate) is in the PreToolUse
# gates. See /charly-internals:agents "Hooks doctrine".

cat <<'EOF'
END-OF-TURN SECOND PASS (soft — does not block): before claiming done,
re-verify THIS turn for FULL CLAUDE.md compliance:
- Walk "Acceptance checklist" (all three groups) + "Post-Execution Policies"
  box-by-box against this turn's work.
- Re-audit EVERY code/config change you made this turn against R0-R10
  (esp. R1 RCA, R2 no-deferral, R3 no-dup, R5 stale-ref grep sweep).
- CHANGELOG: did EACH repo you changed record its entry in its current-month
  CHANGELOG/YYYY-MM.md? (Doc-split: history -> the repo's CHANGELOG/.)
  A behavioral cutover with no CHANGELOG entry is NOT done.
If any box is unchecked and the cutover isn't done: KEEP WORKING
(`disposable: true` targets need no extra permission). If genuinely stuck,
stop with ONE specific actionable question.
EOF
