# M15 + M16 — Remaining Implementation and Acceptance Plan

**Audit date:** 2026-10-03. **State:** planned remaining work; neither milestone
is complete. Baseline is commit `6d41c14` **plus the existing uncommitted
implementation**, not that commit alone. Preserve the working tree.

## 1. Authority and execution order

The [M15 milestone](15-milestone-15-diff-viewer.md) and
[M16 milestone](16-milestone-16-palette.md) remain acceptance contracts.
Blueprint §26, §33, §35, §36, §43–§45, §50–§54 and §62–§64 govern concurrency,
rendering, semantic dispatch, platform seams, bounded inputs and evidence.

This is the current **remaining-work execution sequence**, superseding obsolete
baseline/task inventories in the [M15 resolution plan](m15-full-resolution-plan.md)
and the [M16 implementation plan](m16-implementation-plan.md). Their settled
contracts and the [diff UX plan](diff-files-ux-correction-plan.md) still apply.
The broader [v5 fidelity plan](ui-v5-fidelity-correction-plan.md) remains separate;
measure the affected diff/palette surfaces against its primitives.

Execute:

```text
R0 baseline + fixtures
  → D1 diff data/worker lifecycle
  → D2 diff viewport/anchors
  → D3 diff actions + parity + Wayland → M15 acceptance
  → Q1 M18 query prerequisite
  → P1 palette source scheduler + freshness
  → P2 catalog + ranking + argument flows
  → P3 input/focus + layout
  → V1 end-to-end verification → M16 acceptance → M17 handoff
```

M16 implementation was started before M15 closure; that sequencing deviation
is already recorded. Finish M15 before further M16 feature expansion. Shared
infrastructure corrections may serve both milestones, with evidence attributed
to each. The M18 **query** slice is required; completing process kill and the
whole M18 UI is not a prerequisite for M16.

## 2. What exists already

| Surface | Implemented in the working tree | Recorded evidence, not a new pass |
|---|---|---|
| Diff backend | Bounded literal-path queries, hunk IDs, original hunk header, no-newline metadata, context-owned partial staging, protocol/CLI mapping | Parser/repository/bridge/CLI tests; live two-hunk index proof |
| Diff presentation | Inline/Split pairing, cached flattened rows, native `uniform_list`, hunk reveal, stage/copy/open rows | Long-hunk row retention and copy-extraction tests; release preview screenshots |
| Diff keyboard | Alt+N/P reveal, `Ctrl+Shift+S` stage, `Ctrl+Shift+C` copy | Stage chosen hunk live; clipboard and full navigation live pending |
| Palette | `Ctrl+Shift+P` commands, `Ctrl+P` files/IDs, semantic targets, fuzzy rank, 100-row cap, virtual list, caret editing/paste, MRU keys | Three rank tests; partial keyboard Wayland flow and CLI spot checks |
| File search | Cancellable `FileSearchIndex`, 100k scan/16 MiB retained-path limits, root/hidden-policy cache, serialized builds, watcher invalidation | Existing search/ignore/cancellation tests; comprehensive cache/scheduler tests pending |
| Process query | `ProcessCommand::List`, result DTO, strict `process.list`, thin CLI, manual Info refresh | Decode/mapping/scope tests; live empty response, not child/port attribution proof |
| Checks | Last recorded full serial suite: 453 tests; format/Clippy/release/docs gates recorded | Historical evidence only; rerun on the delivered revision |

Screenshots and disposable fixtures mentioned in [status](status.md) are under
`/tmp/omaterm-m16-l41P/` and `/tmp/omaterm-m15-pj3R`. Check their existence and
identity before reusing them; temporary artifacts are not permanent acceptance
records. Existing terminal-suite timeouts occurred intermittently even though
later serial runs passed. Preserve that history until the cause is resolved.

## 3. Audited gaps to close

### M15

| ID | Code/evidence gap | Required correction |
|---|---|---|
| D01 | `diff_tick` lands by generation before processing current-project transitions; response tuple lacks root/path/context identity | Validate the complete current request before publishing any data |
| D02 | `git_select_path`, mutation and project switches clear `diff_in_flight` while detached workers can still run | Bound actual workers, retain admission slots until real completion, cancel/supersede queued work |
| D03 | `render_diff_preview` clones the full `DiffInfo`; flattened rows also duplicate strings and full copy text | Share immutable selected-file data and derive copy text only on demand |
| D04 | List wrappers are 21px but `split_cell` is 22px; `.h_full()` sits below fixed chrome | Establish one actual row pitch and measured remaining body viewport |
| D05 | Code clips horizontally; independent Split X and Inline X are absent | Measured text extents and correct two-axis ownership/rails |
| D06 | One `diff_scroll_handle` is reused across selections; mode switch lacks source-anchor mapping | Per-preview identity/anchors, retained Y and side-specific X offsets |
| D07 | `align_hunk`/`split_hunk` use unchecked `u32 += 1`; replacement tests cover limited shapes | Checked numbering and broad pairing/span-boundary tests |
| D08 | Row Copy/Open actions do not select their hunk; navigation cursor can differ from clicked action context | One typed action target for pointer, keyboard and contextual controls |
| D09 | Preserved header is copied but visible headers reconstruct spans; generic no-newline row loses Split-side attribution | Truthful original header/metadata and old/new marker placement |
| D10 | Copy/open/refresh/context controls and long-scroll reach lack complete live proof | Release interaction matrix with exact clipboard and terminal-target assertions |
| D11 | Stage eligibility excludes some backend-unsupported metadata only by backend rejection | Expose supported-shape availability/reason; reject stale/conflicted/mode-change shapes consistently |

### M16 and the process-query prerequisite

| ID | Code/evidence gap | Required correction |
|---|---|---|
| Q01 | `CommandRouter::process_list` scans `/proc` synchronously | Shared bounded async query; owner only authorizes, snapshots targets and applies results |
| Q02 | Descendant/port discovery allocates before final 512-row cap and repeats scans per session | Bounded shared process snapshot, PID ownership deduplication, pre-allocation limits |
| Q03 | Shell roots are excluded; CPU/RSS always `None`; only empty live response recorded | Shell-family query contract, CPU sampler/RSS seam, child + port attribution tests |
| P01 | Latest-only worker/cancellation landed; root resolution and scoring results remain unmeasured under burst/shutdown load | Stress source-worker admission, root-resolution cancellation/deadline and task/channel retirement |
| P02 | Project/root/hidden cache key and build generation guard landed; activation validates file root | Test root/ignore generation races and verify no old index/result can publish after invalidation |
| P03 | `watched_root` can lag authoritative root; ignored traversal errors are silent; watcher-unavailable freshness needs a policy | Authoritative root identity, partial/error reporting, bounded fallback refresh |
| P04 | Workspace/Git candidates allocate before a final row cap; global merge has no dedup or aggregate cap indication | Reviewed source input caps, stable typed keys and truthful merged truncation |
| P05 | Root-aware file MRU/recent results and project/path/side Git keys landed; Git root generation and full MRU lifecycle still need coverage | Revalidate Git root/source identity, cover successful/pending/failure pruning and empty-query recents |
| P06 | Deterministic rank/merge/cap is in GPUI-free core; fuzzy score generation remains in the context-using app adapter | Preserve dependency direction and add core matrix coverage for class/score/MRU/kind/key/cap/dedup |
| P07 | Catalog remains in `main.rs` and is a partial action subset; disabled-reason and typed argument-state model is absent | Pure typed registry, availability policies, tested argument collection and a reviewed advertised subset |
| P08 | Pane/session identities and captured-origin checks landed for pane/session and focus-dependent operations | Cover same-pane session replacement, owner changes during execution and target-specific focus semantics |
| P09 | Worker cancellation, duplicate activation blocking and dispatcher-based origin restoration landed | Prove dismiss/execute/pending-failure/closed-origin behavior and timer/worker shutdown lifecycle |
| P10 | Diff queries and refreshes run synchronously on activation; selected-path result is queried then fetched again | Shared async query coordination and single response-to-preview application |
| P11 | Backend arrivals reset selection to row 0; text event uses only first character; no full editing/focus tests | Stable-key selection retention, complete bounded text input and sentinel tests |
| P12 | Palette has fixed 360px list height and labels can clip in narrow center viewports | Measured viewport clamps, readable disambiguation, adaptive result height and contextual help |

These are implementation tasks as well as acceptance tasks. Existing checks do
not establish worker counts, restore focus, prove exact copy or close M15/M16.

### Execution progress since the audit

- **D1 advanced:** `DiffRequestKey` now carries request/data generation, root
  generation and inputs, project, path, side and context. The owner accepts only
  a full current-key match. `DiffWorker` runs one actual Git job and one
  replaceable pending request; supersession cancels the active Git process via
  the bounded/reaped runner. A one-result mailbox bounds completions. The
  current session CWD is captured from tracked state rather than probed through
  `/proc` on the diff scheduling path. The last-good `DiffInfo` is `Arc`-shared.
  Cancellation/publication now share the worker lock; shutdown transfers a join
  to background cleanup. Deterministic tests cover full stale-key rejection,
  100 queued supersessions while an active job is blocked, and active/queued
  shutdown. Dispatcher-wide Git-mutation invalidation, including IPC, remains.
- **D2 partial:** display rows now have a uniform 21px pitch, checked line-number
  overflow, cached content-width estimates and a native horizontal scroll
  container. Per-project/side/path/mode scroll handles do not leak offsets
  between files; mode switches reveal the selected hunk. Independent Split-side
  X offsets, font/tab/Unicode text measurement, rails and source-anchor
  retention across refresh are still open.
- **D3 partial:** original unified hunk headers are retained through DTO/bridge;
  Copy Hunk / `Ctrl+Shift+C` generate header/body/no-newline text on demand.
  Row actions bind to their hunk rather than the cursor. `Ctrl+Shift+S` stages
  the selected complete hunk. Live two-hunk staging proves only the selected
  index hunk changed. Clipboard contents, direct Git-list click and full scroll
  interaction remain unverified.
- **P1 advanced:** one latest-only palette worker replaces per-query detached
  threads. The root file index is capped at 100k scanned entries / 16 MiB of
  paths, built serially, cached by project/root/hidden-policy and invalidated by
  watcher/root/config context changes. Invalidated in-flight builds cannot
  return a publishable index. Workspace source count and merged candidates are
  capped/deduped with truncation metadata.
- **P2/P3 advanced:** deterministic merge rank/cap lives in GPUI-free core;
  File MRU has root-aware identities and valid cached recent results; Git keys
  include project/path/side. Pane/session results carry expected session
  ownership. Captured origin is validated for implicit directional/resize/
  equalize actions; Esc, failed operations and failed pending launches restore
  through semantic focus commands when the origin remains valid.

These changes have targeted tests but are not full acceptance. In particular,
the process query is still synchronous, clipboard/native scroll proof is pending,
and large-source worker/resource behavior is not measured.

## 4. R0 — Baseline, evidence hygiene and fixtures

1. Inspect status/diff and preserve all existing code and documentation changes.
   Record commit, toolchain, selected dependencies and changed-file inventory.
2. Run required checks once for the baseline; retain complete timeout output.
   For a reproduced terminal hang, collect thread/child/wait state while stuck,
   fix the cause and add the relevant lifecycle regression. An isolated pass
   and a later retry do not diagnose the preceding timeout.
3. Create a dedicated temporary acceptance directory. Verify actual resolved
   config, snapshot, history, socket and credential paths; setting HOME alone
   is not isolation proof. Use separate disposable projects and explicit CLI
   socket/identity selectors. Capture only the test window/monitor.
4. Create repeatable fixtures: two separated hunks, same path staged/unstaged,
   300–1000-line hunk, many hunks, long/tab/Unicode lines, no final newline,
   added/deleted/rename/binary/mode-only/conflict, literal pathspec filename;
   two projects with duplicate labels; a 10k-file tree with ignored subtrees;
   spawned `sleep` and local listening server for process inspection.
5. Establish actual input tools/key syntax and target-window focus before
   injection. Earlier `wtype` arrow attempts did not prove Alt-arrow delivery.
   Where pointer/wheel injection is unavailable, use a documented hands-on
   script and leave those acceptance rows pending until exercised.

**Exit:** reproducible isolated fixture/run instructions and honest baseline.

## 5. D1 — Diff data identity and actual worker bounds

Targets: `diff_panel.rs`, `main.rs` diff coordination, context Git runner,
router and `ipc_bridge.rs`.

- Define request identity: project, authoritative root generation/source,
  canonical selected path, staged comparison, context size and data generation.
  Process current selection/root changes before accepting landed completions.
- Introduce real admission accounting: one active selected-diff job and one
  replaceable pending request. Changing selection invalidates display, not the
  active slot. Retire jobs only on completion/reap. Connect cancellation to the
  existing process deadline/drain/reap mechanism; keep stale rejection separate
  from process termination in evidence.
- Share selected immutable data and presentation cache. Bound retained sides/
  files and release obsolete projects/roots. Avoid whole-envelope cloning per
  frame and eager full-hunk clipboard copies in cached action rows.
- Centralize mutation-triggered status/both-side invalidation, retaining file/
  hunk source anchor where valid. Do not schedule capped all-project queries
  when a selected-path request is needed.
- Validate copied/staged hunk completeness, header caps, no-newline metadata,
  mode/conflict handling and stable scope errors. Extend repository fixtures
  for duplicate/stale/external-index/partly-staged cases already requested by M15.

**Tests:** delayed old root/path/side responses; switch-before-poller; mutation
during query; rapid 100-selection sequence with bounded actual jobs; close under
load; cap/cancel failure cleanup; foreign-project denial with unchanged index.

**Exit:** no stale preview, bounded real work/cache, shared operation parity.

## 6. D2 — Diff geometry, source anchors and two-axis scrolling

Targets: pure row model, diff renderer, GPUI scroll handles and measured helpers.

- Verify GPUI 0.2.2 list/scroll APIs with the long-hunk specimen. Keep native
  virtualization if it satisfies shared Y/independent X. Document an observed
  API blocker before selecting a custom viewport owner.
- Make code/header/metadata/action row sizing explicit and consistent. Use
  actual body bounds after file/side chrome. Visible rows plus measured overscan
  must bound render work; instrument requested/rendered range and row counts.
- Measure display text widths with the selected font/tab/Unicode treatment;
  cache extents by data/font/mode generation. Implement Inline X/Y and shared
  Split Y with separate old/new X. Provide proportional rails, paging and drag
  release outside the body. Preserve fractional wheel/trackpad deltas.
- Use source anchors (hunk ID + source side/line + intra-row pixel offset) for
  refresh and mode switches. Handles belong to preview identity; opening a new
  file cannot inherit another file's offset. Alt+N/P reveals the matching row.
- Retain original headers and correct side-specific no-newline notices. Use
  checked line numbering and test 1:1/1:3/3:1, add/delete-only, multiple edit
  blocks, zero spans, maximum spans and bounded malformed input.
- Make unsupported metadata/empty/binary/untracked/conflict/truncated views
  explicit. Code and headers must remain inside the main area with Inspector
  space reserved.

**Exit:** long-hunk last row and long-line last character reachable; truthful
pairing/markers; native wheel/keys/rail agreement; viewport-bounded row count.

## 7. D3 — Diff actions, parity and M15 acceptance

- Use explicit project/root/path/side/hunk targets for row selection and every
  action. Copy the chosen hunk, preserving header suffix, markers and newline
  semantics; reject incomplete copies with a reason. Context-menu and keyboard
  actions share the same selection/action builder.
- Complete accessible Copy/Open/Refresh/Next/Previous/Stage controls. Current
  `Ctrl+Shift+S` and `Ctrl+Shift+C` remain preview-scoped. Ordinary terminal
  input retains its behavior. Open uses `FileCommand::Open`, returns to its
  intended terminal and gives a defined deleted-path failure/disabled state.
- Finish supported-shape staging availability and notices; show whole-file
  controls as separate operations. No UI patch construction.
- Test and record UI-command builder ↔ bridge/CLI mapping ↔ dispatcher
  equivalence, error codes, truncation, JSON/human/exit behavior and project scope.
- Run release Wayland from R0 fixtures: **direct Git-row click** opens preview;
  both comparisons/modes; long scroll/end; independent X; navigation reveal;
  exact clipboard read; terminal-routed open; one-of-two-hunk index proof;
  stale duplicate rejection; mutation refresh; close preview and switch tabs/
  projects with stable session IDs/PIDs and hidden output.

**Exit:** all M15 acceptance requirements have linked automated/live evidence.
Mark M15 complete only here; record unavailable secondary platforms separately.

## 8. Q1 — Finish only the required M18 query slice

Targets: terminal platform/coordinator seam, core DTO/validation, router,
IPC completion bridge, CLI and Info refresh.

1. Owner dispatch authorizes project scope and takes a bounded snapshot of
   pane/session/shell process identities. A bounded worker runs the Linux
   inspector. Return through common async command completion for **all** entry
   points; do not create a palette-only scanner or spawn then join on the owner.
2. Inspect a process snapshot once per job, deduplicate shell families, attribute
   listening ports per PID, and enforce caps on scanned process/FD/socket data
   and retained entries before growth. Final response stays ≤512 entries with
   accurate truncation. Bound queue/in-flight work, cancellation and deadlines.
3. Implement CPU time-delta sampling and RSS behind `ProcessInspector`, including
   start-time/PID reuse guards, first-sample absence, process disappearance and
   errors. Settle shell inclusion versus child-only presentation: query represents
   shell families; the focused-pane UI may label/filter shell and descendants
   explicitly. Publish actual DTO field names consistently with CLI/docs.
4. Revalidate authorization, project/session ownership and job identity at
   completion. Process queries emit no persistence/workspace mutation effects.
   Manual/palette refresh apply the same completed snapshot; show query-not-yet-
   run/loading/error separately from a verified empty result.
5. Verify live child and listening server ownership, two-project exclusion,
   removed/reused PIDs, cap failures, slow scans, shutdown and 100-query FD/thread
   stability. Record worker thread identity and owner responsiveness.

**Exit:** bounded async query parity and focused-pane refresh pass. Process kill,
full collapsible Info fidelity and complete M18 acceptance remain owned by M18;
do not include those as M16 completion requirements.

## 9. P1 — Palette sources, bounded scheduling and freshness

Targets: palette coordinator (extract from `main.rs`), context index/cache,
watcher/root plumbing and dispatcher query coordination.

- Replace thread-per-query with one worker and one replaceable latest request.
  Coalesce typing, keep cancellation checks throughout build/score/wait, and
  close/join or safely drain the bounded owner on overlay/window shutdown.
  Serialized index construction alone is not bounded search-worker admission.
- Carry overlay epoch, query/mode/argument generation, project/root/config/
  ignore/index/workspace/Git generation. Invalidated builds must not return a
  publishable old snapshot even if cache insertion was prevented. Re-resolve
  authoritative root changes; a cached watcher path is not current authority.
- Keep the existing reusable index and input limits. Add deterministic at-cap/
  over-cap tests; distinguish skipped ignore paths from inaccessible/error
  paths. Add convergence policy when watcher arming fails, plus invalidation on
  ignore changes, project-directory updates and debounced event batches.
- Snapshot workspace/Git sources without synchronous scans. Set explicit
  candidate-count/string-size input bounds before allocation. Merge/dedup by
  typed key; aggregate data omissions and the final 100-row cap accurately.
- Do Git/diff/process refresh through common async queries; consume results
  once and reuse the M15 preview coordinator. Preserve underlying `file.search`
  and CLI semantics; no `palette.query` method.

**Tests:** delayed clear/reopen/mode/root/ignore responses; stale cache returned
after invalidation; search bursts with bounded workers/queue; source failures;
watcher-unavailable convergence; concurrent CLI mutations; shutdown during walk.

**Exit:** latest-context results only, source-bound allocations and measured
worker admission independent of typing rate.

## 10. P2 — Catalog, pure ranking, MRU and typed arguments

- Move deterministic rank/merge/MRU helpers into GPUI-free core, taking scored
  metadata as input; context owns fuzzy scoring. Preserve exact → prefix → fuzzy,
  score → MRU → stable tie policy. Do not add core→context or core→GPUI edges.
- Extract catalog/builders from `main.rs`: stable key, title/aliases/category,
  binding, captured target policy, required arguments and disabled reason.
  Freeze and publish the advertised subset from the M16 plan. Implement its
  promised name/directory/target/fraction argument steps or explicitly keep
  actions unavailable until their steps work; do not advertise inert entries.
- Revalidate origin for selection-dependent focus/resize/equalize commands;
  resolve explicit split/tab IDs where possible. Session jumps require the same
  live session↔pane binding on activation. IDs with duplicate labels never use
  shortened display IDs as dispatch selectors.
- Apply successful-use MRU to files and Git paths as well as commands/IDs.
  Include project/root/comparison identity; use original path bytes for identity
  rather than lossy display strings. Record only completed success, cap/prune
  keys, show valid recent targets on empty plain mode without an empty file walk.
- Preserve selected stable key on source arrivals; reset only for changed query/
  mode/argument. Availability and errors remain explicit. Avoid default empty-
  query selection favoring a destructive action merely through alphabetic order.

**Tests:** global top-100 ordering/dedup/partial flags, exact/prefix/alias/Unicode,
MRU across file/project/root changes, failed/pending/cancelled actions, stale
session binding, argument cancel/invalid fraction/control text with zero effects,
catalog-to-CLI/IPC commands for split/focus/project/tab/file/Git/diff/process.

**Exit:** advertised catalog works with typed targets and shared validation;
meaningful parity covers actual state/effects, not just labels or enum creation.

## 11. P3 — Input ownership, focus restoration and layout

- Add a single tested overlay/argument/executing/dismissed state machine. A
  valid origin token includes window focus and project/tab/pane/session.
  Escape restores the valid originating terminal through shared focus logic;
  successful navigation preserves the command's destination. Origin disappearance
  shows a notice; no execution falls back to another shell.
- Opening from Files filter, commit box, preview or empty workspace must release
  old input ownership appropriately. Executing file-open returns to the intended
  terminal view; merely releasing overlay capture while leaving preview visible
  is insufficient. Pending launches restore/select only after owner completion.
- Cancel source work on execute and dismiss, block duplicate Enter while pending,
  and ensure caret timers stop after close. Use full `key_char`/text-event data,
  bounded UTF-8 insertion and paste, supported caret/selection behavior and
  truthful IME limitations. Test editing helpers outside GPUI where possible.
- Clamp width, top offset and dynamic result height to the measured usable
  viewport. Use v5 type/color roles, short badges plus readable parent/ID and
  staged/working labels. Show source loading/partial/error notices without
  clipping footer/actions or hiding selection in narrow/short windows.
- Verify `Ctrl+Shift+P`, `Ctrl+P`, `Ctrl+Alt+N`, clickable entry, mode switching,
  paste and key precedence against actual Omarchy delivery. Update current user
  hints/mapping docs; historical shortcut evidence stays historical.

**Exit:** query/navigation/Enter/Escape never leak into PTYs or inspector inputs;
focus follows semantic intent and every result/control remains reachable.

## 12. V1 — Final acceptance and resource evidence

Required on the final delivered revision:

```bash
cargo fmt --all --check
cargo test --workspace
cargo test --workspace -- --test-threads=1
cargo clippy --workspace --all-targets -- -D warnings
cargo build --release --bin omaterm --bin omaterm-desktop
python3 scripts/check-docs.py
git diff --check
```

Focused runs during increments must name their actual selected targets; adding
`--bin omaterm-desktop` filters out other requested crates' library suites. No
timeout, omitted target or unavailable desktop interaction is a passing check.
Investigate terminal hangs with captured process/thread evidence; do not close
the issue solely by retries or increasing timeouts.

| Acceptance flow | Required proof |
|---|---|
| M15 comparisons and bounds | Raw Git agreement, selected-path literal query, binary/metadata/cap/no-newline states |
| M15 virtualization and scroll | Measured body/row pitch, visible-row render counts, last loaded row/long character reach, independent Split X/shared Y, mode/refresh anchors |
| M15 actions | Exact clipboard header/body/markers, eligible one-hunk index change, duplicate/stale rejection, Open target, pointer + keyboard reachability |
| M18 query prerequisite | Nonempty child/port attribution, CPU/RSS, project scope, async worker and bounded scan/resource proof |
| M16 keyboard-only flow | Split → focus jump → project/tab/session jump → filename open → Git refresh/diff → process refresh, with destination focus/sentinel |
| M16 stale/no-effect matrix | Close selected ID, replace session, change root, remove file, cancel argument, permission denial; intended targets and state/index unchanged on failure |
| M16 lifecycle | 10k-file rapid edit, watcher bursts, 100 open/close + project switches, close while searching/refreshing; stable warmed FD/thread/RSS trends |
| Parity | Same-instance desktop + CLI effects and strict JSON/human errors, direct source queries and no new palette wire method |
| Layout/privacy | Standard/narrow/short-window captures, measured roles, no query/clipboard/diff/token data in default logs |

Record commit/working-tree identity, toolchain, compositor/scale/font/viewport,
fixture/workload size, warm-up, duration and before/after/peak values. Compare
growth across cycles; do not invent universal latency/RSS thresholds. Store
small durable command/result summaries in status and acceptance rows, with
capture locations. Broader platform matrices and formal release baselines pass
to M17 explicitly, not as substitutes for these milestones' acceptance.

## 13. Closure checkpoints

- [ ] R0 reproducible baseline/fixtures and historical timeout tracking.
- [ ] D1 complete identity, real worker bounds and safe selected data.
- [ ] D2 measured virtual two-axis viewport, pairing and source anchors.
- [ ] D3 all M15 automated/live gates linked; M15 marked complete.
- [ ] Q1 bounded off-thread M18 query verified; whole M18 remains separately tracked.
- [ ] P1 bounded source scheduler/cache freshness and partial/error semantics.
- [ ] P2 pure core policy, advertised catalog/arguments/MRU and action parity.
- [ ] P3 actual focus restoration/input containment and responsive layout.
- [ ] V1 final required checks and Wayland matrix pass on delivered revision.
- [ ] M16 checkboxes/status/acceptance updated with evidence; M17 handoff recorded.

**First remaining implementation action:** diagnose the recurring terminal-suite
hang and centralize mutation-triggered diff invalidation for UI/IPC parity, then
continue D2 and D3. The full stale-key and blocked supersession/shutdown
regressions are implemented; they do not close the broader D1 acceptance gate.
