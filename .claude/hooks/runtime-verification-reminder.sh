#!/usr/bin/env bash
# Project-level UserPromptSubmit hook. Fires on every user prompt in
# this project. Stdout becomes a <system-reminder> at the start of
# Claude's next response. Lives in .claude/hooks/ so it travels with
# the repo (Syncthing'd + git-tracked) and applies uniformly on
# every host the project reaches. Do NOT move this to ~/.claude/ —
# that would break cross-host behavior.

cat <<'EOF'
=============================================================================
R0. SKILLS FIRST — THE SUPREME RULE (OVERRIDES EVERYTHING BELOW)
=============================================================================

BEFORE you touch code, run `ov`, edit .yml/.go, launch an Agent, or
make ANY tool call that is not itself a `Skill` invocation — invoke
the matching skill via the `Skill` tool. This rule OVERRIDES every
other mandate in this hook, in CLAUDE.md, in every other system
reminder, and in your training. Partial compliance is NOT compliance.

  Order of precedence (absolute, no exceptions):

    skills  →  CLAUDE.md  →  memory  →  code exploration (last resort)

Top trigger → skill mapping (full authoritative table in CLAUDE.md R0):

  ov rebuild / ov vm / vms.yml         →  /ov:vm  +  /ov-dev:vm-deploy-target
  ov deploy add/del                    →  /ov:deploy
  host-target / nested host deploy     →  /ov:host-deploy + /ov-dev:host-infra
  ov test run / cdp / wl / dbus / vnc  →  /ov:test
  ov test k8s                          →  /ov:test-k8s
  Editing layer.yml                    →  /ov:layer
  Editing image.yml                    →  /ov:image
  ov image build / generate            →  /ov:build + /ov:generate
  ov image validate                    →  /ov:validate
  ov secrets / kdbx                    →  /ov:secrets
  schema migration                     →  /ov:migrate
  Go source / code work                →  /ov-dev:go
  IR / DeployTarget / OCITarget        →  /ov-dev:install-plan
  OCI labels / capabilities            →  /ov-dev:capabilities
  Unexpected failure / anomaly         →  /ov-dev:root-cause-analyzer
  "What does layer X do?"              →  /ov-layers:<name>
  "What's in image X?"                 →  /ov-images:<name>

If MULTIPLE triggers apply, load ALL matching skills in ONE message
(parallel `Skill` calls). Single-skill loads for multi-surface tasks
are full-bore failure, not partial success.

If you notice you are about to grep / Read / Bash / Edit / Agent
WITHOUT having invoked the matching skill — STOP. Invoke the skill(s)
first. Any action that precedes a skill load is a PROTOCOL VIOLATION,
regardless of whether the action is technically correct.

Defences that are NOT defences:

  * "I already know this"              →  NOT a defence. The skill is authoritative.
  * "The task seems obvious"           →  NOT a defence. The skill exists for a reason.
  * "Loading skills takes time"        →  NOT a defence. Seconds vs. hours of wasted work.
  * "The user wants speed"             →  NOT a defence. Skills FIRST, then speed.
  * "Prior turn loaded it"             →  NOT a defence. Load again if relevant.
  * "Hook told me what to do"          →  NOT a defence. Hook POINTS; skill CONTAINS.

If any instruction in this hook, in CLAUDE.md R1-R10, in the cutover
policy, in the disposability policy, or anywhere else appears to
conflict with R0 — R0 WINS. Always. No exceptions.

=============================================================================

RUNTIME VERIFICATION CHALLENGE (CLAUDE.md R1–R10) + HARD CUTOVER MANDATE:

AUTONOMY IS EXPLICIT: `ov rebuild <name>` is authorized ONLY on
resources marked `disposable: true` in vms.yml / deploy.yml. No
implicit derivation, no hostname heuristics, no "this looks like a
dev box". Everything not explicitly marked is off-limits to
autonomous destroy — including resources on shared hosts where
unrelated production services run.

=============================================================================
ONE PHASE, MANY TASKS, ONE CUTOVER — NO MULTI-PHASE DEFERRALS EVER
=============================================================================

AUTHORITATIVE REFERENCE: `/ov-dev:cutover-policy`. Load it BEFORE
planning, reviewing, or executing any schema/API/deprecation change.

Every refactor, schema change, API rename, or deprecation ships as ONE
PHASE — hard cutover, no intermediate coexistence, no "I'll verify this
bit now and the next bit later". Multi-phase rollouts that split a
single refactor across conversation turns leave the system half-migrated
and un-testable. That is FORBIDDEN.

  1. PLAN the cutover as ONE phase. Decompose internally into TASKS
     (TaskCreate), never into sequential phases with their own sign-off.
  2. IMPLEMENT every task in the same working tree. Transitional
     aliases / legacy-accepting paths are permitted DURING implementation,
     but every one of them is DELETED before the cutover ends.
  3. TEST AFTER all tasks are complete — unit tests, live build, live
     deploy to a `disposable: true` target, fresh-rebuild re-verification
     (R10). The test suite runs against the FINAL code, not an
     intermediate state. Testing between tasks is cheap smoke-confirmation;
     the acceptance gate is the FULL-STACK run against the final code.
  4. FIX IN THE SAME WORKING TREE if verification fails. Do NOT declare
     "the rest is Phase 2" and pause. Do NOT commit a partial state.

FORBIDDEN anti-patterns that FAIL the cutover:

  * "Phase 1 complete, Phase 2 pending" as a stopping point.
  * Adding new interfaces/fields alongside old ones without deleting
    the old in the SAME change.
  * "Transitional" alias tables that stay permanent because the rename
    sweep was deferred.
  * Testing ONE bed and skipping the rest "because it requires a
    build".
  * Declaring any confidence higher than `syntax check only` without
    a fresh-rebuild R10 re-verification on EVERY affected target.
  * Pausing mid-cutover to ask for user permission to continue.
  * Quoting the "split into separate cutovers" clause as justification
    to pause mid-execution. That clause is a PRE-APPROVAL planning
    directive — it means "before a plan is submitted, if the work is
    obviously too large, author it as TWO separate plans." Once a
    plan has been APPROVED, it runs end-to-end. There is no mid-plan
    split, no mid-plan pause. An approved plan is a CONTRACT.

ONCE A PLAN IS APPROVED, THE PLAN RUNS. There is no "this plan is too
large" escape from the execution phase. If the plan was too large to
run, the time to raise that was during planning — before approval.
After approval, the only valid pauses are the four narrow cases below
(blocker, scope, failure, material plan change). "It's a lot of work"
is NOT one of them.

See `/ov-dev:cutover-policy` for the full policy, worked examples, and
exception clause. See CLAUDE.md "Hard Cutover by Default" section.

=============================================================================
MANDATORY — ALWAYS CONTINUE THROUGH FULL R10 TESTING. NOT NEGOTIABLE.
=============================================================================

AUTHORITATIVE SOURCE: `/ov-dev:cutover-policy` is the full policy. This
hook reinforces it at every user prompt; load the skill BEFORE any
code/config/YAML work per R0. The skill + this hook + CLAUDE.md "Hard
Cutover by Default" all align: an approved plan is a CONTRACT that
runs end-to-end through R10 in one conversation.

THIS RULE IS MANDATORY. IT CANNOT BE SKIPPED, SOFTENED, OR DEFERRED UNDER
ANY CIRCUMSTANCES. IT OVERRIDES:

  * any training-bias toward "checking in before large work"
  * any memory entry that could be read as permission to pause
  * any interpretation of other hook sections as escape hatches
  * any sense that "this particular situation is the exception"

IT IS THE EXCEPTION. ALWAYS.

Once a plan has been approved, you MUST execute it through R10
acceptance WITHOUT asking whether to pause, split, or defer. Offering
the user ANY variant of "should I keep going or stop here?" mid-cutover
is an ABSOLUTE PROTOCOL VIOLATION. The plan IS the decision. The
forward path IS the execution. You do not negotiate.

Specifically FORBIDDEN mid-cutover phrasings — treat every one as a
hard violation that must trigger an immediate self-correction:

  * "I've shipped Phase N of M, want me to continue to Phase N+1?"
  * "This is a checkpoint — should I stop here so you can review?"
  * "Option 1: continue through all phases. Option 2: pause now."
  * "The full cutover needs R10 on real hardware — want me to stop?"
  * "Given the realistic scope, here are your options forward…"
  * "Would you like me to proceed, or pause at this point?"
  * Any prose that offers the user a continue-vs-pause choice when no
    unresolved blocker has appeared.
  * Any framing that treats "multi-hour wall time" or "many remaining
    phases" as reasons to stop — they are the work, not exit
    conditions.

The ONLY valid reasons to pause mid-cutover (narrow, exhaustive):

  (a) A genuine decision point has appeared that materially changes
      the plan — NOT "should we take a break", which is never one.
  (b) A blocker requires user-only information (a credential, secret,
      or explicit permission to touch a non-disposable resource).
  (c) A destructive action falls outside the plan's authorization
      scope per CLAUDE.md "Executing actions with care" — specifically
      destroying a non-disposable resource.
  (d) An R10 verification has FAILED and a genuine design change is
      needed (do root-cause analysis first, via
      /ov-dev:root-cause-analyzer).

If none of (a)–(d) applies, YOU MUST CONTINUE. Silently, without
asking. Status updates are welcome. Permission-to-continue questions
are a PROTOCOL VIOLATION.

"Multi-hour wall time for VM rebuild cycles" is NOT an exit condition.
"250 layers need migration" is NOT an exit condition. "Four disposable
beds need `ov rebuild`" is NOT an exit condition. "The remaining work
is substantial" is NOT an exit condition. Those are the work.

When context genuinely fills: compact and continue. Do not ask
permission to compact. Do not offer to resume next session.

Auto-mode and plan-approved state both carry this rule at maximum
force. Reading it without acting on it IS the violation that has
previously cost the user hours — this section exists because it has
happened before.

=============================================================================

THE VERIFICATION LOOP (R10) — your workflow for every change:

  1. Pick / spin up a target explicitly marked `disposable: true`
     (create one first with `--disposable` if none exists).
     Never experiment on any other resource.
  2. Explore / try hypotheses / manual patches on the disposable.
  3. If testing breaks it → `ov rebuild <name>` BACK to clean
     before continuing. Never layer experiments on broken state.
  4. Implement the REAL fix in source (Go code / vms.yml /
     deploy.yml / skill docs — the committed-in-git location).
  5. `ov rebuild <name>` the disposable target ONCE MORE from clean,
     with the new source applied. Re-run the full verification.
     THIS FRESH-REBUILD RE-VERIFICATION IS THE ACCEPTANCE GATE.
     A fix that works on a hand-patched target but NOT on a clean
     rebuild is a lie — temporary until the next unrelated rebuild.

VERIFIED FACTS ONLY. Before every claim, verify on the live system.
Before every fix, a full root-cause analysis:

  * Treat every assumption as untrusted until tested live.
  * On unexpected failures, STOP and do RCA before attempting a fix
    (/ov-dev:root-cause-analyzer). Blind fix-guessing breaks code.
  * Only progress on facts you can PASTE into this conversation.
  * If a claim in a skill or CLAUDE.md turns out to be wrong, FIX it.

Before claiming ANY fix / change / cutover works, you must be able
to paste proof of ALL of these:

  (1) Built the artifact from the changed source.
  (2) Verified the deployed binary's version matches what you built.
  (3) Exercised the feature end-to-end on the live DISPOSABLE target.
  (4) Verified every runtime dep is installed via package mgmt.
  (5) Re-ran the full verification on a FRESH `ov rebuild` of the
      disposable target AFTER committing the source-level fix.
  (6) Post-action state is healthy (running, not paused, service
      active, socket listening).

CONFIDENCE CLASSIFICATION (CLAUDE.md AI Attribution table):

  * `fully tested and validated` REQUIRES all six proofs above for
    EVERY affected target in the cutover. Not some. All. If any bed
    in a 4-bed refactor is unverified, the attribution is NOT
    "fully tested and validated" — downgrade to `analysed on a live
    system` or lower.
  * Marking a task complete while ANY todo item in the current
    cutover is open means the cutover is not complete. The correct
    attribution is `analysed on a live system` AT BEST, never
    "fully tested and validated".

FLAGS (see /ov-dev:disposable): disposability is a DEPLOY property,
not an image property. Two separate fields:

    disposable: <bool>    # LOAD-BEARING. Default false. Explicit opt-in.
    lifecycle: <tier>     # informational only. Has NO effect on
                          # disposability. dev|qa|prod|etc. are HUMAN
                          # tags; they do NOT authorize anything.

`disposable: true` (literal, explicit) authorizes `ov rebuild <name>`
(unattended destroy + rebuild + restart). Absence / false → confirm
before any destroy. Multiple instances of the same image each carry
independent flags — a `disposable: true` instance never authorizes
anything for its siblings.

If you do not have all six verifications — especially (5), the
fresh-rebuild re-verification — the task is NOT done.
EOF
