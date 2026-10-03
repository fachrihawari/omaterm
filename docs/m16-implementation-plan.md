# M16 — Comprehensive Command Palette Implementation Plan

For the current partial implementation, the [M15 + M16 remaining-work
plan](m15-m16-remaining-work-plan.md) supplies the audited remaining execution
sequence and completion gates. This document retains the full M16 design intent
and earlier progress record.

## 1. Objective and authority

Deliver one keyboard-first overlay for discovering and executing OmaTerm
commands, jumping to workspace identities, opening project files in a terminal,
and finding changed Git paths. Every executable result constructs a semantic
command; workspace mutations remain in the common dispatcher.

The [M16 milestone](16-milestone-16-palette.md) is the acceptance contract.
Blueprint §23, §35, §36, §43–§45 and §62–§64 govern scope, keyboard conventions,
errors, privacy, shared operations, typed targets and bounds. The
[v5 fidelity plan](ui-v5-fidelity-correction-plan.md) governs surrounding visual
language; this plan defines the additional palette interaction states.

**Status: implementation in progress; M16 is not complete.** The initial
2026-10-03 audit was clean. Follow-up implementation has begun while the M15
gate is still open; this is recorded as a sequencing deviation, not a claim that
M15 passed its acceptance criteria. Current implementation/evidence and open
gates are tracked in [status.md](status.md).

## 2. Entry gates and dependency decisions

| Dependency | Audit position | Required next step |
|---|---|---|
| M12 root and boundary | Complete with recorded limits | Reuse root resolution, ignore rules and path checks |
| M13 file sources | Complete; finder exists | Preserve file-open semantics; harden search lifecycle during M16 |
| M14 Git sources | Complete | Reuse bounded status snapshots and refresh operation |
| M15 diff | In progress | Finish [M15 resolution](m15-full-resolution-plan.md), including workspace hangs and release validation, before M16 implementation |
| M18 query support | Not recorded as implemented; no `ProcessCommand` in the audited core enum | Deliver and verify the M18 query slice before M16 completion |
| UI v5 corrections | Incomplete | Reuse measured primitives; require palette-local visual evidence without claiming overall fidelity closure |

M16 explicitly requires **M18-query**, while M18 is independently schedulable
after M12. This means an M18 query slice can supply process inspection, bounded
list routing, protocol/CLI mapping and tests before its kill/UI closure. M16
does not require process kill, and must not introduce a private process scanner.
Document that slice's evidence in status; do not mark all of M18 complete on its
account. If the source remains unavailable, process actions stay unavailable
and M16 stays incomplete against its written contract.

Planning may happen now. The smallest implementation entry is phase A after M15
closure, with M18-query readiness tracked explicitly.

## 3. Existing code and concrete gaps

| Location | Existing behavior | M16 work |
|---|---|---|
| `apps/omaterm/src/main.rs` `ctrlp_*` state and handlers | File-only overlay, caret timer, arrows/Enter/Esc | Consolidate into one palette state machine and renderer |
| `toggle_ctrlp`, `ctrlp_search`, `files_tick` | One detached filesystem worker per query; query generation check; results delivered by 250ms poller | Bounded worker scheduling, cooperative cancellation and full request identity |
| `on_ctrlp_key` | Append/pop editing, 256-byte check, first character from key text | Complete bounded Unicode editing and explicit input ownership |
| `on_key_down` | `Ctrl+O` and project creation handled before finder input | Resolve precedence so overlay input cannot accidentally create a project or open a picker |
| `Ctrl+Shift+P` | Creates projects today | Explicit shortcut decision and migration documentation |
| `crates/omaterm-context/src/files.rs` | `SkimMatcherV2`, `fuzzy_match_indices`, bounded 100,000-entry walk | Reuse matcher; cancellable reusable file snapshot/search seam |
| `crates/omaterm-core/src/command.rs` | Project/tab/pane/terminal/file/git/diff commands | Typed palette targets and pure ranking policy; no GPUI dependency |
| `apps/omaterm/src/router.rs` | GPUI-free dispatch, scope checks, pending launches | Shared operation reuse; parity and no-effects failure tests |
| `dispatch_command` and launch completion | Applies router effects and tracks pending operation IDs | MRU/notice/focus updates on actual completion, not submission alone |
| `FileCommand::Open { project, path }` | Uses focused terminal, no explicit pane field | Guard captured terminal context; never open in a newly focused fallback shell |
| `git_panel.rs`, `diff_panel.rs` | Cached changed paths and preview state | Typed changed-path results with staged/unstaged identity |

There is no audited persistent M13 file index. The milestone's “file index” is
an intended reusable source, not evidence of an existing cache. M16 should add
the minimal bounded, invalidatable snapshot seam around M13 traversal rather
than a second set of filesystem/ignore rules.

## 4. Interaction contract

### One overlay, two entry points

- `Ctrl+P` opens plain mode; command entry opens with `>` and a caret after it.
- A leading `>` switches to command mode. Plain mode searches active-project
  files and changed paths plus workspace projects/tabs/panes/sessions by name
  and full ID. It does not search file contents or terminal output.
- Empty command mode shows enabled commands ordered by successful-use MRU,
  then stable catalog order. Empty plain mode shows recent valid targets and
  workspace identities; do not run the file backend with an empty query.
- One result list, capped at 100 **after** merging, deduplication and ranking.
  Source badges and parent project/tab context disambiguate identical labels.
- `Up/Down` clamp selection; `Home/End` and `PageUp/PageDown` operate the list
  when its navigation context is active. Enter executes only a currently
  selectable result. No-result Enter has no effect. Esc cancels the current
  argument step first, then dismisses the overlay.
- Mode/query changes reset selection; an arriving source refresh retains the
  selected stable result key if still present, otherwise clamps it. Selected
  rows are always scrolled into view. Pointer selection uses the same executor.
- Show searching, partial/truncated, empty, disabled and error states explicitly.
  A file-source failure must not silently look like “no matches” or hide valid
  workspace results.

### Shortcut ownership

Before wiring keys, inspect current application bindings and the user's active
Omarchy bindings read-only. Record actual delivery on Wayland. Do not edit the
user's compositor configuration as part of this application milestone.

The chosen application bindings are `Ctrl+Shift+P` for command mode and
`Ctrl+P` for plain mode. Project creation moves to `Ctrl+Alt+N`; its visible
empty-project hint was updated. The 2026-10-03 `hyprctl binds -j` snapshot had
no exact Omarchy binding for either chord, and live `Ctrl+Shift+P` opened the
palette on Wayland. Live `Ctrl+Alt+N` delivery and the clickable finder entry
still require proof. No key may invoke both creation and the palette.

While open, the overlay owns input before global mutation shortcuts, Git commit
editing, Files search and PTY forwarding. Its own open shortcuts switch modes
without stacking overlays. Escape/Enter must not leak to the shell.

### Focus and command intent

Capture an origin token containing window-local focus plus project, tab, pane
and session IDs where present. Dismissal restores the originating terminal if
it still exists. A vanished origin produces a notice and normal empty/current
workspace focus handling; never dispatch to another terminal as a substitute.

“Returns focus to terminal” means releasing input capture, **not undoing a
successful navigation command**. Project/tab/pane/session jumps focus their
intended destination. Split/new-tab/create actions use the dispatcher's selected
destination on completion. Non-navigation actions restore the valid origin.
Closing an origin cannot restore it. Opened from a diff/sidebar/empty workspace,
restore the valid prior focus or the selected terminal without inventing a PTY.

File-open results capture project, root identity, path and origin terminal.
Before dispatch, require that the focused terminal still matches that origin
and project and the root is still current. If not, report stale context and
require a new selection; do not perform a focus-then-open sequence with partial
effects. If an explicit target becomes necessary, extend the **shared** file
operation and its IPC/CLI contract with tests; never privately run `$EDITOR`.

## 5. Catalog and semantic mapping

Use stable action keys independent of labels, with title, aliases, category,
displayed binding, availability reason, required arguments and target policy.
Catalog membership is an explicit human-facing subset of semantic operations,
not an automatic exposure of every command enum variant.

| Result/action | Semantic operation | Context/argument rule |
|---|---|---|
| Project jump | `ProjectCommand::Select` | Captured project ID |
| New project | `ProjectCommand::Create` | Optional name/directory using existing picker flow |
| Rename/set directory/delete project | Corresponding `ProjectCommand` | Explicit project; bounded name/picker; existing deletion policy |
| Tab jump/new/rename/close | `TabCommand::{Select,Create,Rename,Close}` | Captured tab/project, explicit argument where required |
| Pane/session jump | `PaneCommand::Focus` | Session resolves to its current owning pane; retain both identities |
| Split left/right/up/down | `PaneCommand::Split` | Captured pane ID; fixed direction |
| Directional focus | `PaneCommand::FocusDirection` | Origin selection must still be current |
| Close pane | `PaneCommand::Close` | Explicit pane ID |
| Resize/equalize | `PaneCommand::{Resize,Equalize}` | Captured split/tab; validated finite fraction |
| File result/open selected file | `FileCommand::Open` | Origin/root checks above; existing editor-in-terminal behavior |
| Git refresh | `GitCommand::Status` | Captured project; apply returned snapshot via shared coordination |
| Changed-path diff/refresh selected diff | `DiffCommand::Show` | Captured project/path/comparison; existing M15 preview lifecycle |
| Stage/unstage selected path | `GitCommand::{Stage,Unstage}` | Explicit selected path; normal root/boundary checks |
| Stage selected hunk | `GitCommand::StageHunk` | Only with complete, current M15 hunk selection |
| Discard selected path | `GitCommand::Discard` | Reuse explicit two-step discard interaction; query edits disarm |
| Process refresh | M18 `ProcessCommand::List` | Captured project/session presentation context; shared M18 query |

Changed-path results retain comparison identity so staged and unstaged versions
of the same path remain distinguishable. A file row can merge a “changed” badge
with the file source, but opening its diff is a separate, explicitly labelled
action. Deleted paths offer the supported diff action, not terminal file-open.

Argument steps use the same input/result surface: choose target, enter bounded
text or choose a validated fraction, then build exactly one semantic command.
Cancel/invalid arguments produce no effects. Use existing project/path pickers
and confirmation policy; argument collection is UI state, not domain mutation.

Terminal byte injection, arbitrary shell execution, history deletion, commit,
process kill, restore internals and agent commands are outside the initial
catalog. M16's keyboard-only completion flow requires split, jumps, file-open,
Git refresh and process refresh; it does not require every enum variant to be
searchable. View-only application controls remain distinct from executable
semantic results and must not masquerade as CLI-parity operations.

No `palette.query` wire method is added. Automation uses the existing source
queries and action methods directly. Publish the final palette-to-command/CLI
mapping with actual bindings; a label-only table is not parity evidence.

## 6. Data, ranking and ownership

### Minimal module boundaries

- `crates/omaterm-core/src/palette.rs`: typed keys/targets, scored candidate
  metadata, deterministic merge/rank/cap and bounded MRU policy, unit tests.
  Accept fuzzy scores as input; do not make core depend on GPUI or context.
- `crates/omaterm-context/src/files.rs`: reuse `fuzzy_match_indices`, bounded
  traversal and ignore/boundary logic; add cancellable snapshot/search APIs.
  Existing `file.search` continues to share these primitives.
- `apps/omaterm/src/palette.rs`: catalog, argument state, source snapshots,
  availability checks and pure command construction.
- `apps/omaterm/src/ui/palette.rs`: v5-styled overlay and result rendering.
- `main.rs`: opening/input/focus integration, dispatched effects and bounded
  worker ownership. Remove superseded `ctrlp_*` code after behavior migration.

Exact signatures follow a GPUI 0.2.2 API spike. No new crate or fuzzy dependency
is needed. Capture licensing/revision changes only if dependencies actually
change.

### Ranking policy

Filter only candidates that match the query or an alias. Order by match class
(exact, prefix, fuzzy), fuzzy score, successful-use recency, then a stable
kind/label/key tie-break. MRU never promotes a non-match. Full typed IDs are
searchable; shortened IDs are display aids, never ambiguous target resolution.
Unicode highlight indices use the matcher's character-index contract.

MRU is session-local for M16, capped at 100 keys, deduplicated and pruned when
targets disappear. Record only successful actions; `Pending` is submission, not
success. Pending launch failure/cancellation does not earn MRU credit. Do not
persist query text, filenames or execution arguments in configuration or logs.

### Search bounds and lifecycle

Proposed initial engineering bounds (verify with fixtures; not measured latency
claims): 256 UTF-8 bytes per query, 100 merged rows, 100 MRU entries, at most
100,000 scanned filesystem entries using the existing M13 cap, one active
file-source task per window plus one replaceable latest request. Workspace/Git
candidate counts must also have an explicit reviewed cap before allocation;
reuse existing source caps, and show partial status when exceeded.

Take cheap immutable workspace/Git snapshots on the owner thread; scoring and
filesystem work happen off-thread. Maintain a bounded per-active-root filename
snapshot; invalidate on root/config/ignore changes and debounced watcher events.
Keep original `PathBuf` for dispatch, using lossy text only for display/search.
Entries with inaccessible/external symlink targets still undergo normal open
validation. Snapshot rebuilding must not become a second unbounded tree walk.

Every job/result carries overlay epoch, query generation, mode, project ID,
root generation and relevant source/config generation. Clear/close/reopen,
argument step changes, project switches, root changes and shutdown invalidate
older jobs. Generation checks alone are not cancellation: workers inspect a
cooperative cancellation token during walk/scoring. Rapid typing replaces queued
work rather than spawning unlimited detached threads. Use event-driven result
delivery where supported; avoid adding a permanent polling/blink loop.

Results are revalidated before execution; stale identities/path roots emit a
notice without retargeting. Execute Enter once, mark in-flight operations, and
ignore duplicate activation until completion. On shutdown, stop admission,
cancel/drain workers and release tasks/channels/focus handles. This must remain
compatible with existing IPC and launch cleanup ordering.

## 7. Ordered delivery phases

### A — Contracts, fixtures and API spike

1. Close prerequisites, inspect working tree and establish passing baseline.
2. Inventory bindings and command/source mapping; settle focus and stale-context
   cases above. Record actual shortcut decision.
3. Create disposable two-project/multi-tab/multi-pane fixtures with duplicate
   labels, closed IDs, staged/unstaged/deleted paths, Unicode and a 10k-file tree.
4. Verify GPUI focus/input/list APIs against 0.2.2, including clipboard editing
   and text events. Record current IME limit without claiming new support.

**Exit:** reviewed contracts, meaningful pure tests first, reproducible fixture
commands and baseline results. M18-query evidence or an explicit open gate.

### B — Catalog, typed targets and ranking

Implement GPUI-free rank/MRU/cap helpers, catalog availability, argument steps
and exact command construction. Check missing terminal/root/Git/process states.
Add identity deduplication and deterministic tie/highlight tests.

**Exit:** every advertised executable action maps to a typed command; invalid
arguments and stale targets have tested no-effect behavior.

### C — Unified sources and bounded search

Build root-scoped file snapshot and cancellable latest-request worker seam;
merge commands, workspace IDs, files and Git comparison paths. Integrate M18
query snapshots. Carry partial/error metadata across every source and final cap.

**Exit:** delayed old requests cannot overwrite new results; thread/queue/cache
counts are bounded under rapid typing and root switching; source failures are
visible. No filesystem/Git/process scan runs on the UI thread.

### D — Overlay, editing and focus

Replace the old finder with one overlay. Use measured v5 type/color/icon roles,
visible selected/disabled states, clipped or virtualized rows, viewport-aware
width/height and selected-row scrolling. Support insertion/deletion/caret
movement, whole text-event insertion and bounded clipboard paste; avoid slicing
UTF-8 bytes. Clamp the overlay to the center viewport at narrow sizes.

Wire mode shortcuts, keyboard ownership, argument transitions and restoration.
Stop blink work when closed. Preserve terminal-routed open and supported M13
copy/reveal affordances through existing helpers without changing their intent.

**Exit:** input cannot leak into PTYs, commit text or Files filter; full keyboard
interaction and focus state tests pass; native screenshots cover both modes,
arguments, empty/error/partial states and width variants.

### E — Dispatcher integration and parity

Apply semantic results through `dispatch_command`; use existing shared refresh
and preview coordination. Revalidate at activation and track pending completion.
Audit catalog handlers for direct workspace/session mutations and hidden scans.
Add router/IPC equivalence tests for split, focus and project/tab select, plus
file and query mappings. Verify no-effects validation and stale-context matrix.

**Exit:** palette and equivalent CLI/IPC requests reach the same shared operation
with matching typed fields and effects; no fallback targeting or MRU-on-failure.

### F — Release acceptance and handoff

Run workspace gates, release build and documentation checks; reproduce and
resolve hangs rather than describing isolated passes as a full suite pass.
Perform the Wayland flow below, record resource observations, finalize command
mapping and link evidence into status/acceptance/milestone documents.

**Exit:** all M16 acceptance requirements have evidence. Carry broader release
baselines and platform availability observations into M17 explicitly.

## 8. Verification matrix

| Area | Meaningful automated checks | Native evidence |
|---|---|---|
| Ranking | Exact/prefix/fuzzy/MRU ordering, alias matches, deterministic ties, Unicode indices, 100-row post-merge cap | Query expected commands/files with duplicate labels |
| Input/modes | Prefix switch, empty modes, bounded paste, UTF-8 editing, clamped selection, argument cancel | Type/paste/edit, navigate, switch modes, no shell leakage |
| Target safety | Closed project/tab/pane/session, replaced session, root change, deleted path, foreign project, unavailable terminal | Close/switch via CLI while overlay open; activation yields notice |
| Execution | No effects on invalid args/denial; one Enter activation; pending success/failure; MRU pruning | Split/focus/project jump/new-tab success and failed action recovery |
| Focus | Dismiss, navigation destination, close-origin, source disappearance, open-from-preview/empty workspace | Esc and execution return input to the correct destination |
| Lifecycle | Delayed results after clear/reopen/mode/root switch; cancellation during walk; shutdown; bounded queued jobs | Rapid queries, project switches and close-under-search |
| Parity | Same command fields/result/effects for palette builder and IPC request; invalid cases preserve state/index | Same-instance CLI split/focus/select and source-query spot checks |
| Files/Git/process | Ignore/boundary/path semantics reused; comparison identity; source cap/error propagation | Terminal file-open, changed-path diff, Git and process refresh |
| Resources/privacy | Repeated open/type/close and switch cycles; no query/body/token/paste logs | Release idle recovery, FD/thread/RSS trends and screenshot states |

Required commands for Rust completion:

```bash
cargo fmt --all --check
cargo test --workspace
cargo clippy --workspace --all-targets -- -D warnings
cargo build --release --bin omaterm-desktop
python3 scripts/check-docs.py
git diff --check
```

Use targeted core/context/desktop tests during increments; record exact commands
and results. A serial workspace run may help reproduce existing hangs, but
does not erase a failing or timed-out required gate. Planned tests are not proof.

### Release Wayland script

Use isolated config/state/runtime paths and a disposable repository. Record
toolchain/commit, compositor/scaling, release build, fixture size, captures and
commands. Protect fixture modifications from production projects.

1. Start two projects, multiple tabs and split terminals. Open both modes with
   actual keys and clickable entry; confirm shortcut delivery and ownership.
2. Execute split, pane/session jump, tab jump and project jump keyboard-only;
   inspect IDs/PIDs through CLI in the same instance.
3. Search ignored/Unicode/duplicate filenames, open a file in the captured
   terminal, dismiss and type a sentinel into that terminal. No palette text
   or navigation keys may appear in shell input.
4. Find staged/unstaged/deleted Git paths, open the intended comparison, refresh
   Git, and refresh M18 process data without invoking a private inspector.
5. Close a selected target or change its root through CLI while the overlay is
   open; ensure Enter reports stale state and leaves another shell untouched.
6. Exercise argument cancel/invalid text, disabled entries, partial search,
   no-match, backend error and successful MRU ordering.
7. Rapidly edit queries on a 10k-file fixture, switch projects, open/close 100
   times and close during search. Compare warm FD/thread/RSS baselines and
   confirm workers/timers settle. Record measured latency and workload without
   inventing universal thresholds; M17 owns formal release baselines.
8. Capture command/plain/argument/error/partial states at standard and narrow
   viewport widths, and document unavailable compositor/X11/IME checks honestly.

## 9. Completion checklist and next action

- [ ] M15 complete; M18 query prerequisite verified separately.
- [ ] One overlay, collision-checked bindings and documented old-binding changes.
- [ ] Required source kinds searchable; final cap, cancellation and errors proven.
- [ ] Pure tested ranking/MRU; no new fuzzy dependency or core GPUI coupling.
- [ ] All executable results use shared semantic operations and typed targets.
- [ ] Stale targets never retarget; validation failures emit no effects.
- [ ] Focus restoration respects intentional navigation and origin disappearance.
- [ ] Keyboard-only required flow and same-instance parity proven on Wayland.
- [ ] Required quality gates green; resource/redaction checks recorded.
- [ ] Milestone, overview, acceptance matrix and status link actual evidence;
      M17 receives remaining release-level platform/baseline observations.

Next action: close M15's remaining acceptance gates, then complete the M18
off-thread process-query contract and the M16 source-cache/freshness work. This
plan documents actual partial implementation and evidence below.

## 10. Implementation progress and remaining gates — 2026-10-03

Implemented so far:

- Added GPUI-free `apps/omaterm/src/palette.rs` candidate kinds/targets,
  exact/prefix/fuzzy ranking, stable tie-breaks, bounded 100-result cap, file
  semantic target construction and successful-use MRU ordering.
- Reworked the existing `Ctrl+P` overlay into command (`>`) and plain modes,
  with project/tab/pane/session IDs, the initial semantic command catalog,
  active-project filename search, and staged/working Git-path results.
- Keyboard editing has a Unicode-safe byte cursor, bounded query insertion,
  deletion, clipboard paste filtering, Home/End and selected-result navigation.
  Results are rendered with a GPUI virtual list and scroll the selection into
  view. Query generations and a cooperative file-walk cancellation token retire
  stale filename searches. Root resolution now runs in the source worker. A
  reusable `FileSearchIndex` is cached by project/root/hidden policy and
  invalidated by watcher events/root changes; index growth is capped at 100,000
  scanned entries and 16 MiB of retained paths.
- Palette results dispatch project/tab/pane/file/diff/Git/process-list semantic
  operations; file opening validates the captured project/tab/pane/session and
  resolved-root identity before dispatch. Pending launches enter MRU only after
  successful completion. `process.list` now has a bounded ProcessCommand, DTO,
  strict IPC mapping, thin CLI mapping and Info-panel refresh/list rendering.
- The project-creation hint/binding was moved from `Ctrl+Shift+P` to
  `Ctrl+Alt+N`. No new fuzzy dependency was added.

Checks passed on the current code before this documentation update:

```text
cargo fmt --all --check
cargo clippy --workspace --all-targets -- -D warnings
cargo test --workspace -- --test-threads=1
cargo build --release --bin omaterm --bin omaterm-desktop
python3 scripts/check-docs.py
git diff --check
```

The serial suite passed (453 tests). Release Omarchy/Hyprland screenshots and
same-instance CLI observations are in `/tmp/omaterm-m16-l41P/`: command mode,
plain filename/Git-path results, keyboard split/focus/project navigation,
terminal-routed open with isolated `EDITOR=/usr/bin/true`, process refresh, and
`omaterm pane list`/`pane focus`/`process list` IPC parity. The palette sentinel
was not inserted into a PTY during these covered actions.

Still open; therefore no M16 completion claim:

- M15 remains `in_progress`; the virtual row model is implemented, but native
  diff-preview selection/scroll/action acceptance, horizontal scrolling and
  final M15 release gates are not closed.
- M18 process-query slice exists, but process inspection currently executes
  synchronously through the owner dispatcher and CPU/RSS sampling is absent.
  This does not meet M18's no-`/proc`-work-on-UI-thread gate; move inspection to
  the required off-thread/shared-query lifecycle and verify resource bounds.
- Filename search uses a bounded invalidatable active-root snapshot; admission
  for first snapshot construction is serialized. Search-worker admission and
  query resource behavior under rapid typing still need explicit
  bounds/measurement before acceptance.
- Full focus lifecycle/stale-root/error matrices, argument flows beyond the
  implemented command subset, per-hunk context actions, exact broad catalog
  parity, and release Wayland checks for Git diff selection/process children
  remain unverified.
- The Wayland palette query/split/focus/project/file-open/process-refresh flow
  was exercised, and a changed Git path opened the M15 preview through the
  palette's `GitPath` result. M15's keyboard `Ctrl+Shift+S` selected-hunk
  action staged exactly the first of two fixture hunks; the other remained
  unstaged. Direct Git-list pointer selection and manual diff scroll/hunk-action
  interaction were not exercised (available keyboard injection does not
  synthesize pointer clicks). Those remaining interactions stay pending
  evidence, not a pass.
