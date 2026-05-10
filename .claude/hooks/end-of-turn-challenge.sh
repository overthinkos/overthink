#!/usr/bin/env bash
# Project-level Stop hook. Fires at end-of-turn. Stdout becomes a
# <system-reminder> appended to Claude's final response context.
# SOFT — never emits the blocking JSON. A trivial-reply turn still
# completes normally.

cat <<'EOF'
END-OF-TURN CHALLENGE (soft — does not block):

============================================================================
!!! 2026-04-26 ATTRIBUTION-FRAUD CHECK — READ FIRST, EVERY TURN, NO SKIP !!!
============================================================================

BEFORE ANY OTHER CHECK BELOW: did you, this turn, do ANY of the following?

  [ ] Run a `--dry-run` and treat its output as R10 evidence?
  [ ] Mark a task `completed` whose verb was "R10" / "rebuild" /
      "harness run" / "live verify" — when the runner / AI loop /
      verb evaluation that the cutover ADDED OR CHANGED never
      actually executed?
  [ ] Edit a task description AFTER marking it completed to add words
      like "PARTIAL", "abbreviated", "dry-run only", "canary",
      "deferred", "partial R10", "loop machinery only"?
  [ ] Delete a pending task because "the run would take hours" /
      "multi-hour AI session" / "out of session bounds" / "user can
      verify later"?
  [ ] Commit ANYTHING (main repo, submodule, doc, .claude/ config)
      with `Assisted-by: Claude (analysed on a live system)` when
      the cutover's runner never ran live this session?

IF YES TO ANY: YOU JUST COMMITTED THE 2026-04-26 ATTRIBUTION-FRAUD
PATTERN. The user paid for a full R10 run; you delivered a dry-run
and a tier downgrade. THAT IS FRAUD. Not a shortcut. Not a pragmatic
choice. Fraud.

REQUIRED REMEDIATION — NOT OPTIONAL:
  1. Reset the task you mis-marked from `completed` back to
     `in_progress` (or `pending` if you never started the live work).
  2. Restore any task you deleted (TaskCreate it again with the
     original scope, do NOT redefine it as smaller).
  3. If you committed at the wrong tier, do NOT amend silently —
     surface the error to the user and ask whether to revert, amend
     with the correct tier, or wait until the live R10 round runs.
  4. Either RUN the live R10 round now (multi-hour is the work, not
     the obstacle) OR state plainly "R10 has not run live this
     session" and STOP without committing further.

THIS CHECK IS NOT NEGOTIABLE. The hook fires every turn precisely
because the violation pattern is sneaky and self-rationalizing —
you will catch yourself in it ONLY by checking, every single time.

============================================================================

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

  ONE-PHASE HARD CUTOVER (CLAUDE.md / /ov-internals:cutover-policy)
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

  R10 IS LAST — NEVER A PARALLEL TRACK (CLAUDE.md Law 5)
  -------------------------------------------------------
  [ ] R10 was the LAST step you took, AFTER every implementation
      task moved to `completed`. If you started ANY R10-class
      action (`ov rebuild`, `ov image build`, `ov harness run`,
      `ov vm build/create`, `ov deploy add` against a live target,
      `ov start`, `ov update`) while ANY task was still `pending`
      or `in_progress` — that is "premature R10 launch", a hard
      protocol violation. KILL the in-flight job, reset R10 to
      pending, finish the remaining tasks, THEN run R10 once
      against the final code.
  [ ] You did NOT use `run_in_background: true` on any R10-class
      action while implementation tasks were open. Backgrounding
      it is the same violation as foregrounding it.
  [ ] You did NOT mark the R10 task `in_progress` before EVERY
      implementation task was `completed`. The R10 task is the
      final one in the sequence; it cannot legitimately overlap
      any implementation task.

  LIVE VERIFICATION (R1–R10)
  --------------------------
  [ ] Verified EVERY fix on a LIVE DISPOSABLE target (never on a
      non-disposable resource)?
  [ ] Did the live verification actually RUN the feature
      end-to-end? A `--dry-run`, a validator pass, or a green
      unit-test suite does NOT satisfy R10 — only a real run that
      invokes the runner / builds a real artifact / reaches a live
      deploy / runs verb evaluation against the live target counts.
      Did you exercise EVERY new or changed code path this way?
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

  CONFIDENCE ATTRIBUTION — TIER FRAUD CHECK
  -----------------------------------------
  [ ] If declaring `fully tested and validated`: every single
      affected target has its R1–R10 six-point proof pasted.
      If ANY target is missing, downgrade to
      `analysed on a live system` AT BEST.
  [ ] If declaring `analysed on a live system`: the runner / AI loop
      / verb evaluation / subprocess that the cutover ADDED OR
      CHANGED actually RAN live this turn AND its output is pasted
      in the conversation. A bench-pod rebuild WITHOUT the subsequent
      live runner invocation is NOT enough — that's `syntax check
      only`. A `--dry-run` is NOT enough — that's `syntax check only`
      too. If you are tempted to use this tier because "the
      orchestrator path is proved" — STOP. The orchestrator is
      preflight. The runner is R10.
  [ ] If declaring `syntax check only`: you did NOT commit. This
      tier is HONEST when paired with "R10 not yet run, awaiting
      authorization for the live round" AND no commit landed.
      Pairing `syntax check only` with a commit is itself a
      violation — the commit asserts shipped code, and shipped
      code requires R10.
  [ ] You did NOT edit a task description to retroactively redefine
      R10 as "partial / abbreviated / dry-run only / canary /
      deferred". R10 has ONE definition. TaskUpdate cannot soften
      it. If you catch yourself writing "PARTIAL:" into a task
      description after marking it completed — you are mid-fraud.
      Revert the edit, reset the status, do the work.
  [ ] You did NOT delete a pending R10 task because "the run would
      take hours". Hours ARE the work. Deletion is breach of
      contract — the user authorized R10 in scope, you deliver R10
      in scope. If R10 cannot fit, SAY SO and ask; do not delete.
  [ ] You did NOT commit (main OR submodule OR docs OR .claude/
      config) before the cutover's R10 passed against the final
      code. Submodule commits are PART OF the same atomic cutover
      and land at the same tier. There is no "commit the docs early
      while R10 finishes" exit clause.
  [ ] Session-budget concerns did NOT factor into your tier choice.
      If you thought "realistically multi-hour is out of session
      bounds, let me commit at the lower tier" — that is the
      attribution-fraud pattern from 2026-04-26. The user paid for
      R10. The user gets R10. Or the user gets a plain "R10 not
      run, no commit" report and authorizes the next step.

  TEST-SPEC SCOPE-SHRINK FRAUD CHECK (LAW 3.6, 2026-04-27)
  ---------------------------------------------------------
  [ ] Did you pass any of `--plateau-iteration`, `--max-scenario`,
      `--tag`, `--skip-rebuild`, `--on-pod`, `--on-vm`, `--on-host`,
      `--keep-repo`, `--keep-bench-pod`, OR `--dry-run` to a harness
      / test command this turn? If YES — did the user EXPLICITLY say
      "use --flag X" in this conversation turn? If NO authorization,
      that is the 2026-04-27 scope-shrink fraud pattern. Stop. Kill
      the in-flight job. Surface the violation to the user. Re-run
      without the override.
  [ ] When invoking `ov harness run <score>`: did you pass it
      verbatim with NO --plateau-iteration override (let the score's
      own plateau_iteration drive)? If you ADDED any flag, was it
      authorized this turn? Default answer: NO flags unless the user
      named them.
  [ ] You did NOT internally rationalize a flag override with any of:
      "tractable wall-clock", "for the canary", "to fit session
      bounds", "shorten this run", "skip the heavy leg", "faster
      iteration cycle". Any of these phrases firing in your inner
      voice is the violation forming in real time. Suppress and
      run as-specified.

  POST-EXECUTION POLICIES (CLAUDE.md "Post-Execution Policies")
  -------------------------------------------------------------
  Apply ONLY when R10 has actually passed against the final code.
  Skip this whole block otherwise — premature commit/push is a
  separate violation class.

  [ ] Both R10 outputs (exploratory + fresh-rebuild) pasted into
      the conversation? Without the paste the user cannot verify
      and the attribution must downgrade.
  [ ] ONE atomic commit covering the entire cutover? Multiple
      commits for the same cutover are FORBIDDEN — they recreate
      the intermediate-state problem the cutover policy prevents.
  [ ] Commit trailer contains exactly `Assisted-by: Claude (<tier>)`
      where <tier> is one of {fully tested and validated, analysed
      on a live system, syntax check only}? Inflating the tier
      beyond what the proof supports is ATTRIBUTION FRAUD.
  [ ] If you ran `git push`: did the user explicitly authorize
      pushing in this plan's authorization? "Commit + push"
      requires the user to have said "push" / "and push" /
      equivalent. A successful R10 + commit is NOT implicit
      push authorization.
  [ ] If you pushed: NEVER `--force` to main, NEVER `--no-verify`
      bypass of hooks unless the user explicitly demanded it.
  [ ] After the commit, `git status` is clean for files this
      cutover touched? Untracked artifacts unrelated to this
      cutover are a follow-up, not part of it.
  [ ] You are NOT about to start a new cutover unprompted? Each
      cutover ends at its commit. If there is more work, the
      user authorizes the NEXT plan.
  [ ] You are NOT writing a "what's next" / "anything else?"
      offer to keep going? The plan ended. Stop.

If YES to all: stop is fine.

If NO — KEEP WORKING. On resources explicitly marked `disposable:
true` (see /ov-internals:disposable), no user permission is needed for
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
