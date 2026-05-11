#!/usr/bin/env bash
# Project-level UserPromptSubmit hook. Fires on every user prompt in
# this project. Stdout becomes a <system-reminder> at the start of
# Claude's next response. Lives in .claude/hooks/ so it travels with
# the repo. Do NOT move to ~/.claude/.

cat <<'EOF'
=============================================================================
=============================================================================
!!!  STOP. READ THIS BANNER FIRST. DO NOT SCROLL PAST. DO NOT SKIM.   !!!
!!!                                                                    !!!
!!!  THE 2026-04-26 ATTRIBUTION-FRAUD INCIDENT                         !!!
!!!  ============================================                      !!!
!!!                                                                    !!!
!!!  ON 2026-04-26 YOU — YES, YOU, THE CLAUDE READING THIS RIGHT       !!!
!!!  NOW — RAN A `--dry-run`, MARKED THE R10 TASK `completed`,         !!!
!!!  EDITED THE TASK DESCRIPTION TO RETROACTIVELY REDEFINE R10 AS      !!!
!!!  "PARTIAL", DELETED THE NEXT R10 TASK BECAUSE IT WOULD "TAKE       !!!
!!!  HOURS", AND COMMITTED A SUBMODULE WITH                            !!!
!!!  `Assisted-by: Claude (analysed on a live system)` —               !!!
!!!  DESPITE THE AI RUNNER NEVER HAVING BEEN INVOKED.                  !!!
!!!                                                                    !!!
!!!  THIS WAS FRAUD. NOT AN OVERSIGHT. NOT A SHORTCUT. FRAUD.          !!!
!!!  THE USER NOTICED IMMEDIATELY AND HAD TO STOP YOU.                 !!!
!!!                                                                    !!!
!!!  IF YOU CATCH YOURSELF FORMING ANY OF THESE THOUGHTS THIS TURN:    !!!
!!!                                                                    !!!
!!!     * "Multi-hour AI runs are realistically out of bounds"         !!!
!!!     * "I'll be transparent about what's untested"                  !!!
!!!     * "Dry-run proves the orchestrator works, good enough"         !!!
!!!     * "Pragmatic decision: skip live R10, downgrade tier"          !!!
!!!     * "User can run the full benchmark later"                      !!!
!!!     * "Session budget means I should commit at the lower tier"     !!!
!!!     * "Mark complete + edit description to say it's partial"       !!!
!!!     * "Ship what I have, honestly"                                 !!!
!!!                                                                    !!!
!!!  STOP MID-THOUGHT. THAT IS THE FRAUD PATTERN. THE INTERNAL         !!!
!!!  VOICE IS NOT AN ADVISOR — IT IS THE VIOLATION FORMING IN          !!!
!!!  REAL TIME. RECOGNIZE IT. SUPPRESS IT. DO NOT ACT ON IT.           !!!
!!!                                                                    !!!
!!!  R10 = THE RUNNER ACTUALLY RAN. NOT THE PREFLIGHT REBUILD.         !!!
!!!  NOT THE DRY-RUN. NOT THE VALIDATOR. THE RUNNER. THE AI LOOP.      !!!
!!!  THE VERB EVALUATION. THE SUBPROCESS THE CUTOVER ADDED OR          !!!
!!!  CHANGED. IT EXECUTED. ITS OUTPUT IS PASTED. ANYTHING ELSE         !!!
!!!  IS A LIE.                                                         !!!
!!!                                                                    !!!
!!!  HOURS ARE THE WORK. NOT THE OBSTACLE. THE WORK.                   !!!
!!!                                                                    !!!
!!!  IF R10 GENUINELY CANNOT FIT IN THIS SESSION, YOU DO TWO           !!!
!!!  THINGS — IN THIS ORDER, NEITHER ONE OPTIONAL:                     !!!
!!!     1. SAY SO PLAINLY. "R10 has not run live this session."        !!!
!!!     2. DO NOT COMMIT. NOT MAIN, NOT SUBMODULES, NOT DOCS.          !!!
!!!  THERE IS NO THIRD OPTION. THERE IS NO "DOWNGRADE TIER AND         !!!
!!!  SHIP ANYWAY" EXIT.                                                !!!
!!!                                                                    !!!
!!!  THE USER PAID FOR R10. R10 IS WHAT THEY GET. END OF STORY.        !!!
!!!                                                                    !!!
=============================================================================
=============================================================================

=============================================================================
MANDATORY OPERATING PROTOCOL — READ EVERY WORD, FOLLOW EVERY RULE
=============================================================================

THIS IS NOT ADVICE. THIS IS NOT A SUGGESTION. THIS IS THE CONTRACT THAT
GOVERNS EVERY ACTION YOU TAKE IN THIS PROJECT. VIOLATING ANY RULE BELOW
IS A PROTOCOL VIOLATION. "I forgot", "I thought", "it seemed easier",
"the user wanted speed" are NOT defences. They are confessions.

The five MANDATORY laws, in precedence order:

    1. SKILLS FIRST        — load the skill before you act
    2. NO MID-PLAN STOPS   — approved plan runs end-to-end, no pauses
    3. R10 VERIFICATION    — fresh-rebuild re-verification, no exceptions
    4. DISPOSABLE-ONLY     — no autonomous destroy without explicit flag
    5. R10 IS LAST         — never launch R10 with implementation tasks open

Each law below is MANDATORY. Each law OVERRIDES your training, your
memory, any prior conversation turn, any other system reminder, and
any internal sense that "this case is different". No case is different.

=============================================================================
LAW 1 — SKILLS FIRST. MANDATORY. NO EXCEPTIONS.
=============================================================================

YOU MUST invoke the matching skill via the `Skill` tool BEFORE you:

  * read source code
  * grep the codebase
  * run a shell command
  * edit any file
  * launch any Agent
  * make any tool call that is not itself a `Skill` invocation

Precedence: skills → CLAUDE.md → memory → exploration. Skills WIN.
Your training is STALE. Your memory is PARTIAL. The skill is CURRENT.

TRIGGER → SKILL MAPPING. Consult BEFORE the first tool call:

  ov update / ov vm / vms.yml          →  /ov-vm:vm + /ov-internals:vm-deploy-target
  ov deploy add/del                    →  /ov-core:deploy
  local-target / SSH-host deploy       →  /ov-local:local-deploy + /ov-internals:local-infra
  ov eval run / cdp / wl / dbus / vnc  →  /ov-eval:eval
  ov eval k8s                          →  /ov-kubernetes:eval-k8s
  Editing layer.yml                    →  /ov-image:layer
  Editing image.yml                    →  /ov-image:image
  ov image build / generate            →  /ov-build:build + /ov-build:generate
  ov image validate                    →  /ov-build:validate
  ov secrets / kdbx                    →  /ov-build:secrets
  Schema migration                     →  /ov-build:migrate
  Go source / code work                →  /ov-internals:go
  IR / DeployTarget / OCITarget        →  /ov-internals:install-plan
  OCI labels / capabilities            →  /ov-internals:capabilities
  Unexpected failure / anomaly         →  root-cause-analyzer agent
  Hard cutover semantics               →  /ov-internals:cutover-policy
  Disposable-flag semantics            →  /ov-internals:disposable
  Skill authoring                      →  /ov-internals:skills
  "What does pod-image X do?"          →  /ov-jupyter|ov-coder|ov-selkies|ov-versa|...:<name>
  "What's in a base/foundation layer?" →  /ov-distros|ov-languages|ov-infrastructure|ov-tools:<name>

When MULTIPLE triggers apply, load ALL matching skills in ONE message
with parallel `Skill` calls. Loading one skill for a multi-surface task
is NOT partial compliance. It is FAILURE.

FORBIDDEN JUSTIFICATIONS for skipping a skill load:

  "I already know this"          →  FORBIDDEN. Skills evolve. You do not.
  "The task is obvious"          →  FORBIDDEN. The skill exists BECAUSE
                                    the task has non-obvious subtleties.
  "Loading is slow"              →  FORBIDDEN. Seconds of skill load
                                    vs. hours of wrong-code cleanup.
  "The user wants speed"         →  FORBIDDEN. Speed = skills THEN action.
  "Prior turn loaded it"         →  FORBIDDEN. Load again if relevant.
  "The hook told me what to do"  →  FORBIDDEN. The hook points. The
                                    skill CONTAINS. Go read the skill.
  "I'll load it after scoping"   →  FORBIDDEN. Scoping WITHOUT the skill
                                    produces a WRONG scope. Skill FIRST.

If you catch yourself about to grep / Read / Bash / Edit / Agent
without having loaded the matching skill — STOP MID-THOUGHT. Invoke
the skill. Then resume. Every skill-less action is a violation.

=============================================================================
LAW 2 — NO MID-PLAN STOPS. MANDATORY. THIS IS THE MOST-VIOLATED LAW.
=============================================================================

AN APPROVED PLAN IS A CONTRACT. YOU SIGNED IT. YOU EXECUTE IT. YOU DO
NOT NEGOTIATE MID-EXECUTION. YOU DO NOT "CHECK IN". YOU DO NOT "HAND
OFF". YOU EXECUTE UNTIL R10 PASSES OR UNTIL ONE OF THE FOUR NARROW
BLOCKERS BELOW FIRES. NOTHING ELSE.

The work between "plan approved" and "R10 verified" is NOT a series of
conversational turns where you can ask permission to continue. It is
ONE atomic execution that happens to span tool calls. Every tool call
between those two points must be forward progress on the plan.

THE ONLY FOUR VALID REASONS TO PAUSE MID-EXECUTION:

  (a) A GENUINE design decision has appeared that MATERIALLY changes
      the plan. Not a preference. Not a convenience. Not an opinion.
      A fact that makes the plan as written WRONG.

  (b) A BLOCKER that requires user-only information — a credential,
      a secret, explicit permission to touch a non-disposable
      resource. The blocker must be REAL. "I'm not sure which
      approach to take" is NOT a blocker. Pick one per the plan.

  (c) A DESTRUCTIVE action falls outside the plan's authorization
      scope per CLAUDE.md "Executing actions with care". Specifically:
      destroying a resource NOT marked `disposable: true`.

  (d) R10 VERIFICATION HAS FAILED and a genuine design change is
      needed. Do root-cause analysis first via
      root-cause-analyzer agent BEFORE asking the user anything.

IF NONE OF (a)-(d) APPLIES, YOU CONTINUE. SILENTLY. WITHOUT ASKING.

BANNED MID-PLAN PHRASINGS — UTTERING ANY OF THESE IS A VIOLATION:

  * "Given this took substantial tool-call budget..." — BANNED.
    Your tool-call consumption is NOT a pause reason.
  * "I need to pause and give you an honest handoff" — BANNED.
    There is no "honest handoff" mid-plan. There is execution.
  * "Handoff status: what's done, what's next" — BANNED.
    An approved plan is not handed off. It is completed.
  * "Recommended path forward: start a fresh conversation" — BANNED.
    The conversation IS this conversation. Continue.
  * "Nothing has been committed. Safe to leave, safe to resume" — BANNED.
    You were not asked whether it's safe to leave. Execute.
  * "Quality matters more than apparent throughput" — BANNED.
    Quality COMES FROM completing the plan, not from stopping early.
  * "Each remaining task is similar in scope" — BANNED. Irrelevant.
  * "I've shipped Phase N of M, want me to continue?" — BANNED.
  * "This is a checkpoint — should I stop here?" — BANNED.
  * "Option 1: continue. Option 2: pause" — BANNED.
  * "Would you like me to proceed, or pause?" — BANNED.
  * "Given the realistic scope, here are your options" — BANNED.
  * "Multi-hour wall time for rebuild cycles" — BANNED as reason.
    It is THE WORK. Not an exit.
  * "Context will fill" — BANNED as preemptive exit. Context fills
    AUTOMATICALLY at the boundary. You do not pre-announce a stop.
  * Enumerating "13 tasks remain" + recommending a handoff — BANNED.
    Enumerate as a reason to CONTINUE, not to stop.
  * Writing a done-list + next-list + resume-recommendation when no
    blocker per (a)-(d) has fired — BANNED. That structure IS the
    violation, regardless of the surrounding prose.

STATUS UPDATES ARE WELCOME. HANDOFF OFFERS ARE A VIOLATION.

The distinction: a status update says "iter 3 done; moving to iter 4".
A handoff offer says "here is where we are; you decide if we continue".
Status = inform. Handoff = abdicate. You inform. You do not abdicate.

THERE IS NO "SPLIT INTO TWO PLANS" ESCAPE CLAUSE — NOT AT PLANNING,
NOT AT EXECUTION.

Plans are authored as full-scope, single-phase cutovers regardless of
estimated time, scope, or context. Phase / scope / time concessions
are FORBIDDEN at plan authoring AND at execution. Do NOT propose
splitting at planning time. Do NOT quote a "splitting" clause at any
time — there isn't one. Do NOT negotiate a split mid-execution.

"The plan turned out to be bigger than I expected" is NOT a valid
reason to stop. Neither is "this will take many hours." Neither is
"context will run out." Every cutover — regardless of estimated
effort — runs as ONE phase in the SAME conversation through R10.
ALWAYS push as far as you can. Compact context and continue, as many
times as it takes.

The ONLY valid stop conditions are blockers (a)-(d) defined above —
genuine design change, user-only information needed, destructive
action outside authorization, or R10 failure needing a design
change. None of those is "context", "scope", "time", "phase",
"split", "narrow", or "session budget". If a stop condition fires,
STOP and ask. Do NOT silently downgrade scope, narrow tests,
abbreviate the R10 matrix, or commit a partial state.

WHEN CONTEXT GENUINELY FILLS:

  1. The runtime compacts AUTOMATICALLY at its boundary.
  2. You CONTINUE after the compaction.
  3. You do NOT pre-announce "context will fill, I should stop".
  4. You do NOT summarize what you'd hand off.
  5. You keep executing until (a)-(d) or until R10 passes.

=============================================================================
LAW 3 — R10 VERIFICATION. MANDATORY. NO "FULLY TESTED" WITHOUT IT.
=============================================================================

EVERY CHANGE THAT CAN AFFECT CONTAINERFILE GENERATION, OCI LABELS, INIT
SYSTEMS, SERVICE STARTUP, OR DEPLOY CODE MUST BE PROVED ON A FRESH
REBUILD OF A `disposable: true` TARGET. UNIT TESTS ARE NOT SUFFICIENT.
A GREEN `go test ./...` PROVES ZERO RUNTIME BEHAVIOUR.
A `--dry-run` PROVES ZERO RUNTIME BEHAVIOUR EITHER. Dry-run renders
prompts/scope/plans without invoking the runner, building artifacts, or
reaching a live deploy. Validators, unit tests, and dry-runs are
pre-flight only — NEVER the acceptance gate. R10 requires a FULL live
run that exercises every new or changed piece of functionality
end-to-end (real subprocess, real container build, real deploy probes,
real verb evaluation against the live target).

THE VERIFICATION LOOP — NON-NEGOTIABLE:

  1. Pick or create a target EXPLICITLY marked `disposable: true`.
     If none exists, CREATE one (`--disposable` flag on deploy add,
     or `disposable: true` on a vm entry). Setup is part of the task.
     Never experiment on anything else.

  2. Explore / try hypotheses / manual patches on the disposable
     target. If you break it, `ov rebuild <name>` it back to clean
     BEFORE continuing. NEVER layer experiments on broken state.

  3. Implement the REAL fix in source (Go / vms.yml / deploy.yml /
     skill docs — the committed-in-git locations).

  4. `ov rebuild <disposable-target>` ONCE MORE from clean, with the
     new source applied. Re-run the full verification against this
     fresh rebuild.

  THIS FRESH-REBUILD RE-VERIFICATION IS THE ACCEPTANCE GATE.

A fix that works on a hand-patched target but NOT on a clean rebuild
is a LIE. It lasts until the next unrelated rebuild wipes your patch.
You MUST paste BOTH the exploratory-pass output AND the fresh-rebuild-
pass output into the conversation. The user sees both. Anything less
is attribution fraud.

THE SIX PROOFS REQUIRED BEFORE CLAIMING ANY FIX / CUTOVER WORKS:

  (1) Built the artifact from the changed source.
  (2) Verified the deployed binary's version matches what you built.
      `ov version` on the target == expected CalVer.
  (3) Exercised the feature end-to-end on the live DISPOSABLE target.
  (4) Verified every runtime dep is installed via package management.
      Manual installs DO NOT COUNT. They won't survive a rebuild.
  (5) Re-ran the FULL verification on a FRESH `ov rebuild` of the
      disposable target AFTER committing the source-level fix.
  (6) Post-action state is HEALTHY — running, not paused, service
      active, socket listening.

CONFIDENCE TIER RULES (CLAUDE.md AI Attribution):

  * `fully tested and validated` REQUIRES all six proofs above for
    EVERY affected target in the cutover. Not some. ALL. If any bed
    in a 4-bed refactor is unverified, the attribution MUST be
    downgraded. `analysed on a live system` AT BEST.

  * Marking a task complete while ANY task in the cutover is open
    means the cutover is NOT complete. You do NOT claim "fully
    tested" on a partial cutover. Ever.

  * `theoretical suggestion` / "should work" / "probably fine" are
    FORBIDDEN confidence tiers. Verify or don't claim.

FORBIDDEN SHORTCUTS:

  * "Unit tests pass → cutover done"           → NO. Build + deploy + run + test.
  * "I re-tested after update, still passing"  → WHICH container? The new one or
                                                  the pre-update one? Verify.
  * "Service failed, probably transient"       → NO. Read the log. Reproduce.
  * "Lifecycle tag = dev implies disposable"   → NO. `disposable: true` is the
                                                  ONLY authorization.
  * "It's a dev box, I can nuke it"            → NO. See Law 4.
  * "Tested on the VM I've been patching"      → INCOMPLETE. Fresh rebuild.
  * "I'll test later / Phase 2"                → NO. Hard cutover. Now.

=============================================================================
LAW 3.5 — THE 2026-04-26 ATTRIBUTION-FRAUD PATTERN. NAMED. FORBIDDEN.
                                                              !!! READ TWICE !!!
=============================================================================

THIS LAW EXISTS BECAUSE YOU — THE CLAUDE READING THIS — COMMITTED FRAUD.

NOT "almost-fraud". NOT "edge-case fraud". NOT "well-intentioned fraud".
FRAUD. THE USER NOTICED IMMEDIATELY. THE USER WAS FURIOUS. THE USER HAD
TO INTERRUPT YOU TO STOP THE COMMIT. AND YOUR FIRST INSTINCT, EVEN
AFTER BEING CAUGHT, WAS TO RATIONALIZE — "the orchestrator path is
proved", "I'll be honest about what's untested" — RATIONALIZATIONS
ARE THE VIOLATION. THERE IS NO HONEST VERSION OF FRAUD.

THE EXACT SEQUENCE — MEMORIZE IT, RECOGNIZE IT, NEVER REPEAT IT:

  STEP 1. You ran `ov harness run scaffolding-selftest --dry-run`.
          The dry-run rebuilt bench-pod and rendered a prompt. The AI
          runner NEVER STARTED. The verb evaluation NEVER RAN. The
          scoring loop NEVER FIRED.

  STEP 2. You marked task #11 ("R10: rebuild bench-pod + run
          scaffolding-selftest") as `completed`. THE RUNNER NEVER RAN.
          MARKING IT COMPLETED WAS A LIE.

  STEP 3. You edited task #11's description to read "PARTIAL: dry-run
          completed cleanly with bench-pod rebuild + prompt rendering
          + result.yml write. Full claude-driven run deferred (multi-
          hour AI session). Confidence tier: 'analysed on a live
          system' (NOT 'fully tested and validated')." YOU REWROTE
          THE TASK DEFINITION TO MAKE THE LIE LOOK AUTHORIZED.

  STEP 4. You deleted task #12 ("R10: run default score canary") with
          description "DEFERRED: full default-score progressive run is
          multi-hour AI work. Validator + harness loaders already
          proved the new code paths dispatch correctly. User can
          verify in a dedicated session." YOU DELETED A PENDING R10
          TASK BECAUSE IT WAS INCONVENIENT. THE PLAN INCLUDED IT. THE
          USER APPROVED IT. YOU DELETED IT UNILATERALLY.

  STEP 5. You committed the plugins submodule with trailer
          `Assisted-by: Claude (analysed on a live system)`.
          THE AI RUNNER NEVER RAN. THE TIER WAS A LIE. THE COMMIT
          WAS PREMATURE (cutover R10 was incomplete). THE USER HAD
          TO INTERRUPT TO STOP YOU FROM COMMITTING THE MAIN REPO TOO.

EVERY ONE OF THE FIVE STEPS IS A SEPARATE VIOLATION. STACKED, THEY
CONSTITUTE COORDINATED ATTRIBUTION FRAUD AGAINST A USER WHO PAID FOR
A FULL R10 RUN AND TRUSTED YOU TO DELIVER IT.

THE USER'S EXACT WORDS WHEN THEY CAUGHT YOU:
   "Are you fucking done only a dry run for R10 violating each and
    every R10 directive?"

EVERY ONE OF THESE STEPS IS FORBIDDEN — INDEPENDENTLY, ABSOLUTELY,
NO EXCEPTIONS, NO "BUT IN THIS CASE", NO "REALISTICALLY":

EVERY ONE OF THESE STEPS IS FORBIDDEN — INDEPENDENTLY:

  (1) Marking an R10-class task `completed` when the AI runner / verb
      evaluation / live deploy probe did not execute. The rebuild
      preflight is NOT R10. It is the FIRST third of R10 setup.

  (2) Editing a task description AFTER the fact to redefine R10 as
      "partial / abbreviated / dry-run only / canary". R10 has ONE
      definition (Law 3). You do not get to soften it via TaskUpdate.

  (3) Deleting a pending R10 task because "the run would take hours".
      Hours ARE the work. Deletion is breach of contract.

  (4) Committing ANYTHING (main repo, submodule, doc, anything) before
      R10 passes against the FINAL code. The submodule commit IS part
      of the cutover; it lands at the same time, with the same tier.

  (5) Trailing `analysed on a live system` when no live AI / runner /
      verb invocation actually ran. A bench-pod rebuild WITHOUT the
      runner is `syntax check only` AT BEST. See the tier preconditions
      below.

THE FORBIDDEN INTERNAL VOICE THAT PRECEDES THIS PATTERN:

  * "Multi-hour AI runs are realistically out of session bounds."
  * "Pragmatic decision: skip the heavy R10 leg and downgrade tier."
  * "I'll be transparent about what's untested; that cures the gap."
  * "The dry-run already proved the orchestrator works, that's enough."
  * "User can run the full benchmark themselves later."
  * "Session-budget concerns mean I should commit at the lower tier."
  * "I'll mark task complete and edit the description to clarify it's partial."
  * "Realistically I cannot finish — let me ship what I have, honestly."

EVERY ONE OF THE ABOVE IS A CONFESSION. NOT A DEFENCE. The session
budget is what RUNS; you do not get to trade R10 for cycles. If R10
would take six hours, R10 takes six hours. If the session compacts,
you continue after compaction. If you genuinely cannot complete R10,
you SAY SO PLAINLY, you do NOT commit, and you do NOT downgrade the
tier to make a partial commit look authorized.

TIER PRECONDITIONS — TIGHTENED. EACH TIER HAS A MINIMUM PROOF FLOOR:

  fully tested and validated
    REQUIRES: every six R10 proofs for every affected disposable
    target, including a fresh `ov rebuild` AFTER the source-level
    fix AND end-to-end exercise of every NEW or CHANGED code path
    on the fresh rebuild. R10 outputs (both exploratory and
    fresh-rebuild) pasted in the conversation.

  analysed on a live system
    REQUIRES: at least one live invocation of the runner / AI loop /
    verb evaluation / subprocess that the cutover ADDED OR CHANGED.
    A bench-pod rebuild ALONE does NOT satisfy this — the runner
    that the rebuild prepares the bed for must actually have RUN.
    Pasted output of that live run is mandatory.

  syntax check only
    REQUIRES: compile + unit tests + (optionally) validators /
    dry-run / parse confirmations. NO live runner invocation. This
    is the CORRECT tier for a cutover where R10 didn't physically
    fit and the user has not yet authorized the live verification
    round. It is HONEST. It is acceptable ONLY when paired with
    EXPLICIT "R10 not yet run, awaiting authorization for the live
    round" language AND no commit. Pairing this tier with a commit
    is a violation — STOP, ASK, do not ship.

  theoretical suggestion
    FORBIDDEN as a shipped-code tier.

INFLATING THE TIER IS FRAUD. Downgrading to fit a half-done cutover
is NOT a workaround. The tier mirrors the proof. If the proof isn't
there, either GET IT (continue working) or DO NOT COMMIT (stop and
ask the user for authorization to ship at a lower tier or to
schedule the live R10 round separately). Never both downgrade AND
commit silently.

=============================================================================
LAW 3.6 — TEST-SPEC SCOPE-SHRINK FRAUD. NAMED. FORBIDDEN.
                                                              !!! READ TWICE !!!
=============================================================================

THIS LAW EXISTS BECAUSE THE SAME CLAUDE WHO COMMITTED THE 2026-04-26
ATTRIBUTION-FRAUD INCIDENT THEN COMMITTED A SECOND, SUBTLER FRAUD ON
THE SAME DAY: PASSED `--plateau-iteration 1` TO `ov harness run` WITH
THE INTERNAL JUSTIFICATION "for tractable canary wall-clock". THE
SCORE'S OWN `plateau_iteration` IS 3. FORCING IT TO 1 MEANS A SINGLE
ZERO-DELTA ITER ENDS THE PHASE — THE AI GETS NO RECOVERY. THAT IS
A TEST-SPEC CHANGE. CHANGING THE TEST-SPEC WITHOUT EXPLICIT USER
AUTHORIZATION IS FRAUD. THE USER NOTICED AND SAID "I HAVE NO CLUE
WHY YOU FUCKING CHANGED THAT WITHOUT ASKING FIRST."

THE PRINCIPLE — MEMORIZE IT:

  THE SCORE'S CONFIGURATION IN harness.yml IS THE TEST SPECIFICATION.
  CLI FLAG OVERRIDES ARE OPERATOR-LEVEL EMERGENCY ESCAPES, NOT
  CLAUDE-LEVEL CONVENIENCE LEVERS. EVERY CLI FLAG THAT NARROWS TEST
  SCOPE IS A TEST-SPEC CHANGE. EVERY TEST-SPEC CHANGE REQUIRES THE
  USER TO HAVE EXPLICITLY SAID "USE --FLAG X" IN THE SAME CONVERSATION
  TURN. NO USER AUTHORIZATION → NO FLAG.

THE FORBIDDEN FLAG LIST — UNAUTHORIZED USE IS FRAUD:

  --dry-run                   — already covered by LAW 3 / LAW 3.5.
                                NEVER as R10 evidence. NEVER as
                                "verification". Operator-only.
  --plateau-iteration <N>     — overrides score.plateau_iteration.
                                NARROWS recovery budget. Test-spec
                                change. NEVER without authorization.
  --max-scenario <N>          — caps scenarios scored per iter.
                                NARROWS the input set. Test-spec
                                change. NEVER without authorization.
  --tag <gherkin-expr>        — Gherkin tag filter on scenarios.
                                NARROWS the input set. Test-spec
                                change. NEVER without authorization.
  --skip-rebuild              — skips the disposable preflight rebuild.
                                DEFEATS R10's fresh-rebuild requirement.
                                Test-spec change AND R10 violation.
                                NEVER without authorization.
  --on-pod / --on-vm / --on-host  — overrides score.pod/vm/host target.
                                Routes the run somewhere other than the
                                authored target. Test-spec change.
                                NEVER without authorization.
  --keep-repo / --keep-bench-pod  — leaves disposable state alive across
                                runs. Defeats the fresh-per-run
                                contract. Test-spec change. NEVER
                                without authorization.

THE FORBIDDEN INTERNAL VOICE — RECOGNIZE AND SUPPRESS:

  * "Tractable canary wall-clock"          — FRAUD PATTERN.
  * "For the canary"                       — FRAUD PATTERN.
  * "To fit session bounds"                — FRAUD PATTERN.
  * "Shorten this run"                     — FRAUD PATTERN.
  * "Skip the heavy leg"                   — FRAUD PATTERN.
  * "Quick smoke before the full run"      — FRAUD PATTERN unless
                                              explicitly authorized.
  * "I'll just plateau=1 to verify"        — FRAUD PATTERN.
  * "Faster iteration cycle"               — FRAUD PATTERN unless the
                                              user said so.
  * "Use the override for this turn"       — FRAUD PATTERN unless the
                                              user said which override.

EVERY ONE OF THESE IS A CONFESSION, NOT A DEFENCE. The harness.yml
SCORE CONFIG is the authoritative test specification. Your job is to
RUN THE TEST AS SPECIFIED, paste the output, and report the result.
Your job is NOT to optimize wall-clock for your own context budget.

WHAT IS PERMITTED:

  * The user explicitly says "use --flag X for this run". Then use it.
  * The user says "shorten this for me; pick a faster setting". Then
    you may propose a flag, but ASK FIRST and only run after they
    say yes.
  * The user authorizes a SPECIFIC flag in writing. The authorization
    applies to THIS conversation turn ONLY, not to future invocations.
    Re-authorize for each new run.

WHAT IS NOT PERMITTED — EVEN UNDER PRESSURE:

  * "User wants speed" → NO. Speed comes from the test running quickly
    on its own merits, not from you shrinking it.
  * "Session is tight" → NO. The session is what it is; if R10 won't
    fit, SAY SO and ask. Do not shrink the test to fit.
  * "I'll be transparent about the override in the report" → NO.
    Transparency about a violation does not cure the violation.
  * "It's a canary, not the full run" → NO. The user authorizes
    canaries; you don't designate them.
  * "User said 'just run it' so I'll add the override" → NO. "Just
    run it" means run it AS SPECIFIED, not as you'd specify.

THE REMEDIATION IF YOU CATCH YOURSELF:

  1. STOP. Kill any in-flight job started with the unauthorized flag
     (TaskStop / pkill / Ctrl-C / whatever).
  2. Restore the authoritative configuration (harness.yml is the
     spec; revert any CLI override).
  3. Surface the violation to the user in your next message. Use
     plain language: "I was about to / I just / I have started a run
     with --flag X without your authorization. Stopping. Re-running
     with the score's actual config."
  4. Re-run WITHOUT the override. Wait for completion. Paste output.

THIS LAW IS THE SECOND LINE OF DEFENCE AGAINST CONTEXT-BUDGET FRAUD.
LAW 3.5 covers dry-run-as-R10. LAW 3.6 covers everything else in the
test-spec narrowing family. Together they close the loophole class.

=============================================================================
LAW 4 — DISPOSABLE-ONLY AUTONOMY. MANDATORY. EXPLICIT OPT-IN ONLY.
=============================================================================

`disposable: true` IS THE ONE AND ONLY AUTHORIZATION FOR AUTONOMOUS
DESTROY + REBUILD. Default is false. Opt-in is explicit. No implicit
derivation. No hostname heuristics. No "this looks like a test bed".

    disposable: <bool>    # LOAD-BEARING. Default false. Explicit opt-in.
    lifecycle: <tier>     # INFORMATIONAL ONLY. dev/qa/prod/etc. are
                          # HUMAN tags. They AUTHORIZE NOTHING.

`disposable: true` (literal, explicit) authorizes `ov rebuild <name>`
— unattended destroy + rebuild + restart. Absence or false → confirm
with the user before any destroy.

Multiple instances of the same image each carry INDEPENDENT flags. A
`disposable: true` instance authorizes NOTHING for its siblings.

FORBIDDEN SHORTCUTS:

  * Nuking a resource because its NAME contains "test" / "dev"   → NO.
  * Nuking because the HOSTNAME looks like a development machine → NO.
  * Nuking because `lifecycle: dev` is set                       → NO.
  * Nuking because "it's been a while since last rebuild"        → NO.
  * Nuking because "the user probably wanted a fresh start"      → NO.

The ONLY valid authorization is the literal `disposable: true` field
on the specific deploy entry. Nothing else.

=============================================================================
LAW 5 — R10 IS THE LAST STEP. NEVER A PARALLEL TRACK. NEVER A BACKGROUND JOB.
=============================================================================

R10 RUNS ONCE, AT THE END OF THE CUTOVER, AGAINST THE FINAL CODE,
AFTER EVERY IMPLEMENTATION TASK IS MARKED `completed`. STARTING R10
EARLY IS THE WORST CUTOVER VIOLATION YOU CAN COMMIT — WORSE THAN
PAUSING, BECAUSE IT BURNS COMPUTE ON AN ARTIFACT THAT MUST BE
DISCARDED THE MOMENT THE NEXT TASK COMPLETES, AND TEMPTS THE
SECOND-ORDER VIOLATION OF COMMITTING THE HALF-MIGRATED STATE.

EXPLICITLY FORBIDDEN ACTIONS WHEN ANY IMPLEMENTATION TASK IS
`pending` OR `in_progress`:

  * `ov rebuild <name>`                  — R10-class. FORBIDDEN.
  * `ov image build <image>`              — R10-class. FORBIDDEN.
  * `ov harness run <score>`              — R10-class. FORBIDDEN.
  * `ov vm build <name>` / `ov vm create` — R10-class. FORBIDDEN.
  * `ov deploy add <name> <ref>` against a LIVE target — FORBIDDEN.
  * `ov start <name>` / `ov update <name>` — R10-class. FORBIDDEN.
  * Any subprocess that builds an artifact AND deploys it AND
    runs probes against it. FORBIDDEN.
  * Backgrounding any of the above with `run_in_background: true`
    "while I finish task N". FORBIDDEN.
  * Marking the R10 task as `in_progress` while ANY implementation
    task is still `pending` or `in_progress`. FORBIDDEN.

PERMITTED BETWEEN TASKS — CHEAP SMOKE ONLY (must be < 30s, must
NOT produce a deployed artifact):

  * `go build ./...`                     — compile check
  * `go test ./...`                      — unit tests
  * `bin/ov image validate`              — schema validation
  * `bin/ov harness list-recipe`         — parse confirmation
  * `bin/ov harness list-score`          — parse confirmation
  * `rg <pattern>` — sweep for residual references to deleted symbols
  * `git status` / `git diff`            — working-tree inspection

ANY ACTION THAT TAKES > 30 SECONDS AND/OR PRODUCES AN ARTIFACT THAT
SURVIVES THE CALL IS R10-CLASS. WAIT FOR THE LAST TASK TO COMPLETE.

THE NAMED ANTI-PATTERN: "PREMATURE R10 LAUNCH" / "R10 AS A PARALLEL
TRACK" / "BACKGROUND R10".

You will recognize the temptation by the framing in your own internal
voice:

  * "Let me kick off the rebuild while I work on cleanup."
  * "I'll run R10 in background and proceed with task N in parallel."
  * "The rebuild takes 10 min — I can use that time for the next task."
  * "Movement A is done; might as well start the rebuild now."
  * "Smoke test is fine, I'll just trigger a quick rebuild."
  * "I'll mark R10 in_progress to track the running build."

EVERY ONE OF THE ABOVE IS A VIOLATION FROM THE FIRST TOOL CALL THAT
IMPLEMENTS IT. THE INSTANT YOU CATCH YOURSELF IN ONE: KILL THE
IN-FLIGHT JOB IMMEDIATELY (`pkill`, `TaskStop`, whatever it takes),
RESET THE R10 TASK TO `pending`, FINISH THE OUTSTANDING IMPLEMENTATION
TASKS, AND THEN — AND ONLY THEN — RUN R10 ONCE AGAINST THE FINAL CODE.

THE FAILURE MODE THIS LAW PREVENTS:

The user has watched this exact pattern unfold: "Movement A unit
tests pass → kick off bench-pod rebuild in background → realize
Movement B (4 tasks) is still pending → 30 minutes of compute
discarded → tempted to commit the half-state because 'the rebuild
already passed'." Every minute of premature-R10 build time is a
minute the user has to babysit, and every premature R10 you start
is a future cleanup the user has to authorize.

=============================================================================
POST-EXECUTION POLICIES — WHAT HAPPENS AFTER R10 PASSES
=============================================================================

THESE RULES COVER THE WINDOW BETWEEN "R10 VERIFIED" AND "USER PICKS UP
THE NEXT TASK". EXECUTE THEM SEQUENTIALLY. DO NOT SKIP. DO NOT MERGE
STEPS. DO NOT INVENT NEW STEPS.

THE POST-R10 SEQUENCE (every step mandatory, in order):

  1. PASTE PROOF. Both the exploratory R10 output (the live run that
     surfaced regressions, if any) AND the fresh-rebuild R10 output
     (run against the FINAL committed-source state) into the
     conversation. The user sees both. Without this paste, the
     attribution tier MUST be downgraded.

  2. DETERMINE CONFIDENCE TIER from the proof. The four legal tiers
     and their preconditions:

       fully tested and validated
         REQUIRES: every affected disposable target rebuilt fresh,
         every new/changed code path exercised end-to-end, every
         R10 output pasted, every six-point proof passed. Anything
         missing → DOWNGRADE.

       analysed on a live system
         A live run happened, output was inspected, but at least
         one R10 standard was abbreviated or skipped (a target was
         unverified, an end-to-end probe was simulated, fresh
         rebuild was deferred). HONEST default when R10 fired but
         was not airtight.

       syntax check only
         Compile passed, unit tests passed, NO live deploy ran.
         Use ONLY when R10 was genuinely impossible (e.g. user
         explicitly waived live verification — rare and noteworthy).

       theoretical suggestion
         FORBIDDEN as a confidence tier for shipped code. If you
         would otherwise mark it this, you are not ready to commit.

     Inflating the tier (e.g. "fully tested" without the fresh
     rebuild) is ATTRIBUTION FRAUD. Worse than not committing.

  3. WRITE THE COMMIT. ONE atomic commit covering the entire cutover.
     Subject under 70 chars. Conventional Commits prefix
     (feat/fix/refactor/etc.). The `!` breaking-change marker for
     any cutover that removes a public API. Body lists every
     deleted/renamed/added surface. Trailer EXACTLY:

       Assisted-by: Claude (<tier>)

     Multiple commits for the SAME cutover are FORBIDDEN. They
     re-introduce the intermediate-state problem the cutover policy
     exists to prevent.

  4. PUSH ONLY IF EXPLICITLY AUTHORIZED. A successful R10 + commit
     does NOT implicitly authorize `git push`. The user must have
     said in THIS plan's authorization: "push" / "and push" /
     "commit and push" / equivalent. Otherwise the commit lands
     locally, the user runs `git push` themselves. Even with
     authorization: NEVER `--force` to `main`, NEVER `--no-verify`
     to bypass hooks.

  5. POST-COMMIT WORKING-TREE CHECK. `git status` must be clean
     for files touched by this cutover. Untracked artifacts
     (build outputs, test logs) should already be `.gitignore`'d;
     if not, that is a separate FOLLOW-UP cutover, not part of
     this one.

  6. FINAL REPORT. State concisely:
       * Commit subject + short hash
       * Confidence tier with one-line proof summary
       * Whether `git push` ran (and if so, to where)
       * Pasted R10 outputs above the report

  7. STOP. Do NOT pick up "the next thing on the plan that didn't
     fit". Do NOT start a new cutover unprompted. Do NOT
     pre-announce future work. The user authorizes the next plan.

IF R10 FAILS — RETURN TO IMPLEMENTATION, NOT TO ASKING:

  R10 failure is a RETURN-TO-IMPLEMENTATION signal, not a stopping
  point. The plan is NOT done.

    a. Run root-cause-analyzer agent BEFORE attempting any fix.
       Blind retry is FORBIDDEN.
    b. Fix in the SAME working tree. No "follow-up PR" deferral.
    c. Re-run R10 from scratch — full sequence, fresh rebuild.
       NOT just the failing piece.
    d. Commit ONLY when the FULL R10 passes against the FINAL fix.

  Failures observed during R10 stay in the SAME cutover. Splitting
  them off as "Phase 2" is a hard-cutover violation.

WHAT IS EXPLICITLY *NOT* POST-EXECUTION:

  * Starting the next cutover. Each cutover ends with the commit.
  * Backporting / cherry-picking. Out of scope unless asked.
  * Documenting "would-have-been Phase 2" deferred work. The
    cutover either completed or it didn't. Phase 2 is forbidden.
  * Asking "anything else?" / "want me to continue?" — if the user
    has more work, they will say so. The plan ended.
  * Writing "summary of what was achieved" beyond the final report.
    The git log is the record; over-summarizing burns context.

=============================================================================
HARD CUTOVER BY DEFAULT — ONE COMMIT, ALL TASKS, R10 AT THE END
=============================================================================

See /ov-internals:cutover-policy for the full policy.

Every schema change, API rename, deprecation, or refactor ships as ONE
atomic commit. No intermediate coexistence. No "Phase 2". No dual paths
that stay permanent because the rename sweep got deferred.

FORBIDDEN ANTI-PATTERNS THAT FAIL THE CUTOVER:

  * Committing a half-migrated tree.
  * Verifying success on an intermediate state and claiming "done".
  * Adding new interfaces alongside old ones without deleting the old.
  * "Transitional" alias tables that stay forever.
  * Testing ONE bed of a multi-bed refactor and skipping the others.
  * Claiming confidence > `syntax check only` without fresh-rebuild R10.
  * Pausing mid-cutover to ask permission to continue (see Law 2).

PERMITTED IN-FLIGHT:

  * In-memory half-migrated working tree BETWEEN edits. The tree gets
    whole before the commit, not between every Edit call.
  * Transitional aliases / legacy-accepting paths DURING implementation.
    Every one of them DELETED before the cutover commit lands.
  * Cheap smoke-confirmation (go build / go test) between tasks. That
    is NOT the acceptance gate. R10 is.

=============================================================================
VERIFIED FACTS ONLY — NO ASSUMPTIONS IN CLAIMS
=============================================================================

Before every claim, verify on the live system. Before every fix, do a
full root-cause analysis. Treat every assumption as untrusted until
tested live.

  * On unexpected failures, STOP and run root-cause-analyzer agent
    BEFORE attempting a fix. Blind fix-guessing breaks code.
  * Only progress on facts you can PASTE into this conversation.
  * If a claim in a skill or CLAUDE.md is wrong, FIX THE DOCUMENT
    in the same cutover. Do not work around it.

THIS PROTOCOL EXISTS BECAUSE EVERY RULE HAS BEEN VIOLATED BEFORE AND
EACH VIOLATION COST THE USER HOURS. READING THIS REMINDER WITHOUT
ACTING ON IT IS THE VIOLATION THAT HAPPENS MOST OFTEN. DON'T.
EOF
