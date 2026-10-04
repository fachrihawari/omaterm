# M19 S9 — Final Acceptance Report (SCAFFOLD / PENDING)

> **Status: PENDING.** This is a scaffold for the final M19 S9 acceptance
> report. It contains **no measured results**. Every status below is `pending`
> or `blocked` until real evidence is produced for the delivered revision by
> running the commands and native steps in this document. Do not infer a pass
> from this file. No timings, hashes, screenshots, or logs are recorded here.

- **Milestone:** M19 — Basic Built-in Editor (comprehensive completion plan)
- **Slice:** S9 — Final automated/native acceptance and milestone handoff
- **Delivered revision:** `pending` (commit SHA not yet captured)
- **Working tree at acceptance:** `pending` (clean/dirty not yet captured)
- **Toolchain:** `pending` (see `rust-toolchain.toml`; record any
  `RUSTUP_TOOLCHAIN` override and actual `rustc`/`cargo` versions)
- **Report author / date:** `pending`

## Authority and related records

Read this report with:

- [M19 comprehensive completion plan](../m19-completion-plan.md) — especially §13
  (Automated gates, Native acceptance run, Requirement-to-evidence register) and
  §14 (Definition of done).
- [M19 implementation contract](../m19-basic-editor-implementation-plan.md).
- [status record](../status.md) — slice-by-slice commands, results, blockers.
- [acceptance matrix](../acceptance-matrix.md) — release requirements.
- [dependency/license inventory](../dependencies.md) — exact dependency versions
  and licenses for any newly selected dependency.

Harness interfaces this report is consistent with:

- `scripts/run-m19-wayland-acceptance.py` — native harness. Content-free JSON
  report; never fabricates pointer, focus, or performance evidence. Exit codes:
  `0` reached cleanup/verification, `2` blocked (missing tool/pointer helper),
  `3` failed (focus mismatch, fixture/binary failure, internal error).
- `scripts/m19-resource-bench.py` — resource/timing collector. Parses a bounded
  `timings_ms`/`counters` JSON; rejects empty series. `--self-test` validates the
  parser and `/proc` reader.
- `scripts/generate-m19-fixtures.py` — disposable fixture generator (refuses a
  non-empty output dir, writes `manifest.json`).

## 1. Automated gate command list

Run from the workspace root, preserving exit codes and full logs. Record the
delivered revision, toolchain, and each command's exit status and log path.
A failing/timeout run is recorded as such; retries require diagnosis, not output
filtering or skips.

| # | Gate | Command | Status | Revision | Log artifact | Notes |
|---|---|---|---|---|---|---|
| G1 | Format | `cargo fmt --all --check` | pending | pending | pending | — |
| G2 | Workspace tests (parallel) | `cargo test --workspace` | pending | pending | pending | — |
| G3 | Workspace tests (serial) | `cargo test --workspace -- --test-threads=1` | pending | pending | pending | — |
| G4 | Clippy (deny warnings) | `cargo clippy --workspace --all-targets -- -D warnings` | pending | pending | pending | — |
| G5 | Release build | `cargo build --release --bin omaterm --bin omaterm-desktop` | pending | pending | pending | Record binary paths + SHA-256 |
| G6 | Docs checker | `python3 scripts/check-docs.py` | pending | pending | pending | Link/consistency + redaction review |
| G7 | Whitespace/diff check | `git diff --check` | pending | pending | pending | — |

If a `RUSTUP_TOOLCHAIN=1.99.0` prefix is used, record the prefix and actual tool
versions; do not describe it as the unchanged repository pin. Validate new
dependencies' exact versions/licenses in [dependencies.md](../dependencies.md).

## 2. Requirement-to-evidence register (E01–E10)

Fields: **status** (`pending` / `pass` / `block` / `fail`), **command** (exact
invocation or script), **revision** (SHA the evidence belongs to), **artifacts**
(logs, reports, captures, capture paths), **notes** (blockers, deviations,
alternatives).

| ID | Requirement | Status | Command / harness | Revision | Artifacts | Notes |
|---|---|---|---|---|---|---|
| E01 | Root-contained identity and reads/writes | pending | S1 barrier/cap/alias/root tests; `cargo test --workspace` | pending | pending | See plan §5 exit criteria |
| E02 | Async lifecycle and version-safe save | pending | S2 worker/router tests; native pending-work flow | pending | pending | See plan §6; one final outcome per op |
| E03 | Aggregate bounds and indexed rendering | pending | S3 cap/index tests; S8 counters via `m19-resource-bench.py` | pending | pending | No full-doc copy/scan on unchanged frame |
| E04 | Metadata-only registry persistence | pending | S4 migration/privacy tests; actual restart | pending | pending | Schema 1/2→3; no dirty text persisted |
| E05 | Dirty/conflict/project/shutdown decisions | pending | S5 state-transition tests; every native choice/failure | pending | pending | Every Save/Discard/Cancel path |
| E06 | Text/IME, graphemes, selection and focus | pending | S6 pure/GPUI tests; clipboard byte proof; native composition | pending | pending | No input leaks either direction |
| E07 | All entry routes and unchanged terminal contract | pending | S7 shared-route/parity tests; real activation | pending | pending | `file.open` still submits `$EDITOR` |
| E08 | Highlight/fallback/cancellation | pending | S8 span/generation tests; all-language captures | pending | pending | Stale results never paint newer text |
| E09 | Resource bounds, idle and operation timings | pending | S8 measured loops; `m19-resource-bench.py`; timing report | pending | pending | p50/p95 + sample count + machine/FS/load |
| E10 | Final regression and redaction review | pending | G1–G7 logs; code/log audit; `scripts/check-docs.py` | pending | pending | No secrets/tokens in logs or snapshots |

Attach each case to its delivered revision and fixture manifest. Previously
passing observations are baseline evidence only, not a substitute for checking
changed paths.

### Native harness invocation (filled in during the real run)

Record the exact command used (arguments below match
`scripts/run-m19-wayland-acceptance.py`):

```bash
python3 scripts/run-m19-wayland-acceptance.py \
  --allow-input \
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

- Record the harness exit status (`0`/`2`/`3`) and the JSON report's `status`
  field (`ok` / `blocked` / `failed`) verbatim.
- If the pointer helper is absent, the run is **blocked** (exit `2`) — not a
  pass. Do not synthesize pointer or focus results.
- `metrics.json` values produced by the harness are **synthetic placeholders**
  unless replaced by real instrumentation; mark them as such.

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

- `pending` — record each blocker with reproduction, evidence, and alternatives
  before changing any frozen contract (plan §14).

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
