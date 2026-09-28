# Milestone 16 — Command Palette

> VSCode `Ctrl+Shift+P` conventions: fuzzy match, `>`-prefixed command
> mode, prefix-less file mode, MRU ranking, `Esc` returns focus to the
> terminal. Every entry dispatches a semantic command — the palette holds
> zero workspace-mutation logic (blueprint §35, §62).

## Product Contract

A single overlay input (after Omarchy collision check; fallback shortcut
documented) searching: OmaCommands (split/focus/resize/tab/project/git
refresh/diff/process refresh…), projects/tabs/panes/sessions by ID and
name, files via the M13 index, and git changed-paths. `Up/Down/Enter/Esc`,
live filtering bounded to 100 rows with cancellation. Focus always returns
to the originating terminal on execute or dismiss. No new wire method is
needed: CLI parity is provided by the underlying query commands
(`project/tab/pane/terminal/file/git/diff/process` lists) — documented
explicitly so the checker contract stays honest.

## Goals

- [ ] Overlay UI with two modes (`>` commands, plain-text files/IDs),
  MRU-ranked fuzzy results reusing M13's fuzzy crate (no new dep).
- [ ] Sources: command registry, workspace IDs, file index, git paths;
  stale IDs are no-ops with an explicit notice, never a fallback target.
- [ ] Validation failures emit no effects (matrix test, M7 precedent).
- [ ] Keyboard-only end-to-end: split, focus jump, project jump,
  file-open-in-terminal, git refresh, process-panel refresh.

## Prerequisites

- M12–M15 + M18-query complete (all searchable sources exist).

## Deliverables

- Desktop: palette overlay component, matcher wiring, action dispatch
  through `dispatch_command`, focus restoration.
- Core: pure ranking/filter helpers (GPUI-free, unit-tested).
- Docs: palette→command mapping table; note on why no `palette.query`
  wire method exists (automation uses the source queries directly).

## Test Plan

- Unit: ranking (exact/prefix/MRU), 100-row cap, stale-ID no-op,
  no-effects-on-validation-failure, focus-restore.
- Integration: palette action produces identical state to the equivalent
  CLI call (split/focus/select parity test).
- Desktop live: full keyboard-only session incl. file + git + process
  actions on Wayland.

## Acceptance Criteria

- [ ] All palette actions route through the dispatcher (no direct mutations)
- [ ] Stale targets never retarget; failures show explicit notices
- [ ] Parity test: palette ≡ CLI for covered actions
- [ ] Quality gates green

## Non-Goals

- No `palette.query` IPC method (deliberate; documented).
- No text-content search. No editor commands beyond terminal-routed open
  (v0.3). No `terminal wait` (v0.4).

## References

- Blueprint §23, §35 (palette — normative), §36 (keyboard-first),
  §43–§45, §62–§64
