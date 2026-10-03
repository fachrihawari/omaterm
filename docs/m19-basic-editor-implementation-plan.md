# M19 — Basic Built-in Editor Implementation Plan

**Date:** 2026-10-04. **Status:** planned; no implementation claimed.
**Scope:** open file + syntax highlight + save. No LSP, autocomplete,
refactoring, debugger, extensions, or cloud.

## 1. Objective and authority

Deliver a native built-in editor so files open **inside OmaTerm** instead of
only submitting `$EDITOR <path>` to a terminal. First delivery is deliberately
small:

```text
open (tree / Ctrl+P / palette / Git diff) → edit → highlight → save
```

Authoritative contracts:

- Blueprint §34 (lightweight editing — normative for scope/non-goals).
- Blueprint §23 (a lightweight editor may enter v0.2 or v0.3).
- `docs/13-milestone-13-file-tree.md` (`file.open` currently terminal-routed;
  editor pane deferred).
- `docs/15-milestone-15-diff-viewer.md` (no editor; preview stays view-local).
- `docs/16-milestone-16-palette.md` (no editor commands beyond
  terminal-routed open).
- `docs/ui-v5-pixel-perfect-plan.md` §14 (editor tab/breadcrumb/code/footer/
  caret presentation; functional slice owns behavior, not a static fixture).
- Architecture rules: §62 shared operations, §63 IDs, §64 bounded inputs,
  §66 cancellation.

This plan does **not** reopen settled decisions: terminal-first workspace,
recursive pane tree, long-lived sessions, semantic dispatcher, Unix-socket
IPC/CLI, or project-root/boundary semantics.

## 2. Entry gates

| Dependency | Position | Required before editor work |
|---|---|---|
| M15 diff | `in_progress` | Finish current D2/D3 acceptance; editor must not mask diff gates |
| M16 palette | `in_progress` | Finish M18 query prerequisite first; editor targets reuse file/Git identity rules |
| M17 v0.2 closure | open | Editor lands as M19 after M17, or explicitly re-sequences v0.2 scope |
| M18 query slice | partial | Editor file I/O must reuse bounded context helpers, not add a parallel FS stack |

Smallest entry is Phase A after M15 closure. Planning may proceed now;
implementation must not start a multi-thousand-line coordinator rewrite.

## 3. Product contract

### 3.1 In scope

- Open a project-root-contained text file in a native document view.
- Entry points: Files tree, `Ctrl+P` filename results, palette file results,
  diff/Git “open path” actions.
- Multiline editing: caret movement, selection, insertion/deletion,
  clipboard copy/cut/paste, undo/redo.
- Unicode-correct text handling: no byte-index slicing, combining/wide-cell
  awareness in the text buffer.
- Language-aware syntax highlighting with plain-text fallback.
- Dirty tracking, explicit save (`Ctrl+S` plus a Save control), save errors.
- External-change detection: warn before overwriting a file changed on disk.
- Document lifecycle: buffers survive tab switches; close prompts for unsaved
  changes; focus returns to a valid terminal/document.
- Bounded loading: file-size/line-count caps, binary rejection, UTF-8 handling.

### 3.2 Explicit non-goals

```text
LSP / diagnostics
autocomplete / snippets
refactoring / rename
debugger / breakpoints
extensions / plugins
multi-cursor / vim mode
content search across files
terminal wait / agent automation
cloud sync / remote editing
```

Find/replace inside the open document may follow **after** open/edit/save is
accepted; it is not part of first delivery.

### 3.3 Compatibility

- Existing terminal-routed `file.open` remains available where configured.
- Native editor becomes the default document surface; terminal submission is
  retained as fallback/explicit action, not removed silently.
- The existing `file.open` CLI/IPC contract — “submit `$EDITOR <path>` to the
  focused terminal” — must be deliberately versioned/extended, not quietly
  reinterpreted.

## 4. Architecture

### 4.1 Ownership

```text
omaterm-core
  DocumentId
  EditorCommand / validation / result DTOs / error codes

omaterm-context
  bounded read_text / write_text
  language detection by extension/name
  syntax tokenization seam (spike decides implementation)

desktop owner
  DocumentStore: open buffers, dirty state, undo, cursors/selections
  editor renderer/input: GPUI views only; no FS/Git/process work in paint
  dispatcher integration and focus ownership
```

Dependency direction is mandatory:

```text
desktop → core, context, terminal, state, IPC
context → core IDs only where needed
core → domain dependencies only; no gpui, no context, no filesystem
```

### 4.2 Document identity

Introduce a typed document identity independent of screen position:

```rust
struct DocumentId(Uuid);
```

Minimum document key:

```text
project + canonical root identity + root-relative path
```

Rationale:

- Same path in different projects/roots must never share a buffer.
- Root replacement invalidates the open document rather than silently
  retargeting it.
- Palette/file/Git identity rules are reused: original path bytes for
  identity, lossy text only for display/search.

### 4.3 Surface model

Recommendation: editor documents live **alongside terminal tabs as workspace
documents**, not as terminal-pane content.

Why not `PaneContent::Editor`:

- Pane leaves own long-lived PTY sessions and PTY geometry.
- Snapshot currently persists terminal working directories per pane.
- Mixing text buffers into PTY geometry/resize/input routing would entangle
  two lifecycles.

Recommended first model:

```text
Project
  terminal tabs (existing PaneTree + sessions; unchanged)
  open documents (new view-local document set with selection/focus)
```

Consequences:

- Core tabs stay terminal-only initially.
- Editor open documents are workspace state, not PTY state.
- Persistence of open editors is a separate versioned decision; first delivery
  may restore closed-editor state explicitly rather than persisting dirty
  buffers implicitly.
- Exact snapshot impact is settled in Phase A before code.

Alternative considered and rejected for Phase 1: putting editors inside split
panes. That would require PTY/pane geometry, focus, IPC selection, and
snapshot changes all at once. Splits remain terminal-only in first delivery.

### 4.4 Command shape

Additive core command family; illustrative shape only:

```rust
enum EditorCommand {
    Open { project: ProjectId, path: PathBuf },
    Close { document: DocumentId },
    Save { document: DocumentId },
    Revert { document: DocumentId },
}
```

Editing keystrokes themselves are UI text-input state, not dispatcher
commands. Only document lifecycle and save/revert mutations go through the
shared dispatcher.

Effects guidance:

- Successful open/close/save emits workspace/persistence effects only where a
  persisted registry actually changes.
- Dirty text itself is **not** persisted to the workspace snapshot.
- Failed validation, missing files, binary files, oversize files, and stale
  root/project targets return stable codes with no effects.

### 4.5 File I/O contract

All file access goes through bounded context helpers:

- Resolve `project → root` with existing M12 semantics.
- Reject escapes with `path_outside_root` before touching disk.
- Reject directories, symlinks outside root, and non-regular files.
- Enforce size cap before allocation; recommended initial bound: **1 MiB**.
- Enforce line-count cap; recommended initial bound: **20,000 lines**.
- Accept UTF-8; decide explicit policy for invalid UTF-8 before coding:
  reject with a reason rather than lossy silent conversion.
- Detect binary content with an explicit heuristic and refuse to open as text.
- Read off the UI thread with cancellation/generation guards.
- Save atomically: same-directory temp file + rename, owner-only permissions
  consistent with existing state writers.
- Record mtime/size on open and before save; if changed, block overwrite and
  offer reload/revert.

### 4.6 Syntax highlighting contract

- Language is detected from filename/extension; unknown maps to plain text.
- Tokenization runs off the UI thread with cooperative cancellation.
- Highlight output is presentation-only spans; it never alters buffer text.
- Invalid/oversize input falls back to plain text, never an error surface.
- No network fetch of grammars at runtime.
- Implementation is chosen by spike: Tree-sitter versus a small built-in
  regex/keyword highlighter for the first language subset.
- Initial language subset recommendation: Rust, Markdown, TOML, JSON, Bash.
  Add more only after the pipeline, cancellation, and fallback behavior are
  proven.

Highlighter dependencies require exact version/license evidence in
`docs/dependencies.md`. Permissive licenses only; no GPL/AGPL grammar runtime.

### 4.7 Text buffer contract

- Rope or line-table buffer; exact implementation follows a small API spike.
- O(line) caret motion and O(visible) rendering; no full-document GPUI nodes.
- Undo/redo is in-memory, bounded, per document.
- Clipboard uses the existing safe clipboard path; no OSC-52 expansion.
- Caret blink uses deadline-driven timers; no repaint loop when idle/hidden.
- Tabs render as a fixed visual width without mutating buffer bytes.
- Line numbers use checked arithmetic; overflow omits rather than wraps.

## 5. UI contract

Follow `ui-v5-pixel-perfect-plan.md` §14 presentation roles:

- Document tab: file icon, filename, dirty marker, close control.
- Breadcrumb/header with root-relative path and document toolbar.
- Code area: monospace, gutter, current-line treatment, whitespace preserved,
  no soft wrap in first delivery; long lines scroll horizontally.
- Footer: cursor line/column, indentation label, encoding label, language label.
- Caret: native 1px caret with explicit blink; selection uses measured roles.
- Empty/loading/error/binary/oversize/external-change states are explicit.
- Editor shortcuts must not leak into terminals; terminal input must not leak
  into the editor.
- Narrow windows preserve a usable code viewport; Inspector space stays
  reserved.

A static diagnostic view is **not** an editor. Actual open/edit/save flows are
required for acceptance.

## 6. IPC/CLI contract

Decide explicitly in Phase A; do not drift:

Option A (recommended for first delivery):

- Keep `file.open` terminal-routing semantics unchanged.
- Add explicit document operations only if automation needs them, e.g.
  `editor.open` / `editor.save`, with strict DTOs and parity tests.
- If no automation need is proven, editor lifecycle stays UI-local and no new
  wire method is added.

Option B:

- Change `file.open` to mean native-editor open.
- This is a breaking automation-contract change and requires milestone-doc
  updates, mapping-table updates, checker updates, migration notes, and
  full parity tests.

Recommended: Option A. Automation continues using source queries plus explicit
terminal operations; the desktop owns document presentation.

## 7. Persistence contract

Phase A must answer these before implementation:

1. Are open documents persisted across restart?
2. Is dirty text ever persisted? Recommended: **no**.
3. Does close with unsaved changes block shutdown, prompt, discard, or save?
4. Do restored documents reopen saved on-disk content or an empty state?
5. What schema version carries the document registry?

Recommended first behavior:

- Persist the list of open documents per project, not their unsaved contents.
- Reopen clean on-disk content on restart.
- Unsaved changes require an explicit user decision before close/shutdown.
- Never persist file contents, clipboard text, or highlight caches in the
  workspace snapshot.

## 8. Ordered delivery phases

### A — Contracts, fixtures, and API spikes

1. Freeze editor scope and the `file.open` compatibility decision.
2. Settle document identity, snapshot impact, persistence, IPC/CLI, focus,
   and close/shutdown semantics in writing.
3. Create disposable fixtures:
   - small Rust/Markdown/TOML/JSON/Bash files;
   - Unicode/combining/wide/tab/very-long-line cases;
   - binary file;
   - invalid UTF-8 file;
   - oversize file above the read cap;
   - external-change race file;
   - two projects with the same relative path.
4. Run dependency spikes:
   - text buffer/rope implementation;
   - Tree-sitter versus fallback highlighter, grammar licensing/packaging;
   - GPUI multiline text input/caret/selection behavior on 0.2.2.
5. Establish passing baseline and record exact commands/results.

**Exit:** written contracts, fixture inventory, spike decisions, baseline
results. No editor code yet.

### B — Document domain and bounded I/O

1. Add `DocumentId`, `EditorCommand`, validation, result/error types to core.
2. Add bounded `read_text`/`write_text`, language detection, binary/encoding
   policy, atomic-save helper, and external-change metadata to context.
3. Keep core GPUI-free and context UI-free.
4. Add pure tests first:
   - traversal/symlink/directory rejection;
   - missing/unreadable files;
   - binary/invalid-encoding/oversize handling;
   - at-cap/over-cap boundaries;
   - stable error codes and no-effect failures;
   - project/root identity separation.

**Exit:** bounded document I/O proven without UI.

### C — Document store and dispatcher integration

1. Implement desktop `DocumentStore`:
   - open/close/save/revert;
   - dirty tracking;
   - mtime/size generation;
   - undo/redo bounds;
   - multi-document isolation;
   - root/project invalidation.
2. Wire lifecycle/save mutations through the dispatcher.
3. Add router/dispatcher tests:
   - successful open/save/close;
   - stale project/root/path;
   - missing/binary/oversize;
   - external-change conflict;
   - concurrent save;
   - shutdown with dirty documents.
4. Prove no terminal session is created, replaced, or disturbed by document
   operations.

**Exit:** document lifecycle works headlessly through shared operations.

### D — Editing surface, caret, and highlight pipeline

1. Implement native multiline editing:
   - caret motion/home/end;
   - selection with keyboard and pointer;
   - insertion/deletion;
   - clipboard cut/copy/paste;
   - undo/redo;
   - Unicode-safe cursor columns.
2. Implement background highlighting:
   - language detection;
   - token generation off-thread;
   - cancellable per-document generation;
   - plain-text fallback;
   - visible-range application.
3. Add input ownership:
   - editor owns keyboard while focused;
   - `Ctrl+S` saves only with an editor focus;
   - Escape/close behavior restores valid focus.
4. Add pure/GPUI-harness tests for buffer, caret, undo, highlight spans,
   stale highlight rejection, and focus transitions.

**Exit:** editing works without file corruption, highlight races, or focus
leaks.

### E — Tabs, dirty UX, external changes, and integration

1. Add document tabs, dirty markers, close prompts, save/revert controls.
2. Integrate entry points:
   - Files tree open;
   - `Ctrl+P` file open;
   - palette file open;
   - diff/Git path open.
3. Implement external-change flow:
   - detect on focus/save;
   - show explicit conflict state;
   - reload/revert and save-anyway paths are explicit and tested.
4. Preserve terminal focus/session behavior when opening/closing documents.
5. Add dispatcher/IPC/CLI parity tests for the frozen automation contract.

**Exit:** complete user-facing open/edit/save loop with safe conflict
handling.

### F — Release acceptance and handoff

1. Run full workspace gates on the delivered revision.
2. Run release Wayland validation with isolated config/state/runtime paths.
3. Record resource behavior: open/edit/highlight/save timings, worker counts,
   FD/thread/RSS stability.
4. Update milestone docs, IPC/CLI tables, dependencies, status, and acceptance
  matrix.
5. Explicitly hand remaining editor work (find/replace, more languages,
   pane splits, persistence expansion) to follow-up milestones.

**Exit:** M19 acceptance has linked automated/live evidence.

## 9. Verification matrix

| Area | Automated checks | Native evidence |
|---|---|---|
| Open | Root/boundary/traversal, missing/binary/encoding/cap matrix | Open from tree, finder, palette, diff/Git path |
| Edit | Buffer/caret/selection/clipboard/undo/Unicode tests | Type/edit/select/copy/paste across languages |
| Highlight | Language detection, fallback, stale-token rejection | Correct colors for initial language set; fallback visible |
| Save | Atomic write, permission/error paths, dirty transitions | `Ctrl+S`, dirty marker clears, file bytes match |
| External change | mtime/size conflict, reload/revert paths | Modify externally, attempt save, resolve explicitly |
| Lifecycle | Multi-document isolation, root/project invalidation | Switch tabs/projects, close with dirty prompt |
| Focus | Editor/terminal/palette/input ownership | No keystroke leaks in either direction |
| Parity | Dispatcher/IPC/CLI equivalence for frozen contract | Same-instance CLI/document behavior |
| Resources | Repeated open/edit/save/close cycles | FD/thread/RSS stability, no idle repaint loop |
| Privacy | No file/clipboard/token logging | Redaction audit for new categories/fields |

Required commands for Rust completion:

```bash
cargo fmt --all --check
cargo test --workspace
cargo test --workspace -- --test-threads=1
cargo clippy --workspace --all-targets -- -D warnings
cargo build --release --bin omaterm --bin omaterm-desktop
python3 scripts/check-docs.py
git diff --check
```

A timeout, omitted target, or unavailable desktop interaction is not a pass.
Planned tests are not proof.

### Release Wayland script

Use isolated config/state/runtime paths and disposable repositories. Record
toolchain/commit, compositor/scale/font/viewport, fixture size, captures, and
commands.

1. Open documents from all four entry points using real keys/pointer.
2. Edit, select, copy/paste, undo/redo, save; verify on-disk bytes.
3. Open Unicode/tab/long-line/binary/oversize files; verify fallback states.
4. Externally modify an open dirty file; verify conflict handling.
5. Switch projects/tabs/terminals/documents; verify buffers and sessions.
6. Close with unsaved changes; verify prompt and focus restoration.
7. Repeat open/edit/save/close cycles; compare FD/thread/RSS trends.
8. Capture document/tab/dirty/conflict/error states at standard and narrow
   widths.

## 10. Risks and mitigations

| Risk | Mitigation |
|---|---|
| Editor entangles PTY/pane lifecycle | Keep documents outside `PaneContent`; preserve terminal-only splits initially |
| Full-file reads become unbounded | Enforce size/line caps before allocation; stream or reject oversize explicitly |
| Highlight blocks UI | Tokenize off-thread with generation/cancellation and visible-range use |
| Silent data loss on save | Atomic writes, external-change generations, explicit conflict UX |
| Focus leaks between editor/terminal | Single tested input-ownership model; verify both directions |
| IPC contract drift | Freeze Option A/B in Phase A; update tables/checker/tests together |
| Grammar license/API risk | Spike versions/licenses before adding dependencies; fallback highlighter available |
| Scope creep toward IDE | Enforce §34 non-goals at every phase review |

## 11. Completion checklist and next action

- [ ] M15/M17 prerequisites closed or explicitly re-sequenced.
- [ ] Document identity, persistence, IPC/CLI, and focus contracts frozen.
- [ ] Bounded I/O, language/highlight pipeline, and buffer behavior proven.
- [ ] Dispatcher-integrated lifecycle with stable errors and no-effect failures.
- [ ] Native editing/highlight/save/conflict/focus flows proven on Wayland.
- [ ] Quality gates green; resource/redaction evidence recorded.
- [ ] Milestone, overview, acceptance matrix, dependencies, and status updated.

**Next action:** freeze the Phase A contracts — especially document surface,
snapshot impact, and the `file.open` compatibility decision — then create
fixtures and run buffer/highlighter/GPUI spikes.

## 12. Phase A frozen contracts (2026-10-04)

Frozen before any editor code:

- **Automation contract:** Option A. `file.open` keeps terminal-routing
  semantics; editor lifecycle stays UI-local in first delivery. No new wire
  method until an automation need is proven.
- **Document surface:** open documents live alongside terminal tabs, not in
  `PaneContent`. Splits remain terminal-only in first delivery.
- **Persistence:** persist the open-document list per project, never dirty
  contents. Unsaved changes require an explicit user decision on close and
  shutdown. Restart reopens clean on-disk content.
- **Bounds:** 1 MiB file cap, 20,000-line cap, enforced before allocation.
- **Encoding:** invalid UTF-8 is rejected with a reason; never silently
  converted. Binary content is refused as text.
- **First languages:** Rust, Markdown, TOML, JSON, Bash; everything else is
  plain text until the pipeline is proven.
- **Fixtures:** `/tmp/opencode/m19-editor-fixtures/` (`proj-a`, `proj-b` with
  the same relative path): small per-language files, Unicode/combining/tab/
  long-line cases, binary, invalid-UTF-8, 1 MiB oversize, duplicate-path
  project pair. Temporary artifacts, not acceptance records.
- **Spike findings so far:** no editor/highlighter buffer exists in-tree;
  GPUI 0.2.2 exposes `shape_line`/`resolve_font` but no text-field widget in
  `elements/`, so multiline editing extends the existing commit-input/palette
  caret patterns. Highlighter implementation (Tree-sitter vs. fallback) is
  still open pending version/license verification; permissive licenses only.
