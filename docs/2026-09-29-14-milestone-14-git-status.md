# Milestone 14 — Git Status Panel (VSCode Source Control style)

> Branch header, grouped Staged/Unstaged/Untracked changes, per-file
> stage/unstage/discard, diff-on-select (opens M15). System `git` binary
> only (blueprint §32) — never embedded libgit2, so user config, hooks,
> SSH, and GPG keep working.

**Status:** Complete — see [status.md](status.md) for the live record and evidence. The `## Goals` list is the delivered scope; `## Acceptance Criteria` items with documented limits stay unchecked.

## Product Contract

VSCode Source Control conventions adapted to a compact sidebar: branch +
ahead/behind badge, three change groups, per-file `stage` / `unstage` /
`discard` (explicit Cancel / Discard Changes confirmation dialog), `refresh`
(manual + debounced interval + post-`terminal.run` hint — never terminal
text scraping), and a minimal commit message row. Commit uses
`git commit -m` and repository-configured authorship; no amend/push UI.
The commit UI was added by user-approved scope change 2026-10-01. Non-repo
projects show the empty state.

## Goals (complete 2026-10-01 — see `docs/status.md` M14 record)

- [x] `git status --porcelain=v2 -z` parser: branch, ahead/behind, `X/Y`
  codes, renames, NUL-safe paths with spaces/Unicode.
- [x] Env hygiene: `GIT_TERMINAL_PROMPT=0`, `--no-optional-locks`; all git
  I/O off the UI thread with timeout + cancellation.
- [x] `GitCommand::{Status, Stage, Unstage, Discard, Commit}` via dispatcher;
  `git.status` / `git.stage` / `git.unstage` / `git.discard` / `git.commit` wire methods +
  CLI, bounded entries + `truncated`.
- [x] Stage/unstage operate on explicit file paths under the M12 root
  (traversal-rejected); desktop discard requires explicit dialog confirmation.

## Prerequisites

- M12–M13 complete. System git present on Omarchy (record version).

## Deliverables

- `omaterm-context` (or `omaterm-git` if the boundary demands it):
  `status()`, `stage(paths)`, `unstage(paths)`, `discard(paths)` —
  argument vectors only, never shell interpolation.
- Core: `GitCommand` + validation (path allow-list, arg caps) + `GitStatus`
  result DTOs; error codes `not_a_repo`, `path_outside_root`,
  `git_failed` (message carries git stderr, bounded).
- Desktop: Source Control section, group headers with counts, per-file
  actions, discard confirmation dialog (user-requested update 2026-10-05).
- Protocol/bridge/CLI with human + `--json` rendering.

## Test Plan

- Unit: porcelain fixtures (rename, binary, spaces, NUL separators),
  ahead/behind parsing, validation rejections.
- Integration: real repos (clean, dirty, locked index, missing git,
  non-repo), stage→status→unstage round-trip, discard-confirm flow.
- Desktop live: edit/stage/untracked appears after refresh; CLI JSON
  matches the panel on the same instance.

## Acceptance Criteria (complete 2026-10-01)

- [x] Status + stage/unstage/discard/commit all work from panel and CLI
- [x] Locked-index / missing-git / non-repo paths show explicit states
- [x] No git process ever runs on the UI thread (timed assertion)
- [x] Discard without confirm is impossible (dialog cancellation/stale-context gate unit-tested; native dialog validation pending)
- [x] Quality gates green

## Non-Goals

- No push/pull/merge/rebase UI; no amend. Commit is limited to staged changes
  with a message and repository-configured authorship.
- No diff rendering here (M15). No editor (v0.3). No `terminal wait` (v0.4).

## References

- Blueprint §21 (approval), §23, §26, §32 (git strategy — normative),
  §43–§45, §52–§54, §61–§66
