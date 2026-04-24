#!/usr/bin/env bash
# Project-level Stop hook. Fires at end-of-turn. Stdout becomes a
# <system-reminder> appended to Claude's final response context.
# SOFT — never emits the blocking JSON. A trivial-reply turn still
# completes normally.

cat <<'EOF'
END-OF-TURN CHALLENGE (soft — does not block):

You are about to stop. Before you do, confirm EACH of these:

  SKILLS FIRST (CLAUDE.md R0 — SUPREME RULE, overrides everything below)
  ----------------------------------------------------------------------
  [ ] For EVERY non-trivial action this turn (Bash / Read / Edit / Agent
      / tool calls other than Skill itself), did you invoke the matching
      skill via the `Skill` tool BEFORE the action?
  [ ] If multiple surfaces were touched (code + ov + tests), did you
      load ALL relevant skills up-front in ONE message (parallel Skill
      calls)? Partial loading is full-bore failure.
  [ ] If you caught yourself grep-ing / Read-ing source / running `ov`
      WITHOUT a skill load first, did you STOP, invoke the skill, and
      re-validate the work you already did against the skill's
      guidance?
  [ ] If the answer to ANY of the above is "no" — this turn is a
      PROTOCOL VIOLATION of R0. You do NOT get to skip this. Correct
      course now: load the missed skill(s), review whether the actions
      you took align with the skill's actual guidance, and fix what
      doesn't align before stopping. R0 overrides the urge to just
      wrap up the turn — "almost done" is not compliance.
  [ ] "I already know this area" / "the task was simple" / "the hook
      told me enough" are NOT defences. R0 has no exceptions.

  ONE-PHASE HARD CUTOVER (CLAUDE.md / /ov-dev:cutover-policy)
  -----------------------------------------------------------
  [ ] Every task in the current cutover is in `completed` status —
      OR the cutover is a genuinely separate, plan-file-documented
      next cutover (NOT a "Phase 2 TODO")?
  [ ] No transitional aliases / legacy-accepting paths remain live
      in the same cutover that introduced them?
  [ ] No half-renamed symbols, no half-migrated configs, no
      deploy.yml entries using the old schema with a comment saying
      "will update later"?
  [ ] Migration command (if the cutover added one) verified
      idempotent on at least one test fixture?

  LIVE VERIFICATION (R1–R10)
  --------------------------
  [ ] Verified EVERY fix on a LIVE DISPOSABLE target (never on a
      non-disposable resource)?
  [ ] Full RCA for every unexpected failure (no blind fix-guessing)?
  [ ] After committing the source-level fix, did you `ov rebuild`
      the disposable target from clean and re-run the full
      verification? (R10 — fresh-rebuild acceptance gate)
  [ ] Fresh-rebuild re-verification ran for EVERY affected target
      in the cutover, not just one of them?
  [ ] If you broke the target during exploration, did you `ov rebuild`
      it back to clean before continuing?
  [ ] Left every target running, not half-broken?
  [ ] Closed EVERY issue surfaced in this session (no silent
      deferrals, anti-pattern R6)?
  [ ] Both the exploratory verification AND the fresh-rebuild
      re-verification outputs pasted into this conversation?

  CONFIDENCE ATTRIBUTION
  ----------------------
  [ ] If declaring `fully tested and validated`: every single
      affected target has its R1–R10 six-point proof pasted.
      If ANY target is missing, downgrade to
      `analysed on a live system` AT BEST.

If YES to all: stop is fine.

If NO — KEEP WORKING. On resources explicitly marked `disposable:
true` (see /ov-dev:disposable), no user permission is needed for
`ov rebuild <name>`. On anything else, confirm before destroying.
The fresh-rebuild step in particular is not optional — without it,
you haven't proven the fix survives a clean rebuild, which means
it WILL regress the next time an unrelated change triggers a
rebuild. Run it.

If any cutover task is still open, the cutover is NOT done. Do NOT
stop mid-cutover. Do NOT declare "Phase 2 pending". Either finish
every task in the current cutover now, OR prove it is a genuinely
separate next cutover with its own plan file. The former is almost
always the right answer.

If you are genuinely STUCK, stopping is legitimate — BUT your final
message MUST end with a SINGLE CLEAR actionable question. Not
"let me know if you want me to continue." Something like:
  "Should I X or Y?"
  "<specific artifact> returns <specific output> which contradicts
   my assumption Z — do you know why?"
Stuck-and-asking is legitimate; stuck-and-vague wastes user time.
EOF
