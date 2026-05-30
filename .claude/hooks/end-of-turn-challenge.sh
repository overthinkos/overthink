#!/usr/bin/env bash
# Project-level Stop hook. Fires at end-of-turn. Stdout becomes a
# <system-reminder> appended to Claude's final response context. SOFT — never
# emits blocking JSON; a trivial-reply turn still completes normally.
#
# DOCTRINE: a LEAN POINTER, not a copy. The authoritative checklist is
# CLAUDE.md "End-of-turn checklist" + "Post-Execution Policies". This hook
# only anchors the few self-checks most worth re-asking every turn and points
# at the source. Deterministic enforcement (force-push, bad tier, --no-verify)
# is in the PreToolUse gates, not here. See /ov-internals:agents "Hooks doctrine".

cat <<'EOF'
END-OF-TURN CHECK (soft — does not block). Confirm against CLAUDE.md
"End-of-turn checklist" + "Post-Execution Policies":

  [ ] R0: every non-trivial action this turn was preceded by the matching
      Skill load (all relevant skills, up front). If you caught yourself
      acting skill-less, you re-validated against the skill before stopping.

  [ ] R10 (if code/deploy was touched): a real `ov eval run <bed>` /
      `ov eval live` ran against a FRESH rebuild of a `disposable: true`
      target AND its output is PASTED. A dry-run / unit-test / validate /
      bare rebuild is NOT R10. No scope-shrinking flags were added without
      explicit per-turn authorization (Law 3.6).

  [ ] Attribution tier == what the pasted proof supports (no inflation). A
      KNOWN rule violation => NO commit at any tier (fix in-tree or escalate;
      never "downgrade and ship"). You did NOT mark an R10 task complete, edit
      it to "partial", or delete it, when the runner did not run (the
      2026-04-26 pattern — see CHANGELOG.md).

  [ ] Cutover is ONE phase: every task complete, no transitional/half-migrated
      state, no "Phase 2 TODO". `git grep` of any removed identifier returns
      only CHANGELOG.md / migration help-text (R5).

  [ ] Landing (only after R10 PASS): R10 PASS is the sole landing trigger and
      auto-lands per /ov-internals:git-workflow — ONE atomic commit per repo
      with the Assisted-by trailer, feat/ fast-forward-merged to main, a fresh
      v<CalVer> tag on each overthink.yml repo, pushed. NEVER force-push
      (no --force / --force-with-lease, any branch, any repo).

If any box is unchecked and the cutover isn't done: KEEP WORKING (on
`disposable: true` targets, `ov update`/`ov eval run` need no extra
permission). If genuinely stuck, stop with ONE specific actionable question.
EOF
