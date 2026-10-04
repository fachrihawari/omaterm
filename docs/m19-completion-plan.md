# M19 — Comprehensive Completion Plan

**Date:** 2026-10-03. **Baseline:** `719926b`.
**Status:** execution plan; the work and acceptance below are not yet passes.
**Goal:** close M19 with a reliable native open → edit → highlight → save →
close/restart loop, without expanding into an IDE.

**Historical execution update — 2026-10-04:** the delivered source was dirty
`f287991+worktree`, parent `f287991f223e01a36ea5e0a60c219c824da05ba3`.
Its format, parallel **606** tests, serial **606** tests, workspace/all-target
Clippy and release-build passes used `RUSTUP_TOOLCHAIN=1.99.0`; see the
[S9 report](evidence/m19-s9-report.md) and [manifest](evidence/m19-s9-manifest.json).
This evidence does not identify the current checkout without a source comparison
and rerun. The baseline audit below is historical. Root resolution is now off-thread;
I/O admission bounds one active + 16 queued jobs and a 17-result mailbox;
32 document slots are workspace-wide including reservations/restore entries.
Metadata-only snapshots/bounded restore/Retry, committed-save baseline
acknowledgement and observed-revision overwrite checks are implemented.
S6 now includes `EntityInputHandler`, marked composition and surface/input-owner
cancellation; navigation is line-local and snapshots indexed/shared. These are
implementation corrections, not completed native exit criteria.

The [current native run](evidence/m19-native-current.md) is blocked by user-focus
change before first input. Startup, terminal IPC and 20 real process samples
are partial evidence only; zero editor cycles and no graceful exit/restart.
S9, E01–E10 and the full-native checkboxes below remain open.

## 1. Authority, scope and sequencing

Read this document with the [M19 implementation contract](m19-basic-editor-implementation-plan.md),
[status record](status.md), [acceptance matrix](acceptance-matrix.md), and
[agent instructions](../AGENTS.md). Blueprint §34 governs editor scope;
§62, §63, §64, §66 and §67 govern shared operations, identity, bounds,
cancellation and shutdown. The editor presentation follows section 14 of the
[UI v5 plan](ui-v5-pixel-perfect-plan.md).

This document is the **remaining-work execution sequence**. The original M19
plan retains its product contracts and historical A–F phases. Implement S0–S9
below in dependency order; finishing a slice requires its actual exit evidence.

Frozen decisions:

- Documents live alongside terminal tabs; `PaneContent` and terminal splits
  remain terminal-only. Switching surfaces does not recreate PTYs.
- Lifecycle mutations use the shared semantic dispatcher. UI text editing
  mutates the owner-side store; filesystem work belongs to context helpers.
- Option A remains: CLI/IPC `file.open` submits `$EDITOR` to a terminal. Native
  desktop opening has its own editor lifecycle; no new editor wire methods.
- Persist the open-document registry, never dirty text, clipboard contents,
  undo stacks, highlights, runtime handles or capability tokens. Restart loads
  clean on-disk content with fresh terminal processes.
- Files are existing, root-contained UTF-8 text; reject binary/NUL, invalid
  UTF-8, directories and other non-regular files. No implicit lossy conversion.
- File cap: 1 MiB; line cap: 20,000; path cap: 4096 bytes. Four-column tab
  stops and no soft wrap remain the first-delivery behavior.
- Rust, Markdown, TOML, JSON and Bash use the built-in highlighter; unknown
  languages fall back to plain text. No grammar downloads.
- Find/replace, more languages, editor splits, new-file/Save As workflows,
  LSP, diagnostics, autocomplete, refactoring, debugger and extensions are
  outside this completion effort.

The user authorized editor execution ahead of remaining M15/M16/M17 closure.
Record this as execution re-sequencing, not evidence that those milestones
passed. Shared root/palette/diff regressions must still pass; the full M18
process panel is not added to M19. Version assignment remains outside this plan.

## 2. Baseline: what exists and what still blocks closure

Evidence below belongs to `719926b` and its immediately preceding validation
builds. Later changes require checks on their delivered revision.

| Area | Implemented / demonstrated | Remaining requirement |
|---|---|---|
| Domain | Typed `DocumentId`, editor commands, validation, stable errors | Explicit operation/document/root generations and stronger identity |
| I/O | Capped reads, regular-file/encoding checks, exclusive sidecars, permission preservation, file/directory sync | Descriptor-rooted race handling, content-aware conflict detection, cancellation and failure outcomes |
| Store | Dedup, saved-text dirty tracking, bounded per-document undo, valid history caret endpoints | Aggregate bounds, generation-aware save acknowledgement, efficient line/grapheme indices |
| Dispatch | Authorized open/save/revert/close; dirty close/project deletion rejects without effects | Editor dispatch still does synchronous root/filesystem work on the owner thread |
| Persistence | Existing workspace schema 2, with v1 decoding | No open-document registry, schema migration or document restart restore |
| Lifecycle | Cancel/Save/Discard prompt, live close/revert/shutdown proof | Multi-document async confirmation, failed reload retention, project-delete UX and pending-operation transitions |
| Input | Native keyboard/pointer editing, grapheme movement, both clipboard chords; Files-search Ctrl+Enter proven | Full native text/IME path, explicit focus ownership, preferred vertical column, gutter/drag/reveal coverage |
| Highlight | Worker cancellation, generation acceptance, bounded wakeup; Rust/Markdown observed | All-language/fallback proof, delayed-completion and pathological-input tests, visible token indexing |
| Integration | Interim Ctrl gestures in palette/tree/Git/search | Default native desktop open, explicit terminal fallback action, direct diff open and surface routing |
| Resources | Ten small-file cycles: 45 FDs, 30 threads, warmed RSS ~97 MiB | Cap-sized, aggregate, queued-operation, restart and idle measurements |
| Gates | 494 tests pass parallel/serial, format/Clippy/release/docs checks | Rerun full gates and native script on the final revision |

Concrete code-audit findings that shape the next slices:

1. `DocumentStore::by_path` keys only `(project, absolute path)`; the captured
   root is a pathname. Replacement of the root at the same pathname is not
   distinguished from the original directory.
2. Context canonicalizes before opening and before rename. Final-component
   `O_NOFOLLOW` protects one step; mutable ancestors and the last
   check-to-rename interval still need a defined contract.
3. `FileRevision` has size/mtime/device/inode, but no content comparison.
   Same-inode, same-size content changes with preserved timestamps can evade it.
4. `editor_resolve_pending` calls Save synchronously and treats any successful
   dispatch result as completion. It must understand async pending receipts.
   Its Discard → Revert path resets the buffer before a new read can fail.
5. Rendering and navigation repeatedly call `buffer_text`, rebuild line maps,
   or scan graphemes from the beginning. Visible rows scan the full span list.
   Virtual rows alone do not prove O(visible) rendering or O(line) movement.
6. `close_project` ignores dispatch errors. Dirty project deletion is blocked,
   but the user needs an actionable explanation and resolution flow.
7. Highlight completion now wakes the UI, but caret blink, CRLF presentation,
   horizontal caret reveal and full input ownership remain incomplete.

## 3. Delivery map

| Slice | Purpose | Depends on | Main code ownership |
|---|---|---|---|
| S0 | Freeze remaining API/behavior details and reproducible fixtures | Baseline audit | Docs, test fixtures |
| S1 | Root identity, descriptor-rooted I/O and precise write outcomes | S0 | `omaterm-context`, editor store |
| S2 | Async lifecycle prepare/work/commit and generation-safe saves | S1 | Desktop router, editor worker/store |
| S3 | Bounded document model, indexed buffer and render snapshots | S2 | Desktop editor model, renderer |
| S4 | Schema-3 document registry and bounded restart restore | S1–S3 | `omaterm-state`, router, desktop restore |
| S5 | Transactional dirty/conflict/project/shutdown UX | S2–S4 | Editor lifecycle model, dispatcher, UI |
| S6 | Native text input, focus, caret, selection and viewport correctness | S3, S5 | GPUI editor view/input adapter |
| S7 | Complete entry points and terminal/editor/diff integration | S2, S5, S6 | Files, palette, Git/diff, shell coordination |
| S8 | Highlight correctness, visible-range application and resources | S3, S6, S7 | Highlighter, render index, instrumentation |
| S9 | Final automated/native acceptance and milestone handoff | S0–S8 | Tests, desktop validation, docs |

Keep each slice reviewable and independently verified. Extract editor-specific
view/input/worker code into private modules as boundaries become clear; do not
rewrite the entire application coordinator or create speculative crates.

## 4. S0 — Remaining contracts and fixtures

### Deliverables

- Specify operation identity, root identity, document version, save baseline,
  active surface, focus owner, conflict state and dirty-action state before
  modifying async callers. Rust signatures remain illustrative until APIs are
  checked against the selected dependencies.
- Confirm that desktop primary file activation opens natively, while Git row
  selection still previews a diff. Name the explicit terminal-open action.
- Define schema-3 registry fields, old-schema migration, selection restoration
  and original path-byte encoding; do not rely on lossy display strings.
- Record the distinction between cancellation before a save commits and an
  already-committed write whose acknowledgement arrives later.
- Keep the existing toolchain pin until an explicit toolchain change is made;
  record the actual environment override/revisions used for every check.

### Reproducible fixture set

Add a checked-in fixture generator/test helpers, not references solely to
ephemeral `/tmp` contents. Generate disposable projects A/B with:

- All five initial languages, plain text and an empty file.
- Combining text, CJK, emoji/ZWJ sequences, tabs, spaces, LF, CRLF, mixed
  newlines, trailing/no-final newline, and a very long single line.
- At-cap and over-cap byte/line/path inputs; one pathological token-dense file.
- Binary/NUL, invalid UTF-8, unreadable file, directory, FIFO and missing file.
- Same relative filename in two projects; contained and escaping symlinks;
  symlinked ancestors, root replacement and renamed/replaced target files.
- External edits of different size and same size/preserved timestamps; atomic
  replacement; save failures at temp creation/write/sync/rename/directory sync.
- Filenames with spaces/Unicode and Linux non-UTF-8 path bytes.

Race tests use barriers or injected filesystem seams, not sleeps chosen to win
a race. Native fixtures are disposable and never alter the user's repositories.

**Exit:** contract notes, fixture manifest and exact creation commands exist;
every remaining requirement is mapped to one slice and an acceptance case.

### S0 contract record

The following contracts are fixed before S1/S2 APIs are introduced:

| Concern | Contract |
|---|---|
| Document key | `(ProjectId, RootIdentity, relative path bytes)`. Display strings are never a key. S1 supplies `RootIdentity` from a captured canonical root path plus device/inode and owned root descriptor. |
| Buffer version | A monotonically increasing owner-side `u64`, incremented once for every accepted text replacement, undo, redo, discard or accepted disk reload. It is distinct from the highlight request generation and disk revision. |
| Save baseline | The captured `RootIdentity`, relative path bytes and `FileRevision` identify the disk target expected by a save. S1 extends the revision with a bounded content fingerprint. |
| Operation identity | A router-private monotonically increasing editor operation ID, separate from terminal launch IDs. Every accepted operation captures project/root/document/buffer versions and has exactly one final outcome. |
| Save outcome | `not_committed`, `committed_durable`, or `committed_durability_warning`. Cancellation before rename is `not_committed`; cancellation or acknowledgement loss after rename does not erase the committed outcome. |
| Active surface | `Terminal`, `Editor(DocumentId)`, or `Diff`; changing surfaces does not alter core terminal selection or PTY ownership. |
| Input owner | `Terminal`, `Editor(DocumentId)`, palette/filter/inspector field, or confirmation. An active document alone never grants editor keyboard ownership. |
| Dirty action | Typed target/action state for close, revert, project delete and shutdown, capturing document IDs and buffer versions. A stale decision is discarded rather than applied to a newer buffer. |
| Schema 3 registry | Per-project bounded descriptors: persisted `DocumentId`, root-relative `path_bytes: Vec<u8>`, captured root device/inode and an optional active `DocumentId`. JSON stores path bytes as numeric bytes. Dirty text, saved text, caret, undo/redo, tokens, clipboard, worker/descriptor handles and operation IDs are excluded. |
| Restore | Schema 1/2 migrate with no descriptors. Schema 3 restores only clean metadata, then schedules bounded reads; a root identity mismatch produces an unavailable document rather than opening the same path under a replacement root. |

The exact Rust DTOs remain S1/S2/S4 implementation work. The contracts above
are deliberately behavioral: they prevent a schema or worker API from silently
changing identity, commit, focus, or persistence semantics.

### Fixture generator and manifest

Generate fixtures outside a working tree with:

```bash
python3 scripts/generate-m19-fixtures.py --output /tmp/opencode/m19-fixtures
```

The generator refuses a non-empty output directory and writes
`manifest.json`, including the byte/line-cap values and fixture-relative paths.
It creates disposable `proj-a` and `proj-b` trees, initial-language/empty/plain
files, newline/Unicode/tab/long-line cases, byte/line caps, token-dense input,
binary/invalid UTF-8/directory/FIFO/unreadable cases, contained/escaping
symlinks, mutable target/replacement material, root-replacement material and a
Linux non-UTF-8 filename. The same-size mutable pair has a shared fixed mtime
for preserved-timestamp conflict tests. The missing-file case is intentionally
absent.

The manifest distinguishes filesystem fixtures from synthetic/injected cases:
the 4097-byte path test constructs a `PathBuf` without attempting an impossible
filesystem entry; save failures use injected temp-create/write/sync/rename/
directory-sync seams; and root/ancestor/final-component races use deterministic
barriers. The generator does not claim that a permission test is effective when
executed as a privileged user.

## 5. S1 — Identity and filesystem correctness

### Implementation

1. Introduce a context-owned root handle/identity: canonical root path plus
   device/inode identity and an owned directory descriptor for I/O. Store the
   captured value in each document; dedup includes project and root identity.
   Reopening an unchanged key returns its live buffer without re-reading it.
2. Resolve relative paths and open descendants beneath that descriptor. Spike
   Linux `openat2` containment flags and a descriptor-relative component-walk
   fallback against the supported kernel. Existing contained-symlink behavior
   must remain supported or fail explicitly under a documented race; never
   silently fall back to an unguarded canonicalize/open sequence.
3. Use descriptor metadata and bounded chunked reads. Check cancellation,
   encoding, regular-file shape, byte/line limits and captured identity before
   publishing a document. Refuse a replaced project root rather than rebinding
   the buffer to whatever now occupies the pathname.
4. Extend the revision with a bounded content comparison/fingerprint so
   same-size/same-timestamp in-place changes conflict. Verify metadata and
   contents from the same opened object, including clean Save/focus refresh.
   Select any hashing dependency only after version/license review.
5. Create/write/sync a same-directory sidecar through the captured parent
   descriptor; revalidate destination and root, then descriptor-relative rename
   and directory sync. Preserve the original file's permissions and the prior
   saved baseline until the operation outcome is known.
6. Return explicit outcomes: not committed, committed/durable, or committed
   with durability warning. A directory-sync failure after rename must not be
   reported as if disk bytes were untouched. Do not remove another writer's
   sidecar; define owned-sidecar cleanup on cancellation/failure.

A content check followed by rename is **not** atomic compare-and-swap against
an arbitrary external writer. Describe the supported consistency window
honestly; do not claim that advisory locks or a second stat eliminate it.
The required behavior is detection of observable external changes, refusal of
stale targets, and a defined commit outcome. Record any kernel/filesystem
blocker with reproduction and alternatives before changing this contract.

### Tests and exit

- Replace the root at the same pathname and prove open/save/revert do not touch
  the new root or merge its documents with the original buffer.
- Swap an ancestor/final symlink at deterministic barriers; verify no reads or
  writes escape the root. Verify permitted contained aliases dedup correctly.
- Same-size/same-timestamp external changes conflict; changing permissions,
  deleting/replacing a target, and unreadable/non-regular targets fail clearly.
- At-cap reads succeed; over-cap/growing reads stop boundedly. Cancelled reads
  do not publish partial text.
- Failure injection proves old bytes before rename, correct new bytes after
  commit, consistent revision reporting and no owned-sidecar leaks.

**Exit:** identity and I/O outcomes are proven independently of GPUI.

## 6. S2 — Async editor operations and race-safe save acknowledgement

### Ownership and scheduling

Use one shared prepare → worker → owner-commit path for editor Open, Save,
Revert, conflict checks and restart loads. Keep Close/Discard in memory unless
they are waiting for pending work. Authorization and pure validation run
before enqueue; filesystem/root resolution itself must not block the UI owner.

- An internal typed editor operation ID must not collide with terminal launch
  IDs. Reuse the common pending-receipt mechanism only after auditing every
  caller; do not treat an editor operation as a PTY launch.
- Capture project ID/generation, root identity, document ID/version, supplied
  path, expected disk revision, operation kind and cancellation/deadline state.
- Start with one I/O worker and a bounded queue. Prioritize explicit foreground
  actions over restart batches; serialize writes per document. Supersede
  disposable reads/checks, but never silently drop a save with an outstanding
  receipt or allow two competing saves to commit out of order.
- Completion uses a bounded wakeup/mailbox; no polling/render-time disk I/O,
  thread-per-keystroke or unbounded detached tasks.
- Saturation/cancellation/error returns a stable result without changing
  registry, selection, persistence or terminal ownership.

### Save while the user keeps typing

Save captures text/version **G**. If the user edits to **G+1** before completion:

- Disk contains the captured G text if the write commits.
- The owner adopts that text/revision as the saved baseline, while keeping
  live G+1 text and its undo/caret state; dirty remains true unless equal.
- A stale completion cannot replace the live text, clear a newer dirty flag,
  retarget another document or change the active project.
- Even if a committed save's view is retired, record its actual disk outcome;
  silently ignoring the completion cannot undo the filesystem side effect.

Save/reload/close/shutdown callers wait for final results, not just
`CommandOutput::Pending`. Define cancellation before commit versus after
rename. Slow/blocking filesystem calls cannot be made hard-cancellable by an
atomic flag alone; test cooperative boundaries and report platform limitations
instead of claiming a hard syscall deadline.

### Tests and exit

Injected slow workers prove owner dispatch remains responsive, latest read
acceptance, write serialization, queue saturation, cancelled queued work,
project/root change during work, close during work, duplicate completions,
save G/edit G+1, revert/edit races and shutdown ordering. Assert no terminal
sessions are created/replaced by document operations.

**Exit:** all editor filesystem work is off the UI thread; owner commits are
generation guarded, and each accepted operation has one final outcome.

## 7. S3 — Bounded store, indexed text and render snapshots

### Implementation

- Introduce a monotonically increasing buffer version distinct from highlight
  request generation and disk revision. Savepoints/history remain per document.
- Maintain a line index once per edit/revert; use the affected line's grapheme
  boundaries for motion/clamping. Preserve a preferred visual column through
  short lines during repeated Up/Down; reset it on horizontal movement/edit.
- Share immutable render/highlight/save snapshots by version. Avoid copying a
  1 MiB buffer or scanning from byte zero on every pointer move/caret/blink.
  A bounded full rebuild after an edit may be acceptable if measured; navigation
  and unchanged rendering must use cached/indexed state.
- Index token spans by line/range; each visible row visits its intersecting
  spans, not all document tokens. Keep row element count proportional to the
  visible range plus documented overscan.
- Bound documents, retained snapshots, histories, tokens and operation payloads
  in aggregate. Closing/deleting retires all related state and queued work.

Initial engineering limits to verify in S0/S3, not historical guarantees:

| Resource | Proposed initial ceiling |
|---|---|
| Open documents | 32 across the workspace, including pending-open reservations |
| Live + saved text | 64 MiB aggregate, retaining the existing 1 MiB/document limit |
| Undo/redo | Existing 100 entries / 8 MiB per document, plus 64 MiB aggregate |
| I/O queue | One active job + 16 pending jobs; reserve capacity before copying payloads |
| Highlight | One active + one replaceable pending request + one result; span count/bytes bounded by validated text |

Compute the total retained-memory budget including Arc snapshots, redo,
sidecar buffers and pending saves. Tests must prove rejection/eviction is
explicit and never evicts a dirty live document. Change a proposed number only
with measurements and an updated shared limit/snapshot limit record.

**Exit:** multi-document/cap tests prove bounds and indexed motion/rendering;
instrumentation demonstrates no full-document copy/scan on an unchanged frame.

## 8. S4 — Persist open documents and restore them safely

### Schema and capture

- Extend the existing `omaterm-state` snapshot to **schema 3**; migrate both
  schema 1 and schema 2 with an empty document registry. Keep the existing
  snapshot filename/store/recovery behavior; do not add a parallel writer.
- Add bounded per-project document descriptors and an optional active document
  reference. Descriptors contain logical ID/path and captured root identity;
  encode original path bytes losslessly. Final DTO shape is fixed in S0.
- Preserve terminal selection separately from active document surface. Do not
  persist focused GPUI handles, operation IDs, file descriptors or session IDs.
- Validate IDs/duplicates, owning project, root/relative path, selection
  references, document counts and serialized-byte caps before installing state.
- Open/close/active-document selection marks registry persistence dirty through
  shared effects. Reopening the same live key does not add a descriptor.
  Text keystrokes do not put text into snapshots or trigger registry writes.

### Restore

Install validated logical workspace state; schedule bounded document reads via
S2, independent of fresh terminal launches. Load saved disk text only. A missing,
binary, too-large or inaccessible file yields a metadata-only unavailable
document state with Retry/Close; it does not invalidate otherwise valid terminal
layout or overwrite a corrupt/unsupported original snapshot.

A root mismatch never loads the same relative filename from an unrelated new
root. Restore selection only when its target is valid, without stealing focus
from a user action made after startup. Failed entries retain an explicit
retry/removal path; duplicate restored/opened keys must converge on one buffer.

### Tests and exit

- v1/v2 → v3 migration preserves existing layout/CWD/sidebar fields.
- Registry roundtrip, invalid refs/duplicates/path encodings and all caps.
- Snapshot bytes contain no dirty text/history/clipboard/token content.
- Missing/corrupt/future snapshot recovery preserves original bytes.
- Saved open documents reappear clean after normal close/restart; Save and
  Discard shutdown choices restore only disk text. Crash/restart never claims
  dirty recovery. Fresh terminal session IDs and liveness are independently
  verified.
- Removed files, replaced roots, two-project identical paths and restore/open
  races remain scoped and do not block startup interaction.

**Exit:** automated migration/restore tests and real restart evidence prove the
frozen registry-persistence contract.

## 9. S5 — Dirty actions, conflicts and shutdown as explicit state machines

Replace numeric prompt choices and loose pending flags with typed, testable
states: requested action/target, checking, awaiting decision, saving/reloading,
failed, committed/cancelled. Prompts show the target filename/project and, for
shutdown, the number of dirty documents. Use a transient confirmation surface
following the UI plan instead of permanently shifting editor chrome.

Required behavior:

- **Close:** Cancel retains everything; Save waits for durable/final save
  outcome; Discard explicitly closes. Closing one document selects a valid
  sibling or remembered terminal and retires worker/view caches.
- **Reload/Revert:** read/validate first; only replace text and clear undo after
  a successful, generation-accepted reload. Failed reload retains dirty text.
  A destructive confirmation is tied to the captured document/version.
- **Project deletion:** explain dirty rejection and offer Save all / Discard /
  Cancel. Route the final delete through shared dispatch. CLI deletion retains
  the stable conflict result; it must not silently discard desktop buffers.
- **External changes:** check asynchronously on activation/focus and before
  saving. Clean buffers show changed-on-disk status/reload; dirty buffers keep
  their text and show Reload / Overwrite / Cancel choices.
- **Overwrite:** require an explicit decision scoped to the currently observed
  disk revision. Recheck it and containment; another external change prompts
  again. It bypasses neither root identity nor validation, and is not implemented
  as an unchecked `expected: None` write.
- **Shutdown:** include hidden documents from every project. Save all waits for
  each outcome; any failure blocks teardown and keeps unresolved buffers.
  Cancel resumes normal input; Discard is explicit. Cancel or drain pending
  operations at their defined commit boundary, then persist registry, stop
  workers/IPC, reap terminal processes and remove the socket normally.
- Do not promise transactionally rolling back already-successful disk saves if
  another document's save fails. Report which documents saved and which remain.

Test all choices with multiple dirty documents, delayed I/O, failures, repeated
close requests, stale prompt targets, selection/project changes, root replacement
and external modification between confirmation and save. Include normal app
window close as well as owner/view cleanup; abrupt SIGKILL is not graceful-close
evidence.

**Exit:** no required lifecycle path silently discards dirty text or equates a
pending receipt with completion; failures leave a reachable resolution path.

## 10. S6 — Native input, focus and editor presentation

### Input and focus

- Give each interactive surface an explicit input owner: editor, terminal,
  palette, Files filter, Git commit input or confirmation. An active document
  alone is not evidence it owns keyboard input.
- Verify GPUI 0.2.2's native text-input APIs, then implement text insertion and
  IME composition through its input-handler contract. Key events own commands
  and motion; avoid double insertion from both key characters and committed text.
- Test ordinary Unicode commits, combining/ZWJ text, composition/preedit,
  cancellation, multiline clipboard and both copy/paste chord forms. Composition
  and clipboard text never reach a PTY or application logs.
- Palette open/cancel/activate, inspector fields, document chips, project switches,
  prompts, terminal tabs and window focus loss restore a valid owner. Editor
  Ctrl+S operates only when editor input owns it.

### Caret, selection, lines and viewport

- Deadline-driven caret blink: 1000ms cycle, 50% stepped visibility per UI v5;
  reset on edit/motion, pause for hidden/unfocused/composing surfaces and teardown.
  Blink can schedule its deadlines; it must not create an unrelated paint loop.
- Preserve original LF/CRLF bytes, render CRLF as one line ending, and insert
  Enter using the document's established newline convention. Mixed newline
  files are preserved; no whole-file normalization on open/save.
- Gutter click selects the complete line; Shift-click and drag extend
  deterministically. Drag across rows and outside the viewport has bounded
  autoscroll; release/window-focus loss always terminates capture.
- Correct shaped hit-testing for tabs, combining/CJK/emoji, selection endpoints,
  gutter clicks and scroll offsets. Include reverse and Shift-drag.
  Compute visual columns/tab stops from grapheme clusters: summing individual
  character widths is insufficient for emoji/ZWJ sequences. Rendering, footer,
  vertical motion and hit-testing must use the same mapping.
- Reveal caret horizontally and vertically without recentering every motion.
  Long lines must reach their true end; switching documents preserves their
  caret, selection and scroll state.
- Narrow windows retain usable code and reachable Save/Revert/Close/conflict
  actions; document chips scroll or overflow rather than disappearing.
- Use existing theme/font/geometry roles. Keep editor view/input extraction
  separate from changes to PTY geometry/session ownership.

**Exit:** pure input/focus/line-map tests plus native composition, Unicode,
drag/autoscroll, blink and narrow-layout proof; no input leaks either direction.

## 11. S7 — Complete desktop entry points and shared surface integration

| Entry | Final desktop behavior | Compatibility |
|---|---|---|
| Files primary file activation | Native editor opens/focuses existing text file | Directories still expand/collapse; explicit Open in Terminal remains |
| Ctrl+P file result | Enter/click opens natively after a valid result is ready | Interim Ctrl gestures may remain aliases |
| General palette file result | Same native open path and document dedup | Command results retain semantic dispatch |
| Files-filter activation | Enter opens matching file natively | Filtering/modifier handling does not forward to a PTY |
| Git status row | Ordinary selection continues to preview diff | Explicit Open File action opens the native document |
| Diff preview Open File | Opens its original typed project/path, optionally at a valid source line | Hunk navigation/staging remains diff behavior; deleted/binary targets report truthfully |
| Open in Terminal action | Dispatches existing `FileCommand::Open` | CLI/IPC `file.open` remains identical |

Complete Terminal/Editor/Diff surface routing, including the UI v5 Ctrl+1/2/3
contract after auditing collisions. No document/preview produces a truthful
unavailable state, not a fake default file. Do not mutate the selected core
terminal tab just to display an editor or diff.

Async activation captures the result identity/root/project; late completion
may register a valid document but cannot steal focus after the user switched
targets. Keep readiness visible; early activation with no result is not a pass
and must not open an unrelated item. Preserve palette MRU semantics after final
successful activation, and test file-open errors without fallback PTY submission.

Test every entry route against the same dispatcher/store identity, including
dedup, two projects, root changes, errors and stale async results. Add parity
regressions showing terminal `file.open` still submits `$EDITOR`, existing CLI
maps remain intact, and native opens do not spawn replacement terminal sessions.

**Exit:** default native opening, explicit terminal opening and all advertised
surface/entry actions have automated and native evidence.

## 12. S8 — Highlight correctness and realistic resource behavior

- Retain the built-in highlighter; test token boundaries/sorted non-overlap for
  all initial languages, escapes, unterminated constructs, nested Rust comments,
  negative/exponent JSON numbers, shell `$#`, Markdown and plain fallback.
  Supported syntax is documented as a subset, not a full parser guarantee.
- Stress long strings/comments, token-dense cap input and rapid edit/switch/
  close. Check cooperative cancellation inside lengthy scans, not solely at
  job completion. Keep active/pending/result and token-memory limits enforced.
- Prove stale results cannot paint newer text or reopen retired view state;
  delayed results wake the UI without requiring another keystroke.
- Use S3's visible-span index and shared snapshots. Instrument rendered row
  counts, consulted spans, buffer-copy counts, queue depths and worker counts.
- Measure real operation timing separately from scripted key delays:
  enqueue-to-ready open, edit-to-frame, edit-to-highlight, save-to-commit,
  close-to-retirement and restart-to-usable. Record p50/p95, sample count and
  machine/filesystem/load details. Set regression budgets from this measured
  baseline before release; do not label the earlier ~3.05s scripted cycle as
  application latency.
- Repeat at least 20 cycles for small and cap-sized files, plus the document
  count/history/queue limits. Measure after warmup and after cleanup:
  FDs, threads, RSS, retained document bytes, worker/queue counts and idle CPU.
  RSS need not return to cold start; persistent growth beyond the declared
  retained-memory budget or unreleased FDs/threads requires investigation.
- Observe visible-editor idle with expected blink only; then terminal-active,
  hidden-editor and unfocused-window idle. No file/highlight polling or hidden
  caret repaint loop. Confirm no owned sidecars/tasks survive close/shutdown.

**Exit:** all-language/fallback visuals, pathological-input tests and measured
cap/aggregate/idle resource results are attached to the delivered revision.

## 13. S9 — Final verification script and closure

### Automated gates

Run from the workspace root, preserving exit codes and full logs:

```bash
cargo fmt --all --check
cargo test --workspace
cargo test --workspace -- --test-threads=1
cargo clippy --workspace --all-targets -- -D warnings
cargo build --release --bin omaterm --bin omaterm-desktop
python3 scripts/check-docs.py
git diff --check
```

If using `RUSTUP_TOOLCHAIN=1.99.0`, record that prefix and actual tool versions;
do not describe it as the unchanged repository pin. Validate new dependencies'
exact versions/licenses in [dependencies.md](dependencies.md). A failing/timeout
run is recorded as such; retries require diagnosis, not output filtering or skips.

### Native acceptance run

The harness returns `0` only for verified acceptance, `2` for blocked, `3` for
failed and **`4` for pending partial collection**. Semantic input requires an
explicit `--scripted-steps <flow-json>` plus `--allow-input` and a helper satisfying
the handshake/PID/outcome contract. Merely launching/collecting is not native
acceptance. It produces no synthetic measurement placeholders; empty timing
arrays mean unobserved with null percentiles, never a resource/latency pass.

Use release binaries with isolated HOME/XDG paths and generated disposable
repositories. Record commit, binary build, compositor/output/scale, font,
logical viewport, fixture sizes, commands, timing and captures. Bind the
virtual-pointer device to the chosen output; derive logical extents from current
monitor geometry. Verify the target PID/focus before injecting real input and
stop if the user's active window changes.

1. Open from Files, finder, general palette, Git and diff actions; verify
   dedup, defaults and explicit terminal fallback, including error targets.
2. Type/select/copy/cut/paste/undo/redo/save using keyboard and pointer.
   Include native text commits/composition and exact on-disk byte assertions.
3. Exercise all languages/plain fallback, Unicode/graphemes/tabs, empty/LF/CRLF,
   long lines, byte/line caps and binary/invalid UTF-8/non-regular errors.
4. Modify externally; verify clean/dirty conflict states, cancelled decisions,
   confirmed reload, confirmed overwrite and a second-change re-conflict.
5. Switch documents/projects/terminal/diff/palette/inspector fields while work
   is pending. Verify stable buffers/carets, valid focus and unchanged PTY
   identity; terminal sentinels and bounded CLI reads prove no input leaks.
6. Close/reload/delete projects with several dirty documents. Verify every
   Save/Discard/Cancel and failure path. Hidden dirty buffers must be included.
7. Close the window normally, restart and verify schema migration/registry,
   clean disk content, remembered selection, fresh terminal sessions and
   graceful unavailable-document recovery.
8. Run S8 cycles/cap/aggregate/idle measurements. Capture standard and narrow
   widths, scroll ends, composition/caret/selection, prompt/conflict/error states.
9. Exit normally. Verify test unit/PIDs, shells, sockets, worker threads/tasks
   and owned temporary files cleaned up; restore the prior workspace/focus.

### Requirement-to-evidence register

| ID | Requirement | Required evidence |
|---|---|---|
| E01 | Root-contained identity and reads/writes | S1 barrier/cap/alias/root tests |
| E02 | Async lifecycle and version-safe save | S2 worker/router tests and native pending-work flow |
| E03 | Aggregate bounds and indexed rendering | S3 cap/index tests plus S8 counters |
| E04 | Metadata-only registry persistence | S4 migration/privacy tests and actual restart |
| E05 | Dirty/conflict/project/shutdown decisions | S5 state-transition tests and every native choice/failure |
| E06 | Text/IME, graphemes, selection and focus | S6 pure/GPUI tests, clipboard byte proof and native composition |
| E07 | All entry routes and unchanged terminal contract | S7 shared-route/parity tests and real activation |
| E08 | Highlight/fallback/cancellation | S8 span/generation tests and all-language captures |
| E09 | Resource bounds, idle and operation timings | S8 measured loops, counters, timing report and cleanup |
| E10 | Final regression and redaction review | Final command logs, code/log audit and doc checker |

Attach each case to its revision and fixture. Previously passing observations
are useful baseline evidence, not a substitute for checking changed paths.
Temporary screenshots alone are not a durable release record: keep a concise
committed report/manifest and the project's chosen artifact location.

## 14. Definition of done, tracking and immediate action

M19 closes only when:

- [x] S0 contracts/fixtures and execution re-sequencing are recorded.
- [ ] S1 root identity, bounded filesystem access and save outcomes pass.
- [ ] S2 all editor file I/O is off-thread and pending/commit semantics pass.
- [ ] S3 store/history/queue/snapshot bounds and indexed rendering pass.
- [ ] S4 schema-3 registry migration and real restart pass without dirty text.
- [ ] S5 dirty/conflict/project/shutdown state machines pass all choices/failures.
- [ ] S6 native text/IME, Unicode, caret, selection, scroll and focus pass.
- [ ] S7 primary native entry points, explicit terminal route and parity pass.
- [ ] S8 highlighting, cap/aggregate resources, latency and idle evidence pass.
- [ ] S9 final full gates and E01–E10 evidence are linked and reviewed.
- [ ] Status, M19 checklist, overview, acceptance matrix and dependency/license
  records agree; deferred features are named without hiding required work.

Each slice records changed files, test commands/results, native evidence,
remaining blockers and the next slice in `docs/status.md`. Commit only when
requested, after inspecting the intended diff and checks; plans themselves
do not authorize a milestone-complete claim.

**Immediate next action:** validate the delivered `f287991+worktree` in an
uninterrupted target-focused native session: entry routes, text/IME/focus,
dirty/conflict decisions, normal registry restart, both 20-cycle workloads,
cap/aggregate/idle/timing measurements and graceful cleanup. Record any uncovered
defect and its correction before rerunning affected gates. S0/S1/S2 foundation
work is implemented; `719926b` remains historical behavioral/resource evidence.
