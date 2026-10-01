# Milestone 15 — Diff Viewer

> Git Source Control detail view: click a changed file in the existing Git
> panel to open its `Files → Hunks → Lines` diff as a preview tab in the
> main area (blueprint §33). The tab holds the file diff, never a terminal.
> Per-hunk stage buttons use M14 mutations. Single-pane hunk list first;
> side-by-side later. No editor (v0.3); preview state is view-local and
> never persisted.

## Product Contract

Read-only-by-default detail view for a selected Git change: unstaged
(`git diff`) and staged (`git diff --cached`) changes plus single-path
diffs. Selecting a row in Git opens its matching diff as a preview tab in
the main tab strip (`Diff: <name>`); selecting a terminal tab or closing
the chip returns to the terminal surface. Core tabs and snapshots stay
terminal-only (editor deferred to v0.3): preview state is view-local,
never persisted or sent over IPC. Binary
files render as "binary, not shown". Everything is capped before
allocation (files/hunks/lines/bytes) with accurate `truncated` flags.
No syntax highlighting in v0.2 (Tree-sitter arrives with the v0.3 editor);
plain monospace with `±` line coloring. Hunk stage buttons dispatch
`GitCommand::Stage`-equivalent scoped mutations — no bespoke git logic in
the view.

## Goals

- [ ] Sources: unstaged, staged, and single-path diffs; `--no-color
  --no-ext-diff` for machine parsing.
- [ ] Virtualized hunk rendering; next/previous-hunk keyboard navigation;
  copy-hunk + jump-to-path (via `file.open` terminal route).
- [ ] `DiffCommand::{Show, ListFiles}` via dispatcher; `diff.show`
  `{project_id?, path?, staged?, context_lines}` + `diff.list-files`,
  bounded JSON with truncation.
- [ ] Per-hunk stage action (visible only when the hunk belongs to an
  unstaged file in a repo project).

## Prerequisites

- M12–M14 complete (root, boundary, git mutations).

## Deliverables

- Diff parser: `Diff → Files[] → Hunks[] → Lines[]` with strict caps,
  multibyte-safe truncation, binary detection.
- Core: `DiffCommand` + validation + result DTOs.
- Desktop: open the selected changed file's diff as a main-area preview
  tab with hunk navigation and stage buttons; no editor, no persistence
  or protocol change for the preview itself.
- Protocol/bridge/CLI with human + `--json` rendering.

## Test Plan

- Unit: unified-diff fixtures (renames, mode changes, binary markers,
  multibyte splits, huge single line).
- Integration: generated 1000-file diff (caps hit, valid envelope),
  cancelled diff on project switch, staged-vs-unstaged equivalence with
  `git diff` CLI output.
- Desktop live: real edit shows correct hunks; large diff truncates
  without freezing (timed).

## Acceptance Criteria

- [ ] Unstaged/staged/path diffs render correctly with hunk navigation
- [ ] Caps enforced pre-allocation; truncation flags accurate
- [ ] Hunk stage dispatches through the same path as M14 file stage
- [ ] Wire/CLI parity tests green incl. JSON + exit codes
- [ ] Quality gates green

## Non-Goals

- No side-by-side view, no highlighting, no word-diff (later).
- No commit UI. No editor (v0.3). No `terminal wait` (v0.4).

## References

- Blueprint §23, §26, §33 (diff strategy — normative), §43–§45,
  §52–§54, §61–§66
