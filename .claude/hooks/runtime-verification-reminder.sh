#!/usr/bin/env bash
# Project-level UserPromptSubmit hook. Fires on every user prompt in
# this project. Stdout becomes a <system-reminder> at the start of
# Claude's next response. Lives in .claude/hooks/ so it travels with
# the repo (Syncthing'd + git-tracked) and applies uniformly on
# every host the project reaches. Do NOT move this to ~/.claude/ —
# that would break cross-host behavior.

cat <<'EOF'
RUNTIME VERIFICATION CHALLENGE (CLAUDE.md R1–R10):

AUTONOMY IS EXPLICIT: `ov rebuild <name>` is authorized ONLY on
resources marked `disposable: true` in vms.yml / deploy.yml. No
implicit derivation, no hostname heuristics, no "this looks like a
dev box". Everything not explicitly marked is off-limits to
autonomous destroy — including resources on shared hosts where
unrelated production services run.

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
