# M15 — Full Resolution Plan (Remaining Work)

> **Historical plan (implementation landed).** Superseded by later increments;
> M15's native interaction matrix remains open. Live status in
> [status.md](status.md).

**Date:** 2026-10-03. **Status:** implementation landed; M15 native matrix open. Superseded by the [M15 + M16 remaining-work plan](2026-10-03-m15-m16-remaining-work-plan.md).

**Execution at the time:** the [M15 + M16 remaining-work
plan](2026-10-03-m15-m16-remaining-work-plan.md) was the active sequence,
audited after the virtual-row, copy/stage action and palette increments. The
baseline and “not landed” inventory below are historical; settled contracts
remain applicable.

This is the complete end-to-end plan for closing M15 from the exact current
baseline. It supersedes the delivery checklist order in
[2026-10-03-m15-completion-plan.md](2026-10-03-m15-completion-plan.md) only where that plan's phase
C is now partly implemented: its contracts, verification matrix, and evidence
rules still apply unless this document changes them explicitly.

## 1. Exact current baseline

Working tree: clean at this audit. Commits `2356c4b` (presentation/UX groundwork)
and `328df51` (parser + hunk-stage foundation) are in.

Already landed:

- Bounded query/parser with aggregate `truncated`, exact newline metadata,
  `--literal-pathspecs`, hunk IDs, and disposable-repo tests.
- Context-owned `git_stage_hunk(root, path, hunk_id)`: authoritative raw reread,
  capped/binary/stale rejection, one raw internal patch to bounded
  `git apply --cached` stdin.
- Core `GitCommand::StageHunk`, validation, router dispatch, project scoping,
  and one two-hunk repository proof.
- Separate Inline/Split models, native Y body, legacy caps/offsets, current
  preview open/close and hunk-cursor behavior.

Explicitly not landed:

- No `git.stage-hunk` IPC method, DTO, bridge mapping, or CLI command.
- No preview UI action using `StageHunk`; header actions remain whole-file.
- No post-stage Git/diff refresh behavior for the completed mutation.
- No selected-path preview query, stale-response identity, bounded worker/cache
  model, or full pairing/viewport/action/validation closure.
- No release Wayland acceptance or unresolved full-workspace serial timeout.

## 2. Remaining scope and exclusions

Close all M15 capability, parity, navigation, scrolling, actions, lifecycle,
bounds, errors, tests, and release evidence.

Do not include:

- Files viewport replacement beyond shared scroll/helper reuse.
- Broader sidebar, icon, typography, theme, or unrelated v5 fidelity work.
- Editor, syntax highlighting, word diff, full-revision loading, search in diff,
  commit UI expansion, M16 palette, lifecycle beyond diff/Git scope.
- Protocol v2, version changes, or unrelated milestone changes.

The approved v5 presentation extension means Split and Inline both remain in
scope. Whole-file staging does not satisfy per-hunk staging.

## 3. Settled contracts for the remaining work

### Hunk staging operation

- Preferred method/DTO: `git.stage-hunk` with `{project_id?, path, hunk_id}`.
  CLI form: `omaterm git stage-hunk [--project ID] <path> --hunk <id>`.
  Final exact spelling must preserve M14 naming conventions and protocol strictness.
- One operation stages only one current complete unstaged hunk. Binary,
  conflicted, truncated, missing, incomplete, or changed hunks are rejected with
  a stable centralized code/message.
- Stale means the current authoritative diff no longer contains the supplied
  hunk ID for that path/side. It does not mean all Git/index/worktree races are
  eliminated.
- UI never constructs or receives executable patch text. Split-row pairing and
  clipped bridge text are display-only.
- Bounds: one root-contained path, one nonzero selector; same Git process
  timeout/cleanup conventions as existing mutations.
- After success: refresh Git status and the currently displayed diff sides. Keep
  the user's selected file/side/anchor unless the staged change makes that
  selection empty. Do not force an unrelated project, tab, terminal, or inspector
  context.

### Preview source and identity

- Give every selected preview a request/response identity including project,
  root generation/source, resolved path, staged side, context size, and data
  generation. Reject landed results that do not match the active request.
- The preview fetches the selected path explicitly, separate from any capped
  project-wide query. It does not rely on the selected file luckily surviving
  file caps.
- Preview/scroll/navigation state remains view-local and non-persistent.
  Returning from preview to a terminal restores terminal focus/session behavior.
- Selected terminal operations continue to use shared `FileCommand::Open`;
  deleted-path behavior must be explicit, not guessed.

### Rendering/viewport contract

- Flatten each loaded selected file into exact rows: file/metadata, hunk header,
  code, notice/truncation, and no-newline state. Cache by data/mode generation.
- Render only the measured visible row range plus a small stated overscan. The
  parser and IPC bounds are data caps, not presentation virtualization.
- Fixed headers, one logical Split Y offset, independent Split X offsets,
  Inline X/Y, fractional pixel offsets, native momentum, source-anchor mode
  switch, and containment that never displaces the Inspector.
- Navigation by Alt+N/P must reveal an actual hunk row in the present viewport.
- Actions: copy exact unified hunk text, open path through shared command,
  refresh, stage only eligible hunks, and hunk next/previous. Incomplete copy or
  unavailable mutation must say so.

## 4. Phase 1 — Complete hunk staging across all entry points

Dependencies: current tree only.

1. Add `GitStageHunk { project_id?, path, hunk_id }` to
   `crates/omaterm-protocol/src/method.rs`; decode `git.stage-hunk`.
2. Reject unknown fields, empty/control paths, oversize paths, and zero hunk ID
   in method/bridge validation before reaching dispatcher authorization.
3. Map `Method::GitStageHunk` to `GitCommand::StageHunk` in `ipc_bridge.rs`.
4. Add `GitCmd::StageHunk { project, path, hunk }`; send exactly one wire call.
5. Render human and JSON success/failure with existing output/exit-code rules.
6. Update IPC/CLI mapping tables and checker expectations.
7. Replace per-hunk whole-file actions with an eligible hunk-level Stage Hunk:
   unstaged side, tracked text, complete, supported file shape; otherwise expose
   the reason and retain whole-file actions.
8. On success schedule status plus both affected diff-side refreshes; preserve or
   rationally update selection/anchor.
9. Tests:
   - Protocol decode/reject.
   - Bridge command conversion and bound rejection.
   - CLI mapping/parser validation.
   - Router success, stale, binary/truncated, traversal, non-repo, scope denial.
   - Already-partly-staged, duplicate submit, external index change, repeated
     success/failure.
   - Fake-server CLI end-to-end and real-owner IPC round trip.
10. Add scoped-denial and concurrent incompatible behavior coverage consistent
    with M14.

Exit: UI, IPC, and CLI operate identically; index proof shows only the chosen
hunk staged.

## 5. Phase 2 — Harden the staging primitive and its lifecycle

Dependencies: Phase 1 API frozen; no UI/viewport dependency.

1. Cover the new stdin path with a bounded Git timeout:
   - Large-but-legal internally generated patch input completes/reaps normally.
   - Slow/blocked Git behavior cannot retain a child, pipe, thread, or pending
     app operation beyond existing mutation conventions.
2. Re-read before staging with the same selected literal path logic and machine
   options as read queries. Verify deleted tracked paths, spaces/Unicode,
   pathspec-like names, rename legs, and added/deleted file shapes. Enable only
   verified shapes in the UI; reject others explicitly.
3. Prove patch exactness for:
   - Multiple hunks and multiple edits per hunk.
   - Context boundaries.
   - Both no-newline markers.
   - Tabs, Unicode, blank and overlong-but-bounded lines.
4. Verify context lines passed to old/new starts stay internally consistent for
   patch generation and future single-path queries.
5. Document the remaining Git index races and explicit lack of atomicity. Do not
   represent check-then-apply as atomic.
6. Log consistent `omaterm::git` operation metadata without bodies/paths beyond
   established redaction rules; audit stderr handling.

Exit: supported-shape and stale/error tests green; documented ineligible shapes
and race behavior.

## 6. Phase 3 — Finish preview data lifecycle

Dependencies: Phases 1–2 selected-path and refresh requirements.

1. Add explicit selected-path queries to panel state/worker parameters.
2. Reject results for old project/root/path/side/context/data selection.
3. Bound workers/cache/memory:
   - In-flight and queued diff work.
   - All-path plus selected-path caches.
   - Generation cleanup on project/side/path switch and mutation.
4. Distinguish ignored stale results from active process cancellation. Cancel a
   process when supported; always preserve timeout/reap. Do not claim ignored
   results equal process cancellation.
5. Handle all explicit states:
   loading, no-root, non-repo, empty, untracked/no-patch, binary, rename,
   mode-only, added/deleted, conflicted, timeout/failure, truncation.
6. Expand deterministic tests for:
   - Late and out-of-order landings.
   - Root/path/side switches.
   - Mutation refresh timing.
   - Group selection to preview open behavior.

Exit: no stale file/side preview; bounded background work and truthful states.

## 7. Phase 4 — Finish pairing and row model

Dependencies: Phase 3 row data identity; before final viewport integration.

1. Keep Inline order and Split positional pairing semantics from the UX plan.
2. Add pure tests before or with changes:
   1:1, 1:3, 3:1; add-only/delete-only; multiple edit blocks; multiple hunks;
   blank lines; tabs/Unicode; zero-count spans; u32/span overflow behavior;
   truncation and metadata placement.
3. Use checked arithmetic for all line-number construction.
4. Flatten complete rows with source anchors once per generation. Remove
   duplicate `align`/`split` reconstruction during every render.
5. Make hunk headers/metadata/truncation rows explicit virtual rows with fixed
   heights. Do not infer their heights from code-row measurements.
6. Delete obsolete hunk-offset/render-cap behavior and comments/tests only when
   their replacement has landed and passes.

Exit: exact source-number/content/anchor agreement in both layouts.

## 8. Phase 5 — Implement viewport, navigation, and actions

Dependencies: Phases 3–4 model/lifecycle stable.

1. Prototype locked GPUI 0.2.2 list/scroll APIs against long-hunk fixtures.
   Choose native ownership when it meets row/axis/scrollbar requirements. If a
   custom owner is needed, record the concrete blocker before building it.
2. Virtualize every loaded bounded row for the selected file:
   - Remove 32-hunk/200-row hidden-data caps.
   - Display parser/wire truncation as data notices.
   - Instrument actual rendered-row count and typed-row heights.
3. Implement:
   - Shared Split Y navigation/scroll.
   - Independent Split X and Inline X/Y.
   - Fractional pixel deltas.
   - Proportional rails, track paging, pointer release outside.
   - Measured X extents from rendered content, not fixed assumptions.
   - Anchor retained across refresh/mode switch.
4. Connect Alt+N/P from hunk cursor to actual row reveal. Focus-gate shortcuts
   so terminal input is unaffected. `Ctrl+Shift+S` stages only the selected
   eligible unstaged hunk through `GitCommand::StageHunk`.
5. Add keyboard- and mouse-accessible context actions:
   - Copy exact unified hunk text (`Ctrl+Shift+C`).
   - Open path via shared command.
   - Refresh current request.
   - Next/previous hunk.
   - Eligible Stage Hunk.
6. Preserve focus/session lifecycle when opening/closing preview and selecting
   terminals/projects/tabs.
7. Regression-test terminal visibility/session continuity, IDs/PIDs, hidden output,
   input focus, and panel containment.

Exit: end and long-line reach, navigation reveal, accessible actions,
viewport-bounded render count, no session regression.

## 9. Phase 6 — Parity, quality, release validation, and closure

Dependencies: all functional phases.

1. Run the required focused suites during implementation.
2. Run and resolve the full final gate on the delivered revision:
   ```bash
   cargo fmt --all --check
   cargo test --workspace
   cargo test --workspace -- --test-threads=1
   cargo clippy --workspace --all-targets -- -D warnings
   cargo build --release --bin omaterm --bin omaterm-desktop
   python3 scripts/check-docs.py
   git diff --check
   ```
3. A serial-suite timeout remains a failure. Preserve failing-test output and
   child/thread/process behavior; fix the cause. Do not weaken, skip, shard,
   raise timeouts repeatedly, or report an incomplete invocation as success.
4. Validate with isolated release config/state/socket paths against disposable
   repositories containing:
   - Two separated hunks.
   - Same file staged + unstaged.
   - Long and many-hunk diffs.
   - Cap/boundary/truncation cases.
   - Binary, rename, mode-only, added/deleted, no-newline, Unicode/tab cases.
5. Prove staged/unstaged/side selection, staging, copy, open, navigation,
   refresh, two-axis scrolling, mode switching, preview closing, project/tab
   switching, and CLI commands against the same desktop.
6. Collect performance evidence with identical fixtures/bounds:
   query/open duration, loaded/rendered rows, scroll behavior, peak RSS,
   worker/FD/thread counts, release build, hardware, font/scale/viewport.
7. Set release acceptance from measured baseline + structural render-count proof,
   not predeclared universal thresholds.
8. Record unavailable hardware separately, including impact/release decision:
   trackpad momentum, alternate scale, X11/second compositor, per-process GPU.
9. Update milestone checkboxes, IPC/CLI mappings, user-facing operation docs,
   acceptance row, and dependencies only if behavior changed them.
10. Mark M15 complete only after every acceptance below has linked evidence.
    Advance the recorded next action to M16.

## 10. Final acceptance table

Every row is pending until linked implementation and live evidence exists.

| Requirement | Proof required |
|---|---|
| Staged, unstaged, and selected-path diffs | Exact spans/content against raw Git; literal/deleted-path behavior; correct UI and CLI target |
| Bounded exact data | At/above every cap; accurate aggregate/local flags; newline/Unicode/tab preservation |
| Hunk pairing and row model | Pure Inline/Split tests plus live representation agreement |
| True hunk staging | Real index assertions; only selected hunk staged; stale/duplicate/unsupported behavior |
| UI/IPC/CLI parity | Typed conversions, validation, scope denial, JSON/human/exit codes, same-desktop proof |
| Row virtualization | Render count scales with viewport; end of loaded data reachable |
| Scroll and navigation | Wheel/keys/drag agreement; Split shared Y; independent X; anchor and mode persistence |
| Actions | Exact clipboard hunk text; terminal-routed path open; refresh; accessible controls |
| Async lifecycle | Stale rejection; bounded workers/caches; project/path/side switching; close-under-load |
| Terminal regression | Hidden output continues; sessions/focus/IDs/PIDs preserved |
| Presentation fidelity | Reference-matched geometry/colors/typography/containment for the diff surface |
| Quality/performance/release | Full gates green; release binaries; baseline and resource evidence recorded |

## 11. Execution order and first action

Order: 1 → 2 → 3 → 4 → 5 → 6. Do not start viewport work before model +
lifecycle semantics are stable; do not close before release evidence.

First action: add the `git.stage-hunk` protocol/bridge/CLI contract plus
failing parity tests, then preview action/refresh wiring, then staging-lifecycle
hardening.
