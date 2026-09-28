# Milestone 14 — Git Status Panel (VSCode Source Control style)

> Branch header, grouped Staged/Unstaged/Untracked changes, per-file
> stage/unstage/discard, diff-on-select (opens M15). System `git` binary
> only (blueprint §32) — never embedded libgit2, so user config, hooks,
> SSH, and GPG keep working.

## Product Contract

VSCode Source Control conventions adapted to a compact sidebar: branch +
ahead/behind badge, three change groups, per-file `stage` / `unstage` /
`discard` (discard is arm-and-confirm, like paste/history), `refresh`
(manual + debounced interval + post-`terminal.run` hint — never terminal
text scraping). Non-repo projects show the empty state. **No commit UI**:
message/authorship/push/pull stay out of v0.2; staging is reversible and
project-scoped, so it fits the approval model, while commit is deferred.

## Goals

- [ ] `git status --porcelain=v2 -z` parser: branch, ahead/behind, `X/Y`
  codes, renames, NUL-safe paths with spaces/Unicode.
- [ ] Env hygiene: `GIT_TERMINAL_PROMPT=0`, `--no-optional-locks`; all git
  I/O off the UI thread with timeout + cancellation.
- [ ] `GitCommand::{Status, Stage, Unstage, Discard}` via dispatcher;
  `git.status` / `git.stage` / `git.unstage` / `git.discard` wire methods +
  CLI, bounded entries + `truncated`.
- [ ] Stage/unstage operate on explicit file paths under the M12 root
  (traversal-rejected); discard requires the two-step confirm.

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
  actions, discard arm banner.
- Protocol/bridge/CLI with human + `--json` rendering.

## Test Plan

- Unit: porcelain fixtures (rename, binary, spaces, NUL separators),
  ahead/behind parsing, validation rejections.
- Integration: real repos (clean, dirty, locked index, missing git,
  non-repo), stage→status→unstage round-trip, discard-confirm flow.
- Desktop live: edit/stage/untracked appears after refresh; CLI JSON
  matches the panel on the same instance.

## Acceptance Criteria

- [ ] Status + stage/unstage/discard all work from panel and CLI
- [ ] Locked-index / missing-git / non-repo paths show explicit states
- [ ] No git process ever runs on the UI thread (timed assertion)
- [ ] Discard without confirm is impossible (test the arm window)
- [ ] Quality gates green

## Non-Goals

- No commit/push/pull/merge/rebase UI (explicit per §32; needs design).
- No diff rendering here (M15). No editor (v0.3). No `terminal wait` (v0.4).

## References

- Blueprint §21 (approval), §23, §26, §32 (git strategy — normative),
  §43–§45, §52–§54, §61–§66
