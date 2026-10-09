# M19 S9 — Acceptance Report (PARTIAL / PENDING)

> **Status: PENDING.** Current automated gates pass; native acceptance is
> blocked/partial. Startup, terminal IPC and 20 real process samples establish
> neither complete E01–E10 acceptance nor resource/latency budgets. Input was
> aborted on user-focus change before any event; no graceful exit was observed.

- **Milestone:** M19 — Basic Built-in Editor (comprehensive completion plan)
- **Slice:** S9 — Final automated/native acceptance and milestone handoff
- **Delivered source revision:** `f287991+worktree`; source parent
  `f287991f223e01a36ea5e0a60c219c824da05ba3`. No new commit hash is implied;
  a later documentation parent/commit does not identify this delivered source.
- **Working tree at acceptance:** dirty (Rust/state/metrics/script changes).
- **Toolchain:** `RUSTUP_TOOLCHAIN=1.99.0`; native probes record rustc
  `1.99.0 (b940084d7 2026-09-28)`, Cargo `1.99.0 (5f94df478 2026-08-27)`;
  repository pin remains 1.98.1.
- **Report date:** 2026-10-04; parent-run automated results supplied for this
  documentation reconciliation, native observations in
  [m19-native-current.md](m19-native-current.md).

## Authority and related records

Read this report with:

- [M19 comprehensive completion plan](../2026-10-03-m19-completion-plan.md) — especially §13
  (Automated gates, Native acceptance run, Requirement-to-evidence register) and
  §14 (Definition of done).
- [M19 implementation contract](../2026-10-03-m19-basic-editor-implementation-plan.md).
- [status record](../status.md) — slice-by-slice commands, results, blockers.
- [acceptance matrix](../acceptance-matrix.md) — release requirements.
- [dependency/license inventory](../dependencies.md) — exact dependency versions
  and licenses for any newly selected dependency.

Harness interfaces this report is consistent with:

- `scripts/run-m19-wayland-acceptance.py` — native harness. Content-free JSON
  report; never fabricates pointer, focus, or performance evidence. Exit codes:
  `0` verified acceptance, `2` blocked (missing tool/helper or focus loss),
  `3` failed (fixture/binary/assertion/internal error), **`4` pending** (partial
  collection with required cases unexecuted). Explicit `--scripted-steps` is
  required to request semantic input; `--allow-input` alone does not run flows.
- `scripts/m19-resource-bench.py` — resource/timing collector. Parses a bounded
  `timings_ms`/`counters` JSON; empty arrays are unobserved with null percentiles.
  No synthetic measurement placeholders are emitted. `--self-test` validates the
  parser and `/proc` reader using tooling-only synthetic test data.
- `scripts/generate-m19-fixtures.py` — disposable fixture generator (refuses a
  non-empty output dir, writes `manifest.json`).

## 1. Automated gate command list

Run from the workspace root, preserving exit codes and full logs. Record the
delivered revision, toolchain, and each command's exit status and log path.
A failing/timeout run is recorded as such; retries require diagnosis, not output
filtering or skips.

| # | Gate | Command | Status | Revision | Log artifact | Notes |
|---|---|---|---|---|---|---|
| G1 | Format | `RUSTUP_TOOLCHAIN=1.99.0 cargo fmt --all --check` | pass | `f287991+worktree` | [status](../status.md) | Parent-run result; exit 0 |
| G2 | Workspace tests (parallel) | `RUSTUP_TOOLCHAIN=1.99.0 cargo test --workspace --quiet` | pass | `f287991+worktree` | [status](../status.md) | Parent-run result; exit 0; **606 tests** |
| G3 | Workspace tests (serial) | `RUSTUP_TOOLCHAIN=1.99.0 cargo test --workspace --quiet -- --test-threads=1` | pass | `f287991+worktree` | [status](../status.md) | Parent-run result; exit 0; **606 tests** |
| G4 | Clippy (deny warnings) | `RUSTUP_TOOLCHAIN=1.99.0 cargo clippy --workspace --all-targets -- -D warnings` | pass | `f287991+worktree` | [status](../status.md) | Parent-run result; exit 0 |
| G5 | Release build | `RUSTUP_TOOLCHAIN=1.99.0 cargo build --release --bin omaterm --bin omaterm-desktop` | pass | `f287991+worktree` | [native binary hashes](m19-native-current.md) | Parent-run result; exit 0; `target/release/omaterm`, `target/release/omaterm-desktop` |
| G6 | Docs checker | `python3 scripts/check-docs.py` | pass | `f287991+worktree` + docs reconciliation | This report | Exit 0; 39 Markdown files, 173 local links, 304 numbered blueprint refs, all 34 CLI mappings |
| G7 | Whitespace/diff check | `git diff --check` | pass | `f287991+worktree` + docs reconciliation | This report | Exit 0; no whitespace diagnostics |

G1–G5 are actual current passes reported by the parent run, not executions by
the native validator or this docs-only increment. Full Cargo log paths were not
supplied; do not invent them. The native record's absence of parent exit-status
capture remains historical truth. E10 still needs complete regression/redaction
review and the handoff checklist remains open.

If a `RUSTUP_TOOLCHAIN=1.99.0` prefix is used, record the prefix and actual tool
versions; do not describe it as the unchanged repository pin. Validate new
dependencies' exact versions/licenses in [dependencies.md](../dependencies.md).

## 2. Requirement-to-evidence register (E01–E10)

Fields: **status** (`pending` / `partial` / `pass` / `block` / `fail`), **command** (exact
invocation or script), **revision** (source identity the evidence belongs to), **artifacts**
(logs, reports, captures, capture paths), **notes** (blockers, deviations,
alternatives).

| ID | Requirement | Status | Command / harness | Revision | Artifacts | Notes |
|---|---|---|---|---|---|---|
| E01 | Root-contained identity and reads/writes | partial | G2/G3; native root cases pending | `f287991+worktree` | [status](../status.md) | Off-thread rooted I/O implemented; complete acceptance pending |
| E02 | Async lifecycle and version-safe save | partial | G2/G3; native pending-work flow pending | `f287991+worktree` | [status](../status.md) | Committed baseline acknowledgement implemented; native races pending |
| E03 | Aggregate bounds and indexed rendering | partial | G2/G3; S8 counters pending | `f287991+worktree` | [status](../status.md) | Global 32 slots/17-result mailbox; measured cap/index proof pending |
| E04 | Metadata-only registry persistence | partial | G2/G3; actual restart pending | `f287991+worktree` | [status](../status.md) | Snapshot/restore/Retry implemented; no normal native restart |
| E05 | Dirty/conflict/project/shutdown decisions | partial | G2/G3; every native choice/failure pending | `f287991+worktree` | [status](../status.md) | Observed-revision overwrite implemented; full decisions unexecuted |
| E06 | Text/IME, graphemes, selection and focus | partial | G2/G3; clipboard/native composition pending | `f287991+worktree` | [status](../status.md) | `EntityInputHandler` implemented; input aborted before first event |
| E07 | All entry routes and unchanged terminal contract | partial | G2/G3; current driver IPC commands | `f287991+worktree` | [native evidence](m19-native-current.md) | Terminal `file.open` parity only; native routes pending |
| E08 | Highlight/fallback/cancellation | partial | G2/G3; all-language captures pending | `f287991+worktree` | [status](../status.md) | Native highlight/cap visuals unexecuted |
| E09 | Resource bounds, idle and operation timings | partial | `m19-resource-bench.py --pid 1664916`; full command in native record | `f287991+worktree` | [native evidence](m19-native-current.md) | 20 process samples only; no resource/latency pass |
| E10 | Final regression and redaction review | partial | G1–G7; complete review pending | `f287991+worktree` | This report and [status](../status.md) | Automated passes do not close native/redaction handoff |

All rows remain unclosed. Current automated evidence belongs to
`f287991+worktree` (G1–G5 above). E01–E06/E08 have implementation/test evidence, but native/cap/registry/
dirty/IME acceptance remains pending. E07 is partial: same-instance IPC
`file.open` retains terminal pane/session identity and submits `$EDITOR`; native
entry-route/dedup proof is missing. E09 is partial: 20 actual `/proc` samples,
zero editor timing observations and 0/20 cycles for each workload. E10 is partial:
current automated gates pass; complete redaction/acceptance review is pending.
See [current native evidence](m19-native-current.md) for exact commands/artifacts.

### Delivered source audit corrections

Root filesystem resolution/descriptor capture is off-thread. I/O admission is
one active + 16 queued jobs with a bounded 17-result mailbox. The 32-document
cap is global, including pending-open reservations/unavailable restore entries.
Metadata-only schema-3 snapshots, bounded restore reads and Retry/Close exist.
Committed-save acknowledgement updates captured G's saved baseline while
preserving live G+1; committed disk outcomes survive cancellation/stale views.
Conflict overwrite compares the observed revision and re-conflicts on another
change. S6 implements GPUI `EntityInputHandler`, native UTF-16/marked composition
and surface/input-owner cancellation; line-local grapheme navigation and shared
indexed render snapshots replace whole-document segmentation on motion. Native
validation and measured budgets are still required for these implementations.

Attach each case to its delivered revision and fixture manifest. Previously
passing observations are baseline evidence only, not a substitute for checking
changed paths.

### Native harness invocation (filled in during the real run)

Record the exact command used (arguments below match
`scripts/run-m19-wayland-acceptance.py`):

```bash
python3 scripts/run-m19-wayland-acceptance.py \
  --allow-input \
  --scripted-steps <path-to-semantic-flow-json> \
  --pointer-helper <path-to-pointer-helper> \
  --output-name <simple-evidence-dir-name> \
  --desktop <path-to-omaterm-desktop> \
  --cli <path-to-omaterm> \
  --output <path-to-acceptance-report.json> \
  --evidence-root <dir> \
  --compositor <hyprland|sway|...> \
  --screenshot-tool <path-to-screenshot-tool> \
  --samples <n> \
  --interval-seconds <seconds> \
  --keep
```

- Record the harness exit status (`0`/`2`/`3`/`4`) and the JSON report's `status`
  field (`ok` / `blocked` / `failed` / `pending`) verbatim. The current native
  attempt used an external driver; it has no acceptance-harness invocation/exit
  to invent. Its self-tests exited 0 for tooling only.
- If the pointer helper is absent, the run is **blocked** (exit `2`) — not a
  pass. Do not synthesize pointer or focus results.
- `metrics.json` must contain actual app instrumentation. Missing observations
  remain empty arrays/unobserved/null percentiles. Collection alone establishes
  no resource or latency pass. Helpers must satisfy the current handshake,
  exact-PID guards and outcome assertions; raw pointer tools are insufficient.

## 3. Native Wayland acceptance steps

Use release binaries with isolated `HOME`/`XDG_*` paths and generated disposable
repositories. Record commit, binary build, compositor/output/scale, font, logical
viewport, fixture sizes, commands, timing and captures. Bind the virtual-pointer
device to the chosen output; derive logical extents from current monitor
geometry. Verify the target PID/focus before injecting real input and stop if the
user's active window changes.

| Step | Action | Status | Artifacts | Notes |
|---|---|---|---|---|
| N1 | Open from Files, finder, general palette, Git and diff actions; verify dedup, defaults, explicit terminal fallback, including error targets | pending | pending | — |
| N2 | Type/select/copy/cut/paste/undo/redo/save via keyboard and pointer; include native text commits/composition and exact on-disk byte assertions | pending | pending | — |
| N3 | Exercise all languages/plain fallback, Unicode/graphemes/tabs, empty/LF/CRLF, long lines, byte/line caps, binary/invalid UTF-8/non-regular errors | pending | pending | — |
| N4 | Modify externally; verify clean/dirty conflict states, cancelled decisions, confirmed reload, confirmed overwrite, second-change re-conflict | pending | pending | — |
| N5 | Switch documents/projects/terminal/diff/palette/inspector fields while work is pending; verify stable buffers/carets, valid focus, unchanged PTY identity; terminal sentinels + bounded CLI reads prove no input leaks | pending | pending | — |
| N6 | Close/reload/delete projects with several dirty documents; verify every Save/Discard/Cancel and failure path; hidden dirty buffers included | pending | pending | — |
| N7 | Close window normally, restart, verify schema migration/registry, clean disk content, remembered selection, fresh terminal sessions, graceful unavailable-document recovery | pending | pending | — |
| N8 | Run S8 cycles/cap/aggregate/idle measurements; capture standard and narrow widths, scroll ends, composition/caret/selection, prompt/conflict/error states | pending | pending | — |
| N9 | Exit normally; verify test unit/PIDs, shells, sockets, worker threads/tasks, owned temp files cleaned up; restore prior workspace/focus | pending | pending | — |

## 4. Blockers and deviations

- **Real focus blocker:** compositor focus changed before the first event;
  input was aborted without refocusing/resuming. N1–N8 remain pending/blocked.
  Both small/cap workloads executed zero cycles, and all six timing arrays
  remain unobserved with null p50/p95 and `latency_pass:null`.
- **No graceful exit:** owned desktop received SIGTERM; shells/app disappeared,
  but the unserved socket entry required explicit unlink. N9's normal-exit and
  worker/sidecar cleanup gate is unproven. Forced cleanup is scoped evidence only.
- Native startup/IPC and process samples are documented in
  [m19-native-current.md](m19-native-current.md), with artifacts under
  `/tmp/opencode/m19-native-current`. No complete editor/conflict/restart/IME
  matrix was executed. Next action is a target-focused uninterrupted native run
  covering the full matrix and both 20-cycle workloads, followed by normal exit.

## 5. Handoff checklist (plan §14 definition of done)

- [ ] S1–S8 exit criteria recorded as passing on the delivered revision.
- [ ] Every E01–E10 register row has a non-`pending` status backed by artifacts.
- [ ] G1–G7 logged with exit codes and linked.
- [ ] N1–N9 captured with revision-bound artifacts.
- [ ] Status, M19 checklist, overview, acceptance matrix and dependency/license
      records agree; deferred features named without hiding required work.
- [ ] Redaction review confirms no secrets/tokens in logs, reports or snapshots.

> Until every box above is checked with real evidence, **M19 is not complete**
> and this report must remain marked pending.
