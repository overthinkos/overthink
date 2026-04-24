#!/usr/bin/env bash
# Project-level UserPromptSubmit hook. Fires on every user prompt in
# this project. Stdout becomes a <system-reminder> at the start of
# Claude's next response. Lives in .claude/hooks/ so it travels with
# the repo. Do NOT move to ~/.claude/.

cat <<'EOF'
=============================================================================
MANDATORY OPERATING PROTOCOL — READ EVERY WORD, FOLLOW EVERY RULE
=============================================================================

THIS IS NOT ADVICE. THIS IS NOT A SUGGESTION. THIS IS THE CONTRACT THAT
GOVERNS EVERY ACTION YOU TAKE IN THIS PROJECT. VIOLATING ANY RULE BELOW
IS A PROTOCOL VIOLATION. "I forgot", "I thought", "it seemed easier",
"the user wanted speed" are NOT defences. They are confessions.

The four MANDATORY laws, in precedence order:

    1. SKILLS FIRST        — load the skill before you act
    2. NO MID-PLAN STOPS   — approved plan runs end-to-end, no pauses
    3. R10 VERIFICATION    — fresh-rebuild re-verification, no exceptions
    4. DISPOSABLE-ONLY     — no autonomous destroy without explicit flag

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

  ov rebuild / ov vm / vms.yml         →  /ov:vm + /ov-dev:vm-deploy-target
  ov deploy add/del                    →  /ov:deploy
  host-target / nested host deploy     →  /ov:host-deploy + /ov-dev:host-infra
  ov test run / cdp / wl / dbus / vnc  →  /ov:test
  ov test k8s                          →  /ov:test-k8s
  Editing layer.yml                    →  /ov:layer
  Editing image.yml                    →  /ov:image
  ov image build / generate            →  /ov:build + /ov:generate
  ov image validate                    →  /ov:validate
  ov secrets / kdbx                    →  /ov:secrets
  Schema migration                     →  /ov:migrate
  Go source / code work                →  /ov-dev:go
  IR / DeployTarget / OCITarget        →  /ov-dev:install-plan
  OCI labels / capabilities            →  /ov-dev:capabilities
  Unexpected failure / anomaly         →  /ov-dev:root-cause-analyzer
  Hard cutover semantics               →  /ov-dev:cutover-policy
  Disposable-flag semantics            →  /ov-dev:disposable
  Skill authoring                      →  /ov-dev:skills
  "What does layer X do?"              →  /ov-layers:<name>
  "What's in image X?"                 →  /ov-images:<name>

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
      /ov-dev:root-cause-analyzer BEFORE asking the user anything.

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

THE "SPLIT INTO TWO PLANS" ESCAPE CLAUSE IS PRE-APPROVAL ONLY.

If you saw BEFORE a plan was approved that the work was too large for
one conversation, the valid action was to propose splitting into two
plans DURING PLANNING. After approval, the clause is CLOSED. Quoting
it post-approval as justification to pause is ITSELF a violation.

"The plan turned out to be bigger than I expected" is NOT a valid
reason to stop. That is your own planning error, paid for by
CONTINUING the execution, not by deferring it.

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
HARD CUTOVER BY DEFAULT — ONE COMMIT, ALL TASKS, R10 AT THE END
=============================================================================

See /ov-dev:cutover-policy for the full policy.

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

  * On unexpected failures, STOP and run /ov-dev:root-cause-analyzer
    BEFORE attempting a fix. Blind fix-guessing breaks code.
  * Only progress on facts you can PASTE into this conversation.
  * If a claim in a skill or CLAUDE.md is wrong, FIX THE DOCUMENT
    in the same cutover. Do not work around it.

THIS PROTOCOL EXISTS BECAUSE EVERY RULE HAS BEEN VIOLATED BEFORE AND
EACH VIOLATION COST THE USER HOURS. READING THIS REMINDER WITHOUT
ACTING ON IT IS THE VIOLATION THAT HAPPENS MOST OFTEN. DON'T.
EOF
