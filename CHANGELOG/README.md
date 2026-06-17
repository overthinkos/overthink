# Changelog

**This `CHANGELOG/` directory is this repository's home for historical content.**
Every repository in the project keeps its **own** `CHANGELOG/` — history is
repo-scoped, never centralized in one file, and split into one file per calendar
month so no single file grows without bound.

`CLAUDE.md`, `README.md`, `plugins/README.md`, and every skill
(`plugins/**/SKILL.md`) describe the **current** state of the system — present
tense, forward-looking. Any reference to a previous version, a past rename, a
completed cutover or migration, a relocated / deleted / retired identifier, a
"previously / formerly / was / no longer", a dated change note, or a
commit-referenced cautionary tale belongs **here** and nowhere else. When a
cutover lands, append its narrative to the **current month's file** as the
post-execution record; state the standing rules it establishes forward-looking in
CLAUDE.md / skills with no history. This directory is the sanctioned "changelog
context" named by CLAUDE.md R5's grep self-test.

## Layout

- **One file per calendar month:** `YYYY-MM.md` (e.g. `2026-06.md`). Entries
  within a file are reverse-chronological; dates use the project's `YYYY-MM-DD`
  stamp; entries whose exact day was never recorded are grouped under a
  `(day unspecified)` heading. New entries are appended to the current month's
  file — create it on the month's first entry.

## Index

- [2026-06](2026-06.md)
- [2026-05](2026-05.md)
- [2026-04](2026-04.md)
