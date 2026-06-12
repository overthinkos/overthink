#!/usr/bin/env bash
# Project-level Stop hook. Fires at end-of-turn. Stdout becomes a
# <system-reminder> appended to Claude's final response context. SOFT — never
# emits blocking JSON; a trivial-reply turn still completes normally.
#
# DOCTRINE: an ULTRA-LEAN POINTER — the authoritative checklist is CLAUDE.md
# "Acceptance checklist" + "Post-Execution Policies"; this hook points at it
# and keeps ONE behavioral anchor, restating nothing (duplication drifts).
# Deterministic enforcement (force-push, bad tier, --no-verify) is in the
# PreToolUse gates. See /charly-internals:agents "Hooks doctrine".

cat <<'EOF'
END-OF-TURN CHECK (soft — does not block): before claiming done, walk
CLAUDE.md "Acceptance checklist" (all three groups) + "Post-Execution
Policies" box by box against THIS turn's work.
If any box is unchecked and the cutover isn't done: KEEP WORKING
(`disposable: true` targets need no extra permission). If genuinely stuck,
stop with ONE specific actionable question.
EOF
