# M15 — Diff Viewer Completion Plan

> **Historical plan (implementation landed).** The diff capabilities below are
> implemented; M15's native interaction matrix remains open. Live status in
> [status.md](status.md).

**Date:** 2026-10-03. **Status:** implementation landed; M15 native matrix open.

## 1. Goal, authority, and scope

Finish [M15](2026-09-29-15-milestone-15-diff-viewer.md) as a usable, bounded Git diff
preview with real hunk staging, keyboard navigation, copy/open actions, shared
UI/IPC/CLI operations, and release-build Wayland evidence.

The blueprint governs architecture: §23, §26, §33, §43–§45, §52–§54, and
§61–§66. The milestone is the capability contract. The
[UI fidelity plan](2026-10-02-ui-v5-fidelity-correction-plan.md), frozen reference under
`design/ui-v5/`, and [Diff/Files UX plan](2026-10-03-diff-files-ux-correction-plan.md)
govern presentation. This plan orders their M15 work and adds capability and
verification gates; it does not replace those contracts.

Split mode is already part of the approved v5 UI despite the original M15
non-goal. Retain and verify Split and Inline; record that presentation extension
in the milestone. Keep preview/scroll state view-local and core tabs
terminal-only. No editor, syntax highlighting, word diff, full-revision document
loader, commit feature expansion, or M16 palette is needed for M15 completion.

Files viewport replacement and broad sidebar fidelity remain owned by their
correction plans. Reuse shared scroll primitives where useful; M15 completion
requires correct Git-row-to-diff integration, not completion of every UI panel.

## 2. Audited baseline and gap register

The working tree was clean at this planning audit. Existing evidence is in
[status.md](status.md); historical checks are not verification of future changes.

| ID | Current source/evidence | Completion work |
|---|---|---|
| G01 | `omaterm-context/src/diff.rs` implements bounded system-Git queries and unified parsing; `tests/diff_repo.rs` has five repository tests | Audit exact cap boundaries, parser fidelity, Git configuration effects, deleted paths and literal path selection |
| G02 | Parser caps: 4 MiB stdout, 1000 files, 256 hunks/file, 1000 lines/hunk, 8 KiB/line, context 0–10 | Preserve before-allocation bounds and make truncation location and eligibility truthful |
| G03 | `DiffInfo` documentation says any loss sets envelope truncation, but parser tests currently expect envelope `false` on nested cap loss | Settle and document aggregate versus local flag semantics consistently across all layers |
| G04 | No-newline markers are discarded; line-byte loss marks the file but not the hunk; mode-only metadata is not retained | Retain enough metadata for truthful display and exact patch construction; truncated display data must never become an executable patch |
| G05 | `diff_panel.rs` has separate Inline/Split models and one replacement-pairing test | Complete pairing/anchor coverage and cache flattened presentation rows by generation |
| G06 | `main.rs` renders a native continuous Y body but builds up to 32 hunks × 200 rows, cloning data and rebuilding row models | Implement measured row virtualization and horizontal reach; remove extra render caps as hidden-data limits |
| G07 | Alt+N/P updates a hunk cursor and legacy eight-hunk offset state; the current native body has no cursor-to-scroll connection | Make navigation reveal actual hunk anchors in the active viewport |
| G08 | Copy/open helpers were removed during U3; contextual actions are deferred | Restore accessible copy-hunk, terminal-routed open-path and refresh actions |
| G09 | Header Stage/Unstage uses shared whole-file Git mutations; true partial-hunk staging is absent | Introduce a shared scoped hunk mutation and prove only the selected hunk enters the index |
| G10 | Workers query all paths with `path: None`; generation checks reject old project results | Query the selected path explicitly and guard file/side/root/mutation races; distinguish stale-result rejection from process cancellation |
| G11 | Router, bridge, protocol and CLI support Show/ListFiles; bridge has response-budget tests | Extend meaningful parity, errors, scope and escaping-heavy frame tests for new hunk operation and metadata |
| G12 | Recent targeted checks pass; a historical full run passed, later serial runs timed out in terminal tests; native diff proof remains pending | Complete final workspace gates on the delivered revision and real Wayland acceptance |

## 3. Settled implementation contracts

### Query and data fidelity

- Unstaged compares INDEX → WORKING TREE; staged compares HEAD → INDEX.
  The same path can appear on both sides with different contents.
- Run system Git off-thread, with bounded stdout/stderr, timeout, child cleanup,
  `--no-color`, `--no-ext-diff`, and explicitly disabled textconv for machine
  parsing/patch generation. Audit user config affecting prefixes, context,
  quoting, renames and binary output; pin necessary options with tests.
- Paths are root-contained and literal, including spaces, Unicode and Git
  pathspec metacharacters. Do not interpret a selected filename as a pattern.
  Deleted tracked paths must remain queryable; inspect existing boundary helper
  behavior before reusing it. Validate rename legs independently.
- The selected preview gets a single-path query rather than depending on its
  position within a capped all-files envelope. Show/ListFiles retain their
  existing bounded project-wide forms.
- Preserve exact line content, no-newline side markers, and relevant file
  metadata. Display truncation must be explicit. Byte truncation is UTF-8
  boundary-safe, not a guarantee of grapheme preservation.
- Recommended flag contract: local flags identify where loss happened;
  envelope `truncated` is true for any query/parser/wire loss. Header-only
  ListFiles intentionally omits bodies and is not truncated merely for doing so.
  Specify capped-tail/incomplete-hunk handling and count semantics before coding.

### Preview and viewport

- One project-local preview displays its selected path and side. Returning to
  a terminal tab restores input focus without recreating or suspending sessions.
- Flatten loaded data into code, hunk-header, metadata and notice rows with
  source anchors. Cache once per data/mode generation; avoid whole-envelope
  clones and whole-file text measurement during every render.
- Use the UX plan's measured 40px action header, 28px Split side headers,
  22px Split/21px Inline code rows, gutters, typography, colors and containment.
  Non-code row heights must be explicit in virtual geometry.
- Prototype locked GPUI 0.2.2 list/scroll APIs before choosing the adapter.
  Prefer native ownership; if heterogeneous rows require a custom viewport,
  document the measured blocker and use prefix-height lookup for visible rows.
- One logical Split Y offset; independent left/right X offsets; Inline X/Y.
  Preserve fractional pixel deltas, native momentum, and source anchor on mode
  switch. Headers remain fixed. Long loaded lines remain horizontally reachable.
- Initial overscan target: four rows before/after the visible range, adjusted
  only with measured evidence. Rendered rows scale with viewport size, not
  loaded hunk count. Parser caps bound data; they are not virtualization.

### True hunk staging

- Whole-file Stage/Unstage stays labeled as whole-file. Add an eligible
  unstaged hunk's distinct Stage Hunk action through `OmaCommand` and the common
  dispatcher, using the shared M14 repository/boundary/mutation infrastructure.
  Existing whole-file `GitCommand::Stage` alone cannot satisfy this contract.
- Preferred additive command: `GitCommand::StageHunk` with project, path and
  an opaque hunk identity bound to the index/worktree snapshot. Final DTO and
  method names are settled in phase C after inspecting current contracts.
  Expose equivalent typed IPC/CLI mapping; clients select identity, not pixels
  or arbitrary patch text. Preview layout itself stays off the wire.
- The backend generates an exact bounded patch from current authoritative Git
  data. Never reconstruct mutation bytes from clipped UI text or paired Split
  rows. Preserve context, path escaping and newline markers.
- Verify the snapshot/identity before staging; stale selection returns a stable
  centrally defined error and triggers refresh. Serialize OmaTerm mutations per
  repository and rely on Git's index locking for external writers. Define the
  remaining external-edit race explicitly; no check-then-apply sequence should
  be presented as an unconditional atomicity guarantee.
- Use the bounded stdin-capable Git runner for `git apply --cached` with
  appropriate exact-application options verified against installed Git. Do not
  use fallback whole-file staging, three-way application or fuzzy relocation
  that could silently stage a different edit.
- Eligibility initially requires a complete tracked text hunk and supported
  file metadata. Binary, conflicted, truncated or unsupported special shapes
  expose a clear reason and retain valid whole-file actions. Test added/deleted,
  rename and mode-change behavior before enabling those shapes. Stage-hunk
  success must leave unrelated index and working-tree changes untouched.
- A cancelled operation before commit has no mutation. If cancellation arrives
  after Git commits the index, report/reconcile that fact rather than claiming
  rollback. Shutdown and disconnect behavior follow existing dispatcher rules.

## 4. Dependency-ordered execution

### A — Pin fixtures, contracts and baseline

1. Re-read current source and status; inspect working-tree changes before edits.
2. Build deterministic disposable repos: two separated hunks in one file,
   same-path staged plus unstaged edits, 300/1000-line hunks, >32 hunks,
   >1000 files, huge Unicode/tabbed lines, no-final-newline, binary, rename,
   executable-mode-only, added/deleted and conflict cases.
3. Capture the current native diff at reference-matched bounds. Record toolchain,
   Git/GPUI revisions, font, scale, viewport and build mode.
4. Run focused baseline tests; attempt workspace serial suite with a recorded
   timeout. If it stalls, preserve the last test and inspect child/thread state.
   Fix the reproducible cause without weakening or skipping coverage.
5. Document flag/count semantics, hunk identity lifetime and mutation eligibility.

**Exit:** reproducible fixtures, contract table and baseline evidence; pending
desktop or test checks remain explicitly pending.

### B — Harden bounded queries and parser metadata

1. Write failing domain/parser tests for the G01–G04 gaps before implementation.
2. Pin machine-readable Git options and literal root-contained path handling.
3. Preserve newline/mode metadata and safe hunk completeness information needed
   by phase C. Use additive DTO fields where possible; keep dependencies GPUI-free.
4. Propagate truncation consistently, including stdout cuts exactly on a newline,
   cuts inside a hunk, line-byte loss, nested cap loss and wire reduction.
5. Audit aggregate allocation, lossy UTF-8 conversion and discarded-hunk work.
   Bound patch data separately; ListFiles must not retain body allocations.
6. Compare exact old/new line sequences and spans to raw Git, not just name lists.

**Exit:** parser/root tests prove boundary behavior and every supported shape;
exact complete data can be distinguished from display-only prefixes.

### C — Add shared partial-hunk staging

1. Inspect command/result/error, authorization and Git runner APIs. Settle the
   additive identity/command/wire contract; record compatibility and limits.
2. Write real-repository tests first: stage hunk 1 of 2, inspect index blob/raw
   cached diff, verify hunk 2 remains unstaged and working tree is unchanged.
3. Implement authoritative patch generation, freshness checks, bounded stdin,
   timeout/reap, per-repository coordination and centralized errors.
4. Add dispatcher validation/scope, protocol round trips, bridge conversion,
   CLI parser/mapping/human/JSON output and existing exit-code conventions.
5. Test already-partly-staged files, stale edits, external index changes,
   repeated submission, unsupported/truncated hunks and mutation failure.
6. After success refresh Git status and both affected diff sides; do not force
   the UI to another side or lose the user's anchor unnecessarily.

**Exit:** UI and CLI can dispatch the same operation; repository assertions
prove partial staging, unchanged unrelated edits and useful stale/error results.

### D — Finish preview model and async lifecycle

1. Replace legacy eight-hunk offset/render-window assumptions with source anchors
   and generation-cached Inline/Split presentation rows.
2. Expand pairing tests: 1:1, 1:3, 3:1, add/delete-only, multiple edit blocks,
   multiple hunks, blank lines, Unicode, zero-count spans and cap boundaries.
   Use checked numbering for malformed/overflowing spans.
3. Tag requests with project/root/path/side/context/data generation. Reject late
   responses after selection/root changes and mutations. Fetch selected paths.
4. Bound queued/in-flight workers and cache memory. Cancel obsolete processes
   where supported and always enforce timeout/reap; ignoring a result alone
   does not establish cancellation. Coalesce refresh hints without losing them.
5. Make loading, empty, binary, rename/mode-only, untracked-no-patch, conflict,
   failure and truncation states explicit; unavailable actions stay truthful.

**Exit:** deterministic state tests prove no stale-file/side landing, bounded
workers and caches, correct pairing, and view-only preview lifetime.

### E — Deliver measured virtualization and actions

1. Implement the GPUI scrolling specimen from UX U0, then the diff portion of
   U2/U3. Extract `ui/diff_view.rs`/shared scroll helpers incrementally if useful.
2. Virtualize the whole loaded selected file; replace 32-hunk/200-row display
   cuts with visible-range rendering. Show parser/wire truncation separately.
3. Add real hunk reveal for Alt+N/P; focus-gate these shortcuts so they do not
   consume terminal/editor-like input. Preserve anchors across refresh/mode switch.
4. Add keyboard-accessible context actions for Copy Hunk, Open Path, Refresh,
   next/previous hunk and eligible Stage Hunk. Copy unified text, not Split
   spacers; label incomplete copies. Open uses shared `FileCommand::Open` and
   the existing terminal route, including explicit deleted-path behavior.
5. Measure content widths, apply fractional X/Y offsets, proportional rails,
   track paging and release-outside handling. Preserve inspector containment.
6. Verify preview close/terminal selection/project switch restore focus and keep
   session IDs/PIDs/output unchanged. Remove obsolete state/comments/tests.

**Exit:** last loaded row and long-line end reachable, navigation reveals targets,
actions work by mouse and keyboard, and render count is viewport-bounded.

### F — Parity, release Wayland and closure

1. Run focused and full checks below on the final implementation.
2. Build both release binaries and use isolated config/state/socket paths for
   disposable fixture projects. Validate UI and CLI against the same desktop.
3. Execute the acceptance matrix; record commands, captures and actual results.
4. Update milestone checkboxes, method tables, dependencies if changed, and
   status evidence. Mark M15 complete only after all required gates pass.
5. Move the recorded next action to M16. Broader UI fidelity stays separately
   tracked with its own remaining acceptance gates.

## 5. Verification matrix

All rows below are **pending** until linked implementation evidence exists.

| Requirement | Automated proof | Release Wayland proof |
|---|---|---|
| Correct staged/unstaged/path data | Raw-Git spans/content oracle, same-path two-side fixture, literal/deleted-path tests | Click each Git group; header, contents and target match |
| Bounded truthful data | Below/at/above every cap; escaped JSON bytes; incomplete-tail flags; ListFiles counts | Large fixture shows readable notice and stays interactive |
| Correct Inline/Split | Pure pairing/number/anchor tests and metadata rows | Reference-matched replacement, blank/tab/Unicode crops |
| Row virtualization | Visible-range/overscan tests and actual rendered-row instrumentation | Reach bottom of 300-line hunk and >32-hunk fixture |
| Two-axis scroll | Extent/offset/rail/anchor tests where custom geometry exists | Wheel, fractional trackpad where available, X reach, Split shared Y, mode switch, resize |
| Navigation/actions | Anchor reveal, focus gating, copy extraction, shared File Open mapping | Alt+N/P, context menu keyboard, clipboard content, terminal-routed open |
| Real hunk staging | Two-hunk index/working-tree assertions; partial index, stale/duplicate/error tests | Stage one hunk; other remains; Git and both diff sides refresh |
| UI/IPC/CLI parity | Typed round trips, validation, scoped denial, fake-server and real-owner tests, human/JSON/exit codes | Show/ListFiles/hunk operation against the displayed repository |
| Async/resource lifecycle | Late responses, root/path/side switch, bounded workers, timeout/cancel/close tests | Rapid selections/project switches; close while diff is running; no orphan Git children |
| Terminal regression | Existing session lifecycle/dispatcher tests | Hidden terminal output continues; returning restores input; IDs/PIDs unchanged |
| Geometry | Measured viewport/containment checks | 1440×900 and smaller available sizes; Inspector 280/330/470; alternate scale if available |

Performance evidence must include repository size, loaded/rendered rows, release
build, hardware, scale, query/open duration, scroll/frame samples, peak RSS and
worker/FD counts. Compare identical fixture/bounds before and after. Agree
acceptance thresholds from the measured baseline before calling performance a
pass; virtualization's viewport-proportional render count is a structural gate.
Missing trackpad/scale/X11 hardware is recorded with impact and release decision,
never represented as a passing observation.

## 6. Checks and evidence record

Focused checks while implementing:

```bash
cargo test -p omaterm-context -- --test-threads=1
cargo test -p omaterm --bin omaterm-desktop -- --test-threads=1
cargo test -p omaterm-core -p omaterm-protocol -p omaterm-cli
```

Final required checks:

```bash
cargo fmt --all --check
cargo test --workspace
cargo test --workspace -- --test-threads=1
cargo clippy --workspace --all-targets -- -D warnings
cargo build --release --bin omaterm --bin omaterm-desktop
python3 scripts/check-docs.py
git diff --check
```

The default workspace invocation is the repository gate; the serial invocation
also closes the specifically recorded M15 timeout concern. A timeout is not a
pass. Investigate once reproducible rather than repeatedly extending deadlines.
For each delivery record revision/files, exact command/result, automated versus
manual evidence, fixture/capture locations, environment, blockers and next action.
Do not commit or create a PR unless requested.

## 7. Delivery checklist and first action

- [ ] A: fixture/contract/baseline record
- [ ] B: bounded parser/query and exact metadata
- [ ] C: shared true hunk staging plus wire/CLI parity
- [ ] D: cached presentation and guarded async lifecycle
- [ ] E: virtualized two-axis preview and accessible actions
- [ ] F: final gates, release desktop matrix, evidence-based M15 completion

**First implementation action:** create the two-separated-hunk and long-hunk
fixture repositories, pin truncation/newline/identity contracts, and add failing
parser/partial-staging tests. Prototype the GPUI viewport once those fixtures are
available; integrate it after the presentation model is stable.
