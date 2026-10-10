# Milestone 19 — Basic Built-in Editor

> Native open → edit → highlight → save inside OmaTerm, without leaving the
> terminal-first workspace and without expanding into an IDE. Blueprint §34 is
> normative for scope and non-goals; §23 allows the lightweight editor into
> v0.2 or v0.3.

**Status:** In progress (core editor landed; S9 native exit criteria open) — see [status.md](status.md) for the live record and evidence.

This milestone-level contract is the index entry for editor work. The detailed
product contract and delivery history live in the
[M19 implementation plan](2026-10-03-m19-basic-editor-implementation-plan.md); the
dependency-ordered remaining work and exit gates live in the
[M19 completion plan](2026-10-03-m19-completion-plan.md). Evidence is recorded in
[status](status.md), the [acceptance matrix](acceptance-matrix.md), and the
[S9 report](evidence/m19-s9-report.md).

## Product Contract

Open a project-root-contained text file in a native document view alongside
terminal tabs, edit it with Unicode-correct multiline editing and syntax
highlighting, and save with external-change protection. Files open from the
Files tree, `Ctrl+P` results, palette file results, and diff/Git "open path"
actions. Documents survive tab switches; close prompts on unsaved changes; focus
always returns to a valid terminal or document.

The document surface is **not** a terminal: `PaneContent` and terminal splits
remain terminal-only, and switching surfaces never recreates PTYs. Lifecycle
mutations go through the shared semantic dispatcher; UI text editing mutates the
owner-side store while filesystem work stays in bounded context helpers.
CLI/IPC `file.open` continues to submit `$EDITOR` to a terminal (Option A); the
native editor adds no editor wire methods.

## Goals

- [ ] Root-bound, bounded file I/O: open/revert/save through captured root
  identity, 1 MiB file cap, 20,000 line cap, 4096-byte path cap, binary/NUL and
  invalid-UTF-8 rejection.
- [ ] Unicode-correct edit buffer: caret movement, selection, insert/delete,
  clipboard cut/copy/paste, undo/redo; no byte-index slicing; wide/combining
  awareness.
- [ ] Language-aware highlighting (Rust, Markdown, TOML, JSON, Bash) with
  plain-text fallback; no grammar downloads.
- [ ] Dirty tracking, explicit save (`Ctrl+S` + Save control), typed save
  outcomes including pre-rename revalidation and external-change warnings.
- [ ] Async I/O worker: one active + 16 queued jobs, cancellation, stale-result
  rejection by generation/document/root identity; no filesystem work on the
  owner/UI thread.
- [ ] Bounded document store (32 workspace-wide slots incl. reservations) and
  open-document registry persistence: restore clean on-disk content only, never
  dirty text, clipboard, undo stacks, highlights, or runtime handles.
- [ ] Graceful shutdown awaits final editor outcomes; dirty close/shutdown must
  not drop pending writes.

## Prerequisites

- v0.1 complete. M12 root/boundary semantics settled (ownership anchor reuse).
- M15 diff and M16 palette `in_progress`; their closure gates are not masked by
  editor work. M17 v0.2 closure is open; editor execution was explicitly
  re-sequenced ahead of remaining M15/M16/M17 closure and that re-sequencing is
  not evidence those milestones passed.
- M18 bounded context/query helpers are reused; no parallel FS stack.

## Deliverables

- `omaterm-context`: rooted open/read/revert/save operations, content revisions
  with SHA-256 digests, descriptor-relative atomic save.
- Desktop: editor surface + input handling (`EntityInputHandler`), document
  store, `EditorIoQueue` worker, registry persistence and restore.
- Domain: editor lifecycle commands through the shared dispatcher and bounded
  validation with stable error codes (`document_conflict`,
  `document_limit_exceeded`, etc.).
- Presentation per [UI v5 plan](2026-10-02-ui-v5-pixel-perfect-plan.md) §14 (tab,
  breadcrumb, code, footer, caret), functional slice owns behavior.

## Test Plan

- Unit: rooted read/save barriers, root/ancestor/final-component swaps, injected
  failures, revision digest conflict, worker capacity/cancellation/stale
  rejection, store lifecycle bounds, tokenizer/caret/highlight, validation.
- Integration: open/edit/save cycles, Unicode, clipboard, language detection,
  dirty close/revert, entry routes, restart restore.
- Desktop live (Wayland): open/edit/save with on-disk byte proof,
  selection/clipboard, focus return, no PTY keystroke leaks, 20 small + 20
  cap-size cycles, graceful-shutdown and restart, IME preedit.
- Gates: `cargo fmt --all --check`, `cargo test --workspace`, workspace/all-target
  Clippy `-D warnings`, release build, `python3 scripts/check-docs.py`,
  `git diff --check`.

## Acceptance Criteria

- [ ] Native open/edit/highlight/save proven with on-disk byte assertions on
  Wayland release.
- [ ] Root identity and content revisions prevent stale overwrite; no filesystem
  work on the UI thread (timed).
- [ ] Bounded store and registry persistence: restart restores clean documents,
  never dirty text; no duplicate registry descriptors.
- [ ] Entry routes (tree, `Ctrl+P`, palette, diff) and focus return proven; no
  PTY keystroke leaks.
- [ ] Graceful shutdown flushes final editor outcomes; no pending write dropped.
- [ ] Quality gates green; `Cargo.lock` tracked.

## Non-Goals

No LSP/diagnostics, autocomplete/snippets, refactoring/rename, debugger, or
extensions. No find/replace, new-file/Save As, editor splits, or additional
grammar downloads in this milestone. No agent awareness/automation or
`terminal wait` (v0.3/v0.4), no theme engine, no Ghostty/macOS/Windows.

## References

- Blueprint §23, §34 (normative), §62–§64, §66–§67
- [M19 implementation plan](2026-10-03-m19-basic-editor-implementation-plan.md)
- [M19 completion plan](2026-10-03-m19-completion-plan.md)
- [UI v5 pixel-perfect plan](2026-10-02-ui-v5-pixel-perfect-plan.md) §14
