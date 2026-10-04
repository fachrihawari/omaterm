# M19 S9 Closure Plan — Remaining Gaps and Blockers

**Date:** 2026-10-04. **Baseline:** `c5c5fd0` (code `b28ca8a`, binary `32cd3d9f`).
**Status:** execution plan; nothing below is claimed until its exit evidence exists.
**Companion evidence:** [native verify-fix](evidence/m19-native-verify-fix.md),
[S9 report scaffold](evidence/m19-s9-report.md), [status](status.md).

## 1. What is already proven (and on which revision)

| Area | Evidence | Revision |
|---|---|---|
| 606 workspace tests, parallel + serial; fmt; Clippy `-D warnings`; release build | CI-equivalent local gates | `50c06cc`–`b28ca8a` (rerun on final HEAD in Phase 5) |
| 61 native cases (open/edit/save cycles, unicode, clipboard, languages, dirty close/revert, entry routes) | `/tmp/opencode/m19-native-reserved/cases.jsonl` + screenshots | `50c06cc` binary |
| Dirty conflict Overwrite/Reload/Cancel via keyboard, clean-conflict Reload, prompt render, post-resolution typing + terminal | `docs/evidence/m19-native-verify-fix.md`, byte assertions, screenshots | `32cd3d9f` binary |
| Crash restart: valid schema-3 snapshot, docs restored clean, dirty not recovered, fresh sessions | screenshot + snapshot diff + session IDs | `32cd3d9f` binary |
| 15 s + 15 s idle: FD/threads stable, no sidecars | sweep samples | `50c06cc` binary |

## 2. Gap register

### G1 — Graceful-shutdown E2E (Save-all / Discard / Cancel on window close)
- **Symptom:** `hl.dsp.window.close()` returns ok but is a no-op; Super+W does
  not fire from the virtual keyboard; no shutdown prompt has ever been
  exercised natively.
- **Severity:** release-blocking for S5-shutdown acceptance.
- **Definition of done:** normal window close with N dirty docs shows the
  shutdown decision; each of Save-all (all outcomes awaited, partial failure
  blocks teardown), Discard (explicit, buffers dropped), Cancel (resumes input)
  proven with byte/process evidence; final snapshot written; worker joined;
  socket removed; shells reaped.
- **Options:**
  1. *Find working close syntax* — read the hyprland-lua `hl.dsp` plugin
     source, discover the correct close call/args. Cheapest; may not exist.
  2. *SIGTERM → graceful shutdown* — handle `SIGTERM` (and `SIGHUP`) by
     routing into the existing `begin_shutdown` flow instead of dying
     instantly. Real product value (blueprint §67: stop IPC, persist,
     terminate sessions, reap, remove socket). `signal-hook 0.4.4` is already
     in `Cargo.lock` (transitive); promoting it to a direct dependency needs
     version/license recording, no new resolution. Medium effort, testable
     headlessly (`kill -TERM`, assert snapshot/exit code/cleanup).
  3. *Human-in-the-loop* — script everything up to the close, then ask the
     user to close the window once via their own keybind. Fallback only.
- **Recommendation:** try (1) for 30 minutes; then implement (2) — it is
  independently justified and makes shutdown testing deterministic forever.
  Keep (3) as the last resort for the dirty-shutdown UI half.

### G2 — 20-cycle small/cap reruns on the final binary
- **Symptom:** 20+20 cycles proven on `50c06cc`; the shipped code changed
  (`87bc5bf`, `b28ca8a`). Spot checks pass on the new binary, full loops do not
  exist for it. Cap-cycle-1 also showed a finder-index race on new files
  (first attempt `HARNESS_ERROR`, passed on retry).
- **Definition of done:** 20 small + 20 cap open/edit/save/close cycles with
  per-cycle byte assertions, real `OMATERM_METRICS_PATH` timings (no zeros),
  warmup + post-cleanup FD/thread/RSS, sidecar count zero.
- **Approach:** extend `/tmp/opencode/verify-fix-driver.py` `cycles()`-style
  loop; refresh the palette index before first-open of new files (or catch the
  known race explicitly and retry once, recorded as such); run on a fresh
  instance of the final binary. ~15 min runtime.

### G3 — IME preedit/composition native proof
- **Symptom:** unicode typing is byte-proven, but no preedit lifecycle
  (marked range render, commit, cancel, focus-loss cancel) has been observed.
- **New fact:** GPUI 0.2.2 implements `zwp_text_input_v3` client-side and
  `fcitx5` is running with `INPUT_METHOD=fcitx` — this test is feasible.
- **Definition of done:** with a CJK method active in the isolated test
  window: preedit renders marked, commit inserts exact bytes, `Escape` and
  focus-loss cancel without residue, no text reaches any PTY.
- **Approach:** read-only probe first (`fcitx5-remote -n`, list configured
  methods — no changes). If a suitable method exists, run one scripted session
  in the isolated window, then restore the prior method. **Requires explicit
  user consent** because it briefly flips the global IME method. Fallback if
  declined/unavailable: unit-level composition tests (already passing) +
  record IME as blocked with the exact reason.

### G4 — Entry-route retakes with robust coordinates
- **Symptom:** Files-tree click, Git-row Open File, diff Open File, and
  explicit Open-in-Terminal failed on driver-coordinate errors (banner shifts
  header y; rows at unexpected y; filter `No matches` on collapsed parents),
  not on proven app defects.
- **Definition of done:** each route opens the typed project/path natively
  (dedup verified), terminal fallback still submits `$EDITOR` (sentinel in the
  focused shell, sessions unchanged), diff Open File lands on the hunk’s
  source line.
- **Approach:** derive click targets from live screenshots/layout instead of
  hardcoded y; expand tree parents before filtering; prefer keyboard-only
  flows where they exist; assert baseline terminal cleanliness before each
  leak-sensitive step (the `CB` lesson).

### G5 — Restart-missing / Retry and aggregate-cap native proof
- **Symptom:** created but never executed (run aborted on tooling syntax).
- **Definition of done:** delete a persisted doc’s file before restart →
  metadata-only Unavailable entry with working Retry (restore after recreating
  the file) and Close; fill 32 docs → 33rd open rejected with the stable
  error and a truthful notice; snapshot stays valid throughout.
- **Approach:** scripted, uses only CLI + filesystem + screenshots. Cheap.

### G6 — Second-change overwrite re-conflict
- **Symptom:** unit-covered, never driven natively.
- **Definition of done:** dirty conflict prompt up → external edit again →
  Overwrite refused → fresh observation + re-prompt → resolve.
- **Approach:** one scripted case on the matrix instance.

### G7 — Idle/resource baselines and budgets
- **Symptom:** only 15 s + 15 s samples; no 30 s idle per mode, no budgets.
- **Definition of done:** 30 s samples for editor-visible, terminal-active,
  editor-hidden, unfocused-window idle; FD/thread/RSS deltas; worker/queue
  counters zero; sidecar count zero; regression budgets recorded from measured
  p50/p95 (never from scripted key delays).
- **Approach:** extend the resource bench reader; runs unattended.

### G8 — Final gates + evidence closure on the final HEAD
- **Symptom:** gates pass per-increment, never once on the final tree.
- **Definition of done:** `cargo fmt --check`, `cargo test --workspace`,
  serial rerun, `cargo clippy --workspace --all-targets -- -D warnings`,
  release build of both binaries, `check-docs.py`, `git diff --check` —
  unfiltered logs attached; E01–E10 register, acceptance matrix, dependencies,
  and S9 report/manifest updated to match; push.

### G9 — Harness hardening (learned rules, codified)
1. Kill-before-relaunch **and verify death**; never share a runtime dir
   (singleton displacement kills silently — root-caused 2026-10-04).
2. Append-only desktop logs, never overwrite (preserves panic evidence).
3. Clean-baseline assertion before every leak-sensitive read.
4. Pointer/keyboard guards stay mandatory; any focus mismatch aborts.
5. Refresh/verify the file index before first-open of newly created files.

### G10 — Small product fix (optional, tiny)
- Files-filter `Ctrl+U` clears the query (matches terminal convention;
  `Ctrl+A` select-all has no selection model to hook into). ~5 lines + test.
  Defer if any release-blocker slips.

## 3. Phases and exit criteria

| Phase | Work | Gaps closed | Exit |
|---|---|---|---|
| P0 | Harness: encode G9 rules into the S9 runner; fix `closewindow` investigation (G1-opt1, 30 min timebox) | — (enabler) | Runner refuses shared runtime dirs; close path found or declared absent |
| P1 | SIGTERM graceful shutdown **or** confirmed close path; unit + headless tests | G1 (mechanism) | `kill -TERM` with dirty docs → prompt-equivalent handling, snapshot, clean exit code, reaped children — or documented close path |
| P2 | Matrix rerun on final binary: 20+20 cycles, entry routes, dirty close/revert, undo/clipboard/unicode, languages, second-change conflict, cap-33 rejection, restart-missing Retry, narrow layout | G2, G4, G5, G6 | cases.json all PASS with byte evidence; failures fixed or re-scoped with rationale |
| P3 | Idle baselines + budgets; IME session (consent-gated) | G3, G7 | 4-mode samples + budgets; IME PASS or consent-declined BLOCKED note |
| P4 | Graceful-shutdown UI E2E (Save-all/Discard/Cancel) + restart-after-graceful restore | G1 (UI half) | Byte/process evidence for all three choices |
| P5 | Final gates, E01–E10, matrix/manifest/dependencies, push | G8 | Clean tree, green logs, pushed HEAD |

P1–P3 parallelize after P0 (separate instances/dirs). P4 needs P1. P5 is last.

## 4. Risks

- Compositor plugin API may simply lack a scriptable close → P1-SIGTERM covers
  it with real product value; human-in-the-loop remains as fallback.
- IME consent declined or no CJK method configured → G3 stays blocked with a
  precise reason; composition unit tests + typed-unicode evidence stand in.
- 32-doc cap makes matrix runs leak-sensitive → the driver must close with
  Discard after every case and assert `project_documents` counts.
- Singleton displacement can masquerade as crashes → P0 rule #1 prevents
  recurrence.

## 5. Immediate next actions (in order)

1. P0: harden the runner (30 min), timebox the close-syntax hunt (30 min).
2. P1: implement SIGTERM graceful shutdown + tests if close is unavailable.
3. P2: full matrix rerun on the final binary.
